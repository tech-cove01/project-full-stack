"""Run one predeclared CAP08 stage; failures retain their immutable slot.

Uses the existing read classifier and five-second safety supervisor. Diagnostic
sampling is explicit and separate from the formal group, never a formal score.
"""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import subprocess
import sys


REPO = Path(__file__).resolve().parent.parent


def module(name, filename):
    spec = importlib.util.spec_from_file_location(name, REPO / 'scripts' / filename)
    result = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(result)
    return result


policy = module('cap08_policy', 'cap08-policy.py')
prior = module('cap08_common_stage_guards', 'cap07-stage.py')
adapter = module('cap08_lifecycle', 'cap08-env.py')
observations = module('cap08_bounded_http_observations', 'cap08-collector.py')
ROOT, owned = policy.ROOT, adapter.owned


def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def child_identity(child):
    try:
        return prior.process_identity(child.pid)
    except prior.psutil.NoSuchProcess:
        assert child.poll() is not None
        return {'pid': child.pid, 'alreadyExitedBeforeIdentityCapture': True}


def update_lock(lock, ownership):
    pending = lock.with_suffix('.pending')
    policy.save(pending, ownership)
    pending.replace(lock)


class SnapshotUnavailable(RuntimeError):
    def __init__(self, fetched):
        super().__init__('bounded capacity snapshot unavailable')
        self.fetch = fetched


