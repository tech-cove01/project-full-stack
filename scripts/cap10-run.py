"""Execute one bounded CAP10 stage with the existing owned supervisor."""
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

import psutil

REPO = Path(__file__).resolve().parent.parent


def module(name, filename):
    spec = importlib.util.spec_from_file_location(name, REPO / 'scripts' / filename)
    value = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    return value


owned = module('cap10_owned_run', 'cap10-env.py')
ROOT = owned.ROOT
BATCH_ROOT = owned.BATCH_ROOT
KINDS = ('diagnostic', 'comparison', 'search', 'candidate', 'formal')
TOOL_NAMES = ('cap10-run.py', 'cap10-grade.py', 'cap10-env.py', 'cap10-stage.py', 'cap10-resources.py',
              'load/cap10-native-window.mjs', 'cap10-like.mjs', 'load/cap10-like.js',
              'load/cap10-like-contract.mjs', 'load/cap10-db-observe.mjs', 'cap10-downstream.go')
PTS_TOOLS = ('pts/cap10-pts-phase.py', 'pts/cap10-pts-api.py', 'pts/cap10-pts-collector.py',
             'pts/cap10-jmeter.groovy', 'pts/cap10-pts-contract.groovy', 'pts/cap10-jmeter.mjs', 'pts/cap10-jmeter.jmx',
             'pts/cap10-pts-prepare.py')


def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def reserve(args, api, value):
    grade = module('cap10_reservation_grade', 'cap10-grade.py')
    for path in (args.preflight, args.ability, args.prediction):
        if path is not None:
            assert path.resolve().is_relative_to(ROOT) and path.is_file()
    prediction = owned.read(args.prediction) if args.prediction else None
    if value.get('CAP10_LOAD_BACKEND') == 'pts':
        repair_path = ROOT / 'tool-repair-transition.json'
        repaired = repair_path.exists()
        packet_path = ROOT / ('pts-packet-repair1.json' if repaired else 'pts-packet.json')
        admission_path = ROOT / ('pts-scene-admission-repair1.json' if repaired else 'pts-scene-admission.json')
        admission = owned.read(admission_path)
        configuration = owned.read(value['CAP10_PTS_CONFIG'])
        preparation = module('cap10_pts_admission_contract', 'pts/cap10-pts-prepare.py')
        assert admission.get('passed') is True and admission['sceneId'] == configuration['sceneId']
        assert admission['sceneFilesMD5'] == configuration['sceneFiles']
        assert admission['sourceSHA256'] == preparation.source_hashes()
        assert admission['configurationReference'] == value['CAP10_PTS_CONFIG']
        pts_identity = {'loadBackend': 'pts', 'ptsPacketSHA256': sha(packet_path),
                        'ptsAdmissionSHA256': sha(admission_path)}
        for prior in grade.history(ROOT):
            if repaired and prior['tag'] in ('S1', 'S2', 'S3', 'S4'):
                continue  # The grade validates the retained old/new transition.
            assert all(prior.get(key) == digest for key, digest in pts_identity.items()), 'same PTS packet/admission must cover the entire batch'
    freeze_path = ROOT / 'freeze-like.json'
    freeze = owned.read(freeze_path) if args.kind == 'formal' else None
    if freeze is not None and value.get('CAP10_LOAD_BACKEND') == 'pts':
        assert all(freeze.get(key) == digest for key, digest in pts_identity.items()), 'formal PTS identity differs from candidate freeze'
    names = TOOL_NAMES + (PTS_TOOLS if value.get('CAP10_LOAD_BACKEND') == 'pts' else ())
    tools = {name: sha(REPO / 'scripts' / name) for name in names}
    observer_hash = sha(args.observer)
    reservation = grade.check_reservation(ROOT, args.kind, args.rate, args.seconds, api,
        value['CAP_LIKE_FIXTURE'], owned.read(args.preflight), owned.read(args.ability),
        freeze=freeze, prediction=prediction, current_tools=tools, observer_hash=observer_hash)
    tag = reservation['tag']
    command = {'task': 'CAP-10', 'at': owned.now(), 'chain': 'like', 'kind': args.kind, 'tag': tag,
               'mode': 'multi', 'rate': args.rate, 'seconds': args.seconds,
               'warmSeconds': 10, 'warmRate': min(25, args.rate), 'preVUs': 512, 'maxVUs': 512,
               'apiSourceSha': api['sourceSha'], 'apiPid': api['pid'], 'binarySHA256': api['sha256'],
               'likeDiagnosticsEnabled': api['likeDiagnosticsRequested'], 'feedTailEnabled': api['tailEnabledRequested'],
               'executionPlatform': api['executionPlatform'], 'transitionSHA256': api.get('transitionSHA256'),
               'environmentSHA256': sha(ROOT / 'environment.json'), 'fixtureSHA256': sha(value['CAP_LIKE_FIXTURE']),
               'toolSourceSha': owned.run(['git', 'rev-parse', 'HEAD']).stdout.strip(),
               'scriptHashes': tools,
               'observerBinarySHA256': observer_hash, 'reservationCheck': reservation,
               'freezeSHA256': sha(freeze_path) if freeze else None,
               'preflightSHA256': sha(args.preflight), 'abilitySHA256': sha(args.ability),
               'predictionSHA256': sha(args.prediction) if args.prediction else None,
               'setupNormalization': {'existing': 512, 'active': 0, 'allWindows': True},
               'failedStartsConsumeSlot': True}
    command['loadBackend'] = value.get('CAP10_LOAD_BACKEND', 'k6')
    if command['loadBackend'] == 'pts':
        command['ptsPacketSHA256'] = pts_identity['ptsPacketSHA256']
        command['ptsAdmissionSHA256'] = pts_identity['ptsAdmissionSHA256']
    owned.save(ROOT / ('slot-' + tag + '.json'), command)
    owned.save(ROOT / (tag + '-command.json'), command)
    return command


