"""Execute one bounded CAP09 stage with the existing owned supervisor."""
import argparse
import ctypes
import datetime as dt
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import time
import xml.etree.ElementTree as ET

import psutil

REPO = Path(__file__).resolve().parent.parent


def module(name, filename):
    spec = importlib.util.spec_from_file_location(name, REPO / 'scripts' / filename)
    value = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    return value


owned = module('cap09_owned_run', 'cap09-env.py')
ROOT = owned.ROOT
LIMITS = {'probe': 1, 'baseline-stair': 7, 'baseline-hot': 1,
          'diagnostic': 2, 'final-stair': 7, 'hold': 2, 'final-hot': 1}
TOOL_NAMES = ('cap09-run.py', 'cap09-grade.py', 'cap09-env.py', 'capacity-stage.py', 'capacity-resources.py',
              'go073-cap-read.mjs', 'load/go073-cap-read.js', 'load/cap09-feed-contract.js',
              'load/cap09-native-window.mjs',
              'cap09-like.mjs', 'load/cap09-like.js', 'load/cap09-like-contract.mjs', 'cap09-downstream.go')


def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def sleep_events(since):
    query = "*[System[Provider[@Name='Microsoft-Windows-Kernel-Power'] and (EventID=506 or EventID=507) and TimeCreated[@SystemTime >= '" + since.replace('+00:00', 'Z') + "']]]"
    result = subprocess.run(['wevtutil', 'qe', 'System', '/q:' + query, '/f:xml', '/rd:true', '/c:32', '/e:Events'],
                            capture_output=True, creationflags=owned.FLAGS, timeout=15)
    if result.returncode:
        return {'available': False, 'events': []}
    if not result.stdout.strip():
        return {'available': True, 'events': []}
    text = result.stdout.decode('utf-16' if result.stdout.startswith(b'\xff\xfe') else 'utf-8-sig')
    root = ET.fromstring(text); ns = {'e': 'http://schemas.microsoft.com/win/2004/08/events/event'}
    rows = []
    for event in root.findall('e:Event', ns):
        system = event.find('e:System', ns)
        rows.append({'id': int(system.find('e:EventID', ns).text),
                     'recordId': int(system.find('e:EventRecordID', ns).text),
                     'at': system.find('e:TimeCreated', ns).attrib['SystemTime']})
    return {'available': True, 'events': rows}


def reserve(args, api, value):
    grade = module('cap09_reservation_grade', 'cap09-grade.py')
    fixture = value['CAP_READ_FIXTURE' if args.chain == 'feed' else 'CAP_LIKE_FIXTURE']
    prediction = owned.read(args.prediction) if args.prediction else None
    if args.prediction:
        assert args.prediction.resolve().parent == ROOT
    freeze_path = ROOT / ('freeze-' + args.chain + '.json')
    freeze = owned.read(freeze_path) if args.kind == 'hold' else None
    reservation = grade.check_reservation(ROOT, args.chain, args.kind, args.rate, args.seconds,
        args.mode, api['sourceSha'], api, fixture, freeze=freeze, prediction=prediction)
    records = [owned.read(p) for p in ROOT.glob('*-command.json')]
    previous = sorted((r for r in records if r.get('chain') == args.chain and r.get('kind') == args.kind), key=lambda x: x['at'])
    for record in previous:
        terminal = owned.read(ROOT / (record['tag'] + '-stage.json'))
        assert terminal['childrenConverged'], 'prior owned children have not converged'
    tag = reservation['tag']
    command = {'task': 'CAP-09', 'at': owned.now(), 'chain': args.chain, 'kind': args.kind, 'tag': tag,
               'mode': args.mode, 'rate': args.rate, 'seconds': args.seconds,
               'warmSeconds': 1 if args.kind == 'probe' else 10, 'warmRate': 1 if args.kind == 'probe' else 25,
               'preVUs': 2 if args.kind == 'probe' else 256, 'maxVUs': 2 if args.kind == 'probe' else 512,
               'apiSourceSha': api['sourceSha'], 'apiPid': api['pid'], 'binarySHA256': api['sha256'],
               'feedTailEnabled': api['tailEnabledRequested'],
               'environmentSHA256': sha(ROOT / 'environment.json'),
               'fixtureSHA256': sha(value['CAP_READ_FIXTURE' if args.chain == 'feed' else 'CAP_LIKE_FIXTURE']),
               'toolSourceSha': owned.run(['git', 'rev-parse', 'HEAD']).stdout.strip(),
               'scriptHashes': {name: sha(REPO / 'scripts' / name) for name in TOOL_NAMES},
               'observerBinarySHA256': sha(args.observer), 'stopAfterSeconds': args.stop_after,
               'reservationCheck': reservation,
               'freezeSHA256': sha(freeze_path) if freeze else None,
               'predictionSHA256': sha(args.prediction) if args.prediction else None,
               'failedStartsConsumeSlot': True}
    owned.save(ROOT / ('slot-' + tag + '.json'), command)
    owned.save(ROOT / (tag + '-command.json'), command)
    return command


