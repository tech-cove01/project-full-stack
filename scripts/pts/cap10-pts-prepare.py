"""Prepare/register, bind and precisely clean a private PTS upload packet.

No cloud API, dependency, service or load calls. Actual CLI packet/bind require
the authorized native Linux CAP10 runtime. Node receives secrets via stdin only.
Nested cleanup contract: runtime/owned-pts-upload-<nonce>.json records exact
packet paths before bundle() may write cap10-pts-private.json. Cleanup must use
those registered paths, never recursively scan/delete an arbitrary directory.
"""
import argparse
import hashlib
import importlib.util
import ipaddress
import json
import os
from pathlib import Path
import re
import secrets
import stat
import subprocess
import sys

REPO = Path(__file__).resolve().parents[2]
_cohort_spec = importlib.util.spec_from_file_location('cap10_pts_prepare_cohort', REPO / 'scripts/cap10-cohort.py')
cohort = importlib.util.module_from_spec(_cohort_spec); _cohort_spec.loader.exec_module(cohort)
UPLOAD_FILES = ('cap10-jmeter.jmx', 'slots.csv', 'cap10-pts-private.json')
PACKET_FILES = (*UPLOAD_FILES, 'cap10-jmeter-manifest.json')
NODE_PROGRAM = """import {bundle} from './scripts/pts/cap10-jmeter.mjs';
let input=''; for await (const chunk of process.stdin) input+=chunk;
const v=JSON.parse(input); const m=await bundle(v.directory,{collectorUrl:v.collectorUrl,controlToken:v.controlToken,poolSize:v.poolSize,expectedEngines:v.expectedEngines});
process.stdout.write(JSON.stringify(m));"""


def source_hashes():
    return {name: hashlib.sha256((REPO / 'scripts/pts' / name).read_bytes()).hexdigest()
            for name in ('cap10-pts-prepare.py', 'cap10-jmeter.mjs', 'cap10-jmeter.groovy', 'cap10-pts-contract.groovy')}


def cohort_source_hashes():
    return {name: hashlib.sha256((REPO / name).read_bytes()).hexdigest()
            for name in ('scripts/cap10-cohort.py', 'scripts/load/cap10-cohort-contract.mjs')}


def manifest_dimensions(manifest, require_frozen=False):
    if not isinstance(manifest, dict):
        raise ValueError('JMeter manifest object required')
    pool = cohort.pool_size(manifest.get('poolSize', cohort.DEFAULT_POOL_SIZE))
    dims = cohort.dimensions({'poolSize': pool, 'expectedEngines': manifest.get('expectedEngines', 2),
                              'ptsConcurrency': manifest.get('ptsConcurrency')}, {'poolSize': pool})
    if ('poolSize' not in manifest and dims != {'poolSize': 512, 'expectedEngines': 2, 'ptsConcurrency': 513}):
        raise ValueError('missing pool fields only identify the historical 512/2 profile')
    if (manifest.get('standardJMeter') != '5.6.3' or type(manifest.get('normalThreads')) is not int
            or manifest['normalThreads'] != pool or type(manifest.get('teardownThreads')) is not int
            or manifest['teardownThreads'] != 1 or manifest.get('csvSplitRequired') is not True):
        raise ValueError('generated JMeter cohort contract differs')
    declared = manifest.get('cohortSourceSHA256')
    expected = {'scripts/load/cap10-cohort-contract.mjs': cohort_source_hashes()['scripts/load/cap10-cohort-contract.mjs']}
    if declared is None:
        if require_frozen or dims != {'poolSize': 512, 'expectedEngines': 2, 'ptsConcurrency': 513}:
            raise ValueError('legacy cohort lacks a current frozen helper source')
    elif declared != expected:
        raise ValueError('generated cohort helper source differs')
    return dims


def bound_cohort(packet_record, config):
    dims = manifest_dimensions(packet_record['manifest'], require_frozen=True)
    if cohort.dimensions(config, {'poolSize': dims['poolSize']}) != dims:
        raise ValueError('private configuration differs from packet cohort')
    expected = cohort_source_hashes()
    if packet_record.get('cohortSourceSHA256') != expected or config.get('cohortSourceSHA256') != expected:
        raise ValueError('packet and private cohort source freeze differ')
    return dims


