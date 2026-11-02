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

    def add(self, tag, rate, outcome='pass', kind=None, source=None, latency=(100, 250), execution_root=None, **terminal):
        execution_root = execution_root or self.root
        prefix = 'linux/' if execution_root == self.root / 'linux' else ''
        kind = kind or ('formal' if tag.startswith('H') else 'diagnostic' if tag == 'S1' else 'search')
        number = int(tag[1:]) + (20 if tag.startswith('H') else 0)
        command = {
            'task': 'CAP-10', 'tag': tag, 'kind': kind, 'rate': rate,
            'seconds': 180 if kind == 'formal' else 60,
            'at': (self.now + dt.timedelta(seconds=number)).isoformat(),
            'apiSourceSha': source or self.source,
            **{k: v for k, v in self.identity.items() if k != 'sourceSha'},
            'environmentSHA256': grade.sha(execution_root / 'environment.json'),
            'fixtureSHA256': grade.sha(execution_root / 'fixture.json'),
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
        self.put(prefix + tag + '-command.json', command)
        self.put(prefix + tag + '-stage.json', state)
        self.put(prefix + tag + '-load/result.json', {'result': {'allLatency': {'p(95)': latency[0], 'p(99)': latency[1]}}})
        return command

    def reserve(self, kind, rate, source=None, freeze=None, execution_root=None, **overrides):
        execution_root = execution_root or self.root
        source = source or self.source
        api = {
            'sourceSha': source, 'sha256': self.identity['binarySHA256'], 'exe': str(self.binary),
            'expectedVersion': 'capacity-' + source,
            'executionPlatform': 'windows',
            'likeDiagnosticsRequested': kind in ('diagnostic', 'comparison'), 'tailEnabledRequested': False,
        }
        preflight = {'passed': True, 'environmentSHA256': grade.sha(execution_root / 'environment.json')}
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
        return grade.check_reservation(execution_root, kind, rate, 180 if kind == 'formal' else 60,
                                       api, execution_root / 'fixture.json', preflight, ability, **options)

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

    def linux_transition(self, used=4200):
        anchor = self.now - dt.timedelta(seconds=600)
        self.put('idle-result.json', {'passed': False, 'synthetic': 'retained local timing failure'})
        self.put('cleanup.json', {'complete': True, 'synthetic': 'owned local cleanup'})
        self.put('raw-manifest.json', {'synthetic': 'retained local inventory'})
        self.put('budget-handoff.json', {
            'version': 1, 'batch': 'CAP-10', 'recordedAt': (anchor - dt.timedelta(seconds=60)).isoformat(),
            'maxEffectiveWorkSeconds': 10800, 'countedEffectiveWorkSecondsUpperBound': 4200,
            'remainingEffectiveWorkSecondsLowerBound': 6600,
        })
        transition = {
            'version': 1, 'batch': 'CAP-10', 'fromPlatform': 'windows', 'toPlatform': 'linux',
            'switchCount': 1, 'authorized': True, 'hostLabel': 'synthetic-linux',
            'userAuthorizationReference': 'synthetic explicit host approval', 'localCheckpointSHA': 'c' * 40,
            'recordedAt': anchor.isoformat(), 'effectiveWorkSecondsUsedBeforeSwitch': used,
            'effectiveWorkSecondsRemaining': 10800 - used, 'effectiveWorkSecondsMaximum': 10800,
            'shortSlotsUsedBeforeSwitch': 0, 'formalSlotsUsedBeforeSwitch': 0,
            'optimizationsUsedBeforeSwitch': 0, 'mainLoadSecondsUsedBeforeSwitch': 0,
            'maxShortWindows': 8, 'maxFormalWindows': 2, 'maxMainLoadSeconds': 840, 'maxOptimizations': 1,
        }
        for key, name in (('localEnvironmentSHA256', 'environment.json'), ('localIdleResultSHA256', 'idle-result.json'),
                          ('localCleanupSHA256', 'cleanup.json'), ('localRawManifestSHA256', 'raw-manifest.json'),
                          ('budgetHandoffSHA256', 'budget-handoff.json')):
            transition[key] = grade.sha(self.root / name)
        self.put('environment-transition.json', transition)
        self.put('linux/environment.json', {'synthetic': 'new Linux identity, same batch'})
        self.put('linux/fixture.json', {'synthetic': 'new Linux exclusive relations'})
        self.put('linux/final-application.json', {**self.identity,
            'environmentSHA256': grade.sha(self.root / 'linux/environment.json'),
            'fixtureSHA256': grade.sha(self.root / 'linux/fixture.json')})
        api = {'executionPlatform': 'linux', 'transitionSHA256': grade.sha(self.root / 'environment-transition.json')}
        return self.root / 'linux', transition, api, anchor

    def reseal_transition(self, transition, api):
        self.put('environment-transition.json', transition)
        return {**api, 'transitionSHA256': grade.sha(self.root / 'environment-transition.json')}

    def test_linux_inherits_checkpoint_and_counts_offline_adaptation(self):
        directory, transition, api, anchor = self.linux_transition(used=4500)
        budget = grade.effective_work_budget(directory, api, now=self.now)
        self.assertEqual(budget['effectiveSecondsUpperBound'], 5100)
        self.assertEqual(budget['usedBeforeSwitch'], 4500)
        self.assertEqual(budget['provedLoadWaitSecondsDeducted'], 0)
        with self.assertRaises(ValueError):
            grade.effective_work_budget(self.root, {'executionPlatform': 'windows'}, now=self.now)
        with self.assertRaises(ValueError):
            grade.effective_work_budget(directory, api, now=anchor - dt.timedelta(seconds=1))

    def test_linux_cannot_reset_handoff_even_when_resealed_consistently(self):
        directory, transition, api, anchor = self.linux_transition()
        changed = {**transition, 'effectiveWorkSecondsUsedBeforeSwitch': 4199, 'effectiveWorkSecondsRemaining': 6601}
        with self.assertRaises(ValueError):
            grade.effective_work_budget(directory, self.reseal_transition(changed, api), now=self.now)
        handoff = grade.read(self.root / 'budget-handoff.json')
        handoff.update(countedEffectiveWorkSecondsUpperBound=0, remainingEffectiveWorkSecondsLowerBound=10800)
        self.put('budget-handoff.json', handoff)
        changed.update(effectiveWorkSecondsUsedBeforeSwitch=0, effectiveWorkSecondsRemaining=10800,
                       budgetHandoffSHA256=grade.sha(self.root / 'budget-handoff.json'))
        with self.assertRaises(ValueError):
            grade.effective_work_budget(directory, self.reseal_transition(changed, api), now=self.now)

    def test_linux_budget_boundary_is_original_three_hours(self):
        directory, transition, api, anchor = self.linux_transition()
        self.assertEqual(grade.effective_work_budget(directory, api, now=anchor + dt.timedelta(seconds=6600))['effectiveSecondsUpperBound'], 10800)
        with self.assertRaises(ValueError):
            grade.effective_work_budget(directory, api, now=anchor + dt.timedelta(seconds=6601))

    def continuation(self):
        failed = self.put('linux/idle-attempt-1/idle-result.json', {'passed': False, 'samples': []})
        authorization = {'version': 1, 'batch': 'CAP-10', 'authorized': True, 'waiveEffectiveWorkTimeLimit': True,
            'originalMaximumSeconds': 10800, 'maxIdleRetries': 1,
            'userAuthorizationReference': 'synthetic explicit cancellation of time limit and one idle retry',
            'transitionSHA256': grade.sha(self.root / 'environment-transition.json'),
            'failedIdleResultPath': 'linux/idle-attempt-1/idle-result.json',
            'failedIdleResultSHA256': grade.sha(failed),
            'recordedAt': (self.now - dt.timedelta(seconds=1)).isoformat()}
        self.put('continuation-authorization.json', authorization)
        return authorization

    def test_explicit_time_waiver_retains_real_usage_and_original_limits(self):
        directory, transition, api, anchor = self.linux_transition(used=7800)
        self.continuation()
        budget = grade.effective_work_budget(directory, api, now=anchor + dt.timedelta(seconds=200000))
        self.assertEqual((budget['effectiveSecondsUpperBound'], budget['usedBeforeSwitch']), (207800, 7800))
        self.assertEqual(budget['anchor'], transition['recordedAt'])
        self.assertEqual(budget['originalMaximumSeconds'], 10800)
        self.assertIsNone(budget['maximumSeconds'])
        self.assertIsNone(budget['remainingSeconds'])
        self.assertTrue(budget['passed'] and budget['waiveEffectiveWorkTimeLimit'])
        json.dumps(budget, allow_nan=False)
        reservation = self.reserve('diagnostic', 50, execution_root=directory, api=api)
        self.assertEqual((reservation['shortMaximum'], reservation['formalMaximum'], reservation['mainSecondsMaximum']), (8, 2, 840))
        self.assertEqual(reservation['effectiveWorkBudget']['continuationAuthorizationSHA256'], grade.sha(self.root / 'continuation-authorization.json'))

    def test_time_waiver_requires_exact_authorization_and_retained_evidence(self):
        directory, transition, api, anchor = self.linux_transition()
        authorization = self.continuation()
        for key, value in (('version', True), ('batch', 'CAP-11'), ('authorized', False), ('authorized', 1), ('waiveEffectiveWorkTimeLimit', False),
                ('waiveEffectiveWorkTimeLimit', 1), ('maxIdleRetries', 2), ('maxIdleRetries', True),
                ('originalMaximumSeconds', 12000), ('userAuthorizationReference', ''),
                ('transitionSHA256', 'f' * 64), ('failedIdleResultSHA256', 'f' * 64),
                ('failedIdleResultPath', 'idle-result.json'), ('recordedAt', None),
                ('recordedAt', (self.now + dt.timedelta(seconds=1)).isoformat())):
            self.put('continuation-authorization.json', {**authorization, key: value})
            with self.subTest(key=key, value=value), self.assertRaises(ValueError):
                grade.effective_work_budget(directory, api, now=self.now)
        self.put('continuation-authorization.json', authorization)
        self.put('linux/idle-attempt-1/idle-result.json', {'passed': True, 'samples': []})
        with self.assertRaises(ValueError):
            grade.effective_work_budget(directory, api, now=self.now)

    def test_time_waiver_cannot_be_added_after_prior_slot(self):
        directory, transition, api, anchor = self.linux_transition()
        self.add('S1', 50, execution_root=directory)
        self.continuation()
        with self.assertRaises(ValueError):
            grade.effective_work_budget(directory, api, now=self.now)

    def test_time_waiver_hash_is_immutable_and_cannot_be_removed(self):
        directory, transition, api, anchor = self.linux_transition()
        authorization = self.continuation()
        budget = grade.effective_work_budget(directory, api, now=self.now)
        command = self.add('S1', 50, execution_root=directory)
        command['reservationCheck'] = {'effectiveWorkBudget': budget}
        self.put('linux/S1-command.json', command)
        self.put('linux/slot-S1.json', command)
        self.assertTrue(grade.effective_work_budget(directory, api, now=self.now)['passed'])
        self.put('continuation-authorization.json', {**authorization, 'userAuthorizationReference': 'changed later'})
        with self.assertRaises(ValueError):
            grade.effective_work_budget(directory, api, now=self.now)
        (self.root / 'continuation-authorization.json').unlink()
        with self.assertRaises(ValueError):
            grade.effective_work_budget(directory, api, now=self.now)

    def test_time_waiver_cannot_follow_orphan_reservation(self):
        directory, transition, api, anchor = self.linux_transition()
        self.put('linux/slot-S1.json', {'at': self.now.isoformat()})
        self.continuation()
        with self.assertRaises(ValueError):
            grade.effective_work_budget(directory, api, now=self.now)

    def test_linux_requires_explicit_single_switch_and_retained_bytes(self):
        directory, transition, api, anchor = self.linux_transition()
        for key, value in (('authorized', False), ('switchCount', 2), ('hostLabel', ''),
                           ('userAuthorizationReference', ''), ('localCheckpointSHA', 'invalid'),
                           ('maxShortWindows', 9), ('shortSlotsUsedBeforeSwitch', 1)):
            with self.subTest(key=key), self.assertRaises(ValueError):
                grade.effective_work_budget(directory, self.reseal_transition({**transition, key: value}, api), now=self.now)
        api = self.reseal_transition(transition, api)
        with self.assertRaises(ValueError):
            grade.effective_work_budget(directory, {**api, 'transitionSHA256': 'f' * 64}, now=self.now)
        self.put('idle-result.json', {'passed': True, 'synthetic': 'cannot rewrite the retained failure'})
        with self.assertRaises(ValueError):
            grade.effective_work_budget(directory, api, now=self.now)

    def test_wait_union_clips_and_never_double_deducts(self):
        start = self.now
        end = start + dt.timedelta(seconds=100)
        at = lambda seconds: (start + dt.timedelta(seconds=seconds)).isoformat()
        intervals = [(at(-50), at(30)), (at(20), at(70)), (at(90), at(150)), (at(110), at(120))]
        self.assertEqual(grade.merged_wait_seconds(intervals + intervals, start, end), 80)
        self.assertEqual(grade.merged_wait_seconds([(at(0), at(10)), (at(10), at(20))], start, end), 20)
        with self.assertRaises(ValueError):
            grade.merged_wait_seconds([(at(-10), at(-20))], start, end)

    def test_linux_wait_deduction_starts_at_transition_not_local_history(self):
        directory, transition, api, anchor = self.linux_transition()
        at = lambda seconds: (anchor + dt.timedelta(seconds=seconds)).isoformat()
        for i, (begin, finish) in enumerate(((-100, -10), (100, 200), (150, 300)), 1):
            self.add('S' + str(i), 50, execution_root=directory,
                     loadWaitStartedAt=at(begin), loadWaitFinishedAt=at(finish))
        self.add('S4', 50, execution_root=directory, loadWaitStartedAt=at(400))
        budget = grade.effective_work_budget(directory, api, now=self.now)
        self.assertEqual(budget['provedLoadWaitSecondsDeducted'], 200)
        self.assertEqual(budget['effectiveSecondsUpperBound'], 4600)
        self.add('S5', 50, execution_root=directory, loadWaitStartedAt=at(-100), loadWaitFinishedAt=at(900))
        budget = grade.effective_work_budget(directory, api, now=self.now)
        self.assertEqual(budget['provedLoadWaitSecondsDeducted'], 600)
        self.assertEqual(budget['effectiveSecondsUpperBound'], 4200)

    def test_linux_history_does_not_reissue_local_or_duplicate_slots(self):
        directory, transition, api, anchor = self.linux_transition()
        self.add('S1', 50, execution_root=directory)
        self.add('S2', 200, execution_root=directory)
        reservation = self.reserve('search', 800, execution_root=directory, api=api)
        self.assertEqual((reservation['tag'], reservation['shortSlotsUsedAfter']), ('S3', 3))
        self.assertEqual(reservation['mainSecondsReservedAfter'], 180)
        self.add('S1', 50)
        with self.assertRaises(ValueError):
            grade.history(directory)
        with self.assertRaises(ValueError):
            grade.effective_work_budget(directory, api, now=self.now)

    def test_terminal_artifact_is_unique_across_execution_directories(self):
        directory, transition, api, anchor = self.linux_transition()
        command = self.add('S1', 50, execution_root=directory)
        self.assertTrue(grade.stage(directory, command)['passed'])
        self.put('S1-stage.json', {'passed': True})
        with self.assertRaises(ValueError):
            grade.stage(directory, command)

    def zero_startup(self):
        directory, transition, api, anchor = self.linux_transition(used=7800)
        manifest = {'multiNoteIds': [str(i + 1) for i in range(512)], 'userIds': [str(i + 1001) for i in range(512)]}
        self.put('linux/fixture.json', manifest)
        self.put('linux/final-application.json', {**self.identity,
            'environmentSHA256': grade.sha(directory / 'environment.json'), 'fixtureSHA256': grade.sha(directory / 'fixture.json')})
        self.put('linux/pts-scene-admission.json', {'sceneId': 'OWNED_SCENE'})
        command = self.add('S1', 50, 'correctness_stop', execution_root=directory, batchFatal=True,
            childrenConverged=False, recoveryVerified=False, finishedAt=(self.now - dt.timedelta(seconds=3)).isoformat(),
            knownOwnedProcesses=[{'pid': 12345, 'created': 123.5}])
        command.update(loadBackend='pts', ptsAdmissionSHA256=grade.sha(directory / 'pts-scene-admission.json'),
            at=(self.now - dt.timedelta(seconds=20)).isoformat())
        self.put('linux/S1-command.json', command)
        counts = dict.fromkeys(('attempted', 'completed', 'mutation', 'noop', 'uncertain', 'rejected', 'invalid', 'dropped'), 0)
        identity = {'runId': 'ZERO_WARM', 'phase': 'warm', 'rate': 25, 'seconds': 10,
                    'fixtureSHA256': command['fixtureSHA256'], 'expectedEngines': 2}
        completions = [{'kind': 'complete', 'engineId': engine, 'accepted': True, 'stoppedOffering': True,
                        'inFlight': 0, 'counts': {**counts, 'planned': 125}, 'collectorRawCounts': {**counts, 'planned': 125}}
                       for engine in ('engine-1', 'engine-2')]
        journal = self.put('linux/recovery/records.ndjson', {})
        journal.write_text(''.join(json.dumps(row) + '\n' for row in [{'kind': 'configuration', 'configuration': identity}, *completions]), encoding='utf-8')
        collector = {**identity, 'startUnixMs': None, 'stop': True, 'childrenConverged': True,
                     'latencySampleCount': 0, 'counts': {**counts, 'planned': 250},
                     'evidenceSHA256': grade.sha(journal), 'unknown': None,
                     'engines': [{'engineId': row['engineId'], 'stoppedOffering': True, 'counts': row['counts']} for row in completions]}
        self.put('linux/recovery/collector-terminal.json', collector)
        at = lambda seconds: (self.now - dt.timedelta(seconds=seconds)).isoformat()
        self.put('linux/recovery/stop.json', {'action': 'StopTestingJMeterScene', 'httpStatus': 200,
            'sceneId': 'OWNED_SCENE', 'responseFinishedAt': at(12), 'sanitizedRawResponse': {'Success': True, 'Code': ''}})
        for name, start, finish in [('idle1', 11, 10), ('idle2', 8, 7)]:
            self.put('linux/recovery/' + name + '.json', {'action': 'GetJMeterSceneRunningData', 'httpStatus': 200,
                'sceneId': 'OWNED_SCENE', 'requestStartedAt': at(start), 'responseFinishedAt': at(finish),
                'sanitizedRawResponse': {'RequestId': name, 'Success': True}})
        self.put('linux/recovery/exit.json', {'owners': [{'pid': 12345, 'created': 123.5, 'alive': False, 'identityChecked': True}],
            'observerAlive': False, 'controllerAlive': False, 'wrapperAlive': False})
        self.put('linux/recovery/inspect.json', {'valid': True, 'errors': [],
            'distribution': {'configuredRelationships': 512, 'existing': 512, 'active': 0, 'inactive': 512,
                             'missing': 0, 'versionHistogram': {'0': 512}},
            'facts': {'transitions': 0, 'eventCount': 0, 'events': [],
                'facts': [{'noteId': note, 'userId': user, 'active': 0, 'version': 0} for note, user in zip(manifest['multiNoteIds'], manifest['userIds'])],
                'notes': [{'noteId': note, 'liked': 0, 'activeRelations': 0} for note in manifest['multiNoteIds']]}})
        names = {'command': 'S1-command.json', 'stage': 'S1-stage.json', 'collectorTerminal': 'recovery/collector-terminal.json',
                 'collectorRecords': 'recovery/records.ndjson', 'stopAudit': 'recovery/stop.json',
                 'idleAudit1': 'recovery/idle1.json', 'idleAudit2': 'recovery/idle2.json', 'localExit': 'recovery/exit.json',
                 'fixtureManifest': 'fixture.json', 'fixtureInspection': 'recovery/inspect.json'}
        proof = {'version': 1, 'batch': 'CAP-10', 'failedTag': 'S1',
                 'artifacts': {key: {'path': value, 'sha256': grade.sha(directory / value)} for key, value in names.items()}}
        self.put('linux/pre-load-recovery-proof.json', proof)
        authorization = {'version': 1, 'batch': 'CAP-10', 'authorized': True, 'failedTag': 'S1', 'nextDiagnosticTag': 'S2',
            'remoteEngineRSSLimitBytes': 6 * 1024 ** 3, 'toolRefreezeAuthorized': True,
            'userAuthorizationReference': 'synthetic authorized autonomous zero-request repair', 'recordedAt': at(1),
            'proofPath': 'pre-load-recovery-proof.json', 'proofSHA256': grade.sha(directory / 'pre-load-recovery-proof.json')}
        self.put('linux/pre-load-recovery.json', authorization)
        return directory, api, proof, authorization

    def test_zero_request_startup_consumes_S1_then_allows_only_S2_diagnostic(self):
        directory, api, proof, authorization = self.zero_startup()
        original = (directory / 'S1-stage.json').read_bytes()
        reservation = self.reserve('diagnostic', 50, execution_root=directory, api=api)
        self.assertEqual((reservation['tag'], reservation['shortSlotsUsedAfter'], reservation['mainSecondsReservedAfter']), ('S2', 2, 120))
        self.assertEqual(grade.search_state(directory, self.source)['boundaryTags'], [])
        self.assertIsNone(grade.search_state(directory, self.source)['L'])
        self.assertEqual((directory / 'S1-stage.json').read_bytes(), original)
        for kind in ('search', 'candidate', 'comparison'):
            with self.subTest(kind=kind), self.assertRaises(ValueError):
                self.reserve(kind, 50, execution_root=directory, api=api)

    def test_recovered_diagnostic_supports_immediate_single_comparison_and_search(self):
        directory, api, proof, authorization = self.zero_startup()
        reservation = self.reserve('diagnostic', 50, execution_root=directory, api=api)
        command = self.add('S2', 50, kind='diagnostic', execution_root=directory)
        command['reservationCheck'] = reservation
        self.put('linux/S2-command.json', command)
        self.assertEqual(grade.search_state(directory, self.source)['nextRate'], 200)
        self.assertEqual(self.reserve('search', 200, execution_root=directory, api=api)['tag'], 'S3')
        prediction = {'observedEvidence': ['actual S2 segments'], 'differentPredictions': ['SQL', 'cache'],
                      'comparisonOf': 'S2', 'singleVariableEvidence': ['one application change']}
        self.assertEqual(self.reserve('comparison', 50, source=self.other, execution_root=directory, api=api,
                                     prediction=prediction)['tag'], 'S3')
        with self.assertRaises(ValueError):
            self.reserve('comparison', 50, source=self.other, execution_root=directory, api=api)
        with self.assertRaises(ValueError):
            self.reserve('search', 200, execution_root=directory, api=api, current_tools={'changed': 'f' * 64})
        self.put('linux/pre-load-recovery.json', {**authorization, 'userAuthorizationReference': 'changed after S2'})
        with self.assertRaises(ValueError):
            grade.search_state(directory, self.source)

    def test_zero_request_recovery_rejects_any_put_uncertainty_live_owner_or_short_idle(self):
        directory, api, proof, authorization = self.zero_startup()
        for name, mutate in [('collectorTerminal', lambda value: value['counts'].update(attempted=1)),
                ('collectorTerminal', lambda value: value['counts'].update(uncertain=1)),
                ('localExit', lambda value: value['owners'][0].update(alive=True)),
                ('idleAudit2', lambda value: value.update(requestStartedAt=grade.read(directory / 'recovery/idle1.json')['requestStartedAt'])),
                ('fixtureInspection', lambda value: value['facts']['facts'][0].update(active=1))]:
            target = directory / proof['artifacts'][name]['path']
            retained = target.read_bytes()
            value = grade.read(target); mutate(value); target.write_text(json.dumps(value), encoding='utf-8')
            proof['artifacts'][name]['sha256'] = grade.sha(target)
            self.put('linux/pre-load-recovery-proof.json', proof)
            self.put('linux/pre-load-recovery.json', {**authorization, 'proofSHA256': grade.sha(directory / 'pre-load-recovery-proof.json')})
            with self.subTest(name=name), self.assertRaises(ValueError):
                self.reserve('diagnostic', 50, execution_root=directory, api=api)
            target.write_bytes(retained)
            proof['artifacts'][name]['sha256'] = grade.sha(target)

    def test_zero_request_recovery_accepts_future_barrier_only_when_stop_precedes_it(self):
        directory, api, proof, authorization = self.zero_startup()
        journal = directory / proof['artifacts']['collectorRecords']['path']
        rows = [json.loads(line) for line in journal.read_text(encoding='utf-8').splitlines()]
        rows.insert(1, {'kind': 'barrier', 'startUnixMs': 10000, 'endUnixMs': 20000})
        rows.insert(2, {'kind': 'stop', 'atUnixMs': 9000})
        for stopped, accepted in ((9000, True), (10000, False), (10001, False)):
            rows[2]['atUnixMs'] = stopped
            journal.write_text(''.join(json.dumps(row) + '\n' for row in rows), encoding='utf-8')
            proof['artifacts']['collectorRecords']['sha256'] = grade.sha(journal)
            collector_path = directory / proof['artifacts']['collectorTerminal']['path']
            collector = grade.read(collector_path)
            collector.update(startUnixMs=10000, evidenceSHA256=grade.sha(journal))
            collector_path.write_text(json.dumps(collector), encoding='utf-8')
            proof['artifacts']['collectorTerminal']['sha256'] = grade.sha(collector_path)
            self.put('linux/pre-load-recovery-proof.json', proof)
            self.put('linux/pre-load-recovery.json', {**authorization, 'proofSHA256': grade.sha(directory / 'pre-load-recovery-proof.json')})
            if accepted:
                self.assertEqual(self.reserve('diagnostic', 50, execution_root=directory, api=api)['tag'], 'S2')
            else:
                with self.assertRaises(ValueError):
                    self.reserve('diagnostic', 50, execution_root=directory, api=api)

    def repaired_s4(self):
        self.now -= dt.timedelta(seconds=100)
        directory, api, proof, authorization = self.zero_startup()
        self.put('linux/pts-packet.json', {'sceneFilesMD5': {'plan': 'old'}})
        old = {'scriptHashes': self.tools.copy(), 'observerBinarySHA256': self.observer,
               'ptsPacketSHA256': grade.sha(directory / 'pts-packet.json'),
               'ptsAdmissionSHA256': grade.sha(directory / 'pts-scene-admission.json')}
        for tag, kind, rate in [('S2', 'diagnostic', 50), ('S3', 'search', 200), ('S4', 'search', 800)]:
            reservation = self.reserve(kind, rate, execution_root=directory, api=api)
            terminal = {} if tag != 'S4' else {'batchFatal': True, 'childrenConverged': False, 'recoveryVerified': False,
                'finishedAt': (self.now + dt.timedelta(seconds=5)).isoformat(), 'knownOwnedProcesses': [{'pid': 23456, 'created': 234.5}]}
            command = self.add(tag, rate, 'measurement_invalid' if tag == 'S4' else 'pass', kind=kind, execution_root=directory, **terminal)
            command.update(old, reservationCheck=reservation)
            self.put('linux/' + tag + '-command.json', command)
        s4 = grade.read(directory / 'S4-command.json')
        self.put('linux/pts-packet-repair1.json', {'sceneFilesMD5': {'plan': 'new'}})
        self.put('linux/pts-scene-admission-repair1.json', {'passed': True, 'agentCount': 2, 'concurrency': 513,
            'duration': 600, 'sceneFilesMD5': {'plan': 'new'}})
        new = {'scriptHashes': {'synthetic-tool.py': 'f' * 64}, 'observerBinarySHA256': self.observer,
            'ptsPacketPath': 'pts-packet-repair1.json', 'ptsAdmissionPath': 'pts-scene-admission-repair1.json',
            'ptsPacketSHA256': grade.sha(directory / 'pts-packet-repair1.json'),
            'ptsAdmissionSHA256': grade.sha(directory / 'pts-scene-admission-repair1.json')}
        journal = self.put('linux/tool-repair/records.ndjson', {'kind': 'batch', 'records': [{'partial': True}]})
        self.put('linux/tool-repair/collector.json', {'phase': 'stable', 'runId': 'S4_MAIN',
            'fixtureSHA256': s4['fixtureSHA256'], 'counts': {'attempted': 2048}, 'unknown': None,
            'evidenceSHA256': grade.sha(journal)})
        for phase in ('warm', 'stable'):
            self.put('linux/tool-repair/' + phase + '-terminal.json', {'backend': 'pts', 'reportId': phase + '-report',
                'runId': 'S4_MAIN' if phase == 'stable' else 'S4_WARM', 'originalTerminalAbsent': phase == 'stable',
                'nativeEvidenceComplete': False})
            self.put('linux/tool-repair/' + phase + '-report.json', {'reportId': phase + '-report', 'ended': True,
                'startTimeRaw': '2026-11-02 00:00:00', 'endTimeRaw': '2026-11-02 00:01:00'})
        self.put('linux/tool-repair/start-ack.json', {'action': 'StartTestingJMeterScene', 'httpStatus': 200,
            'sceneId': 'OWNED_SCENE', 'sanitizedRawResponse': {'Success': True, 'ReportId': 'stable-report'}})
        self.put('linux/tool-repair/exit.json', {'owners': [{'pid': 23456, 'created': 234.5, 'alive': False, 'identityChecked': True}],
            'observerAlive': False, 'controllerAlive': False, 'wrapperAlive': False})
        manifest = grade.read(directory / 'fixture.json')
        pairs = list(zip(manifest['multiNoteIds'], manifest['userIds']))
        total = len(pairs)
        reconciliation = {'valid': True, 'factsValid': True, 'downstreamConverged': True, 'errors': [],
            'startedAt': int((self.now + dt.timedelta(seconds=17)).timestamp() * 1000),
            'finishedAt': int((self.now + dt.timedelta(seconds=18)).timestamp() * 1000),
            'facts': {'transitions': total, 'eventCount': total,
                'facts': [{'noteId': note, 'userId': user, 'active': 1, 'version': 1} for note, user in pairs],
                'notes': [{'noteId': note, 'liked': 1, 'activeRelations': 1} for note, user in pairs],
                'events': [{'noteId': note, 'userId': user, 'count': 1, 'distinctVersions': 1, 'minVersion': 1, 'maxVersion': 1, 'invalid': 0} for note, user in pairs],
                'delivery': [{'status': 'DONE', 'count': total}], 'parked': [],
                'receipts': [{'projection': group, 'state': 'IGNORED', 'count': total} for group in ('follow-v1', 'feed-v1', 'search-v1')]},
            'derived': {'available': True, 'errors': [],
                'notes': [{'noteId': note, 'members': [user], 'versions': {user: '1'}} for note, user in pairs]}}
        self.put('linux/tool-repair/reconcile.json', reconciliation)
        names = {'command': 'S4-command.json', 'stage': 'S4-stage.json', 'collectorTerminal': 'tool-repair/collector.json',
            'collectorRecords': 'tool-repair/records.ndjson', 'warmTerminal': 'tool-repair/warm-terminal.json',
            'stableTerminal': 'tool-repair/stable-terminal.json', 'stableStartAudit': 'tool-repair/start-ack.json',
            'warmReport': 'tool-repair/warm-report.json', 'stableReport': 'tool-repair/stable-report.json',
            'localExit': 'tool-repair/exit.json', 'fixtureManifest': 'fixture.json', 'reconciliation': 'tool-repair/reconcile.json'}
        transition = {'version': 1, 'batch': 'CAP-10', 'authorized': True, 'afterTag': 'S4', 'nextTag': 'S5', 'firstRate': 800,
            'settleSeconds': 10, 'terminalObservedAt': (self.now + dt.timedelta(seconds=6)).isoformat(),
            'recordedAt': (self.now + dt.timedelta(seconds=19)).isoformat(),
            'userAuthorizationReference': 'synthetic explicit same-session repair without extra windows',
            'preLoadRecoverySHA256': grade.sha(directory / 'pre-load-recovery.json'),
            'sourceSha': self.source, 'binarySHA256': s4['binarySHA256'],
            'environmentSHA256': s4['environmentSHA256'], 'fixtureSHA256': s4['fixtureSHA256'],
            'oldIdentity': old, 'newIdentity': new,
            'artifacts': {key: {'path': value, 'sha256': grade.sha(directory / value)} for key, value in names.items()}}
        self.put('linux/tool-repair-transition.json', transition)
        return directory, api, transition

    def test_S4_tool_repair_retains_unknown_and_only_restarts_S5_search(self):
        directory, api, transition = self.repaired_s4()
        original_stage = (directory / 'S4-stage.json').read_bytes()
        bound = grade.search_state(directory, self.source)
        self.assertEqual((bound['L'], bound['U'], bound['nextRate'], bound['boundaryTags']), (None, None, 800, []))
        reservation = self.reserve('search', 800, execution_root=directory, api=api, current_tools=transition['newIdentity']['scriptHashes'])
        self.assertEqual((reservation['tag'], reservation['mainSecondsReservedAfter']), ('S5', 300))
        self.assertEqual((directory / 'S4-stage.json').read_bytes(), original_stage)
        self.assertIsNone(grade.read(directory / 'tool-repair/collector.json')['unknown'])
        for kind, rate in [('search', 50), ('diagnostic', 50), ('comparison', 50), ('candidate', 800)]:
            with self.subTest(kind=kind), self.assertRaises(ValueError):
                self.reserve(kind, rate, execution_root=directory, api=api, current_tools=transition['newIdentity']['scriptHashes'])

    def test_repaired_tool_bounds_exclude_old_windows_and_freeze_new_identity(self):
        directory, api, transition = self.repaired_s4()
        new = transition['newIdentity']
        reservation = self.reserve('search', 800, execution_root=directory, api=api, current_tools=new['scriptHashes'])
        command = self.add('S5', 800, execution_root=directory)
        command.update(new, reservationCheck=reservation, at=(self.now + dt.timedelta(seconds=20)).isoformat())
        self.put('linux/S5-command.json', command)
        bound = grade.search_state(directory, self.source)
        self.assertEqual((bound['L'], bound['nextRate'], bound['boundaryTags']), (800, 3200, ['S5']))
        self.assertEqual(self.reserve('search', 3200, execution_root=directory, api=api, current_tools=new['scriptHashes'])['tag'], 'S6')
        with self.assertRaises(ValueError):
            self.reserve('search', 3200, execution_root=directory, api=api)
        self.put('linux/tool-repair-transition.json', {**transition, 'userAuthorizationReference': 'changed after S5'})
        with self.assertRaises(ValueError):
            grade.search_state(directory, self.source)

    def test_tool_repair_rejects_unsettled_data_or_missing_real_EndTime(self):
        directory, api, transition = self.repaired_s4()
        for name, mutate in [('stableReport', lambda value: value.update(endTimeRaw='')),
                ('stableStartAudit', lambda value: value['sanitizedRawResponse'].update(ReportId='FOREIGN')),
                ('reconciliation', lambda value: value.update(startedAt=int(grade.instant(transition['terminalObservedAt']).timestamp() * 1000))),
                ('reconciliation', lambda value: value['derived']['notes'][0].update(members=[]))]:
            path = directory / transition['artifacts'][name]['path']
            retained = path.read_bytes(); value = grade.read(path); mutate(value)
            path.write_text(json.dumps(value), encoding='utf-8')
            transition['artifacts'][name]['sha256'] = grade.sha(path)
            self.put('linux/tool-repair-transition.json', transition)
            with self.subTest(name=name), self.assertRaises(ValueError):
                self.reserve('search', 800, execution_root=directory, api=api, current_tools=transition['newIdentity']['scriptHashes'])
            path.write_bytes(retained); transition['artifacts'][name]['sha256'] = grade.sha(path)

    def test_tool_repair_can_read_original_latest_only_when_collector_terminal_is_absent(self):
        directory, api, transition = self.repaired_s4()
        collector = grade.read(directory / transition['artifacts']['collectorTerminal']['path'])
        collector['stop'] = True
        latest = self.put('linux/S4-load/stable-pts/latest.json', {'at': self.now.isoformat(), 'summary': collector})
        transition['artifacts']['collectorTerminal'] = {'path': 'S4-load/stable-pts/latest.json', 'sha256': grade.sha(latest)}
        self.put('linux/tool-repair-transition.json', transition)
        self.assertEqual(self.reserve('search', 800, execution_root=directory, api=api,
                                     current_tools=transition['newIdentity']['scriptHashes'])['tag'], 'S5')
        self.put('linux/S4-load/stable-pts/collector-terminal.json', collector)
        with self.assertRaises(ValueError):
            self.reserve('search', 800, execution_root=directory, api=api,
                         current_tools=transition['newIdentity']['scriptHashes'])

    def claim_baseline(self):
        directory, api, transition = self.repaired_s4()
        new = transition['newIdentity']
        new['scriptHashes'].update({'cap10-grade.py': '1' * 64, 'cap10-run.py': '2' * 64})
        self.put('linux/tool-repair-transition.json', transition)
        reservation = self.reserve('search', 800, execution_root=directory, api=api, current_tools=new['scriptHashes'])
        command = self.add('S5', 800, 'performance_limited', execution_root=directory, recoveryVerified=False,
            finishedAt=(self.now + dt.timedelta(seconds=21)).isoformat())
        command.update(new, reservationCheck=reservation, at=(self.now + dt.timedelta(seconds=20)).isoformat())
        self.put('linux/S5-command.json', command)
        metrics = dict.fromkeys(('noop', 'rejected', 'contentErrors', 'dropped', 'unknown', 'classificationUnknown', 'effectUnknown'), 0)
        metrics.update(attempted=48000, completed=48000, mutation=48000, mutationCounterMatchesDB=True,
                       allLatency={'p(95)': 53, 'p(99)': 95})
        self.put('linux/S5-load/result.json', {'nativeScenario': {'complete': True}, 'meetsAPIBudget': True,
            'finalFactsValid': True, 'phaseAccountingValid': True, 'result': metrics})
        before = {'sourceSha': self.source, **{key: command[key] for key in ('binarySHA256', 'environmentSHA256', 'fixtureSHA256',
                  'scriptHashes', 'observerBinarySHA256', 'ptsPacketSHA256', 'ptsAdmissionSHA256')}}
        binary = directory / 'optimized-never-executed.bin'; binary.write_bytes(b'synthetic optimized Claim; never executed')
        after = {**before, 'sourceSha': self.other, 'binarySHA256': grade.sha(binary),
                 'scriptHashes': {**before['scriptHashes'], 'cap10-grade.py': '3' * 64, 'cap10-run.py': '4' * 64}}
        optimized_api = {**api, 'exe': str(binary), 'sha256': after['binarySHA256'], 'likeDiagnosticsRequested': False}
        for phase in ('warm', 'stable'):
            self.put('linux/claim-opt/' + phase + '-terminal.json', {'backend': 'pts', 'remoteConverged': True,
                'nativeEvidenceComplete': True, 'reportId': 'S5-' + phase})
        self.put('linux/claim-opt/index.json', {'table': 'tb_community_outbox', 'column': 'claim_active',
            'extra': 'VIRTUAL GENERATED', 'indexColumns': ['claim_active', 'id'], 'indexName': 'idx_claim_active_id',
            'explainKey': 'idx_claim_active_id'})
        self.put('linux/claim-opt/checks.json', {'passed': True, 'sourceSha': after['sourceSha'],
            'checks': ['synthetic related Claim locks and fence checks']})
        reconciliation = grade.read(directory / 'tool-repair/reconcile.json')
        reconciliation.update(startedAt=int((self.now + dt.timedelta(seconds=27)).timestamp() * 1000),
                              finishedAt=int((self.now + dt.timedelta(seconds=28)).timestamp() * 1000),
                              timeline=[{'pending': 0, 'parked': 0, 'receiptErrors': []}])
        facts = reconciliation['facts']; total = 51200
        facts.update(transitions=total, eventCount=total)
        for row in facts['facts']: row.update(active=0, version=100)
        for note in facts['notes']: note.update(liked=0, activeRelations=0)
        for event in facts['events']: event.update(count=100, distinctVersions=100, maxVersion=100)
        facts['delivery'][0]['count'] = total
        for receipt in facts['receipts']: receipt['count'] = total
        for note in reconciliation['derived']['notes']:
            user = next(iter(note['versions'])); note.update(members=[], versions={user: '100'})
        self.put('linux/claim-opt/reconcile.json', reconciliation)
        names = {'command': 'S5-command.json', 'stage': 'S5-stage.json', 'result': 'S5-load/result.json',
            'warmTerminal': 'claim-opt/warm-terminal.json', 'stableTerminal': 'claim-opt/stable-terminal.json',
            'indexReceipt': 'claim-opt/index.json', 'relatedChecks': 'claim-opt/checks.json',
            'reconciliation': 'claim-opt/reconcile.json'}
        proof = {'version': 1, 'batch': 'CAP-10', 'authorized': True, 'optimizationKind': 'outbox-claim-access-path',
            'baselineTag': 'S5', 'nextComparisonTag': 'S6', 'rate': 800, 'seconds': 60, 'diagnostics': False,
            'userAuthorizationReference': 'synthetic one logical Claim virtual-column/index/predicate optimization',
            'recordedAt': (self.now + dt.timedelta(seconds=30)).isoformat(),
            'toolRepairTransitionSHA256': grade.sha(directory / 'tool-repair-transition.json'),
            'beforeIdentity': before, 'afterIdentity': after,
            'artifacts': {key: {'path': value, 'sha256': grade.sha(directory / value)} for key, value in names.items()}}
        self.put('linux/S5-optimization-proof.json', proof)
        self.put('linux/final-application.json', {key: after[key] for key in ('sourceSha', 'binarySHA256', 'environmentSHA256', 'fixtureSHA256')})
        return directory, optimized_api, proof

    def test_Claim_optimization_requires_S6_comparison800_without_diagnostics(self):
        directory, api, proof = self.claim_baseline()
        original = (directory / 'S5-stage.json').read_bytes()
        reservation = self.reserve('comparison', 800, source=self.other, execution_root=directory, api=api,
                                   current_tools=proof['afterIdentity']['scriptHashes'])
        self.assertEqual((reservation['tag'], reservation['mainSecondsReservedAfter']), ('S6', 360))
        self.assertEqual((directory / 'S5-stage.json').read_bytes(), original)
        for kind, rate, edits in [('comparison', 800, {'likeDiagnosticsRequested': True}),
                                  ('comparison', 50, {}), ('search', 800, {}), ('diagnostic', 800, {})]:
            with self.subTest(kind=kind, rate=rate), self.assertRaises(ValueError):
                self.reserve(kind, rate, source=self.other, execution_root=directory, api={**api, **edits},
                             current_tools=proof['afterIdentity']['scriptHashes'])

    def test_Claim_optimization_retains_all_eight_slots_and_same_two_formals(self):
        directory, api, proof = self.claim_baseline(); after = proof['afterIdentity']
        for tag, kind, rate in [('S6', 'comparison', 800), ('S7', 'search', 3200), ('S8', 'candidate', 3200)]:
            reservation = self.reserve(kind, rate, source=self.other, execution_root=directory, api=api,
                                       current_tools=after['scriptHashes'])
            command = self.add(tag, rate, kind=kind, source=self.other, execution_root=directory)
            command.update({key: after[key] for key in ('binarySHA256', 'environmentSHA256', 'fixtureSHA256', 'scriptHashes',
                           'observerBinarySHA256', 'ptsPacketSHA256', 'ptsAdmissionSHA256')},
                           reservationCheck=reservation, at=(self.now + dt.timedelta(seconds=30 + int(tag[1:]))).isoformat())
            self.put('linux/' + tag + '-command.json', command)
        bound = grade.search_state(directory, self.other)
        self.assertEqual((bound['L'], bound['boundaryTags']), (3200, ['S6', 'S7']))
        freeze = {**after, 'rate': 3200, 'sourceCIPassed': True, 'relatedChecksPassed': True,
            'initialDistributionContract': {'existing': 512, 'active': 0, 'relationships': 512},
            'toolRepairTransitionSHA256': proof['toolRepairTransitionSHA256'],
            'claimOptimizationSHA256': grade.sha(directory / 'S5-optimization-proof.json')}
        self.put('freeze-like.json', freeze)
        self.assertEqual(self.reserve('formal', 3200, source=self.other, freeze=freeze, execution_root=directory,
                                     api=api, current_tools=after['scriptHashes'])['mainSecondsReservedAfter'], 660)

    def test_Claim_optimization_cannot_change_workload_or_skip_drain(self):
        directory, api, proof = self.claim_baseline()
        original = json.loads(json.dumps(proof))
        for mutate in (lambda value: value['afterIdentity']['scriptHashes'].update({'synthetic-tool.py': '9' * 64}),
                       lambda value: value['afterIdentity'].update(fixtureSHA256='9' * 64),
                       lambda value: value.update(diagnostics=True)):
            changed = json.loads(json.dumps(original)); mutate(changed)
            self.put('linux/S5-optimization-proof.json', changed)
            with self.assertRaises(ValueError):
                self.reserve('comparison', 800, source=self.other, execution_root=directory, api=api,
                             current_tools=proof['afterIdentity']['scriptHashes'])

    def delayed_s7(self):
        directory, api, optimization = self.claim_baseline(); after = optimization['afterIdentity']
        for tag, kind, rate in [('S6', 'comparison', 800), ('S7', 'search', 3200)]:
            reservation = self.reserve(kind, rate, source=self.other, execution_root=directory, api=api,
                                       current_tools=after['scriptHashes'])
            terminal = {} if tag == 'S6' else {'recoveryVerified': False,
                'finishedAt': (self.now + dt.timedelta(seconds=40)).isoformat(),
                'knownOwnedProcesses': [{'pid': 34567, 'created': 345.5}]}
            command = self.add(tag, rate, 'pass' if tag == 'S6' else 'performance_limited', kind=kind,
                               source=self.other, execution_root=directory, **terminal)
            command.update({key: after[key] for key in ('binarySHA256', 'environmentSHA256', 'fixtureSHA256', 'scriptHashes',
                           'observerBinarySHA256', 'ptsPacketSHA256', 'ptsAdmissionSHA256')},
                           reservationCheck=reservation, at=(self.now + dt.timedelta(seconds=30 + int(tag[1:]))).isoformat())
            self.put('linux/' + tag + '-command.json', command)
        metrics = dict.fromkeys(('noop', 'rejected', 'contentErrors', 'dropped', 'unknown', 'classificationUnknown',
                                 'effectUnknown', 'httpErrors', 'systemErrors'), 0)
        metrics.update(attempted=192000, completed=192000, mutation=192000, mutationCounterMatchesDB=True,
                       actualOfferedRatio=1, allLatency={'p(95)': 100, 'p(99)': 600})
        self.put('linux/S7-load/result.json', {'nativeScenario': {'complete': True}, 'finalFactsValid': True,
            'phaseAccountingValid': True, 'fixtureManifestSHA256': after['fixtureSHA256'], 'result': metrics})
        for phase in ('warm', 'stable'):
            self.put('linux/S7-late/' + phase + '-terminal.json', {'backend': 'pts', 'remoteConverged': True,
                'nativeEvidenceComplete': True, 'reportId': 'S7-' + phase})
            self.put('linux/S7-late/' + phase + '-report.json', {'reportId': 'S7-' + phase, 'ended': True,
                'startTimeRaw': '2026-11-02 00:00:00', 'endTimeRaw': '2026-11-02 00:01:00'})
        self.put('linux/S7-late/exit.json', {'owners': [{'pid': 34567, 'created': 345.5, 'alive': False, 'identityChecked': True}],
            'observerAlive': False, 'controllerAlive': False, 'wrapperAlive': False})
        reconciliation = grade.read(directory / 'claim-opt/reconcile.json'); total = 512000
        reconciliation.update(startedAt=int((self.now + dt.timedelta(seconds=43)).timestamp() * 1000),
                              finishedAt=int((self.now + dt.timedelta(seconds=44)).timestamp() * 1000))
        facts = reconciliation['facts']; facts.update(transitions=total, eventCount=total)
        for row in facts['facts']: row['version'] = 1000
        for event in facts['events']: event.update(count=1000, distinctVersions=1000, maxVersion=1000)
        facts['delivery'][0]['count'] = total
        for receipt in facts['receipts']: receipt['count'] = total
        for note in reconciliation['derived']['notes']:
            user = next(iter(note['versions'])); note['versions'][user] = '1000'
        self.put('linux/S7-late/reconcile.json', reconciliation)
        names = {'command': 'S7-command.json', 'stage': 'S7-stage.json', 'result': 'S7-load/result.json',
            'warmTerminal': 'S7-late/warm-terminal.json', 'stableTerminal': 'S7-late/stable-terminal.json',
            'warmReport': 'S7-late/warm-report.json', 'stableReport': 'S7-late/stable-report.json',
            'localExit': 'S7-late/exit.json', 'reconciliation': 'S7-late/reconcile.json'}
        proof = {'version': 1, 'batch': 'CAP-10', 'authorized': True, 'failedTag': 'S7', 'nextTag': 'S8',
            'claimOptimizationSHA256': grade.sha(directory / 'S5-optimization-proof.json'),
            'sourceSha': after['sourceSha'], 'binarySHA256': after['binarySHA256'],
            'userAuthorizationReference': 'synthetic one later drain before the already reserved candidate',
            'recordedAt': (self.now + dt.timedelta(seconds=45)).isoformat(),
            'artifacts': {key: {'path': value, 'sha256': grade.sha(directory / value)} for key, value in names.items()}}
        self.put('linux/S7-delayed-recovery.json', proof)
        return directory, api, optimization, proof

    def test_delayed_S7_recovery_preserves_failure_and_upper_bound_for_only_S8(self):
        directory, api, optimization, proof = self.delayed_s7(); after = optimization['afterIdentity']
        original = (directory / 'S7-stage.json').read_bytes()
        bound = grade.search_state(directory, self.other)
        self.assertEqual((bound['L'], bound['U'], bound['candidateRate']), (800, 3200, 800))
        reservation = self.reserve('candidate', 800, source=self.other, execution_root=directory, api=api,
                                   current_tools=after['scriptHashes'])
        self.assertEqual((reservation['tag'], reservation['mainSecondsReservedAfter']), ('S8', 480))
        self.assertEqual((directory / 'S7-stage.json').read_bytes(), original)
        with self.assertRaises(ValueError):
            self.reserve('search', 800, source=self.other, execution_root=directory, api=api,
                         current_tools=after['scriptHashes'])
        command = self.add('S8', 800, kind='candidate', source=self.other, execution_root=directory, recoveryVerified=False)
        command.update({key: after[key] for key in ('binarySHA256', 'environmentSHA256', 'fixtureSHA256', 'scriptHashes',
                       'observerBinarySHA256', 'ptsPacketSHA256', 'ptsAdmissionSHA256')}, reservationCheck=reservation,
                       at=(self.now + dt.timedelta(seconds=50)).isoformat())
        self.put('linux/S8-command.json', command)
        freeze = {**after, 'rate': 800, 'sourceCIPassed': True, 'relatedChecksPassed': True,
            'initialDistributionContract': {'existing': 512, 'active': 0, 'relationships': 512},
            'toolRepairTransitionSHA256': optimization['toolRepairTransitionSHA256'],
            'claimOptimizationSHA256': proof['claimOptimizationSHA256'],
            'delayedRecoverySHA256': grade.sha(directory / 'S7-delayed-recovery.json')}
        with self.assertRaises(ValueError):
            self.reserve('formal', 800, source=self.other, freeze=freeze, execution_root=directory,
                         api=api, current_tools=after['scriptHashes'])

    def test_delayed_S7_recovery_rejects_unknown_live_owner_or_incomplete_drain(self):
        directory, api, optimization, proof = self.delayed_s7()
        for name, mutate in [('result', lambda value: value['result'].update(unknown=1)),
                ('localExit', lambda value: value['owners'][0].update(alive=True)),
                ('stableReport', lambda value: value.update(endTimeRaw='')),
                ('reconciliation', lambda value: value['timeline'][-1].update(pending=1))]:
            path = directory / proof['artifacts'][name]['path']; retained = path.read_bytes()
            value = grade.read(path); mutate(value); path.write_text(json.dumps(value), encoding='utf-8')
            proof['artifacts'][name]['sha256'] = grade.sha(path)
            self.put('linux/S7-delayed-recovery.json', proof)
            with self.subTest(name=name), self.assertRaises(ValueError):
                self.reserve('candidate', 800, source=self.other, execution_root=directory, api=api,
                             current_tools=optimization['afterIdentity']['scriptHashes'])
            path.write_bytes(retained); proof['artifacts'][name]['sha256'] = grade.sha(path)

    def authority_fixture(self):
        self.now -= dt.timedelta(seconds=100)
        self.put('linux/environment.json', {'synthetic': 'retained 4CPU/DB20 environment'})
        self.put('linux/fixture.json', {'synthetic': 'retained 512 relationships'})
        self.put('environment-transition.json', {'recordedAt': (self.now - dt.timedelta(seconds=600)).isoformat(),
            'effectiveWorkSecondsUsedBeforeSwitch': 7800, 'effectiveWorkSecondsRemaining': 3000})
        directory = self.root / 'linux'
        for i in range(1, 7):
            self.add('S' + str(i), 800, 'performance_limited', execution_root=directory, recoveryVerified=False,
                finishedAt=(self.now + dt.timedelta(seconds=i + 1)).isoformat(),
                knownOwnedProcesses=[{'pid': 45678, 'created': 456.5}])
        retained = {'environment-transition.json': grade.sha(self.root / 'environment-transition.json')}
        retained.update({p.relative_to(self.root).as_posix(): grade.sha(p) for p in directory.glob('S*-*.json')
                         if p.name.endswith(('-command.json', '-stage.json'))})
        authority = {'version': 1, 'batch': 'CAP-10', 'authorized': True, 'generation': 1,
            'workflowLimitsWaived': True, 'userAuthorizationReference': 'synthetic explicit removal of workflow limits',
            'recordedAt': (self.now + dt.timedelta(seconds=9)).isoformat(),
            'retainedTags': ['S' + str(i) for i in range(1, 7)], 'retainedArtifacts': retained}
        self.put('execution-authority-20261102.json', authority)
        binary = directory / 'authority-never-executed.bin'; binary.write_bytes(b'synthetic current worker8 binary')
        self.put('linux/packet.json', {'sceneFilesMD5': {'plan': 'frozen'}})
        self.put('linux/admission.json', {'passed': True, 'sceneFilesMD5': {'plan': 'frozen'}})
        ready = self.put('linux/new-api-health.json', {'at': (self.now + dt.timedelta(seconds=10)).isoformat(),
            'ready': True, 'readyBody': {'version': 'capacity-' + self.other}})
        active = {'version': 1, 'batch': 'CAP-10', 'authorityGeneration': 1, 'deploymentId': 'worker8-current',
            'sourceSha': self.other, 'binarySHA256': grade.sha(binary), 'environmentSHA256': grade.sha(directory / 'environment.json'),
            'fixtureSHA256': grade.sha(directory / 'fixture.json'), 'fixturePath': 'fixture.json',
            'scriptHashes': self.tools.copy(), 'observerBinarySHA256': self.observer,
            'ptsPacketPath': 'packet.json', 'ptsPacketSHA256': grade.sha(directory / 'packet.json'),
            'ptsAdmissionPath': 'admission.json', 'ptsAdmissionSHA256': grade.sha(directory / 'admission.json'),
            'deployment': {'relayWorkers': 8, 'gomaxprocs': 4, 'mysqlMaxOpen': 20},
            'readiness': {'path': ready.name, 'sha256': grade.sha(ready)}, 'initialRate': 800}
        self.put('linux/active-application.json', active)
        api = {'executionPlatform': 'linux', 'transitionSHA256': grade.sha(self.root / 'environment-transition.json'),
               'exe': str(binary), 'sha256': active['binarySHA256'], 'likeDiagnosticsRequested': False}
        self.authority_admission(directory, 'S7')
        return directory, api, active

    def authority_admission(self, directory, tag):
        context = grade.execution_context(directory); previous = grade.history(directory)[-1]
        old_stage = grade.stage(directory, previous)
        terminal = {'tag': previous['tag'], 'reports': [{'phase': phase, 'reportId': previous['tag'] + '-' + phase,
            'ended': True, 'startTimeRaw': '2026-11-02 00:00:00', 'endTimeRaw': '2026-11-02 00:01:00'} for phase in ('warm', 'stable')],
            'owners': [{**p, 'alive': False, 'identityChecked': True} for p in old_stage.get('knownOwnedProcesses', [])],
            'observerAlive': False, 'controllerAlive': False, 'wrapperAlive': False}
        terminal_path = self.put('linux/terminal-before-' + tag + '.json', terminal)
        finished = max(self.now + dt.timedelta(seconds=20), grade.instant(old_stage['finishedAt']) + dt.timedelta(milliseconds=2))
        reconciliation = {'valid': True, 'factsValid': True, 'downstreamConverged': True, 'errors': [],
            'startedAt': int((finished - dt.timedelta(milliseconds=1)).timestamp() * 1000), 'finishedAt': int(finished.timestamp() * 1000),
            'facts': {'facts': [{}] * 512, 'notes': [{}] * 512, 'transitions': 0, 'eventCount': 0},
            'derived': {'available': True, 'errors': [], 'notes': [{}] * 512},
            'timeline': [{'pending': 0, 'parked': 0, 'receiptErrors': []}]}
        reconcile_path = self.put('linux/reconcile-before-' + tag + '.json', reconciliation)
        return self.put('linux/admission-' + tag + '.json', {'version': 1, 'batch': 'CAP-10', 'tag': tag,
            'authoritySHA256': context['authoritySHA256'], 'activeApplicationSHA256': context['activeApplicationSHA256'],
            'recordedAt': (finished + dt.timedelta(milliseconds=1)).isoformat(),
            'termination': {'path': terminal_path.name, 'sha256': grade.sha(terminal_path)},
            'reconciliation': {'path': reconcile_path.name, 'sha256': grade.sha(reconcile_path)}})

    def authority_add(self, directory, tag, kind, rate, reservation, active, outcome='pass'):
        command_at = max(self.now + dt.timedelta(seconds=25 + int(tag[1:])),
                         grade.instant(grade.read(directory / ('admission-' + tag + '.json'))['recordedAt']) + dt.timedelta(milliseconds=1))
        command = self.add(tag, rate, outcome, kind=kind, source=active['sourceSha'], execution_root=directory,
            finishedAt=(command_at + dt.timedelta(milliseconds=1)).isoformat())
        command.update({key: active[key] for key in ('binarySHA256', 'environmentSHA256', 'fixtureSHA256', 'scriptHashes',
                       'observerBinarySHA256', 'ptsPacketSHA256', 'ptsAdmissionSHA256')}, reservationCheck=reservation,
                       at=command_at.isoformat())
        self.put('linux/' + tag + '-command.json', command)
        return command

    def test_authority_preserves_failures_and_uses_worker8_comparison_without_old_caps(self):
        directory, api, active = self.authority_fixture()
        original = (directory / 'S6-stage.json').read_bytes()
        reservation = self.reserve('comparison', 800, source=self.other, execution_root=directory, api=api)
        self.assertEqual(reservation['tag'], 'S7')
        self.assertFalse(reservation['expectedDiagnostics'])
        self.assertIsNone(reservation['shortMaximum']); self.assertIsNone(reservation['mainSecondsMaximum'])
        self.assertEqual((reservation['effectiveWorkBudget']['usedBeforeSwitch'], reservation['effectiveWorkBudget']['remainingBeforeSwitch']), (7800, 3000))
        self.assertEqual((directory / 'S6-stage.json').read_bytes(), original)
        self.authority_add(directory, 'S7', 'comparison', 800, reservation, active)
        for number in range(8, 16):
            context = grade.execution_context(directory)
            admission = self.authority_admission(directory, 'S' + str(number))
            fake = {**reservation, 'tag': 'S' + str(number), 'admissionSHA256': grade.sha(admission)}
            self.authority_add(directory, 'S' + str(number), 'search', 800, fake, active, 'measurement_invalid')
        self.authority_admission(directory, 'S16')
        next_window = self.reserve('search', 3200, source=self.other, execution_root=directory, api=api)
        self.assertEqual((next_window['tag'], next_window['mainSecondsReservedAfter']), ('S16', 960))
        bound = grade.search_state(directory, self.other)
        self.assertEqual((bound['L'], bound['U'], bound['boundaryTags']), (800, None, ['S7']))
        active['deploymentId'] = 'new-deployment-same-source'; self.put('linux/active-application.json', active)
        self.assertIsNone(grade.search_state(directory, self.other)['L'])

    def test_authority_rejects_modified_legacy_artifact_or_missing_failed_window_reconciliation(self):
        directory, api, active = self.authority_fixture()
        admission = grade.read(directory / 'admission-S7.json')
        admission.pop('reconciliation'); self.put('linux/admission-S7.json', admission)
        with self.assertRaises((ValueError, KeyError)):
            self.reserve('comparison', 800, source=self.other, execution_root=directory, api=api)
        self.put('linux/S6-stage.json', {'passed': True})
        with self.assertRaises(ValueError):
            grade.execution_context(directory)

    def authority_zero_fixture(self):
        directory, api, active = self.authority_fixture()
        self.put('linux/admission.json', {'passed': True, 'sceneFilesMD5': {'plan': 'frozen'}, 'sceneId': 'SCENE'})
        active['ptsAdmissionSHA256'] = grade.sha(directory / 'admission.json')
        self.put('linux/active-application.json', active); self.authority_admission(directory, 'S7')
        reservation = self.reserve('comparison', 800, source=self.other, execution_root=directory, api=api)
        command = self.authority_add(directory, 'S7', 'comparison', 800, reservation, active, 'measurement_invalid')
        command['loadBackend'] = 'pts'; self.put('linux/S7-command.json', command)
        previous = grade.read(directory / 'S7-stage.json')
        previous.update(wrapperExit=None, observerExit=1, observerPid=45678, knownOwnedProcesses=[],
                        failedStartOrException='AssertionError', recoveryVerified=False)
        self.put('linux/S7-stage.json', previous)
        (directory / 'S7-load/result.json').unlink()
        admission_path = self.authority_admission(directory, 'S8')
        finished = grade.instant(previous['finishedAt'])
        audit = self.put('linux/zero-idle.json', {'action': 'GetJMeterSceneRunningData', 'sceneId': 'SCENE',
            'regionId': 'cn-shanghai', 'httpStatus': 200, 'sanitizedRawResponse': {'Success': True, 'RequestId': 'read-only-request'},
            'requestStartedAt': (finished + dt.timedelta(microseconds=1)).isoformat(),
            'responseFinishedAt': (finished + dt.timedelta(microseconds=2)).isoformat()})
        observer = {'pid': 45678, 'created': None, 'pidAbsent': True, 'alive': False, 'identityChecked': True}
        terminal_path = directory / 'terminal-before-S8.json'; terminal = grade.read(terminal_path)
        terminal.update(terminationKind='zero_start', reports=[], owners=[observer],
            originalCommand={'path': 'S7-command.json', 'sha256': grade.sha(directory / 'S7-command.json')},
            originalStage={'path': 'S7-stage.json', 'sha256': grade.sha(directory / 'S7-stage.json')},
            originalObserverExit=1, originalWrapperExit=None, zeroStartObserver=observer,
            zeroStartObservedAt=(finished + dt.timedelta(microseconds=3)).isoformat(),
            zeroStartIdleAudit={'path': audit.name, 'sha256': grade.sha(audit)},
            noLoadPaths=grade.zero_start_layout(directory, command, previous))
        self.put('linux/terminal-before-S8.json', terminal)
        admission = grade.read(admission_path); admission['termination']['sha256'] = grade.sha(terminal_path)
        self.put('linux/admission-S8.json', admission)
        return directory, api, active, command, previous, terminal

    def test_authority_zero_start_retains_failure_and_requires_no_invented_reports(self):
        directory, api, active, command, previous, terminal = self.authority_zero_fixture()
        before = (directory / 'S7-stage.json').read_bytes()
        reservation = self.reserve('search', 800, source=self.other, execution_root=directory, api=api)
        self.assertEqual(reservation['tag'], 'S8')
        self.assertEqual((directory / 'S7-stage.json').read_bytes(), before)
        self.assertEqual(grade.search_state(directory, self.other)['boundaryTags'], [])
        self.assertEqual(terminal['reports'], [])

    def test_authority_zero_start_rejects_any_phase_start_native_file_or_wrapper_identity(self):
        directory, api, active, command, previous, terminal = self.authority_zero_fixture()
        for relative in ('warm-process.json', 'stable-process.json', 'warm-pts/api/one-StartTestingJMeterScene.json',
                         'stable-pts/records.ndjson', 'before.json'):
            with self.subTest(path=relative):
                path = self.put('linux/S7-load/' + relative, {})
                with self.assertRaises(ValueError):
                    grade.authority_zero_start(directory, command, previous, terminal, {'active': active})
                path.unlink()
                # Empty phase directories still reject; remove only these exact
                # generated empty fixture directories to isolate later cases.
                if (directory / 'S7-load/warm-pts').exists():
                    (directory / 'S7-load/warm-pts/api').rmdir(); (directory / 'S7-load/warm-pts').rmdir()
                if (directory / 'S7-load/stable-pts').exists(): (directory / 'S7-load/stable-pts').rmdir()
        for key, value in (('wrapperPid', None), ('wrapperPid', 1234), ('wrapperExit', 0)):
            self.assertIsNone(grade.zero_start_layout(directory, command, {**previous, key: value}))

    def test_authority_zero_start_rejects_forged_idle_owner_hash_report_and_missing_reconcile(self):
        directory, api, active, command, previous, terminal = self.authority_zero_fixture()
        bad_cases = []
        for key, value in (('reports', [{'phase': 'warm'}]), ('originalCommand', terminal['originalStage']),
                           ('originalStage', terminal['originalCommand']), ('originalObserverExit', 0)):
            bad_cases.append({**terminal, key: value})
        bad_cases.append({**terminal, 'zeroStartObserver': {**terminal['zeroStartObserver'], 'pidAbsent': False}})
        bad_cases.append({**terminal, 'zeroStartIdleAudit': {**terminal['zeroStartIdleAudit'], 'sha256': 'f' * 64}})
        for bad in bad_cases:
            with self.assertRaises(ValueError): grade.authority_zero_start(directory, command, previous, bad, {'active': active})
        idle_path = directory / terminal['zeroStartIdleAudit']['path']; idle = grade.read(idle_path)
        idle['sanitizedRawResponse']['RunningData'] = {'Status': 'RUNNING'}
        self.put('linux/' + idle_path.name, idle)
        altered = {**terminal, 'zeroStartIdleAudit': {'path': idle_path.name, 'sha256': grade.sha(idle_path)}}
        with self.assertRaises(ValueError): grade.authority_zero_start(directory, command, previous, altered, {'active': active})
        admission = grade.read(directory / 'admission-S8.json'); admission.pop('reconciliation')
        self.put('linux/admission-S8.json', admission)
        with self.assertRaises((ValueError, KeyError)):
            self.reserve('search', 800, source=self.other, execution_root=directory, api=api)

    def test_authority_formal_still_requires_exact_candidate_and_stops_after_first_failure(self):
        directory, api, active = self.authority_fixture()
        reservation = self.reserve('candidate', 800, source=self.other, execution_root=directory, api=api)
        self.authority_add(directory, 'S7', 'candidate', 800, reservation, active)
        context = grade.execution_context(directory)
        freeze = {**active, 'rate': 800, 'candidateTag': 'S7', 'sourceCIPassed': True, 'relatedChecksPassed': True,
            'initialDistributionContract': {'existing': 512, 'active': 0, 'relationships': 512},
            'executionAuthoritySHA256': context['authoritySHA256'], 'activeApplicationSHA256': context['activeApplicationSHA256']}
        self.put('freeze-like.json', freeze); self.put('linux/freeze-like.json', freeze)
        self.authority_admission(directory, 'H1')
        first = self.reserve('formal', 800, source=self.other, freeze=freeze, execution_root=directory, api=api)
        self.assertEqual(first['tag'], 'H1')
        self.authority_add(directory, 'H1', 'formal', 800, first, active, 'performance_limited')
        self.authority_admission(directory, 'H2')
        with self.assertRaises(ValueError):
            self.reserve('formal', 800, source=self.other, freeze=freeze, execution_root=directory, api=api)

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
