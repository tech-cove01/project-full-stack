"""One reserved CAP10 JMeter phase; no live calls without explicit owned inputs."""
import argparse
import datetime as dt
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import importlib.util
import ipaddress
import json
import math
import os
from pathlib import Path
import secrets
import signal
import sys
import threading
import time
import urllib.parse

REPO = Path(__file__).resolve().parents[2]


def module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    value = importlib.util.module_from_spec(spec)
    sys.modules[name] = value
    spec.loader.exec_module(value)
    return value


def read(path):
    return json.loads(Path(path).read_text(encoding='utf-8-sig'))


def save(path, value, replace=False):
    path = Path(path)
    temporary = path.with_name(path.name + '.next') if replace else path
    with temporary.open('x', encoding='utf-8', newline='\n') as stream:
        json.dump(value, stream, ensure_ascii=False, indent=2)
        stream.write('\n')
        stream.flush()
        os.fsync(stream.fileno())
    if replace:
        os.replace(temporary, path)


def private_url(value, port):
    url = urllib.parse.urlsplit(value)
    if (url.scheme != 'http' or url.port != port or url.path not in ('', '/') or
            url.query or url.fragment or url.username or url.password):
        raise ValueError('fixed private HTTP endpoint required')
    address = ipaddress.ip_address(url.hostname)
    if address.version != 4 or not any(address in network for network in
            (ipaddress.ip_network('10.0.0.0/8'), ipaddress.ip_network('172.16.0.0/12'), ipaddress.ip_network('192.168.0.0/16'))):
        raise ValueError('RFC1918 ECS IPv4 required')
    return str(address)


def normalized(summary, seconds):
    """Compatibility envelope, explicitly sourced from complete JMeter records."""
    if summary.get('complete') is not True or summary.get('collectionValid') is not True or summary.get('unknownIsExact') is not True:
        raise ValueError('incomplete JMeter evidence cannot become a native summary')
    counts = summary['counts']
    required = ('planned', 'attempted', 'completed', 'mutation', 'noop', 'uncertain', 'rejected', 'invalid', 'dropped')
    if not all(type(counts.get(key)) is int and counts[key] >= 0 for key in required):
        raise ValueError('integer complete JMeter counts required')
    if counts['attempted'] != counts['completed'] or summary['unknown'] != counts['uncertain']:
        raise ValueError('JMeter terminal accounting differs')
    start, end = summary['startUnixMs'], summary['endUnixMs']
    if type(start) is not int or end != start + seconds * 1000:
        raise ValueError('JMeter native window differs')
    mapping = {'cap_like_attempted': counts['attempted'], 'cap_like_completed': counts['completed'],
        'cap_like_success': counts['mutation'] + counts['noop'], 'cap_like_mutation': counts['mutation'],
        'cap_like_noop': counts['noop'], 'cap_like_effect_unknown': counts['uncertain'],
        'cap_like_rejected': counts['rejected'], 'cap_like_content_errors': counts['invalid'],
        'cap_like_system_errors': summary.get('systemErrors'), 'cap_like_http_errors': summary.get('httpErrors'),
        'cap_like_recovery_reads': 0, 'cap_like_recovery_failed': 0,
        'cap_like_retired_relations': counts['uncertain'] + counts['invalid'] + counts['rejected'] + counts['noop'],
        'dropped_iterations': counts['dropped'], 'http_reqs{name:like-write}': counts['attempted']}
    if not all(type(value) is int and value >= 0 for value in mapping.values()):
        raise ValueError('HTTP/system classifications missing from full records')
    metrics = {key: {'count': value} for key, value in mapping.items()}
    for name, field in [('cap_like_all_ms', 'allLatency'), ('cap_like_mutation_ms', 'mutationLatency')]:
        metrics[name] = summary[field]
        if not isinstance(metrics[name], dict) or any(type(metrics[name].get(key)) not in (float, int) or
                not math.isfinite(metrics[name][key]) or metrics[name][key] < 0 for key in ('p(95)', 'p(99)', 'max')):
            raise ValueError('full-record percentiles unavailable')
    metrics['cap_native_scenario_start_unix_ms'] = {'min': start, 'max': start}
    metrics['cap_native_scenario_duration_ms'] = {'min': seconds * 1000, 'max': seconds * 1000}
    return {'sourceEngine': 'JMeter 5.6.3 on Alibaba PTS', 'source': 'complete authenticated collector records; not sampled PTS logs or k6 output',
            'quantileMethod': summary.get('quantileMethod'), 'metrics': metrics,
            'nativeScenario': {'startedAt': start, 'scheduledEndedAt': end, 'durationMs': seconds * 1000,
                'complete': not summary.get('stop'), 'startMetric': {'min': start, 'max': start},
                'durationMetric': {'min': seconds * 1000, 'max': seconds * 1000},
                'scope': 'All 512 registered slots, common ECS-clock offer window, full JMeter terminal records; platform report time is separate.'}}


