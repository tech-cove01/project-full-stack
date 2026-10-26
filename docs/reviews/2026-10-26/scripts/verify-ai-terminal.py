"""H0-A1 evidence: every terminal branch must check the lease and the conversation.

Runs against the stub provider started with AI_STUB_DELAY_MS (>= 2000) so a turn stays open
long enough to land a delete or a lease loss mid-flight. Two scenarios:

 1. the conversation is deleted while the model is generating -> no answer/done events and no
    dedupe record, because the turn may not persist or stream a result for a deleted session
 2. the conversation lock is deleted (lease lost) while generating -> same expectation

The test document is removed in finally; the stub makes no external calls.
"""
from pathlib import Path
import json
import sys
import threading
import time
import uuid

sys.stdout.reconfigure(encoding='utf-8')


def helpers(filename, marker):
    path = Path(__file__).with_name(filename)
    source = path.read_text(encoding='utf-8')
    if marker not in source:
        raise RuntimeError('unexpected helper shape for ' + filename)
    namespace = {'__file__': str(path), '__name__': 'review_helpers'}
    exec(compile(source.split(marker, 1)[0], str(path), 'exec'), namespace)
    return namespace


AI_MARKER = '\ntry:\n    main()'
LOCAL_MARKER = "\nfor name, check in (('trade', trade_regression), ('lua_faults', lua_fault_repro)):"


def main():
    ai = helpers('verify-ai.py', AI_MARKER)
    local = helpers('verify-local.py', LOCAL_MARKER)
    api, request, sse, login, db, Redis = (ai['api'], ai['request'], ai['sse'],
                                           ai['login'], ai['db'], local['Redis'])
    caps = None
    owner = login(ai['OWNER'])
    token = login('173' + str(uuid.uuid4().int)[:8])
    caps = api('GET', '/ai/capabilities', token=token)
    if caps.get('mode') != 'stub':
        raise RuntimeError('refusing to run: provider is ' + str(caps.get('mode')))
    user_id = api('GET', '/user/me', token=token)['id']

    title = 'A1 terminal ' + uuid.uuid4().hex[:4]
    body = '测试门店规则：核销后不可退款，核销前可以联系门店处理退款。'
    draft = api('POST', '/merchant/knowledge', {'shopId': '1', 'title': title, 'body': body}, owner)
    document_id = draft['id']
    evidence = {'documentId': document_id, 'provider': 'stub', 'cleanupErrors': []}
    try:
        api('POST', f'/merchant/knowledge/{document_id}/publish', token=owner)
        question = '测试门店的核销退款规则是什么？'

        # Scenario 1: conversation deleted mid-generation.
        conversation = api('POST', '/ai/conversations', {'shopId': '1'}, token)['id']
        captured = {}

        def run_sse(target, message_id):
            captured['status'], captured['events'] = sse(
                target, {'clientMessageId': message_id, 'shopId': '1', 'message': question}, token)

        worker = threading.Thread(target=run_sse, args=(conversation, 'a1-deleted'))
        worker.start()
        time.sleep(1.5)
        api('DELETE', f'/ai/conversations/{conversation}', token=token)
        worker.join(timeout=30)
        names = [name for name, _ in captured.get('events') or []]
        dedupe_key = f'ai:dedupe:{user_id}:{conversation}:a1-deleted'
        with Redis() as redis:
            dedupe_exists = redis.command('EXISTS', dedupe_key)
        evidence['deletedMidFlight'] = {'events': names, 'dedupeExists': dedupe_exists,
                                        'hasAnswer': 'answer' in names, 'hasDone': 'done' in names}

        # Scenario 2: lease removed mid-generation.
        conversation2 = api('POST', '/ai/conversations', {'shopId': '1'}, token)['id']
        captured2 = {}

        def run_sse2():
            captured2['status'], captured2['events'] = sse(
                conversation2, {'clientMessageId': 'a1-lease', 'shopId': '1', 'message': question}, token)

        worker2 = threading.Thread(target=run_sse2)
        worker2.start()
        time.sleep(1.5)
        with Redis() as redis:
            redis.command('DEL', f'ai:lock:{user_id}:{conversation2}')
        worker2.join(timeout=30)
        names2 = [name for name, _ in captured2.get('events') or []]
        dedupe2 = f'ai:dedupe:{user_id}:{conversation2}:a1-lease'
        with Redis() as redis:
            dedupe2_exists = redis.command('EXISTS', dedupe2)
        evidence['leaseLost'] = {'events': names2, 'dedupeExists': dedupe2_exists,
                                 'hasAnswer': 'answer' in names2, 'hasDone': 'done' in names2}
        ok = (not evidence['deletedMidFlight']['hasAnswer']
              and not evidence['deletedMidFlight']['hasDone']
              and evidence['deletedMidFlight']['dedupeExists'] == 0
              and not evidence['leaseLost']['hasAnswer']
              and not evidence['leaseLost']['hasDone']
              and evidence['leaseLost']['dedupeExists'] == 0)
        evidence['finding'] = 'FIXED' if ok else 'REVIEW_REQUIRED'
    finally:
        for label, cleanup in (
                ('delete-conversation2', lambda: api('DELETE', f'/ai/conversations/{conversation2}', token=token)),
                ('delete-conversation', lambda: api('DELETE', f'/ai/conversations/{conversation}', token=token)),
                ('document-chunks', lambda: db(f'DELETE FROM tb_ai_chunk WHERE document_id = {document_id}')),
                ('document-versions', lambda: db(f'DELETE FROM tb_ai_document_version WHERE document_id = {document_id}')),
                ('document', lambda: db(f'DELETE FROM tb_ai_document WHERE id = {document_id}'))):
            try:
                cleanup()
            except Exception as error:
                evidence['cleanupErrors'].append(f'{label}: {error}')
        from datetime import datetime
        stamp = datetime.now().strftime('%Y%m%d-%H%M%S')
        out = Path(__file__).resolve().parents[1] / f'ai-terminal-{stamp}.json'
        out.write_text(json.dumps(evidence, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
        print(json.dumps(evidence, ensure_ascii=False))


try:
    main()
except Exception as error:
    import traceback
    traceback.print_exc()
    print(json.dumps({'result': 'FAIL', 'error': str(error)}, ensure_ascii=False))
    sys.exit(1)
