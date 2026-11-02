"""Pure CPU-stat compatibility checks; no Docker, cloud or workload calls."""
import importlib.util
from pathlib import Path
from types import SimpleNamespace
import unittest
from unittest.mock import patch


REPO = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location('cap10_resource_checks', REPO / 'scripts/cap10-resources.py')
resources = importlib.util.module_from_spec(spec)
spec.loader.exec_module(resources)
CID = 'a' * 64
BASE = [('usage_usec', '100000'), ('user_usec', '70000'), ('system_usec', '30000'),
        ('nr_periods', '100'), ('nr_throttled', '2'), ('throttled_usec', '1200'),
        ('nr_bursts', '0'), ('burst_usec', '0')]


class Checks(unittest.TestCase):
    def sample(self, pairs, quota='400000 100000', version=2):
        paths = ['/sys/fs/cgroup/cpu.stat', '/sys/fs/cgroup/cpu.max']
        suffix = quota + '\n'
        if version == 1:
            paths = ['/sys/fs/cgroup/cpu.stat', '/sys/fs/cgroup/cpu.cfs_quota_us',
                     '/sys/fs/cgroup/cpu.cfs_period_us', '/sys/fs/cgroup/cpuacct.usage']
            suffix = '400000\n100000\n1000000\n'
        layout = {'version': version, 'membership': '/', 'paths': paths}
        text = ''.join(key + ' ' + value + '\n' for key, value in pairs) + suffix
        with patch.dict(resources.CPU_LAYOUTS, {CID: layout}, clear=True), \
                patch.object(resources.subprocess, 'run', return_value=SimpleNamespace(stdout=text)) as run:
            result = resources.cpu_stat({'id': CID, 'service': 'mysql'})
            run.assert_called_once_with(['docker', 'exec', CID, 'cat', *paths],
                capture_output=True, text=True, timeout=4, check=True)
        return result

    def test_original_v2_fields_and_quota_remain_exact(self):
        result = self.sample(BASE)
        self.assertEqual(result['rawCounters'], {key: int(value) for key, value in BASE})
        self.assertEqual(result['counters'], result['rawCounters'])
        self.assertEqual((result['quotaUsec'], result['periodUsec']), (400000, 100000))

    def test_ubuntu_core_sched_field_is_preserved(self):
        # Ubuntu 24 adds this exact dotted field; other values are synthetic.
        pairs = BASE + [('core_sched.force_idle_usec', '0')]
        result = self.sample(pairs)
        self.assertEqual(result['rawCounters'], {key: int(value) for key, value in pairs})
        self.assertEqual(result['counters']['core_sched.force_idle_usec'], 0)

    def test_malformed_dotted_keys_are_rejected(self):
        for key in ('.core_sched', 'core_sched.', 'core_sched..force_idle_usec',
                    'core-sched.force_idle_usec', 'Core_sched.force_idle_usec',
                    'core_sched.force_idle_usec/extra', 'core_sched.1force_idle_usec'):
            with self.subTest(key=key), self.assertRaises(ValueError):
                self.sample(BASE + [(key, '0')])

    def test_duplicate_original_or_dotted_counter_is_rejected(self):
        for key in ('usage_usec', 'core_sched.force_idle_usec'):
            with self.subTest(key=key), self.assertRaises(ValueError):
                pairs = BASE if key == 'usage_usec' else BASE + [(key, '0')]
                self.sample(pairs + [(key, '1')])

    def test_only_nonnegative_ascii_integer_counters_are_accepted(self):
        for value in ('-1', '+1', '1.5', 'nan', 'inf', 'NaN', '1e3', '１２'):
            with self.subTest(value=value), self.assertRaises(ValueError):
                self.sample(BASE + [('core_sched.force_idle_usec', value)])

    def test_required_fields_and_exact_quota_are_still_required(self):
        for required in ('usage_usec', 'nr_periods', 'nr_throttled', 'throttled_usec'):
            with self.subTest(required=required), self.assertRaises(ValueError):
                self.sample([(key, value) for key, value in BASE if key != required] +
                    [('core_sched.force_idle_usec', '0')])
        with self.assertRaises(ValueError):
            self.sample(BASE + [('core_sched.force_idle_usec', '0')], quota='200000 100000')

    def test_v1_units_and_original_raw_fields_are_preserved(self):
        pairs = [('nr_periods', '100'), ('nr_throttled', '2'), ('throttled_time', '1200000'),
                 ('core_sched.force_idle_usec', '0')]
        result = self.sample(pairs, version=1)
        self.assertEqual(result['rawCounters'], {key: int(value) for key, value in pairs})
        self.assertEqual(result['cpuacctUsageNanoseconds'], 1000000)
        self.assertEqual(result['counters'], {'usage_usec': 1000, 'nr_periods': 100,
            'nr_throttled': 2, 'throttled_usec': 1200})


if __name__ == '__main__':
    unittest.main()
