"""Independent Stub-only checks for sensitive input, lock retry and daily budget.

Creates a fresh dev account and conversation; changes only that account's AI
keys. Refuses to generate answers unless capabilities explicitly report stub.
No merchant document is modified and no external model is called.
"""
from datetime import datetime, timedelta, timezone
from pathlib import Path
import argparse
import json
import secrets
import time


def helpers(filename, marker):
    path = Path(__file__).with_name(filename)
    source = path.read_text(encoding='utf-8')
    if marker not in source:
        raise RuntimeError('Unexpected helper shape')
    namespace = {'__file__': str(path), '__name__': 'review_helpers'}
    exec(compile(source.split(marker, 1)[0], str(path), 'exec'), namespace)
    return namespace


def main(output_path=None):
    ai = helpers('verify-ai.py', '\ntry:\n    main()')
    local = helpers('verify-local.py',
        "\nfor name, check in (('trade', trade_regression), ('lua_faults', lua_fault_repro)):")
    api, request, sse = (ai[name] for name in ('api', 'request', 'sse'))
    token = ai['login']('178' + f'{secrets.randbelow(100_000_000):08d}')
    caps = api('GET', '/ai/capabilities', token=token)
    if caps.get('mode') != 'stub':
        raise RuntimeError('Refusing AI generation: active provider is not stub')
    user_id = api('GET', '/user/me', token=token)['id']
    conversation = api('POST', '/ai/conversations', {}, token)['id']
    message_id = 'review-lock-retry'
    lock = f'ai:lock:{user_id}:{conversation}'
    dedupe = f'ai:dedupe:{user_id}:{conversation}:{message_id}'
    day = datetime.now(timezone(timedelta(hours=8))).date().isoformat()
    budget = f'ai:budget:{user_id}:{day}'
    evidence = {'provider': 'stub', 'cleanupErrors': []}
    try:
        before = api('GET', '/dev/ai/calls', token=token)
        statuses = {}
        for name, text in {
            'login_token': '0' * 32, 'code_is': '验证码是123456',
            'code_colon': '验证码为：123456', 'plain_code': '验证码 123456',
        }.items():
            status, _ = request('POST', f'/ai/conversations/{conversation}/messages',
                {'clientMessageId': 'review-sensitive-' + name, 'message': text}, token)
            statuses[name] = status
        after = api('GET', '/dev/ai/calls', token=token)
        evidence['sensitive'] = {'http': statuses, 'callsBefore': before, 'callsAfter': after,
            'finding': 'PASS' if all(s == 400 for s in statuses.values()) and before == after else 'FAIL'}

        with local['Redis']() as redis:
            if redis.command('SET', lock, 'independent-review', 'EX', 60, 'NX') != 'OK':
                raise RuntimeError('Unexpected occupied fixture lock')
        body = {'clientMessageId': message_id, 'message': '未支付订单可以取消后重新购买吗'}
        rejected, _ = sse(conversation, body, token)
        with local['Redis']() as redis:
            dedupe_after_reject = redis.command('EXISTS', dedupe)
            redis.command('DEL', lock)
        retried, events = sse(conversation, body, token)
        event_names = [event[0] for event in events]
        evidence['lockRetry'] = {'lockedHttp': rejected, 'dedupeAfterReject': dedupe_after_reject,
            'retryHttp': retried, 'events': event_names,
            'finding': 'PASS' if rejected == 409 and dedupe_after_reject == 0
            and retried == 200 and 'done' in event_names and 'error' not in event_names else 'FAIL'}

        before = api('GET', '/dev/ai/calls', token=token)
        with local['Redis']() as redis:
            original_budget = redis.command('GET', budget)
            redis.command('SET', budget, 1000000, 'EX', 60)
        try:
            status, response = request('POST', f'/ai/conversations/{conversation}/messages',
                {'clientMessageId': 'review-budget', 'message': '平台规则是什么'}, token)
            after = api('GET', '/dev/ai/calls', token=token)
            evidence['dailyBudget'] = {'http': status, 'message': response.get('errorMsg'),
                'callsBefore': before, 'callsAfter': after,
                'finding': 'PASS' if status == 429 and before == after else 'FAIL'}
        finally:
            with local['Redis']() as redis:
                if original_budget is None:
                    redis.command('DEL', budget)
                else:
                    redis.command('SET', budget, original_budget, 'EX', 172800)
    finally:
        try:
            api('DELETE', f'/ai/conversations/{conversation}', token=token)
            with local['Redis']() as redis:
                redis.command('DEL', lock, dedupe)
        except Exception as error:
            evidence['cleanupErrors'].append(str(error))
        out = (Path(output_path) if output_path else Path(__file__).resolve().parents[1]
               / f'ai-boundaries-{int(time.time())}.json')
        out.write_text(json.dumps(evidence, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
        print(json.dumps(evidence, ensure_ascii=False))


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', help='New result file; use a SHA-specific path to preserve prior evidence')
    main(parser.parse_args().output)
