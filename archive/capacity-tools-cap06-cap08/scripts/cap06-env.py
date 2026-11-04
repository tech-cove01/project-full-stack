"""Owned CAP-06 environment lifecycle; explicit actions, no implicit load.

prepare-deps replaces only the two handed-over, stopped CAP-read containers with
fresh random named volumes. All old volumes remain. Other actions require the
recorded immutable identities. Credentials remain in a private sibling runtime.
"""
import argparse
import datetime as dt
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import re
import secrets
import socket
import subprocess
import sys
import time
import urllib.request

import psutil


REPO = Path(__file__).resolve().parent.parent
COHORT = 'cap06'
KEY_PREFIX = 'CAP06'
TASK = 'CAP-06A'
ROOT = REPO / 'docs/evidence/2026-10-30-cap06'
PROJECT = 'project-full-stack-cap-read'
FLAGS = subprocess.CREATE_NO_WINDOW if os.name == 'nt' else 0
OPENER = urllib.request.build_opener(urllib.request.ProxyHandler({}))


# The CAP07/CAP08 adapters select a fixed cohort before parsing arguments. The
# default CAP06 CLI retains its original paths/guards; arbitrary roots and
# projects are deliberately not configurable.
def configure_cohort(cohort):
    global COHORT, KEY_PREFIX, TASK, ROOT
    if cohort not in ('cap06', 'cap07', 'cap08'):
        raise ValueError('unsupported fixed capacity cohort')
    COHORT = cohort
    KEY_PREFIX = cohort.upper()
    TASK = {'cap06': 'CAP-06A', 'cap07': 'CAP-07', 'cap08': 'CAP-08'}[cohort]
    ROOT = REPO / ('docs/evidence/2026-10-30-' + cohort)


def now():
    return dt.datetime.now(dt.timezone.utc).isoformat()


def read(path):
    return json.loads(Path(path).read_text(encoding='utf-8-sig'))


def save(path, value):
    with Path(path).open('x', encoding='utf-8') as stream:
        json.dump(value, stream, ensure_ascii=False, indent=2)


def run(command, *, env=None, input=None, allow_failure=False):
    result = subprocess.run(command, env=env, input=input, capture_output=True,
                            text=True, encoding='utf-8', errors='replace', creationflags=FLAGS)
    if result.returncode and not allow_failure:
        raise RuntimeError('owned command failed; diagnostics suppressed')
    return result


def compose(settings):
    return ['docker', 'compose', '-p', PROJECT, '-f', 'compose.go.yaml', '-f',
            'scripts/load/compose.capacity.yaml', '-f', 'scripts/load/compose.capacity-read.yaml',
            '-f', settings[KEY_PREFIX + '_VOLUME_OVERRIDE']]


def environment(settings):
    return {**os.environ, **{key: str(value) for key, value in settings.items()}}


def port_open(port):
    try:
        with socket.create_connection(('127.0.0.1', port), timeout=.3):
            return True
    except OSError:
        return False


def own_sessions(tokens):
    parts = ['EXISTS'] + ['login:token:' + token for token in tokens]
    message = b'*' + str(len(parts)).encode() + b'\r\n'
    for part in parts:
        raw = part.encode()
        message += b'$' + str(len(raw)).encode() + b'\r\n' + raw + b'\r\n'
    with socket.create_connection(('127.0.0.1', 6551), timeout=5) as connection:
        connection.sendall(message)
        response = b''
        while b'\r\n' not in response:
            chunk = connection.recv(256)
            if not chunk:
                raise RuntimeError('owned Redis session verification closed')
            response += chunk
        assert response.startswith(b':'), 'owned Redis integer response required'
        return int(response[1:].split(b'\r\n')[0])


