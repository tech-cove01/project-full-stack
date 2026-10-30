"""Crosscheck new independent CAP artifacts; stdout only, no services or writes.

An integrity pass for a limited stage does not mean its capacity budget passed.
Missing result/control files are pending and make this script exit nonzero.
"""
import argparse
import datetime as dt
import hashlib
import importlib.util
import json
from pathlib import Path
import sys


ROOT = Path(__file__).resolve().parents[4]
sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location('archived_audit', Path(__file__).with_name('offline-audit.py'))
archived = importlib.util.module_from_spec(spec)
spec.loader.exec_module(archived)
read_json, read_lines, check = archived.read_json, archived.read_lines, archived.check


def millis(value):
    return dt.datetime.fromisoformat(value.replace('Z', '+00:00')).timestamp() * 1000


def resource_audit(control_dir, load_dir, api, mode, stable_start=None, stable_end=None):
    control = read_json(control_dir / 'exit-status.json')
    rows = read_lines(control_dir / 'resources.ndjson')
    configuration = rows[0]
    samples = [row for row in rows if row.get('kind') == 'sample']
    process = read_json(load_dir / ('stable-process.json' if mode == 'read' else 'process.json'))
    check(control['api_pid'] == api['pid'] == configuration['pid'], 'API PID differs')
    check(control['sampled_k6_pid'] == process['k6_pid'], 'generator PID differs')
    check(configuration['project'] == 'project-full-stack-cap-' + mode,
          'resource project differs')
    check(len(samples) == control['resource_samples'], 'resource sample count differs')
    complete = [row for row in samples if not row['errors'] and row['generators']]
    check(len(complete) == control['complete_resource_samples'], 'complete sample count differs')
    for row in samples:
        if 'api' in row:
            check(row['api']['pid'] == api['pid'], 'sample API PID differs')
        if 'diagnostics' in row:
            check(row['diagnostics']['pid'] == api['pid'], 'diagnostics PID differs')
        check(all(g['pid'] == process['k6_pid'] for g in row['generators']),
              'sample generator PID differs')
    check(control['sampler_exit'] == 0 and not control['forced'] and not control['errors'],
          'supervision unavailable/forced; inspect before capacity claims')
    expected_env = ({'GOMAXPROCS': None, 'GOMEMLIMIT': None} if mode == 'trade'
                    else {'GOMAXPROCS': '4', 'GOMEMLIMIT': '1GiB'})
    actual_env = control['generator_runtime_environment']
    check(all(actual_env[key] == value for key, value in expected_env.items()),
          'generator runtime conditions differ')
    times = [millis(row['at']) for row in samples]
    check(times and times == sorted(times), 'missing or nonmonotonic samples')
    summary = dict(samples=len(samples), completeSamples=len(complete),
                   validCPUIntervals=sum(bool(row.get('cpu_interval_valid')) for row in samples),
                   validGeneratorCPUIntervals=sum(bool(g.get('cpu_interval_valid'))
                       for row in samples for g in row['generators']),
                   firstAt=samples[0]['at'], lastAt=samples[-1]['at'],
                   startTimeSpanMs=times[-1] - times[0],
                   maxStartTimeGapMs=max((b - a for a, b in zip(times, times[1:])), default=0),
                   resourceStop=control['stop_signal'], samplerExit=control['sampler_exit'],
                   apiPID=api['pid'], k6PID=process['k6_pid'],
                   fullPostExitCoverage=False)
    if stable_start is not None:
        summary.update(firstSampleAfterStableCommandMs=times[0] - stable_start,
                       lastSampleBeforeStableCommandEndMs=stable_end - times[-1],
                       lastSampleEstimatedWorkAfterCommandMs=max(0, times[-1]
                           + samples[-1]['sampler']['sample_elapsed_ms'] - stable_end))
    if mode == 'read':
        summary['firstSampleAfterK6SpawnMs'] = times[0] - process['k6_started_unix_ms']
    return summary


