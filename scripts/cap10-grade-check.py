"""Synthetic CAP10 policy counterexamples; no services, SQL, HTTP or workload."""
import argparse
import copy
import datetime as dt
import hashlib
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch


REPO = Path(__file__).resolve().parent.parent
EVIDENCE = REPO / 'docs/evidence/2026-11-01-cap10'
GRADE_PATH = Path(__file__).with_name('cap10-grade.py')
spec = importlib.util.spec_from_file_location('cap10_policy_under_review', GRADE_PATH)
grade = importlib.util.module_from_spec(spec)
spec.loader.exec_module(grade)
idle_spec = importlib.util.spec_from_file_location('cap10_grade_check_idle_fixture', Path(__file__).with_name('cap10-idle-authorization-check.py'))
idle_checks = importlib.util.module_from_spec(idle_spec); idle_spec.loader.exec_module(idle_checks)


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
        fixture_name = overrides.pop('fixture_name', 'fixture.json')
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
        if 'preflight_reference' not in options and preflight.get('idleSchemaVersion') == 1:
            path = execution_root / preflight['idleDirectory'] / 'idle-result.json'
            original = grade.read(path)
            options['preflight_reference'] = {'path': path.relative_to(execution_root).as_posix(), 'sha256': grade.sha(path),
                **{key: original[key] for key in ('environmentPath', 'environmentSHA256', 'resourceProfile')}}
        return grade.check_reservation(execution_root, kind, rate, 180 if kind == 'formal' else 60,
                                       api, execution_root / fixture_name, preflight, ability, **options)

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

    def test_old_reservation_records_canonical_legacy_observer_without_relabelling(self):
        reservation = self.reserve('diagnostic', 50)
        self.assertEqual(reservation['observerPolicy'], grade.OBSERVER.environment_policy({}))
        self.assertEqual(reservation['observerSamplingLimits'], grade.OBSERVER.sampling_limits())

    def test_actual_authority_reservation_binds_explicit_vector_identity(self):
        policy = grade.OBSERVER.environment_policy({'CAP10_OBSERVER_MODE': 'like-vector-v1'})
        metadata = {'observerPolicy': policy, 'observerSamplingLimits': grade.OBSERVER.sampling_limits()}
        with patch.object(grade, 'execution_context', return_value={'active': copy.deepcopy(metadata)}), \
             patch.object(grade, 'authority_reservation', return_value={'tag': 'S20'}) as authority:
            result = self.reserve('search', 5000, api=metadata, observer_policy=policy)
        self.assertEqual(result, {'tag': 'S20', **metadata})
        authority.assert_called_once()

    def test_actual_reservation_rejects_mixed_or_unbound_vector_before_authority(self):
        policy = grade.OBSERVER.environment_policy({'CAP10_OBSERVER_MODE': 'like-vector-v1'})
        metadata = {'observerPolicy': policy, 'observerSamplingLimits': grade.OBSERVER.sampling_limits()}
        cases = [(metadata, {}, policy), ({}, metadata, policy),
                 (metadata, metadata, None),
                 (metadata, {**metadata, 'observerSamplingLimits': {**grade.OBSERVER.sampling_limits(), 'loadBudgetMs': 4000.0}}, policy)]
        for api, active, selected in cases:
            with self.subTest(api=api, active=active, selected=selected), \
                 patch.object(grade, 'execution_context', return_value={'active': active}), \
                 patch.object(grade, 'authority_reservation') as authority:
                with self.assertRaises(ValueError):
                    self.reserve('search', 5000, api=api, observer_policy=selected)
                authority.assert_not_called()
        with patch.object(grade, 'execution_context', return_value=None), self.assertRaises(ValueError):
            self.reserve('search', 5000, api=metadata, observer_policy=policy)

    def test_actual_vector_formal_requires_the_same_explicit_policy_and_limits_in_freeze(self):
        policy = grade.OBSERVER.environment_policy({'CAP10_OBSERVER_MODE': 'like-vector-v1'})
        metadata = {'observerPolicy': policy, 'observerSamplingLimits': grade.OBSERVER.sampling_limits()}
        with patch.object(grade, 'execution_context', return_value={'active': metadata}), \
             patch.object(grade, 'authority_reservation', return_value={'tag': 'H2'}) as authority:
            for freeze in ({}, {'observerPolicy': policy},
                           {**metadata, 'observerPolicy': grade.OBSERVER.environment_policy({})}):
                with self.subTest(freeze=freeze), self.assertRaises(ValueError):
                    self.reserve('formal', 5000, api=metadata, observer_policy=policy, freeze=freeze)
            authority.assert_not_called()
            self.assertEqual(self.reserve('formal', 5000, api=metadata, observer_policy=policy, freeze=metadata)['tag'], 'H2')

    def test_new_legacy_metadata_requires_integer_limits_but_old_ready_stays_legacy(self):
        legacy = grade.OBSERVER.environment_policy({})
        self.assertEqual(grade.observer_identity(legacy, {'ready': True})['observerPolicy'], legacy)
        for metadata in ({'observerPolicy': legacy}, {'observerPolicy': None},
                         {'observerPolicy': legacy, 'observerSamplingLimits': {**grade.OBSERVER.sampling_limits(), 'cadenceSeconds': 5.0}}):
            with self.subTest(metadata=metadata), self.assertRaises(ValueError):
                grade.observer_identity(legacy, metadata)
        vector = grade.OBSERVER.environment_policy({'CAP10_OBSERVER_MODE': 'like-vector-v1'})
        with self.assertRaises(ValueError): grade.observer_identity(vector, {'ready': True})

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
        pool = context['dimensions']['poolSize']
        old_stage = grade.stage(directory, previous)
        terminal = {'tag': previous['tag'], 'reports': [{'phase': phase, 'reportId': previous['tag'] + '-' + phase,
            'ended': True, 'startTimeRaw': '2026-11-02 00:00:00', 'endTimeRaw': '2026-11-02 00:01:00'} for phase in ('warm', 'stable')],
            'owners': [{**p, 'alive': False, 'identityChecked': True} for p in old_stage.get('knownOwnedProcesses', [])],
            'observerAlive': False, 'controllerAlive': False, 'wrapperAlive': False}
        terminal_path = self.put('linux/terminal-before-' + tag + '.json', terminal)
        finished = max(self.now + dt.timedelta(seconds=20), grade.instant(old_stage['finishedAt']) + dt.timedelta(milliseconds=2))
        reconciliation = {'valid': True, 'factsValid': True, 'downstreamConverged': True, 'errors': [],
            'startedAt': int((finished - dt.timedelta(milliseconds=1)).timestamp() * 1000), 'finishedAt': int(finished.timestamp() * 1000),
            'facts': {'facts': [{}] * pool, 'notes': [{}] * pool, 'transitions': 0, 'eventCount': 0},
            'derived': {'available': True, 'errors': [], 'notes': [{}] * pool},
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
        command.update({key: active[key] for key in ('poolSize', 'expectedEngines', 'ptsConcurrency', 'environmentPath', 'resourceProfile') if key in active})
        self.put('linux/' + tag + '-command.json', command)
        return command

    def large_active(self, directory, active):
        active = copy.deepcopy(active)
        pool, engines = 4096, 9
        dimensions = {'poolSize': pool, 'expectedEngines': engines, 'ptsConcurrency': 4105}
        helpers = {'scripts/cap10-cohort.py': '1' * 64, 'scripts/load/cap10-cohort-contract.mjs': '2' * 64}
        active['scriptHashes'].update({key.removeprefix('scripts/'): value for key, value in helpers.items()})
        self.tools = active['scriptHashes'].copy()
        fixture = self.put('linux/fixture-large.json', {'poolSize': pool, 'users': pool, 'multiNotes': pool, 'multiRelations': pool})
        packet = self.put('linux/packet-large.json', {'sceneFilesMD5': {'plan': 'large-frozen'}, 'cohortSourceSHA256': helpers,
            'manifest': {**dimensions, 'normalThreads': pool,
                         'cohortSourceSHA256': {'scripts/load/cap10-cohort-contract.mjs': helpers['scripts/load/cap10-cohort-contract.mjs']}}})
        admission = self.put('linux/admission-large.json', {'passed': True, 'sceneFilesMD5': {'plan': 'large-frozen'},
            'sceneId': 'LARGE-SCENE', 'agentCount': engines, 'concurrency': 4105,
            **dimensions, 'cohortSourceSHA256': helpers})
        active.update(poolSize=pool, expectedEngines=engines, ptsConcurrency=4105, deploymentId='new-explicit-cohort',
            fixturePath=fixture.name, fixtureSHA256=grade.sha(fixture), ptsPacketPath=packet.name,
            ptsPacketSHA256=grade.sha(packet), ptsAdmissionPath=admission.name, ptsAdmissionSHA256=grade.sha(admission))
        self.put('linux/active-application.json', active)
        return active

    def resource_active(self, directory, api, active):
        active = copy.deepcopy(active)
        path = self.put('linux/environment-012345abcdef.json', {'resourceProfile': 'mysql8-v1', 'synthetic': 'explicit new resource metadata'})
        identity = {'environmentPath': path.name, 'environmentSHA256': grade.sha(path), 'resourceProfile': 'mysql8-v1'}
        active.update(identity, deploymentId='mysql8-resource-deployment')
        self.put('linux/active-application.json', active)
        preflight, _ = idle_checks.versioned_preflight_fixture(directory, identity)
        return {**api, **identity}, active, preflight

    def test_environment_identity_never_follows_current_versioned_metadata_for_old_records(self):
        original = (self.root / 'environment.json').read_bytes()
        old = {'environmentSHA256': grade.sha(self.root / 'environment.json')}
        new_path = self.put('environment-012345abcdef.json', {'resourceProfile': 'mysql8-v1'})
        new = {'environmentPath': new_path.name, 'environmentSHA256': grade.sha(new_path), 'resourceProfile': 'mysql8-v1'}
        self.assertEqual(grade.environment_identity(old, self.root), (self.root / 'environment.json', old['environmentSHA256'], 'baseline'))
        self.assertEqual(grade.environment_identity(new, self.root), (new_path, new['environmentSHA256'], 'mysql8-v1'))
        for change in ({'environmentPath': new_path.name}, {'environmentSHA256': new['environmentSHA256']}, {'resourceProfile': 'mysql8-v1'}):
            with self.subTest(change=change), self.assertRaises(ValueError):
                grade.environment_identity({**old, **change}, self.root)
        for name in ('../environment.json', 'nested/environment.json', 'environment-wrong.json'):
            with self.subTest(name=name), self.assertRaises(ValueError):
                grade.environment_identity({**new, 'environmentPath': name}, self.root)
        self.assertEqual((self.root / 'environment.json').read_bytes(), original)

    def test_explicit_resource_reservation_requires_api_active_and_idle_path_sha_and_profile(self):
        directory, api, active = self.authority_fixture()
        api, active, preflight = self.resource_active(directory, api, active)
        self.authority_admission(directory, 'S7')
        self.assertEqual(self.reserve('comparison', 800, source=self.other, execution_root=directory, api=api,
                                     preflight=preflight)['tag'], 'S7')
        for api_change, idle_change in (
            ({'environmentPath': 'environment.json', 'resourceProfile': 'baseline'}, {}),
            ({'environmentSHA256': 'f' * 64}, {}), ({'resourceProfile': 'baseline'}, {}),
            ({}, {'environmentPath': 'environment.json'}), ({}, {'environmentSHA256': 'f' * 64}),
            ({}, {'resourceProfile': 'baseline'}),
        ):
            with self.subTest(api=api_change, idle=idle_change), self.assertRaises(ValueError):
                self.reserve('comparison', 800, source=self.other, execution_root=directory,
                             api={**api, **api_change}, preflight={**preflight, **idle_change})
        # A byte-identical versioned file still denotes a distinct explicit source.
        same = directory / 'environment-abcdef012345.json'
        same.write_bytes((directory / active['environmentPath']).read_bytes())
        with self.assertRaises(ValueError):
            self.reserve('comparison', 800, source=self.other, execution_root=directory,
                         api={**api, 'environmentPath': same.name}, preflight={**preflight, 'environmentPath': same.name})
        with self.assertRaises(ValueError):
            self.reserve('comparison', 800, source=self.other, execution_root=directory,
                         api={k: v for k, v in api.items() if k != 'environmentSHA256'}, preflight=preflight)

    def test_only_original_baseline_api_can_omit_environment_sha(self):
        self.assertEqual(grade.environment_identity({}, self.root, require_sha=False)[0], self.root / 'environment.json')
        for name, metadata, profile in (
            ('environment-012345abcdef.json', {}, 'baseline'),
            ('environment-abcdef012345.json', {'resourceProfile': 'mysql8-v1'}, 'mysql8-v1'),
            ('environment.json', {'resourceProfile': 'mysql8-v1'}, 'mysql8-v1'),
        ):
            self.put(name, metadata)
            with self.subTest(name=name, profile=profile), self.assertRaises(ValueError):
                grade.environment_identity({'environmentPath': name, 'resourceProfile': profile}, self.root, require_sha=False)

    def test_new_resource_requires_original_idle_reference_before_reservation(self):
        directory, api, active = self.authority_fixture()
        api, active, preflight = self.resource_active(directory, api, active)
        self.authority_admission(directory, 'S7')
        options = {'source': self.other, 'execution_root': directory, 'api': api, 'preflight': preflight}
        with self.assertRaises(ValueError):
            self.reserve('comparison', 800, preflight_reference=None, **options)
        result_path = directory / preflight['idleDirectory'] / 'idle-result.json'
        original = result_path.read_bytes()
        result_path.write_text(json.dumps({**preflight, 'samples': preflight['samples'][:-1]}), encoding='utf-8')
        with self.assertRaises(ValueError):
            self.reserve('comparison', 800, **options)
        result_path.write_bytes(original)
        self.assertEqual(self.reserve('comparison', 800, **options)['tag'], 'S7')

    def test_resource_formal_candidate_and_freeze_cannot_mix_metadata_paths(self):
        directory, api, active = self.authority_fixture()
        api, active, preflight = self.resource_active(directory, api, active)
        self.authority_admission(directory, 'S7')
        options = {'source': self.other, 'execution_root': directory, 'api': api, 'preflight': preflight}
        reserved = self.reserve('candidate', 800, **options)
        self.authority_add(directory, 'S7', 'candidate', 800, reserved, active)
        context = grade.execution_context(directory)
        freeze = {**active, 'rate': 800, 'candidateTag': 'S7', 'sourceCIPassed': True, 'relatedChecksPassed': True,
            'initialDistributionContract': {'existing': 512, 'active': 0, 'relationships': 512},
            'executionAuthoritySHA256': context['authoritySHA256'], 'activeApplicationSHA256': context['activeApplicationSHA256']}
        self.put('freeze-like.json', freeze); self.put('linux/freeze-like.json', freeze)
        self.authority_admission(directory, 'H1')
        self.assertEqual(self.reserve('formal', 800, freeze=freeze, **options)['tag'], 'H1')
        same = directory / 'environment-abcdef012345.json'; same.write_bytes((directory / active['environmentPath']).read_bytes())
        with self.assertRaises(ValueError):
            self.reserve('formal', 800, freeze={**freeze, 'environmentPath': same.name}, **options)
        command = grade.read(directory / 'S7-command.json'); command['environmentPath'] = same.name
        self.assertFalse(grade._active_command(command, context))

    def test_old_retirement_environment_stays_original_after_new_resource_profile(self):
        directory, api, active, command, proof = self.formal_start_recovery_fixture()
        old_environment = (directory / 'environment.json').read_bytes()
        _, active, _ = self.resource_active(directory, api, active)
        context = grade.execution_context(directory)
        proof['successorActiveApplicationSHA256'] = context['activeApplicationSHA256']
        self.put('linux/formal-start-recovery-H1.json', proof)
        self.assertEqual(grade.formal_start_retirement(directory, command, context)['tag'], 'H1')
        self.assertEqual((directory / 'environment.json').read_bytes(), old_environment)
        old_sha = command['reservationCheck']['activeApplicationSHA256']
        original = grade.read(directory / ('active-applications/' + old_sha + '.json'))
        with self.assertRaises(ValueError):
            grade.original_command_dimensions(directory, {**command, 'environmentPath': active['environmentPath']}, original)

    def test_explicit_packet_and_admission_reject_tampered_dimensions_or_helper_freeze(self):
        directory, _, active = self.authority_fixture()
        active = self.large_active(directory, active)
        packet_path, admission_path = directory / active['ptsPacketPath'], directory / active['ptsAdmissionPath']
        packet_bytes, admission_bytes = packet_path.read_bytes(), admission_path.read_bytes()
        for artifact, field, wrong in (
            ('manifest', 'poolSize', 512), ('manifest', 'expectedEngines', 2), ('manifest', 'ptsConcurrency', 513),
            ('manifest', 'cohortSourceSHA256', {'scripts/load/cap10-cohort-contract.mjs': '3' * 64}),
            ('packet', 'cohortSourceSHA256', {'scripts/cap10-cohort.py': '3' * 64, 'scripts/load/cap10-cohort-contract.mjs': '2' * 64}),
            ('admission', 'poolSize', 512), ('admission', 'expectedEngines', 2), ('admission', 'ptsConcurrency', 513),
            ('admission', 'cohortSourceSHA256', None),
        ):
            with self.subTest(artifact=artifact, field=field):
                packet, admission = json.loads(packet_bytes), json.loads(admission_bytes)
                target = packet['manifest'] if artifact == 'manifest' else packet if artifact == 'packet' else admission
                target[field] = wrong
                packet_path.write_text(json.dumps(packet), encoding='utf-8')
                admission_path.write_text(json.dumps(admission), encoding='utf-8')
                self.put('linux/active-application.json', {**active, 'ptsPacketSHA256': grade.sha(packet_path),
                                                          'ptsAdmissionSHA256': grade.sha(admission_path)})
                with self.assertRaises(ValueError): grade.execution_context(directory)
        packet_path.write_bytes(packet_bytes); admission_path.write_bytes(admission_bytes)
        bad = copy.deepcopy(active); bad['scriptHashes']['cap10-cohort.py'] = '3' * 64
        self.put('linux/active-application.json', bad)
        with self.assertRaises(ValueError): grade.execution_context(directory)

    def test_closed_history_reads_original_manifest_reference_without_root_fixture(self):
        # Match the real archive layout: the command has a hash, the retained
        # tool-repair proof supplies like-fixture/fixture.json, and no root alias.
        directory = self.root / 'linux'; directory.mkdir()
        manifest = self.put('linux/like-fixture/fixture.json', {'users': 512, 'multiNotes': 512, 'multiRelations': 512})
        environment = self.put('linux/environment.json', {'synthetic': 'retained original environment'})
        command = {'tag': 'S5', 'fixtureSHA256': grade.sha(manifest), 'environmentSHA256': grade.sha(environment)}
        repair = self.put('linux/tool-repair-transition.json', {'artifacts': {'fixtureManifest':
            {'path': 'like-fixture/fixture.json', 'sha256': grade.sha(manifest)}}})
        self.assertFalse((directory / 'fixture.json').exists())
        self.assertEqual(grade.legacy_command_pool(directory, command, repair_sha=grade.sha(repair)), 512)
        for edits in ({'fixtureSHA256': 'f' * 64}, {'poolSize': 4096}):
            with self.subTest(edits=edits), self.assertRaises(ValueError):
                grade.legacy_command_pool(directory, {**command, **edits}, repair_sha=grade.sha(repair))
        with self.assertRaises(ValueError): grade.legacy_command_pool(directory, command)
        with self.assertRaises(ValueError): grade.legacy_command_pool(directory, command, repair_sha='f' * 64)

    def test_explicit_large_cohort_reservation_rejects_old_ability_and_incomplete_current_reconciliation(self):
        directory, api, active = self.authority_fixture()
        active = self.large_active(directory, active)
        self.authority_admission(directory, 'S7')
        ability = {'maxVUs': 4096, 'exclusiveRelationships': 4096}
        result = self.reserve('comparison', 800, source=self.other, execution_root=directory, api=api,
                              fixture_name='fixture-large.json', ability=ability)
        self.assertEqual(result['poolSize'], 4096)
        for change in ({'maxVUs': 512}, {'exclusiveRelationships': 512}, {'estimatedInFlight': 4097},
                       {'estimatedInFlight': True}, {'estimatedInFlight': float('nan')}, {'estimatedInFlight': float('inf')}):
            with self.subTest(change=change), self.assertRaises(ValueError):
                self.reserve('comparison', 800, source=self.other, execution_root=directory, api=api,
                             fixture_name='fixture-large.json', ability={**ability, **change})
        admission = grade.read(directory / 'admission-S7.json')
        path = directory / admission['reconciliation']['path']; reconciliation = grade.read(path)
        reconciliation['facts']['facts'].pop()
        path.write_text(json.dumps(reconciliation), encoding='utf-8')
        admission['reconciliation']['sha256'] = grade.sha(path); self.put('linux/admission-S7.json', admission)
        with self.assertRaises(ValueError):
            self.reserve('comparison', 800, source=self.other, execution_root=directory, api=api,
                         fixture_name='fixture-large.json', ability=ability)

    def test_new_context_requires_explicit_fixture_and_scene_dimensions(self):
        directory, _, active = self.authority_fixture()
        active = self.large_active(directory, active)
        for field, wrong in (('poolSize', 512), ('expectedEngines', 2), ('ptsConcurrency', 4104)):
            with self.subTest(field=field), self.assertRaises(ValueError):
                self.put('linux/active-application.json', {**active, field: wrong})
                grade.execution_context(directory)
        self.put('linux/active-application.json', active)
        admission = grade.read(directory / active['ptsAdmissionPath']); admission['agentCount'] = 2
        path = self.put('linux/' + active['ptsAdmissionPath'], admission)
        self.put('linux/active-application.json', {**active, 'ptsAdmissionSHA256': grade.sha(path)})
        with self.assertRaises(ValueError): grade.execution_context(directory)

    def test_large_formal_freeze_retains_its_own_candidate_and_pool(self):
        directory, api, active = self.authority_fixture()
        active = self.large_active(directory, active)
        self.authority_admission(directory, 'S7')
        options = {'source': self.other, 'execution_root': directory, 'api': api,
                   'fixture_name': 'fixture-large.json', 'ability': {'maxVUs': 4096, 'exclusiveRelationships': 4096}}
        reserved = self.reserve('candidate', 800, **options)
        self.authority_add(directory, 'S7', 'candidate', 800, reserved, active)
        context = grade.execution_context(directory)
        freeze = {**active, 'rate': 800, 'candidateTag': 'S7', 'sourceCIPassed': True, 'relatedChecksPassed': True,
            'initialDistributionContract': {'existing': 4096, 'active': 0, 'relationships': 4096},
            'executionAuthoritySHA256': context['authoritySHA256'], 'activeApplicationSHA256': context['activeApplicationSHA256']}
        self.put('freeze-like.json', freeze); self.put('linux/freeze-like.json', freeze)
        self.authority_admission(directory, 'H1')
        self.assertEqual(self.reserve('formal', 800, freeze=freeze, **options)['poolSize'], 4096)
        for change in ({'poolSize': 512}, {'initialDistributionContract': {'existing': 512, 'active': 0, 'relationships': 512}}):
            with self.subTest(change=change), self.assertRaises(ValueError):
                self.reserve('formal', 800, freeze={**freeze, **change}, **options)
        command = grade.read(directory / 'S7-command.json'); command['poolSize'] = 512
        self.assertFalse(grade._active_command(command, context))

    def test_old_formal_retirement_keeps_original_512_fixture_under_large_successor(self):
        directory, _, active, command, proof = self.formal_start_recovery_fixture()
        original_paths = [directory / name for name in ('H1-command.json', 'H1-stage.json', 'fixture.json', proof['nativeJournal']['path'])]
        originals = {path: path.read_bytes() for path in original_paths}
        self.large_active(directory, active)
        context = grade.execution_context(directory)
        proof['successorActiveApplicationSHA256'] = context['activeApplicationSHA256']
        self.put('linux/formal-start-recovery-H1.json', proof)
        self.assertEqual(grade.formal_start_retirement(directory, command, context)['tag'], 'H1')
        self.assertTrue(all(path.read_bytes() == raw for path, raw in originals.items()))
        before_path = directory / proof['beforeWarm']['path']; before = grade.read(before_path)
        reconciliation_path = directory / proof['reconciliation']['path']; reconciliation = grade.read(reconciliation_path)
        before['facts'] = [{}] * 4096; before['notes'] = [{}] * 4096
        reconciliation['facts'] = before; reconciliation['derived']['notes'] = [{}] * 4096
        before_path.write_text(json.dumps(before), encoding='utf-8')
        reconciliation_path.write_text(json.dumps(reconciliation), encoding='utf-8')
        proof['beforeWarm']['sha256'] = grade.sha(before_path); proof['reconciliation']['sha256'] = grade.sha(reconciliation_path)
        self.put('linux/formal-start-recovery-H1.json', proof)
        with self.assertRaises(ValueError): grade.formal_start_retirement(directory, command, context)

    def test_original_command_cannot_borrow_successor_pool_or_engine_count(self):
        directory, _, active, command, _ = self.formal_start_recovery_fixture()
        old_sha = command['reservationCheck']['activeApplicationSHA256']
        original = grade.read(directory / ('active-applications/' + old_sha + '.json'))
        self.large_active(directory, active)
        self.assertEqual(grade.original_command_dimensions(directory, command, original),
                         {'poolSize': 512, 'expectedEngines': 2, 'ptsConcurrency': 513})
        for change in ({'poolSize': 4096}, {'expectedEngines': 9}, {'ptsConcurrency': 4105}):
            with self.subTest(change=change), self.assertRaises(ValueError):
                grade.original_command_dimensions(directory, {**command, **change}, original)

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

    def authority_warm_fixture(self):
        directory, api, active = self.authority_fixture()
        self.put('linux/admission.json', {'passed': True, 'sceneFilesMD5': {'plan': 'frozen'}, 'sceneId': 'SCENE'})
        active['ptsAdmissionSHA256'] = grade.sha(directory / 'admission.json')
        self.put('linux/active-application.json', active); self.authority_admission(directory, 'S7')
        reservation = self.reserve('candidate', 800, source=self.other, execution_root=directory, api=api)
        command = self.authority_add(directory, 'S7', 'candidate', 800, reservation, active, 'measurement_invalid')
        command['loadBackend'] = 'pts'; self.put('linux/S7-command.json', command)
        previous = grade.read(directory / 'S7-stage.json')
        previous.update(wrapperExit=1, observerExit=0, wrapperPid=45678, recoveryVerified=False,
                        knownOwnedProcesses=[{'pid': 45678, 'created': 456.5}])
        self.put('linux/S7-stage.json', previous)
        self.put('linux/S7-load/result.json', {'loadBackend': 'pts', 'warmExit': 1,
            'nativeScenario': {'complete': False}, 'stableExit': None, 'setup': None,
            'stableStartedAt': None, 'result': None, 'failure': 'warm failed; stable phase not started'})
        marker = self.put('linux/S7-load/warm-process.json', {'phase': 'warm', 'backend': 'pts',
            'controller_pid': 45678, 'controller_started_unix_ms': 456500})
        self.put('linux/S7-load/process.json', grade.read(marker))
        start = self.put('linux/S7-load/warm-pts/api/one-StartTestingJMeterScene.json',
            {'action': 'StartTestingJMeterScene', 'sceneId': 'SCENE', 'httpStatus': 200,
             'sanitizedRawResponse': {'Success': True, 'ReportId': 'WARM_ONLY'}})
        admission_path = self.authority_admission(directory, 'S8')
        report = {'reportId': 'WARM_ONLY', 'ended': True, 'startTimeRaw': '2026-11-02 00:00:00',
                  'endTimeRaw': '2026-11-02 00:01:00', 'requestId': 'report-request'}
        report_path = self.put('linux/warm-real-report.json', report)
        report_audit = self.put('linux/warm-report-api.json', {'action': 'GetJMeterReportDetails', 'httpStatus': 200,
            'sanitizedRawResponse': {'Success': True, 'RequestId': 'report-request',
                'ReportOverView': {'ReportId': 'WARM_ONLY', 'StartTime': report['startTimeRaw'], 'EndTime': report['endTimeRaw']}}})
        terminal_path = directory / 'terminal-before-S8.json'; terminal = grade.read(terminal_path)
        ref = lambda p: {'path': p.relative_to(directory).as_posix(), 'sha256': grade.sha(p)}
        terminal.update(terminationKind='warm_only', reports=[{'phase': 'warm', **report, 'originalStartAudit': ref(start)}],
            originalCommand=ref(directory / 'S7-command.json'), originalStage=ref(directory / 'S7-stage.json'),
            originalResult=ref(directory / 'S7-load/result.json'), warmProcess=ref(marker),
            warmReport=ref(report_path), warmReportAudit=ref(report_audit),
            noStablePaths=grade.warm_only_layout(directory, command, previous))
        self.put('linux/terminal-before-S8.json', terminal)
        admission = grade.read(admission_path); admission['termination']['sha256'] = grade.sha(terminal_path)
        self.put('linux/admission-S8.json', admission)
        return directory, api, active, command, previous, terminal

    def test_authority_warm_only_accepts_real_single_report_without_inventing_stable(self):
        directory, api, active, command, previous, terminal = self.authority_warm_fixture()
        originals = {path: path.read_bytes() for path in (directory / 'S7-command.json', directory / 'S7-stage.json', directory / 'S7-load/result.json')}
        reservation = self.reserve('candidate', 800, source=self.other, execution_root=directory, api=api)
        self.assertEqual(reservation['tag'], 'S8')
        self.assertEqual([r['phase'] for r in terminal['reports']], ['warm'])
        self.assertEqual(grade.search_state(directory, self.other)['boundaryTags'], [])
        self.assertTrue(all(path.read_bytes() == original for path, original in originals.items()))

    def test_authority_warm_only_rejects_stable_setup_and_second_start_evidence(self):
        directory, api, active, command, previous, terminal = self.authority_warm_fixture()
        for relative in ('stable-process.json', 'stable-pts/api/start-StartTestingJMeterScene.json',
                         'stable-summary.json', 'before.json', 'setup-transaction.json', 'stable-other-proof.json'):
            with self.subTest(path=relative):
                path = self.put('linux/S7-load/' + relative, {})
                self.assertIsNone(grade.warm_only_layout(directory, command, previous))
                path.unlink()
                if (directory / 'S7-load/stable-pts').exists():
                    (directory / 'S7-load/stable-pts/api').rmdir(); (directory / 'S7-load/stable-pts').rmdir()
        result_path = directory / 'S7-load/result.json'; original = result_path.read_bytes()
        for changes in ({'warmExit': 0}, {'stableExit': 1}, {'setup': {}}, {'stableStartedAt': 123}, {'nativeScenario': {'complete': True}}):
            self.put('linux/S7-load/result.json', {**json.loads(original), **changes})
            self.assertIsNone(grade.warm_only_layout(directory, command, previous))
        result_path.write_bytes(original)
        extra = self.put('linux/S7-load/warm-pts/api/two-StartTestingJMeterScene.json', {})
        self.assertIsNone(grade.warm_only_layout(directory, command, previous)); extra.unlink()

    def test_authority_warm_only_rejects_wrong_report_hash_live_owner_or_missing_reconcile(self):
        directory, api, active, command, previous, terminal = self.authority_warm_fixture()
        for change in ({'reports': []}, {'reports': terminal['reports'] * 2},
                       {'originalResult': terminal['originalStage']}, {'warmReportAudit': terminal['warmReport']}):
            with self.assertRaises((ValueError, KeyError)):
                grade.authority_warm_only(directory, command, previous, {**terminal, **change}, {'active': active})
        for key, value in (('reportId', 'OTHER'), ('ended', False), ('endTimeRaw', '')):
            bad = {**terminal, 'reports': [{**terminal['reports'][0], key: value}]}
            with self.assertRaises((ValueError, KeyError)):
                grade.authority_warm_only(directory, command, previous, bad, {'active': active})
        bad = {**terminal, 'owners': [{**terminal['owners'][0], 'alive': True}]}
        with self.assertRaises(ValueError): grade.authority_warm_only(directory, command, previous, bad, {'active': active})
        admission = grade.read(directory / 'admission-S8.json'); admission.pop('reconciliation')
        self.put('linux/admission-S8.json', admission)
        with self.assertRaises((ValueError, KeyError)):
            self.reserve('candidate', 800, source=self.other, execution_root=directory, api=api)

    def authority_missing_warm_result_fixture(self):
        directory, api, active, command, previous, terminal = self.authority_warm_fixture()
        (directory / 'S7-load/result.json').unlink()
        previous.update(outcome='correctness_stop', failedStartOrException='FileNotFoundError',
                        childrenConverged=False, batchFatal=True)
        path = self.put('linux/S7-stage.json', previous)
        terminal.update(originalStage={'path': path.name, 'sha256': grade.sha(path)}, originalResult=None,
                        missingOriginalResult={'path': 'S7-load/result.json', 'exists': False},
                        noStablePaths=grade.warm_only_layout(directory, command, previous))
        terminal_path = self.put('linux/terminal-before-S8.json', terminal)
        admission = grade.read(directory / 'admission-S8.json')
        admission['termination']['sha256'] = grade.sha(terminal_path)
        self.put('linux/admission-S8.json', admission)
        return directory, api, active, command, previous, terminal

    def test_authority_warm_missing_result_accepts_ended_warm_without_creating_result(self):
        directory, api, active, command, previous, terminal = self.authority_missing_warm_result_fixture()
        originals = {path: path.read_bytes() for path in (directory / 'S7-command.json',
            directory / 'S7-stage.json', directory / 'S7-load/warm-process.json')}
        self.assertEqual(terminal['noStablePaths']['missingResult'], terminal['missingOriginalResult'])
        reservation = self.reserve('candidate', 800, source=self.other, execution_root=directory, api=api)
        self.assertEqual(reservation['tag'], 'S8')
        self.assertEqual(grade.search_state(directory, self.other)['boundaryTags'], [])
        self.assertFalse((directory / 'S7-load/result.json').exists())
        self.assertFalse((directory / 'S7-load/stable-pts').exists())
        self.assertTrue(all(path.read_bytes() == raw for path, raw in originals.items()))

    def test_authority_warm_missing_result_requires_exact_absence_and_original_failure(self):
        directory, api, active, command, previous, terminal = self.authority_missing_warm_result_fixture()
        for change in ({'originalResult': terminal['originalStage']}, {'missingOriginalResult': None},
                {'missingOriginalResult': {'path': 'S8-load/result.json', 'exists': False}},
                {'missingOriginalResult': {'path': 'S7-load/result.json', 'exists': True}}):
            with self.subTest(change=change), self.assertRaises(ValueError):
                grade.authority_warm_only(directory, command, previous, {**terminal, **change}, {'active': active})
        for change in ({'outcome': 'performance_limited'}, {'failedStartOrException': 'OtherError'},
                {'childrenConverged': True}, {'batchFatal': False}, {'knownOwnedProcesses': []},
                {'knownOwnedProcesses': [{'pid': 99999, 'created': 456.5}]}):
            self.assertIsNone(grade.warm_only_layout(directory, command, {**previous, **change}))
        result = self.put('linux/S7-load/result.json', {'loadBackend': 'pts', 'warmExit': 1,
            'nativeScenario': {'complete': False}, 'result': None})
        with self.assertRaises(ValueError):
            grade.authority_warm_only(directory, command, previous, terminal, {'active': active})
        result.unlink()

    def test_authority_warm_missing_result_uses_registered_birth_not_spawn_wall_clock(self):
        directory, api, active, command, previous, terminal = self.authority_zero_native_cancel_fixture()
        marker_path = directory / 'S7-load/warm-process.json'; marker = grade.read(marker_path)
        # Actual S18 controller: birth 1793628864.25, Node spawn stamp
        # 1793628865063ms. These differ by .813s and are different clocks.
        marker['controller_started_unix_ms'] = (previous['knownOwnedProcesses'][0]['created'] + .813) * 1000
        marker_path.write_text(json.dumps(marker), encoding='utf-8')
        self.put('linux/S7-load/process.json', marker)
        terminal['warmProcess']['sha256'] = grade.sha(marker_path)
        self.assertIsNotNone(grade.warm_only_layout(directory, command, previous))
        self.assertTrue(grade.authority_warm_only(directory, command, previous, terminal, {'active': active}))
        wrong = {**terminal, 'owners': [{**terminal['owners'][0],
            'created': marker['controller_started_unix_ms'] / 1000}]}
        with self.assertRaises(ValueError):
            grade.authority_warm_only(directory, command, previous, wrong, {'active': active})
        path = self.put('linux/terminal-before-S8.json', terminal)
        admission = grade.read(directory / 'admission-S8.json'); admission['termination']['sha256'] = grade.sha(path)
        self.put('linux/admission-S8.json', admission)
        self.assertEqual(self.reserve('candidate', 800, source=self.other, execution_root=directory, api=api)['tag'], 'S8')

    def test_authority_warm_missing_result_rejects_setup_owner_report_and_freshness_gaps(self):
        directory, api, active, command, previous, terminal = self.authority_missing_warm_result_fixture()
        for relative in ('stable-console.txt', 'setup-extra.json', 'setup/new-proof.json', 'before.json'):
            path = self.put('linux/S7-load/' + relative, {})
            self.assertIsNone(grade.warm_only_layout(directory, command, previous))
            path.unlink()
            if path.parent.name == 'setup':
                path.parent.rmdir()
        for change in ({'owners': []}, {'controllerAlive': True},
                {'owners': [{**terminal['owners'][0], 'alive': True}]},
                {'reports': []}, {'warmReportAudit': terminal['warmReport']},
                {'reports': [{**terminal['reports'][0], 'reportId': 'OTHER'}]}):
            with self.subTest(change=change), self.assertRaises((ValueError, KeyError)):
                grade.authority_warm_only(directory, command, previous, {**terminal, **change}, {'active': active})
        admission = grade.read(directory / 'admission-S8.json'); admission.pop('reconciliation')
        self.put('linux/admission-S8.json', admission)
        with self.assertRaises((ValueError, KeyError)):
            self.reserve('candidate', 800, source=self.other, execution_root=directory, api=api)

    def authority_zero_native_cancel_fixture(self):
        directory, api, active, command, previous, terminal = self.authority_missing_warm_result_fixture()
        names = ('cap10-pts-prepare.py', 'cap10-jmeter.mjs', 'cap10-jmeter.groovy', 'cap10-pts-contract.groovy')
        raw_sources = {name: (REPO / 'scripts/pts' / name).read_bytes() for name in names}
        sources = {name: hashlib.sha256(raw).hexdigest() for name, raw in raw_sources.items()}
        bundle = raw_sources['cap10-pts-contract.groovy'] + b'\n' + raw_sources['cap10-jmeter.groovy']
        bundle_hash = hashlib.sha256(bundle).hexdigest()
        self.assertNotEqual(bundle_hash, sources['cap10-jmeter.groovy'])
        packet = {'version': 1, 'sourceSHA256': sources,
            'manifest': {'version': 1, 'generator': 'pts-jmeter', 'scriptSHA256': bundle_hash,
                'jmxSHA256': hashlib.sha256(b'synthetic frozen JMX').hexdigest(),
                'standardJMeter': '5.6.3', 'normalThreads': 512, 'teardownThreads': 1,
                'ptsConcurrency': 513, 'csvSplitRequired': True}, 'sceneFilesMD5': {'plan': 'frozen'}}
        packet_path = self.put('linux/packet.json', packet)
        command.update(warmRate=25, warmSeconds=10,
                       ptsPacketSHA256=grade.sha(packet_path),
                       scriptHashes={**command['scriptHashes'], **{'pts/' + name: digest for name, digest in sources.items()}})
        active['ptsPacketSHA256'] = grade.sha(packet_path)
        active['scriptHashes'] = command['scriptHashes'].copy()
        self.tools = active['scriptHashes'].copy()
        self.put('linux/active-application.json', active)
        # Fixture construction finishes before reserving this synthetic command:
        # retain the final packet/tool application identity, not the earlier stub.
        command['reservationCheck']['activeApplicationSHA256'] = grade.sha(directory / 'active-application.json')
        command_path = self.put('linux/S7-command.json', command)
        ref = lambda p: {'path': p.relative_to(directory).as_posix(), 'sha256': grade.sha(p)}
        at = grade.instant(previous['finishedAt'])
        stamp = lambda seconds: (at + dt.timedelta(seconds=seconds)).isoformat()
        start_path = directory / terminal['noStablePaths']['warmStartAuditPath']
        start = grade.read(start_path)
        start.update(regionId='cn-shanghai', requestStartedAt=stamp(-10), responseFinishedAt=stamp(-9))
        start_path.write_text(json.dumps(start), encoding='utf-8')
        stop_path = self.put('linux/S7-load/warm-pts/api/one-StopTestingJMeterScene.json',
            {'action': 'StopTestingJMeterScene', 'regionId': 'cn-shanghai', 'sceneId': 'SCENE',
             'requestStartedAt': stamp(-2), 'responseFinishedAt': stamp(-1), 'httpStatus': 200,
             'sanitizedRawResponse': {'RequestId': 'stop-request', 'Success': True, 'Code': ''}})
        idle_paths = []
        for i in (1, 4):
            idle_paths.append(self.put(f'linux/cancel-idle-{i}.json',
                {'action': 'GetJMeterSceneRunningData', 'regionId': 'cn-shanghai', 'sceneId': 'SCENE',
                 'requestStartedAt': stamp(i), 'responseFinishedAt': stamp(i + .1), 'httpStatus': 200,
                 'sanitizedRawResponse': {'RequestId': 'idle-' + str(i), 'Success': True}}))
        identity = {'runId': 'never-started-native', 'phase': 'warm', 'rate': 25, 'seconds': 10,
                    'apiUrl': 'http://172.16.1.2:8281', 'expectedEngines': 2,
                    'fixtureSHA256': command['fixtureSHA256'], 'scriptSHA256': bundle_hash,
                    'version': 1, 'jitterBudgetMs': 50, 'clockUncertaintyMaxMs': 50}
        identity_hash = hashlib.sha256(json.dumps(identity, sort_keys=True, separators=(',', ':')).encode()).hexdigest()
        journal_path = directory / 'S7-load/warm-pts/records.ndjson'
        journal_path.write_text('\n'.join(json.dumps(row) for row in (
            {'kind': 'configuration', 'configuration': identity, 'hash': identity_hash},
            {'kind': 'stop', 'atUnixMs': int(at.timestamp() * 1000), 'reason': 'owned_supervisor_stop'})) + '\n', encoding='utf-8')
        summary = {**identity, 'hash': identity_hash, 'startUnixMs': None, 'endUnixMs': None,
                   'stop': True, 'stopReason': 'owned_supervisor_stop', 'complete': False, 'engines': [],
                   'counts': {'planned': 250, **{key: 0 for key in ('attempted', 'completed', 'mutation',
                       'noop', 'uncertain', 'rejected', 'invalid', 'dropped')}},
                   'evidenceSHA256': grade.sha(journal_path)}
        summary_path = self.put('linux/S7-load/warm-pts/latest.json', {'at': stamp(0), 'summary': summary})
        reconcile_path = directory / 'reconcile-before-S8.json'; reconciliation = grade.read(reconcile_path)
        reconciliation['facts']['events'] = []
        reconciliation.update(startedAt=int((at.timestamp() + 5) * 1000), finishedAt=int((at.timestamp() + 5.1) * 1000))
        reconcile_path.write_text(json.dumps(reconciliation), encoding='utf-8')
        before_path = self.put('linux/S7-load/warm-before.json', reconciliation['facts'])
        for key in ('warmReport', 'warmReportAudit'):
            terminal.pop(key)
        terminal.update(terminationKind='warm_zero_native_cancel', reports=[], cancelledReportId='WARM_ONLY',
            originalCommand=ref(command_path), originalStartAudit=ref(start_path), originalStopAudit=ref(stop_path),
            emptyRunningAudits=[ref(p) for p in idle_paths], cancellationObservedAt=stamp(6),
            nativeJournal=ref(journal_path), nativeSummary=ref(summary_path), beforeWarm=ref(before_path),
            cancellationReconciliation=ref(reconcile_path))
        terminal_path = self.put('linux/terminal-before-S8.json', terminal)
        admission = grade.read(directory / 'admission-S8.json')
        admission.update(recordedAt=stamp(7), termination=ref(terminal_path), reconciliation=ref(reconcile_path),
                         activeApplicationSHA256=grade.sha(directory / 'active-application.json'))
        self.put('linux/admission-S8.json', admission)
        return directory, api, active, command, previous, terminal

    def test_authority_zero_native_cancel_accepts_only_unregistered_stopped_warm(self):
        directory, api, active, command, previous, terminal = self.authority_zero_native_cancel_fixture()
        originals = {path: path.read_bytes() for path in (directory / 'S7-command.json',
            directory / 'S7-stage.json', directory / 'S7-load/warm-pts/records.ndjson')}
        reservation = self.reserve('candidate', 800, source=self.other, execution_root=directory, api=api)
        self.assertEqual(reservation['tag'], 'S8')
        self.assertEqual(terminal['reports'], [])
        self.assertFalse((directory / 'S7-load/result.json').exists())
        self.assertEqual(grade.search_state(directory, self.other)['boundaryTags'], [])
        self.assertTrue(all(path.read_bytes() == raw for path, raw in originals.items()))

    def test_authority_zero_native_cancel_binds_bundle_manifest_and_each_original_source_hash(self):
        directory, api, active, command, previous, terminal = self.authority_zero_native_cancel_fixture()
        packet_path = directory / active['ptsPacketPath']; original = packet_path.read_bytes()
        packet = json.loads(original)
        self.assertNotEqual(packet['manifest']['scriptSHA256'], command['scriptHashes']['pts/cap10-jmeter.groovy'])
        self.assertTrue(grade.authority_warm_only(directory, command, previous, terminal, {'active': active}))
        for mutate in (lambda p: p['manifest'].update(scriptSHA256=p['sourceSHA256']['cap10-jmeter.groovy']),
                       lambda p: p['sourceSHA256'].update({'cap10-pts-contract.groovy': '0' * 64}),
                       lambda p: p['sourceSHA256'].pop('cap10-jmeter.mjs')):
            invalid = copy.deepcopy(packet); mutate(invalid)
            packet_path.write_text(json.dumps(invalid), encoding='utf-8')
            # Even a separately re-bound packet cannot change the original source contract.
            changed = {**command, 'ptsPacketSHA256': grade.sha(packet_path)}
            command_path = directory / 'S7-command.json'; saved = command_path.read_bytes()
            command_path.write_text(json.dumps(changed), encoding='utf-8')
            bad = {**terminal, 'originalCommand': {'path': command_path.name, 'sha256': grade.sha(command_path)}}
            with self.assertRaises(ValueError):
                grade.authority_warm_only(directory, changed, previous, bad, {'active': active})
            command_path.write_bytes(saved)
        packet_path.write_bytes(original)
        packet_path.write_bytes(original + b' ')
        with self.assertRaises(ValueError):
            grade.authority_warm_only(directory, command, previous, terminal, {'active': active})
        packet_path.write_bytes(original)

    def test_authority_zero_native_cancel_rejects_any_registration_sample_or_changed_fact(self):
        directory, api, active, command, previous, terminal = self.authority_zero_native_cancel_fixture()
        for key, mutate in (
                ('nativeSummary', lambda v: v['summary'].update(engines=[{'engineId': 'registered'}])),
                ('nativeSummary', lambda v: v['summary'].update(startUnixMs=1)),
                ('nativeSummary', lambda v: v['summary']['counts'].update(attempted=1)),
                ('nativeSummary', lambda v: v['summary'].update(evidenceSHA256='0' * 64)),
                ('cancellationReconciliation', lambda v: v['facts'].update(eventCount=1)),
                ('cancellationReconciliation', lambda v: v['facts']['facts'][0].update(version=1))):
            path = directory / terminal[key]['path']; original = path.read_bytes()
            value = json.loads(original); mutate(value); path.write_text(json.dumps(value), encoding='utf-8')
            bad = {**terminal, key: {**terminal[key], 'sha256': grade.sha(path)}}
            with self.subTest(key=key), self.assertRaises(ValueError):
                grade.authority_warm_only(directory, command, previous, bad, {'active': active})
            path.write_bytes(original)
        path = directory / terminal['nativeJournal']['path']; original = path.read_bytes()
        path.write_bytes(original + b'{"kind":"register"}\n')
        bad = {**terminal, 'nativeJournal': {**terminal['nativeJournal'], 'sha256': grade.sha(path)}}
        with self.assertRaises(ValueError):
            grade.authority_warm_only(directory, command, previous, bad, {'active': active})
        path.write_bytes(original)

    def test_authority_zero_native_cancel_rejects_wrong_stop_running_or_later_start(self):
        directory, api, active, command, previous, terminal = self.authority_zero_native_cancel_fixture()
        for key, mutate in (
                ('originalStopAudit', lambda v: v['sanitizedRawResponse'].update(Code='Rejected')),
                ('originalStopAudit', lambda v: v.update(sceneId='OTHER')),
                ('originalStartAudit', lambda v: v['sanitizedRawResponse'].update(ReportId='OTHER'))):
            path = directory / terminal[key]['path']; original = path.read_bytes()
            value = json.loads(original); mutate(value); path.write_text(json.dumps(value), encoding='utf-8')
            bad = {**terminal, key: {**terminal[key], 'sha256': grade.sha(path)}}
            with self.subTest(key=key), self.assertRaises(ValueError):
                grade.authority_warm_only(directory, command, previous, bad, {'active': active})
            path.write_bytes(original)
        path = directory / terminal['emptyRunningAudits'][1]['path']; original = path.read_bytes()
        for mutate in (lambda v: v['sanitizedRawResponse'].update(RunningData={}),
                       lambda v: v.update(requestStartedAt=grade.read(directory / terminal['emptyRunningAudits'][0]['path'])['responseFinishedAt'])):
            value = json.loads(original); mutate(value); path.write_text(json.dumps(value), encoding='utf-8')
            bad = {**terminal, 'emptyRunningAudits': [terminal['emptyRunningAudits'][0],
                {**terminal['emptyRunningAudits'][1], 'sha256': grade.sha(path)}]}
            with self.assertRaises(ValueError):
                grade.authority_warm_only(directory, command, previous, bad, {'active': active})
        path.write_bytes(original)
        extra = self.put('linux/other-api/late-StartTestingJMeterScene.json',
            {'action': 'StartTestingJMeterScene', 'sceneId': 'SCENE', 'requestStartedAt': previous['finishedAt']})
        with self.assertRaises(ValueError):
            grade.authority_warm_only(directory, command, previous, terminal, {'active': active})
        extra.unlink()
        admission = grade.read(directory / 'admission-S8.json'); admission.pop('reconciliation')
        self.put('linux/admission-S8.json', admission)
        with self.assertRaises((ValueError, KeyError)):
            self.reserve('candidate', 800, source=self.other, execution_root=directory, api=api)

    def formal_start_recovery_fixture(self):
        directory, api, active, command, previous, terminal = self.authority_warm_fixture()
        sources = {name: 'c' * 64 for name in ('cap10-pts-prepare.py', 'cap10-jmeter.mjs', 'cap10-jmeter.groovy', 'cap10-pts-contract.groovy')}
        tools = {**active['scriptHashes'], **{'pts/' + key: value for key, value in sources.items()}}
        packet = {'version': 1, 'sceneFilesMD5': {'plan': 'frozen'}, 'sourceSHA256': sources, 'manifest': {'scriptSHA256': 'b' * 64}}
        self.put('linux/packet.json', packet)
        active.update(scriptHashes=tools, ptsPacketSHA256=grade.sha(directory / 'packet.json'))
        self.put('linux/active-application.json', active)
        old_sha = grade.sha(directory / 'active-application.json')
        self.put('linux/active-applications/' + old_sha + '.json', active)
        admission = grade.read(directory / 'admission-S7.json'); admission.update(tag='H1', activeApplicationSHA256=old_sha)
        admission_path = self.put('linux/admission-H1.json', admission)
        (directory / 'S7-load').rename(directory / 'H1-load')
        (directory / 'S7-command.json').unlink(); (directory / 'S7-stage.json').unlink()
        command.update(tag='H1', kind='formal', warmRate=25, warmSeconds=10, freezeSHA256='a' * 64,
                       scriptHashes=tools, ptsPacketSHA256=active['ptsPacketSHA256'])
        command['reservationCheck'].update(tag='H1', activeApplicationSHA256=old_sha, admissionSHA256=grade.sha(admission_path))
        command_path = self.put('linux/H1-command.json', command)
        previous.update(tag='H1'); stage_path = self.put('linux/H1-stage.json', previous)
        ref = lambda path: {'path': path.relative_to(directory).as_posix(), 'sha256': grade.sha(path)}
        terminal.update(tag='H1', originalCommand=ref(command_path), originalStage=ref(stage_path),
            originalResult=ref(directory / 'H1-load/result.json'), warmProcess=ref(directory / 'H1-load/warm-process.json'),
            noStablePaths=grade.warm_only_layout(directory, command, previous))
        terminal['reports'][0]['originalStartAudit'] = ref(directory / 'H1-load/warm-pts/api/one-StartTestingJMeterScene.json')
        terminal_path = self.put('linux/formal-warm-terminal.json', terminal)
        reconciliation = grade.read(directory / 'reconcile-before-S8.json'); reconciliation['facts']['events'] = []
        reconcile_path = self.put('linux/formal-warm-reconcile.json', reconciliation)
        before_path = self.put('linux/H1-load/warm-before.json', reconciliation['facts'])
        identity = {'phase': 'warm', 'rate': 25, 'seconds': 10, 'fixtureSHA256': command['fixtureSHA256'], 'scriptSHA256': 'b' * 64}
        identity_hash = hashlib.sha256(json.dumps(identity, sort_keys=True, separators=(',', ':'), ensure_ascii=False).encode()).hexdigest()
        zero = {key: 0 for key in ('attempted', 'completed', 'mutation', 'noop', 'uncertain', 'rejected', 'invalid', 'dropped')}
        rows = [{'kind': 'configuration', 'configuration': identity, 'hash': identity_hash},
                {'kind': 'register', 'engineId': 'one'}, {'kind': 'register', 'engineId': 'two'},
                {'kind': 'ready'}, {'kind': 'batch', 'records': []}, {'kind': 'complete', 'counts': zero}, {'kind': 'stop'}]
        journal = directory / 'H1-load/warm-pts/records.ndjson'
        journal.write_text(''.join(json.dumps(row) + '\n' for row in rows), encoding='utf-8')
        summary = {**identity, 'hash': identity_hash, 'startUnixMs': None, 'endUnixMs': None,
                   'stop': True, 'complete': False, 'counts': {'planned': 250, **zero}, 'evidenceSHA256': grade.sha(journal)}
        summary_path = self.put('linux/H1-load/warm-pts/latest.json', {'summary': summary})
        active.update(deploymentId='repair2-deployment', ptsPacketPath='packet2.json', ptsAdmissionPath='admission2.json',
                      scriptHashes={**tools, 'cap10-grade.py': 'f' * 64})
        self.put('linux/packet2.json', {**packet, 'repair': 2})
        self.put('linux/admission2.json', {'passed': True, 'sceneFilesMD5': {'plan': 'frozen'}, 'sceneId': 'SCENE'})
        active.update(ptsPacketSHA256=grade.sha(directory / 'packet2.json'), ptsAdmissionSHA256=grade.sha(directory / 'admission2.json'))
        self.put('linux/active-application.json', active)
        context = grade.execution_context(directory)
        proof = {'version': 1, 'batch': 'CAP-10', 'authorized': True, 'tag': 'H1',
            'userAuthorizationReference': 'synthetic explicit continue after zero-native warm failure',
            'recordedAt': (dt.datetime.now(dt.timezone.utc) - dt.timedelta(seconds=1)).isoformat(),
            'successorActiveApplicationSHA256': context['activeApplicationSHA256'], 'terminal': ref(terminal_path),
            'reconciliation': ref(reconcile_path), 'nativeJournal': ref(journal), 'nativeSummary': ref(summary_path), 'beforeWarm': ref(before_path)}
        self.put('linux/formal-start-recovery-H1.json', proof)
        return directory, api, active, command, proof

    def test_formal_groups_retain_zero_native_H1_then_allow_only_H2_H3_same_new_freeze(self):
        directory, api, active, old, proof = self.formal_start_recovery_fixture()
        original = (directory / 'H1-stage.json').read_bytes()
        context = grade.execution_context(directory)
        self.assertEqual(grade.formal_start_retirement(directory, old, context)['tag'], 'H1')
        admission_path = self.authority_admission(directory, 'S7')
        admission = grade.read(admission_path); admission['termination'] = proof['terminal']
        self.put('linux/admission-S7.json', admission)
        first = self.reserve('candidate', 800, source=self.other, execution_root=directory, api=api, current_tools=active['scriptHashes'])
        self.authority_add(directory, 'S7', 'candidate', 800, first, active)
        context = grade.execution_context(directory)
        freeze = {**active, 'rate': 800, 'candidateTag': 'S7', 'sourceCIPassed': True, 'relatedChecksPassed': True,
            'initialDistributionContract': {'existing': 512, 'active': 0, 'relationships': 512},
            'executionAuthoritySHA256': context['authoritySHA256'], 'activeApplicationSHA256': context['activeApplicationSHA256']}
        self.put('freeze-like.json', freeze); self.put('linux/freeze-like.json', freeze)
        for tag in ('H2', 'H3'):
            self.authority_admission(directory, tag)
            reserved = self.reserve('formal', 800, source=self.other, freeze=freeze, execution_root=directory, api=api, current_tools=active['scriptHashes'])
            self.assertEqual(reserved['tag'], tag)
            command = self.authority_add(directory, tag, 'formal', 800, reserved, active)
            command['freezeSHA256'] = grade.sha(directory / 'freeze-like.json'); self.put('linux/' + tag + '-command.json', command)
        self.authority_admission(directory, 'H4')
        with self.assertRaises(ValueError):
            self.reserve('formal', 800, source=self.other, freeze=freeze, execution_root=directory, api=api, current_tools=active['scriptHashes'])
        self.assertEqual((directory / 'H1-stage.json').read_bytes(), original)

    def test_formal_start_retirement_rejects_stable_native_HTTP_or_changed_facts(self):
        directory, api, active, old, proof = self.formal_start_recovery_fixture()
        context = grade.execution_context(directory)
        path = self.put('linux/H1-load/stable-process.json', {})
        with self.assertRaises(ValueError): grade.formal_start_retirement(directory, old, context)
        path.unlink()
        for key, mutate in (('nativeSummary', lambda value: value['summary'].update(startUnixMs=1)),
                            ('nativeSummary', lambda value: value['summary']['counts'].update(attempted=1)),
                            ('reconciliation', lambda value: value['facts'].update(eventCount=1))):
            path = directory / proof[key]['path']; original = path.read_bytes()
            value = json.loads(original); mutate(value); path.write_text(json.dumps(value), encoding='utf-8')
            bad = {**proof, key: {**proof[key], 'sha256': grade.sha(path)}}; self.put('linux/formal-start-recovery-H1.json', bad)
            with self.assertRaises(ValueError): grade.formal_start_retirement(directory, old, context)
            path.write_bytes(original)
        self.put('linux/formal-start-recovery-H1.json', proof)
        journal = directory / proof['nativeJournal']['path']; original = journal.read_bytes()
        journal.write_bytes(original + b'{"kind":"barrier"}\n')
        bad = {**proof, 'nativeJournal': {**proof['nativeJournal'], 'sha256': grade.sha(journal)}}
        self.put('linux/formal-start-recovery-H1.json', bad)
        with self.assertRaises(ValueError): grade.formal_start_retirement(directory, old, context)

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
