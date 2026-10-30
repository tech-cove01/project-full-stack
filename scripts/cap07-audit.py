"""Read-only CAP07 arithmetic/hash/protocol audit; no API, SQL or load access.

Percentiles remain the native per-stage k6 summary values. This audit cannot
reconstruct response distributions or merge percentiles across repetitions.
"""
import argparse
import hashlib
import json
import math
from pathlib import Path
import re


REPO = Path(__file__).resolve().parent.parent
ROOT = REPO / 'docs/evidence/2026-10-30-cap07'
A_SHA = 'f3d99ee43dfb422a0cba525de93e9266f07c4ee5'
COUNTERS = ('requests', 'l1_hits', 'redis_hits', 'miss_items', 'loads_started', 'flight_started',
            'flight_joins', 'content_sql_batches', 'content_sql_rows', 'content_bytes', 'load_errors',
            'oversize_uncached', 'redis_errors', 'invalid_payloads', 'busy_rejected', 'expired', 'evictions')
GAUGES = ('inflight', 'active_loads', 'waiting_callers', 'l1_entries', 'l1_bytes')


def read(path):
    return json.loads(path.read_text(encoding='utf-8-sig'))


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def require(condition, message):
    if not condition:
        raise ValueError(message)


def native_score(summary, report, command):
    metrics = summary['metrics']
    names = {'attempted': 'cap_read_attempted', 'http': 'http_reqs', 'completed': 'cap_read_completed',
             'successful': 'cap_read_success', 'systemErrors': 'cap_read_system_errors',
             'contentErrors': 'cap_read_content_errors', 'httpErrors': 'cap_read_http_errors', 'dropped': 'dropped_iterations'}
    counts = {key: metrics[name]['count'] for key, name in names.items()}
    require(all(type(value) in (int, float) and math.isfinite(value) and value >= 0 and value == int(value)
                for value in counts.values()), 'native counters missing, negative or nonintegral')
    all_ms, success_ms = metrics['cap_read_all_ms'], metrics.get('cap_read_success_ms')
    require(all(type(all_ms.get(key)) in (int, float) and math.isfinite(all_ms[key]) and all_ms[key] >= 0 for key in ('p(95)', 'p(99)')), 'native all latency missing')
    successful_latency = (isinstance(success_ms, dict) and all(type(success_ms.get(key)) in (int, float) and
                          math.isfinite(success_ms[key]) and success_ms[key] >= 0 for key in ('p(95)', 'p(99)')))
    a, h, c, s = (counts[key] for key in ('attempted', 'http', 'completed', 'successful'))
    require(a >= h >= c >= s, 'native classification counts out of order')
    denominator = command['rate'] * command['seconds']
    met = (report.get('warmExit') == 0 and report.get('stableExit') == 0 and
           not report.get('warmExternalStop') and not report.get('stableExternalStop') and
           not report.get('failure') and a > 0 and a == h == c and a / denominator >= .99 and
           s / a >= .99 and counts['dropped'] == 0 and counts['contentErrors'] == 0 and
           counts['systemErrors'] / a < .01 and counts['httpErrors'] / a < .01 and
           all_ms['p(95)'] <= 200 and all_ms['p(99)'] <= 500 and successful_latency and
           success_ms['p(95)'] <= 200 and success_ms['p(99)'] <= 500)
    return {'meetsBudget': bool(met), **counts, 'unknown': a - c,
            'targetOffers': denominator, 'offeredRatio': a / denominator,
            'successfulFraction': s / a if a else None, 'successfulRPS': s / command['seconds'],
            'allP95Ms': all_ms['p(95)'], 'allP99Ms': all_ms['p(99)'],
            'successfulP95Ms': success_ms['p(95)'] if successful_latency else None,
            'successfulP99Ms': success_ms['p(99)'] if successful_latency else None}


