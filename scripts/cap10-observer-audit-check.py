"""Pure file-bound audit/observer integration checks; no processes or services."""
import copy
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

REPO = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location('cap10_audit_grade_check', REPO / 'scripts/cap10-grade.py')
grade = importlib.util.module_from_spec(spec)
spec.loader.exec_module(grade)


class Checks(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix='.runtime-cap10-audit-bind-', dir=REPO.parent)
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name).resolve()
        self.policy = grade.OBSERVER.environment_policy({'CAP10_OBSERVER_MODE': 'like-vector-v1'})
        self.fixture = self.put('fixture.json', {'multiNoteIds': ['11', '12'], 'userIds': ['21', '22']})
        self.fixture_hash = grade.sha(self.fixture)
        self.baseline = self.proof(1)
        self.baseline_path = self.put('audits/reconcile.json', self.baseline)
        self.ref = {'path': 'audits/reconcile.json', 'sha256': grade.sha(self.baseline_path)}
        admission = self.put('admission-S20.json', {'reconciliation': self.ref})
        self.reservation = {'tag': 'S20', 'poolSize': 2, 'admissionSHA256': grade.sha(admission)}
        self.command = {'tag': 'S20', 'poolSize': 2, 'fixtureSHA256': self.fixture_hash,
                        'observerPolicy': self.policy, **grade.observer_baseline(self.root, self.reservation, self.fixture, self.policy)}
        self.ready = {'observerPolicy': self.policy, 'observerSamplingLimits': grade.OBSERVER.sampling_limits(),
                      'baselineAudit': self.ref, 'baselineAuditEventCount': 2,
                      'baselineCountSource': 'bound-node-full-reconciliation',
                      'baselineVectorSHA256': 'a' * 64, 'baselineControlsSHA256': 'b' * 64,
                      'observerVectorLimits': copy.deepcopy(grade.VECTOR_LIMITS)}
        self.directory = self.root / 'S20-downstream'
        self.put('S20-downstream/ready.json', self.ready)
        self.final = self.proof(3)
        self.put('S20-load/reconcile.json', self.final)
        self.report = {'observerPolicy': self.policy, 'fixtureManifestSHA256': self.fixture_hash,
                       'nativeScenario': {'scheduledEndedAt': 1500}}
        self.rows = [{'sql': {'counts': {'events': 4}}}]

    def put(self, name, value):
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(value), encoding='utf-8')
        return path

    def proof(self, version):
        total = 2 * version
        pairs = [('11', '21'), ('12', '22')]
        return {'valid': True, 'factsValid': True, 'downstreamConverged': True, 'errors': [],
                'startedAt': 1000 if version == 1 else 2000, 'finishedAt': 3000,
                'facts': {'observerPolicy': self.policy, 'auditContract': grade.AUDIT_CONTRACT,
                          'fixtureManifestSHA256': self.fixture_hash, 'eventCount': total, 'transitions': total,
                          'facts': [{'noteId': n, 'userId': u, 'active': version % 2, 'version': version} for n, u in pairs],
                          'notes': [{'noteId': n, 'liked': version % 2, 'activeRelations': version % 2} for n, _ in pairs],
                          'events': [{'noteId': n, 'userId': u, 'count': version, 'invalid': 0} for n, u in pairs],
                          'delivery': [{'status': 'DONE', 'count': total}], 'parked': [],
                          'receipts': [{'projection': p, 'state': 'IGNORED', 'count': total,
                                        'invalidHash': 0, 'invalidProjection': 0, 'invalidState': 0}
                                       for p in ('follow-v1', 'feed-v1', 'search-v1')],
                          'controlRows': {'projections': [{'projection': p, 'rebuiltAt': None} for p in ('follow-v1', 'feed-v1')],
                                          'search': {'name': 'search-v1', 'indexAlias': 'community', 'activeGeneration': 'g1', 'rebuiltAt': None}}},
                'derived': {'available': True, 'errors': [], 'notes': [{'noteId': n} for n, _ in pairs]}}

    def test_original_admission_bytes_bind_per_window_baseline(self):
        self.assertEqual(self.command['baselineAudit'], self.ref)
        self.assertEqual(self.command['baselineAuditEventCount'], 2)
        self.assertEqual(grade.observer_baseline(self.root, {}, self.fixture, grade.OBSERVER.environment_policy({})), {})
        self.baseline_path.write_text(json.dumps({**self.baseline, 'unbound': True}), encoding='utf-8')
        with self.assertRaises(ValueError):
            grade.observer_baseline(self.root, self.reservation, self.fixture, self.policy)

    def test_baseline_rejects_legacy_policy_fixture_and_bool_counts(self):
        for mutate in (lambda v: v['facts'].pop('auditContract'),
                       lambda v: v['facts'].update(observerPolicy=grade.OBSERVER.environment_policy({})),
                       lambda v: v['facts'].update(fixtureManifestSHA256='f' * 64),
                       lambda v: v['facts'].update(eventCount=True),
                       lambda v: v['facts']['receipts'][0].update(invalidHash=1)):
            proof = copy.deepcopy(self.baseline); mutate(proof)
            with self.subTest(proof=proof), self.assertRaises(ValueError):
                grade._complete_vector_audit(proof, self.fixture_hash, 2)

    def test_ready_rejects_other_reference_count_source_limits_and_digests(self):
        grade.observer_ready_identity(self.root, self.command, self.ready)
        for mutate in (lambda v: v['baselineAudit'].update(path='another.json'),
                       lambda v: v.update(baselineAuditEventCount=True),
                       lambda v: v.update(baselineCountSource='fresh-full-Go-query'),
                       lambda v: v['observerVectorLimits'].update(sourceChunkKeys=257),
                       lambda v: v['observerVectorLimits'].update(sourceChunkKeys=256.0),
                       lambda v: v.update(baselineVectorSHA256=None)):
            ready = copy.deepcopy(self.ready); mutate(ready)
            with self.subTest(ready=ready), self.assertRaises(ValueError):
                grade.observer_ready_identity(self.root, self.command, ready)

    def test_reference_rejects_noncanonical_and_traversal(self):
        for path in ('../original.json', '/absolute.json', 'linux/../original.json', './original.json', 'a\\original.json', 'a//original.json'):
            with self.subTest(path=path), self.assertRaises(ValueError):
                grade._audit_reference(self.root, {**self.ref, 'path': path})
        self.put('audits/original.json', self.baseline)
        with self.assertRaises(ValueError):
            grade._audit_reference(self.root, {**self.ref, 'path': 'audits/original.json'})

    def test_observer_invocation_passes_original_bytes_only_in_new_mode(self):
        self.assertEqual(grade.observer_arguments(self.root, self.command),
                         ['--baseline-audit', str(self.baseline_path), '--baseline-audit-sha256', self.ref['sha256']])
        self.assertEqual(grade.observer_arguments(self.root, {'observerPolicy': grade.OBSERVER.environment_policy({})}), [])

    def test_final_full_count_equals_baseline_plus_delta(self):
        result = grade.observer_final_audit(self.directory, self.report, self.command, self.ready, self.rows)
        self.assertEqual((result['baselineEventCount'], result['terminalStageEvents'], result['finalEventCount']), (2, 4, 6))
        self.assertEqual(result['finalAuditSHA256'], grade.sha(self.root / 'S20-load/reconcile.json'))
        with self.assertRaises(ValueError):
            grade.observer_final_audit(self.directory, self.report, self.command, self.ready, [{'sql': {'counts': {'events': 6}}}])

    def test_final_requires_original_ready_summary_identity(self):
        summary = {**self.ready, 'baselineControlsSHA256': 'c' * 64}
        with self.assertRaises(ValueError):
            grade.observer_final_audit(self.directory, self.report, self.command, summary, self.rows)

    def test_final_rejects_control_change_and_missing_complete_audit(self):
        changed = copy.deepcopy(self.final)
        changed['facts']['controlRows']['search']['activeGeneration'] = 'g2'
        self.put('S20-load/reconcile.json', changed)
        with self.assertRaises(ValueError):
            grade.observer_final_audit(self.directory, self.report, self.command, self.ready, self.rows)
        (self.root / 'S20-load/reconcile.json').unlink()
        with self.assertRaises(OSError):
            grade.observer_final_audit(self.directory, self.report, self.command, self.ready, self.rows)

    def test_final_full_audit_must_follow_native_end(self):
        early = {**self.final, 'startedAt': 1000}
        self.put('S20-load/reconcile.json', early)
        with self.assertRaises(ValueError):
            grade.observer_final_audit(self.directory, self.report, self.command, self.ready, self.rows)


if __name__ == '__main__':
    unittest.main(verbosity=2)
