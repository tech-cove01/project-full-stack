"""Bounded synthetic collector replay; never emits HTTP or business/cloud load.

Uses the actual store and durable journal with 4096 exclusive slots, 9 engines,
5000/s ordinal schedules and a full 180s virtual window (899500 guarded records).
Engine records flush every 5s, staggered by 200ms; control polls every 200ms and
summary polls every 1s. Wall costs and real local process RSS are observed, not
reported as service capacity or fabricated remote-engine resource measurements.
"""
import argparse
import ctypes
import gc
import hashlib
import json
import math
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import time
import types
import uuid
from unittest.mock import patch

REPO = Path(__file__).resolve().parents[2]
SOURCE = Path(__file__).with_name('cap10-pts-collector.py')


def rss():
    if sys.platform == 'win32':
        from ctypes import wintypes
        class Counters(ctypes.Structure):
            _fields_ = [('cb', wintypes.DWORD), ('PageFaultCount', wintypes.DWORD),
                *[(name, ctypes.c_size_t) for name in ('PeakWorkingSetSize', 'WorkingSetSize', 'QuotaPeakPagedPoolUsage',
                'QuotaPagedPoolUsage', 'QuotaPeakNonPagedPoolUsage', 'QuotaNonPagedPoolUsage', 'PagefileUsage', 'PeakPagefileUsage')]]
        value = Counters(); value.cb = ctypes.sizeof(value)
        process = ctypes.windll.kernel32.GetCurrentProcess
        process.restype = wintypes.HANDLE
        memory = ctypes.windll.psapi.GetProcessMemoryInfo
        memory.argtypes = [wintypes.HANDLE, ctypes.POINTER(Counters), wintypes.DWORD]
        memory.restype = wintypes.BOOL
        if not memory(process(), ctypes.byref(value), value.cb):
            raise OSError('actual process RSS unavailable')
        return value.WorkingSetSize
    status = Path('/proc/self/status').read_text()
    return next(int(line.split()[1]) * 1024 for line in status.splitlines() if line.startswith('VmRSS:'))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--directory', type=Path)
    parser.add_argument('--summary-mode', choices=('full', 'progress'), default='progress')
    parser.add_argument('--latency-distribution', choices=('repeated', 'unique'), default='repeated')
    parser.add_argument('--collector-revision', help='explicit full Git SHA for an archived, trusted collector implementation')
    parser.add_argument('--frame-record-limit', type=int, default=100000)
    parser.add_argument('--diagnostic-profile', action='store_true', help='observe GC, state table growth and batch CPU without changing GC policy')
    args = parser.parse_args()
    if args.diagnostic_profile:
        import psutil
        observed_process = psutil.Process()
        psutil.cpu_percent(None); observed_process.cpu_percent(None)
        def host_sample():
            return {'hostCPUPercent': psutil.cpu_percent(None),
                'processCPUPercent': observed_process.cpu_percent(None),
                'availableMemoryBytes': psutil.virtual_memory().available,
                'logicalCPUs': psutil.cpu_count()}
    if not 1 <= args.frame_record_limit <= 100000:
        raise ValueError('bounded collector frame record limit required')
    directory = (args.directory or (REPO.parent / ('.runtime-cap10-pts-scale-' + uuid.uuid4().hex))).resolve()
    if directory.parent != REPO.parent or not directory.name.startswith('.runtime-cap10-pts-scale-') or directory.exists():
        raise ValueError('new workspace-owned synthetic replay directory required')
    if shutil.disk_usage(directory.parent).free < 1 << 30:
        raise ValueError('synthetic full journal needs at least 1 GiB free disk')
    directory.mkdir(mode=0o700)
    source_bytes = SOURCE.read_bytes()
    if args.collector_revision:
        if not re.fullmatch(r'[a-f0-9]{40}', args.collector_revision):
            raise ValueError('explicit full collector Git revision required')
        source_bytes = subprocess.run(['git', 'show', args.collector_revision + ':scripts/pts/cap10-pts-collector.py'],
            cwd=REPO, check=True, capture_output=True, timeout=10).stdout
    collector = types.ModuleType('cap10_actual_scale_store')
    collector.__file__ = str(SOURCE)
    exec(compile(source_bytes, str(SOURCE), 'exec'), collector.__dict__)
    frozen = {str(path.relative_to(REPO)): hashlib.sha256(path.read_bytes()).hexdigest()
        for path in (REPO / 'scripts/cap10-cohort.py', Path(__file__))}
    if not args.collector_revision:
        frozen[str(SOURCE.relative_to(REPO))] = hashlib.sha256(source_bytes).hexdigest()
    for name, raw in (('collector-source.py', source_bytes), ('harness-source.py', Path(__file__).read_bytes()),
                      ('cohort-source.py', (REPO / 'scripts/cap10-cohort.py').read_bytes())):
        with (directory / name).open('xb') as stream: stream.write(raw)
    pool, engines, rate, seconds = 4096, 9, 5000, 180
    stamp = [1000000]
    clock = {'uncertaintyMs': 1, 'offsetMs': 0, 'roundTripMs': 1}
    config = {'runId': 'synthetic-scale-' + uuid.uuid4().hex, 'phase': 'stable', 'rate': rate, 'seconds': seconds,
        'apiUrl': 'http://127.0.0.1:8281', 'poolSize': pool, 'expectedEngines': engines, 'ptsConcurrency': 4105,
        'fixtureSHA256': 'a' * 64, 'scriptSHA256': 'b' * 64, 'controlToken': 'SYNTHETIC-NO-LIVE-CREDENTIAL',
        'relations': [{'slot': slot, 'userId': str(slot + 10001), 'noteId': str(slot + 20001),
                       'active': 0, 'version': 0, 'token': 'SYNTHETIC-' + str(slot)} for slot in range(pool)]}
    ids = [str(uuid.UUID(int=index + 1)) for index in range(engines)]
    pending, sequences = [[] for _ in ids], [0] * engines
    samples, summary_costs, batch_costs, control_costs = [], [], [], []
    max_frame_bytes = max_rss = total = 0
    control_calls = summary_calls = batch_calls = 0
    journal = directory / 'records.ndjson'
    actual_fsync = os.fsync
    profile = {'frameHashMs': [], 'appendMs': [], 'fsyncMs': [], 'batchOtherMs': []}
    slowest_batch = {}
    gc_events, state_resizes, slow_batches = [], [], []
    active_batch, collecting = {}, {}
    def observe_gc(phase, info):
        if phase == 'start':
            collecting[info['generation']] = (time.perf_counter_ns(), time.process_time_ns(), dict(active_batch))
        else:
            began, cpu_began, batch = collecting.pop(info['generation'])
            gc_events.append({**batch, 'generation': info['generation'], 'collected': info['collected'],
                'wallMs': (time.perf_counter_ns() - began) / 1000000,
                'cpuMs': (time.process_time_ns() - cpu_began) / 1000000})
    def timed_fsync(fd):
        call = time.perf_counter()
        try: return actual_fsync(fd)
        finally: profile['fsyncMs'].append((time.perf_counter() - call) * 1000)
    started, cpu_started = time.perf_counter(), time.process_time()
    store = None
    result = {'scope': __doc__, 'businessRequests': 0, 'cloudCalls': 0, 'passed': False, 'toolReadyFor5000': False,
              'sourceSHA256': frozen, 'fixture': 'synthetic-only; no real account/note/session', 'directory': str(directory)}
    result.update(collectorRevision=args.collector_revision, collectorSourceSHA256=hashlib.sha256(source_bytes).hexdigest(),
                  summaryMode=args.summary_mode, latencyDistribution=args.latency_distribution,
                  frameRecordLimit=args.frame_record_limit,
                  readinessScope='This synthetic collector replay only; not full-generator, service capacity or measured real network/PTS clock readiness.')
    try:
        if args.diagnostic_profile: gc.callbacks.append(observe_gc)
        with patch.object(collector.os, 'fsync', wraps=timed_fsync) as synced:
            store = collector.EngineStore(config, journal, clock=lambda: stamp[0])
            def timed_method(method, field):
                def timed(*values, **kwargs):
                    call = time.perf_counter()
                    try: return method(*values, **kwargs)
                    finally: profile[field].append((time.perf_counter() - call) * 1000)
                return timed
            store._frame_hash = timed_method(store._frame_hash, 'frameHashMs')
            store._append = timed_method(store._append, 'appendMs')
            for index, engine in enumerate(ids):
                slots = list(range(index, pool, engines))
                store.register({'runId': config['runId'], 'engineId': engine, 'slots': slots,
                    'readyWorkers': len(slots), 'scriptSHA256': config['scriptSHA256'], 'fixtureSHA256': config['fixtureSHA256']})
            start = store.control()['startUnixMs']
            planned = collector.planned_total(rate, seconds)
            next_ordinal = 0
            for elapsed in range(0, 185001, 200):
                stamp[0] = start + elapsed
                while next_ordinal < planned:
                    ordinal = next_ordinal
                    duration = ordinal % 100 + 1 if args.latency_distribution == 'repeated' else 1 + 99 * (((ordinal * 104729) % planned) + 1) / (planned + 1)
                    due = ordinal * 1000 / rate
                    if due + duration > elapsed:
                        break
                    slot, previous = ordinal % pool, ordinal // pool
                    wanted = previous % 2 == 0
                    pending[slot % engines].append({'ordinal': ordinal, 'slot': slot, 'kind': 'mutation',
                        'startedUnixMs': start + due, 'elapsedMs': duration, 'status': 200,
                        'wanted': wanted, 'observedLiked': wanted, 'previousVersion': previous,
                        'observedVersion': previous + 1, 'observedNoteId': str(slot + 20001)})
                    next_ordinal += 1
                for index, engine in enumerate(ids):
                    call = time.perf_counter(); store.control()
                    control_costs.append((time.perf_counter() - call) * 1000); control_calls += 1
                    if elapsed == 0 or (elapsed >= 5000 + index * 200 and (elapsed - index * 200) % 5000 == 0):
                        snapshot = pending[index]; pending[index] = []
                        frames = [snapshot[offset:offset + args.frame_record_limit]
                                  for offset in range(0, len(snapshot), args.frame_record_limit)] or [[]]
                        for frame in frames:
                            body = {'runId': config['runId'], 'engineId': engine, 'seq': sequences[index],
                                'records': frame, 'clock': clock,
                                'resources': {'cpuTimeNs': elapsed * 100000 + 100000000,
                                    'monotonicNs': elapsed * 1000000 + 1000000000 + (sequences[index] if args.frame_record_limit != 100000 else 0),
                                    'rssBytes': 100000000, 'logicalCPUs': 2}}
                            size = len(collector.canonical(body))
                            max_frame_bytes = max(max_frame_bytes, size)
                            assert size <= 16 << 20 and len(body['records']) <= args.frame_record_limit
                            if args.diagnostic_profile:
                                active_batch.update(virtualElapsedMs=elapsed, engineIndex=index, seq=sequences[index],
                                    recordsBefore=len(store._records), firstOrdinal=frame[0]['ordinal'] if frame else None,
                                    lastOrdinal=frame[-1]['ordinal'] if frame else None)
                                gc_before, bytes_before = len(gc_events), sys.getsizeof(store._records)
                                batch_cpu, batch_thread_cpu = time.process_time_ns(), time.thread_time_ns()
                            call = time.perf_counter(); ack = store.batch(body)
                            cost = (time.perf_counter() - call) * 1000
                            batch_costs.append(cost)
                            profile['batchOtherMs'].append(cost - profile['frameHashMs'][-1] - profile['appendMs'][-1])
                            if not slowest_batch or cost > slowest_batch['totalMs']:
                                slowest_batch = {'virtualElapsedMs': elapsed, 'records': len(body['records']), 'frameBytes': size,
                                    'totalMs': cost, **{key: values[-1] for key, values in profile.items()}}
                            if args.diagnostic_profile:
                                detail = {**active_batch, 'recordsAfter': len(store._records),
                                    'cpuMs': (time.process_time_ns() - batch_cpu) / 1000000,
                                    'threadCPUTimeMs': (time.thread_time_ns() - batch_thread_cpu) / 1000000,
                                    'wallMs': cost, 'stateBytesBefore': bytes_before,
                                    'stateBytesAfter': sys.getsizeof(store._records),
                                    'gcEvents': gc_events[gc_before:]}
                                if detail['stateBytesAfter'] != bytes_before: state_resizes.append(detail)
                                if cost >= 50: slow_batches.append({**detail, 'host': host_sample()})
                                active_batch.clear()
                            assert ack['ack'] == sequences[index]
                            total += len(frame)
                            sequences[index] += 1; batch_calls += 1
                if elapsed % 1000 == 0:
                    call = time.perf_counter()
                    snapshot = store.summary() if args.summary_mode == 'full' else store.progress()
                    summary_costs.append((time.perf_counter() - call) * 1000); summary_calls += 1
                    actual_rss = rss(); max_rss = max(max_rss, actual_rss)
                    samples.append({'virtualElapsedMs': elapsed, 'records': snapshot['counts']['mutation'],
                                    'rssBytes': actual_rss, 'summaryWallMs': summary_costs[-1],
                                    **({'host': host_sample()} if args.diagnostic_profile else {})})
                    assert actual_rss < 2 << 30, 'actual local collector RSS reached 2 GiB'
                    assert not snapshot['stop'], snapshot['stopReason']
            assert next_ordinal == total == planned == 899500 and all(not rows for rows in pending)
            for index, engine in enumerate(ids):
                counts = store._raw_counts(engine)
                assert store.complete({'runId': config['runId'], 'engineId': engine, 'lastSeq': sequences[index] - 1,
                    'counts': counts, 'clock': clock, 'stoppedOffering': True, 'inFlight': 0})['complete']
            final = store.summary()
            assert final['complete'] and final['collectionValid'] and final['childrenConverged']
            assert final['unknown'] == final['generatorTimingViolations'] == final['notOffered'] == 0
            assert final['latencySampleCount'] == final['counts']['mutation'] == 899500
            assert final['endUnixMs'] - final['startUnixMs'] == 180000
            expected = {'avg': 50.5, 'min': 1, 'med': 50.5, 'p(95)': 95.05, 'p(99)': 99.01, 'max': 100}
            if args.latency_distribution == 'unique':
                assert math.gcd(104729, planned) == 1
                expected.update(min=1 + 99 / (planned + 1), max=100 - 99 / (planned + 1),
                    **{'p(95)': 1 + 99 * ((planned - 1) * .95 + 1) / (planned + 1),
                       'p(99)': 1 + 99 * ((planned - 1) * .99 + 1) / (planned + 1)})
            for key, value in expected.items():
                assert math.isclose(final['allLatency'][key], value, abs_tol=1e-8)
            assert synced.call_count >= batch_calls + engines * 3 + 2
            result['fsyncCalls'] = synced.call_count
        store.close()
        digest = hashlib.sha256()
        with journal.open('rb') as stream:
            for block in iter(lambda: stream.read(1 << 20), b''): digest.update(block)
        assert final['evidenceSHA256'] == digest.hexdigest()
        wall = time.perf_counter() - started
        risk = max(summary_costs + batch_costs + control_costs) >= 98
        result.update(passed=True, records=total, virtualNativeSeconds=180, launchGuardMs=100,
            engineCount=engines, poolSize=pool, ptsConcurrency=4105, controlCalls=control_calls,
            summaryCalls=summary_calls, batchCalls=batch_calls, maximumFrameBytes=max_frame_bytes,
            maximumObservedRSSBytes=max_rss, wallSeconds=wall, cpuSeconds=time.process_time() - cpu_started,
            acceptedSyntheticRecordsPerWallSecond=total / wall, maximumSummaryWallMs=max(summary_costs),
            maximumBatchWallMs=max(batch_costs), maximumControlWallMs=max(control_costs), clockBudgetCriticalSectionRisk=risk,
            toolReadyFor5000=not risk and total / wall >= 5000, finalSummary=final, samples=samples,
            slowestBatchProfile=slowest_batch,
            operationProfile={key: {'calls': len(values), 'sumMs': sum(values), 'maxMs': max(values)} for key, values in profile.items()},
            journalBytes=journal.stat().st_size, journalSHA256=digest.hexdigest(),
            resourceScope='Observed RSS is this replay process only. Engine telemetry resources are fixed synthetic protocol inputs; no actual PTS/Node/remote resource claim.')
    except BaseException as error:
        result.update(errorClass=type(error).__name__, wallSeconds=time.perf_counter() - started)
        raise
    finally:
        if args.diagnostic_profile: gc.callbacks.remove(observe_gc)
        if store is not None: store.close()
        result['sourceBytesStable'] = all(hashlib.sha256((REPO / name).read_bytes()).hexdigest() == digest for name, digest in frozen.items())
        result['passed'] = result['passed'] and result['sourceBytesStable']
        result['toolReadyFor5000'] = result['toolReadyFor5000'] and result['sourceBytesStable']
        if args.diagnostic_profile:
            result['diagnosticProfile'] = {'scope': 'Observer only; GC policy and collector implementation unchanged.',
                'psutilVersion': psutil.__version__,
                'gcEvents': gc_events, 'stateResizes': state_resizes, 'batchesAtLeast50Ms': slow_batches}
        with (directory / 'result.json').open('x', encoding='utf-8') as stream:
            json.dump(result, stream, indent=2); stream.write('\n')
        print(json.dumps({key: result.get(key) for key in ('passed', 'toolReadyFor5000', 'records', 'wallSeconds',
            'maximumObservedRSSBytes', 'maximumSummaryWallMs', 'maximumBatchWallMs', 'clockBudgetCriticalSectionRisk', 'directory')}))
    return 0 if result['passed'] and result['sourceBytesStable'] else 1


if __name__ == '__main__': raise SystemExit(main())
