"""Synthetic-only profile transition checks. No real subprocess/socket/SQL."""
import copy
import importlib.util
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest import mock


spec = importlib.util.spec_from_file_location('cap10_profile_check_target', Path(__file__).with_name('cap10-profile.py'))
P = importlib.util.module_from_spec(spec)
spec.loader.exec_module(P)


def facts(buffer=P.BASE_BUFFER):
    return {'version': '8.4.6', 'serverUUID': '11111111-2222-3333-4444-555555555555',
            'bufferBytes': buffer, 'flushAtCommit': 1, 'syncBinlog': 1, 'groupCommitDelay': 0,
            'isolation': 'REPEATABLE-READ', 'dedicatedServer': 0, 'logBin': 1, 'doublewrite': 'ON',
            'readOnly': 0, 'superReadOnly': 0, 'sqlMode': 'STRICT_TRANS_TABLES', 'instances': 1,
            'chunkBytes': 128 << 20, 'resizeCode': 0, 'resizeProgress': 0,
            'resizeText': 'Completed resizing buffer pool at 261102  3:00:00.', 'uptime': 100,
            'otherSessions': 0, 'activeTransactions': 0}


class Fixture:
    def __init__(self, directory):
        self.repo = directory / 'repo'
        self.repo.mkdir()
        self.runtime = directory / '.runtime-fullstack-cap10-0123456789ab'
        self.runtime.mkdir(mode=0o700)
        self.root = directory / 'evidence' / 'linux'
        self.root.mkdir(parents=True)
        self.configuration = {'CAP10_RUNTIME': str(self.runtime), 'CAP10_EVIDENCE': str(self.root),
                              'CAP_COHORT': 'cap10', 'CAP10_PLATFORM': 'linux',
                              'MYSQL_PASSWORD': 'synthetic-secret-not-real', 'GOMAXPROCS': '4'}
        self.private = self.runtime / 'env.private.json'
        P.save(self.private, self.configuration, private=True)
        self.containers, self.volumes = {}, {}
        project = P.OWNED.PROJECT
        network = 'f' * 64
        for number, service in enumerate(P.SERVICES, 1):
            name = project + '-0123456789ab-' + service
            mount = {'Type': 'volume', 'Name': name, 'Source': '/var/lib/docker/volumes/' + name + '/_data',
                     'Destination': P.OWNED.MOUNTS[service], 'Driver': 'local', 'Mode': 'rw', 'RW': True,
                     'Propagation': ''}
            self.volumes[name] = {'name': name, 'driver': 'local', 'mountpoint': mount['Source'],
                                  'labels': {'com.docker.compose.project': project}}
            cpu, memory = P.RESOURCE.container_limits()[service]
            self.containers[service] = {'id': str(number) * 64, 'name': '/' + project + '-' + service + '-1',
                                        'imageId': 'sha256:' + str(number + 4) * 64,
                                        'project': project, 'service': service, 'running': True,
                                        'healthStatus': 'healthy', 'cpuNano': cpu, 'memoryBytes': memory,
                                        'memorySwap': memory * 2, 'mounts': [mount],
                                        'ports': {P.OWNED.PORTS[service][1]: [{'HostIp': '127.0.0.1',
                                                  'HostPort': str(P.OWNED.PORTS[service][0])}]},
                                        'cmd': ['mysqld'] if service == 'mysql' else ['service'],
                                        'networks': {project + '_default': network},
                                        'policy': {key: None for key in P.POLICY_KEYS}}
        # A registered init bind and Kafka anonymous image volumes are retained;
        # exact snapshots bind their identities, without inventing owner labels.
        self.containers['mysql']['mounts'].append({'Type': 'bind', 'Source': '/original/infra/mysql',
                                                   'Destination': '/docker-entrypoint-initdb.d', 'Mode': 'ro',
                                                   'RW': False, 'Propagation': 'rprivate'})
        for number, destination in enumerate(('/etc/kafka/secrets', '/mnt/shared/config'), 10):
            name = format(number, 'x') * 64
            self.containers['kafka']['mounts'].append({'Type': 'volume', 'Name': name,
                'Source': '/var/lib/docker/volumes/' + name + '/_data', 'Destination': destination,
                'Driver': 'local', 'Mode': '', 'RW': True, 'Propagation': ''})
            self.volumes[name] = {'name': name, 'driver': 'local',
                                 'mountpoint': '/var/lib/docker/volumes/' + name + '/_data', 'labels': {}}
        self.metadata = {'project': project, 'executionPlatform': 'linux', 'containers': {},
                         'privateEnvironmentFile': str(self.private), 'groups': ['follow', 'feed', 'search']}
        for service, value in self.containers.items():
            self.metadata['containers'][service] = {key: value[key] for key in
                ('id', 'name', 'imageId', 'ports', 'cpuNano', 'memoryBytes')}
            self.metadata['containers'][service].update(networkId=network,
                volumes=[{'name': name, 'destination': destination} for name, destination in P.volume_pairs(value)])
        P.save(self.root / 'environment.json', self.metadata)
        self.baseline = {'schemaVersion': 1, 'sourceEnvironmentSHA256': P.digest(self.root / 'environment.json'),
                         'containers': self.containers, 'volumes': self.volumes,
                         'network': {'id': network, 'name': project + '_default', 'driver': 'bridge',
                                     'labels': {'com.docker.compose.project': project}},
                         'mysqlStartup': {'configCmd': ['mysqld'], 'lastMeasuredBufferBytes': P.BASE_BUFFER,
                                          'lastMeasuredKind': 'historical-contract-reference-not-current-read',
                                          'startupBufferBytes': P.START_BUFFER, 'versionFamily': '8.4',
                                          'basis': 'mysql84-image-default', 'defaultContract': P.BUFFER_DOC},
                         'capturedMySQL': facts()}
        self.baseline_path = self.root / 'baseline-snapshot.json'
        P.save(self.baseline_path, self.baseline)
        self.plan_path = self.root / 'profile-plan.json'

    def plan(self):
        value = P.build_plan(self.private, self.baseline_path, self.root, 'abcdef012345')
        P.save(self.plan_path, value)
        return value


