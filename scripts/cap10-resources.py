"""GO-073-CAP external sampler; never changes or kills a service.

Requires the already installed psutil. Writes bounded-rate NDJSON and an owned
stop signal for the workload supervisor if the predeclared resource cap is hit.
No command line, environment, credential, request body or SQL text is exported.
"""
import argparse
from concurrent.futures import ThreadPoolExecutor
import datetime as dt
import hashlib
import json
import math
import os
from pathlib import Path
import re
import subprocess
import time
import urllib.parse
import urllib.request

import psutil


CAP10_PROJECT = 'project-full-stack-cap10'
CAP10_LIMITS = {
    'mysql': (4_000_000_000, 2 << 30, '3306/tcp', '3481', '/var/lib/mysql'),
    'redis': (2_000_000_000, 512 << 20, '6379/tcp', '6581', '/data'),
    'kafka': (2_000_000_000, 1 << 30, '9092/tcp', '9401', '/var/lib/kafka/data'),
    'elasticsearch': (2_000_000_000, 1 << 30, '9200/tcp', '9281', '/usr/share/elasticsearch/data'),
}


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, response, code, message, headers, new_url):
        return None


def cap10_container(expected, actual, service):
    """Validate selected inspect fields; never serialize a container environment."""
    cpu, memory, port, host_port, destination = CAP10_LIMITS[service]
    if not re.fullmatch(r'[a-f0-9]{64}', expected.get('id', '')) or actual.get('id') != expected['id']:
        raise ValueError('CAP10 immutable container identity differs')
    labels = actual.get('labels', {})
    if labels.get('com.docker.compose.project') != CAP10_PROJECT or labels.get('com.docker.compose.service') != service:
        raise ValueError('CAP10 project/service differs')
    if actual.get('running') is not True or actual.get('cpuNano') != cpu or actual.get('memoryBytes') != memory:
        raise ValueError('CAP10 state/resource limit differs')
    if expected.get('cpuNano') != cpu or expected.get('memoryBytes') != memory:
        raise ValueError('CAP10 recorded resource limit differs')
    bindings = actual.get('ports', {}).get(port, []) or []
    if not any(value.get('HostIp') == '127.0.0.1' and value.get('HostPort') == host_port for value in bindings):
        raise ValueError('CAP10 loopback binding differs')
    network = CAP10_PROJECT + '_default'
    if actual.get('networkMode') != network or set(actual.get('networks', {})) != {network}:
        raise ValueError('CAP10 network namespace differs')
    volumes = sorted((value.get('Name'), value.get('Destination')) for value in actual.get('mounts', []) if value.get('Type') == 'volume')
    recorded = sorted((value.get('name'), value.get('destination')) for value in expected.get('volumes', []))
    if volumes != recorded:
        raise ValueError('CAP10 volume/mount differs')
    data_volumes = [value for value in volumes if value[1] == destination]
    if len(data_volumes) != 1:
        raise ValueError('CAP10 data volume/mount differs')
    data_volume = data_volumes[0]
    if service == 'kafka':
        anonymous = [value for value in volumes if value[1] != destination]
        if (len(actual.get('mounts', [])) != 3 or any(value.get('Type') != 'volume' for value in actual.get('mounts', [])) or
                len(volumes) != 3 or len({value[0] for value in volumes}) != 3 or
                {value[1] for value in anonymous} != {'/etc/kafka/secrets', '/mnt/shared/config'} or
                not all(re.fullmatch(r'[a-f0-9]{64}', value[0] or '') for value in anonymous)):
            raise ValueError('CAP10 Kafka image-created anonymous volumes differ')
    elif len(volumes) != 1:
        raise ValueError('CAP10 unexpected extra volume/mount')
    match = re.fullmatch(re.escape(CAP10_PROJECT) + r'-([a-f0-9]{12})-' + service, data_volume[0] or '')
    if not match:
        raise ValueError('CAP10 fresh volume namespace differs')
    return {'service': service, 'id': expected['id'], 'cpuNano': cpu, 'memoryBytes': memory,
            'volume': data_volume[0], 'destination': destination,
            'volumes': [{'name': name, 'destination': target} for name, target in volumes],
            'nonce': match.group(1), 'network': network}


