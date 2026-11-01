"""Small PTS/2020-10-20 controller; importing it never calls the cloud.

Only https://pts.aliyuncs.com is contacted, using POST RPC and ACS3-HMAC-SHA256.
Credentials: ALIBABA_CLOUD_ACCESS_KEY_ID/SECRET and optional SECURITY_TOKEN,
or those same keys in an explicitly supplied *.private.json (0600 on POSIX).
No environment file is auto-discovered. CLI defaults to offline capabilities.

Minimum RAM actions: pts:GetOpenJMeterScene, pts:GetJMeterSceneRunningData,
pts:StartTestingJMeterScene, pts:StopTestingJMeterScene,
pts:GetJMeterReportDetails, pts:GetJMeterSampleMetrics. Optional scene update:
pts:SaveOpenJMeterScene. These APIs document Resource="*"; use a dedicated RAM
identity and an exclusively owned SceneId. No purchase/create/delete API exists
in this client. File upload itself is NOT a PTS OpenAPI operation here.

Official sources, checked 2026-11-01 (no authenticated API calls):
https://help.aliyun.com/zh/pts/performance-test-pts-3-0/developer-reference/api-reference-1/
https://help.aliyun.com/en/pts/performance-test-pts-3-0/developer-reference/api-pts-2020-10-20-endpoint
https://help.aliyun.com/en/sdk/product-overview/v3-request-structure-and-signature
API pages use the preceding developer-reference prefix +
api-pts-2020-10-20-{starttestingjmeterscene,stoptestingjmeterscene,
getjmeterscenerunningdata,getjmeterreportdetails,getjmetersamplemetrics,
getopenjmeterscene,saveopenjmeterscene}.

Limits: Stop accepts SceneId, not ReportId (check/stop is not atomic). Scene
exclusivity is therefore required for the whole batch. No official complete
terminal Status enumeration was found: an unknown string is never sufficient
to prove completion. terminal_evidence requires HasReport plus this report's
actual EndTime, and is NOT collector/native-window evidence. StartTimeTS is a
planned timestamp; report StartTime/EndTime strings have no documented timezone.
Public report APIs do not establish P95, full JTL or agent process RSS. Raw
aggregate JSON is supplemental; the private collector owns full samples/time.
SaveScene accepts uploaded OSS files (docs currently say Shanghai), Duration
60..86400; a 10s warm must terminate in JMX, not be extended to 60s.
"""
import argparse
import datetime as dt
import hashlib
import hmac
import http.client
import json
import math
import os
from pathlib import Path
import re
import ssl
import time
from urllib.parse import quote
import uuid

ENDPOINT = 'pts.aliyuncs.com'
VERSION = '2020-10-20'
REGIONS = frozenset(('cn-hangzhou', 'cn-shanghai', 'cn-beijing', 'cn-shenzhen',
    'cn-chengdu', 'cn-hongkong', 'cn-huhehaote', 'cn-qingdao', 'cn-zhangjiakou',
    'ap-northeast-1', 'ap-southeast-1', 'ap-southeast-3', 'ap-southeast-5',
    'us-west-1', 'us-east-1', 'eu-west-1', 'eu-central-1', 'me-east-1'))
READ_ACTIONS = frozenset(('GetOpenJMeterScene', 'GetJMeterSceneRunningData',
    'GetJMeterReportDetails', 'GetJMeterSampleMetrics'))
WRITE_ACTIONS = frozenset(('StartTestingJMeterScene', 'StopTestingJMeterScene', 'SaveOpenJMeterScene'))
MAX_RESPONSE = 4 * 1024 * 1024
SENSITIVE = re.compile(r'accesskey|secret|token|signature|authorization|cookie|password|header|url|address|requestdata|responsedata|samplerdata|message|principal|createname|modifyname|canonicalrequest|stringtosign|jmeterproperties|hosttable|remark|agentid', re.I)


class Error(Exception):
    """Contains only a fixed/sanitized code, never a request or provider text."""
    def __init__(self, code, unknown=False):
        self.code = code if re.fullmatch(r'[A-Za-z0-9_.-]{1,100}', str(code)) else 'provider_error'
        self.unknown = unknown
        super().__init__(self.code)


