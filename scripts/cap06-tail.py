"""CAP-06 bounded, read-only tail collector for an owned stable k6 process.

Adds diagnostic overhead; never a formal capacity result. No environment,
command-line dump, response body, SQL text or user/key label is retained.
"""
import argparse
from collections import deque
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import time
import urllib.request

import psutil


def now():
    return dt.datetime.now(dt.timezone.utc).isoformat()


def read(path):
    return json.loads(path.read_text(encoding='utf-8-sig'))


def write_new(path, value):
    with path.open('x', encoding='utf-8') as stream:
        json.dump(value, stream, ensure_ascii=False, indent=2)


def identity(metadata):
    process = psutil.Process(metadata['pid'])
    if os.path.normcase(process.exe()) != os.path.normcase(metadata['exe']):
        raise ValueError('API executable mismatch')
    if abs(process.create_time() * 1000 - metadata['creationUnixMs']) > 2000:
        raise ValueError('API creation mismatch')
    return process, process.create_time()


def stable_process(load, api_pid):
    detail = read(load / 'stable-process.json')
    if detail.get('phase') != 'stable':
        raise ValueError('stable phase required')
    candidate = psutil.Process(int(detail['k6_pid']))
    created = candidate.create_time()
    if candidate.name().lower() not in ('k6', 'k6.exe'):
        raise ValueError('stable executable mismatch')
    if abs(created * 1000 - detail['k6_started_unix_ms']) > 2000:
        raise ValueError('stable creation mismatch')
    # Inspect privately; never export the command line or inherited environment.
    if not any(Path(part).name == 'go073-cap-read.js' for part in candidate.cmdline()):
        raise ValueError('stable workload mismatch')
    helper = psutil.Process(int(detail['helper_pid']))
    if helper.pid not in {p.pid for p in candidate.parents()}:
        raise ValueError('stable ancestry mismatch')
    if candidate.pid == api_pid:
        raise ValueError('generator cannot be API')
    return candidate, created


def alive(process, created):
    try:
        return process.is_running() and process.create_time() == created
    except psutil.Error:
        return False


def triggers(previous, current):
    if not previous:
        return []
    reasons = []
    for pool in ('sql', 'redis'):
        left, right = previous.get(pool, {}), current.get(pool, {})
        count = right.get('wait_count', 0) - left.get('wait_count', 0)
        duration = right.get('wait_duration_ns', 0) - left.get('wait_duration_ns', 0)
        if count > 0 or duration >= 100_000_000:
            reasons.append({'kind': pool + '_global_pool_wait_delta', 'count': count, 'duration_ns': duration,
                            'scope': 'whole API pool; not a per-request acquisition duration'})
    old_seq = previous.get('tail', {}).get('latest_sequence', 0)
    for trace in current.get('tail', {}).get('slow_traces', []):
        if trace.get('sequence', 0) > old_seq and trace.get('total_ns', 0) >= 200_000_000:
            reasons.append({'kind': 'completed_slow_trace', 'sequence': trace['sequence'], 'total_ns': trace['total_ns']})
    return reasons


