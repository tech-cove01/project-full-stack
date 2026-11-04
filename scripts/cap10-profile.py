"""Plan an immutable mysql8-v1 transition; only explicit native --execute mutates.

The default path reads explicit registered files and writes one plan. It does
not inspect Docker, connect to SQL/HTTP, start services, or grant admission.
"""
import argparse
import copy
import datetime as dt
import hashlib
import importlib.util
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

import psutil


REPO = Path(__file__).resolve().parent.parent


def module(name, filename):
    spec = importlib.util.spec_from_file_location(name, Path(__file__).with_name(filename))
    value = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    return value


OWNED = module('cap10_profile_environment', 'cap10-env.py')
RESOURCE = OWNED.RESOURCE
SERVICES = ('mysql', 'redis', 'kafka', 'elasticsearch')
GIB = 1 << 30
BASE_BUFFER = 512 << 20
START_BUFFER = 128 << 20
TARGET_BUFFER = 4 * GIB
POLICY_KEYS = ('MemoryReservation', 'MemorySwappiness', 'OomKillDisable', 'PidsLimit',
               'CpusetCpus', 'CpusetMems', 'CpuPeriod', 'CpuQuota')
FACT_KEYS = ('serverUUID', 'version', 'flushAtCommit', 'syncBinlog', 'groupCommitDelay',
             'isolation', 'dedicatedServer', 'logBin', 'doublewrite', 'readOnly',
             'superReadOnly', 'sqlMode', 'instances', 'chunkBytes')
MYSQL_DOC = 'https://dev.mysql.com/doc/refman/8.4/en/server-status-variables.html'
BUFFER_DOC = 'https://dev.mysql.com/doc/refman/8.4/en/innodb-parameters.html'
RESIZE_DOC = 'https://dev.mysql.com/doc/refman/8.4/en/innodb-buffer-pool-resize.html'
RESIZE_SOURCE = 'https://github.com/mysql/mysql-server/blob/mysql-8.4.0/storage/innobase/buf/buf0buf.cc#L2394-L2407'
# MySQL 8.4: resize code 0 means no operation in progress; code 7 is failure.
# Progress resets on stage changes, so 100 is not a completion requirement.
# The source publishes its completed text only after the resizing work. A
# requested size plus the old code0 is insufficient before the thread starts.


class ProfileError(ValueError):
    """Only fixed, credential-free gate descriptions may be recorded."""


def require(condition, message):
    if not condition:
        raise ProfileError(message)


def now():
    return dt.datetime.now(dt.timezone.utc).isoformat()


def digest(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def regular(path):
    path = Path(path)
    require(path.is_absolute() and path.resolve() == path and not path.is_symlink()
            and path.is_file(), 'original regular absolute file required')
    return path


def read(path):
    value = json.loads(regular(path).read_text(encoding='utf-8-sig'))
    require(isinstance(value, dict), 'evidence object required')
    return value


def save(path, value, private=False):
    path = Path(path)
    require(not path.is_symlink() and path.resolve() == path, 'ordinary output path required')
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600 if private else 0o644)
    with os.fdopen(descriptor, 'w', encoding='utf-8', newline='\n') as stream:
        json.dump(value, stream, ensure_ascii=False, indent=2)
        stream.write('\n')
        stream.flush()
        os.fsync(stream.fileno())


def source_hashes():
    return {name: digest(Path(__file__).with_name(name)) for name in
            ('cap10-profile.py', 'cap10-env.py', 'cap10-resource-contract.py')}


