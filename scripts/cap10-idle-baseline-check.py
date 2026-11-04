"""Pure idle baseline wiring checks; never execute Node, SQL, HTTP or an observer."""
import copy
import hashlib
import importlib.util
import json
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest import mock


spec = importlib.util.spec_from_file_location('cap10_idle_baseline_target', Path(__file__).with_name('cap10-idle.py'))
I = importlib.util.module_from_spec(spec)
spec.loader.exec_module(I)


class Checks(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix='cap10-idle-baseline-')
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name).resolve()
        self.idle = self.root / 'idle-012345abcdef'; self.idle.mkdir()
        fixture_directory = self.root / 'fixture'; fixture_directory.mkdir()
        self.fixture = fixture_directory / 'fixture.json'
        self.fixture.write_bytes(b'{"poolSize":2}')
        self.value = {'CAP10_OBSERVER_MODE': 'like-vector-v1', 'CAP_LIKE_FIXTURE': str(self.fixture),
                      'syntheticPrivateSetting': 'do-not-print'}
        self.audit = {'valid': True, 'factsValid': True, 'downstreamConverged': True, 'errors': [],
                      'facts': {'facts': [{}, {}], 'notes': [{}, {}], 'eventCount': 0, 'transitions': 0,
                                'observerPolicy': I.OBSERVER.environment_policy(self.value),
                                'fixtureManifestSHA256': hashlib.sha256(self.fixture.read_bytes()).hexdigest()},
                      'derived': {'available': True, 'errors': [], 'notes': [{}, {}]},
                      'timeline': [{'pending': 0, 'parked': 0, 'receiptErrors': []}]}
        for target in ('subprocess.Popen', 'socket.socket'):
            patch = mock.patch(target, side_effect=AssertionError('real operation forbidden'))
            patch.start(); self.addCleanup(patch.stop)
        self.owned = SimpleNamespace(env=lambda value: value)

    def reconcile(self, audit=None, exit_code=0, change_fixture=False):
        def run(command, **kwargs):
            self.assertEqual(command, ['node', 'scripts/cap10-like.mjs', 'reconcile'])
            self.assertEqual(kwargs['cwd'], I.REPO)
            self.assertEqual(kwargs['timeout'], 180)
            environment = kwargs['env']
            self.assertEqual(environment['CAP_API_PID'], '12345')
            self.assertEqual(environment['CAP_LIKE_MODE'], 'multi')
            self.assertEqual(environment['CAP_LIKE_FIXTURE'], str(self.fixture))
            self.assertEqual(environment['syntheticPrivateSetting'], 'do-not-print')
            directory = Path(environment['CAP_LIKE_EVIDENCE'])
            self.assertFalse(directory.exists())
            directory.mkdir()
            (directory / 'reconcile.json').write_text(json.dumps(self.audit if audit is None else audit), encoding='utf-8')
            kwargs['stdout'].write(b'original synthetic stdout\n')
            kwargs['stderr'].write(b'original synthetic stderr\n')
            if change_fixture:
                self.fixture.write_bytes(b'{"poolSize":1}')
            return SimpleNamespace(returncode=exit_code)
        return run

    def prepare(self):
        return I.prepare_vector_baseline(self.value, {'pid': 12345}, self.idle, self.root, self.owned)

    def test_vector_uses_original_complete_audit_bytes_and_actual_go_flags(self):
        with mock.patch.object(I.subprocess, 'run', side_effect=self.reconcile()) as node:
            baseline = self.prepare()
        node.assert_called_once()
        path = self.root / baseline['path']
        self.assertEqual(baseline['sha256'], hashlib.sha256(path.read_bytes()).hexdigest())
        command = I.idle_observer_command(self.root / 'observer', '/original/env.private.json', self.value,
                                          self.idle, self.root, baseline)
        self.assertEqual(command[-4:], ['--baseline-audit', str(path), '--baseline-audit-sha256', baseline['sha256']])
        self.assertIn('150', command)
        self.assertEqual((self.idle / 'idle-baseline.stdout.txt').read_bytes(), b'original synthetic stdout\n')
        self.assertEqual((self.idle / 'idle-baseline.stderr.txt').read_bytes(), b'original synthetic stderr\n')
        self.assertFalse((self.idle / 'idle-downstream').exists())

    def test_legacy_has_no_node_call_or_new_go_flags(self):
        value = {key: item for key, item in self.value.items() if key != 'CAP10_OBSERVER_MODE'}
        with mock.patch.object(I.subprocess, 'run', side_effect=AssertionError('legacy must not audit')):
            self.assertIsNone(I.prepare_vector_baseline(value, {}, self.idle, self.root, self.owned))
        command = I.idle_observer_command(self.root / 'observer', '/original/env.private.json', value,
                                          self.idle, self.root, None)
        self.assertNotIn('--baseline-audit', command)
        self.assertFalse(list(self.idle.iterdir()))

    def test_node_failure_keeps_original_outputs_and_cannot_supply_baseline(self):
        with mock.patch.object(I.subprocess, 'run', side_effect=self.reconcile(exit_code=1)), self.assertRaises(AssertionError):
            self.prepare()
        self.assertTrue((self.idle / 'idle-baseline/reconcile.json').exists())
        self.assertTrue((self.idle / 'idle-baseline.stderr.txt').exists())

    def test_incomplete_or_wrong_fixture_policy_verdict_cannot_start_observer(self):
        mutations = [lambda a: a.update(valid=False), lambda a: a.update(factsValid=False),
                     lambda a: a.update(downstreamConverged=False), lambda a: a.update(errors=['invalid']),
                     lambda a: a['derived'].update(available=False), lambda a: a['derived'].update(errors=['invalid']),
                     lambda a: a['facts'].update(facts=[{}]), lambda a: a['facts'].update(eventCount=None),
                     lambda a: a['timeline'][-1].update(pending=1), lambda a: a['timeline'][-1].update(parked=1),
                     lambda a: a['timeline'][-1].update(receiptErrors=['missing']),
                     lambda a: a['facts'].update(observerPolicy={}),
                     lambda a: a['facts'].update(fixtureManifestSHA256='a' * 64)]
        for number, mutate in enumerate(mutations):
            with self.subTest(case=number):
                original_idle = self.idle
                self.idle = self.root / ('negative-' + str(number)); self.idle.mkdir()
                audit = copy.deepcopy(self.audit); mutate(audit)
                with mock.patch.object(I.subprocess, 'run', side_effect=self.reconcile(audit)), self.assertRaises(AssertionError):
                    self.prepare()
                self.idle = original_idle

    def test_fixture_change_after_audit_rejects(self):
        with mock.patch.object(I.subprocess, 'run', side_effect=self.reconcile(change_fixture=True)), self.assertRaises(AssertionError):
            self.prepare()

    def test_vector_command_cannot_silently_omit_baseline(self):
        with self.assertRaises(AssertionError):
            I.idle_observer_command('observer', 'env', self.value, self.idle, self.root, None)


if __name__ == '__main__':
    unittest.main(verbosity=2)
