"""A1/A2/A3 evidence on a stub provider (no external model calls).

A1: a cached answer and the conversation memory must stop serving a rule that was disabled
    after the answer was generated.
A2: per-user minute rate limit rejects before the provider is called.
A3: real credential shapes (32-hex login token, labelled six-digit code) are rejected before
    embedding.

The test document is created, published, disabled and finally removed from MySQL.
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
OWNER = '13800000000'
MYSQL = os.environ.get('MYSQL_BIN', 'mysql')
DB_PORT = os.environ.get('MYSQL_PORT', '3306')


def db(sql):
    env = {**os.environ, 'MYSQL_PWD': os.environ.get('MYSQL_PASSWORD', 'fullstack_dev_only')}
    result = subprocess.run(
        [MYSQL, '--protocol=TCP', '--host=' + os.environ.get('MYSQL_HOST', '127.0.0.1'),
         '--port=' + DB_PORT, '--user=' + os.environ.get('MYSQL_USER', 'fullstack'),
         '--default-character-set=utf8mb4', '--batch', '--skip-column-names',
         os.environ.get('MYSQL_DATABASE', 'project_full_stack'), '-e', sql],
        env=env, capture_output=True, text=True, encoding='utf-8')
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
        with urlopen(req, timeout=30) as response:
            return response.status, json.load(response)
    except HTTPError as error:
        return error.code, json.loads(error.read().decode())


def api(method, path, body=None, token=None):
    status, data = request(method, path, body, token)
    if status >= 400 or not data.get('success'):
        raise RuntimeError(f'{method} {path}: HTTP {status}: {data.get("errorMsg")}')
    return data.get('data')


def login(phone):
    for attempt in range(3):
        try:
            status, payload = request('POST', '/user/code?' + urlencode({'phone': phone}))
            if status >= 400 or not payload.get('success'):
                raise RuntimeError(str(payload.get('errorMsg')))
            code = payload['data']
            break
        except RuntimeError as error:
            if '频繁' in str(error) and attempt < 2:
                time.sleep(61)
                continue
            raise
    return api('POST', '/user/login', {'phone': phone, 'code': code['code']})


def sse(conversation_id, body, token):
    # Errors are returned as JSON; only accepting text/event-stream makes the 4xx body
    # unnegotiable and hides the actual reason.
    headers = {'Content-Type': 'application/json',
               'Accept': 'text/event-stream, application/json'}
    if token:
        headers['authorization'] = token
    req = Request(BASE + '/ai/conversations/' + conversation_id + '/messages',
                  data=json.dumps(body).encode(), headers=headers, method='POST')
    events = []
    try:
        with urlopen(req, timeout=120) as response:
            name = None
            for raw in response:
                line = raw.decode('utf-8').rstrip('\r\n')
                if line.startswith('event:'):
                    name = line[6:].strip()
                elif line.startswith('data:'):
                    try:
                        events.append((name, json.loads(line[5:].strip())))
                    except json.JSONDecodeError:
                        events.append((name, line[5:].strip()))
    except HTTPError as error:
        return error.code, json.loads(error.read().decode())
    return 200, events


def event(events, name):
    for item_name, data in events:
        if item_name == name:
            return data
    return None


def main():
    owner = login(OWNER)
    user = login('173' + str(uuid.uuid4().int)[:8])
    title = 'A1 测试门店核销规则 ' + uuid.uuid4().hex[:4]
    body = ('测试门店的咖啡券核销后不可退款。核销前可以退款，核销后只能联系门店处理。'
            '咖啡券需要到店出示核销凭证。')
    draft = api('POST', '/merchant/knowledge', {'shopId': '1', 'title': title, 'body': body}, owner)
    document_id = draft['id']
    evidence = {'documentId': document_id}
    try:
        api('POST', f'/merchant/knowledge/{document_id}/publish', token=owner)
        conversation = api('POST', '/ai/conversations', {'shopId': '1'}, user)
        conversation_id = conversation['id']

        status, first = sse(conversation_id, {
            'clientMessageId': 'a1-first', 'shopId': '1',
            'message': '测试门店的咖啡券核销规则是什么？'}, user)
        first_answer = event(first, 'answer')
        first_sources = event(first, 'sources') or []
        first_docs = sorted({str(source.get('documentId')) for source in first_sources})
        evidence['firstTurn'] = {'http': status, 'answer': first_answer,
                                 'sourceCount': len(first_sources), 'documentIds': first_docs}

        status, cached = sse(conversation_id, {
            'clientMessageId': 'a1-first', 'shopId': '1',
            'message': '测试门店的咖啡券核销规则是什么？'}, user)
        evidence['cachedReplay'] = {'http': status, 'done': event(cached, 'done')}

        api('POST', f'/merchant/knowledge/{document_id}/disable', token=owner)
        status, replay = sse(conversation_id, {
            'clientMessageId': 'a1-first', 'shopId': '1',
            'message': '测试门店的咖啡券核销规则是什么？'}, user)
        evidence['replayAfterDisable'] = {'http': status, 'error': event(replay, 'error'),
                                          'answer': event(replay, 'answer')}

        status, after = sse(conversation_id, {
            'clientMessageId': 'a1-after-disable', 'shopId': '1',
            'message': '测试门店的咖啡券核销规则是什么？'}, user)
        after_sources = event(after, 'sources') or []
        after_docs = sorted({str(source.get('documentId')) for source in after_sources})
        evidence['newTurnAfterDisable'] = {'http': status, 'answer': event(after, 'answer'),
                                           'sourceCount': len(after_sources),
                                           'documentIds': after_docs}

        status, sensitive = request('POST', f'/ai/conversations/{conversation_id}/messages',
                                    {'clientMessageId': 'a3-token', 'shopId': '1',
                                     'message': '我的登录 token 是 0123456789abcdef0123456789abcdef'}, user)
        status_code, labelled = request('POST', f'/ai/conversations/{conversation_id}/messages',
                                        {'clientMessageId': 'a3-code', 'shopId': '1',
                                         'message': '验证码 123456 帮我登录'}, user)
        evidence['sensitive'] = {'tokenStatus': status, 'tokenMsg': sensitive.get('errorMsg'),
                                 'codeStatus': status_code, 'codeMsg': labelled.get('errorMsg')}

        limited = []
        for index in range(24):
            code, _ = sse(conversation_id, {
                'clientMessageId': f'a2-{index}', 'shopId': '1', 'message': '测试门店规则'}, user)
            limited.append(code)
            if code == 429:
                break
        evidence['rateLimit'] = limited

        api('DELETE', f'/ai/conversations/{conversation_id}', token=user)
        # Other platform FAQs may still answer the question; the disabled document must not.
        a1_ok = (str(document_id) in first_docs
                 and (evidence['cachedReplay']['done'] or {}).get('cached') is True
                 and (evidence['replayAfterDisable']['error'] or {}).get('code') == 'KNOWLEDGE_CHANGED'
                 and str(document_id) not in after_docs)
        a3_ok = status == 400 and status_code == 400
        a2_ok = 429 in limited
        evidence['finding'] = {
            'A1': 'FIXED' if a1_ok else 'REVIEW_REQUIRED',
            'A2': 'FIXED' if a2_ok else 'REVIEW_REQUIRED',
            'A3': 'FIXED' if a3_ok else 'REVIEW_REQUIRED',
        }
        print(json.dumps(evidence, ensure_ascii=False))
    finally:
        db(f'DELETE FROM tb_ai_chunk WHERE document_id = {document_id}')
        db(f'DELETE FROM tb_ai_document_version WHERE document_id = {document_id}')
        db(f'DELETE FROM tb_ai_document WHERE id = {document_id}')


try:
    main()
except Exception as error:
    print(json.dumps({'result': 'FAIL', 'error': str(error)}, ensure_ascii=False))
    sys.exit(1)
