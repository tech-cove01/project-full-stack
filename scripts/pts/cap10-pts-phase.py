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
import re
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


cohort = module('cap10_phase_cohort', REPO / 'scripts/cap10-cohort.py')
IDLE = module('cap10_phase_idle', REPO / 'scripts/cap10-idle-contract.py')


def phase_preflight(reservation, configuration, root):
    selected = IDLE.RESOURCE.metadata_path(configuration, root)
    if IDLE.RESOURCE.application_metadata_path(reservation, root) != selected:
        raise ValueError('phase resource environment differs from its original reservation')
    profile = IDLE.RESOURCE.bound_profile(configuration, read(selected))
    if IDLE.RESOURCE.profile_name(reservation.get('resourceProfile', 'baseline')) != profile:
        raise ValueError('phase resource profile differs from its original reservation')
    return IDLE.validate_preflight(reservation, root)


def phase_cohort(configuration, source, manifest, reservation, admission):
    """Bind current hydration and cloud admission to their own original pool."""
    if clock_protocol(configuration) != clock_protocol(admission):
        raise ValueError('phase clock protocol differs from admission')
    dimensions = cohort.dimensions(configuration, manifest)
    count = dimensions['poolSize']
    for value in (source, reservation, admission):
        if cohort.command_pool_size(value, manifest) != count:
            raise ValueError('phase pool differs from its original fixture')
    for value in (reservation, admission):
        if cohort.dimensions(value, manifest) != dimensions:
            raise ValueError('phase engine dimensions differ from admission or reservation')
    expected = {name: hashlib.sha256((REPO / name).read_bytes()).hexdigest()
                for name in ('scripts/cap10-cohort.py', 'scripts/load/cap10-cohort-contract.mjs')}
    if any(value.get('cohortSourceSHA256') != expected for value in (configuration, admission)):
        raise ValueError('phase cohort helper sources are not frozen by admission')
    if (source.get('fixture') != manifest.get('fixture') or source.get('multiNoteIds') != manifest.get('multiNoteIds')
            or not isinstance(source.get('users'), list) or len(source['users']) != count
            or any(not isinstance(user, dict) or set(user) != {'userId', 'token'} for user in source['users'])
            or not isinstance(source.get('initial'), list) or len(source['initial']) != count
            or [user.get('userId') for user in source['users']] != manifest.get('userIds')):
        raise ValueError('hydrated exclusive identities differ from their bound fixture')
    for index, fact in enumerate(source['initial']):
        if (not isinstance(fact, dict) or fact.get('userId') != manifest['userIds'][index]
                or fact.get('noteId') != manifest['multiNoteIds'][index]):
            raise ValueError('hydrated fact order differs from the exclusive fixture')
    return dimensions


