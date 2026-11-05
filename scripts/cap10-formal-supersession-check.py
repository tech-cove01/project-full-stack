"""Focused synthetic failed-formal-group checks; no API, SQL or processes."""
import copy
import datetime as dt
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

REPO = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location('formal_supersession_grade', REPO / 'scripts/cap10-grade.py')
grade = importlib.util.module_from_spec(spec); spec.loader.exec_module(grade)


class Checks(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='.runtime-formal-group-check-', dir=REPO.parent)
        self.base = Path(self.temp.name).resolve()
        assert self.base.parent == REPO.parent and self.base.name.startswith('.runtime-formal-group-check-')
        self.old_root = grade.ROOT; grade.ROOT = self.base
        self.addCleanup(self.cleanup)
        self.root = self.base / 'linux'; self.root.mkdir()
        self.clock = dt.datetime.now(dt.timezone.utc) - dt.timedelta(minutes=10)
        self.t = lambda n: (self.clock + dt.timedelta(seconds=n)).isoformat()
        self.ms = lambda n: int((self.clock + dt.timedelta(seconds=n)).timestamp() * 1000)
        self.dims = {'poolSize': 2, 'expectedEngines': 1, 'ptsConcurrency': 3}
        self.fixture = {'poolSize': 2, 'multiNoteIds': ['9', '10'], 'userIds': ['19', '20']}
        fixture = self.put('fixture.json', self.fixture)
        self.put('admission-old.json', {'sceneId': 'SYNTHETIC_SCENE'})
        self.containers = {name: {'id': str(i) * 64, 'name': name, 'project': 'synthetic', 'service': name,
            'running': True, 'status': 'running', 'healthStatus': 'healthy', 'exitCode': 0, 'oomKilled': False,
            'restartCount': 0, 'cpuNano': cpu, 'memoryBytes': memory,
            'memorySwap': 2 * memory, 'imageId': 'synthetic-' + name, 'volumes': ['preserved'],
            'networks': {'owned': {}}, 'ports': {}, 'mounts': [
                {'Type': 'volume', 'Name': name + '-data', 'Destination': '/data', 'RW': True},
                {'Type': 'bind', 'Source': '/synthetic/config', 'Destination': '/config', 'RW': False}], 'policy': {}}
            for i, (name, (cpu, memory)) in enumerate(grade.RESOURCE.container_limits('mysql8-v1').items(), 1)}
        mysql = {'serverUUID': '11111111-1111-1111-1111-111111111111', 'version': '8.4.8', 'flushAtCommit': 1,
            'syncBinlog': 1, 'groupCommitDelay': 0, 'isolation': 'REPEATABLE-READ', 'dedicatedServer': 0,
            'logBin': 1, 'doublewrite': 'ON', 'readOnly': 0, 'superReadOnly': 0, 'sqlMode': 'STRICT_TRANS_TABLES',
            'instances': 1, 'chunkBytes': 134217728}
        self.old_meta = {'at': self.t(1), 'resourceProfile': 'mysql8-v1',
            'bootIdentity': {'bootID': 'synthetic-boot', 'bootTimeUnix': self.clock.timestamp() - 1000},
            'containers': self.containers, 'mysqlActual': mysql, 'redoActual': {'retained': True}}
        old_env = self.put('environment-111111111111.json', self.old_meta)
        self.old_active = {'version': 1, 'batch': 'CAP-10', 'sourceSha': 'a' * 40, 'binarySHA256': 'b' * 64,
            'environmentPath': old_env.name, 'environmentSHA256': grade.sha(old_env), 'resourceProfile': 'mysql8-v1',
            'fixturePath': fixture.name, 'fixtureSHA256': grade.sha(fixture), 'scriptHashes': {'synthetic': 'c' * 64},
            'observerBinarySHA256': 'd' * 64, 'ptsPacketSHA256': 'e' * 64, 'ptsAdmissionSHA256': grade.sha(self.root / 'admission-old.json'),
            'ptsAdmissionPath': 'admission-old.json', 'deploymentId': 'old-deployment',
            'deployment': {'relayWorkers': 8, 'gomaxprocs': 8, 'mysqlMaxOpen': 128}, **self.dims}
        old_active_path = self.put('old-active.json', self.old_active); old_active_sha = grade.sha(old_active_path)
        self.put('active-applications/' + old_active_sha + '.json', self.old_active)
        self.old_freeze = {**self.old_active, 'rate': 3010, 'activeApplicationSHA256': old_active_sha}
        self.old_freeze_sha = grade.sha(self.put('freeze-like.json', self.old_freeze))
        self.put('freeze-history/' + self.old_freeze_sha + '.json', self.old_freeze)
        self.command = {'task': 'CAP-10', 'tag': 'H2', 'kind': 'formal', 'seconds': 180, 'rate': 3010,
            'warmRate': 25, 'warmSeconds': 10, 'at': self.t(20), 'loadBackend': 'pts',
            'apiSourceSha': self.old_active['sourceSha'], 'freezeSHA256': self.old_freeze_sha,
            **{key: self.old_active[key] for key in ('binarySHA256', 'environmentSHA256', 'fixtureSHA256',
                'scriptHashes', 'observerBinarySHA256', 'ptsPacketSHA256', 'ptsAdmissionSHA256', 'environmentPath', 'resourceProfile')},
            **self.dims, 'reservationCheck': {'activeApplicationSHA256': old_active_sha,
                'executionAuthoritySHA256': 'f' * 64, 'executionAuthorityGeneration': 1, 'deploymentId': 'old-deployment'}}
        self.stage = {'passed': False, 'outcome': 'correctness_stop', 'batchFatal': True, 'childrenConverged': True,
            'finishedAt': self.t(60), 'failedStartOrException': None, 'observerExit': 1, 'wrapperExit': 1,
            'knownOwnedProcesses': [{'pid': n, 'created': n + .5} for n in (11, 12, 13, 14)]}
        self.put('H2-command.json', self.command); self.put('H2-stage.json', self.stage)
        self.put('H2-load/result.json', {'nativeScenario': {'complete': False}, 'finalReconcileValid': True,
            'finalFactsValid': True, 'downstreamConverged': True, 'unknown': None})
        reports, audits, processes = [], {}, {}
        for phase, pid, began in (('warm', 11, 21), ('stable', 12, 32)):
            rid = 'SYNTHETIC_' + phase
            start = self.put(f'H2-load/{phase}-pts/api/001-StartTestingJMeterScene.json', {'action': 'StartTestingJMeterScene',
                'sceneId': 'SYNTHETIC_SCENE', 'regionId': 'cn-shanghai', 'httpStatus': 200,
                'requestStartedAt': self.t(began), 'responseFinishedAt': self.t(began + .1),
                'sanitizedRawResponse': {'Success': True, 'ReportId': rid}})
            report = {'phase': phase, 'reportId': rid, 'ended': True, 'requestId': 'query-' + phase,
                'startTimeRaw': self.t(began), 'endTimeRaw': self.t(began + 10), 'originalStartAudit': self.ref(start)}
            reports.append(report)
            audit = self.put(f'closure/{phase}-GetJMeterReportDetails.json', {'action': 'GetJMeterReportDetails',
                'sceneId': 'SYNTHETIC_SCENE', 'regionId': 'cn-shanghai', 'httpStatus': 200,
                'requestStartedAt': self.t(61), 'responseFinishedAt': self.t(62),
                'sanitizedRawResponse': {'Success': True, 'RequestId': report['requestId'], 'ReportOverView': {
                    'ReportId': rid, 'StartTime': report['startTimeRaw'], 'EndTime': report['endTimeRaw']}}})
            audits[phase] = self.ref(audit)
            processes[phase] = self.ref(self.put(f'H2-load/{phase}-process.json', {'phase': phase, 'backend': 'pts', 'controller_pid': pid}))
        self.terminal = {'tag': 'H2', 'reports': reports, 'originalStageSHA256': grade.sha(self.root / 'H2-stage.json'),
            'owners': [{**row, 'alive': False, 'identityChecked': True} for row in self.stage['knownOwnedProcesses']],
            'observerAlive': False, 'controllerAlive': False, 'wrapperAlive': False}
        terminal = self.put('closure/termination-H2.json', self.terminal)
        journal = self.root / 'H2-load/stable-pts/records.ndjson'; journal.parent.mkdir(parents=True, exist_ok=True)
        journal.write_text(json.dumps({'kind': 'configuration', 'configuration': {'phase': 'stable', 'rate': 3010,
            'seconds': 180, 'fixtureSHA256': self.command['fixtureSHA256'], **self.dims}}) + '\n' +
            json.dumps({'kind': 'batch', 'records': [{'ordinal': 0}]}) + '\n', encoding='utf-8')
        self.audit = {'valid': True, 'factsValid': True, 'downstreamConverged': True, 'errors': [],
            'startedAt': self.ms(70), 'finishedAt': self.ms(80), 'facts': {
                'fixtureManifestSHA256': self.command['fixtureSHA256'],
                'observerPolicy': grade.OBSERVER.environment_policy({'CAP10_OBSERVER_MODE': 'like-vector-v1'}),
                'auditContract': grade.AUDIT_CONTRACT, 'facts': [
                    {'noteId': '9', 'userId': '19', 'active': 0, 'version': 2},
                    {'noteId': '10', 'userId': '20', 'active': 0, 'version': 0}],
                'notes': [{'noteId': n, 'liked': 0, 'activeRelations': 0} for n in ('9', '10')],
                'eventCount': 2, 'transitions': 2, 'events': [{'count': 2, 'invalid': 0}],
                'delivery': [{'status': 'DONE', 'count': 2}],
                'receipts': [{'projection': p, 'state': 'IGNORED', 'count': 2, 'invalidHash': 0,
                    'invalidProjection': 0, 'invalidState': 0} for p in ('follow-v1', 'feed-v1', 'search-v1')],
                'parked': [], 'controlRows': {}}, 'derived': {'available': True, 'errors': [],
                    'notes': [{'noteId': n} for n in ('9', '10')]},
            'timeline': [{'pending': 0, 'parked': 0, 'receiptErrors': []}]}
        self.put('closure/reconcile.json', self.audit)
        parent = self.ref(old_env); before = {'at': self.t(81), 'bootIdentity': {'bootId': 'synthetic-boot'},
            'parentEnvironment': parent, 'containers': copy.deepcopy(self.containers),
            'processIdentity': {name: {'pid': 100 + i, 'startedAt': self.t(-10), 'created': self.t(-1000),
                'restartCount': 0, 'oomKilled': False} for i, name in enumerate(self.containers)},
            'volumes': ['retained'], 'network': {'retained': True}, 'mysqlFacts': {**mysql, 'bufferBytes': 4 << 30,
                'resizeCode': 0, 'resizeText': 'complete', 'resizeProgress': 100, 'uptime': 100,
                'otherSessions': 0, 'activeTransactions': 0}, 'mysqlRedo': {'serverUUID': mysql['serverUUID'],
                    'mysqlRedoCapacityBytes': 2 << 30, 'mysqlRedoEffectiveCapacityBytes': 2 << 30, 'mysqlRedoResizeStatus': 'OK'}}
        after = copy.deepcopy(before); after['at'] = self.t(82)
        after['mysqlFacts']['uptime'] = 101
        after['containers']['kafka'].update(memoryBytes=2 << 30, memorySwap=3 << 30)
        before_ref = self.ref(self.put('resource-kafka/before.json', before)); after_ref = self.ref(self.put('resource-kafka/actual-001.json', after))
        self.verified = {'complete': True, 'runtimeOnly': True, 'noRestart': True, 'noLoad': True,
            'bootId': 'synthetic-boot', 'bootIdentity': {'bootId': 'synthetic-boot'}, 'containerId': self.containers['kafka']['id'],
            'parentEnvironment': parent, 'before': before_ref, 'after': after_ref,
            'fromProfile': 'mysql8-v1', 'toProfile': 'mysql8-kafka2-v1', 'memoryBeforeBytes': 1 << 30,
            'memoryBytes': 2 << 30, 'memorySwapBeforeBytes': 2 << 30, 'memorySwapBytes': 3 << 30,
            'swapPolicy': 'preserve-extra-swap-bytes-v1', 'swapAllowanceBytes': 1 << 30}
        proof_ref = self.ref(self.put('resource-kafka/verified.json', self.verified))
        new_meta = copy.deepcopy(self.old_meta); new_meta.update(at=self.t(83), resourceProfile='mysql8-kafka2-v1')
        new_meta['containers']['kafka'].update(memoryBytes=2 << 30, memorySwap=3 << 30)
        new_meta['kafkaMemoryActual'] = {'containerId': self.containers['kafka']['id'], 'memoryBytes': 2 << 30,
            'memorySwapBytes': 3 << 30, 'bootIdentity': {'bootId': 'synthetic-boot'}, 'parentEnvironment': parent,
            'before': before_ref, 'after': after_ref, 'proof': proof_ref,
            'swapPolicy': 'preserve-extra-swap-bytes-v1', 'swapAllowanceBytes': 1 << 30}
        self.new_meta_path = self.put('environment-222222222222.json', new_meta)
        self.active = {**self.old_active, 'deploymentId': 'new-kafka2-deployment', 'resourceProfile': 'mysql8-kafka2-v1',
            'environmentPath': self.new_meta_path.name, 'environmentSHA256': grade.sha(self.new_meta_path)}
        self.put('active-application.json', self.active)
        self.context = {'active': self.active, 'activeApplicationSHA256': grade.sha(self.root / 'active-application.json'),
            'authoritySHA256': 'f' * 64, 'authority': {'generation': 1}}
        self.proof = {'version': 1, 'batch': 'CAP-10', 'kind': 'closed_failed_formal_group', 'authorized': True,
            'userAuthorizationReference': 'synthetic explicit continued stable3000 authorization', 'recordedAt': self.t(90),
            'originalFreeze': {'path': 'freeze-history/' + self.old_freeze_sha + '.json', 'sha256': self.old_freeze_sha},
            'successorDeploymentId': self.active['deploymentId'], 'successorEnvironment': self.ref(self.new_meta_path),
            'resourceChangeProof': proof_ref, 'members': [{'tag': 'H2', 'command': self.ref(self.root / 'H2-command.json'),
                'stage': self.ref(self.root / 'H2-stage.json'), 'result': self.ref(self.root / 'H2-load/result.json'),
                'terminal': self.ref(terminal), 'reportAudits': audits, 'processes': processes,
                'nativeJournal': self.ref(journal)}], 'reconciliation': self.ref(self.root / 'closure/reconcile.json')}
        self.proof_path = self.put('formal-group-supersessions/' + self.old_freeze_sha + '-' + self.active['deploymentId'] + '.json', self.proof)
        (self.base / 'environment-transition.json').write_text(json.dumps({'recordedAt': self.t(-600),
            'effectiveWorkSecondsUsedBeforeSwitch': 7800, 'effectiveWorkSecondsRemaining': 3000}), encoding='utf-8')

    def cleanup(self):
        grade.ROOT = self.old_root; self.temp.cleanup()

    def put(self, name, value):
        path = self.root / name; path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(value), encoding='utf-8'); return path

    def ref(self, path):
        return {'path': path.relative_to(self.root).as_posix(), 'sha256': grade.sha(path)}

    def validate(self):
        return grade.failed_formal_group_supersession(self.root, self.old_freeze_sha, self.context)

    def test_closed_native_failure_is_not_retirement_or_capacity_and_needs_no_future_candidate(self):
        original = {p: p.read_bytes() for p in self.root.rglob('*') if p.is_file()}
        retired_path = self.put('formal-start-recovery-H2.json', {'version': 1, 'batch': 'CAP-10',
            'authorized': True, 'tag': 'H2', 'recordedAt': self.proof['recordedAt'],
            'userAuthorizationReference': self.proof['userAuthorizationReference'],
            'successorActiveApplicationSHA256': self.context['activeApplicationSHA256'],
            'terminal': self.proof['members'][0]['terminal']})
        with self.assertRaises(ValueError):
            grade.formal_start_retirement(self.root, self.command, self.context)
        retired_path.unlink()
        value = self.validate()
        self.assertEqual(value['kind'], 'closed_failed_formal_group')
        self.assertNotIn('passed', value); self.assertNotIn('retiredFormalStarts', value)
        self.assertEqual(value['tags'], ['H2'])
        self.assertTrue(all(p.read_bytes() == raw for p, raw in original.items()))
        self.assertFalse(any(self.root.glob('S*-command.json')))

    def test_rejects_missing_or_resealed_wrong_signed_report_and_live_owner(self):
        for relative, mutate in (
            ('closure/stable-GetJMeterReportDetails.json', lambda v: v['sanitizedRawResponse']['ReportOverView'].pop('ReportId')),
            ('closure/stable-GetJMeterReportDetails.json', lambda v: v['sanitizedRawResponse']['ReportOverView'].update(EndTime='')),
            ('closure/termination-H2.json', lambda v: v['owners'][0].update(alive=True)),
            ('closure/termination-H2.json', lambda v: v.update(owners=v['owners'][1:])),
        ):
            path = self.root / relative; raw = path.read_bytes(); value = json.loads(raw); mutate(value); self.put(relative, value)
            proof = copy.deepcopy(self.proof)
            key = 'terminal' if 'termination' in relative else 'reportAudits'
            if key == 'terminal': proof['members'][0][key] = self.ref(path)
            else: proof['members'][0][key]['stable'] = self.ref(path)
            self.put(self.proof_path.relative_to(self.root).as_posix(), proof)
            with self.subTest(relative=relative), self.assertRaises((ValueError, KeyError)): self.validate()
            path.write_bytes(raw)
        self.put(self.proof_path.relative_to(self.root).as_posix(), self.proof)

    def test_rejects_fact_subset_stale_audit_wrong_pairs_and_native_zero(self):
        for mutate in (lambda v: v['facts'].pop('auditContract'), lambda v: v.update(startedAt=self.ms(59)),
                       lambda v: v['facts']['facts'][0].update(userId='999'),
                       lambda v: v['facts']['receipts'][0].update(invalidHash=1)):
            value = copy.deepcopy(self.audit); mutate(value); path = self.put('closure/reconcile.json', value)
            proof = {**self.proof, 'reconciliation': self.ref(path)}
            self.put(self.proof_path.relative_to(self.root).as_posix(), proof)
            with self.assertRaises((ValueError, KeyError)): self.validate()
        self.put('closure/reconcile.json', self.audit); self.put(self.proof_path.relative_to(self.root).as_posix(), self.proof)
        path = self.root / self.proof['members'][0]['nativeJournal']['path']; path.write_text('{"kind":"configuration","configuration":{}}\n', encoding='utf-8')
        proof = copy.deepcopy(self.proof); proof['members'][0]['nativeJournal'] = self.ref(path)
        self.put(self.proof_path.relative_to(self.root).as_posix(), proof)
        with self.assertRaises(ValueError): self.validate()

    def test_resource_change_must_be_exact_and_successor_must_differ(self):
        for mutate in (lambda v: v.update(successorDeploymentId='old-deployment'),
                       lambda v: v.update(successorEnvironment=self.proof['originalFreeze']),
                       lambda v: v.update(resourceChangeProof={**v['resourceChangeProof'], 'sha256': '0' * 64})):
            proof = copy.deepcopy(self.proof); mutate(proof); self.put(self.proof_path.relative_to(self.root).as_posix(), proof)
            with self.assertRaises((ValueError, KeyError)): self.validate()
        self.put(self.proof_path.relative_to(self.root).as_posix(), self.proof)

    def test_resealed_swap_restart_or_mysql_drift_is_not_a_kafka_memory_change(self):
        originals = {p: p.read_bytes() for p in self.root.rglob('*') if p.is_file()}
        original_active = copy.deepcopy(self.active); original_context_sha = self.context['activeApplicationSHA256']
        for change in ('swap', 'process', 'mysql', 'uptime', 'busy', 'durability'):
            metadata = grade.read(self.new_meta_path); actual = metadata['kafkaMemoryActual']
            after = grade._authority_ref(self.root, actual['after'])
            verified = grade._authority_ref(self.root, actual['proof'])
            if change == 'swap':
                after['containers']['kafka']['memorySwap'] = 4 << 30
                metadata['containers']['kafka']['memorySwap'] = actual['memorySwapBytes'] = verified['memorySwapBytes'] = 4 << 30
            elif change == 'process':
                after['processIdentity'] = {'retained': False}
            elif change == 'mysql':
                metadata['mysqlActual'] = {'serverUUID': 'wrong-server'}
            elif change == 'uptime':
                after['mysqlFacts']['uptime'] = 99
            elif change == 'busy':
                after['mysqlFacts']['otherSessions'] = 1
            else:
                after['mysqlFacts']['syncBinlog'] = 0
            actual['after'] = self.ref(self.put('resource-kafka/actual-001.json', after))
            verified['after'] = actual['after']
            actual['proof'] = self.ref(self.put('resource-kafka/verified.json', verified))
            self.put(self.new_meta_path.name, metadata)
            self.active['environmentSHA256'] = grade.sha(self.new_meta_path)
            self.put('active-application.json', self.active)
            self.context['activeApplicationSHA256'] = grade.sha(self.root / 'active-application.json')
            proof = {**self.proof, 'successorEnvironment': self.ref(self.new_meta_path), 'resourceChangeProof': actual['proof']}
            self.put(self.proof_path.relative_to(self.root).as_posix(), proof)
            with self.subTest(change=change), self.assertRaises(ValueError): self.validate()
            for path, raw in originals.items(): path.write_bytes(raw)
            self.active.clear(); self.active.update(copy.deepcopy(original_active))
            self.context['activeApplicationSHA256'] = original_context_sha

    def admission(self, tag):
        previous = grade.history(self.root)[-1]
        terminal = {**self.terminal, 'tag': previous['tag']}
        path = self.put('terminal-before-' + tag + '.json', terminal)
        fresh = {**self.audit, 'startedAt': self.ms(130), 'finishedAt': self.ms(135)}
        reconciliation = self.put('audit-before-' + tag + '/reconcile.json', fresh)
        return self.put('admission-' + tag + '.json', {'version': 1, 'batch': 'CAP-10', 'tag': tag,
            'authoritySHA256': self.context['authoritySHA256'], 'activeApplicationSHA256': self.context['activeApplicationSHA256'],
            'recordedAt': self.t(140), 'termination': self.ref(path), 'reconciliation': self.ref(reconciliation)})

    def reserve(self, kind, freeze=None):
        api = {'sourceSha': self.active['sourceSha'], 'sha256': self.active['binarySHA256'],
            'likeDiagnosticsRequested': False, 'tailEnabledRequested': False,
            **{key: self.active[key] for key in ('environmentPath', 'environmentSHA256', 'resourceProfile')}}
        return grade.authority_reservation(self.root, kind, 3010, 180 if kind == 'formal' else 60,
            api, self.root / 'fixture.json', {}, {}, freeze, self.active['scriptHashes'], self.active['observerBinarySHA256'], self.context)

    def current_command(self, tag, reservation, kind, passed=True):
        value = {**self.command, 'tag': tag, 'kind': kind, 'at': self.t(100 + int(tag[1:])),
            'seconds': 180 if kind == 'formal' else 60, 'reservationCheck': reservation,
            **{key: self.active[key] for key in ('binarySHA256', 'environmentSHA256', 'fixtureSHA256',
                'scriptHashes', 'observerBinarySHA256', 'ptsPacketSHA256', 'ptsAdmissionSHA256', 'environmentPath', 'resourceProfile')}}
        value['freezeSHA256'] = grade.sha(self.root / 'freeze-like.json') if kind == 'formal' else None
        self.put(tag + '-command.json', value)
        self.put(tag + '-stage.json', {'passed': passed, 'finishedAt': self.t(100 + int(tag[1:]) + .1),
            'candidateMarginPassed': passed, 'recoveryVerified': passed, 'childrenConverged': True})
        return value

    def new_freeze(self):
        (self.base / 'slot-S28.json').write_text('{}', encoding='utf-8')
        self.admission('S29'); candidate = self.reserve('candidate')
        self.assertEqual(candidate['tag'], 'S29')
        self.assertEqual(candidate['closedFailedFormalGroups'], {self.old_freeze_sha: grade.sha(self.proof_path)})
        self.current_command('S29', candidate, 'candidate')
        value = {**self.active, 'rate': 3010, 'candidateTag': 'S29', 'sourceCIPassed': True, 'relatedChecksPassed': True,
            'executionAuthoritySHA256': self.context['authoritySHA256'], 'activeApplicationSHA256': self.context['activeApplicationSHA256'],
            'initialDistributionContract': {'existing': 2, 'active': 0, 'relationships': 2},
            'closedFailedFormalGroups': candidate['closedFailedFormalGroups']}
        self.put('freeze-like.json', value)
        return value

    def test_candidate_preclosure_then_two_unused_formals_keep_counts_and_same_proof(self):
        freeze = self.new_freeze(); self.admission('H3')
        first = self.reserve('formal', freeze)
        self.assertEqual((first['tag'], first['formalSlotsUsedAfter'], first['mainSecondsReservedAfter']), ('H3', 2, 420))
        self.assertEqual(first['formalGroupSlotsUsedAfter'], 1)
        self.current_command('H3', first, 'formal'); self.admission('H4')
        second = self.reserve('formal', freeze)
        self.assertEqual((second['tag'], second['formalSlotsUsedAfter'], second['mainSecondsReservedAfter']), ('H4', 3, 600))
        self.assertEqual(first['closedFailedFormalGroups'], second['closedFailedFormalGroups'])
        self.current_command('H4', second, 'formal'); self.admission('H5')
        with self.assertRaises(ValueError): self.reserve('formal', freeze)

    def test_new_candidate_and_first_formal_failure_or_changed_map_still_block(self):
        freeze = self.new_freeze(); self.admission('H3')
        stage = grade.read(self.root / 'S29-stage.json'); self.put('S29-stage.json', {**stage, 'passed': False})
        with self.assertRaises(ValueError): self.reserve('formal', freeze)
        self.put('S29-stage.json', stage)
        first = self.reserve('formal', freeze)
        self.current_command('H3', first, 'formal', False)
        self.admission('H4')
        with self.assertRaises(ValueError): self.reserve('formal', freeze)
        self.put('H3-stage.json', {'passed': True, 'finishedAt': self.t(103.1)})
        command = grade.read(self.root / 'H3-command.json'); command['reservationCheck']['closedFailedFormalGroups'] = {}
        self.put('H3-command.json', command)
        with self.assertRaises(ValueError): self.reserve('formal', freeze)

    def boot_restore(self):
        parent_ref = self.ref(self.new_meta_path); parent = grade.read(self.new_meta_path)
        before = copy.deepcopy(grade._authority_ref(self.root, parent['kafkaMemoryActual']['after']))
        before.update(at=self.t(91), bootIdentity={'bootId': 'new-boot'}, parentEnvironment=parent_ref)
        for row in before['processIdentity'].values(): row.update(pid=row['pid'] + 100, startedAt=self.t(90))
        before['mysqlFacts'].update(bufferBytes=128 << 20, uptime=5, resizeText='not started', resizeProgress=0)
        before['mysqlRedo'].update(mysqlRedoCapacityBytes=100 << 20, mysqlRedoEffectiveCapacityBytes=100 << 20)
        after = copy.deepcopy(before); after['at'] = self.t(93)
        after['mysqlFacts'].update(bufferBytes=4 << 30, uptime=7, resizeText='complete', resizeProgress=100)
        after['mysqlRedo'].update(mysqlRedoCapacityBytes=2 << 30, mysqlRedoEffectiveCapacityBytes=2 << 30)
        before_ref = self.ref(self.put('boot-restore/before.json', before))
        after_ref = self.ref(self.put('boot-restore/actual-001.json', after))
        verified = {'version': 1, 'batch': 'CAP-10', 'kind': 'same-containers-new-boot-v1', 'complete': True,
            'noLoad': True, 'parentEnvironment': parent_ref, 'before': before_ref, 'after': after_ref,
            'previousBootId': 'synthetic-boot', 'bootId': 'new-boot'}
        proof_ref = self.ref(self.put('boot-restore/verified.json', verified))
        metadata = copy.deepcopy(parent); metadata.update(at=self.t(94),
            bootIdentity={'bootID': 'new-boot', 'bootTimeUnix': (self.clock + dt.timedelta(seconds=90)).timestamp()},
            bootRestoreActual={'contract': 'same-containers-new-boot-v1', 'parentEnvironment': parent_ref,
                'before': before_ref, 'after': after_ref, 'proof': proof_ref, 'bootIdentity': {'bootId': 'new-boot'}})
        self.new_meta_path = self.put('environment-333333333333.json', metadata)
        self.active.update(environmentPath=self.new_meta_path.name, environmentSHA256=grade.sha(self.new_meta_path),
            deploymentId='new-boot-deployment')
        self.put('active-application.json', self.active)
        self.context['activeApplicationSHA256'] = grade.sha(self.root / 'active-application.json')
        audit = {**self.audit, 'startedAt': self.ms(95), 'finishedAt': self.ms(98)}
        self.proof.update(successorDeploymentId=self.active['deploymentId'], successorEnvironment=self.ref(self.new_meta_path),
            reconciliation=self.ref(self.put('new-boot-audit/reconcile.json', audit)), recordedAt=self.t(99))
        self.proof_path = self.put('formal-group-supersessions/' + self.old_freeze_sha + '-' + self.active['deploymentId'] + '.json', self.proof)
        return metadata

    def test_historical_resource_mount_order_is_not_a_resource_change(self):
        metadata = grade.read(self.new_meta_path); actual = metadata['kafkaMemoryActual']
        after = grade._authority_ref(self.root, actual['after'])
        after['containers']['mysql']['mounts'].reverse()
        actual['after'] = self.ref(self.put(actual['after']['path'], after))
        verified = grade._authority_ref(self.root, actual['proof']); verified['after'] = actual['after']
        actual['proof'] = self.ref(self.put(actual['proof']['path'], verified))
        metadata['containers']['redis']['mounts'].reverse()
        self.put(self.new_meta_path.name, metadata)
        self.active['environmentSHA256'] = grade.sha(self.new_meta_path)
        self.proof.update(successorEnvironment=self.ref(self.new_meta_path), resourceChangeProof=actual['proof'])
        self.put(self.proof_path.relative_to(self.root).as_posix(), self.proof)
        originals = {p: p.read_bytes() for p in self.root.rglob('*') if p.is_file()}
        self.assertEqual(self.validate()['kind'], 'closed_failed_formal_group')
        self.assertEqual(originals, {p: p.read_bytes() for p in self.root.rglob('*') if p.is_file()})

    def reseal_boot(self, metadata):
        bridge = metadata['bootRestoreActual']
        bridge['before'] = self.ref(self.root / bridge['before']['path'])
        bridge['after'] = self.ref(self.root / bridge['after']['path'])
        verified = grade._authority_ref(self.root, bridge['proof']); verified.update(before=bridge['before'], after=bridge['after'])
        bridge['proof'] = self.ref(self.put(bridge['proof']['path'], verified))
        self.put(self.new_meta_path.name, metadata)
        self.active['environmentSHA256'] = grade.sha(self.new_meta_path)
        self.put('active-application.json', self.active)
        self.context['activeApplicationSHA256'] = grade.sha(self.root / 'active-application.json')
        self.proof['successorEnvironment'] = self.ref(self.new_meta_path)
        self.put(self.proof_path.relative_to(self.root).as_posix(), self.proof)

    def test_new_boot_reuses_old_resource_history_but_requires_current_restoration(self):
        original_stage = (self.root / 'H2-stage.json').read_bytes()
        metadata = self.boot_restore()
        self.assertEqual(self.validate()['kind'], 'closed_failed_formal_group')
        bridge = grade.boot_restore_identity(self.root, self.ref(self.new_meta_path))
        self.assertEqual(bridge, {'parentEnvironment': metadata['bootRestoreActual']['parentEnvironment'],
            'bootId': 'new-boot', 'restoredAtUnixMs': (self.clock + dt.timedelta(seconds=93)).timestamp() * 1000})
        self.assertEqual(grade.read(self.root / metadata['bootRestoreActual']['parentEnvironment']['path'])['bootIdentity']['bootID'], 'synthetic-boot')
        self.assertEqual((self.root / 'H2-stage.json').read_bytes(), original_stage)

    def test_new_boot_rejects_old_audit_or_missing_explicit_bridge(self):
        metadata = self.boot_restore()
        original = copy.deepcopy(self.proof)
        self.proof['reconciliation'] = self.ref(self.root / 'closure/reconcile.json')
        self.put(self.proof_path.relative_to(self.root).as_posix(), self.proof)
        with self.assertRaises(ValueError): self.validate()
        self.proof = original; self.put(self.proof_path.relative_to(self.root).as_posix(), self.proof)
        metadata.pop('bootRestoreActual'); self.put(self.new_meta_path.name, metadata)
        self.active['environmentSHA256'] = grade.sha(self.new_meta_path)
        self.proof['successorEnvironment'] = self.ref(self.new_meta_path)
        self.put(self.proof_path.relative_to(self.root).as_posix(), self.proof)
        with self.assertRaises(ValueError): self.validate()

    def test_resealed_new_boot_wrong_volume_uuid_durability_targets_or_restart_rejected(self):
        metadata = self.boot_restore()
        originals = {p: p.read_bytes() for p in self.root.rglob('*') if p.is_file()}
        original_proof = copy.deepcopy(self.proof); original_active = copy.deepcopy(self.active)
        for case in ('volume', 'uuid', 'durability', 'buffer', 'redo', 'process'):
            value = copy.deepcopy(metadata); after_path = self.root / value['bootRestoreActual']['after']['path']
            after = grade.read(after_path)
            if case == 'volume': after['volumes'] = ['wrong-volume']
            elif case == 'uuid': after['mysqlFacts']['serverUUID'] = 'wrong-server'
            elif case == 'durability': after['mysqlFacts']['syncBinlog'] = 0
            elif case == 'buffer': after['mysqlFacts']['bufferBytes'] = 128 << 20
            elif case == 'redo': after['mysqlRedo']['mysqlRedoEffectiveCapacityBytes'] = 100 << 20
            else: after['processIdentity']['mysql']['pid'] += 1
            self.put(after_path.relative_to(self.root).as_posix(), after); self.reseal_boot(value)
            with self.subTest(case=case), self.assertRaises(ValueError): self.validate()
            for path, raw in originals.items(): path.write_bytes(raw)
            self.proof = copy.deepcopy(original_proof); self.active.clear(); self.active.update(copy.deepcopy(original_active))
            self.context['activeApplicationSHA256'] = grade.sha(self.root / 'active-application.json')

    def test_new_boot_candidate_and_formal_still_keep_old_slots_and_new_freeze(self):
        self.boot_restore(); freeze = self.new_freeze(); self.admission('H3')
        result = self.reserve('formal', freeze)
        self.assertEqual((result['tag'], result['formalSlotsUsedAfter'], result['mainSecondsReservedAfter']), ('H3', 2, 420))
        self.assertFalse(grade.stage(self.root, self.command)['passed'])

    def test_new_boot_mount_permutation_preserves_raw_and_rejects_changed_field_or_duplicate(self):
        metadata = self.boot_restore()
        for phase in ('before', 'after'):
            path = self.root / metadata['bootRestoreActual'][phase]['path']; value = grade.read(path)
            for row in value['containers'].values(): row['mounts'].reverse()
            self.put(path.relative_to(self.root).as_posix(), value)
        for row in metadata['containers'].values(): row['mounts'].reverse()
        self.reseal_boot(metadata)
        originals = {p: p.read_bytes() for p in self.root.rglob('*') if p.is_file()}
        self.assertEqual(self.validate()['kind'], 'closed_failed_formal_group')
        self.assertEqual(originals, {p: p.read_bytes() for p in self.root.rglob('*') if p.is_file()})
        original_proof = copy.deepcopy(self.proof); original_active = copy.deepcopy(self.active)
        for case in ('field', 'duplicate'):
            value = copy.deepcopy(metadata); path = self.root / value['bootRestoreActual']['after']['path']; after = grade.read(path)
            mounts = after['containers']['kafka']['mounts']
            if case == 'field': mounts[0]['RW'] = not mounts[0]['RW']
            else: mounts.append(copy.deepcopy(mounts[0]))
            self.put(path.relative_to(self.root).as_posix(), after); self.reseal_boot(value)
            with self.subTest(case=case), self.assertRaises(ValueError): self.validate()
            for p, raw in originals.items(): p.write_bytes(raw)
            self.proof = copy.deepcopy(original_proof); self.active.clear(); self.active.update(copy.deepcopy(original_active))
            self.context['activeApplicationSHA256'] = grade.sha(self.root / 'active-application.json')


if __name__ == '__main__':
    unittest.main()
