"""Pure CAP08 tool guard checks; no API, Docker, SQL, Redis or load is used."""
import contextlib
import datetime as dt
import importlib.util
import io
import json
from pathlib import Path
import sys
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch


REPO = Path(__file__).resolve().parent.parent
ROOT = REPO / 'docs/evidence/2026-10-30-cap08'
SHA = 'a' * 40


def module(name, filename):
    spec = importlib.util.spec_from_file_location(name, REPO / 'scripts' / filename)
    value = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    return value


policy = module('cap08_check_policy', 'cap08-policy.py')
collector = module('cap08_check_collector', 'cap08-collector.py')
stage = module('cap08_check_stage', 'cap08-stage.py')


def sample():
    return {'pid': 1234, 'sql': {'wait_count': 0, 'wait_duration_ns': 0},
            'redis': {'wait_count': 0, 'wait_duration_ns': 0}, 'tail': {'enabled': True, 'slow_traces': [], 'stages': [
                {'name': name, 'active': 0, 'tracked_active': 0, 'untracked_active': 0,
                 'oldest_active_ns': 0, 'oldest_complete': True, 'active_tracking_overflows': 0}
                for name in sorted(collector.STAGES)]}}


class Checks(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix='.pure-check-', dir=ROOT)
        self.root = Path(self.temporary.name).resolve()
        self.assertTrue(self.root.is_relative_to(ROOT.resolve()))
        self.addCleanup(self.temporary.cleanup)  # Only this newly created, verified child tree.
        self.write('budget-start.json', {'task': 'CAP-08', 'maximumDevelopmentSeconds': 14400,
                   'startedAt': (dt.datetime.now(dt.timezone.utc) - dt.timedelta(seconds=60)).isoformat()})
        self.evidence = self.write('evidence.json', {'synthetic': True})

    def write(self, name, value):
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(value), encoding='utf-8')
        return path

    def args(self, kind='diagnostic', **extra):
        args = dict(kind=kind, mode='multi', rate=100, seconds=180, source_sha=SHA,
                    prediction=self.root / 'prediction.json', gate=self.root / 'formal-gate.json')
        args.update(extra)
        return SimpleNamespace(**args)

    def prediction(self):
        return self.write('prediction.json', {'task': 'CAP-08', 'kind': 'diagnostic',
            'prediction': 'synthetic guard expectation', 'differentObservation': 'synthetic alternative',
            'evidence': [str(self.evidence)], 'wiringEvidence': str(self.evidence)})

    def record(self, tag, kind, passed=True, mode='multi', rate=100, seconds=180):
        command = {'task': 'CAP-08', 'kind': kind, 'tag': tag, 'apiSourceSha': SHA,
                   'mode': mode, 'rate': rate, 'seconds': seconds, 'at': policy.now()}
        self.write(tag + '-command.json', command)
        self.write(tag + '-load/result.json', {'meetsBudget': passed, 'result': {'attempted': 100, 'contentErrors': 0},
                   'warmExit': 0, 'stableExit': 0 if passed else 99})
        self.write(tag + '-load/stable-summary.json', {'synthetic': True})
        self.write(tag + '-stage-exit.json', {'capacityWrapperExit': 0 if passed else 1,
                   'cardsEvidenceValid': True, 'toolBytesStable': True, 'wrapperExited': True,
                   'failedStartOrException': None,
                   'extraDiagnosticCollectorExit': 0 if kind == 'diagnostic' else None})
        self.write(tag + '-control/exit-status.json', {'harness_exit': 0 if passed else 1,
                   'sampler_exit': 0, 'errors': [], 'stop_signal': False, 'forced': False})
        if kind == 'diagnostic':
            self.write(tag + '-diagnostic/manifest.json', {'api_source_sha': SHA, 'stable_observed': True,
                       'successful_polls': 180, 'poll_coverage_complete': True, 'stage_fields_valid': True,
                       'active_age_coverage_complete': True, 'slow_records_visibility_complete': True, 'errors': [], 'captures': []})
            (self.root / (tag + '-diagnostic/timeline.ndjson')).write_text('{}\n', encoding='utf-8')
        return command

    def gate(self, outcome='not-reproduced'):
        return self.write('formal-gate.json', {'task': 'CAP-08', 'kind': 'formal-gate', 'sourceSha': SHA,
            'at': policy.now(), 'workCompletedAt': (dt.datetime.now(dt.timezone.utc) - dt.timedelta(seconds=5)).isoformat(),
            'relatedChecksPassed': True, 'exactSourceCIPassed': True, 'provenIssuesHandled': True,
            'workCompletedWithinBudget': True, 'diagnosticOutcome': outcome, 'unresolvedReproducedFailure': False,
            'evidence': [str(self.evidence)]})

    def test_budget_and_two_diagnostic_slots(self):
        self.assertTrue(policy.budget(self.root)['withinBudget'])
        self.prediction()
        self.write('slot-controlled-1.json', {'synthetic': True})
        self.assertEqual(policy.stage_slot(self.args(), [], self.root), 'diagnostic-1')
        first = self.record('d1', 'diagnostic')
        with self.assertRaises(AssertionError):
            policy.stage_slot(self.args(), [first], self.root)
        prediction = policy.read(self.prediction())
        prediction['change'] = {'type': 'candidate', 'baselineTag': 'd1', 'description': 'one synthetic observed change', 'oneChangeOnly': True}
        self.write('prediction.json', prediction)
        self.assertEqual(policy.stage_slot(self.args(), [first], self.root), 'diagnostic-2')
        with self.assertRaises(AssertionError):
            policy.stage_slot(self.args(), [first, first], self.root)
        with self.assertRaises(AssertionError):
            policy.stage_slot(self.args(rate=101), [], self.root)
        expired = dt.datetime.now(dt.timezone.utc) + dt.timedelta(hours=5)
        self.assertFalse(policy.budget(self.root, expired)['withinBudget'])

    def test_formal_order_source_and_first_failure(self):
        diagnostic = self.record('d1', 'diagnostic')
        self.gate()
        formal = self.args('formal')
        self.assertEqual(policy.stage_slot(formal, [diagnostic], self.root), 'formal-1')
        first = self.record('f1', 'formal', passed=False)
        with self.assertRaises(AssertionError):
            policy.stage_slot(formal, [diagnostic, first], self.root)
        first = self.record('f1', 'formal')
        self.assertEqual(policy.stage_slot(formal, [diagnostic, first], self.root), 'formal-2')
        with self.assertRaises(AssertionError):
            policy.stage_slot(self.args('formal', mode='hot', rate=200), [diagnostic, first], self.root)
        second = self.record('f2', 'formal')
        self.assertEqual(policy.stage_slot(self.args('formal', mode='hot', rate=200), [diagnostic, first, second], self.root), 'formal-3')
        first['apiSourceSha'] = 'b' * 40
        with self.assertRaises(AssertionError):
            policy.stage_slot(formal, [diagnostic, first], self.root)

    def test_completed_stage_requires_normal_exit_without_late_interrupt(self):
        completed = self.record('complete', 'formal')
        limited = self.record('limited', 'stair', passed=False, rate=150, seconds=45)
        self.assertTrue(policy.passed(self.root, completed))
        self.assertTrue(policy.measured_slo_failure(self.root, limited))
        # A late KeyboardInterrupt can occur after tool hashes/counters are
        # complete but before the normal exit artifact is saved. The exception
        # artifact must not qualify a next formal round or a capacity bracket.
        for changes in ({'failedStartOrException': 'KeyboardInterrupt'}, {'wrapperExited': False}):
            for command, predicate in ((completed, policy.passed), (limited, policy.measured_slo_failure)):
                with self.subTest(changes=changes, predicate=predicate.__name__):
                    name = command['tag'] + '-stage-exit.json'
                    original = policy.read(self.root / name)
                    self.write(name, {**original, **changes})
                    try:
                        self.assertFalse(predicate(self.root, command))
                    finally:
                        self.write(name, original)

    def test_stage_without_explicit_exception_field_is_incomplete(self):
        # The writer explicitly records null on normal completion. A missing
        # terminal field is unknown, not evidence of a normal completion.
        for completed, predicate in ((True, policy.passed), (False, policy.measured_slo_failure)):
            with self.subTest(predicate=predicate.__name__):
                command = self.record('incomplete', 'formal', passed=completed)
                name = command['tag'] + '-stage-exit.json'
                incomplete = policy.read(self.root / name)
                del incomplete['failedStartOrException']
                self.write(name, incomplete)
                self.assertFalse(predicate(self.root, command))

    def test_missing_collection_is_not_non_reproduction_and_treated_needs_proof(self):
        self.record('d1', 'diagnostic')
        self.gate()
        manifest = policy.read(self.root / 'd1-diagnostic/manifest.json')
        manifest['active_age_coverage_complete'] = False
        self.write('d1-diagnostic/manifest.json', manifest)
        with self.assertRaises(AssertionError):
            policy.formal_gate(self.root / 'formal-gate.json', SHA, self.root)
        self.gate('treated')
        with self.assertRaises(KeyError):
            policy.formal_gate(self.root / 'formal-gate.json', SHA, self.root)
        configuration = self.write('configuration.json', {'synthetic_public_limits': True})
        proof = self.write('treatment.json', {'task': 'CAP-08', 'kind': 'treatment-proof', 'method': 'old-fail-new-pass',
            'mechanismObserved': True, 'mechanism': 'synthetic guard-only mechanism', 'siteEvidence': [str(self.evidence)],
            'baseline': {'sourceSha': 'b' * 40, 'configuration': str(configuration), 'result': 'FAIL', 'evidence': [str(self.evidence)]},
            'treated': {'sourceSha': SHA, 'configuration': str(configuration), 'result': 'PASS', 'evidence': [str(self.evidence)]}})
        gate = policy.read(self.root / 'formal-gate.json')
        gate['treatmentEvidence'] = str(proof)
        self.write('formal-gate.json', gate)
        self.assertEqual(policy.formal_gate(self.root / 'formal-gate.json', SHA, self.root)['diagnosticOutcome'], 'treated')
        gate['unresolvedReproducedFailure'] = True
        self.write('formal-gate.json', gate)
        with self.assertRaises(AssertionError):
            policy.formal_gate(self.root / 'formal-gate.json', SHA, self.root)

    def test_optional_native_failure_only_and_no_backfill(self):
        commands = [self.record('d1', 'diagnostic'), self.record('f1', 'formal'), self.record('f2', 'formal'),
                    self.record('f3', 'formal', mode='hot', rate=200)]
        self.gate()
        self.assertEqual(policy.stage_slot(self.args('stair', rate=150, seconds=45), commands, self.root), 'stair-1')
        failed = self.record('s1', 'stair', passed=False, rate=150, seconds=45)
        with self.assertRaises(AssertionError):
            policy.stage_slot(self.args('stair', rate=200, seconds=45), commands + [failed], self.root)
        self.assertEqual(policy.stage_slot(self.args('neighbor', rate=125, seconds=45), commands + [failed], self.root), 'neighbor-1')
        native = policy.read(self.root / 's1-load/result.json')
        native['meetsBudget'] = True
        self.write('s1-load/result.json', native)
        with self.assertRaises(AssertionError):
            policy.stage_slot(self.args('neighbor', rate=125, seconds=45), commands + [failed], self.root)
        self.record('s1', 'stair', passed=False, rate=150, seconds=45)
        control = policy.read(self.root / 's1-control/exit-status.json')
        control['stop_signal'] = True
        self.write('s1-control/exit-status.json', control)
        with self.assertRaises(AssertionError):
            policy.stage_slot(self.args('neighbor', rate=125, seconds=45), commands + [failed], self.root)

    def test_trigger_completeness_pool_and_coverage_boundaries(self):
        before, current = sample(), sample()
        item = next(stage for stage in current['tail']['stages'] if stage['name'] == 'cards.cold_sql')
        item.update(active=1, tracked_active=1, oldest_active_ns=100_000_000)
        reasons, incomplete = collector.triggers(before, current)
        self.assertEqual(reasons[0]['kind'], 'stage_active_age')
        self.assertEqual(incomplete, [])
        item.update(oldest_complete=False, untracked_active=1)
        reasons, incomplete = collector.triggers(before, current)
        self.assertFalse(reasons)
        self.assertEqual(incomplete, ['cards.cold_sql'])
        current['sql']['wait_count'] = 1
        self.assertEqual(collector.triggers(before, current)[0][0]['kind'], 'sql_global_pool_wait_delta')
        del item['oldest_active_ns']
        with self.assertRaises(AssertionError):
            collector.triggers(before, current)
        start = dt.datetime(2026, 10, 30, tzinfo=dt.timezone.utc)
        def at(seconds): return (start + dt.timedelta(seconds=seconds)).isoformat()
        self.assertFalse(collector.coverage([at(1)], start.timestamp() * 1000, at(180), 0)['complete'])
        self.assertTrue(collector.coverage([at(1), at(179)], start.timestamp() * 1000, at(180), 2500)['complete'])
        self.assertFalse(collector.coverage([at(1), at(179)], start.timestamp() * 1000, at(180), 2501)['complete'])
        self.assertFalse(collector.coverage([at(3), at(179)], start.timestamp() * 1000, at(180), 1000)['complete'])
        self.assertFalse(collector.coverage([], None, None, 0)['complete'])

    def test_fetch_failure_timings_redirect_rejection_and_profile_bound(self):
        class Failing:
            def open(self, *args, **kwargs): raise TimeoutError('synthetic')
        raw, observation = collector.fetch(Failing(), 'http://127.0.0.1:8351/debug/capacity', 100, .1)
        self.assertIsNone(raw)
        self.assertIsNone(observation['bytes'])
        self.assertEqual(observation['error'], 'TimeoutError')
        self.assertTrue(observation['started_at'] and observation['finished_at'])
        self.assertGreaterEqual(observation['elapsed_ms'], 0)
        self.assertIsNone(collector.NoRedirect().redirect_request(None, None, 302, '', None, 'https://example.invalid'))
        for invalid in ('http://localhost:1/debug/capacity', 'https://127.0.0.1:1/debug/capacity',
                        'http://127.0.0.1:1/debug/capacity?key=x', 'http://user@127.0.0.1:1/debug/capacity'):
            with self.assertRaises(AssertionError): collector.endpoint(invalid)
        self.run_collector_fake(oversize=True)

    def run_collector_fake(self, oversize=False):
        snapshot = sample()
        next(item for item in snapshot['tail']['stages'] if item['name'] == 'cards.cold_sql').update(active=1, tracked_active=1, oldest_active_ns=150_000_000)
        class Response:
            def __init__(self, raw): self.raw = raw
            def __enter__(self): return self
            def __exit__(self, *args): return False
            def getcode(self): return 200
            def read(self, maximum): return self.raw[:maximum]
        class Opener:
            def open(self, url, timeout):
                return Response(json.dumps(snapshot).encode() if '/capacity' in url else b'x' * ((1 << 20) + 1) if oversize else b'synthetic profile\n')
        output = self.root / ('over' if oversize else 'collector')
        argv = ['cap08-collector.py', '--url', 'http://127.0.0.1:32123/debug/capacity', '--output', str(output),
                '--max-seconds', '2', '--max-captures', '1', '--stop-after-capture', '--expected-pid', '1234']
        with patch.object(sys, 'argv', argv), patch.object(collector.urllib.request, 'build_opener', return_value=Opener()), contextlib.redirect_stdout(io.StringIO()):
            code = collector.main()
        manifest = policy.read(output / 'manifest.json')
        self.assertEqual(code, 1 if oversize else 0)
        self.assertEqual(len(manifest['captures']), 1)
        self.assertEqual(manifest['captures'][0]['truncated'], oversize)
        self.assertLessEqual((output / 'goroutine-1.txt').stat().st_size, 1 << 20)
        self.assertEqual(manifest['captures'][0]['reasons'][0]['name'], 'cards.cold_sql')
        return manifest

    def test_controlled_first_capture_returns_without_full_window(self):
        manifest = self.run_collector_fake()
        self.assertTrue(manifest['poll_coverage_complete'])
        self.assertEqual(manifest['samples'], 1)
        self.assertLess(manifest['elapsed_seconds'], 1)

    def test_slow_archive_bounds_and_parsed_subset_raw_hash(self):
        archive = collector.SlowRecords()
        current = sample()
        current['tail']['slow_traces'] = [
            {'sequence': index, 'scope': 'feed.http', 'start_at': 'synthetic', 'end_at': 'synthetic',
             'total_ns': index * 100_000_000, 'error': False, 'canceled': False, 'truncated': False, 'spans': []}
            for index in range(1, 41)]
        compact = archive.compact(current)
        self.assertNotIn('slow_traces', compact['tail'])
        self.assertEqual(compact['tail']['stages'], current['tail']['stages'])
        self.assertEqual(compact['tail']['visible_slow_sequences'], list(range(1, 41)))
        self.assertEqual(archive.manifest()['retained_sequences'], list(range(40, 8, -1)))
        self.assertEqual(len(archive.manifest()['retained']), 32)
        current['tail']['slow_traces'] = [{'total_ns': 5}]
        self.assertEqual(archive.compact(current)['tail']['slow_record_visibility_errors'], 1)
        self.assertFalse(archive.manifest()['visibility_complete'])
        raw = ('  ' + json.dumps(sample()) + '\n').encode()
        import hashlib
        with patch.object(stage.observations, 'fetch', return_value=(raw, {'available': True, 'truncated': False, 'sha256': hashlib.sha256(raw).hexdigest()})):
            snapshot = stage.capacity_snapshot(1234, True, self.root / 'slow-records.json')
        self.assertFalse(snapshot['rawResponseArchived'])
        self.assertEqual(snapshot['fetch']['sha256'], hashlib.sha256(raw).hexdigest())
        self.assertNotIn('slow_traces', snapshot['capacity']['tail'])

    def test_fixed_cohorts_reject_each_others_private_paths(self):
        lifecycle = module('cap08_check_lifecycle', 'cap06-env.py')
        def settings(cohort):
            private = REPO.parent / ('.runtime-fullstack-' + cohort + '-0123456789ab')
            value = {cohort.upper() + '_RUNTIME': str(private), cohort.upper() + '_EVIDENCE': str(REPO / ('docs/evidence/2026-10-30-' + cohort)),
                     'MYSQL_HOST': '127.0.0.1', 'REDIS_HOST': '127.0.0.1', 'MYSQL_PORT': '3451', 'REDIS_PORT': '6551',
                     'API_BASE': 'http://127.0.0.1:8251', 'ORDER_STREAM_WORKERS': '1'}
            return private / 'env.private.json', value
        for cohort in ('cap06', 'cap07', 'cap08'):
            lifecycle.configure_cohort(cohort)
            private, value = settings(cohort)
            with patch.object(lifecycle, 'read', return_value=value):
                self.assertEqual(lifecycle.load_settings(private)[0], value)
                wrong = settings('cap06' if cohort != 'cap06' else 'cap08')[0]
                with self.assertRaises(AssertionError): lifecycle.load_settings(wrong)
        with self.assertRaises(ValueError): lifecycle.configure_cohort('arbitrary-root')

    def test_genuine_startup_failure_keeps_reservation(self):
        self.prediction()
        self.write('slot-controlled-1.json', {'synthetic': True})
        metadata = self.write('api-synthetic.json', {'pid': 1234})
        argv = ['cap08-stage.py', '--environment', str(self.root / 'absent.private.json'), '--api-metadata', str(metadata),
                '--source-sha', SHA, '--tag', 'failed-start', '--kind', 'diagnostic', '--prediction', str(self.root / 'prediction.json')]
        with patch.object(sys, 'argv', argv), patch.object(stage, 'ROOT', self.root), patch.object(stage.owned, 'load_settings', side_effect=RuntimeError('synthetic')):
            with self.assertRaises(RuntimeError): stage.main()
        self.assertTrue((self.root / 'slot-diagnostic-1.json').is_file())
        self.assertTrue((self.root / 'failed-start-command.json').is_file())
        self.assertEqual(policy.read(self.root / 'failed-start-stage-exit.json')['failedStartOrException'], 'RuntimeError')


if __name__ == '__main__':
    unittest.main(verbosity=2)