def loader_score(root, command):
    stem = root / command['tag']
    bodies = [read(Path(str(stem) + suffix))['capacity'] for suffix in ('-capacity-before.json', '-capacity-after.json')]
    require(all(body.get('pid') == command['apiPid'] for body in bodies), 'loader snapshot API identity differs')
    before, after = (body.get('cards') for body in bodies)
    recorded = read(Path(str(stem) + '-cards-delta.json'))
    require(recorded.get('valid') is True and recorded.get('variant') == command['variant'], 'recorded loader classification differs')
    if command['variant'] == 'A':
        require(before is None and after is None and recorded.get('contentLoaderCounters') is None, 'A loader absence must remain null, not zero')
        return None
    require(isinstance(before, dict) and isinstance(after, dict), 'B loader snapshots missing')
    require(before.get('coalescing') is True and after.get('coalescing') is True, 'B is not final shared-flight implementation')
    require(all(type(value.get(key)) is int and value[key] >= 0 for value in (before, after) for key in COUNTERS + GAUGES), 'invalid loader counter/gauge')
    delta = {key: after[key] - before[key] for key in COUNTERS}
    require(min(delta.values()) >= 0 and delta == recorded['counterDelta'], 'loader counter reset or arithmetic differs')
    require(recorded['beforeGauges'] == {key: before[key] for key in GAUGES} and
            recorded['afterGauges'] == {key: after[key] for key in GAUGES}, 'loader gauges differ')
    bounds = dict(zip(GAUGES, (64, 8, 1024, 2048, 8 << 20)))
    require(all(value[key] <= bound for value in (before, after) for key, bound in bounds.items()), 'loader gauge exceeds fixed bound')
    return delta