def classify(state, report, load, kind):
    fatal = (report.get('finalFactsValid') is False or report.get('phaseAccountingValid') is False or
             (report.get('result') or {}).get('mutationCounterMatchesDB') is False or
             (report.get('warmExit') == 0 and not report.get('setup')))
    summaries_complete = True
    for phase in ('warm', 'stable'):
        path = load / (phase + '-summary.json')
        if not path.is_file():
            summaries_complete = False
            fatal = fatal or (load / (phase + '-process.json')).exists()
            continue
        metrics = owned.read(path).get('metrics', {})
        count = lambda name: metrics.get(name, {}).get('count')
        a, c = count('cap_like_attempted'), count('cap_like_completed')
        if type(a) is not int or type(c) is not int or c > a:
            fatal = True
        elif a != c:
            fatal = True
        for name in ('cap_like_noop', 'cap_like_effect_unknown', 'cap_like_content_errors', 'cap_like_rejected'):
            value = count(name)
            fatal = fatal or type(value) is not int or value < 0 or value > 0
    state['batchFatal'] = bool(fatal)
    complete = report.get('nativeScenario', {}).get('complete') is True
    measured = summaries_complete and all(state.get(k) is True for k in ('resourcesPassed', 'toolBytesStable', 'collectionValid', 'traceSwitchesValid', 'containersValid')) and state.get('powerEvidence', {}).get('valid') is True and state.get('failedStartOrException') is None
    result = report.get('result') or {}
    generator = (result.get('dropped', 0) > 0 or result.get('actualOfferedRatio', 0) < .99
                 or result.get('generatorTimingViolations', 0) > 0)
    state['recoveryVerified'] = state.get('pipeline', {}).get('recoverySecondsFromNativeEnd') is not None and state['pipeline']['recoverySecondsFromNativeEnd'] <= 60
    if fatal:
        outcome = 'correctness_stop'
    elif not measured:
        outcome = 'measurement_invalid'
    elif generator:
        outcome = 'generator_limited'
    elif not complete:
        outcome = 'measurement_invalid'
    else:
        outcome = 'pass' if state['passed'] else 'performance_limited'
    state['outcome'] = outcome
    state['boundaryEligible'] = outcome in ('pass', 'performance_limited') and complete and measured and not generator and report.get('finalFactsValid') is True and kind in ('diagnostic', 'comparison', 'search')
    state['pauseBeforeFurtherLoad'] = outcome in ('measurement_invalid', 'generator_limited') or not state['recoveryVerified']


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


