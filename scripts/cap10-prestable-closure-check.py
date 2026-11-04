"""Focused synthetic pre-stable closure checks; no network, SQL or processes."""
import copy
import datetime as dt
import importlib.util
import json
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('prestable_old_checks', Path(__file__).with_name('cap10-grade-check.py'))
old = importlib.util.module_from_spec(spec); spec.loader.exec_module(old)
grade = old.grade


class PrestableChecks(unittest.TestCase):
    def setUp(self):
        self.case = old.Checks('runTest'); self.case.setUp(); self.addCleanup(self.case.cleanup)
        self.case.now -= dt.timedelta(seconds=100)
        self.root, self.api, self.active, self.command, self.stage, self.terminal = self.case.authority_warm_fixture()
        self.pairs = [(str(i + 1), str(1000 + i)) for i in range(512)]
        fixture = {'multiNoteIds': [n for n, _ in self.pairs], 'userIds': [u for _, u in self.pairs]}
        self.case.put('linux/fixture.json', fixture)
        self.active['fixtureSHA256'] = grade.sha(self.root / 'fixture.json')
        self.case.put('linux/active-application.json', self.active)
        self.command.update(fixtureSHA256=self.active['fixtureSHA256'], warmRate=25, warmSeconds=10, apiPid=123456)
        self.command['reservationCheck']['activeApplicationSHA256'] = grade.sha(self.root / 'active-application.json')
        baseline = self.put('baseline/reconcile.json', {'facts': {'eventCount': 0}})
        self.command['reservationCheck']['baselineAudit'] = self.ref(baseline)
        self.put('S7-command.json', self.command)
        ended = self.case.now + dt.timedelta(seconds=150)
        self.stage.update(outcome='correctness_stop', batchFatal=True, childrenConverged=True,
                          finishedAt=ended.isoformat(), failedStartOrException='FileNotFoundError')
        self.put('S7-stage.json', self.stage)
        start = int(ended.timestamp() * 1000) - 70000
        self.put('S7-load/result.json', {'loadBackend': 'pts', 'warmExit': 0, 'warmExternalStop': None,
            'nativeScenario': {'complete': False}, 'failure': 'setup requires one exact accepted fact/control vector plus fully clean downstream within60s',
            'finalReconcileValid': True, 'finalFactsValid': True, 'downstreamConverged': True,
            'failedPhaseSettleWaitMs': 5500, 'finished': (ended - dt.timedelta(milliseconds=1)).isoformat()})
        records = [{'_kind': 'locked-note', 'noteId': n} for n, _ in self.pairs]
        records += [{'_kind': 'locked-pair', 'noteId': n, 'userId': u, 'active': int(i < 250),
                     'version': int(i < 250)} for i, (n, u) in enumerate(self.pairs)]
        self.put('S7-load/setup-transaction.json', {'startedAt': start, 'committedAt': start + 100,
            'records': records, 'countedAsHTTPMutation': False})
        self.put('S7-load/final-full-audit-attempt.json', {'reason': 'failed-before-stable-start-once',
            'attemptedAt': int(ended.timestamp() * 1000) - 2800, 'baselineAudit': self.ref(baseline), 'noAutomaticRetry': True})
        facts = [{'noteId': n, 'userId': u, 'active': 0, 'version': 2 if i < 250 else 0}
                 for i, (n, u) in enumerate(self.pairs)]
        notes = [{'noteId': n, 'liked': 0, 'activeRelations': 0} for n, _ in self.pairs]
        self.put('S7-load/failed-phase-reconcile.json', {'valid': True, 'factsValid': True,
            'downstreamConverged': True, 'errors': [], 'startedAt': int(ended.timestamp() * 1000) - 2799,
            'finishedAt': int(ended.timestamp() * 1000) - 5,
            'facts': {'fixtureManifestSHA256': self.command['fixtureSHA256'], 'facts': facts, 'notes': notes,
                      'eventCount': 500, 'transitions': 500,
                      'observerPolicy': grade.OBSERVER.environment_policy({'CAP10_OBSERVER_MODE': grade.OBSERVER.LIKE_VECTOR}),
                      'auditContract': grade.AUDIT_CONTRACT,
                      'events': [{'noteId': n, 'userId': u, 'count': 2, 'distinctVersions': 2, 'minVersion': 1, 'maxVersion': 2, 'invalid': 0} for n, u in self.pairs[:250]],
                      'delivery': [{'status': 'DONE', 'count': 500}],
                      'receipts': [{'projection': group, 'state': 'IGNORED', 'count': 500, 'invalidHash': 0, 'invalidProjection': 0, 'invalidState': 0} for group in ('follow-v1', 'feed-v1', 'search-v1')],
                      'parked': [], 'controlRows': {}},
            'derived': {'available': True, 'errors': [], 'notes': notes},
            'timeline': [{'pending': 0, 'parked': 0, 'receiptErrors': []}]})
        self.put('S7-load/warm-pts/terminal.json', {'backend': 'pts', 'reportId': 'WARM_ONLY',
            'remoteConverged': True, 'nativeEvidenceComplete': True, 'failed': None,
            'finishedAt': dt.datetime.fromtimestamp((start - 1) / 1000, dt.timezone.utc).isoformat()})
        counters = {'cap_like_attempted': 250, 'cap_like_completed': 250, 'cap_like_success': 250,
                    'cap_like_mutation': 250, 'cap_like_noop': 0, 'cap_like_effect_unknown': 0,
                    'cap_like_rejected': 0, 'cap_like_content_errors': 0, 'cap_like_system_errors': 0,
                    'cap_like_http_errors': 0, 'dropped_iterations': 0, 'cap_like_generator_timing_violations': 0}
        self.put('S7-load/warm-summary.json', {'nativeScenario': {'complete': True},
            'metrics': {k: {'count': v} for k, v in counters.items()}})
        self.put('S7-control/exit-status.json', {'cohort': 'cap10', 'mode': 'like', 'api_pid': self.command['apiPid'],
            'ownership_metadata': str(self.root / 'environment.json'), 'ownership_metadata_sha256': self.command['environmentSHA256'],
            'sampler_started': False, 'resource_samples': 0, 'complete_resource_samples': 0,
            'first_resource_sample_at': None, 'last_resource_sample_at': None, 'harness_exit': 1,
            'stop_signal': False, 'forced': False, 'forced_owned_processes': [],
            'ended_at': (ended - dt.timedelta(microseconds=100)).isoformat(),
            'errors': ['no stable owned generator was observed; no resource sample',
                       'resource evidence unavailable or malformed', 'no complete resource sample; no measurable capacity result']})
        self.put('S7-control/done.json', {'source': 'cap10-stage', 'at': (ended - dt.timedelta(microseconds=200)).isoformat()})
        self.reseal()

    def put(self, name, value):
        return self.case.put('linux/' + name, value)

    def ref(self, path):
        return {'path': path.relative_to(self.root).as_posix(), 'sha256': grade.sha(path)}

    def reseal(self):
        self.terminal.update(originalCommand=self.ref(self.root / 'S7-command.json'),
            originalStage=self.ref(self.root / 'S7-stage.json'), originalResult=self.ref(self.root / 'S7-load/result.json'),
            noStablePaths=grade.warm_only_layout(self.root, self.command, self.stage))

    def validate(self, terminal=None):
        return grade.authority_warm_only(self.root, self.command, self.stage,
            terminal or self.terminal, {'active': self.active})

    def test_accepts_successful_warm_and_exact_failed_setup_without_rewriting_results(self):
        originals = {p: p.read_bytes() for p in self.root.rglob('*') if p.is_file()}
        self.assertEqual(self.terminal['noStablePaths']['kind'], 'prestable_setup_failed')
        self.assertTrue(self.validate())
        self.assertTrue(all(p.read_bytes() == value for p, value in originals.items()))
        self.assertEqual([r['phase'] for r in self.terminal['reports']], ['warm'])
        self.assertFalse(self.stage['passed'])

    def test_rejects_any_stable_evidence_or_unknown_failure(self):
        for name in ('stable-process.json', 'stable-pts/unexpected', 'stable-anything.json', 'before.json', 'after.json'):
            with self.subTest(name=name):
                path = self.put('S7-load/' + name, {})
                self.assertIsNone(grade.warm_only_layout(self.root, self.command, self.stage))
                path.unlink()
                if (self.root / 'S7-load/stable-pts').exists(): (self.root / 'S7-load/stable-pts').rmdir()
        path = self.root / 'S7-load/result.json'; saved = grade.read(path)
        for change in ({'failure': 'unknown'}, {'warmExit': 1}, {'stableStartedAt': 123}, {'setup': {}},
                       {'finalFactsValid': False}, {'nativeScenario': {'complete': True}}):
            self.put('S7-load/result.json', {**saved, **change})
            self.assertIsNone(grade.warm_only_layout(self.root, self.command, self.stage))
        self.put('S7-load/result.json', saved)
        self.assertIsNone(grade.warm_only_layout(self.root, {**self.command, 'kind': 'formal'}, self.stage))

    def test_rejects_missing_original_full_audit_or_setup(self):
        for name in ('setup-transaction.json', 'failed-phase-reconcile.json', 'final-full-audit-attempt.json', 'warm-summary.json', 'warm-pts/terminal.json'):
            path = self.root / 'S7-load' / name; saved = path.read_bytes(); path.unlink()
            self.assertIsNone(grade.warm_only_layout(self.root, self.command, self.stage))
            path.write_bytes(saved)

    def test_rejects_resealed_invalid_setup_facts_or_audit(self):
        for name, change in (
            ('setup-transaction.json', lambda v: v.update(countedAsHTTPMutation=True)),
            ('setup-transaction.json', lambda v: v['records'][512].update(userId='999999')),
            ('failed-phase-reconcile.json', lambda v: v.update(valid=False)),
            ('failed-phase-reconcile.json', lambda v: v['facts']['facts'][0].update(version=999)),
            ('failed-phase-reconcile.json', lambda v: v['derived'].update(available=False)),
            ('failed-phase-reconcile.json', lambda v: v['facts']['receipts'][0].update(invalidHash=1)),
            ('failed-phase-reconcile.json', lambda v: v['facts'].pop('auditContract')),
            ('failed-phase-reconcile.json', lambda v: v['facts'].update(events=[])),
            ('warm-summary.json', lambda v: v['metrics']['cap_like_effect_unknown'].update(count=1)),
            ('warm-pts/terminal.json', lambda v: v.update(nativeEvidenceComplete=False)),
        ):
            path = self.root / 'S7-load' / name; saved = path.read_bytes(); value = json.loads(saved); change(value)
            self.put('S7-load/' + name, value); self.reseal()
            with self.subTest(name=name), self.assertRaises((ValueError, TypeError, KeyError)): self.validate()
            path.write_bytes(saved)
        self.reseal()

    def test_requires_all_original_owners_and_signed_matching_endtime(self):
        for change in (
            lambda t: t['owners'].append({'pid': 999, 'created': 99, 'alive': True, 'identityChecked': True}),
            lambda t: t.update(owners=[]),
            lambda t: t['reports'][0].update(endTimeRaw=''),
            lambda t: t['reports'][0].update(reportId='wrong'),
            lambda t: t.update(warmReportAudit={**t['warmReportAudit'], 'sha256': 'f' * 64}),
        ):
            terminal = copy.deepcopy(self.terminal); change(terminal)
            with self.assertRaises((ValueError, TypeError, KeyError)): self.validate(terminal)
        path = self.root / self.terminal['warmReportAudit']['path']; value = grade.read(path)
        value['sanitizedRawResponse']['ReportOverView'].pop('ReportId')
        self.put(path.relative_to(self.root).as_posix(), value)
        terminal = copy.deepcopy(self.terminal); terminal['warmReportAudit'] = self.ref(path)
        with self.assertRaises(ValueError): self.validate(terminal)

    def test_secondary_missing_resources_requires_original_exact_zero_sample_control(self):
        self.assertEqual(self.terminal['noStablePaths']['secondaryException'], 'FileNotFoundError')
        control = self.root / 'S7-control/exit-status.json'; saved = control.read_bytes()
        for change in ({'resource_samples': 1}, {'sampler_started': True}, {'errors': ['unknown']}, {'api_pid': 999}):
            self.put('S7-control/exit-status.json', {**json.loads(saved), **change})
            self.assertIsNone(grade.warm_only_layout(self.root, self.command, self.stage))
        control.write_bytes(saved)
        resource = self.put('S7-control/resources.ndjson', {})
        self.assertIsNone(grade.warm_only_layout(self.root, self.command, self.stage)); resource.unlink()
        self.assertIsNone(grade.warm_only_layout(self.root, self.command, {**self.stage, 'failedStartOrException': 'PermissionError'}))

    def test_public_next_reservation_requires_later_fresh_reconciliation(self):
        terminal = copy.deepcopy(self.terminal)
        admission_path = self.case.authority_admission(self.root, 'S8')
        terminal_path = self.put('terminal-before-S8.json', terminal)
        admission = grade.read(admission_path)
        admission['termination'] = self.ref(terminal_path)
        proof = copy.deepcopy(grade.read(self.root / 'S7-load/failed-phase-reconcile.json'))
        ended = grade.instant(self.stage['finishedAt'])
        proof.update(startedAt=int(ended.timestamp() * 1000) + 1, finishedAt=int(ended.timestamp() * 1000) + 2)
        path = self.put('reconcile-before-S8.json', proof)
        admission.update(reconciliation=self.ref(path), recordedAt=(ended + dt.timedelta(milliseconds=3)).isoformat())
        self.put('admission-S8.json', admission)
        self.assertEqual(self.case.reserve('candidate', 800, source=self.case.other,
            execution_root=self.root, api=self.api)['tag'], 'S8')
        proof['startedAt'] = int(ended.timestamp() * 1000) - 1
        self.put('reconcile-before-S8.json', proof); admission['reconciliation'] = self.ref(path)
        self.put('admission-S8.json', admission)
        with self.assertRaises(ValueError):
            self.case.reserve('candidate', 800, source=self.case.other, execution_root=self.root, api=self.api)

    def test_old_failed_warm_contract_remains_accepted(self):
        isolated = old.Checks('runTest'); isolated.setUp(); self.addCleanup(isolated.cleanup)
        directory, api, active, command, stage, terminal = isolated.authority_warm_fixture()
        self.assertNotIn('kind', terminal['noStablePaths'])
        self.assertTrue(grade.authority_warm_only(directory, command, stage, terminal, {'active': active}))


if __name__ == '__main__':
    unittest.main()
