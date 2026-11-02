"""Fixed, owned CAP10 environment lifecycle. No implicit load or global cleanup."""
import argparse
import ctypes
import datetime as dt
import hashlib
import importlib.util
import ipaddress
import json
import math
import os
from pathlib import Path
import re
import secrets
import socket
import subprocess
import sys
import time
import urllib.request
import xml.etree.ElementTree as ET

import psutil


REPO = Path(__file__).resolve().parent.parent
_cohort_spec = importlib.util.spec_from_file_location('cap10_cohort_environment', REPO / 'scripts/cap10-cohort.py')
COHORT = importlib.util.module_from_spec(_cohort_spec)
_cohort_spec.loader.exec_module(COHORT)
BATCH_ROOT = REPO / 'docs/evidence/2026-11-01-cap10'
PLATFORM = 'windows' if os.name == 'nt' else 'linux'
assert os.name == 'nt' or sys.platform == 'linux', 'CAP10 supports Windows and the single Linux transition only'
assert os.environ.get('CAP10_PLATFORM', PLATFORM) == PLATFORM
ROOT = BATCH_ROOT / 'linux' if PLATFORM == 'linux' else BATCH_ROOT
PROJECT = 'project-full-stack-cap10'
PORTS = {'mysql': (3481, '3306/tcp'), 'redis': (6581, '6379/tcp'),
         'kafka': (9401, '9092/tcp'), 'elasticsearch': (9281, '9200/tcp')}
LIMITS = {'mysql': (4_000_000_000, 2 << 30), 'redis': (2_000_000_000, 512 << 20),
          'kafka': (2_000_000_000, 1 << 30), 'elasticsearch': (2_000_000_000, 1 << 30)}
MOUNTS = {'mysql': '/var/lib/mysql', 'redis': '/data', 'kafka': '/var/lib/kafka/data',
          'elasticsearch': '/usr/share/elasticsearch/data'}
VOLUME_KEYS = {'mysql': 'go-mysql-data', 'redis': 'go-redis-data',
               'kafka': 'go-kafka-data', 'elasticsearch': 'go-search-data'}
FLAGS = subprocess.CREATE_NO_WINDOW if os.name == 'nt' else 0
class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, file, code, message, headers, url):
        return None


OPENER = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())


def now():
    return dt.datetime.now(dt.timezone.utc).isoformat()


def read(path):
    return json.loads(Path(path).read_text(encoding='utf-8-sig'))


def save(path, value):
    with Path(path).open('x', encoding='utf-8', newline='\n') as stream:
        json.dump(value, stream, ensure_ascii=False, indent=2)


def run(argv, *, env=None, input=None, allow_failure=False, timeout=180):
    result = subprocess.run(argv, cwd=REPO, env=env, input=input, capture_output=True,
                            text=True, encoding='utf-8', errors='replace',
                            creationflags=FLAGS, timeout=timeout)
    if result.returncode and not allow_failure:
        raise RuntimeError('owned command failed; details suppressed')
    return result


def env(settings):
    return {**os.environ, **{key: str(value) for key, value in settings.items()}}


def port_open(port):
    with socket.socket() as connection:
        connection.settimeout(.25)
        return connection.connect_ex(('127.0.0.1', port)) == 0


def clock_sample():
    first = time.monotonic()
    wall = time.time()
    boot_time = time.clock_gettime(time.CLOCK_BOOTTIME) if PLATFORM == 'linux' else None
    last = time.monotonic()
    boot_id = Path('/proc/sys/kernel/random/boot_id').read_text().strip() if PLATFORM == 'linux' else None
    return {'wallUnixSeconds': wall, 'monotonicSeconds': (first + last) / 2,
            'boottimeSeconds': boot_time, 'bootId': boot_id, 'uncertaintySeconds': (last - first) / 2}


def power():
    class Status(ctypes.Structure):
        _fields_ = [('ACLineStatus', ctypes.c_ubyte), ('BatteryFlag', ctypes.c_ubyte),
                    ('BatteryLifePercent', ctypes.c_ubyte), ('SystemStatusFlag', ctypes.c_ubyte),
                    ('BatteryLifeTime', ctypes.c_ulong), ('BatteryFullLifeTime', ctypes.c_ulong)]
    result = {'at': now(), 'hostMemoryPercent': psutil.virtual_memory().percent,
              'platform': PLATFORM, 'clock': clock_sample()}
    if os.name == 'nt':
        status = Status()
        assert ctypes.windll.kernel32.GetSystemPowerStatus(ctypes.byref(status))
        result.update(ac=status.ACLineStatus, batteryPercent=status.BatteryLifePercent,
                      batterySaver=status.SystemStatusFlag)
    else:
        supplies = Path('/sys/class/power_supply')
        entries = [{'name': p.name, 'type': (p / 'type').read_text().strip()} for p in supplies.iterdir()]
        result.update(ac=None, batterySaver=None, batteryApplicable=any(e['type'] == 'Battery' for e in entries),
                      powerSupplyDirectoryPresent=supplies.is_dir(), powerSupplyEntries=entries,
                      powerCheck='server_without_battery; physical AC/battery saver are not applicable')
    return result


