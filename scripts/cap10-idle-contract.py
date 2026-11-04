"""Immutable CAP10 idle namespaces and preflight references; no runtime actions."""
import hashlib
import datetime as dt
import importlib.util
import json
import math
from pathlib import Path
import re


spec = importlib.util.spec_from_file_location('cap10_idle_resource_contract', Path(__file__).with_name('cap10-resource-contract.py'))
RESOURCE = importlib.util.module_from_spec(spec)
spec.loader.exec_module(RESOURCE)


def idle_directory(environment_path, root, profile=RESOURCE.BASELINE_PROFILE):
    root = Path(root).resolve()
    path = RESOURCE.metadata_path({'CAP10_ENVIRONMENT_METADATA': str(environment_path)}, root)
    profile = RESOURCE.profile_name(profile)
    if path.name == 'environment.json':
        if profile != RESOURCE.BASELINE_PROFILE:
            raise ValueError('a new resource profile requires versioned environment metadata')
        return root
    suffix = re.fullmatch(r'environment-([a-f0-9]{12})\.json', path.name).group(1)
    directory = root / ('idle-' + suffix)
    if directory.is_symlink() or directory.resolve() != directory:
        raise ValueError('idle namespace must remain its original ordinary directory')
    return directory


def preflight_path(record, root):
    if not isinstance(record, dict):
        raise ValueError('original command identity required')
    root = Path(root).resolve()
    environment = RESOURCE.application_metadata_path(record, root)
    directory = idle_directory(environment, root, record.get('resourceProfile', RESOURCE.BASELINE_PROFILE))
    canonical = directory / 'idle-result.json'
    retry = root / (directory.name + '-attempt-2') / 'idle-result.json'
    if directory != root and record.get('preflightPath') == retry.relative_to(root).as_posix():
        canonical = retry
    relative = canonical.relative_to(root).as_posix()
    if 'preflightPath' not in record:
        if directory != root:
            raise ValueError('versioned environment requires its explicit idle reference')
    elif type(record['preflightPath']) is not str or record['preflightPath'] != relative:
        raise ValueError('idle reference differs from its original environment namespace')
    if canonical.is_symlink() or canonical.resolve() != canonical:
        raise ValueError('idle result must remain its original ordinary file')
    return canonical


def _original_ref(value, root, expected):
    if (type(value) is not dict or set(value) != {'path', 'sha256'}
            or value.get('path') != expected.relative_to(root).as_posix()):
        raise ValueError('exact original startup failure reference required')
    raw = expected.read_bytes() if expected.is_file() and not expected.is_symlink() and expected.resolve() == expected else None
    if raw is None or value.get('sha256') != hashlib.sha256(raw).hexdigest():
        raise ValueError('original startup evidence bytes changed')
    return raw


def _instant(value):
    if not isinstance(value, str):
        raise ValueError('original aware observation timestamp required')
    parsed = dt.datetime.fromisoformat(value.replace('Z', '+00:00'))
    if parsed.tzinfo is None:
        raise ValueError('original aware observation timestamp required')
    return parsed