def remember_children(wrapper, identities):
    if wrapper is None or wrapper.poll() is not None:
        return
    try:
        candidates = [psutil.Process(wrapper.pid)]
        candidates.extend(candidates[0].children(recursive=True))
        for process in candidates:
            try:
                key = str(process.pid) + ':' + str(process.create_time())
                identities[key] = {'pid': process.pid, 'created': process.create_time(), 'name': process.name()}
            except psutil.NoSuchProcess:
                pass
    except psutil.NoSuchProcess:
        pass


def children_converged(wrapper, observer, control, load, identities):
    """Do not release exclusivity on an outer wrapper exit alone."""
    if observer is not None and observer.poll() is None:
        return False
    if wrapper is None:
        return True
    if wrapper.poll() is None or not (control / 'done.json').is_file() or not (control / 'exit-status.json').is_file():
        return False
    try:
        status = owned.read(control / 'exit-status.json')
        if (not status.get('ended_at') or type(status.get('harness_exit')) is not int or
            type(status.get('sampler_started')) is not bool or
            (status['sampler_started'] and type(status.get('sampler_exit')) is not int)):
            return False
        for identity in identities.values():
            try:
                if psutil.Process(identity['pid']).create_time() == identity['created']:
                    return False
            except psutil.NoSuchProcess:
                pass
        # Markers also cover short-lived descendants missed between ticks.
        for marker in load.glob('*-process.json'):
            detail = owned.read(marker)
            try:
                if abs(psutil.Process(detail['k6_pid']).create_time() * 1000 - detail['k6_started_unix_ms']) <= 2000:
                    return False
            except psutil.NoSuchProcess:
                pass
        for field in ('helper_launcher_pid', 'sampler_pid', 'sampled_k6_pid'):
            pid = status.get(field)
            if pid is None or not psutil.pid_exists(pid):
                continue
            matches = [v for v in identities.values() if v['pid'] == pid]
            if not matches or any(psutil.Process(pid).create_time() == v['created'] for v in matches):
                return False
        return True
    except (OSError, ValueError, KeyError, TypeError, psutil.Error):
        return False