def private_ip(value):
    address = ipaddress.IPv4Address(value)
    if not any(address in ipaddress.ip_network(cidr) for cidr in ('10.0.0.0/8', '172.16.0.0/12', '192.168.0.0/16')):
        raise ValueError('explicit RFC1918 ECS IPv4 required')
    return str(address)


def run_bundle(directory, collector_url, token, runner=subprocess.run, *, pool_size=512, expected_engines=2):
    # Fixed script/arguments: credential values are never command-line arguments.
    argv = ['node', '--input-type=module', '-e', NODE_PROGRAM]
    safe_env = {k: v for k, v in os.environ.items() if k.upper() in ('PATH', 'SYSTEMROOT', 'TEMP', 'TMP', 'TMPDIR')}
    result = runner(argv, input=json.dumps({'directory': str(directory), 'collectorUrl': collector_url,
                    'controlToken': token, 'poolSize': pool_size, 'expectedEngines': expected_engines}), cwd=REPO, env=safe_env, capture_output=True,
                    text=True, encoding='utf-8', timeout=30)
    if result.returncode != 0:
        raise ValueError('local packet generator failed; output suppressed')
    return json.loads(result.stdout)


def validate_owned(owned, environment):
    value, runtime = owned.settings(environment)
    runtime = Path(runtime).resolve()
    if value.get('CAP10_PLATFORM') != 'linux' or not re.fullmatch(r'\.runtime-fullstack-cap10-[a-f0-9]{12}', runtime.name):
        raise ValueError('native authorized Linux runtime required')
    if runtime.parent != owned.REPO.parent or Path(value['CAP10_RUNTIME']).resolve() != runtime:
        raise ValueError('runtime ownership differs')
    if Path(value['CAP10_EVIDENCE']).resolve() != owned.ROOT:
        raise ValueError('evidence root differs')
    return value, runtime


def packet(owned, environment, ecs_private_ip, credentials_file=None, region='cn-shanghai', bundle_runner=run_bundle, *, pool_size=512, expected_engines=2):
    requested = cohort.dimensions({'poolSize': pool_size, 'expectedEngines': expected_engines}, {'poolSize': pool_size})
    value, runtime = validate_owned(owned, environment)
    if region != 'cn-shanghai':
        raise ValueError('this frozen ECS/PTS packet uses Shanghai')
    host = private_ip(ecs_private_ip)
    if (owned.ROOT / 'pts-packet.json').exists() or list(runtime.glob('owned-pts-upload-*.json')):
        raise ValueError('preserve existing or partial upload packet; no automatic repeat')
    credential_path = None
    if credentials_file is not None:
        credential_path = Path(credentials_file).resolve()
        if not credential_path.is_file() or not credential_path.name.endswith('.private.json') or (os.name != 'nt' and credential_path.stat().st_mode & 0o077):
            raise ValueError('explicit private credential file required')
    nonce, token = secrets.token_hex(6), secrets.token_hex(32)
    directory = runtime / ('pts-upload-' + nonce)
    if directory.exists():
        raise ValueError('new packet directory required')
    registration = runtime / ('owned-pts-upload-' + nonce + '.json')
    owned.save(registration, {'version': 1, 'parent': str(directory),
        'privateInputPath': str(directory / 'cap10-pts-private.json'),
        'files': [str(directory / name) for name in PACKET_FILES], 'registeredBeforeSecretWrite': True})
    collector_url, api_url = 'http://' + host + ':8481', 'http://' + host + ':8281'
    before_sources, before_cohort = source_hashes(), cohort_source_hashes()
    manifest = bundle_runner(directory, collector_url, token, pool_size=pool_size, expected_engines=expected_engines)
    if source_hashes() != before_sources or cohort_source_hashes() != before_cohort:
        raise ValueError('packet sources changed during generation')
    if manifest_dimensions(manifest, require_frozen=True) != requested:
        raise ValueError('generated JMeter contract differs')
    if not re.fullmatch(r'[a-f0-9]{64}', manifest.get('scriptSHA256', '')) or not all((directory / name).is_file() for name in PACKET_FILES):
        raise ValueError('complete upload packet required')
    if os.name != 'nt' and (directory.stat().st_mode & 0o077 or (directory / 'cap10-pts-private.json').stat().st_mode & 0o077):
        raise ValueError('packet/private file permission differs')
    # Check secret-file shape before returning paths, without publishing values.
    private = json.loads((directory / 'cap10-pts-private.json').read_text(encoding='utf-8'))
    if private != {'collectorUrl': collector_url, 'controlToken': token}:
        raise ValueError('generated private collector binding differs')
    md5s = {name: hashlib.md5((directory / name).read_bytes(), usedforsecurity=False).hexdigest() for name in UPLOAD_FILES}
    cfg = {'sceneId': 'UNBOUND', 'regionId': region, 'collectorUrl': collector_url, 'apiUrl': api_url,
           'controlToken': token, **requested, 'cohortSourceSHA256': before_cohort,
           'scriptSHA256': manifest['scriptSHA256'], 'sceneFiles': md5s}
    if credential_path is not None:
        cfg['credentialsFile'] = str(credential_path)
    config_path = owned.private_write(runtime, 'env-pts-control-' + nonce + '.private.json', cfg)
    updated = {**value, 'CAP10_LOAD_BACKEND': 'pts', 'CAP10_PTS_CONFIG': str(config_path),
               'CAP10_PYTHON_BIN': sys.executable, 'API_ADDR': '0.0.0.0:8281'}
    next_env = owned.private_write(runtime, 'env-pts-' + nonce + '.private.json', updated)
    public = {'version': 1, 'manifest': manifest, 'sourceSHA256': before_sources, 'cohortSourceSHA256': before_cohort,
        'privatePaths': {'packetDirectory': str(directory), 'cleanupRegistration': str(registration),
                         'configuration': str(config_path), 'environment': str(next_env)}, 'sceneFilesMD5': md5s}
    if token in json.dumps(public):
        raise ValueError('public packet metadata contains a credential')
    owned.save(owned.ROOT / 'pts-packet.json', public)
    return public


