"""Actual client offline checks. All transports are injected; no cloud/dependency."""
import importlib.util
import json
from pathlib import Path
import tempfile
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
        plain = api.Client('cn-hangzhou', root / 'no-cloud', AUTH)
        expect_error(lambda: plain.running(SCENE), 'cloud_calls_disabled')
        expect_error(lambda: api.Client('localhost', None, AUTH), 'unsupported_official_region')
        expect_error(lambda: plain._call('CreateInstance', {}), 'action_not_allowed')
        check(api.estimate_vum(2, 60)['theoreticalVUM'] == 1000 and api.estimate_vum(2, 10)['currencyAmount'] is None, 'read-only VUM arithmetic never quotes an unverified account price')
        check(not api.capabilities()['terminalStatusEnumVerified'] and not api.capabilities()['atomicReportScopedStop'], 'capability gaps are explicit rather than filled with guesses')
    print(json.dumps({'checks': checks, 'failed': 0, 'cloudCalls': 0, 'servicesStarted': 0, 'loadWindows': 0}))


if __name__ == '__main__':
    main()
