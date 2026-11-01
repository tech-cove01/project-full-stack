"""CAP10 in-process PTS evidence store. No listener, business or cloud calls."""
from collections import Counter
import copy
import hashlib
import json
import math
import os
from pathlib import Path
import threading
import time
import uuid
from urllib.parse import urlsplit


KINDS = ('mutation', 'noop', 'uncertain', 'rejected', 'invalid', 'dropped')
COUNT_KEYS = ('planned', 'attempted', 'completed', *KINDS)
FACT_KEYS = ('wanted', 'observedLiked', 'previousVersion', 'observedVersion', 'observedNoteId')
RECORD_KEYS = {'ordinal', 'slot', 'kind', 'startedUnixMs', 'elapsedMs', 'status', *FACT_KEYS}
MAX_SAFE = 9007199254740991
RSS_LIMIT = 2147483648


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(',', ':'), ensure_ascii=False, allow_nan=False).encode('utf-8')


def digest(value):
    return hashlib.sha256(canonical(value)).hexdigest()


def integer(value, minimum=0, maximum=MAX_SAFE):
    return type(value) is int and minimum <= value <= maximum


def number(value, minimum=0):
    return type(value) in (int, float) and math.isfinite(value) and value >= minimum


def quantiles(values):
    """Exact sorted R7 quantiles of the full unaggregated HTTP elapsed samples."""
    if not values:
        return None
    ordered = sorted(values)
    def percentile(fraction):
        index = (len(ordered) - 1) * fraction
        low = math.floor(index)
        high = math.ceil(index)
        return ordered[low] + (ordered[high] - ordered[low]) * (index - low)
    return {'avg': sum(ordered) / len(ordered), 'med': percentile(.5), 'min': ordered[0],
            'p(95)': percentile(.95), 'p(99)': percentile(.99), 'max': ordered[-1]}


