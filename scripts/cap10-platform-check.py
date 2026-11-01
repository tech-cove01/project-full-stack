"""Pure checks of the actual CAP10 power/transition functions; no collection."""
import ast
import datetime as dt
import hashlib
import json
import math
from pathlib import Path
import re
from types import SimpleNamespace
import unittest
import uuid

REPO = Path(__file__).resolve().parent.parent
SOURCE = REPO / 'scripts/cap10-env.py'
NAMES = {'read', 'clock_valid', 'power_valid', 'power_window_evidence', 'transition'}
TREE = ast.parse(SOURCE.read_text(encoding='utf-8'))
FUNCTIONS = ast.Module(body=[node for node in TREE.body if isinstance(node, ast.FunctionDef) and node.name in NAMES], type_ignores=[])
BOOT = '12345678-1234-1234-1234-123456789abc'


class Checks(unittest.TestCase):
    def setUp(self):
        self.root = REPO.parent / ('.qa-cap10-platform-' + uuid.uuid4().hex)
        self.root.mkdir()
        self.addCleanup(self.cleanup)
        def forbidden(*args, **kwargs):
            raise AssertionError('Pure checker attempted subprocess/resource collection')
        self.module = {'PLATFORM': 'linux', 'BATCH_ROOT': self.root, 'Path': Path, 'json': json,
                       'math': math, 're': re, 'dt': dt, 'hashlib': hashlib, 'FLAGS': 0,
                       'subprocess': SimpleNamespace(run=forbidden)}
        exec(compile(FUNCTIONS, str(SOURCE) + ' (actual isolated functions)', 'exec'), self.module)
        self.transition = {
            'version': 1, 'batch': 'CAP-10', 'fromPlatform': 'windows', 'toPlatform': 'linux',
            'switchCount': 1, 'authorized': True, 'userAuthorizationReference': 'synthetic-user-host-reference',
            'hostLabel': 'synthetic-owned-Linux-host', 'localCheckpointSHA': 'a' * 40,
            'recordedAt': '2026-11-01T09:00:00+00:00',
            'shortSlotsUsedBeforeSwitch': 0, 'formalSlotsUsedBeforeSwitch': 0,
            'optimizationsUsedBeforeSwitch': 0, 'mainLoadSecondsUsedBeforeSwitch': 0,
            'maxShortWindows': 8, 'maxFormalWindows': 2, 'maxMainLoadSeconds': 840,
            'maxOptimizations': 1, 'effectiveWorkSecondsMaximum': 10800,
            'effectiveWorkSecondsUsedBeforeSwitch': 4500, 'effectiveWorkSecondsRemaining': 6300,
        }
        for key, name, data in [
            ('localEnvironmentSHA256', 'environment.json', {'synthetic': 'Windows original'}),
            ('localIdleResultSHA256', 'idle-result.json', {'passed': False}),
            ('localCleanupSHA256', 'cleanup.json', {'complete': True}),
            ('localRawManifestSHA256', 'raw-manifest.json', {'synthetic': 'original captures'}),
            ('budgetHandoffSHA256', 'budget-handoff.json', {'version': 1, 'batch': 'CAP-10', 'maxEffectiveWorkSeconds': 10800,
                'countedEffectiveWorkSecondsUpperBound': 4200, 'remainingEffectiveWorkSecondsLowerBound': 6600,
                'recordedAt': '2026-11-01T08:50:00+00:00'}),
        ]:
            path = self.put(name, data)
            self.transition[key] = hashlib.sha256(path.read_bytes()).hexdigest()

    def cleanup(self):
        assert self.root.parent == REPO.parent and self.root.name.startswith('.qa-cap10-platform-')
        for path in self.root.iterdir():
            assert path.is_file() and path.resolve().parent == self.root
            path.unlink()
        self.root.rmdir()

    def put(self, name, value):
        path = self.root / name
        path.write_text(json.dumps(value), encoding='utf-8')
        return path

    def power(self, seconds=0):
        return {'platform': 'linux', 'ac': None, 'batterySaver': None,
                'batteryApplicable': False, 'powerSupplyDirectoryPresent': True, 'powerSupplyEntries': [],
                'clock': {'wallUnixSeconds': 1000 + seconds, 'monotonicSeconds': 100 + seconds,
                          'boottimeSeconds': 200 + seconds, 'bootId': BOOT, 'uncertaintySeconds': .001}}

    def window(self, before=None, after=None, lightweight=True):
        return self.module['power_window_evidence'](before or self.power(), after or self.power(60),
            '2026-11-01T09:00:00+00:00', lightweight=lightweight)

    def validate_transition(self, **changes):
        path = self.put('environment-transition.json', {**self.transition, **changes})
        return self.module['transition'](), hashlib.sha256(path.read_bytes()).hexdigest()

    def test_linux_server_explicit_na_and_real_clock_fields(self):
        self.assertTrue(self.module['power_valid'](self.power()))
        evidence = self.window(lightweight=False)
        self.assertTrue(evidence['valid'])
        self.assertFalse(evidence['journalEventsAvailable'])
        self.assertIn('hypervisor', evidence['limitations'])

    def test_battery_or_fake_ac_saver_na_is_rejected(self):
        for changes in ({'batteryApplicable': True}, {'powerSupplyDirectoryPresent': False}, {'ac': 1}, {'batterySaver': 0}):
            with self.subTest(changes=changes):
                self.assertFalse(self.module['power_valid']({**self.power(), **changes}))

    def test_missing_bool_nan_and_bad_boot_clock_rejected(self):
        for key, value in [('wallUnixSeconds', True), ('wallUnixSeconds', 0), ('monotonicSeconds', -1),
                           ('monotonicSeconds', float('nan')), ('boottimeSeconds', 99),
                           ('boottimeSeconds', float('inf')), ('uncertaintySeconds', False), ('bootId', 'unknown')]:
            row = self.power(); row['clock'][key] = value
            self.assertFalse(self.module['power_valid'](row))
        row = self.power(); del row['clock']['boottimeSeconds']
        self.assertFalse(self.module['power_valid'](row))

    def test_lightweight_windows_and_linux_never_spawn(self):
        self.assertTrue(self.window()['valid'])
        self.module['PLATFORM'] = 'windows'
        before, after = self.power(), self.power(60)
        for row in (before, after):
            row.update(platform='windows', ac=1, batterySaver=0)
        self.assertTrue(self.window(before, after)['valid'])

    def test_boot_identity_change_rejected(self):
        after = self.power(60); after['clock']['bootId'] = 'abcdefab-1234-1234-1234-123456789abc'
        self.assertFalse(self.window(after=after)['valid'])

    def test_wall_clock_jump_rejected(self):
        after = self.power(60); after['clock']['wallUnixSeconds'] += 1
        self.assertFalse(self.window(after=after)['valid'])

    def test_suspend_offset_rejected(self):
        after = self.power(60); after['clock']['boottimeSeconds'] += 1
        self.assertFalse(self.window(after=after)['valid'])

    def test_cross_platform_window_rejected(self):
        before = self.power(); before.update(platform='windows', ac=1, batterySaver=0)
        self.assertFalse(self.window(before=before)['valid'])

    def test_backwards_monotonic_rejected(self):
        after = self.power(60); after['clock']['monotonicSeconds'] = 99
        self.assertFalse(self.window(after=after)['valid'])

    def test_transition_keeps_original_hashes_and_budget(self):
        actual, expected = self.validate_transition()
        self.assertEqual(actual, expected)
        actual, expected = self.validate_transition(effectiveWorkSecondsUsedBeforeSwitch=4200, effectiveWorkSecondsRemaining=6600)
        self.assertEqual(actual, expected)

    def test_transition_requires_authorized_user_host(self):
        for changes in ({'authorized': False}, {'hostLabel': ''}, {'userAuthorizationReference': ''}):
            with self.subTest(changes=changes), self.assertRaises(AssertionError):
                self.validate_transition(**changes)

    def test_second_switch_and_noninteger_switch_rejected(self):
        for value in (2, True):
            with self.subTest(value=value), self.assertRaises(AssertionError):
                self.validate_transition(switchCount=value)

    def test_transition_cannot_reset_quota_or_work_budget(self):
        for changes in ({'shortSlotsUsedBeforeSwitch': 1}, {'maxShortWindows': 9},
                        {'effectiveWorkSecondsUsedBeforeSwitch': 0, 'effectiveWorkSecondsRemaining': 10800}):
            with self.subTest(changes=changes), self.assertRaises(AssertionError):
                self.validate_transition(**changes)
        self.put('slot-S1.json', {'synthetic': 'retained prior reservation'})
        with self.assertRaises(AssertionError):
            self.validate_transition()
        (self.root / 'slot-S1.json').unlink()
        handoff = json.loads((self.root / 'budget-handoff.json').read_text())
        handoff.update(countedEffectiveWorkSecondsUpperBound=0, remainingEffectiveWorkSecondsLowerBound=10800)
        path = self.put('budget-handoff.json', handoff)
        self.transition['budgetHandoffSHA256'] = hashlib.sha256(path.read_bytes()).hexdigest()
        with self.assertRaises(AssertionError):
            self.validate_transition(effectiveWorkSecondsUsedBeforeSwitch=0, effectiveWorkSecondsRemaining=10800)

    def test_tampered_capture_is_rejected(self):
        self.put('environment.json', {'synthetic': 'modified capture'})
        with self.assertRaises(AssertionError):
            self.validate_transition()

    def test_naive_transition_time_is_rejected(self):
        with self.assertRaises(AssertionError):
            self.validate_transition(recordedAt='2026-11-01T09:00:00')


if __name__ == '__main__':
    result = unittest.TextTestRunner(verbosity=2).run(unittest.defaultTestLoader.loadTestsFromTestCase(Checks))
    print(json.dumps({'checks': result.testsRun, 'failed': len(result.failures) + len(result.errors),
                      'actualCollection': False, 'servicesStarted': False, 'loadRuns': 0,
                      'reviewedEnvironmentSourceSHA256': hashlib.sha256(SOURCE.read_bytes()).hexdigest()}))
    raise SystemExit(not result.wasSuccessful())