def api_audit(path, source):
    api = read_json(path)
    check(api['sourceSha'] == source and api['expectedRevision'] == 'capacity-' + source,
          'unexpected API source')
    check(api['health']['version'] == api['expectedRevision']
          and api['health']['status'] == 'UP', 'API health version differs')
    check(api['diagnostics']['pid'] == api['pid'], 'API metadata PID differs')
    binary_hash = {
        '3db296dd0beedd6f85d06cf5479d707b0498ce7e':
            'ef29e9e3838b7ec8ffd1aca5b1a51848fbd8a020ca084228879481adc209bd69',
        '767c2fc8bdf1be08c5636143a71898eec25081a9':
            '97a5165e48a74c98ee521eebda01f79d049c44938239e2bd4ed89fe38e25e20c',
    }
    check(api['binarySHA256'] == binary_hash[source], 'saved API binary hash differs')
    return api


def trade_audit(base, stage):
    load, control = base / (stage + '-load'), base / (stage + '-control')
    result = read_json(load / 'result.json')
    api = api_audit(base / 'api.json', '3db296dd0beedd6f85d06cf5479d707b0498ce7e')
    check(read_json(load / 'api-health.json')['Version'] == api['expectedRevision'],
          'stage trade version differs')
    pool = read_json(base / 'prepare/pool-progress.json')
    direct = archived.trade(stage, pool, load)
    resources = resource_audit(control, load, api, 'trade')
    reconciliation = result['analysis']['reconciliation']
    check(reconciliation['mysqlStock'] == reconciliation['redisStock'], 'inventory differs')
    zero_fields = ('duplicateEffectiveOrders', 'mismatchedUserOrStatus', 'mappingMismatches',
                   'unresolvedReview', 'durableRejections', 'unfinishedOrderOrCompensationOutbox',
                   'finalLag', 'finalPending', 'unloggedDurableOrders')
    check(all(reconciliation[key] == 0 for key in zero_fields), 'unresolved trade outcome')
    check(result['model']['initialStock'] - reconciliation['durableRows']
          == reconciliation['mysqlStock'], 'inventory equation differs')
    check(result['k6ExitCode'] == 0 and result['observerErrors'] == 0,
          'trade run/observer not complete')
    assessment = result['lastThirdQueueAssessment']
    queue_budget = (assessment['measured']
                    and direct['lastThirdDB'] / direct['lastThirdAccepted'] >= .98
                    and direct['lagNet'] <= 2 * result['model']['rate'])
    if result['model']['stableSeconds'] >= 180:
        last_minute = result['lastMinuteQueueAssessment']
        queue_budget = (queue_budget and last_minute['measured']
                        and last_minute['dbToAcceptedRatio'] >= .98
                        and last_minute['lagNetGrowth'] <= 2 * result['model']['rate']
                        and result['nominalStableEndToConvergedObservedMs'] <= 15000)
    budget = (direct['stableRequests'] /
              (result['model']['rate'] * result['model']['stableSeconds']) >= .99
              and direct['dropped'] == 0 and direct['rawCounters']['cap_system_errors'] == 0
              and direct['rawCounters']['cap_accepted'] == direct['rawCounters']['cap_requests']
              and direct['p95Ms'] <= 200 and direct['p99Ms'] <= 500 and queue_budget)
    check(bool(budget) == result['passed'], 'independent trade budget differs')
    return dict(stage=stage, integrityPassed=True, capacityBudgetPassed=bool(budget),
                direct=direct, reconciliation=reconciliation, resources=resources,
                nominalStableEndToObservedConvergenceMs=result['nominalStableEndToConvergedObservedMs'])