def utc():
    return dt.datetime.now(dt.timezone.utc).isoformat()


def digest(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True, separators=(',', ':'), ensure_ascii=False).encode()).hexdigest()


def identity(value):
    if not isinstance(value, str) or not re.fullmatch(r'[A-Za-z0-9_-]{1,128}', value):
        raise Error('invalid_identity')
    return value


def load_credentials(path=None):
    source = os.environ
    if path is not None:
        path = Path(path).resolve()
        if not path.name.endswith('.private.json') or (os.name != 'nt' and path.stat().st_mode & 0o077):
            raise Error('credentials_require_private_file')
        source = json.loads(path.read_text(encoding='utf-8'))
    values = tuple(source.get('ALIBABA_CLOUD_' + key, '') for key in
                   ('ACCESS_KEY_ID', 'ACCESS_KEY_SECRET', 'SECURITY_TOKEN'))
    if not all(isinstance(value, str) for value in values) or not all(values[:2]):
        raise Error('credentials_unavailable')
    return values


def clean(value, secrets=(), key=''):
    """Preserve response shape/numbers; never archive auth, OSS URLs or bodies."""
    if SENSITIVE.search(key):
        return '[redacted]'
    if isinstance(value, dict):
        return {clean(str(k), secrets): clean(v, secrets, str(k)) for k, v in value.items()}
    if isinstance(value, list):
        return [clean(v, secrets, key) for v in value]
    if isinstance(value, str):
        if value.lstrip().startswith(('{', '[')):
            try:
                return clean(json.loads(value), secrets)
            except (ValueError, TypeError):
                pass
        for secret in secrets:
            if secret:
                value = value.replace(secret, '[redacted]')
        return re.sub(r'https?://\S+', '[redacted-url]', value)
    return value


def signed_request(action, params, auth, timestamp=None, nonce=None,
                   host=ENDPOINT, version=VERSION):
    """Pure ACS3 signing; host override is for the official offline vector only."""
    query = '&'.join(quote(str(k), safe='-_.~') + '=' + quote(str(v), safe='-_.~')
                     for k, v in sorted(params.items()))
    headers = {'host': host, 'x-acs-action': action, 'x-acs-version': version,
               'x-acs-date': timestamp or dt.datetime.now(dt.timezone.utc).strftime('%Y-%m-%dT%H:%M:%SZ'),
               'x-acs-signature-nonce': nonce or uuid.uuid4().hex,
               'x-acs-content-sha256': hashlib.sha256(b'').hexdigest()}
    if auth[2]:
        headers['x-acs-security-token'] = auth[2]
    signed = ';'.join(sorted(headers))
    canonical_headers = ''.join(k + ':' + headers[k].strip() + '\n' for k in sorted(headers))
    canonical = '\n'.join(('POST', '/', query, canonical_headers, signed, headers['x-acs-content-sha256']))
    to_sign = 'ACS3-HMAC-SHA256\n' + hashlib.sha256(canonical.encode()).hexdigest()
    signature = hmac.new(auth[1].encode(), to_sign.encode(), hashlib.sha256).hexdigest()
    headers['authorization'] = 'ACS3-HMAC-SHA256 Credential=' + auth[0] + ',SignedHeaders=' + signed + ',Signature=' + signature
    headers['accept'] = 'application/json'
    return '/?' + query, headers


def https_transport(path, headers, timeout):
    """Direct fixed-host HTTPS, no proxies, redirects, SDK retries or URL logs."""
    deadline = time.monotonic() + timeout
    connection = http.client.HTTPSConnection(ENDPOINT, timeout=timeout, context=ssl.create_default_context())
    try:
        connection.connect()
        connection.sock.settimeout(max(.01, deadline - time.monotonic()))
        connection.request('POST', path, body=b'', headers=headers)
        connection.sock.settimeout(max(.01, deadline - time.monotonic()))
        response = connection.getresponse()
        parts, size = [], 0
        while True:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise TimeoutError()
            if connection.sock is not None:
                connection.sock.settimeout(remaining)
            chunk = response.read1(min(65536, MAX_RESPONSE + 1 - size))
            if not chunk:
                break
            parts.append(chunk); size += len(chunk)
            if size > MAX_RESPONSE:
                raise Error('response_too_large', unknown=True)
        return response.status, b''.join(parts)
    finally:
        connection.close()


