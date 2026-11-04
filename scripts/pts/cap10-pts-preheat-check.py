"""Preparation protocol checks only; no listener, JVM, business or cloud calls."""
import copy
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location('cap10_preheat_store', HERE / 'cap10-pts-collector.py')
collector = importlib.util.module_from_spec(spec)
spec.loader.exec_module(collector)
ENGINES = ['00000000-0000-4000-8000-000000000001', '00000000-0000-4000-8000-000000000002']


class PreparationChecks(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix='.qa-cap10-preheat-', dir=HERE.parents[2])
        self.addCleanup(self.tmp.cleanup)
        self.path = Path(self.tmp.name) / 'records.ndjson'
        self.stamp = 1800000000000
        self.config = {'runId': 'synthetic-preheat', 'phase': 'stable', 'rate': 1, 'seconds': 60,
            'controlToken': 'synthetic-only-control', 'apiUrl': 'http://127.0.0.1:8281',
            'fixtureSHA256': 'a' * 64, 'scriptSHA256': 'b' * 64, 'poolSize': 2, 'expectedEngines': 2,
            'preheatProtocol': collector.PREHEAT_PROTOCOL,
            'relations': [{'slot': i, 'userId': str(10 + i), 'noteId': str(20 + i),
                           'active': 0, 'version': 0, 'token': 'SYNTHETIC-TOKEN-' + str(i)} for i in range(2)]}
        self.store = collector.EngineStore(self.config, self.path, clock=lambda: self.stamp)
        self.addCleanup(self.store.close)

    def clock(self):
        return {'uncertaintyMs': 1, 'offsetMs': 0, 'roundTripMs': 1}

    def resources(self):
        elapsed = self.stamp - 1799999000000
        return {'cpuTimeNs': elapsed * 10000, 'monotonicNs': elapsed * 1000000,
                'rssBytes': 100000000, 'logicalCPUs': 2}

    def register(self, engine):
        return self.store.register({'runId': self.config['runId'], 'engineId': ENGINES[engine],
            'slots': [engine], 'readyWorkers': 1, 'scriptSHA256': self.config['scriptSHA256'],
            'fixtureSHA256': self.config['fixtureSHA256']})

    def bound(self):
        self.register(0)
        self.register(1)
        self.stamp = self.store.control()['preheatStartUnixMs'] + 100

    def record(self, slot=0, **changes):
        return {'ordinal': slot, 'slot': slot, 'kind': 'noop', 'startedUnixMs': self.stamp - 10,
                'elapsedMs': 2, 'status': 200, 'wanted': False, 'observedLiked': False,
                'previousVersion': 0, 'observedVersion': 0, 'observedNoteId': str(20 + slot),
                'observedLikes': 0, **changes}

    def frame(self, engine=0, records=None, seq=0):
        return {'runId': self.config['runId'], 'engineId': ENGINES[engine], 'seq': seq,
                'records': [self.record(engine)] if records is None else records,
                'clock': self.clock(), 'resources': self.resources()}

    def prepared(self):
        self.bound()
        self.store.preheat(self.frame(0))
        self.store.preheat(self.frame(1))
        return self.store.control()['startUnixMs']

    def full_native(self, late=False):
        start = self.prepared()
        pending = []
        for ordinal in range(collector.planned_total(1, 60)):
            slot, previous = ordinal % 2, ordinal // 2
            wanted = previous % 2 == 0
            pending.append({'ordinal': ordinal, 'slot': slot, 'kind': 'mutation',
                'startedUnixMs': start + ordinal * 1000 + (51 if late and ordinal == 0 else 0),
                'elapsedMs': 1, 'status': 200, 'wanted': wanted, 'observedLiked': wanted,
                'previousVersion': previous, 'observedVersion': previous + 1,
                'observedNoteId': str(20 + slot)})
        for elapsed in range(5000, 60001, 5000):
            self.stamp = start + elapsed
            ready = [row for row in pending if row['startedUnixMs'] + row['elapsedMs'] <= self.stamp]
            for engine in range(2):
                owner = self.store._engines[ENGINES[engine]]
                self.store.batch(self.frame(engine, [row for row in ready if row['slot'] == engine], len(owner['batches'])))
            pending = [row for row in pending if row not in ready]

    def complete(self, engine, **changes):
        owner = self.store._engines[ENGINES[engine]]
        return self.store.complete({'runId': self.config['runId'], 'engineId': ENGINES[engine],
            'lastSeq': len(owner['batches']) - 1, 'counts': self.store._raw_counts(ENGINES[engine]),
            'preparationCounts': self.store._preheat_raw_counts(ENGINES[engine]),
            'clock': self.clock(), 'stoppedOffering': True, 'inFlight': 0, **changes})

    def test_bound_threads_open_only_preparation_barrier(self):
        self.bound()
        self.assertIsNone(self.store.control()['startUnixMs'])
        self.assertEqual(self.store.control()['preheatRate'], 500)
        self.assertEqual(self.store.summary()['counts']['attempted'], 0)
        self.assertFalse(self.store.summary()['preparation']['passed'])

    def test_all_durable_noops_then_native_start_and_separate_counts(self):
        self.bound()
        self.store.preheat(self.frame(0))
        self.assertIsNone(self.store.control()['startUnixMs'])
        self.assertEqual(self.store._facts, {0: (False, 0), 1: (False, 0)})
        result = self.store.preheat(self.frame(1))
        self.assertEqual(result['startUnixMs'], self.stamp + 10000)
        summary = self.store.summary()
        self.assertEqual(summary['preparation']['counts']['noop'], 2)
        self.assertTrue(summary['preparation']['passed'])
        self.assertEqual(summary['counts']['noop'], 0)
        self.assertEqual(summary['counts']['attempted'], 0)
        self.assertIsNone(summary['allLatency'])
        kinds = [json.loads(line)['kind'] for line in self.path.read_text().splitlines()]
        self.assertLess(max(i for i, kind in enumerate(kinds) if kind == 'preheat'), kinds.index('barrier'))

    def test_duplicate_ack_is_idempotent_but_changed_frame_fails(self):
        self.bound()
        frame = self.frame()
        self.store.preheat(frame)
        self.assertTrue(self.store.preheat(frame)['duplicate'])
        self.assertEqual(self.store.summary()['preparation']['counts']['attempted'], 1)
        changed = copy.deepcopy(frame)
        changed['records'][0]['elapsedMs'] = 3
        with self.assertRaises(ValueError):
            self.store.preheat(changed)
        self.assertIsNone(self.store.control()['startUnixMs'])

    def test_bad_terminal_is_preserved_and_never_releases_native(self):
        self.bound()
        self.store.preheat(self.frame(records=[self.record(kind='uncertain', status=503,
            observedLiked=None, observedVersion=None, observedNoteId=None, observedLikes=None)]))
        summary = self.store.summary()
        self.assertTrue(summary['stop'])
        self.assertIsNone(summary['startUnixMs'])
        self.assertEqual(summary['preparation']['counts']['uncertain'], 1)
        self.assertEqual(summary['counts']['attempted'], 0)
        self.assertTrue(any(json.loads(line).get('kind') == 'preheat' for line in self.path.read_text().splitlines()))

    def test_wrong_response_note_version_state_count_or_mutation_blocks(self):
        variants = [{'observedNoteId': '999'}, {'observedVersion': 1}, {'observedLiked': True},
                    {'observedLikes': 1}, {'kind': 'mutation', 'observedVersion': 1, 'observedLiked': True}]
        self.bound()
        for index, changed in enumerate(variants):
            with self.subTest(changed=changed):
                store = collector.EngineStore(self.config, self.path.with_name('bad-' + str(index)), clock=lambda: self.stamp)
                old = self.store
                self.store = store
                try:
                    self.register(0)
                    self.register(1)
                    self.stamp = store.control()['preheatStartUnixMs'] + 100
                    store.preheat(self.frame(records=[self.record(**changed)]))
                    self.assertTrue(store.control()['stop'])
                    self.assertIsNone(store.control()['startUnixMs'])
                finally:
                    store.close()
                    self.store = old

    def test_wrong_owner_or_repeated_slot_is_rejected(self):
        self.bound()
        with self.assertRaises(ValueError):
            self.store.preheat(self.frame(records=[self.record(1)]))
        self.assertEqual(len(self.store._preheat_records), 0)

    def test_unpersisted_preparation_never_advances_counts_or_barrier(self):
        self.bound()
        with patch.object(collector.os, 'fsync', side_effect=OSError('synthetic durable failure')):
            with self.assertRaises(ValueError):
                self.store.preheat(self.frame())
        self.assertEqual(len(self.store._preheat_records), 0)
        self.assertIsNone(self.store.control()['startUnixMs'])

    def test_native_ordinal_zero_and_complete_window_are_unchanged(self):
        self.full_native()
        self.complete(0)
        self.complete(1)
        summary = self.store.summary()
        self.assertTrue(summary['complete'])
        self.assertTrue(summary['collectionValid'])
        self.assertEqual(summary['counts']['mutation'], 59)
        self.assertEqual(summary['latencySampleCount'], 59)
        self.assertEqual(summary['counts']['noop'], 0)
        self.assertEqual(summary['endUnixMs'] - summary['startUnixMs'], 60000)
        self.assertEqual(summary['preparation']['counts']['noop'], 2)

    def test_real_51ms_late_native_start_still_fails_generator(self):
        self.full_native(late=True)
        self.complete(0)
        self.complete(1)
        summary = self.store.summary()
        self.assertTrue(summary['collectionValid'])
        self.assertEqual(summary['generatorTimingViolations'], 1)
        self.assertFalse(summary['generatorTimingValid'])

    def test_completion_cannot_hide_extra_or_missing_preparation_attempt(self):
        self.full_native()
        counts = self.store._preheat_raw_counts(ENGINES[0])
        self.complete(0, preparationCounts={**counts, 'attempted': 2})
        self.assertFalse(self.store.summary()['collectionValid'])
        self.assertTrue(self.store.control()['stop'])

    def test_legacy_missing_protocol_keeps_original_single_barrier(self):
        config = {k: v for k, v in self.config.items() if k != 'preheatProtocol'}
        store = collector.EngineStore(config, self.path.with_name('legacy'), clock=lambda: self.stamp)
        old, self.store = self.store, store
        try:
            self.register(0)
            self.register(1)
            self.assertEqual(store.control()['startUnixMs'], self.stamp + 10000)
            self.assertNotIn('preheatProtocol', store.control())
            self.assertNotIn('preparation', store.summary())
            with self.assertRaises(ValueError):
                store.preheat(self.frame())
        finally:
            store.close()
            self.store = old

    def test_unknown_protocol_and_shared_note_reject_before_journal(self):
        for index, bad in enumerate([None, True, '', 'other']):
            path = self.path.with_name('invalid-' + str(index))
            with self.subTest(protocol=bad), self.assertRaises(ValueError):
                collector.EngineStore({**self.config, 'preheatProtocol': bad}, path)
            self.assertFalse(path.exists())
        config = copy.deepcopy(self.config)
        config['relations'][1]['noteId'] = config['relations'][0]['noteId']
        path = self.path.with_name('shared-note')
        with self.assertRaises(AssertionError):
            collector.EngineStore(config, path)
        self.assertFalse(path.exists())


if __name__ == '__main__':
    unittest.main()