def clock_valid(clock, linux=False):
    required = ('wallUnixSeconds', 'monotonicSeconds', 'uncertaintySeconds') + (('boottimeSeconds',) if linux else ())
    return (all(type(clock.get(k)) in (int, float) and math.isfinite(clock[k]) for k in required) and
            clock['wallUnixSeconds'] > 0 and clock['monotonicSeconds'] >= 0 and
            0 <= clock['uncertaintySeconds'] <= 1 and
            (not linux or (clock['boottimeSeconds'] + clock['uncertaintySeconds'] + .001 >= clock['monotonicSeconds'] and
             bool(re.fullmatch(r'[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}', clock.get('bootId', ''))))))


def power_valid(value):
    if value.get('platform', 'windows') == 'windows':
        return value.get('ac') == 1 and value.get('batterySaver') == 0
    clock = value.get('clock', {})
    return (value.get('platform') == 'linux' and value.get('batteryApplicable') is False and
            value.get('powerSupplyDirectoryPresent') is True and value.get('ac') is None and
            value.get('batterySaver') is None and clock_valid(clock, linux=True))


def power_window_evidence(before, after, since, lightweight=False):
    valid = power_valid(before) and power_valid(after)
    events = []
    left, right = before.get('clock', {}), after.get('clock', {})
    same_platform = before.get('platform', 'windows') == after.get('platform', 'windows') == PLATFORM
    linux = after.get('platform') == 'linux'
    clock_ok = same_platform and clock_valid(left, linux) and clock_valid(right, linux)
    clock_available = clock_ok
    comparison = {'available': clock_ok, 'toleranceSeconds': None}
    if clock_ok:
        tolerance = .25 + left['uncertaintySeconds'] + right['uncertaintySeconds']
        mono = right['monotonicSeconds'] - left['monotonicSeconds']
        wall = right['wallUnixSeconds'] - left['wallUnixSeconds']
        comparison.update(monotonicSeconds=mono, wallSeconds=wall, toleranceSeconds=tolerance)
        clock_ok = mono >= 0 and abs(wall - mono) <= tolerance
        if linux:
            boot = right['boottimeSeconds'] - left['boottimeSeconds']
            comparison.update(boottimeSeconds=boot, bootIdUnchanged=left['bootId'] == right['bootId'])
            clock_ok = clock_ok and left['bootId'] == right['bootId'] and abs(boot - mono) <= tolerance
        if not clock_ok:
            events.append({'kind': 'clock_or_suspend_disagreement'})
    available = clock_available
    source = 'Linux boot_id and BOOTTIME/MONOTONIC/UTC snapshots' if PLATFORM == 'linux' else 'Windows power and clock snapshots'
    if PLATFORM == 'windows' and not lightweight:
        dt.datetime.fromisoformat(since.replace('Z', '+00:00'))
        query = "*[System[Provider[@Name='Microsoft-Windows-Kernel-Power'] and (EventID=506 or EventID=507) and TimeCreated[@SystemTime >= '" + since.replace('+00:00', 'Z') + "']]]"
        result = subprocess.run(['wevtutil', 'qe', 'System', '/q:' + query, '/f:xml', '/rd:true', '/c:32', '/e:Events'],
                                capture_output=True, creationflags=FLAGS, timeout=15)
        available = result.returncode == 0 and clock_available
        if result.returncode == 0 and result.stdout.strip():
            body = result.stdout.decode('utf-16' if result.stdout.startswith(b'\xff\xfe') else 'utf-8-sig')
            ns = {'e': 'http://schemas.microsoft.com/win/2004/08/events/event'}
            for event in ET.fromstring(body).findall('e:Event', ns):
                system = event.find('e:System', ns)
                events.append({'id': int(system.find('e:EventID', ns).text),
                               'at': system.find('e:TimeCreated', ns).attrib['SystemTime']})
        source += ' and Kernel-Power event query'
    return {'valid': bool(valid and clock_ok and available and not events), 'available': available,
            'events': events, 'source': source, 'clockComparison': comparison,
            'journalEventsAvailable': False,
            'limitations': 'No Linux journal events collected. Snapshot comparisons do not prove absence of hypervisor pauses; Windows lightweight calls defer event query until final.'}