def bind(owned, environment, scene_id):
    value, runtime = validate_owned(owned, environment)
    if not re.fullmatch(r'[A-Za-z0-9_-]{1,128}', scene_id) or scene_id == 'UNBOUND':
        raise ValueError('explicit existing SceneId required')
    if value.get('CAP10_LOAD_BACKEND') != 'pts' or (owned.ROOT / 'pts-scene-binding.json').exists():
        raise ValueError('new PTS scene binding required')
    old_path = Path(value['CAP10_PTS_CONFIG']).resolve()
    if old_path.parent != runtime or not re.fullmatch(r'env-pts-control-[a-f0-9]{12}\.private\.json', old_path.name):
        raise ValueError('owned packet configuration required')
    cfg = owned.read(old_path)
    packet_record = owned.read(owned.ROOT / 'pts-packet.json')
    bound_cohort(packet_record, cfg)
    if packet_record['sourceSHA256'] != source_hashes():
        raise ValueError('packet sources differ before binding')
    if old_path != Path(packet_record['privatePaths']['configuration']).resolve():
        raise ValueError('binding requires the original registered packet config')
    upload = Path(packet_record['privatePaths']['packetDirectory']).resolve()
    if upload.parent != runtime or not re.fullmatch(r'pts-upload-[a-f0-9]{12}', upload.name):
        raise ValueError('owned upload directory required')
    actual_md5 = {name: hashlib.md5((upload / name).read_bytes(), usedforsecurity=False).hexdigest() for name in UPLOAD_FILES}
    uploaded_private = owned.read(upload / 'cap10-pts-private.json')
    if cfg.get('sceneId') != 'UNBOUND' or set(cfg.get('sceneFiles', {})) != set(UPLOAD_FILES) or cfg['sceneFiles'] != packet_record['sceneFilesMD5']:
        raise ValueError('unbound original packet configuration required')
    if cfg['sceneFiles'] != actual_md5 or uploaded_private != {'collectorUrl': cfg['collectorUrl'], 'controlToken': cfg['controlToken']}:
        raise ValueError('packet files or private binding changed after generation')
    nonce = secrets.token_hex(6)
    config_path = owned.private_write(runtime, 'env-pts-control-' + nonce + '.private.json', {**cfg, 'sceneId': scene_id})
    next_env = owned.private_write(runtime, 'env-pts-bound-' + nonce + '.private.json', {**value, 'CAP10_PTS_CONFIG': str(config_path)})
    public = {'version': 1, 'sceneId': scene_id, 'sourceSHA256': source_hashes(), 'cohortSourceSHA256': cohort_source_hashes(),
        'packetManifestSHA256': hashlib.sha256((owned.ROOT / 'pts-packet.json').read_bytes()).hexdigest(),
        'privatePaths': {'configuration': str(config_path), 'environment': str(next_env)},
        'sceneFilesMD5': cfg['sceneFiles'], 'cloudCalled': False, 'sceneCreatedByThisTool': False}
    if cfg['controlToken'] in json.dumps(public):
        raise ValueError('public binding contains a credential')
    owned.save(owned.ROOT / 'pts-scene-binding.json', public)
    return public


