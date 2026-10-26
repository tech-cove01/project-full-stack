"""T1 evidence: an unknown DB outcome must never be acknowledged on a short-lived Redis hash.

Sequence:
 1. create activity, pause the dev consumer, accept one order (message lands in the Stream)
 2. stop MySQL, resume the consumer -> the create transaction fails past the retry threshold
 3. the review row cannot be written either, so the message must stay pending (no ACK)
 4. start MySQL again -> the pending message completes exactly once

The MySQL container is restarted in `finally`; no data volume is touched.
"""
from datetime import datetime, timedelta, timezone
from urllib.error import HTTPError
from urllib.parse import urlencode
from urllib.request import Request, urlopen
import json
import os
import socket
import subprocess
import sys
import time
import uuid

sys.stdout.reconfigure(encoding='utf-8')
BASE = 'http://127.0.0.1:8081'
OWNER = '13800000000'
MYSQL_CONTAINER = os.environ.get('MYSQL_CONTAINER', 'project-full-stack-mysql-1')


def request(method, path, body=None, token=None, extra=None):
    headers = {'Content-Type': 'application/json'}
    if token:
        headers['authorization'] = token
    headers.update(extra or {})
    data = json.dumps(body).encode() if body is not None else None
    req = Request(BASE + path, data=data, headers=headers, method=method)
    try:
        with urlopen(req, timeout=20) as response:
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


def docker(*args):
    result = subprocess.run(['docker', *args], capture_output=True, text=True, encoding='utf-8')
    if result.returncode != 0:
        raise RuntimeError(result.stderr.strip() or 'docker failed')


def db_scalar(sql):
    result = subprocess.run(
        ['docker', 'exec', MYSQL_CONTAINER, 'mysql', '-ufullstack', '-pfullstack_dev_only',
         'project_full_stack', '-N', '-B', '-e', sql],
        capture_output=True, text=True, encoding='utf-8')
    if result.returncode != 0:
        return None
    return result.stdout.strip()


def wait_created(token, order_id, seconds):
    final = None
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        try:
            final = api('GET', '/voucher-order/' + order_id, token=token)
        except RuntimeError:
            time.sleep(2)
            continue
        if final.get('state') == 'CREATED':
            return final, final
        time.sleep(2)
    return None, final


def mysql_ready():
    result = subprocess.run(
        ['docker', 'exec', MYSQL_CONTAINER, 'mysqladmin', 'ping', '-h', 'localhost', '-uroot',
         '-pfullstack_dev_only', '--silent'],
        capture_output=True, text=True, encoding='utf-8')
    return result.returncode == 0


