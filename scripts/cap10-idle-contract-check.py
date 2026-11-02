"""Pure namespace/preflight checks in new synthetic local directories."""
import copy
import hashlib
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest


REPO = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location('cap10_idle_contract_checks', Path(__file__).with_name('cap10-idle-contract.py'))
idle = importlib.util.module_from_spec(spec)
spec.loader.exec_module(idle)


class Checks(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix='.qa-cap10-idle-contract-', dir=REPO.parent)
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name).resolve()

    def put(self, name, value):
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(value), encoding='utf-8')
        return path

    def sha(self, path):
        return hashlib.sha256(path.read_bytes()).hexdigest()

    def fixture(self, *, versioned=True, profile='mysql8-v1'):
        name = 'environment-012345abcdef.json' if versioned else 'environment.json'
        metadata = self.put(name, {'resourceProfile': profile})
        directory = idle.idle_directory(metadata, self.root, profile)
        prefix = '' if directory == self.root else directory.name + '/'
        identity = {'environmentPath': name, 'environmentSHA256': self.sha(metadata), 'resourceProfile': profile}
        reservation = {**identity, 'idleSchemaVersion': 1, 'idleDirectory': directory.name,
                       'seconds': 60, 'intervalSeconds': 5, 'attempt': 1}
        reservation_path = self.put(prefix + 'idle-reservation.json', reservation)
        result = {**reservation, 'passed': True, 'noWorkloadStarted': True, 'errors': [],
                  'cadenceValid': True, 'uptimeValid': True, 'downstreamIdleAndComplete': True,
                  'threeConsecutiveBusy': False, 'observerExit': 0, 'hostCPUMedian': 20,
                  'hostMemoryMaxPercent': 60, 'downstreamSamples': 12, 'powerEvents': {'valid': True},
                  'samples': [{'elapsedSeconds': 5 * i, 'cpuIntervalValid': i > 0, 'cpuIntervalSeconds': 5}
                              for i in range(13)], 'clockIntervals': [{'valid': True} for _ in range(12)],
                  'reservationPath': prefix + 'idle-reservation.json', 'reservationSHA256': self.sha(reservation_path)}
        result_path = self.put(prefix + 'idle-result.json', result)
        command = {**identity, 'preflightPath': prefix + 'idle-result.json', 'preflightSHA256': self.sha(result_path)}
        return command, result_path, result

    def reject_result(self, command, path, original, edit):
        result = copy.deepcopy(original)
        edit(result)
        path.write_text(json.dumps(result), encoding='utf-8')
        with self.assertRaises(ValueError):
            idle.validate_preflight({**command, 'preflightSHA256': self.sha(path)}, self.root)

    def test_versioned_namespace_is_derived_from_original_filename(self):
        command, path, _ = self.fixture()
        self.assertEqual(idle.preflight_path(command, self.root), path)
        self.assertEqual(idle.validate_preflight(command, self.root)['path'], 'idle-012345abcdef/idle-result.json')

    def test_legacy_missing_path_keeps_original_result_and_schema(self):
        metadata = self.put('environment.json', {})
        result = self.put('idle-result.json', {'passed': True, 'environmentSHA256': self.sha(metadata)})
        command = {'environmentSHA256': self.sha(metadata), 'preflightSHA256': self.sha(result)}
        self.fixture()
        self.assertEqual(idle.preflight_path(command, self.root), result)
        self.assertEqual(idle.validate_preflight(command, self.root)['resourceProfile'], 'baseline')

    def test_versioned_baseline_requires_explicit_reference(self):
        command, _, _ = self.fixture(profile='baseline')
        self.assertEqual(idle.validate_preflight(command, self.root)['resourceProfile'], 'baseline')
        command.pop('preflightPath')
        with self.assertRaises(ValueError):
            idle.preflight_path(command, self.root)

    def test_new_profile_cannot_overwrite_original_namespace(self):
        with self.assertRaises(ValueError):
            idle.idle_directory(self.root / 'environment.json', self.root, 'mysql8-v1')

    def test_namespace_paths_reject_escape_or_arbitrary_suffix(self):
        for name in ('../environment-012345abcdef.json', 'nested/environment-012345abcdef.json',
                     'environment-wrong.json', 'environment-012345ABCDEf.json'):
            with self.subTest(name=name), self.assertRaises(ValueError):
                idle.idle_directory(self.root / name, self.root)

    def test_new_command_requires_exact_canonical_path(self):
        command, _, _ = self.fixture()
        for path in (None, 'idle-result.json', '../idle-012345abcdef/idle-result.json',
                     'idle-abcdef012345/idle-result.json', '/idle-012345abcdef/idle-result.json'):
            modified = {**command, 'preflightPath': path}
            if path is None:
                modified.pop('preflightPath')
            with self.subTest(path=path), self.assertRaises(ValueError):
                idle.preflight_path(modified, self.root)

    def test_command_result_and_metadata_byte_hashes_are_independent(self):
        command, path, result = self.fixture()
        for field in ('preflightSHA256', 'environmentSHA256'):
            with self.subTest(field=field), self.assertRaises(ValueError):
                idle.validate_preflight({**command, field: '0' * 64}, self.root)
        for field in ('environmentPath', 'environmentSHA256', 'resourceProfile'):
            self.reject_result(command, path, result,
                               lambda item, field=field: item.update({field: 'environment.json' if field == 'environmentPath' else 'wrong'}))

    def test_identical_bytes_in_another_namespace_cannot_replace_reference(self):
        command, path, _ = self.fixture()
        other = self.root / 'idle-abcdef012345/idle-result.json'
        other.parent.mkdir()
        other.write_bytes(path.read_bytes())
        with self.assertRaises(ValueError):
            idle.validate_preflight({**command, 'preflightPath': 'idle-abcdef012345/idle-result.json'}, self.root)

    def test_incomplete_duration_samples_clock_and_downstream_cannot_pass(self):
        command, path, result = self.fixture()
        edits = [lambda v: v.update(passed=False), lambda v: v.update(seconds=59),
                 lambda v: v.update(intervalSeconds=4), lambda v: v.update(attempt=2),
                 lambda v: v.update(idleSchemaVersion=True), lambda v: v['samples'].pop(),
                 lambda v: v['samples'][-1].update(elapsedSeconds=59.99),
                 lambda v: v['samples'][1].update(cpuIntervalSeconds=7.6),
                 lambda v: v['clockIntervals'].pop(), lambda v: v['clockIntervals'][0].update(valid=False),
                 lambda v: v.update(downstreamIdleAndComplete=False), lambda v: v.update(downstreamSamples=11),
                 lambda v: v.update(hostCPUMedian=50.1), lambda v: v.update(hostMemoryMaxPercent=80.1),
                 lambda v: v.update(threeConsecutiveBusy=True), lambda v: v.update(observerExit=1),
                 lambda v: v.update(noWorkloadStarted=False), lambda v: v['powerEvents'].update(valid=False),
                 lambda v: v.update(errors=[{'class': 'synthetic'}])]
        for index, edit in enumerate(edits):
            with self.subTest(index=index):
                self.reject_result(command, path, result, edit)

    def test_new_result_binds_original_reservation_bytes(self):
        command, path, result = self.fixture()
        for field, wrong in (('reservationPath', 'idle-reservation.json'), ('reservationSHA256', '0' * 64)):
            self.reject_result(command, path, result, lambda v, field=field, wrong=wrong: v.update({field: wrong}))
        path.write_text(json.dumps(result), encoding='utf-8')
        reservation_path = self.root / result['reservationPath']
        reservation = json.loads(reservation_path.read_bytes())
        reservation['environmentPath'] = 'environment-abcdef012345.json'
        reservation_path.write_text(json.dumps(reservation), encoding='utf-8')
        self.reject_result(command, path, result, lambda v: v.update(reservationSHA256=self.sha(reservation_path)))

    def test_validation_does_not_rewrite_original_slots_or_idle(self):
        original = self.put('slot-S19.json', {'retained': True})
        old_idle = self.put('idle-result.json', {'passed': False, 'retained': True})
        before = {path: path.read_bytes() for path in (original, old_idle)}
        command, _, _ = self.fixture()
        idle.validate_preflight(command, self.root)
        self.assertTrue(all(path.read_bytes() == raw for path, raw in before.items()))


if __name__ == '__main__':
    unittest.main(verbosity=2)