def transition():
    if PLATFORM == 'windows':
        return None
    path = BATCH_ROOT / 'environment-transition.json'
    value = read(path)
    assert value['batch'] == 'CAP-10' and type(value['version']) is type(value['switchCount']) is int
    assert value['version'] == value['switchCount'] == 1 and value['authorized'] is True
    assert value['fromPlatform'] == 'windows' and value['toPlatform'] == 'linux'
    assert value['userAuthorizationReference'] and value['hostLabel']
    assert re.fullmatch(r'[a-f0-9]{40}', value['localCheckpointSHA'])
    assert dt.datetime.fromisoformat(value['recordedAt'].replace('Z', '+00:00')).tzinfo is not None
    for key, filename in [('localEnvironmentSHA256', 'environment.json'), ('localIdleResultSHA256', 'idle-result.json'),
                          ('localCleanupSHA256', 'cleanup.json'), ('localRawManifestSHA256', 'raw-manifest.json'),
                          ('budgetHandoffSHA256', 'budget-handoff.json')]:
        assert value[key] == hashlib.sha256((BATCH_ROOT / filename).read_bytes()).hexdigest()
    assert not read(BATCH_ROOT / 'idle-result.json')['passed'] and read(BATCH_ROOT / 'cleanup.json')['complete']
    assert not list(BATCH_ROOT.glob('slot-*.json')) and not list(BATCH_ROOT.glob('*-command.json'))
    for key in ('shortSlotsUsedBeforeSwitch', 'formalSlotsUsedBeforeSwitch', 'optimizationsUsedBeforeSwitch', 'mainLoadSecondsUsedBeforeSwitch'):
        assert type(value[key]) is int and value[key] == 0
    for key, expected in [('maxShortWindows', 8), ('maxFormalWindows', 2), ('maxMainLoadSeconds', 840),
                          ('maxOptimizations', 1), ('effectiveWorkSecondsMaximum', 10800)]:
        assert type(value[key]) is int and value[key] == expected
    used, remaining = value['effectiveWorkSecondsUsedBeforeSwitch'], value['effectiveWorkSecondsRemaining']
    handoff = read(BATCH_ROOT / 'budget-handoff.json')
    counted = handoff['countedEffectiveWorkSecondsUpperBound']
    remaining_handoff = handoff['remainingEffectiveWorkSecondsLowerBound']
    assert handoff['maxEffectiveWorkSeconds'] == 10800 and type(counted) is type(remaining_handoff) is int
    assert counted >= 4200 and remaining_handoff >= 0 and counted + remaining_handoff == 10800
    assert type(used) is int and type(remaining) is int and used >= counted
    assert remaining > 0 and used + remaining == 10800
    return hashlib.sha256(path.read_bytes()).hexdigest()


def private_write(runtime, name, value):
    assert re.fullmatch(r'env[a-z0-9-]*\.private\.json', name)
    path = runtime / name
    save(runtime / ('owned-env-' + secrets.token_hex(5) + '.json'), {'path': str(path)})
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, 'O_BINARY', 0), 0o600)
    with os.fdopen(descriptor, 'w', encoding='utf-8', newline='\n') as stream:
        json.dump(value, stream, ensure_ascii=False, indent=2)
    return path


def settings(path):
    path = Path(path).resolve()
    assert re.fullmatch(r'env[a-z0-9-]*\.private\.json', path.name)
    runtime = path.parent
    assert runtime.parent == REPO.parent and re.fullmatch(r'\.runtime-fullstack-cap10-[a-f0-9]{12}', runtime.name)
    value = read(path)
    assert value['CAP10_RUNTIME'] == str(runtime) and value['CAP10_EVIDENCE'] == str(ROOT)
    assert value['CAP_COHORT'] == 'cap10' and value['MYSQL_HOST'] == value['REDIS_HOST'] == '127.0.0.1'
    assert value['MYSQL_PORT'] == '3481' and value['REDIS_PORT'] == '6581'
    assert value['API_BASE'] == 'http://127.0.0.1:8281'
    assert value['KAFKA_BROKERS'] == '127.0.0.1:9401' and value['ES_URL'] == 'http://127.0.0.1:9281'
    assert value['CAP10_ENVIRONMENT_METADATA'] == str(ROOT / 'environment.json')
    assert value.get('CAP10_PLATFORM', 'windows') == PLATFORM
    if PLATFORM == 'linux':
        assert value['CAP10_TRANSITION_SHA256'] == transition()
    return value, runtime


def container(service, cid=None, running=None):
    assert service in PORTS
    if cid:
        assert re.fullmatch(r'[a-f0-9]{64}', cid)
    raw = json.loads(run(['docker', 'inspect', cid or PROJECT + '-' + service + '-1']).stdout)[0]
    assert raw['Config']['Labels']['com.docker.compose.project'] == PROJECT
    assert raw['Config']['Labels']['com.docker.compose.service'] == service
    if cid:
        assert raw['Id'] == cid
    if running is not None:
        assert raw['State']['Running'] is running
    port, target = PORTS[service]
    assert any(x['HostIp'] == '127.0.0.1' and x['HostPort'] == str(port)
               for x in raw['HostConfig']['PortBindings'][target])
    assert (raw['HostConfig']['NanoCpus'], raw['HostConfig']['Memory']) == LIMITS[service]
    volumes = sorted(({'name': item['Name'], 'destination': item['Destination']}
                      for item in raw['Mounts'] if item['Type'] == 'volume'), key=lambda item: item['destination'])
    primary = [item for item in volumes if item['destination'] == MOUNTS[service]]
    assert len(primary) == 1 and re.fullmatch(PROJECT + r'-[a-f0-9]{12}-' + service, primary[0]['name'])
    if service == 'kafka':
        assert len(raw['Mounts']) == 3 and all(item['Type'] == 'volume' for item in raw['Mounts'])
        assert len(volumes) == 3 and len({item['name'] for item in volumes}) == 3
        ancillary = [item for item in volumes if item['destination'] != MOUNTS[service]]
        assert {item['destination'] for item in ancillary} == {'/etc/kafka/secrets', '/mnt/shared/config'}
        assert all(re.fullmatch(r'[a-f0-9]{64}', item['name']) for item in ancillary)
    else:
        assert len(volumes) == 1
    return {'id': raw['Id'], 'name': raw['Name'], 'project': PROJECT, 'service': service,
            'image': raw['Config']['Image'], 'imageId': raw['Image'],
            'running': raw['State']['Running'], 'status': raw['State']['Status'],
            'healthStatus': raw['State'].get('Health', {}).get('Status'),
            'exitCode': raw['State']['ExitCode'], 'oomKilled': raw['State']['OOMKilled'],
            'restartCount': raw['RestartCount'], 'cpuNano': raw['HostConfig']['NanoCpus'],
            'memoryBytes': raw['HostConfig']['Memory'], 'ports': raw['HostConfig']['PortBindings'],
            'volumes': volumes, 'networkId': raw['NetworkSettings']['Networks'][PROJECT + '_default']['NetworkID']}