def swap_target(memory, swap, target=8 * GIB):
    require(type(memory) is type(swap) is int and memory == 2 * GIB,
            'exact baseline memory and actual swap policy required')
    if swap == -1:
        return -1
    require(swap in (memory, 2 * memory), 'ambiguous swap policy; explicit recovery required')
    return target * (swap // memory)


def startup_buffer(snapshot):
    """Bind last measurement separately from the original startup command."""
    startup = snapshot['mysqlStartup']
    command = snapshot['containers']['mysql']['cmd']
    require(startup['configCmd'] == command and startup['lastMeasuredBufferBytes'] == BASE_BUFFER
            and startup['lastMeasuredKind'] == 'historical-contract-reference-not-current-read',
            'original startup command and last measured cache must be bound separately')
    require(isinstance(command, list) and all(type(item) is str for item in command),
            'original MySQL command array required')
    normalized = [item.replace('_', '-') for item in command]
    options = [item for item in normalized if item.startswith('--innodb-buffer-pool-size=')]
    require(not any(item == '--innodb-buffer-pool-size' for item in normalized),
            'unsupported split startup buffer option')
    require(startup['versionFamily'] == '8.4', 'known MySQL8.4 startup contract required')
    if options:
        require(len(options) == 1 and options[0] in ('--innodb-buffer-pool-size=134217728',
                '--innodb-buffer-pool-size=536870912'), 'unknown startup buffer option')
        expected = int(options[0].split('=')[1])
        require(startup['basis'] == 'explicit-cmd', 'explicit startup option proof required')
    else:
        require(command == ['mysqld'] and startup['basis'] == 'mysql84-image-default'
                and startup['defaultContract'] == BUFFER_DOC,
                'default startup buffer requires the exact registered default command')
        expected = START_BUFFER
    require(startup['startupBufferBytes'] == expected, 'declared startup cache differs from command')
    return expected


def startup_contract(command):
    """Known immutable command contract; actual SQL cache is a separate field."""
    expected = START_BUFFER
    basis = 'mysql84-image-default'
    options = [item.replace('_', '-') for item in command
               if item.replace('_', '-').startswith('--innodb-buffer-pool-size=')]
    if options:
        require(len(options) == 1 and options[0].split('=')[1] in ('134217728', '536870912'),
                'unknown explicit startup buffer option')
        expected = int(options[0].split('=')[1])
        basis = 'explicit-cmd'
    value = {'configCmd': command, 'lastMeasuredBufferBytes': BASE_BUFFER,
             'lastMeasuredKind': 'historical-contract-reference-not-current-read',
             'lastMeasuredContractReference': 'CAP10-baseline-cache512-v1',
             'startupBufferBytes': expected, 'versionFamily': '8.4', 'basis': basis,
             'defaultContract': BUFFER_DOC}
    startup_buffer({'mysqlStartup': value, 'containers': {'mysql': {'cmd': command}}})
    return value


def volume_pairs(container):
    return sorted((item['Name'], item['Destination']) for item in container['mounts']
                  if item['Type'] == 'volume')


def validate_snapshot(metadata, snapshot, profile='baseline'):
    require(set(metadata['containers']) == set(snapshot) == set(SERVICES),
            'four registered services required')
    limits = RESOURCE.container_limits(profile)
    ids = []
    for service in SERVICES:
        current, original = snapshot[service], metadata['containers'][service]
        ids.append(current['id'])
        require(re.fullmatch(r'[a-f0-9]{64}', current['id']) is not None
                and current['id'] == original['id'] and current['imageId'] == original['imageId']
                and current['name'] == original['name'] and current['project'] == metadata['project']
                and current['service'] == service, 'registered CID/image/owner identity differs')
        require((current['cpuNano'], current['memoryBytes']) == limits[service],
                'actual container limits differ from bound profile')
        require(current['ports'] == original['ports'] and len(current['networks']) == 1
                and current['networks'][OWNED.PROJECT + '_default'] == original['networkId'],
                'registered ports/network differ')
        expected = sorted((item['name'], item['destination']) for item in original['volumes'])
        require(volume_pairs(current) == expected, 'registered volume identity differs')
        require(current['running'] is True and current['healthStatus'] == 'healthy',
                'registered service must be running and healthy')
        require(isinstance(current['policy'], dict) and set(current['policy']) == set(POLICY_KEYS),
                'complete protected resource policy required')
    require(len(set(ids)) == 4, 'four distinct full CIDs required')


def validate_baseline_resources(baseline):
    names = {mount['Name'] for item in baseline['containers'].values()
             for mount in item['mounts'] if mount['Type'] == 'volume'}
    require(set(baseline['volumes']) == names, 'exact registered volume snapshots required')
    for item in baseline['containers'].values():
        for mount in item['mounts']:
            if mount['Type'] == 'volume':
                registered = baseline['volumes'][mount['Name']]
                require(registered['name'] == mount['Name'] and registered['driver'] == 'local'
                        and registered['mountpoint'] == mount['Source'], 'volume snapshot and mount disagree')
    for service, item in baseline['containers'].items():
        primary = [mount for mount in item['mounts'] if mount['Destination'] == OWNED.MOUNTS[service]]
        require(len(primary) == 1 and primary[0]['Type'] == 'volume', 'registered primary volume required')
        volume = baseline['volumes'][primary[0]['Name']]
        require(volume['labels'].get('com.docker.compose.project') == OWNED.PROJECT,
                'primary volume project owner differs')
    network = baseline['network']
    require(network['id'] == next(iter(baseline['containers']['mysql']['networks'].values()))
            and network['name'] == OWNED.PROJECT + '_default'
            and network['labels'].get('com.docker.compose.project') == OWNED.PROJECT,
            'registered network snapshot owner differs')


def private_configuration(path, root):
    path, root = regular(path), Path(root)
    value = read(path)
    runtime = path.parent
    require(re.fullmatch(r'env[a-z0-9-]*\.private\.json', path.name) is not None
            and re.fullmatch(r'\.runtime-fullstack-cap10-[a-f0-9]{12}', runtime.name) is not None
            and runtime.parent == REPO.parent and runtime.resolve() == runtime,
            'registered private runtime and env filename required')
    require(value['CAP10_RUNTIME'] == str(runtime) and value['CAP10_EVIDENCE'] == str(root)
            and value['CAP_COHORT'] == 'cap10' and value['CAP10_PLATFORM'] == 'linux',
            'original native CAP10 private configuration required')
    require(value.get('CAP10_RESOURCE_PROFILE', 'baseline') == 'baseline'
            and RESOURCE.metadata_path(value, root) == root / 'environment.json',
            'only original baseline metadata can start a profile transition')
    return value, runtime


def build_plan(environment, baseline_path, root, nonce=None):
    """Pure file contract: never constructs or calls a native execution adapter."""
    root = Path(root).resolve()
    value, runtime = private_configuration(environment, root)
    metadata_path = regular(root / 'environment.json')
    metadata, baseline = read(metadata_path), read(baseline_path)
    require(metadata['project'] == OWNED.PROJECT and metadata['executionPlatform'] == 'linux'
            and RESOURCE.bound_profile(value, metadata) == 'baseline', 'original CAP10 baseline required')
    require(baseline['schemaVersion'] == 1 and baseline['sourceEnvironmentSHA256'] == digest(metadata_path),
            'original sanitized snapshot must bind its environment metadata')
    validate_snapshot(metadata, baseline['containers'])
    validate_baseline_resources(baseline)
    startup = startup_buffer(baseline)
    validate_facts(baseline['capturedMySQL'], {BASE_BUFFER, startup})
    require(all(not item['Destination'].startswith(('/etc/my.cnf', '/etc/mysql'))
                for item in baseline['containers']['mysql']['mounts']),
            'external MySQL configuration needs a separate reviewed startup contract')
    swap_target(2 * GIB, baseline['containers']['mysql']['memorySwap'])
    nonce = nonce or secrets.token_hex(6)
    require(type(nonce) is str and re.fullmatch(r'[a-f0-9]{12}', nonce) is not None, 'nonce required')
    return {'schemaVersion': 1, 'kind': 'cap10-resource-profile', 'createdAt': now(), 'nonce': nonce,
            'fromProfile': 'baseline', 'toProfile': 'mysql8-v1',
            'sourceEnvironmentPath': 'environment.json', 'sourceEnvironmentSHA256': digest(metadata_path),
            'sourcePrivatePath': str(Path(environment)), 'sourcePrivateSHA256': digest(environment),
            'baselineSnapshotPath': str(regular(baseline_path)), 'baselineSnapshotSHA256': digest(baseline_path),
            'sourceHashes': source_hashes(), 'targetEnvironmentPath': 'environment-' + nonce + '.json',
            'targetPrivatePath': str(runtime / ('env-mysql8-' + nonce + '.private.json')),
            'journalPath': 'resource-profile-' + nonce, 'lastMeasuredBufferBytes': BASE_BUFFER,
            'knownStartupBufferBytes': startup, 'targetBufferBytes': TARGET_BUFFER,
            'targetCPU': 8_000_000_000, 'targetMemoryBytes': 8 * GIB,
            'hostAvailableBeforeBytes': 10 * GIB, 'hostAvailableDuringBytes': 4 * GIB,
            'resizeTimeoutSeconds': 180, 'resizeContract': MYSQL_DOC,
            'resizeCompletionContract': RESIZE_DOC, 'resizeCompletionSource': RESIZE_SOURCE,
            'noIdleAdmissionOrLoad': True}


def verify_plan(plan_path, root):
    plan_path = regular(plan_path)
    plan = read(plan_path)
    rebuilt = build_plan(plan['sourcePrivatePath'], plan['baselineSnapshotPath'], root, plan['nonce'])
    rebuilt['createdAt'] = plan['createdAt']
    require(rebuilt == plan, 'reviewed plan/source inputs changed')
    return plan


def same_identity(before, after, *, mysql_target=False):
    expected, actual = copy.deepcopy(before), copy.deepcopy(after)
    # Docker may reorder Mounts between inspect calls. Compare every complete
    # mount object without changing raw snapshots or discarding duplicates.
    for item in (expected, actual):
        require(type(item['mounts']) is list and all(type(mount) is dict for mount in item['mounts']),
                'complete mount objects required')
        item['mounts'] = sorted(item['mounts'], key=lambda mount: json.dumps(
            mount, sort_keys=True, separators=(',', ':'), ensure_ascii=False, allow_nan=False))
    if mysql_target:
        expected['cpuNano'] = 8_000_000_000
        expected['memoryBytes'] = 8 * GIB
        expected['memorySwap'] = swap_target(before['memoryBytes'], before['memorySwap'])
    require(expected == actual, 'complete service identity/policy changed beyond the plan')


def validate_facts(facts, allowed_buffers):
    require(facts['version'].startswith('8.4.') and facts['bufferBytes'] in allowed_buffers,
            'actual cache is unknown or partially applied; explicit recovery required')
    require((facts['flushAtCommit'], facts['syncBinlog'], facts['groupCommitDelay'], facts['isolation'],
             facts['dedicatedServer']) == (1, 1, 0, 'REPEATABLE-READ', 0),
            'actual durability/isolation/dedicated policy differs')
    require(facts['logBin'] == 1 and facts['readOnly'] == 0 and facts['superReadOnly'] == 0,
            'actual binlog/read-only policy differs')
    require(type(facts['instances']) is int and facts['instances'] >= 1
            and type(facts['chunkBytes']) is int and facts['chunkBytes'] > 0
            and TARGET_BUFFER % (facts['instances'] * facts['chunkBytes']) == 0,
            'target cache is not an exact resize unit')
    require(type(facts['resizeCode']) is int and 0 <= facts['resizeCode'] <= 7
            and type(facts['resizeProgress']) is int and 0 <= facts['resizeProgress'] <= 100,
            'actual MySQL8.4 resize status is missing or invalid')
    require(type(facts['resizeText']) is str and type(facts['uptime']) is int and facts['uptime'] >= 0,
            'actual resize text/server uptime is unknown')
    require(type(facts['serverUUID']) is str and re.fullmatch(r'[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}',
                                                           facts['serverUUID']) is not None,
            'actual MySQL server UUID is unknown')


def resize_complete(before, after):
    # Oracle8.4 buf0buf.cc emits this text only at the end of a successful
    # operation. Require it to differ from the text observed immediately before
    # our SET. If two resizes finish in the same timestamp second and the text
    # cannot prove freshness, fail closed rather than invent completion.
    completed = re.fullmatch(r'Completed resizing buffer pool at [0-9]{6}\s+[0-9]{1,2}:[0-9]{2}:[0-9]{2}\.',
                             after['resizeText']) is not None
    return (after['bufferBytes'] == TARGET_BUFFER and after['resizeCode'] == 0 and completed
            and after['resizeText'] != before['resizeText'] and after['uptime'] >= before['uptime'])


def memory_gate(adapter, before=False):
    value = adapter.memory()
    minimum = (10 if before else 4) * GIB
    require(type(value['availableBytes']) is int and type(value['totalBytes']) is int,
            'actual host memory is unknown')
    total_limits = sum(memory for _, memory in RESOURCE.container_limits('mysql8-v1').values())
    require(value['totalBytes'] >= total_limits + GIB and value['availableBytes'] >= minimum,
            'actual host memory gate refused the resource transition')
    return {**value, 'at': now(), 'minimumAvailableBytes': minimum}


class Native:
    """Future native Linux execution only; no SSH/cloud or service startup."""
    def __init__(self):
        self.children = []

    def command(self, argv, *, input=None, timeout=15):
        child = subprocess.Popen(argv, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                 text=True, encoding='utf-8', errors='replace', cwd=REPO)
        self.children.append(child)
        try:
            output, _ = child.communicate(input=input, timeout=timeout)
        except BaseException:
            child.kill()
            child.communicate()
            raise RuntimeError('native command interrupted; raw details suppressed') from None
        require(child.returncode == 0, 'native command failed; raw details suppressed')
        return output

    def close(self):
        for child in self.children:
            if child.poll() is None:
                child.kill()
                child.communicate()

    def settled(self):
        return all(child.poll() is not None for child in self.children)

    def local(self):
        require(sys.platform == 'linux' and OWNED.PLATFORM == 'linux', 'native Linux execute required')
        require(os.environ.get('DOCKER_HOST', 'unix:///var/run/docker.sock') == 'unix:///var/run/docker.sock',
                'only native local Unix Docker is allowed')
        context = self.command(['docker', 'context', 'show']).strip()
        endpoint = self.command(['docker', 'context', 'inspect', '--format',
                                 '{{.Endpoints.docker.Host}}', context]).strip()
        require(endpoint == 'unix:///var/run/docker.sock', 'remote Docker endpoint refused')

    def memory(self):
        value = psutil.virtual_memory()
        return {'availableBytes': value.available, 'totalBytes': value.total}

    def guard_idle(self, root):
        for port in (8281, 8381):
            with socket.socket() as connection:
                connection.settimeout(.25)
                require(connection.connect_ex(('127.0.0.1', port)) != 0, 'API/diagnostics port is live')
        for path in root.glob('api-*.json'):
            metadata = read(path)
            # stop_api emits pid/identityMatched/exitCode, without a birth time;
            # start_api's health artifact has ready/readyBody/capacityBody. They
            # are results, not process identities. Check each actual process
            # metadata independently; an old stop result never proves it absent.
            if ('creationUnixMs' not in metadata and path.name.endswith('-stop.json')
                    and metadata.get('identityMatched') is True
                    and type(metadata.get('pid')) is int and metadata['pid'] > 0):
                if type(metadata.get('exitCode')) is int:
                    continue
                if 'exitCode' in metadata and metadata['exitCode'] is None:
                    # psutil.wait() can return None for a non-child process.
                    # This is only a bound result artifact: the companion's
                    # actual PID/birth must still pass the process check below.
                    companion = path.with_name(path.name.removesuffix('-stop.json') + '.json')
                    require(companion.is_file() and not companion.is_symlink()
                            and companion.resolve() == companion,
                            'null API stop requires its original process metadata')
                    original = read(companion)
                    require(original.get('cohort') == 'cap10'
                            and type(original.get('pid')) is int and original['pid'] == metadata['pid']
                            and type(original.get('creationUnixMs')) in (int, float)
                            and math.isfinite(original['creationUnixMs']) and original['creationUnixMs'] > 0,
                            'null API stop differs from its original process identity')
                    continue
            if ('pid' not in metadata and path.name.endswith('-health.json')
                    and type(metadata.get('ready')) is bool and 'readyBody' in metadata
                    and 'capacityBody' in metadata):
                continue
            require(metadata.get('cohort') == 'cap10' and type(metadata.get('pid')) is int
                    and type(metadata.get('creationUnixMs')) in (int, float)
                    and math.isfinite(metadata['creationUnixMs'])
                    and metadata['pid'] > 0 and metadata['creationUnixMs'] > 0,
                    'unknown API artifact/process identity schema')
            try:
                process = psutil.Process(metadata['pid'])
                if abs(process.create_time() * 1000 - metadata['creationUnixMs']) > 2:
                    # Numeric PID reuse proves the recorded owner is gone. Do
                    # not inspect, stop or claim the unrelated replacement.
                    continue
            except psutil.NoSuchProcess:
                continue
            raise ValueError('recorded API process is still present')

    def inspect(self, cid):
        require(re.fullmatch(r'[a-f0-9]{64}', cid) is not None, 'exact CID required')
        # Deliberately select fields; never capture Config.Env or health log output.
        fields = {'id': '.Id', 'name': '.Name', 'imageId': '.Image',
                  'project': 'index .Config.Labels "com.docker.compose.project"',
                  'service': 'index .Config.Labels "com.docker.compose.service"',
                  'running': '.State.Running', 'cpuNano': '.HostConfig.NanoCpus',
                  'memoryBytes': '.HostConfig.Memory', 'memorySwap': '.HostConfig.MemorySwap',
                  'ports': '.HostConfig.PortBindings', 'mounts': '.Mounts', 'cmd': '.Config.Cmd'}
        template = '{' + ','.join(json.dumps(key) + ':{{json (' + expression + ')}}'
                                  for key, expression in fields.items())
        template += ',"healthStatus":{{if .State.Health}}{{json .State.Health.Status}}{{else}}null{{end}}'
        template += ',"policy":{' + ','.join(json.dumps(key) + ':{{json .HostConfig.' + key + '}}'
                                              for key in POLICY_KEYS) + '}'
        template += ',"networks":{{json .NetworkSettings.Networks}}}'
        value = json.loads(self.command(['docker', 'inspect', '--format', template, cid]))
        value['networks'] = {name: item['NetworkID'] for name, item in value['networks'].items()}
        return value

    def resources(self, containers):
        volumes = {}
        for item in containers.values():
            for mount in item['mounts']:
                if mount['Type'] == 'volume':
                    template = '{"name":{{json .Name}},"driver":{{json .Driver}},"mountpoint":{{json .Mountpoint}},"labels":{{json .Labels}}}'
                    actual = json.loads(self.command(['docker', 'volume', 'inspect', '--format', template, mount['Name']]))
                    actual['labels'] = actual['labels'] or {}
                    volumes[mount['Name']] = actual
        network_id = next(iter(containers['mysql']['networks'].values()))
        template = '{"id":{{json .Id}},"name":{{json .Name}},"driver":{{json .Driver}},"labels":{{json .Labels}}}'
        network = json.loads(self.command(['docker', 'network', 'inspect', '--format', template, network_id]))
        network['labels'] = network['labels'] or {}
        return volumes, network

    def guards(self, containers, baseline):
        ids = self.command(['docker', 'ps', '-aq', '--no-trunc', '--filter',
                            'label=com.docker.compose.project=' + OWNED.PROJECT]).split()
        require(sorted(ids) == sorted(item['id'] for item in containers.values()),
                'unknown or missing CAP10 project service')
        for item in containers.values():
            for mount in item['mounts']:
                if mount['Type'] == 'volume':
                    users = self.command(['docker', 'ps', '-aq', '--no-trunc', '--filter',
                                          'volume=' + mount['Name']]).split()
                    require(users == [item['id']], 'unknown/shared registered volume mount user')
        volumes, network = self.resources(containers)
        require(volumes == baseline['volumes'], 'registered volume identity/labels differ')
        require(network == baseline['network'], 'registered network identity/labels differ')

    def sql(self, cid, query):
        command = 'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" exec mysql -uroot --batch --skip-column-names --default-character-set=utf8mb4'
        return self.command(['docker', 'exec', '-i', cid, 'sh', '-c', command], input=query, timeout=10)

    def facts(self, cid):
        variables = {'version': '@@version', 'serverUUID': '@@server_uuid', 'bufferBytes': '@@GLOBAL.innodb_buffer_pool_size',
                     'flushAtCommit': '@@GLOBAL.innodb_flush_log_at_trx_commit', 'syncBinlog': '@@GLOBAL.sync_binlog',
                     'groupCommitDelay': '@@GLOBAL.binlog_group_commit_sync_delay', 'isolation': '@@GLOBAL.transaction_isolation',
                     'dedicatedServer': '@@GLOBAL.innodb_dedicated_server', 'logBin': '@@GLOBAL.log_bin',
                     'doublewrite': '@@GLOBAL.innodb_doublewrite', 'readOnly': '@@GLOBAL.read_only',
                     'superReadOnly': '@@GLOBAL.super_read_only', 'sqlMode': '@@GLOBAL.sql_mode',
                     'instances': '@@GLOBAL.innodb_buffer_pool_instances', 'chunkBytes': '@@GLOBAL.innodb_buffer_pool_chunk_size'}
        expressions = ["'" + key + "'," + expression for key, expression in variables.items()]
        for key, name in (('resizeCode', 'Innodb_buffer_pool_resize_status_code'),
                          ('resizeProgress', 'Innodb_buffer_pool_resize_status_progress'), ('uptime', 'Uptime')):
            expressions.append("'" + key + "',CAST((SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='" + name + "') AS UNSIGNED)")
        expressions.append("'resizeText',(SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Innodb_buffer_pool_resize_status')")
        expressions.append("'otherSessions',(SELECT COUNT(*) FROM information_schema.processlist WHERE ID<>CONNECTION_ID() AND COMMAND<>'Daemon')")
        expressions.append("'activeTransactions',(SELECT COUNT(*) FROM information_schema.innodb_trx)")
        return json.loads(self.sql(cid, 'SELECT JSON_OBJECT(' + ','.join(expressions) + ');'))

    def update(self, mysql):
        self.command(['docker', 'update', '--cpus', '8', '--memory', str(8 * GIB), '--memory-swap',
                      str(swap_target(mysql['memoryBytes'], mysql['memorySwap'])), mysql['id']])

    def resize(self, cid):
        self.sql(cid, 'SET GLOBAL innodb_buffer_pool_size=4294967296;')

    def pause(self):
        time.sleep(1)


def claim_owner(runtime, owner, prepared_name):
    # Only a complete fsynced record can become the shared active mutex.
    prepared = runtime / prepared_name
    save(prepared, owner, private=True)
    os.link(prepared, runtime / 'cap10-active.private.json')


def release_owner(runtime, owner, native):
    lock = runtime / 'cap10-active.private.json'
    if (native.settled() and owner['pid'] == os.getpid()
            and owner['created'] == psutil.Process().create_time()
            and lock.is_file() and not lock.is_symlink() and read(lock) == owner):
        lock.unlink()


def capture_baseline(environment, output, root, adapter=None):
    """Explicit future native read-only capture; never update/resize resources."""
    root, output = Path(root).resolve(), Path(output)
    configuration, runtime = private_configuration(environment, root)
    match = re.fullmatch(r'baseline-([a-f0-9]{12})\.json', output.name)
    require(output.is_absolute() and output.parent == root and output.resolve() == output
            and not output.exists() and not output.is_symlink() and match is not None,
            'new canonical immutable baseline output required')
    nonce = match.group(1)
    require(runtime.stat().st_mode & 0o077 == 0 or os.name == 'nt', 'private runtime permissions are not owner-only')
    source_path = root / 'environment.json'
    metadata, source_sha = read(source_path), digest(source_path)
    hashes_before = source_hashes()
    require(metadata['project'] == OWNED.PROJECT and metadata['executionPlatform'] == 'linux'
            and RESOURCE.bound_profile(configuration, metadata) == 'baseline', 'original CAP10 baseline required')
    owner = {'kind': 'resource-profile-capture', 'pid': os.getpid(), 'created': psutil.Process().create_time(),
             'nonce': nonce, 'sourceEnvironmentSHA256': source_sha, 'output': output.name}
    native = adapter or Native()
    claimed = False
    current = None
    actual = None
    try:
        claim_owner(runtime, owner, 'profile-capture-owner-' + nonce + '.private.json')
        claimed = True
        native.local()
        if adapter is None:
            checked, checked_runtime = OWNED.settings(environment)
            require(checked == configuration and checked_runtime == runtime, 'native private identity differs')
        native.guard_idle(root)
        current = {service: native.inspect(metadata['containers'][service]['id']) for service in SERVICES}
        validate_snapshot(metadata, current)
        require(all(mount['Type'] == 'volume' or (service == 'mysql' and mount['Type'] == 'bind'
                    and mount['Destination'] == '/docker-entrypoint-initdb.d' and mount['RW'] is False)
                    for service, item in current.items() for mount in item['mounts']),
                'unknown non-volume mount in registered service')
        startup = startup_contract(current['mysql']['cmd'])
        volumes, network = native.resources(current)
        snapshot = {'schemaVersion': 1, 'capturedAt': now(), 'readOnlyCapture': True,
                    'sourceEnvironmentSHA256': source_sha, 'containers': current,
                    'volumes': volumes, 'network': network, 'mysqlStartup': startup}
        validate_baseline_resources(snapshot)
        native.guards(current, snapshot)
        actual = native.facts(current['mysql']['id'])
        validate_facts(actual, {BASE_BUFFER, startup['startupBufferBytes']})
        require(actual['otherSessions'] == actual['activeTransactions'] == actual['resizeCode'] == 0,
                'capture requires an idle baseline server')
        snapshot['capturedMySQL'] = actual
        snapshot['capturedBySourceHashes'] = hashes_before
        native.guard_idle(root)
        for service in SERVICES:
            same_identity(current[service], native.inspect(current[service]['id']))
        native.guards(current, snapshot)
        require(digest(source_path) == source_sha, 'original environment changed during capture')
        require(source_hashes() == hashes_before, 'helper sources changed during capture')
        save(output, snapshot)
        return {'captured': True, 'baselinePath': str(output), 'baselineSHA256': digest(output),
                'actualBufferBytes': actual['bufferBytes'], 'lastMeasuredContractBytes': BASE_BUFFER,
                'lastMeasuredIsCurrentRead': False, 'resourcesChanged': False, 'noIdleAdmissionOrLoad': True}
    except BaseException as error:
        if claimed:
            try:
                save(runtime / ('profile-capture-failure-' + nonce + '.private.json'),
                     {'at': now(), 'errorClass': type(error).__name__, 'containersObserved': current,
                      'mysqlObserved': actual, 'resourcesChanged': False,
                      'reason': str(error) if isinstance(error, ProfileError) else 'capture failed; raw details suppressed'}, private=True)
            except BaseException:
                pass
        raise RuntimeError('read-only baseline capture failed; preserve first artifacts') from None
    finally:
        try:
            native.close()
        finally:
            release_owner(runtime, owner, native)


def execute(plan_path, root, adapter=None):
    """No retries/rollback. Preserve exact intermediate state on every failure."""
    root = Path(root).resolve()
    plan = verify_plan(plan_path, root)
    configuration, runtime = private_configuration(plan['sourcePrivatePath'], root)
    baseline = read(plan['baselineSnapshotPath'])
    metadata = read(root / 'environment.json')
    target_metadata = RESOURCE.metadata_path({'CAP10_ENVIRONMENT_METADATA': str(root / plan['targetEnvironmentPath'])}, root)
    target_private = runtime / Path(plan['targetPrivatePath']).name
    journal = root / plan['journalPath']
    require(not any(path.exists() or path.is_symlink() for path in (journal, target_metadata, target_private)),
            'resource namespace consumed; preserve original outcome')
    require(runtime.stat().st_mode & 0o077 == 0 or os.name == 'nt', 'private runtime permissions are not owner-only')
    native = adapter or Native()
    owner = {'kind': 'resource-profile', 'pid': os.getpid(), 'created': psutil.Process().create_time(),
             'nonce': plan['nonce'], 'planSHA256': digest(plan_path)}
    lock = runtime / 'cap10-active.private.json'
    claimed = False
    local_verified = False
    published = False
    phase = 'claim'
    actual_before = None
    last_facts = None
    try:
        # Link a fully written/fsynced owner atomically into the shared mutex.
        # A failed partial owner write can never leave a malformed active lock.
        claim_owner(runtime, owner, 'profile-owner-' + plan['nonce'] + '.private.json')
        claimed = True
        journal.mkdir()
        native.local()
        local_verified = True
        # Native environment/transition validation is intentionally after claim
        # and inside finally; pure injected checks need no historical host files.
        if adapter is None:
            checked, checked_runtime = OWNED.settings(plan['sourcePrivatePath'])
            require(checked == configuration and checked_runtime == runtime, 'native private identity differs')
        native.guard_idle(root)
        current = {service: native.inspect(metadata['containers'][service]['id']) for service in SERVICES}
        validate_snapshot(metadata, current)
        for service in SERVICES:
            same_identity(baseline['containers'][service], current[service])
        native.guards(current, baseline)
        memory_before = memory_gate(native, before=True)
        mysql = current['mysql']
        actual_before = native.facts(mysql['id'])
        last_facts = actual_before
        validate_facts(actual_before, {BASE_BUFFER, plan['knownStartupBufferBytes']})
        require(actual_before['resizeCode'] == 0 and actual_before['otherSessions'] == 0
                and actual_before['activeTransactions'] == 0, 'MySQL must be idle before resource mutation')
        save(journal / 'before.json', {'at': now(), 'containers': current, 'mysql': actual_before,
                                     'memory': memory_before, 'lastMeasuredBufferBytes': BASE_BUFFER,
                                     'knownStartupBufferBytes': plan['knownStartupBufferBytes'],
                                     'actualBufferBytes': actual_before['bufferBytes']})
        phase = 'docker-update'
        native.guard_idle(root)
        native.guards(current, baseline)
        native.update(mysql)
        updated = {service: native.inspect(current[service]['id']) for service in SERVICES}
        for service in SERVICES:
            same_identity(current[service], updated[service], mysql_target=service == 'mysql')
        save(journal / 'after-update.json', {'at': now(), 'containers': updated, 'memory': memory_gate(native)})
        phase = 'buffer-resize'
        native.guard_idle(root)
        immediate = native.facts(mysql['id'])
        last_facts = immediate
        validate_facts(immediate, {actual_before['bufferBytes']})
        require(immediate['otherSessions'] == immediate['activeTransactions'] == immediate['resizeCode'] == 0,
                'server became busy before resize')
        require(all(immediate[key] == actual_before[key] for key in FACT_KEYS), 'server facts changed before resize')
        save(journal / 'resize-request.json', {'at': now(), 'beforeSet': immediate,
             'requestedBufferBytes': TARGET_BUFFER, 'planSHA256': digest(plan_path),
             'statement': 'SET GLOBAL innodb_buffer_pool_size=4294967296;'})
        native.resize(mysql['id'])
        save(journal / 'resize-dispatched.json', {'at': now(), 'requestSHA256': digest(journal / 'resize-request.json')})
        samples = []
        deadline = time.monotonic() + plan['resizeTimeoutSeconds']
        while True:
            facts = native.facts(mysql['id'])
            last_facts = facts
            validate_facts(facts, {actual_before['bufferBytes'], TARGET_BUFFER})
            memory = memory_gate(native)
            require(all(facts[key] == actual_before[key] for key in FACT_KEYS), 'server policy changed during resize')
            require(facts['uptime'] >= actual_before['uptime'], 'server restarted during resource transition')
            sample = {'at': now(), 'mysql': facts, 'memory': memory}
            samples.append(sample)
            save(journal / ('resize-' + str(len(samples)).zfill(3) + '.json'), sample)
            require(facts['resizeCode'] != 7, 'server reported resize failure')
            if resize_complete(immediate, facts):
                break
            require(time.monotonic() < deadline, 'bounded resize completion timeout')
            native.pause()
        require(facts['otherSessions'] == facts['activeTransactions'] == 0, 'server not idle at resize completion')
        final = {service: native.inspect(current[service]['id']) for service in SERVICES}
        for service in SERVICES:
            same_identity(current[service], final[service], mysql_target=service == 'mysql')
        native.guard_idle(root)
        native.guards(final, baseline)
        verify_plan(plan_path, root)
        phase = 'export'
        target_configuration = {**configuration, 'CAP10_RESOURCE_PROFILE': 'mysql8-v1',
                                'CAP10_ENVIRONMENT_METADATA': str(target_metadata)}
        # Register even a partially written private target. No secret values in
        # public files; a missing public metadata file is never usable by callers.
        # Match private_write's original independent 10hex registration token.
        # The 12hex profile namespace belongs to the env filename, not this
        # registry schema consumed by continuation and cleanup tools.
        save(runtime / ('owned-env-' + secrets.token_hex(5) + '.json'), {'path': str(target_private)})
        save(target_private, target_configuration, private=True)
        exported = copy.deepcopy(metadata)
        for service in SERVICES:
            exported['containers'][service].update(cpuNano=final[service]['cpuNano'], memoryBytes=final[service]['memoryBytes'],
                                                   memorySwap=final[service]['memorySwap'])
        exported.update(at=now(), resourceProfile='mysql8-v1', privateEnvironmentFile=str(target_private),
                        sourceEnvironmentPath='environment.json', sourceEnvironmentSHA256=plan['sourceEnvironmentSHA256'],
                        resourceChangePlanSHA256=digest(plan_path), mysqlActual=facts,
                        bufferRuntimeOnly=True, serverRestartRequiresActualRevalidation=True)
        resize_proof = {'at': now(), 'complete': True, 'actualBefore': actual_before, 'actualAfter': facts,
                        'beforeSet': immediate, 'resizeRequestSHA256': digest(journal / 'resize-request.json'),
                        'containersAfter': final, 'memoryAfter': memory_gate(native), 'samples': len(samples),
                        'noIdleAdmissionOrLoad': True, 'sourceEnvironmentSHA256': plan['sourceEnvironmentSHA256']}
        save(journal / 'verified.json', resize_proof)
        exported['resourceApplicationEvidencePath'] = (journal / 'verified.json').relative_to(root).as_posix()
        exported['resourceApplicationEvidenceSHA256'] = digest(journal / 'verified.json')
        require(RESOURCE.bound_profile(target_configuration, exported) == 'mysql8-v1', 'target profile binding differs')
        # Publish metadata last, after verified evidence and the registered
        # private target exist. Partial earlier phases cannot look admissible.
        save(target_metadata, exported)
        published = True
        return {'applied': True, 'environmentPath': target_metadata.name,
                'environmentSHA256': digest(target_metadata), 'targetPrivatePath': str(target_private),
                'targetPrivateSHA256': digest(target_private), 'actualBeforeBufferBytes': actual_before['bufferBytes'],
                'actualBufferBytes': facts['bufferBytes'], 'noIdleAdmissionOrLoad': True}
    except BaseException as error:
        if claimed:
            # No raw exception/subprocess text: those may contain private input.
            record = {'at': now(), 'phase': phase, 'errorClass': type(error).__name__,
                      'publicMetadataPublished': published, 'noAutomaticRollback': True,
                      'actualBefore': actual_before, 'lastObservedMySQL': last_facts,
                      'reason': str(error) if isinstance(error, ProfileError) else 'operation failed; raw details suppressed',
                      'planSHA256': digest(plan_path)}
            if local_verified:
                try:
                    record['containersObservedAfterFailure'] = {service: native.inspect(metadata['containers'][service]['id'])
                                                                for service in SERVICES}
                except BaseException:
                    record['containersObservedAfterFailure'] = None
            try:
                failure_path = (journal / 'failure.json' if journal.is_dir() and not journal.is_symlink()
                                else runtime / ('profile-failure-' + plan['nonce'] + '.private.json'))
                save(failure_path, record, private=not journal.is_dir())
            except BaseException:
                pass
        raise RuntimeError('resource transition failed; preserve journal and actual state; explicit recovery required') from None
    finally:
        try:
            native.close()
        finally:
            release_owner(runtime, owner, native)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--execute', action='store_true', help='future native mutation; absent by default')
    parser.add_argument('--capture-baseline', action='store_true', help='future native read-only capture, no mutation')
    parser.add_argument('--plan', type=Path, help='exact existing reviewed plan for --execute')
    parser.add_argument('--environment', type=Path, help='original private configuration for static planning')
    parser.add_argument('--baseline-snapshot', type=Path, help='registered sanitized original before snapshot')
    parser.add_argument('--output', type=Path, help='new static plan filename')
    args = parser.parse_args()
    require(not (args.execute and args.capture_baseline), 'execute and capture are separate explicit actions')
    if args.capture_baseline:
        require(args.environment is not None and args.output is not None
                and args.plan is args.baseline_snapshot is None, 'capture requires original env and new baseline output')
        result = capture_baseline(args.environment.resolve(), args.output.resolve(), OWNED.ROOT)
    elif args.execute:
        require(args.plan is not None and args.environment is args.baseline_snapshot is args.output is None,
                '--execute accepts only its reviewed --plan')
        result = execute(args.plan, OWNED.ROOT)
    else:
        require(args.plan is None and args.environment is not None and args.baseline_snapshot is not None
                and args.output is not None, 'static planning requires environment, baseline snapshot and new output')
        plan = build_plan(args.environment, args.baseline_snapshot, OWNED.ROOT)
        save(args.output.resolve(), plan)
        result = {'planned': True, 'plan': str(args.output.resolve()), 'planSHA256': digest(args.output),
                  'executed': False, 'noIdleAdmissionOrLoad': True}
    print(json.dumps(result))


if __name__ == '__main__':
    main()
