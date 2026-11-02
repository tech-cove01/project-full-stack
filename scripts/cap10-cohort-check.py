"""Pure legacy/new cohort contract checks; no live dependencies or reservations."""
import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('cohort_contract', Path(__file__).with_name('cap10-cohort.py'))
cohort = importlib.util.module_from_spec(spec); spec.loader.exec_module(cohort)


class Checks(unittest.TestCase):
    def test_old_artifacts_keep_their_original_meaning(self):
        self.assertEqual(cohort.fixture_pool_size({'users': 512, 'multiNotes': 512, 'multiRelations': 512}), 512)
        self.assertEqual(cohort.command_pool_size({'tag': 'H1'}), 512)
        self.assertEqual(cohort.dimensions({'expectedEngines': 2}, {}),
                         {'poolSize': 512, 'expectedEngines': 2, 'ptsConcurrency': 513})

    def test_large_and_uneven_cohorts_have_explicit_dimensions(self):
        fixture = {'poolSize': 4096, 'users': 4096, 'multiNotes': 4096, 'multiRelations': 4096}
        self.assertEqual(cohort.dimensions({'poolSize': 4096, 'expectedEngines': 9}, fixture),
                         {'poolSize': 4096, 'expectedEngines': 9, 'ptsConcurrency': 4105})
        self.assertEqual(cohort.pts_concurrency(17, 3), 20)
        self.assertGreaterEqual(4105 // 9, (4096 + 8) // 9)

    def test_new_context_cannot_relabel_an_old_command_or_fixture(self):
        for command, fixture in (({}, {'poolSize': 4096}), ({'poolSize': 4096}, {})):
            with self.assertRaises(ValueError): cohort.command_pool_size(command, fixture)
        with self.assertRaises(ValueError): cohort.dimensions({'poolSize': 4096, 'expectedEngines': 9}, {})
        with self.assertRaises(ValueError): cohort.dimensions({'expectedEngines': 2}, {'poolSize': 4096})

    def test_invalid_dimensions_and_inconsistent_arrays_fail_closed(self):
        for value in (0, -1, 4097, True, None, '4096', 4096.0):
            with self.assertRaises(ValueError): cohort.pool_size(value)
        for value in (0, 17, True, None, 2.0):
            with self.assertRaises(ValueError): cohort.engine_count(value, pool=4096)
        with self.assertRaises(ValueError): cohort.engine_count(3, pool=2)
        for fixture in ({'poolSize': None}, {'poolSize': 17, 'users': 512},
                        {'poolSize': 17, 'userIds': ['1']}, {'poolSize': 17, 'multiNotes': True}):
            with self.assertRaises(ValueError): cohort.fixture_pool_size(fixture)
        for value in (4096, 4104, True, '4105'):
            with self.assertRaises(ValueError):
                cohort.dimensions({'poolSize': 4096, 'expectedEngines': 9, 'ptsConcurrency': value}, {'poolSize': 4096})


if __name__ == '__main__': unittest.main(verbosity=2)