def capacity_snapshot(api_pid, diagnostic, slow_path):
    opener = observations.urllib.request.build_opener(observations.urllib.request.ProxyHandler({}), observations.NoRedirect())
    raw, fetched = observations.fetch(opener, 'http://127.0.0.1:8351/debug/capacity', 2 << 20, 3)
    if not fetched['available'] or fetched['truncated']:
        raise SnapshotUnavailable(fetched)
    try:
        body = json.loads(raw)
        assert body['pid'] == api_pid
    except (ValueError, TypeError, KeyError, AssertionError) as error:
        raise SnapshotUnavailable({**fetched, 'validationError': type(error).__name__}) from None
    if diagnostic:
        # Never write another set of up to 32 slow payloads outside the single
        # collector archive. These are references; absent payloads stay missing.
        body = observations.SlowRecords().compact(body)
    return {'at': policy.now(), 'scope': prior.SCOPE, 'capacity': body, 'fetch': fetched,
            'archiveScope': 'parsed JSON subset with slow payloads omitted' if diagnostic else 'parsed JSON object with tail observation disabled',
            'rawResponseArchived': False,
            'rawResponseHashScope': 'fetch SHA256 hashes actual HTTP bytes, not reserialized JSON; full raw response not retained',
            'slowRecordReference': str(slow_path) if diagnostic else None,
            'missingSlowRecordScope': 'visible sequence absent from collector slow-records.json has no retained payload; endpoint references do not create extra slow archives'}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--environment', type=Path, required=True)
    parser.add_argument('--api-metadata', type=Path, required=True)
    parser.add_argument('--source-sha', required=True)
    parser.add_argument('--tag', required=True)
    parser.add_argument('--kind', choices=('diagnostic', 'formal', 'stair', 'neighbor', 'hold'), required=True)
    parser.add_argument('--mode', choices=('multi', 'hot'), default='multi')
    parser.add_argument('--rate', type=int, default=100)
    parser.add_argument('--seconds', type=int, default=180)
    parser.add_argument('--prediction', type=Path)
    parser.add_argument('--gate', type=Path)
    args = parser.parse_args()
    assert Path.cwd().resolve() == REPO and ROOT.is_dir()
    assert re.fullmatch(r'[a-z0-9][a-z0-9-]{1,60}', args.tag)
    assert re.fullmatch(r'[a-f0-9]{40}', args.source_sha)
    assert args.api_metadata.resolve().parent == ROOT and re.fullmatch(r'api-[a-z0-9-]+\.json', args.api_metadata.name)
    assert 1 <= args.rate <= 400 and 1 <= args.seconds <= 180
    commands = policy.history(ROOT)
    slot = policy.stage_slot(args, commands, ROOT)
    stem = ROOT / args.tag
    load, control, diagnostic = (Path(str(stem) + ending) for ending in ('-load', '-control', '-diagnostic'))
    command_file, stage_exit = Path(str(stem) + '-command.json'), Path(str(stem) + '-stage-exit.json')
    assert not any(path.exists() for path in (load, control, diagnostic, command_file, stage_exit))
    # Reserve before API/dependency/network validation; genuine failed starts
    # occupy a slot too. A missing command after a crash leaves the slot intact.
    policy.save(ROOT / ('slot-' + slot + '.json'), {'task': 'CAP-08', 'stage': args.tag,
                'slot': slot, 'at': policy.now(), 'failedStartsConsumeSlot': True})
    try:
        metadata = policy.read(args.api_metadata) if args.api_metadata.is_file() else {}
        assert isinstance(metadata, dict)
    except (OSError, ValueError, AssertionError):
        metadata = {}
    policy.save(command_file, {'task': 'CAP-08', 'kind': args.kind, 'tag': args.tag, 'slot': slot,
         'apiSourceSha': args.source_sha, 'apiPid': metadata.get('pid'),
         'apiMetadataPath': str(args.api_metadata.resolve()),
         'apiMetadataSHA256': sha(args.api_metadata) if args.api_metadata.is_file() else None,
         'mode': args.mode, 'rate': args.rate, 'seconds': args.seconds,
         'warmSeconds': 10, 'warmRate': 25, 'preVUs': 256, 'maxVUs': 512,
         'at': policy.now(), 'budget': policy.budget(ROOT), 'environmentValuesExported': False,
         'toolSourceSha': subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=REPO, text=True).strip(),
         'predictionPath': str(args.prediction.resolve()) if args.prediction else None,
         'predictionSHA256': sha(args.prediction) if args.prediction else None,
         'gatePath': str(args.gate.resolve()) if args.gate else None,
         'gateSHA256': sha(args.gate) if args.gate else None,
         'scriptHashes': {name: sha(REPO / 'scripts' / name) for name in (
             'cap08-stage.py', 'cap08-policy.py', 'cap08-collector.py', 'cap08-env.py', 'cap06-env.py',
             'cap07-stage.py', 'capacity-stage.py', 'capacity-resources.py',
             'go073-cap-read.mjs', 'load/go073-cap-read.js')}})
    wrapper = watcher = None
    lock = owner = None
    before = None
    stage_record = {'task': 'CAP-08', 'kind': args.kind, 'tag': args.tag, 'slot': slot,
                    'startedAt': policy.now(), 'cardsEvidenceValid': False,
                    'wrapperPid': None, 'wrapperExited': False,
                    'capacityWrapperExit': None, 'extraDiagnosticCollectorExit': None,
                    'toolBytesStable': False,
                    'failedStartOrException': None,
                    'limits': 'Wrapper/collector exits and formal budget classifications are separate; diagnostic is not a formal score.'}
    try:
        settings, runtime = owned.load_settings(args.environment)
        api = owned.read_api_metadata(args.api_metadata)
        phase = 'diagnostic' if args.kind == 'diagnostic' else 'formal'
        assert api['sourceSha'] == settings['CAP_SOURCE_SHA'] == args.source_sha
        assert api['expectedVersion'] == 'capacity-' + args.source_sha
        assert api['phase'] == settings['CAP08_PHASE'] == phase
        assert api['tailEnabledRequested'] is (phase == 'diagnostic')
        assert settings['GO_FEED_DIAGNOSTICS'] == ('true' if phase == 'diagnostic' else 'false')
        owned.api_identity(api, require_health=True)
        lock = runtime / 'cap08-stage-active.private.json'
        owner = prior.process_identity(os.getpid())
        ownership = {'owner': owner, 'stage': args.tag, 'at': policy.now(), 'wrapper': None, 'collector': None}
        policy.save(lock, ownership)
        before_snapshot = capacity_snapshot(api['pid'], phase == 'diagnostic', diagnostic / 'slow-records.json')
        before = before_snapshot['capacity']
        assert before['pid'] == api['pid'] and before['tail']['enabled'] is (phase == 'diagnostic')
        prior.cards_delta(before, before, 'B')
        policy.save(Path(str(stem) + '-capacity-before.json'), before_snapshot)
        env = owned.environment(settings)
        env.update(CAP_READ_MODE=args.mode, CAP_READ_RATE=str(args.rate), CAP_READ_SECONDS=str(args.seconds),
                   CAP_READ_WARM_SECONDS='10', CAP_READ_WARM_RATE='25', CAP_READ_PRE_VUS='256',
                   CAP_READ_MAX_VUS='512', CAP_READ_EVIDENCE=str(load), CAP_STOP_FILE=str(control / 'stop.json'))
        with Path(str(stem) + '-diagnostic-console.txt').open('xb') as collector_log:
            if phase == 'diagnostic':
                watcher = subprocess.Popen([sys.executable, '-B', '-X', 'utf8', str(REPO / 'scripts/cap08-collector.py'),
                    '--url', 'http://127.0.0.1:8351/debug/capacity', '--api-metadata', str(args.api_metadata.resolve()),
                    '--expected-pid', str(api['pid']), '--start-file', str(load / 'stable-process.json'),
                    '--done-file', str(control / 'done.json'), '--output', str(diagnostic),
                    '--max-seconds', '360', '--max-captures', '3'],
                    env=env, stdout=collector_log, stderr=subprocess.STDOUT, creationflags=owned.FLAGS)
                ownership['collector'] = child_identity(watcher)
                update_lock(lock, ownership)
            command = [sys.executable, '-B', '-X', 'utf8', str(REPO / 'scripts/capacity-stage.py'),
                       '--mode', 'read', '--control-dir', str(control), '--api-pid', str(api['pid']),
                       '--diagnostics', 'http://127.0.0.1:8351/debug/capacity', '--interval', '5',
                       '--max-seconds', '360', '--stop-grace-seconds', '90', '--',
                       'node', 'scripts/go073-cap-read.mjs', 'run']
            stage_record['loadWaitStartedAt'] = policy.now()
            wrapper = subprocess.Popen(command, env=env, creationflags=owned.FLAGS)
            ownership['wrapper'] = child_identity(wrapper)
            update_lock(lock, ownership)
            stage_record['wrapperPid'] = wrapper.pid
            stage_record['capacityWrapperExit'] = wrapper.wait()
            stage_record['wrapperExited'] = wrapper.poll() is not None
            if watcher is not None:
                try:
                    watcher.wait(timeout=15)
                except subprocess.TimeoutExpired:
                    # This is the exact child handle, not name-based process cleanup.
                    watcher.terminate()
                    watcher.wait(timeout=10)
                    stage_record['collectorForcedStop'] = True
                stage_record['extraDiagnosticCollectorExit'] = watcher.returncode
            stage_record['loadWaitFinishedAt'] = policy.now()
            try:
                owned.api_identity(api, require_health=True)
                after_snapshot = capacity_snapshot(api['pid'], phase == 'diagnostic', diagnostic / 'slow-records.json')
                after = after_snapshot['capacity']
                assert after['pid'] == api['pid']
                policy.save(Path(str(stem) + '-capacity-after.json'), after_snapshot)
                policy.save(Path(str(stem) + '-cards-delta.json'), prior.cards_delta(before, after, 'B'))
                stage_record['cardsEvidenceValid'] = True
            except Exception as error:
                policy.save(Path(str(stem) + '-cards-error.json'), {'at': policy.now(), 'class': type(error).__name__,
                            'fetch': getattr(error, 'fetch', None), 'detailsSuppressed': True})
        original_hashes = policy.read(command_file)['scriptHashes']
        stage_record['toolBytesStable'] = all(sha(REPO / 'scripts' / name) == digest for name, digest in original_hashes.items())
        stage_record['finishedAt'] = policy.now()
        policy.save(stage_exit, stage_record)
        return stage_record['capacityWrapperExit'] or (stage_record['extraDiagnosticCollectorExit'] or 0) or (0 if stage_record['cardsEvidenceValid'] and stage_record['toolBytesStable'] else 1)
    except BaseException as error:
        stage_record.update(finishedAt=policy.now(), failedStartOrException=type(error).__name__)
        if hasattr(error, 'fetch'):
            stage_record['failedCapacityFetch'] = error.fetch
        if not stage_exit.exists():
            policy.save(stage_exit, stage_record)
        raise
    finally:
        # Retain on interrupt, wrapper death without supervision terminal, or a
        # surviving collector. An operator must review identities to recover.
        if lock is not None and owner is not None and (watcher is None or watcher.poll() is not None):
            prior.release_completed_lock(lock, owner, wrapper, stage_exit, control / 'exit-status.json')


if __name__ == '__main__':
    try:
        raise SystemExit(main())
    except Exception as error:
        print('cap08-stage: ' + type(error).__name__ + '; preserve immutable slots, active ownership and all failure evidence')
        raise SystemExit(1)