def cap10_stats(rows, identities):
    """Missing service rows stay failures; do not turn them into zero use."""
    by_name = {CAP10_PROJECT + '-' + row['service'] + '-1': row for row in identities}
    if len(rows) != 4 or len({row.get('Name') for row in rows}) != 4 or set(row.get('Name') for row in rows) != set(by_name):
        raise ValueError('CAP10 stats missing/duplicate/foreign container')
    for row in rows:
        identifier = row.get('ID', '')
        if len(identifier) < 12 or not by_name[row['Name']]['id'].startswith(identifier):
            raise ValueError('CAP10 stats immutable identity differs')
        value = float(row['MemPerc'].rstrip('%'))
        if not math.isfinite(value) or value < 0:
            raise ValueError('CAP10 memory percentage unavailable')


CPU_LAYOUTS = {}


def cpu_layout(cid, proc_text):
    """Resolve the verified process's CPU mount; never read the VM root group."""
    memberships = []
    mounts = []
    for line in proc_text.splitlines():
        if re.match(r'^\d+:[^ ]*:', line):
            _, controllers, membership = line.split(':', 2)
            memberships.append((set(controllers.split(',')), membership))
        elif ' - cgroup' in line:
            fields = line.split()
            dash = fields.index('-')
            mounts.append((fields[3], fields[4], fields[dash + 1], set(fields[dash + 3].split(','))))
    cpu = [(controllers, path) for controllers, path in memberships if 'cpu' in controllers]
    version = 1 if cpu else 2
    selected = cpu if cpu else [(controllers, path) for controllers, path in memberships if controllers == {''}]
    if len(selected) != 1:
        raise ValueError('CPU cgroup membership is ambiguous')
    membership = selected[0][1]
    if membership not in ('/', '/docker/' + cid):
        raise ValueError('CPU cgroup does not identify this exact container')
    if version == 1:
        accounting = [path for controllers, path in memberships if 'cpuacct' in controllers]
        if accounting != [membership]:
            raise ValueError('CPU accounting membership differs from target CPU group')
    def directory(controller):
        if version == 1:
            matches = [(root, point) for root, point, kind, controllers in mounts
                       if kind == 'cgroup' and controller in controllers]
        else:
            matches = [(root, point) for root, point, kind, _ in mounts if kind == 'cgroup2']
        if len(matches) != 1:
            raise ValueError('CPU controller mount is ambiguous')
        root, point = matches[0]
        if not point.startswith('/sys/fs/cgroup/') and point != '/sys/fs/cgroup':
            raise ValueError('unexpected CPU controller mountpoint')
        if membership == root:
            suffix = ''
        elif root == '/' and membership.startswith('/'):
            suffix = membership
        else:
            raise ValueError('CPU mount root does not contain target membership')
        return point + suffix
    directory_cpu = directory('cpu')
    if version == 1:
        paths = [directory_cpu + '/cpu.stat', directory_cpu + '/cpu.cfs_quota_us',
                 directory_cpu + '/cpu.cfs_period_us', directory('cpuacct') + '/cpuacct.usage']
    else:
        paths = [directory_cpu + '/cpu.stat', directory_cpu + '/cpu.max']
    return {'version': version, 'membership': membership, 'paths': paths}