def container(service, *, expected_id=None, running=None):
    value = json.loads(run(['docker', 'inspect', PROJECT + '-' + service + '-1']).stdout)[0]
    labels = value['Config']['Labels']
    assert labels.get('com.docker.compose.project') == PROJECT
    assert labels.get('com.docker.compose.service') == service
    if expected_id is not None:
        assert value['Id'] == expected_id, 'owned container identity changed'
    if running is not None:
        assert value['State']['Running'] is running
    target, port = ('3306/tcp', '3451') if service == 'mysql' else ('6379/tcp', '6551')
    assert any(x['HostIp'] == '127.0.0.1' and x['HostPort'] == port
               for x in value['HostConfig']['PortBindings'][target])
    return {'id': value['Id'], 'name': value['Name'], 'service': service, 'project': PROJECT,
            'image': value['Config']['Image'], 'imageId': value['Image'],
            'running': value['State']['Running'], 'status': value['State']['Status'],
            'exitCode': value['State']['ExitCode'], 'oomKilled': value['State']['OOMKilled'],
            'cpuNano': value['HostConfig']['NanoCpus'], 'memoryBytes': value['HostConfig']['Memory'],
            'ports': value['HostConfig']['PortBindings'],
            'volumes': [{'name': x['Name'], 'destination': x['Destination']}
                        for x in value['Mounts'] if x['Type'] == 'volume']}


def load_settings(path):
    path = Path(path).resolve()
    assert path.name.startswith('env') and path.name.endswith('.private.json')
    runtime = path.parent
    assert runtime.parent == REPO.parent and re.fullmatch(r'\.runtime-fullstack-' + re.escape(COHORT) + '-[a-f0-9]{12}', runtime.name)
    value = read(path)
    assert value[KEY_PREFIX + '_RUNTIME'] == str(runtime)
    assert value['MYSQL_HOST'] == value['REDIS_HOST'] == '127.0.0.1'
    assert value['MYSQL_PORT'] == '3451' and value['REDIS_PORT'] == '6551'
    assert value['API_BASE'] == 'http://127.0.0.1:8251' and value['ORDER_STREAM_WORKERS'] == '1'
    assert value[KEY_PREFIX + '_EVIDENCE'] == str(ROOT)
    return value, runtime


def get_json(url):
    with OPENER.open(url, timeout=3) as response:
        body = response.read(2 * 1024 * 1024 + 1)
        assert len(body) <= 2 * 1024 * 1024
        return json.loads(body)


def read_api_metadata(path):
    path = Path(path).resolve()
    value = read(path)
    if COHORT in ('cap07', 'cap08'):
        assert path.parent == ROOT and re.fullmatch(r'api-[a-z0-9-]+\.json', path.name)
        private = Path(value['privateEnvironmentFile']).resolve()
        assert private.parent.parent == REPO.parent
        assert re.fullmatch(r'\.runtime-fullstack-' + re.escape(COHORT) + '-[a-f0-9]{12}', private.parent.name)
        if COHORT == 'cap07':
            assert value['variant'] in ('A', 'B')
        else:
            assert value['phase'] in ('diagnostic', 'formal')
            assert value['tailEnabledRequested'] is (value['phase'] == 'diagnostic')
    return value


def api_identity(metadata, *, require_health=False):
    process = psutil.Process(metadata['pid'])
    assert os.path.normcase(process.exe()) == os.path.normcase(metadata['exe'])
    difference = abs(process.create_time() * 1000 - metadata['creationUnixMs'])
    assert difference <= 2000, 'owned API creation time mismatch'
    assert hashlib.sha256(Path(process.exe()).read_bytes()).hexdigest() == metadata['sha256']
    if require_health:
        health = get_json('http://127.0.0.1:8251/health/ready')
        assert health['version'] == metadata['expectedVersion'] and health['status'] == 'UP'
        assert get_json('http://127.0.0.1:8351/debug/capacity')['pid'] == process.pid
    return process, difference


def private_write(runtime, name, settings):
    path = runtime / name
    save(path, settings)
    if os.name != 'nt':
        path.chmod(0o600)
    # Private manifest is append-only by exclusive numbered entries.
    save(runtime / ('owned-file-' + secrets.token_hex(5) + '.json'), {'path': str(path)})
    return path