def get_json(url):
    with OPENER.open(url, timeout=5) as response:
        raw = response.read((2 << 20) + 1)
        assert len(raw) <= 2 << 20
        return json.loads(raw)


def api_identity(metadata, health=False):
    assert metadata['cohort'] == 'cap10'
    assert metadata.get('executionPlatform', 'windows') == PLATFORM
    if PLATFORM == 'linux':
        assert metadata['transitionSHA256'] == transition()
    private, _ = settings(metadata['privateEnvironmentFile'])
    assert private['CAP_SOURCE_SHA'] == metadata['sourceSha']
    assert private['CAP_READ_EXPECTED_REVISION'] == metadata['expectedVersion']
    process = psutil.Process(metadata['pid'])
    assert abs(process.create_time() * 1000 - metadata['creationUnixMs']) <= 2
    assert Path(process.exe()).resolve() == Path(metadata['exe']).resolve()
    assert hashlib.sha256(Path(process.exe()).read_bytes()).hexdigest() == metadata['sha256']
    if health:
        body = get_json('http://127.0.0.1:8281/health/ready')
        # The old app readiness covers SQL/Redis only. Kafka/ES are checked independently.
        assert body['version'] == metadata['expectedVersion']
        assert get_json('http://127.0.0.1:8381/debug/capacity')['pid'] == process.pid
    return process


def compose(value):
    return ['docker', 'compose', '-p', PROJECT, '-f', 'compose.go.yaml', '-f',
            'scripts/load/compose.capacity.yaml', '-f', value['CAP10_OVERRIDE'],
            '--profile', 'kafka', '--profile', 'search']


