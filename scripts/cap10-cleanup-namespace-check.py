"""Finite local-file checks; service boundaries are replaced, never contacted."""
from contextlib import ExitStack, redirect_stdout
import importlib.util
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch


REPO = Path(__file__).resolve().parents[1]


def module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    value = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    return value


OWNED = module('cleanup_namespace_owned', REPO / 'scripts/cap10-env.py')
PACKET = module('cleanup_namespace_packet', REPO / 'scripts/pts/cap10-pts-prepare.py')


class NamespaceChecks(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix='cap10-cleanup-ns-')
        self.addCleanup(self.temporary.cleanup)
        self.base = Path(self.temporary.name).resolve()
        self.root = self.base / 'evidence'; self.root.mkdir()
        self.runtime = self.base / '.runtime-fullstack-cap10-123456789abc'; self.runtime.mkdir()
        self.environment = self.runtime / 'env-native.private.json'
        self.environment.write_text('{}', encoding='utf-8')
        self.patch = patch.object(OWNED, 'ROOT', self.root); self.patch.start(); self.addCleanup(self.patch.stop)
        self.validation = patch.object(PACKET, 'validate_owned', return_value=({}, self.runtime))
        self.validation.start(); self.addCleanup(self.validation.stop)

    def register_packet(self):
        directory = self.runtime / ('pts-upload-' + '1' * 12); directory.mkdir()
        paths = [directory / name for name in PACKET.PACKET_FILES]
        for path in paths:
            path.write_text('synthetic', encoding='utf-8')
        registration = self.runtime / ('owned-pts-upload-' + '1' * 12 + '.json')
        OWNED.save(registration, {'version': 1, 'parent': str(directory),
            'privateInputPath': str(directory / 'cap10-pts-private.json'),
            'files': [str(path) for path in paths], 'registeredBeforeSecretWrite': True})
        return directory, registration

    def test_invalid_namespace_and_exclusive_claim(self):
        for bad in ('', '../123456789abc', 'A' * 12, 'a' * 11, 'a' * 13, True):
            with self.assertRaises(AssertionError):
                OWNED.cleanup_directory(bad, self.environment)
        directory = OWNED.cleanup_directory('a' * 12, self.environment)
        self.assertEqual(directory, self.root / ('cleanup-' + 'a' * 12))
        with self.assertRaises(FileExistsError):
            OWNED.cleanup_directory('a' * 12, self.environment)
        self.assertEqual(OWNED.cleanup_directory('a' * 12, self.environment, packet=True), directory)

    def test_changed_environment_and_reused_owner_rejected(self):
        directory = OWNED.cleanup_directory('b' * 12, self.environment)
        self.environment.write_text('{"changed":true}', encoding='utf-8')
        with self.assertRaises(AssertionError):
            OWNED.cleanup_directory('b' * 12, self.environment, packet=True)
        self.environment.write_text('{}', encoding='utf-8')
        owner_path = directory / 'owner.json'; original = OWNED.read(owner_path)
        for change in ({'created': original['created'] - 100}, {'kind': 'unknown'}, {'pid': -1}, {'extra': True}):
            owner_path.write_text(json.dumps({**original, **change}), encoding='utf-8')
            with self.assertRaises(AssertionError):
                OWNED.cleanup_directory('b' * 12, self.environment, packet=True)

    def test_real_child_may_join_only_live_parent_identity(self):
        directory = OWNED.cleanup_directory('f' * 12, self.environment)
        code = ("import importlib.util,sys;from pathlib import Path;"
                "s=importlib.util.spec_from_file_location('namespace_child',sys.argv[1]);"
                "m=importlib.util.module_from_spec(s);s.loader.exec_module(m);"
                "m.ROOT=Path(sys.argv[2]);"
                "print(m.cleanup_directory(sys.argv[3],Path(sys.argv[4]),packet=True))")
        result = subprocess.run([sys.executable, '-B', '-X', 'utf8', '-c', code,
            str(REPO / 'scripts/cap10-env.py'), str(self.root), 'f' * 12, str(self.environment)],
            capture_output=True, text=True, encoding='utf-8', timeout=10)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.strip(), str(directory))

    def test_partial_namespace_is_not_adopted(self):
        (self.root / ('cleanup-' + 'c' * 12)).mkdir()
        with self.assertRaises(AssertionError):
            OWNED.cleanup_directory('c' * 12, self.environment, packet=True)
        with self.assertRaises(FileExistsError):
            OWNED.cleanup_directory('c' * 12, self.environment)

    def test_standalone_packet_and_legacy_paths(self):
        directory, registration = self.register_packet()
        legacy_plan = self.root / 'pts-packet-cleanup-plan.json'; legacy_plan.write_text('legacy', encoding='utf-8')
        with self.assertRaises(ValueError):
            PACKET.cleanup_packet(OWNED, str(self.environment))
        self.assertTrue(directory.exists())
        result = PACKET.cleanup_packet(OWNED, str(self.environment), cleanup_namespace='d' * 12)
        self.assertTrue(result['complete']); self.assertFalse(directory.exists()); self.assertTrue(registration.exists())
        self.assertEqual(legacy_plan.read_text(encoding='utf-8'), 'legacy')
        with self.assertRaises(AssertionError):
            PACKET.cleanup_packet(OWNED, str(self.environment), cleanup_namespace='d' * 12)

    def finish_boundaries(self, namespace):
        api = self.root / 'api-native.json'; OWNED.save(api, {'label': 'native'})
        value = {'CAP_READ_OBSERVER_USER': 'cap10_123456789abc', 'CAP10_LOAD_BACKEND': 'pts'}
        services = {name: {'id': name, 'volumes': []} for name in OWNED.PORTS}
        calls = []
        def run(argv, **kwargs):
            calls.append(argv)
            if 'cleanup-packet' in argv:
                position = argv.index('--cleanup-namespace')
                self.assertEqual(argv[position + 1], namespace)
                result = PACKET.cleanup_packet(OWNED, str(self.environment), cleanup_namespace=namespace)
                return SimpleNamespace(stdout=json.dumps(result), returncode=0)
            return SimpleNamespace(stdout='0', returncode=0)
        process = SimpleNamespace(pid=os.getpid(), terminate=lambda: None, wait=lambda timeout: 0)
        patches = {'settings': lambda path: (value, self.runtime), 'api_identity': lambda *a, **k: process,
            'api_environment': lambda *a: ({'containers': services}, self.root / 'environment.json', 'baseline'),
            'container': lambda name, *a, **k: {**services[name], 'exitCode': 0, 'oomKilled': False},
            'port_open': lambda port: False, 'run': run}
        stack = ExitStack()
        for name, replacement in patches.items():
            stack.enter_context(patch.object(OWNED, name, replacement))
        self.addCleanup(stack.close)
        return SimpleNamespace(environment=str(self.environment), api_metadata=str(api), cleanup_namespace=namespace), calls

    def test_actual_finish_to_packet_chain_preserves_old_results(self):
        self.register_packet()
        old = {name: b'{"old":true}' for name in ('cleanup-progress.json', 'cleanup.json',
              'pts-packet-cleanup-plan.json', 'pts-packet-cleanup.json')}
        for name, raw in old.items():
            (self.root / name).write_bytes(raw)
        args, calls = self.finish_boundaries('e' * 12)
        with redirect_stdout(io.StringIO()):
            OWNED.finish(args)
        output = self.root / ('cleanup-' + 'e' * 12)
        self.assertTrue(OWNED.read(output / 'cleanup.json')['complete'])
        self.assertTrue(OWNED.read(output / 'pts-packet-cleanup.json')['complete'])
        self.assertTrue(any('cleanup-packet' in argv for argv in calls))
        for name, raw in old.items():
            self.assertEqual((self.root / name).read_bytes(), raw)
        before = len(calls)
        with self.assertRaises(FileExistsError):
            OWNED.finish(args)
        self.assertEqual(len(calls), before)

    def test_default_finish_keeps_original_partial_guard(self):
        progress = self.root / 'cleanup-progress.json'; progress.write_text('old', encoding='utf-8')
        args, calls = self.finish_boundaries(None)
        del args.cleanup_namespace  # Existing SimpleNamespace callers remain compatible.
        with self.assertRaises(AssertionError):
            OWNED.finish(args)
        self.assertEqual(calls, [])
        self.assertEqual(progress.read_text(encoding='utf-8'), 'old')
        self.assertFalse(list(self.root.glob('cleanup-????????????')))


if __name__ == '__main__':
    unittest.main(verbosity=2)