class Fake:
    def __init__(self, fixture, *, start_buffer=P.BASE_BUFFER, fault=None):
        self.fixture = fixture
        self.containers = copy.deepcopy(fixture.containers)
        self.before = facts(start_buffer)
        self.fault = fault
        self.calls = []
        self.updated = False
        self.resized = False
        self.polls = 0
        self.closed = False
        self.is_settled = True
        self.progress = [(0, self.before['resizeText']), (5, 'Resizing hash tables.'),
                         (0, 'Completed resizing buffer pool at 261103  4:15:00.')]

    def event(self, name):
        self.calls.append(name)
        if self.fault == name:
            raise RuntimeError('synthetic-secret-not-real')

    def local(self):
        self.event('local')

    def guard_idle(self, root):
        self.event('idle')

    def guards(self, containers, baseline):
        self.event('identities')

    def resources(self, containers):
        self.event('resources')
        return copy.deepcopy(self.fixture.volumes), copy.deepcopy(self.fixture.baseline['network'])

    def memory(self):
        self.event('memory')
        return {'availableBytes': 16 * P.GIB, 'totalBytes': 32 * P.GIB}

    def inspect(self, cid):
        self.event('inspect')
        return copy.deepcopy(next(item for item in self.containers.values() if item['id'] == cid))

    def facts(self, cid):
        self.event('facts')
        value = copy.deepcopy(self.before)
        if self.resized:
            index = min(self.polls, len(self.progress) - 1)
            code, text = self.progress[index]
            self.polls += 1
            value.update(bufferBytes=P.TARGET_BUFFER, resizeCode=code, resizeText=text, uptime=102)
        return value

    def update(self, mysql):
        self.event('update-before')
        current = self.containers['mysql']
        current.update(cpuNano=8_000_000_000, memoryBytes=8 * P.GIB,
                       memorySwap=P.swap_target(mysql['memoryBytes'], mysql['memorySwap']))
        self.updated = True
        self.event('update-after')

    def resize(self, cid):
        self.event('resize-before')
        self.resized = True
        self.event('resize-after')

    def pause(self):
        self.event('pause')

    def close(self):
        self.closed = True

    def settled(self):
        return self.is_settled


