"""Pure synthetic CAP09 grading counterexamples; never network or a load."""
import datetime as dt
import copy
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('cap09_pure_grading_checks', Path(__file__).with_name('cap09-grade.py'))
grade = importlib.util.module_from_spec(spec)
spec.loader.exec_module(grade)


class Checks(unittest.TestCase):
    def setUp(self):
        self.old_root = grade.ROOT
        self.root = Path(tempfile.mkdtemp(prefix='cap09-pure-grade-')).resolve()
        grade.ROOT = self.root
        self.directory = self.root / 'synthetic-downstream'
        self.directory.mkdir()
        self.base = dt.datetime(2026, 11, 1, tzinfo=dt.timezone.utc)
        self.set_window(0, 180)

    def tearDown(self):
        grade.ROOT = self.old_root
        # Exact generated files/directories only; no recursive removal command.
        for path in sorted(self.root.rglob('*'), key=lambda p: len(p.parts), reverse=True):
            assert path.resolve().is_relative_to(self.root)
            path.unlink() if path.is_file() else path.rmdir()
        self.root.rmdir()

    def at(self, seconds):
        return (self.base + dt.timedelta(seconds=seconds)).isoformat()

    def set_window(self, begin, end):
        start, finish = (int((self.base + dt.timedelta(seconds=t)).timestamp() * 1000) for t in (begin, end))
        self.seconds = end - begin
        self.report = {'stableWrapperStartedAt': start - 100, 'stableWrapperEndedAt': finish + 10000,
                       'stableStartedAt': start - 100, 'stableEndedAt': finish + 10000,
                       'nativeScenario': {'startedAt': start, 'scheduledEndedAt': finish, 'durationMs': self.seconds * 1000,
                                          'startMetric': {'min': start, 'max': start},
                                          'durationMetric': {'min': self.seconds * 1000, 'max': self.seconds * 1000}, 'complete': True}}

    def rows(self, rate=100, shift=0):
        result = []
        for t in range(shift, 251, 5):
            events = max(0, min(t, 180)) * rate
            result.append({'at': self.at(t), 'errors': [], 'semanticViolations': [],
                           'sql': {'startedAt': self.at(t + .001), 'finishedAt': self.at(t + .01),
                                   'counts': {'events': events, 'pending': 0, 'processing': 0, 'done': events, 'needsReview': 0, 'other': 0},
                                   'parked': 0, 'receipts': [{'projection': g, 'missing': 0, 'applied': 0, 'ignored': events} for g in ('follow-v1', 'feed-v1', 'search-v1')]},
                           'kafka': {'startedAt': self.at(t + .02), 'finishedAt': self.at(t + .03),
                                     'groups': [{'group': 'community.' + g, 'lag': 0, 'initialized': True} for g in ('follow-v1', 'feed-v1', 'search-v1')]}})
        return result

    def run_grade(self, rows, chain='like'):
        (self.directory / 'summary.json').write_text(json.dumps({'valid': True, 'collectionValid': True}), encoding='utf-8')
        (self.directory / 'timeline.ndjson').write_text(''.join(json.dumps(r) + '\n' for r in rows), encoding='utf-8')
        return grade.pipeline_grade(self.directory, self.report, self.seconds, chain)

    def test_clean_like_and_zero_event_feed(self):
        result = self.run_grade(self.rows())
        self.assertTrue(result['passed'])
        self.assertEqual(result['newEventsInConservativeSampledWindow'], 5500)
        self.assertTrue(self.run_grade(self.rows(rate=0), 'feed')['passed'])

    def test_like_zero_events_is_not_a_write_capacity_result(self):
        with self.assertRaises(ValueError):
            self.run_grade(self.rows(rate=0))

    def test_zero_event_feed_requires_all_observed_queues_zero(self):
        rows = self.rows(rate=0)
        rows[10]['kafka']['groups'][0]['lag'] = 1
        self.assertFalse(self.run_grade(rows, 'feed')['passed'])

    def test_boundary_events_cannot_enlarge_one_percent_budget(self):
        rows = self.rows(shift=-2)  # inside samples 123..178: 5500, not boundary 118..183: 6200
        for row in rows:
            t = (grade.instant(row['at']) - self.base).total_seconds()
            if 178 <= t <= 183:
                row['sql']['counts']['pending'] = 60
                row['sql']['counts']['done'] -= 60
        result = self.run_grade(rows)
        self.assertEqual(result['newEventsInConservativeSampledWindow'], 5500)
        self.assertEqual(result['maximumGrowthBudget'], 55)
        self.assertFalse(result['passed'])

    def test_recovery_uses_finished_at_not_a_59s_start(self):
        rows = self.rows(shift=-1)
        for row in rows:
            t = (grade.instant(row['at']) - self.base).total_seconds()
            if 179 <= t < 239:
                row['sql']['counts']['pending'] = 1
                row['sql']['counts']['done'] -= 1
            if t == 239:
                row['kafka']['finishedAt'] = self.at(242)
        result = self.run_grade(rows)
        self.assertEqual(result['recoverySecondsFromNativeEnd'], 62)
        self.assertFalse(result['passed'])

    def test_long_interior_gap_cannot_pass_good_boundaries(self):
        rows = [r for r in self.rows() if not 140 <= (grade.instant(r['at']) - self.base).total_seconds() <= 150]
        with self.assertRaises(ValueError):
            self.run_grade(rows)

    def test_sql_crossing_native_end_cannot_supply_denominator_events(self):
        self.set_window(100, 145)
        rows = self.rows(rate=0)
        for row in rows:
            t = (grade.instant(row['at']) - self.base).total_seconds()
            if t >= 145:
                row['sql']['counts']['events'] = 10000
                row['sql']['counts']['done'] = 10000
                for receipt in row['sql']['receipts']:
                    receipt['ignored'] = 10000
            if t == 145:
                row['sql']['startedAt'] = self.at(145.05)
                row['sql']['finishedAt'] = self.at(146)
                row['kafka']['startedAt'] = self.at(146.01)
                row['kafka']['finishedAt'] = self.at(146.03)
                row['sql']['counts']['pending'] = 50
                row['sql']['counts']['done'] -= 50
                for receipt in row['sql']['receipts']:
                    receipt['missing'] = 50
                    receipt['ignored'] -= 50
                for group in row['kafka']['groups']:
                    group['lag'] = 50
        with self.assertRaisesRegex(ValueError, 'lacks observed real event progress'):
            self.run_grade(rows)

    def test_crossing_end_sql_still_contributes_boundary_queue_growth(self):
        rows = self.rows(shift=-2)
        for row in rows:
            t = (grade.instant(row['at']) - self.base).total_seconds()
            if t == 178:
                row['sql']['finishedAt'] = self.at(181)
                row['kafka']['startedAt'] = self.at(181.01)
                row['kafka']['finishedAt'] = self.at(181.03)
                row['sql']['counts']['pending'] = 60
                row['sql']['counts']['done'] -= 60
                for receipt in row['sql']['receipts']:
                    receipt['missing'] = 60
                    receipt['ignored'] -= 60
                for group in row['kafka']['groups']:
                    group['lag'] = 60
        result = self.run_grade(rows)
        self.assertEqual(result['newEventsInConservativeSampledWindow'], 5000)
        self.assertEqual(result['maximumGrowthBudget'], 50)
        self.assertTrue(all(value == 60 for value in result['growthByQueue'].values()))
        self.assertFalse(result['passed'])

    def test_unknown_group_and_bad_interval_are_not_zero(self):
        rows = self.rows()
        rows[1]['kafka']['groups'][0]['initialized'] = False
        with self.assertRaises(ValueError):
            self.run_grade(rows)
        rows = self.rows()
        rows[1]['sql']['finishedAt'] = self.at(0)
        with self.assertRaises(ValueError):
            self.run_grade(rows)

    def test_wrapper_grace_never_moves_the_native_recovery_deadline(self):
        result = self.run_grade(self.rows())
        self.assertEqual(result['recoverySecondsFromNativeEnd'], .03)
        self.report['nativeScenario']['complete'] = False
        incomplete = self.run_grade(self.rows())
        self.assertFalse(incomplete['passed'])
        self.assertFalse(incomplete['nativeRecoveryBudgetVerified'])

    def test_short_final_fifteen_second_slope_is_descriptive(self):
        self.set_window(135, 180)
        rows = self.rows()
        for row in rows:
            t = (grade.instant(row['at']) - self.base).total_seconds()
            if 165 <= t <= 180:
                row['sql']['counts']['pending'] = t - 165
                row['sql']['counts']['done'] -= t - 165
        result = self.run_grade(rows)
        self.assertTrue(result['passed'])
        self.assertEqual(result['shortStageLast15SecondsQueueSlope']['sampleCount'], 4)
        self.assertEqual(result['shortStageLast15SecondsQueueSlope']['perSecond']['outbox'], 1)

    def reservation_fixture(self):
        (self.root / 'start.json').write_text(json.dumps({'conservativeStartedAt': dt.datetime.now(dt.timezone.utc).isoformat()}))
        (self.root / 'environment.json').write_text('{"synthetic":true}')
        fixture = self.root / 'fixture.json'; fixture.write_text('{"synthetic":true}')
        binary = self.root / 'not-an-executable'; binary.write_bytes(b'synthetic')
        source = 'a' * 40
        api = {'sourceSha': source, 'expectedVersion': 'capacity-' + source, 'exe': str(binary), 'sha256': grade.sha(binary)}
        (self.root / 'final-application.json').write_text(json.dumps({'sourceSha': source, 'binarySHA256': api['sha256']}))
        return fixture, source, api

    def test_hold_stops_after_first_failure_and_checks_chain(self):
        fixture, source, api = self.reservation_fixture()
        command = {'at': dt.datetime.now(dt.timezone.utc).isoformat(), 'chain': 'like', 'kind': 'hold', 'tag': 'like-hold-1', 'rate': 50, 'apiSourceSha': source}
        (self.root / 'like-hold-1-command.json').write_text(json.dumps(command))
        (self.root / 'like-hold-1-stage.json').write_text('{"passed":false}')
        with self.assertRaisesRegex(ValueError, 'first failure'):
            grade.check_reservation(self.root, 'like', 'hold', 50, 180, 'multi', source, api, fixture)

    def test_one_staircase_cannot_mix_binaries_or_environments(self):
        fixture, source, api = self.reservation_fixture()
        command = {'at': dt.datetime.now(dt.timezone.utc).isoformat(), 'chain': 'like', 'kind': 'baseline-stair',
                   'tag': 'like-baseline-stair-1', 'rate': 50, 'apiSourceSha': source,
                   'binarySHA256': '0' * 64, 'environmentSHA256': grade.sha(self.root / 'environment.json'),
                   'fixtureSHA256': grade.sha(fixture)}
        path = self.root / 'like-baseline-stair-1-command.json'
        path.write_text(json.dumps(command))
        (self.root / 'like-baseline-stair-1-stage.json').write_text('{"passed":true}')
        with self.assertRaisesRegex(ValueError, 'source and binary'):
            grade.check_reservation(self.root, 'like', 'baseline-stair', 60, 45, 'multi', source, api, fixture)
        command['binarySHA256'] = api['sha256']; command['environmentSHA256'] = '0' * 64
        path.write_text(json.dumps(command))
        with self.assertRaisesRegex(ValueError, 'environment and fixture'):
            grade.check_reservation(self.root, 'like', 'baseline-stair', 60, 45, 'multi', source, api, fixture)

    def test_final_stair_and_hot_require_the_fixed_final_source(self):
        fixture, source, api = self.reservation_fixture()
        (self.root / 'final-application.json').write_text(json.dumps({'sourceSha': 'b' * 40, 'binarySHA256': api['sha256']}))
        for kind, mode in [('final-stair', 'multi'), ('final-hot', 'hot')]:
            with self.assertRaisesRegex(ValueError, 'final clean application'):
                grade.check_reservation(self.root, 'like', kind, 50, 45, mode, source, api, fixture)

    def test_hold_cannot_use_wrong_chain_or_partial_short_stage(self):
        fixture, source, api = self.reservation_fixture()
        freeze = {'sourceSha': source, 'rate': 50, 'environmentSHA256': grade.sha(self.root / 'environment.json'),
                  'binarySHA256': api['sha256'], 'fixtureSHA256': grade.sha(fixture),
                  'relatedChecksPassed': True, 'sourceCIPassed': True, 'shortStageEvidence': ['like-final-stair-1']}
        command = {'chain': 'feed', 'apiSourceSha': source, 'rate': 50, 'mode': 'multi', 'seconds': 45,
                   'fixtureSHA256': grade.sha(fixture), 'environmentSHA256': freeze['environmentSHA256'],
                   'binarySHA256': api['sha256'], 'kind': 'final-stair'}
        path = self.root / 'like-final-stair-1-command.json'; path.write_text(json.dumps(command))
        (self.root / 'like-final-stair-1-stage.json').write_text('{"passed":true}')
        with self.assertRaisesRegex(ValueError, 'chain/source/rate/duration'):
            grade.check_reservation(self.root, 'like', 'hold', 50, 180, 'multi', source, api, fixture, freeze)
        command['chain'] = 'like'; command['seconds'] = 8; path.write_text(json.dumps(command))
        with self.assertRaisesRegex(ValueError, 'chain/source/rate/duration'):
            grade.check_reservation(self.root, 'like', 'hold', 50, 180, 'multi', source, api, fixture, freeze)

    def regression_fixture(self):
        fixture, source, api = self.reservation_fixture()
        command = {'at': dt.datetime.now(dt.timezone.utc).isoformat(), 'chain': 'feed', 'kind': 'probe',
                   'tag': 'feed-probe-1', 'mode': 'multi', 'rate': 2, 'seconds': 8,
                   'apiSourceSha': source, 'binarySHA256': api['sha256'], 'fixtureSHA256': grade.sha(fixture),
                   'environmentSHA256': grade.sha(self.root / 'environment.json')}
        (self.root / 'feed-probe-1-command.json').write_text(json.dumps(command))
        (self.root / 'feed-probe-1-stage.json').write_text('{"passed":false,"childrenConverged":true,"probeExpectedStopConfirmed":false}')
        prediction = {'probeRegressionOf': 'feed-probe-1', 'observedEvidence': 'original preserved safety-stop artifact',
                      'repairCommit': 'c' * 40, 'differentPredictions': 'lower heap should remove the original fixed resource stop',
                      'sameLowSpeedCase': True, 'originalEnvironmentSHA256': command['environmentSHA256']}
        (self.root / 'environment.json').write_text('{"synthetic":"repaired environment"}')
        return fixture, source, api, command, prediction

    def test_exactly_one_environment_repair_regression_preserves_original(self):
        fixture, source, api, command, prediction = self.regression_fixture()
        result = grade.check_reservation(self.root, 'feed', 'probe', 2, 8, 'multi', source, api, fixture, prediction=prediction)
        self.assertEqual(result['tag'], 'feed-probe-1-regression-1')
        self.assertTrue(result['isToolRepairRegression'])
        self.assertEqual(result['maximumSlots'], 1)
        self.assertEqual(result['originalEnvironmentSHA256'], command['environmentSHA256'])
        self.assertEqual(grade.read(self.root / 'feed-probe-1-command.json'), command)
        second = {**command, 'tag': result['tag'], 'at': dt.datetime.now(dt.timezone.utc).isoformat()}
        (self.root / (result['tag'] + '-command.json')).write_text(json.dumps(second))
        with self.assertRaisesRegex(ValueError, 'exactly one original'):
            grade.check_reservation(self.root, 'feed', 'probe', 2, 8, 'multi', source, api, fixture, prediction=prediction)
        with self.assertRaisesRegex(ValueError, 'budget exhausted'):
            grade.check_reservation(self.root, 'feed', 'probe', 2, 8, 'multi', source, api, fixture)

    def test_probe_regression_requires_committed_repair_and_original_environment(self):
        fixture, source, api, command, prediction = self.regression_fixture()
        for key in prediction:
            changed = copy.deepcopy(prediction); changed.pop(key)
            with self.subTest(missing=key), self.assertRaises(ValueError):
                grade.check_reservation(self.root, 'feed', 'probe', 2, 8, 'multi', source, api, fixture, prediction=changed)
        with self.assertRaisesRegex(ValueError, 'retain and change'):
            grade.check_reservation(self.root, 'feed', 'probe', 2, 8, 'multi', source, api, fixture,
                                    prediction={**prediction, 'originalEnvironmentSHA256': '0' * 64})
        (self.root / 'environment.json').write_text('{"synthetic":true}')
        self.assertEqual(grade.sha(self.root / 'environment.json'), command['environmentSHA256'])
        with self.assertRaisesRegex(ValueError, 'retain and change'):
            grade.check_reservation(self.root, 'feed', 'probe', 2, 8, 'multi', source, api, fixture, prediction=prediction)

    def test_probe_regression_cannot_change_application_fixture_or_rate(self):
        fixture, source, api, command, prediction = self.regression_fixture()
        changed_source = 'b' * 40
        with self.assertRaisesRegex(ValueError, 'original application'):
            grade.check_reservation(self.root, 'feed', 'probe', 2, 8, 'multi', changed_source,
                                    {**api, 'sourceSha': changed_source, 'expectedVersion': 'capacity-' + changed_source}, fixture, prediction=prediction)
        with self.assertRaisesRegex(ValueError, 'original application'):
            grade.check_reservation(self.root, 'feed', 'probe', 3, 8, 'multi', source, api, fixture, prediction=prediction)
        fixture.write_text('{"synthetic":"different data"}')
        with self.assertRaisesRegex(ValueError, 'original application'):
            grade.check_reservation(self.root, 'feed', 'probe', 2, 8, 'multi', source, api, fixture, prediction=prediction)
        fixture.write_text('{"synthetic":true}')
        binary = Path(api['exe']); binary.write_bytes(b'changed synthetic binary')
        with self.assertRaisesRegex(ValueError, 'original application'):
            grade.check_reservation(self.root, 'feed', 'probe', 2, 8, 'multi', source,
                                    {**api, 'sha256': grade.sha(binary)}, fixture, prediction=prediction)

    def test_probe_regression_requires_failed_converged_unconfirmed_original(self):
        fixture, source, api, command, prediction = self.regression_fixture()
        for stage in ({'passed': True, 'childrenConverged': True}, {'passed': False, 'childrenConverged': False},
                      {'passed': False, 'childrenConverged': True, 'probeExpectedStopConfirmed': True}):
            (self.root / 'feed-probe-1-stage.json').write_text(json.dumps(stage))
            with self.subTest(stage=stage), self.assertRaisesRegex(ValueError, 'failed converged'):
                grade.check_reservation(self.root, 'feed', 'probe', 2, 8, 'multi', source, api, fixture, prediction=prediction)


if __name__ == '__main__':
    unittest.main(verbosity=2)