def prepare(args):
    assert not (ROOT / 'environment.json').exists(), 'preserve existing cohort environment'
    assert not any(port_open(p) for p in (3451, 6551, 8251, 8351)), 'dedicated ports already occupied'
    assert re.fullmatch(r'[a-f0-9]{64}', args.previous_mysql_id) and re.fullmatch(r'[a-f0-9]{64}', args.previous_redis_id)
    old = {'mysql': container('mysql', expected_id=args.previous_mysql_id, running=False),
           'redis': container('redis', expected_id=args.previous_redis_id, running=False)}
    assert all(x['status'] == 'exited' for x in old.values()), 'only handed-over stopped containers may be replaced'
    network = ipaddress.ip_network(args.subnet, strict=True)
    assert network.version == 4 and network.is_private and network.prefixlen == 24
    # Retain the existing dedicated network rather than reconfigure a global pool.
    net = json.loads(run(['docker', 'network', 'inspect', PROJECT + '_default']).stdout)[0]
    assert net['Labels']['com.docker.compose.project'] == PROJECT
    assert any(x.get('Subnet') == str(network) for x in net['IPAM']['Config']), 'explicit subnet must match retained owned network'
    nonce = secrets.token_hex(6)
    runtime = REPO.parent / ('.runtime-fullstack-' + COHORT + '-' + nonce)
    runtime.mkdir()
    if os.name == 'nt':
        # Do not assume Python mode0600 creates a Windows-only owner ACL.
        identity = run(['whoami']).stdout.strip()
        run(['icacls', str(runtime), '/inheritance:r', '/grant:r', identity + ':(OI)(CI)F'])
    else:
        runtime.chmod(0o700)
    (runtime / 'tmp').mkdir()
    ROOT.mkdir(parents=True, exist_ok=True)
    volumes = {s: PROJECT + '-' + COHORT + '-' + nonce + '-' + s for s in ('mysql', 'redis')}
    for value in volumes.values():
        assert run(['docker', 'volume', 'inspect', value], allow_failure=True).returncode != 0, 'new random volume already exists'
    override = runtime / 'fresh-volumes.yaml'
    override.write_text('volumes:\n  go-mysql-data:\n    name: ' + volumes['mysql'] + '\n  go-redis-data:\n    name: ' + volumes['redis'] + '\n', encoding='utf-8')
    settings = {'GO_MYSQL_PORT': '3451', 'GO_REDIS_PORT': '6551', 'CAP_READ_SUBNET': str(network),
                'MYSQL_HOST': '127.0.0.1', 'MYSQL_PORT': '3451', 'MYSQL_DATABASE': 'project_full_stack_go',
                'MYSQL_USER': 'fullstack', 'MYSQL_PASSWORD': 'fullstack_go_dev_only',
                'REDIS_HOST': '127.0.0.1', 'REDIS_PORT': '6551', 'REDIS_DATABASE': '0', 'REDIS_PASSWORD': '',
                'API_BASE': 'http://127.0.0.1:8251', 'GO_ENV': 'development', 'API_ADDR': '127.0.0.1:8251',
                'GO_DIAGNOSTICS_ADDR': '127.0.0.1:8351', 'GOMAXPROCS': '4', 'GOMEMLIMIT': '1GiB',
                'ORDER_STREAM_WORKERS': '1', 'KAFKA_BROKERS': '', 'ES_URL': '', 'AI_ENABLED': 'false',
                'TEMP': str(runtime / 'tmp'), 'TMP': str(runtime / 'tmp'),
                'CAP_READ_OBSERVER_USER': COHORT + '_' + nonce, 'CAP_READ_OBSERVER_PASSWORD': secrets.token_hex(24),
                KEY_PREFIX + '_RUNTIME': str(runtime), KEY_PREFIX + '_EVIDENCE': str(ROOT), KEY_PREFIX + '_VOLUME_OVERRIDE': str(override)}
    if args.mysql_bin:
        settings['MYSQL_BIN'] = str(Path(args.mysql_bin).resolve())
    settings_path = private_write(runtime, 'env.private.json', settings)
    save(ROOT / 'preparation.json', {'at': now(), 'task': TASK, 'runtime': str(runtime),
         'privateEnvironmentFile': str(settings_path), 'oldContainers': old, 'newVolumes': volumes,
         'retainedNetworkId': net['Id'], 'subnet': str(network), 'credentialValuesExported': False})
    env = environment(settings)
    assert run(compose(settings) + ['config', '--quiet'], env=env).returncode == 0
    for service in ('mysql', 'redis'):
        container(service, expected_id=old[service]['id'], running=False)
    assert not any(port_open(p) for p in (3451, 6551, 8251, 8351))
    result = run(compose(settings) + ['up', '-d', '--wait', '--force-recreate', 'mysql', 'redis'], env=env, allow_failure=True)
    (ROOT / 'compose-start.txt').write_text(result.stdout + result.stderr, encoding='utf-8')
    assert result.returncode == 0, 'preserve failed dependency setup; do not start API'
    owned = {s: container(s, running=True) for s in ('mysql', 'redis')}
    for service, destination in [('mysql', '/var/lib/mysql'), ('redis', '/data')]:
        assert any(v['name'] == volumes[service] and v['destination'] == destination for v in owned[service]['volumes'])
    for old_container in old.values():
        for volume in old_container['volumes']:
            run(['docker', 'volume', 'inspect', volume['name']])
    docker_host = json.loads(run(['docker', 'info', '--format', '{{json .}}']).stdout)
    versions = {}
    for key, command in [('go', ['go', 'version']), ('node', ['node', '--version']), ('k6', ['k6', 'version'])]:
        versions[key] = run(command).stdout.strip()
    save(ROOT / 'environment.json', {'task': TASK, 'at': now(), 'project': PROJECT,
         'runtime': str(runtime), 'privateEnvironmentFile': str(settings_path), 'containers': owned,
         'oldContainers': old, 'oldVolumesRetained': True, 'newVolumes': volumes,
         'subnet': str(network), 'sharedHost': True,
         'host': {'platform': sys.platform, 'physicalCores': psutil.cpu_count(logical=False),
                  'logicalCpus': psutil.cpu_count(), 'memoryBytes': psutil.virtual_memory().total},
         'docker': {'serverVersion': docker_host['ServerVersion'], 'cpus': docker_host['NCPU'], 'memoryBytes': docker_host['MemTotal']},
         'toolVersions': versions,
         'runningContainers': run(['docker', 'ps', '--format', '{{.ID}}|{{.Names}}|{{.Status}}']).stdout.strip().splitlines(),
         'limits': 'No API or workload started by prepare-deps.'})
    migration = run(['node', 'scripts/migrate.mjs'], env=env, allow_failure=True)
    (ROOT / 'migrate.txt').write_text(migration.stdout + migration.stderr, encoding='utf-8')
    save(ROOT / 'migrate-exit.json', {'at': now(), 'exitCode': migration.returncode})
    assert migration.returncode == 0
    observer = settings['CAP_READ_OBSERVER_USER']
    password = settings['CAP_READ_OBSERVER_PASSWORD']
    sql = f"CREATE USER '{observer}'@'%' IDENTIFIED BY '{password}'; GRANT SELECT ON project_full_stack_go.* TO '{observer}'@'%'; GRANT SELECT ON performance_schema.* TO '{observer}'@'%';\n"
    run(['docker', 'exec', '-i', owned['mysql']['id'], 'sh', '-c', 'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" exec mysql -uroot --default-character-set=utf8mb4'], input=sql)
    save(ROOT / 'observer-setup.json', {'at': now(), 'user': observer, 'privileges': ['SELECT project_full_stack_go.*', 'SELECT performance_schema.*'], 'passwordExported': False})
    print(json.dumps({'preparedDependencies': True, 'privateEnvironmentFile': str(settings_path), 'freshVolumes': volumes, 'apiStarted': False}))


