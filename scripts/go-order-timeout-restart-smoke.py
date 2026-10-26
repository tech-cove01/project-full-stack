"""Real HTTP orders survive an offline deadline and close after API restart.

Uses only stdlib and the mysql CLI, with explicit isolated MYSQL_*/REDIS_*.
Creates two orders through publication/admission/Stream, then stops the API
before one deadline and restarts with a different timeout configuration.
No deadline SQL fixture, ZSet hint, token output or shared group reset.
"""
import argparse
import datetime
import importlib.util
import json
import os
from pathlib import Path
import re
import secrets
import socket
import subprocess
import tempfile
import time
from urllib.error import HTTPError
from urllib.request import Request, build_opener, ProxyHandler


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    for key in ('MYSQL_HOST', 'MYSQL_PORT', 'MYSQL_DATABASE', 'MYSQL_USER',
                'MYSQL_PASSWORD', 'REDIS_HOST', 'REDIS_PORT'):
        if not os.environ.get(key):
            raise SystemExit('Explicit isolated dependency settings are required')
    spec = importlib.util.spec_from_file_location('bootstrap', Path(__file__).with_name('go-bootstrap-smoke.py'))
    boot = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(boot)
    opener = build_opener(ProxyHandler({}))
    binary = args.binary.resolve(strict=True)
    report = {'task': 'GO-031', 'passed': False, 'checks': [], 'runs': [], 'cleanup': False}
    base = 9007199254740993 + secrets.randbelow(100000000) * 1000
    users = [str(base + index) for index in range(3)]
    shop = merchant = member = str(base)
    prefix = 'GOTR' + secrets.token_hex(4)
    phones = set()
    while len(phones) < 3:
        phones.add('139' + str(secrets.randbelow(90000000) + 10000000))
    phones = list(phones)
    private_values = set(phones)
    sessions, activities, orders = [], set(), set()
    processes, logs = [], []
    seeded, current_port = False, None
    redactor = boot.Redactor(os.environ)

    def check(condition, description):
        if not condition:
            raise RuntimeError(description)
        report['checks'].append(description)

    def sql(statement):
        environment = dict(os.environ, MYSQL_PWD=os.environ['MYSQL_PASSWORD'])
        result = subprocess.run([
            os.environ.get('MYSQL_BIN', 'mysql'), '--protocol=TCP',
            '--host=' + os.environ['MYSQL_HOST'], '--port=' + os.environ['MYSQL_PORT'],
            '--user=' + os.environ['MYSQL_USER'], '--default-character-set=utf8mb4',
            '--batch', '--skip-column-names', os.environ['MYSQL_DATABASE'],
        ], input="SET NAMES utf8mb4; SET time_zone='+08:00'; " + statement,
            text=True, encoding='utf-8', capture_output=True, env=environment, timeout=15)
        if result.returncode:
            raise RuntimeError('Owned SQL fixture command failed; diagnostics omitted')
        return result.stdout.strip()

    def redis(*arguments):
        def send(stream, values):
            encoded = [str(value).encode() for value in values]
            stream.write(b'*' + str(len(encoded)).encode() + b'\r\n' + b''.join(
                b'$' + str(len(value)).encode() + b'\r\n' + value + b'\r\n' for value in encoded))
            stream.flush()

        def read(stream):
            line = stream.readline()
            if not line.endswith(b'\r\n'):
                raise RuntimeError('Redis reply incomplete')
            kind, value = line[:1], line[1:-2]
            if kind == b'-':
                raise RuntimeError('Owned Redis command failed; diagnostics omitted')
            if kind == b'+':
                return value.decode()
            if kind == b':':
                return int(value)
            size = int(value)
            if kind == b'$':
                if size == -1:
                    return None
                data = stream.read(size + 2)
                if len(data) != size + 2:
                    raise RuntimeError('Redis bulk reply incomplete')
                return data[:-2].decode()
            if kind == b'*':
                return None if size == -1 else [read(stream) for _ in range(size)]
            raise RuntimeError('Redis reply shape unexpected')

        with socket.create_connection((os.environ['REDIS_HOST'], int(os.environ['REDIS_PORT'])), timeout=5) as connection:
            with connection.makefile('rwb') as stream:
                if os.environ.get('REDIS_PASSWORD'):
                    send(stream, ('AUTH', os.environ['REDIS_PASSWORD']))
                    read(stream)
                if os.environ.get('REDIS_DATABASE'):
                    send(stream, ('SELECT', os.environ['REDIS_DATABASE']))
                    read(stream)
                send(stream, arguments)
                return read(stream)

    def api(path, token=None, method='GET', body=None, key=None):
        headers = {'Content-Type': 'application/json'}
        if token:
            headers['authorization'] = token
        if key:
            headers['Idempotency-Key'] = key
        request = Request(f'http://127.0.0.1:{current_port}' + path,
                          data=json.dumps(body).encode() if body is not None else None,
                          headers=headers, method=method)
        try:
            response = opener.open(request, timeout=8)
        except HTTPError as error:
            response = error
        with response:
            value = json.load(response)
            data = value.get('data')
            if isinstance(data, dict) and isinstance(data.get('redeemToken'), str):
                private_values.add(data['redeemToken'])
            return response.status, value

    def eventually(read, accept, timeout=20):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            value = read()
            if accept(value):
                return value
            time.sleep(.15)
        raise RuntimeError('Bounded verification wait exceeded')

    def start(timeout):
        nonlocal current_port
        current_port = boot.api_port()
        environment = dict(os.environ, GO_ENV='development', API_ADDR=f'127.0.0.1:{current_port}',
                           GO_ORDER_PAY_TIMEOUT=timeout)
        log = tempfile.TemporaryFile()
        logs.append(log)
        process = subprocess.Popen([str(binary)], env=environment, stdin=subprocess.DEVNULL,
                                   stdout=log, stderr=subprocess.STDOUT,
                                   creationflags=subprocess.CREATE_NEW_PROCESS_GROUP if os.name == 'nt' else 0)
        processes.append(process)
        health = boot.await_health(process, current_port, opener, time.monotonic() + 15)
        report['runs'].append({'timeoutConfig': timeout, 'port': current_port,
                               'version': health[0]['body']['version']})
        return process

    def stop(process):
        signal_name, code = boot.graceful_shutdown(process)
        report['runs'][-1].update({'shutdownSignal': signal_name, 'exitCode': code,
                                   'portRebindable': boot.can_rebind(current_port)})
        check(code == 0 and report['runs'][-1]['portRebindable'], 'API exits cleanly and releases its port')

    def owned_messages():
        result, cursor = [], '-'
        while True:
            batch = redis('XRANGE', 'stream.orders', cursor, '+', 'COUNT', 500)
            for source, values in batch:
                fields = dict(zip(values[::2], values[1::2]))
                if fields.get('voucherId') in activities and fields.get('userId') in users:
                    value = fields.get('id', '')
                    if not re.fullmatch(r'[1-9]\d*', value) or int(value) > 9223372036854775807:
                        raise RuntimeError('Owned source has an unsafe order identifier')
                    result.append({'sourceId': source, 'orderId': value})
            if len(batch) < 500:
                return result
            cursor = '(' + batch[-1][0]

    try:
        report['step'] = 'seed owned identities'
        sql('START TRANSACTION; INSERT INTO tb_user(id,phone,nick_name) VALUES ' + ','.join(
            f"({user},'{phone}','{prefix} user{index}')" for index, (user, phone) in enumerate(zip(users, phones))) +
            f"; INSERT INTO tb_merchant(id,name) VALUES({merchant},'{prefix} merchant');"
            f" INSERT INTO tb_shop(id,merchant_id,name,type_id,address,x,y) VALUES({shop},{merchant},'{prefix} shop',1,'GO031 restart fixture',120,30);"
            f" INSERT INTO tb_merchant_member(id,merchant_id,user_id,role_code) VALUES({member},{merchant},{users[0]},'OWNER'); COMMIT;")
        seeded = True
        process = start('10s')
        for phone in phones:
            status, sent = api('/user/code?phone=' + phone, method='POST')
            code = sent.get('data', {}).get('code')
            check(status == 200 and isinstance(code, str) and re.fullmatch(r'\d{6}', code), 'Development login returns a code without exposing it')
            private_values.add(code)
            status, logged = api('/user/login', method='POST', body={'phone': phone, 'code': code})
            token = logged.get('data')
            if status != 200 or not isinstance(token, str) or not re.fullmatch(r'[a-f0-9]{32}', token):
                raise RuntimeError('Owned login unavailable')
            private_values.add(token)
            sessions.append(token)
        def business_time(seconds):
            return sql(f"SELECT DATE_FORMAT(TIMESTAMPADD(SECOND,{seconds},NOW()),'%Y-%m-%dT%H:%i:%s')") + '+08:00'
        status, published = api('/merchant/activities', sessions[0], 'POST', {
            'shopId': shop, 'title': prefix + ' restart', 'subTitle': None, 'rules': 'Real timeout restart verification',
            'payValue': '990', 'actualValue': '2000', 'totalStock': 3,
            'beginTime': business_time(-60), 'endTime': business_time(3600),
            'redeemBeginTime': business_time(-60), 'redeemEndTime': business_time(7200),
        }, prefix + '-publish')
        voucher = published.get('data', {}).get('id')
        if status != 200 or not isinstance(voucher, str) or not re.fullmatch(r'[1-9]\d*', voucher):
            raise RuntimeError('Owned publication failed')
        activities.add(voucher)
        eventually(lambda: api(f'/merchant/activities?shopId={shop}', sessions[0]),
                   lambda value: any(item.get('id') == voucher and item.get('ready') for item in value[1].get('data', [])))
        report['step'] = 'real admission and Stream creation'
        ids = []
        for token in sessions[1:]:
            status, accepted = api('/voucher-order/seckill/' + voucher, token, 'POST')
            order_id = accepted.get('data')
            if status != 200 or not isinstance(order_id, str) or not re.fullmatch(r'[1-9]\d*', order_id):
                raise RuntimeError('Real admission failed')
            orders.add(order_id)
            ids.append(order_id)
            eventually(lambda: api('/voucher-order/' + order_id, token),
                       lambda value: value[0] == 200 and value[1].get('data', {}).get('orderStatus') == 1)
        unpaid, paid = ids
        status, paid_view = api('/voucher-order/' + paid + '/pay', sessions[2], 'POST')
        check(status == 200 and paid_view.get('data', {}).get('orderStatus') == 2, 'A real second buyer pays before the deadline')
        messages = eventually(owned_messages, lambda rows: len(rows) == 2)
        eventually(lambda: [redis('XPENDING', 'stream.orders', 'g1', item['sourceId'], item['sourceId'], 1) for item in messages], lambda rows: all(not row for row in rows))
        report['sourceMessages'] = messages
        before = sql(f"SELECT status,version,CAST(UNIX_TIMESTAMP(pay_deadline_at)*1000 AS UNSIGNED),CAST(UNIX_TIMESTAMP(NOW(3))*1000 AS UNSIGNED) FROM tb_voucher_order WHERE id={unpaid}").split('\t')
        check(before[:2] == ['1', '0'] and int(before[2]) > int(before[3]), 'Unpaid real order is stopped before its immutable DB deadline')
        report['beforeStop'] = {'orderId': unpaid, 'status': 1, 'version': 0, 'deadlineEpochMs': before[2], 'dbNowEpochMs': before[3]}
        stop(process)
        stopped_clock = sql(f'SELECT CAST(UNIX_TIMESTAMP(NOW(3))*1000 AS UNSIGNED) FROM tb_voucher_order WHERE id={unpaid}')
        check(int(stopped_clock) < int(before[2]), 'The API has fully exited before the DB deadline passes')
        report['afterStop'] = {'dbNowEpochMs': stopped_clock, 'deadlineEpochMs': before[2], 'apiExited': True}
        report['step'] = 'deadline passes while API is stopped'
        eventually(lambda: sql(f'SELECT NOW(3)>=pay_deadline_at FROM tb_voucher_order WHERE id={unpaid}'), lambda value: value == '1', 15)
        check(sql(f'SELECT status FROM tb_voucher_order WHERE id={unpaid}') == '1', 'Deadline passes offline without pretending the stopped scanner ran')
        report['step'] = 'restart with changed configuration and no ZSet hints'
        process = start('1h')
        eventually(lambda: api('/voucher-order/' + unpaid, sessions[1]), lambda value: value[1].get('data', {}).get('orderStatus') == 4, 12)
        snapshot = sql(f"SELECT status,version,cancel_reason,CAST(UNIX_TIMESTAMP(pay_deadline_at)*1000 AS UNSIGNED),cancel_time>=pay_deadline_at FROM tb_voucher_order WHERE id={unpaid}").split('\t')
        check(snapshot == ['4', '1', 'PAY_TIMEOUT', before[2], '1'], 'Restart closes the old deadline once even when new-order timeout changes to one hour')
        check(sql(f'SELECT stock FROM tb_seckill_voucher WHERE voucher_id={voucher}') == '2', 'Only the expired unpaid order restores one DB stock unit')
        check(sql(f"SELECT COUNT(*) FROM tb_audit_log WHERE resource_id='{unpaid}' AND action='ORDER_CANCELLED' AND operator_type='SYSTEM' AND operator_user_id=0") == '1', 'Timeout has exactly one trusted SYSTEM cancellation audit')
        check(sql(f"SELECT COUNT(*) FROM tb_outbox WHERE event_id='ORDER:{unpaid}:1:CHANGED' AND delivery_status='PENDING' AND payload->>'$.stockRestore'='1' AND payload->>'$.releaseEligibility'='true'") == '1', 'Timeout persists one compensation event without claiming Redis compensation completed')
        check(redis('GET', 'seckill:stock:' + voucher) == '1', 'Redis preallocation stays unchanged until GO-033 compensation')
        check((api('/voucher-order/' + paid, sessions[2]))[1].get('data', {}).get('orderStatus') == 2, 'The paid order stays paid after its old deadline')
        check(api('/voucher-order/' + unpaid + '/pay', sessions[1], 'POST')[0] == 409, 'Late payment cannot revive a timeout-cancelled order')
        check(api('/voucher-order/' + unpaid + '/cancel', sessions[1], 'POST')[1].get('data', {}).get('orderStatus') == 4, 'Repeating cancellation returns the existing cancelled order')
        time.sleep(5.3)
        check(sql(f'SELECT version FROM tb_voucher_order WHERE id={unpaid}') == '1'
              and sql(f'SELECT stock FROM tb_seckill_voucher WHERE voucher_id={voucher}') == '2', 'A later production-interval scan causes no second transition or stock restoration')
        report['afterRestart'] = {'orderId': unpaid, 'status': 4, 'version': 1, 'dbStock': 2, 'redisStock': 1, 'compensation': 'PENDING', 'zsetBoundary': 'No hint is created or needed by the DB-based producer/scanner'}
        for token in sessions:
            api('/user/logout', token, 'POST')
        stop(process)
        report['passed'] = True
    except Exception as error:
        report['failure'] = 'Verification failed at ' + report.get('step', 'setup') + ' (' + type(error).__name__ + ')'
    finally:
        report['forcedCleanup'] = False
        for process in processes:
            if process.poll() is None:
                try:
                    boot.graceful_shutdown(process)
                except Exception:
                    process.kill()
                    process.wait(timeout=5)
                    report['forcedCleanup'] = True
                    report['passed'] = False
        clean = True
        try:
            if seeded:
                catalog = sql(f'SELECT id FROM tb_voucher WHERE shop_id={shop}').split()
                activities.update(catalog)
                messages = owned_messages() if activities else []
                orders.update(item['orderId'] for item in messages)
                voucher_list = ','.join(sorted(activities)) or '0'
                user_list = ','.join(users)
                own_rows = set(sql(f'SELECT id FROM tb_voucher_order WHERE user_id IN ({user_list}) AND voucher_id IN ({voucher_list})').split())
                orders.update(own_rows)
                verified = set()
                for order_id in orders:
                    if not re.fullmatch(r'[1-9]\d*', order_id) or int(order_id) > 9223372036854775807:
                        clean = False
                        continue
                    foreign = sql(f'SELECT COUNT(*) FROM tb_voucher_order WHERE id={order_id} AND (user_id NOT IN ({user_list}) OR voucher_id NOT IN ({voucher_list}))')
                    if foreign != '0':
                        clean = False
                        continue
                    values = redis('HGETALL', 'order:reservation:' + order_id)
                    reservation = dict(zip(values[::2], values[1::2]))
                    reservation_owned = reservation.get('userId') in users and reservation.get('voucherId') in activities
                    if reservation and not reservation_owned:
                        clean = False
                        continue
                    if order_id in own_rows or reservation_owned:
                        verified.add(order_id)
                    else:
                        clean = False
                orders = verified
                order_list = ','.join(sorted(orders)) or '0'
                order_strings = ','.join("'" + value + "'" for value in orders) or "''"
                voucher_strings = ','.join("'" + value + "'" for value in activities) or "''"
                user_strings = ','.join("'" + value + "'" for value in users)
                owned_events = (f"(aggregate_type='ACTIVITY' AND aggregate_id IN ({voucher_strings})) OR "
                                f"(aggregate_type IN ('ORDER','RESERVATION') AND aggregate_id IN ({order_strings}) "
                                f"AND payload->>'$.userId' IN ({user_strings}) AND payload->>'$.voucherId' IN ({voucher_strings}))")
                sql(f"START TRANSACTION; SELECT id FROM tb_voucher WHERE shop_id={shop} FOR UPDATE;"
                    f" SELECT voucher_id FROM tb_seckill_voucher WHERE voucher_id IN ({voucher_list}) FOR UPDATE;"
                    f" DELETE FROM tb_outbox WHERE {owned_events};"
                    f" DELETE FROM tb_audit_log WHERE merchant_id={merchant};"
                    f" DELETE FROM tb_voucher_order WHERE id IN ({order_list}) AND user_id IN ({user_list}) AND voucher_id IN ({voucher_list});"
                    f" DELETE FROM tb_seckill_voucher WHERE voucher_id IN ({voucher_list}); DELETE FROM tb_voucher WHERE shop_id={shop};"
                    f" DELETE FROM tb_member_shop_scope WHERE member_id={member}; DELETE FROM tb_merchant_member WHERE id={member};"
                    f" DELETE FROM tb_shop WHERE id={shop}; DELETE FROM tb_merchant WHERE id={merchant};"
                    f" DELETE FROM tb_user WHERE id IN ({','.join(users)}); COMMIT;")
                for item in messages:
                    redis('XACK', 'stream.orders', 'g1', item['sourceId'])
                    redis('XDEL', 'stream.orders', item['sourceId'])
                keys = ['seckill:' + part + ':' + voucher for voucher in activities for part in ('gate', 'stock', 'begin', 'end', 'order', 'accepted')]
                keys += ['order:reservation:' + order_id for order_id in orders]
                keys += ['login:token:' + token for token in sessions]
                keys += [prefix + phone for phone in phones for prefix in ('login:code:', 'login:code:throttle:')]
                if keys:
                    redis('DEL', *keys)
                    remaining_keys = redis('EXISTS', *keys)
                    clean = clean and remaining_keys == 0
                clean = clean and sql(f"SELECT (SELECT COUNT(*) FROM tb_voucher_order WHERE user_id IN ({user_list}) AND voucher_id IN ({voucher_list}))+(SELECT COUNT(*) FROM tb_voucher WHERE shop_id={shop})+(SELECT COUNT(*) FROM tb_user WHERE id IN ({user_list}))+(SELECT COUNT(*) FROM tb_outbox WHERE {owned_events})") == '0'
                clean = clean and not owned_messages()
        except Exception:
            clean = False
        report['cleanup'] = clean
        report['passed'] = report['passed'] and clean
        sanitized_logs = []
        for log in logs:
            log.seek(0)
            raw = log.read().decode('utf-8', errors='replace')
            leaked = any(value in raw for value in private_values) or redactor.contains_password(raw)
            report['passed'] = report['passed'] and not leaked
            for value in sorted(private_values, key=len, reverse=True):
                raw = raw.replace(value, '[REDACTED]')
            sanitized_logs.append({'privateValueFound': leaked, 'log': redactor.text(raw)})
            log.close()
        report['processLogs'] = sanitized_logs
        report['recordedAt'] = datetime.datetime.now(datetime.timezone.utc).isoformat()
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
        print(json.dumps({'passed': report['passed'], 'checks': len(report['checks']), 'cleanup': clean, 'output': str(args.output)}))
    return 0 if report['passed'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
