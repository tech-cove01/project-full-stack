"""One owned CAP10 60-second idle observation. Never starts a workload."""
import argparse
from concurrent.futures import ThreadPoolExecutor
import datetime as dt
import hashlib
import importlib.util
import json
from pathlib import Path
import statistics
import subprocess
import time
import xml.etree.ElementTree as ET

import psutil


REPO = Path(__file__).resolve().parent.parent


def module(name, filename):
    spec = importlib.util.spec_from_file_location(name, REPO / 'scripts' / filename)
    value = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    return value


def sleep_events(since):
    query = "*[System[Provider[@Name='Microsoft-Windows-Kernel-Power'] and (EventID=506 or EventID=507) and TimeCreated[@SystemTime >= '" + since.replace('+00:00', 'Z') + "']]]"
    result = subprocess.run(['wevtutil', 'qe', 'System', '/q:' + query, '/f:xml', '/rd:true', '/c:32', '/e:Events'],
                            capture_output=True, creationflags=subprocess.CREATE_NO_WINDOW, timeout=15)
    if result.returncode:
        return {'available': False, 'events': []}
    if not result.stdout.strip():
        return {'available': True, 'events': []}
    text = result.stdout.decode('utf-16' if result.stdout.startswith(b'\xff\xfe') else 'utf-8-sig')
    root = ET.fromstring(text)
    ns = {'e': 'http://schemas.microsoft.com/win/2004/08/events/event'}
    rows = []
    for event in root.findall('e:Event', ns):
        system = event.find('e:System', ns)
        rows.append({'id': int(system.find('e:EventID', ns).text),
                     'at': system.find('e:TimeCreated', ns).attrib['SystemTime']})
    return {'available': True, 'events': rows}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--environment', required=True)
    parser.add_argument('--api-metadata', required=True)
    parser.add_argument('--observer', required=True)
    args = parser.parse_args()
    owned = module('cap10_idle_owned', 'cap10-env.py')
    resources = module('cap10_idle_resources', 'cap10-resources.py')
    value, runtime = owned.settings(args.environment)
    root = owned.ROOT
    api_metadata = owned.read(args.api_metadata)
    api = owned.api_identity(api_metadata, health=True)
    assert api_metadata['privateEnvironmentFile'] == str(Path(args.environment).resolve())
    assert not list(root.glob('slot-*.json')), 'idle observation must precede S1'
    reservation = root / 'idle-reservation.json'
    owned.save(reservation, {'at': owned.now(), 'seconds': 60, 'intervalSeconds': 5,
                            'attempt': 1, 'failedAttemptConsumesOnlyIdleObservation': True})
    owned_containers = owned.read(root / 'environment.json')['containers']
    for service, container in owned_containers.items():
        checked = owned.container(service, container['id'], True)
        assert checked['volumes'] == container['volumes']
    identities = [{'id': c['id'], 'service': service} for service, c in owned_containers.items()]
    control = root / 'idle-control'
    control.mkdir()
    done = control / 'done.json'
    downstream = root / 'idle-downstream'
    result = {'at': owned.now(), 'passed': False, 'seconds': 60, 'samples': [], 'errors': [],
              'noWorkloadStarted': True, 'apiPid': api.pid, 'sourceSha': api_metadata['sourceSha'],
              'environmentSHA256': hashlib.sha256((root / 'environment.json').read_bytes()).hexdigest()}
    observer = None
    try:
        with (root / 'idle-observer.txt').open('xb') as output:
            observer = subprocess.Popen([str(Path(args.observer).resolve()), '--environment', args.environment,
                '--output', str(downstream), '--done-file', str(done),
                '--like-fixture', value['CAP_LIKE_FIXTURE'], '--max-seconds', '150'],
                cwd=REPO, stdout=output, stderr=subprocess.STDOUT, creationflags=owned.FLAGS)
            for _ in range(80):
                if (downstream / 'ready.json').exists() or observer.poll() is not None:
                    break
                time.sleep(.25)
            assert observer.poll() is None and owned.read(downstream / 'ready.json')['ready']
            psutil.cpu_percent(None)
            api.cpu_percent(None)
            started = time.monotonic()
            previous_tick = started
            uptime_rows = []
            for index in range(13):
                deadline = started + index * 5
                if deadline > time.monotonic():
                    time.sleep(deadline - time.monotonic())
                tick = time.monotonic()
                row = {'at': owned.now(), 'elapsedSeconds': tick - started,
                       'cpuIntervalSeconds': tick - previous_tick, 'cpuIntervalValid': index > 0 and tick - previous_tick >= 1,
                       'hostCPUPercent': psutil.cpu_percent(None), 'power': owned.power(),
                       'apiRSSBytes': api.memory_info().rss, 'apiCPUPercentOneCore': api.cpu_percent(None)}
                previous_tick = tick
                state_output = owned.run(['docker', 'inspect', '--format',
                    '{"id":{{json .Id}},"state":{{json .State}},"restartCount":{{json .RestartCount}}}',
                    *[c['id'] for c in owned_containers.values()]]).stdout
                states = [json.loads(line) for line in state_output.splitlines() if line.strip()]
                assert {s['id'] for s in states} == {c['id'] for c in owned_containers.values()}
                assert all(s['state']['Running'] and not s['state']['OOMKilled'] and
                           s['state']['Health']['Status'] == 'healthy' and s['restartCount'] == 0 for s in states)
                row['containerStates'] = states
                stats = owned.run(['docker', 'stats', '--no-stream', '--format', '{{json .}}',
                                   *[c['id'] for c in owned_containers.values()]])
                row['containerResources'] = [json.loads(s) for s in stats.stdout.splitlines() if s.strip()]
                resources.cap10_stats(row['containerResources'], identities)
                with ThreadPoolExecutor(max_workers=4) as workers:
                    row['containerCPUStat'] = list(workers.map(resources.cpu_stat, identities))
                row['capacity'] = owned.get_json('http://127.0.0.1:8381/debug/capacity')
                assert row['capacity']['pid'] == api.pid
                if index in (0, 12):
                    before = time.monotonic()
                    sql = owned.run([value['MYSQL_BIN'], '--protocol=TCP', '--host=127.0.0.1', '--port=3481',
                        '--user=' + value['CAP_READ_OBSERVER_USER'], '--batch', '--skip-column-names'],
                        env={**owned.os.environ, 'MYSQL_PWD': value['CAP_READ_OBSERVER_PASSWORD']},
                        input="SHOW GLOBAL STATUS LIKE 'Uptime';\n")
                    after = time.monotonic()
                    uptime_rows.append({'uptimeSeconds': int(sql.stdout.strip().split()[-1]),
                                        'midpointMonotonic': (before + after) / 2,
                                        'uncertaintySeconds': (after - before) / 2})
                row['observationElapsedMs'] = (time.monotonic() - tick) * 1000
                result['samples'].append(row)
                with (root / 'idle-samples.ndjson').open('a', encoding='utf-8', newline='\n') as samples:
                    samples.write(json.dumps(row, ensure_ascii=False) + '\n')
            owned.save(done, {'at': owned.now(), 'reason': '60-second idle observation complete; no load'})
            result['observerExit'] = observer.wait(timeout=15)
            assert result['observerExit'] == 0
            timeline = [json.loads(line) for line in (downstream / 'timeline.ndjson').read_text(encoding='utf-8').splitlines() if line]
            assert len(timeline) >= 12
            assert all(not s['errors'] and not s['semanticViolations'] and
                       all(s['sql']['counts'][key] == 0 for key in ('events', 'pending', 'processing', 'needsReview', 'other')) and
                       s['sql']['parked'] == 0 and len(s['sql']['receipts']) == 3 and
                       all(r['missing'] == 0 for r in s['sql']['receipts']) and
                       len(s['kafka']['groups']) == 3 and all(g['initialized'] and g['lag'] == 0 for g in s['kafka']['groups'])
                       for s in timeline)
            result['downstreamSamples'] = len(timeline)
            result['downstreamIdleAndComplete'] = True
            for service, container in owned_containers.items():
                checked = owned.container(service, container['id'], True)
                assert checked['volumes'] == container['volumes']
            result['powerEvents'] = sleep_events(result['at'])
            result['uptime'] = uptime_rows
            elapsed = uptime_rows[1]['midpointMonotonic'] - uptime_rows[0]['midpointMonotonic']
            delta = uptime_rows[1]['uptimeSeconds'] - uptime_rows[0]['uptimeSeconds']
            result['uptimeToleranceSeconds'] = 2 + sum(r['uncertaintySeconds'] for r in uptime_rows)
            result['uptimeValid'] = abs(delta - elapsed) <= result['uptimeToleranceSeconds']
            cpus = [r['hostCPUPercent'] for r in result['samples'] if r['cpuIntervalValid']]
            result['hostCPUMedian'] = statistics.median(cpus)
            result['hostMemoryMaxPercent'] = max(r['power']['hostMemoryPercent'] for r in result['samples'])
            result['threeConsecutiveBusy'] = any(all(x >= 85 for x in cpus[i:i + 3]) for i in range(len(cpus) - 2))
            result['cadenceIntervalBoundsSeconds'] = [4, 7.5]
            result['cadenceValid'] = (len(result['samples']) == 13 and len(cpus) == 12 and
                result['samples'][0]['elapsedSeconds'] < 1 and
                60 <= result['samples'][-1]['elapsedSeconds'] <= 62.5 and
                all(4 <= r['cpuIntervalSeconds'] <= 7.5 for r in result['samples'][1:]))
            result['passed'] = (result['hostCPUMedian'] <= 50 and result['hostMemoryMaxPercent'] <= 80 and
                result['cadenceValid'] and not result['threeConsecutiveBusy'] and result['uptimeValid'] and
                result['powerEvents']['available'] and not result['powerEvents']['events'] and
                all(r['power']['ac'] == 1 and r['power']['batterySaver'] == 0 and
                    r['observationElapsedMs'] < 5000 and r['apiRSSBytes'] < 1610612736 and
                    all(float(c['MemPerc'].rstrip('%')) < 85 for c in r['containerResources'])
                    for r in result['samples']))
    except Exception as error:
        result['errors'].append({'class': type(error).__name__, 'detailsSuppressed': True})
    finally:
        if observer is not None and observer.poll() is None:
            if not done.exists():
                owned.save(done, {'at': owned.now(), 'reason': 'idle observation failed; no load'})
            try:
                observer.wait(timeout=15)
            except subprocess.TimeoutExpired:
                observer.terminate()
                observer.wait(timeout=5)
        result['finishedAt'] = owned.now()
        owned.save(root / 'idle-result.json', result)
    print(json.dumps({'idlePassed': result['passed'], 'samples': len(result['samples']),
                      'hostMemoryMaxPercent': result.get('hostMemoryMaxPercent'),
                      'hostCPUMedian': result.get('hostCPUMedian'), 'errors': result['errors']}))
    raise SystemExit(0 if result['passed'] else 1)


if __name__ == '__main__':
    main()