def stage_score(root, command, fixture_sha):
    out = {key: command[key] for key in ('tag', 'kind', 'variant', 'apiSourceSha', 'mode', 'rate', 'seconds')}
    out.update(measured=False, capacityPassed=False, errors=[])
    try:
        require(re.fullmatch(r'[a-z0-9][a-z0-9-]{1,60}', command['tag']), 'invalid stage tag')
        require(command['variant'] in ('A', 'B') and re.fullmatch(r'[a-f0-9]{40}', command['apiSourceSha']), 'invalid source/variant')
        require(command['kind'] in ('probe', 'comparison', 'stair', 'neighbor', 'hold', 'hot') and
                command['mode'] in ('multi', 'hot') and 1 <= command['rate'] <= 400 and 1 <= command['seconds'] <= 180,
                'stage outside declared kinds/bounds')
        require(re.fullmatch(r'(probe-[ab]|comparison-[1-4]|stair-[1-4]|neighbor-1|hold-[12]|hot-1)', command['slot']), 'invalid immutable slot')
        reservation = read(root / ('slot-' + command['slot'] + '.json'))
        require(reservation.get('stage') == command['tag'] and reservation.get('failedStartsConsumeSlot') is True, 'immutable reservation differs')
        require(command['variant'] != 'A' or command['apiSourceSha'] == A_SHA, 'A differs from fixed CAP06V SHA')
        metadata_path = Path(command['apiMetadataPath']).resolve()
        require(metadata_path.parent == root and sha(metadata_path) == command['apiMetadataSHA256'], 'API metadata hash/path differs')
        api = read(metadata_path)
        require(api['sourceSha'] == command['apiSourceSha'] and api['variant'] == command['variant'] and
                api['expectedVersion'] == 'capacity-' + command['apiSourceSha'] and api['tailEnabledRequested'] is False and
                api['pid'] == command['apiPid'],
                'API metadata version/variant/diagnostics differs')
        require(sha(Path(api['exe'])) == api['sha256'], 'immutable API binary hash differs or binary absent')
        build = (root / ('api-' + api['label'] + '-build.txt')).read_text(encoding='utf-8-sig')
        require('vcs.revision=' + command['apiSourceSha'] in build and 'vcs.modified=false' in build, 'clean build VCS evidence missing')
        require(set(command['scriptHashes']) == {'cap07-stage.py', 'cap07-env.py', 'cap06-env.py', 'capacity-stage.py',
                'capacity-resources.py', 'go073-cap-read.mjs', 'load/go073-cap-read.js'}, 'required tool hash set differs')
        for name, digest in command['scriptHashes'].items():
            path = (REPO / 'scripts' / name).resolve()
            require(path.is_relative_to(REPO / 'scripts') and sha(path) == digest, 'recorded tool hash differs from current audited file')
        require(command['preVUs'] == 256 and command['maxVUs'] == 512 and
                command['warmSeconds'] == (1 if command['kind'] == 'probe' else 10) and
                command['warmRate'] == min(command['rate'], 25), 'workload configuration differs')
        stem = root / command['tag']
        report = read(Path(str(stem) + '-load/result.json'))
        require(report['apiSourceSha'] == command['apiSourceSha'] and report['fixtureManifestSHA256'] == fixture_sha and
                all(report[key] == command[key] for key in ('mode', 'rate', 'seconds', 'warmSeconds', 'warmRate', 'preVUs', 'maxVUs')),
                'native report source/fixture/parameters differs')
        require(report['apiExpectedRevision'] == 'capacity-' + command['apiSourceSha'] and
                report['fixtureSourceSha'] == A_SHA and report['fixtureExpectedRevision'] == 'capacity-' + A_SHA and
                report['pageSize'] == 20 and report['activeReaders'] == (1 if command['mode'] == 'hot' else 32) and
                report['activeCountKeys'] == (20 if command['mode'] == 'hot' else 640), 'native revision/key population differs')
        require(all(digest == command['scriptHashes'][name.removeprefix('scripts/')]
                    for name, digest in report.get('scriptSHA256', {}).items()) and
                set(report.get('scriptSHA256', {})) == {'scripts/go073-cap-read.mjs', 'scripts/load/go073-cap-read.js'}, 'native workload script hashes differ')
        summary_path = Path(str(stem) + '-load/stable-summary.json')
        if not summary_path.is_file():
            require(report.get('meetsBudget') is False, 'unmeasured attempt promoted to pass')
            out['status'] = 'failed before complete native stable summary; preserved'
            return out
        score = native_score(read(summary_path), report, command)
        out.update(measured=True, native=score)
        require(report.get('meetsBudget') is score['meetsBudget'], 'native SLO arithmetic differs from report')
        counted = report['result']
        for native, saved in (('attempted', 'attempted'), ('http', 'completedHttpRequests'), ('completed', 'completed'),
                              ('successful', 'successful'), ('systemErrors', 'systemErrors'), ('contentErrors', 'contentErrors'),
                              ('httpErrors', 'httpErrors'), ('dropped', 'dropped'), ('unknown', 'incompleteOrUnclassified')):
            require(score[native] == counted[saved], 'native counts differ from report result')
        require(score['allP95Ms'] == counted['allLatency']['p(95)'] and score['allP99Ms'] == counted['allLatency']['p(99)'], 'native all percentiles differ from report')
        if score['successful']:
            require(score['successfulP95Ms'] == counted['successfulLatency']['p(95)'] and
                    score['successfulP99Ms'] == counted['successfulLatency']['p(99)'], 'native success percentiles differ from report')
        out['loaderCounterDeltaIncludingWarmup'] = loader_score(root, command)
        access = read(Path(str(stem) + '-load/access-delta.json'))
        before = read(Path(str(stem) + '-load/before.json'))['globalStatus']['counters']
        after = read(Path(str(stem) + '-load/after.json'))['globalStatus']['counters']
        sql = {key: after[key] - before[key] for key in ('Com_select', 'Com_stmt_execute')}
        require(all(value >= 0 and value == access['globalCommands'][key] for key, value in sql.items()), 'instance SQL arithmetic differs or reset')
        out['overlappingInstanceSqlCounters'] = sql
        stage = read(Path(str(stem) + '-stage-exit.json'))
        control = read(Path(str(stem) + '-control/exit-status.json'))
        supervised = (control.get('harness_exit') == 0 and control.get('sampler_exit') == 0 and
                      not control.get('stop_signal') and not control.get('errors') and not control.get('forced'))
        out['resourceOrSupervisionStop'] = bool(control.get('stop_signal') or control.get('errors') or control.get('forced'))
        out['capacityPassed'] = score['meetsBudget'] and stage['capacityWrapperExit'] == 0 and stage['cardsEvidenceValid'] is True and supervised
    except (OSError, ValueError, KeyError, TypeError) as error:
        out['errors'].append(str(error) if isinstance(error, ValueError) else 'missing/malformed required public artifact: ' + type(error).__name__)
    return out


