"""Actual client offline checks. All transports are injected; no cloud/dependency."""
import importlib.util
import json
from pathlib import Path
import tempfile
from unittest.mock import patch
from urllib.parse import parse_qs, urlsplit

path = Path(__file__).with_name('cap10-pts-api.py')
spec = importlib.util.spec_from_file_location('cap10_pts_api', path)
api = importlib.util.module_from_spec(spec)
spec.loader.exec_module(api)
checks = 0


def check(condition, name):
    global checks
    assert condition, name
    checks += 1
    print('PASS ' + name)


def expect_error(fn, code):
    try:
        fn()
    except api.Error as error:
        check(error.code == code, code)
        return error
    raise AssertionError('expected safe error: ' + code)


AUTH = ('mock-access-id', 'mock-access-secret', 'mock-sts-token')
SCENE = 'CAP10_SCENE'
REPORT = 'CAP10_REPORT'


class Fake:
    def __init__(self):
        self.calls = []
        self.active = False
        self.ended = False
        self.report_id = REPORT
        self.fault = None
        self.start_journal = None

    def __call__(self, path, headers, timeout):
        action = headers['x-acs-action']; params = parse_qs(urlsplit(path).query)
        self.calls.append((action, params))
        assert timeout > 0 and timeout <= 5
        if action == 'GetOpenJMeterScene':
            data = {'Scene': {'SceneId': SCENE, 'Duration': 60, 'FileList': [
                {'FileOssAddress': 'https://mock.oss-cn-shanghai.aliyuncs.com/private.jmx?Signature=oss-secret'}],
                'JMeterProperties': [{'Name': 'controlToken', 'Value': 'collector-private-value'}]}}
        elif action == 'GetJMeterSceneRunningData':
            if not self.active:
                return 403, json.dumps({'Success': False, 'Code': 'ReportNotExist', 'Message': AUTH[1]}).encode()
            data = {'RunningData': {'SceneId': SCENE, 'ReportId': self.report_id,
                'Status': 'PROVIDER_TERMINAL_VALUE_NOT_ASSUMED' if self.ended else 'RUNNING',
                'HasReport': self.ended, 'StartTimeTS': 1793520000000, 'AgentCount': 2}}
        elif action == 'StartTestingJMeterScene':
            assert self.start_journal is not None and self.start_journal.is_file(), 'start intent missing before write'
            assert json.loads(self.start_journal.read_text())['state'] == 'START_INTENT'
            self.active = True
            if self.fault == 'start':
                raise TimeoutError('private-provider-text-' + AUTH[1])
            data = {'ReportId': REPORT}
        elif action == 'StopTestingJMeterScene':
            if self.fault == 'stop':
                raise TimeoutError('private-stop-text-' + AUTH[2])
            self.ended = True
            data = {}
        elif action == 'GetJMeterReportDetails':
            data = {'ReportOverView': {'ReportId': self.report_id,
                'StartTime': '2026-11-01 12:00:00', 'EndTime': '2026-11-01 12:01:00' if self.ended else '',
                'AgentCount': 2, 'Vum': 1000}}
        elif action == 'GetJMeterSampleMetrics':
            data = {'SampleMetricList': ['{"totalCount":100,"rtMax":50}']}
        elif action == 'SaveOpenJMeterScene':
            assert json.loads(params['OpenJMeterScene'][0])['SceneId'] == SCENE
            data = {'SceneId': SCENE}
        else:
            raise AssertionError('unexpected cloud action')
        return 200, json.dumps({'Success': True, 'RequestId': 'mock-request', **data}).encode()


