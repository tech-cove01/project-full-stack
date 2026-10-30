"""Finish only this CAP03 run after two current-build holds per mode qualify.

No process environment, credentials, or native memory dump is exported. Windows
terminate is recorded as native termination, never described as Go graceful exit.
"""
import argparse
import datetime
import hashlib
import json
import os
from pathlib import Path
import re
import socket
import subprocess
import sys
import time

import psutil


def read(path):
    return json.loads(path.read_text(encoding='utf-8-sig'))


def call(args, *, input=None, env=None):
    result = subprocess.run(args, input=input, env=env, capture_output=True, text=True, encoding='utf-8', errors='replace')
    if result.returncode:
        raise RuntimeError(f'owned cleanup command failed with exit {result.returncode}; diagnostics suppressed')
    return result.stdout.strip()


def redis_exists(tokens):
    parts = ['EXISTS'] + ['login:token:' + token for token in tokens]
    request = b'*' + str(len(parts)).encode() + b'\r\n'
    for part in parts:
        value = part.encode()
        request += b'$' + str(len(value)).encode() + b'\r\n' + value + b'\r\n'
    with socket.create_connection(('127.0.0.1', 6551), timeout=5) as stream:
        stream.sendall(request)
        response = b''
        while b'\r\n' not in response:
            chunk = stream.recv(256)
            if not chunk:
                raise RuntimeError('owned Redis cleanup verification closed')
            response += chunk
    assert response.startswith(b':'), 'owned Redis verification must return integer'
    return int(response[1:].split(b'\r\n')[0])


parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--environment', type=Path, required=True)
parser.add_argument('--original-environment', type=Path, required=True)
parser.add_argument('--api-metadata', type=Path, required=True)
args = parser.parse_args()
base = Path(__file__).resolve().parent
repo = base.parents[3]
assert Path.cwd().resolve() == repo, 'run from project-full-stack'
assert not (base / 'cleanup.json').exists(), 'cleanup record must be new'
assert not (base / 'cleanup-progress.json').exists(), 'cleanup progress must be new; inspect partial prior execution before recovery'
summary = read(base / 'summary.json')
assert args.api_metadata.resolve() == base / 'api-pool-fix.json'
metadata = read(args.api_metadata)
assert summary['api'] == metadata and metadata['sourceSha'] == '767c2fc8bdf1be08c5636143a71898eec25081a9'
assert metadata['pid'] == 64908
assert set(summary['qualifiedModes']) == {'hot', 'multi'}
selected_names = []
for mode in ('hot', 'multi'):
    selected = summary['qualifiedModes'][mode]
    assert selected and selected['secondsPerRound'] == 180 and len(selected['rounds']) == 2
    for stage in selected['rounds']:
        name = stage['stage']
        assert re.fullmatch(r'hold-fix-(hot|multi)-[0-9]+-180-[ab]', name)
        selected_names.append(name)
        report = read(base / (name + '-load') / 'result.json')
        control = read(base / (name + '-control') / 'exit-status.json')
        command = read(base / (name + '-command.json'))
        assert stage['result'] == report['result'] and stage['mode'] == report['mode'] == mode
        assert stage['targetRate'] == report['rate'] == selected['rate'] and stage['plannedSeconds'] == report['seconds'] == 180
        assert report['apiSourceSha'] == metadata['sourceSha'] and report['apiExpectedRevision'] == metadata['expectedVersion']
        assert report['fixtureManifestSHA256'] == metadata['fixtureManifestSHA256'] and report['fixtureSourceSha'] == metadata['fixtureSourceSha']
        assert report['meetsBudget'] and stage['meetsBudget'] and stage['supervisionOk'] and not stage['stopSignal']
        result = report['result']
        assert result['attempted'] == result['completedHttpRequests'] == result['completed']
        assert not any(result[key] for key in ('incompleteOrUnclassified', 'completedButUnclassified', 'dropped', 'contentErrors'))
        assert result['attempted'] >= .99 * report['rate'] * 180 and result['successful'] >= .99 * result['attempted']
        assert result['systemErrors'] < .01 * result['attempted'] and result['httpErrors'] < .01 * result['attempted']
        for key in ('allLatency', 'successfulLatency'):
            assert result[key]['p(95)'] <= 200 and result[key]['p(99)'] <= 500
        assert control['api_pid'] == metadata['pid'] and control['harness_exit'] == control['sampler_exit'] == 0
        assert not control['stop_signal'] and not control['forced'] and not control['errors']
        assert control['completed_without_supervision_failure'] and control['resource_samples'] >= 35
        assert control['resource_samples'] == control['complete_resource_samples']
        assert Path(command['apiMetadataFile']).resolve() == args.api_metadata.resolve()
        assert command['apiMetadataSHA256'] == hashlib.sha256(args.api_metadata.read_bytes()).hexdigest()
        assert command['apiPid'] == metadata['pid'] and command['sourceSha'] == metadata['sourceSha']
