"""One owned CAP10 60-second idle observation. Never starts a workload."""
import argparse
from concurrent.futures import ThreadPoolExecutor
import datetime as dt
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import statistics
import subprocess
import time

import psutil


REPO = Path(__file__).resolve().parent.parent


def module(name, filename):
    spec = importlib.util.spec_from_file_location(name, REPO / 'scripts' / filename)
    value = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    return value


RESOURCE = module('cap10_idle_resource_contract', 'cap10-resource-contract.py')
IDLE = module('cap10_idle_contract', 'cap10-idle-contract.py')
OBSERVER = module('cap10_idle_observer_contract', 'cap10-observer-contract.py')
COHORT = module('cap10_idle_cohort_contract', 'cap10-cohort.py')


def prepare_vector_baseline(value, api_metadata, evidence_root, root, owned):
    """Run the existing complete read-only reconciliation before idle starts."""
    policy = OBSERVER.environment_policy(value)
    if policy['mode'] != OBSERVER.LIKE_VECTOR:
        return None
    fixture = Path(value['CAP_LIKE_FIXTURE'])
    assert (fixture.is_absolute() and fixture.resolve() == fixture and fixture.is_file()
            and not fixture.is_symlink() and fixture.is_relative_to(root))
    fixture_bytes = fixture.read_bytes()
    pool = COHORT.fixture_pool_size(json.loads(fixture_bytes))
    directory = evidence_root / 'idle-baseline'
    assert not directory.exists() and not directory.is_symlink()
    environment = owned.env({**value, 'CAP_API_PID': str(api_metadata['pid']), 'CAP_LIKE_MODE': 'multi',
                             'CAP_LIKE_FIXTURE': str(fixture), 'CAP_LIKE_EVIDENCE': str(directory)})
    with (evidence_root / 'idle-baseline.stdout.txt').open('xb') as output, \
            (evidence_root / 'idle-baseline.stderr.txt').open('xb') as error:
        completed = subprocess.run(['node', 'scripts/cap10-like.mjs', 'reconcile'], cwd=REPO,
                                   env=environment, stdout=output, stderr=error, timeout=180)
    assert completed.returncode == 0, 'complete idle baseline reconciliation failed; raw output retained'
    path = directory / 'reconcile.json'
    assert (path.is_file() and not path.is_symlink() and path.resolve() == path
            and path.is_relative_to(root) and path.stat().st_size <= 32 << 20)
    raw = path.read_bytes()
    audit = json.loads(raw)
    facts, derived, timeline = audit.get('facts', {}), audit.get('derived', {}), audit.get('timeline', [])
    # Same complete verdict as prepare_profile_authority.reconcile_checked;
    # Go independently validates the original audit and a fresh RR snapshot.
    assert (audit.get('valid') is True and audit.get('factsValid') is True
            and audit.get('downstreamConverged') is True and audit.get('errors') == []
            and derived.get('available') is True and derived.get('errors') == []
            and len(facts.get('facts', [])) == len(facts.get('notes', [])) == len(derived.get('notes', [])) == pool
            and type(facts.get('eventCount')) is int and facts['eventCount'] >= 0
            and facts.get('transitions') == facts['eventCount'] and timeline
            and timeline[-1].get('pending') == timeline[-1].get('parked') == 0
            and timeline[-1].get('receiptErrors') == []
            and facts.get('observerPolicy') == policy
            and facts.get('fixtureManifestSHA256') == hashlib.sha256(fixture_bytes).hexdigest()), \
        'complete scoped idle facts/derived/receipts reconciliation required'
    assert fixture.read_bytes() == fixture_bytes, 'idle fixture changed during reconciliation'
    return {'path': path.relative_to(root).as_posix(), 'sha256': hashlib.sha256(raw).hexdigest(),
            'eventCount': facts['eventCount']}


def idle_observer_command(observer, environment, value, evidence_root, root, baseline):
    command = [str(Path(observer).resolve()), '--environment', environment,
               '--output', str(evidence_root / 'idle-downstream'),
               '--done-file', str(evidence_root / 'idle-control/done.json'),
               '--like-fixture', value['CAP_LIKE_FIXTURE'], '--max-seconds', '150']
    if OBSERVER.environment_policy(value)['mode'] == OBSERVER.LIKE_VECTOR:
        assert baseline is not None, 'vector idle requires its original complete baseline'
        command += ['--baseline-audit', str(root / baseline['path']), '--baseline-audit-sha256', baseline['sha256']]
    else:
        assert baseline is None
    return command


