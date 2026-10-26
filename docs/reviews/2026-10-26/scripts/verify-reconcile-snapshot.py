"""Reproduce the late-outbox snapshot window on one isolated dev activity.

A MySQL connection holds this activity's row lock. Reconcile starts and waits
for that lock after creating its RR snapshot. The lock holder then inserts a
synthetic leased PROCESSING event and commits. Reconcile must see the event and
return 409. No real compensation is executed; the future-leased row is deleted
and the test activity is offlined in finally. No service is stopped.
"""
from datetime import datetime, timedelta, timezone
from pathlib import Path
import argparse
import concurrent.futures
import json
import os
import subprocess
import time
import uuid


def main(output):
    helper_path = Path(__file__).with_name('verify-reconcile-blocked.py')
    source = helper_path.read_text(encoding='utf-8')
    marker = '\ntry:\n    main()'
    if marker not in source:
        raise RuntimeError('Unexpected helper shape')
    ns = {'__file__': str(helper_path), '__name__': 'review_helpers'}
    exec(compile(source.split(marker, 1)[0], str(helper_path), 'exec'), ns)
    api, request, db = (ns[name] for name in ('api', 'request', 'db'))
    owner = ns['login'](ns['OWNER'])
    now = datetime.now(timezone(timedelta(hours=8)))
    stamp = lambda value: value.isoformat(timespec='seconds')
    tag = uuid.uuid4().hex[:12]
    row = api('POST', '/merchant/activities', {
        'shopId': '1', 'title': 'review-snapshot-' + tag,
        'rules': 'Isolated snapshot test, offlined after check.',
        'payValue': '1000', 'actualValue': '2000', 'totalStock': 3,
        'beginTime': stamp(now - timedelta(minutes=1)),
        'endTime': stamp(now + timedelta(minutes=20)),
        'redeemBeginTime': stamp(now - timedelta(minutes=1)),
        'redeemEndTime': stamp(now + timedelta(days=1)),
    }, owner, {'Idempotency-Key': 'review-snapshot-' + tag})
    activity_id = row['id']
    event_id = 'review-snapshot-' + tag
    signal = 'review-lock-' + tag
    evidence = {'activityId': activity_id, 'eventId': event_id,
                'expected': '409 when an event commits before the activity row lock is acquired',
                'cleanupErrors': []}
    holder = None
    try:
        deadline = time.monotonic() + 15
        while True:
            page = api('GET', '/merchant/activities?current=1&pageSize=50', token=owner)
            row = next(item for item in page if item['id'] == activity_id)
            if row.get('ready'):
                break
            if time.monotonic() > deadline:
                raise RuntimeError('Fixture did not become ready')
            time.sleep(0.2)
        evidence['versionBefore'] = row['publishVersion']
        evidence['isolation'] = db('SELECT @@transaction_isolation')
        sql = f"""START TRANSACTION;
            SELECT stock FROM tb_seckill_voucher WHERE voucher_id={activity_id} FOR UPDATE;
            DO GET_LOCK('{signal}',0);
            DO SLEEP(6);
            INSERT INTO tb_outbox
              (event_id,aggregate_type,aggregate_id,aggregate_version,event_type,payload,
               delivery_status,lease_until)
            VALUES ('{event_id}','ORDER','{event_id}',0,'ORDER_CHANGED',
              JSON_OBJECT('voucherId','{activity_id}'),'PROCESSING',DATE_ADD(NOW(3),INTERVAL 1 DAY));
            COMMIT;"""
        env = {**os.environ, 'MYSQL_PWD': os.environ.get('MYSQL_PASSWORD', 'fullstack_dev_only')}
        holder = subprocess.Popen([
            ns['MYSQL'], '--protocol=TCP', '--host=' + os.environ.get('MYSQL_HOST', '127.0.0.1'),
            '--port=' + os.environ.get('MYSQL_PORT', '3306'),
            '--user=' + os.environ.get('MYSQL_USER', 'fullstack'), '--batch', '--skip-column-names',
            os.environ.get('MYSQL_DATABASE', 'project_full_stack'), '-e', sql],
            env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, encoding='utf-8')
        deadline = time.monotonic() + 3
        while db(f"SELECT COALESCE(IS_USED_LOCK('{signal}'),0)") == '0':
            if holder.poll() is not None or time.monotonic() > deadline:
                raise RuntimeError('Could not establish isolated row lock')
            time.sleep(0.1)
        with concurrent.futures.ThreadPoolExecutor(max_workers=1) as pool:
            future = pool.submit(request, 'POST',
                f'/merchant/activities/{activity_id}/reconcile?confirm=true', token=owner)
            observed_wait = False
            while not future.done() and holder.poll() is None:
                waiting = db("""SELECT COUNT(*) FROM information_schema.PROCESSLIST
                    WHERE ID<>CONNECTION_ID() AND INFO LIKE '%FOR UPDATE%'
                      AND INFO LIKE '%SELECT publish_status%'
                      AND INFO NOT LIKE '%information_schema%'""")
                observed_wait |= int(waiting) > 0
                time.sleep(0.1)
            status, response = future.result(timeout=20)
        stdout, stderr = holder.communicate(timeout=5)
        evidence['holderExit'] = holder.returncode
        if holder.returncode != 0:
            raise RuntimeError(stderr.strip() or 'Fixture transaction failed')
        evidence['observedReconcileLockWait'] = observed_wait
        evidence['http'] = status
        evidence['apiSuccess'] = response.get('success')
        evidence['eventStatusAfter'] = db(
            f"SELECT delivery_status FROM tb_outbox WHERE event_id='{event_id}'")
        evidence['versionAfter'] = db(
            f'SELECT publish_version FROM tb_seckill_voucher WHERE voucher_id={activity_id}')
        evidence['finding'] = ('INCONCLUSIVE_WINDOW_NOT_OBSERVED' if not observed_wait else
            'PASS' if status == 409 else 'BUG_REPRODUCED_LATE_EVENT_MISSED_BY_SNAPSHOT')
    finally:
        if holder is not None and holder.poll() is None:
            holder.communicate(timeout=15)
        for name, cleanup in (
            ('outbox', lambda: db(f"DELETE FROM tb_outbox WHERE event_id='{event_id}'")),
            ('offline', lambda: api('POST', f'/merchant/activities/{activity_id}/offline', token=owner)),
        ):
            try:
                cleanup()
            except Exception as error:
                evidence['cleanupErrors'].append(f'{name}: {error}')
        Path(output).write_text(json.dumps(evidence, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
        print(json.dumps(evidence, ensure_ascii=False))


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', required=True)
    main(parser.parse_args().output)
