"""Explicit CAP10 resource profiles and immutable metadata paths; no actions."""
from pathlib import Path
import re


BASELINE_PROFILE = 'baseline'
TARGET_PROFILE = 'mysql8-v1'
_BASELINE = {'mysql': (4_000_000_000, 2 << 30), 'redis': (2_000_000_000, 512 << 20),
             'kafka': (2_000_000_000, 1 << 30), 'elasticsearch': (2_000_000_000, 1 << 30)}
_METADATA_NAME = re.compile(r'environment(?:-[a-f0-9]{12})?\.json')


def profile_name(value=BASELINE_PROFILE):
    if type(value) is not str or value not in (BASELINE_PROFILE, TARGET_PROFILE):
        raise ValueError('explicit supported CAP10 resource profile required')
    return value


def container_limits(profile=BASELINE_PROFILE):
    limits = dict(_BASELINE)
    if profile_name(profile) == TARGET_PROFILE:
        limits['mysql'] = (8_000_000_000, 8 << 30)
    return limits


def metadata_path(configuration, root):
    """Only the original file or one explicitly versioned root metadata file."""
    if not isinstance(configuration, dict):
        raise ValueError('private configuration object required')
    root = Path(root).resolve()
    raw = configuration.get('CAP10_ENVIRONMENT_METADATA', str(root / 'environment.json'))
    if type(raw) is not str:
        raise ValueError('environment metadata path must be explicit text')
    path = Path(raw)
    if (not path.is_absolute() or path.resolve() != path or path.parent != root
            or not _METADATA_NAME.fullmatch(path.name)):
        raise ValueError('environment metadata outside its fixed versioned namespace')
    return path


def bound_profile(configuration, metadata):
    if not isinstance(configuration, dict) or not isinstance(metadata, dict):
        raise ValueError('private configuration and public metadata objects required')
    declared = profile_name(configuration.get('CAP10_RESOURCE_PROFILE', BASELINE_PROFILE))
    if profile_name(metadata.get('resourceProfile', BASELINE_PROFILE)) != declared:
        raise ValueError('private and public resource profiles differ')
    return declared


def application_metadata_path(application, root):
    """An old application always keeps environment.json, even after a switch."""
    if not isinstance(application, dict):
        raise ValueError('application identity object required')
    name = application.get('environmentPath', 'environment.json')
    if type(name) is not str or not _METADATA_NAME.fullmatch(name):
        raise ValueError('application environment reference is outside its original namespace')
    return metadata_path({'CAP10_ENVIRONMENT_METADATA': str(Path(root).resolve() / name)}, root)