def environment_identity(value, api_metadata, api_values, owned, root):
    metadata, path, profile = owned.environment(value)
    _, api_path, api_profile = owned.environment(api_values)
    assert (path, profile) == (api_path, api_profile), 'idle and API private resource environments differ'
    assert RESOURCE.application_metadata_path(api_metadata, root) == path, 'API environment metadata path differs'
    assert RESOURCE.profile_name(api_metadata.get('resourceProfile', RESOURCE.BASELINE_PROFILE)) == profile
    digest = hashlib.sha256(path.read_bytes()).hexdigest()
    if 'environmentSHA256' in api_metadata:
        assert api_metadata['environmentSHA256'] == digest, 'API environment metadata bytes differ'
    else:
        assert path == root / 'environment.json' and profile == RESOURCE.BASELINE_PROFILE, 'new API environment requires its original metadata SHA'
    return metadata, path, profile, digest


def container_identities(containers, resource_profile):
    limits = RESOURCE.container_limits(resource_profile)
    identities = []
    for service, container in containers.items():
        cpu, memory = limits[service]
        assert (container['cpuNano'], container['memoryBytes']) == (cpu, memory), 'idle container limits differ from selected profile'
        identities.append({'id': container['id'], 'service': service, 'resourceProfile': resource_profile,
                           'cpuNano': cpu, 'memoryBytes': memory})
    return identities


def claim_versioned_idle(root, runtime, environment_path, resource_profile, owned, retry_arguments):
    assert not any(retry_arguments), 'versioned idle is one observation; legacy retry records remain separate'
    directory = IDLE.idle_directory(environment_path, root, resource_profile)
    assert directory != root and not directory.exists(), 'versioned idle namespace is already consumed'
    histories = (root, root.parent) if root.name == 'linux' else (root, root / 'linux')
    for history_root in histories:
        for path in (*history_root.glob('slot-[SH]*.json'), *history_root.glob('[SH]*-command.json')):
            if not re.fullmatch(r'(?:slot-[SH][1-9][0-9]*|[SH][1-9][0-9]*-command)\.json', path.name):
                continue
            assert path.is_file() and not path.is_symlink() and path.resolve() == path, 'original slot history must remain ordinary files'
            assert RESOURCE.application_metadata_path(owned.read(path), history_root) != environment_path, 'idle must precede every reservation for this environment'
    lock = runtime / 'cap10-active.private.json'
    owner = {'kind': 'idle', 'pid': os.getpid(), 'created': psutil.Process().create_time(),
             'environmentPath': environment_path.name, 'environmentSHA256': hashlib.sha256(environment_path.read_bytes()).hexdigest(),
             'resourceProfile': resource_profile}
    owned.save(lock, owner)
    try:
        directory.mkdir()
    except BaseException:
        if lock.is_file() and not lock.is_symlink() and owned.read(lock) == owner:
            lock.unlink()
        raise
    return directory, owner


def release_idle_owner(runtime, owner, observer, owned):
    if (owner is None or owner.get('pid') != os.getpid()
            or owner.get('created') != psutil.Process().create_time()
            or observer is not None and observer.poll() is None):
        return False
    lock = runtime / 'cap10-active.private.json'
    if lock.is_file() and not lock.is_symlink() and owned.read(lock) == owner:
        lock.unlink()
        return True
    return False