def cleanup_packet(owned, environment):
    """Delete only registered packet files; private env/config cleanup is separate."""
    _, runtime = validate_owned(owned, environment)
    plan_path, result_path = owned.ROOT / 'pts-packet-cleanup-plan.json', owned.ROOT / 'pts-packet-cleanup.json'
    if plan_path.exists() or result_path.exists():
        raise ValueError('preserve prior packet cleanup evidence; no automatic repeat')
    registrations = sorted(runtime.glob('owned-pts-upload-*.json'))
    if not registrations:
        raise ValueError('explicit upload registration required')
    entries = []
    # Validate every registration and the complete actual directory contents
    # before deleting anything. Partial bundle outputs may contain fewer files.
    for registration in registrations:
        match = re.fullmatch(r'owned-pts-upload-([a-f0-9]{12})\.json', registration.name)
        if not match or registration.is_symlink() or registration.resolve().parent != runtime or not registration.is_file():
            raise ValueError('owned regular upload registration required')
        directory = runtime / ('pts-upload-' + match[1])
        expected = [directory / name for name in PACKET_FILES]
        value = owned.read(registration)
        if (not isinstance(value, dict) or value.get('version') != 1 or value.get('registeredBeforeSecretWrite') is not True
                or value.get('parent') != str(directory)
                or value.get('privateInputPath') != str(directory / 'cap10-pts-private.json')
                or value.get('files') != [str(path) for path in expected]):
            raise ValueError('registered packet paths differ from exact owned packet')
        if directory.is_symlink() or directory.resolve() != directory or directory.parent != runtime:
            raise ValueError('packet directory resolves outside owned runtime')
        if directory.exists() and not directory.is_dir():
            raise ValueError('packet parent is not a directory')
        if directory.exists() and not set(directory.iterdir()).issubset(set(expected)):
            raise ValueError('packet contains an unregistered entry')
        files = []
        for path in expected:
            if path.is_symlink() or path.resolve() != path or path.parent != directory:
                raise ValueError('packet file resolves outside exact owned parent')
            present = path.exists()
            if present and not stat.S_ISREG(path.lstat().st_mode):
                raise ValueError('packet entries must be regular files')
            files.append({'path': str(path), 'presentBefore': present, 'deleted': False})
        entries.append({'registration': str(registration),
            'registrationSHA256': hashlib.sha256(registration.read_bytes()).hexdigest(),
            'packetDirectory': str(directory), 'directoryPresentBefore': directory.exists(),
            'files': files, 'directoryRemoved': False, 'registrationRetained': True})
    owned.save(plan_path, {'version': 1, 'entries': entries, 'cloudCalled': False})
    result = {'version': 1, 'complete': False, 'entries': entries, 'cloudCalled': False,
              'privateConfigurationCleanup': 'separate-owned-env-cleanup', 'externalCredentialsTouched': False}
    try:
        for entry in entries:
            directory = Path(entry['packetDirectory'])
            registration = Path(entry['registration'])
            if registration.is_symlink() or hashlib.sha256(registration.read_bytes()).hexdigest() != entry['registrationSHA256']:
                raise ValueError('registration changed after cleanup preflight')
            for item in entry['files']:
                path = Path(item['path'])
                if path.is_symlink() or directory.is_symlink() or path.resolve() != path:
                    raise ValueError('packet path changed after cleanup preflight')
                if path.exists():
                    if not item['presentBefore'] or not stat.S_ISREG(path.lstat().st_mode):
                        raise ValueError('packet entry changed after cleanup preflight')
                    path.unlink()
                    item['deleted'] = True
            if directory.exists():
                directory.rmdir()  # Fails on unknown entries; never recurse.
                entry['directoryRemoved'] = True
        result['complete'] = True
    except BaseException as error:
        result['errorClass'] = type(error).__name__
        raise
    finally:
        owned.save(result_path, result)
    return result