def plan_score(stages):
    comparisons = [row for row in stages if row['kind'] == 'comparison']
    stairs = [row for row in stages if row['kind'] == 'stair']
    neighbors = [row for row in stages if row['kind'] == 'neighbor']
    holds, hot = ([row for row in stages if row['kind'] == kind] for kind in ('hold', 'hot'))
    errors = []
    def check(condition, message):
        if not condition:
            errors.append(message)
    check(len({row['tag'] for row in stages}) == len(stages), 'duplicate stage evidence cannot count as a repetition')
    check([row['variant'] for row in comparisons] == list(('A', 'B', 'A', 'B')[:len(comparisons)]) and len(comparisons) <= 4, 'comparison order/count differs')
    check(all((row['mode'], row['rate'], row['seconds']) == ('multi', 100, 180) for row in comparisons), 'comparison parameters differ')
    check(all(len({row['apiSourceSha'] for row in stages if row['variant'] == variant and row['kind'] != 'probe'}) <= 1 for variant in ('A', 'B')), 'variant source SHA changed')
    b100 = [row for row in comparisons if row['variant'] == 'B']
    base_ok = len(b100) == 2 and all(row['capacityPassed'] for row in b100)
    check(len(stairs) <= 4 and [row['rate'] for row in stairs] == [150, 200, 300, 400][:len(stairs)], 'stair limit/order differs')
    check(all((row['variant'], row['mode'], row['seconds']) == ('B', 'multi', 45) for row in stairs), 'stair parameters differ')
    check(not stairs or (len(comparisons) == 4 and base_ok), 'ascent without both final-B baseline passes')
    check(all(row['capacityPassed'] for row in stairs[:-1]), 'ascent continued after first failed stair')
    failed = [row for row in stairs if not row['capacityPassed']]
    high = max([100] + [row['rate'] for row in stairs if row['capacityPassed']])
    check(len(neighbors) <= 1, 'more than one neighbor attempt')
    for row in neighbors:
        check(len(failed) == 1 and high < row['rate'] < failed[0]['rate'] and row['seconds'] == 45 and
              row['variant'] == 'B' and row['mode'] == 'multi' and failed[0]['measured'] and
              not failed[0].get('errors') and not failed[0].get('resourceOrSupervisionStop'), 'neighbor is not a measured first-limit bracket')
        if row['capacityPassed']:
            high = max(high, row['rate'])
    check(len(holds) <= 2 and all((row['variant'], row['mode'], row['rate'], row['seconds']) == ('B', 'multi', high, 180) for row in holds), 'highest hold parameters/count differs')
    check(high > 100 or not holds, 'highest100 must reuse B comparisons')
    check(len(hot) <= 1 and all((row['variant'], row['mode'], row['rate'], row['seconds']) == ('B', 'hot', 200, 180) for row in hot), 'hot hold parameters/count differs')
    for variant in ('A', 'B'):
        probes = [row for row in stages if row['kind'] == 'probe' and row['variant'] == variant]
        check(len(probes) <= 1 and all(row['rate'] <= 25 and row['seconds'] <= 15 for row in probes), 'probe budget differs')
    position = {row['tag']: number for number, row in enumerate(stages)}
    exploration = stairs + neighbors
    if exploration:
        check(len(comparisons) == 4 and min(position[row['tag']] for row in exploration) >
              max(position[row['tag']] for row in comparisons), 'exploration occurred before comparison completion')
    if neighbors and failed:
        check(position[neighbors[0]['tag']] > position[failed[0]['tag']], 'neighbor preceded its first failed stair')
    if holds:
        check(bool(exploration) and min(position[row['tag']] for row in holds) >
              max(position[row['tag']] for row in exploration), 'highest hold preceded completed exploration')
    if hot:
        earlier = holds or exploration
        check(bool(earlier) and position[hot[0]['tag']] > max(position[row['tag']] for row in earlier), 'hot hold preceded exploration/highest holds')
    explored = len(stairs) == 4 or bool(failed)
    final = (not errors and len(comparisons) == 4 and base_ok and explored and
             (high == 100 or (len(holds) == 2 and all(row['capacityPassed'] for row in holds))) and
             len(hot) == 1 and hot[0]['capacityPassed'] and not any(row['errors'] for row in stages))
    return {'errors': errors, 'finalBProtocolPassed': final, 'highestExploredPassedRate': high if base_ok else None,
            'aRepeatedPassed': len(comparisons) == 4 and all(row['capacityPassed'] for row in comparisons if row['variant'] == 'A'),
            'limits': 'A/B compare only rate100. Higher B-only stairs cannot establish a capacity increase; no averaging or merging of per-stage percentiles.'}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    require(Path.cwd().resolve() == REPO and args.output.resolve().parent == ROOT and
            re.fullmatch(r'audit-[a-z0-9-]+\.json', args.output.name), 'new audit-*.json inside fixed CAP07 evidence root required')
    manifest = ROOT / 'fixture/fixture.json'
    fixture = read(manifest)
    require(fixture['status'] == 'READY' and [fixture[key] for key in ('authors', 'readers', 'notesPerAuthor', 'noteCount', 'pageSize')] == [32, 32, 200, 6400, 20], 'fixed fresh fixture shape differs')
    require(fixture['sourceSha'] == A_SHA and fixture['expectedRevision'] == 'capacity-' + A_SHA and
            fixture['project'] == 'project-full-stack-cap-read' and fixture['baseUrl'] == 'http://127.0.0.1:8251', 'fixture provenance differs')
    environment = read(ROOT / 'environment.json')
    require(environment['task'] == 'CAP-07' and environment['project'] == 'project-full-stack-cap-read' and environment['oldVolumesRetained'] is True and
            all(re.fullmatch(r'project-full-stack-cap-read-cap07-[a-f0-9]{12}-' + service, environment['newVolumes'][service]) and
                environment['containers'][service]['id'] != environment['oldContainers'][service]['id'] for service in ('mysql', 'redis')), 'fresh CAP07 dependency ownership evidence differs')
    commands = sorted((read(path) for path in ROOT.glob('*-command.json')), key=lambda row: row['at'])
    require(commands and all(row.get('task') == 'CAP-07' for row in commands), 'CAP07 command attempts required')
    require(len({row['slot'] for row in commands}) == len(commands), 'an immutable attempt slot cannot count twice')
    stages = [stage_score(ROOT, row, sha(manifest)) for row in commands]
    plan = plan_score(stages)
    reservations = {path.name.removeprefix('slot-').removesuffix('.json') for path in ROOT.glob('slot-*.json')}
    if reservations != {row['slot'] for row in commands}:
        plan['errors'].append('orphan/missing immutable reservation; preserve the failed start')
        plan['finalBProtocolPassed'] = False
    report = {'task': 'CAP-07', 'auditScriptSHA256': sha(Path(__file__)), 'fixtureManifestSHA256': sha(manifest),
              'stages': stages, 'plan': plan, 'sourceOfPercentiles': 'native per-stage stable-summary.json only; no raw-response distribution reconstruction',
              'loaderScope': 'includes warmup through post-wrapper reconciliation, separate from stable-window instance SQL; counts are not zero-DB claims'}
    with args.output.open('x', encoding='utf-8') as stream:
        json.dump(report, stream, ensure_ascii=False, indent=2)
    print(json.dumps({'stages': len(stages), 'artifactErrors': sum(bool(row['errors']) for row in stages), **plan}, ensure_ascii=False))
    return 0 if not plan['errors'] and not any(row['errors'] for row in stages) else 1


if __name__ == '__main__':
    try:
        raise SystemExit(main())
    except Exception as error:
        print('cap07-audit: ' + type(error).__name__ + '; required public evidence absent/inconsistent; no environment accessed')
        raise SystemExit(1)
