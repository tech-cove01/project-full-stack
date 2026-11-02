"""Actual phase envelope/auth checks; synthetic loopback collector only."""
import ast
import copy
import hashlib
from http.server import ThreadingHTTPServer
import importlib.util
import json
from pathlib import Path
import threading
import tempfile
from types import SimpleNamespace
import unittest
import urllib.error
import urllib.request
import uuid
from unittest.mock import Mock

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]


def load(name):
    spec = importlib.util.spec_from_file_location(name, HERE / (name + '.py'))
    value = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    return value


phase = load('cap10-pts-phase')
collector = load('cap10-pts-collector')


def full_summary():
    return {'complete': True, 'collectionValid': True, 'unknownIsExact': True, 'unknown': 0,
        'startUnixMs': 1800000000000, 'endUnixMs': 1800000060000, 'stop': False,
        'generatorTimingViolations': 0, 'generatorTimingValid': True,
        'counts': {'planned': 60, 'attempted': 60, 'completed': 60, 'mutation': 60,
                   'noop': 0, 'uncertain': 0, 'rejected': 0, 'invalid': 0, 'dropped': 0},
        'systemErrors': 0, 'httpErrors': 0, 'quantileMethod': 'full raw R7',
        'allLatency': {'avg': 2, 'med': 2, 'min': 1, 'p(95)': 3, 'p(99)': 4, 'max': 5},
        'mutationLatency': {'avg': 2, 'med': 2, 'min': 1, 'p(95)': 3, 'p(99)': 4, 'max': 5}}


