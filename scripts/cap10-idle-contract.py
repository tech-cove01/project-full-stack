"""Immutable CAP10 idle namespaces and preflight references; no runtime actions."""
import hashlib
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
    relative = canonical.relative_to(root).as_posix()
    if 'preflightPath' not in record:
        if directory != root:
            raise ValueError('versioned environment requires its explicit idle reference')
    elif type(record['preflightPath']) is not str or record['preflightPath'] != relative:
        raise ValueError('idle reference differs from its original environment namespace')
    if canonical.is_symlink() or canonical.resolve() != canonical:
        raise ValueError('idle result must remain its original ordinary file')
    return canonical


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


def _complete_versioned(result, directory):
    """Require completion fields emitted by the unchanged 60-second sampler."""
    if (type(result.get('idleSchemaVersion')) is not int or result['idleSchemaVersion'] != 1
            or result.get('idleDirectory') != directory.name
            or type(result.get('seconds')) is not int or result['seconds'] != 60
            or type(result.get('intervalSeconds')) is not int or result['intervalSeconds'] != 5
            or type(result.get('attempt')) is not int or result['attempt'] != 1
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
        _complete_versioned(result, path.parent)
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
                or type(reservation.get('attempt')) is not int or reservation['attempt'] != 1):
            raise ValueError('versioned idle reservation identity or duration differs')
    return {'path': path.relative_to(root).as_posix(), 'sha256': digest,
            'environmentPath': environment.name, 'environmentSHA256': environment_sha, 'resourceProfile': profile}