def validate_versioned_retry(root, environment_path, resource_profile, authorization_path):
    """Pure, explicit second observation of a retained zero-sample startup failure.

    The closure is a hash-bound root observation, not a live process check here.
    The producer must still claim its exclusive owner and recheck live absence.
    Consumers use the same chain without inspecting a later load's live owner.
    """
    root = Path(root).resolve()
    supplied_environment = Path(environment_path)
    environment_path = RESOURCE.application_metadata_path({'environmentPath': supplied_environment.name}, root)
    if supplied_environment != environment_path:
        raise ValueError('exact original versioned environment path required')
    directory = idle_directory(environment_path, root, resource_profile)
    if directory == root:
        raise ValueError('startup retry applies only to an explicit versioned environment')
    metadata, environment_sha = _read(environment_path)
    profile = RESOURCE.bound_profile({'CAP10_RESOURCE_PROFILE': resource_profile}, metadata)
    auth_path = root / (directory.name + '-retry-2-authorization.json')
    if Path(authorization_path) != auth_path:
        raise ValueError('one canonical startup-retry authorization required')
    auth, auth_sha = _read(auth_path)
    identity = {'environmentPath': environment_path.name, 'environmentSHA256': environment_sha, 'resourceProfile': profile}
    if (auth.get('version') != 1 or type(auth.get('version')) is not int
            or auth.get('kind') != 'versioned-idle-startup-retry'
            or type(auth.get('attempt')) is not int or auth['attempt'] != 2
            or any(auth.get(key) != value for key, value in identity.items())
            or not isinstance(auth.get('userAuthorizationReference'), str) or not auth['userAuthorizationReference'].strip()):
        raise ValueError('explicit same-environment second idle authorization required')
    originals = {'failedIdleResult': directory / 'idle-result.json',
                 'failedIdleReservation': directory / 'idle-reservation.json',
                 'failedObserverLog': directory / 'idle-observer.txt',
                 'closureProof': root / (directory.name + '-startup-closure.json')}
    raw = {name: _original_ref(auth.get(name), root, path) for name, path in originals.items()}
    failed, reservation, closure = (json.loads(raw[name]) for name in ('failedIdleResult', 'failedIdleReservation', 'closureProof'))
    if not all(type(value) is dict for value in (failed, reservation, closure)):
        raise ValueError('original startup evidence objects required')
    if (failed.get('passed') is not False or failed.get('samples') != []
            or failed.get('noWorkloadStarted') is not True
            or any(failed.get(key) != value or reservation.get(key) != value for key, value in identity.items())
            or any(type(value.get('attempt')) is not int or value['attempt'] != 1
                   or type(value.get('seconds')) is not int or value['seconds'] != 60
                   or type(value.get('intervalSeconds')) is not int or value['intervalSeconds'] != 5
                   or type(value.get('idleSchemaVersion')) is not int or value['idleSchemaVersion'] != 1
                   or value.get('idleDirectory') != directory.name
                   for value in (failed, reservation))
            or reservation.get('failedAttemptConsumesOnlyIdleObservation') is not True
            or failed.get('reservationPath') != originals['failedIdleReservation'].relative_to(root).as_posix()
            or failed.get('reservationSHA256') != auth['failedIdleReservation']['sha256']
            or raw['failedObserverLog'].decode('utf-8').strip() != 'cap10-downstream: invalid complete vector baseline audit'):
        raise ValueError('only the retained zero-sample vector-baseline startup failure may retry')
    for path in (directory / 'idle-samples.ndjson', directory / 'idle-downstream' / 'timeline.ndjson'):
        if path.exists() and (not path.is_file() or path.is_symlink() or path.resolve() != path or path.stat().st_size != 0):
            raise ValueError('startup failure must contain no host or observer sample')
    if (closure.get('version') != 1 or type(closure.get('version')) is not int
            or closure.get('kind') != 'versioned-idle-startup-closure'
            or any(closure.get(key) != value for key, value in identity.items())
            or closure.get('failedIdleResultSHA256') != auth['failedIdleResult']['sha256']
            or any(closure.get(key) is not True for key in ('idleOwnerReleased', 'observerAbsent', 'launcherExited', 'noWorkloadStarted'))
            or not (_instant(failed.get('finishedAt')) <= _instant(closure.get('observedAt'))
                    <= _instant(auth.get('recordedAt')) <= dt.datetime.now(dt.timezone.utc))):
        raise ValueError('root-observed original startup owners must be closed before retry')
    retry_directory = root / (directory.name + '-attempt-2')
    if retry_directory.is_symlink() or retry_directory.resolve() != retry_directory:
        raise ValueError('second idle namespace must remain ordinary')
    fields = {'attempt': 2, 'idleSchemaVersion': 1, 'idleDirectory': retry_directory.name,
              'retryAuthorizationPath': auth_path.relative_to(root).as_posix(), 'retryAuthorizationSHA256': auth_sha,
              'failedIdleResultPath': auth['failedIdleResult']['path'], 'failedIdleResultSHA256': auth['failedIdleResult']['sha256'],
              'failedIdleReservationPath': auth['failedIdleReservation']['path'], 'failedIdleReservationSHA256': auth['failedIdleReservation']['sha256'],
              'failedObserverLogPath': auth['failedObserverLog']['path'], 'failedObserverLogSHA256': auth['failedObserverLog']['sha256'],
              'startupClosurePath': auth['closureProof']['path'], 'startupClosureSHA256': auth['closureProof']['sha256']}
    return retry_directory, fields


def _read(path):
    if path.is_symlink() or path.resolve() != path or not path.is_file():
        raise ValueError('original regular idle evidence file required')
    raw = path.read_bytes()
    value = json.loads(raw)
    if not isinstance(value, dict):
        raise ValueError('idle evidence object required')
    return value, hashlib.sha256(raw).hexdigest()


def _number(value, lower, upper):
    return type(value) in (int, float) and math.isfinite(value) and lower <= value <= upper