def handler_type(store, token, lock):
    class Handler(BaseHTTPRequestHandler):
        protocol_version = 'HTTP/1.1'

        def log_message(self, *args):
            pass

        def handle_operation(self, method):
            if not secrets.compare_digest(self.headers.get('Authorization', ''), 'Bearer ' + token):
                self.reply(403, {'error': 'unauthorized'})
                return
            try:
                if self.path not in ('/control', '/register', '/batch', '/complete'):
                    self.reply(404, {'error': 'unknown endpoint'})
                    return
                if method == 'GET':
                    if self.path != '/control':
                        raise ValueError('wrong method')
                    with lock:
                        value = store.control()
                else:
                    size = int(self.headers.get('Content-Length', '-1'))
                    if not 1 <= size <= 16 * 1024 * 1024:
                        raise ValueError('bounded content length required')
                    payload = json.loads(self.rfile.read(size))
                    with lock:
                        value = getattr(store, self.path[1:])(payload)
                self.reply(200, value)
            except (ValueError, AssertionError, TypeError, KeyError, OSError):
                with lock:
                    if hasattr(store, 'fail'):
                        store.fail('collector_protocol_error')
                self.reply(409, {'error': 'collector evidence rejected', 'stop': True})

        def reply(self, code, value):
            raw = json.dumps(value).encode('utf-8')
            self.send_response(code)
            self.send_header('Content-Type', 'application/json')
            self.send_header('Content-Length', str(len(raw)))
            self.send_header('Cache-Control', 'no-store')
            self.end_headers()
            self.wfile.write(raw)

        def do_GET(self):
            self.handle_operation('GET')

        def do_POST(self):
            self.handle_operation('POST')
    return Handler


def poll_termination(client, scene_id, report_id, stop_sent):
    """A pending read must not consume the one safe scene-level Stop."""
    if not stop_sent:
        acknowledgement = client.stop(scene_id, report_id)
        stop_sent = any(acknowledgement.get(key) is True
                        for key in ('requested', 'terminalVerified', 'alreadyTerminal'))
    return stop_sent, client.terminal_evidence(scene_id, report_id)


