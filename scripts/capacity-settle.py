"""Cancel only already-reconciled CAP fixture orders, outside load windows.

Retains every SQL fact and uses each order's genuine owner session and normal
cancel/outbox path. This prevents earlier unpaid cohorts expiring mid-comparison.
Never run concurrently with a capacity stage. Credentials are never exported.
"""
import argparse
from concurrent.futures import ThreadPoolExecutor
import importlib.util
import json
import os
from pathlib import Path
import socket
import urllib.request


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--stage', type=Path, action='append', required=True)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    repo = Path(__file__).resolve().parent.parent
    base = repo / 'docs/evidence/2026-10-30-go073-cap'
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
    orders = {}
    for raw in args.stage:
        stage = raw.resolve(strict=True)
        if not stage.is_relative_to(base):
            raise ValueError('only this CAP evidence root is permitted')
        result = json.loads((stage / 'result.json').read_text())
        if result.get('poolTag') != pool['tag'] or result['analysis']['reconciliationPassed'] is not True:
            raise ValueError('only this pool and exactly reconciled stages may settle')
        fixture = json.loads((stage / 'fixture.json').read_text())
        original = json.loads((stage / 'external-container-preflight.json').read_text())
        if (fixture['activityId'] != result['activityId'] or
                {x['id'] for x in original['containers']} != {x['id'] for x in live['containers']}):
            raise ValueError('stage fixture or immutable dependency identity mismatch')
        for line in (stage / 'orders.ndjson').read_text().splitlines():
            row = json.loads(line)
            if row['userId'] not in sessions or not str(row['id']).isdigit():
                raise ValueError('order owner not present in private fixture pool')
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