def pre_stable_guard(api, load, identities, expected_containers, cpu_interval_seconds):
    """Read-only warm/setup guard; a completed warm k6 is explicitly absent."""
    row = {'at': owned.now(), 'phase': 'waiting-for-harness', 'errors': [], 'violations': [],
           'hostCPUIntervalSeconds': cpu_interval_seconds, 'hostCPUIntervalValid': cpu_interval_seconds >= 1}
    try:
        process = owned.api_identity(api)
        row['apiRSSBytes'] = process.memory_info().rss
        row['hostMemoryPercent'] = psutil.virtual_memory().percent
        row['hostCPUPercent'] = psutil.cpu_percent(None)
        row['helperRSSBytes'] = None; row['warmK6RSSBytes'] = None
        for identity in identities.values():
            try:
                child = psutil.Process(identity['pid'])
                if child.create_time() != identity['created']:
                    continue
                if 'cap10-like.mjs' in ' '.join(child.cmdline()):
                    row['helperRSSBytes'] = child.memory_info().rss; row['helperPID'] = child.pid
                    row['phase'] = 'setup-or-harness'
            except psutil.NoSuchProcess:
                pass
        marker = load / 'warm-process.json'
        if marker.is_file():
            detail = owned.read(marker)
            try:
                pts = detail.get('backend') == 'pts'
                child = psutil.Process(detail['controller_pid'] if pts else detail['k6_pid'])
                created = detail['controller_started_unix_ms'] if pts else detail['k6_started_unix_ms']
                if abs(child.create_time() * 1000 - created) <= 2000:
                    assert ('cap10-pts-phase.py' if pts else 'cap10-like.js') in ' '.join(child.cmdline())
                    if pts:
                        row['warmControllerRSSBytes'] = child.memory_info().rss
                        row['warmControllerPID'] = child.pid
                    else:
                        row['warmK6RSSBytes'] = child.memory_info().rss; row['warmK6PID'] = child.pid
                    row['phase'] = 'warm'
                else:
                    row['warmK6AbsentConfirmed'] = True; row['phase'] = 'setup'
            except psutil.NoSuchProcess:
                row['warmK6AbsentConfirmed'] = True; row['phase'] = 'setup'
        assert row['helperRSSBytes'] is not None or row['phase'] == 'waiting-for-harness'
        row['ownedLoadRSSBytes'] = (row['helperRSSBytes'] or 0) + (row['warmK6RSSBytes'] or 0) + row.get('warmControllerRSSBytes', 0)
        result = owned.run(['docker', 'stats', '--no-stream', '--format', '{{json .}}',
                            *[c['id'] for c in expected_containers.values()]], timeout=12)
        stats = [json.loads(line) for line in result.stdout.splitlines() if line]
        assert len(stats) == 4
        seen = set()
        for item in stats:
            matches = [c for c in expected_containers.values() if c['id'].startswith(item['ID']) and len(item['ID']) >= 12]
            assert len(matches) == 1 and matches[0]['id'] not in seen
            expected = matches[0]; seen.add(expected['id'])
            assert item['Name'] == expected['name'].lstrip('/')
            memory = float(item['MemPerc'].rstrip('%'))
            assert 0 <= memory <= 100
            if memory >= 85:
                row['violations'].append('container memory >=85%: ' + expected['service'])
        row['containers'] = stats
        if row['apiRSSBytes'] >= 1536 * 1024 * 1024:
            row['violations'].append('API RSS >=1.5GiB')
        if row['ownedLoadRSSBytes'] >= 2 * 1024 * 1024 * 1024:
            row['violations'].append('warm k6 plus helper RSS >=2GiB')
        if row['hostMemoryPercent'] >= 88:
            row['violations'].append('host memory >=88%')
    except (OSError, ValueError, KeyError, AssertionError, RuntimeError, subprocess.SubprocessError, psutil.Error) as error:
        row['errors'].append(type(error).__name__)
    return row


