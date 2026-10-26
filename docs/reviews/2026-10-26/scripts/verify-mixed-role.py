"""T4 evidence: audit scope must follow management scope only.

Creates a synthetic account that is STAFF (scope shop 1) of merchant 1 and OWNER of merchant 2.
Before the fix audits() used every visible shop, so shop 1 audits leaked to a manager of a
different merchant. After the fix the audit list must not contain shop 1, while ordinary order
queries still work for both the STAFF scope and the OWNER merchant.

The membership rows are deleted in `finally`; no other member data is touched.
"""
from pathlib import Path
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
MYSQL = os.environ.get('MYSQL_BIN', 'mysql')
DB_HOST = os.environ.get('MYSQL_HOST', '127.0.0.1')
DB_PORT = os.environ.get('MYSQL_PORT', '3306')
DB_NAME = os.environ.get('MYSQL_DATABASE', 'project_full_stack')
DB_USER = os.environ.get('MYSQL_USER', 'fullstack')
DB_PASSWORD = os.environ.get('MYSQL_PASSWORD', 'fullstack_dev_only')


def db(sql):
    env = {**os.environ, 'MYSQL_PWD': DB_PASSWORD}
    args = [MYSQL, '--protocol=TCP', '--host=' + DB_HOST, '--port=' + DB_PORT, '--user=' + DB_USER,
            '--default-character-set=utf8mb4', '--batch', '--skip-column-names', DB_NAME, '-e', sql]
    result = subprocess.run(args, env=env, capture_output=True, text=True, encoding='utf-8')
    if result.returncode != 0:
        raise RuntimeError(result.stderr.strip() or 'mysql failed')
    return result.stdout.strip()


def request(method, path, body=None, token=None):
    headers = {'Content-Type': 'application/json'}
    if token:
        headers['authorization'] = token
    data = json.dumps(body).encode() if body is not None else None
    req = Request(BASE + path, data=data, headers=headers, method=method)
    try:
        with urlopen(req, timeout=15) as response:
            return response.status, json.load(response)
    except HTTPError as error:
        return error.code, json.loads(error.read().decode())


def api(method, path, body=None, token=None):
    status, data = request(method, path, body, token)
    if status >= 400 or not data.get('success'):
        raise RuntimeError(f'{method} {path}: HTTP {status}: {data.get("errorMsg")}')
    return data


def login(phone):
    for attempt in range(3):
        try:
            status, code = request('POST', '/user/code?' + urlencode({'phone': phone}))
            if status >= 400 or not code.get('success'):
                raise RuntimeError(str(code.get('errorMsg')))
            code = code.get('data')
            break
        except RuntimeError as error:
            if '频繁' in str(error) and attempt < 2:
                time.sleep(61)
                continue
            raise
    return api('POST', '/user/login', {'phone': phone, 'code': code['code']}).get('data')


def main():
    token = login('176' + str(uuid.uuid4().int)[:8])
    user_id = api('GET', '/user/me', token=token).get('data', {}).get('id') or \
        api('GET', '/user/me', token=token)['data']['id']
    member_ids = []
    try:
        db(f"INSERT INTO tb_merchant_member(merchant_id, user_id, role_code, status) "
           f"VALUES (1, {user_id}, 'STAFF', 'ACTIVE')")
        staff_member = db(f"SELECT id FROM tb_merchant_member "
                          f"WHERE user_id = {user_id} AND merchant_id = 1 AND role_code = 'STAFF'")
        member_ids.append(staff_member)
        db(f"INSERT INTO tb_member_shop_scope(member_id, shop_id) VALUES ({staff_member}, 1)")
        db(f"INSERT INTO tb_merchant_member(merchant_id, user_id, role_code, status) "
           f"VALUES (2, {user_id}, 'OWNER', 'ACTIVE')")
        owner_member = db(f"SELECT id FROM tb_merchant_member "
                          f"WHERE user_id = {user_id} AND merchant_id = 2 AND role_code = 'OWNER'")
        member_ids.append(owner_member)

        audits = api('GET', '/merchant/audits?current=1&pageSize=50', token=token)
        audit_shop_ids = sorted({item['shopId'] for item in audits.get('data') or []})
        audit_total = audits.get('total')

        staff_scope_orders = api('GET', '/merchant/orders?current=1&pageSize=5&shopId=1', token=token)
        owner_orders = api('GET', '/merchant/orders?current=1&pageSize=5&shopId=3', token=token)

        leaked = '1' in audit_shop_ids or '2' in audit_shop_ids
        ok = (not leaked) and all(shop in ('3', None) for shop in audit_shop_ids)
        print(json.dumps({
            'userId': user_id,
            'roles': {'merchant1': 'STAFF@shop1', 'merchant2': 'OWNER@shop3'},
            'auditShopIds': audit_shop_ids,
            'auditTotal': audit_total,
            'staffScopeOrderQuery': staff_scope_orders.get('total'),
            'ownerOrderQuery': owner_orders.get('total'),
            'finding': 'FIXED' if ok else 'BUG_REPRODUCED',
        }, ensure_ascii=False))
    finally:
        for member_id in member_ids:
            db(f"DELETE FROM tb_member_shop_scope WHERE member_id = {member_id}")
        db(f"DELETE FROM tb_merchant_member WHERE user_id = {user_id}")


try:
    main()
except Exception as error:
    print(json.dumps({'result': 'FAIL', 'error': str(error)}, ensure_ascii=False))
    sys.exit(1)