assert len(set(selected_names)) == 4
for directory in base.glob('*-load'):
    assert (directory / 'result.json').exists() or (directory / 'unscored-failure.json').exists(), 'pending read load prevents cleanup'
settings = read(args.environment)
original = read(args.original_environment)
runtime = (repo.parent / '.runtime-fullstack-cap-read-2c79ddf4f58b').resolve()
assert args.environment.resolve() == runtime / 'env-pool-fix.private.json'
assert args.original_environment.resolve() == runtime / 'env.private.json'
assert settings['CAP_SOURCE_SHA'] == metadata['sourceSha']
assert settings['CAP_READ_EXPECTED_REVISION'] == metadata['expectedVersion']
assert settings['CAP_READ_FIXTURE_SOURCE_SHA'] == '3db296dd0beedd6f85d06cf5479d707b0498ce7e'
assert settings['MYSQL_PORT'] == '3451' and settings['REDIS_PORT'] == '6551'
assert settings['API_BASE'] == 'http://127.0.0.1:8251' and settings['CAP_READ_SUBNET'] == '10.253.51.0/24'
env = {**os.environ, **{key: str(value) for key, value in settings.items()}}
manifest_path = Path(settings['CAP_READ_FIXTURE']).resolve()
assert manifest_path == base / 'fixture-01' / 'fixture.json'
assert hashlib.sha256(manifest_path.read_bytes()).hexdigest() == metadata['fixtureManifestSHA256']
manifest = read(manifest_path)
credentials = Path(manifest['credentialFile']).resolve()
assert credentials.name == 'input.json' and credentials.is_relative_to(runtime / 'tmp')
tokens = [item['token'] for item in read(credentials)['readers']]
assert len(tokens) == len(set(tokens)) == 32
assert settings['CAP_READ_OBSERVER_USER'] == original['CAP_READ_OBSERVER_USER'] == 'capread_2c79ddf4f58b'
observer = settings['CAP_READ_OBSERVER_USER']
assert re.fullmatch(r'capread_[a-f0-9]{12}', observer)
process = psutil.Process(metadata['pid'])
assert os.path.normcase(process.exe()) == os.path.normcase(metadata['exe'])
created_delta = abs(process.create_time() * 1000 - metadata['creationUnixMs'])
assert created_delta <= 2000, 'owned API UTC creation mismatch; do not stop'
assert hashlib.sha256(Path(process.exe()).read_bytes()).hexdigest() == metadata['sha256']

owned = {}
known_ids = {line.split('|')[0].split('-')[-2]: line.split('|')[1] for line in read(base / 'environment.json')['ownedContainerLimits']}
for service, target, port in [('mysql', '3306/tcp', '3451'), ('redis', '6379/tcp', '6551')]:
    item = json.loads(call(['docker', 'inspect', known_ids[service]]))[0]
    assert item['Id'] == known_ids[service] and item['Name'] == f'/project-full-stack-cap-read-{service}-1'
    labels = item['Config']['Labels']
    assert labels['com.docker.compose.project'] == 'project-full-stack-cap-read' and labels['com.docker.compose.service'] == service
    assert item['State']['Running'] and any(p['HostIp'] == '127.0.0.1' and p['HostPort'] == port for p in item['NetworkSettings']['Ports'][target])
    owned[service] = {'id': item['Id'], 'volumes': [v['Name'] for v in item['Mounts'] if v['Type'] == 'volume']}

secrets = set(tokens)
for source in (settings, original):
    secrets.update(str(value) for key, value in source.items() if value and ('PASSWORD' in key or 'API_KEY' in key))
files = [path for directory in (base, base.parent / 'cap04') for path in directory.rglob('*') if path.is_file() and '__pycache__' not in path.parts]
matches = []
for path in files:
    data = path.read_bytes()
    if any(secret.encode() in data for secret in secrets):
        matches.append(path.name)
assert not matches, 'private-value match in public evidence; values suppressed; preserve runtime for investigation'

out = {'at': datetime.datetime.now(datetime.timezone.utc).isoformat(), 'complete': False,
       'privateValueScan': {'files': len(files), 'uniqueValues': len(secrets), 'matchingFiles': 0},
       'callbackExceptions': [], 'exceptions': [], 'selectedHolds': selected_names,
       'sqlFixtureRetained': True, 'volumesRetained': True, 'step': 'preflight-complete'}


def checkpoint(step):
    out['step'] = step
    out['updatedAt'] = datetime.datetime.now(datetime.timezone.utc).isoformat()
    (base / 'cleanup-progress.json').write_text(json.dumps(out, ensure_ascii=False, indent=2), encoding='utf-8')


