"""One bounded CAP07 experiment over the existing read harness and safety sampler.

No automatic ascent, retries, credential output or environment cleanup. Every
attempt reserves an immutable slot before loading; wrapper exit is not a score.
"""
import argparse
import datetime as dt
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import subprocess
import sys


REPO = Path(__file__).resolve().parent.parent
ROOT = REPO / 'docs/evidence/2026-10-30-cap07'
A_SOURCE_SHA = 'f3d99ee43dfb422a0cba525de93e9266f07c4ee5'
STAIRS = (150, 200, 300, 400)
COUNTERS = ('requests', 'l1_hits', 'redis_hits', 'miss_items', 'loads_started',
            'flight_started', 'flight_joins', 'content_sql_batches', 'content_sql_rows',
            'content_bytes', 'load_errors', 'oversize_uncached', 'redis_errors',
            'invalid_payloads', 'busy_rejected', 'expired', 'evictions')
GAUGES = ('inflight', 'active_loads', 'waiting_callers', 'l1_entries', 'l1_bytes')
SCOPE = ('before warmup through after wrapper reconciliation, including warmup, '
         'stable workload and background work; endpoint snapshots are sequential, '
         'not an atomic stable-only or per-request measurement')


def read(path):
    return json.loads(path.read_text(encoding='utf-8-sig'))


def save(path, value):
    with path.open('x', encoding='utf-8') as stream:
        json.dump(value, stream, ensure_ascii=False, indent=2)


def now():
    return dt.datetime.now(dt.timezone.utc).isoformat()


def records(root):
    values = [read(path) for path in root.glob('*-command.json')]
    return sorted((value for value in values if value.get('task') == 'CAP-07'), key=lambda value: value['at'])


def passed(root, record):
    result = root / (record['tag'] + '-load/result.json')
    exit_file = root / (record['tag'] + '-stage-exit.json')
    return (result.is_file() and exit_file.is_file() and
            read(result).get('meetsBudget') is True and
            read(exit_file).get('capacityWrapperExit') == 0 and
            read(exit_file).get('cardsEvidenceValid') is True)


def capacity_failure(root, record):
    paths = [root / (record['tag'] + suffix) for suffix in
             ('-load/result.json', '-stage-exit.json', '-control/exit-status.json')]
    if not all(path.is_file() for path in paths):
        return False
    result, stage, control = (read(path) for path in paths)
    return (result.get('meetsBudget') is False and isinstance(result.get('result'), dict) and
            not result.get('failure') and stage.get('cardsEvidenceValid') is True and
            control.get('sampler_exit') == 0 and not control.get('errors') and
            not control.get('stop_signal') and not control.get('forced'))


def protocol(args, history, root):
    """Pure, fail-closed reservation policy; failed starts consume their slot."""
    own = [row for row in history if row['kind'] == args.kind]
    if args.kind == 'probe':
        assert args.rate <= 25 and args.seconds <= 15
        assert not any(row['variant'] == args.variant for row in own), 'one short probe per variant'
        return 'probe-' + args.variant.lower()
    comparisons = [row for row in history if row['kind'] == 'comparison']
    if args.kind == 'comparison':
        assert (args.mode, args.rate, args.seconds) == ('multi', 100, 180)
        order = ('A', 'B', 'A', 'B')
        assert len(comparisons) < len(order) and args.variant == order[len(comparisons)], 'predeclared A1/B1/A2/B2 order'
        return 'comparison-' + str(len(comparisons) + 1)
    assert args.variant == 'B' and len(comparisons) == 4, 'complete the four comparison attempts first'
    baseline = [row for row in comparisons if row['variant'] == 'B']
    assert len(baseline) == 2 and all(passed(root, row) for row in baseline), 'both final-B multi100 rounds must pass before ascent'
    stairs = sorted((row for row in history if row['kind'] == 'stair'), key=lambda row: row['rate'])
    neighbor = [row for row in history if row['kind'] == 'neighbor']
    if args.kind == 'stair':
        assert args.mode == 'multi' and args.seconds == 45
        assert len(stairs) < 4 and args.rate == STAIRS[len(stairs)], 'only the four predeclared ascending stairs'
        assert not neighbor and all(passed(root, row) for row in stairs), 'first failed stair stops ascent'
        return 'stair-' + str(len(stairs) + 1)
    failed = [row for row in stairs if not passed(root, row)]
    successful_rates = [100] + [row['rate'] for row in stairs + neighbor if passed(root, row)]
    highest = max(successful_rates)
    if args.kind == 'neighbor':
        assert args.mode == 'multi' and args.seconds == 45 and len(failed) == 1 and not neighbor
        assert max([100] + [row['rate'] for row in stairs if passed(root, row)]) < args.rate < failed[0]['rate'], 'one interior step near the first limit'
        assert capacity_failure(root, failed[0]), 'only a measured SLO failure, not a failed start or resource/supervision stop, defines a capacity bracket'
        return 'neighbor-1'
    assert (failed or len(stairs) == 4), 'finish bounded exploration first'
    if args.kind == 'hold':
        assert args.mode == 'multi' and args.rate == highest and args.seconds == 180
        assert highest > 100, 'when highest is 100, reuse the two final-B comparison rounds'
        assert len(own) < 2, 'at most two highest-rate holds, retaining either failure'
        return 'hold-' + str(len(own) + 1)
    assert args.kind == 'hot' and (args.mode, args.rate, args.seconds) == ('hot', 200, 180)
    assert highest == 100 or len([row for row in history if row['kind'] == 'hold']) == 2, 'attempt the two highest-rate holds before the final hot hold'
    assert not own, 'one final hot200 hold'
    return 'hot-1'


