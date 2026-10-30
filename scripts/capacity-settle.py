"""Cancel only already-reconciled CAP fixture orders, outside load windows.

Retains every SQL fact and uses each order's genuine owner session and normal
cancel/outbox path. This prevents earlier unpaid cohorts expiring mid-comparison.
Never run concurrently with a capacity stage. Credentials are never exported.
"""
import argparse
from concurrent.futures import ThreadPoolExecutor
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import socket
import urllib.request


def evidence_rows(path):
    with path.open(encoding='utf-8-sig') as stream:
        for line in stream:
            if line.strip():
                yield json.loads(line)


def canonical_id(value):
    return isinstance(value, str) and re.fullmatch(r'[1-9][0-9]*', value) is not None


def account_stopped_stage(stage, result, fixture, pool, sessions):
    """Recompute a stopped cohort's durable destinations; never waive loss.

    Only the completed-response/fully durable acceptance count may differ.
    These are the original captured SQL/Redis snapshots, not fresh live reads.
    """
    reconciliation = result['analysis']['reconciliation']
    if (result.get('stoppedEarly') is not True or not isinstance(result.get('stopReason'), str) or
            not result['stopReason'] or result.get('stopConfirmed') is not True or
            type(result.get('k6ExitCode')) is not int or result['k6ExitCode'] == 0 or
            result.get('exportedLogsComplete') is not True or
            result['analysis'].get('reconciliationPassed') is not False):
        raise ValueError('accounted-stop requires a completed stopped-stage record')
    for name in ('mismatchedUserOrStatus', 'mappingMismatches', 'duplicateEffectiveOrders',
                 'unresolvedReview', 'durableRejections',
                 'unfinishedOrderOrCompensationOutbox', 'finalPending', 'finalLag'):
        if reconciliation[name] != 0:
            raise ValueError('accounted-stop has another failed reconciliation invariant')

    sql_orders, sql_users = {}, set()
    for row in evidence_rows(stage / 'orders.ndjson'):
        order_id, user_id = row['id'], row['userId']
        if (not canonical_id(order_id) or int(order_id) > 9223372036854775807 or
                user_id not in sessions or type(row['status']) is not int or row['status'] != 1 or
                order_id in sql_orders or user_id in sql_users):
            raise ValueError('accounted-stop SQL ownership, state or uniqueness mismatch')
        sql_orders[order_id] = user_id
        sql_users.add(user_id)
    redis_orders, redis_users = {}, set()
    for row in evidence_rows(stage / 'redis-accepted.ndjson'):
        order_id, user_id = row['orderId'], row['userId']
        if order_id in redis_orders or user_id in redis_users or user_id not in sessions:
            raise ValueError('accounted-stop Redis ownership or uniqueness mismatch')
        redis_orders[order_id] = user_id
        redis_users.add(user_id)
    if sql_orders != redis_orders:
        raise ValueError('accounted-stop SQL and Redis ID/user sets differ')

    logged, indexes, completed = {}, set(), 0
    for row in evidence_rows(stage / 'requests.ndjson'):
        completed += 1
        request = row['request']
        index = request['index']
        if (type(index) is not int or index < 0 or index >= len(pool['userIds']) or
                index in indexes or pool['userIds'][index] != row['expectedUserId'] or
                row['expectedUserId'] not in sessions):
            raise ValueError('accounted-stop response index/owner mismatch')
        indexes.add(index)
        if request['category'] == 'accepted':
            order_id = request['orderId']
            if (request['status'] != 200 or order_id in logged or
                    sql_orders.get(order_id) != row['expectedUserId']):
                raise ValueError('accounted-stop completed acceptance lacks matching durable identity')
            logged[order_id] = row['expectedUserId']
    uncompleted = len(set(sql_orders) - set(logged))
    count = len(sql_orders)
    initial_stock = fixture['stock']
    stock = reconciliation['mysqlStock']
    if (uncompleted < 1 or
            reconciliation['unloggedDurableOrders'] != uncompleted or
            reconciliation['durableRows'] != count or reconciliation['redisAccepted'] != count or
            reconciliation['uniqueAcceptedRecords'] != len(logged) or
            result['completedRequestRecords'] != completed or
            result['k6RequestCount'] != completed or result['k6RecordCount'] != completed or
            result['model']['initialStock'] != initial_stock or
            stock != reconciliation['redisStock'] or not 0 <= stock <= initial_stock or
            initial_stock - stock != count):
        raise ValueError('accounted-stop count, response gap or stock invariant mismatch')
    return {'activityId': result['activityId'], 'durableOrders': count,
            'completedResponseRecords': completed, 'uncompletedResponses': uncompleted,
            'responseGapMeaning': 'durable acceptances absent from completed accepted-response records',
            'unknownDurableDestinations': 0,
            'evidenceSHA256': {name: hashlib.sha256((stage / name).read_bytes()).hexdigest()
                               for name in ('orders.ndjson', 'redis-accepted.ndjson', 'requests.ndjson')}}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--stage', type=Path, action='append', required=True)
    parser.add_argument('--accounted-stop', type=Path, action='append', default=[],
                        help='explicit --stage whose only mismatch is fully accounted unlogged durable rows')
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    repo = Path(__file__).resolve().parent.parent
    base = repo / 'docs/evidence/2026-10-30-go073-cap'
    stages = list(dict.fromkeys(path.resolve(strict=True) for path in args.stage))
    accounted_stops = {path.resolve(strict=True) for path in args.accounted_stop}
    if not accounted_stops.issubset(stages):
        raise ValueError('every accounted-stop must also name exactly one selected stage path')
    pool_path = Path(os.environ['CAP_POOL_FILE']).resolve(strict=True)
    if pool_path.is_relative_to(repo) or args.output.exists():
        raise ValueError('private pool must be outside Git and output must be new')
    spec = importlib.util.spec_from_file_location('cap_launcher', repo / 'scripts/capacity-trade-launch.py')
    launcher = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(launcher)
    live = launcher.preflight()  # Fresh owned labels/IDs/loopback binding guard.
    with socket.socket() as control:
        control.settimeout(1)
        if control.connect_ex(('127.0.0.1', 8260)) == 0:
            raise ValueError('trade generator control listener exists; refuse fixture mutation')
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    with opener.open('http://127.0.0.1:8250/health/ready', timeout=3) as response:
        health = json.load(response)
    if health.get('status') != 'UP' or health.get('version') != os.environ['CAP_API_VERSION']:
        raise ValueError('dedicated API identity mismatch')
    pool = json.loads(pool_path.read_text())
    sessions = dict(zip(pool['userIds'], pool['tokens'], strict=True))
    if len(sessions) != len(pool['userIds']) or not all(sessions.values()):
        raise ValueError('private pool owner sessions must be unique and nonempty')
    orders, accounted = {}, []
    for stage in stages:
        if not stage.is_relative_to(base):
            raise ValueError('only this CAP evidence root is permitted')
        result = json.loads((stage / 'result.json').read_text())
        if result.get('poolTag') != pool['tag']:
            raise ValueError('stage belongs to another private pool')
        if stage not in accounted_stops and result['analysis']['reconciliationPassed'] is not True:
            raise ValueError('only this pool and exactly reconciled stages may settle')
        fixture = json.loads((stage / 'fixture.json').read_text())
        original = json.loads((stage / 'external-container-preflight.json').read_text())
        if (fixture['activityId'] != result['activityId'] or
                {x['id'] for x in original['containers']} != {x['id'] for x in live['containers']}):
            raise ValueError('stage fixture or immutable dependency identity mismatch')
        if stage in accounted_stops:
            entry = account_stopped_stage(stage, result, fixture, pool, sessions)
            accounted.append({'stage': str(stage.relative_to(base)), **entry})
        for row in evidence_rows(stage / 'orders.ndjson'):
            if row['userId'] not in sessions or not str(row['id']).isdigit():
                raise ValueError('order owner not present in private fixture pool')
            if row['id'] in orders and orders[row['id']] != row['userId']:
                raise ValueError('selected stages disagree on an order owner')
            orders[row['id']] = row['userId']
    args.output.mkdir()

    def cancel(item):
        order_id, user_id = item
        try:
            request = urllib.request.Request(
                f'http://127.0.0.1:8250/voucher-order/{order_id}/cancel?reason=CAP_WINDOW_COMPLETE',
                headers={'authorization': sessions[user_id]}, data=b'', method='POST')
            with opener.open(request, timeout=8) as response:
                body = json.load(response)
            ok = body.get('success') is True and body.get('data', {}).get('id') == order_id and body['data'].get('orderStatus') == 4
            return {'orderId': order_id, 'cancelled': ok}
        except Exception as error:
            return {'orderId': order_id, 'cancelled': False, 'errorClass': type(error).__name__}

    completed = succeeded = 0
    with (args.output / 'orders.ndjson').open('x') as output, ThreadPoolExecutor(max_workers=8) as executor:
        for item in executor.map(cancel, orders.items()):
            output.write(json.dumps(item) + '\n')
            output.flush()
            completed += 1
            succeeded += item['cancelled']
    summary = {'ownedOrders': len(orders), 'completedReceipts': completed, 'cancelledOrAlreadyCancelled': succeeded,
               'accountedStoppedStages': accounted,
               'accountedUncompletedResponses': sum(x['uncompletedResponses'] for x in accounted),
               'factsRetained': True, 'normalAuthAndLifecycle': True, 'noTradeControlListenerObserved': True,
               'operatorExclusiveOutsideWindowRequired': True,
               'compensationDrainStillRequired': True}
    (args.output / 'summary.json').write_text(json.dumps(summary, indent=2))
    print(json.dumps(summary))
    return 0 if succeeded == len(orders) else 1


if __name__ == '__main__':
    try:
        raise SystemExit(main())
    except Exception as error:
        print('capacity-settle: ' + type(error).__name__ + '; no credential diagnostics')
        raise SystemExit(1)
