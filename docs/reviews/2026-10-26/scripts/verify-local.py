"""Independent local review: creates a labelled activity and three finalised orders.

Uses dev login only, never calls an AI provider. Redis fault keys are unique and
deleted in finally. The activity is taken offline; audit/order rows are retained.
"""
from datetime import datetime, timedelta, timezone
from pathlib import Path
from urllib.error import HTTPError
from urllib.parse import urlencode
from urllib.request import Request, urlopen
import json
import secrets
import socket
import sys
import time
import uuid

sys.stdout.reconfigure(encoding='utf-8')
ROOT = Path(__file__).resolve().parents[4]
BASE = 'http://127.0.0.1:8081'
results = {'date': '2026-10-26', 'scope': 'local existing service; no AI calls'}


def request(method, path, body=None, token=None, extra=None):
    headers = {'Content-Type': 'application/json'}
    if token:
        headers['authorization'] = token
    headers.update(extra or {})
    data = json.dumps(body).encode() if body is not None else None
    req = Request(BASE + path, data=data, headers=headers, method=method)
    try:
        with urlopen(req, timeout=15) as response:
            return response.status, json.load(response)
    except HTTPError as error:
        return error.code, json.loads(error.read().decode())


def api(method, path, body=None, token=None, extra=None):
    status, data = request(method, path, body, token, extra)
    if status >= 400 or not data.get('success'):
        raise RuntimeError(f'{method} {path}: HTTP {status}: {data.get("errorMsg")}')
    return data.get('data')


def login(phone):
    # dev has a 60 s per-phone send interval; wait it out instead of failing a rerun.
    for attempt in range(3):
        try:
            code = api('POST', '/user/code?' + urlencode({'phone': phone}))
            break
        except RuntimeError as error:
            if '频繁' in str(error) and attempt < 2:
                time.sleep(61)
                continue
            raise
    if not isinstance(code, dict) or code.get('mode') != 'development':
        raise RuntimeError('Review requires the localhost dev profile.')
    return api('POST', '/user/login', {'phone': phone, 'code': code['code']})


def wait_created(order_id, token):
    deadline = time.monotonic() + 15
    while time.monotonic() < deadline:
        state = api('GET', '/voucher-order/' + order_id, token=token)
        if state.get('state') == 'CREATED':
            return state
        if state.get('state') == 'FAILED':
            raise AssertionError('Order rejected after acceptance.')
        time.sleep(0.2)
    raise AssertionError('Order did not reach CREATED within 15 seconds.')


class RedisError(Exception):
    pass


class Redis:
    def __enter__(self):
        self.sock = socket.create_connection(('127.0.0.1', 6379), timeout=5)
        self.reader = self.sock.makefile('rb')
        return self

    def __exit__(self, *_):
        self.reader.close()
        self.sock.close()

    def command(self, *args):
        parts = [str(arg).encode() for arg in args]
        wire = b'*' + str(len(parts)).encode() + b'\r\n'
        for part in parts:
            wire += b'$' + str(len(part)).encode() + b'\r\n' + part + b'\r\n'
        self.sock.sendall(wire)
        return self.read()

    def read(self):
        line = self.reader.readline()
        prefix, value = line[:1], line[1:-2]
        if prefix == b'-':
            raise RedisError(value.decode())
        if prefix == b'+':
            return value.decode()
        if prefix == b':':
            return int(value)
        if prefix == b'$':
            length = int(value)
            if length == -1:
                return None
            data = self.reader.read(length)
            self.reader.read(2)
            return data.decode()
        if prefix == b'*':
            return [self.read() for _ in range(int(value))]
        raise RuntimeError('Invalid Redis protocol response.')


