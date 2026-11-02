"""Nine real loopback telemetry clients plus independent control RTT polling.

Only synthetic collector records/resources; no business HTTP, database, PTS or
cloud calls. This checks transport queue/ACK/clock-RTT cost, not service capacity.
The partial native record set intentionally remains incomplete/unknown=None.
"""
import ast
import datetime as dt
import argparse
from concurrent.futures import ThreadPoolExecutor
import hashlib
import http.client
import importlib.util
import json
import math
import os
import re
from pathlib import Path
import secrets
import threading
import time
import uuid
from unittest.mock import patch

REPO = Path(__file__).resolve().parents[2]


def module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    value = importlib.util.module_from_spec(spec); spec.loader.exec_module(value)
    return value


def handler_hash(raw):
    tree = ast.parse(raw)
    node = next(node for node in tree.body if isinstance(node, ast.FunctionDef) and node.name == 'handler_type')
    return hashlib.sha256(ast.dump(node, include_attributes=False).encode()).hexdigest()


def listener_hash(raw):
    tree = ast.parse(raw)
    node = next(node for node in tree.body if isinstance(node, ast.ClassDef) and node.name == 'CollectorHTTPServer')
    return hashlib.sha256(ast.dump(node, include_attributes=False).encode()).hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--baseline-backlog-five', action='store_true', help='explicit diagnostic override only; production remains the imported listener class')
    args = parser.parse_args()
    harness_sha = hashlib.sha256(Path(__file__).read_bytes()).hexdigest()
    root = REPO.parent / ('.runtime-cap10-pts-http-' + uuid.uuid4().hex)
    root.mkdir(mode=0o700)
    collector_file = REPO / 'scripts/pts/cap10-pts-collector.py'
    phase_file = REPO / 'scripts/pts/cap10-pts-phase.py'
    collector = module('cap10_http_actual_store', collector_file)
    phase = module('cap10_http_actual_routes', phase_file)
    frozen = {str(path.relative_to(REPO)): hashlib.sha256(path.read_bytes()).hexdigest()
              for path in (collector_file, phase_file, REPO / 'scripts/cap10-cohort.py', REPO / 'scripts/pts/cap10-pts-contract.groovy')}
    route_hash = handler_hash(phase_file.read_text(encoding='utf-8'))
    contract = (REPO / 'scripts/pts/cap10-pts-contract.groovy').read_text(encoding='utf-8')
    frame_limit = int(re.search(r'MAX_FRAME_RECORDS = ([0-9]+)', contract).group(1))
    assert frame_limit == 128
    pool, engines = 4096, 9
    rounds = 1024 // frame_limit
    run_id, token = 'synthetic-http-' + uuid.uuid4().hex, secrets.token_hex(32)
    config = {'runId': run_id, 'phase': 'warm', 'rate': 5000, 'seconds': 10,
        'apiUrl': 'http://127.0.0.1:9', 'poolSize': pool, 'expectedEngines': engines, 'ptsConcurrency': 4105,
        'fixtureSHA256': 'a' * 64, 'scriptSHA256': 'b' * 64, 'controlToken': token,
        'relations': [{'slot': slot, 'userId': str(slot + 10001), 'noteId': str(slot + 20001),
            'active': 0, 'version': 0, 'token': 'SYNTHETIC-ONLY'} for slot in range(pool)]}
    ids = [str(uuid.UUID(int=index + 1)) for index in range(engines)]
    ordinals = [[] for _ in ids]
    for ordinal in range(50000):
        rows = ordinals[(ordinal % pool) % engines]
        if len(rows) < rounds * frame_limit: rows.append(ordinal)
    calls, errors, services, accepts = [], [], [], {}
    metrics_lock = threading.Lock()
    local = threading.local()
    class TracedLock:
        def __init__(self): self.lock = threading.RLock()
        def __enter__(self):
            before = time.perf_counter_ns(); self.lock.acquire()
            self.entered = time.perf_counter_ns()
            if hasattr(local, 'service'): local.service['lockWaitMs'] += (self.entered - before) / 1000000
            return self
        def __exit__(self, *args):
            if hasattr(local, 'service'): local.service['lockHeldMs'] += (time.perf_counter_ns() - self.entered) / 1000000
            self.lock.release()
    finished = threading.Event()
    store = server = serving = polling = sampling = None
    progress_samples = []
    result = {'scope': __doc__, 'passed': False, 'businessHTTPRequests': 0, 'cloudCalls': 0,
              'realNativeWindowMeasured': False, 'sourceSHA256': frozen, 'phaseHandlerASTSHA256': route_hash, 'harnessSourceSHA256': harness_sha,
              'baselineBacklogOverride': args.baseline_backlog_five, 'phaseListenerASTSHA256': listener_hash(phase_file.read_text(encoding='utf-8'))}
    actual_fsync = os.fsync
    original_loads = json.loads
    def timed_fsync(fd):
        before = time.perf_counter_ns()
        try: return actual_fsync(fd)
        finally:
            if hasattr(local, 'service'): local.service['fsyncMs'] += (time.perf_counter_ns() - before) / 1000000
    def timed_loads(*args, **kwargs):
        before = time.perf_counter_ns()
        try: return original_loads(*args, **kwargs)
        finally:
            if hasattr(local, 'service'): local.service['jsonDecodeMs'] += (time.perf_counter_ns() - before) / 1000000
    began = time.perf_counter()
    try:
        with patch.object(collector.os, 'fsync', wraps=timed_fsync) as synced, patch.object(phase.json, 'loads', new=timed_loads):
            store = collector.EngineStore(config, root / 'records.ndjson')
            gate = TracedLock()
            base_handler = phase.handler_type(store, token, gate)
            class Handler(base_handler):
                def handle_operation(self, method):
                    before = time.perf_counter_ns()
                    local.service = {'peerPort': self.client_address[1], 'path': self.path, 'startedNano': before,
                        'lockWaitMs': 0, 'lockHeldMs': 0, 'jsonDecodeMs': 0, 'fsyncMs': 0, 'replyMs': 0}
                    try: return super().handle_operation(method)
                    finally:
                        local.service['totalMs'] = (time.perf_counter_ns() - before) / 1000000
                        with metrics_lock: services.append(local.service)
                        del local.service
                def reply(self, *args):
                    before = time.perf_counter_ns()
                    try: return super().reply(*args)
                    finally:
                        if hasattr(local, 'service'): local.service['replyMs'] += (time.perf_counter_ns() - before) / 1000000
            class Server(phase.CollectorHTTPServer):
                if args.baseline_backlog_five: request_queue_size = 5
                def get_request(self):
                    connection, address = super().get_request()
                    with metrics_lock: accepts[address[1]] = time.perf_counter_ns()
                    return connection, address
            server = Server(('127.0.0.1', 0), Handler)
            server.daemon_threads = True
            serving = threading.Thread(target=server.serve_forever, daemon=True); serving.start()
            def request(path, body=None, actor='independent-control'):
                before = time.perf_counter_ns(); wall = time.time_ns() / 1000000
                raw = None if body is None else json.dumps(body).encode()
                encoded = time.perf_counter_ns()
                connection = http.client.HTTPConnection('127.0.0.1', server.server_port, timeout=5)
                try:
                    connection.connect(); connected = time.perf_counter_ns()
                    port = connection.sock.getsockname()[1]
                    connection.request('GET' if body is None else 'POST', path, body=raw,
                        headers={'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json', 'Connection': 'close'})
                    sent = time.perf_counter_ns()
                    response = connection.getresponse(); headers = time.perf_counter_ns()
                    assert response.status == 200
                    data = response.read(); received = time.perf_counter_ns()
                    value = json.loads(data)
                finally: connection.close()
                elapsed = (time.perf_counter_ns() - before) / 1000000
                sample = {'path': path, 'actor': actor, 'rttMs': elapsed,
                    'peerPort': port, 'startedNano': before, 'encodeMs': (encoded - before) / 1000000,
                    'connectMs': (connected - encoded) / 1000000, 'sendMs': (sent - connected) / 1000000,
                    'headersMs': (headers - sent) / 1000000, 'bodyMs': (received - headers) / 1000000,
                    'uncertaintyMs': elapsed / 2 + 1, 'withinClockRTTBudget': elapsed <= 98,
                    'seq': body.get('seq') if body else None, 'records': len(body.get('records', [])) if body else 0}
                with metrics_lock: calls.append(sample)
                clock = {'uncertaintyMs': elapsed / 2 + 1, 'roundTripMs': elapsed,
                         'offsetMs': value['serverUnixMs'] - (wall + elapsed / 2)}
                return value, clock
            clocks = [request('/control', actor=engine)[1] for engine in ids]
            registration_barrier = threading.Barrier(engines)
            def register(index):
                slots = list(range(index, pool, engines))
                registration_barrier.wait(timeout=6)
                reply, observed = request('/register', {'runId': run_id, 'engineId': ids[index], 'slots': slots,
                    'readyWorkers': len(slots), 'scriptSHA256': 'b' * 64, 'fixtureSHA256': 'a' * 64}, ids[index])
                if observed['uncertaintyMs'] < clocks[index]['uncertaintyMs']: clocks[index] = observed
                return reply
            with ThreadPoolExecutor(max_workers=engines) as executor:
                replies = list(executor.map(register, range(engines)))
            start = request('/control')[0]['startUnixMs']
            assert start is not None
            def resources():
                return {'cpuTimeNs': time.process_time_ns(), 'monotonicNs': time.monotonic_ns(),
                        'rssBytes': 100000000, 'logicalCPUs': os.cpu_count()}
            for index, engine in enumerate(ids):
                reply, new_clock = request('/batch', {'runId': run_id, 'engineId': engine, 'seq': 0,
                    'records': [], 'clock': clocks[index], 'resources': resources()}, engine)
                assert reply['ack'] == 0
                if new_clock['uncertaintyMs'] < clocks[index]['uncertaintyMs']: clocks[index] = new_clock
            def poll():
                try:
                    while not finished.is_set():
                        request('/control')
                        finished.wait(.2)
                except BaseException as error:
                    with metrics_lock: errors.append(type(error).__name__)
            polling = threading.Thread(target=poll, daemon=True); polling.start()
            def sample_progress():
                try:
                    while not finished.is_set():
                        before = time.perf_counter_ns()
                        local.service = {'peerPort': None, 'path': 'controller-progress', 'startedNano': before,
                            'lockWaitMs': 0, 'lockHeldMs': 0, 'jsonDecodeMs': 0, 'fsyncMs': 0, 'replyMs': 0}
                        with gate: snapshot = store.progress()
                        phase.save(root / 'latest.json', {'at': dt.datetime.now(dt.timezone.utc).isoformat(), 'summary': snapshot}, replace=True)
                        local.service['totalMs'] = (time.perf_counter_ns() - before) / 1000000
                        progress_samples.append({'records': snapshot['counts']['mutation'], **local.service})
                        del local.service
                        finished.wait(1)
                except BaseException as error:
                    with metrics_lock: errors.append(type(error).__name__)
            sampling = threading.Thread(target=sample_progress, daemon=True); sampling.start()
            barrier = threading.Barrier(engines)
            ready_times = [start + max(rows[(round_ + 1) * frame_limit - 1] for rows in ordinals) / 5 + 3 for round_ in range(rounds)]
            def engine(index):
                clock = clocks[index]
                for round_ in range(rounds):
                    while time.time_ns() / 1000000 < ready_times[round_]:
                        if time.perf_counter() - began >= 45: raise TimeoutError('bounded local protocol deadline')
                        time.sleep(.005)
                    barrier.wait(timeout=6)
                    records = []
                    for ordinal in ordinals[index][round_ * frame_limit:(round_ + 1) * frame_limit]:
                        slot, previous = ordinal % pool, ordinal // pool
                        wanted = previous % 2 == 0
                        records.append({'ordinal': ordinal, 'slot': slot, 'kind': 'mutation',
                            'startedUnixMs': start + ordinal / 5, 'elapsedMs': 1 + ordinal % 997 / 997,
                            'status': 200, 'wanted': wanted, 'observedLiked': wanted,
                            'previousVersion': previous, 'observedVersion': previous + 1, 'observedNoteId': str(slot + 20001)})
                    body = {'runId': run_id, 'engineId': ids[index], 'seq': round_ + 1,
                            'records': records, 'clock': clock, 'resources': resources()}
                    assert len(collector.canonical(body)) <= 16 << 20
                    reply, observed = request('/batch', body, ids[index])
                    assert reply['ack'] == round_ + 1
                    if observed['uncertaintyMs'] < clock['uncertaintyMs']: clock = observed
                return True
            with ThreadPoolExecutor(max_workers=engines) as executor:
                futures = [executor.submit(engine, index) for index in range(engines)]
                for future in futures:
                    try: assert future.result(timeout=45)
                    except BaseException as error: errors.append(type(error).__name__)
            finished.set(); polling.join(timeout=6); sampling.join(timeout=6)
            assert not polling.is_alive() and not sampling.is_alive()
            summary = store.progress()
            expected = engines * rounds * frame_limit
            assert summary['counts']['mutation'] == summary['counts']['attempted'] == summary['counts']['completed'] == expected
            assert summary['unknown'] is None and summary['complete'] is False
            assert not summary['stop']
            raw_hash = hashlib.sha256((root / 'records.ndjson').read_bytes()).hexdigest()
            assert raw_hash == summary['evidenceSHA256']
            controls = [sample['rttMs'] for sample in calls if sample['path'] == '/control']
            batches = [sample['rttMs'] for sample in calls if sample['path'] == '/batch']
            result.update(passed=not errors and all(sample['withinClockRTTBudget'] for sample in calls),
                syntheticRecords=expected, syntheticScheduleRate=5000, syntheticScheduleSeconds=10,
                actualHTTPClients=engines, frameRecordLimit=frame_limit, simultaneousBurstRounds=rounds,
                maximumControlRTTMs=max(controls), maximumBatchRTTMs=max(batches), maximumAnyRTTMs=max(s['rttMs'] for s in calls),
                fsyncCalls=synced.call_count, journalSHA256=raw_hash, summary=summary,
                clientTransport='direct HTTPConnection; no per-request HTTPS context or proxy discovery',
                listenerBacklog=server.request_queue_size, progressSamples=progress_samples,
                fsyncScope='actual collector journal and actual 1s progress/latest saves',
                scopeLimit='Physical local HTTP queue/RTT only. Engine resources and native HTTP results are synthetic; this partial record set is not native-window or business-capacity evidence.')
    finally:
        finished.set()
        if polling is not None: polling.join(timeout=6)
        if sampling is not None: sampling.join(timeout=6)
        if server is not None:
            if serving is not None and serving.is_alive(): server.shutdown()
            server.server_close()
        if serving is not None and serving.ident is not None: serving.join(timeout=5)
        if store is not None: store.close()
        result.update(wallSeconds=time.perf_counter() - began, calls=calls, errors=errors, serverServices=services,
            serverAccepts={str(port): nano for port, nano in accepts.items()},
            sourceBytesStable=all(hashlib.sha256((REPO / path).read_bytes()).hexdigest() == digest for path, digest in frozen.items())
                and handler_hash(phase_file.read_text(encoding='utf-8')) == route_hash
                and listener_hash(phase_file.read_text(encoding='utf-8')) == result['phaseListenerASTSHA256']
                and hashlib.sha256(Path(__file__).read_bytes()).hexdigest() == harness_sha)
        result['passed'] = result['passed'] and result['sourceBytesStable']
        with (root / 'result.json').open('x', encoding='utf-8') as stream:
            json.dump(result, stream, indent=2); stream.write('\n')
        print(json.dumps({key: result.get(key) for key in ('passed', 'syntheticRecords', 'maximumControlRTTMs',
            'maximumBatchRTTMs', 'maximumAnyRTTMs', 'wallSeconds', 'errors')} | {'directory': str(root)}))
    return 0 if result['passed'] else 1


if __name__ == '__main__': raise SystemExit(main())