class EnvelopeChecks(unittest.TestCase):
    def test_uncomputed_progress_cannot_become_final_percentiles(self):
        progress = full_summary()
        progress.update(allLatency=None, mutationLatency=None, latencyComplete=False, latencyStatisticsComputed=False)
        with self.assertRaisesRegex(ValueError, 'percentiles'):
            phase.normalized(progress, 60)

    def phase_fixture(self, count=4096, engines=9):
        dimensions = {'poolSize': count, 'expectedEngines': engines,
                      'ptsConcurrency': phase.cohort.pts_concurrency(count, engines)}
        hashes = {name: hashlib.sha256((REPO / name).read_bytes()).hexdigest()
                  for name in ('scripts/cap10-cohort.py', 'scripts/load/cap10-cohort-contract.mjs')}
        manifest = {'poolSize': count, 'fixture': 'synthetic-cohort',
                    'users': count, 'multiNotes': count, 'multiRelations': count,
                    'userIds': [str(i + 1) for i in range(count)],
                    'multiNoteIds': [str(i + 10001) for i in range(count)]}
        source = {'poolSize': count, 'fixture': manifest['fixture'], 'multiNoteIds': manifest['multiNoteIds'],
                  'users': [{'userId': user, 'token': 'synthetic-only'} for user in manifest['userIds']],
                  'initial': [{'userId': user, 'noteId': manifest['multiNoteIds'][i], 'active': 0, 'version': 0}
                              for i, user in enumerate(manifest['userIds'])]}
        return ({**dimensions, 'cohortSourceSHA256': hashes}, source, manifest,
                dict(dimensions), {**dimensions, 'cohortSourceSHA256': hashes})

    def test_phase_binds_all_4096_hydrated_identities_and_nine_engines(self):
        values = self.phase_fixture()
        self.assertEqual(phase.phase_cohort(*values), {'poolSize': 4096, 'expectedEngines': 9, 'ptsConcurrency': 4105})
        legacy = self.phase_fixture(512, 2)
        for value in legacy:
            value.pop('poolSize', None)
        self.assertEqual(phase.phase_cohort(*legacy)['poolSize'], 512)

    def test_phase_cannot_relabel_legacy_source_command_or_admission(self):
        for index in (1, 3, 4):
            values = self.phase_fixture()
            values[index].pop('poolSize')
            with self.subTest(index=index), self.assertRaises(ValueError):
                phase.phase_cohort(*values)
        for index in (0, 3, 4):
            values = self.phase_fixture()
            values[index]['expectedEngines'] = 2
            with self.subTest(index=index), self.assertRaises(ValueError):
                phase.phase_cohort(*values)

    def test_phase_rejects_stale_helpers_and_reordered_facts_before_start(self):
        for index in (0, 4):
            values = self.phase_fixture()
            values[index]['cohortSourceSHA256'] = {}
            with self.subTest(index=index), self.assertRaises(ValueError):
                phase.phase_cohort(*values)
        values = self.phase_fixture()
        values[1]['initial'][-1]['noteId'] = values[2]['multiNoteIds'][0]
        with self.assertRaises(ValueError):
            phase.phase_cohort(*values)
        values = self.phase_fixture()
        values[1]['users'][-1]['active'] = 1
        with self.assertRaises(ValueError):
            phase.phase_cohort(*values)

    def test_large_normalized_window_keeps_full_duration_and_dimensions(self):
        summary = full_summary()
        summary.update(poolSize=4096, expectedEngines=9, ptsConcurrency=4105)
        value = phase.normalized(summary, 60)
        self.assertEqual((value['poolSize'], value['expectedEngines'], value['ptsConcurrency']), (4096, 9, 4105))
        self.assertEqual(value['nativeScenario']['durationMs'], 60000)
        self.assertEqual(value['metrics']['cap_like_mutation']['count'], 60)

    def test_guarded_plan_keeps_full_native_window_and_actual_counts(self):
        summary = full_summary()
        summary.update(rate=800, launchGuardMs=100, plannedTotal=47920)
        for key in ('planned', 'attempted', 'completed', 'mutation'):
            summary['counts'][key] = 47920
        value = phase.normalized(summary, 60)
        self.assertEqual(value['metrics']['cap_like_mutation']['count'], 47920)
        self.assertEqual(value['nativeScenario']['durationMs'], 60000)
        self.assertEqual(value['nativeScenario']['scheduledEndedAt'], summary['startUnixMs'] + 60000)
        self.assertAlmostEqual(value['launchPlan']['plannedOfferedRatio'], 47920 / 48000)
        for changes in ({'launchGuardMs': 101}, {'plannedTotal': 48000}, {'rate': True}):
            with self.assertRaises(ValueError):
                phase.normalized({**summary, **changes}, 60)
        bad = copy.deepcopy(summary); bad['counts']['planned'] = 48000
        with self.assertRaises(ValueError): phase.normalized(bad, 60)
        bad = {**summary, 'endUnixMs': summary['endUnixMs'] - 100}
        with self.assertRaises(ValueError): phase.normalized(bad, 60)

    def test_application_snapshot_binds_the_original_admission_path(self):
        with tempfile.TemporaryDirectory(prefix='.qa-cap10-active-admission-', dir=REPO.parent) as folder:
            root = Path(folder) / 'linux'; root.mkdir()
            authority = root.parent / 'execution-authority-20261102.json'; authority.write_bytes(b'{"generation":1}')
            authority_hash = hashlib.sha256(authority.read_bytes()).hexdigest()
            admission = root / 'pts-scene-admission-repair2.json'; admission.write_bytes(b'{"passed":true}')
            admission_hash = hashlib.sha256(admission.read_bytes()).hexdigest()
            active = {'ptsAdmissionPath': admission.name, 'ptsAdmissionSHA256': admission_hash}
            raw = json.dumps(active).encode(); active_hash = hashlib.sha256(raw).hexdigest()
            snapshots = root / 'active-applications'; snapshots.mkdir()
            snapshot = snapshots / (active_hash + '.json'); snapshot.write_bytes(raw)
            reservation = {'tag': 'H2', 'ptsAdmissionSHA256': admission_hash,
                'reservationCheck': {'executionAuthoritySHA256': authority_hash, 'activeApplicationSHA256': active_hash}}
            self.assertEqual(phase.scene_admission_path(root, reservation), admission)
            admission.write_bytes(b'{"passed":false}')
            with self.assertRaises(ValueError): phase.scene_admission_path(root, reservation)
            admission.write_bytes(b'{"passed":true}')
            snapshot.write_bytes(raw + b' ')
            with self.assertRaises(ValueError): phase.scene_admission_path(root, reservation)

    def test_execution_authority_selects_existing_repair_for_positive_future_tags_only(self):
        with tempfile.TemporaryDirectory(prefix='.qa-cap10-authority-', dir=REPO.parent) as folder:
            batch = Path(folder); root = batch / 'linux'; root.mkdir()
            authority = batch / 'execution-authority-20261102.json'
            authority.write_bytes(b'{"authority":1}\n')
            digest = hashlib.sha256(authority.read_bytes()).hexdigest()
            for tag in ('S1', 'S5', 'S9', 'S10', 'S123', 'H1', 'H2', 'H10'):
                reservation = {'tag': tag, 'reservationCheck': {'executionAuthoritySHA256': digest}}
                self.assertEqual(phase.scene_admission_path(root, reservation), root / 'pts-scene-admission-repair1.json')
            for tag in ('S0', 'H0', 'S01', 'H01', 'S-1', 'S1x', 'S1\n', 'X10', None, 10):
                reservation = {'tag': tag, 'reservationCheck': {'executionAuthoritySHA256': digest}}
                self.assertEqual(phase.scene_admission_path(root, reservation), root / 'pts-scene-admission.json')
            for marker in (None, '', 'a' * 64, 123):
                reservation = {'tag': 'S10', 'reservationCheck': {'executionAuthoritySHA256': marker}}
                self.assertEqual(phase.scene_admission_path(root, reservation), root / 'pts-scene-admission.json')
            authority.write_bytes(b'{"authority":2}\n')
            reservation = {'tag': 'S10', 'reservationCheck': {'executionAuthoritySHA256': digest}}
            self.assertEqual(phase.scene_admission_path(root, reservation), root / 'pts-scene-admission.json')
            authority.unlink()
            # A similarly named Linux-local file cannot stand in for batch authority.
            local = root / authority.name; local.write_bytes(b'{"authority":1}\n')
            self.assertEqual(phase.scene_admission_path(root, reservation), root / 'pts-scene-admission.json')
            local.unlink(); root.rmdir()

    def test_complete_timing_invalid_warm_returns_failure_after_preserving_summary(self):
        good = full_summary()
        self.assertEqual(phase.phase_exit_code(good), 0)
        bad = {**good, 'generatorTimingValid': False, 'generatorTimingViolations': 1}
        native = phase.normalized(bad, 60)
        self.assertTrue(native['nativeScenario']['complete'])
        self.assertEqual(native['metrics']['cap_like_mutation']['count'], 60)
        self.assertEqual(phase.phase_exit_code(bad), 1)
        self.assertEqual(phase.phase_exit_code({**good, 'stop': True}), 1)
        self.assertEqual(phase.phase_exit_code({**good, 'generatorTimingValid': None}), 1)
        self.assertEqual(phase.phase_exit_code({**good, 'generatorTimingViolations': 1}), 1)

    def test_repaired_admission_is_only_selected_by_exact_late_reservation_transition(self):
        with tempfile.TemporaryDirectory(prefix='.qa-cap10-admission-', dir=REPO.parent) as folder:
            root = Path(folder)
            transition = root / 'tool-repair-transition.json'
            transition.write_bytes(b'{"repair":1}\n')
            digest = hashlib.sha256(transition.read_bytes()).hexdigest()
            for tag in ('S5', 'S6', 'S7', 'S8', 'H1', 'H2'):
                reservation = {'tag': tag, 'reservationCheck': {'toolRepairTransitionSHA256': digest}}
                self.assertEqual(phase.scene_admission_path(root, reservation), root / 'pts-scene-admission-repair1.json')
            for tag in ('S1', 'S2', 'S3', 'S4', 'S9', 'H3', None):
                reservation = {'tag': tag, 'reservationCheck': {'toolRepairTransitionSHA256': digest}}
                self.assertEqual(phase.scene_admission_path(root, reservation), root / 'pts-scene-admission.json')
            for marker in (None, '', 'a' * 64, 123):
                reservation = {'tag': 'S5', 'reservationCheck': {'toolRepairTransitionSHA256': marker}}
                self.assertEqual(phase.scene_admission_path(root, reservation), root / 'pts-scene-admission.json')
            self.assertEqual(phase.scene_admission_path(root, {'tag': 'S5'}), root / 'pts-scene-admission.json')
            transition.write_bytes(b'{"repair":2}\n')
            self.assertEqual(phase.scene_admission_path(root, {'tag': 'S5', 'reservationCheck': {
                'toolRepairTransitionSHA256': digest}}), root / 'pts-scene-admission.json')
            transition.unlink()
            self.assertEqual(phase.scene_admission_path(root, {'tag': 'S5', 'reservationCheck': {
                'toolRepairTransitionSHA256': digest}}), root / 'pts-scene-admission.json')

    def test_generator_timing_violation_survives_full_accounting_without_faking_unknown(self):
        summary = full_summary(); summary['generatorTimingViolations'] = 2
        value = phase.normalized(summary, 60)
        self.assertEqual(value['metrics']['cap_like_generator_timing_violations']['count'], 2)
        self.assertFalse(value['nativeScenario']['generatorTimingValid'])
        self.assertTrue(value['nativeScenario']['complete'])
        self.assertEqual(value['metrics']['cap_like_mutation']['count'], 60)
        self.assertEqual(value['metrics']['cap_like_effect_unknown']['count'], 0)

    def test_generator_timing_count_cannot_be_absent_negative_or_noninteger(self):
        for count in (None, -1, 1.5, True):
            with self.subTest(count=count):
                summary = full_summary(); summary['generatorTimingViolations'] = count
                with self.assertRaises(ValueError): phase.normalized(summary, 60)

    def test_pending_stop_keeps_polling_and_can_stop_after_identity_returns(self):
        client = SimpleNamespace(stop=Mock(side_effect=[
            {'requested': False, 'terminationPending': True, 'terminalVerified': False},
            {'requested': True, 'terminalVerified': False}]),
            terminal_evidence=Mock(side_effect=[{'terminal': False}, {'terminal': False}, {'terminal': True}]))
        sent, evidence = phase.poll_termination(client, 'scene', 'report', False)
        self.assertFalse(sent); self.assertFalse(evidence['terminal'])
        sent, evidence = phase.poll_termination(client, 'scene', 'report', sent)
        self.assertTrue(sent); self.assertFalse(evidence['terminal'])
        sent, evidence = phase.poll_termination(client, 'scene', 'report', sent)
        self.assertTrue(sent); self.assertTrue(evidence['terminal'])
        self.assertEqual(client.stop.call_count, 2)
        self.assertEqual(client.terminal_evidence.call_count, 3)
        client.stop.assert_called_with('scene', 'report')

    def test_report_pending_without_current_identity_never_claims_stop(self):
        client = SimpleNamespace(stop=Mock(return_value={
            'requested': False, 'terminationPending': True, 'terminalVerified': False}),
            terminal_evidence=Mock(return_value={'terminal': False}))
        sent = False
        for _ in range(3):
            sent, evidence = phase.poll_termination(client, 'scene', 'report', sent)
            self.assertFalse(sent); self.assertFalse(evidence['terminal'])
        self.assertEqual(client.stop.call_count, 3)
        self.assertEqual(client.terminal_evidence.call_count, 3)

    def test_real_terminal_ack_stops_retries_but_still_polls_end_evidence(self):
        for acknowledgement in ({'alreadyTerminal': True}, {'terminalVerified': True}):
            with self.subTest(acknowledgement=acknowledgement):
                client = SimpleNamespace(stop=Mock(return_value=acknowledgement),
                    terminal_evidence=Mock(return_value={'terminal': True}))
                sent, evidence = phase.poll_termination(client, 'scene', 'report', False)
                self.assertTrue(sent); self.assertTrue(evidence['terminal'])
                phase.poll_termination(client, 'scene', 'report', sent)
                self.assertEqual(client.stop.call_count, 1)
                self.assertEqual(client.terminal_evidence.call_count, 2)

    def test_full_jmeter_envelope_has_explicit_provenance(self):
        value = phase.normalized(full_summary(), 60)
        self.assertIn('JMeter', value['sourceEngine'])
        self.assertTrue(value['nativeScenario']['complete'])
        self.assertEqual(value['metrics']['cap_like_mutation']['count'], 60)
        self.assertEqual(value['metrics']['cap_like_all_ms']['p(99)'], 4)

    def test_incomplete_collector_never_fills_unknown_zero(self):
        for field in ('complete', 'collectionValid', 'unknownIsExact'):
            with self.subTest(field=field):
                value = full_summary(); value[field] = False; value['unknown'] = None
                with self.assertRaises(ValueError): phase.normalized(value, 60)

    def test_missing_http_classification_rejected(self):
        value = full_summary(); del value['systemErrors']
        with self.assertRaises(ValueError): phase.normalized(value, 60)

    def test_unclassified_inflight_cannot_become_completion(self):
        value = full_summary(); value['counts']['completed'] -= 1
        with self.assertRaises(ValueError): phase.normalized(value, 60)

    def test_unknown_effect_not_erased(self):
        value = full_summary(); value['counts']['uncertain'] = 1
        with self.assertRaises(ValueError): phase.normalized(value, 60)

    def test_missing_percentiles_not_taken_from_platform_report(self):
        value = full_summary(); value['allLatency'] = {'p(90)': 5, 'max': 10}
        with self.assertRaises(ValueError): phase.normalized(value, 60)

    def test_nonfinite_or_negative_percentiles_rejected(self):
        for sample in (float('nan'), float('inf'), -1):
            value = full_summary(); value['allLatency']['p(95)'] = sample
            with self.assertRaises(ValueError): phase.normalized(value, 60)

    def test_native_duration_never_shrinks_to_observed_time(self):
        with self.assertRaises(ValueError): phase.normalized(full_summary(), 180)

    def test_safety_stop_cannot_claim_complete_window(self):
        value = full_summary(); value['stop'] = True
        self.assertFalse(phase.normalized(value, 60)['nativeScenario']['complete'])

    def test_bounded_private_endpoints(self):
        self.assertEqual(phase.private_url('http://10.2.3.4:8481', 8481), '10.2.3.4')
        for url in ('http://127.0.0.1:8481', 'http://8.8.8.8:8481', 'http://10.2.3.4:8281',
                    'http://name.invalid:8481', 'http://user@10.2.3.4:8481', 'http://10.2.3.4:8481/?x=1'):
            with self.subTest(url=url):
                with self.assertRaises(ValueError): phase.private_url(url, 8481)