def start_api(args):
    settings, runtime = load_settings(args.environment)
    label = args.label
    assert re.fullmatch(r'[a-z0-9][a-z0-9-]{1,30}', label)
    assert re.fullmatch(r'[a-f0-9]{40}', args.source_sha)
    variant = getattr(args, 'variant', None)
    assert (COHORT == 'cap07' and variant in ('A', 'B')) or (COHORT in ('cap06', 'cap08') and variant is None), 'variant is required only for CAP07'
    assert not any(port_open(p) for p in (8251, 8351)), 'stop only the previous guarded API before replacement'
    owned = read(ROOT / 'environment.json')['containers']
    for service in ('mysql', 'redis'):
        container(service, expected_id=owned[service]['id'], running=True)
    binary = Path(args.binary).resolve()
    info = run(['go', 'version', '-m', str(binary)]).stdout
    assert 'vcs.revision=' + args.source_sha in info and 'vcs.modified=false' in info, 'fixed clean source build required'
    version = 'capacity-' + args.source_sha
    current = {**settings, 'CAP_SOURCE_SHA': args.source_sha, 'CAP_READ_EXPECTED_REVISION': version,
               'GO_FEED_DIAGNOSTICS': 'true' if args.tail else 'false'}
    if COHORT == 'cap07':
        assert not args.tail, 'CAP07 formal experiments keep heavy Feed diagnostics disabled'
        current['CAP07_VARIANT'] = variant
    if COHORT == 'cap08':
        assert args.phase in ('diagnostic', 'formal')
        assert args.tail is (args.phase == 'diagnostic'), 'CAP08 diagnostic/formal phase must match the explicit tail switch'
        current['CAP08_PHASE'] = args.phase
    if current.get('CAP_READ_FIXTURE'):
        manifest = read(current['CAP_READ_FIXTURE'])
        current['CAP_READ_FIXTURE_SOURCE_SHA'] = manifest['sourceSha']
    path = private_write(runtime, 'env-' + label + '.private.json', current)
    stdout, stderr = ROOT / ('api-' + label + '.stdout.txt'), ROOT / ('api-' + label + '.stderr.txt')
    assert not stdout.exists() and not stderr.exists() and not (ROOT / ('api-' + label + '.json')).exists()
    with stdout.open('xb') as out, stderr.open('xb') as error:
        child = subprocess.Popen([str(binary)], cwd=REPO, env=environment(current), stdout=out, stderr=error, creationflags=FLAGS)
    process = psutil.Process(child.pid)
    metadata = {'pid': process.pid, 'exe': str(binary), 'creationUnixMs': process.create_time() * 1000,
                'createdAtUtc': dt.datetime.fromtimestamp(process.create_time(), dt.timezone.utc).isoformat(),
                'sourceSha': args.source_sha, 'expectedVersion': version, 'sha256': hashlib.sha256(binary.read_bytes()).hexdigest(),
                'privateEnvironmentFile': str(path), 'label': label, 'tailEnabledRequested': args.tail}
    if COHORT == 'cap07':
        metadata['variant'] = variant
    if COHORT == 'cap08':
        metadata['phase'] = args.phase
    save(ROOT / ('api-' + label + '.json'), metadata)
    (ROOT / ('api-' + label + '-build.txt')).write_text(info, encoding='utf-8')
    ready = False
    for _ in range(80):
        if child.poll() is not None:
            break
        try:
            api_identity(metadata, require_health=True)
            ready = True
            break
        except (OSError, ValueError, AssertionError, psutil.Error):
            time.sleep(.25)
    save(ROOT / ('api-' + label + '-health.json'), {'at': now(), 'ready': ready,
         'readyBody': get_json('http://127.0.0.1:8251/health/ready') if ready else None,
         'capacityBody': get_json('http://127.0.0.1:8351/debug/capacity') if ready else None})
    assert ready, 'API did not become ready; preserve exact recorded identity and output'
    print(json.dumps({'apiStarted': True, 'pid': process.pid, 'metadata': str(ROOT / ('api-' + label + '.json')), 'privateEnvironmentFile': str(path)}))


