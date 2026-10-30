"""GO-073-CAP external sampler; never changes or kills a service.

Requires the already installed psutil. Writes bounded-rate NDJSON and an owned
stop signal for the workload supervisor if the predeclared resource cap is hit.
No command line, environment, credential, request body or SQL text is exported.
"""
import argparse
import datetime as dt
import json
import math
import os
from pathlib import Path
import subprocess
import time
import urllib.parse
import urllib.request

import psutil


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--api-pid', type=int, required=True)
    parser.add_argument('--diagnostics', required=True)
    parser.add_argument('--project', required=True)
    parser.add_argument('--container', action='append', required=True)
    parser.add_argument('--load-script', choices=['capacity-trade.js', 'go073-cap-read.js'], required=True)
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
    if args.project not in ('project-full-stack-cap-trade', 'project-full-stack-cap-read'):
        parser.error('dedicated CAP project required')
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
    for name in args.container:
        result = subprocess.run(['docker', 'inspect', '--format', '{{json .Config.Labels}}', name], capture_output=True, text=True, timeout=15, check=True)
        labels = json.loads(result.stdout)
        if labels.get('com.docker.compose.project') != args.project or labels.get('com.docker.compose.service') not in ('mysql', 'redis'):
            parser.error('container project/service ownership mismatch')
        container_ids.append(subprocess.run(['docker', 'inspect', '--format', '{{.Id}}', name], capture_output=True, text=True, timeout=15, check=True).stdout.strip())
    no_proxy = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    generator = None
    generator_identity = None
    api.cpu_percent(None)
    psutil.cpu_percent(None)
    me = psutil.Process()
    me.cpu_percent(None)
    started = time.monotonic()
    busy_samples = 0
    failed_observations = 0

    def signal(reason):
        args.stop_file.parent.mkdir(parents=True, exist_ok=True)
        try:
            with args.stop_file.open('x', encoding='utf-8') as f:
                json.dump({'at': dt.datetime.now(dt.timezone.utc).isoformat(), 'reason': reason, 'source': 'capacity-resources'}, f)
        except FileExistsError:
            pass

    with args.output.open('x', encoding='utf-8') as output:
        output.write(json.dumps({'kind': 'configuration', 'project': args.project, 'interval_seconds': args.interval, 'logical_cpus': psutil.cpu_count(), 'stop_caps': {'host_memory_percent': 88, 'api_rss_bytes': 1610612736, 'generator_rss_bytes': 2147483648, 'container_memory_percent': 85, 'host_cpu_percent_for_three_samples': 85, 'consecutive_observation_failures': 3}, 'pid': api.pid, 'containers': args.container, 'container_ids': container_ids, 'load_script': args.load_script}) + '\n')
        output.flush()
        while time.monotonic() - started < args.max_seconds and not args.done_file.exists():
            tick = time.monotonic()
            row = {'kind': 'sample', 'at': dt.datetime.now(dt.timezone.utc).isoformat(), 'elapsed_seconds': tick - started, 'errors': []}
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
            busy_samples = busy_samples + 1 if row['host']['cpu_percent'] >= 85 else 0
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
                if not generator.is_running() or generator.pid != int(identity_file['k6_pid']) or generator.create_time() != generator_identity:
                    raise ValueError('generator identity changed')
                generators.append({'pid': generator.pid, 'rss_bytes': generator.memory_info().rss, 'cpu_percent_one_core': generator.cpu_percent(None), 'threads': generator.num_threads()})
            except Exception as error:
                row['errors'].append('owned generator observation unavailable: ' + type(error).__name__)
            row['generators'] = generators
            if sum(item['rss_bytes'] for item in generators) >= 2147483648:
                signal('load generator RSS reached 2 GiB; generator limit possible')
            try:
                with no_proxy.open(args.diagnostics, timeout=2) as response:
                    body = response.read(131073)
                    if len(body) > 131072:
                        raise ValueError('diagnostics response too large')
                    row['diagnostics'] = json.loads(body)
                if row['diagnostics'].get('pid') != args.api_pid:
                    raise ValueError('diagnostics process identity mismatch')
            except Exception as error:
                row['errors'].append('diagnostics unavailable: ' + type(error).__name__)
            try:
                stats = subprocess.run(['docker', 'stats', '--no-stream', '--format', '{{json .}}', *container_ids], capture_output=True, text=True, timeout=12, check=True)
                row['containers'] = [json.loads(line) for line in stats.stdout.splitlines() if line.strip()]
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