class CollectorHTTPChecks(unittest.TestCase):
    def setUp(self):
        self.root = REPO.parent / ('.qa-cap10-pts-phase-' + uuid.uuid4().hex)
        self.root.mkdir()
        self.token = 'synthetic-control-' + 'a' * 64
        config = {'runId': 'synthetic-run', 'phase': 'warm', 'rate': 1, 'seconds': 10,
                  'fixtureSHA256': 'a' * 64, 'scriptSHA256': 'b' * 64, 'expectedEngines': 2,
                  'controlToken': self.token, 'apiUrl': 'http://10.2.3.4:8281',
                  'relations': [{'slot': slot, 'userId': str(slot + 1), 'noteId': str(slot + 1000),
                      'active': 0, 'version': 0, 'token': 'synthetic-session-' + str(slot)} for slot in range(512)]}
        self.store = collector.EngineStore(config, self.root / 'records.ndjson')
        self.server = ThreadingHTTPServer(('127.0.0.1', 0), phase.handler_type(self.store, self.token, threading.RLock()))
        self.server.daemon_threads = True
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True); self.thread.start()
        self.base = 'http://127.0.0.1:' + str(self.server.server_port)
        self.addCleanup(self.cleanup)

    def cleanup(self):
        self.server.shutdown(); self.server.server_close(); self.thread.join(timeout=3); self.store.close()
        self.assertEqual(self.root.resolve().parent, REPO.parent)
        self.assertTrue(self.root.name.startswith('.qa-cap10-pts-phase-'))
        for path in self.root.iterdir():
            self.assertTrue(path.is_file()); path.unlink()
        self.root.rmdir()

    def request(self, path, token=True, payload=None):
        req = urllib.request.Request(self.base + path,
            data=None if payload is None else json.dumps(payload).encode(),
            headers={'Authorization': 'Bearer ' + self.token} if token else {})
        try:
            opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
            with opener.open(req, timeout=2) as response:
                return response.status, json.load(response)
        except urllib.error.HTTPError as error:
            return error.code, json.load(error)

    def test_all_paths_require_private_bearer(self):
        for path in ('/control', '/register', '/batch', '/complete'):
            self.assertEqual(self.request(path, token=False)[0], 403)

    def test_control_has_binding_and_no_session_credentials(self):
        status, value = self.request('/control')
        self.assertEqual(status, 200); self.assertEqual(value['seconds'], 10)
        self.assertNotIn(self.token, json.dumps(value)); self.assertNotIn('relations', value)

    def test_unknown_route_never_exposes_files(self):
        self.assertEqual(self.request('/../private.json')[0], 404)

    def test_bad_batch_locks_stop_and_does_not_echo_payload(self):
        status, value = self.request('/batch', payload={'token': 'synthetic-sensitive-payload'})
        self.assertEqual(status, 409)
        self.assertNotIn('synthetic-sensitive-payload', json.dumps(value))
        self.assertTrue(self.store.control()['stop'])

    def test_journal_contains_no_control_or_session_token(self):
        self.request('/control')
        content = (self.root / 'records.ndjson').read_text(encoding='utf-8')
        self.assertNotIn(self.token, content); self.assertNotIn('synthetic-session', content)