class Checks(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='cap10-profile-pure-')
        self.addCleanup(self.temp.cleanup)
        self.fixture = Fixture(Path(self.temp.name).resolve())
        patch = mock.patch.object(P, 'REPO', self.fixture.repo)
        patch.start()
        self.addCleanup(patch.stop)
        # Any accidental real execution or endpoint use fails every test.
        for target in ('subprocess.Popen', 'socket.socket'):
            patch = mock.patch(target, side_effect=AssertionError('real operation forbidden in pure checks'))
            patch.start()
            self.addCleanup(patch.stop)

    def execute(self, fake=None):
        return P.execute(self.fixture.plan_path, self.fixture.root, fake or Fake(self.fixture))

    def failed(self, fake):
        with self.assertRaises(RuntimeError):
            self.execute(fake)
        self.assertFalse((self.fixture.root / 'environment-abcdef012345.json').exists())
        self.assertFalse((self.fixture.runtime / 'cap10-active.private.json').exists())
        self.assertTrue(fake.closed)
        return P.read(self.fixture.root / 'resource-profile-abcdef012345' / 'failure.json')

    def test_default_plan_is_files_only(self):
        with mock.patch.object(P, 'Native', side_effect=AssertionError('no adapter in plan')):
            plan = self.fixture.plan()
            self.assertEqual(P.verify_plan(self.fixture.plan_path, self.fixture.root), plan)
        self.assertEqual(plan['lastMeasuredBufferBytes'], P.BASE_BUFFER)
        self.assertEqual(plan['knownStartupBufferBytes'], P.START_BUFFER)
        self.assertNotIn('synthetic-secret-not-real', self.fixture.plan_path.read_text())

    def test_complete_export_uses_actual_startup128_and_leaves_legacy_files(self):
        self.fixture.plan()
        old = {path: path.read_bytes() for path in (self.fixture.private, self.fixture.root / 'environment.json')}
        fake = Fake(self.fixture, start_buffer=P.START_BUFFER)
        result = self.execute(fake)
        self.assertEqual(result['actualBeforeBufferBytes'], P.START_BUFFER)
        self.assertEqual(fake.polls, 3)  # Requested target+old code0 did not pass.
        self.assertEqual(P.RESOURCE.bound_profile(P.read(result['targetPrivatePath']),
                         P.read(self.fixture.root / result['environmentPath'])), 'mysql8-v1')
        self.assertEqual({path: path.read_bytes() for path in old}, old)
        exported = P.read(self.fixture.root / result['environmentPath'])
        self.assertEqual(exported['mysqlActual']['bufferBytes'], P.TARGET_BUFFER)
        self.assertNotIn('synthetic-secret-not-real', json.dumps(exported))
        self.assertEqual(P.read(result['targetPrivatePath'])['MYSQL_PASSWORD'], 'synthetic-secret-not-real')
        self.assertEqual(P.read(result['targetPrivatePath'])['GOMAXPROCS'], '4')
        registrations = list(self.fixture.runtime.glob('owned-env-*.json'))
        self.assertEqual(len(registrations), 1)
        self.assertRegex(registrations[0].name, r'^owned-env-[a-f0-9]{10}\.json$')
        self.assertEqual(P.read(registrations[0]), {'path': result['targetPrivatePath']})
        self.assertFalse((self.fixture.runtime / 'cap10-active.private.json').exists())
        self.assertFalse(list(self.fixture.root.glob('idle-*')))
        self.assertFalse(list(self.fixture.root.glob('slot-*')))
        self.assertFalse(list(self.fixture.root.glob('*-command.json')))
        self.assertTrue(fake.closed)
        journal = self.fixture.root / 'resource-profile-abcdef012345'
        request = P.read(journal / 'resize-request.json')
        dispatched = P.read(journal / 'resize-dispatched.json')
        verified = P.read(journal / 'verified.json')
        self.assertEqual(request['beforeSet']['bufferBytes'], P.START_BUFFER)
        self.assertEqual(dispatched['requestSHA256'], P.digest(journal / 'resize-request.json'))
        self.assertEqual(verified['resizeRequestSHA256'], dispatched['requestSHA256'])
        self.assertNotEqual(verified['beforeSet']['resizeText'], verified['actualAfter']['resizeText'])

    def test_actual_last_measured512_is_distinct(self):
        self.fixture.plan()
        self.assertEqual(self.execute()['actualBeforeBufferBytes'], P.BASE_BUFFER)

    def test_swap_policies_are_preserved(self):
        for swap, expected in ((2 * P.GIB, 8 * P.GIB), (4 * P.GIB, 16 * P.GIB), (-1, -1)):
            with self.subTest(swap=swap):
                self.assertEqual(P.swap_target(2 * P.GIB, swap), expected)
        for swap in (0, P.GIB, 3 * P.GIB, 8 * P.GIB, True):
            with self.subTest(refused=swap), self.assertRaises(ValueError):
                P.swap_target(2 * P.GIB, swap)

    def test_explicit_startup_command_and_default_contract(self):
        baseline = copy.deepcopy(self.fixture.baseline)
        baseline['containers']['mysql']['cmd'] = ['mysqld', '--innodb-buffer-pool-size=536870912']
        baseline['mysqlStartup'].update(configCmd=baseline['containers']['mysql']['cmd'],
            basis='explicit-cmd', startupBufferBytes=P.BASE_BUFFER)
        self.assertEqual(P.startup_buffer(baseline), P.BASE_BUFFER)
        baseline['mysqlStartup']['startupBufferBytes'] = P.START_BUFFER
        with self.assertRaises(ValueError):
            P.startup_buffer(baseline)

    def test_unknown_actual_cache_refuses_before_update(self):
        self.fixture.plan()
        fake = Fake(self.fixture, start_buffer=P.GIB)
        failure = self.failed(fake)
        self.assertFalse(fake.updated)
        self.assertIn('unknown or partially applied', failure['reason'])

    def test_already_target_caps_are_not_adopted(self):
        self.fixture.plan()
        fake = Fake(self.fixture)
        fake.containers['mysql']['memoryBytes'] = 8 * P.GIB
        self.failed(fake)
        self.assertFalse(fake.updated)

    def test_complete_identity_rejects_mount_image_network_and_policy_drift(self):
        original = self.fixture.containers['mysql']
        for key, value in (('imageId', 'sha256:' + '0' * 64), ('ports', {}), ('networks', {}),
                           ('mounts', original['mounts'][:-1]), ('policy', {})):
            with self.subTest(key=key), self.assertRaises(ValueError):
                P.same_identity(original, {**original, key: value})

    def test_other_service_drift_after_update_retains_actual_partial_state(self):
        self.fixture.plan()
        fake = Fake(self.fixture)
        original_update = fake.update
        def update(mysql):
            original_update(mysql)
            fake.containers['redis']['memoryBytes'] += 1
        fake.update = update
        failure = self.failed(fake)
        self.assertTrue(fake.updated)
        self.assertFalse(fake.resized)
        self.assertEqual(failure['phase'], 'docker-update')
        self.assertEqual(failure['containersObservedAfterFailure']['mysql']['memoryBytes'], 8 * P.GIB)

    def test_foreign_owner_is_not_removed(self):
        self.fixture.plan()
        lock = self.fixture.runtime / 'cap10-active.private.json'
        foreign = {'kind': 'load', 'pid': 123, 'created': 456}
        P.save(lock, foreign, private=True)
        fake = Fake(self.fixture)
        with self.assertRaises(RuntimeError):
            self.execute(fake)
        self.assertEqual(P.read(lock), foreign)
        self.assertEqual(fake.calls, [])

    def test_changed_owner_is_not_removed(self):
        self.fixture.plan()
        fake = Fake(self.fixture)
        foreign = {'kind': 'load', 'pid': 123, 'created': 456}
        def guard(root):
            (self.fixture.runtime / 'cap10-active.private.json').write_text(json.dumps(foreign))
            raise ValueError('changed')
        fake.guard_idle = guard
        with self.assertRaises(RuntimeError):
            self.execute(fake)
        self.assertEqual(P.read(self.fixture.runtime / 'cap10-active.private.json'), foreign)

    def test_live_child_prevents_owner_release(self):
        self.fixture.plan()
        fake = Fake(self.fixture, fault='local')
        fake.is_settled = False
        with self.assertRaises(RuntimeError):
            self.execute(fake)
        self.assertTrue((self.fixture.runtime / 'cap10-active.private.json').exists())

    def test_journal_mkdir_failure_releases_complete_owner_and_keeps_failure(self):
        self.fixture.plan()
        original = Path.mkdir
        def mkdir(path, *args, **kwargs):
            if path.name == 'resource-profile-abcdef012345':
                raise OSError('synthetic setup failure')
            return original(path, *args, **kwargs)
        fake = Fake(self.fixture)
        with mock.patch.object(Path, 'mkdir', mkdir), self.assertRaises(RuntimeError):
            self.execute(fake)
        self.assertFalse((self.fixture.runtime / 'cap10-active.private.json').exists())
        self.assertTrue((self.fixture.runtime / 'profile-failure-abcdef012345.private.json').exists())
        self.assertEqual(fake.calls, [])

    def test_setup_snapshot_write_failure_never_updates(self):
        self.fixture.plan()
        original = P.save
        def save(path, *args, **kwargs):
            if Path(path).name == 'before.json':
                raise OSError('synthetic setup failure')
            return original(path, *args, **kwargs)
        fake = Fake(self.fixture)
        with mock.patch.object(P, 'save', save):
            failure = self.failed(fake)
        self.assertFalse(fake.updated)
        self.assertEqual(failure['phase'], 'claim')

    def test_source_hash_drift_rejects_before_owner_or_native(self):
        self.fixture.plan()
        fake = Fake(self.fixture)
        with mock.patch.object(P, 'source_hashes', return_value={}), self.assertRaises(ValueError):
            self.execute(fake)
        self.assertEqual(fake.calls, [])
        self.assertFalse((self.fixture.runtime / 'cap10-active.private.json').exists())

    def test_private_source_drift_rejects_without_update(self):
        self.fixture.plan()
        self.fixture.private.write_text(json.dumps({**self.fixture.configuration, 'GOMAXPROCS': '9'}))
        fake = Fake(self.fixture)
        with self.assertRaises(ValueError):
            self.execute(fake)
        self.assertFalse(fake.updated)

    def test_memory_low_before_mutation(self):
        self.fixture.plan()
        fake = Fake(self.fixture)
        fake.memory = lambda: {'availableBytes': 9 * P.GIB, 'totalBytes': 32 * P.GIB}
        self.failed(fake)
        self.assertFalse(fake.updated)

    def test_memory_low_after_update_does_not_resize_or_rollback(self):
        self.fixture.plan()
        fake = Fake(self.fixture)
        fake.memory = lambda: {'availableBytes': (3 if fake.updated else 16) * P.GIB, 'totalBytes': 32 * P.GIB}
        self.failed(fake)
        self.assertTrue(fake.updated)
        self.assertFalse(fake.resized)
        self.assertEqual(fake.calls.count('update-before'), 1)

    def test_active_session_or_durability_drift_refuses(self):
        self.fixture.plan()
        fake = Fake(self.fixture)
        fake.before['otherSessions'] = 1
        self.failed(fake)
        self.assertFalse(fake.updated)
        changed = facts()
        changed['syncBinlog'] = 0
        with self.assertRaises(ValueError):
            P.validate_facts(changed, {P.BASE_BUFFER})

    def test_fresh_completed_text_not_requested_size_or_old_code0(self):
        before = facts()
        target = {**before, 'bufferBytes': P.TARGET_BUFFER}
        self.assertFalse(P.resize_complete(before, target))
        target['resizeText'] = 'Completed resizing buffer pool at 261103  4:15:00.'
        self.assertTrue(P.resize_complete(before, target))
        for key, value in (('resizeCode', 7), ('bufferBytes', P.BASE_BUFFER), ('resizeText', ''), ('uptime', 0)):
            with self.subTest(key=key):
                self.assertFalse(P.resize_complete(before, {**target, key: value}))

    def test_stale_completion_text_times_out_without_public_metadata(self):
        self.fixture.plan()
        fake = Fake(self.fixture)
        fake.progress = [(0, fake.before['resizeText'])]
        with mock.patch.object(P.time, 'monotonic', side_effect=[0, 181]):
            failure = self.failed(fake)
        self.assertEqual(failure['phase'], 'buffer-resize')
        self.assertEqual(fake.polls, 1)
        self.assertTrue(fake.updated and fake.resized)

    def test_server_resize_failure_retained_without_rollback(self):
        self.fixture.plan()
        fake = Fake(self.fixture)
        fake.progress = [(7, 'Resizing buffer pool failed, finished resizing at 261103  4:15:00.')]
        failure = self.failed(fake)
        self.assertIn('resize failure', failure['reason'])
        self.assertEqual(fake.calls.count('update-before'), 1)

    def test_export_write_failure_keeps_private_registration_but_no_public_metadata(self):
        self.fixture.plan()
        original = P.save
        def save(path, *args, **kwargs):
            if Path(path).name == 'environment-abcdef012345.json':
                raise OSError('synthetic export failure')
            return original(path, *args, **kwargs)
        fake = Fake(self.fixture)
        with mock.patch.object(P, 'save', save):
            failure = self.failed(fake)
        self.assertEqual(failure['phase'], 'export')
        self.assertTrue((self.fixture.runtime / 'env-mysql8-abcdef012345.private.json').exists())
        public_files = list((self.fixture.root / 'resource-profile-abcdef012345').glob('*.json'))
        self.assertNotIn('synthetic-secret-not-real', ''.join(path.read_text() for path in public_files))

    def test_intermediate_native_failure_preserves_first_outcome(self):
        self.fixture.plan()
        fake = Fake(self.fixture, fault='update-after')
        failure = self.failed(fake)
        self.assertTrue(fake.updated)
        self.assertFalse(fake.resized)
        self.assertEqual(failure['phase'], 'docker-update')
        self.assertNotIn('synthetic-secret-not-real', json.dumps(failure))
        with self.assertRaises(ValueError):
            self.execute(Fake(self.fixture))

    def test_unknown_or_shared_resources_refused_before_update(self):
        self.fixture.plan()
        fake = Fake(self.fixture, fault='identities')
        self.failed(fake)
        self.assertFalse(fake.updated)

    def native_resource_adapter(self, fault=None):
        adapter = P.Native()
        recorded = []
        def command(argv, **kwargs):
            recorded.append(argv)
            if argv[:3] == ['docker', 'ps', '-aq']:
                selector = argv[-1]
                if selector.startswith('label='):
                    ids = [item['id'] for item in self.fixture.containers.values()]
                    if fault == 'extra-service':
                        ids.append('9' * 64)
                    return '\n'.join(ids)
                name = selector.removeprefix('volume=')
                ids = [item['id'] for item in self.fixture.containers.values()
                       if any(mount.get('Name') == name for mount in item['mounts'])]
                if fault == 'shared-volume':
                    ids.append('9' * 64)
                return '\n'.join(ids)
            if argv[:3] == ['docker', 'volume', 'inspect']:
                value = copy.deepcopy(self.fixture.volumes[argv[-1]])
                if fault == 'volume-owner':
                    value['labels'] = {'com.docker.compose.project': 'foreign'}
                return json.dumps(value)
            if argv[:3] == ['docker', 'network', 'inspect']:
                value = copy.deepcopy(self.fixture.baseline['network'])
                if fault == 'network-owner':
                    value['labels'] = {}
                return json.dumps(value)
            raise AssertionError('unexpected command in native resource test')
        adapter.command = command
        return adapter, recorded

    def test_actual_native_guards_preserve_registered_anonymous_kafka_volumes(self):
        adapter, recorded = self.native_resource_adapter()
        adapter.guards(self.fixture.containers, self.fixture.baseline)
        self.assertEqual(len([argv for argv in recorded if argv[:3] == ['docker', 'volume', 'inspect']]), 6)
        self.assertFalse(any(argv[1] in ('exec', 'update', 'start', 'create', 'rm') for argv in recorded))

    def test_actual_native_guards_refuse_unknown_shared_owner_drift(self):
        for fault in ('extra-service', 'shared-volume', 'volume-owner', 'network-owner'):
            with self.subTest(fault=fault):
                adapter, _ = self.native_resource_adapter(fault)
                with self.assertRaises(ValueError):
                    adapter.guards(self.fixture.containers, self.fixture.baseline)

    def test_actual_native_local_refuses_tcp_and_wrong_context_endpoint(self):
        adapter = P.Native()
        adapter.command = mock.Mock(side_effect=['default\n', 'tcp://127.0.0.1:2375\n'])
        with mock.patch.object(P.sys, 'platform', 'linux'), mock.patch.object(P.OWNED, 'PLATFORM', 'linux'):
            with mock.patch.dict(os.environ, {'DOCKER_HOST': 'tcp://remote:2375'}), self.assertRaises(ValueError):
                adapter.local()
            self.assertFalse(adapter.command.called)
            with mock.patch.dict(os.environ, {'DOCKER_HOST': 'unix:///var/run/docker.sock'}), self.assertRaises(ValueError):
                adapter.local()

    def test_actual_native_update_args_preserve_swap_policy_without_other_mutations(self):
        adapter = P.Native()
        adapter.command = mock.Mock(return_value='')
        mysql = copy.deepcopy(self.fixture.containers['mysql'])
        mysql['memorySwap'] = mysql['memoryBytes']
        adapter.update(mysql)
        self.assertEqual(adapter.command.call_args.args[0], ['docker', 'update', '--cpus', '8', '--memory',
                         str(8 * P.GIB), '--memory-swap', str(8 * P.GIB), mysql['id']])

    def test_actual_native_sql_and_facts_only_use_literal_container_password_expansion(self):
        adapter = P.Native()
        adapter.command = mock.Mock(return_value=json.dumps(facts()))
        cid = self.fixture.containers['mysql']['id']
        self.assertEqual(adapter.facts(cid)['bufferBytes'], P.BASE_BUFFER)
        argv = adapter.command.call_args.args[0]
        query = adapter.command.call_args.kwargs['input']
        self.assertEqual(argv[:4], ['docker', 'exec', '-i', cid])
        self.assertIn('$MYSQL_ROOT_PASSWORD', argv[-1])
        self.assertIn('Innodb_buffer_pool_resize_status', query)
        self.assertIn('innodb_trx', query)
        self.assertNotIn('synthetic-secret-not-real', json.dumps([argv, query]))

    def test_actual_native_inspect_selects_no_env_or_health_log(self):
        adapter = P.Native()
        actual = copy.deepcopy(self.fixture.containers['mysql'])
        actual['networks'] = {name: {'NetworkID': identity} for name, identity in actual['networks'].items()}
        adapter.command = mock.Mock(return_value=json.dumps(actual))
        selected = adapter.inspect(actual['id'])
        template = adapter.command.call_args.args[0][-2]
        self.assertNotIn('Config.Env', template)
        self.assertNotIn('Health.Log', template)
        self.assertIn('Config.Cmd', template)
        self.assertEqual(selected, self.fixture.containers['mysql'])

    def test_actual_native_api_stop_health_results_and_pid_reuse(self):
        adapter = P.Native()
        connection = mock.MagicMock()
        connection.__enter__.return_value.connect_ex.return_value = 0
        with mock.patch.object(P.socket, 'socket', return_value=connection), self.assertRaises(ValueError):
            adapter.guard_idle(self.fixture.root)
        P.save(self.fixture.root / 'api-old.json', {'cohort': 'cap10', 'pid': 321, 'creationUnixMs': 1000})
        P.save(self.fixture.root / 'api-old-stop.json', {'at': P.now(), 'pid': 321, 'identityMatched': True,
                                                       'exitCode': 0, 'goGracefulShutdownClaimed': False})
        P.save(self.fixture.root / 'api-old-health.json', {'at': P.now(), 'ready': True, 'readyBody': {},
                                                         'capacityBody': {'pid': 321}, 'scope': 'producer health'})
        connection.__enter__.return_value.connect_ex.return_value = 1
        process = mock.Mock()
        process.create_time.return_value = 2
        with mock.patch.object(P.socket, 'socket', return_value=connection), \
                mock.patch.object(P.psutil, 'Process', return_value=process):
            adapter.guard_idle(self.fixture.root)
        self.assertFalse(process.terminate.called)
        # A historical stop result cannot excuse the original process still
        # being alive with the same birth time.
        process.create_time.return_value = 1
        with mock.patch.object(P.socket, 'socket', return_value=connection), \
                mock.patch.object(P.psutil, 'Process', return_value=process), self.assertRaises(ValueError):
            adapter.guard_idle(self.fixture.root)

    def test_null_api_stop_is_only_a_bound_result_not_absence_proof(self):
        root = self.fixture.root
        # Write the stop first so this also exercises that artifact before its
        # companion's ordinary owner check in directory iteration order.
        P.save(root / 'api-old-stop.json', {'at': P.now(), 'pid': 321, 'identityMatched': True,
                                          'exitCode': None, 'goGracefulShutdownClaimed': False})
        P.save(root / 'api-old.json', {'cohort': 'cap10', 'pid': 321, 'creationUnixMs': 1000})
        connection = mock.MagicMock()
        connection.__enter__.return_value.connect_ex.return_value = 1
        with mock.patch.object(P.socket, 'socket', return_value=connection), \
                mock.patch.object(P.psutil, 'Process', side_effect=P.psutil.NoSuchProcess(321)) as inspect:
            P.Native().guard_idle(root)
            inspect.assert_called_once_with(321)
        process = mock.Mock()
        process.create_time.return_value = 2
        with mock.patch.object(P.socket, 'socket', return_value=connection), \
                mock.patch.object(P.psutil, 'Process', return_value=process):
            P.Native().guard_idle(root)  # A different birth is an unrelated reused PID.
        process.create_time.return_value = 1
        with mock.patch.object(P.socket, 'socket', return_value=connection), \
                mock.patch.object(P.psutil, 'Process', return_value=process), self.assertRaises(ValueError):
            P.Native().guard_idle(root)
        self.assertFalse(process.terminate.called)

    def test_null_api_stop_requires_exact_companion_identity(self):
        root = self.fixture.root
        stop = {'pid': 321, 'identityMatched': True, 'exitCode': None}
        original = {'cohort': 'cap10', 'pid': 321, 'creationUnixMs': 1000}
        connection = mock.MagicMock()
        connection.__enter__.return_value.connect_ex.return_value = 1
        cases = [None, {**original, 'cohort': 'other'}, {**original, 'pid': 322},
                 {**original, 'pid': True}, {'cohort': 'cap10', 'pid': 321},
                 *({**original, 'creationUnixMs': birth} for birth in (True, 0, -1, float('inf'), float('nan')))]
        for number, companion in enumerate(cases):
            with self.subTest(case=number):
                path = root / ('api-null' + str(number))
                P.save(path.with_name(path.name + '-stop.json'), stop)
                if companion is not None:
                    P.save(path.with_suffix('.json'), companion)
                with mock.patch.object(P.socket, 'socket', return_value=connection), self.assertRaises(ValueError):
                    P.Native().guard_idle(root)
                path.with_name(path.name + '-stop.json').unlink()
                if companion is not None:
                    path.with_suffix('.json').unlink()

    def test_integer_api_stop_result_compatibility_and_missing_exit_rejection(self):
        path = self.fixture.root / 'api-old-stop.json'
        connection = mock.MagicMock()
        connection.__enter__.return_value.connect_ex.return_value = 1
        P.save(path, {'pid': 321, 'identityMatched': True, 'exitCode': 0})
        with mock.patch.object(P.socket, 'socket', return_value=connection), \
                mock.patch.object(P.psutil, 'Process', side_effect=AssertionError('result is not an owner')):
            P.Native().guard_idle(self.fixture.root)
        P.save(path.with_name('api-missing-stop.json'), {'pid': 321, 'identityMatched': True})
        with mock.patch.object(P.socket, 'socket', return_value=connection), self.assertRaises(ValueError):
            P.Native().guard_idle(self.fixture.root)

    def test_real_cli_default_branch_never_constructs_native(self):
        output = self.fixture.root / 'cli-plan.json'
        arguments = ['cap10-profile.py', '--environment', str(self.fixture.private),
                     '--baseline-snapshot', str(self.fixture.baseline_path), '--output', str(output)]
        with mock.patch.object(P.sys, 'argv', arguments), mock.patch.object(P.OWNED, 'ROOT', self.fixture.root), \
                mock.patch.object(P, 'Native', side_effect=AssertionError('plan constructed Native')), \
                mock.patch('builtins.print'):
            P.main()
        self.assertTrue(output.exists())
        self.assertFalse((self.fixture.runtime / 'cap10-active.private.json').exists())

    def test_incomplete_owner_preparation_never_creates_active_lock(self):
        self.fixture.plan()
        original = P.save
        def save(path, *args, **kwargs):
            if Path(path).name.startswith('profile-owner-'):
                Path(path).write_text('{partial')
                raise OSError('synthetic owner write failure')
            return original(path, *args, **kwargs)
        fake = Fake(self.fixture)
        with mock.patch.object(P, 'save', save), self.assertRaises(RuntimeError):
            self.execute(fake)
        self.assertFalse((self.fixture.runtime / 'cap10-active.private.json').exists())
        self.assertEqual(fake.calls, [])

    def test_capture_factory_records_actual128_separate_from_historical512(self):
        output = self.fixture.root / 'baseline-abcdef012345.json'
        fake = Fake(self.fixture, start_buffer=P.START_BUFFER)
        result = P.capture_baseline(self.fixture.private, output, self.fixture.root, fake)
        self.assertEqual(result['actualBufferBytes'], P.START_BUFFER)
        self.assertFalse(result['lastMeasuredIsCurrentRead'])
        snapshot = P.read(output)
        self.assertEqual(snapshot['mysqlStartup']['lastMeasuredBufferBytes'], P.BASE_BUFFER)
        self.assertEqual(snapshot['capturedMySQL']['bufferBytes'], P.START_BUFFER)
        self.assertEqual(snapshot['mysqlStartup']['configCmd'], ['mysqld'])
        self.assertFalse(fake.updated or fake.resized)
        self.assertFalse((self.fixture.runtime / 'cap10-active.private.json').exists())
        self.assertNotIn('synthetic-secret-not-real', output.read_text())
        plan = P.build_plan(self.fixture.private, output, self.fixture.root, '123456abcdef')
        self.assertEqual(plan['baselineSnapshotSHA256'], P.digest(output))
        self.assertEqual(plan['knownStartupBufferBytes'], P.START_BUFFER)
        self.assertFalse(list(self.fixture.root.glob('environment-*.json')))

    def test_capture_cli_is_explicit_read_only_and_never_calls_update(self):
        output = self.fixture.root / 'baseline-abcdef012345.json'
        fake = Fake(self.fixture)
        arguments = ['cap10-profile.py', '--capture-baseline', '--environment', str(self.fixture.private),
                     '--output', str(output)]
        with mock.patch.object(P.sys, 'argv', arguments), mock.patch.object(P.OWNED, 'ROOT', self.fixture.root), \
                mock.patch.object(P.OWNED, 'settings', return_value=(self.fixture.configuration, self.fixture.runtime)), \
                mock.patch.object(P, 'Native', return_value=fake), mock.patch('builtins.print'):
            P.main()
        self.assertTrue(output.exists())
        self.assertFalse(fake.updated or fake.resized)

    def test_capture_already_changed_caps_refuses_without_sql_or_mutation(self):
        fake = Fake(self.fixture)
        fake.containers['mysql']['memoryBytes'] = 8 * P.GIB
        with self.assertRaises(RuntimeError):
            P.capture_baseline(self.fixture.private, self.fixture.root / 'baseline-abcdef012345.json',
                               self.fixture.root, fake)
        self.assertFalse(fake.updated or fake.resized)
        self.assertNotIn('facts', fake.calls)
        self.assertFalse((self.fixture.runtime / 'cap10-active.private.json').exists())

    def test_capture_unknown_cache_preserves_first_failure(self):
        fake = Fake(self.fixture, start_buffer=P.GIB)
        output = self.fixture.root / 'baseline-abcdef012345.json'
        with self.assertRaises(RuntimeError):
            P.capture_baseline(self.fixture.private, output, self.fixture.root, fake)
        self.assertFalse(output.exists())
        failure = P.read(self.fixture.runtime / 'profile-capture-failure-abcdef012345.private.json')
        self.assertEqual(failure['mysqlObserved']['bufferBytes'], P.GIB)
        self.assertFalse(failure['resourcesChanged'])
        self.assertFalse((self.fixture.runtime / 'cap10-active.private.json').exists())

    def test_capture_foreign_owner_preserved(self):
        foreign = {'kind': 'load', 'pid': 123, 'created': 456}
        lock = self.fixture.runtime / 'cap10-active.private.json'
        P.save(lock, foreign, private=True)
        fake = Fake(self.fixture)
        with self.assertRaises(RuntimeError):
            P.capture_baseline(self.fixture.private, self.fixture.root / 'baseline-abcdef012345.json',
                               self.fixture.root, fake)
        self.assertEqual(P.read(lock), foreign)
        self.assertEqual(fake.calls, [])

    def test_capture_output_collision_never_constructs_adapter(self):
        output = self.fixture.root / 'baseline-abcdef012345.json'
        P.save(output, {'original': True})
        with mock.patch.object(P, 'Native', side_effect=AssertionError('collision reached native')), \
                self.assertRaises(ValueError):
            P.capture_baseline(self.fixture.private, output, self.fixture.root)
        self.assertEqual(P.read(output), {'original': True})

    def test_capture_output_write_failure_releases_own_owner(self):
        original = P.save
        output = self.fixture.root / 'baseline-abcdef012345.json'
        def save(path, *args, **kwargs):
            if Path(path) == output:
                raise OSError('synthetic output write failure')
            return original(path, *args, **kwargs)
        fake = Fake(self.fixture)
        with mock.patch.object(P, 'save', save), self.assertRaises(RuntimeError):
            P.capture_baseline(self.fixture.private, output, self.fixture.root, fake)
        self.assertFalse(fake.updated or fake.resized)
        self.assertTrue(fake.closed)
        self.assertFalse((self.fixture.runtime / 'cap10-active.private.json').exists())
        self.assertTrue((self.fixture.runtime / 'profile-capture-failure-abcdef012345.private.json').exists())

    def test_unknown_api_artifact_schema_fails_closed(self):
        P.save(self.fixture.root / 'api-unknown.json', {'pid': 321})
        connection = mock.MagicMock()
        connection.__enter__.return_value.connect_ex.return_value = 1
        with mock.patch.object(P.socket, 'socket', return_value=connection), self.assertRaises(ValueError):
            P.Native().guard_idle(self.fixture.root)

    def test_resize_request_write_failure_never_dispatches_set(self):
        self.fixture.plan()
        original = P.save
        def save(path, *args, **kwargs):
            if Path(path).name == 'resize-request.json':
                raise OSError('synthetic request evidence failure')
            return original(path, *args, **kwargs)
        fake = Fake(self.fixture)
        with mock.patch.object(P, 'save', save):
            failure = self.failed(fake)
        self.assertTrue(fake.updated)
        self.assertFalse(fake.resized)
        self.assertEqual(failure['phase'], 'buffer-resize')

    def test_capture_source_drift_never_publishes_snapshot(self):
        fake = Fake(self.fixture)
        output = self.fixture.root / 'baseline-abcdef012345.json'
        original_hashes = P.source_hashes()
        with mock.patch.object(P, 'source_hashes', side_effect=[original_hashes, {}]), self.assertRaises(RuntimeError):
            P.capture_baseline(self.fixture.private, output, self.fixture.root, fake)
        self.assertFalse(output.exists())
        self.assertFalse((self.fixture.runtime / 'cap10-active.private.json').exists())
        self.assertFalse(fake.updated or fake.resized)

    def test_nonfinite_api_birth_is_not_treated_as_pid_reuse(self):
        P.save(self.fixture.root / 'api-corrupt.json', {'cohort': 'cap10', 'pid': 321, 'creationUnixMs': float('inf')})
        connection = mock.MagicMock()
        connection.__enter__.return_value.connect_ex.return_value = 1
        with mock.patch.object(P.socket, 'socket', return_value=connection), self.assertRaises(ValueError):
            P.Native().guard_idle(self.fixture.root)

    def test_private_export_interruption_keeps_original_registration_schema_first(self):
        self.fixture.plan()
        original = P.save
        observed = []
        target = self.fixture.runtime / 'env-mysql8-abcdef012345.private.json'
        def save(path, *args, **kwargs):
            if Path(path) == target:
                registrations = list(self.fixture.runtime.glob('owned-env-*.json'))
                self.assertEqual(len(registrations), 1)
                self.assertRegex(registrations[0].name, r'^owned-env-[a-f0-9]{10}\.json$')
                self.assertEqual(P.read(registrations[0]), {'path': str(target)})
                observed.append(True)
                raise OSError('synthetic private export interruption')
            return original(path, *args, **kwargs)
        fake = Fake(self.fixture)
        with mock.patch.object(P, 'save', save):
            self.failed(fake)
        self.assertEqual(observed, [True])
        self.assertFalse(target.exists())
        self.assertNotIn('synthetic-secret-not-real', ''.join(path.read_text() for path in
                         (self.fixture.root / 'resource-profile-abcdef012345').glob('*.json')))


if __name__ == '__main__':
    unittest.main(verbosity=2)