def prepare(args):
    transition_sha = transition()
    ROOT.mkdir(exist_ok=True)
    assert not (ROOT / 'preparation.json').exists(), 'preserve partial setup and recovery inputs'
    assert not any(port_open(p) for p in (3481, 6581, 9401, 9281, 8281, 8381))
    assert not run(['docker', 'ps', '-aq', '--filter', 'label=com.docker.compose.project=' + PROJECT]).stdout.strip()
    assert run(['docker', 'network', 'inspect', PROJECT + '_default'], allow_failure=True).returncode != 0
    subnet = ipaddress.ip_network(args.subnet, strict=True)
    assert subnet.version == 4 and subnet.is_private and subnet.prefixlen == 24
    network_ids = run(['docker', 'network', 'ls', '-q']).stdout.split()
    existing = json.loads(run(['docker', 'network', 'inspect', *network_ids]).stdout)
    assert not any(subnet.overlaps(ipaddress.ip_network(c['Subnet'], strict=False))
                   for net in existing for c in (net.get('IPAM', {}).get('Config') or [])
                   if c.get('Subnet') and ':' not in c['Subnet'])
    initial_power = power()
    assert power_valid(initial_power) and initial_power['hostMemoryPercent'] < 88
    nonce = secrets.token_hex(6)
    runtime = REPO.parent / ('.runtime-fullstack-cap10-' + nonce)
    runtime.mkdir(mode=0o700)
    if os.name == 'nt':
        identity = run(['whoami']).stdout.strip()
        run(['icacls', str(runtime), '/inheritance:r', '/grant:r', identity + ':(OI)(CI)F'])
    else:
        runtime.chmod(0o700)
    (runtime / 'tmp').mkdir(mode=0o700)
    volumes = {name: PROJECT + '-' + nonce + '-' + name for name in PORTS}
    for name in volumes.values():
        assert run(['docker', 'volume', 'inspect', name], allow_failure=True).returncode != 0
    override = runtime / 'fresh-volumes.yaml'
    text = ('services:\n  kafka:\n    cpus: 2\n    mem_limit: 1g\n    environment:\n'
            '      KAFKA_HEAP_OPTS: "-Xms256m -Xmx512m"\n'
            '  elasticsearch:\n    cpus: 2\n    mem_limit: 1g\n    environment:\n      ES_JAVA_OPTS: "-Xms256m -Xmx256m"\n'
            'networks:\n  default:\n    ipam:\n      config:\n        - subnet: ' + str(subnet) + '\nvolumes:\n')
    text += ''.join('  ' + VOLUME_KEYS[name] + ':\n    name: ' + value + '\n' for name, value in volumes.items())
    override.write_text(text, encoding='utf-8', newline='\n')
    value = {'CAP_COHORT': 'cap10', 'CAP10_RUNTIME': str(runtime), 'CAP10_EVIDENCE': str(ROOT),
             'CAP10_PLATFORM': PLATFORM, 'CAP10_TRANSITION_SHA256': transition_sha,
             'CAP10_ENVIRONMENT_METADATA': str(ROOT / 'environment.json'), 'CAP10_OVERRIDE': str(override),
             'GO_MYSQL_PORT': '3481', 'GO_REDIS_PORT': '6581', 'GO_KAFKA_PORT': '9401', 'GO_ES_PORT': '9281',
             'MYSQL_HOST': '127.0.0.1', 'MYSQL_PORT': '3481', 'MYSQL_DATABASE': 'project_full_stack_go',
             'MYSQL_USER': 'fullstack', 'MYSQL_PASSWORD': 'fullstack_go_dev_only',
             'MYSQL_BIN': str(Path(args.mysql_bin).resolve()),
             'REDIS_HOST': '127.0.0.1', 'REDIS_PORT': '6581', 'REDIS_DATABASE': '0', 'REDIS_PASSWORD': '',
             'API_BASE': 'http://127.0.0.1:8281', 'API_ADDR': '127.0.0.1:8281',
             'GO_DIAGNOSTICS_ADDR': '127.0.0.1:8381', 'GO_ENV': 'development',
             'GOMAXPROCS': '4', 'GOMEMLIMIT': '1GiB', 'ORDER_STREAM_WORKERS': '1',
             'KAFKA_BROKERS': '127.0.0.1:9401', 'KAFKA_TOPIC': 'community.events',
             'ES_URL': 'http://127.0.0.1:9281', 'ES_INDEX': 'community-notes-v1', 'AI_ENABLED': 'false',
             'TEMP': str(runtime / 'tmp'), 'TMP': str(runtime / 'tmp'),
             'TMPDIR': str(runtime / 'tmp'),
             'CAP_READ_OBSERVER_USER': 'cap10_' + nonce, 'CAP_READ_OBSERVER_PASSWORD': secrets.token_hex(24)}
    private = private_write(runtime, 'env.private.json', value)
    save(ROOT / 'preparation.json', {'at': now(), 'project': PROJECT, 'runtime': str(runtime),
         'privateEnvironmentFile': str(private), 'newVolumes': volumes, 'subnet': str(subnet),
         'power': initial_power, 'credentialValuesExported': False})
    run(compose(value) + ['config', '--quiet'], env=env(value))
    result = run(compose(value) + ['up', '-d', '--wait', 'mysql', 'redis', 'kafka', 'elasticsearch'],
                 env=env(value), allow_failure=True, timeout=360)
    (ROOT / 'compose-start.txt').write_text(result.stdout + result.stderr, encoding='utf-8')
    assert result.returncode == 0, 'preserve partial own setup; no API/load'
    owned = {name: container(name, running=True) for name in PORTS}
    assert all(any(item['name'] == volumes[name] and item['destination'] == MOUNTS[name]
                   for item in owned[name]['volumes']) for name in PORTS)
    complete_dependencies(value, runtime, private, owned)


def complete_dependencies(value, runtime, private, owned):
    preparation = read(ROOT / 'preparation.json')
    save(ROOT / 'environment.json', {'task': 'CAP-10A', 'at': now(), 'project': PROJECT,
         'executionPlatform': PLATFORM, 'transitionSHA256': transition(),
         'runtime': str(runtime), 'privateEnvironmentFile': str(private), 'containers': owned,
         'newVolumes': preparation['newVolumes'], 'subnet': preparation['subnet'], 'powerAfterStart': power(),
         'groups': ['community.follow-v1', 'community.feed-v1', 'community.search-v1'],
         'kafkaTopic': 'community.events', 'mysqlMaxOpen': 20, 'redisPoolSize': 10,
         'gomaxprocs': 4, 'gomemlimit': '1GiB', 'sharedHost': True, 'oldVolumesUntouched': True})
    result = run(['node', 'scripts/migrate.mjs'], env=env(value), allow_failure=True)
    (ROOT / 'migrate.txt').write_text(result.stdout + result.stderr, encoding='utf-8')
    save(ROOT / 'migrate-exit.json', {'at': now(), 'exitCode': result.returncode})
    assert result.returncode == 0
    user, password = value['CAP_READ_OBSERVER_USER'], value['CAP_READ_OBSERVER_PASSWORD']
    run(['docker', 'exec', '-i', owned['mysql']['id'], 'sh', '-c',
         'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" exec mysql -uroot --default-character-set=utf8mb4'],
        input=f"CREATE USER '{user}'@'%' IDENTIFIED BY '{password}'; GRANT SELECT ON project_full_stack_go.* TO '{user}'@'%'; GRANT SELECT ON performance_schema.* TO '{user}'@'%';\n")
    save(ROOT / 'observer-setup.json', {'at': now(), 'user': user, 'passwordExported': False,
         'privileges': ['SELECT project_full_stack_go.*', 'SELECT performance_schema.*']})
    run(['docker', 'exec', owned['kafka']['id'], '/opt/kafka/bin/kafka-topics.sh',
         '--bootstrap-server', 'localhost:9094', '--create', '--if-not-exists', '--topic',
         'community.events', '--partitions', '1', '--replication-factor', '1'])
    es_health = get_json('http://127.0.0.1:9281/_cluster/health')
    assert es_health['status'] in ('yellow', 'green')
    save(ROOT / 'dependency-health.json', {'at': now(), 'es': es_health,
         'kafkaTopic': 'community.events', 'topicCreatedBeforeAPI': True})
    print(json.dumps({'prepared': True, 'privateEnvironmentFile': str(private), 'runtime': str(runtime)}))


