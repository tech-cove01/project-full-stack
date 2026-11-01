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
        planned = sum(1 for i in range(self.config['rate'] * self.config['seconds']) if engine * 256 <= i % 512 < (engine + 1) * 256)
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
        pending = [self.record(i) for i in range(self.config['rate'] * seconds)]
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
        self.assertEqual((summary['counts']['planned'], summary['counts']['mutation'], summary['notOffered']), (3000, 3000, 0))
        self.assertEqual((summary['unknown'], summary['unknownIsExact']), (0, True))
        self.assertEqual(summary['allLatency']['max'], 250)
        self.assertEqual(summary['latencySampleCount'], 3000)
        self.assertEqual(summary['evidenceSHA256'], hashlib.sha256((self.root / 'journal.ndjson').read_bytes()).hexdigest())

    def test_180_window_has_same_fixed_nominal_boundary(self):
        declared = self.full_window(seconds=180)
        for engine in (0, 1):
            self.complete(engine, declared[engine])
        summary = self.store.summary()
        self.assertTrue(summary['complete'])
        self.assertEqual(summary['counts']['mutation'], 9000)
        self.assertEqual(summary['endUnixMs'] - summary['startUnixMs'], 180000)

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

    def test_late_or_pre_window_http_not_relabelled_as_success(self):
        self.ready(); self.stamp = self.start + 1000
        for started in (self.start - 1, self.start + 51, self.start + 60000):
            with self.subTest(started=started), self.assertRaises(ValueError):
                self.batch(records=[self.record(startedUnixMs=started)])

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
        self.assertEqual(summary['notOffered'], 3000)
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
            if row['ordinal'] == 2999:
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
            self.batch(engine, resources=self.resources(rssBytes=1200000000))
        summary = self.store.summary()
        self.assertEqual(summary['remoteRSSBytes'], 2400000000)
        self.assertFalse(summary['stop'])
        self.assertIn('each independently allocated remote engine', summary['resourcesScope'])
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