def main():
    owner = login(OWNER)
    user = login('175' + str(uuid.uuid4().int)[:8])
    now = datetime.now(timezone(timedelta(hours=8)))
    stamp = lambda value: value.isoformat(timespec='seconds')
    body = {
        'shopId': '1', 'title': 'T1-review-' + uuid.uuid4().hex[:6],
        'rules': '验证 DB 不可用超过重试阈值后的归宿。',
        'payValue': '300', 'actualValue': '600', 'totalStock': 2,
        'beginTime': stamp(now - timedelta(minutes=1)),
        'endTime': stamp(now + timedelta(minutes=30)),
        'redeemBeginTime': stamp(now - timedelta(minutes=1)),
        'redeemEndTime': stamp(now + timedelta(days=1)),
    }
    activity = api('POST', '/merchant/activities', body, owner,
                   {'Idempotency-Key': 't1-' + uuid.uuid4().hex})
    activity_id = activity['id']
    deadline = time.monotonic() + 15
    while True:
        page = api('GET', '/merchant/activities?current=1&pageSize=100', token=owner)
        current = next(item for item in page if item['id'] == activity_id)
        if current.get('ready'):
            break
        if time.monotonic() > deadline:
            raise RuntimeError('activity did not become ready')
        time.sleep(0.2)

    api('POST', '/dev/consumer/pause', token=owner)
    # The consumer may still be inside a 2 s blocking read; wait until it has really stopped
    # before accepting, otherwise the message is created before MySQL goes down.
    time.sleep(4)
    order_id = api('POST', '/voucher-order/seckill/' + activity_id, token=user)
    time.sleep(1)

    evidence = {'activityId': activity_id, 'orderId': order_id}
    try:
        with Redis() as redis:
            evidence['beforeStop'] = {'streamPending': redis.command('XPENDING', 'stream.orders', 'g1')[0]}
        docker('stop', MYSQL_CONTAINER)
        api('POST', '/dev/consumer/resume', token=owner)
        # Each connection attempt waits for the Hikari timeout, so passing the 5-attempt
        # threshold can take a few minutes; poll instead of guessing one delay.
        retries_seen = []
        deadline = time.monotonic() + 300
        while time.monotonic() < deadline:
            time.sleep(10)
            with Redis() as redis:
                reservation = redis.command('HGETALL', 'order:reservation:' + order_id)
            pairs = dict(zip(reservation[::2], reservation[1::2]))
            retries_seen.append(pairs.get('retries'))
            if int(pairs.get('retries') or 0) >= 5:
                break
        with Redis() as redis:
            reservation = redis.command('HGETALL', 'order:reservation:' + order_id)
            pending = redis.command('XPENDING', 'stream.orders', 'g1')
            dead = redis.command('XLEN', 'stream.orders.dead')
        pairs = dict(zip(reservation[::2], reservation[1::2]))
        evidence['whileDbDown'] = {
            'retriesSeen': retries_seen,
            'reservationState': pairs.get('state'),
            'retries': pairs.get('retries'),
            'manualReview': pairs.get('manualReview'),
            'streamPending': pending[0] if isinstance(pending, list) else pending,
            'deadLetters': dead,
        }
        still_pending = isinstance(pending, list) and pending[0] > 0
        threshold_reached = int(pairs.get('retries') or 0) >= 5
    finally:
        docker('start', MYSQL_CONTAINER)
    for _ in range(60):
        if mysql_ready():
            break
        time.sleep(2)

    created, final = wait_created(user, order_id, 120)
    review_status = db_scalar(
        f"SELECT status FROM tb_order_review WHERE order_id = '{order_id}'") or 'NONE'
    # Both outcomes are legal: the message created the order directly after recovery, or the
    # failed attempt persisted a review row that the controlled replay now finishes.
    if created is None and review_status in ('OPEN', 'REQUEUED'):
        # MySQL may accept connections a moment before the CLI login works, so retry briefly.
        replay = None
        # The CLI needs the same database endpoint as this process; without an explicit
        # env it would fall back to 3306 and fail against the local MySQL instance.
        child_env = {**os.environ, 'MYSQL_PORT': os.environ.get('MYSQL_PORT', '3306'),
                     'MYSQL_HOST': os.environ.get('MYSQL_HOST', '127.0.0.1')}
        for _ in range(3):
            replay = subprocess.run(
                ['node', 'scripts/review-order.mjs', 'replay', order_id, '--confirm'],
                capture_output=True, text=True, encoding='utf-8', env=child_env)
            if replay.returncode == 0:
                break
            time.sleep(5)
        evidence['replay'] = {'exit': replay.returncode,
                              'output': (replay.stdout or '').strip()[:400],
                              'error': (replay.stderr or '').strip()[:400]}
        if replay.returncode == 0:
            created, final = wait_created(user, order_id, 90)
            review_status = db_scalar(
                f"SELECT status FROM tb_order_review WHERE order_id = '{order_id}'") or review_status
    with Redis() as redis:
        pending_after = redis.command('XPENDING', 'stream.orders', 'g1')
    ok = (threshold_reached and still_pending and created is not None
          and created.get('orderStatus') == 1)
    evidence['afterRecovery'] = {
        'state': final.get('state') if final else None,
        'orderStatus': created.get('orderStatus') if created else None,
        'streamPending': pending_after[0] if isinstance(pending_after, list) else pending_after,
        'reviewStatus': review_status,
    }
    cleanup = []
    for label, call in (
            ('cancel', lambda: api('POST', '/voucher-order/' + order_id + '/cancel', token=user)),
            ('offline', lambda: api('POST', '/merchant/activities/' + activity_id + '/offline', token=owner))):
        try:
            call()
        except Exception as error:
            cleanup.append(f'{label}: {error}')
    if cleanup:
        evidence['cleanupErrors'] = cleanup
    evidence['finding'] = 'FIXED' if ok else 'REVIEW_REQUIRED'
    print(json.dumps(evidence, ensure_ascii=False))


try:
    main()
except Exception as error:
    print(json.dumps({'result': 'FAIL', 'error': str(error)}, ensure_ascii=False))
    sys.exit(1)