class TerminationChecks(unittest.TestCase):
    def setUp(self):
        self.root = REPO.parent / ('.qa-cap10-pts-termination-' + uuid.uuid4().hex)
        self.load = self.root / 'S1-load'; self.remote = self.load / 'warm-pts'
        self.remote.mkdir(parents=True)
        self.terminal = self.remote / 'terminal.json'
        self.terminal.write_text('{"remoteConverged":true}', encoding='utf-8')
        source = REPO / 'scripts/cap10-env.py'
        tree = ast.parse(source.read_text(encoding='utf-8'))
        body = ast.Module(body=[node for node in tree.body if isinstance(node, ast.FunctionDef) and node.name == 'phase_terminated'], type_ignores=[])
        class Missing(Exception): pass
        def absent(_): raise Missing()
        self.psutil = SimpleNamespace(Process=absent, NoSuchProcess=Missing)
        self.namespace = {'Path': Path, 'psutil': self.psutil,
            'read': lambda path: json.loads(Path(path).read_text(encoding='utf-8'))}
        exec(compile(body, str(source) + ' actual isolated termination function', 'exec'), self.namespace)
        self.check = self.namespace['phase_terminated']
        self.detail = {'phase': 'warm', 'backend': 'pts', 'controller_pid': 123,
            'controller_started_unix_ms': 100000, 'remoteEvidence': str(self.remote)}
        self.addCleanup(self.cleanup)

    def cleanup(self):
        self.assertEqual(self.root.resolve().parent, REPO.parent)
        self.assertTrue(self.root.name.startswith('.qa-cap10-pts-termination-'))
        self.terminal.unlink(); self.remote.rmdir(); self.load.rmdir(); self.root.rmdir()

    def test_absent_local_controller_is_insufficient_without_remote_proof(self):
        self.assertTrue(self.check(self.detail, self.load))
        self.terminal.write_text('{"remoteConverged":false}', encoding='utf-8')
        self.assertFalse(self.check(self.detail, self.load))

    def test_matching_live_controller_prevents_cleanup(self):
        self.psutil.Process = lambda _: SimpleNamespace(create_time=lambda: 100.0)
        self.assertFalse(self.check(self.detail, self.load))
        self.psutil.Process = lambda _: SimpleNamespace(create_time=lambda: 200.0)
        self.assertTrue(self.check(self.detail, self.load))

    def test_foreign_terminal_is_rejected_and_legacy_path_preserved(self):
        foreign = {**self.detail, 'remoteEvidence': str(self.root / 'foreign-pts')}
        with self.assertRaises(ValueError): self.check(foreign, self.load)
        self.assertTrue(self.check({'k6_pid': 123, 'k6_started_unix_ms': 100000}, self.load))


if __name__ == '__main__':
    unittest.main(verbosity=2)