def failure(kind, value, traceback):
    out['complete'] = False
    out['exceptions'].append({'type': kind.__name__, 'step': out['step'], 'detailsSuppressed': True})
    checkpoint(out['step'])
    (base / 'cleanup-failure.json').write_text(json.dumps(out, ensure_ascii=False, indent=2), encoding='utf-8')
    sys.stderr.write('Owned cleanup stopped; sanitized partial progress/failure recorded. Do not rerun blindly.\n')


sys.excepthook = failure
checkpoint('preflight-complete')
out['sessionsBeforeLogout'] = redis_exists(tokens)
out['finishCommandOutput'] = json.loads(call(['node', 'scripts/go073-cap-read.mjs', 'finish'], env=env))
checkpoint('logout-command-complete')
out['sessionsAfterLogout'] = redis_exists(tokens)
assert out['sessionsAfterLogout'] == 0 and not credentials.exists()
checkpoint('sessions-verified-missing')
drop_sql = f"DROP USER '{observer}'@'%'; SELECT COUNT(*) FROM mysql.user WHERE User='{observer}' AND Host='%';\n"
assert call(['docker', 'exec', '-i', owned['mysql']['id'], 'sh', '-c', 'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" exec mysql -uroot --batch --skip-column-names'], input=drop_sql) == '0'
out['observer'] = {'user': observer, 'host': '%', 'droppedAndVerifiedMissing': True}
checkpoint('observer-dropped-and-verified')

process = psutil.Process(metadata['pid'])
assert os.path.normcase(process.exe()) == os.path.normcase(metadata['exe'])
created_delta = abs(process.create_time() * 1000 - metadata['creationUnixMs'])
assert created_delta <= 2000, 'owned API UTC creation mismatch; do not stop'
process.terminate()
exit_code = process.wait(timeout=15)
out['apiStop'] = {'pid': metadata['pid'], 'exeMatched': True, 'creationTimeDifferenceMs': created_delta,
                  'binarySHA256Matched': True, 'method': 'Windows native terminate of guarded PID',
                  'goGracefulShutdownClaimed': False, 'waitExitCode': exit_code}
checkpoint('api-terminated')
out['portsClosed'] = {}
for port in (8251, 8351):
    closed = False
    for _ in range(20):
        try:
            with socket.create_connection(('127.0.0.1', port), timeout=.5):
                pass
        except OSError:
            closed = True
            break
        time.sleep(.25)
    out['portsClosed'][str(port)] = closed
assert all(out['portsClosed'].values())
checkpoint('api-ports-closed')
for service, identity in owned.items():
    current = json.loads(call(['docker', 'inspect', identity['id']]))[0]
    assert current['Id'] == known_ids[service] and current['Config']['Labels']['com.docker.compose.project'] == 'project-full-stack-cap-read'
    assert current['Name'] == f'/project-full-stack-cap-read-{service}-1'
out['containerStopOutput'] = call(['docker', 'stop', '-t', '60', owned['mysql']['id'], owned['redis']['id']], env=env)
checkpoint('owned-container-stop-complete')
out['containers'] = {}
for service, identity in owned.items():
    item = json.loads(call(['docker', 'inspect', identity['id']]))[0]
    assert not item['State']['Running'] and not item['State']['OOMKilled'] and item['State']['ExitCode'] == 0
    assert identity['volumes'] == [v['Name'] for v in item['Mounts'] if v['Type'] == 'volume']
    for volume in identity['volumes']:
        call(['docker', 'volume', 'inspect', volume])
    out['containers'][service] = {'id': identity['id'], 'running': False, 'oomKilled': False, 'exitCode': 0, 'volumes': identity['volumes']}
checkpoint('owned-containers-stopped-volumes-retained')
assert hashlib.sha256(manifest_path.read_bytes()).hexdigest() == metadata['fixtureManifestSHA256']
out['removedPrivateEnvironmentFiles'] = []
for private in (args.environment, args.original_environment):
    private.unlink()
    out['removedPrivateEnvironmentFiles'].append(private.name)
    checkpoint('private-environment-file-removed')
assert not args.environment.exists() and not args.original_environment.exists()
out['privateCredentialsRemoved'] = {'currentEnvironment': True, 'originalEnvironment': True, 'tokenInputAndLeaf': not credentials.parent.exists()}
assert out['privateCredentialsRemoved']['tokenInputAndLeaf']
out['complete'] = True
checkpoint('complete')
(base / 'cleanup.json').write_text(json.dumps(out, ensure_ascii=False, indent=2), encoding='utf-8')
print(json.dumps({'cleanupComplete': True, 'logoutCount': 32, 'remainingOwnSessions': 0, 'observerRemoved': True, 'portsClosed': out['portsClosed'], 'ownContainersStopped': True, 'volumesRetained': True, 'privateCredentialFilesRemoved': True}))