def cpu_stat(identity):
    """Read target cgroup v1/v2 counters with original units and exact quota."""
    started = time.monotonic()
    cid = identity['id']
    if not re.fullmatch(r'[a-f0-9]{64}', cid):
        raise ValueError('full verified CID required')
    layout = CPU_LAYOUTS.get(cid)
    if layout is None:
        proc = subprocess.run(['docker', 'exec', cid, 'cat', '/proc/self/cgroup', '/proc/self/mountinfo'],
                              capture_output=True, text=True, timeout=4, check=True)
        layout = cpu_layout(cid, proc.stdout)
        CPU_LAYOUTS[cid] = layout
    result = subprocess.run(['docker', 'exec', cid, 'cat', *layout['paths']],
                            capture_output=True, text=True, timeout=4, check=True)
    lines = result.stdout.splitlines()
    if layout['version'] == 1:
        quota, period, usage_ns = map(int, lines[-3:])
        lines = lines[:-3]
    else:
        quota, period = map(int, lines.pop().split())
        usage_ns = None
    if period <= 0 or quota * 1_000_000_000 != CAP10_LIMITS[identity['service']][0] * period:
        raise ValueError('actual CPU cgroup quota differs from declared container limit')
    raw = {}
    for line in lines:
        key, value = line.split()
        if not re.fullmatch(r'[a-z_]+', key) or not value.isdigit() or key in raw:
            raise ValueError('invalid cgroup CPU counters')
        raw[key] = int(value)
    if layout['version'] == 1:
        if not {'nr_periods', 'nr_throttled', 'throttled_time'} <= raw.keys() or usage_ns < 0:
            raise ValueError('required cgroup-v1 CPU counters unavailable')
        counters = {'usage_usec': usage_ns // 1000, 'nr_periods': raw['nr_periods'],
                    'nr_throttled': raw['nr_throttled'], 'throttled_usec': raw['throttled_time'] // 1000}
    else:
        if not {'usage_usec', 'nr_periods', 'nr_throttled', 'throttled_usec'} <= raw.keys():
            raise ValueError('required cgroup-v2 CPU counters unavailable')
        counters = raw.copy()
    return {'service': identity['service'], 'id': cid, 'at': dt.datetime.now(dt.timezone.utc).isoformat(),
            'layout': layout, 'quotaUsec': quota, 'periodUsec': period,
            'rawCounters': raw, 'cpuacctUsageNanoseconds': usage_ns,
            'counters': counters, 'read_elapsed_ms': (time.monotonic() - started) * 1000}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--api-pid', type=int, required=True)
    parser.add_argument('--diagnostics', required=True)
    parser.add_argument('--project', required=True)
    parser.add_argument('--cohort', choices=('legacy', 'cap10'), default='legacy')
    parser.add_argument('--ownership', type=Path)
    parser.add_argument('--container', action='append', required=True)
    parser.add_argument('--load-script', choices=['capacity-trade.js', 'go073-cap-read.js', 'capacity-health.js', 'cap10-like.js'], required=True)
    parser.add_argument('--load-pid-file', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--stop-file', type=Path, required=True)
    parser.add_argument('--done-file', type=Path, required=True)
    parser.add_argument('--interval', type=float, default=5)
    parser.add_argument('--max-seconds', type=int, default=900)
    args = parser.parse_args()
    url = urllib.parse.urlsplit(args.diagnostics)
    if url.scheme != 'http' or url.hostname != '127.0.0.1' or url.path != '/debug/capacity' or url.username or url.password:
        parser.error('explicit loopback diagnostics URL required')
    if args.cohort == 'cap10':
        if args.project != CAP10_PROJECT or args.load_script != 'cap10-like.js' or args.diagnostics != 'http://127.0.0.1:8381/debug/capacity':
            parser.error('fixed CAP10 project/script/diagnostics required')
        expected_path = Path(__file__).resolve().parent.parent / 'docs/evidence/2026-11-01-cap10/environment.json'
        if args.ownership is None or args.ownership.resolve() != expected_path or not expected_path.is_file():
            parser.error('fixed CAP10 public ownership manifest required')
        ownership = json.loads(expected_path.read_text(encoding='utf-8-sig'))
        if ownership.get('project') != CAP10_PROJECT or set(ownership.get('containers', {})) != set(CAP10_LIMITS):
            parser.error('CAP10 ownership services differ')
        expected_ids = {value['id'] for value in ownership['containers'].values()}
        if len(args.container) != 4 or set(args.container) != expected_ids or not all(re.fullmatch(r'[a-f0-9]{64}', value) for value in args.container):
            parser.error('four exact CAP10 container IDs required')
    elif args.project not in ('project-full-stack-cap-trade', 'project-full-stack-cap-read') or args.load_script == 'cap10-like.js':
        parser.error('dedicated legacy CAP project/script required')
    if not math.isfinite(args.interval) or args.interval < 2 or args.interval > 30 or args.max_seconds < 1 or args.max_seconds > 3600:
        parser.error('invalid sampling interval/duration')
    if len({p.resolve() for p in (args.output, args.stop_file, args.done_file, args.load_pid_file)}) != 4:
        parser.error('output, stop, done and PID paths must be distinct')
    if any(p.exists() for p in (args.output, args.stop_file, args.done_file)):
        parser.error('new output and signal paths required; preserve earlier records')
    args.output.parent.mkdir(parents=True, exist_ok=True)
    api = psutil.Process(args.api_pid)
    api_identity = api.create_time()
    container_ids = []
    identities = []
    for name in args.container:
        if args.cohort == 'cap10':
            template = ('{"id":{{json .Id}},"labels":{{json .Config.Labels}},"running":{{json .State.Running}},'
                        '"ports":{{json .HostConfig.PortBindings}},"cpuNano":{{json .HostConfig.NanoCpus}},'
                        '"memoryBytes":{{json .HostConfig.Memory}},"networkMode":{{json .HostConfig.NetworkMode}},'
                        '"networks":{{json .NetworkSettings.Networks}},"mounts":{{json .Mounts}}}')
            result = subprocess.run(['docker', 'inspect', '--format', template, name], capture_output=True, text=True, timeout=15, check=True)
            actual = json.loads(result.stdout)
            services = [service for service, value in ownership['containers'].items() if value['id'] == name]
            if len(services) != 1:
                parser.error('CAP10 manifest container identity duplicated')
            identities.append(cap10_container(ownership['containers'][services[0]], actual, services[0]))
            container_ids.append(name)
            continue
        result = subprocess.run(['docker', 'inspect', '--format', '{{json .Config.Labels}}', name], capture_output=True, text=True, timeout=15, check=True)
        labels = json.loads(result.stdout)
        if labels.get('com.docker.compose.project') != args.project or labels.get('com.docker.compose.service') not in ('mysql', 'redis'):
            parser.error('container project/service ownership mismatch')
        container_ids.append(subprocess.run(['docker', 'inspect', '--format', '{{.Id}}', name], capture_output=True, text=True, timeout=15, check=True).stdout.strip())
    if args.cohort == 'cap10' and len({value['nonce'] for value in identities}) != 1:
        parser.error('CAP10 containers are not from one fresh-volume cohort')
    handlers = [urllib.request.ProxyHandler({})]
    if args.cohort == 'cap10':
        handlers.append(NoRedirect())
    no_proxy = urllib.request.build_opener(*handlers)
    generator = None
    generator_identity = None
    generator_cpu_at = None
    helper = None
    helper_identity = None
    helper_cpu_at = None
    api.cpu_percent(None)
    psutil.cpu_percent(None)
    me = psutil.Process()
    me.cpu_percent(None)
    cpu_at = time.monotonic()
    started = time.monotonic()
    busy_samples = 0
    failed_observations = 0
    previous_cpu_stats = {}

    def signal(reason):
        args.stop_file.parent.mkdir(parents=True, exist_ok=True)
        try:
            with args.stop_file.open('x', encoding='utf-8') as f:
                json.dump({'at': dt.datetime.now(dt.timezone.utc).isoformat(), 'reason': reason, 'source': 'capacity-resources'}, f)
        except FileExistsError:
            pass

    with args.output.open('x', encoding='utf-8') as output:
        output.write(json.dumps({'kind': 'configuration', 'project': args.project, 'interval_seconds': args.interval, 'logical_cpus': psutil.cpu_count(), 'sampler_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest(), 'cpu_minimum_interval_seconds': 1, 'stop_caps': {'host_memory_percent': 88, 'api_rss_bytes': 1610612736, 'generator_rss_bytes': 2147483648, 'container_memory_percent': 85, 'host_cpu_percent_for_three_samples': 85, 'consecutive_observation_failures': 3}, 'pid': api.pid, 'containers': args.container, 'container_ids': container_ids, 'load_script': args.load_script}) + '\n')
        if args.cohort == 'cap10':
            output.write(json.dumps({'kind': 'ownership', 'cohort': 'cap10', 'ownership': str(args.ownership),
                                     'ownership_sha256': hashlib.sha256(args.ownership.read_bytes()).hexdigest(),
                                     'verified_containers': identities,
                                     'load_rss_scope': 'CAP10 2GiB cap covers k6 plus its Node workload/classification helper; both processes reported separately'}) + '\n')
        output.flush()
        while time.monotonic() - started < args.max_seconds and not args.done_file.exists():
            tick = time.monotonic()
            cpu_interval = tick - cpu_at
            cpu_at = tick
            row = {'kind': 'sample', 'at': dt.datetime.now(dt.timezone.utc).isoformat(), 'elapsed_seconds': tick - started, 'cpu_interval_seconds': cpu_interval, 'cpu_interval_valid': cpu_interval >= 1, 'errors': []}
            try:
                if not api.is_running() or api.create_time() != api_identity:
                    raise RuntimeError('API PID identity changed')
                row['api'] = {'pid': api.pid, 'rss_bytes': api.memory_info().rss, 'cpu_percent_one_core': api.cpu_percent(None), 'threads': api.num_threads()}
                if row['api']['rss_bytes'] >= 1610612736:
                    signal('API RSS reached 1.5 GiB')
            except (psutil.Error, RuntimeError) as error:
                row['errors'].append('API identity/process unavailable: ' + type(error).__name__)
                signal('API process unavailable')
            row['host'] = {'cpu_percent': psutil.cpu_percent(None), 'memory_percent': psutil.virtual_memory().percent, 'available_memory_bytes': psutil.virtual_memory().available}
            # Preserve the raw first value, but a sub-second priming interval
            # cannot establish a steady CPU peak or a high-CPU budget window.
            busy_samples = busy_samples + 1 if row['cpu_interval_valid'] and row['host']['cpu_percent'] >= 85 else 0
            if row['host']['memory_percent'] >= 88 or busy_samples >= 3:
                signal('shared host resource budget reached; not a server-only capacity bound')
            generators = []
            try:
                identity_file = json.loads(args.load_pid_file.read_text(encoding='utf-8-sig'))
                if generator is None:
                    candidate = psutil.Process(int(identity_file['k6_pid']))
                    created = candidate.create_time()
                    if candidate.name().lower() not in ('k6', 'k6.exe') or not any(args.load_script in arg for arg in candidate.cmdline()):
                        raise ValueError('generator executable/script identity mismatch')
                    if abs(created * 1000 - identity_file['k6_started_unix_ms']) > 2000:
                        raise ValueError('generator creation time mismatch')
                    candidate.cpu_percent(None)
                    generator, generator_identity = candidate, created
                    generator_cpu_at = time.monotonic()
                if not generator.is_running() or generator.pid != int(identity_file['k6_pid']) or generator.create_time() != generator_identity:
                    raise ValueError('generator identity changed')
                generator_now = time.monotonic()
                generator_interval = generator_now - generator_cpu_at
                generators.append({'pid': generator.pid, 'rss_bytes': generator.memory_info().rss, 'cpu_percent_one_core': generator.cpu_percent(None), 'cpu_interval_seconds': generator_interval, 'cpu_interval_valid': generator_interval >= 1, 'threads': generator.num_threads()})
                generator_cpu_at = generator_now
            except Exception as error:
                row['errors'].append('owned generator observation unavailable: ' + type(error).__name__)
            row['generators'] = generators
            if sum(item['rss_bytes'] for item in generators) >= 2147483648:
                signal('load generator RSS reached 2 GiB; generator limit possible')
            if args.cohort == 'cap10':
                try:
                    if helper is None:
                        candidate_helper = psutil.Process(int(identity_file['helper_pid']))
                        expected_helper = 'cap10-like.mjs' if args.load_script == 'cap10-like.js' else 'go073-cap-read.mjs'
                        if candidate_helper.name().lower() not in ('node', 'node.exe') or not any(expected_helper in value for value in candidate_helper.cmdline()):
                            raise ValueError('CAP10 helper executable/script differs')
                        helper = candidate_helper
                        helper_identity = helper.create_time()
                        helper.cpu_percent(None)
                        helper_cpu_at = time.monotonic()
                    if not helper.is_running() or helper.pid != int(identity_file['helper_pid']) or helper.create_time() != helper_identity:
                        raise ValueError('CAP10 helper identity changed')
                    helper_now = time.monotonic()
                    helper_interval = helper_now - helper_cpu_at
                    row['helper'] = {'pid': helper.pid, 'rss_bytes': helper.memory_info().rss,
                                     'cpu_percent_one_core': helper.cpu_percent(None), 'cpu_interval_seconds': helper_interval,
                                     'cpu_interval_valid': helper_interval >= 1, 'threads': helper.num_threads()}
                    helper_cpu_at = helper_now
                    if row['helper']['rss_bytes'] + sum(item['rss_bytes'] for item in generators) >= 2147483648:
                        signal('CAP10 k6 and classification helper RSS reached 2 GiB')
                except Exception as error:
                    row['errors'].append('CAP10 helper observation unavailable: ' + type(error).__name__)
            try:
                with no_proxy.open(args.diagnostics, timeout=2) as response:
                    # CAP-06 adds a fixed 32-trace/256-span diagnostic ring.
                    # Bound the whole response, including this opt-in evidence.
                    body = response.read(2 * 1024 * 1024 + 1)
                    if len(body) > 2 * 1024 * 1024:
                        raise ValueError('diagnostics response too large')
                    row['diagnostics'] = json.loads(body)
                if row['diagnostics'].get('pid') != args.api_pid:
                    raise ValueError('diagnostics process identity mismatch')
            except Exception as error:
                row['errors'].append('diagnostics unavailable: ' + type(error).__name__)
            try:
                stats = subprocess.run(['docker', 'stats', '--no-stream', '--format', '{{json .}}', *container_ids], capture_output=True, text=True, timeout=12, check=True)
                row['containers'] = [json.loads(line) for line in stats.stdout.splitlines() if line.strip()]
                if args.cohort == 'cap10':
                    cap10_stats(row['containers'], identities)
                    with ThreadPoolExecutor(max_workers=4) as workers:
                        cpu_rows = list(workers.map(cpu_stat, identities))
                    for item in cpu_rows:
                        previous = previous_cpu_stats.get(item['service'])
                        counters = item['counters']
                        if previous is not None:
                            if any(counters[key] < previous[key] for key in previous):
                                raise ValueError('cgroup CPU counters reset during stage')
                            item['delta'] = {key: counters[key] - previous[key] for key in previous}
                        else:
                            item['delta'] = None
                        previous_cpu_stats[item['service']] = counters
                    row['container_cpu_stat'] = cpu_rows
                for item in row['containers']:
                    if float(item['MemPerc'].rstrip('%')) >= 85:
                        signal('owned container memory budget reached: ' + item.get('Name', 'unknown'))
            except Exception as error:
                row['errors'].append('container stats unavailable: ' + type(error).__name__)
            failed_observations = failed_observations + 1 if row['errors'] else 0
            if failed_observations >= 3:
                signal('three consecutive incomplete observations; capacity result not measurable')
            row['sampler'] = {'pid': os.getpid(), 'rss_bytes': me.memory_info().rss, 'cpu_percent_one_core': me.cpu_percent(None), 'sample_elapsed_ms': (time.monotonic() - tick) * 1000}
            output.write(json.dumps(row, ensure_ascii=False) + '\n')
            output.flush()
            if args.stop_file.exists():
                break
            deadline = tick + args.interval
            while time.monotonic() < deadline and not args.done_file.exists():
                time.sleep(min(.2, max(0, deadline - time.monotonic())))
        output.write(json.dumps({'kind': 'end', 'at': dt.datetime.now(dt.timezone.utc).isoformat(), 'elapsed_seconds': time.monotonic() - started, 'stop_signal': args.stop_file.exists(), 'done_signal': args.done_file.exists()}) + '\n')


if __name__ == '__main__':
    main()