def exception_stop(wrapper, observer, control, tag, identities):
    """Ask the verified existing supervisor to stop; never kill arbitrary PIDs."""
    if wrapper is not None and wrapper.poll() is None:
        if control.is_dir() and not (control / 'stop.json').exists():
            owned.save(control / 'stop.json', {'at': owned.now(), 'source': 'cap09-run',
                'tag': tag, 'reason': 'runner exception; bounded owned supervisor stop'})
        deadline = time.monotonic() + 105
        while wrapper.poll() is None and time.monotonic() < deadline:
            if control.is_dir() and not (control / 'stop.json').exists():
                owned.save(control / 'stop.json', {'at': owned.now(), 'source': 'cap09-run',
                    'tag': tag, 'reason': 'runner exception; bounded owned supervisor stop'})
            remember_children(wrapper, identities)
            time.sleep(.5)
    if observer is not None and observer.poll() is None:
        try:
            observer.wait(timeout=75)
        except subprocess.TimeoutExpired:
            pass


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--environment', type=Path, required=True)
    parser.add_argument('--api-metadata', type=Path, required=True)
    parser.add_argument('--observer', type=Path, required=True)
    parser.add_argument('--chain', choices=('feed', 'like'), required=True)
    parser.add_argument('--kind', choices=tuple(LIMITS), required=True)
    parser.add_argument('--mode', choices=('multi', 'hot'), default='multi')
    parser.add_argument('--rate', type=int, required=True); parser.add_argument('--seconds', type=int, required=True)
    parser.add_argument('--prediction', type=Path); parser.add_argument('--stop-after', type=float)
    args = parser.parse_args()
    assert Path.cwd().resolve() == REPO and args.observer.is_file()
    assert args.api_metadata.resolve().parent == ROOT
    assert args.stop_after is None or args.kind == 'probe' and 0 < args.stop_after < 8
    grade = module('cap09_native_downstream_grade', 'cap09-grade.py')
    value, runtime = owned.settings(args.environment)
    api = owned.read(args.api_metadata); owned.api_identity(api, health=True)
    assert value['CAP_SOURCE_SHA'] == api['sourceSha']
    # Only Feed has request-tail instrumentation; like comparisons keep final flags.
    assert api['tailEnabledRequested'] is (args.kind == 'diagnostic' and args.chain == 'feed')
    lock = runtime / 'cap09-active.private.json'
    assert not lock.exists(), 'prior owned stage still holds exclusivity'
    command = reserve(args, api, value); tag = command['tag']
    control, load, downstream = (ROOT / (tag + suffix) for suffix in ('-control', '-load', '-downstream'))
    owner = {'at': owned.now(), 'tag': tag, 'pid': os.getpid(), 'created': psutil.Process().create_time()}
    owned.save(lock, owner)
    state = {'at': owned.now(), 'tag': tag, 'passed': False, 'failedStartOrException': None,
             'wrapperExit': None, 'observerExit': None, 'childrenConverged': False}
    wrapper = observer = None; handles = []; observer_samples = []; intentional_stop_written = False
    identities = {}
    start = owned.now(); before_power = {}; power_changed = False
    try:
        before_power = owned.power()
        assert before_power.get('ac', 1) == 1 and before_power['hostMemoryPercent'] < 88
        if os.name == 'nt':
            assert ctypes.windll.kernel32.SetThreadExecutionState(0x80000001)
        owned.save(ROOT / (tag + '-capacity-before.json'), {'at': owned.now(), 'capacity': owned.get_json('http://127.0.0.1:8371/debug/capacity'), 'scope': 'parsed pre-warm snapshot'})
        log = (ROOT / (tag + '-observer.txt')).open('xb'); handles.append(log)
        observer = subprocess.Popen([str(args.observer.resolve()), '--environment', str(args.environment.resolve()),
            '--output', str(downstream), '--done-file', str(control / 'done.json'), '--max-seconds', '360'],
            cwd=REPO, stdout=log, stderr=subprocess.STDOUT, creationflags=owned.FLAGS)
        process = psutil.Process(observer.pid); created = process.create_time(); process.cpu_percent(None); cpu_at = time.monotonic()
        state['observerPid'] = observer.pid; deadline = time.monotonic() + 30
        while not (downstream / 'ready.json').exists() and observer.poll() is None and time.monotonic() < deadline:
            time.sleep(.1)
        assert (downstream / 'ready.json').exists() and owned.read(downstream / 'ready.json')['ready']
        native = {**value, 'CAP_STOP_FILE': str(control / 'stop.json')}
        prefix = 'CAP_READ_' if args.chain == 'feed' else 'CAP_LIKE_'
        native.update({prefix + key: str(command[field]) for key, field in
                       (('MODE', 'mode'), ('RATE', 'rate'), ('SECONDS', 'seconds'), ('WARM_SECONDS', 'warmSeconds'),
                        ('WARM_RATE', 'warmRate'), ('PRE_VUS', 'preVUs'), ('MAX_VUS', 'maxVUs'))})
        native[prefix + 'EVIDENCE'] = str(load)
        script = 'go073-cap-read.mjs' if args.chain == 'feed' else 'cap09-like.mjs'
        argv = [sys.executable, '-B', '-X', 'utf8', 'scripts/capacity-stage.py', '--cohort', 'cap09',
                '--mode', 'read' if args.chain == 'feed' else 'like', '--control-dir', str(control),
                '--api-pid', str(api['pid']), '--diagnostics', 'http://127.0.0.1:8371/debug/capacity',
                '--interval', '5', '--max-seconds', '360', '--stop-grace-seconds', '90', '--', 'node', 'scripts/' + script, 'run']
        owned.save(ROOT / (tag + '-invocation.json'), {'argv': argv, 'environmentValuesExported': False})
        log = (ROOT / (tag + '-console.txt')).open('xb'); handles.append(log)
        wrapper = subprocess.Popen(argv, cwd=REPO, env=owned.env(native), stdout=log, stderr=subprocess.STDOUT, creationflags=owned.FLAGS)
        state.update(wrapperPid=wrapper.pid, loadWaitStartedAt=owned.now())
        stable_seen = None; tick = 0
        while wrapper.poll() is None:
            remember_children(wrapper, identities)
            if observer.poll() is not None:
                summary_path = downstream / 'summary.json'
                if not ((control / 'done.json').is_file() and observer.returncode == 0 and summary_path.is_file() and
                        owned.read(summary_path).get('valid') is True and owned.read(summary_path).get('collectionValid') is True):
                    raise RuntimeError('downstream observer exited without a trusted terminal summary')
                state['earlyNormalObserverCompletion'] = True
            if tick % 10 == 0:
                current_power = owned.power(); stamp = time.monotonic()
                if observer.poll() is None:
                    assert process.create_time() == created
                    observer_samples.append({'at': owned.now(), 'pid': process.pid, 'rssBytes': process.memory_info().rss,
                        'cpuPercentOneCore': process.cpu_percent(None), 'cpuIntervalSeconds': stamp - cpu_at,
                        'cpuIntervalValid': stamp - cpu_at >= 1, 'power': current_power})
                cpu_at = stamp
                if current_power.get('ac', 1) != before_power.get('ac', 1):
                    power_changed = True
                    if control.is_dir() and not (control / 'stop.json').exists():
                        owned.save(control / 'stop.json', {'at': owned.now(), 'reason': 'CAP09 supply transition'})
            if stable_seen is None and (load / 'stable-process.json').exists():
                stable_seen = time.monotonic()
            if args.stop_after and stable_seen is not None and time.monotonic() - stable_seen >= args.stop_after and not (control / 'stop.json').exists():
                owned.save(control / 'stop.json', {'at': owned.now(), 'source': 'cap09-run', 'tag': tag,
                                                  'reason': 'predeclared native low-speed stop probe'})
                intentional_stop_written = True
            tick += 1; time.sleep(.5)
        state.update(wrapperExit=wrapper.returncode, loadWaitFinishedAt=owned.now())
        state['observerExit'] = observer.wait(timeout=90)
        owned.save(ROOT / (tag + '-capacity-after.json'), {'at': owned.now(), 'capacity': owned.get_json('http://127.0.0.1:8371/debug/capacity'), 'scope': 'parsed post-wrapper/drain snapshot; includes warm and background'})
        events = sleep_events(start)
        state['powerEvidence'] = {'before': before_power, 'after': owned.power(), 'events': events,
                                 'valid': not power_changed and events['available'] and not events['events']}
        state['observerResources'] = observer_samples
        state['toolBytesStable'] = all(sha(REPO / 'scripts' / name) == digest for name, digest in command['scriptHashes'].items())
        report, status = owned.read(load / 'result.json'), owned.read(control / 'exit-status.json')
        state['apiPassed'] = report.get('meetsAPIBudget', report.get('meetsBudget')) is True
        state['factsConverged'] = report.get('finalReconcileValid', args.chain == 'feed') is True
        state['resourcesPassed'] = status['sampler_exit'] == 0 and not status['errors'] and not status['stop_signal'] and not status['forced'] and status['resource_samples'] == status['complete_resource_samples'] and status['resource_samples'] > 0
        stop_record = owned.read(control / 'stop.json') if (control / 'stop.json').exists() else {}
        state['intentionalStopWritten'] = intentional_stop_written
        observer_summary = owned.read(downstream / 'summary.json')
        state['probeExpectedStopConfirmed'] = bool(args.stop_after and intentional_stop_written and stable_seen is not None and
            stop_record.get('source') == 'cap09-run' and stop_record.get('tag') == tag and
            stop_record.get('reason') == 'predeclared native low-speed stop probe' and status['stop_signal'] and
            not status['forced'] and status['sampler_exit'] == 0 and not status['errors'] and state['factsConverged'] and
            status['resource_samples'] == status['complete_resource_samples'] and status['resource_samples'] > 0 and
            observer.returncode == 0 and observer_summary.get('valid') is True and observer_summary.get('collectionValid') is True and
            state['toolBytesStable'] and state['powerEvidence']['valid'])
        state['pipeline'] = ({'passed': False, 'scope': 'intentional native stop probe; no throughput pass'} if state['probeExpectedStopConfirmed']
                             else grade.pipeline_grade(downstream, report, args.seconds, args.chain))
        state['passed'] = all((state['apiPassed'], state['factsConverged'], state['resourcesPassed'], state['pipeline']['passed'],
                               state['powerEvidence']['valid'], state['toolBytesStable'], wrapper.returncode == 0, observer.returncode == 0))
    except (Exception, KeyboardInterrupt) as error:
        state['failedStartOrException'] = type(error).__name__
        state['passed'] = False
        state['probeExpectedStopConfirmed'] = False
        try:
            exception_stop(wrapper, observer, control, tag, identities)
        except (OSError, ValueError, psutil.Error):
            state['exceptionStopFailed'] = True
    finally:
        if os.name == 'nt':
            ctypes.windll.kernel32.SetThreadExecutionState(0x80000000)
        state['childrenConverged'] = children_converged(wrapper, observer, control, load, identities)
        state['knownOwnedProcesses'] = list(identities.values())
        state['wrapperExit'] = wrapper.poll() if wrapper is not None else None
        state['observerExit'] = observer.poll() if observer is not None else None
        state['passed'] = bool(state['passed'] and state['childrenConverged'] and state['failedStartOrException'] is None)
        if not state['childrenConverged'] or state['failedStartOrException']:
            state['probeExpectedStopConfirmed'] = False
        state['finishedAt'] = owned.now(); owned.save(ROOT / (tag + '-stage.json'), state)
        for handle in handles:
            handle.close()
        if state['childrenConverged'] and owned.read(lock) == owner:
            lock.unlink()
    print(json.dumps({k: state.get(k) for k in ('tag', 'passed', 'apiPassed', 'factsConverged', 'pipeline', 'probeExpectedStopConfirmed', 'failedStartOrException', 'childrenConverged')}))
    return 0 if state['childrenConverged'] and state['failedStartOrException'] is None and (state['passed'] or state.get('probeExpectedStopConfirmed')) else 1


if __name__ == '__main__':
    raise SystemExit(main())
