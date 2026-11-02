"""Pure collector protocol counterexamples. No HTTP, JMeter, business or cloud."""
import argparse
import copy
import hashlib
import importlib.util
import json
from pathlib import Path
import unittest
from unittest.mock import patch
import uuid


SOURCE = Path(__file__).with_name('cap10-pts-collector.py')
REPO = SOURCE.parents[2]
spec = importlib.util.spec_from_file_location('cap10_pts_collector_checks', SOURCE)
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
ENGINE_IDS = ('00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000002')


class Checks(unittest.TestCase):
    def setUp(self):
        self.root = REPO.parent / ('.qa-cap10-pts-collector-' + uuid.uuid4().hex)
        self.root.mkdir()
        self.stamp = 1000000
        self.config = {'runId': 'synthetic-run', 'phase': 'stable', 'rate': 50, 'seconds': 60, 'apiUrl': 'http://10.0.0.1:8281',
                       'expectedEngines': 2, 'controlToken': 'PRIVATE-CONTROL-SENTINEL',
                       'fixtureSHA256': 'a' * 64, 'scriptSHA256': 'b' * 64,
                       'relations': [{'slot': i, 'userId': str(i + 1000), 'noteId': str(i + 2000),
                                      'active': 0, 'version': 0, 'token': 'PRIVATE-SESSION-SENTINEL-' + str(i)} for i in range(512)]}
        self.store = module.EngineStore(self.config, self.root / 'journal.ndjson', clock=lambda: self.stamp)
        self.addCleanup(self.cleanup)

    def cleanup(self):
        self.store.close()
        target = self.root.resolve()
        assert target.parent == REPO.parent.resolve() and target.name.startswith('.qa-cap10-pts-collector-')
        for path in target.iterdir():
            assert path.is_file() and path.resolve().parent == target
            path.unlink()
        target.rmdir()

    def register(self, engine=0, slots=None, **changes):
        slots = list(range(engine * 256, (engine + 1) * 256)) if slots is None else slots
        payload = {'runId': self.config['runId'], 'engineId': ENGINE_IDS[engine], 'slots': slots,
                   'readyWorkers': len(slots), 'scriptSHA256': self.config['scriptSHA256'],
                   'fixtureSHA256': self.config['fixtureSHA256'], **changes}
        return self.store.register(payload)

    def ready(self):
        self.register(0)
        self.register(1)
        self.start = self.store.control()['startUnixMs']

    def clock(self, **changes):
        return {'uncertaintyMs': 1, 'offsetMs': 0, 'roundTripMs': 1, **changes}

    def resources(self, **changes):
        return {'cpuTimeNs': self.stamp * 100000, 'monotonicNs': self.stamp * 1000000,
                'rssBytes': 100000000, 'logicalCPUs': 2, **changes}

    def record(self, ordinal=0, **changes):
        slot = ordinal % 512
        previous = ordinal // 512
        wanted = not bool(previous % 2)
        return {'ordinal': ordinal, 'slot': slot, 'kind': 'mutation',
                'startedUnixMs': self.start + ordinal * 1000 / self.config['rate'],
                'elapsedMs': ordinal % 250 + 1, 'status': 200, 'wanted': wanted,
                'observedLiked': wanted, 'previousVersion': previous,
                'observedVersion': previous + 1, 'observedNoteId': str(slot + 2000), **changes}

    def batch(self, engine=0, records=None, seq=None, **changes):
        if seq is None:
            seq = len(self.store._engines[ENGINE_IDS[engine]]['batches'])
        payload = {'runId': self.config['runId'], 'engineId': ENGINE_IDS[engine], 'seq': seq,
                   'records': records or [], 'clock': self.clock(), 'resources': self.resources(), **changes}
        return self.store.batch(payload)

    def declared(self, engine=0, **changes):
        planned = sum(1 for i in range(module.planned_total(self.config['rate'], self.config['seconds'])) if engine * 256 <= i % 512 < (engine + 1) * 256)
        return {'planned': planned, 'attempted': 0, 'completed': 0, 'mutation': 0, 'noop': 0,
                'uncertain': 0, 'rejected': 0, 'invalid': 0, 'dropped': 0, **changes}

    def complete(self, engine=0, counts=None, **changes):
        payload = {'runId': self.config['runId'], 'engineId': ENGINE_IDS[engine],
                   'lastSeq': len(self.store._engines[ENGINE_IDS[engine]]['batches']) - 1,
                   'counts': counts or self.declared(engine), 'clock': self.clock(),
                   'stoppedOffering': True, 'inFlight': 0, **changes}
        return self.store.complete(payload)

    def full_window(self, seconds=60, mutate=None):
        if seconds != self.config['seconds']:
            self.store.close()
            self.config['seconds'] = seconds
            self.store = module.EngineStore(self.config, self.root / 'journal-180.ndjson', clock=lambda: self.stamp)
        self.ready()
        declared = [self.declared(0), self.declared(1)]
        pending = [self.record(i) for i in range(module.planned_total(self.config['rate'], seconds))]
        if mutate:
            for row in pending:
                mutate(row)
        for elapsed in range(5000, seconds * 1000 + 5001, 5000):
            self.stamp = self.start + elapsed
            records = [[], []]
            ready = [row for row in pending if row['startedUnixMs'] + row['elapsedMs'] <= self.stamp]
            pending = [row for row in pending if row['startedUnixMs'] + row['elapsedMs'] > self.stamp]
            for row in ready:
                engine = row['slot'] // 256
                records[engine].append(row)
                declared[engine][row['kind']] += 1
                if row['kind'] != 'dropped':
                    declared[engine]['attempted'] += 1
                    declared[engine]['completed'] += 1
            for engine in (0, 1):
                self.batch(engine, records[engine])
        return declared

    def test_exact_two_engine_512_barrier_and_private_rows(self):
        first = self.register(0)
        self.assertIsNone(first['startUnixMs'])
        self.assertEqual(len(first['relations']), 256)
        retry = self.register(0)
        self.assertIsNone(retry['startUnixMs'])
        second = self.register(1)
        self.assertEqual(second['startUnixMs'], self.stamp + 10000)
        self.assertNotIn('relations', self.store.control())
        text = (self.root / 'journal.ndjson').read_text()
        self.assertNotIn('PRIVATE-', text)
        self.assertNotIn('userId', text)

    def test_overlap_different_owner_is_fatal(self):
        self.register(0)
        with self.assertRaisesRegex(ValueError, 'ownership'):
            self.register(1, slots=list(range(255, 511)))
        self.assertTrue(self.store.control()['stop'])

    def test_private_row_allocation_precedes_full_worker_barrier(self):
        self.assertIsNone(self.register(0, readyWorkers=0)['startUnixMs'])
        self.assertIsNone(self.register(1, readyWorkers=0)['startUnixMs'])
        self.assertIsNone(self.register(0)['startUnixMs'])
        self.assertEqual(self.register(1)['startUnixMs'], self.stamp + 10000)
        self.assertEqual(self.store.control()['apiUrl'], 'http://10.0.0.1:8281')

    def test_missing_workers_or_wrong_script_cannot_release(self):
        self.register(0, readyWorkers=255)
        self.register(1)
        self.assertIsNone(self.store.control()['startUnixMs'])
        with self.assertRaises(ValueError):
            self.register(0, scriptSHA256='c' * 64)

    def test_registration_is_immutable_after_barrier(self):
        self.ready()
        with self.assertRaises(ValueError):
            self.register(0, slots=list(range(256, 512)))

    def test_native_60s_complete_counts_and_full_quantiles(self):
        declared = self.full_window()
        self.assertFalse(self.store.summary()['batchFatal'])
        for engine in (0, 1):
            self.assertTrue(self.complete(engine, declared[engine])['complete'])
        summary = self.store.summary()
        self.assertTrue(summary['complete'] and summary['collectionValid'])
        self.assertEqual(summary['endUnixMs'] - summary['startUnixMs'], 60000)
        self.assertEqual((summary['counts']['planned'], summary['counts']['mutation'], summary['notOffered']), (2995, 2995, 0))
        self.assertEqual((summary['launchGuardMs'], summary['plannedTotal']), (100, 2995))
        self.assertEqual((summary['unknown'], summary['unknownIsExact']), (0, True))
        self.assertEqual(summary['allLatency']['max'], 250)
        self.assertEqual(summary['latencySampleCount'], 2995)
        self.assertEqual(summary['evidenceSHA256'], hashlib.sha256((self.root / 'journal.ndjson').read_bytes()).hexdigest())

    def test_180_window_has_same_fixed_nominal_boundary(self):
        declared = self.full_window(seconds=180)
        for engine in (0, 1):
            self.complete(engine, declared[engine])
        summary = self.store.summary()
        self.assertTrue(summary['complete'])
        self.assertEqual(summary['counts']['mutation'], 8995)
        self.assertEqual(summary['endUnixMs'] - summary['startUnixMs'], 180000)

    def test_guard_plan_rounding_shards_and_unchanged_warm(self):
        for rate in (1, 50, 800, 5000):
            for seconds in (10, 60, 180):
                expected = rate * (seconds * 1000 - (0 if seconds == 10 else 100)) // 1000
                self.assertEqual(module.planned_total(rate, seconds), expected)
        self.assertEqual((module.planned_total(800, 60), module.planned_total(800, 180)), (47920, 143920))
        self.assertGreaterEqual(module.planned_total(800, 180) / (800 * 180), .99)
        self.assertEqual(self.store._planned(range(0, 512, 2)) + self.store._planned(range(1, 512, 2)), 2995)
        self.store.close(); self.config.update(phase='warm', rate=25, seconds=10)
        self.store = module.EngineStore(self.config, self.root / 'warm-journal.ndjson', clock=lambda: self.stamp)
        self.assertEqual((self.store.summary()['launchGuardMs'], self.store.summary()['counts']['planned']), (0, 250))

    def test_guard_ordinals_cannot_be_reported_as_native_requests_or_drops(self):
        self.ready(); self.stamp = self.start + 60000
        # The old final five ordinals are no longer offered. Neither a response
        # nor invented dropped records can fill this intentional guard interval.
        for kind in ('mutation', 'dropped'):
            self.stamp += 1
            row = self.record(2995, kind=kind)
            if kind == 'dropped':
                row = {key: row[key] for key in ('ordinal', 'slot', 'kind')}
                row.update(startedUnixMs=None, elapsedMs=None, status=None)
            with self.assertRaisesRegex(ValueError, 'ordinal ownership'):
                self.batch(1, records=[row])
        self.assertEqual(self.store.summary()['counts']['attempted'], 0)

    def test_old_unguarded_declared_count_still_rejects_complete(self):
        declared = self.full_window()
        declared[1]['planned'] += 5
        self.assertFalse(self.complete(1, declared[1])['complete'])
        self.assertFalse(self.store.summary()['collectionValid'])

    def test_ack_retry_is_idempotent_and_different_content_stops(self):
        self.ready(); self.stamp = self.start + 1000
        payload = {'runId': self.config['runId'], 'engineId': ENGINE_IDS[0], 'seq': 0,
                   'records': [self.record()], 'clock': self.clock(), 'resources': self.resources()}
        self.assertEqual(self.store.batch(payload)['ack'], 0)
        self.assertTrue(self.store.batch(payload)['duplicate'])
        self.assertEqual(self.store.summary()['counts']['mutation'], 1)
        changed = copy.deepcopy(payload); changed['records'][0]['elapsedMs'] += 1
        with self.assertRaises(ValueError):
            self.store.batch(changed)

    def test_missing_or_out_of_order_batch_has_no_ack(self):
        self.ready(); self.stamp = self.start
        with self.assertRaises(ValueError):
            self.batch(seq=1)
        self.assertTrue(self.store.summary()['batchFatal'])

    def test_fsync_failure_does_not_commit_in_memory_or_ack(self):
        self.ready(); self.stamp = self.start + 1000
        with patch.object(module.os, 'fsync', side_effect=OSError('synthetic write failure')):
            with self.assertRaisesRegex(ValueError, 'persistence'):
                self.batch(records=[self.record()])
        self.assertEqual(self.store.summary()['counts']['mutation'], 0)
        self.assertIsNone(self.store.summary()['unknown'])
        self.assertEqual(self.store._engines[ENGINE_IDS[0]]['batches'], {})

    def test_tampered_version_or_note_is_independently_rejected(self):
        self.ready(); self.stamp = self.start + 1000
        for changes in ({'observedVersion': 2}, {'observedLiked': False}, {'observedNoteId': '2001'}, {'previousVersion': 1}):
            with self.subTest(changes=changes), self.assertRaises(ValueError):
                self.batch(records=[self.record(**changes)])
        self.assertEqual(self.store.summary()['counts']['mutation'], 0)

    def test_missing_mutation_facts_or_private_extras_rejected_without_logging(self):
        self.ready(); self.stamp = self.start + 1000
        row = self.record(); del row['wanted']
        with self.assertRaises(ValueError):
            self.batch(records=[row])
        with self.assertRaises(ValueError):
            self.batch(records=[self.record(token='PRIVATE-FORBIDDEN-RECORD')])
        self.assertNotIn('PRIVATE-FORBIDDEN-RECORD', (self.root / 'journal.ndjson').read_text())

    def test_relationship_overlap_and_partial_bad_batch_roll_back(self):
        self.ready(); self.stamp = self.start + 11500
        with self.assertRaisesRegex(ValueError, 'overlap'):
            self.batch(records=[self.record(0, elapsedMs=11000), self.record(512)])
        self.assertEqual(self.store.summary()['counts']['mutation'], 0)

    def test_uncertain_relation_cannot_resume_inside_same_phase(self):
        self.ready(); self.stamp = self.start + 1000
        self.batch(records=[self.record(kind='uncertain', status=0)])
        self.stamp = self.start + 11000
        with self.assertRaisesRegex(ValueError, 'retired'):
            self.batch(records=[self.record(512, previousVersion=0, observedVersion=1, wanted=True, observedLiked=True)])

    def test_future_terminal_is_not_a_completed_http_sample(self):
        self.ready(); self.stamp = self.start
        with self.assertRaises(ValueError):
            self.batch(records=[self.record(elapsedMs=200)])

    def test_late_or_pre_window_http_keeps_complete_frame_and_fact_chain(self):
        self.ready(); self.stamp = self.start + 1000
        rows = [self.record(0, startedUnixMs=self.start - 1),
                self.record(1, startedUnixMs=self.start + 71)]
        result = self.batch(records=rows)
        self.assertEqual(result['ack'], 0)
        self.assertFalse(result['stop'])
        self.assertTrue(self.batch(records=rows, seq=0)['duplicate'])
        summary = self.store.summary()
        self.assertEqual(summary['counts']['mutation'], 2)
        self.assertEqual(summary['generatorTimingViolations'], 2)
        self.assertEqual([r['dispatchOffsetMs'] for r in summary['generatorTimingViolationDetails']], [-1, 51])
        self.assertFalse(summary['generatorTimingValid'] or summary['batchFatal'])
        self.assertEqual(summary['collectionErrors'], [])
        self.stamp = self.start + 11000
        self.batch(records=[self.record(512)])
        self.assertEqual(self.store._facts[0], (False, 2))
        frames = [json.loads(line) for line in (self.root / 'journal.ndjson').read_text().splitlines()]
        native = [row for frame in frames if frame['kind'] == 'batch' for row in frame['records']]
        self.assertEqual([row['ordinal'] for row in native], [0, 1, 512])
        self.assertEqual(native[:2], rows)

    def test_full_timing_invalid_window_retains_all_real_terminals_and_exact_unknown(self):
        declared = self.full_window(mutate=lambda row: row.update(startedUnixMs=row['startedUnixMs'] + 51))
        for engine in (0, 1):
            self.assertTrue(self.complete(engine, declared[engine])['complete'])
        summary = self.store.summary()
        self.assertTrue(summary['complete'] and summary['collectionValid'])
        self.assertEqual((summary['counts']['mutation'], summary['latencySampleCount']), (2995, 2995))
        self.assertEqual((summary['unknown'], summary['unknownIsExact']), (0, True))
        self.assertEqual(summary['generatorTimingViolations'], 2995)
        self.assertEqual(len(summary['generatorTimingViolationDetails']), 64)
        self.assertTrue(all(row['dispatchOffsetMs'] == 51 for row in summary['generatorTimingViolationDetails']))
        self.assertFalse(summary['generatorTimingValid'] or summary['batchFatal'] or summary['stop'])
        frames = [json.loads(line) for line in (self.root / 'journal.ndjson').read_text().splitlines()]
        native = [row for frame in frames if frame['kind'] == 'batch' for row in frame['records']]
        self.assertEqual(len(native), 2995)
        self.assertLess(native[-1]['startedUnixMs'], self.start + 60000)

    def test_bad_fact_after_timing_violation_remains_fatal_and_rolls_back_new_counters(self):
        self.ready(); self.stamp = self.start + 1000
        with self.assertRaisesRegex(ValueError, 'mutation facts'):
            self.batch(records=[self.record(0, startedUnixMs=self.start + 51),
                                self.record(1, observedVersion=5)])
        summary = self.store.summary()
        self.assertTrue(summary['batchFatal'] and summary['stop'])
        self.assertEqual((summary['counts']['mutation'], summary['generatorTimingViolations']), (0, 0))
        self.assertEqual(self.store._facts[0], (False, 0))

    def test_future_drop_cannot_fill_unoffered_nominal_schedule(self):
        self.ready(); self.stamp = self.start
        row = {'ordinal': 0, 'slot': 0, 'kind': 'dropped', 'startedUnixMs': None, 'elapsedMs': None, 'status': None}
        with self.assertRaises(ValueError):
            self.batch(records=[row])

    def test_early_stop_is_accepted_but_never_complete_or_zero_unknown(self):
        self.ready(); self.stamp = self.start
        for engine in (0, 1):
            result = self.complete(engine)
            self.assertTrue(result['accepted'])
            self.assertFalse(result['complete'])
        summary = self.store.summary()
        self.assertFalse(summary['complete'] or summary['collectionValid'] or summary['unknownIsExact'])
        self.assertIsNone(summary['unknown'])
        self.assertEqual(summary['notOffered'], 2995)
        self.assertEqual(summary['counts']['dropped'], 0)
        self.assertTrue(summary['childrenConverged'] and summary['batchFatal'])

    def test_missing_terminal_counter_cannot_be_hidden_by_declared_counts(self):
        self.ready(); self.stamp = self.start + 1000
        self.batch(records=[self.record()])
        result = self.complete(counts=self.declared(attempted=2, completed=2, mutation=2))
        self.assertTrue(result['accepted'])
        self.assertFalse(result['complete'])
        self.assertIsNone(self.store.summary()['unknown'])

    def test_invalid_http_with_null_optional_facts_is_retained_as_known_terminal(self):
        self.ready(); self.stamp = self.start + 1000
        self.batch(records=[self.record(kind='invalid', observedLiked=None, observedVersion=None, observedNoteId=None)])
        summary = self.store.summary()
        self.assertEqual((summary['counts']['attempted'], summary['counts']['completed'], summary['counts']['invalid']), (1, 1, 1))
        self.assertTrue(summary['stop'] and summary['batchFatal'])

    def test_uncertain_response_remains_unknown_and_in_all_http_quantile(self):
        def alter(row):
            if row['ordinal'] == 2994:
                row.update(kind='uncertain', status=0, elapsedMs=500)
        declared = self.full_window(mutate=alter)
        for engine in (0, 1):
            self.complete(engine, declared[engine])
        summary = self.store.summary()
        self.assertEqual((summary['unknown'], summary['effectUnknown']), (1, 1))
        self.assertTrue(summary['stop'] and summary['batchFatal'])
        self.assertEqual(summary['allLatency']['max'], 500)
        self.assertEqual(summary['mutationLatency']['max'], 250)

    def test_heartbeat_latch_survives_later_batch(self):
        self.ready(); self.stamp = self.start + 15000
        self.assertTrue(self.store.tick()['stop'])
        self.batch()
        self.assertTrue(self.store.control()['stop'])
        self.assertFalse(self.store.summary()['collectionValid'])

    def test_register_and_duplicate_ack_cannot_refresh_resource_evidence(self):
        self.ready(); self.stamp = self.start
        self.batch(0); self.batch(1)
        self.stamp += 5000
        payload = {'runId': self.config['runId'], 'engineId': ENGINE_IDS[0], 'seq': 1,
                   'records': [], 'clock': self.clock(), 'resources': self.resources()}
        self.store.batch(payload); self.batch(1)
        resource_stamp = self.stamp
        self.stamp = self.start + 18000
        self.register(0); self.store.batch(payload)
        engine = next(e for e in self.store.summary()['engines'] if e['engineId'] == ENGINE_IDS[0])
        self.assertEqual(engine['lastSeenUnixMs'], self.stamp)
        self.assertEqual(engine['lastResourceUnixMs'], resource_stamp)
        self.stamp = self.start + 20000
        self.assertTrue(self.store.tick()['stop'])
        self.assertIn('collector engine resource observation missing', self.store.summary()['collectionErrors'])

    def test_complete_requires_a_real_valid_cpu_delta_not_one_baseline(self):
        self.store.close()
        self.config.update(phase='warm', seconds=10, rate=1)
        self.store = module.EngineStore(self.config, self.root / 'warm.ndjson', clock=lambda: self.stamp)
        self.ready(); self.stamp = self.start + 10000
        self.batch(0, records=[self.record(i) for i in range(10)])
        self.batch(1)
        self.assertFalse(self.complete(0, self.declared(0, attempted=10, completed=10, mutation=10))['complete'])
        self.assertFalse(self.complete(1)['complete'])
        self.assertFalse(self.store.summary()['collectionValid'])

    def test_compact_records_and_cached_statistics_preserve_full_raw_quantiles(self):
        self.ready(); self.stamp = self.start + 1000
        self.batch(records=[self.record(0), self.record(1)])
        with patch.object(module, 'quantiles', wraps=module.quantiles) as calculated:
            first = self.store.summary()
            self.store.summary()
            self.assertEqual(calculated.call_count, 2)
            self.stamp += 1000
            self.batch(records=[self.record(2)])
            result = self.store.summary()
            self.assertEqual(calculated.call_count, 4)
        self.assertTrue(all(isinstance(row, tuple) and len(row) == 4 for row in self.store._records.values()))
        self.assertEqual((result['counts']['mutation'], result['allLatency']['p(95)']), (3, 2.9))
        self.assertEqual(result['allLatency'], module.quantiles([1, 2, 3]))
        result['counts']['mutation'] = 99
        self.assertEqual(self.store.summary()['counts']['mutation'], 3)
        self.assertIn('"observedVersion":1', (self.root / 'journal.ndjson').read_text())

    def test_cpu_streak_and_real_rss_are_guarded(self):
        self.ready()
        for i in range(4):
            self.stamp = self.start + i * 5000
            self.batch(0, resources=self.resources(cpuTimeNs=1000000000 + i * 8600000000))
            self.batch(1)
        self.assertIn('collector engine CPU safety limit', self.store.summary()['violations'])
        self.stamp += 5000
        self.batch(0, resources=self.resources(cpuTimeNs=1000000000 + 4 * 8600000000, rssBytes=module.RSS_LIMIT))
        self.assertIn('collector engine RSS safety limit', self.store.summary()['violations'])

    def test_remote_rss_limit_is_per_engine_not_two_host_aggregate(self):
        self.ready(); self.stamp = self.start
        for engine in (0, 1):
            self.batch(engine, resources=self.resources(rssBytes=3400000000))
        summary = self.store.summary()
        self.assertEqual(summary['remoteRSSBytes'], 6800000000)
        self.assertEqual(summary['remoteEngineRSSLimitBytes'], 6 << 30)
        self.assertFalse(summary['stop'])
        self.assertIn('each independently allocated remote engine', summary['resourcesScope'])
        self.stamp += 5000
        self.batch(0, resources=self.resources(rssBytes=module.RSS_LIMIT - 1))
        self.assertFalse(self.store.control()['stop'])
        self.stamp += 5000
        self.batch(0, resources=self.resources(rssBytes=module.RSS_LIMIT))
        self.assertTrue(self.store.control()['stop'])

    def test_bad_clock_or_resource_time_cannot_be_treated_as_zero(self):
        self.ready(); self.stamp = self.start
        for clock in (self.clock(uncertaintyMs=51), self.clock(uncertaintyMs=0), self.clock(offsetMs=float('nan'))):
            with self.subTest(clock=clock), self.assertRaises(ValueError):
                self.batch(clock=clock)
        with self.assertRaises(ValueError):
            self.batch(resources={'cpuTimeNs': 0, 'monotonicNs': 0, 'logicalCPUs': 2})

    def test_interpolated_quantiles_are_not_engine_percentile_averages(self):
        stats = module.quantiles([10, 110])
        self.assertEqual((stats['med'], stats['p(95)'], stats['p(99)']), (60, 105, 109))

    def test_dedicated_clock_is_identity_bound_and_has_no_evidence_side_effect(self):
        self.store.close()
        self.config['clockProtocol'] = 'dedicated-v1'
        self.store = module.EngineStore(self.config, self.root / 'clock.ndjson', clock=lambda: self.stamp)
        before = (self.root / 'clock.ndjson').read_bytes()
        with patch.object(self.store, 'tick', side_effect=AssertionError('clock must not tick')):
            value = self.store.clock(self.config['runId'])
        self.assertEqual(value, {'version': 1, 'clockProtocol': 'dedicated-v1', 'runId': 'synthetic-run',
                                'phase': 'stable', 'serverUnixMs': self.stamp})
        value['runId'] = 'foreign'
        self.assertEqual(self.store.clock(self.config['runId'])['runId'], 'synthetic-run')
        for wrong in (None, '', 'foreign'):
            with self.assertRaises(ValueError): self.store.clock(wrong)
        self.assertEqual((self.root / 'clock.ndjson').read_bytes(), before)
        self.assertEqual(self.store.control()['clockProtocol'], 'dedicated-v1')

    def test_clock_missing_is_legacy_and_invalid_modes_fail_before_journal_creation(self):
        self.assertEqual(module.clock_protocol({}), 'embedded-v1')
        self.assertNotIn('clockProtocol', self.store.control())
        with self.assertRaises(ValueError): self.store.clock(self.config['runId'])
        path = self.root / 'invalid-clock.ndjson'
        for wrong in (None, '', 'unknown', True):
            with self.assertRaises(ValueError):
                module.EngineStore({**self.config, 'clockProtocol': wrong}, path)
        self.assertFalse(path.exists())

    def test_progress_never_computes_percentiles_and_counts_every_durable_terminal(self):
        self.ready(); self.stamp = self.start + 1000
        rows = [self.record(0), self.record(1, kind='invalid', status=503),
                {'ordinal': 2, 'slot': 2, 'kind': 'dropped', 'startedUnixMs': None, 'elapsedMs': None, 'status': None}]
        self.batch(records=rows)
        with patch.object(module, 'quantiles', side_effect=AssertionError('progress must not sort latency')):
            progress = self.store.progress()
        self.assertIsNone(progress['allLatency']); self.assertIsNone(progress['mutationLatency'])
        self.assertIs(progress['latencyComplete'], False)
        self.assertIs(progress['latencyStatisticsComputed'], False)
        self.assertEqual((progress['counts']['attempted'], progress['counts']['completed'], progress['counts']['mutation'],
                          progress['counts']['invalid'], progress['counts']['dropped'], progress['latencySampleCount'],
                          progress['httpErrors'], progress['systemErrors']), (2, 2, 1, 1, 1, 2, 1, 1))
        final = self.store.summary()
        self.assertTrue(final['latencyStatisticsComputed'])
        self.assertEqual(final['allLatency'], module.quantiles([1, 2]))
        self.assertEqual(progress['counts'], final['counts'])
        by_engine = self.store._raw_counts(ENGINE_IDS[0])
        self.assertEqual({key: value for key, value in progress['counts'].items() if key != 'planned'},
                         {key: value for key, value in by_engine.items() if key != 'planned'})

    def test_durable_counters_ignore_duplicate_frames_and_rejected_or_unsynced_prefixes(self):
        self.ready(); self.stamp = self.start + 1000
        first = [self.record()]
        self.batch(records=first, seq=0)
        self.batch(records=first, seq=0)
        self.assertEqual(self.store.progress()['counts']['mutation'], 1)
        with self.assertRaises(ValueError):
            self.batch(records=[self.record(1), self.record(2, observedVersion=999)], seq=1)
        self.assertEqual(self.store.progress()['counts']['mutation'], 1)
        self.assertEqual(self.store._raw_counts(ENGINE_IDS[0])['mutation'], 1)

    def test_progress_keeps_latest_invalid_and_previous_valid_cpu_without_history_copy(self):
        self.ready(); self.stamp = self.start + 1000
        self.batch()
        self.stamp += 1000; self.batch()
        valid = copy.deepcopy(self.store._engines[ENGINE_IDS[0]]['cpuSamples'][-1])
        self.assertTrue(valid['intervalValid'])
        for unused in range(50):
            self.stamp += 10; self.batch()
        before = self.store.summary()
        engine = before['engines'][0]
        self.assertTrue(engine['cpuSamplesComplete'])
        self.assertEqual(len(engine['cpuSamples']), 52)
        live = self.store.progress()['engines'][0]
        self.assertIs(live['cpuSamplesComplete'], False)
        self.assertEqual(live['cpuSamples'], [valid, engine['cpuSamples'][-1]])
        self.assertFalse(live['cpuSamples'][-1]['intervalValid'])
        self.assertEqual(live['resources'], engine['resources'])
        self.assertEqual(live['lastResourceUnixMs'], engine['lastResourceUnixMs'])
        self.assertEqual(live['lastValidCPUUnixMs'], engine['lastValidCPUUnixMs'])
        live['cpuSamples'][0]['raw']['rssBytes'] = 1
        live['cpuSamples'][-1]['raw']['monotonicNs'] = 0
        self.assertEqual(self.store.summary(), before)

    def test_progress_does_not_invent_valid_cpu_intervals(self):
        self.ready(); self.stamp = self.start + 1000
        self.batch()
        self.stamp += 10; self.batch()
        live = self.store.progress()['engines'][0]
        self.assertEqual(len(live['cpuSamples']), 1)
        self.assertFalse(live['cpuSamples'][0]['intervalValid'])
        self.assertIsNone(live['lastValidCPUUnixMs'])
        self.assertEqual(len(self.store.summary()['engines'][0]['cpuSamples']), 2)

    def test_unsynced_frame_cannot_update_progress_counters(self):
        self.ready(); self.stamp = self.start + 1000
        with patch.object(module.os, 'fsync', side_effect=OSError('synthetic sync failure')):
            with self.assertRaises(ValueError):
                self.batch(records=[self.record()])
        self.assertEqual(self.store.progress()['counts']['mutation'], 0)
        self.assertEqual(self.store._raw_counts(ENGINE_IDS[0])['mutation'], 0)
        self.assertEqual(len(self.store._records), 0)

    def test_full_r7_preserves_high_cardinality_fractional_samples_after_progress(self):
        self.ready(); self.stamp = self.start + 5000
        values = [((ordinal * 173) % 997) / 997 + 1 for ordinal in range(200)]
        self.batch(records=[self.record(ordinal, elapsedMs=value) for ordinal, value in enumerate(values)])
        self.assertEqual(len(set(values)), len(values))
        with patch.object(module, 'quantiles', side_effect=AssertionError('progress must not round or sample')):
            progress = self.store.progress()
        result = self.store.summary()
        self.assertEqual(result['allLatency'], module.quantiles(values))
        self.assertEqual(result['mutationLatency'], module.quantiles(values))
        self.assertEqual(result['allLatency']['max'], max(values))
        self.assertEqual(progress['counts'], result['counts'])

    def test_acknowledged_scalar_records_do_not_alias_the_callers_mapping(self):
        self.ready(); self.stamp = self.start + 1000
        record = self.record()
        self.batch(records=[record])
        journal = (self.root / 'journal.ndjson').read_bytes()
        record.update(kind='invalid', observedVersion=999, observedNoteId='FOREIGN', elapsedMs=999)
        result = self.store.summary()
        self.assertEqual((result['counts']['mutation'], result['allLatency']['max']), (1, 1))
        self.assertEqual(self.store._facts[0], (True, 1))
        self.assertEqual((self.root / 'journal.ndjson').read_bytes(), journal)
        self.assertEqual(result['evidenceSHA256'], hashlib.sha256(journal).hexdigest())

    def test_nested_record_values_are_rejected_without_committing_a_prefix(self):
        for field in ('ordinal', 'slot', 'kind', 'startedUnixMs', 'wanted', 'observedLiked',
                      'previousVersion', 'observedVersion', 'observedNoteId', 'elapsedMs', 'status'):
            with self.subTest(field=field):
                self.store.close()
                self.store = module.EngineStore(self.config, self.root / ('nested-' + field + '.ndjson'), clock=lambda: self.stamp)
                self.ready(); self.stamp = self.start + 1000
                invalid = self.record(1)
                invalid[field] = {'mutable': []}
                with self.assertRaisesRegex(ValueError, 'JSON scalars'):
                    self.batch(records=[self.record(), invalid])
                self.assertEqual(self.store.progress()['counts']['attempted'], 0)
                self.assertEqual(len(self.store._records), 0)
                self.assertEqual(self.store._facts[0], (False, 0))

    def cohort(self, pool, engines):
        self.store.close()
        self.config.update(poolSize=pool, expectedEngines=engines,
                           ptsConcurrency=module.cohort.pts_concurrency(pool, engines), phase='warm', seconds=10, rate=1000)
        self.config['relations'] = [{'slot': i, 'userId': str(i + 1000), 'noteId': str(i + 2000),
            'active': 0, 'version': 0, 'token': 'PRIVATE-SESSION-SENTINEL-' + str(i)} for i in range(pool)]
        self.store = module.EngineStore(self.config, self.root / ('cohort-' + str(pool) + '.ndjson'), clock=lambda: self.stamp)
        return [str(uuid.UUID(int=i + 1)) for i in range(engines)], [list(range(i, pool, engines)) for i in range(engines)]

    def test_missing_pool_never_infers_a_large_fixture_and_legacy_identity_is_unchanged(self):
        self.assertNotIn('poolSize', self.store.control())
        self.assertNotIn('ptsConcurrency', self.store.control())
        config = copy.deepcopy(self.config)
        config['relations'] *= 8
        with self.assertRaises(ValueError):
            module.EngineStore(config, self.root / 'invalid-large.ndjson')
        for changes in ({'poolSize': True}, {'poolSize': 4097}, {'poolSize': 17},
                        {'poolSize': 512, 'expectedEngines': True}, {'ptsConcurrency': 514}):
            with self.subTest(changes=changes), self.assertRaises(ValueError):
                module.EngineStore({**self.config, **changes}, self.root / 'invalid-dimensions.ndjson')

    def test_nine_uneven_engines_need_all_4096_workers_before_barrier(self):
        ids, shards = self.cohort(4096, 9)
        self.assertEqual(sorted(map(len, shards)), [455] * 8 + [456])
        for index, (engine, slots) in enumerate(zip(ids, shards)):
            self.store.register({'runId': self.config['runId'], 'engineId': engine, 'slots': slots,
                'readyWorkers': len(slots) - (index == 8), 'scriptSHA256': 'b' * 64, 'fixtureSHA256': 'a' * 64})
        self.assertIsNone(self.store.control()['startUnixMs'])
        last = self.store.register({'runId': self.config['runId'], 'engineId': ids[-1], 'slots': shards[-1],
            'readyWorkers': len(shards[-1]), 'scriptSHA256': 'b' * 64, 'fixtureSHA256': 'a' * 64})
        self.assertEqual(last['startUnixMs'], self.stamp + 10000)
        self.assertEqual((last['poolSize'], last['expectedEngines'], last['ptsConcurrency']), (4096, 9, 4105))
        self.assertEqual(len(self.store._owners), 4096)
        self.assertEqual(sum(self.store._planned(slots) for slots in shards), 10000)
        self.assertNotIn('PRIVATE-', (self.root / 'cohort-4096.ndjson').read_text())
        self.start = last['startUnixMs']
        self.stamp = self.start + 1000
        wrong = self.record(512, slot=0)
        with self.assertRaisesRegex(ValueError, 'ordinal ownership'):
            self.store.batch({'runId': self.config['runId'], 'engineId': ids[0], 'seq': 0,
                'records': [wrong], 'clock': self.clock(), 'resources': self.resources()})

    def test_large_and_small_cohort_complete_exact_native_facts_and_quantiles(self):
        for pool, engines in ((4096, 9), (17, 3)):
            with self.subTest(pool=pool, engines=engines):
                ids, shards = self.cohort(pool, engines)
                for engine, slots in zip(ids, shards):
                    self.store.register({'runId': self.config['runId'], 'engineId': engine, 'slots': slots,
                        'readyWorkers': len(slots), 'scriptSHA256': 'b' * 64, 'fixtureSHA256': 'a' * 64})
                self.start = self.store.control()['startUnixMs']
                counts = [{'planned': self.store._planned(slots), 'attempted': 0, 'completed': 0,
                    **{kind: 0 for kind in module.KINDS}} for slots in shards]
                for seq, elapsed in enumerate((0, 5000, 10000)):
                    self.stamp = self.start + elapsed
                    records = [[] for _ in ids]
                    for ordinal in range(max(0, elapsed - 5000), elapsed):
                        slot, previous = ordinal % pool, ordinal // pool
                        wanted = not bool(previous % 2)
                        records[slot % engines].append({'ordinal': ordinal, 'slot': slot, 'kind': 'mutation',
                            'startedUnixMs': self.start + ordinal, 'elapsedMs': 1, 'status': 200,
                            'wanted': wanted, 'observedLiked': wanted, 'previousVersion': previous,
                            'observedVersion': previous + 1, 'observedNoteId': str(slot + 2000)})
                    for index, engine in enumerate(ids):
                        self.store.batch({'runId': self.config['runId'], 'engineId': engine, 'seq': seq,
                            'records': records[index], 'clock': self.clock(), 'resources': self.resources()})
                        for key in ('attempted', 'completed', 'mutation'):
                            counts[index][key] += len(records[index])
                for engine, expected in zip(ids, counts):
                    response = self.store.complete({'runId': self.config['runId'], 'engineId': engine,
                        'lastSeq': 2, 'counts': expected, 'clock': self.clock(), 'stoppedOffering': True, 'inFlight': 0})
                    self.assertTrue(response['complete'])
                result = self.store.summary()
                self.assertTrue(result['complete'] and result['collectionValid'] and result['childrenConverged'])
                self.assertEqual((result['counts']['planned'], result['counts']['mutation'], result['unknown'], result['generatorTimingViolations']), (10000, 10000, 0, 0))
                self.assertEqual((result['poolSize'], result['expectedEngines'], result['latencySampleCount']), (pool, engines, 10000))
                self.assertEqual(result['allLatency']['p(99)'], 1)
                self.assertEqual(result['endUnixMs'] - result['startUnixMs'], 10000)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output')
    args = parser.parse_args()
    before = hashlib.sha256(SOURCE.read_bytes()).hexdigest()
    result = unittest.TextTestRunner(verbosity=2).run(unittest.defaultTestLoader.loadTestsFromTestCase(Checks))
    record = {'scope': 'Synthetic in-process EngineStore only; no HTTP server, business, cloud or load.',
              'testsRun': result.testsRun, 'failures': len(result.failures), 'errors': len(result.errors),
              'passed': result.wasSuccessful(), 'sourceSHA256': before,
              'sourceBytesStable': before == hashlib.sha256(SOURCE.read_bytes()).hexdigest(),
              'checkerSHA256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest()}
    if args.output:
        path = Path(args.output).resolve()
        assert path.is_relative_to(REPO / 'docs/evidence/2026-11-01-cap10') and path.suffix == '.json'
        with path.open('x', encoding='utf-8') as stream:
            json.dump(record, stream, indent=2)
            stream.write('\n')
    print(json.dumps(record))
    raise SystemExit(not record['passed'] or not record['sourceBytesStable'])