def private_save(path, value, create=False):
    path = Path(path)
    if not path.name.endswith('.private.json') or not path.parent.is_dir():
        raise Error('journal_path_not_private')
    data = (json.dumps(value, indent=2) + '\n').encode()
    target = path if create else path.with_name(path.name + '.' + uuid.uuid4().hex + '.pending')
    descriptor = os.open(target, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    try:
        with os.fdopen(descriptor, 'wb') as stream:
            stream.write(data); stream.flush(); os.fsync(stream.fileno())
        if not create:
            os.replace(target, path)
    finally:
        if not create and target.exists():
            target.unlink()


class Client:
    def __init__(self, region_id, evidence_dir, credentials=None, *, scene_id=None,
                 credential_file=None, transport=None, allow_live=False, exclusive_scene=False):
        if region_id not in REGIONS:
            raise Error('unsupported_official_region')
        self.region = region_id; self.scene_id = identity(scene_id) if scene_id else None
        if isinstance(credentials, dict):
            credentials = tuple(credentials.get('ALIBABA_CLOUD_' + name, '') for name in
                                ('ACCESS_KEY_ID', 'ACCESS_KEY_SECRET', 'SECURITY_TOKEN'))
        self.auth = credentials if credentials is not None else load_credentials(credential_file)
        if not isinstance(self.auth, (tuple, list)) or len(self.auth) != 3 or not all(isinstance(v, str) and '\n' not in v and '\r' not in v for v in self.auth) or not all(self.auth[:2]):
            raise Error('invalid_credentials')
        self.transport = transport if transport is not None else https_transport
        self.allow_cloud = allow_live; self.exclusive = exclusive_scene
        self.audit_dir = Path(evidence_dir) if evidence_dir else None
        if self.audit_dir:
            self.audit_dir.mkdir(parents=True, exist_ok=True)
        self.stops = {}; self.reports = set()

    def _audit(self, action, data):
        if self.audit_dir:
            path = self.audit_dir / (str(time.time_ns()) + '-' + uuid.uuid4().hex[:8] + '-' + action + '.json')
            with path.open('x', encoding='utf-8') as stream:
                json.dump(clean(data, self.auth), stream, ensure_ascii=False, indent=2)

    def _call(self, action, params):
        if action not in READ_ACTIONS | WRITE_ACTIONS:
            raise Error('action_not_allowed')
        if self.transport is https_transport and not self.allow_cloud:
            raise Error('cloud_calls_disabled')
        for attempt in range(2 if action in READ_ACTIONS else 1):
            start = utc(); began = time.monotonic()
            path, headers = signed_request(action, {'RegionId': self.region, **params}, self.auth)
            try:
                status, raw = self.transport(path, headers, 5)
                if not isinstance(raw, bytes) or len(raw) > MAX_RESPONSE:
                    raise Error('response_too_large', unknown=True)
                try:
                    data = json.loads(raw)
                except (ValueError, UnicodeError):
                    raise Error('invalid_provider_json', unknown=True) from None
                self._audit(action, {'action': action, 'regionId': self.region, 'sceneId': self.scene_id,
                    'attempt': attempt + 1, 'requestStartedAt': start, 'responseFinishedAt': utc(),
                    'elapsedSeconds': time.monotonic() - began, 'httpStatus': status,
                    'sanitizedRawResponse': data, 'rawResponseIsSanitized': True})
                if not isinstance(data, dict) or not 200 <= status < 300 or data.get('Success') is not True:
                    code = data.get('Code', 'provider_error') if isinstance(data, dict) else 'provider_error'
                    raise Error(clean(code, self.auth), unknown=status >= 500 or status == 429)
                return data
            except (OSError, http.client.HTTPException) as error:
                self._audit(action, {'action': action, 'attempt': attempt + 1, 'requestStartedAt': start,
                    'responseFinishedAt': utc(), 'errorClass': type(error).__name__, 'detailsSuppressed': True})
                failure = Error('transport_unknown', unknown=True)
            except Error as error:
                failure = error
            if action in WRITE_ACTIONS or not failure.unknown or attempt == 1:
                raise failure from None
            time.sleep(.1)

    def _scene(self, scene_id):
        scene_id = identity(scene_id)
        if self.scene_id is None:
            self.scene_id = scene_id
        if scene_id != self.scene_id:
            raise Error('scene_identity_differs')

    def scene(self, scene_id):
        self._scene(scene_id)
        response = self._call('GetOpenJMeterScene', {'SceneId': scene_id})
        if response.get('Scene', {}).get('SceneId') != scene_id:
            raise Error('scene_identity_differs')
        return response

    def running(self, scene_id):
        self._scene(scene_id)
        response = self._call('GetJMeterSceneRunningData', {'SceneId': scene_id})
        if response.get('RunningData', {}).get('SceneId') != scene_id:
            raise Error('scene_identity_differs')
        data = response['RunningData']
        return {'sceneId': scene_id, 'reportId': data.get('ReportId'), 'status': data.get('Status'),
            'hasReport': data.get('HasReport'), 'hasError': data.get('HasError'),
            'startTimeTS': data.get('StartTimeTS'), 'startTimeIsPlanned': True,
            'agentCount': data.get('AgentCount'), 'holdFor': data.get('HoldFor'),
            'nativeWindowVerified': False, 'requestId': response.get('RequestId')}

    def report(self, report_id):
        report_id = identity(report_id)
        if report_id not in self.reports:
            raise Error('report_not_bound_to_this_client')
        response = self._call('GetJMeterReportDetails', {'ReportId': report_id})
        if response.get('ReportOverView', {}).get('ReportId') != report_id:
            raise Error('report_identity_differs')
        overview = response['ReportOverView']
        start, end = overview.get('StartTime'), overview.get('EndTime')
        try:
            ended = isinstance(start, str) and isinstance(end, str) and len(start) >= 19 and len(end) >= 19 and dt.datetime.fromisoformat(end) >= dt.datetime.fromisoformat(start)
        except (ValueError, TypeError):
            ended = False
        return {'reportId': report_id, 'ended': ended, 'startTimeRaw': start, 'endTimeRaw': end,
            'timeZoneVerified': False, 'nativeWindowVerified': False,
            'agentCount': overview.get('AgentCount'), 'vum': overview.get('Vum'),
            'requestId': response.get('RequestId')}

    def terminal_evidence(self, scene_id, report_id):
        running = self.running(scene_id)
        if running.get('reportId') != report_id:
            raise Error('current_report_identity_differs')
        # The official Status schema has no complete enum. Do not guess one.
        if running.get('hasReport') is not True or not isinstance(running.get('status'), str) or not running['status'] or running.get('status') == 'RUNNING':
            return {'terminal': False, 'nativeWindowVerified': False}
        self.reports.add(identity(report_id))
        report = self.report(report_id)
        return {'terminal': report['ended'], 'reportId': report_id, 'status': running.get('status'),
            'reportedEndTimeRaw': report.get('endTimeRaw'), 'endTimeZoneVerified': False,
            'nativeWindowVerified': False, 'basis': 'HasReport plus matching report EndTime; not a documented Status enum'}

    def _idle(self, scene_id):
        self.scene(scene_id)
        try:
            running = self.running(scene_id)
        except Error as error:
            if error.code == 'ReportNotExist' and not error.unknown:
                return
            raise
        report_id = identity(running.get('reportId'))
        if not self.terminal_evidence(scene_id, report_id)['terminal']:
            raise Error('scene_not_proven_idle')

    def start(self, scene_id, journal_path, should_stop=None):
        self._scene(scene_id)
        if not self.exclusive:
            raise Error('exclusive_scene_required_for_scene_level_stop')
        if should_stop is not None and not callable(should_stop):
            raise Error('invalid_start_stop_callback')

        def check_stop():
            if should_stop is not None:
                try:
                    stopped = should_stop()
                except Exception:
                    raise Error('start_stop_check_failed') from None
                if stopped:
                    raise Error('start_cancelled_before_send')

        journal_path = Path(journal_path)
        if journal_path.exists():
            raise Error('start_intent_exists_never_repeat_start')
        self._idle(scene_id)
        check_stop()  # Read-only idle checks can block while an owned stop arrives.
        state = {'version': 1, 'action': 'StartTestingJMeterScene', 'sceneId': scene_id,
            'regionId': self.region, 'intentAt': utc(), 'state': 'START_INTENT', 'reportId': None,
            'startRetriesAllowed': False, 'sceneLevelStopHasRace': True}
        private_save(journal_path, state, create=True)
        try:
            check_stop()  # Durable intent does not authorize sending after a stop.
            response = self._call('StartTestingJMeterScene', {'SceneId': scene_id})
            report_id = identity(response.get('ReportId'))
            self.reports.add(report_id)
            state.update(state='START_ACKNOWLEDGED', reportId=report_id, acknowledgedAt=utc())
            private_save(journal_path, state)
            return {'reportId': report_id, 'sceneId': scene_id, 'requestId': response.get('RequestId'),
                    'acknowledged': True, 'nativeWindowVerified': False}
        except BaseException as error:
            if isinstance(error, Error) and error.code == 'start_cancelled_before_send':
                state['cancelledBeforeSend'] = True
            if state.get('reportId') in self.reports:
                try:
                    self.stop(scene_id, state['reportId'])
                except (Error, OSError):
                    pass
            state.update(state='START_UNKNOWN' if not isinstance(error, Error) or error.unknown or error.code == 'invalid_identity' else 'START_REJECTED',
                         failedAt=utc(), failureClass=type(error).__name__)
            private_save(journal_path, state)
            raise

    def bind_journal(self, path):
        """Recover only a persisted positive start acknowledgement, never guess."""
        state = json.loads(Path(path).read_text(encoding='utf-8'))
        if state.get('state') != 'START_ACKNOWLEDGED' or state.get('regionId') != self.region or state.get('sceneId') != self.scene_id:
            raise Error('journal_has_no_acknowledged_task')
        self.reports.add(identity(state.get('reportId')))
        return state['reportId']

    def stop(self, scene_id, expectedReportId):
        self._scene(scene_id); expectedReportId = identity(expectedReportId)
        if not self.exclusive or expectedReportId not in self.reports:
            raise Error('stop_requires_exclusive_acknowledged_report')
        if expectedReportId in self.stops:
            raise Error('stop_already_attempted_read_reconcile_instead')
        current = self.running(scene_id)
        if current.get('reportId') != expectedReportId:
            raise Error('refuse_to_stop_another_report')
        if self.terminal_evidence(scene_id, expectedReportId)['terminal']:
            return {'requested': False, 'alreadyTerminal': True, 'reportId': expectedReportId}
        self.stops[expectedReportId] = 'STOP_INTENT'
        try:
            result = self._call('StopTestingJMeterScene', {'SceneId': scene_id})
            self.stops[expectedReportId] = 'STOP_ACKNOWLEDGED'
            return {'requested': True, 'reportId': expectedReportId, 'requestId': result.get('RequestId'), 'sceneLevelStopHasRace': True,
                    'terminalVerified': False}
        except BaseException:
            self.stops[expectedReportId] = 'STOP_UNKNOWN'
            raise

    def wait_terminal(self, scene_id, expectedReportId, timeout=90, interval=2, stop_file=None):
        if not 1 <= timeout <= 360 or not 1 <= interval <= 5:
            raise Error('poll_budget_invalid')
        deadline = time.monotonic() + timeout
        try:
            while time.monotonic() < deadline:
                if stop_file is not None and Path(stop_file).exists() and expectedReportId not in self.stops:
                    self.stop(scene_id, expectedReportId)
                evidence = self.terminal_evidence(scene_id, expectedReportId)
                if evidence['terminal']:
                    return evidence
                time.sleep(min(interval, max(0, deadline - time.monotonic())))
            raise Error('terminal_poll_deadline')
        except BaseException:
            if expectedReportId not in self.stops:
                try:
                    self.stop(scene_id, expectedReportId)
                except (Error, OSError):
                    pass  # Original failure remains; audit retains stop failure.
            raise

    def metrics(self, report_id, begin_ms, end_ms, sampler_id=-1):
        if report_id not in self.reports or not all(type(x) is int for x in (begin_ms, end_ms, sampler_id)) or not 0 <= begin_ms < end_ms or sampler_id < -1:
            raise Error('metrics_identity_or_window_invalid')
        return self._call('GetJMeterSampleMetrics', {'ReportId': report_id, 'BeginTime': begin_ms,
            'EndTime': end_ms, 'SamplerId': sampler_id})

    def update_scene(self, scene, journal_path):
        """Update the bound scene only; caller supplies PRIVATE uploaded OSS URLs."""
        self._scene(scene.get('SceneId'))
        if not self.exclusive or type(scene.get('Duration')) is not int or not 60 <= scene['Duration'] <= 86400:
            raise Error('scene_update_contract_invalid')
        self._idle(self.scene_id)
        state = {'action': 'SaveOpenJMeterScene', 'sceneId': self.scene_id, 'regionId': self.region,
            'intentAt': utc(), 'state': 'UPDATE_INTENT', 'payloadSHA256': digest(scene), 'retriesAllowed': False}
        private_save(journal_path, state, create=True)
        try:
            response = self._call('SaveOpenJMeterScene', {'OpenJMeterScene': json.dumps(scene, separators=(',', ':'))})
            if response.get('SceneId') != self.scene_id:
                raise Error('scene_identity_differs', unknown=True)
            state.update(state='UPDATE_ACKNOWLEDGED', acknowledgedAt=utc())
            private_save(journal_path, state)
            return response
        except BaseException:
            state.update(state='UPDATE_UNKNOWN', failedAt=utc())
            private_save(journal_path, state)
            raise


def estimate_vum(agent_count, task_seconds, sampling_factor=1):
    if type(agent_count) is not int or agent_count < 1 or type(task_seconds) is not int or task_seconds < 1 or not math.isfinite(sampling_factor) or sampling_factor < 1:
        raise Error('estimate_parameters_invalid')
    return {'theoreticalVUM': agent_count * 500 * task_seconds / 60 * sampling_factor,
        'actualBillUnknown': True, 'currencyAmount': None,
        'basis': 'IPs x500 x task minutes x sampling factor; default 1% sampling factor=1. Actual engine VUM/rounding/account pricing prevail; no purchase or bill API call.'}


def capabilities():
    return {'version': VERSION, 'endpoint': 'https://' + ENDPOINT, 'signature': 'ACS3-HMAC-SHA256',
        'minimumRAMActions': ['pts:' + action for action in sorted(READ_ACTIONS | WRITE_ACTIONS - {'SaveOpenJMeterScene'})],
        'optionalUpdateRAMAction': 'pts:SaveOpenJMeterScene', 'cloudCalled': False,
        'fullJTLAvailableViaThisAPI': False, 'p95AvailableViaReportDetails': False,
        'agentProcessRSSAvailableViaThisAPI': False, 'terminalStatusEnumVerified': False,
        'atomicReportScopedStop': False, 'collectorRequiredForNativeWindowAndSamples': True,
        'noSceneCreateUploadOrPurchase': True}


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description='Offline PTS capabilities/cost arithmetic. Live orchestration imports Client; this CLI cannot start cloud tasks.')
    parser.add_argument('--estimate-agents', type=int)
    parser.add_argument('--estimate-seconds', type=int)
    arguments = parser.parse_args()
    result = capabilities()
    if arguments.estimate_agents is not None:
        result['estimate'] = estimate_vum(arguments.estimate_agents, arguments.estimate_seconds)
    print(json.dumps(result, ensure_ascii=False))