def verify_scene(owned, environment, allow_live=False, client_factory=None):
    """Explicit read-only admission; never call Start, Stop, running or _idle."""
    if allow_live is not True:
        raise ValueError('verify-scene requires explicit --allow-live')
    output = owned.ROOT / 'pts-scene-admission.json'
    if output.exists():
        raise ValueError('preserve prior scene admission evidence')
    result = {'version': 1, 'passed': False, 'cloudCalled': False,
        'readRAMActionVerified': None, 'startStopPermissionsVerified': False, 'startStopCalled': False}
    try:
        value, runtime = validate_owned(owned, environment)
        before, before_cohort = source_hashes(), cohort_source_hashes()
        cfg_path = Path(value['CAP10_PTS_CONFIG'])
        if (value.get('CAP10_LOAD_BACKEND') != 'pts' or cfg_path.is_symlink() or cfg_path.resolve() != cfg_path
                or cfg_path.parent != runtime or not re.fullmatch(r'env-pts-control-[a-f0-9]{12}\.private\.json', cfg_path.name)
                or not cfg_path.is_file() or (os.name != 'nt' and cfg_path.stat().st_mode & 0o077)):
            raise ValueError('owned private bound PTS configuration required')
        if not any(owned.read(path).get('path') == str(cfg_path) for path in runtime.glob('owned-env-*.json')):
            raise ValueError('private configuration lacks owned-env registration')
        cfg = owned.read(cfg_path)
        if (cfg.get('regionId') != 'cn-shanghai'
                or not re.fullmatch(r'[a-f0-9]{64}', cfg.get('controlToken', ''))):
            raise ValueError('fixed Shanghai private configuration required')
        packet_record = owned.read(owned.ROOT / 'pts-packet.json')
        binding = owned.read(owned.ROOT / 'pts-scene-binding.json')
        dims = bound_cohort(packet_record, cfg)
        if binding.get('cohortSourceSHA256') != before_cohort:
            raise ValueError('binding cohort helper sources differ')
        scene_id = cfg['sceneId']
        if (not re.fullmatch(r'[A-Za-z0-9_-]{1,128}', scene_id) or scene_id == 'UNBOUND'
                or binding['sceneId'] != scene_id or Path(binding['privatePaths']['configuration']) != cfg_path
                or binding['packetManifestSHA256'] != hashlib.sha256((owned.ROOT / 'pts-packet.json').read_bytes()).hexdigest()
                or packet_record['sourceSHA256'] != before or binding['sourceSHA256'] != before):
            raise ValueError('bound scene or frozen packet sources differ')
        upload = Path(packet_record['privatePaths']['packetDirectory'])
        if upload.is_symlink() or upload.resolve() != upload or upload.parent != runtime or not re.fullmatch(r'pts-upload-[a-f0-9]{12}', upload.name):
            raise ValueError('owned original packet directory required')
        if any((upload / name).is_symlink() or not (upload / name).is_file() for name in PACKET_FILES):
            raise ValueError('original regular packet files required')
        manifest = packet_record['manifest']
        script = (REPO / 'scripts/pts/cap10-pts-contract.groovy').read_bytes() + b'\n' + (REPO / 'scripts/pts/cap10-jmeter.groovy').read_bytes()
        md5s = {name: hashlib.md5((upload / name).read_bytes(), usedforsecurity=False).hexdigest() for name in UPLOAD_FILES}
        if (owned.read(upload / 'cap10-jmeter-manifest.json') != manifest
                or manifest['scriptSHA256'] != hashlib.sha256(script).hexdigest() or cfg['scriptSHA256'] != manifest['scriptSHA256']
                or manifest['jmxSHA256'] != hashlib.sha256((upload / 'cap10-jmeter.jmx').read_bytes()).hexdigest()
                or manifest['slotsSHA256'] != hashlib.sha256((upload / 'slots.csv').read_bytes()).hexdigest()
                or md5s != cfg['sceneFiles'] or md5s != packet_record['sceneFilesMD5'] or md5s != binding['sceneFilesMD5']
                or owned.read(upload / 'cap10-pts-private.json') != {'collectorUrl': cfg['collectorUrl'], 'controlToken': cfg['controlToken']}):
            raise ValueError('packet bytes or script identity changed')
        if client_factory is None:
            spec = importlib.util.spec_from_file_location('cap10_pts_admission_api', Path(__file__).with_name('cap10-pts-api.py'))
            api = importlib.util.module_from_spec(spec); spec.loader.exec_module(api)
            client_factory = api.Client
        client = client_factory(cfg['regionId'], owned.ROOT / 'pts-scene-admission-api',
            credential_file=cfg.get('credentialsFile'), allow_live=True, scene_id=scene_id)
        result['cloudCalled'] = True
        scene = client.scene(scene_id)['Scene']  # Exactly GetOpenJMeterScene.
        result['readRAMActionVerified'] = 'pts:GetOpenJMeterScene'
        if (scene.get('SceneId') != scene_id or len(scene.get('FileList', [])) != 3
                or {item['FileName']: item['Md5'].lower() for item in scene['FileList']} != md5s
                or any(type(scene.get(key)) is not int or scene[key] != expected for key, expected in
                    (('Duration', 600), ('Concurrency', dims['ptsConcurrency']), ('AgentCount', dims['expectedEngines'])))):
            raise ValueError('cloud scene files or lifecycle/engine settings differ')
        if source_hashes() != before or cohort_source_hashes() != before_cohort:
            raise ValueError('packet sources changed during scene admission')
        result.update(passed=True, sceneId=scene_id, sceneFilesMD5=md5s, sourceSHA256=before, cohortSourceSHA256=before_cohort, **dims,
            scriptSHA256=manifest['scriptSHA256'], duration=600, concurrency=dims['ptsConcurrency'], agentCount=dims['expectedEngines'],
            configurationReference=str(cfg_path), scope='Only GetOpenJMeterScene read permission verified; no Start/Stop permission or cloud workload verified.')
        return result
    except BaseException as error:
        result['errorClass'] = type(error).__name__
        raise
    finally:
        owned.save(output, result)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    actions = parser.add_subparsers(dest='action', required=True)
    first = actions.add_parser('packet'); first.add_argument('--environment', required=True)
    first.add_argument('--ecs-private-ip', required=True); first.add_argument('--credentials-file')
    first.add_argument('--region', default='cn-shanghai')
    first.add_argument('--pool-size', type=int, default=512); first.add_argument('--expected-engines', type=int, default=2)
    second = actions.add_parser('bind'); second.add_argument('--environment', required=True); second.add_argument('--scene-id', required=True)
    third = actions.add_parser('cleanup-packet'); third.add_argument('--environment', required=True)
    fourth = actions.add_parser('verify-scene'); fourth.add_argument('--environment', required=True); fourth.add_argument('--allow-live', action='store_true')
    args = parser.parse_args()
    if sys.platform != 'linux' or Path.cwd().resolve() != REPO:
        raise ValueError('actual packet/bind/cleanup CLI requires native Linux repository cwd')
    spec = importlib.util.spec_from_file_location('cap10_pts_prepare_owned', REPO / 'scripts/cap10-env.py')
    owned = importlib.util.module_from_spec(spec); spec.loader.exec_module(owned)
    if args.action == 'packet':
        result = packet(owned, args.environment, args.ecs_private_ip, args.credentials_file, args.region,
                        pool_size=args.pool_size, expected_engines=args.expected_engines)
    elif args.action == 'bind':
        result = bind(owned, args.environment, args.scene_id)
    elif args.action == 'cleanup-packet':
        result = cleanup_packet(owned, args.environment)
    else:
        result = verify_scene(owned, args.environment, args.allow_live)
    print(json.dumps(result, ensure_ascii=False))


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        print(json.dumps({'prepared': False, 'errorClass': type(error).__name__, 'detailsSuppressed': True}), file=sys.stderr)
        raise SystemExit(1)