def cards_delta(before, after, variant):
    left, right = before.get('cards'), after.get('cards')
    if variant == 'A':
        assert left is None and right is None, 'A must be the uncached-content implementation'
        return {'valid': True, 'variant': 'A', 'scope': SCOPE, 'contentLoaderCounters': None,
                'limits': 'Absent component counters do not mean A content SQL is zero.'}
    assert isinstance(left, dict) and isinstance(right, dict)
    assert left.get('coalescing') is True and right.get('coalescing') is True, 'final B requires shared-flight implementation'
    for value in (left, right):
        assert all(type(value.get(key)) is int and value[key] >= 0 for key in COUNTERS + GAUGES)
    delta = {key: right[key] - left[key] for key in COUNTERS}
    assert all(value >= 0 for value in delta.values()), 'content counter reset'
    return {'valid': True, 'variant': 'B', 'scope': SCOPE, 'counterDelta': delta,
            'beforeGauges': {key: left[key] for key in GAUGES},
            'afterGauges': {key: right[key] for key in GAUGES},
            'contentBytesDefinition': 'UTF8 bytes of DB title/content/raw images returned to the content loader; not wire/RSS',
            'limits': 'Content loader SQL counts are separate from instance SQL. Gauges are snapshots, not peaks; hot cache is not a zero-DB Feed.'}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--environment', type=Path, required=True)
    parser.add_argument('--api-metadata', type=Path, required=True)
    parser.add_argument('--tag', required=True)
    parser.add_argument('--variant', choices=('A', 'B'), required=True)
    parser.add_argument('--kind', choices=('probe', 'comparison', 'stair', 'neighbor', 'hold', 'hot'), required=True)
    parser.add_argument('--mode', choices=('multi', 'hot'), default='multi')
    parser.add_argument('--rate', type=int, required=True)
    parser.add_argument('--seconds', type=int, required=True)
    args = parser.parse_args()
    assert Path.cwd().resolve() == REPO and ROOT.is_dir()
    assert re.fullmatch(r'[a-z0-9][a-z0-9-]{1,60}', args.tag)
    assert 1 <= args.rate <= 400 and 1 <= args.seconds <= 180
    spec = importlib.util.spec_from_file_location('cap07_lifecycle', REPO / 'scripts/cap07-env.py')
    adapter = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(adapter)
    owned = adapter.owned
    settings, runtime = owned.load_settings(args.environment)
    api = read(args.api_metadata)
    assert args.api_metadata.resolve().parent == ROOT and re.fullmatch(r'api-[a-z0-9-]+\.json', args.api_metadata.name)
    assert api['sourceSha'] == settings['CAP_SOURCE_SHA'] and re.fullmatch(r'[a-f0-9]{40}', api['sourceSha'])
    assert args.variant != 'A' or api['sourceSha'] == A_SOURCE_SHA, 'A is the clean CAP06V commit before cards'
    assert api['expectedVersion'] == 'capacity-' + settings['CAP_SOURCE_SHA']
    assert api['variant'] == settings['CAP07_VARIANT'] == args.variant
    assert settings.get('GO_FEED_DIAGNOSTICS') == 'false' and api['tailEnabledRequested'] is False
    owned.api_identity(api, require_health=True)
    history = records(ROOT)
    assert all(row['apiSourceSha'] == api['sourceSha'] for row in history
               if row['variant'] == args.variant and row['kind'] != 'probe'), 'fixed source SHA per comparison variant'
    slot = protocol(args, history, ROOT)
    stem = ROOT / args.tag
    load, control = Path(str(stem) + '-load'), Path(str(stem) + '-control')
    command_file = Path(str(stem) + '-command.json')
    assert not any(path.exists() for path in (load, control, command_file)), 'never overwrite an attempted stage'
    warm_seconds = 1 if args.kind == 'probe' else 10
    env = owned.environment(settings)
    env.update(CAP_READ_MODE=args.mode, CAP_READ_RATE=str(args.rate), CAP_READ_SECONDS=str(args.seconds),
               CAP_READ_WARM_SECONDS=str(warm_seconds), CAP_READ_WARM_RATE=str(min(args.rate, 25)),
               CAP_READ_PRE_VUS='256', CAP_READ_MAX_VUS='512', CAP_READ_EVIDENCE=str(load),
               CAP_STOP_FILE=str(control / 'stop.json'))
    # A crashed owner deliberately leaves this private lock for identity-reviewed recovery.
    lock = runtime / 'cap07-stage-active.private.json'
    save(lock, {'pid': os.getpid(), 'stage': args.tag, 'at': now()})
    try:
        save(ROOT / ('slot-' + slot + '.json'), {'stage': args.tag, 'at': now(), 'failedStartsConsumeSlot': True})
        save(command_file, {'task': 'CAP-07', 'kind': args.kind, 'variant': args.variant, 'tag': args.tag,
             'slot': slot, 'apiSourceSha': api['sourceSha'], 'apiPid': api['pid'],
             'apiMetadataPath': str(args.api_metadata.resolve()),
             'apiMetadataSHA256': hashlib.sha256(args.api_metadata.read_bytes()).hexdigest(),
             'mode': args.mode, 'rate': args.rate, 'seconds': args.seconds,
             'warmSeconds': warm_seconds, 'warmRate': min(args.rate, 25), 'preVUs': 256, 'maxVUs': 512,
             'at': now(), 'environmentValuesExported': False,
             'scriptHashes': {name: hashlib.sha256((REPO / 'scripts' / name).read_bytes()).hexdigest()
                 for name in ('cap07-stage.py', 'cap07-env.py', 'cap06-env.py', 'capacity-stage.py',
                              'capacity-resources.py', 'go073-cap-read.mjs', 'load/go073-cap-read.js')}})
        before = owned.get_json('http://127.0.0.1:8351/debug/capacity')
        assert before['pid'] == api['pid']
        cards_delta(before, before, args.variant)
        save(Path(str(stem) + '-capacity-before.json'), {'at': now(), 'scope': SCOPE, 'capacity': before})
        command = [sys.executable, '-B', '-X', 'utf8', str(REPO / 'scripts/capacity-stage.py'),
                   '--mode', 'read', '--control-dir', str(control), '--api-pid', str(api['pid']),
                   '--diagnostics', 'http://127.0.0.1:8351/debug/capacity', '--interval', '5',
                   '--max-seconds', '360', '--stop-grace-seconds', '90', '--',
                   'node', 'scripts/go073-cap-read.mjs', 'run']
        result = subprocess.run(command, env=env)
        valid = False
        try:
            owned.api_identity(api, require_health=True)
            after = owned.get_json('http://127.0.0.1:8351/debug/capacity')
            assert after['pid'] == api['pid']
            save(Path(str(stem) + '-capacity-after.json'), {'at': now(), 'scope': SCOPE, 'capacity': after})
            delta = cards_delta(before, after, args.variant)
            save(Path(str(stem) + '-cards-delta.json'), delta)
            valid = True
        except Exception as error:
            save(Path(str(stem) + '-cards-error.json'), {'at': now(), 'class': type(error).__name__, 'detailsSuppressed': True})
        save(Path(str(stem) + '-stage-exit.json'), {'task': 'CAP-07', 'kind': args.kind,
             'capacityWrapperExit': result.returncode, 'cardsEvidenceValid': valid, 'finishedAt': now(),
             'limits': 'Wrapper exit is supervision only. Read load/result.json, resource stop and cards evidence separately.'})
        return result.returncode or (0 if valid else 1)
    finally:
        if read(lock)['pid'] == os.getpid() and read(lock)['stage'] == args.tag:
            lock.unlink()


if __name__ == '__main__':
    try:
        raise SystemExit(main())
    except Exception as error:
        print('cap07-stage: ' + type(error).__name__ + '; details suppressed; preserve all stage/slot evidence')
        raise SystemExit(1)
