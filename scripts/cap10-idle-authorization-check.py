"""Pure local checks of separately authorized CAP10 idle retries."""
import datetime as dt
from contextlib import redirect_stdout
import hashlib
import importlib.util
import json
import io
from pathlib import Path
import tempfile
from types import SimpleNamespace
from unittest.mock import patch


REPO = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location('cap10_idle_authorization', REPO / 'scripts/cap10-idle.py')
idle = importlib.util.module_from_spec(spec)
spec.loader.exec_module(idle)
TRANSITION = 'a' * 64


def write(path, value):
    path.write_text(json.dumps(value), encoding='utf-8')


def versioned_preflight_fixture(root, identity):
    """Only complete synthetic files for pure consumer checks; no observation."""
    directory = idle.IDLE.idle_directory(root / identity['environmentPath'], root, identity['resourceProfile'])
    directory.mkdir(exist_ok=True)
    reservation = {**identity, 'idleSchemaVersion': 1, 'idleDirectory': directory.name,
                   'attempt': 1, 'seconds': 60, 'intervalSeconds': 5}
    reservation_path = directory / 'idle-reservation.json'; write(reservation_path, reservation)
    result = {**reservation, 'passed': True, 'noWorkloadStarted': True, 'errors': [],
              'cadenceValid': True, 'uptimeValid': True, 'downstreamIdleAndComplete': True,
              'threeConsecutiveBusy': False, 'observerExit': 0, 'hostCPUMedian': 20,
              'hostMemoryMaxPercent': 60, 'downstreamSamples': 12, 'powerEvents': {'valid': True},
              'samples': [{'elapsedSeconds': i * 5, 'cpuIntervalValid': i > 0, 'cpuIntervalSeconds': 5} for i in range(13)],
              'clockIntervals': [{'valid': True} for _ in range(12)],
              'reservationPath': reservation_path.relative_to(root).as_posix(),
              'reservationSHA256': hashlib.sha256(reservation_path.read_bytes()).hexdigest()}
    result_path = directory / 'idle-result.json'; write(result_path, result)
    reference = {**identity, 'preflightPath': result_path.relative_to(root).as_posix(),
                 'preflightSHA256': hashlib.sha256(result_path.read_bytes()).hexdigest()}
    return result, idle.IDLE.validate_preflight(reference, root)


