"""Focused policy checks; one bounded local Node import, no services or load."""
import copy
import contextlib
import importlib.util
import io
import json
from pathlib import Path
import subprocess
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location('cap10_observer_contract_check', HERE / 'cap10-observer-contract.py')
policy = importlib.util.module_from_spec(spec)
spec.loader.exec_module(policy)


class Checks(unittest.TestCase):
    def test_legacy_default_and_only_explicit_vector_mode(self):
        self.assertEqual(policy.environment_policy({}), policy.environment_policy({'CAP10_OBSERVER_MODE': 'full-history-v1'}))
        legacy = policy.environment_policy({})
        vector = policy.environment_policy({'CAP10_OBSERVER_MODE': 'like-vector-v1'})
        self.assertEqual((legacy['mode'], legacy['fullAuditTimeoutMs']), ('full-history-v1', 30000))
        self.assertEqual((vector['mode'], vector['fullAuditTimeoutMs']), ('like-vector-v1', 120000))
        self.assertEqual(legacy['scope'], 'full-snapshot-audit-only')

    def test_invalid_environment_and_explicit_values_do_not_become_legacy(self):
        for value in [None, [], True, 'full-history-v1']:
            with self.subTest(environment=value), self.assertRaises(ValueError): policy.environment_policy(value)
        for mode in [None, '', False, 1, 120000, {}, [], 'LIKE-VECTOR-V1', 'full-history-v2']:
            with self.subTest(mode=mode), self.assertRaises(ValueError): policy.environment_policy({'CAP10_OBSERVER_MODE': mode})

    def test_exact_metadata_policy_rejects_resealed_wrong_types_keys_or_budget(self):
        expected = policy.environment_policy({'CAP10_OBSERVER_MODE': 'like-vector-v1'})
        self.assertEqual(policy.metadata_policy({'observerPolicy': expected}, expected), expected)
        for value in [None, [], {**expected, 'extra': True}, {'mode': 'like-vector-v1'},
                      {**expected, 'fullAuditTimeoutMs': True}, {**expected, 'fullAuditTimeoutMs': 120000.0},
                      {**expected, 'fullAuditTimeoutMs': 30000}, {**expected, 'scope': 'window-too'},
                      policy.environment_policy({})]:
            with self.subTest(actual=value), self.assertRaises(ValueError):
                policy.metadata_policy({'observerPolicy': value}, expected)
        with self.assertRaises(ValueError): policy.metadata_policy({'observerPolicy': expected}, {**expected, 'fullAuditTimeoutMs': True})

    def test_missing_metadata_requires_explicit_legacy_compatibility_only(self):
        legacy, vector = policy.environment_policy({}), policy.environment_policy({'CAP10_OBSERVER_MODE': 'like-vector-v1'})
        with self.assertRaises(ValueError): policy.metadata_policy({}, legacy)
        self.assertEqual(policy.metadata_policy({}, legacy, allow_missing_legacy=True), legacy)
        with self.assertRaises(ValueError): policy.metadata_policy({}, vector, allow_missing_legacy=True)
        with self.assertRaises(ValueError): policy.metadata_policy({'observerPolicy': None}, legacy, allow_missing_legacy=True)
        with self.assertRaises(ValueError): policy.metadata_policy({}, legacy, allow_missing_legacy=1)
        for metadata in [None, [], 'legacy']:
            with self.assertRaises(ValueError): policy.metadata_policy(metadata, legacy, allow_missing_legacy=True)

    def test_returned_policies_and_sampling_limits_have_no_shared_mutable_state(self):
        environment = {'CAP10_OBSERVER_MODE': 'like-vector-v1'}
        expected = policy.environment_policy(environment)
        metadata = {'observerPolicy': copy.deepcopy(expected)}
        before = copy.deepcopy(metadata)
        returned = policy.metadata_policy(metadata, expected)
        returned['mode'] = 'changed'
        self.assertEqual(metadata, before)
        self.assertEqual(environment['CAP10_OBSERVER_MODE'], 'like-vector-v1')
        limits = policy.sampling_limits()
        self.assertEqual(limits, {'loadBudgetMs': 4000, 'readTimeoutMs': 3000, 'cadenceSeconds': 5, 'nativeRecoverySeconds': 60})
        limits['loadBudgetMs'] = 99999
        self.assertEqual(policy.sampling_limits()['loadBudgetMs'], 4000)

    def test_actual_node_tuple_equals_python_one_local_import(self):
        uri = (HERE / 'load/cap10-observer-policy.mjs').as_uri()
        source = 'import {observerPolicy} from ' + json.dumps(uri) + ';console.log(JSON.stringify([observerPolicy({}),observerPolicy({CAP10_OBSERVER_MODE:"like-vector-v1"})]))'
        result = subprocess.run(['node', '--input-type=module', '-e', source], check=True,
                                capture_output=True, text=True, timeout=10)
        self.assertEqual(json.loads(result.stdout), [policy.environment_policy({}),
                         policy.environment_policy({'CAP10_OBSERVER_MODE': 'like-vector-v1'})])


