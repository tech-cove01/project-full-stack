"""Bounded CAP08 loopback HTTP collector, explicit diagnostic use only.

Only Python's standard library is imported. Controlled mode returns after its
first capture; actual diagnosis uses the fixed 8351 origin and stage files.
"""
import argparse
from collections import deque
import datetime as dt
import hashlib
import json
from pathlib import Path
import time
import urllib.parse
import urllib.request


STAGES = frozenset(('feed.service', 'feed.facts', 'feed.cards', 'feed.card_fallback', 'feed.counts', 'auth.resolve',
    'sql.begin_acquire_roundtrip', 'sql.transaction_hold', 'sql.statement_total', 'sql.call_until_rows',
    'sql.exec_call', 'sql.prepare_call', 'redis.command_total', 'redis.pipeline_total', 'redis.dial',
    'counts.get_many', 'counts.publication_wait', 'counts.publication_hold', 'counts.redis_read',
    'counts.redis_write', 'counts.redis_delete', 'counts.cold_sql', 'cards.get_many', 'cards.caller_redis_read',
    'cards.shared', 'cards.slot_wait', 'cards.recheck', 'cards.cold_sql', 'cards.fill'))
REQUIRED = frozenset(('feed.facts', 'feed.cards', 'feed.counts', 'auth.resolve', 'cards.shared',
                      'cards.slot_wait', 'cards.recheck', 'cards.cold_sql', 'cards.fill'))
AGE_NS = 100_000_000
PROFILE_LIMIT = 1 << 20
CAPACITY_LIMIT = 2 << 20
MAX_GAP_SECONDS = 2.5


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, response, code, message, headers, url):
        return None


class SlowRecords:
    """At most 32 actual slow payloads; timelines keep only sequence references."""
    def __init__(self):
        self.selected = {}
        self.observed_occurrences = 0
        self.invalid_occurrences = 0
        self.complete = True

    def compact(self, capacity):
        result = dict(capacity)
        source = capacity.get('tail')
        if not isinstance(source, dict):
            self.complete = False
            result['tail'] = {'slow_traces_omitted': True, 'slow_record_visibility_missing': True}
            return result
        tail = dict(source)
        traces = tail.pop('slow_traces', None)
        visible, invalid = [], 0
        if not isinstance(traces, list):
            self.complete = False
            traces = []
            invalid += 1
        for trace in traces:
            self.observed_occurrences += 1
            fields = {'sequence', 'scope', 'start_at', 'end_at', 'total_ns', 'error', 'canceled', 'truncated', 'spans'}
            valid = (isinstance(trace, dict) and set(trace) == fields and type(trace.get('sequence')) is int and
                     trace['sequence'] > 0 and type(trace.get('total_ns')) is int and trace['total_ns'] >= 0 and
                     trace.get('scope') in ('feed.http', 'feed.service', 'cards.shared') and
                     isinstance(trace.get('spans'), list) and len(trace['spans']) <= 256 and
                     all(isinstance(span, dict) and set(span) == {'name', 'offset_ns', 'duration_ns', 'error', 'items'} and
                         span.get('name') in STAGES for span in trace['spans']))
            if not valid:
                invalid += 1
                self.complete = False
                continue
            sequence = trace['sequence']
            visible.append(sequence)
            self.selected[sequence] = trace
            if len(self.selected) > 32:
                smallest = min(self.selected, key=lambda key: (self.selected[key]['total_ns'], key))
                del self.selected[smallest]
        self.invalid_occurrences += invalid
        tail.update(visible_slow_sequences=visible, slow_traces_omitted=True,
                    slow_record_visibility_errors=invalid,
                    slow_record_reference_scope='visible sequences; payload only if retained in slow-records.json top 32; absent references are explicit missing payloads')
        result['tail'] = tail
        return result

    def manifest(self):
        retained = sorted(self.selected.values(), key=lambda item: (item['total_ns'], item['sequence']), reverse=True)
        return {'maximum_records': 32, 'selection': 'highest observed total_ns, then highest sequence; at most 32 distinct actual payloads',
                'retained': retained, 'retained_sequences': [trace['sequence'] for trace in retained],
                'observed_record_occurrences': self.observed_occurrences,
                'invalid_record_occurrences': self.invalid_occurrences,
                'visibility_complete': self.complete, 'full_responses_archived': False,
                'missing_scope': 'timeline/window sequence reference not in retained_sequences has no archived payload; app ring/poll gaps may hide additional records'}