def main():
    # Official V3 published golden vector, not a mirrored signing implementation.
    _, headers = api.signed_request('RunInstances', {
        'ImageId': 'win2019_1809_x64_dtc_zh-cn_40G_alibase_20230811.vhd', 'RegionId': 'cn-shanghai'},
        ('YourAccessKeyId', 'YourAccessKeySecret', ''), timestamp='2023-10-26T10:22:32Z',
        nonce='3156853299f313e23d1673dc12e1703d', host='ecs.cn-shanghai.aliyuncs.com', version='2014-05-26')
    check(headers['authorization'].endswith('Signature=06563a9e1b43f5dfe96b81484da74bceab24a1d853912eee15083a6f0f3283c0'), 'official ACS3 signature vector')
    encoded, sts = api.signed_request('GetOpenJMeterScene', {'SceneId': 'space +/~中文'}, AUTH)
    check('space%20%2B%2F~%E4%B8%AD%E6%96%87' in encoded and 'x-acs-security-token' in sts['authorization'], 'RFC3986 query and STS signed header')
    check(api.ENDPOINT == 'pts.aliyuncs.com' and api.VERSION == '2020-10-20', 'fixed official endpoint and API version')
    workspace = Path(__file__).resolve().parents[3]
    with tempfile.TemporaryDirectory(prefix='.qa-cap10-pts-offline-', dir=workspace) as folder:
        root = Path(folder).resolve()
        assert root.parent == workspace and root.name.startswith('.qa-cap10-pts-offline-')
        # Only this newly created temporary directory is removed by the context.
        def scene_client(scene, label, include_scene=True):
            def transport(path, headers, timeout):
                assert headers['x-acs-action'] == 'GetOpenJMeterScene'
                assert parse_qs(urlsplit(path).query)['SceneId'] == [SCENE]
                body = {'Success': True, 'RequestId': 'scene-shape-check'}
                if include_scene:
                    body['Scene'] = scene
                return 200, json.dumps(body).encode()
            return api.Client('cn-shanghai', root / label, AUTH,
                transport=transport, scene_id=SCENE)

        omitted = scene_client({'Duration': 600, 'Concurrency': 513, 'AgentCount': 2}, 'scene-omitted')
        audited = []; original_audit = omitted._audit
        def capture_scene_audit(action, data):
            audited.append(data['sanitizedRawResponse'])
            original_audit(action, data)
        with patch.object(omitted, '_audit', side_effect=capture_scene_audit):
            normalized = omitted.scene(SCENE)
        check(normalized['Scene']['SceneId'] == SCENE and
            normalized['Scene']['Duration'] == 600 and
            normalized['sceneIdentitySource'] == 'signed-request-SceneId',
            'omitted SceneId uses bound signed request with explicit provenance')
        check(normalized is not audited[0] and normalized['Scene'] is not audited[0]['Scene'] and
            'SceneId' not in audited[0]['Scene'] and 'sceneIdentitySource' not in audited[0],
            'scene normalization copies response and Scene without mutating audited data')
        raw_audit = json.loads(next((root / 'scene-omitted').glob('*.json')).read_text(encoding='utf-8'))
        check('SceneId' not in raw_audit['sanitizedRawResponse']['Scene'],
            'stored sanitized raw response preserves provider omission')
        check(scene_client({'SceneId': SCENE, 'Duration': 600}, 'scene-explicit').scene(SCENE)['Scene']['SceneId'] == SCENE,
            'matching explicit SceneId remains accepted')
        for index, value in enumerate(('FOREIGN_SCENE', '', None, 123)):
            candidate = scene_client({'SceneId': value, 'Duration': 600}, 'scene-mismatch-' + str(index))
            expect_error(lambda: candidate.scene(SCENE), 'scene_identity_differs')
        for index, value in enumerate(({}, [], None, 'not-a-scene', 123, False)):
            candidate = scene_client(value, 'scene-invalid-' + str(index))
            expect_error(lambda: candidate.scene(SCENE), 'scene_response_invalid')
        expect_error(lambda: scene_client(None, 'scene-missing', include_scene=False).scene(SCENE), 'scene_response_invalid')

        def running_client(data, label, include_running=True, request_id='running-shape-check', http_status=200):
            def transport(path, headers, timeout):
                assert headers['x-acs-action'] == 'GetJMeterSceneRunningData'
                assert parse_qs(urlsplit(path).query)['SceneId'] == [SCENE]
                body = {'Success': True, 'RequestId': request_id}
                if include_running:
                    body['RunningData'] = data
                return http_status, json.dumps(body).encode()
            return api.Client('cn-shanghai', root / label, AUTH,
                transport=transport, scene_id=SCENE)

        running_data = {'ReportId': REPORT, 'Status': 'RUNNING', 'HasReport': False, 'AgentCount': 2}
        omitted_running = running_client(running_data, 'running-omitted')
        normalized_running = omitted_running.running(SCENE)
        check(normalized_running['sceneId'] == SCENE and normalized_running['reportId'] == REPORT and
            normalized_running['status'] == 'RUNNING' and normalized_running['hasReport'] is False and
            normalized_running['sceneIdentitySource'] == 'signed-request-SceneId',
            'running omission uses bound signed request without changing status or report fields')
        raw_running = json.loads(next((root / 'running-omitted').glob('*.json')).read_text(encoding='utf-8'))
        check('SceneId' not in raw_running['sanitizedRawResponse']['RunningData'] and 'SceneId' not in running_data,
            'running raw audit preserves omitted SceneId')
        explicit_running = running_client({**running_data, 'SceneId': SCENE}, 'running-explicit').running(SCENE)
        check(explicit_running['sceneId'] == SCENE and explicit_running['sceneIdentitySource'] == 'provider-response-SceneId',
            'matching explicit running SceneId retains provider provenance')
        for index, value in enumerate(('FOREIGN_SCENE', '', None, 123)):
            candidate = running_client({**running_data, 'SceneId': value}, 'running-mismatch-' + str(index))
            expect_error(lambda: candidate.running(SCENE), 'scene_identity_differs')
        for index, value in enumerate(({}, [], None, 'not-running-data', 123, False)):
            candidate = running_client(value, 'running-invalid-' + str(index))
            expect_error(lambda: candidate.running(SCENE), 'running_response_invalid')
        missing_running = running_client(None, 'running-missing', include_running=False)
        missing_error = expect_error(lambda: missing_running.running(SCENE), 'no_running_data')
        check(not missing_error.unknown, 'exact successful empty running response is a known local no-data condition')
        missing_audit = json.loads(next((root / 'running-missing').glob('*.json')).read_text(encoding='utf-8'))
        check(set(missing_audit['sanitizedRawResponse']) == {'RequestId', 'Success'} and
            missing_audit['sanitizedRawResponse']['Success'] is True,
            'successful empty running audit remains original without invented RunningData or provider error')
        for index, (status, request_id) in enumerate(((202, 'request'), (204, 'request'),
                (200, ''), (200, ' '), (200, None), (200, 123))):
            candidate = running_client(None, 'running-empty-boundary-' + str(index),
                include_running=False, request_id=request_id, http_status=status)
            expect_error(lambda: candidate.running(SCENE), 'running_response_invalid')
        for status in (202, 204):
            def empty_body_transport(path, headers, timeout):
                return status, b''
            candidate = api.Client('cn-shanghai', root / ('running-empty-body-' + str(status)), AUTH,
                transport=empty_body_transport, scene_id=SCENE)
            expect_error(lambda: candidate.running(SCENE), 'invalid_provider_json')

        fresh = Fake()
        def fresh_transport(path, headers, timeout):
            if headers['x-acs-action'] == 'GetJMeterSceneRunningData':
                fresh.calls.append((headers['x-acs-action'], parse_qs(urlsplit(path).query)))
                return 200, json.dumps({'Success': True, 'RequestId': 'fresh-scene'}).encode()
            return fresh(path, headers, timeout)
        fresh_client = api.Client('cn-shanghai', root / 'fresh-scene', AUTH,
            transport=fresh_transport, scene_id=SCENE)
        check(fresh_client._idle(SCENE) is None and
            [action for action, _ in fresh.calls] == ['GetOpenJMeterScene', 'GetJMeterSceneRunningData'],
            'fresh scene idle accepts exact no-data only after successful scene read and makes no writes')
        expect_error(lambda: fresh_client.terminal_evidence(SCENE, REPORT), 'no_running_data')
        check(not fresh_client.reports, 'no running data never binds or proves a terminal report')

        def extra_running_transport(path, headers, timeout):
            return 200, json.dumps({'Success': True, 'RequestId': 'extra-data', 'Status': 'RUNNING'}).encode()
        extra_running = api.Client('cn-shanghai', root / 'extra-running-field', AUTH,
            transport=extra_running_transport, scene_id=SCENE)
        expect_error(lambda: extra_running.running(SCENE), 'running_response_invalid')
        def provider_no_data_error(path, headers, timeout):
            if headers['x-acs-action'] == 'GetJMeterSceneRunningData':
                return 400, json.dumps({'Success': False, 'Code': 'no_running_data'}).encode()
            return fresh(path, headers, timeout)
        error_client = api.Client('cn-shanghai', root / 'provider-no-data-error', AUTH,
            transport=provider_no_data_error, scene_id=SCENE)
        expect_error(lambda: error_client._idle(SCENE), 'no_running_data')

        def report_client(overview, label):
            def transport(path, headers, timeout):
                assert headers['x-acs-action'] == 'GetJMeterReportDetails'
                assert parse_qs(urlsplit(path).query)['ReportId'] == [REPORT]
                return 200, json.dumps({'Success': True, 'RequestId': 'report-shape-check',
                    'ReportOverView': overview}).encode()
            client = api.Client('cn-shanghai', root / label, AUTH, transport=transport, scene_id=SCENE)
            client.reports.add(REPORT)
            return client
        overview = {'StartTime': '2026-11-01 12:00:00', 'EndTime': '2026-11-01 12:01:00', 'AgentCount': 2}
        missing_report = report_client(overview, 'report-omitted')
        expect_error(lambda: missing_report.report('UNBOUND_REPORT'), 'report_not_bound_to_this_client')
        normalized_report = missing_report.report(REPORT)
        check(normalized_report['reportId'] == REPORT and normalized_report['ended'] is True and
            normalized_report['reportIdentitySource'] == 'signed-request-ReportId',
            'omitted report ID uses acknowledged signed request without changing EndTime judgement')
        report_audit = json.loads(next((root / 'report-omitted').glob('*.json')).read_text(encoding='utf-8'))
        check('ReportId' not in report_audit['sanitizedRawResponse']['ReportOverView'] and 'ReportId' not in overview,
            'report raw audit preserves provider ID omission')
        for index, value in enumerate(('FOREIGN_REPORT', '', None)):
            candidate = report_client({**overview, 'ReportId': value}, 'report-mismatch-' + str(index))
            expect_error(lambda: candidate.report(REPORT), 'report_identity_differs')
        for index, value in enumerate(({}, None, [], 'not-an-overview')):
            candidate = report_client(value, 'report-invalid-' + str(index))
            expect_error(lambda: candidate.report(REPORT), 'report_response_invalid')

        fake = Fake(); journal = root / 'S1-warm.private.json'; fake.start_journal = journal
        client = api.Client('cn-hangzhou', root / 'audit', AUTH, transport=fake, scene_id=SCENE, exclusive_scene=True)
        expect_error(lambda: client.running('FOREIGN'), 'scene_identity_differs')
        response = client.start(SCENE, journal)
        check(response['reportId'] == REPORT and json.loads(journal.read_text())['state'] == 'START_ACKNOWLEDGED', 'acknowledged start binds report and durable journal')
        if api.os.name != 'nt':
            check(journal.stat().st_mode & 0o077 == 0, 'journal private POSIX mode')
        expect_error(lambda: client.start(SCENE, journal), 'start_intent_exists_never_repeat_start')
        check(sum(a == 'StartTestingJMeterScene' for a, _ in fake.calls) == 1, 'retry cannot issue duplicate start')
        expect_error(lambda: client.report('FOREIGN'), 'report_not_bound_to_this_client')
        check(client.running(SCENE)['status'] == 'RUNNING' and not client.report(REPORT)['ended'], 'running and unfinished report never claim terminal')
        fake.report_id = 'FOREIGN_REPORT'
        expect_error(lambda: client.stop(SCENE, REPORT), 'refuse_to_stop_another_report')
        check(not any(a == 'StopTestingJMeterScene' for a, _ in fake.calls), 'foreign task is never stopped')
        fake.report_id = REPORT
        stopped = client.stop(SCENE, REPORT)
        check(stopped['requested'] and stopped['sceneLevelStopHasRace'] and not stopped['terminalVerified'], 'stop ack is separate from terminal and exposes non-atomic scene limit')
        expect_error(lambda: client.stop(SCENE, REPORT), 'stop_already_attempted_read_reconcile_instead')
        check(client.terminal_evidence(SCENE, REPORT)['terminal'] and client.report(REPORT)['ended'], 'HasReport and matching valid EndTime establish report completion')
        check(client.metrics(REPORT, 1000, 2000)['Success'], 'bound aggregate export uses explicit report and interval')
        check(client.bind_journal(journal) == REPORT, 'positive acknowledgement can be recovered')
        public = ''.join(p.read_text(encoding='utf-8') for p in (root / 'audit').glob('*.json'))
        check(all(secret not in public for secret in (*AUTH, 'oss-secret', 'collector-private-value')), 'response audit redacts credentials signed OSS URL and JMeter control properties')
        check('rawResponseIsSanitized' in public and '1793520000000' in public, 'sanitized raw response retains numeric evidence and explicit scope')
        # Start timeout affects cloud state but must never be retried or guessed.
        unknown = Fake(); unknown.fault = 'start'; unknown.start_journal = root / 'S2.private.json'
        second = api.Client('cn-shanghai', root / 'unknown-audit', AUTH, transport=unknown, exclusive_scene=True)
        error = expect_error(lambda: second.start(SCENE, unknown.start_journal), 'transport_unknown')
        check(error.unknown and json.loads(unknown.start_journal.read_text())['state'] == 'START_UNKNOWN', 'transport failure durably remains unknown')
        expect_error(lambda: second.start(SCENE, unknown.start_journal), 'start_intent_exists_never_repeat_start')
        expect_error(lambda: second.bind_journal(unknown.start_journal), 'journal_has_no_acknowledged_task')
        expect_error(lambda: second.stop(SCENE, REPORT), 'stop_requires_exclusive_acknowledged_report')
        check(sum(a == 'StartTestingJMeterScene' for a, _ in unknown.calls) == 1, 'unknown start not resent or adopted from unrelated status')
        # The actual transport sets a stop during the _idle() reads, rather than
        # replacing _idle() with a mocked implementation.
        idle_stop = Fake(); idle_stop.start_journal = root / 'stop-in-idle.private.json'
        stop = [False]
        def idle_transport(path, headers, timeout):
            result = idle_stop(path, headers, timeout)
            if headers['x-acs-action'] == 'GetJMeterSceneRunningData':
                stop[0] = True
            return result
        cancelled = api.Client('cn-shanghai', root / 'idle-stop-audit', AUTH,
            transport=idle_transport, scene_id=SCENE, exclusive_scene=True)
        expect_error(lambda: cancelled.start(SCENE, idle_stop.start_journal, should_stop=lambda: stop[0]), 'start_cancelled_before_send')
        check(not idle_stop.start_journal.exists() and not any(a == 'StartTestingJMeterScene' for a, _ in idle_stop.calls), 'stop arriving during idle reads prevents intent and all Start calls')
        intent_stop = Fake(); intent_stop.start_journal = root / 'stop-after-intent.private.json'
        stopped = [False]; original_save = api.private_save
        def stop_after_intent(path, value, create=False):
            original_save(path, value, create=create)
            if create:
                stopped[0] = True
        cancelled = api.Client('cn-shanghai', root / 'intent-stop-audit', AUTH,
            transport=intent_stop, scene_id=SCENE, exclusive_scene=True)
        with patch.object(api, 'private_save', stop_after_intent):
            expect_error(lambda: cancelled.start(SCENE, intent_stop.start_journal, should_stop=lambda: stopped[0]), 'start_cancelled_before_send')
        cancelled_state = json.loads(intent_stop.start_journal.read_text())
        check(cancelled_state['state'] == 'START_REJECTED' and cancelled_state['cancelledBeforeSend'] is True and cancelled_state['intentAt'] and cancelled_state['reportId'] is None and not any(a == 'StartTestingJMeterScene' for a, _ in intent_stop.calls), 'stop after durable intent preserves cancelled intent evidence and sends zero Start calls')
        expect_error(lambda: cancelled.start(SCENE, intent_stop.start_journal), 'start_intent_exists_never_repeat_start')
        plain = api.Client('cn-hangzhou', root / 'no-cloud', AUTH)
        expect_error(lambda: plain.running(SCENE), 'cloud_calls_disabled')
        expect_error(lambda: api.Client('localhost', None, AUTH), 'unsupported_official_region')
        expect_error(lambda: plain._call('CreateInstance', {}), 'action_not_allowed')
        check(api.estimate_vum(2, 60)['theoreticalVUM'] == 1000 and api.estimate_vum(2, 10)['currencyAmount'] is None, 'read-only VUM arithmetic never quotes an unverified account price')
        check(not api.capabilities()['terminalStatusEnumVerified'] and not api.capabilities()['atomicReportScopedStop'], 'capability gaps are explicit rather than filled with guesses')
    print(json.dumps({'checks': checks, 'failed': 0, 'cloudCalls': 0, 'servicesStarted': 0, 'loadWindows': 0}))


if __name__ == '__main__':
    main()