def idle_attempt(root, authorization, transition_sha, additional_authorization=None,
                 interruption_authorization=None, current_boot_id=None):
    """Authorize explicit retries of the retained zero-sample failures."""
    root = Path(root).resolve()
    archive = root / 'idle-attempt-1'
    if (root / 'idle-reservation.json').exists() or (root / 'idle-attempt-4').exists():
        raise ValueError('current idle reservation already consumed; preserve it')
    if authorization is None:
        if archive.exists() or additional_authorization is not None or interruption_authorization is not None:
            raise ValueError('archived idle failure requires explicit retry authorization')
        return {'attempt': 1}
    expected = root.parent / 'continuation-authorization.json'
    path = Path(authorization)
    if root.name != 'linux' or path.is_symlink() or path.resolve() != expected or not path.is_file():
        raise ValueError('fixed batch continuation authorization required')
    raw = path.read_bytes()
    auth = json.loads(raw)
    if (not isinstance(auth, dict) or type(auth.get('version')) is not int or auth['version'] != 1
            or auth.get('batch') != 'CAP-10' or auth.get('authorized') is not True
            or type(auth.get('maxIdleRetries')) is not int or auth['maxIdleRetries'] != 1
            or not isinstance(auth.get('userAuthorizationReference'), str)
            or not auth['userAuthorizationReference'].strip()
            or auth.get('failedIdleResultPath') != 'linux/idle-attempt-1/idle-result.json'
            or not isinstance(transition_sha, str) or len(transition_sha) != 64
            or auth.get('transitionSHA256') != transition_sha):
        raise ValueError('one explicit same-transition idle retry authorization required')
    for directory in (root.parent, root):
        if list(directory.glob('slot-*.json')) or list(directory.glob('[SH]*-command.json')):
            raise ValueError('idle retry must precede every native reservation')
    failed_path, reservation_path = archive / 'idle-result.json', archive / 'idle-reservation.json'
    for original in (failed_path, reservation_path):
        if original.is_symlink() or original.resolve() != original or not original.is_file():
            raise ValueError('original regular failed idle artifacts required')
    failed_raw = failed_path.read_bytes()
    failure_sha = hashlib.sha256(failed_raw).hexdigest()
    failed, reservation = json.loads(failed_raw), json.loads(reservation_path.read_bytes())
    if (auth.get('failedIdleResultSHA256') != failure_sha
            or not isinstance(failed, dict) or failed.get('passed') is not False
            or failed.get('samples') != [] or failed.get('noWorkloadStarted') is not True
            or failed.get('seconds') != 60 or failed.get('executionPlatform') != 'linux'
            or failed.get('transitionSHA256') != transition_sha
            or not isinstance(reservation, dict) or type(reservation.get('attempt')) is not int
            or reservation['attempt'] != 1 or reservation.get('seconds') != 60
            or reservation.get('failedAttemptConsumesOnlyIdleObservation') is not True
            or reservation.get('executionPlatform') != 'linux'
            or reservation.get('transitionSHA256') != transition_sha):
        raise ValueError('retained zero-sample first idle failure differs')
    base = {'attempt': 2, 'retryAuthorizationSHA256': hashlib.sha256(raw).hexdigest(),
            'failedIdleResultSHA256': failure_sha}
    if additional_authorization is None:
        if (root / 'idle-attempt-2').exists() or interruption_authorization is not None:
            raise ValueError('archived second failure requires a new explicit authorization')
        return base
    path = Path(additional_authorization)
    expected = root.parent / 'idle-retry-3-authorization.json'
    if path.is_symlink() or path.resolve() != expected or not path.is_file():
        raise ValueError('fixed additional idle authorization required')
    extra_raw = path.read_bytes()
    extra = json.loads(extra_raw)
    if (not isinstance(extra, dict) or type(extra.get('version')) is not int or extra['version'] != 1
            or extra.get('batch') != 'CAP-10' or extra.get('authorized') is not True
            or type(extra.get('attempt')) is not int or extra['attempt'] != 3
            or type(extra.get('maxAdditionalIdleRetries')) is not int or extra['maxAdditionalIdleRetries'] != 1
            or not isinstance(extra.get('userAuthorizationReference'), str) or not extra['userAuthorizationReference'].strip()
            or extra.get('continuationAuthorizationSHA256') != base['retryAuthorizationSHA256']
            or extra.get('transitionSHA256') != transition_sha
            or extra.get('failedIdleResultPath') != 'linux/idle-attempt-2/idle-result.json'
            or not isinstance(extra.get('recordedAt'), str)):
        raise ValueError('one explicit third-attempt authorization required')
    registered = dt.datetime.fromisoformat(extra['recordedAt'].replace('Z', '+00:00'))
    if registered.tzinfo is None or registered > dt.datetime.now(dt.timezone.utc):
        raise ValueError('additional authorization registration timestamp invalid')
    archive = root / 'idle-attempt-2'
    failed_path, reservation_path = archive / 'idle-result.json', archive / 'idle-reservation.json'
    for original in (failed_path, reservation_path):
        if original.is_symlink() or original.resolve() != original or not original.is_file():
            raise ValueError('original regular second idle artifacts required')
    failed_raw = failed_path.read_bytes()
    failure_sha = hashlib.sha256(failed_raw).hexdigest()
    failed, reservation = json.loads(failed_raw), json.loads(reservation_path.read_bytes())
    if (extra.get('failedIdleResultSHA256') != failure_sha
            or not isinstance(failed, dict) or failed.get('passed') is not False or failed.get('samples') != []
            or failed.get('noWorkloadStarted') is not True or failed.get('seconds') != 60
            or type(failed.get('attempt')) is not int or failed['attempt'] != 2
            or failed.get('executionPlatform') != 'linux' or failed.get('transitionSHA256') != transition_sha
            or failed.get('retryAuthorizationSHA256') != base['retryAuthorizationSHA256']
            or failed.get('failedIdleResultSHA256') != base['failedIdleResultSHA256']
            or not isinstance(reservation, dict) or type(reservation.get('attempt')) is not int or reservation['attempt'] != 2
            or reservation.get('seconds') != 60 or reservation.get('failedAttemptConsumesOnlyIdleObservation') is not True
            or reservation.get('executionPlatform') != 'linux' or reservation.get('transitionSHA256') != transition_sha
            or reservation.get('retryAuthorizationSHA256') != base['retryAuthorizationSHA256']
            or reservation.get('failedIdleResultSHA256') != base['failedIdleResultSHA256']):
        raise ValueError('retained zero-sample second idle failure differs')
    third = {'attempt': 3, 'retryAuthorizationSHA256': hashlib.sha256(extra_raw).hexdigest(),
            'continuationAuthorizationSHA256': base['retryAuthorizationSHA256'],
            'failedIdleResultSHA256': failure_sha, 'firstFailedIdleResultSHA256': base['failedIdleResultSHA256']}
    if interruption_authorization is None:
        if (root / 'idle-attempt-3').exists():
            raise ValueError('archived third interruption requires explicit recovery authorization')
        return third
    return interruption_attempt(root, interruption_authorization, transition_sha, third, current_boot_id)