def lua_finding(scenario, first, retry, done, stock, membership):
    """FIXED means: invalid input changed nothing, and a legal retry still works exactly once."""
    if isinstance(first, dict):
        return 'BUG_REPRODUCED' if retry == 0 else 'REVIEW_REQUIRED'
    if scenario in ('non_integer_stock', 'wrong_purchase_type'):
        ok = first == -2 and retry == -2 and done is None and membership == 'fixture-order'
        return 'FIXED' if ok else 'REVIEW_REQUIRED'
    if scenario == 'over_total':
        ok = first == -3 and retry == -3 and done is None and stock == '5'
        return 'FIXED' if ok else 'REVIEW_REQUIRED'
    if scenario == 'normal_restore':
        ok = first == 1 and retry == 0 and done == '1' and stock == '6'
        return 'FIXED' if ok else 'REVIEW_REQUIRED'
    return 'REVIEW_REQUIRED'


def lua_fault_repro():
    script = (ROOT / 'backend/src/main/resources/order-restore.lua').read_text(encoding='utf-8')
    output = []
    with Redis() as redis:
        for scenario in ('non_integer_stock', 'wrong_purchase_type', 'normal_restore', 'over_total'):
            prefix = 'review:20261026:' + uuid.uuid4().hex + ':'
            keys = [prefix + name for name in ('done', 'stock', 'purchased', 'accepted')]
            try:
                if scenario == 'non_integer_stock':
                    redis.command('SET', keys[1], 'not-an-integer')
                    redis.command('SADD', keys[2], 'fixture-user')
                elif scenario == 'wrong_purchase_type':
                    redis.command('SET', keys[1], '5')
                    redis.command('SET', keys[2], 'wrong-type-fixture')
                else:
                    redis.command('SET', keys[1], '5')
                    redis.command('SADD', keys[2], 'fixture-user')
                redis.command('HSET', keys[3], 'fixture-user', 'fixture-order')
                max_stock = '5' if scenario == 'over_total' else '100'
                try:
                    first = redis.command('EVAL', script, 4, *keys,
                                          'fixture-user', 'fixture-order', '1', max_stock)
                except RedisError as error:
                    first = {'redis_error': str(error)}
                retry = redis.command('EVAL', script, 4, *keys,
                                      'fixture-user', 'fixture-order', '1', max_stock)
                done = redis.command('GET', keys[0])
                stock = redis.command('GET', keys[1])
                membership = redis.command('HGET', keys[3], 'fixture-user')
                try:
                    purchase_marker = redis.command('SISMEMBER', keys[2], 'fixture-user')
                except RedisError:
                    purchase_marker = 'wrong-type-fixture'
                output.append({
                    'scenario': scenario,
                    'first': first,
                    'retry': retry,
                    'done_marker': done,
                    'stock_after': stock,
                    'purchase_marker': purchase_marker,
                    'accepted_mapping_after': membership,
                    'finding': lua_finding(scenario, first, retry, done, stock, membership),
                })
            finally:
                redis.command('DEL', *keys)
    return output