def resume_preparation(args):
    # Only the known healthy-before-migration failure is supported. Never repeat
    # compose up, replace volumes, or silently retry an already applied phase.
    assert not (ROOT / 'environment.json').exists() and not (ROOT / 'migrate-exit.json').exists()
    value, runtime = settings(args.environment)
    recovery = read(ROOT / 'preparation-recovery-1.json')
    preparation = read(ROOT / 'preparation.json')
    assert recovery['preparationSHA256'] == hashlib.sha256((ROOT / 'preparation.json').read_bytes()).hexdigest()
    assert recovery['project'] == preparation['project'] == PROJECT
    assert preparation['runtime'] == str(runtime) and preparation['privateEnvironmentFile'] == str(Path(args.environment).resolve())
    owned = {name: container(name, recovery['containers'][name]['id'], True) for name in PORTS}
    for name in PORTS:
        assert owned[name]['volumes'] == recovery['containers'][name]['volumes']
        assert any(item['name'] == preparation['newVolumes'][name] and item['destination'] == MOUNTS[name]
                   for item in owned[name]['volumes'])
    save(ROOT / 'preparation-resumed.json', {'at': now(), 'reason': 'strict Kafka image ancillary-mount adaptation',
         'exactIdsAndVolumesRevalidated': True, 'composeRepeated': False, 'volumesDeleted': False})
    complete_dependencies(value, runtime, Path(args.environment).resolve(), owned)


def start_api(args):
    value, runtime = settings(args.environment)
    assert not any(port_open(p) for p in (8281, 8381))
    assert re.fullmatch(r'[a-f0-9]{40}', args.source_sha) and re.fullmatch(r'[a-z0-9-]{1,30}', args.label)
    owned = read(ROOT / 'environment.json')['containers']
    for name in PORTS:
        container(name, owned[name]['id'], True)
    binary = Path(args.binary).resolve()
    info = run(['go', 'version', '-m', str(binary)]).stdout
    assert 'vcs.revision=' + args.source_sha in info and 'vcs.modified=false' in info
    current = {**value, 'CAP_SOURCE_SHA': args.source_sha, 'CAP_READ_EXPECTED_REVISION': 'capacity-' + args.source_sha,
               'GO_FEED_DIAGNOSTICS': 'true' if args.tail else 'false',
               'GO_LIKE_DIAGNOSTICS': 'true' if args.like_diagnostics else 'false'}
    private = private_write(runtime, 'env-' + args.label + '.private.json', current)
    out_path, err_path = (ROOT / ('api-' + args.label + suffix) for suffix in ('.stdout.txt', '.stderr.txt'))
    with out_path.open('xb') as out, err_path.open('xb') as error:
        child = subprocess.Popen([str(binary)], cwd=REPO, env=env(current), stdout=out, stderr=error, creationflags=FLAGS)
    process = psutil.Process(child.pid)
    metadata = {'at': now(), 'cohort': 'cap10', 'pid': child.pid, 'exe': str(binary),
                'executionPlatform': PLATFORM, 'transitionSHA256': transition(),
                'creationUnixMs': process.create_time() * 1000, 'createdAtUtc': dt.datetime.fromtimestamp(process.create_time(), dt.timezone.utc).isoformat(),
                'sourceSha': args.source_sha, 'expectedVersion': current['CAP_READ_EXPECTED_REVISION'],
                'sha256': hashlib.sha256(binary.read_bytes()).hexdigest(), 'label': args.label,
                'privateEnvironmentFile': str(private), 'tailEnabledRequested': args.tail,
                'likeDiagnosticsRequested': args.like_diagnostics}
    save(ROOT / ('api-' + args.label + '.json'), metadata)
    (ROOT / ('api-' + args.label + '-build.txt')).write_text(info, encoding='utf-8')
    ready = False
    for _ in range(100):
        if child.poll() is not None:
            break
        try:
            api_identity(metadata, health=True)
            ready = True
            break
        except (OSError, ValueError, AssertionError, psutil.Error):
            time.sleep(.25)
    save(ROOT / ('api-' + args.label + '-health.json'), {'at': now(), 'ready': ready,
         'readyBody': get_json('http://127.0.0.1:8281/health/ready') if ready else None,
         'capacityBody': get_json('http://127.0.0.1:8381/debug/capacity') if ready else None,
         'scope': 'App readiness alone does not establish consumer progress; low-speed canary receipts do.'})
    assert ready
    print(json.dumps({'startedAPI': child.pid, 'privateEnvironmentFile': str(private)}))


def stop_api(args):
    path = Path(args.api_metadata).resolve()
    assert path.parent == ROOT
    metadata = read(path)
    _, runtime = settings(metadata['privateEnvironmentFile'])
    assert not (runtime / 'cap10-active.private.json').exists(), 'owned stage remains active'
    process = api_identity(metadata)
    process.terminate()
    save(ROOT / ('api-' + metadata['label'] + '-stop.json'), {'at': now(), 'pid': process.pid,
         'identityMatched': True, 'exitCode': process.wait(timeout=15), 'goGracefulShutdownClaimed': False})
    assert not any(port_open(p) for p in (8281, 8381))