def interruption_attempt(root, authorization, transition_sha, third, current_boot_id):
    path = Path(authorization)
    if path.is_symlink() or path.resolve() != root.parent / 'idle-retry-4-authorization.json' or not path.is_file():
        raise ValueError('fixed interruption recovery authorization required')
    raw = path.read_bytes()
    auth = json.loads(raw)
    if (not isinstance(auth, dict) or type(auth.get('version')) is not int or auth['version'] != 1
            or auth.get('batch') != 'CAP-10' or auth.get('authorized') is not True
            or type(auth.get('attempt')) is not int or auth['attempt'] != 4
            or type(auth.get('maxInterruptionRetries')) is not int or auth['maxInterruptionRetries'] != 1
            or not isinstance(auth.get('userAuthorizationReference'), str) or not auth['userAuthorizationReference'].strip()
            or auth.get('continuationAuthorizationSHA256') != third['continuationAuthorizationSHA256']
            or auth.get('additionalRetryAuthorizationSHA256') != third['retryAuthorizationSHA256']
            or auth.get('transitionSHA256') != transition_sha
            or auth.get('interruptionRecordPath') != 'linux/idle-attempt-3/interruption-record.json'
            or not isinstance(auth.get('recordedAt'), str)):
        raise ValueError('one explicit fourth-attempt interruption recovery required')
    registered = dt.datetime.fromisoformat(auth['recordedAt'].replace('Z', '+00:00'))
    if registered.tzinfo is None or registered > dt.datetime.now(dt.timezone.utc):
        raise ValueError('interruption recovery registration timestamp invalid')
    archive = root / 'idle-attempt-3'
    paths = [archive / name for name in ('interruption-record.json', 'idle-reservation.json', 'idle-samples.ndjson')]
    if (archive / 'idle-result.json').exists():
        raise ValueError('interruption cannot replace a completed idle result')
    for original in paths:
        if original.is_symlink() or original.resolve() != original or not original.is_file():
            raise ValueError('original regular interrupted idle artifacts required')
    record_raw, reservation_raw, samples_raw = [original.read_bytes() for original in paths]
    record, reservation = json.loads(record_raw), json.loads(reservation_raw)
    samples = [json.loads(line) for line in samples_raw.splitlines() if line.strip()]
    record_sha = hashlib.sha256(record_raw).hexdigest()
    samples_sha = hashlib.sha256(samples_raw).hexdigest()
    boot_pattern = r'[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}'
    if (auth.get('interruptionRecordSHA256') != record_sha or not isinstance(record, dict)
            or type(record.get('version')) is not int or record['version'] != 1
            or record.get('batch') != 'CAP-10' or type(record.get('attempt')) is not int or record['attempt'] != 3
            or record.get('executionPlatform') != 'linux' or record.get('interruptionKind') != 'ecs_billing_shutdown'
            or record.get('noWorkloadStarted') is not True or record.get('transitionSHA256') != transition_sha
            or type(record.get('partialSampleCount')) is not int or record['partialSampleCount'] != 3 or len(samples) != 3
            or record.get('reservationSHA256') != hashlib.sha256(reservation_raw).hexdigest()
            or record.get('partialSamplesSHA256') != samples_sha
            or not isinstance(current_boot_id, str) or not re.fullmatch(boot_pattern, current_boot_id)
            or record.get('currentBootId') != current_boot_id
            or not isinstance(record.get('previousBootId'), str) or not re.fullmatch(boot_pattern, record['previousBootId'])
            or record['previousBootId'] == current_boot_id
            or any(not isinstance(sample, dict) or sample.get('power', {}).get('clock', {}).get('bootId') != record['previousBootId'] for sample in samples)
            or not isinstance(reservation, dict) or type(reservation.get('attempt')) is not int or reservation['attempt'] != 3
            or reservation.get('seconds') != 60 or reservation.get('failedAttemptConsumesOnlyIdleObservation') is not True
            or reservation.get('executionPlatform') != 'linux' or reservation.get('transitionSHA256') != transition_sha
            or any(reservation.get(key) != value for key, value in third.items())):
        raise ValueError('retained third idle interruption or boot evidence differs')
    return {**third, 'attempt': 4, 'retryAuthorizationSHA256': hashlib.sha256(raw).hexdigest(),
            'additionalRetryAuthorizationSHA256': third['retryAuthorizationSHA256'],
            'interruptionRecordSHA256': record_sha, 'partialIdleSamplesSHA256': samples_sha}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--environment', required=True)
    parser.add_argument('--api-metadata', required=True)
    parser.add_argument('--observer', required=True)
    parser.add_argument('--retry-authorization')
    parser.add_argument('--additional-retry-authorization')
    parser.add_argument('--interruption-retry-authorization')
    args = parser.parse_args()
    owned = module('cap10_idle_owned', 'cap10-env.py')
    resources = module('cap10_idle_resources', 'cap10-resources.py')
    value, runtime = owned.settings(args.environment)
    root = owned.ROOT
    api_metadata = owned.read(args.api_metadata)
    api_values, api_runtime = owned.settings(api_metadata['privateEnvironmentFile'])
    environment_metadata, environment_path, resource_profile, environment_sha = environment_identity(value, api_metadata, api_values, owned, root)
    api = owned.api_identity(api_metadata, health=True)
    app_config = lambda settings: {k: v for k, v in settings.items() if not k.startswith('CAP')}
    assert api_runtime == runtime and app_config(api_values) == app_config(value)
    assert value['CAP_SOURCE_SHA'] == api_metadata['sourceSha']
    assert value['CAP_READ_EXPECTED_REVISION'] == api_metadata['expectedVersion']
    evidence_root = IDLE.idle_directory(environment_path, root, resource_profile)
    idle_owner = None
    if evidence_root == root:
        assert not list(root.glob('slot-*.json')), 'idle observation must precede S1'
        boot_id = owned.clock_sample().get('bootId') if args.interruption_retry_authorization else None
        attempt = idle_attempt(root, args.retry_authorization, owned.transition(), args.additional_retry_authorization,
                               args.interruption_retry_authorization, boot_id)
    else:
        evidence_root, idle_owner = claim_versioned_idle(root, runtime, environment_path, resource_profile, owned,
            (args.retry_authorization, args.additional_retry_authorization, args.interruption_retry_authorization))
        attempt = {'attempt': 1, 'idleSchemaVersion': 1, 'idleDirectory': evidence_root.name}
    reservation = evidence_root / 'idle-reservation.json'
    control = evidence_root / 'idle-control'
    done = control / 'done.json'
    downstream = evidence_root / 'idle-downstream'
    observer = None
    result = {'passed': False, 'seconds': 60, 'samples': [], 'errors': [],
              **attempt,
              'noWorkloadStarted': True, 'apiPid': api.pid, 'sourceSha': api_metadata['sourceSha'],
              'executionPlatform': owned.PLATFORM,
              'environmentPath': environment_path.name, 'environmentSHA256': environment_sha, 'resourceProfile': resource_profile}
    if idle_owner is not None:
        result.update(intervalSeconds=5, reservationPath=reservation.relative_to(root).as_posix())
    try:
        result.update(at=owned.now(), transitionSHA256=owned.transition(),
                      scriptSHA256=hashlib.sha256(Path(__file__).read_bytes()).hexdigest())
        owned.save(reservation, {'at': result['at'], 'seconds': 60, 'intervalSeconds': 5,
                                **attempt, 'failedAttemptConsumesOnlyIdleObservation': True,
                                'environmentPath': environment_path.name, 'environmentSHA256': environment_sha, 'resourceProfile': resource_profile,
                                'executionPlatform': owned.PLATFORM, 'transitionSHA256': result['transitionSHA256']})
        if idle_owner is not None:
            result['reservationSHA256'] = hashlib.sha256(reservation.read_bytes()).hexdigest()
        owned_containers = environment_metadata['containers']
        for service, container in owned_containers.items():
            checked = owned.container(service, container['id'], True, resource_profile=resource_profile)
            assert checked['volumes'] == container['volumes']
        identities = container_identities(owned_containers, resource_profile)
        baseline = prepare_vector_baseline(value, api_metadata, evidence_root, root, owned)
        if baseline is not None:
            result['baselineAudit'] = {key: baseline[key] for key in ('path', 'sha256')}
            result['baselineAuditEventCount'] = baseline['eventCount']
        control.mkdir()
        with (evidence_root / 'idle-observer.txt').open('xb') as output:
            observer = subprocess.Popen(idle_observer_command(args.observer, args.environment, value, evidence_root, root, baseline),
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
                with (evidence_root / 'idle-samples.ndjson').open('a', encoding='utf-8', newline='\n') as samples:
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
                checked = owned.container(service, container['id'], True, resource_profile=resource_profile)
                assert checked['volumes'] == container['volumes']
            result['powerEvents'] = owned.power_window_evidence(result['samples'][0]['power'],
                result['samples'][-1]['power'], result['at'])
            result['clockIntervals'] = [owned.power_window_evidence(left['power'], right['power'], result['at'], lightweight=True)
                                       for left, right in zip(result['samples'], result['samples'][1:])]
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
                result['powerEvents']['valid'] and all(c['valid'] for c in result['clockIntervals']) and
                all(owned.power_valid(r['power']) and
                    r['observationElapsedMs'] < 5000 and r['apiRSSBytes'] < 1610612736 and
                    all(float(c['MemPerc'].rstrip('%')) < 85 for c in r['containerResources'])
                    for r in result['samples']))
    except Exception as error:
        result['errors'].append({'class': type(error).__name__, 'detailsSuppressed': True})
    finally:
        try:
            if observer is not None and observer.poll() is None:
                if not done.exists():
                    owned.save(done, {'at': owned.now(), 'reason': 'idle observation failed; no load'})
                try:
                    observer.wait(timeout=15)
                except subprocess.TimeoutExpired:
                    observer.terminate()
                    observer.wait(timeout=5)
        except Exception as error:
            result['passed'] = False
            result['errors'].append({'class': type(error).__name__, 'detailsSuppressed': True})
        finally:
            try:
                result['finishedAt'] = owned.now()
                owned.save(evidence_root / 'idle-result.json', result)
            finally:
                release_idle_owner(runtime, idle_owner, observer, owned)
    print(json.dumps({'idlePassed': result['passed'], 'samples': len(result['samples']),
                      'hostMemoryMaxPercent': result.get('hostMemoryMaxPercent'),
                      'hostCPUMedian': result.get('hostCPUMedian'), 'errors': result['errors']}))
    raise SystemExit(0 if result['passed'] else 1)


if __name__ == '__main__':
    main()