def early_setup_failure_checks(base):
    """Drive actual main through three faults before its observer can start."""
    checked = 0
    original_mkdir = Path.mkdir
    for fault in ('reservation', 'container', 'control', 'identity_changed'):
        root = base / ('main-fault-' + fault); root.mkdir()
        runtime = root / 'private'; runtime.mkdir()
        metadata_path = root / 'environment-012345abcdef.json'
        limits = idle.RESOURCE.container_limits('mysql8-v1')
        containers = {service: {'id': format(index + 1, '064x'), 'cpuNano': cpu, 'memoryBytes': memory,
                                'volumes': [{'name': 'synthetic-' + service}]} for index, (service, (cpu, memory)) in enumerate(limits.items())}
        metadata = {'resourceProfile': 'mysql8-v1', 'containers': containers}; write(metadata_path, metadata)
        original_idle = root / 'idle-result.json'; write(original_idle, {'passed': False, 'retained': True})
        original_slot = root / 'slot-S19.json'; write(original_slot, {'environmentSHA256': 'a' * 64})
        originals = {path: path.read_bytes() for path in (original_idle, original_slot)}
        configuration = {'CAP_SOURCE_SHA': 'a' * 40, 'CAP_READ_EXPECTED_REVISION': 'capacity-' + 'a' * 40,
                         'CAP_LIKE_FIXTURE': str(root / 'unused-fixture.json')}
        api_metadata = {'environmentPath': metadata_path.name, 'environmentSHA256': hashlib.sha256(metadata_path.read_bytes()).hexdigest(),
                        'resourceProfile': 'mysql8-v1', 'privateEnvironmentFile': str(root / 'unused-private.json'),
                        'sourceSha': configuration['CAP_SOURCE_SHA'], 'expectedVersion': configuration['CAP_READ_EXPECTED_REVISION']}
        api_path = root / 'api.json'; write(api_path, api_metadata)
        def save(path, value):
            if fault == 'reservation' and path.name == 'idle-reservation.json':
                raise OSError('synthetic reservation failure')
            with path.open('x', encoding='utf-8') as stream:
                json.dump(value, stream)
        def container(service, cid, running, **kwargs):
            if fault == 'container':
                raise ValueError('synthetic container verification failure')
            return containers[service]
        def mkdir(path, *args, **kwargs):
            if fault in ('control', 'identity_changed') and path.name == 'idle-control':
                if fault == 'identity_changed':
                    lock = runtime / 'cap10-active.private.json'
                    changed = json.loads(lock.read_bytes()); changed['created'] += 1
                    write(lock, changed)
                raise OSError('synthetic control directory failure')
            return original_mkdir(path, *args, **kwargs)
        owned = SimpleNamespace(ROOT=root, PLATFORM='linux', FLAGS=0, settings=lambda p: (configuration, runtime),
            read=lambda p: json.loads(Path(p).read_bytes()), environment=lambda value: (metadata, metadata_path, 'mysql8-v1'),
            api_identity=lambda *a, **kw: SimpleNamespace(pid=12345), transition=lambda: TRANSITION,
            now=lambda: '2026-11-02T17:50:00Z', save=save, container=container)
        with patch.object(idle, 'module', side_effect=lambda name, filename: owned if filename == 'cap10-env.py' else SimpleNamespace()), \
                patch('sys.argv', ['cap10-idle.py', '--environment', str(root / 'unused-private.json'), '--api-metadata', str(api_path),
                                   '--observer', str(root / 'never-started-observer')]), \
                patch.object(Path, 'mkdir', mkdir), patch.object(idle.subprocess, 'Popen') as popen, redirect_stdout(io.StringIO()):
            try:
                idle.main()
            except SystemExit as error:
                assert error.code == 1
            else:
                raise AssertionError('early setup failure must return failure')
            popen.assert_not_called()
        evidence = root / 'idle-012345abcdef'
        result = owned.read(evidence / 'idle-result.json')
        assert result['passed'] is False and result['samples'] == [] and result['noWorkloadStarted'] is True and result['errors']
        assert evidence.is_dir()
        assert (runtime / 'cap10-active.private.json').exists() is (fault == 'identity_changed')
        assert not (evidence / 'idle-observer.txt').exists()
        assert all(path.read_bytes() == raw for path, raw in originals.items())
        checked += 1
    return checked