def _complete_versioned(result, directory, expected_attempt=1):
    """Require completion fields emitted by the unchanged 60-second sampler."""
    if (type(result.get('idleSchemaVersion')) is not int or result['idleSchemaVersion'] != 1
            or result.get('idleDirectory') != directory.name
            or type(result.get('seconds')) is not int or result['seconds'] != 60
            or type(result.get('intervalSeconds')) is not int or result['intervalSeconds'] != 5
            or type(result.get('attempt')) is not int or result['attempt'] != expected_attempt
            or result.get('noWorkloadStarted') is not True or result.get('errors') != []
            or any(result.get(key) is not True for key in ('cadenceValid', 'uptimeValid', 'downstreamIdleAndComplete'))
            or result.get('threeConsecutiveBusy') is not False
            or type(result.get('observerExit')) is not int or result['observerExit'] != 0
            or not _number(result.get('hostCPUMedian'), 0, 50)
            or not _number(result.get('hostMemoryMaxPercent'), 0, 80)
            or type(result.get('downstreamSamples')) is not int or result['downstreamSamples'] < 12
            or not isinstance(result.get('powerEvents'), dict) or result['powerEvents'].get('valid') is not True):
        raise ValueError('versioned idle must retain its complete original 60-second admission')
    samples, intervals = result.get('samples'), result.get('clockIntervals')
    if (not isinstance(samples, list) or len(samples) != 13
            or not isinstance(intervals, list) or len(intervals) != 12
            or any(not isinstance(item, dict) or item.get('valid') is not True for item in intervals)
            or any(not isinstance(item, dict) for item in samples)
            or not _number(samples[0].get('elapsedSeconds'), 0, 1)
            or samples[0]['elapsedSeconds'] >= 1
            or samples[0].get('cpuIntervalValid') is not False
            or not _number(samples[-1].get('elapsedSeconds'), 60, 62.5)
            or any(item.get('cpuIntervalValid') is not True
                   or not _number(item.get('cpuIntervalSeconds'), 4, 7.5) for item in samples[1:])):
        raise ValueError('versioned idle requires thirteen original samples and twelve valid clock intervals')


def validate_preflight(record, root):
    """Verify this command's exact preflight file and original metadata bytes."""
    root = Path(root).resolve()
    path = preflight_path(record, root)
    result, digest = _read(path)
    if record.get('preflightSHA256') != digest or result.get('passed') is not True:
        raise ValueError('original idle preflight bytes must match and pass')
    environment = RESOURCE.application_metadata_path(record, root)
    metadata, environment_sha = _read(environment)
    profile = RESOURCE.bound_profile({'CAP10_RESOURCE_PROFILE': record.get('resourceProfile', RESOURCE.BASELINE_PROFILE)}, metadata)
    if (record.get('environmentSHA256') != environment_sha
            or RESOURCE.application_metadata_path(result, root) != environment
            or result.get('environmentSHA256') != environment_sha
            or RESOURCE.profile_name(result.get('resourceProfile', RESOURCE.BASELINE_PROFILE)) != profile):
        raise ValueError('idle preflight belongs to another original environment identity')
    if path.parent != root:
        base_directory = idle_directory(environment, root, profile)
        retry_fields = None
        if path.parent != base_directory:
            retry_directory, retry_fields = validate_versioned_retry(root, environment, profile,
                root / (base_directory.name + '-retry-2-authorization.json'))
            if path.parent != retry_directory or any(result.get(key) != value for key, value in retry_fields.items()):
                raise ValueError('second idle result must bind the exact retained startup failure')
        _complete_versioned(result, path.parent, 2 if retry_fields is not None else 1)
        reservation_path = path.parent / 'idle-reservation.json'
        if result.get('reservationPath') != reservation_path.relative_to(root).as_posix():
            raise ValueError('versioned idle requires its exact original reservation')
        reservation, reservation_sha = _read(reservation_path)
        if (result.get('reservationSHA256') != reservation_sha
                or reservation.get('idleSchemaVersion') != 1 or type(reservation.get('idleSchemaVersion')) is not int
                or reservation.get('idleDirectory') != path.parent.name
                or reservation.get('environmentPath') != environment.name
                or reservation.get('environmentSHA256') != environment_sha
                or reservation.get('resourceProfile') != profile
                or type(reservation.get('seconds')) is not int or reservation['seconds'] != 60
                or type(reservation.get('intervalSeconds')) is not int or reservation['intervalSeconds'] != 5
                or type(reservation.get('attempt')) is not int or reservation['attempt'] != (2 if retry_fields is not None else 1)
                or retry_fields is not None and any(reservation.get(key) != value for key, value in retry_fields.items())):
            raise ValueError('versioned idle reservation identity or duration differs')
    return {'path': path.relative_to(root).as_posix(), 'sha256': digest,
            'environmentPath': environment.name, 'environmentSHA256': environment_sha, 'resourceProfile': profile}
