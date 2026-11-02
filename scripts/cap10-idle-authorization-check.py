"""Pure local checks of separately authorized CAP10 idle retries."""
import datetime as dt
import hashlib
import importlib.util
import json
from pathlib import Path
import tempfile


REPO = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location('cap10_idle_authorization', REPO / 'scripts/cap10-idle.py')
idle = importlib.util.module_from_spec(spec)
spec.loader.exec_module(idle)
TRANSITION = 'a' * 64


def write(path, value):
    path.write_text(json.dumps(value), encoding='utf-8')


def main():
    checks = 0
    with tempfile.TemporaryDirectory(prefix='.qa-cap10-idle-', dir=REPO.parent) as folder:
        base = Path(folder).resolve()
        assert base.parent == REPO.parent and base.name.startswith('.qa-cap10-idle-')

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
    print(json.dumps({'passed': True, 'checks': checks, 'cloudCalled': False, 'workloadStarted': False}))


if __name__ == '__main__':
    main()