def main():
    checks = 0
    with tempfile.TemporaryDirectory(prefix='.qa-cap10-idle-', dir=REPO.parent) as folder:
        base = Path(folder).resolve()
        assert base.parent == REPO.parent and base.name.startswith('.qa-cap10-idle-')
        metadata_root = base / 'metadata'; metadata_root.mkdir()
        original = metadata_root / 'environment.json'; write(original, {})
        target = metadata_root / 'environment-012345abcdef.json'; write(target, {'resourceProfile': 'mysql8-v1'})
        def environment(value):
            path = idle.RESOURCE.metadata_path(value, metadata_root)
            public = json.loads(path.read_bytes())
            return public, path, idle.RESOURCE.bound_profile(value, public)
        owned = SimpleNamespace(environment=environment)
        _, path, profile, digest = idle.environment_identity({}, {}, {}, owned, metadata_root)
        assert path == original and profile == 'baseline' and digest == hashlib.sha256(original.read_bytes()).hexdigest()
        checks += 1
        config = {'CAP10_ENVIRONMENT_METADATA': str(target), 'CAP10_RESOURCE_PROFILE': 'mysql8-v1'}
        identity = {'environmentPath': target.name, 'resourceProfile': 'mysql8-v1',
                    'environmentSHA256': hashlib.sha256(target.read_bytes()).hexdigest()}
        _, path, profile, digest = idle.environment_identity(config, identity, config, owned, metadata_root)
        assert path == target and profile == 'mysql8-v1' and digest == identity['environmentSHA256']
        checks += 1
        for value, api, api_values in ((config, {}, config), (config, identity, {}),
                                      (config, {**identity, 'environmentSHA256': '0' * 64}, config),
                                      (config, {k: v for k, v in identity.items() if k != 'environmentSHA256'}, config),
                                      (config, {**identity, 'resourceProfile': 'baseline'}, config)):
            try:
                idle.environment_identity(value, api, api_values, owned, metadata_root)
            except (AssertionError, ValueError):
                checks += 1
            else:
                raise AssertionError('idle must reject a different original environment identity')
        baseline_versioned = metadata_root / 'environment-abcdef012345.json'; write(baseline_versioned, {})
        baseline_config = {'CAP10_ENVIRONMENT_METADATA': str(baseline_versioned)}
        try:
            idle.environment_identity(baseline_config, {'environmentPath': baseline_versioned.name}, baseline_config, owned, metadata_root)
        except (AssertionError, ValueError):
            checks += 1
        else:
            raise AssertionError('versioned baseline idle API must retain environment SHA')

        resources = idle.module('cap10_idle_cpu_path_check', 'cap10-resources.py')
        cid = 'a' * 64
        cpu, memory = idle.RESOURCE.container_limits('mysql8-v1')['mysql']
        actual_identities = idle.container_identities({'mysql': {'id': cid, 'cpuNano': cpu, 'memoryBytes': memory}}, 'mysql8-v1')
        assert actual_identities == [{'id': cid, 'service': 'mysql', 'cpuNano': 8_000_000_000,
                                     'memoryBytes': 8 * 1024 ** 3, 'resourceProfile': 'mysql8-v1'}]
        checks += 1
        layout = {'version': 2, 'membership': '/', 'paths': ['/sys/fs/cgroup/cpu.stat', '/sys/fs/cgroup/cpu.max']}
        counters = 'usage_usec 100000\nnr_periods 100\nnr_throttled 2\nthrottled_usec 1200\n800000 100000\n'
        with patch.dict(resources.CPU_LAYOUTS, {cid: layout}, clear=True), \
                patch.object(resources.subprocess, 'run', return_value=SimpleNamespace(stdout=counters)) as mocked_run:
            assert resources.cpu_stat(actual_identities[0])['quotaUsec'] == 800000
            mocked_run.assert_called_once_with(['docker', 'exec', cid, 'cat', *layout['paths']],
                                              capture_output=True, text=True, timeout=4, check=True)
            checks += 1
            try:
                resources.cpu_stat({'id': cid, 'service': 'mysql'})
            except ValueError:
                checks += 1
            else:
                raise AssertionError('missing resource profile must not validate an eight-CPU cgroup as baseline')

        namespace_root = base / 'versioned'; namespace_root.mkdir()
        private_runtime = base / 'private-runtime'; private_runtime.mkdir()
        metadata_path = namespace_root / target.name; write(metadata_path, {'resourceProfile': 'mysql8-v1'})
        old_slot = namespace_root / 'slot-S19.json'; write(old_slot, {'environmentSHA256': 'a' * 64})
        old_idle = namespace_root / 'idle-result.json'; write(old_idle, {'passed': False})
        retained = {path: path.read_bytes() for path in (old_slot, old_idle)}
        def exclusive_save(path, value):
            with path.open('x', encoding='utf-8') as stream:
                json.dump(value, stream)
        owner_tools = SimpleNamespace(read=lambda p: json.loads(p.read_bytes()), save=exclusive_save)
        directory, owner = idle.claim_versioned_idle(namespace_root, private_runtime, metadata_path, 'mysql8-v1', owner_tools, (None,) * 3)
        lock = private_runtime / 'cap10-active.private.json'
        assert directory.name == 'idle-012345abcdef' and owner['pid'] > 0 and owner['created'] > 0
        assert owner_tools.read(lock) == owner and all(path.read_bytes() == raw for path, raw in retained.items())
        checks += 1
        assert not idle.release_idle_owner(private_runtime, {**owner, 'created': owner['created'] + 1}, None, owner_tools) and lock.exists()
        checks += 1
        assert not idle.release_idle_owner(private_runtime, owner, SimpleNamespace(poll=lambda: None), owner_tools) and lock.exists()
        checks += 1
        assert idle.release_idle_owner(private_runtime, owner, SimpleNamespace(poll=lambda: 0), owner_tools) and not lock.exists()
        checks += 1
        for retries in ((None,) * 3, ('legacy-authorization', None, None)):
            try:
                idle.claim_versioned_idle(namespace_root, private_runtime, metadata_path, 'mysql8-v1', owner_tools, retries)
            except AssertionError:
                checks += 1
            else:
                raise AssertionError('claimed namespace or legacy retry cannot create another observation')
        second_metadata = namespace_root / 'environment-abcdef012345.json'; write(second_metadata, {'resourceProfile': 'mysql8-v1'})
        new_slot = namespace_root / 'slot-S20.json'; write(new_slot, {'environmentPath': second_metadata.name, 'resourceProfile': 'mysql8-v1'})
        try:
            idle.claim_versioned_idle(namespace_root, private_runtime, second_metadata, 'mysql8-v1', owner_tools, (None,) * 3)
        except AssertionError:
            checks += 1
        else:
            raise AssertionError('new environment cannot fill its idle after a reservation')
        new_slot.unlink()
        exclusive_save(lock, {'kind': 'another-owner'})
        unknown_lock = lock.read_bytes()
        try:
            idle.claim_versioned_idle(namespace_root, private_runtime, second_metadata, 'mysql8-v1', owner_tools, (None,) * 3)
        except FileExistsError:
            assert lock.read_bytes() == unknown_lock
            checks += 1
        else:
            raise AssertionError('idle must take a real exclusive owner file')
        checks += early_setup_failure_checks(base)

        def case():
            nonlocal checks
            checks += 1
            batch = base / str(checks)
            root = batch / 'linux'
            archive = root / 'idle-attempt-1'
            archive.mkdir(parents=True)
            failed = {'passed': False, 'samples': [], 'noWorkloadStarted': True, 'seconds': 60,
                      'executionPlatform': 'linux', 'transitionSHA256': TRANSITION}
            reservation = {'attempt': 1, 'seconds': 60, 'failedAttemptConsumesOnlyIdleObservation': True,
                           'executionPlatform': 'linux', 'transitionSHA256': TRANSITION}
            failed_path = archive / 'idle-result.json'
            write(failed_path, failed)
            write(archive / 'idle-reservation.json', reservation)
            authorization = {'version': 1, 'batch': 'CAP-10', 'authorized': True,
                'userAuthorizationReference': 'user explicitly authorized one idle retry', 'maxIdleRetries': 1,
                'failedIdleResultPath': 'linux/idle-attempt-1/idle-result.json',
                'failedIdleResultSHA256': hashlib.sha256(failed_path.read_bytes()).hexdigest(),
                'transitionSHA256': TRANSITION}
            auth_path = batch / 'continuation-authorization.json'
            write(auth_path, authorization)
            return root, auth_path, authorization, failed, reservation

        def rejected(change):
            root, auth_path, authorization, failed, reservation = case()
            change(root, auth_path, authorization, failed, reservation)
            try:
                idle.idle_attempt(root, auth_path, TRANSITION)
            except ValueError:
                return
            raise AssertionError('invalid idle retry authorization accepted')

        fresh = base / 'fresh' / 'linux'
        fresh.mkdir(parents=True)
        assert idle.idle_attempt(fresh, None, TRANSITION) == {'attempt': 1}
        checks += 1
        root, auth_path, authorization, failed, reservation = case()
        result = idle.idle_attempt(root, auth_path, TRANSITION)
        assert result == {'attempt': 2,
            'retryAuthorizationSHA256': hashlib.sha256(auth_path.read_bytes()).hexdigest(),
            'failedIdleResultSHA256': authorization['failedIdleResultSHA256']}
        try:
            idle.idle_attempt(root, None, TRANSITION)
        except ValueError:
            checks += 1
        else:
            raise AssertionError('archived failure was relabeled as attempt one')
        for field, value in [('version', True), ('version', 2), ('batch', 'CAP-11'),
                ('authorized', False), ('userAuthorizationReference', ' '), ('maxIdleRetries', True),
                ('maxIdleRetries', 2), ('failedIdleResultPath', 'linux/other/idle-result.json'),
                ('failedIdleResultSHA256', 'b' * 64), ('transitionSHA256', 'b' * 64)]:
            def change(root, path, auth, failed, reservation, field=field, value=value):
                auth[field] = value
                write(path, auth)
            rejected(change)
        for field, value in [('passed', True), ('samples', [{}]), ('noWorkloadStarted', False),
                ('transitionSHA256', 'b' * 64)]:
            def change(root, path, auth, failed, reservation, field=field, value=value):
                failed[field] = value
                original = root / 'idle-attempt-1/idle-result.json'
                write(original, failed)
                auth['failedIdleResultSHA256'] = hashlib.sha256(original.read_bytes()).hexdigest()
                write(path, auth)
            rejected(change)
        for field, value in [('attempt', 2), ('attempt', True), ('failedAttemptConsumesOnlyIdleObservation', False),
                ('transitionSHA256', 'b' * 64)]:
            def change(root, path, auth, failed, reservation, field=field, value=value):
                reservation[field] = value
                write(root / 'idle-attempt-1/idle-reservation.json', reservation)
            rejected(change)
        rejected(lambda root, path, auth, failed, reservation: write(root / 'slot-S1.json', {}))
        rejected(lambda root, path, auth, failed, reservation: write(root.parent / 'S1-command.json', {}))
        rejected(lambda root, path, auth, failed, reservation: path.rename(path.with_name('other.json')))

        def third_case():
            root, auth_path, auth, failed, reservation = case()
            base_sha = hashlib.sha256(auth_path.read_bytes()).hexdigest()
            second = {**failed, 'attempt': 2, 'retryAuthorizationSHA256': base_sha,
                'failedIdleResultSHA256': auth['failedIdleResultSHA256']}
            second_reservation = {**reservation, 'attempt': 2, 'retryAuthorizationSHA256': base_sha,
                'failedIdleResultSHA256': auth['failedIdleResultSHA256']}
            archive = root / 'idle-attempt-2'
            archive.mkdir()
            write(archive / 'idle-result.json', second)
            write(archive / 'idle-reservation.json', second_reservation)
            extra = {'version': 1, 'batch': 'CAP-10', 'authorized': True, 'attempt': 3,
                'maxAdditionalIdleRetries': 1, 'userAuthorizationReference': 'synthetic new explicit third idle approval',
                'recordedAt': dt.datetime.now(dt.timezone.utc).isoformat(),
                'continuationAuthorizationSHA256': base_sha, 'transitionSHA256': TRANSITION,
                'failedIdleResultPath': 'linux/idle-attempt-2/idle-result.json',
                'failedIdleResultSHA256': hashlib.sha256((archive / 'idle-result.json').read_bytes()).hexdigest()}
            extra_path = root.parent / 'idle-retry-3-authorization.json'
            write(extra_path, extra)
            return root, auth_path, extra_path, extra, second, second_reservation

        def third_rejected(change):
            root, auth_path, extra_path, extra, second, second_reservation = third_case()
            change(root, auth_path, extra_path, extra, second, second_reservation)
            try:
                idle.idle_attempt(root, auth_path, TRANSITION, extra_path)
            except ValueError:
                return
            raise AssertionError('invalid third-attempt authorization accepted')

        root, auth_path, extra_path, extra, second, second_reservation = third_case()
        original_auth_bytes = auth_path.read_bytes()
        third = idle.idle_attempt(root, auth_path, TRANSITION, extra_path)
        assert third == {'attempt': 3,
            'retryAuthorizationSHA256': hashlib.sha256(extra_path.read_bytes()).hexdigest(),
            'continuationAuthorizationSHA256': hashlib.sha256(original_auth_bytes).hexdigest(),
            'failedIdleResultSHA256': extra['failedIdleResultSHA256'],
            'firstFailedIdleResultSHA256': second['failedIdleResultSHA256']}
        assert auth_path.read_bytes() == original_auth_bytes
        try:
            idle.idle_attempt(root, auth_path, TRANSITION)
        except ValueError:
            checks += 1
        else:
            raise AssertionError('second failed attempt reused old approval without a new authorization')
        write(root / 'idle-reservation.json', third)
        try:
            idle.idle_attempt(root, auth_path, TRANSITION, extra_path)
        except ValueError:
            checks += 1
        else:
            raise AssertionError('third consumed reservation was reused')
        for field, value in [('authorized', False), ('authorized', 1), ('attempt', 4), ('attempt', True),
                ('maxAdditionalIdleRetries', 2), ('userAuthorizationReference', ''),
                ('continuationAuthorizationSHA256', 'b' * 64), ('transitionSHA256', 'b' * 64),
                ('failedIdleResultPath', 'linux/idle-attempt-1/idle-result.json'),
                ('failedIdleResultSHA256', 'b' * 64), ('recordedAt', '2026-11-02T00:00:00')]:
            def change(root, auth_path, path, auth, failed, reservation, field=field, value=value):
                auth[field] = value
                write(path, auth)
            third_rejected(change)
        for field, value in [('passed', True), ('samples', [{}]), ('attempt', 3),
                ('retryAuthorizationSHA256', 'b' * 64)]:
            def change(root, auth_path, path, auth, failed, reservation, field=field, value=value):
                failed[field] = value
                original = root / 'idle-attempt-2/idle-result.json'
                write(original, failed)
                auth['failedIdleResultSHA256'] = hashlib.sha256(original.read_bytes()).hexdigest()
                write(path, auth)
            third_rejected(change)
        third_rejected(lambda root, auth_path, path, auth, failed, reservation: write(root / 'slot-S1.json', {}))
        third_rejected(lambda root, auth_path, path, auth, failed, reservation: write(root.parent / 'H1-command.json', {}))

        old_boot, new_boot = 'a' * 8 + '-aaaa-aaaa-aaaa-' + 'a' * 12, 'b' * 8 + '-bbbb-bbbb-bbbb-' + 'b' * 12
        def fourth_case():
            root, auth_path, extra_path, extra, second, second_reservation = third_case()
            third = idle.idle_attempt(root, auth_path, TRANSITION, extra_path)
            archive = root / 'idle-attempt-3'
            archive.mkdir()
            reservation = {**third, 'seconds': 60, 'failedAttemptConsumesOnlyIdleObservation': True,
                'executionPlatform': 'linux', 'transitionSHA256': TRANSITION}
            write(archive / 'idle-reservation.json', reservation)
            samples = [{'power': {'clock': {'bootId': old_boot}}} for _ in range(3)]
            (archive / 'idle-samples.ndjson').write_text(''.join(json.dumps(row) + '\n' for row in samples), encoding='utf-8')
            record = {'version': 1, 'batch': 'CAP-10', 'attempt': 3, 'executionPlatform': 'linux',
                'interruptionKind': 'ecs_billing_shutdown', 'noWorkloadStarted': True, 'partialSampleCount': 3,
                'previousBootId': old_boot, 'currentBootId': new_boot, 'transitionSHA256': TRANSITION,
                'reservationSHA256': hashlib.sha256((archive / 'idle-reservation.json').read_bytes()).hexdigest(),
                'partialSamplesSHA256': hashlib.sha256((archive / 'idle-samples.ndjson').read_bytes()).hexdigest()}
            write(archive / 'interruption-record.json', record)
            auth = {'version': 1, 'batch': 'CAP-10', 'authorized': True, 'attempt': 4, 'maxInterruptionRetries': 1,
                'userAuthorizationReference': 'synthetic continuous authorization and billing shutdown recovery',
                'recordedAt': dt.datetime.now(dt.timezone.utc).isoformat(),
                'continuationAuthorizationSHA256': third['continuationAuthorizationSHA256'],
                'additionalRetryAuthorizationSHA256': third['retryAuthorizationSHA256'], 'transitionSHA256': TRANSITION,
                'interruptionRecordPath': 'linux/idle-attempt-3/interruption-record.json',
                'interruptionRecordSHA256': hashlib.sha256((archive / 'interruption-record.json').read_bytes()).hexdigest()}
            path = root.parent / 'idle-retry-4-authorization.json'
            write(path, auth)
            return root, auth_path, extra_path, path, auth, record, reservation

        def fourth_rejected(change):
            root, auth_path, extra_path, path, auth, record, reservation = fourth_case()
            change(root, auth_path, extra_path, path, auth, record, reservation)
            try:
                idle.idle_attempt(root, auth_path, TRANSITION, extra_path, path, new_boot)
            except ValueError:
                return
            raise AssertionError('invalid interrupted fourth-attempt recovery accepted')

        root, auth_path, extra_path, path, auth, record, reservation = fourth_case()
        retained = (auth_path.read_bytes(), extra_path.read_bytes())
        fourth = idle.idle_attempt(root, auth_path, TRANSITION, extra_path, path, new_boot)
        assert fourth['attempt'] == 4 and fourth['interruptionRecordSHA256'] == auth['interruptionRecordSHA256']
        assert fourth['additionalRetryAuthorizationSHA256'] == auth['additionalRetryAuthorizationSHA256']
        assert retained == (auth_path.read_bytes(), extra_path.read_bytes())
        assert not (root / 'idle-attempt-3/idle-result.json').exists()
        try:
            idle.idle_attempt(root, auth_path, TRANSITION, extra_path)
        except ValueError:
            checks += 1
        else:
            raise AssertionError('interrupted third attempt reused its old authorization')
        write(root / 'idle-reservation.json', fourth)
        try:
            idle.idle_attempt(root, auth_path, TRANSITION, extra_path, path, new_boot)
        except ValueError:
            checks += 1
        else:
            raise AssertionError('interruption recovery reused consumed fourth attempt')
        for field, value in [('authorized', False), ('attempt', 5), ('maxInterruptionRetries', 2),
                ('userAuthorizationReference', ''), ('continuationAuthorizationSHA256', 'c' * 64),
                ('additionalRetryAuthorizationSHA256', 'c' * 64), ('transitionSHA256', 'c' * 64),
                ('interruptionRecordPath', 'linux/idle-attempt-2/interruption-record.json'),
                ('interruptionRecordSHA256', 'c' * 64), ('recordedAt', '2026-11-02T00:00:00')]:
            def change(root, auth_path, extra_path, path, auth, record, reservation, field=field, value=value):
                auth[field] = value
                write(path, auth)
            fourth_rejected(change)
        for field, value in [('noWorkloadStarted', False), ('partialSampleCount', 2),
                ('interruptionKind', 'unknown'), ('previousBootId', new_boot), ('currentBootId', old_boot),
                ('reservationSHA256', 'c' * 64), ('partialSamplesSHA256', 'c' * 64)]:
            def change(root, auth_path, extra_path, path, auth, record, reservation, field=field, value=value):
                record[field] = value
                original = root / 'idle-attempt-3/interruption-record.json'
                write(original, record)
                auth['interruptionRecordSHA256'] = hashlib.sha256(original.read_bytes()).hexdigest()
                write(path, auth)
            fourth_rejected(change)
        fourth_rejected(lambda root, auth_path, extra_path, path, auth, record, reservation:
            write(root / 'idle-attempt-3/idle-result.json', {'passed': False}))
        fourth_rejected(lambda root, auth_path, extra_path, path, auth, record, reservation: write(root / 'slot-S1.json', {}))
        fourth_rejected(lambda root, auth_path, extra_path, path, auth, record, reservation: write(root.parent / 'H1-command.json', {}))
    print(json.dumps({'passed': True, 'checks': checks, 'cloudCalled': False, 'workloadStarted': False}))


if __name__ == '__main__':
    main()