def trade_regression():
    owner, staff = login('13800000000'), login('13900000000')
    users = [login('178' + f'{secrets.randbelow(100_000_000):08d}') for _ in range(2)]
    cap = api('GET', '/ai/capabilities', token=users[0])
    results['ai_readonly_capabilities'] = {key: cap.get(key) for key in ('enabled', 'ready', 'mode')}
    now = datetime.now(timezone(timedelta(hours=8)))
    stamp = lambda value: value.isoformat(timespec='seconds')
    body = {
        'shopId': '1', 'title': '核验-20261026-' + uuid.uuid4().hex[:6],
        'rules': '独立回归测试活动；完成后下架。',
        'payValue': '1000', 'actualValue': '2000', 'totalStock': 3,
        'beginTime': stamp(now - timedelta(minutes=1)),
        'endTime': stamp(now + timedelta(minutes=20)),
        'redeemBeginTime': stamp(now - timedelta(minutes=1)),
        'redeemEndTime': stamp(now + timedelta(days=2)),
    }
    key = 'review-' + uuid.uuid4().hex
    activity = api('POST', '/merchant/activities', body, owner, {'Idempotency-Key': key})
    activity_id = activity['id']
    output = {'activity_id': activity_id, 'initial_stock': 3, 'checks': {}}
    try:
        repeated = api('POST', '/merchant/activities', body, owner, {'Idempotency-Key': key})
        assert repeated['id'] == activity_id
        output['checks']['publication_idempotent'] = True
        status, _ = request('POST', '/merchant/activities', body, staff,
                            {'Idempotency-Key': 'review-staff-' + uuid.uuid4().hex})
        assert status == 403, status
        output['checks']['staff_publish_denied'] = True

        ready_deadline = time.monotonic() + 15
        while True:
            activities = api('GET', '/merchant/activities?current=1&pageSize=100', token=owner)
            ready_activity = next(item for item in activities if item['id'] == activity_id)
            if ready_activity.get('ready'):
                break
            if time.monotonic() >= ready_deadline:
                raise AssertionError('Publication did not become ready within 15 seconds.')
            time.sleep(0.2)
        output['checks']['publication_ready'] = True

        first = api('POST', '/voucher-order/seckill/' + activity_id, token=users[0])
        assert isinstance(first, str)
        assert wait_created(first, users[0])['orderStatus'] == 1
        status, other = request('GET', '/voucher-order/' + first, token=users[1])
        assert status in (403, 404) or other.get('success') is False
        output['checks']['other_user_order_denied'] = True
        assert api('POST', '/voucher-order/' + first + '/cancel', token=users[0])['orderStatus'] == 4
        time.sleep(0.4)
        second = api('POST', '/voucher-order/seckill/' + activity_id, token=users[0])
        assert second != first
        wait_created(second, users[0])
        paid = api('POST', '/voucher-order/' + second + '/pay', token=users[0])
        assert paid['orderStatus'] == 2
        token = paid['redeemToken']
        redeem_body = {'shopId': '1', 'token': token}
        redeemed = api('POST', '/merchant/orders/redeem', redeem_body, staff)
        again = api('POST', '/merchant/orders/redeem', redeem_body, staff)
        assert redeemed['order']['orderStatus'] == 3 and again['alreadyRedeemed'] is True
        output['checks']['cancel_rebuy_pay_redeem_idempotent'] = True

        third = api('POST', '/voucher-order/seckill/' + activity_id, token=users[1])
        wait_created(third, users[1])
        api('POST', '/voucher-order/' + third + '/pay', token=users[1])
        for _ in range(2):
            refunded = api('POST', '/merchant/orders/' + third + '/refund', {'reason': 'REVIEW'}, owner)
            assert refunded['orderStatus'] == 6
        retry_status, retry = request('POST', '/voucher-order/seckill/' + activity_id, token=users[1])
        assert retry_status >= 400 or not retry.get('success') or retry.get('data') == third
        output['checks']['refund_idempotent_no_rebuy'] = True
        time.sleep(0.4)
        with Redis() as redis:
            redis_stock = int(redis.command('GET', 'seckill:stock:' + activity_id))
        page = api('GET', '/merchant/activities?current=1&pageSize=100', token=owner)
        current = next(item for item in page if item['id'] == activity_id)
        assert redis_stock == current['stock'] == 2, (redis_stock, current['stock'])
        output.update({'orders': [first, second, third], 'final_db_stock': current['stock'],
                       'final_redis_stock': redis_stock, 'result': 'PASS'})
        return output
    finally:
        api('POST', '/merchant/activities/' + activity_id + '/offline', token=owner)


for name, check in (('trade', trade_regression), ('lua_faults', lua_fault_repro)):
    try:
        results[name] = check()
    except Exception as error:
        results[name] = {'result': 'FAIL', 'error': str(error)}

output = Path(__file__).resolve().parents[1] / 'runtime-check.json'
output.write_text(json.dumps(results, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
print(json.dumps(results, ensure_ascii=False, indent=2))