def now():
    return dt.datetime.now(dt.timezone.utc).isoformat()


def read(path):
    return json.loads(Path(path).read_text(encoding='utf-8-sig'))


def save(path, value):
    with Path(path).open('x', encoding='utf-8') as stream:
        json.dump(value, stream, ensure_ascii=False, indent=2)


def endpoint(value):
    parsed = urllib.parse.urlsplit(value)
    assert parsed.scheme == 'http' and parsed.hostname == '127.0.0.1' and parsed.port and 1 <= parsed.port <= 65535
    assert parsed.path == '/debug/capacity' and not parsed.query and not parsed.fragment and parsed.username is None
    return parsed.scheme + '://' + parsed.netloc


def fetch(opener, url, maximum, timeout):
    started, tick = now(), time.monotonic()
    observed = {'started_at': started, 'available': False, 'bytes': None, 'sha256': None,
                'truncated': None, 'http_status': None, 'error': None}
    raw = None
    try:
        with opener.open(url, timeout=timeout) as response:
            observed['http_status'] = response.getcode()
            raw = response.read(maximum + 1)
        observed['truncated'] = len(raw) > maximum
        raw = raw[:maximum]
        observed.update(available=True, bytes=len(raw), sha256=hashlib.sha256(raw).hexdigest())
    except Exception as error:
        observed['error'] = type(error).__name__
        observed['http_status'] = getattr(error, 'code', observed['http_status'])
    finally:
        observed.update(finished_at=now(), elapsed_ms=(time.monotonic() - tick) * 1000)
    return raw, observed


def coverage(starts, stable_unix_ms, done_at, maximum_gap_ms):
    if stable_unix_ms is None or done_at is None or not starts:
        return {'complete': False, 'first_gap_seconds': None, 'tail_gap_seconds': None,
                'maximum_allowed_gap_seconds': MAX_GAP_SECONDS, 'missing_boundary': True}
    stable = dt.datetime.fromtimestamp(stable_unix_ms / 1000, dt.timezone.utc)
    done = dt.datetime.fromisoformat(done_at.replace('Z', '+00:00'))
    eligible = [dt.datetime.fromisoformat(value.replace('Z', '+00:00')) for value in starts]
    eligible = [value for value in eligible if stable <= value <= done]
    first = (eligible[0] - stable).total_seconds() if eligible else None
    tail = (done - eligible[-1]).total_seconds() if eligible else None
    return {'complete': bool(eligible and 0 <= first <= MAX_GAP_SECONDS and 0 <= tail <= MAX_GAP_SECONDS and
                             maximum_gap_ms <= MAX_GAP_SECONDS * 1000),
            'first_gap_seconds': first, 'tail_gap_seconds': tail,
            'maximum_allowed_gap_seconds': MAX_GAP_SECONDS, 'missing_boundary': False,
            'scope': 'stable launcher creation stamp to supervisor done UTC; not atomic workload/capture timing'}