class LauncherChecks(unittest.TestCase):
    """Run the actual launcher/identity logic; fake only OS and service edges."""
    def setUp(self):
        spec = importlib.util.spec_from_file_location('cap10_observer_launcher_check', HERE / 'cap10-env.py')
        self.owned = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(self.owned)
        workspace = HERE.parent.parent.resolve()
        temporary = tempfile.TemporaryDirectory(prefix='.runtime-cap10-observer-launch-', dir=workspace)
        self.assertEqual(Path(temporary.name).resolve().parent, workspace)
        self.addCleanup(temporary.cleanup)
        self.base = Path(temporary.name).resolve()
        self.root, self.runtime = self.base / 'evidence', self.base / 'private'
        self.root.mkdir(); self.runtime.mkdir()
        self.binary = self.base / 'synthetic-api'
        self.binary.write_bytes(b'synthetic artifact, never executed')
        self.metadata = self.root / 'environment.json'
        self.metadata.write_text('{}', encoding='utf-8')
        self.source, self.transition = 'a' * 40, 'b' * 64
        self.input = self.runtime / 'env-before.private.json'
        self.captured = {}
        self.process = SimpleNamespace(pid=12345, create_time=lambda: 123456.0,
            exe=lambda: str(self.binary), environ=lambda: dict(self.captured))
        self.popen_calls = []

    def values(self, mode):
        values = {'CAP_SOURCE_SHA': self.source, 'CAP_READ_EXPECTED_REVISION': 'capacity-' + self.source,
                  'API_ADDR': '0.0.0.0:8281', 'GO_MYSQL_MAX_OPEN_CONNS': '64',
                  'MYSQL_PASSWORD': 'synthetic-private-value'}
        if mode != 'missing': values['CAP10_OBSERVER_MODE'] = mode
        self.input.write_text(json.dumps(values), encoding='utf-8')

    def edges(self):
        owned = self.owned
        def popen(argv, **kwargs):
            self.popen_calls.append(argv)
            self.assertEqual(argv, [str(self.binary)])
            self.captured.update(kwargs['env'])
            return SimpleNamespace(pid=self.process.pid, poll=lambda: None)
        return contextlib.ExitStack(), {
            'ROOT': self.root, 'REPO': self.base, 'PLATFORM': 'linux',
            'settings': lambda path: (owned.read(path), self.runtime), 'port_open': lambda port: False,
            'environment': lambda value: ({'containers': {name: {'id': 'synthetic-' + name} for name in owned.PORTS}}, self.metadata, 'baseline'),
            'container': lambda *args, **kwargs: {},
            'transition': lambda: self.transition,
            'run': lambda argv: SimpleNamespace(stdout='vcs.revision=' + self.source + '\nvcs.modified=false'),
            'get_json': lambda url: {'version': 'capacity-' + self.source} if url.endswith('/health/ready') else {'pid': self.process.pid},
        }, popen

    def launch(self, mode, *, label='observed'):
        self.values(mode)
        stack, values, popen = self.edges()
        with stack:
            stack.enter_context(patch.multiple(self.owned, **values))
            stack.enter_context(patch.object(self.owned.subprocess, 'Popen', side_effect=popen))
            stack.enter_context(patch.object(self.owned.psutil, 'Process', return_value=self.process))
            stack.enter_context(contextlib.redirect_stdout(io.StringIO()))
            self.owned.start_api(SimpleNamespace(environment=str(self.input), binary=str(self.binary),
                source_sha=self.source, label=label, tail=False, like_diagnostics=False))
            metadata = self.owned.read(self.root / ('api-' + label + '.json'))
            self.assertEqual(self.owned.api_identity(metadata, health=True), self.process)
        return metadata

    def check_identity(self, metadata):
        stack, values, _ = self.edges()
        with stack:
            stack.enter_context(patch.multiple(self.owned, **values))
            stack.enter_context(patch.object(self.owned.psutil, 'Process', return_value=self.process))
            return self.owned.api_identity(metadata)

    def test_actual_launcher_pins_default_mode_and_records_exact_vector_policy(self):
        metadata = self.launch('missing')
        self.assertEqual(self.captured['CAP10_OBSERVER_MODE'], policy.FULL_HISTORY)
        self.assertEqual(metadata['observerPolicy'], policy.environment_policy({}))
        self.assertEqual(metadata['observerSamplingLimits'], policy.sampling_limits())
        self.captured.clear()
        metadata = self.launch(policy.LIKE_VECTOR, label='vector')
        self.assertEqual(metadata['observerPolicy'], policy.environment_policy({'CAP10_OBSERVER_MODE': policy.LIKE_VECTOR}))
        self.assertNotIn('synthetic-private-value', json.dumps(metadata))

    def test_invalid_mode_is_rejected_before_registration_or_process_start(self):
        with self.assertRaises(ValueError): self.launch(None)
        self.assertEqual(self.popen_calls, [])
        self.assertEqual(list(self.runtime.glob('owned-env-*')), [])

    def test_identity_refuses_resealed_policy_sampling_or_actual_process_drift(self):
        metadata = self.launch(policy.LIKE_VECTOR)
        for changed in ({**metadata, 'observerPolicy': policy.environment_policy({})},
                        {**metadata, 'observerSamplingLimits': {**policy.sampling_limits(), 'loadBudgetMs': 120000}},
                        {**metadata, 'observerSamplingLimits': {**policy.sampling_limits(), 'loadBudgetMs': 4000.0}},
                        {key: value for key, value in metadata.items() if key != 'observerPolicy'}):
            with self.assertRaises((ValueError, AssertionError)): self.check_identity(changed)
        self.captured['CAP10_OBSERVER_MODE'] = policy.FULL_HISTORY
        with self.assertRaises(AssertionError): self.check_identity(metadata)

    def test_historical_metadata_only_keeps_missing_legacy_compatibility(self):
        metadata = self.launch('missing')
        with self.assertRaises(AssertionError):
            self.check_identity({key: value for key, value in metadata.items() if key != 'observerSamplingLimits'})
        historical = {key: value for key, value in metadata.items() if key not in ('observerPolicy', 'observerSamplingLimits')}
        private = self.owned.read(historical['privateEnvironmentFile'])
        private.pop('CAP10_OBSERVER_MODE')
        Path(historical['privateEnvironmentFile']).write_text(json.dumps(private), encoding='utf-8')
        self.assertEqual(self.check_identity(historical), self.process)
        private['CAP10_OBSERVER_MODE'] = policy.LIKE_VECTOR
        Path(historical['privateEnvironmentFile']).write_text(json.dumps(private), encoding='utf-8')
        with self.assertRaises(ValueError): self.check_identity(historical)


if __name__ == '__main__':
    unittest.main(verbosity=2)
