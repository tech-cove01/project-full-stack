"""Focused policy checks; one bounded local Node import, no services or load."""
import copy
import importlib.util
import json
from pathlib import Path
import subprocess
import unittest

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


if __name__ == '__main__':
    unittest.main(verbosity=2)