def own_sessions(tokens):
    assert isinstance(tokens, list) and len(tokens) <= COHORT.MAX_POOL_SIZE
    if not tokens:
        return 0
    parts = ['EXISTS'] + ['login:token:' + token for token in tokens]
    payload = b'*' + str(len(parts)).encode() + b'\r\n'
    for part in parts:
        raw = part.encode()
        payload += b'$' + str(len(raw)).encode() + b'\r\n' + raw + b'\r\n'
    with socket.create_connection(('127.0.0.1', 6581), timeout=5) as connection:
        connection.sendall(payload)
        data = b''
        while b'\r\n' not in data:
            chunk = connection.recv(256)
            assert chunk
            data += chunk
        assert data.startswith(b':')
        return int(data[1:].split(b'\r\n')[0])


def credential_tokens(manifest_path, runtime):
    manifest_path = Path(manifest_path).resolve()
    assert manifest_path.name == 'fixture.json' and manifest_path.parent.parent == ROOT
    assert manifest_path.parent.name in ('fixture', 'like-fixture') or re.fullmatch(r'like-fixture-[a-z0-9-]{1,80}', manifest_path.parent.name)
    manifest = read(manifest_path)
    count = COHORT.fixture_pool_size(manifest)
    path = Path(manifest['credentialFile']).resolve()
    assert path.is_relative_to(runtime / 'tmp') and path.name == 'input.json'
    if not path.exists() and manifest['status'] == 'PREPARING':
        return path, []
    assert path.is_file()
    found = []
    def walk(value):
        if isinstance(value, dict):
            for key, item in value.items():
                if key == 'token':
                    assert isinstance(item, str) and item
                    found.append(item)
                else:
                    walk(item)
        elif isinstance(value, list):
            for item in value:
                walk(item)
    walk(read(path))
    assert len(found) == len(set(found))
    assert len(found) == count or (manifest['status'] == 'PREPARING' and len(found) < count)
    return path, found


def phase_terminated(detail, load):
    """Require both remote termination and absence of the exact local process."""
    pts = detail.get('backend') == 'pts'
    if pts:
        remote = Path(detail['remoteEvidence']).resolve()
        if detail['phase'] not in ('warm', 'stable') or remote.parent != Path(load).resolve() or remote.name != detail['phase'] + '-pts':
            raise ValueError('PTS phase termination evidence is outside its owned load directory')
        if read(remote / 'terminal.json').get('remoteConverged') is not True:
            return False
    try:
        candidate = psutil.Process(detail['controller_pid'] if pts else detail['k6_pid'])
        created = detail['controller_started_unix_ms'] if pts else detail['k6_started_unix_ms']
        return abs(candidate.create_time() * 1000 - created) > 2000
    except psutil.NoSuchProcess:
        return True