def run(args):
    if sys.platform != 'linux' or os.environ.get('CAP10_PLATFORM') != 'linux' or os.environ.get('CAP10_LOAD_BACKEND') != 'pts':
        raise ValueError('explicit native Linux PTS execution required')
    owned = module('cap10_pts_owned', REPO / 'scripts/cap10-env.py')
    cfg_path = Path(os.environ['CAP10_PTS_CONFIG']).resolve()
    runtime = Path(os.environ['CAP10_RUNTIME']).resolve()
    if cfg_path.parent != runtime or not cfg_path.name.startswith('env-') or cfg_path.stat().st_mode & 0o077:
        raise ValueError('registered 0600 private PTS configuration required')
    cfg, source = read(cfg_path), read(args.input)
    host = private_url(cfg['collectorUrl'], 8481)
    if private_url(cfg['apiUrl'], 8281) != host or len(cfg['controlToken']) < 64 or cfg['expectedEngines'] != 2:
        raise ValueError('one fixed ECS and two PTS engines required')
    if args.evidence.resolve().parent != Path(os.environ['CAP_LIKE_EVIDENCE']).resolve():
        raise ValueError('fixed per-phase evidence directory required')
    args.evidence.mkdir()
    expected_root = owned.ROOT
    if not args.evidence.resolve().is_relative_to(expected_root):
        raise ValueError('same CAP10 Linux evidence root required')
    owned.transition()
    if not owned.read(expected_root / 'idle-result.json')['passed']:
        raise ValueError('cloud idle admission must pass before live PTS')
    phase = os.environ['CAP_LIKE_PHASE']; seconds = int(os.environ['CAP_LIKE_SECONDS'])
    rate = int(os.environ['CAP_LIKE_RATE'])
    if phase not in ('warm', 'stable') or seconds not in ((10,) if phase == 'warm' else (60, 180)) or not 1 <= rate <= 5000:
        raise ValueError('fixed bounded phase required')
    reservation_path = Path(os.environ['CAP10_RESERVATION_FILE']).resolve()
    reservation = read(reservation_path)
    tag = reservation['tag']
    if (reservation_path != expected_root / (tag + '-command.json') or reservation.get('loadBackend') != 'pts'
            or reservation['apiSourceSha'] != os.environ['CAP_SOURCE_SHA']
            or args.evidence.resolve() != expected_root / (tag + '-load') / (phase + '-pts')
            or seconds != reservation['warmSeconds' if phase == 'warm' else 'seconds']
            or rate != reservation['warmRate' if phase == 'warm' else 'rate']):
        raise ValueError('phase must belong to its existing same-batch reservation')
    admission_path = expected_root / 'pts-scene-admission.json'
    if (hashlib.sha256(admission_path.read_bytes()).hexdigest() != reservation['ptsAdmissionSHA256']
            or read(admission_path).get('passed') is not True):
        raise ValueError('reserved PTS scene admission differs')
    import psutil
    owner = read(runtime / 'cap10-active.private.json')
    parent = psutil.Process(owner['pid'])
    if (owner['tag'] != tag or parent.create_time() != owner['created']
            or parent.pid not in {p.pid for p in psutil.Process().parents()}):
        raise ValueError('PTS phase is not descended from the exact active reservation owner')
    if len(source['users']) != 512 or len(source['initial']) != 512:
        raise ValueError('512 hydrated exclusive facts required')
    relations = [{**source['initial'][slot], **user, 'slot': slot} for slot, user in enumerate(source['users'])]
    run_config = {**cfg, 'runId': secrets.token_hex(16), 'phase': phase, 'rate': rate, 'seconds': seconds,
        'fixtureSHA256': hashlib.sha256(Path(os.environ['CAP_LIKE_FIXTURE']).read_bytes()).hexdigest(),
        'relations': relations, 'apiUrl': cfg['apiUrl']}
    collector = module('cap10_pts_collector', Path(__file__).with_name('cap10-pts-collector.py'))
    api = module('cap10_pts_api', Path(__file__).with_name('cap10-pts-api.py'))
    client = api.Client(cfg['regionId'], args.evidence / 'api', credential_file=cfg.get('credentialsFile'),
                        allow_live=True, exclusive_scene=True, scene_id=cfg['sceneId'])
    store = collector.EngineStore(run_config, args.evidence / 'records.ndjson')
    lock = threading.RLock()
    server = ThreadingHTTPServer((host, 8481), handler_type(store, cfg['controlToken'], lock))
    server.daemon_threads = True
    serving = threading.Thread(target=server.serve_forever, daemon=True)
    serving.start()
    terminal = {'backend': 'pts', 'runId': run_config['runId'], 'remoteConverged': False,
                'reportId': None, 'failed': None, 'nativeEvidenceComplete': False}
    stop_requested = threading.Event()
    for sig in (signal.SIGTERM, signal.SIGINT):
        signal.signal(sig, lambda *_: stop_requested.set())
    started = time.monotonic(); report_id = None; stop_sent = False
    try:
        save(args.evidence / 'latest.json', {'at': dt.datetime.now(dt.timezone.utc).isoformat(), 'summary': store.summary()}, replace=True)
        scene = client.scene(cfg['sceneId'])['Scene']
        actual_files = {item['FileName']: item['Md5'].lower() for item in scene['FileList']}
        if actual_files != cfg['sceneFiles']:
            raise ValueError('uploaded PTS scene artifact bytes differ from frozen packet')
        if any(type(scene.get(key)) is not int or scene[key] != expected for key, expected in
               (('Duration', 600), ('Concurrency', 513), ('AgentCount', 2))):
            raise ValueError('fixed PTS lifecycle/concurrency configuration differs')
        response = client.start(cfg['sceneId'], runtime / ('pts-' + run_config['runId'] + '-start.private.json'),
            should_stop=lambda: stop_requested.is_set() or Path(os.environ['CAP_STOP_FILE']).exists() or store.control()['stop'])
        report_id = response['reportId']; terminal['reportId'] = report_id
        deadline = started + seconds + 150
        while time.monotonic() < deadline:
            with lock:
                store.tick()
                if stop_requested.is_set() or Path(os.environ['CAP_STOP_FILE']).exists():
                    store.fail('owned_supervisor_stop')
                summary = store.summary()
            save(args.evidence / 'latest.json', {'at': dt.datetime.now(dt.timezone.utc).isoformat(), 'summary': summary}, replace=True)
            if summary.get('stop') or summary.get('complete'):
                stop_sent, report = poll_termination(client, cfg['sceneId'], report_id, stop_sent)
                if report.get('terminal') is True and summary.get('childrenConverged') is True:
                    terminal['remoteConverged'] = True
                    break
            time.sleep(1)
        if not terminal['remoteConverged']:
            raise ValueError('remote PTS termination unproved')
        with lock:
            summary = store.summary()
        save(args.evidence / 'collector-summary.json', summary)
        terminal['nativeEvidenceComplete'] = summary.get('complete') is True and summary.get('collectionValid') is True
        if not terminal['nativeEvidenceComplete']:
            raise ValueError('full native JMeter evidence unavailable')
        save(args.summary, normalized(summary, seconds))
        return 0 if not summary.get('stop') else 1
    except (Exception, KeyboardInterrupt) as error:
        with lock:
            store.fail('phase_exception')
        terminal['failed'] = type(error).__name__
        if report_id is not None and not stop_sent:
            try:
                acknowledgement = client.stop(cfg['sceneId'], report_id)
                stop_sent = any(acknowledgement.get(key) is True
                                for key in ('requested', 'terminalVerified', 'alreadyTerminal'))
            except Exception:
                terminal['stopUnconfirmed'] = True
        return 1
    finally:
        if report_id is not None and not terminal['remoteConverged']:
            try:
                client.wait_terminal(cfg['sceneId'], report_id, timeout=60, interval=2)
                with lock:
                    terminal['remoteConverged'] = store.summary().get('childrenConverged') is True
            except Exception:
                terminal['remoteConverged'] = False
        with lock:
            save(args.evidence / 'collector-terminal.json', store.summary())
        server.shutdown(); server.server_close(); serving.join(timeout=3)
        store.close()
        terminal['finishedAt'] = dt.datetime.now(dt.timezone.utc).isoformat()
        save(args.evidence / 'terminal.json', terminal)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--input', type=Path, required=True)
    parser.add_argument('--summary', type=Path, required=True)
    parser.add_argument('--evidence', type=Path, required=True)
    args = parser.parse_args()
    return run(args)


if __name__ == '__main__':
    raise SystemExit(main())