class EngineStore:
    def __init__(self, config, journalPath, clock=None):
        self._lock = threading.RLock()
        self._clock = clock or (lambda: time.time_ns() / 1000000)
        self._config = copy.deepcopy(config)
        assert isinstance(config, dict), 'fixed collector configuration required'
        for key in ('runId', 'phase', 'controlToken'):
            assert type(config.get(key)) is str and config[key], 'collector identity required'
        assert config['phase'] in ('warm', 'stable'), 'fixed collector phase required'
        assert integer(config.get('rate'), 1, 5000) and config.get('seconds') in ((10,) if config['phase'] == 'warm' else (60, 180)), 'fixed phase duration/rate required'
        assert type(config['seconds']) is int and config.get('expectedEngines') == 2 and type(config['expectedEngines']) is int
        target = urlsplit(config.get('apiUrl', ''))
        assert target.scheme == 'http' and target.hostname and target.port and not target.username and not target.password and not target.query and not target.fragment and target.path in ('', '/'), 'fixed API target required'
        for key in ('fixtureSHA256', 'scriptSHA256'):
            assert type(config.get(key)) is str and len(config[key]) == 64 and all(c in '0123456789abcdef' for c in config[key]), 'evidence hash required'
        rows = config.get('relations')
        assert isinstance(rows, list) and len(rows) == 512, '512 private relationships required'
        self._relations = {}
        for row in rows:
            assert isinstance(row, dict) and integer(row.get('slot'), 0, 511), 'private relationship slot invalid'
            assert row['slot'] not in self._relations, 'private relationship slots duplicated'
            assert all(type(row.get(key)) is str and row[key] for key in ('userId', 'noteId', 'token')), 'private relationship identity required'
            assert type(row.get('active')) is int and row['active'] in (0, 1) and integer(row.get('version')), 'private relationship fact invalid'
            self._relations[row['slot']] = copy.deepcopy(row)
        assert len({(r['userId'], r['noteId']) for r in rows}) == 512, 'private relationships duplicated'
        self._note_ids = {r['noteId'] for r in rows}
        self._identity = {key: config[key] for key in ('runId', 'phase', 'rate', 'seconds', 'apiUrl', 'fixtureSHA256', 'scriptSHA256', 'expectedEngines')}
        self._identity.update(version=1, jitterBudgetMs=50, clockUncertaintyMaxMs=50)
        self._identity_hash = digest(self._identity)
        self._engines = {}
        self._owners = {}
        self._facts = {slot: (bool(row['active']), row['version']) for slot, row in self._relations.items()}
        self._last_ordinal = {}
        self._last_http_end = {}
        self._retired_slots = set()
        self._records = {}
        self._stats_version = -1
        self._stats_cache = None
        self._start = None
        self._stop = False
        self._reason = None
        self._violations = []
        self._collection_errors = []
        self._closed = False
        self._journal_hasher = hashlib.sha256()
        self._journal_path = Path(journalPath)
        assert self._journal_path.parent.is_dir(), 'collector journal parent must exist'
        self._fd = os.open(self._journal_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, 'O_BINARY', 0), 0o600)
        self._append({'kind': 'configuration', 'configuration': self._identity, 'hash': self._identity_hash,
                      'quantileMethod': 'R7 over all unaggregated native HTTP elapsedMs; warm/control excluded'})

    def close(self):
        with self._lock:
            if not self._closed:
                os.close(self._fd)
                self._closed = True

    def _now(self):
        value = self._clock()
        if not number(value, 1):
            raise ValueError('collector clock invalid')
        return value

    def _append(self, row):
        # No ACK is possible until the complete frame is written and fsynced.
        try:
            raw = canonical(row) + b'\n'
            view = memoryview(raw)
            while view:
                written = os.write(self._fd, view)
                if written <= 0:
                    raise OSError('collector write incomplete')
                self._journal_hasher.update(view[:written])
                view = view[written:]
            os.fsync(self._fd)
        except (OSError, TypeError, ValueError):
            self._stop = True
            self._reason = self._reason or 'collector persistence failed'
            self._collection_errors.append('collector persistence failed')
            raise ValueError('collector persistence failed') from None

    def _stop_for(self, reason, collection=False):
        if reason not in self._violations:
            self._append({'kind': 'stop', 'atUnixMs': self._now(), 'reason': reason})
            self._violations.append(reason)
        self._stop = True
        self._reason = self._reason or reason
        if collection and reason not in self._collection_errors:
            self._collection_errors.append(reason)

    def _require(self, condition, reason):
        if not condition:
            self._stop_for(reason, collection=True)
            raise ValueError(reason)

    def _base(self, payload):
        self._require(isinstance(payload, dict) and payload.get('runId') == self._config['runId'], 'collector run identity differs')
        engine_id = payload.get('engineId')
        try:
            valid = type(engine_id) is str and str(uuid.UUID(engine_id)) == engine_id
        except (ValueError, AttributeError):
            valid = False
        self._require(valid, 'collector engine identity invalid')
        return engine_id

    def _frame_hash(self, payload):
        try:
            return digest(payload)
        except (TypeError, ValueError):
            self._require(False, 'collector frame encoding invalid')

    def _control(self):
        return {**self._identity, 'serverUnixMs': self._now(), 'startUnixMs': self._start,
                'stop': self._stop, 'stopReason': self._reason, 'hash': self._identity_hash}

    def control(self):
        with self._lock:
            self.tick()
            return self._control()

    def fail(self, reason):
        """Supervisor-only trusted fixed reason; HTTP wrapper must not echo input."""
        with self._lock:
            self._stop_for(reason if type(reason) is str and 0 < len(reason) <= 160 else 'collector supervisor stopped', collection=True)
            return self._control()

    def register(self, payload):
        with self._lock:
            self.tick()
            engine_id = self._base(payload)
            self._require(set(payload) == {'runId', 'engineId', 'slots', 'readyWorkers', 'scriptSHA256', 'fixtureSHA256'}, 'collector registration fields differ')
            self._require(payload['scriptSHA256'] == self._config['scriptSHA256'] and payload['fixtureSHA256'] == self._config['fixtureSHA256'], 'collector fixture/script identity differs')
            slots = payload['slots']
            self._require(isinstance(slots, list) and 0 < len(slots) <= 512 and all(integer(s, 0, 511) for s in slots) and len(set(slots)) == len(slots), 'collector slot assignment invalid')
            self._require(integer(payload['readyWorkers'], 0, len(slots)), 'collector worker barrier incomplete')
            slots = sorted(slots)
            if engine_id in self._engines:
                self._require(self._engines[engine_id]['slots'] == slots, 'collector engine attempted to change slot ownership')
            else:
                self._require(self._start is None and not self._stop, 'collector registration already frozen')
                self._require(len(self._engines) < 2 and not any(slot in self._owners for slot in slots), 'collector slot ownership overlaps')
                self._engines[engine_id] = {'slots': slots, 'lastSeenUnixMs': self._now(), 'batches': {}, 'complete': None,
                                           'readyWorkers': 0, 'resources': None, 'lastResourceUnixMs': None,
                                           'lastValidCPUUnixMs': None, 'clock': None, 'highCPUStreak': 0, 'cpuSamples': []}
                self._owners.update({slot: engine_id for slot in slots})
                self._append({'kind': 'register', 'engineId': engine_id, 'slots': slots, 'readyWorkers': 0})
            if payload['readyWorkers'] > self._engines[engine_id]['readyWorkers']:
                self._append({'kind': 'ready', 'engineId': engine_id, 'readyWorkers': payload['readyWorkers']})
                self._engines[engine_id]['readyWorkers'] = payload['readyWorkers']
            self._engines[engine_id]['lastSeenUnixMs'] = self._now()
            if len(self._engines) == 2 and len(self._owners) == 512 and self._start is None and all(e['readyWorkers'] == len(e['slots']) for e in self._engines.values()):
                self._start = math.ceil(self._now()) + 10000
                self._append({'kind': 'barrier', 'startUnixMs': self._start, 'endUnixMs': self._start + self._config['seconds'] * 1000})
            return {**self._control(), 'relations': [copy.deepcopy(self._relations[slot]) for slot in slots]}

    def _clock_resource(self, engine, clock, resources=None):
        self._require(isinstance(clock, dict) and set(clock) == {'uncertaintyMs', 'offsetMs', 'roundTripMs'}, 'collector engine clock fields invalid')
        self._require(number(clock['uncertaintyMs']) and clock['uncertaintyMs'] <= 50 and
                      number(clock['roundTripMs']) and clock['roundTripMs'] <= 2 * clock['uncertaintyMs'] + .001 and
                      type(clock['offsetMs']) in (int, float) and math.isfinite(clock['offsetMs']), 'collector engine clock uncertainty invalid')
        if engine['clock'] is not None:
            old = engine['clock']
            self._require(abs(old['offsetMs'] - clock['offsetMs']) <= old['uncertaintyMs'] + clock['uncertaintyMs'] + .001, 'collector engine clock offset changed')
        engine['clock'] = copy.deepcopy(clock)
        if resources is None:
            return
        self._require(isinstance(resources, dict) and set(resources) == {'cpuTimeNs', 'monotonicNs', 'rssBytes', 'logicalCPUs'}, 'collector engine resource fields invalid')
        self._require(all(integer(resources[k]) for k in ('cpuTimeNs', 'monotonicNs', 'rssBytes')) and
                      integer(resources['rssBytes'], 1) and integer(resources['logicalCPUs'], 1, 65536), 'collector engine resource sample invalid')
        old = engine['resources']
        sample = {'atUnixMs': self._now(), 'raw': copy.deepcopy(resources), 'intervalValid': False,
                  'oneCoreCPUPercent': None, 'normalizedCPUPercent': None}
        if old is not None:
            delta = resources['monotonicNs'] - old['monotonicNs']
            used = resources['cpuTimeNs'] - old['cpuTimeNs']
            self._require(delta > 0 and used >= 0 and resources['logicalCPUs'] == old['logicalCPUs'], 'collector engine resource clock changed')
            sample.update(intervalSeconds=delta / 1000000000, oneCoreCPUPercent=used / delta * 100,
                          normalizedCPUPercent=used / delta / resources['logicalCPUs'] * 100,
                          intervalValid=delta >= 1000000000)
            if sample['intervalValid']:
                engine['highCPUStreak'] = engine['highCPUStreak'] + 1 if sample['normalizedCPUPercent'] >= 85 else 0
            if engine['highCPUStreak'] >= 3:
                self._stop_for('collector engine CPU safety limit')
        engine['resources'] = copy.deepcopy(resources)
        engine['cpuSamples'].append(sample)
        if resources['rssBytes'] >= RSS_LIMIT:
            self._stop_for('collector engine RSS safety limit')

    def _record(self, raw, engine_id):
        self._require(isinstance(raw, dict) and not (set(raw) - RECORD_KEYS) and
                      {'ordinal', 'slot', 'kind', 'startedUnixMs', 'elapsedMs', 'status'} <= set(raw), 'collector native record fields invalid')
        record = copy.deepcopy(raw)
        ordinal, slot, kind = record['ordinal'], record['slot'], record['kind']
        self._require(integer(ordinal, 0, self._config['rate'] * self._config['seconds'] - 1) and integer(slot, 0, 511) and
                      ordinal % 512 == slot and self._owners.get(slot) == engine_id and kind in KINDS, 'collector native ordinal ownership invalid')
        self._require(ordinal not in self._records and ordinal > self._last_ordinal.get(slot, -1), 'collector native ordinal repeated or reordered')
        self._require(slot not in self._retired_slots, 'collector retired relationship used again')
        for key in ('wanted', 'observedLiked'):
            self._require(key not in record or record[key] is None or type(record[key]) is bool, 'collector native fact type invalid')
        for key in ('previousVersion', 'observedVersion'):
            self._require(key not in record or record[key] is None or integer(record[key]), 'collector native fact type invalid')
        self._require('observedNoteId' not in record or record['observedNoteId'] is None or (type(record['observedNoteId']) is str and
                      record['observedNoteId'] in self._note_ids), 'collector native note identity invalid')
        scheduled = self._start + ordinal * 1000 / self._config['rate']
        if kind == 'dropped':
            self._require(all(record[k] is None for k in ('startedUnixMs', 'elapsedMs', 'status')), 'collector dropped record contains HTTP sample')
            self._require(self._now() > scheduled + 50, 'collector dropped ordinal is not overdue')
        else:
            self._require(number(record['startedUnixMs'], 1) and number(record['elapsedMs']) and
                          (record['status'] is None or integer(record['status'], 0, 599)), 'collector native HTTP timing invalid')
            self._require(self._start <= record['startedUnixMs'] < self._start + self._config['seconds'] * 1000 and
                          0 <= record['startedUnixMs'] - scheduled <= 50, 'collector native request outside scheduled window')
            self._require(record['startedUnixMs'] >= self._last_http_end.get(slot, 0), 'collector relationship HTTP samples overlap')
            uncertainty = self._engines[engine_id]['clock']['uncertaintyMs']
            self._require(record['startedUnixMs'] + record['elapsedMs'] <= self._now() + uncertainty, 'collector HTTP terminal precedes its measured completion')
        if kind == 'mutation':
            self._require(all(k in record for k in FACT_KEYS), 'collector mutation facts missing')
            active, version = self._facts[slot]
            self._require(type(record['wanted']) is bool and type(record['observedLiked']) is bool and
                          integer(record['previousVersion']) and integer(record['observedVersion']) and
                          record['status'] == 200 and record['observedNoteId'] == self._relations[slot]['noteId'] and
                          record['previousVersion'] == version and record['wanted'] is not active and
                          record['observedLiked'] is record['wanted'] and record['observedVersion'] == version + 1,
                          'collector mutation facts differ from private relationship')
            self._facts[slot] = (record['observedLiked'], record['observedVersion'])
        else:
            self._stop_for('collector non-mutation native result')
            if kind != 'dropped':
                self._retired_slots.add(slot)
        # Full native facts remain in the durable journal. Runtime aggregation
        # retains no repeated JSON dictionaries or business identifiers.
        self._records[ordinal] = (slot, kind, record['status'], record['elapsedMs'])
        self._last_ordinal[slot] = ordinal
        if kind != 'dropped':
            self._last_http_end[slot] = record['startedUnixMs'] + record['elapsedMs']
        return record

    def batch(self, payload):
        with self._lock:
            self.tick()
            engine_id = self._base(payload)
            self._require(set(payload) == {'runId', 'engineId', 'seq', 'records', 'clock', 'resources'}, 'collector batch fields differ')
            self._require(engine_id in self._engines, 'collector engine not registered')
            engine = self._engines[engine_id]
            self._require(integer(payload['seq']) and isinstance(payload['records'], list) and len(payload['records']) <= 100000, 'collector batch sequence/size invalid')
            frame_hash = self._frame_hash(payload)
            if payload['seq'] in engine['batches']:
                self._require(engine['batches'][payload['seq']] == frame_hash, 'collector duplicate sequence content differs')
                engine['lastSeenUnixMs'] = self._now()
                return {**self._control(), 'ack': payload['seq'], 'seq': payload['seq'], 'duplicate': True}
            self._require(engine['complete'] is None and payload['seq'] == len(engine['batches']), 'collector batch sequence is not continuous')
            self._require(self._start is not None or not payload['records'], 'collector native result precedes barrier')
            self._clock_resource(engine, payload['clock'], payload['resources'])
            prior = (dict(self._facts), dict(self._last_ordinal), dict(self._last_http_end), set(self._retired_slots))
            added_ordinals = []
            try:
                records = []
                for row in payload['records']:
                    record = self._record(row, engine_id)
                    records.append(record)
                    added_ordinals.append(record['ordinal'])
                # Private rows/tokens never enter a public frame; only strict native fields do.
                self._append({'kind': 'batch', 'runId': payload['runId'], 'engineId': engine_id, 'seq': payload['seq'],
                              'frameSHA256': frame_hash, 'records': records, 'clock': payload['clock'], 'resources': payload['resources']})
            except ValueError:
                self._facts, self._last_ordinal, self._last_http_end, self._retired_slots = prior
                for ordinal in added_ordinals:
                    self._records.pop(ordinal, None)
                raise
            engine['batches'][payload['seq']] = frame_hash
            engine['lastSeenUnixMs'] = self._now()
            engine['lastResourceUnixMs'] = engine['lastSeenUnixMs']
            if engine['cpuSamples'][-1]['intervalValid']:
                engine['lastValidCPUUnixMs'] = engine['lastSeenUnixMs']
            return {**self._control(), 'ack': payload['seq'], 'seq': payload['seq'], 'duplicate': False}

    def _planned(self, slots):
        total = self._config['rate'] * self._config['seconds']
        return sum((total - 1 - slot) // 512 + 1 for slot in slots if slot < total)

    def _raw_counts(self, engine_id=None):
        kinds = Counter(r[1] for r in self._records.values() if engine_id is None or self._owners[r[0]] == engine_id)
        attempted = sum(kinds.values()) - kinds['dropped']
        return {'planned': self._config['rate'] * self._config['seconds'] if engine_id is None else self._planned(self._engines[engine_id]['slots']),
                'attempted': attempted, 'completed': attempted, **{key: kinds[key] for key in KINDS}}

    def _statistics(self):
        if self._stats_version != len(self._records):
            all_elapsed = [r[3] for r in self._records.values() if r[1] != 'dropped']
            mutation_elapsed = [r[3] for r in self._records.values() if r[1] == 'mutation']
            self._stats_cache = {'counts': self._raw_counts(), 'allLatency': quantiles(all_elapsed),
                'mutationLatency': quantiles(mutation_elapsed), 'latencySampleCount': len(all_elapsed),
                'httpErrors': sum(r[1] != 'dropped' and (r[2] in (None, 0) or r[2] >= 400) for r in self._records.values()),
                'systemErrors': sum(r[1] != 'dropped' and (r[2] in (None, 0) or r[2] >= 500) for r in self._records.values())}
            self._stats_version = len(self._records)
        return copy.deepcopy(self._stats_cache)

    def complete(self, payload):
        with self._lock:
            self.tick()
            engine_id = self._base(payload)
            self._require(set(payload) == {'runId', 'engineId', 'lastSeq', 'counts', 'clock', 'stoppedOffering', 'inFlight'}, 'collector completion fields differ')
            self._require(engine_id in self._engines, 'collector engine not registered')
            engine = self._engines[engine_id]
            completion_hash = self._frame_hash(payload)
            if engine['complete'] is not None:
                self._require(engine['complete']['hash'] == completion_hash, 'collector completion changed')
                return {**self._control(), 'accepted': True, 'complete': engine['complete']['valid'], 'duplicate': True}
            self._require(type(payload['lastSeq']) is int and payload['lastSeq'] == len(engine['batches']) - 1, 'collector completion missing final batch ACK')
            counts = payload['counts']
            self._require(isinstance(counts, dict) and set(counts) == set(COUNT_KEYS) and all(integer(v) for v in counts.values()), 'collector completion counts invalid')
            self._clock_resource(engine, payload['clock'])
            raw = self._raw_counts(engine_id)
            stopped = payload['stoppedOffering'] is True and type(payload['inFlight']) is int and payload['inFlight'] == 0
            counters_match = counts == raw
            coverage = sum(raw[k] for k in KINDS) == raw['planned']
            duration = self._start is not None and self._now() >= self._start + self._config['seconds'] * 1000
            resource_fresh = (engine['lastResourceUnixMs'] is not None and engine['lastValidCPUUnixMs'] is not None and
                              0 <= self._now() - engine['lastResourceUnixMs'] < 15000 and
                              0 <= self._now() - engine['lastValidCPUUnixMs'] < 15000)
            valid = bool(stopped and counters_match and coverage and duration and resource_fresh)
            self._append({'kind': 'complete', 'runId': payload['runId'], 'engineId': engine_id,
                          'completionSHA256': completion_hash, 'lastSeq': payload['lastSeq'], 'counts': counts,
                          'clock': payload['clock'], 'stoppedOffering': payload['stoppedOffering'], 'inFlight': payload['inFlight'],
                          'collectorRawCounts': raw, 'ordinalCoverageComplete': coverage, 'nativeDurationComplete': duration,
                          'resourceEvidenceFresh': resource_fresh,
                          'accepted': True, 'complete': valid})
            engine['complete'] = {'hash': completion_hash, 'valid': valid, 'counts': copy.deepcopy(counts),
                                  'stoppedOffering': stopped, 'counterMatches': counters_match, 'coverage': coverage, 'duration': duration}
            engine['lastSeenUnixMs'] = self._now()
            if not valid:
                self._stop_for('collector native completion incomplete', collection=True)
            return {**self._control(), 'accepted': True, 'complete': valid, 'duplicate': False}

    def tick(self):
        with self._lock:
            stamp = self._now()
            if self._start is not None and stamp >= self._start:
                for engine in self._engines.values():
                    if engine['complete'] is None and stamp - max(self._start, engine['lastSeenUnixMs']) >= 15000:
                        self._stop_for('collector engine heartbeat missing', collection=True)
                    if engine['complete'] is None and stamp - max(self._start, engine['lastResourceUnixMs'] or self._start) >= 15000:
                        self._stop_for('collector engine resource observation missing', collection=True)
                    if engine['complete'] is None and stamp - max(self._start, engine['lastValidCPUUnixMs'] or self._start) >= 15000:
                        self._stop_for('collector engine valid CPU interval missing', collection=True)
                    if engine['complete'] is None and stamp >= self._start + self._config['seconds'] * 1000 + 15000:
                        self._stop_for('collector native terminal deadline missing', collection=True)
            return self._control()

    def summary(self):
        with self._lock:
            self.tick()
            statistics = self._statistics()
            raw = statistics['counts']
            engines = [{'engineId': engine_id, 'lastSeenUnixMs': engine['lastSeenUnixMs'],
                        'complete': engine['complete'] is not None and engine['complete']['valid'],
                        'stoppedOffering': engine['complete'] is not None and engine['complete']['stoppedOffering'],
                        'resources': copy.deepcopy(engine['resources']), 'clock': copy.deepcopy(engine['clock']),
                        'lastResourceUnixMs': engine['lastResourceUnixMs'], 'lastValidCPUUnixMs': engine['lastValidCPUUnixMs'],
                        'cpuSamples': copy.deepcopy(engine['cpuSamples']), 'lastSeq': len(engine['batches']) - 1,
                        'counts': copy.deepcopy(engine['complete']['counts']) if engine['complete'] is not None else None}
                       for engine_id, engine in sorted(self._engines.items())]
            complete = len(engines) == 2 and len(self._owners) == 512 and all(e['complete'] for e in engines)
            collection_valid = complete and not self._collection_errors
            unknown_exact = collection_valid
            return {**self._control(), 'complete': complete, 'endUnixMs': self._start + self._config['seconds'] * 1000 if self._start is not None else None,
                    'counts': raw, 'notOffered': raw['planned'] - raw['attempted'] - raw['dropped'],
                    'allLatency': statistics['allLatency'], 'mutationLatency': statistics['mutationLatency'],
                    'quantileMethod': 'R7; full raw native HTTP elapsedMs, including failed HTTP terminals',
                    'latencyComplete': collection_valid, 'latencySampleCount': statistics['latencySampleCount'],
                    'engines': engines, 'collectionValid': collection_valid,
                    'unknownIsExact': unknown_exact, 'unknown': raw['uncertain'] if unknown_exact else None,
                    'effectUnknown': raw['uncertain'], 'classificationUnknown': 0 if unknown_exact else None,
                    'httpErrors': statistics['httpErrors'], 'systemErrors': statistics['systemErrors'],
                    'batchFatal': bool(self._collection_errors or any(raw[k] for k in ('noop', 'uncertain', 'rejected', 'invalid'))),
                    'childrenConverged': len(engines) == 2 and all(e['stoppedOffering'] for e in engines),
                    'violations': list(self._violations), 'collectionErrors': list(self._collection_errors),
                    'resourcesScope': 'PTS environment: each independently allocated remote engine must stay below 2 GiB process RSS; the aggregate is displayed, not compared with a shared limit. Local Python collector plus Node classification retain their separate combined 2 GiB limit. This environment adaptation is not API optimization evidence.',
                    'remoteRSSBytes': sum(e['resources']['rssBytes'] for e in engines if e['resources'] is not None),
                    'evidenceSHA256': self._journal_hasher.hexdigest()}