def fixture(args):
    settings, runtime = load_settings(args.environment)
    metadata = read_api_metadata(args.api_metadata)
    api_identity(metadata, require_health=True)
    assert metadata['sourceSha'] == settings['CAP_SOURCE_SHA']
    assert 'CAP_READ_FIXTURE' not in settings, 'new fixture only; preserve existing manifest'
    current = {**settings, 'CAP_READ_FIXTURE_DIR': str(ROOT / 'fixture')}
    result = run(['node', 'scripts/go073-cap-read.mjs', 'prepare'], env=environment(current), allow_failure=True)
    (ROOT / 'fixture-prepare.txt').write_text(result.stdout + result.stderr, encoding='utf-8')
    save(ROOT / 'fixture-prepare-exit.json', {'at': now(), 'exitCode': result.returncode})
    assert result.returncode == 0, 'partial fixture is recoverable; preserve its private input'
    manifest = ROOT / 'fixture/fixture.json'
    current.update(CAP_READ_FIXTURE=str(manifest), CAP_READ_FIXTURE_SOURCE_SHA=settings['CAP_SOURCE_SHA'])
    path = private_write(runtime, 'env-' + metadata['label'] + '-fixture.private.json', current)
    save(ROOT / 'fixture-provenance.json', {'at': now(), 'fixture': str(manifest), 'manifestSHA256': hashlib.sha256(manifest.read_bytes()).hexdigest(),
         'fixtureSourceSha': settings['CAP_SOURCE_SHA'], 'privateEnvironmentFile': str(path), 'readers': 32, 'notes': 6400})
    print(json.dumps({'preparedFixture': True, 'privateEnvironmentFile': str(path), 'manifest': str(manifest)}))


