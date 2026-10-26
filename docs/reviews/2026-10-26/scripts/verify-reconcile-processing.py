"""Check that a leased PROCESSING compensation prevents inventory rebuild.

Creates one labelled activity and one synthetic outbox row with a future lease.
The row is removed in finally, and the activity is taken offline. No worker is
paused, no database is stopped, and the synthetic compensation is never applied.
Requires the localhost dev environment and MYSQL_PORT=3307 for this workstation.
"""
from datetime import datetime, timedelta, timezone
from pathlib import Path
import argparse
import json
import time
import uuid


def load_helpers():
    source_path = Path(__file__).with_name('verify-reconcile-blocked.py')
    source = source_path.read_text(encoding='utf-8')
    marker = '\ntry:\n    main()'
    if marker not in source:
        raise RuntimeError('Unexpected helper shape; refusing to execute its main')
    namespace = {'__file__': str(source_path), '__name__': 'review_helpers'}
    exec(compile(source.split(marker, 1)[0], str(source_path), 'exec'), namespace)
    return namespace


def main(output_path=None):
    helpers = load_helpers()
    api, request, db = (helpers[name] for name in ('api', 'request', 'db'))
    db('SELECT 1')
    owner = helpers['login'](helpers['OWNER'])
    now = datetime.now(timezone(timedelta(hours=8)))
    stamp = lambda value: value.isoformat(timespec='seconds')
    fixture = uuid.uuid4().hex[:12]
    activity = api('POST', '/merchant/activities', {
        'shopId': '1', 'title': 'review-processing-' + fixture,
        'rules': 'Independent leased-outbox blocker check; offlined afterwards.',
        'payValue': '1000', 'actualValue': '2000', 'totalStock': 3,
        'beginTime': stamp(now - timedelta(minutes=1)),
        'endTime': stamp(now + timedelta(minutes=20)),
        'redeemBeginTime': stamp(now - timedelta(minutes=1)),
        'redeemEndTime': stamp(now + timedelta(days=1)),
    }, owner, {'Idempotency-Key': 'review-processing-' + fixture})
    activity_id = activity['id']
    event_id = 'review-processing-' + fixture
    evidence = {'activityId': activity_id, 'eventId': event_id,
                'expected': 'HTTP 409 while PROCESSING compensation is leased',
                'cleanupErrors': []}
    try:
        deadline = time.monotonic() + 15
        while True:
            rows = api('GET', '/merchant/activities?current=1&pageSize=50', token=owner)
            current = next(row for row in rows if row['id'] == activity_id)
            if current.get('ready'):
                break
            if time.monotonic() > deadline:
                raise RuntimeError('Fixture did not become ready')
            time.sleep(0.2)
        evidence['versionBefore'] = current['publishVersion']
        db(f"""INSERT INTO tb_outbox
                (event_id,aggregate_type,aggregate_id,aggregate_version,event_type,
                 payload,delivery_status,lease_until)
                VALUES ('{event_id}','ORDER','{event_id}',0,'ORDER_CHANGED',
                        JSON_OBJECT('voucherId','{activity_id}'), 'PROCESSING',
                        DATE_ADD(NOW(3), INTERVAL 1 DAY))""")
        evidence['processingBefore'] = db(
            f"SELECT delivery_status FROM tb_outbox WHERE event_id = '{event_id}'")
        status, result = request('POST',
            f'/merchant/activities/{activity_id}/reconcile?confirm=true', token=owner)
        evidence['http'] = status
        evidence['apiSuccess'] = result.get('success')
        evidence['versionAfter'] = db(
            f'SELECT publish_version FROM tb_seckill_voucher WHERE voucher_id={activity_id}')
        evidence['processingAfter'] = db(
            f"SELECT delivery_status FROM tb_outbox WHERE event_id = '{event_id}'")
        evidence['finding'] = ('PASS' if status == 409 else
                               'BUG_REPRODUCED_PROCESSING_COMPENSATION_NOT_BLOCKED')
    finally:
        for label, cleanup in (
            ('outbox', lambda: db(f"DELETE FROM tb_outbox WHERE event_id = '{event_id}'")),
            ('offline', lambda: api('POST', f'/merchant/activities/{activity_id}/offline', token=owner)),
        ):
            try:
                cleanup()
            except Exception as error:
                evidence['cleanupErrors'].append(f'{label}: {error}')
        out = (Path(output_path) if output_path else Path(__file__).resolve().parents[1]
               / f'reconcile-processing-{int(time.time())}.json')
        out.write_text(json.dumps(evidence, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
        print(json.dumps(evidence, ensure_ascii=False))


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', help='New result file; use a SHA-specific path to preserve prior evidence')
    main(parser.parse_args().output)
