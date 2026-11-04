"""Pure profile/path compatibility checks; no Docker, cloud or service calls."""
import importlib.util
from pathlib import Path
import unittest


SPEC = importlib.util.spec_from_file_location('cap10_resource_contract_checks', Path(__file__).with_name('cap10-resource-contract.py'))
CONTRACT = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(CONTRACT)
ROOT = Path(__file__).resolve().parent.parent / 'docs/evidence/2026-11-01-cap10'


class Checks(unittest.TestCase):
    def test_baseline_and_explicit_target_have_bounded_separate_limits(self):
        baseline, target = CONTRACT.container_limits(), CONTRACT.container_limits('mysql8-v1')
        self.assertEqual(baseline['mysql'], (4_000_000_000, 2 << 30))
        self.assertEqual(target['mysql'], (8_000_000_000, 8 << 30))
        self.assertEqual({key: value for key, value in baseline.items() if key != 'mysql'},
                         {key: value for key, value in target.items() if key != 'mysql'})
        target['mysql'] = (0, 0)
        self.assertEqual(CONTRACT.container_limits('mysql8-v1')['mysql'], (8_000_000_000, 8 << 30))
        for invalid in (None, True, '', 'automatic', 'mysql16-v1'):
            with self.assertRaises(ValueError):
                CONTRACT.container_limits(invalid)

    def test_old_application_does_not_follow_new_environment(self):
        old = CONTRACT.application_metadata_path({}, ROOT)
        new = CONTRACT.application_metadata_path({'environmentPath': 'environment-012345abcdef.json'}, ROOT)
        self.assertEqual(old, ROOT / 'environment.json')
        self.assertEqual(new, ROOT / 'environment-012345abcdef.json')
        self.assertNotEqual(old, new)
        self.assertEqual(CONTRACT.metadata_path({}, ROOT), old)

    def test_kafka2_profile_changes_only_fixed_kafka_memory(self):
        original = CONTRACT.container_limits('mysql8-v1')
        target = CONTRACT.container_limits('mysql8-kafka2-v1')
        self.assertEqual(original['kafka'], (2_000_000_000, 1 << 30))
        self.assertEqual(target['kafka'], (2_000_000_000, 2 << 30))
        self.assertEqual({key: value for key, value in original.items() if key != 'kafka'},
                         {key: value for key, value in target.items() if key != 'kafka'})
        target['kafka'] = (0, 0)
        self.assertEqual(CONTRACT.container_limits('mysql8-kafka2-v1')['kafka'], (2_000_000_000, 2 << 30))
        self.assertEqual(CONTRACT.container_limits('mysql8-v1'), original)
        self.assertEqual(CONTRACT.bound_profile({'CAP10_RESOURCE_PROFILE': 'mysql8-kafka2-v1'},
                                              {'resourceProfile': 'mysql8-kafka2-v1'}), 'mysql8-kafka2-v1')
        for config, metadata in (({'CAP10_RESOURCE_PROFILE': 'mysql8-kafka2-v1'}, {'resourceProfile': 'mysql8-v1'}),
                                 ({'CAP10_RESOURCE_PROFILE': 'mysql8-v1'}, {'resourceProfile': 'mysql8-kafka2-v1'}),
                                 ({'CAP10_RESOURCE_PROFILE': 'mysql8-kafka4-v1'}, {'resourceProfile': 'mysql8-kafka4-v1'})):
            with self.assertRaises(ValueError):
                CONTRACT.bound_profile(config, metadata)

    def test_paths_are_explicit_root_files_not_arbitrary_cleanup_expansion(self):
        for name in ('../environment.json', 'nested/environment.json', 'environment-other.json', 'input.json', '', None):
            with self.assertRaises(ValueError):
                CONTRACT.application_metadata_path({'environmentPath': name}, ROOT)
        for raw in ('environment.json', str(ROOT.parent / 'environment.json'), str(ROOT / 'nested/environment.json'), None):
            with self.assertRaises(ValueError):
                CONTRACT.metadata_path({'CAP10_ENVIRONMENT_METADATA': raw}, ROOT)

    def test_profiles_must_be_bound_in_private_and_public_metadata(self):
        self.assertEqual(CONTRACT.bound_profile({}, {}), 'baseline')
        self.assertEqual(CONTRACT.bound_profile({'CAP10_RESOURCE_PROFILE': 'mysql8-v1'}, {'resourceProfile': 'mysql8-v1'}), 'mysql8-v1')
        for config, public in (({}, {'resourceProfile': 'mysql8-v1'}),
                               ({'CAP10_RESOURCE_PROFILE': 'mysql8-v1'}, {}),
                               ({'CAP10_RESOURCE_PROFILE': None}, {'resourceProfile': None})):
            with self.assertRaises(ValueError):
                CONTRACT.bound_profile(config, public)


if __name__ == '__main__':
    unittest.main(verbosity=2)
