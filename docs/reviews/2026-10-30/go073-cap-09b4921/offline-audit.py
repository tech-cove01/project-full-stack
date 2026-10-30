"""Read archived CAP evidence only; emit JSON to stdout, never start services."""
import collections
import gzip
import json
import math
from pathlib import Path


ROOT = Path(__file__).resolve().parents[4]
CAP = ROOT / 'docs/evidence/2026-10-30-go073-cap'


def read_json(path):
    return json.loads(path.read_text(encoding='utf-8-sig'))


def read_lines(path):
    with path.open(encoding='utf-8-sig') as stream:
        return [json.loads(line) for line in stream if line.strip()]


def check(value, message):
    if not value:
        raise ValueError(message)


def trade(stage, pool, directory=None):
    directory = directory or CAP / 'cap02' / stage
    result = read_json(directory / 'result.json')
    records = read_lines(directory / 'requests.ndjson')
    requests = [row['request'] for row in records]
    orders = read_lines(directory / 'orders.ndjson')
    accepted = read_lines(directory / 'redis-accepted.ndjson')
    by_id = {row['id']: row for row in orders}
    by_redis = {row['orderId']: row['userId'] for row in accepted}
    ids = [row['orderId'] for row in requests]
    check(len(ids) == len(set(ids)) == len(orders) == len(by_id)
          == len(accepted) == len(by_redis), stage + ': duplicate/omitted ID')
    check(set(ids) == set(by_id) == set(by_redis), stage + ': ID sets differ')
    check(result['poolTag'] == pool['tag'], stage + ': pool origin differs')
    for row in records:
        request = row['request']
        expected_user = pool['userIds'][request['index']]
        check(expected_user == row['expectedUserId']
              == by_id[request['orderId']]['userId']
              == by_redis[request['orderId']], stage + ': user/index mismatch')
        check(by_id[request['orderId']]['status'] == 1,
              stage + ': captured order status is not unpaid')
    counters = collections.Counter()
    exported = []
    archive = directory / 'k6-metrics.ndjson.gz'
    metrics_file = (gzip.open(archive, 'rt', encoding='utf-8-sig') if archive.exists()
                    else (directory / 'k6-metrics.ndjson').open(encoding='utf-8-sig'))
    with metrics_file as stream:
        for line in stream:
            point = json.loads(line)
            if point.get('type') != 'Point':
                continue
            if point['metric'] == 'cap_request_record':
                exported.append(json.loads(point['data']['tags']['record']))
            if point['metric'] in ('http_reqs', 'cap_requests',
                                   'cap_accepted', 'cap_system_errors'):
                counters[point['metric']] += point['data']['value']
    check(len(exported) == len(requests), stage + ': exporter count differs')
    canonical = lambda values: sorted(json.dumps(x, sort_keys=True) for x in values)
    check(canonical(exported) == canonical(requests), stage + ': exporter differs')
    stable = [row for row in requests if row['phase'] == 'stable']
    latency = sorted(row['durationMs'] for row in stable)
    p95, p99 = (latency[math.ceil(len(latency) * p) - 1] for p in (.95, .99))
    check((p95, p99) == (result['analysis']['stable']['allRequestP95Ms'],
                         result['analysis']['stable']['allRequestP99Ms']),
          stage + ': nearest-rank latency differs')
    queue = read_lines(directory / 'queue.ndjson')
    assessment = result['lastThirdQueueAssessment']
    first = next(x for x in queue if x['atMs'] == assessment['firstSampleAtMs'])
    last = next(x for x in queue if x['atMs'] == assessment['lastSampleAtMs'])
    completed = last['durableOrders'] - first['durableOrders']
    admitted = sum(first['sqlCountObservedAtMs'] < x['ended']
                   <= last['sqlCountObservedAtMs'] for x in stable)
    lag = last['lag'] - first['lag']
    check((completed, admitted, lag) ==
          (assessment['dbCompletedBetweenSamples'],
           assessment['acceptedResponsesBetweenSQLObservations'],
           assessment['lagNetGrowth']), stage + ': queue assessment differs')
    dropped = read_json(directory / 'k6-summary.json')['metrics']['dropped_iterations']['count']
    return dict(stage=stage, stableRequests=len(stable), totalIDUserMatches=len(ids),
                p95Ms=p95, p99Ms=p99, dropped=dropped, rawCounters=dict(counters),
                lastThirdDB=completed, lastThirdAccepted=admitted, lagNet=lag,
                sqlObservationIntervalMs=last['sqlCountObservedAtMs'] - first['sqlCountObservedAtMs'])


def read(stage):
    directory = CAP / 'cap03' / (stage + '-load')
    report = read_json(directory / 'result.json')
    result = report['result']
    metrics = read_json(directory / 'stable-summary.json')['metrics']
    mapping = dict(attempted='cap_read_attempted', completedHttpRequests='http_reqs',
                   completed='cap_read_completed', successful='cap_read_success',
                   systemErrors='cap_read_system_errors', contentErrors='cap_read_content_errors',
                   httpErrors='cap_read_http_errors', dropped='dropped_iterations')
    check(all(result[key] == metrics[name]['count'] for key, name in mapping.items()),
          stage + ': raw counter differs')
    for key in ('avg', 'med', 'p(95)', 'p(99)', 'max'):
        check(result['allLatency'][key] == metrics['cap_read_all_ms'][key]
              and result['successfulLatency'][key] == metrics['cap_read_success_ms'][key],
              stage + ': raw exported latency differs')
    check(report['apiSourceSha'] == '767c2fc8bdf1be08c5636143a71898eec25081a9'
          and report['seconds'] == 180 and report['meetsBudget'] is True,
          stage + ': selected build/duration/budget differs')
    resource_path = CAP / 'cap03' / (stage + '-control') / 'resources.ndjson'
    resources = [row for row in read_lines(resource_path) if row.get('kind') == 'sample']
    check(len(resources) == 37 and all(not row['errors'] and row['generators']
                                      for row in resources), stage + ': incomplete samples')
    return dict(stage=stage, successful=result['successful'],
                p95Ms=metrics['cap_read_all_ms']['p(95)'],
                p99Ms=metrics['cap_read_all_ms']['p(99)'],
                activeReaders=report['activeReaders'], activeCountKeys=report['activeCountKeys'],
                completeResourceSamples=len(resources),
                validCPUIntervals=sum(row['cpu_interval_valid'] for row in resources))


if __name__ == '__main__':
    public_pool = read_json(CAP / 'cap01/prepare-25000h/pool-progress.json')
    trades = [trade(stage, public_pool) for stage in
              ('workers4-rate-30-hold-1', 'workers4-rate-30-hold-2', 'workers4-rate-35')]
    reads = [read(stage) for stage in
             ('hold-fix-hot-200-180-a', 'hold-fix-hot-200-180-b',
              'hold-fix-multi-100-180-a', 'hold-fix-multi-100-180-b')]
    print(json.dumps(dict(scope='independent read-only archived evidence crosscheck; no live load',
                          trade=trades, read=reads), ensure_ascii=False, indent=2))
