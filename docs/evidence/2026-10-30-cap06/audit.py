"""Read-only cross-file audit of this fixed CAP06 cohort; no API/SQL calls.

Prints the audit JSON to stdout. Redirect to a new output; preserve prior files.
Checks saved exports, not unarchived response bodies or HTTP latency samples.
"""
import datetime as dt
import hashlib
import json
from pathlib import Path
import subprocess


BASE = Path(__file__).resolve().parent
REPO = BASE.parents[2]
SOURCE = 'bb03d3fe9172f3bb369cbbaa473a2ab041ec4db5'
INPUTS, CHECKS = {}, []


def read(path, lines=False):
    raw = path.read_bytes()
    INPUTS[path.relative_to(REPO).as_posix()] = {'sha256': hashlib.sha256(raw).hexdigest(), 'bytes': len(raw)}
    text = raw.decode('utf-8-sig')
    return [json.loads(x) for x in text.splitlines() if x] if lines else json.loads(text)


def check(name, condition):
    CHECKS.append({'check': name, 'passed': bool(condition)})


def stamp(value):
    return dt.datetime.fromisoformat(value.replace('Z', '+00:00')).timestamp() * 1000


def main():
    fixture = read(BASE / 'fixture/fixture.json')
    fixture_hash = INPUTS[(BASE / 'fixture/fixture.json').relative_to(REPO).as_posix()]['sha256']
    sources = {}
    for revision in ['1bed127', '3904d7d']:
        sha = subprocess.check_output(['git', 'rev-parse', revision], cwd=REPO, text=True).strip()
        sources[sha] = {name: hashlib.sha256(subprocess.check_output(['git', 'show', sha + ':scripts/' + name], cwd=REPO)).hexdigest()
                        for name in ['cap06-stage.py', 'cap06-tail.py', 'capacity-stage.py', 'capacity-resources.py', 'go073-cap-read.mjs']}
    stages = []
    names = ['baseline-off-probe', 'baseline-off-short25', 'baseline-on-probe', 'baseline-on-short25']
    names += ['baseline-diag-' + str(x) for x in range(1, 4)]
    for name in names:
        load, control = BASE / (name + '-load'), BASE / (name + '-control')
        report, command = read(load / 'result.json'), read(BASE / (name + '-command.json'))
        metadata_name = command['apiMetadataPath'].replace('\\', '/').rsplit('/', 1)[-1]
        check(name + ':known_metadata_file', metadata_name in ['api-baseline-off.json', 'api-baseline-on.json'])
        if metadata_name not in ['api-baseline-off.json', 'api-baseline-on.json']:
            raise ValueError('unrecognized captured metadata filename')
        metadata_path = BASE / metadata_name
        metadata = read(metadata_path)
        check(name + ':metadata_bytes_and_source', INPUTS[metadata_path.relative_to(REPO).as_posix()]['sha256'] == command['apiMetadataSHA256']
              and metadata['sourceSha'] == SOURCE and metadata['pid'] == command['apiPid']
              and metadata['expectedVersion'] == 'capacity-' + SOURCE
              and metadata['sha256'] == 'a8ef9aa640164b0eaad729139e348491bafbcd19220c6a62b7a97fcc152a6d79')
        health = read(BASE / ('api-' + metadata['label'] + '-health.json'))
        check(name + ':startup_identity_and_switch', health['ready'] and health['readyBody']['version'] == metadata['expectedVersion']
              and health['capacityBody']['pid'] == metadata['pid']
              and health['capacityBody']['tail']['enabled'] is metadata['tailEnabledRequested'])
        metrics = read(load / 'stable-summary.json')['metrics']
        supervision = read(control / 'exit-status.json')
        result = report['result']
        for field, metric in [('attempted', 'cap_read_attempted'), ('completedHttpRequests', 'http_reqs'),
                              ('completed', 'cap_read_completed'), ('successful', 'cap_read_success'),
                              ('systemErrors', 'cap_read_system_errors'), ('httpErrors', 'cap_read_http_errors'),
                              ('contentErrors', 'cap_read_content_errors'), ('dropped', 'dropped_iterations')]:
            check(name + ':' + field, result[field] == metrics[metric]['count'])
        check(name + ':complete_success', result['attempted'] == result['completedHttpRequests'] == result['completed'] == result['successful'])
        check(name + ':unknown_zero', result['incompleteOrUnclassified'] == result['attempted'] - result['completed'] == 0)
        for output, metric in [('allLatency', 'cap_read_all_ms'), ('successfulLatency', 'cap_read_success_ms')]:
            for quantile, budget in [('p(95)', 200), ('p(99)', 500)]:
                check(name + ':' + output + quantile, result[output][quantile] == metrics[metric][quantile] <= budget)
        check(name + ':source_and_fixture', report['apiSourceSha'] == command['apiSourceSha'] == fixture['sourceSha'] == SOURCE
              and report['apiExpectedRevision'] == 'capacity-' + SOURCE and report['fixtureManifestSHA256'] == fixture_hash)
        check(name + ':recorded_tools', set(command['scriptHashes']) == set(next(iter(sources.values())))
              and any(all(hash_value == source.get(script) for script, hash_value in command['scriptHashes'].items())
                      for source in sources.values()))
        check(name + ':budget_result', report['meetsBudget'] is True and report['stableExit'] == 0
              and result['actualOfferedRatio'] == result['attempted'] / (report['rate'] * report['seconds']) >= .99
              and all(result[x] == 0 for x in ['systemErrors', 'httpErrors', 'contentErrors', 'dropped']))
        resource_rows = read(control / 'resources.ndjson', lines=True)
        samples = [x for x in resource_rows if x.get('kind') == 'sample']
        check(name + ':complete_supervision', supervision['completed_without_supervision_failure'] is True
              and not supervision['stop_signal'] and bool(samples)
              and len(samples) == supervision['resource_samples'] == supervision['complete_resource_samples']
              and all(not x['errors'] and x['generators'] and x['api']['pid'] == metadata['pid'] for x in samples))
        access = read(load / 'access-delta.json')
        check(name + ':access_counters_valid', not access['counterResets'] and access['uptimeConsistent']
              and access['lostDigestsDelta'] == access['preparedStatementsLostDelta'] == 0)
        item = {'stage': name, 'kind': command['kind'], 'attempted': result['attempted'], 'successful': result['successful'],
                'p95': result['allLatency']['p(95)'], 'p99': result['allLatency']['p(99)'], 'resource_samples': len(samples),
                'formal_CAP06D_result': False}
        if command['kind'] == 'diagnostic':
            diagnostic = BASE / (name + '-diagnostic')
            manifest = read(diagnostic / 'manifest.json')
            rows = read(diagnostic / 'timeline.ndjson', lines=True)
            check(name + ':complete_collector', manifest['successful_polls'] == manifest['samples'] == len(rows)
                  and manifest['failed_polls'] == 0 and not manifest['errors'] and all(not row['errors'] for row in rows))
            check(name + ':bounded_diagnostic', manifest['diagnostic_only'] is True and len(manifest['captures']) <= 3
                  and report['seconds'] == 180 and report['rate'] == 100 and report['mode'] == 'multi')
            for capture in manifest['captures']:
                raw = (diagnostic / capture['file']).read_bytes()
                check(name + ':capture_hash', hashlib.sha256(raw).hexdigest() == capture['sha256'])
                check(name + ':load_time_guards', capture['stable_alive_before_capture'] and capture['stable_alive_after_capture'])
            traces = {trace['sequence']: trace for row in rows for trace in row['diagnostics']['tail']['slow_traces']
                      if report['stableStartedAt'] <= stamp(trace['start_at']) <= stamp(trace['end_at']) <= report['stableEndedAt']}
            item.update(collector_polls=len(rows), captures=len(manifest['captures']), sampled_fresh_slow_traces=len(traces))
        stages.append(item)
    slots = sorted(BASE.glob('diagnostic-slot-*.json'))
    check('exact_three_reserved_rounds', len(slots) == 3 and {read(p)['number'] for p in slots} == {1, 2, 3})
    cleanup = read(BASE / 'cleanup.json')
    owned = read(BASE / 'environment.json')['containers']
    check('cleanup_complete', cleanup['complete'] and cleanup['sessionsAfterLogout'] == 0 and not cleanup['exceptions']
          and cleanup['volumesRetained'] and set(cleanup['containers']) == set(owned) == {'mysql', 'redis'}
          and all(not c['running'] and c['exitCode'] == 0 and not c['oomKilled']
                  and c['id'] == owned[service]['id'] and c['volumes'] == owned[service]['volumes']
                  for service, c in cleanup['containers'].items()))
    source_bytes = Path(__file__).read_bytes()
    output = {'task': 'CAP-06', 'at': dt.datetime.now(dt.timezone.utc).isoformat(), 'internal_only': True,
              'original_tail_cause_confirmed': False, 'CAP06_closed': False, 'CAP07_implemented': False,
              'script_sha256': hashlib.sha256(source_bytes).hexdigest(), 'tool_sources': sources,
              'checks': CHECKS, 'failed_checks': [x for x in CHECKS if not x['passed']], 'stages': stages, 'inputs': INPUTS,
              'limits': ['Cross-checks saved native aggregates, not individual HTTP bodies or reconstructed quantiles.',
                         'HTTP capacity payload SHA records do not archive the original response encoding; not rehashed here.',
                         'Diagnostic rounds have extra observations and cannot substitute for CAP06D or overwrite the independent failure.',
                         'Request/store stages overlap and pool samples include background activity; no causal repair has been established.']}
    print(json.dumps(output, ensure_ascii=False, indent=2))
    return bool(output['failed_checks'])


if __name__ == '__main__':
    raise SystemExit(main())
