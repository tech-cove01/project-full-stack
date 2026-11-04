"""Pure versioned startup-retry producer checks. No observer, services or network."""
from contextlib import redirect_stdout
import datetime as dt
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest import mock


spec = importlib.util.spec_from_file_location('cap10_idle_retry_producer_target', Path(__file__).with_name('cap10-idle.py'))
I = importlib.util.module_from_spec(spec)
spec.loader.exec_module(I)


class Checks(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix='cap10-idle-retry-producer-')
        self.addCleanup(temporary.cleanup)
        self.base = Path(temporary.name).resolve()
        self.root = self.base / 'evidence/linux'; self.root.mkdir(parents=True)
        self.runtime = self.base / 'private'; self.runtime.mkdir()
        self.environment_path = self.root / 'environment-012345abcdef.json'
        limits = I.RESOURCE.container_limits('mysql8-v1')
        containers = {service: {'id': format(index + 1, '064x'), 'cpuNano': cpu, 'memoryBytes': memory,
                                'volumes': [{'name': 'synthetic-' + service}]}
                      for index, (service, (cpu, memory)) in enumerate(limits.items())}
        self.metadata = {'resourceProfile': 'mysql8-v1', 'containers': containers}
        self.put(self.environment_path, self.metadata)
        identity = {'environmentPath': self.environment_path.name, 'environmentSHA256': self.sha(self.environment_path),
                    'resourceProfile': 'mysql8-v1'}
        self.original = I.IDLE.idle_directory(self.environment_path, self.root, 'mysql8-v1'); self.original.mkdir()
        common = {**identity, 'idleSchemaVersion': 1, 'idleDirectory': self.original.name,
                  'attempt': 1, 'seconds': 60, 'intervalSeconds': 5}
        reservation = self.original / 'idle-reservation.json'
        self.put(reservation, {**common, 'failedAttemptConsumesOnlyIdleObservation': True})
        failed = self.original / 'idle-result.json'
        self.put(failed, {**common, 'passed': False, 'samples': [], 'noWorkloadStarted': True,
                          'finishedAt': '2026-11-02T00:00:00Z', 'reservationPath': reservation.relative_to(self.root).as_posix(),
                          'reservationSHA256': self.sha(reservation)})
        log = self.original / 'idle-observer.txt'
        log.write_bytes(b'cap10-downstream: invalid complete vector baseline audit\n')
        closure = self.root / (self.original.name + '-startup-closure.json')
        self.put(closure, {**identity, 'version': 1, 'kind': 'versioned-idle-startup-closure',
                           'failedIdleResultSHA256': self.sha(failed), 'observedAt': '2026-11-02T00:00:01Z',
                           'idleOwnerReleased': True, 'observerAbsent': True, 'launcherExited': True, 'noWorkloadStarted': True})
        self.authorization = self.root / (self.original.name + '-retry-2-authorization.json')
        auth = {**identity, 'version': 1, 'kind': 'versioned-idle-startup-retry', 'attempt': 2,
                'recordedAt': '2026-11-02T00:00:02Z', 'userAuthorizationReference': 'synthetic explicit continuation'}
        for key, path in (('failedIdleResult', failed), ('failedIdleReservation', reservation),
                          ('failedObserverLog', log), ('closureProof', closure)):
            auth[key] = {'path': path.relative_to(self.root).as_posix(), 'sha256': self.sha(path)}
        self.put(self.authorization, auth)
        self.retained = {path: path.read_bytes() for path in (*self.original.iterdir(), closure, self.authorization)}
        self.launch = self.runtime / 'env-final.private.json'
        self.value = {'CAP_SOURCE_SHA': 'a' * 40, 'CAP_READ_EXPECTED_REVISION': 'capacity-' + 'a' * 40,
                      'CAP10_OBSERVER_MODE': 'like-vector-v1', 'CAP_LIKE_FIXTURE': str(self.root / 'fixture.json')}
        self.put(self.launch, self.value)
        self.api_metadata = {**identity, 'pid': 12345, 'privateEnvironmentFile': str(self.launch),
                             'sourceSha': self.value['CAP_SOURCE_SHA'], 'expectedVersion': self.value['CAP_READ_EXPECTED_REVISION']}
        self.api_path = self.root / 'api-final.json'; self.put(self.api_path, self.api_metadata)
        self.owned = SimpleNamespace(ROOT=self.root, PLATFORM='linux', FLAGS=0,
            settings=lambda p: (self.value, self.runtime), read=lambda p: json.loads(Path(p).read_bytes()),
            save=self.put, environment=lambda value: (self.metadata, self.environment_path, 'mysql8-v1'),
            api_identity=lambda *a, **kw: SimpleNamespace(pid=12345), transition=lambda: 'b' * 64,
            now=lambda: dt.datetime.now(dt.timezone.utc).isoformat(),
            container=lambda service, *a, **kw: containers[service])
        for target in ('subprocess.Popen', 'socket.socket'):
            patch = mock.patch(target, side_effect=AssertionError('real operation forbidden'))
            patch.start(); self.addCleanup(patch.stop)

    @staticmethod
    def sha(path):
        return hashlib.sha256(path.read_bytes()).hexdigest()

    @staticmethod
    def put(path, value):
        with Path(path).open('x', encoding='utf-8') as stream:
            json.dump(value, stream)

    def claim(self, arguments=None):
        return I.claim_versioned_idle(self.root, self.runtime, self.environment_path, 'mysql8-v1', self.owned,
            arguments if arguments is not None else (str(self.authorization), None, None), environment=str(self.launch))

    def assert_retained(self):
        self.assertEqual({path: path.read_bytes() for path in self.retained}, self.retained)

    def test_explicit_retry_preserves_fields_and_exclusive_second_namespace(self):
        expected_directory, fields = I.IDLE.validate_versioned_retry(self.root, self.environment_path, 'mysql8-v1', self.authorization)
        with mock.patch.object(I.psutil, 'process_iter', return_value=[]):
            directory, owner = self.claim()
        self.assertEqual(directory, expected_directory)
        self.assertEqual(owner['attemptFields'], fields)
        self.assertEqual(self.owned.read(self.runtime / 'cap10-active.private.json'), owner)
        self.assertTrue(I.release_idle_owner(self.runtime, owner, None, self.owned))
        with mock.patch.object(I.psutil, 'process_iter', return_value=[]), self.assertRaises(AssertionError):
            self.claim()
        self.assertFalse((self.runtime / 'cap10-active.private.json').exists())
        self.assert_retained()

    def test_no_automatic_retry_or_legacy_additional_flags(self):
        for arguments in ((None, None, None), (str(self.authorization), 'additional', None),
                          (str(self.authorization), None, 'interruption')):
            with self.subTest(arguments=arguments), self.assertRaises(AssertionError):
                self.claim(arguments)
        self.assertFalse((self.runtime / 'cap10-active.private.json').exists())
        self.assert_retained()

    def test_live_old_observer_launcher_or_unknown_process_access_blocks_retry(self):
        commands = [['observer', '--output', str(self.original / 'idle-downstream')],
                    ['observer', '--done-file', str(self.original / 'idle-control/done.json')],
                    ['observer', '--output', str(self.original / 'idle-downstream'), '--output', '/other'],
                    ['python3', '/repo/scripts/cap10-idle.py', '--environment', str(self.launch)]]
        for command in commands:
            process = SimpleNamespace(pid=os.getpid() + 1000, cmdline=lambda command=command: command)
            with self.subTest(command=command), mock.patch.object(I.psutil, 'process_iter', return_value=[process]), \
                    self.assertRaises(AssertionError):
                self.claim()
        denied = SimpleNamespace(pid=os.getpid() + 1000, cmdline=mock.Mock(side_effect=I.psutil.AccessDenied()))
        with mock.patch.object(I.psutil, 'process_iter', return_value=[denied]), self.assertRaises(I.psutil.AccessDenied):
            self.claim()
        self.assertFalse((self.runtime / 'cap10-active.private.json').exists())

    def test_same_environment_reservation_or_existing_owner_blocks_retry(self):
        slot = self.root / 'slot-S20.json'
        self.put(slot, {'environmentPath': self.environment_path.name, 'resourceProfile': 'mysql8-v1'})
        with mock.patch.object(I.psutil, 'process_iter', return_value=[]), self.assertRaises(AssertionError):
            self.claim()
        slot.unlink()
        lock = self.runtime / 'cap10-active.private.json'; self.put(lock, {'kind': 'another-owned-operation'})
        with mock.patch.object(I.psutil, 'process_iter', return_value=[]), self.assertRaises(FileExistsError):
            self.claim()
        self.assertEqual(self.owned.read(lock), {'kind': 'another-owned-operation'})
        self.assert_retained()

    def test_actual_main_writes_original_shared_attempt_fields_to_both_files(self):
        directory, fields = I.IDLE.validate_versioned_retry(self.root, self.environment_path, 'mysql8-v1', self.authorization)
        arguments = ['cap10-idle.py', '--environment', str(self.launch), '--api-metadata', str(self.api_path),
                     '--observer', '/original/observer', '--retry-authorization', str(self.authorization)]
        with mock.patch.object(I, 'module', side_effect=lambda name, filename: self.owned if filename == 'cap10-env.py' else SimpleNamespace()), \
                mock.patch('sys.argv', arguments), mock.patch.object(I.psutil, 'process_iter', return_value=[]), \
                mock.patch.object(I, 'prepare_vector_baseline', side_effect=ValueError('synthetic failure after reservation')), \
                redirect_stdout(io.StringIO()), self.assertRaises(SystemExit) as stopped:
            I.main()
        self.assertEqual(stopped.exception.code, 1)
        for name in ('idle-result.json', 'idle-reservation.json'):
            recorded = self.owned.read(directory / name)
            for key, value in fields.items():
                self.assertEqual(recorded[key], value)
        self.assertEqual(self.owned.read(directory / 'idle-result.json')['samples'], [])
        self.assertFalse((self.runtime / 'cap10-active.private.json').exists())
        self.assert_retained()


if __name__ == '__main__':
    unittest.main(verbosity=2)