def fetch(opener, url, limit, timeout):
    started, tick = now(), time.monotonic()
    with opener.open(url, timeout=timeout) as response:
        raw = response.read(limit + 1)
    if len(raw) > limit:
        raise ValueError('bounded diagnostic response exceeded')
    return raw, {'started_at': started, 'finished_at': now(), 'elapsed_ms': (time.monotonic() - tick) * 1000,
                 'bytes': len(raw), 'sha256': hashlib.sha256(raw).hexdigest()}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--api-metadata', type=Path, required=True)
    parser.add_argument('--load-dir', type=Path, required=True)
    parser.add_argument('--control-dir', type=Path, required=True)
    parser.add_argument('--output-dir', type=Path, required=True)
    parser.add_argument('--max-seconds', type=int, default=360)
    args = parser.parse_args()
    if not 20 <= args.max_seconds <= 900:
        parser.error('bounded whole-stage lifetime required')
    load, control, output = args.load_dir.resolve(), args.control_dir.resolve(), args.output_dir.resolve()
    if len({load, control, output}) != 3 or load.parent != control.parent or output.parent != load.parent:
        parser.error('three distinct sibling directories required')
    if output.exists():
        parser.error('new diagnostics directory required')
    metadata = read(args.api_metadata)
    api, api_created = identity(metadata)
    output.mkdir()
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    record = {'task': 'CAP-06B', 'started_at': now(), 'api_pid': api.pid, 'api_source_sha': metadata['sourceSha'],
              'api_metadata_sha256': hashlib.sha256(args.api_metadata.read_bytes()).hexdigest(),
              'script_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
              'poll_interval_seconds': 1, 'pre_trigger_seconds': 20, 'post_trigger_seconds': 10,
              'maximum_goroutine_captures': 3, 'minimum_capture_spacing_seconds': 15,
              'trigger_policy': 'positive SQL/Redis global wait-count delta, >=100ms cumulative wait delta, or new completed request trace >=200ms',
              'diagnostic_only': True, 'captures': [], 'errors': [], 'stable_observed': False}
    before = deque(maxlen=21)
    windows = []
    previous = None
    stable = None
    started = time.monotonic()
    last_capture = -1000.0
    sequence = 0
    scheduled = None
    successful_polls = failed_polls = 0
    first_sample_at = last_sample_at = None
    prior_sample_tick = None
    maximum_sample_gap_ms = 0
    with (output / 'timeline.ndjson').open('x', encoding='utf-8') as timeline:
        while time.monotonic() - started < args.max_seconds:
            if (control / 'done.json').exists() or (control / 'exit-status.json').exists():
                break
            if not alive(api, api_created):
                record['errors'].append('API identity unavailable; no further diagnostic requests')
                break
            if stable is None:
                try:
                    stable = stable_process(load, api.pid)
                    record['stable_observed'] = True
                    record['stable_pid'] = stable[0].pid
                    record['stable_created_unix_ms'] = stable[1] * 1000
                    record['stable_observed_at'] = now()
                except (OSError, ValueError, KeyError, psutil.Error):
                    time.sleep(.1)
                    continue
            if not alive(*stable):
                record['stable_exited_at'] = now()
                break
            tick = time.monotonic()
            if scheduled is None:
                scheduled = tick
            sequence += 1
            first_sample_at = first_sample_at or now()
            last_sample_at = now()
            if prior_sample_tick is not None:
                maximum_sample_gap_ms = max(maximum_sample_gap_ms, (tick - prior_sample_tick) * 1000)
            prior_sample_tick = tick
            while before and tick - before[0][0] > 20:
                before.popleft()
            row = {'kind': 'sample', 'sequence': sequence, 'at': now(), 'elapsed_seconds': tick - started,
                   'scheduled_monotonic_offset_seconds': scheduled - started,
                   'actual_monotonic_offset_seconds': tick - started,
                   'collector_scheduling_lateness_ms': max(0, tick - scheduled) * 1000,
                   'stable_pid': stable[0].pid, 'stable_alive_before_poll': True, 'errors': []}
            diagnostic = None
            try:
                raw, observation = fetch(opener, 'http://127.0.0.1:8351/debug/capacity', 2 * 1024 * 1024, 2)
                diagnostic = json.loads(raw)
                if diagnostic.get('pid') != api.pid:
                    raise ValueError('diagnostic API identity mismatch')
                app_at = dt.datetime.fromisoformat(diagnostic['at'].replace('Z', '+00:00'))
                poll_at = dt.datetime.fromisoformat(observation['started_at'])
                received_at = dt.datetime.fromisoformat(observation['finished_at'])
                observation['server_at_after_request_start_ms'] = (app_at - poll_at).total_seconds() * 1000
                observation['receive_after_server_at_ms'] = (received_at - app_at).total_seconds() * 1000
                observation['clock_scope'] = 'wall-clock comparison on same host; scheduling/network/server work are not separately identified'
                row.update(diagnostics=diagnostic, observation=observation)
                successful_polls += 1
            except Exception as error:
                diagnostic = None
                row['errors'].append(type(error).__name__)
                failed_polls += 1
            row['stable_alive_after_poll'] = alive(*stable)
            if diagnostic is not None:
                reasons = triggers(previous, diagnostic)
                row['trigger_reasons'] = reasons
                previous = diagnostic
                if reasons and len(record['captures']) < 3 and tick - last_capture >= 15 and alive(*stable):
                    number = len(record['captures']) + 1
                    capture = {'number': number, 'at': now(), 'sample_sequence': sequence,
                               'reasons': reasons, 'stable_pid': stable[0].pid, 'stable_alive_before_capture': True}
                    try:
                        raw, observation = fetch(opener, 'http://127.0.0.1:8351/debug/pprof/goroutine', 5 * 1024 * 1024, 3)
                        name = f'goroutine-{number}.pprof'
                        (output / name).write_bytes(raw)
                        capture.update(file=name, available=True, **observation)
                    except Exception as error:
                        capture.update(available=False, error=type(error).__name__)
                    capture['stable_alive_after_capture'] = alive(*stable)
                    capture['load_scope'] = ('load was alive at both guards; exact profile sampling is not atomic'
                                             if capture['stable_alive_after_capture'] else 'generator exited during capture; not wholly load-time')
                    record['captures'].append(capture)
                    windows.append({'trigger': capture, 'pre_samples': [sample for _, sample in before], 'trigger_sample_sequence': sequence,
                                    'samples': [], 'ends_monotonic': time.monotonic() + 10})
                    last_capture = tick
            row['poll_and_capture_elapsed_ms'] = (time.monotonic() - tick) * 1000
            row['missed_poll_intervals'] = max(0, int((time.monotonic() - scheduled) // 1))
            row['next_cadence_restarted_after_overrun'] = time.monotonic() > scheduled + 1
            timeline.write(json.dumps(row, ensure_ascii=False) + '\n')
            timeline.flush()
            before.append((tick, row))
            for window in windows:
                if window['ends_monotonic'] >= tick:
                    window['samples'].append(row)
            # Do not burst catch-up requests after a slow fetch/profile. The
            # missed schedule is recorded above; subsequent cadence restarts.
            scheduled = max(scheduled + 1, time.monotonic())
            while time.monotonic() < scheduled and not (control / 'done.json').exists():
                time.sleep(.05)
        record['finished_at'] = now()
        record['elapsed_seconds'] = time.monotonic() - started
        record['samples'] = sequence
        record['successful_polls'] = successful_polls
        record['failed_polls'] = failed_polls
        record['first_sample_at'] = first_sample_at
        record['last_sample_at'] = last_sample_at
        record['maximum_sample_start_gap_ms'] = maximum_sample_gap_ms
        record['poll_coverage_complete'] = successful_polls > 0 and failed_polls == 0
        record['stopped_by_control'] = (control / 'done.json').exists() or (control / 'exit-status.json').exists()
        for index, window in enumerate(windows, 1):
            window.pop('ends_monotonic')
            window['post_scope'] = 'up to 10 seconds while stable generator remained observable; early exit truncates this window'
            write_new(output / f'trigger-{index}-window.json', window)
        record['limits'] = ['Extra one-second polling and profiles add overhead; not a scored formal run.',
                            'Global pool counters include background work; request spans do not invent unavailable acquisition-only timings.',
                            'Slow traces appear on request completion. An in-flight stall is instead eligible through global wait deltas.',
                            'Three captures are a hard cap, including failed attempts; no CPU profile or native dump is taken.']
        write_new(output / 'manifest.json', record)
    print(json.dumps({'task': 'CAP-06B', 'samples': sequence, 'captures': len(record['captures']),
                      'stableObserved': record['stable_observed'], 'errors': len(record['errors']),
                      'successfulPolls': successful_polls, 'failedPolls': failed_polls,
                      'coverageComplete': record['poll_coverage_complete']}))
    return 0 if record['stable_observed'] and successful_polls > 0 and failed_polls == 0 and not record['errors'] else 1


if __name__ == '__main__':
    try:
        raise SystemExit(main())
    except Exception as error:
        print('cap06-tail: ' + type(error).__name__ + '; details suppressed')
        raise SystemExit(1)