def read_audit(base, stage):
    load, control = base / (stage + '-load'), base / (stage + '-control')
    report = read_json(load / 'result.json')
    api = api_audit(base / 'api.json', '767c2fc8bdf1be08c5636143a71898eec25081a9')
    fixture_path = base / 'fixture/fixture.json'
    fixture = read_json(fixture_path)
    check(report['fixtureManifestSHA256'] == hashlib.sha256(fixture_path.read_bytes()).hexdigest(),
          'fixture manifest bytes/hash differ')
    check(report['apiSourceSha'] == api['sourceSha']
          and report['apiExpectedRevision'] == api['expectedRevision']
          and report['fixtureSourceSha'] == fixture['sourceSha']
          and report['fixtureExpectedRevision'] == fixture['expectedRevision']
          and report['fixture'] == fixture['fixture'], 'API/fixture origin differs')
    for relative, expected in report['scriptSHA256'].items():
        check(hashlib.sha256((ROOT / relative).read_bytes()).hexdigest() == expected,
              'measured script bytes differ: ' + relative)
    result = report['result']
    metrics = read_json(load / 'stable-summary.json')['metrics']
    names = dict(attempted='cap_read_attempted', completedHttpRequests='http_reqs',
                 completed='cap_read_completed', successful='cap_read_success',
                 systemErrors='cap_read_system_errors', contentErrors='cap_read_content_errors',
                 httpErrors='cap_read_http_errors', dropped='dropped_iterations')
    check(all(result[key] == metrics[name]['count'] for key, name in names.items()),
          'native read counts differ')
    for key in ('avg', 'med', 'p(95)', 'p(99)', 'max'):
        check(result['allLatency'][key] == metrics['cap_read_all_ms'][key]
              and result['successfulLatency'][key] == metrics['cap_read_success_ms'][key],
              'native all/success read latency differs')
    attempted, completed, successful = (result[key] for key in ('attempted', 'completed', 'successful'))
    check(attempted > 0 and result['incompleteOrUnclassified'] == attempted - completed,
          'read unknown count differs')
    resources = resource_audit(control, load, api, 'read', report['stableStartedAt'], report['stableEndedAt'])
    budget = (report['stableExit'] == 0 and not report['stableExternalStop']
              and completed == attempted == result['completedHttpRequests']
              and attempted / (report['rate'] * report['seconds']) >= .99
              and successful / attempted >= .99 and result['dropped'] == 0
              and result['systemErrors'] / attempted < .01
              and result['httpErrors'] / attempted < .01 and result['contentErrors'] == 0
              and result['allLatency']['p(95)'] <= 200 and result['allLatency']['p(99)'] <= 500
              and result['successfulLatency']['p(95)'] <= 200
              and result['successfulLatency']['p(99)'] <= 500)
    check(budget == report['meetsBudget'], 'independent read budget differs')
    return dict(stage=stage, integrityPassed=True, capacityBudgetPassed=bool(budget),
                apiSourceSha=api['sourceSha'], fixtureManifestSHA256=report['fixtureManifestSHA256'],
                rate=report['rate'], nominalSeconds=report['seconds'],
                started=attempted, nativeHTTP=result['completedHttpRequests'],
                classified=completed, successful=successful, unknown=attempted-completed,
                systemErrors=result['systemErrors'], httpErrors=result['httpErrors'],
                contentErrors=result['contentErrors'], dropped=result['dropped'],
                p95Ms=result['allLatency']['p(95)'], p99Ms=result['allLatency']['p(99)'],
                resources=resources)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--evidence', type=Path, default=ROOT / 'docs/evidence/2026-10-30-go073-cap/qa-independent-6e6af1e78699')
    parser.add_argument('--read-stage', action='append',
                        help='exact sibling stage basename; default hot200-hold and multi100-hold')
    args = parser.parse_args()
    output = dict(scope='fresh independent archived artifacts; read-only offline crosscheck',
                  trade=[], read=[], pending=[], errors=[])
    for mode, stages, audit in (
        ('trade', ('trade30-hold', 'trade35-neighbor'), trade_audit),
        ('read', args.read_stage or ('hot200-hold', 'multi100-hold'), read_audit),
    ):
        for stage in stages:
            if Path(stage).name != stage:
                raise ValueError('stage must be a basename')
            base = args.evidence / mode
            required = [base / (stage + '-load') / 'result.json',
                        base / (stage + '-control') / 'exit-status.json']
            if not all(path.is_file() for path in required):
                output['pending'].append(mode + '/' + stage)
                continue
            try:
                output[mode].append(audit(base, stage))
            except Exception as error:
                output['errors'].append(dict(stage=mode+'/'+stage,
                                             type=type(error).__name__, message=str(error)))
    output['allSelectedIntegrityChecksPassed'] = not output['pending'] and not output['errors']
    print(json.dumps(output, ensure_ascii=False, indent=2))
    return 0 if output['allSelectedIntegrityChecksPassed'] else 1


if __name__ == '__main__':
    sys.exit(main())