def container_snapshot(expected):
    values = {name: owned.container(name, detail['id'], True) for name, detail in expected.items()}
    valid = all(value['restartCount'] == 0 and value['oomKilled'] is False and value['healthStatus'] == 'healthy' and
                value['volumes'] == expected[name]['volumes'] for name, value in values.items())
    return {'at': owned.now(), 'valid': valid, 'containers': values}


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
            pts = detail.get('backend') == 'pts'
            if pts:
                if not owned.phase_terminated(detail, load):
                    return False
                continue
            try:
                pid = detail['controller_pid'] if pts else detail['k6_pid']
                created = detail['controller_started_unix_ms'] if pts else detail['k6_started_unix_ms']
                if abs(psutil.Process(pid).create_time() * 1000 - created) <= 2000:
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
            owned.save(control / 'stop.json', {'at': owned.now(), 'source': 'cap10-run',
                'tag': tag, 'reason': 'runner exception; bounded owned supervisor stop'})
        deadline = time.monotonic() + 105
        while wrapper.poll() is None and time.monotonic() < deadline:
            if control.is_dir() and not (control / 'stop.json').exists():
                owned.save(control / 'stop.json', {'at': owned.now(), 'source': 'cap10-run',
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
    parser.add_argument('--chain', choices=('like',), default='like')
    parser.add_argument('--kind', choices=KINDS, required=True)
    parser.add_argument('--mode', choices=('multi',), default='multi')
    parser.add_argument('--rate', type=int, required=True); parser.add_argument('--seconds', type=int, required=True)
    parser.add_argument('--prediction', type=Path)
    parser.add_argument('--preflight', type=Path, required=True); parser.add_argument('--ability', type=Path, required=True)
    args = parser.parse_args()
    assert Path.cwd().resolve() == REPO and args.observer.is_file()
    assert args.api_metadata.resolve().parent == ROOT
    grade = module('cap10_native_downstream_grade', 'cap10-grade.py')
    value, runtime = owned.settings(args.environment)
    pts = value.get('CAP10_LOAD_BACKEND') == 'pts'
    stage_deadline = '600' if pts else '360'
    api = owned.read(args.api_metadata); owned.api_identity(api, health=True)
    assert value['CAP_SOURCE_SHA'] == api['sourceSha']
    assert api['tailEnabledRequested'] is False
    assert api['likeDiagnosticsRequested'] is (args.kind in ('diagnostic', 'comparison'))
    lock = runtime / 'cap10-active.private.json'
    assert not lock.exists(), 'prior owned stage still holds exclusivity'
    command = reserve(args, api, value); tag = command['tag']
    control, load, downstream = (ROOT / (tag + suffix) for suffix in ('-control', '-load', '-downstream'))
    owner = {'at': owned.now(), 'tag': tag, 'pid': os.getpid(), 'created': psutil.Process().create_time()}
    owned.save(lock, owner)
    state = {'at': owned.now(), 'tag': tag, 'passed': False, 'failedStartOrException': None,
             'wrapperExit': None, 'observerExit': None, 'childrenConverged': False}
    wrapper = observer = None; handles = []; observer_samples = []; report = {}
    identities = {}; pre_guard_samples = []; pre_guard_failures = 0; pre_guard_cpu_high = 0; pending_pre_stop = None; pending_power_stop = None
    pre_guard_at = time.monotonic(); psutil.cpu_percent(None)
    expected_containers = owned.read(ROOT / 'environment.json')['containers']
    start = owned.now(); before_power = {}; power_changed = False
    try:
        before_power = owned.power()
        assert owned.power_valid(before_power) and before_power['hostMemoryPercent'] < 88
        state['containersBefore'] = container_snapshot(expected_containers)
        assert state['containersBefore']['valid'], 'dependency state invalid before warm'
        if os.name == 'nt':
            assert ctypes.windll.kernel32.SetThreadExecutionState(0x80000001)
        owned.save(ROOT / (tag + '-capacity-before.json'), {'at': owned.now(), 'capacity': owned.get_json('http://127.0.0.1:8381/debug/capacity'), 'scope': 'parsed pre-warm snapshot'})
        log = (ROOT / (tag + '-observer.txt')).open('xb'); handles.append(log)
        observer = subprocess.Popen([str(args.observer.resolve()), '--environment', str(args.environment.resolve()),
            '--output', str(downstream), '--done-file', str(control / 'done.json'), '--max-seconds', stage_deadline],
            cwd=REPO, env=owned.env(value), stdout=log, stderr=subprocess.STDOUT, creationflags=owned.FLAGS)
        process = psutil.Process(observer.pid); created = process.create_time(); process.cpu_percent(None); cpu_at = time.monotonic()
        state['observerPid'] = observer.pid; deadline = time.monotonic() + 30
        while not (downstream / 'ready.json').exists() and observer.poll() is None and time.monotonic() < deadline:
            time.sleep(.1)
        assert (downstream / 'ready.json').exists() and owned.read(downstream / 'ready.json')['ready']
        native = {**value, 'CAP_STOP_FILE': str(control / 'stop.json'), 'CAP_API_PID': str(api['pid']), 'CAP10_DOWNSTREAM_EVIDENCE': str(downstream), 'CAP10_FORMAL': '1' if args.kind == 'formal' else '0'}
        if pts:
            native['CAP10_PYTHON_BIN'] = sys.executable
            native['CAP10_RESERVATION_FILE'] = str(ROOT / (tag + '-command.json'))
        prefix = 'CAP_LIKE_'
        native.update({prefix + key: str(command[field]) for key, field in
                       (('MODE', 'mode'), ('RATE', 'rate'), ('SECONDS', 'seconds'), ('WARM_SECONDS', 'warmSeconds'),
                        ('WARM_RATE', 'warmRate'), ('PRE_VUS', 'preVUs'), ('MAX_VUS', 'maxVUs'))})
        native[prefix + 'EVIDENCE'] = str(load)
        script = 'cap10-like.mjs'
        argv = [sys.executable, '-B', '-X', 'utf8', 'scripts/cap10-stage.py', '--cohort', 'cap10',
                '--mode', 'like', '--control-dir', str(control),
                '--api-pid', str(api['pid']), '--diagnostics', 'http://127.0.0.1:8381/debug/capacity',
                '--interval', '5', '--max-seconds', stage_deadline, '--stop-grace-seconds', '90', '--', 'node', 'scripts/' + script, 'run']
        owned.save(ROOT / (tag + '-invocation.json'), {'argv': argv, 'environmentValuesExported': False})
        log = (ROOT / (tag + '-console.txt')).open('xb'); handles.append(log)
        wrapper = subprocess.Popen(argv, cwd=REPO, env=owned.env(native), stdout=log, stderr=subprocess.STDOUT, creationflags=owned.FLAGS)
        state.update(wrapperPid=wrapper.pid, loadWaitStartedAt=owned.now())
        next_observation = time.monotonic()
        while wrapper.poll() is None:
            remember_children(wrapper, identities)
            pending_stop = pending_power_stop or pending_pre_stop
            if pending_stop and control.is_dir() and not (control / 'stop.json').exists():
                owned.save(control / 'stop.json', {'at': owned.now(), 'source': 'cap10-run', 'reason': pending_stop})
            if observer.poll() is not None:
                summary_path = downstream / 'summary.json'
                if not ((control / 'done.json').is_file() and observer.returncode == 0 and summary_path.is_file() and
                        owned.read(summary_path).get('valid') is True and owned.read(summary_path).get('collectionValid') is True):
                    raise RuntimeError('downstream observer exited without a trusted terminal summary')
                state['earlyNormalObserverCompletion'] = True
            if time.monotonic() >= next_observation:
                current_power = owned.power(); stamp = time.monotonic()
                window_power = owned.power_window_evidence(before_power, current_power, start, lightweight=True)
                if not owned.power_valid(current_power) or not window_power.get('valid'):
                    power_changed = True
                    pending_power_stop = {'kind': 'power/suspend/clock evidence invalid', 'evidence': window_power}
                    if control.is_dir() and not (control / 'stop.json').exists():
                        owned.save(control / 'stop.json', {'at': owned.now(), 'source': 'cap10-run', 'reason': pending_power_stop})
                next_observation = stamp + 5
                if not (load / 'stable-process.json').exists() and not pending_power_stop:
                    guard = pre_stable_guard(api, load, identities, expected_containers, stamp - pre_guard_at)
                    pre_guard_at = stamp; pre_guard_samples.append(guard)
                    pre_guard_failures = pre_guard_failures + 1 if guard['errors'] else 0
                    if not guard['errors'] and guard['hostCPUIntervalValid']:
                        pre_guard_cpu_high = pre_guard_cpu_high + 1 if guard['hostCPUPercent'] >= 85 else 0
                    if guard['violations'] or pre_guard_failures >= 3 or pre_guard_cpu_high >= 3:
                        pending_pre_stop = {'violations': guard['violations'], 'consecutiveIncomplete': pre_guard_failures, 'consecutiveHighHostCPU': pre_guard_cpu_high}
                if observer.poll() is None:
                    assert process.create_time() == created
                    observer_samples.append({'at': owned.now(), 'pid': process.pid, 'rssBytes': process.memory_info().rss,
                        'cpuPercentOneCore': process.cpu_percent(None), 'cpuIntervalSeconds': stamp - cpu_at,
                        'cpuIntervalValid': stamp - cpu_at >= 1, 'power': current_power})
                cpu_at = stamp
            time.sleep(.5)
        state.update(wrapperExit=wrapper.returncode, loadWaitFinishedAt=owned.now())
        state['observerExit'] = observer.wait(timeout=90)
        state['containersAfter'] = container_snapshot(expected_containers)
        state['containersValid'] = state['containersBefore']['valid'] and state['containersAfter']['valid']
        owned.save(ROOT / (tag + '-capacity-after.json'), {'at': owned.now(), 'capacity': owned.get_json('http://127.0.0.1:8381/debug/capacity'), 'scope': 'parsed post-wrapper/drain snapshot; includes warm and background'})
        after_power = owned.power()
        state['powerEvidence'] = {**owned.power_window_evidence(before_power, after_power, start),
                                 'before': before_power, 'after': after_power}
        state['powerEvidence']['valid'] = not power_changed and state['powerEvidence'].get('valid') is True
        state['observerResources'] = observer_samples
        state['toolBytesStable'] = all(sha(REPO / 'scripts' / name) == digest for name, digest in command['scriptHashes'].items())
        report, status = owned.read(load / 'result.json'), owned.read(control / 'exit-status.json')
        state['apiPassed'] = report.get('meetsAPIBudget', report.get('meetsBudget')) is True
        state['factsConverged'] = report.get('finalReconcileValid') is True
        state['resourcesPassed'] = status.get('sampler_exit') == 0 and not status['errors'] and not status['stop_signal'] and not status['forced'] and status['resource_samples'] == status['complete_resource_samples'] and status['resource_samples'] > 0
        state['pipeline'] = grade.pipeline_grade(downstream, report, args.seconds, 'like')
        observed = owned.read(downstream / 'summary.json')
        state['collectionValid'] = observed.get('collectionValid') is True and observed.get('semanticViolationCount') == 0
        rows = [json.loads(line) for line in (control / 'resources.ndjson').read_text(encoding='utf-8-sig').splitlines() if line]
        samples = [row for row in rows if row.get('kind') == 'sample']
        expected_trace = args.kind in ('diagnostic', 'comparison')
        state['traceSwitchesValid'] = bool(samples) and all(row.get('diagnostics', {}).get('tail', {}).get('enabled') is expected_trace for row in samples)
        result = report.get('result') or {}
        state['candidateMarginPassed'] = kind_margin = args.kind != 'candidate' or all((result.get(key) or {}).get('p(95)', float('inf')) <= 160 and (result.get(key) or {}).get('p(99)', float('inf')) <= 400 for key in ('allLatency', 'mutationLatency'))
        state['passed'] = all((state['apiPassed'], state['factsConverged'], state['resourcesPassed'], state['pipeline']['passed'], state['containersValid'],
                               state['powerEvidence']['valid'], state['toolBytesStable'], state['traceSwitchesValid'], kind_margin, wrapper.returncode == 0, observer.returncode == 0))
    except (Exception, KeyboardInterrupt) as error:
        state['failedStartOrException'] = type(error).__name__
        state['passed'] = False
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
        state['preStableGuardSamples'] = pre_guard_samples
        try:
            if not report and (load / 'result.json').is_file():
                report = owned.read(load / 'result.json')
            classify(state, report, load, args.kind)
        except (OSError, ValueError, KeyError, TypeError, AssertionError) as error:
            # A broken native summary must not prevent retaining the terminal
            # reservation or silently allow a later window after unknown work.
            state.update(passed=False, outcome='measurement_invalid', batchFatal=any(load.glob('*-process.json')),
                         boundaryEligible=False, recoveryVerified=False, pauseBeforeFurtherLoad=True,
                         classificationEvidenceError=type(error).__name__)
        state['finishedAt'] = owned.now(); owned.save(ROOT / (tag + '-stage.json'), state)
        for handle in handles:
            handle.close()
        if state['childrenConverged'] and owned.read(lock) == owner:
            lock.unlink()
    print(json.dumps({k: state.get(k) for k in ('tag', 'passed', 'outcome', 'batchFatal', 'boundaryEligible', 'apiPassed', 'factsConverged', 'pipeline', 'failedStartOrException', 'childrenConverged')}))
    return 0 if state['passed'] else 1



if __name__ == '__main__':
    raise SystemExit(main())
