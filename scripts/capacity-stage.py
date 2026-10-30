"""Run one existing CAP harness with an owned resource sampler.

The workload keeps all connection/configuration settings in its inherited
environment. This wrapper never exports environment values or command lines.
The control directory is separate from the harness's new output directory.
Read warmup uses a separate k6 process: sampling starts only at stable phase.
"""
import argparse
import datetime as dt
import json
import os
from pathlib import Path
import subprocess
import sys
import time

import psutil


def now():
    return dt.datetime.now(dt.timezone.utc).isoformat()


def write_new(path, value):
    try:
        with path.open('x', encoding='utf-8') as stream:
            json.dump(value, stream, ensure_ascii=False)
            stream.write('\n')
        return True
    except FileExistsError:
        return False


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--mode', choices=('trade', 'read'), required=True)
    parser.add_argument('--control-dir', type=Path, required=True,
                        help='new directory for sampler, stop/done signals and exit status')
    parser.add_argument('--api-pid', type=int, required=True)
    parser.add_argument('--diagnostics', required=True)
    parser.add_argument('--interval', type=float, default=5)
    parser.add_argument('--max-seconds', type=int, default=900,
                        help='whole stage deadline, including preparation and drain')
    parser.add_argument('--stop-grace-seconds', type=int, default=90,
                        help='allow the harness to stop owned k6 and reconcile after stop')
    parser.add_argument('command', nargs=argparse.REMAINDER,
                        help='existing harness command after --; credentials belong in environment')
    args = parser.parse_args()
    command = args.command[1:] if args.command[:1] == ['--'] else args.command
    if not command or not 2 <= args.interval <= 30 or not 1 <= args.max_seconds <= 3600 or not 5 <= args.stop_grace_seconds <= 300:
        parser.error('command and bounded sampling/deadline/grace settings required')
    expected_diagnostics = f"http://127.0.0.1:{8350 if args.mode == 'trade' else 8351}/debug/capacity"
    if args.diagnostics != expected_diagnostics:
        parser.error('exact dedicated loopback diagnostics URL required for this mode')
    control = args.control_dir.resolve()
    output_var = 'CAP_OUTPUT_DIR' if args.mode == 'trade' else 'CAP_READ_EVIDENCE'
    raw_output, raw_stop = os.environ.get(output_var), os.environ.get('CAP_STOP_FILE')
    if not raw_output or not raw_stop:
        parser.error(f'explicit {output_var} and CAP_STOP_FILE required in environment')
    load = Path(raw_output).resolve()
    stop = Path(raw_stop).resolve()
    if control.exists() or load.exists() or stop.exists():
        parser.error('control directory, harness output and stop path must all be new')
    if control == load or control in load.parents or load in control.parents:
        parser.error('control and harness output must be separate sibling directories')
    if stop != control / 'stop.json':
        parser.error('CAP_STOP_FILE must equal <control-dir>/stop.json')
    if not load.parent.is_dir() or not control.parent.is_dir():
        parser.error('existing evidence parents required')
    api = psutil.Process(args.api_pid)
    api_identity = api.create_time()
    sampler_script = Path(__file__).resolve().with_name('capacity-resources.py')
    if not sampler_script.is_file():
        parser.error('capacity-resources.py must exist beside this wrapper')
    project = 'project-full-stack-cap-' + args.mode
    script = 'capacity-trade.js' if args.mode == 'trade' else 'go073-cap-read.js'
    control.mkdir()
    state = {'started_at': now(), 'mode': args.mode, 'api_pid': api.pid,
             'sampler_started': False, 'stop_signal': False, 'forced_owned_processes': [],
             'errors': []}
    child = sampler = None
    owned_generator = None
    child_identity = None
    started = time.monotonic()
    stop_started = None
    forced = False
    flags = subprocess.CREATE_NO_WINDOW if os.name == 'nt' else 0

    def signal(reason):
        nonlocal stop_started
        write_new(stop, {'at': now(), 'source': 'capacity-stage', 'reason': reason})
        if stop_started is None:
            stop_started = time.monotonic()

    def generator():
        # Read the file privately; only the selected identity fields are used.
        detail = json.loads((load / 'process.json').read_text(encoding='utf-8-sig'))
        if args.mode == 'read' and detail.get('phase') != 'stable':
            return None
        helper = psutil.Process(int(detail['helper_pid']))
        candidate = psutil.Process(int(detail['k6_pid']))
        root = psutil.Process(child.pid)
        if root.create_time() != child_identity:
            raise RuntimeError('owned harness identity changed')
        descendants = {p.pid for p in root.children(recursive=True)} | {root.pid}
        if helper.pid not in descendants or candidate.pid not in descendants or candidate.pid == api.pid:
            raise RuntimeError('generator/helper is not owned by this stage')
        if candidate.name().lower() not in ('k6', 'k6.exe') or not any(script in arg for arg in candidate.cmdline()):
            raise RuntimeError('owned generator executable/script mismatch')
        identity = candidate.create_time()
        if abs(identity * 1000 - float(detail['k6_started_unix_ms'])) > 2000:
            raise RuntimeError('owned generator creation time mismatch')
        return candidate, identity

    def force_owned():
        # Only descendants of the exact child identity, plus the previously
        # verified generator identity if the helper has exited. Never scan by
        # name or kill the API, Docker, another environment or any container.
        targets = []
        try:
            root = psutil.Process(child.pid)
            if root.create_time() == child_identity:
                targets = root.children(recursive=True) + [root]
        except psutil.Error:
            pass
        if owned_generator:
            candidate, identity = owned_generator
            try:
                if candidate.is_running() and candidate.create_time() == identity:
                    targets.insert(0, candidate)
            except psutil.Error:
                pass
        seen = set()
        for process in targets:
            if process.pid in seen or process.pid in (api.pid, os.getpid()) or (sampler and process.pid == sampler.pid):
                continue
            seen.add(process.pid)
            try:
                process.kill()
                state['forced_owned_processes'].append(process.pid)
            except psutil.Error:
                pass

    try:
        with (control / 'stage-console.txt').open('x', encoding='utf-8') as child_log, (control / 'sampler-console.txt').open('x', encoding='utf-8') as sampler_log:
            child = subprocess.Popen(command, stdout=child_log, stderr=subprocess.STDOUT,
                                     creationflags=flags)
            child_identity = psutil.Process(child.pid).create_time()
            state['helper_launcher_pid'] = child.pid
            while child.poll() is None:
                if not api.is_running() or api.create_time() != api_identity:
                    signal('API process identity unavailable')
                if stop.exists() and stop_started is None:
                    stop_started = time.monotonic()
                if time.monotonic() - started >= args.max_seconds:
                    signal('whole stage deadline reached')
                if stop_started is not None and time.monotonic() - stop_started >= args.stop_grace_seconds:
                    signal('harness did not exit within bounded stop grace; incomplete reconciliation')
                    force_owned()
                    forced = True
                    break
                if sampler is None and not stop.exists():
                    try:
                        owned_generator = generator()
                    except (FileNotFoundError, json.JSONDecodeError, psutil.NoSuchProcess):
                        owned_generator = None  # writer or phase transition; retry
                    if owned_generator:
                        sampler_command = [sys.executable, str(sampler_script), '--api-pid', str(api.pid),
                            '--diagnostics', args.diagnostics, '--project', project,
                            '--container', project + '-mysql-1', '--container', project + '-redis-1',
                            '--load-script', script, '--load-pid-file', str(load / 'process.json'),
                            '--output', str(control / 'resources.ndjson'), '--stop-file', str(stop),
                            '--done-file', str(control / 'done.json'), '--interval', str(args.interval),
                            '--max-seconds', str(args.max_seconds)]
                        sampler = subprocess.Popen(sampler_command, stdout=sampler_log,
                            stderr=subprocess.STDOUT, creationflags=flags)
                        state.update(sampler_started=True, sampler_pid=sampler.pid,
                                     sampled_k6_pid=owned_generator[0].pid, sampling_started_at=now())
                if owned_generator is not None and sampler is not None:
                    candidate, identity = owned_generator
                    # No new generator may replace the verified one. In read
                    # mode we start only at stable; trade uses one process.
                    try:
                        current = json.loads((load / 'process.json').read_text(encoding='utf-8-sig'))
                        if int(current['k6_pid']) != candidate.pid:
                            signal('generator PID changed after sampling began; no automatic switch')
                    except (OSError, ValueError, KeyError):
                        pass  # sampler separately validates its exact PID file
                    try:
                        alive = candidate.is_running() and candidate.create_time() == identity
                    except psutil.NoSuchProcess:
                        alive = False
                    if not alive and not state.get('sampling_ended_after_generator_exit'):
                        # A normal k6 end can precede long helper reconciliation.
                        # Stop resource collection before missing-process samples
                        # fabricate a resource failure during drain/JSON parsing.
                        write_new(control / 'done.json', {'at': now(), 'source': 'capacity-stage',
                            'reason': 'verified owned generator exited; harness reconciliation continues'})
                        state.update(sampling_ended_after_generator_exit=True, sampling_ended_at=now())
                if sampler is not None and sampler.poll() is not None and not stop.exists() and not state.get('sampling_ended_after_generator_exit'):
                    signal('resource sampler exited before harness; observation incomplete')
                time.sleep(.1)
            if forced:
                child.wait(timeout=10)
            state['harness_exit'] = child.returncode
            write_new(control / 'done.json', {'at': now(), 'source': 'capacity-stage'})
            if sampler is not None:
                try:
                    sampler.wait(timeout=30)
                except subprocess.TimeoutExpired:
                    sampler.kill()
                    sampler.wait(timeout=10)
                    state['errors'].append('sampler did not stop after done signal; own sampler killed')
                state['sampler_exit'] = sampler.returncode
            else:
                state['errors'].append('no stable owned generator was observed; no resource sample')
    except (Exception, KeyboardInterrupt) as error:
        # Do not serialize exception text, command lines or environments.
        state['errors'].append('supervision failure: ' + type(error).__name__)
        signal('stage supervision interrupted or unavailable')
        if child is not None:
            try:
                child.wait(timeout=args.stop_grace_seconds)
            except subprocess.TimeoutExpired:
                force_owned()
                forced = True
                child.wait(timeout=10)
            state['harness_exit'] = child.returncode
        write_new(control / 'done.json', {'at': now(), 'source': 'capacity-stage'})
        if sampler is not None:
            try:
                sampler.wait(timeout=30)
            except subprocess.TimeoutExpired:
                sampler.kill()
                sampler.wait(timeout=10)
            state['sampler_exit'] = sampler.returncode
    samples = complete_samples = 0
    first_sample = last_sample = None
    try:
        with (control / 'resources.ndjson').open(encoding='utf-8-sig') as observations:
            for line in observations:
                row = json.loads(line)
                if row.get('kind') == 'sample':
                    samples += 1
                    first_sample = first_sample or row.get('at')
                    last_sample = row.get('at')
                    if not row.get('errors') and row.get('generators'):
                        complete_samples += 1
    except (OSError, ValueError):
        state['errors'].append('resource evidence unavailable or malformed')
    state['resource_samples'] = samples
    state['complete_resource_samples'] = complete_samples
    state['first_resource_sample_at'] = first_sample
    state['last_resource_sample_at'] = last_sample
    if not complete_samples:
        state['errors'].append('no complete resource sample; no measurable capacity result')
    state.update(ended_at=now(), elapsed_seconds=time.monotonic() - started,
                 stop_signal=stop.exists(), forced=forced)
    state['completed_without_supervision_failure'] = (state.get('harness_exit') == 0 and
        state.get('sampler_exit') == 0 and not state['stop_signal'] and not state['errors'])
    # A harness may return zero after preserving a limited workload result.
    # This status is orchestration only; read result.json/trade report.json
    # explicitly for budget/correctness. Never turn it into a capacity pass.
    write_new(control / 'exit-status.json', state)
    print(json.dumps({'mode': args.mode, 'harness_exit': state.get('harness_exit'),
                      'sampler_exit': state.get('sampler_exit'), 'stop_signal': state['stop_signal'],
                      'supervision_ok': state['completed_without_supervision_failure']}))
    return 0 if state['completed_without_supervision_failure'] else 1


if __name__ == '__main__':
    try:
        raise SystemExit(main())
    except Exception as error:
        print('capacity-stage: ' + type(error).__name__ + '; see selected-field control files', file=sys.stderr)
        raise SystemExit(1)