def stop_api(args):
    metadata = read_api_metadata(args.api_metadata)
    process, difference = api_identity(metadata)
    process.terminate()
    code = process.wait(timeout=15)
    save(ROOT / ('api-' + metadata['label'] + '-stop.json'), {'at': now(), 'pid': metadata['pid'], 'exeMatched': True,
         'creationTimeDifferenceMs': difference, 'binaryHashMatched': True, 'waitExitCode': code,
         'method': 'Windows native terminate' if os.name == 'nt' else 'terminate owned process', 'goGracefulShutdownClaimed': False})
    assert not any(port_open(p) for p in (8251, 8351))
    print(json.dumps({'stoppedOwnedApi': metadata['pid'], 'volumesChanged': False}))


def finish(args):
    settings, runtime = load_settings(args.environment)
    metadata = read_api_metadata(args.api_metadata)
    if COHORT in ('cap07', 'cap08'):
        assert not (runtime / (COHORT + '-stage-active.private.json')).exists(), 'review active/crashed stage identity before cleanup'
    owned = read(ROOT / 'environment.json')['containers']
    api_identity(metadata, require_health=True)
    assert metadata['sourceSha'] == settings['CAP_SOURCE_SHA']
    for service in ('mysql', 'redis'):
        container(service, expected_id=owned[service]['id'], running=True)
    for path in ROOT.glob('*-load/stable-process.json'):
        detail = read(path)
        try:
            process = psutil.Process(detail['k6_pid'])
            assert not (process.is_running() and abs(process.create_time() * 1000 - detail['k6_started_unix_ms']) <= 2000), 'owned stable generator still active'
        except psutil.NoSuchProcess:
            pass
    # Preserve progress before any mutation, including a failure in a later step.
    progress = ROOT / 'cleanup-progress.json'
    assert not progress.exists(), 'inspect preserved partial cleanup before choosing a recovery step'
    record = {'at': now(), 'complete': False, 'steps': [], 'exceptions': [], 'volumesRetained': True}
    def checkpoint(step):
        record['steps'].append({'step': step, 'at': now()})
        temporary = progress.with_suffix('.pending')
        temporary.write_text(json.dumps(record, ensure_ascii=False, indent=2), encoding='utf-8')
        temporary.replace(progress)
    checkpoint('identity_preflight_passed')
    try:
        if settings.get('CAP_READ_FIXTURE'):
            manifest = Path(settings['CAP_READ_FIXTURE']).resolve()
            assert manifest == ROOT / 'fixture/fixture.json'
            credentials = Path(read(manifest)['credentialFile']).resolve()
            assert credentials.is_relative_to(runtime / 'tmp') and credentials.name == 'input.json'
            tokens = [reader['token'] for reader in read(credentials)['readers']]
            assert len(tokens) == len(set(tokens)) == 32
            record['sessionsBeforeLogout'] = own_sessions(tokens)
            result = run(['node', 'scripts/go073-cap-read.mjs', 'finish'], env=environment(settings))
            record['finishOutput'] = json.loads(result.stdout)
            record['sessionsAfterLogout'] = own_sessions(tokens)
            assert record['sessionsAfterLogout'] == 0 and record['finishOutput']['readersLoggedOut'] == 32 and not credentials.exists()
            checkpoint('normal_reader_logout_and_private_input_removal')
        observer = settings['CAP_READ_OBSERVER_USER']
        assert re.fullmatch(re.escape(COHORT) + '_[a-f0-9]{12}', observer)
        assert observer.endswith(runtime.name[-12:])
        sql = f"DROP USER '{observer}'@'%'; SELECT COUNT(*) FROM mysql.user WHERE User='{observer}' AND Host='%';\n"
        result = run(['docker', 'exec', '-i', owned['mysql']['id'], 'sh', '-c', 'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" exec mysql -uroot --batch --skip-column-names'], input=sql)
        assert result.stdout.strip() == '0'
        checkpoint('owned_observer_removed_and_verified')
        process, difference = api_identity(metadata)
        process.terminate()
        record['apiStop'] = {'pid': metadata['pid'], 'creationTimeDifferenceMs': difference,
             'waitExitCode': process.wait(timeout=15), 'goGracefulShutdownClaimed': False}
        assert not any(port_open(p) for p in (8251, 8351))
        checkpoint('owned_api_stopped')
        for service in ('mysql', 'redis'):
            container(service, expected_id=owned[service]['id'], running=True)
        run(['docker', 'stop', '--time', '60', owned['mysql']['id'], owned['redis']['id']])
        record['containers'] = {}
        for service in ('mysql', 'redis'):
            stopped = container(service, expected_id=owned[service]['id'], running=False)
            assert stopped['exitCode'] == 0 and not stopped['oomKilled'] and stopped['volumes'] == owned[service]['volumes']
            for volume in stopped['volumes']:
                run(['docker', 'volume', 'inspect', volume['name']])
            record['containers'][service] = stopped
        checkpoint('exact_container_ids_stopped_and_volumes_retained')
        removed = []
        for entry in runtime.glob('owned-file-*.json'):
            private = Path(read(entry)['path']).resolve()
            assert private.parent == runtime and private.name.startswith('env') and private.name.endswith('.private.json')
            if private.exists():
                private.unlink()
                removed.append(private.name)
        record['removedPrivateEnvironmentFiles'] = removed
        checkpoint('exact_owned_private_environment_files_removed')
        record['complete'] = True
        checkpoint('complete')
        save(ROOT / 'cleanup.json', record)
        print(json.dumps({'cleanupComplete': True, 'ownApiStopped': True, 'ownContainersStopped': True, 'volumesRetained': True}))
    except Exception as error:
        record['exceptions'].append({'at': now(), 'class': type(error).__name__, 'detailsSuppressed': True})
        checkpoint('failed_preserve_partial_progress')
        raise