def triggers(previous, current):
    tail = current.get('tail', {})
    assert tail.get('enabled') is True and isinstance(tail.get('stages'), list), 'enabled fixed-stage observations required'
    seen, reasons, incomplete = set(), [], []
    for stage in tail['stages']:
        name = stage.get('name')
        assert name in STAGES and name not in seen, 'unknown or duplicate diagnostic stage'
        seen.add(name)
        for field in ('active', 'tracked_active', 'untracked_active', 'oldest_active_ns', 'active_tracking_overflows'):
            assert type(stage.get(field)) is int and stage[field] >= 0, 'missing/invalid stage age fields'
        assert type(stage.get('oldest_complete')) is bool
        if stage['active'] > 0 and not stage['oldest_complete']:
            incomplete.append(name)
        if stage['active'] > 0 and stage['oldest_complete'] and stage['oldest_active_ns'] >= AGE_NS:
            reasons.append({'kind': 'stage_active_age', 'name': name,
                            'active': stage['active'], 'oldest_active_ns': stage['oldest_active_ns'],
                            'scope': 'in-flight boundary age; includes nested work and is not pure pool acquisition time'})
    assert REQUIRED.issubset(seen), 'required CAP08 stages absent; missing is not zero'
    if previous is not None:
        for pool in ('sql', 'redis'):
            left, right = previous.get(pool, {}), current.get(pool, {})
            for field in ('wait_count', 'wait_duration_ns'):
                assert type(left.get(field)) is int and type(right.get(field)) is int, 'pool counter unavailable'
                delta = right[field] - left[field]
                assert delta >= 0, 'pool counter reset'
            count = right['wait_count'] - left['wait_count']
            duration = right['wait_duration_ns'] - left['wait_duration_ns']
            if count > 0 or duration > 0:
                reasons.append({'kind': pool + '_global_pool_wait_delta', 'count': count, 'duration_ns': duration,
                                'scope': 'whole API pool cumulative delta, not any one request latency'})
    return reasons, incomplete


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--url', required=True)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--max-seconds', type=float, default=360)
    parser.add_argument('--max-captures', type=int, default=3)
    parser.add_argument('--stop-after-capture', action='store_true')
    parser.add_argument('--expected-pid', type=int)
    parser.add_argument('--api-metadata', type=Path)
    parser.add_argument('--start-file', type=Path)
    parser.add_argument('--done-file', type=Path)
    args = parser.parse_args()
    origin = endpoint(args.url)
    assert .1 <= args.max_seconds <= 360 and 1 <= args.max_captures <= 3
    if args.stop_after_capture:
        assert args.max_captures == 1 and args.max_seconds <= 5, 'one bounded controlled capture only'
    else:
        assert origin == 'http://127.0.0.1:8351' and args.expected_pid and args.start_file and args.done_file and args.api_metadata
    if args.api_metadata:
        metadata = read(args.api_metadata)
        assert metadata['pid'] == args.expected_pid and metadata['phase'] == 'diagnostic' and metadata['tailEnabledRequested'] is True
    output = args.output.resolve()
    assert not output.exists(), 'exclusive new output required'
    output.mkdir(parents=False)
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
    record = {'task': 'CAP-08B' if args.stop_after_capture else 'CAP-08C', 'started_at': now(),
              'diagnostic_only': True, 'controlled': args.stop_after_capture, 'url': args.url,
              'expected_pid': args.expected_pid, 'poll_interval_seconds': 1,
              'age_trigger_ns': AGE_NS, 'maximum_goroutine_captures': args.max_captures,
              'maximum_profile_bytes': PROFILE_LIMIT, 'minimum_capture_spacing_seconds': 15,
              'minimum_capture_spacing_scope': 'previous capture completion to next capture request',
              'maximum_poll_start_gap_seconds': MAX_GAP_SECONDS,
              'pre_trigger_seconds': 20, 'post_trigger_seconds': 10,
              'captures': [], 'errors': [], 'stable_observed': args.stop_after_capture,
              'successful_polls': 0, 'failed_polls': 0, 'stage_fields_valid': True,
              'active_age_coverage_complete': True,
              'script_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
              'generator_identity_scope': 'HTTP-only collector; actual k6/API identity is guarded by the separate five-second supervisor. Capture timing must be aligned to its native stage records.'}
    if args.api_metadata:
        record['api_source_sha'] = metadata['sourceSha']
        record['api_metadata_sha256'] = hashlib.sha256(args.api_metadata.read_bytes()).hexdigest()
    before, windows = deque(maxlen=21), []
    previous = None
    sequence = 0
    started = time.monotonic()
    deadline = started + args.max_seconds
    scheduled = None
    last_capture = -1_000_000.0
    prior_sample = None
    maximum_gap = 0
    sample_starts = []
    slow_records = SlowRecords()
    try:
        with (output / 'timeline.ndjson').open('x', encoding='utf-8') as timeline:
            while time.monotonic() < deadline:
                if args.done_file and args.done_file.exists():
                    break
                if not record['stable_observed']:
                    try:
                        stable = read(args.start_file)
                        assert stable.get('phase') == 'stable' and type(stable.get('k6_pid')) is int
                        record.update(stable_observed=True, stable_observed_at=now(), stable_pid=stable['k6_pid'],
                                      stable_created_unix_ms=stable['k6_started_unix_ms'])
                    except (OSError, ValueError, KeyError, AssertionError):
                        time.sleep(min(.05, max(0, deadline - time.monotonic())))
                        continue
                tick = time.monotonic()
                scheduled = tick if scheduled is None else scheduled
                sequence += 1
                if prior_sample is not None:
                    maximum_gap = max(maximum_gap, (tick - prior_sample) * 1000)
                prior_sample = tick
                while before and tick - before[0][0] > 20:
                    before.popleft()
                sample_started_at = now()
                sample_starts.append(sample_started_at)
                row = {'kind': 'sample', 'sequence': sequence, 'at': sample_started_at, 'elapsed_seconds': tick - started,
                       'collector_scheduling_lateness_ms': max(0, tick - scheduled) * 1000, 'errors': []}
                diagnostic = None
                try:
                    raw, observation = fetch(opener, args.url, CAPACITY_LIMIT, min(2, max(.01, deadline - time.monotonic())))
                    row['observation'] = observation
                    assert observation['available'], 'capacity fetch unavailable; missing is not zero'
                    assert not observation['truncated'], 'capacity exceeded fixed bound'
                    diagnostic = json.loads(raw)
                    assert args.expected_pid is None or diagnostic.get('pid') == args.expected_pid, 'diagnostic API PID mismatch'
                    row['diagnostics'] = slow_records.compact(diagnostic)
                    row['capacity_archive_scope'] = 'parsed subset: all original stage/pool/card/runtime fields, slow payloads removed; observation SHA256 is the actual fetched raw response, which is not stored in full'
                    reasons, incomplete = triggers(previous, diagnostic)
                    row.update(observation=observation, trigger_reasons=reasons,
                               oldest_incomplete_stages=incomplete)
                    record['active_age_coverage_complete'] &= not bool(incomplete)
                    record['successful_polls'] += 1
                    previous = diagnostic
                    if reasons and len(record['captures']) < args.max_captures and time.monotonic() - last_capture >= 15:
                        number = len(record['captures']) + 1
                        capture = {'number': number, 'at': now(), 'trigger_sample_sequence': sequence, 'reasons': reasons,
                                   'released_state': 'not observed by collector; controlled caller must separately prove barrier remains held'}
                        try:
                            raw, observed = fetch(opener, origin + '/debug/pprof/goroutine?debug=1', PROFILE_LIMIT,
                                                  min(3, max(.01, deadline - time.monotonic())))
                            capture['fetch'] = observed
                            capture.update(observed)
                            if observed['available']:
                                name = 'goroutine-' + str(number) + '.txt'
                                with (output / name).open('xb') as profile:
                                    profile.write(raw)
                                capture.update(file=name, **observed)
                        except Exception as error:
                            capture.update(available=False, error=type(error).__name__)
                        capture['done_file_present_after_capture'] = bool(args.done_file and args.done_file.exists())
                        capture['load_scope'] = 'not atomic with workload lifetime; align request/response timestamps with supervisor start/end before excluding prior waits'
                        record['captures'].append(capture)
                        windows.append({'trigger': capture, 'pre_samples': [sample for _, sample in before],
                                        'trigger_sample_sequence': sequence, 'samples': [], 'ends_monotonic': time.monotonic() + 10})
                        last_capture = time.monotonic()
                except Exception as error:
                    row['errors'].append(type(error).__name__)
                    record['failed_polls'] += 1
                    record['stage_fields_valid'] = False
                row['poll_and_capture_elapsed_ms'] = (time.monotonic() - tick) * 1000
                row['missed_poll_intervals'] = max(0, int((time.monotonic() - scheduled) // 1))
                timeline.write(json.dumps(row, ensure_ascii=False) + '\n')
                timeline.flush()
                before.append((tick, row))
                for window in windows:
                    if window['ends_monotonic'] >= tick:
                        window['samples'].append(row)
                if args.stop_after_capture and record['captures']:
                    break
                scheduled = max(scheduled + 1, time.monotonic())
                while time.monotonic() < min(scheduled, deadline):
                    if args.done_file and args.done_file.exists():
                        break
                    time.sleep(min(.05, max(0, min(scheduled, deadline) - time.monotonic())))
    except BaseException as error:
        record['errors'].append(type(error).__name__)
        raise
    finally:
        done_at = None
        if args.done_file and args.done_file.exists():
            try:
                done_at = read(args.done_file)['at']
            except (OSError, ValueError, KeyError):
                record['errors'].append('done boundary unavailable or malformed')
        try:
            observed_coverage = coverage(sample_starts, record.get('stable_created_unix_ms'), done_at, maximum_gap)
        except (ValueError, TypeError, OverflowError):
            record['errors'].append('stable/done coverage boundary invalid')
            observed_coverage = {'complete': False, 'missing_boundary': True, 'maximum_allowed_gap_seconds': MAX_GAP_SECONDS}
        if args.stop_after_capture:
            observed_coverage = {'complete': True, 'scope': 'controlled single capture; no stable-window coverage claim'}
        record.update(finished_at=now(), elapsed_seconds=time.monotonic() - started, samples=sequence,
                      maximum_sample_start_gap_ms=maximum_gap, window_coverage=observed_coverage,
                      slow_records_visibility_complete=slow_records.complete,
                      poll_coverage_complete=record['successful_polls'] > 0 and record['failed_polls'] == 0 and observed_coverage['complete'],
                      stopped_by_done_file=bool(args.done_file and args.done_file.exists()))
        for number, window in enumerate(windows, 1):
            window.pop('ends_monotonic')
            window['post_scope'] = 'up to ten seconds; stop/capture-only/diagnostic exit can truncate the window'
            save(output / ('trigger-' + str(number) + '-window.json'), window)
        save(output / 'slow-records.json', slow_records.manifest())
        record['limits'] = ['Diagnostic-only overhead; never a formal score.',
            'Incomplete oldest tracking is explicit and never converted to a zero-age exclusion.',
            'Cumulative pool wait is not per-request wait; nested request/shared stages must not be added.',
            'No process enumeration, SQL, Redis, secrets, keys or command-line collection is performed.',
            'Profile bytes are capped; any truncated/missing profile remains explicit.']
        save(output / 'manifest.json', record)
    valid = (record['stable_observed'] and record['poll_coverage_complete'] and record['stage_fields_valid'] and record['active_age_coverage_complete'] and record['slow_records_visibility_complete'] and
             not record['errors'] and all(capture.get('available') is True and not capture.get('truncated') for capture in record['captures']))
    if args.stop_after_capture:
        valid &= len(record['captures']) == 1
    print(json.dumps({'task': record['task'], 'samples': sequence, 'captures': len(record['captures']),
                      'successfulPolls': record['successful_polls'], 'failedPolls': record['failed_polls'], 'valid': bool(valid)}))
    return 0 if valid else 1


if __name__ == '__main__':
    try:
        raise SystemExit(main())
    except Exception as error:
        print('cap08-collector: ' + type(error).__name__ + '; bounded evidence preserved where initialized')
        raise SystemExit(1)
