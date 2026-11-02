"""Pure local checks of the sole explicitly authorized CAP10 idle retry."""
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
    print(json.dumps({'passed': True, 'checks': checks, 'cloudCalled': False, 'workloadStarted': False}))


if __name__ == '__main__':
    main()
