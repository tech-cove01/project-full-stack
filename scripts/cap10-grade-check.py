"""Synthetic CAP10 policy counterexamples; no services, SQL, HTTP or workload."""
import argparse
import datetime as dt
import hashlib
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest


REPO = Path(__file__).resolve().parent.parent
EVIDENCE = REPO / 'docs/evidence/2026-11-01-cap10'
GRADE_PATH = Path(__file__).with_name('cap10-grade.py')
spec = importlib.util.spec_from_file_location('cap10_policy_under_review', GRADE_PATH)
grade = importlib.util.module_from_spec(spec)
spec.loader.exec_module(grade)


class Checks(unittest.TestCase):
    def setUp(self):
        self.original_root = grade.ROOT
        self.root = Path(tempfile.mkdtemp(prefix='.runtime-cap10-grade-check-', dir=REPO.parent)).resolve()
        self.addCleanup(self.cleanup)
        grade.ROOT = self.root
        self.now = dt.datetime.now(dt.timezone.utc)
        self.source, self.other = 'a' * 40, 'b' * 40
        self.put('start.json', {'effectiveWorkStartConservativeUTC': self.now.isoformat()})
        self.put('environment.json', {'synthetic': 'owned environment'})
        self.fixture = self.put('fixture.json', {'synthetic': '512 exclusive relations'})
        self.binary = self.root / 'never-executed.bin'
        self.binary.write_bytes(b'pure synthetic binary; never executed')
        self.tools = {'synthetic-tool.py': 'd' * 64}
        self.observer = 'e' * 64
        self.identity = {
            'sourceSha': self.source, 'binarySHA256': grade.sha(self.binary),
            'environmentSHA256': grade.sha(self.root / 'environment.json'),
            'fixtureSHA256': grade.sha(self.fixture),
        }
        self.put('final-application.json', self.identity)

    def cleanup(self):
        grade.ROOT = self.original_root
        assert self.root.parent == REPO.parent and self.root.name.startswith('.runtime-cap10-grade-check-')
        for path in sorted(self.root.rglob('*'), key=lambda p: len(p.parts), reverse=True):
            assert path.resolve().is_relative_to(self.root)
            path.unlink() if path.is_file() else path.rmdir()
        self.root.rmdir()

    def put(self, relative, value):
        path = self.root / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(value), encoding='utf-8')
        return path

    def add(self, tag, rate, outcome='pass', kind=None, source=None, latency=(100, 250), **terminal):
        kind = kind or ('formal' if tag.startswith('H') else 'diagnostic' if tag == 'S1' else 'search')
        number = int(tag[1:]) + (20 if tag.startswith('H') else 0)
        command = {
            'task': 'CAP-10', 'tag': tag, 'kind': kind, 'rate': rate,
            'seconds': 180 if kind == 'formal' else 60,
            'at': (self.now + dt.timedelta(seconds=number)).isoformat(),
            'apiSourceSha': source or self.source,
            **{k: v for k, v in self.identity.items() if k != 'sourceSha'},
            'scriptHashes': self.tools.copy(), 'observerBinarySHA256': self.observer,
        }
        if kind == 'formal':
            command['freezeSHA256'] = grade.sha(self.root / 'freeze-like.json')
        state = {
            'outcome': outcome, 'passed': outcome == 'pass', 'boundaryEligible': outcome == 'performance_limited',
            'batchFatal': False, 'childrenConverged': True, 'recoveryVerified': True,
            'candidateMarginPassed': kind == 'candidate' and latency[0] <= 160 and latency[1] <= 400,
            **terminal,
        }
        self.put(tag + '-command.json', command)
        self.put(tag + '-stage.json', state)
        self.put(tag + '-load/result.json', {'result': {'allLatency': {'p(95)': latency[0], 'p(99)': latency[1]}}})
        return command

    def reserve(self, kind, rate, source=None, freeze=None, **overrides):
        source = source or self.source
        api = {
            'sourceSha': source, 'sha256': self.identity['binarySHA256'], 'exe': str(self.binary),
            'expectedVersion': 'capacity-' + source,
            'likeDiagnosticsRequested': kind in ('diagnostic', 'comparison'), 'tailEnabledRequested': False,
        }
        preflight = {'passed': True, 'environmentSHA256': self.identity['environmentSHA256']}
        ability = {'rate': rate, 'sufficient': True, 'maxVUs': 512, 'exclusiveRelationships': 512,
                   'classificationIncluded': True, 'evidence': ['synthetic measured iteration cost'], 'estimatedInFlight': 20}
        options = {'freeze': freeze, 'prediction': {'observedEvidence': ['synthetic old artifact'],
                   'differentPredictions': ['transaction', 'postcommit'], 'comparisonOf': 'S1',
                   'singleVariableEvidence': ['one synthetic production change']},
                   'current_tools': self.tools, 'observer_hash': self.observer}
        for name in ('api', 'preflight', 'ability'):
            target = {'api': api, 'preflight': preflight, 'ability': ability}[name]
            target.update(overrides.pop(name, {}))
        options.update(overrides)
        return grade.check_reservation(self.root, kind, rate, 180 if kind == 'formal' else 60,
                                       api, self.fixture, preflight, ability, **options)

    def bounds(self, source=None):
        return grade.search_state(self.root, source or self.source)

    def formal_ready(self):
        self.add('S1', 50)
        self.add('S2', 50, kind='candidate')
        frozen = {**self.identity, 'rate': 50, 'sourceCIPassed': True, 'relatedChecksPassed': True,
                  'initialDistributionContract': {'existing': 512, 'active': 0, 'relationships': 512},
                  'scriptHashes': self.tools.copy(), 'observerBinarySHA256': self.observer}
        self.put('freeze-like.json', frozen)
        return frozen

    def test_first_window_is_50_diagnostic_with_prediction(self):
        self.assertEqual(self.reserve('diagnostic', 50)['tag'], 'S1')
        for kind, rate in [('search', 50), ('diagnostic', 20), ('candidate', 50)]:
            with self.subTest(kind=kind, rate=rate), self.assertRaises(ValueError):
                self.reserve(kind, rate)
        with self.assertRaises(ValueError):
            self.reserve('diagnostic', 50, prediction=None)

    def test_passing_windows_expand_fourfold_not_one_small_step(self):
        self.add('S1', 50)
        self.assertEqual(self.bounds()['nextRate'], 200)
        self.assertEqual(self.reserve('search', 200)['tag'], 'S2')
        with self.assertRaises(ValueError):
            self.reserve('search', 100)
        self.add('S2', 200)
        self.assertEqual(self.bounds()['nextRate'], 800)

    def test_complete_performance_miss_can_halve_and_continue(self):
        self.add('S1', 50, 'performance_limited')
        self.assertEqual((self.bounds()['L'], self.bounds()['U'], self.bounds()['nextRate']), (None, 50, 25))
        self.assertEqual(self.reserve('search', 25)['tag'], 'S2')

    def test_first_rate_one_failure_ends_without_zero_rate(self):
        for i, rate in enumerate((50, 25, 12, 6, 3, 1), 1):
            self.add('S' + str(i), rate, 'performance_limited')
        self.assertTrue(self.bounds()['searchFinished'])
        self.assertIsNone(self.bounds()['nextRate'])
        with self.assertRaises(ValueError):
            self.reserve('search', 0)

    def test_midpoint_contract_and_quarter_width_stop(self):
        self.add('S1', 50)
        self.add('S2', 200, 'performance_limited')
        self.assertEqual(self.bounds()['nextRate'], 125)
        self.assertEqual(self.reserve('search', 125)['tag'], 'S3')
        self.add('S3', 125)
        self.assertEqual(self.bounds()['nextRate'], 162)
        self.add('S4', 162, 'performance_limited')
        self.assertEqual(self.bounds()['nextRate'], 143)
        self.add('S5', 143, 'performance_limited')
        self.assertTrue(self.bounds()['searchFinished'])

    def test_adjacent_integer_bounds_stop(self):
        self.add('S1', 50)
        self.add('S2', 51, 'performance_limited')
        self.assertTrue(self.bounds()['searchFinished'])

    def test_5000_is_only_observed_lower_bound(self):
        for i, rate in enumerate((50, 200, 800, 3200), 1):
            self.add('S' + str(i), rate)
        self.assertEqual(self.bounds()['nextRate'], 5000)
        self.add('S5', 5000)
        self.assertEqual(self.bounds()['L'], 5000)
        self.assertIsNone(self.bounds()['U'])
        self.assertTrue(self.bounds()['searchFinished'])

    def test_final_version_discards_old_bounds_and_requires_immediate_comparison(self):
        self.add('S1', 50, source=self.other)
        self.assertIsNone(self.bounds()['L'])
        with self.assertRaises(ValueError):
            self.reserve('search', 50)
        self.assertEqual(self.reserve('comparison', 50)['tag'], 'S2')
        self.add('S2', 50, 'performance_limited', kind='comparison')
        self.assertEqual((self.bounds()['L'], self.bounds()['nextRate']), (None, 25))
        with self.assertRaises(ValueError):
            self.reserve('comparison', 50, source=self.other)

    def test_later_source_change_cannot_start_second_optimization(self):
        self.add('S1', 50, source=self.other)
        self.add('S2', 50, kind='comparison')
        self.identity['sourceSha'] = 'c' * 40
        self.put('final-application.json', self.identity)
        with self.assertRaises(ValueError):
            self.reserve('search', 50, source='c' * 40)

    def test_rollback_must_cut_off_the_old_same_source_bound(self):
        self.add('S1', 50)
        self.add('S2', 50, 'performance_limited', kind='comparison', source=self.other)
        with self.assertRaises(ValueError):
            self.reserve('search', 50)
        self.put('final-application.json', {**self.identity, 'boundaryEvidenceAfter': 'S2',
                                          'rollbackEvidence': ['the only change was reverted without a second optimization']})
        self.assertIsNone(self.bounds()['L'])
        self.assertIsNone(self.bounds()['U'])
        self.assertEqual(self.bounds()['nextRate'], 50)
        self.assertEqual(self.reserve('search', 50)['tag'], 'S3')
        self.add('S3', 50)
        self.assertEqual(self.bounds()['L'], 50)

    def test_unknown_noop_and_unsettled_work_end_batch(self):
        for reason in ('unknown', 'unexpected_noop', 'version_mismatch'):
            with self.subTest(reason=reason):
                self.add('S1', 50, 'fatal', batchFatal=True, reason=reason)
                with self.assertRaises(ValueError):
                    self.reserve('search', 25)
        self.add('S1', 50, childrenConverged=False)
        with self.assertRaises(ValueError):
            self.reserve('search', 200)

    def test_measurement_generator_and_unrecovered_windows_are_not_service_bounds(self):
        for outcome in ('measurement_invalid', 'generator_limited', 'environment_invalid', 'failed_start'):
            with self.subTest(outcome=outcome):
                self.add('S1', 50, outcome)
                self.assertIsNone(self.bounds()['U'])
                with self.assertRaises(ValueError):
                    self.reserve('search', 25)
        self.add('S1', 50, 'performance_limited', recoveryVerified=False)
        with self.assertRaises(ValueError):
            self.reserve('search', 25)

    def test_failed_start_and_orphan_slot_cannot_be_reused(self):
        self.put('slot-S1.json', {'reservationOnly': True})
        with self.assertRaises(ValueError):
            self.reserve('diagnostic', 50)
        (self.root / 'slot-S1.json').unlink()
        self.add('S1', 50, 'failed_start')
        with self.assertRaises(ValueError):
            self.reserve('diagnostic', 50)
        with self.assertRaises(ValueError):
            self.reserve('search', 25)

    def test_eighth_short_slot_is_confirmation_not_more_search(self):
        for i, rate in enumerate((50, 200, 800, 3200, 2000, 1400, 1100), 1):
            self.add('S' + str(i), rate, 'pass' if i <= 3 else 'performance_limited')
        with self.assertRaises(ValueError):
            self.reserve('search', 950)
        reservation = self.reserve('candidate', 800)
        self.assertEqual((reservation['tag'], reservation['mainSecondsReservedAfter']), ('S8', 480))
        self.add('S8', 800, kind='candidate')
        with self.assertRaises(ValueError):
            self.reserve('candidate', 800)
        freeze = {**self.identity, 'rate': 800, 'sourceCIPassed': True, 'relatedChecksPassed': True,
                  'initialDistributionContract': {'existing': 512, 'active': 0, 'relationships': 512},
                  'scriptHashes': self.tools.copy(), 'observerBinarySHA256': self.observer}
        self.put('freeze-like.json', freeze)
        self.assertEqual(self.reserve('formal', 800, freeze=freeze)['mainSecondsReservedAfter'], 660)
        self.add('H1', 800)
        self.assertEqual(self.reserve('formal', 800, freeze=freeze)['mainSecondsReservedAfter'], 840)

    def test_no_lower_bound_cannot_gamble_candidate_slot(self):
        self.add('S1', 50, 'performance_limited')
        with self.assertRaises(ValueError):
            self.reserve('candidate', 20)

    def test_candidate_margin_uses_both_percentiles_and_eighty_percent_fallback(self):
        self.add('S1', 50, latency=(160, 400))
        self.assertEqual(self.bounds()['candidateRate'], 50)
        for latency in ((160.01, 400), (160, 400.01)):
            with self.subTest(latency=latency):
                self.add('S1', 50, latency=latency)
                self.assertEqual(self.bounds()['candidateRate'], 40)
                self.assertEqual(self.reserve('candidate', 40)['tag'], 'S2')
                with self.assertRaises(ValueError):
                    self.reserve('candidate', 50)

    def test_failed_candidate_never_gets_replacement_or_formal(self):
        freeze = self.formal_ready()
        self.add('S2', 50, 'performance_limited', kind='candidate', latency=(161, 350))
        with self.assertRaises(ValueError):
            self.reserve('candidate', 40)
        with self.assertRaises(ValueError):
            self.reserve('formal', 50, freeze=freeze)

    def test_environment_flags_and_generator_checks_fail_closed(self):
        for edits in ({'preflight': {'passed': False}}, {'preflight': {'environmentSHA256': 'f' * 64}},
                      {'ability': {'sufficient': False}}, {'ability': {'estimatedInFlight': 513}},
                      {'ability': {'classificationIncluded': False}}, {'api': {'likeDiagnosticsRequested': False}},
                      {'api': {'tailEnabledRequested': True}}):
            with self.subTest(edits=edits), self.assertRaises(ValueError):
                self.reserve('diagnostic', 50, **edits)
        self.put('start.json', {'effectiveWorkStartConservativeUTC': (self.now - dt.timedelta(seconds=10801)).isoformat()})
        with self.assertRaises(ValueError):
            self.reserve('diagnostic', 50)

    def test_formal_uses_same_frozen_candidate_and_no_third_round(self):
        freeze = self.formal_ready()
        self.assertEqual(self.reserve('formal', 50, freeze=freeze)['tag'], 'H1')
        self.add('H1', 50)
        self.assertEqual(self.reserve('formal', 50, freeze=freeze)['tag'], 'H2')
        for edits in ({'current_tools': {'different.py': 'f' * 64}}, {'observer_hash': 'f' * 64},
                      {'api': {'likeDiagnosticsRequested': True}}):
            with self.subTest(edits=edits), self.assertRaises(ValueError):
                self.reserve('formal', 50, freeze=freeze, **edits)
        self.add('H2', 50)
        with self.assertRaises(ValueError):
            self.reserve('formal', 50, freeze=freeze)

    def test_first_formal_failure_and_changed_freeze_end_formal_group(self):
        freeze = self.formal_ready()
        self.add('H1', 50, 'performance_limited')
        with self.assertRaises(ValueError):
            self.reserve('formal', 50, freeze=freeze)
        self.add('H1', 50)
        self.put('freeze-like.json', {**freeze, 'changedAfterH1': True})
        with self.assertRaises(ValueError):
            self.reserve('formal', 50, freeze=freeze)

    def test_formal_rate_identity_initial_distribution_and_margin_are_required(self):
        freeze = self.formal_ready()
        for key, value in (('rate', 40), ('sourceSha', self.other), ('environmentSHA256', 'f' * 64),
                           ('fixtureSHA256', 'f' * 64), ('sourceCIPassed', False),
                           ('initialDistributionContract', {'existing': 512, 'active': 1, 'relationships': 512})):
            with self.subTest(key=key), self.assertRaises(ValueError):
                self.reserve('formal', 50, freeze={**freeze, key: value})
        self.add('S2', 50, kind='candidate', candidateMarginPassed=False)
        with self.assertRaises(ValueError):
            self.reserve('formal', 50, freeze=freeze)

    def pipeline(self, mutate=None, seconds=60, summary=None):
        base = dt.datetime(2026, 11, 1, tzinfo=dt.timezone.utc)
        at = lambda value: (base + dt.timedelta(seconds=value)).isoformat()
        rows = []
        for t in range(-5, seconds + 71, 5):
            events = max(0, min(t, seconds)) * 100
            rows.append({'at': at(t), 'errors': [], 'semanticViolations': [],
                'sql': {'startedAt': at(t + .001), 'finishedAt': at(t + .01),
                    'counts': {'events': events, 'pending': 0, 'processing': 0, 'done': events, 'needsReview': 0, 'other': 0},
                    'parked': 0, 'receipts': [{'projection': g, 'missing': 0, 'applied': 0, 'ignored': events}
                        for g in ('follow-v1', 'feed-v1', 'search-v1')]},
                'kafka': {'startedAt': at(t + .02), 'finishedAt': at(t + .03),
                    'groups': [{'group': 'community.' + g, 'lag': 0, 'initialized': True}
                        for g in ('follow-v1', 'feed-v1', 'search-v1')]}})
        if mutate:
            mutate(rows, at)
        directory = self.root / 'synthetic-downstream'
        self.put('synthetic-downstream/summary.json', summary or {'valid': True, 'collectionValid': True, 'semanticViolationCount': 0})
        (directory / 'timeline.ndjson').write_text(''.join(json.dumps(r) + '\n' for r in rows), encoding='utf-8')
        started = int(base.timestamp() * 1000)
        report = {'stableWrapperStartedAt': started - 100, 'stableWrapperEndedAt': started + (seconds + 10) * 1000,
            'nativeScenario': {'startedAt': started, 'scheduledEndedAt': started + seconds * 1000,
                'durationMs': seconds * 1000, 'complete': True,
                'startMetric': {'min': started, 'max': started},
                'durationMetric': {'min': seconds * 1000, 'max': seconds * 1000}}}
        return grade.pipeline_grade(directory, report, seconds, 'like')

    @staticmethod
    def backlog(row, value):
        row['sql']['counts']['pending'] = value
        row['sql']['counts']['done'] = row['sql']['counts']['events'] - value
        for receipt in row['sql']['receipts']:
            receipt['missing'] = value
            receipt['ignored'] = row['sql']['counts']['events'] - value

    def test_full_and_tail15_queue_windows_are_independent(self):
        self.assertTrue(self.pipeline()['passed'])
        def tail_growth(rows, at):
            self.backlog(next(r for r in rows if r['at'] == at(60)), 20)
        result = self.pipeline(tail_growth)
        self.assertEqual(result['growthByQueue']['outbox'], 20)
        self.assertLess(20, result['maximumGrowthBudget'])
        self.assertFalse(result['tail15']['passed'])
        self.assertFalse(result['passed'])
        def earlier_growth(rows, at):
            for row in rows:
                if at(30) <= row['at'] <= at(60):
                    self.backlog(row, 60)
        result = self.pipeline(earlier_growth)
        self.assertTrue(result['tail15']['passed'])
        self.assertFalse(result['passed'])

    def test_sql_interval_crossing_native_end_cannot_enlarge_event_budget(self):
        def crossing(rows, at):
            row = next(r for r in rows if r['at'] == at(55))
            row['sql']['finishedAt'] = at(60.1)
            row['kafka'].update(startedAt=at(60.11), finishedAt=at(60.12))
        # Dropping the crossing endpoint leaves a ten-second evidence gap: it
        # must fail closed, rather than invent a denominator at native end.
        with self.assertRaises(ValueError):
            self.pipeline(crossing)
        def shifted_crossing(rows, at):
            for row in rows:
                row['at'] = (grade.instant(row['at']) + dt.timedelta(seconds=3)).isoformat()
                for kind in ('sql', 'kafka'):
                    for key in ('startedAt', 'finishedAt'):
                        row[kind][key] = (grade.instant(row[kind][key]) + dt.timedelta(seconds=3)).isoformat()
            row = next(r for r in rows if r['at'] == at(58))
            row['sql']['finishedAt'] = at(60.1)
            row['kafka'].update(startedAt=at(60.11), finishedAt=at(60.12))
        self.assertEqual(self.pipeline(shifted_crossing)['newEventsInConservativeSampledWindow'], 5000)

    def test_recovery_uses_completion_not_start_and_is_performance_not_collection(self):
        def recovery(rows, at):
            for row in rows:
                if at(60) <= row['at'] < at(120):
                    self.backlog(row, 1)
        result = self.pipeline(recovery, summary={'valid': False, 'collectionValid': True, 'semanticViolationCount': 0})
        self.assertFalse(result['passed'])
        self.assertGreater(result['recoverySecondsFromNativeEnd'], 60)

    def test_formal_backlog_budget_uses_last_minute_not_all_180_seconds(self):
        def early_queue(rows, at):
            self.backlog(next(r for r in rows if r['at'] == at(90)), 1000)
        result = self.pipeline(early_queue, seconds=180)
        self.assertTrue(result['passed'])
        self.assertEqual(result['newEventsInConservativeSampledWindow'], 5500)
        self.assertEqual(result['maximumGrowthBudget'], 55)
        self.assertIsNone(result['tail15'])

    def test_interior_gap_and_uninitialized_group_are_invalid_not_zero(self):
        def gap(rows, at):
            rows[:] = [r for r in rows if r['at'] != at(30)]
        with self.assertRaises(ValueError):
            self.pipeline(gap)
        def missing_offset(rows, at):
            rows[4]['kafka']['groups'][0]['initialized'] = False
        with self.assertRaises(ValueError):
            self.pipeline(missing_offset)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    output = args.output.resolve()
    assert output.parent == EVIDENCE and output.name.startswith('grade-check') and output.suffix == '.json'
    assert not output.exists(), 'preserve earlier pure-check results'
    before = hashlib.sha256(GRADE_PATH.read_bytes()).hexdigest()
    suite = unittest.defaultTestLoader.loadTestsFromTestCase(Checks)
    result = unittest.TextTestRunner(verbosity=2).run(suite)
    after = hashlib.sha256(GRADE_PATH.read_bytes()).hexdigest()
    record = {'task': 'CAP-10 independent synthetic policy checks', 'at': dt.datetime.now(dt.timezone.utc).isoformat(),
              'passed': result.wasSuccessful() and before == after, 'testsRun': result.testsRun,
              'failures': [{'check': str(test), 'traceback': detail} for test, detail in result.failures],
              'errors': [{'check': str(test), 'traceback': detail} for test, detail in result.errors],
              'gradeSHA256': before, 'gradeBytesStable': before == after,
              'checkerSHA256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
              'scope': 'Synthetic files in generated isolated workspace directories only; no real reservations, services, SQL, HTTP or load.'}
    with output.open('x', encoding='utf-8') as stream:
        json.dump(record, stream, ensure_ascii=False, indent=2)
    print(json.dumps({'passed': record['passed'], 'testsRun': record['testsRun'], 'failures': len(result.failures), 'errors': len(result.errors)}))
    raise SystemExit(0 if record['passed'] else 1)


if __name__ == '__main__':
    main()
