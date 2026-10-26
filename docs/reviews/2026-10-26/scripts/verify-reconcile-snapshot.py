"""H0-R2 evidence: blocker checks must see rows committed after the reconcile snapshot.

Window construction:
 1. a second MySQL session locks the activity row (BEGIN ... FOR UPDATE) and holds it
 2. reconcile runs and blocks on that row after its ordinary voucher read (snapshot created)
 3. while it is blocked, another connection inserts a leased PROCESSING compensation
 4. the lock is released; reconcile continues into the blocker queries

A REPEATABLE READ snapshot plus ordinary reads would miss the new row (HTTP 200). The current
implementation uses READ_COMMITTED and locking reads, so the call must return 409 and leave the
version untouched. The synthetic row is deleted in finally.
"""
from datetime import datetime, timedelta, timezone
from pathlib import Path
import json
import os
import subprocess
import sys
import threading
import time
import uuid

sys.stdout.reconfigure(encoding='utf-8')
HELPER = Path(__file__).with_name('verify-reconcile-blocked.py')


def helpers():
    source = HELPER.read_text(encoding='utf-8')
    marker = '\ntry:\n    main()'
    if marker not in source:
        raise RuntimeError('unexpected helper shape')
    namespace = {'__file__': str(HELPER), '__name__': 'review_helpers'}
    exec(compile(source.split(marker, 1)[0], str(HELPER), 'exec'), namespace)
    return namespace


def mysql_args():
    return ['--protocol=TCP', '--host=' + os.environ.get('MYSQL_HOST', '127.0.0.1'),
            '--port=' + os.environ.get('MYSQL_PORT', '3307'),
            '--user=' + os.environ.get('MYSQL_USER', 'fullstack'),
            '--default-character-set=utf8mb4', '--batch', '--skip-column-names',
            os.environ.get('MYSQL_DATABASE', 'project_full_stack')]


def main():
    h = helpers()
    api, request, db, login = (h[name] for name in ('api', 'request', 'db', 'login'))
    owner = login(h['OWNER'])
    now = datetime.now(timezone(timedelta(hours=8)))
    stamp = lambda value: value.isoformat(timespec='seconds')
    fixture = uuid.uuid4().hex[:12]
    activity = api('POST', '/merchant/activities', {
        'shopId': '1', 'title': 'review-snapshot-' + fixture,
        'rules': 'Independent snapshot-window check; offlined afterwards.',
        'payValue': '900', 'actualValue': '1800', 'totalStock': 2,
        'beginTime': stamp(now - timedelta(minutes=1)),
        'endTime': stamp(now + timedelta(minutes=20)),
        'redeemBeginTime': stamp(now - timedelta(minutes=1)),
        'redeemEndTime': stamp(now + timedelta(days=1)),
    }, owner, {'Idempotency-Key': 'review-snapshot-' + fixture})
    activity_id = activity['id']
    event_id = 'review-snapshot-' + fixture
    evidence = {'activityId': activity_id, 'eventId': event_id,
                'window': 'row locked before insert; reconcile blocked inside its transaction'}
    session = None
    try:
        deadline = time.monotonic() + 15
        while True:
            rows = api('GET', '/merchant/activities?current=1&pageSize=50', token=owner)
            current = next(row for row in rows if row['id'] == activity_id)
            if current.get('ready'):
                break
            if time.monotonic() > deadline:
                raise RuntimeError('fixture did not become ready')
            time.sleep(0.2)
        evidence['versionBefore'] = current['publishVersion']

        env = {**os.environ, 'MYSQL_PWD': os.environ.get('MYSQL_PASSWORD', 'fullstack_dev_only')}
        session = subprocess.Popen(
            [os.environ.get('MYSQL_BIN', 'mysql'), *mysql_args()],
            stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
            text=True, encoding='utf-8', env=env)
        session.stdin.write(f'BEGIN;\n'
                            f'SELECT publish_version FROM tb_seckill_voucher '
                            f'WHERE voucher_id = {activity_id} FOR UPDATE;\n')
        session.stdin.flush()
        time.sleep(1.0)

        result = {}

        def call_reconcile():
            status, payload = request('POST',
                f'/merchant/activities/{activity_id}/reconcile?confirm=true', token=owner)
            result['status'] = status
            result['payload'] = payload

        worker = threading.Thread(target=call_reconcile)
        worker.start()
        time.sleep(2.0)
        evidence['reconcileBlocked'] = worker.is_alive()
        db(f"""INSERT INTO tb_outbox
               (event_id,aggregate_type,aggregate_id,aggregate_version,event_type,
                payload,delivery_status,lease_until)
               VALUES ('{event_id}','ORDER','{event_id}',0,'ORDER_CHANGED',
                       JSON_OBJECT('voucherId','{activity_id}'),'PROCESSING',
                       DATE_ADD(NOW(3), INTERVAL 1 DAY))""")
        evidence['insertedWhileBlocked'] = db(
            f"SELECT delivery_status FROM tb_outbox WHERE event_id = '{event_id}'")

        session.stdin.write('COMMIT;\n')
        session.stdin.flush()
        worker.join(timeout=30)
        evidence['http'] = result.get('status')
        evidence['errorMsg'] = (result.get('payload') or {}).get('errorMsg')
        evidence['versionAfter'] = db(
            f'SELECT publish_version FROM tb_seckill_voucher WHERE voucher_id = {activity_id}')
        unchanged = str(evidence['versionAfter']) == str(evidence['versionBefore'])
        evidence['finding'] = ('FIXED' if result.get('status') == 409 and unchanged
                               else 'REVIEW_REQUIRED')
    finally:
        if session is not None:
            try:
                session.stdin.write('ROLLBACK;\n')
                session.stdin.flush()
                session.kill()
            except Exception:
                pass
        for label, cleanup in (
                ('outbox', lambda: db(f"DELETE FROM tb_outbox WHERE event_id = '{event_id}'")),
                ('offline', lambda: api('POST', f'/merchant/activities/{activity_id}/offline', token=owner))):
            try:
                cleanup()
            except Exception as error:
                evidence.setdefault('cleanupErrors', []).append(f'{label}: {error}')
        # One file per run: an older PASS must not be overwritten by a later attempt.
        stamp = datetime.now().strftime('%Y%m%d-%H%M%S')
        out = Path(__file__).resolve().parents[1] / f'reconcile-snapshot-{stamp}.json'
        out.write_text(json.dumps(evidence, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
        print(json.dumps(evidence, ensure_ascii=False))


try:
    main()
except Exception as error:
    import traceback
    traceback.print_exc()
    print(json.dumps({'result': 'FAIL', 'error': str(error)}, ensure_ascii=False))
    sys.exit(1)
