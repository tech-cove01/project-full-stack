"""Assemble saved CAP03 results only; never connects to a service or reads secrets."""
import collections
import argparse
import json
import hashlib
from pathlib import Path
import subprocess
import sys


def read(path):
    return json.loads(path.read_text(encoding='utf-8-sig'))


base = Path(__file__).resolve().parent
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--api-metadata', type=Path, required=True, help='explicit current API metadata; old application results are historical only')
args = parser.parse_args()
current_api = read(args.api_metadata)
stages = []
for result_path in base.glob('*-load/result.json'):
    report = read(result_path)
    tag = result_path.parent.name[:-5]
    derived = base / (tag + '-summary.json')
    if not derived.exists() and (result_path.parent / 'stable-summary.json').exists():
        result = subprocess.run([sys.executable, '-X', 'utf8', str(base / 'summarize.py'), str(base / tag)], capture_output=True, text=True, encoding='utf-8')
        if result.returncode:
            raise RuntimeError('saved-stage summary unavailable: ' + tag)
    details = read(derived) if derived.exists() else {}
    category = 'hold' if tag.startswith('hold-') else 'diagnostic' if tag.startswith('profile-') else 'probe' if tag.startswith('probe-') else 'post-pool-fix-exploration' if tag.startswith('post-fix-') else 'post-index-exploration' if tag.startswith('post-') else 'pre-index-exploration'
    stages.append({'stage': tag, 'category': category, 'startedAt': report['started'],
        'mode': report['mode'], 'targetRate': report['rate'], 'plannedSeconds': report['seconds'],
        'apiSourceSha': report.get('apiSourceSha'), 'apiProvenance': 'explicit' if report.get('apiSourceSha') else 'legacy-no-explicit-api-source', 'fixtureSourceSha': report['fixtureSourceSha'],
        'preVUs': report['preVUs'], 'maxVUs': report['maxVUs'], 'meetsBudget': report['meetsBudget'],
        'failure': report.get('failure'), 'stableCommandElapsedMs': report.get('stableCommandElapsedMs'),
        'result': report.get('result'), 'completionAudit': details.get('completionAudit'),
        'supervisionOk': details.get('supervisionOk'), 'stopSignal': details.get('stopSignal'),
        'resources': details.get('resources'), 'mysqlGlobalDelta': details.get('mysqlGlobalDelta'),
        'redisDelta': details.get('redisDelta'), 'counterResets': details.get('counterResets'),
        'uptimeConsistent': details.get('uptimeConsistent'), 'scriptSha256': report.get('scriptSHA256')})
stages.sort(key=lambda item: item['startedAt'])
unscored = []
for marker_path in base.glob('*-load/unscored-failure.json'):
    marker = read(marker_path)
    tag = marker_path.parent.name[:-5]
    control_path = base / (tag + '-control') / 'exit-status.json'
    control = read(control_path)
    assert marker['classification'] == 'native_harness_abort_before_stable'
    assert marker['stage'] == tag and marker['counts'] is None and marker['scored'] is False
    assert marker['stablePhaseStarted'] is False and marker['samplerStarted'] is False
    assert control['harness_exit'] != 0 and control['sampler_started'] is False and control['resource_samples'] == 0
    assert marker['harnessExit'] == control['harness_exit'] and not control['stop_signal'] and not control['forced'] and not control['forced_owned_processes']
    references = [item for item in marker['evidence'] if Path(item['path']).parts[-2:] == (tag + '-control', 'exit-status.json')]
    assert len(references) == 1 and references[0]['sha256'] == hashlib.sha256(control_path.read_bytes()).hexdigest()
    assert not any((marker_path.parent / name).exists() for name in ('before.json', 'process.json', 'stable-summary.json', 'result.json'))
    unscored.append(marker)
qualified = {}
for mode in ('hot', 'multi'):
    groups = collections.defaultdict(list)
    for stage in stages:
        if stage['category'] == 'hold' and stage['mode'] == mode and stage['apiSourceSha'] == current_api['sourceSha'] and stage['plannedSeconds'] == 180 and stage['meetsBudget'] and stage['supervisionOk']:
            coverage = stage['resources']
            if coverage['completeSamples'] == coverage['samples'] and coverage['samples'] >= 35 and not stage['completionAudit']['incompleteOrUnclassified']:
                groups[stage['targetRate']].append(stage)
    rates = [rate for rate, rounds in groups.items() if len(rounds) >= 2]
    qualified[mode] = {'rate': max(rates), 'secondsPerRound': 180, 'rounds': groups[max(rates)][:2]} if rates else None
cleanup_path = base / 'cleanup.json'
cleanup = read(cleanup_path) if cleanup_path.exists() else {'state': 'pending'}
out = {'task': 'GO-073-CAP-03', 'state': 'complete-self-tested' if all(qualified.values()) and cleanup.get('complete') else 'in_progress',
    'api': current_api, 'schemaCommit': '5aa658a7c5c65149d3e0c47759d3798e71c7ddfd',
    'data': {'notes': 6400, 'authors': 32, 'readers': 32, 'pageSize': 20, 'bodyAsciiBytes': 256},
    'qualifiedModes': qualified, 'stages': stages, 'unscoredStages': unscored, 'cleanup': cleanup,
    'criteria': {'offeredRatioMin': .99, 'successfulFractionMin': .99, 'droppedMax': 0, 'unknownMax': 0,
        'systemAndHttpErrorRatesMaxExclusive': .01, 'contentErrorsMax': 0,
        'allAndSuccessP95MsMax': 200, 'allAndSuccessP99MsMax': 500, 'resourceStopAllowed': False},
    'scope': 'shared Windows/Docker host and fixed pools/resources; repeated passing hold is an observed bound, not theoretical server maximum',
    'sqlRedisScope': 'whole isolated instance between sequential snapshots, includes background/auth/helper/observer; Com_select and Com_stmt_execute overlap, never add them',
    'publicCountDatabaseLoads': None, 'responseBodiesRetained': False, 'independentExternalVerification': 'pending'}
(base / 'summary.json').write_text(json.dumps(out, ensure_ascii=False, indent=2), encoding='utf-8')
print(json.dumps({'state': out['state'], 'savedStages': len(stages), 'qualifiedRates': {mode: result['rate'] if result else None for mode, result in qualified.items()}, 'cleanupComplete': bool(cleanup.get('complete'))}))