def main():
    assert Path.cwd().resolve() == REPO, 'run from repository root'
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest='action', required=True)
    p = sub.add_parser('prepare-deps'); p.add_argument('--subnet', required=True); p.add_argument('--mysql-bin')
    p.add_argument('--previous-mysql-id', required=True); p.add_argument('--previous-redis-id', required=True)
    p = sub.add_parser('start-api'); p.add_argument('--environment', required=True); p.add_argument('--binary', required=True)
    p.add_argument('--source-sha', required=True); p.add_argument('--label', required=True); p.add_argument('--tail', action='store_true')
    if COHORT == 'cap07':
        p.add_argument('--variant', choices=('A', 'B'), required=True)
    if COHORT == 'cap08':
        p.add_argument('--phase', choices=('diagnostic', 'formal'), required=True)
    p = sub.add_parser('prepare-fixture'); p.add_argument('--environment', required=True); p.add_argument('--api-metadata', required=True)
    p = sub.add_parser('stop-api'); p.add_argument('--api-metadata', required=True)
    p = sub.add_parser('finish'); p.add_argument('--environment', required=True); p.add_argument('--api-metadata', required=True)
    args = parser.parse_args()
    {'prepare-deps': prepare, 'start-api': start_api, 'prepare-fixture': fixture, 'stop-api': stop_api, 'finish': finish}[args.action](args)


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        print('cap06-env: ' + type(error).__name__ + '; details suppressed; preserve selected public progress and private recovery files')
        raise SystemExit(1)
