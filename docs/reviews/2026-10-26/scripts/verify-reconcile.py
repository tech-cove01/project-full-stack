"""T3 evidence: an ACTIVE activity that lost its Redis keys must fail closed.

Run in two steps because the guard runs on application startup:

    python docs/reviews/2026-10-26/scripts/verify-reconcile.py prepare
    # restart the backend (new DevDataInitializer guard)
    python docs/reviews/2026-10-26/scripts/verify-reconcile.py check <activityId>

`prepare` creates a dedicated activity, waits until it is ready, then deletes its stock and
gate keys. `check` proves admission stayed closed, runs the explicit reconciliation endpoint,
and finally cancels the demo order and takes the activity offline.
"""
from datetime import datetime, timedelta, timezone
from urllib.error import HTTPError
from urllib.parse import urlencode
from urllib.request import Request, urlopen
import json
import socket
import sys
import time
import uuid

sys.stdout.reconfigure(encoding='utf-8')
BASE = 'http://127.0.0.1:8081'
OWNER = '13800000000'


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
        raise RuntimeError('dev profile required')
    return api('POST', '/user/login', {'phone': phone, 'code': code['code']})


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
            raise RuntimeError(value.decode())
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
        raise RuntimeError('bad RESP reply')


def prepare():
    token = login(OWNER)
    now = datetime.now(timezone(timedelta(hours=8)))
    stamp = lambda value: value.isoformat(timespec='seconds')
    body = {
        'shopId': '1', 'title': 'T3-reconcile-' + uuid.uuid4().hex[:6],
        'rules': '验证 Redis 缺键时保持关闭准入。',
        'payValue': '500', 'actualValue': '1000', 'totalStock': 2,
        'beginTime': stamp(now - timedelta(minutes=1)),
        'endTime': stamp(now + timedelta(minutes=30)),
        'redeemBeginTime': stamp(now - timedelta(minutes=1)),
        'redeemEndTime': stamp(now + timedelta(days=1)),
    }
    activity = api('POST', '/merchant/activities', body, token,
                   {'Idempotency-Key': 't3-' + uuid.uuid4().hex})
    activity_id = activity['id']
    deadline = time.monotonic() + 15
    while True:
        page = api('GET', '/merchant/activities?current=1&pageSize=100', token=token)
        current = next(item for item in page if item['id'] == activity_id)
        if current.get('ready'):
            break
        if time.monotonic() > deadline:
            raise RuntimeError('activity did not become ready')
        time.sleep(0.2)
    with Redis() as redis:
        for prefix in ('seckill:stock:', 'seckill:gate:', 'seckill:begin:', 'seckill:end:'):
            redis.command('DEL', prefix + activity_id)
    print(json.dumps({'activityId': activity_id, 'redisKeysDeleted': True,
                      'next': 'restart the backend, then run check ' + activity_id}, ensure_ascii=False))


def check(activity_id):
    token = login(OWNER)
    user = login('177' + str(uuid.uuid4().int)[:8])
    with Redis() as redis:
        gate = redis.command('HGETALL', 'seckill:gate:' + activity_id)
        marker = redis.command('HGETALL', 'seckill:reconcile:' + activity_id)
        stock_missing = redis.command('EXISTS', 'seckill:stock:' + activity_id) == 0

    status, blocked = request('POST', '/voucher-order/seckill/' + activity_id, token=user)
    closed_to_users = status >= 400 or not blocked.get('success')

    reconciled = api('POST', '/merchant/activities/' + activity_id + '/reconcile?confirm=true', token=token)
    deadline = time.monotonic() + 10
    opened, state, stock = False, None, None
    while time.monotonic() < deadline:
        with Redis() as redis:
            state = redis.command('HGET', 'seckill:gate:' + activity_id, 'state')
            stock = redis.command('GET', 'seckill:stock:' + activity_id)
        if state == 'OPEN' and stock is not None:
            opened = True
            break
        time.sleep(0.5)

    accepted = api('POST', '/voucher-order/seckill/' + activity_id, token=user)
    deadline = time.monotonic() + 15
    created = None
    while time.monotonic() < deadline:
        detail = api('GET', '/voucher-order/' + accepted, token=user)
        if detail.get('state') == 'CREATED':
            created = detail
            break
        time.sleep(0.2)

    api('POST', '/voucher-order/' + accepted + '/cancel', token=user)
    api('POST', '/merchant/activities/' + activity_id + '/offline', token=token)
    print(json.dumps({
        'activityId': activity_id,
        'startupGate': gate,
        'reconcileMarker': marker,
        'stockKeyMissing': stock_missing,
        'admissionClosedBeforeReconcile': closed_to_users,
        'reconciledStatus': reconciled.get('publishStatus'),
        'gateAfterReconcile': {'state': state, 'stock': stock},
        'orderAfterReconcile': created,
        'finding': 'FIXED' if (setup_ok(gate) and stock_missing and closed_to_users and opened and created) else 'REVIEW_REQUIRED',
    }, ensure_ascii=False))


def setup_ok(gate):
    pairs = dict(zip(gate[::2], gate[1::2])) if gate else {}
    return pairs.get('state') == 'CLOSED'


command = sys.argv[1] if len(sys.argv) > 1 else ''
try:
    if command == 'prepare':
        prepare()
    elif command == 'check' and len(sys.argv) > 2:
        check(sys.argv[2])
    else:
        print('usage: verify-reconcile.py prepare | check <activityId>')
        sys.exit(1)
except Exception as error:
    print(json.dumps({'result': 'FAIL', 'error': str(error)}, ensure_ascii=False))
    sys.exit(1)