def clock_protocol(value):
    protocol = value.get('clockProtocol', 'embedded-v1')
    if type(protocol) is not str or protocol not in ('embedded-v1', 'dedicated-v1'):
        raise ValueError('phase clock protocol invalid')
    return protocol


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
    preparation = {}
    if summary.get('preheatProtocol', 'none') != 'none':
        value = summary.get('preparation')
        count = summary.get('poolSize')
        if (summary.get('preheatProtocol') != 'relation-noop-v1' or type(count) is not int
                or not isinstance(value, dict) or value.get('protocol') != 'relation-noop-v1'
                or value.get('rate') != 500 or value.get('passed') is not True or value.get('coverageComplete') is not True
                or not isinstance(value.get('counts'), dict)
                or any(type(value['counts'].get(k)) is not int or value['counts'][k] != count
                       for k in ('planned', 'attempted', 'completed', 'noop'))
                or any(type(value['counts'].get(k)) is not int or value['counts'][k] != 0
                       for k in ('mutation', 'uncertain', 'rejected', 'invalid', 'dropped'))):
            raise ValueError('complete unchanged preparation required before native summary')
        preparation = {'preparation': value, 'preheatProtocol': 'relation-noop-v1'}
    dimensions = cohort.dimensions(summary, {'poolSize': summary.get('poolSize', 512)})
    counts = summary['counts']
    required = ('planned', 'attempted', 'completed', 'mutation', 'noop', 'uncertain', 'rejected', 'invalid', 'dropped')
    if not all(type(counts.get(key)) is int and counts[key] >= 0 for key in required):
        raise ValueError('integer complete JMeter counts required')
    if counts['attempted'] != counts['completed'] or summary['unknown'] != counts['uncertain']:
        raise ValueError('JMeter terminal accounting differs')
    launch_plan = {}
    if 'launchGuardMs' in summary or 'plannedTotal' in summary:
        rate, guard, planned = summary.get('rate'), summary.get('launchGuardMs'), summary.get('plannedTotal')
        if (type(rate) is not int or not 1 <= rate <= 5000 or type(guard) is not int
                or guard != (0 if seconds == 10 else 100) or type(planned) is not int
                or planned != rate * (seconds * 1000 - guard) // 1000 or counts['planned'] != planned):
            raise ValueError('JMeter guarded launch plan differs from the complete native window')
        launch_plan = {'launchGuardMs': guard, 'plannedTotal': planned,
                       'targetRate': rate, 'plannedOfferedRatio': planned / (rate * seconds)}
    start, end = summary['startUnixMs'], summary['endUnixMs']
    if type(start) is not int or end != start + seconds * 1000:
        raise ValueError('JMeter native window differs')
    mapping = {'cap_like_attempted': counts['attempted'], 'cap_like_completed': counts['completed'],
        'cap_like_success': counts['mutation'] + counts['noop'], 'cap_like_mutation': counts['mutation'],
        'cap_like_noop': counts['noop'], 'cap_like_effect_unknown': counts['uncertain'],
        'cap_like_rejected': counts['rejected'], 'cap_like_content_errors': counts['invalid'],
        'cap_like_system_errors': summary.get('systemErrors'), 'cap_like_http_errors': summary.get('httpErrors'),
        'cap_like_recovery_reads': 0, 'cap_like_recovery_failed': 0,
        'cap_like_generator_timing_violations': summary.get('generatorTimingViolations'),
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
    return {**dimensions, **preparation, 'sourceEngine': 'JMeter 5.6.3 on Alibaba PTS', 'source': 'complete authenticated collector records; not sampled PTS logs or k6 output',
            'quantileMethod': summary.get('quantileMethod'), 'metrics': metrics, 'launchPlan': launch_plan,
            'nativeScenario': {'startedAt': start, 'scheduledEndedAt': end, 'durationMs': seconds * 1000,
                'complete': not summary.get('stop'), 'startMetric': {'min': start, 'max': start},
                'generatorTimingValid': summary['generatorTimingViolations'] == 0,
                'durationMetric': {'min': seconds * 1000, 'max': seconds * 1000},
                'scope': 'All fixture-bound registered slots, complete ECS-clock offer window, full JMeter terminal records; guarded launches do not shorten the QPS denominator. Platform report time is separate.'}}


class CollectorHTTPServer(ThreadingHTTPServer):
    # Nine engine connections plus control/progress must not overflow the
    # stdlib backlog of five and incur SYN/accept delays before HTTP handling.
    request_queue_size = 64


def handler_type(store, token, lock):
    class Handler(BaseHTTPRequestHandler):
        protocol_version = 'HTTP/1.1'

        def log_message(self, *args):
            pass

        def handle_operation(self, method):
            if not secrets.compare_digest(self.headers.get('Authorization', ''), 'Bearer ' + token):
                self.reply(403, {'error': 'unauthorized'})
                return
            # Clock reads must not queue behind validation, journal fsync or the
            # store's record lock. Invalid clock requests also leave it intact.
            if self.path == '/clock':
                try:
                    if method != 'GET':
                        raise ValueError('wrong clock method')
                    value = store.clock(self.headers.get('X-CAP10-Run-ID'))
                except (ValueError, TypeError):
                    self.reply(409, {'error': 'collector clock rejected'})
                    return
                self.reply(200, value)
                return
            try:
                if self.path not in ('/control', '/register', '/batch', '/preheat', '/complete'):
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


def scene_admission_path(root, reservation):
    root = Path(root)
    authority = root.parent / 'execution-authority-20261102.json'
    authority_hash = reservation.get('reservationCheck', {}).get('executionAuthoritySHA256')
    if (isinstance(reservation.get('tag'), str)
            and re.fullmatch(r'[SH][1-9][0-9]*', reservation['tag'])
            and isinstance(authority_hash, str) and len(authority_hash) == 64
            and authority.is_file() and not authority.is_symlink()
            and hashlib.sha256(authority.read_bytes()).hexdigest() == authority_hash):
        active_hash = reservation.get('reservationCheck', {}).get('activeApplicationSHA256')
        if active_hash is not None:
            if not isinstance(active_hash, str) or not re.fullmatch('[a-f0-9]{64}', active_hash):
                raise ValueError('exact reserved application snapshot required')
            snapshot = root / 'active-applications' / (active_hash + '.json')
            if (snapshot.is_symlink() or not snapshot.is_file()
                    or hashlib.sha256(snapshot.read_bytes()).hexdigest() != active_hash):
                raise ValueError('reserved application snapshot bytes differ')
            active = read(snapshot)
            relative = active.get('ptsAdmissionPath')
            if not isinstance(relative, str):
                raise ValueError('reserved scene admission path required')
            admission = root / relative
            expected_hash = reservation.get('ptsAdmissionSHA256')
            if (not admission.resolve().is_relative_to(root.resolve()) or admission.is_symlink()
                    or not admission.is_file() or active.get('ptsAdmissionSHA256') != expected_hash
                    or hashlib.sha256(admission.read_bytes()).hexdigest() != expected_hash):
                raise ValueError('reserved scene admission bytes differ')
            return admission
        return root / 'pts-scene-admission-repair1.json'
    transition = root / 'tool-repair-transition.json'
    repair_hash = reservation.get('reservationCheck', {}).get('toolRepairTransitionSHA256')
    if (reservation.get('tag') in {'S5', 'S6', 'S7', 'S8', 'H1', 'H2'}
            and isinstance(repair_hash, str) and len(repair_hash) == 64
            and transition.is_file() and not transition.is_symlink()
            and hashlib.sha256(transition.read_bytes()).hexdigest() == repair_hash):
        return root / 'pts-scene-admission-repair1.json'
    return root / 'pts-scene-admission.json'


def phase_exit_code(summary):
    return 0 if (summary.get('stop') is False and summary.get('generatorTimingValid') is True
                 and summary.get('generatorTimingViolations') == 0) else 1


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
    if private_url(cfg['apiUrl'], 8281) != host or len(cfg['controlToken']) < 64:
        raise ValueError('one fixed ECS with packet-bound PTS engines required')
    if args.evidence.resolve().parent != Path(os.environ['CAP_LIKE_EVIDENCE']).resolve():
        raise ValueError('fixed per-phase evidence directory required')
    args.evidence.mkdir()
    expected_root = owned.ROOT
    if not args.evidence.resolve().is_relative_to(expected_root):
        raise ValueError('same CAP10 Linux evidence root required')
    owned.transition()
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
    phase_preflight(reservation, dict(os.environ), expected_root)
    admission_path = scene_admission_path(expected_root, reservation)
    admission = read(admission_path)
    if (hashlib.sha256(admission_path.read_bytes()).hexdigest() != reservation['ptsAdmissionSHA256']
            or admission.get('passed') is not True):
        raise ValueError('reserved PTS scene admission differs')
    import psutil
    owner = read(runtime / 'cap10-active.private.json')
    parent = psutil.Process(owner['pid'])
    if (owner['tag'] != tag or parent.create_time() != owner['created']
            or parent.pid not in {p.pid for p in psutil.Process().parents()}):
        raise ValueError('PTS phase is not descended from the exact active reservation owner')
    fixture_bytes = Path(os.environ['CAP_LIKE_FIXTURE']).read_bytes()
    if hashlib.sha256(fixture_bytes).hexdigest() != reservation['fixtureSHA256']:
        raise ValueError('phase fixture bytes differ from reservation')
    dimensions = phase_cohort(cfg, source, json.loads(fixture_bytes), reservation, admission)
    relations = [{**source['initial'][slot], 'userId': user['userId'], 'token': user['token'], 'slot': slot}
                 for slot, user in enumerate(source['users'])]
    run_config = {**cfg, **dimensions, 'runId': secrets.token_hex(16), 'phase': phase, 'rate': rate, 'seconds': seconds,
        'preheatProtocol': 'relation-noop-v1',
        'fixtureSHA256': hashlib.sha256(fixture_bytes).hexdigest(),
        'relations': relations, 'apiUrl': cfg['apiUrl']}
    collector = module('cap10_pts_collector', Path(__file__).with_name('cap10-pts-collector.py'))
    api = module('cap10_pts_api', Path(__file__).with_name('cap10-pts-api.py'))
    client = api.Client(cfg['regionId'], args.evidence / 'api', credential_file=cfg.get('credentialsFile'),
                        allow_live=True, exclusive_scene=True, scene_id=cfg['sceneId'])
    store = collector.EngineStore(run_config, args.evidence / 'records.ndjson')
    lock = threading.RLock()
    server = CollectorHTTPServer((host, 8481), handler_type(store, cfg['controlToken'], lock))
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
        save(args.evidence / 'latest.json', {'at': dt.datetime.now(dt.timezone.utc).isoformat(), 'summary': store.progress()}, replace=True)
        scene = client.scene(cfg['sceneId'])['Scene']
        actual_files = {item['FileName']: item['Md5'].lower() for item in scene['FileList']}
        if actual_files != cfg['sceneFiles']:
            raise ValueError('uploaded PTS scene artifact bytes differ from frozen packet')
        if any(type(scene.get(key)) is not int or scene[key] != expected for key, expected in
               (('Duration', 600), ('Concurrency', dimensions['ptsConcurrency']), ('AgentCount', dimensions['expectedEngines']))):
            raise ValueError('fixed PTS lifecycle/concurrency configuration differs')
        response = client.start(cfg['sceneId'], runtime / ('pts-' + run_config['runId'] + '-start.private.json'),
            should_stop=lambda: stop_requested.is_set() or Path(os.environ['CAP_STOP_FILE']).exists() or store.control()['stop'])
        report_id = response['reportId']; terminal['reportId'] = report_id
        # Includes the 300s engine readiness barrier and bounded platform
        # startup/clock negotiation. Native offer duration remains unchanged.
        deadline = started + seconds + 390
        while time.monotonic() < deadline:
            with lock:
                store.tick()
                if stop_requested.is_set() or Path(os.environ['CAP_STOP_FILE']).exists():
                    store.fail('owned_supervisor_stop')
                summary = store.progress()
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
        return phase_exit_code(summary)
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
