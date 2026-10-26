"""T3 follow-up: reconcile must close admission first and refuse while work is unresolved.

A synthetic OPEN row in tb_order_review stands in for an order whose outcome is still unknown.
The first reconcile call must return 409, leave the gate CLOSED and keep the version unchanged;
after the row is removed the second call rebuilds the stock and lets the outbox reopen the gate.
"""
from datetime import datetime, timedelta, timezone
from urllib.error import HTTPError
from urllib.parse import urlencode
from urllib.request import Request, urlopen
import json
import os
import subprocess
import sys
import time
import uuid

sys.stdout.reconfigure(encoding='utf-8')
BASE = 'http://127.0.0.1:8081'
OWNER = '13800000000'
MYSQL = os.environ.get('MYSQL_BIN', 'mysql')


def db(sql):
    env = {**os.environ, 'MYSQL_PWD': os.environ.get('MYSQL_PASSWORD', 'fullstack_dev_only')}
    result = subprocess.run(
        [MYSQL, '--protocol=TCP', '--host=' + os.environ.get('MYSQL_HOST', '127.0.0.1'),
         '--port=' + os.environ.get('MYSQL_PORT', '3306'),
         '--user=' + os.environ.get('MYSQL_USER', 'fullstack'), '--default-character-set=utf8mb4',
         '--batch', '--skip-column-names', os.environ.get('MYSQL_DATABASE', 'project_full_stack'),
         '-e', sql], env=env, capture_output=True, text=True, encoding='utf-8')
    if result.returncode != 0:
        raise RuntimeError(result.stderr.strip() or 'mysql failed')
    return result.stdout.strip()


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


def main():
    token = login(OWNER)
    now = datetime.now(timezone(timedelta(hours=8)))
    stamp = lambda value: value.isoformat(timespec='seconds')
    body = {
        'shopId': '1', 'title': 'T3-blocked-' + uuid.uuid4().hex[:6],
        'rules': '验证重建前的阻断检查。',
        'payValue': '400', 'actualValue': '800', 'totalStock': 2,
        'beginTime': stamp(now - timedelta(minutes=1)),
        'endTime': stamp(now + timedelta(minutes=30)),
        'redeemBeginTime': stamp(now - timedelta(minutes=1)),
        'redeemEndTime': stamp(now + timedelta(days=1)),
    }
    activity = api('POST', '/merchant/activities', body, token,
                   {'Idempotency-Key': 't3b-' + uuid.uuid4().hex})
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
    version_before = db(f'SELECT publish_version FROM tb_seckill_voucher WHERE voucher_id = {activity_id}')

    synthetic = 'block-' + uuid.uuid4().hex[:12]
    evidence = {'activityId': activity_id, 'versionBefore': version_before}
    try:
        db(f"""INSERT INTO tb_order_review (order_id, voucher_id, user_id, attempts, reason, status)
               VALUES ('{synthetic}', {activity_id}, 1, 5, 'SYNTHETIC_BLOCKER', 'OPEN')""")
        status, payload = request('POST', f'/merchant/activities/{activity_id}/reconcile?confirm=true', token=token)
        view = api('GET', '/merchant/activities?current=1&pageSize=100', token=token)
        row = next(item for item in view if item['id'] == activity_id)
        version_blocked = db(f'SELECT publish_version FROM tb_seckill_voucher WHERE voucher_id = {activity_id}')
        evidence['blocked'] = {'http': status, 'error': payload.get('errorMsg'),
                               'ready': row.get('ready'),
                               'versionUnchanged': version_blocked == version_before}
    finally:
        db(f"DELETE FROM tb_order_review WHERE order_id = '{synthetic}'")

    reconciled = api('POST', f'/merchant/activities/{activity_id}/reconcile?confirm=true', token=token)
    deadline = time.monotonic() + 10
    ready = False
    while time.monotonic() < deadline:
        page = api('GET', '/merchant/activities?current=1&pageSize=100', token=token)
        row = next(item for item in page if item['id'] == activity_id)
        if row.get('ready'):
            ready = True
            break
        time.sleep(0.5)
    api('POST', f'/merchant/activities/{activity_id}/offline', token=token)
    evidence['afterRemoval'] = {'status': reconciled.get('publishStatus'), 'ready': ready,
                                'version': db(
                                    f'SELECT publish_version FROM tb_seckill_voucher WHERE voucher_id = {activity_id}')}
    ok = (evidence['blocked']['http'] == 409 and evidence['blocked']['versionUnchanged']
          and evidence['blocked']['ready'] is False and ready)
    evidence['finding'] = 'FIXED' if ok else 'REVIEW_REQUIRED'
    print(json.dumps(evidence, ensure_ascii=False))


try:
    main()
except Exception as error:
    print(json.dumps({'result': 'FAIL', 'error': str(error)}, ensure_ascii=False))
    sys.exit(1)