def finish(args):
    value, runtime = settings(args.environment)
    metadata_path = Path(args.api_metadata).resolve()
    assert metadata_path.parent == ROOT
    metadata = read(metadata_path)
    assert not (runtime / 'cap10-active.private.json').exists(), 'resolve owned stage before cleanup'
    api_identity(metadata, health=True)
    owned = read(ROOT / 'environment.json')['containers']
    for name in PORTS:
        container(name, owned[name]['id'], True)
    phase_records = list(ROOT.glob('*-load/warm-process.json')) + list(ROOT.glob('*-load/stable-process.json'))
    for path in phase_records:
        detail = read(path)
        pts = detail.get('backend') == 'pts'
        if pts:
            assert value.get('CAP10_LOAD_BACKEND') == 'pts'
        assert phase_terminated(detail, path.parent), 'owned workload controller or remote PTS termination unproved'
    progress = ROOT / 'cleanup-progress.json'
    assert not progress.exists(), 'preserve partial cleanup; choose explicit recovery'
    record = {'at': now(), 'complete': False, 'steps': [], 'exceptions': [], 'volumesRetained': True}
    def checkpoint(step):
        record['steps'].append({'step': step, 'at': now()})
        temporary = progress.with_suffix('.pending')
        temporary.write_text(json.dumps(record, ensure_ascii=False, indent=2), encoding='utf-8', newline='\n')
        temporary.replace(progress)
    checkpoint('identity_preflight')
    try:
        record['cohorts'] = {}
        for kind, key, script in (('like', 'CAP_LIKE_FIXTURE', 'cap10-like.mjs'),):
            if key not in value:
                continue
            credentials, tokens = credential_tokens(value[key], runtime)
            count = COHORT.fixture_pool_size(read(value[key]))
            before = own_sessions(tokens)
            result = run(['node', 'scripts/' + script, 'finish'], env=env(value), timeout=180)
            body = json.loads(result.stdout.strip())
            after = own_sessions(tokens)
            record['cohorts'][kind] = {'poolSize': count, 'registeredSessions': len(tokens), 'sessionsBeforeLogout': before, 'sessionsAfterLogout': after,
                                     'finishOutput': body, 'privateInputRemoved': not credentials.exists()}
            assert after == 0 and not credentials.exists()
            checkpoint(kind + '_normal_logout_and_private_input_removal')
        # A killed Node helper can miss its finally block. Only remove exact
        # phase input paths registered by this cohort after all owned k6 exited.
        removed_phase_inputs = []
        for entry in ROOT.glob('*-load/*-private-input.json'):
            assert entry.name in ('warm-private-input.json', 'stable-private-input.json')
            registered = read(entry)
            phase_input = Path(registered['path']).resolve()
            parent = Path(registered['parent']).resolve()
            assert phase_input.name == 'input.json' and phase_input.parent == parent
            assert parent.parent == runtime / 'tmp' and re.fullmatch(r'cap-like-phase-[A-Za-z0-9_-]+', parent.name)
            if phase_input.exists():
                phase_input.unlink()
                removed_phase_inputs.append(str(phase_input))
            if parent.exists():
                parent.rmdir()
        record['removedInterruptedPrivatePhaseInputs'] = removed_phase_inputs
        checkpoint('registered_phase_private_inputs_removed')
        if value.get('CAP10_LOAD_BACKEND') == 'pts':
            result = run([sys.executable, '-B', '-X', 'utf8', 'scripts/pts/cap10-pts-prepare.py',
                          'cleanup-packet', '--environment', str(Path(args.environment).resolve())], timeout=30)
            record['ptsPacketCleanup'] = json.loads(result.stdout)
            checkpoint('registered_pts_upload_packet_removed')
        user = value['CAP_READ_OBSERVER_USER']
        assert re.fullmatch(r'cap10_[a-f0-9]{12}', user) and user.endswith(runtime.name[-12:])
        output = run(['docker', 'exec', '-i', owned['mysql']['id'], 'sh', '-c',
                      'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" exec mysql -uroot --batch --skip-column-names'],
                     input=f"DROP USER '{user}'@'%'; SELECT COUNT(*) FROM mysql.user WHERE User='{user}' AND Host='%';\n")
        assert output.stdout.strip() == '0'
        checkpoint('observer_removed_and_verified')
        process = api_identity(metadata)
        process.terminate()
        record['apiStop'] = {'pid': process.pid, 'identityMatched': True,
                             'exitCode': process.wait(timeout=15), 'goGracefulShutdownClaimed': False}
        assert not any(port_open(p) for p in (8281, 8381))
        checkpoint('owned_api_stopped')
        for name in PORTS:
            container(name, owned[name]['id'], True)
        run(['docker', 'stop', '--time', '60', *[owned[name]['id'] for name in PORTS]])
        record['containers'] = {}
        for name in PORTS:
            stopped = container(name, owned[name]['id'], False)
            # These Java images retain SIGTERM (128 + 15) after docker stop.
            # Other services must still exit zero; never accept OOM or SIGKILL.
            accepted_exit_codes = (0, 143) if name in ('kafka', 'elasticsearch') else (0,)
            assert stopped['exitCode'] in accepted_exit_codes and not stopped['oomKilled'] and stopped['volumes'] == owned[name]['volumes']
            for volume in stopped['volumes']:
                run(['docker', 'volume', 'inspect', volume['name']])
            record['containers'][name] = stopped
        checkpoint('four_exact_container_ids_stopped_and_volumes_retained')
        removed = []
        for entry in runtime.glob('owned-env-*.json'):
            private = Path(read(entry)['path']).resolve()
            assert private.parent == runtime and re.fullmatch(r'env[a-z0-9-]*\.private\.json', private.name)
            if private.exists():
                private.unlink()
                removed.append(private.name)
        record['removedPrivateEnvironmentFiles'] = removed
        record['complete'] = True
        checkpoint('complete')
        save(ROOT / 'cleanup.json', record)
        print(json.dumps({'cleanupComplete': True, 'ownServicesStopped': True, 'volumesRetained': True}))
    except Exception as error:
        record['exceptions'].append({'at': now(), 'class': type(error).__name__, 'detailsSuppressed': True})
        checkpoint('failed_preserve_recovery_inputs')
        raise


def main():
    assert Path.cwd().resolve() == REPO
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest='action', required=True)
    p = sub.add_parser('prepare-deps'); p.add_argument('--subnet', required=True); p.add_argument('--mysql-bin', required=True)
    p = sub.add_parser('resume-preparation'); p.add_argument('--environment', required=True)
    p = sub.add_parser('start-api'); p.add_argument('--environment', required=True); p.add_argument('--binary', required=True)
    p.add_argument('--source-sha', required=True); p.add_argument('--label', required=True); p.add_argument('--tail', action='store_true')
    p.add_argument('--like-diagnostics', action='store_true')
    p = sub.add_parser('stop-api'); p.add_argument('--api-metadata', required=True)
    p = sub.add_parser('finish'); p.add_argument('--environment', required=True); p.add_argument('--api-metadata', required=True)
    args = parser.parse_args()
    {'prepare-deps': prepare, 'resume-preparation': resume_preparation,
     'start-api': start_api, 'stop-api': stop_api, 'finish': finish}[args.action](args)


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        print('cap10-env: ' + type(error).__name__ + '; details suppressed; retain ownership and recovery inputs')
        raise SystemExit(1)
