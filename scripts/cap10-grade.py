"""Pure CAP10 bounded search, reservation and public-artifact grading.

No process, network or dependency APIs. The caller exclusively writes a slot
before any warm/native start; failures and abandoned reservations are retained.
"""
import datetime as dt
import hashlib
import json
import math
from pathlib import Path
import re

REPO = Path(__file__).resolve().parent.parent
ROOT = REPO / 'docs/evidence/2026-11-01-cap10'
KINDS = ('diagnostic', 'comparison', 'search', 'candidate', 'formal')

def require(condition, message):
    if not condition:
        raise ValueError(message)

def read(path):
    return json.loads(Path(path).read_text(encoding='utf-8-sig'))

def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()

def instant(value):
    result = dt.datetime.fromisoformat(value.replace('Z', '+00:00'))
    require(result.tzinfo is not None, 'explicit timezone required')
    return result

def history(root):
    require(Path(root).resolve() in (ROOT, ROOT / 'linux'), 'fixed CAP10 executor root required')
    records = [read(path) for directory in (ROOT, ROOT / 'linux') for path in directory.glob('*-command.json')]
    records = sorted((r for r in records if r.get('task') == 'CAP-10' and re.fullmatch(r'[SH][1-9][0-9]*', r.get('tag', ''))), key=lambda r: r['at'])
    require(len({r['tag'] for r in records}) == len(records), 'same CAP10 reservation cannot be reset across environments')
    return records

def stage(root, command):
    paths = {directory / (command['tag'] + '-stage.json') for directory in (ROOT, ROOT / 'linux')}
    found = [path for path in paths if path.is_file()]
    require(len(found) == 1, 'previous reservation has no unique terminal artifact; preserve it')
    return read(found[0])


def merged_wait_seconds(intervals, started, ended):
    """Clip and union only proved completed load/drain waits, never double count."""
    require(ended >= started, 'budget clock moved backwards')
    clipped = []
    for first, last in intervals:
        first, last = instant(first), instant(last)
        require(last >= first, 'negative completed-stage wait is invalid')
        first, last = max(started, first), min(ended, last)
        if last > first:
            clipped.append((first, last))
    total, current = 0.0, None
    for first, last in sorted(clipped):
        if current is None:
            current = (first, last)
        elif first <= current[1]:
            current = (current[0], max(current[1], last))
        else:
            total += (current[1] - current[0]).total_seconds()
            current = (first, last)
    if current:
        total += (current[1] - current[0]).total_seconds()
    require(0 <= total <= (ended - started).total_seconds(), 'invalid proved-wait deduction')
    return total


def continuation_authorization(root, now):
    path = ROOT / 'continuation-authorization.json'
    records = history(root)
    records += [read(p) for directory in (ROOT, ROOT / 'linux') for p in directory.glob('slot-[SH]*.json')]
    def recorded_hash(record):
        return record.get('reservationCheck', {}).get('effectiveWorkBudget', {}).get('continuationAuthorizationSHA256')
    if not path.exists():
        require(all(recorded_hash(record) is None for record in records), 'continuation authorization cannot be removed after reservation')
        return {'waiveEffectiveWorkTimeLimit': False, 'continuationAuthorizationSHA256': None}
    require(root == ROOT / 'linux' and path.is_file() and not path.is_symlink(), 'continuation applies only to the owned Linux batch')
    value, digest = read(path), sha(path)
    require(isinstance(value, dict) and type(value.get('version')) is int and value['version'] == 1 and value.get('batch') == 'CAP-10', 'invalid continuation authorization identity')
    require(value.get('authorized') is True, 'continuation authorization must be explicitly approved')
    require(value.get('waiveEffectiveWorkTimeLimit') is True and type(value.get('maxIdleRetries')) is int and value['maxIdleRetries'] == 1, 'explicit work-time waiver and exactly one idle retry required')
    require(type(value.get('originalMaximumSeconds', 10800)) is int and value.get('originalMaximumSeconds', 10800) == 10800, 'original work-time maximum remains 10800 seconds')
    reference = value.get('userAuthorizationReference')
    require(isinstance(reference, str) and reference.strip(), 'explicit continuation user authorization required')
    require(value.get('transitionSHA256') == sha(ROOT / 'environment-transition.json'), 'continuation must retain the original transition')
    failed_path = value.get('failedIdleResultPath')
    require(isinstance(failed_path, str) and failed_path, 'retained failed idle path required')
    failed_path = (ROOT / failed_path).resolve()
    require(failed_path.is_relative_to((ROOT / 'linux').resolve()) and failed_path == (ROOT / 'linux/idle-attempt-1/idle-result.json').resolve() and failed_path.is_file() and value.get('failedIdleResultSHA256') == sha(failed_path) and read(failed_path).get('passed') is False, 'original failed cloud idle bytes must be retained')
    require(isinstance(value.get('recordedAt'), str), 'continuation registration timestamp required')
    registered = instant(value['recordedAt'])
    require(instant(read(ROOT / 'environment-transition.json')['recordedAt']) <= registered <= now, 'continuation registration time differs from batch chronology')
    require(all(recorded_hash(record) == digest and instant(record['at']) >= registered for record in records), 'continuation must predate the first slot and remain byte-identical')
    return {'waiveEffectiveWorkTimeLimit': True, 'continuationAuthorizationSHA256': digest}


def effective_work_budget(root, api_metadata, now=None):
    now = now or dt.datetime.now(dt.timezone.utc)
    if root == ROOT:
        require(api_metadata.get('executionPlatform') == 'windows', 'Windows metadata platform differs')
        require(not (ROOT / 'environment-transition.json').exists(), 'Windows execution closed after the single Linux switch')
        elapsed = (now - instant(read(ROOT / 'start.json')['effectiveWorkStartConservativeUTC'])).total_seconds()
        continuation = continuation_authorization(root, now)
        waived = continuation['waiveEffectiveWorkTimeLimit']
        require(elapsed >= 0 and (waived or elapsed <= 10800), 'conservative effective development bound exhausted')
        return {'effectiveSecondsUpperBound': elapsed, 'provedLoadWaitSecondsDeducted': 0,
                'maximumSeconds': None if waived else 10800, 'remainingSeconds': None if waived else 10800 - elapsed,
                'originalMaximumSeconds': 10800, 'passed': True, 'platform': 'windows', **continuation}
    require(root == ROOT / 'linux' and api_metadata.get('executionPlatform') == 'linux', 'fixed Linux metadata platform required')
    path = ROOT / 'environment-transition.json'
    require(api_metadata.get('transitionSHA256') == sha(path), 'Linux transition hash differs')
    transition, handoff = read(path), read(ROOT / 'budget-handoff.json')
    require(transition.get('version') == 1 and transition.get('batch') == 'CAP-10' and transition.get('fromPlatform') == 'windows' and transition.get('toPlatform') == 'linux' and transition.get('switchCount') == 1 and transition.get('authorized') is True and transition.get('hostLabel') and transition.get('userAuthorizationReference'), 'one explicit host-authorized Linux switch required')
    require(re.fullmatch(r'[a-f0-9]{40}', transition.get('localCheckpointSHA', '')), 'retained local checkpoint revision required')
    for key, filename in (('localEnvironmentSHA256', 'environment.json'), ('localIdleResultSHA256', 'idle-result.json'), ('localCleanupSHA256', 'cleanup.json'), ('localRawManifestSHA256', 'raw-manifest.json'), ('budgetHandoffSHA256', 'budget-handoff.json')):
        require(transition.get(key) == sha(ROOT / filename), 'retained local evidence differs: ' + key)
    constants = {'shortSlotsUsedBeforeSwitch': 0, 'formalSlotsUsedBeforeSwitch': 0, 'optimizationsUsedBeforeSwitch': 0, 'mainLoadSecondsUsedBeforeSwitch': 0, 'maxShortWindows': 8, 'maxFormalWindows': 2, 'maxMainLoadSeconds': 840, 'maxOptimizations': 1, 'effectiveWorkSecondsMaximum': 10800}
    require(all(type(transition.get(k)) is int and transition[k] == v for k, v in constants.items()), 'Linux switch must carry the same unused CAP10 quota')
    require(not any(ROOT.glob('slot-[SH]*.json')) and not any(ROOT.glob('[SH]*-command.json')), 'local slots cannot be reset by switching environment')
    require(handoff.get('version') == 1 and handoff.get('batch') == 'CAP-10' and handoff.get('maxEffectiveWorkSeconds') == 10800, 'original work-budget handoff invalid')
    used, remaining = transition.get('effectiveWorkSecondsUsedBeforeSwitch'), transition.get('effectiveWorkSecondsRemaining')
    prior = handoff.get('countedEffectiveWorkSecondsUpperBound')
    require(all(type(value) is int for value in (used, remaining, prior, handoff.get('remainingEffectiveWorkSecondsLowerBound'))) and prior >= 4200 and prior + handoff['remainingEffectiveWorkSecondsLowerBound'] == 10800 and used >= prior and remaining >= 0 and used + remaining == 10800, 'effective development budget cannot reset the retained 4200s checkpoint or increase its remainder')
    anchor = instant(transition['recordedAt'])
    require(anchor >= instant(handoff['recordedAt']), 'transition predates its original handoff')
    intervals = []
    for command in history(root):
        filename = root / (command['tag'] + '-stage.json')
        if filename.is_file():
            terminal = read(filename)
            first, last = terminal.get('loadWaitStartedAt'), terminal.get('loadWaitFinishedAt')
            if first is not None and last is not None:
                intervals.append((first, last))
    wall = (now - anchor).total_seconds()
    deducted = merged_wait_seconds(intervals, anchor, now)
    elapsed = used + wall - deducted
    continuation = continuation_authorization(root, now)
    waived = continuation['waiveEffectiveWorkTimeLimit']
    require(elapsed >= 0 and (waived or elapsed <= 10800), 'continued effective development bound exhausted')
    return {'effectiveSecondsUpperBound': elapsed, 'provedLoadWaitSecondsDeducted': deducted,
            'maximumSeconds': None if waived else 10800, 'remainingSeconds': None if waived else 10800 - elapsed,
            'originalMaximumSeconds': 10800, 'passed': True, 'platform': 'linux',
            'usedBeforeSwitch': used, 'anchor': transition['recordedAt'], **continuation}

def pre_load_recovery(root, records):
    """One zero-PUT S1 startup failure can consume its slot without a boundary."""
    path = Path(root) / 'pre-load-recovery.json'
    if not path.exists():
        require(not any(c.get('reservationCheck', {}).get('preLoadRecoverySHA256') for c in records), 'pre-load recovery authorization cannot be removed')
        return None
    require(root == ROOT / 'linux' and path.is_file() and not path.is_symlink(), 'fixed Linux pre-load recovery required')
    auth, digest = read(path), sha(path)
    require(isinstance(auth, dict) and type(auth.get('version')) is int and auth['version'] == 1 and auth.get('batch') == 'CAP-10' and auth.get('authorized') is True and auth.get('failedTag') == 'S1' and auth.get('nextDiagnosticTag') == 'S2', 'only the first zero-request startup may recover')
    require(type(auth.get('remoteEngineRSSLimitBytes')) is int and auth['remoteEngineRSSLimitBytes'] == 6 * 1024 ** 3 and auth.get('toolRefreezeAuthorized') is True, 'explicit per-engine 6GiB environment and one tool refreeze required')
    require(isinstance(auth.get('userAuthorizationReference'), str) and auth['userAuthorizationReference'].strip() and isinstance(auth.get('recordedAt'), str), 'explicit autonomous repair authorization required')
    registered = instant(auth['recordedAt'])
    require(registered <= dt.datetime.now(dt.timezone.utc), 'recovery authorization cannot be future-dated')
    require(records and records[0]['tag'] == 'S1' and records[0]['kind'] == 'diagnostic' and records[0]['rate'] == 50 and records[0]['seconds'] == 60, 'original S1 reservation must be retained')
    for command in records[1:]:
        require(command.get('reservationCheck', {}).get('preLoadRecoverySHA256') == digest and instant(command['at']) >= registered, 'pre-load recovery must remain fixed before S2')
    require(auth.get('proofPath') == 'pre-load-recovery-proof.json', 'fixed pre-load proof path required')
    proof_path = Path(root) / auth['proofPath']
    require(proof_path.is_file() and not proof_path.is_symlink() and auth.get('proofSHA256') == sha(proof_path), 'pre-load proof bytes differ')
    proof = read(proof_path)
    require(isinstance(proof, dict) and proof.get('version') == 1 and proof.get('batch') == 'CAP-10' and proof.get('failedTag') == 'S1' and isinstance(proof.get('artifacts'), dict), 'pre-load proof identity invalid')
    def artifact(name, ndjson=False):
        reference = proof['artifacts'].get(name, {})
        require(isinstance(reference, dict) and isinstance(reference.get('path'), str), 'pre-load artifact reference missing: ' + name)
        relative = Path(reference['path'])
        candidate = Path(root) / relative
        require(not relative.is_absolute() and '..' not in relative.parts and candidate.is_file() and not candidate.is_symlink() and candidate.resolve() == candidate and candidate.is_relative_to(root), 'pre-load artifact must stay in executor root: ' + name)
        require(reference.get('sha256') == sha(candidate), 'pre-load artifact bytes differ: ' + name)
        return [json.loads(line) for line in candidate.read_bytes().splitlines() if line.strip()] if ndjson else read(candidate)
    command, previous = artifact('command'), artifact('stage')
    require(proof['artifacts']['command']['path'] == 'S1-command.json' and proof['artifacts']['stage']['path'] == 'S1-stage.json' and command == records[0] and previous == stage(root, command) and previous.get('passed') is False, 'original failed S1 artifacts cannot be replaced or relabeled')
    require(instant(previous['finishedAt']) <= registered and command.get('loadBackend') == 'pts', 'recovery must follow the original PTS startup failure')
    collector, journal = artifact('collectorTerminal'), artifact('collectorRecords', True)
    zeros = ('attempted', 'completed', 'mutation', 'noop', 'uncertain', 'rejected', 'invalid', 'dropped')
    zero_counts = lambda counts: isinstance(counts, dict) and all(type(counts.get(key)) is int and counts[key] == 0 for key in zeros)
    require(collector.get('phase') == 'warm' and collector.get('rate') == 25 and collector.get('seconds') == 10 and collector.get('expectedEngines') == 2 and collector.get('fixtureSHA256') == command['fixtureSHA256'] and collector.get('stop') is True and collector.get('childrenConverged') is True and collector.get('latencySampleCount') == 0 and zero_counts(collector.get('counts')), 'any HTTP outcome forbids startup recovery')
    require(collector.get('evidenceSHA256') == proof['artifacts']['collectorRecords']['sha256'], 'collector must bind the complete retained journal')
    configurations = [row.get('configuration', {}) for row in journal if row.get('kind') == 'configuration']
    require(len(configurations) == 1 and all(configurations[0].get(key) == collector.get(key) for key in ('runId', 'phase', 'rate', 'seconds', 'fixtureSHA256', 'expectedEngines')), 'retained collector run identity differs')
    engines = collector.get('engines', [])
    require(len(engines) == 2 and len({e.get('engineId') for e in engines}) == 2 and all(e.get('stoppedOffering') is True and zero_counts(e.get('counts')) for e in engines), 'both engines must attest zero requests and stopped offering')
    completions = [row for row in journal if row.get('kind') == 'complete']
    require(len(completions) == 2 and {row.get('engineId') for row in completions} == {e['engineId'] for e in engines} and all(row.get('accepted') is True and row.get('stoppedOffering') is True and type(row.get('inFlight')) is int and row['inFlight'] == 0 and zero_counts(row.get('counts')) and row.get('collectorRawCounts') == row['counts'] for row in completions), 'complete zero-request engine termination frames required')
    require(not any(row.get('kind') == 'batch' and row.get('records') != [] for row in journal), 'any recorded native HTTP forbids startup recovery')
    barriers = [row for row in journal if row.get('kind') == 'barrier']
    stops = [row for row in journal if row.get('kind') == 'stop']
    require(not barriers and collector.get('startUnixMs') is None or len(barriers) == 1 and stops
            and type(barriers[0].get('startUnixMs')) in (int, float) and math.isfinite(barriers[0]['startUnixMs'])
            and collector.get('startUnixMs') == barriers[0]['startUnixMs']
            and type(stops[0].get('atUnixMs')) in (int, float) and math.isfinite(stops[0]['atUnixMs'])
            and stops[0]['atUnixMs'] < barriers[0]['startUnixMs'], 'startup stop must strictly precede its only scheduled barrier')
    stop, idle1, idle2 = artifact('stopAudit'), artifact('idleAudit1'), artifact('idleAudit2')
    scene = stop.get('sceneId')
    require(stop.get('action') == 'StopTestingJMeterScene' and stop.get('httpStatus') == 200 and isinstance(scene, str) and scene and stop.get('sanitizedRawResponse', {}).get('Success') is True, 'actual accepted scene stop evidence required')
    admission_path = Path(root) / 'pts-scene-admission.json'
    require(admission_path.is_file() and command.get('ptsAdmissionSHA256') == sha(admission_path) and read(admission_path).get('sceneId') == scene, 'stopped scene must match the original S1 admission')
    for row in (idle1, idle2):
        response = row.get('sanitizedRawResponse')
        require(row.get('action') == 'GetJMeterSceneRunningData' and row.get('sceneId') == scene and row.get('httpStatus') == 200 and isinstance(response, dict) and set(response) == {'RequestId', 'Success'} and response['Success'] is True and isinstance(response['RequestId'], str) and response['RequestId'].strip(), 'only strict signed no-active replies can recover zero-PUT startup')
    require(instant(stop['responseFinishedAt']) <= instant(idle1['requestStartedAt']) and instant(idle1['responseFinishedAt']) <= instant(idle2['requestStartedAt']) and (instant(idle2['requestStartedAt']) - instant(idle1['requestStartedAt'])).total_seconds() >= 2 and idle1['sanitizedRawResponse']['RequestId'] != idle2['sanitizedRawResponse']['RequestId'], 'two distinct no-active replies at least two seconds apart required')
    exited = artifact('localExit')
    owners = exited.get('owners', [])
    expected_owners = {(p['pid'], p['created']) for p in previous.get('knownOwnedProcesses', [])}
    actual_owners = {(p.get('pid'), p.get('created')) for p in owners}
    require(owners and expected_owners and expected_owners <= actual_owners and all(type(p.get('pid')) is int and p['pid'] > 0 and type(p.get('created')) in (int, float) and p.get('alive') is False and p.get('identityChecked') is True for p in owners) and all(exited.get(key) is False for key in ('observerAlive', 'controllerAlive', 'wrapperAlive')), 'all original local load owners must be proved gone')
    manifest, inspection = artifact('fixtureManifest'), artifact('fixtureInspection')
    require(proof['artifacts']['fixtureManifest']['sha256'] == command['fixtureSHA256'] and inspection.get('valid') is True and inspection.get('errors') == [], 'authoritative unchanged fixture inspection required')
    distribution, facts = inspection.get('distribution', {}), inspection.get('facts', {})
    require(distribution.get('configuredRelationships') == 512 and distribution.get('existing') == 512 and distribution.get('active') == 0 and distribution.get('inactive') == 512 and distribution.get('missing') == 0 and distribution.get('versionHistogram') == {'0': 512} and facts.get('transitions') == facts.get('eventCount') == 0 and facts.get('events') == [], 'all 512 original relationships must remain version-zero without LIKE outbox')
    pairs = list(zip(manifest.get('multiNoteIds', []), manifest.get('userIds', [])))
    rows, notes = facts.get('facts', []), facts.get('notes', [])
    require(len(pairs) == len(set(pairs)) == len(rows) == 512 and {(r.get('noteId'), r.get('userId')) for r in rows} == set(pairs) and all(type(r.get('active')) is int and r['active'] == 0 and type(r.get('version')) is int and r['version'] == 0 for r in rows) and len(notes) == 512 and {n.get('noteId') for n in notes} == {p[0] for p in pairs} and all(n.get('liked') == n.get('activeRelations') == 0 for n in notes), 'SQL facts and note counters must cover the exact original pairs')
    return {'sha256': digest, 'failedTag': 'S1', 'nextDiagnosticTag': 'S2'}


def claim_optimization(root, records):
    """One indexed Claim access-path optimization, compared at S5's 800/s."""
    path = Path(root) / 'S5-optimization-proof.json'
    if not path.exists():
        require(not any(c.get('reservationCheck', {}).get('claimOptimizationSHA256') for c in records), 'Claim optimization proof cannot be removed')
        return None
    require(root == ROOT / 'linux' and path.is_file() and not path.is_symlink(), 'fixed Claim optimization proof required')
    value, digest = read(path), sha(path)
    require(isinstance(value, dict) and type(value.get('version')) is int and value['version'] == 1 and value.get('batch') == 'CAP-10' and value.get('authorized') is True and value.get('optimizationKind') == 'outbox-claim-access-path' and value.get('baselineTag') == 'S5' and value.get('nextComparisonTag') == 'S6' and value.get('rate') == 800 and value.get('seconds') == 60 and value.get('diagnostics') is False, 'only one fixed S5 to S6 indexed Claim comparison is authorized')
    require(isinstance(value.get('userAuthorizationReference'), str) and value['userAuthorizationReference'].strip() and isinstance(value.get('recordedAt'), str) and isinstance(value.get('artifacts'), dict), 'explicit Claim optimization evidence required')
    registered = instant(value['recordedAt'])
    require(registered <= dt.datetime.now(dt.timezone.utc) and len(records) >= 5 and [c['tag'] for c in records[:5]] == ['S1', 'S2', 'S3', 'S4', 'S5'], 'optimization must follow the retained S5 baseline')
    require(value.get('toolRepairTransitionSHA256') == sha(Path(root) / 'tool-repair-transition.json'), 'original tool repair transition must not change')
    def artifact(name):
        ref = value['artifacts'].get(name, {})
        require(isinstance(ref, dict) and isinstance(ref.get('path'), str), 'optimization artifact missing: ' + name)
        relative = Path(ref['path']); candidate = Path(root) / relative
        require(not relative.is_absolute() and '..' not in relative.parts and candidate.is_file() and not candidate.is_symlink() and candidate.resolve() == candidate and candidate.is_relative_to(root) and ref.get('sha256') == sha(candidate), 'optimization artifact bytes or path differ: ' + name)
        return read(candidate)
    command, previous, result = artifact('command'), artifact('stage'), artifact('result')
    require(value['artifacts']['command']['path'] == 'S5-command.json' and value['artifacts']['stage']['path'] == 'S5-stage.json' and value['artifacts']['result']['path'] == 'S5-load/result.json' and command == records[4] and previous == stage(root, command) and previous.get('outcome') == 'performance_limited' and previous.get('passed') is False and previous.get('recoveryVerified') is False and command['kind'] == 'search' and command['rate'] == 800 and command['seconds'] == 60 and instant(previous['finishedAt']) <= registered, 'S5 failed recovery and effective performance baseline must remain unchanged')
    metrics = result.get('result', {})
    require(result.get('nativeScenario', {}).get('complete') is True and result.get('meetsAPIBudget') is True and result.get('finalFactsValid') is True and result.get('phaseAccountingValid') is True and metrics.get('mutationCounterMatchesDB') is True and metrics.get('attempted') == metrics.get('completed') == metrics.get('mutation') == 48000 and all(type(metrics.get(key)) is int and metrics[key] == 0 for key in ('noop', 'rejected', 'contentErrors', 'dropped', 'unknown', 'classificationUnknown', 'effectUnknown')), 'S5 must be a complete correctly measured 48000-mutation baseline')
    before, after = value.get('beforeIdentity'), value.get('afterIdentity')
    require(isinstance(before, dict) and isinstance(after, dict), 'before and after Claim identities required')
    baseline = {'sourceSha': command['apiSourceSha'], **{key: command[key] for key in ('binarySHA256', 'environmentSHA256', 'fixtureSHA256', 'scriptHashes', 'observerBinarySHA256', 'ptsPacketSHA256', 'ptsAdmissionSHA256')}}
    require(all(before.get(key) == actual for key, actual in baseline.items()) and re.fullmatch(r'[a-f0-9]{40}', after.get('sourceSha', '')) and after['sourceSha'] != before['sourceSha'] and re.fullmatch(r'[a-f0-9]{64}', after.get('binarySHA256', '')) and after['binarySHA256'] != before['binarySHA256'], 'one clean new Go source and binary required for the Claim predicate/index optimization')
    require(all(after.get(key) == before[key] for key in ('environmentSHA256', 'fixtureSHA256', 'observerBinarySHA256', 'ptsPacketSHA256', 'ptsAdmissionSHA256')) and isinstance(after.get('scriptHashes'), dict) and set(after['scriptHashes']) == set(before['scriptHashes']) and all(after['scriptHashes'][key] == before['scriptHashes'][key] for key in before['scriptHashes'] if key not in ('cap10-grade.py', 'cap10-run.py')), 'only grade/runner comparison protocol may change; workload, collector, observer and resources remain fixed')
    index, checks = artifact('indexReceipt'), artifact('relatedChecks')
    require(index.get('table') == 'tb_community_outbox' and index.get('column') == 'claim_active' and isinstance(index.get('extra'), str) and 'VIRTUAL' in index['extra'].upper() and index.get('indexColumns') == ['claim_active', 'id'] and isinstance(index.get('indexName'), str) and index['indexName'] and index.get('explainKey') == index['indexName'], 'physical generated-column/index and actual chosen Claim access path required')
    require(checks.get('passed') is True and checks.get('sourceSha') == after['sourceSha'] and isinstance(checks.get('checks'), list) and checks['checks'], 'related checks must pass on the exact new Claim source')
    for phase in ('warm', 'stable'):
        terminal = artifact(phase + 'Terminal')
        require(terminal.get('backend') == 'pts' and terminal.get('remoteConverged') is True and terminal.get('nativeEvidenceComplete') is True and isinstance(terminal.get('reportId'), str) and terminal['reportId'], 'S5 actual PUT phases must have their original proved remote terminal')
    reconciliation = artifact('reconciliation')
    require(reconciliation.get('valid') is True and reconciliation.get('factsValid') is True and reconciliation.get('downstreamConverged') is True and reconciliation.get('errors') == [] and type(reconciliation.get('finishedAt')) in (int, float) and reconciliation['finishedAt'] / 1000 <= registered.timestamp(), 'new API must drain and reconcile before the Claim comparison proof is frozen')
    facts, derived, timeline = reconciliation.get('facts', {}), reconciliation.get('derived', {}), reconciliation.get('timeline', [])
    require(timeline and timeline[-1].get('pending') == timeline[-1].get('parked') == 0 and timeline[-1].get('receiptErrors') == [] and derived.get('available') is True and derived.get('errors') == [] and len(derived.get('notes', [])) == 512 and len(facts.get('facts', [])) == len(facts.get('notes', [])) == 512 and facts.get('transitions') == facts.get('eventCount') and not any(p.get('state') == 'PARKED' and p.get('count', 0) for p in facts.get('parked', [])), 'full facts, Redis and independent receipt convergence required before comparison')
    require(sum(row.get('version', -1) for row in facts['facts']) == facts['eventCount'] and facts['eventCount'] >= metrics['mutation'] and all(type(row.get('version')) is int and row['version'] >= 0 and row.get('active') == row['version'] % 2 for row in facts['facts']) and all(note.get('liked') == note.get('activeRelations') and note.get('liked', -1) >= 0 for note in facts['notes']), 'reconciled relation versions and public counts must agree')
    total = facts['eventCount']; receipts = facts.get('receipts', [])
    require(sum(d.get('count', 0) for d in facts.get('delivery', []) if d.get('status') == 'DONE') == total and not any(d.get('status') != 'DONE' and d.get('count', 0) for d in facts.get('delivery', [])) and all(sum(r.get('count', 0) for r in receipts if r.get('projection') == group and r.get('state') == 'IGNORED') == total for group in ('follow-v1', 'feed-v1', 'search-v1')), 'all reconciled Outbox effects and three LIKE receipt groups must be complete')
    for c in records[5:]:
        require(c.get('reservationCheck', {}).get('claimOptimizationSHA256') == digest and instant(c['at']) >= registered and c.get('apiSourceSha') == after['sourceSha'] and all(c.get(key) == after[key] for key in ('binarySHA256', 'environmentSHA256', 'fixtureSHA256', 'scriptHashes', 'observerBinarySHA256', 'ptsPacketSHA256', 'ptsAdmissionSHA256')), 'S6 onward must use the one fixed optimized Claim identity')
    return {**value, 'sha256': digest}


def delayed_recovery(root, records, optimization):
    """Permit only S8 after a fully measured S7 overload later drains."""
    path = Path(root) / 'S7-delayed-recovery.json'
    if not path.exists():
        require(not any(c.get('reservationCheck', {}).get('delayedRecoverySHA256') for c in records), 'S7 delayed recovery proof cannot be removed')
        return None
    require(root == ROOT / 'linux' and optimization and path.is_file() and not path.is_symlink(), 'fixed optimized S7 delayed recovery proof required')
    value, digest = read(path), sha(path); final = optimization['afterIdentity']
    require(isinstance(value, dict) and type(value.get('version')) is int and value['version'] == 1 and value.get('batch') == 'CAP-10' and value.get('authorized') is True and value.get('failedTag') == 'S7' and value.get('nextTag') == 'S8' and value.get('claimOptimizationSHA256') == optimization['sha256'] and value.get('sourceSha') == final['sourceSha'] and value.get('binarySHA256') == final['binarySHA256'], 'only the fixed final-source S7 to S8 continuation is allowed')
    require(isinstance(value.get('userAuthorizationReference'), str) and value['userAuthorizationReference'].strip() and isinstance(value.get('recordedAt'), str) and isinstance(value.get('artifacts'), dict), 'explicit same-session delayed recovery evidence required')
    registered = instant(value['recordedAt'])
    require(registered <= dt.datetime.now(dt.timezone.utc) and len(records) >= 7 and records[6]['tag'] == 'S7', 'delayed recovery must follow the consumed S7 slot')
    def artifact(name):
        ref = value['artifacts'].get(name, {}); require(isinstance(ref, dict) and isinstance(ref.get('path'), str), 'delayed recovery artifact missing: ' + name)
        relative = Path(ref['path']); candidate = Path(root) / relative
        require(not relative.is_absolute() and '..' not in relative.parts and candidate.is_file() and not candidate.is_symlink() and candidate.resolve() == candidate and candidate.is_relative_to(root) and ref.get('sha256') == sha(candidate), 'delayed recovery artifact bytes or path differ: ' + name)
        return read(candidate)
    command, previous, result = artifact('command'), artifact('stage'), artifact('result')
    require(value['artifacts']['command']['path'] == 'S7-command.json' and value['artifacts']['stage']['path'] == 'S7-stage.json' and value['artifacts']['result']['path'] == 'S7-load/result.json' and command == records[6] and previous == stage(root, command) and previous.get('outcome') == 'performance_limited' and previous.get('passed') is False and previous.get('recoveryVerified') is False and previous.get('batchFatal') is False and previous.get('childrenConverged') is True and previous.get('boundaryEligible') is True and command['kind'] == 'search' and command['seconds'] == 60 and instant(previous['finishedAt']) <= registered, 'original S7 failed recovery and genuine upper-bound evidence must remain')
    metrics = result.get('result', {})
    require(result.get('nativeScenario', {}).get('complete') is True and result.get('finalFactsValid') is True and result.get('phaseAccountingValid') is True and result.get('fixtureManifestSHA256') == final['fixtureSHA256'] and metrics.get('mutationCounterMatchesDB') is True and type(metrics.get('attempted')) is int and metrics['attempted'] > 0 and metrics['attempted'] == metrics.get('completed') == metrics.get('mutation') and metrics.get('actualOfferedRatio', 0) >= .99 and all(type(metrics.get(key)) is int and metrics[key] == 0 for key in ('noop', 'rejected', 'contentErrors', 'dropped', 'unknown', 'classificationUnknown', 'effectUnknown', 'httpErrors', 'systemErrors')), 'only complete error-free S7 performance overload may await later drain')
    for phase in ('warm', 'stable'):
        terminal, report = artifact(phase + 'Terminal'), artifact(phase + 'Report')
        require(terminal.get('backend') == 'pts' and terminal.get('remoteConverged') is True and terminal.get('nativeEvidenceComplete') is True and terminal.get('reportId') == report.get('reportId') and isinstance(report.get('reportId'), str) and report['reportId'] and report.get('ended') is True and isinstance(report.get('startTimeRaw'), str) and isinstance(report.get('endTimeRaw'), str) and len(report['endTimeRaw']) >= 19 and dt.datetime.fromisoformat(report['endTimeRaw']) >= dt.datetime.fromisoformat(report['startTimeRaw']), 'both actual S7 PUT phases require matching real provider EndTime')
    exited = artifact('localExit'); owners = exited.get('owners', [])
    expected = {(p['pid'], p['created']) for p in previous.get('knownOwnedProcesses', [])}
    require(owners and expected and expected <= {(p.get('pid'), p.get('created')) for p in owners} and all(p.get('alive') is False and p.get('identityChecked') is True for p in owners) and all(exited.get(key) is False for key in ('observerAlive', 'controllerAlive', 'wrapperAlive')), 'all original S7 local load owners must be gone before the candidate')
    reconciliation = artifact('reconciliation')
    facts, derived, timeline = reconciliation.get('facts', {}), reconciliation.get('derived', {}), reconciliation.get('timeline', [])
    require(reconciliation.get('valid') is True and reconciliation.get('factsValid') is True and reconciliation.get('downstreamConverged') is True and reconciliation.get('errors') == [] and type(reconciliation.get('startedAt')) in (int, float) and type(reconciliation.get('finishedAt')) in (int, float) and instant(previous['finishedAt']).timestamp() <= reconciliation['startedAt'] / 1000 <= reconciliation['finishedAt'] / 1000 <= registered.timestamp(), 'supplementary later reconciliation cannot rewrite S7 sixty-second recovery')
    require(timeline and timeline[-1].get('pending') == timeline[-1].get('parked') == 0 and timeline[-1].get('receiptErrors') == [] and derived.get('available') is True and derived.get('errors') == [] and len(derived.get('notes', [])) == 512 and len(facts.get('facts', [])) == len(facts.get('notes', [])) == 512 and facts.get('transitions') == facts.get('eventCount') and facts['eventCount'] >= metrics['mutation'] and sum(r.get('version', -1) for r in facts['facts']) == facts['eventCount'] and all(type(r.get('version')) is int and r['version'] >= 0 and r.get('active') == r['version'] % 2 for r in facts['facts']) and all(n.get('liked') == n.get('activeRelations') for n in facts['notes']), 'all later authoritative facts, versions, counters and Redis projections must converge')
    total = facts['eventCount']; receipts = facts.get('receipts', [])
    require(sum(d.get('count', 0) for d in facts.get('delivery', []) if d.get('status') == 'DONE') == total and not any(d.get('status') != 'DONE' and d.get('count', 0) for d in facts.get('delivery', [])) and not any(p.get('state') == 'PARKED' and p.get('count', 0) for p in facts.get('parked', [])) and all(sum(r.get('count', 0) for r in receipts if r.get('projection') == group and r.get('state') == 'IGNORED') == total for group in ('follow-v1', 'feed-v1', 'search-v1')), 'all delayed Outbox and independent consumer receipts must drain')
    for c in records[7:]:
        require(c['tag'] in ('S8', 'H1', 'H2') and c.get('reservationCheck', {}).get('delayedRecoverySHA256') == digest and instant(c['at']) >= registered, 'only the reserved candidate and two formals can follow fixed S7 delayed recovery')
    return {**value, 'sha256': digest}


def tool_repair_transition(root, records):
    """Permit one S4 tool failure to consume its slot, retaining its unknowns."""
    path = Path(root) / 'tool-repair-transition.json'
    if not path.exists():
        require(not any(c.get('reservationCheck', {}).get('toolRepairTransitionSHA256') for c in records), 'tool repair transition cannot be removed')
        return None
    require(root == ROOT / 'linux' and path.is_file() and not path.is_symlink(), 'fixed Linux tool repair transition required')
    value, digest = read(path), sha(path)
    require(isinstance(value, dict) and type(value.get('version')) is int and value['version'] == 1 and value.get('batch') == 'CAP-10' and value.get('authorized') is True and value.get('afterTag') == 'S4' and value.get('nextTag') == 'S5' and type(value.get('firstRate')) is int and value['firstRate'] == 800 and value.get('settleSeconds') == 10, 'only the fixed S4 to S5 tool repair is authorized')
    require(isinstance(value.get('userAuthorizationReference'), str) and value['userAuthorizationReference'].strip() and isinstance(value.get('recordedAt'), str), 'explicit continuous repair authorization required')
    registered = instant(value['recordedAt'])
    require(registered <= dt.datetime.now(dt.timezone.utc) and len(records) >= 4 and [c['tag'] for c in records[:4]] == ['S1', 'S2', 'S3', 'S4'], 'repair must follow all four retained short reservations')
    recovery = pre_load_recovery(root, records)
    require(recovery and value.get('preLoadRecoverySHA256') == recovery['sha256'], 'original S1 exception chain must remain intact')
    require(isinstance(value.get('artifacts'), dict), 'tool repair artifact references required')
    def artifact(name, ndjson=False):
        ref = value['artifacts'].get(name, {})
        require(isinstance(ref, dict) and isinstance(ref.get('path'), str), 'tool repair artifact missing: ' + name)
        relative = Path(ref['path']); candidate = Path(root) / relative
        require(not relative.is_absolute() and '..' not in relative.parts and candidate.is_file() and not candidate.is_symlink() and candidate.resolve() == candidate and candidate.is_relative_to(root) and ref.get('sha256') == sha(candidate), 'tool repair artifact bytes or path differ: ' + name)
        return [json.loads(line) for line in candidate.read_bytes().splitlines() if line.strip()] if ndjson else read(candidate)
    command, previous = artifact('command'), artifact('stage')
    require(value['artifacts']['command']['path'] == 'S4-command.json' and value['artifacts']['stage']['path'] == 'S4-stage.json' and command == records[3] and previous == stage(root, command) and previous.get('passed') is False and command['kind'] == 'search' and command['rate'] == 800 and command['seconds'] == 60 and instant(previous['finishedAt']) <= registered, 'original failed S4 must be retained without relabeling')
    for key, actual in (('sourceSha', command['apiSourceSha']), ('binarySHA256', command['binarySHA256']), ('environmentSHA256', command['environmentSHA256']), ('fixtureSHA256', command['fixtureSHA256'])):
        require(value.get(key) == actual, 'repair cannot change application, environment or fixture: ' + key)
    old, new = value.get('oldIdentity'), value.get('newIdentity')
    require(isinstance(old, dict) and isinstance(new, dict) and isinstance(new.get('scriptHashes'), dict) and new['scriptHashes'] and re.fullmatch(r'[a-f0-9]{64}', new.get('observerBinarySHA256', '')), 'old and new tool identities required')
    for key in ('scriptHashes', 'observerBinarySHA256', 'ptsPacketSHA256', 'ptsAdmissionSHA256'):
        require(old.get(key) == command.get(key), 'old S4 tool identity cannot be overwritten: ' + key)
    for key, filename in (('ptsPacketSHA256', 'pts-packet.json'), ('ptsAdmissionSHA256', 'pts-scene-admission.json')):
        require(old[key] == sha(Path(root) / filename), 'original PTS packet and admission must remain')
    require(new.get('ptsPacketPath') == 'pts-packet-repair1.json' and new.get('ptsAdmissionPath') == 'pts-scene-admission-repair1.json', 'new PTS identities require distinct fixed paths')
    for key, filename in (('ptsPacketSHA256', new['ptsPacketPath']), ('ptsAdmissionSHA256', new['ptsAdmissionPath'])):
        require(new.get(key) == sha(Path(root) / filename), 'new PTS packet/admission bytes differ')
    packet, admission = read(Path(root) / new['ptsPacketPath']), read(Path(root) / new['ptsAdmissionPath'])
    require(admission.get('passed') is True and admission.get('agentCount') == 2 and admission.get('concurrency') == 513 and admission.get('duration') == 600 and packet.get('sceneFilesMD5') == admission.get('sceneFilesMD5'), 'new PTS packet must have matching read-only scene admission')
    optimization = claim_optimization(root, records)
    for index, c in enumerate(records[4:], 4):
        identity = optimization['afterIdentity'] if optimization and index >= 5 else {**new, 'sourceSha': value['sourceSha'], 'binarySHA256': value['binarySHA256']}
        require(c.get('reservationCheck', {}).get('toolRepairTransitionSHA256') == digest and instant(c['at']) >= registered and c.get('apiSourceSha') == identity['sourceSha'] and c.get('binarySHA256') == identity['binarySHA256'] and all(c.get(key) == identity[key] for key in ('scriptHashes', 'observerBinarySHA256', 'ptsPacketSHA256', 'ptsAdmissionSHA256')), 'S5 onward must use the repaired tools or the single proved Claim optimization')
    collector = artifact('collectorTerminal'); artifact('collectorRecords', True)
    if value['artifacts']['collectorTerminal']['path'] == 'S4-load/stable-pts/latest.json':
        original = Path(root) / 'S4-load/stable-pts/collector-terminal.json'
        require(not original.exists() and not original.is_symlink() and isinstance(collector.get('summary'), dict) and collector['summary'].get('stop') is True, 'only the retained stopped S4 latest snapshot may replace a missing collector terminal')
        collector = collector['summary']
    require(collector.get('phase') == 'stable' and collector.get('fixtureSHA256') == value['fixtureSHA256'] and type(collector.get('counts', {}).get('attempted')) is int and collector['counts']['attempted'] > 0 and collector.get('evidenceSHA256') == value['artifacts']['collectorRecords']['sha256'], 'retain the actual nonzero S4 client evidence and its unknowns')
    terminal_observed = instant(value['terminalObservedAt'])
    for phase in ('warm', 'stable'):
        terminal, report = artifact(phase + 'Terminal'), artifact(phase + 'Report')
        require(terminal.get('backend') == 'pts' and isinstance(terminal.get('reportId'), str) and terminal['reportId'] and report.get('reportId') == terminal['reportId'] and report.get('ended') is True and isinstance(report.get('startTimeRaw'), str) and isinstance(report.get('endTimeRaw'), str) and len(report['endTimeRaw']) >= 19 and dt.datetime.fromisoformat(report['endTimeRaw']) >= dt.datetime.fromisoformat(report['startTimeRaw']), 'every S4 phase with PUTs requires its real report EndTime')
        if phase == 'stable':
            require(terminal.get('runId') == collector.get('runId'), 'S4 stable collector and PTS report identity differ')
            acknowledgement = artifact('stableStartAudit')
            require(acknowledgement.get('action') == 'StartTestingJMeterScene' and acknowledgement.get('httpStatus') == 200 and acknowledgement.get('sceneId') == read(Path(root) / 'pts-scene-admission.json').get('sceneId') and acknowledgement.get('sanitizedRawResponse', {}).get('Success') is True and acknowledgement['sanitizedRawResponse'].get('ReportId') == terminal['reportId'], 'S4 stable report must bind its original accepted Start acknowledgement')
            if not (Path(root) / 'S4-load/stable-pts/terminal.json').exists():
                require(terminal.get('originalTerminalAbsent') is True and terminal.get('nativeEvidenceComplete') is False and value['artifacts']['stableTerminal']['path'] != 'S4-load/stable-pts/terminal.json', 'external S4 terminal verification cannot recreate a missing original')
    exited = artifact('localExit'); owners = exited.get('owners', [])
    expected = {(p['pid'], p['created']) for p in previous.get('knownOwnedProcesses', [])}
    require(owners and expected and expected <= {(p.get('pid'), p.get('created')) for p in owners} and all(p.get('alive') is False and p.get('identityChecked') is True for p in owners) and all(exited.get(key) is False for key in ('observerAlive', 'controllerAlive', 'wrapperAlive')), 'all original S4 local load owners must be proved gone')
    manifest, reconciliation = artifact('fixtureManifest'), artifact('reconciliation')
    require(value['artifacts']['fixtureManifest']['sha256'] == value['fixtureSHA256'] and reconciliation.get('valid') is True and reconciliation.get('factsValid') is True and reconciliation.get('downstreamConverged') is True and reconciliation.get('errors') == [] and type(reconciliation.get('startedAt')) in (int, float) and reconciliation['startedAt'] / 1000 >= terminal_observed.timestamp() + 10 and reconciliation.get('finishedAt', 0) >= reconciliation['startedAt'], 'S4 requires complete reconciliation after ten seconds of terminal settle')
    facts, derived = reconciliation.get('facts', {}), reconciliation.get('derived', {})
    pairs = set(zip(manifest.get('multiNoteIds', []), manifest.get('userIds', [])))
    rows, notes, events = facts.get('facts', []), facts.get('notes', []), facts.get('events', [])
    require(len(pairs) == len(rows) == len(notes) == 512 and {(r.get('noteId'), r.get('userId')) for r in rows} == pairs and {n.get('noteId') for n in notes} == {p[0] for p in pairs}, 'reconciliation must cover the original 512 exact relationships')
    by_pair = {(e.get('noteId'), e.get('userId')): e for e in events}
    require(len(by_pair) == len(events) and set(by_pair) <= pairs, 'reconciliation LIKE events must belong to exact pairs')
    for row in rows:
        version = row.get('version'); event = by_pair.get((row['noteId'], row['userId']))
        require(type(version) is int and version >= 0 and row.get('active') == version % 2 and (version == 0 and event is None or version > 0 and event and event.get('count') == event.get('distinctVersions') == event.get('maxVersion') == version and event.get('minVersion') == 1 and event.get('invalid') == 0), 'S4 relation version and unique LIKE outbox facts must reconcile')
    active = {r['noteId']: r['active'] for r in rows}
    total = sum(r['version'] for r in rows)
    require(facts.get('transitions') == facts.get('eventCount') == total and all(n.get('liked') == n.get('activeRelations') == active[n['noteId']] for n in notes) and sum(e.get('count', 0) for e in events) == total, 'S4 public counts, versions and events must agree')
    require(derived.get('available') is True and derived.get('errors') == [] and len(derived.get('notes', [])) == 512, 'full Redis projection reconciliation required')
    redis_by_note = {n.get('noteId'): n for n in derived['notes']}
    require(set(redis_by_note) == set(active), 'Redis projection must cover exact notes')
    for row in rows:
        projection = redis_by_note[row['noteId']]; versions = projection.get('versions', {})
        require(isinstance(versions, dict) and set(projection.get('members', [])) == ({row['userId']} if row['active'] else set()) and (versions.get(row['userId']) == str(row['version']) if row['version'] else row['userId'] not in versions) and set(versions) <= {row['userId'], '__complete', '__count'}, 'Redis member/version projection must match authoritative facts')
    require(sum(d.get('count', 0) for d in facts.get('delivery', []) if d.get('status') == 'DONE') == total and not any(d.get('status') != 'DONE' and d.get('count', 0) for d in facts.get('delivery', [])) and not any(p.get('state') == 'PARKED' and p.get('count', 0) for p in facts.get('parked', [])), 'Outbox and parked failures must fully drain')
    receipts = facts.get('receipts', [])
    require(all(sum(r.get('count', 0) for r in receipts if r.get('projection') == group and r.get('state') == 'IGNORED') == total and not any(r.get('projection') == group and r.get('state') == 'APPLIED' and r.get('count', 0) for r in receipts) for group in ('follow-v1', 'feed-v1', 'search-v1')), 'all three independent LIKE receipt groups must reconcile')
    return {**value, 'sha256': digest}


def _authority_ref(root, reference):
    require(isinstance(reference, dict) and isinstance(reference.get('path'), str), 'bound artifact reference required')
    relative = Path(reference['path']); path = Path(root) / relative
    require(not relative.is_absolute() and '..' not in relative.parts and path.is_file() and not path.is_symlink() and path.resolve() == path and path.is_relative_to(root) and reference.get('sha256') == sha(path), 'bound artifact path or bytes differ')
    return read(path)


def execution_context(root):
    path = ROOT / 'execution-authority-20261102.json'
    if not path.exists():
        require(not any(c.get('reservationCheck', {}).get('executionAuthoritySHA256') for c in history(root)), 'execution authority cannot be removed')
        return None
    require(root == ROOT / 'linux' and not path.is_symlink(), 'execution authority belongs to the existing Linux batch')
    authority, digest = read(path), sha(path)
    require(isinstance(authority, dict) and authority.get('version') == 1 and authority.get('batch') == 'CAP-10' and authority.get('authorized') is True and authority.get('workflowLimitsWaived') is True and type(authority.get('generation')) is int and authority['generation'] >= 1 and isinstance(authority.get('userAuthorizationReference'), str) and authority['userAuthorizationReference'].strip(), 'explicit superseding workflow authority required')
    registered = instant(authority['recordedAt']); require(registered <= dt.datetime.now(dt.timezone.utc), 'execution authority cannot be future-dated')
    records, tags, retained = history(root), authority.get('retainedTags'), authority.get('retainedArtifacts')
    require(isinstance(tags, list) and tags and len(tags) == len(set(tags)) and isinstance(retained, dict) and [c['tag'] for c in records[:len(tags)]] == tags, 'all original reservation tags must remain retained')
    for relative, expected in retained.items(): _authority_ref(ROOT, {'path': relative, 'sha256': expected})
    for command in records[:len(tags)]:
        for suffix in ('-command.json', '-stage.json'):
            matches = [directory / (command['tag'] + suffix) for directory in (ROOT, ROOT / 'linux') if (directory / (command['tag'] + suffix)).is_file()]
            require(len(matches) == 1 and retained.get(matches[0].relative_to(ROOT).as_posix()) == sha(matches[0]), 'original commands and stages cannot be changed or omitted')
    for relative in ('environment-transition.json', 'continuation-authorization.json', 'idle-retry-3-authorization.json', 'idle-retry-4-authorization.json', 'linux/pre-load-recovery.json', 'linux/pre-load-recovery-proof.json', 'linux/tool-repair-transition.json', 'linux/S5-optimization-proof.json'):
        original = ROOT / relative
        require(not original.exists() or retained.get(relative) == sha(original), 'existing recovery and optimization proofs must remain retained')
    for command in records[len(tags):]:
        reservation = command.get('reservationCheck', {})
        require(reservation.get('executionAuthoritySHA256') == digest and reservation.get('executionAuthorityGeneration') == authority['generation'] and instant(command['at']) >= registered, 'new reservations must bind the fixed execution authority')
        admission_path = Path(root) / ('admission-' + command['tag'] + '.json')
        require(admission_path.is_file() and reservation.get('admissionSHA256') == sha(admission_path) and instant(read(admission_path)['recordedAt']) <= instant(command['at']), 'each new reservation must retain its original independent admission')
    active_path = Path(root) / 'active-application.json'; active = read(active_path)
    require(not active_path.is_symlink() and active.get('version') == 1 and active.get('batch') == 'CAP-10' and active.get('authorityGeneration') == authority['generation'] and isinstance(active.get('deploymentId'), str) and active['deploymentId'] and re.fullmatch(r'[a-f0-9]{40}', active.get('sourceSha', '')) and re.fullmatch(r'[a-f0-9]{64}', active.get('binarySHA256', '')) and isinstance(active.get('scriptHashes'), dict) and active['scriptHashes'], 'current application and deployment identity required')
    deployment = active.get('deployment', {})
    require(all(type(deployment.get(key)) is int and deployment[key] > 0 for key in ('relayWorkers', 'gomaxprocs', 'mysqlMaxOpen')), 'effective worker and connection/CPU configuration must be declared')
    require(active.get('environmentSHA256') == sha(Path(root) / 'environment.json'), 'current environment metadata differs')
    _authority_ref(root, {'path': active['fixturePath'], 'sha256': active['fixtureSHA256']})
    packet = _authority_ref(root, {'path': active['ptsPacketPath'], 'sha256': active['ptsPacketSHA256']})
    admission = _authority_ref(root, {'path': active['ptsAdmissionPath'], 'sha256': active['ptsAdmissionSHA256']})
    require(admission.get('passed') is True and packet.get('sceneFilesMD5') == admission.get('sceneFilesMD5'), 'current PTS packet must retain its real scene admission')
    ready = _authority_ref(root, active['readiness'])
    require(ready.get('ready') is True and ready.get('readyBody', {}).get('version') == 'capacity-' + active['sourceSha'], 'positive readiness must belong to the current source')
    return {'authority': authority, 'authoritySHA256': digest, 'active': active, 'activeApplicationSHA256': sha(active_path)}


def _active_command(command, context):
    active, reservation = context['active'], command.get('reservationCheck', {})
    return (reservation.get('executionAuthoritySHA256') == context['authoritySHA256'] and
            reservation.get('executionAuthorityGeneration') == context['authority']['generation'] and
            reservation.get('deploymentId') == active['deploymentId'] and command.get('apiSourceSha') == active['sourceSha'] and
            all(command.get(key) == active[key] for key in ('binarySHA256', 'environmentSHA256', 'fixtureSHA256', 'scriptHashes', 'observerBinarySHA256', 'ptsPacketSHA256', 'ptsAdmissionSHA256')))


def zero_start_layout(root, command, previous):
    """Only an uncreated wrapper and an untouched load path can have no task."""
    tag = command.get('tag', '')
    if not (re.fullmatch(r'[SH][1-9][0-9]*', tag) and command.get('loadBackend') == 'pts'
            and 'wrapperPid' not in previous and 'wrapperExit' in previous and previous['wrapperExit'] is None
            and type(previous.get('observerPid')) is int and previous['observerPid'] > 0
            and type(previous.get('observerExit')) is int and previous['observerExit'] != 0
            and previous.get('knownOwnedProcesses') == [] and previous.get('childrenConverged') is True
            and previous.get('passed') is False and previous.get('batchFatal') is False
            and previous.get('outcome') == 'measurement_invalid'
            and isinstance(previous.get('failedStartOrException'), str) and previous['failedStartOrException']):
        return None
    load = Path(root) / (tag + '-load')
    paths = [load / name for name in ('process.json', 'warm-process.json', 'stable-process.json', 'warm-pts', 'stable-pts', 'result.json')]
    if load.is_symlink() or load.exists() and not load.is_dir() or any(path.exists() or path.is_symlink() for path in paths):
        return None
    if load.exists() and any(path.is_file() or path.is_symlink() or not path.is_dir() for path in load.rglob('*')):
        return None
    return {'loadPath': load.relative_to(root).as_posix(), 'loadDirectoryExists': load.exists(),
            'absentPaths': [path.relative_to(root).as_posix() for path in paths], 'loadFiles': []}


def authority_zero_start(root, command, previous, terminal, context):
    layout = zero_start_layout(root, command, previous)
    require(layout is not None and terminal.get('reports') == [] and terminal.get('noLoadPaths') == layout,
            'zero-start recovery requires an uncreated wrapper and no phase or load evidence')
    require(terminal.get('originalCommand') == {'path': command['tag'] + '-command.json', 'sha256': sha(Path(root) / (command['tag'] + '-command.json'))}
            and _authority_ref(root, terminal['originalCommand']) == command,
            'zero-start recovery must retain the exact original command')
    require(terminal.get('originalStage') == {'path': command['tag'] + '-stage.json', 'sha256': sha(Path(root) / (command['tag'] + '-stage.json'))}
            and _authority_ref(root, terminal['originalStage']) == previous,
            'zero-start recovery must retain the exact original failed stage')
    observed = instant(terminal['zeroStartObservedAt'])
    require(instant(previous['finishedAt']) <= observed <= dt.datetime.now(dt.timezone.utc)
            and terminal.get('originalObserverExit') == previous['observerExit']
            and terminal.get('originalWrapperExit') is None, 'zero-start proof must follow the failed observer exit')
    observer = terminal.get('zeroStartObserver', {})
    require(observer.get('pid') == previous['observerPid'] and observer.get('created') is None
            and observer.get('pidAbsent') is True and observer.get('alive') is False
            and observer.get('identityChecked') is True and observer in terminal.get('owners', []),
            'unrecorded observer birth requires proof that its exact PID is completely absent')
    audit = _authority_ref(root, terminal['zeroStartIdleAudit'])
    scene = read(Path(root) / context['active']['ptsAdmissionPath']).get('sceneId')
    raw = audit.get('sanitizedRawResponse', {})
    require(isinstance(scene, str) and scene and audit.get('sceneId') == scene
            and audit.get('action') == 'GetJMeterSceneRunningData' and audit.get('regionId') == 'cn-shanghai'
            and audit.get('httpStatus') == 200 and raw.get('Success') is True
            and set(raw) == {'RequestId', 'Success'} and isinstance(raw.get('RequestId'), str) and raw['RequestId'].strip()
            and instant(previous['finishedAt']) <= instant(audit['requestStartedAt'])
            <= instant(audit['responseFinishedAt']) <= observed,
            'zero-start recovery needs the real successful exact-empty signed PTS read after failure')
    return True


def warm_only_layout(root, command, previous):
    """A failed warm may have a real report while setup/stable never existed."""
    tag = command.get('tag', '')
    if not (re.fullmatch(r'[SH][1-9][0-9]*', tag) and command.get('loadBackend') == 'pts'
            and previous.get('passed') is False and type(previous.get('wrapperExit')) is int
            and previous['wrapperExit'] != 0):
        return None
    load = Path(root) / (tag + '-load')
    result_path, marker = load / 'result.json', load / 'warm-process.json'
    if load.is_symlink() or result_path.is_symlink() or not marker.is_file() or marker.is_symlink():
        return None
    missing_result = not result_path.exists()
    if not missing_result and not result_path.is_file():
        return None
    if missing_result and not (previous.get('outcome') == 'correctness_stop'
            and previous.get('failedStartOrException') == 'FileNotFoundError'
            and previous.get('childrenConverged') is False and previous.get('batchFatal') is True
            and type(previous.get('observerExit')) is int and previous.get('knownOwnedProcesses')):
        return None
    result, process = (None if missing_result else read(result_path)), read(marker)
    forbidden = ('setup', 'stableExit', 'stableStartedAt', 'stableEndedAt', 'stableWrapperStartedAt',
                 'stableWrapperEndedAt', 'stableCommandElapsedMs', 'result')
    if not missing_result and not (result.get('loadBackend') == 'pts' and type(result.get('warmExit')) is int and result['warmExit'] != 0
            and result.get('nativeScenario', {}).get('complete') is False and not any(result.get(key) is not None for key in forbidden)):
        return None
    if not (process.get('phase') == 'warm' and process.get('backend') == 'pts'
            and type(process.get('controller_pid')) is int and process['controller_pid'] > 0
            and type(process.get('controller_started_unix_ms')) in (int, float)):
        return None
    if missing_result and not any(owner.get('pid') == process['controller_pid']
            and abs(owner.get('created', 0) - process['controller_started_unix_ms'] / 1000) <= .002
            for owner in previous['knownOwnedProcesses']):
        return None
    absent = [load / name for name in ('stable-process.json', 'stable-pts', 'stable-summary.json',
        'stable-console.txt', 'stable-private-input.json', 'stable-private-input-removed.json',
        'stable-db-before.json', 'stable-db-after.json', 'stable-capacity-before.json',
        'stable-capacity-after.json', 'before.json', 'after.json', 'setup-transaction.json', 'setup-reconcile.json')]
    if any(path.exists() or path.is_symlink() for path in absent) or any(path.name.startswith('stable') for path in load.rglob('*')):
        return None
    if missing_result and any(path.name.startswith('setup') for path in load.rglob('*')):
        return None
    current = load / 'process.json'
    if current.exists() and (current.is_symlink() or read(current) != process):
        return None
    starts = list((load / 'warm-pts/api').glob('*StartTestingJMeterScene.json'))
    if len(starts) != 1:
        return None
    return {'absentPaths': [path.relative_to(root).as_posix() for path in absent],
            'warmStartAuditPath': starts[0].relative_to(root).as_posix(),
            **({'missingResult': {'path': result_path.relative_to(root).as_posix(), 'exists': False}}
               if missing_result else {})}


def authority_warm_only(root, command, previous, terminal, context):
    layout = warm_only_layout(root, command, previous)
    reports = terminal.get('reports', [])
    zero_cancel = terminal.get('terminationKind') == 'warm_zero_native_cancel'
    require(layout is not None and terminal.get('noStablePaths') == layout
            and (layout.get('missingResult') and reports == [] if zero_cancel
                 else len(reports) == 1 and reports[0].get('phase') == 'warm'),
            'warm-only recovery requires failed warm and no setup or stable evidence')
    missing = layout.get('missingResult')
    originals = [('originalCommand', '-command.json'), ('originalStage', '-stage.json')]
    if missing:
        require('originalResult' in terminal and terminal['originalResult'] is None
                and terminal.get('missingOriginalResult') == missing,
                'missing wrapper result must be explicitly bound as absent, never fabricated')
        expected = {(owner['pid'], owner['created']) for owner in previous['knownOwnedProcesses']}
        owners = terminal.get('owners', [])
        require(all(terminal.get(key) is False for key in ('observerAlive', 'controllerAlive', 'wrapperAlive'))
                and expected <= {(owner.get('pid'), owner.get('created')) for owner in owners}
                and all(owner.get('alive') is False and owner.get('identityChecked') is True for owner in owners),
                'missing-result recovery requires every exact original owner to have exited')
    else:
        require(terminal.get('missingOriginalResult') is None, 'an existing result cannot be declared missing')
        originals.append(('originalResult', '-load/result.json'))
    for key, suffix in originals:
        reference = {'path': command['tag'] + suffix, 'sha256': sha(Path(root) / (command['tag'] + suffix))}
        require(terminal.get(key) == reference, 'warm-only recovery must bind the exact original ' + key)
        value = _authority_ref(root, reference)
        require(key not in ('originalCommand', 'originalStage') or value == (command if key == 'originalCommand' else previous), 'warm-only original bytes differ')
    process = _authority_ref(root, terminal['warmProcess'])
    require(terminal['warmProcess']['path'] == command['tag'] + '-load/warm-process.json', 'warm process must be its original marker')
    require(any(owner.get('pid') == process['controller_pid'] and abs(owner.get('created', 0) - process['controller_started_unix_ms'] / 1000) <= .002
                and owner.get('alive') is False and owner.get('identityChecked') is True for owner in terminal.get('owners', [])), 'exact warm controller must be stopped')
    if zero_cancel:
        return authority_warm_zero_native_cancel(root, command, previous, terminal, context, layout)
    report = reports[0]
    require(report.get('ended') is True and isinstance(report.get('reportId'), str) and report['reportId']
            and isinstance(report.get('startTimeRaw'), str) and isinstance(report.get('endTimeRaw'), str)
            and len(report['endTimeRaw']) >= 19 and dt.datetime.fromisoformat(report['endTimeRaw']) >= dt.datetime.fromisoformat(report['startTimeRaw']), 'warm-only phase needs its real report EndTime')
    start_ref = report['originalStartAudit']; start = _authority_ref(root, start_ref)
    scene = read(Path(root) / context['active']['ptsAdmissionPath']).get('sceneId')
    require(start_ref['path'] == layout['warmStartAuditPath'] and start.get('action') == 'StartTestingJMeterScene'
            and start.get('sceneId') == scene and start.get('httpStatus') == 200
            and start.get('sanitizedRawResponse', {}).get('Success') is True
            and start['sanitizedRawResponse'].get('ReportId') == report['reportId'], 'warm report must match the unique original Start acknowledgement')
    original_report = _authority_ref(root, terminal['warmReport'])
    require(all(original_report.get(key) == report.get(key) for key in ('reportId', 'ended', 'startTimeRaw', 'endTimeRaw', 'requestId')), 'warm report projection differs from its bound original')
    audit = _authority_ref(root, terminal['warmReportAudit']); raw = audit.get('sanitizedRawResponse', {})
    overview = raw.get('ReportOverView', {})
    require(audit.get('action') == 'GetJMeterReportDetails' and audit.get('httpStatus') == 200 and raw.get('Success') is True
            and isinstance(raw.get('RequestId'), str) and raw['RequestId'] and raw['RequestId'] == report.get('requestId')
            and overview.get('StartTime') == report['startTimeRaw'] and overview.get('EndTime') == report['endTimeRaw']
            and overview.get('ReportId', report['reportId']) == report['reportId'], 'warm-only EndTime needs the real signed report response')
    return True


def authority_warm_zero_native_cancel(root, command, previous, terminal, context, layout):
    """Cancellation before any collector registration is never an ended report."""
    root = Path(root); prefix = command['tag'] + '-load/warm-pts/'
    scene = read(root / context['active']['ptsAdmissionPath']).get('sceneId')
    start_ref = terminal['originalStartAudit']; start = _authority_ref(root, start_ref)
    report_id = terminal.get('cancelledReportId')
    require(isinstance(report_id, str) and report_id and start_ref['path'] == layout['warmStartAuditPath']
            and start.get('action') == 'StartTestingJMeterScene' and start.get('sceneId') == scene
            and start.get('regionId') == 'cn-shanghai' and start.get('httpStatus') == 200
            and start.get('sanitizedRawResponse', {}).get('Success') is True
            and start['sanitizedRawResponse'].get('ReportId') == report_id,
            'zero-native cancellation must bind the exact original acknowledged Start')
    stop_ref = terminal['originalStopAudit']; stop = _authority_ref(root, stop_ref)
    raw = stop.get('sanitizedRawResponse', {})
    require(stop_ref['path'].startswith(prefix + 'api/') and stop.get('action') == 'StopTestingJMeterScene'
            and stop.get('sceneId') == scene and stop.get('regionId') == 'cn-shanghai'
            and stop.get('httpStatus') == 200 and raw.get('Success') is True and raw.get('Code', '') == ''
            and isinstance(raw.get('RequestId'), str) and raw['RequestId'].strip(),
            'zero-native cancellation requires the original successful signed scene Stop acknowledgement')
    observed = instant(terminal['cancellationObservedAt'])
    require(instant(start['requestStartedAt']) <= instant(start['responseFinishedAt'])
            <= instant(stop['requestStartedAt']) <= instant(stop['responseFinishedAt'])
            <= observed <= dt.datetime.now(dt.timezone.utc), 'cancellation audit chronology differs')
    idle_refs = terminal.get('emptyRunningAudits', [])
    require(len(idle_refs) == 2 and idle_refs[0] != idle_refs[1], 'two independent post-stop empty running reads required')
    idle_times, request_ids = [], set()
    for reference in idle_refs:
        audit = _authority_ref(root, reference); response = audit.get('sanitizedRawResponse', {})
        began, ended = instant(audit['requestStartedAt']), instant(audit['responseFinishedAt'])
        require(audit.get('action') == 'GetJMeterSceneRunningData' and audit.get('sceneId') == scene
                and audit.get('regionId') == 'cn-shanghai' and audit.get('httpStatus') == 200
                and set(response) == {'RequestId', 'Success'} and response.get('Success') is True
                and isinstance(response.get('RequestId'), str) and response['RequestId'].strip()
                and response['RequestId'] not in request_ids
                and max(instant(stop['responseFinishedAt']), instant(previous['finishedAt'])) <= began <= ended <= observed,
                'post-stop reads must be exact successful signed empty responses after the failed stage')
        request_ids.add(response['RequestId']); idle_times.append((began, ended))
    require(idle_times[1][0] >= idle_times[0][1] + dt.timedelta(seconds=2), 'empty running observations must be at least two seconds apart')
    for path in root.rglob('*StartTestingJMeterScene.json'):
        audit = read(path)
        if audit.get('sceneId') == scene and audit.get('action') == 'StartTestingJMeterScene':
            began = instant(audit['requestStartedAt'])
            require(not instant(start['requestStartedAt']) <= began <= observed
                    or path == root / start_ref['path'], 'a different Start exists inside the cancellation observation interval')
    journal_ref = terminal['nativeJournal']; journal_path = root / journal_ref['path']
    require(journal_ref['path'] == prefix + 'records.ndjson' and journal_path.is_file()
            and not journal_path.is_symlink() and journal_path.resolve() == journal_path
            and journal_ref.get('sha256') == sha(journal_path), 'original zero-native journal bytes must be retained')
    journal = [json.loads(line) for line in journal_path.read_bytes().splitlines()]
    require(len(journal) == 2 and [row.get('kind') for row in journal] == ['configuration', 'stop'],
            'any native register, batch, completion or additional journal record forbids zero-native cancellation')
    identity = journal[0].get('configuration', {})
    identity_hash = hashlib.sha256(json.dumps(identity, sort_keys=True, separators=(',', ':'), ensure_ascii=False, allow_nan=False).encode()).hexdigest()
    summary_ref = terminal['nativeSummary']; latest = _authority_ref(root, summary_ref); summary = latest.get('summary', {})
    require(summary_ref['path'] == prefix + 'latest.json' and journal[0].get('hash') == identity_hash == summary.get('hash')
            and all(summary.get(key) == value for key, value in identity.items())
            and identity.get('phase') == 'warm' and identity.get('rate') == command.get('warmRate')
            and identity.get('seconds') == command.get('warmSeconds') and identity.get('expectedEngines') == 2
            and identity.get('fixtureSHA256') == command.get('fixtureSHA256')
            and identity.get('scriptSHA256') == command.get('scriptHashes', {}).get('pts/cap10-jmeter.groovy')
            and isinstance(identity.get('runId'), str) and identity['runId']
            and summary.get('startUnixMs') is None and summary.get('endUnixMs') is None
            and summary.get('stop') is True and summary.get('complete') is False and summary.get('engines') == []
            and isinstance(journal[1].get('reason'), str) and journal[1]['reason'] == summary.get('stopReason')
            and summary.get('evidenceSHA256') == journal_ref['sha256'],
            'zero-native summary must match the exact frozen unregistered stopped collector identity and journal')
    counts = summary.get('counts', {})
    require(counts.get('planned') == command['warmRate'] * command['warmSeconds']
            and all(type(counts.get(key)) is int and counts[key] == 0 for key in
                    ('attempted', 'completed', 'mutation', 'noop', 'uncertain', 'rejected', 'invalid', 'dropped')),
            'zero-native cancellation cannot contain any offered or terminal native request')
    before_ref = terminal['beforeWarm']; before = _authority_ref(root, before_ref)
    reconciliation = _authority_ref(root, terminal['cancellationReconciliation']); after = reconciliation.get('facts', {})
    require(before_ref['path'] == command['tag'] + '-load/warm-before.json'
            and reconciliation.get('valid') is True and reconciliation.get('factsValid') is True
            and reconciliation.get('downstreamConverged') is True and reconciliation.get('errors') == []
            and instant(previous['finishedAt']).timestamp() * 1000 <= reconciliation.get('startedAt', 0)
            <= reconciliation.get('finishedAt', 0) <= observed.timestamp() * 1000
            and len(before.get('facts', [])) == len(before.get('notes', [])) == 512
            and all(key in before and key in after and before[key] == after[key]
                    for key in ('facts', 'notes', 'events', 'transitions', 'eventCount')),
            'zero-native cancellation needs later complete reconciliation with no authoritative fixture fact or event change')
    return True


def authority_search_state(root, context):
    records = [c for c in history(root) if _active_command(c, context) and c['tag'].startswith('S') and c['kind'] != 'candidate']
    relevant = [(c, stage(root, c)) for c in records]
    passes = [(c, s) for c, s in relevant if s.get('outcome') == 'pass' and s.get('passed') is True]
    limited = [c for c, s in relevant if s.get('outcome') == 'performance_limited' and s.get('boundaryEligible') is True]
    best = max(passes, key=lambda pair: pair[0]['rate'], default=None); lower = best[0]['rate'] if best else None
    upper = min((c['rate'] for c in limited if lower is None or c['rate'] > lower), default=None)
    finished = lower is not None and (lower == 5000 or upper is not None and (upper - lower <= lower * .25 or upper - lower <= 1))
    candidate = None
    if best:
        latency = read(Path(root) / (best[0]['tag'] + '-load/result.json'))['result']['allLatency']
        candidate = lower if latency['p(95)'] <= 160 and latency['p(99)'] <= 400 else max(1, math.floor(.8 * lower))
    next_rate = (None if finished else min(4 * lower, 5000) if upper is None else (lower + upper) // 2) if lower else max(1, upper // 2) if upper else context['active'].get('initialRate', 50)
    return {'L': lower, 'U': upper, 'nextRate': next_rate, 'candidateRate': candidate, 'searchFinished': finished,
            'sourceSha': context['active']['sourceSha'], 'deploymentId': context['active']['deploymentId'],
            'boundaryTags': [c['tag'] for c, s in relevant if s.get('outcome') == 'pass' and s.get('passed') is True or s.get('outcome') == 'performance_limited' and s.get('boundaryEligible') is True]}


def authority_reservation(root, kind, rate, seconds, api, fixture, preflight, ability, freeze, tools, observer_hash, context):
    active, records = context['active'], history(root)
    require(api.get('sourceSha') == active['sourceSha'] and api.get('sha256') == active['binarySHA256'] and sha(fixture) == active['fixtureSHA256'] and tools == active['scriptHashes'] and observer_hash == active['observerBinarySHA256'], 'window must use the current exact active application and tools')
    expected_diagnostics = kind == 'diagnostic'
    require(api.get('likeDiagnosticsRequested') is expected_diagnostics and api.get('tailEnabledRequested') is False, 'comparison and capacity windows keep diagnostics disabled')
    shorts, formals = [c for c in records if c['tag'].startswith('S')], [c for c in records if c['tag'].startswith('H')]
    prefix = 'H' if kind == 'formal' else 'S'
    used_numbers = [int(c['tag'][1:]) for c in records if c['tag'].startswith(prefix)]
    used_numbers += [int(p.stem[6:]) for directory in (ROOT, ROOT / 'linux') for p in directory.glob('slot-' + prefix + '*.json') if re.fullmatch(r'slot-' + prefix + r'[1-9][0-9]*', p.stem)]
    tag = prefix + str(max(used_numbers, default=0) + 1)
    require(not any((directory / ('slot-' + tag + '.json')).exists() or (directory / (tag + '-command.json')).exists() for directory in (ROOT, ROOT / 'linux')), 'reservation tag cannot be reused')
    admission_path = Path(root) / ('admission-' + tag + '.json'); admission = read(admission_path)
    require(not admission_path.is_symlink() and admission.get('version') == 1 and admission.get('batch') == 'CAP-10' and admission.get('tag') == tag and admission.get('authoritySHA256') == context['authoritySHA256'] and admission.get('activeApplicationSHA256') == context['activeApplicationSHA256'], 'one current application admission required for each independent window')
    admitted = instant(admission['recordedAt']); require(admitted <= dt.datetime.now(dt.timezone.utc), 'window admission cannot be future-dated')
    previous = records[-1] if records else None
    if previous:
        terminal = _authority_ref(root, admission['termination']); old_stage = stage(root, previous)
        require(terminal.get('tag') == previous['tag'] and all(terminal.get(key) is False for key in ('observerAlive', 'controllerAlive', 'wrapperAlive')), 'previous exact load owners must be stopped')
        owners = terminal.get('owners', []); expected = {(p['pid'], p['created']) for p in old_stage.get('knownOwnedProcesses', [])}
        require(expected <= {(p.get('pid'), p.get('created')) for p in owners} and all(p.get('alive') is False and p.get('identityChecked') is True for p in owners), 'previous local process identities must be checked')
        reports = terminal.get('reports', [])
        if terminal.get('terminationKind') == 'zero_start':
            authority_zero_start(root, previous, old_stage, terminal, context)
            require(instant(terminal['zeroStartObservedAt']) <= admitted, 'zero-start observation must precede its window admission')
        elif terminal.get('terminationKind') in ('warm_only', 'warm_zero_native_cancel'):
            authority_warm_only(root, previous, old_stage, terminal, context)
            if terminal['terminationKind'] == 'warm_zero_native_cancel':
                require(admission.get('reconciliation') == terminal['cancellationReconciliation'],
                        'zero-native cancellation must use the same fresh full reconciliation as the next admission')
        else:
            require({r.get('phase') for r in reports} == {'warm', 'stable'} and len(reports) == 2 and all(r.get('ended') is True and isinstance(r.get('reportId'), str) and r['reportId'] and isinstance(r.get('startTimeRaw'), str) and isinstance(r.get('endTimeRaw'), str) and len(r['endTimeRaw']) >= 19 and dt.datetime.fromisoformat(r['endTimeRaw']) >= dt.datetime.fromisoformat(r['startTimeRaw']) for r in reports), 'both previous PTS phases require real matching report EndTime')
        for report in reports:
            marker = Path(root) / (previous['tag'] + '-load') / (report['phase'] + '-pts/terminal.json')
            require(not marker.exists() or read(marker).get('reportId') == report['reportId'], 'terminal report identity differs from its original phase')
    require_reconciliation = previous is None or not _active_command(previous, context) or stage(root, previous).get('passed') is not True
    if require_reconciliation or admission.get('reconciliation'):
        reconciliation = _authority_ref(root, admission['reconciliation'])
        facts, derived, timeline = reconciliation.get('facts', {}), reconciliation.get('derived', {}), reconciliation.get('timeline', [])
        require(reconciliation.get('valid') is True and reconciliation.get('factsValid') is True and reconciliation.get('downstreamConverged') is True and reconciliation.get('errors') == [] and derived.get('available') is True and derived.get('errors') == [] and len(facts.get('facts', [])) == len(facts.get('notes', [])) == len(derived.get('notes', [])) == 512 and facts.get('transitions') == facts.get('eventCount') and timeline and timeline[-1].get('pending') == timeline[-1].get('parked') == 0 and timeline[-1].get('receiptErrors') == [] and reconciliation.get('finishedAt', float('inf')) / 1000 <= admitted.timestamp(), 'fresh complete authoritative reconciliation required before continuing')
        if previous and old_stage.get('passed') is not True:
            require(reconciliation.get('startedAt', 0) / 1000 >= instant(old_stage['finishedAt']).timestamp(), 'failed window requires later reconciliation, without rewriting its old recovery result')
    if kind == 'formal':
        require(len(formals) < 2 and all(stage(root, c).get('passed') is True for c in formals), 'only the original two-formal group is supported; first formal failure ends it')
        require(isinstance(freeze, dict) and freeze.get('executionAuthoritySHA256') == context['authoritySHA256'] and freeze.get('activeApplicationSHA256') == context['activeApplicationSHA256'] and freeze.get('deploymentId') == active['deploymentId'] and freeze.get('rate') == rate and freeze.get('sourceSha') == active['sourceSha'] and all(freeze.get(key) == active[key] for key in ('binarySHA256', 'environmentSHA256', 'fixtureSHA256', 'scriptHashes', 'observerBinarySHA256', 'ptsPacketSHA256', 'ptsAdmissionSHA256')) and freeze.get('sourceCIPassed') is True and freeze.get('relatedChecksPassed') is True and freeze.get('initialDistributionContract') == {'existing': 512, 'active': 0, 'relationships': 512}, 'formal requires one exact current-source CI/identity freeze')
        candidates = [c for c in records if c['tag'] == freeze.get('candidateTag') and c['kind'] == 'candidate' and c['rate'] == rate and _active_command(c, context)]
        require(len(candidates) == 1 and stage(root, candidates[0]).get('passed') is True and stage(root, candidates[0]).get('candidateMarginPassed') is True and stage(root, candidates[0]).get('recoveryVerified') is True, 'formal requires its completed passing current-identity candidate')
        require(all(_active_command(c, context) and c.get('freezeSHA256') == sha(Path(root) / 'freeze-like.json') for c in formals), 'both formals must retain the same application and freeze')
    transition = read(ROOT / 'environment-transition.json'); anchor = instant(transition['recordedAt']); now = dt.datetime.now(dt.timezone.utc)
    intervals = [(s['loadWaitStartedAt'], s['loadWaitFinishedAt']) for c in records for s in [stage(root, c)] if s.get('loadWaitStartedAt') and s.get('loadWaitFinishedAt')]
    deducted = merged_wait_seconds(intervals, anchor, now); used = transition['effectiveWorkSecondsUsedBeforeSwitch']
    budget = {'passed': True, 'effectiveSecondsUpperBound': used + (now - anchor).total_seconds() - deducted,
              'usedBeforeSwitch': used, 'anchor': transition['recordedAt'], 'provedLoadWaitSecondsDeducted': deducted,
              'remainingBeforeSwitch': transition.get('effectiveWorkSecondsRemaining'),
              'continuationAuthorizationSHA256': sha(ROOT / 'continuation-authorization.json') if (ROOT / 'continuation-authorization.json').is_file() else None,
              'executionAuthorityGeneration': context['authority']['generation'],
              'originalMaximumSeconds': 10800, 'maximumSeconds': None, 'remainingSeconds': None, 'workflowLimitsWaived': True}
    return {'tag': tag, 'shortSlotsUsedAfter': len(shorts) + (kind != 'formal'), 'formalSlotsUsedAfter': len(formals) + (kind == 'formal'),
            'mainSecondsReservedAfter': sum(c['seconds'] for c in records) + seconds, 'shortMaximum': None, 'formalMaximum': 2,
            'mainSecondsMaximum': None, 'S8ReservedForCandidate': False, 'effectiveWorkBudget': budget,
            'elapsedConservativeSeconds': budget['effectiveSecondsUpperBound'], 'executionAuthoritySHA256': context['authoritySHA256'],
            'executionAuthorityGeneration': context['authority']['generation'], 'deploymentId': active['deploymentId'],
            'activeApplicationSHA256': context['activeApplicationSHA256'], 'admissionSHA256': sha(admission_path),
            'expectedDiagnostics': expected_diagnostics, 'failedStartsConsumeSlot': True, 'writesPerformed': False}


def search_state(root, source_sha):
    context = execution_context(root)
    if context:
        require(source_sha == context['active']['sourceSha'], 'search bounds belong to the current active source')
        return authority_search_state(root, context)
    records = history(root)
    recovery = pre_load_recovery(root, records)
    repair = tool_repair_transition(root, records)
    optimization = claim_optimization(root, records)
    boundary_records = records[5:] if optimization else records[4:] if repair else records
    final_path = Path(root) / 'final-application.json'
    if final_path.is_file() and not repair:
        final = read(final_path)
        after = final.get('boundaryEvidenceAfter') if final.get('sourceSha') == source_sha else None
        if after:
            indexes = [index for index, command in enumerate(records) if command['tag'] == after]
            require(len(indexes) == 1, 'rollback boundary reset must name a retained reservation')
            boundary_records = records[indexes[0] + 1:]
    relevant = [(c, stage(root, c)) for c in boundary_records if c['tag'].startswith('S') and c['kind'] != 'candidate' and c['apiSourceSha'] == source_sha and (not recovery or c['tag'] != recovery['failedTag'])]
    passes = [(c, s) for c, s in relevant if s.get('outcome') == 'pass']
    limited = [(c, s) for c, s in relevant if s.get('outcome') == 'performance_limited' and s.get('boundaryEligible') is True]
    best = max(passes, key=lambda pair: pair[0]['rate'], default=None)
    lower = best[0]['rate'] if best else None
    higher = [c['rate'] for c, _ in limited if lower is None or c['rate'] > lower]
    upper = min(higher) if higher else None
    result = {'L': lower, 'U': upper, 'sourceSha': source_sha, 'nextRate': None, 'searchFinished': False, 'candidateRate': None,
              'boundaryTags': [c['tag'] for c, _ in relevant]}
    used = sum(c['tag'].startswith('S') for c in records)
    if not relevant:
        result['nextRate'] = 800 if optimization else repair['firstRate'] if repair else 50
    elif lower is None:
        current = relevant[-1][0]['rate']
        if current == 1 or used >= 7:
            result.update(searchFinished=True, reason='no passing lower bound; candidate is forbidden')
        else:
            result['nextRate'] = max(1, current // 2)
    else:
        if used >= 7 or lower == 5000 or (upper is not None and (upper - lower <= lower * .25 or upper - lower <= 1)):
            result.update(searchFinished=True, reason='declared search stop reached')
        else:
            result['nextRate'] = min(4 * lower, 5000) if upper is None else (lower + upper) // 2
        report = read(Path(root) / (best[0]['tag'] + '-load') / 'result.json')
        latency = report['result']['allLatency']
        result['candidateRate'] = lower if latency['p(95)'] <= 160 and latency['p(99)'] <= 400 else max(1, math.floor(.8 * lower))
        result['lowerEvidence'] = best[0]['tag']
    return result

def check_reservation(root, kind, rate, seconds, api_metadata, fixture, preflight, ability, freeze=None, prediction=None, current_tools=None, observer_hash=None):
    root, fixture = Path(root).resolve(), Path(fixture).resolve()
    require(root in (ROOT, ROOT / 'linux') and fixture.is_file() and fixture.is_relative_to(root), 'fixed CAP10 public paths required')
    require(kind in KINDS and type(rate) is int and 1 <= rate <= 5000, 'invalid CAP10 purpose/rate')
    require(seconds == (180 if kind == 'formal' else 60), 'CAP10 duration differs from purpose')
    source = api_metadata['sourceSha']; binary = api_metadata['sha256']
    require(re.fullmatch(r'[a-f0-9]{40}', source or '') and api_metadata['expectedVersion'] == 'capacity-' + source, 'clean fixed source/version required')
    require(sha(api_metadata['exe']) == binary, 'application binary bytes differ')
    environment, fixture_hash = sha(root / 'environment.json'), sha(fixture)
    require(preflight.get('passed') is True and preflight.get('environmentSHA256') == environment, 'one valid idle preflight is required before load')
    require(ability.get('rate') == rate and ability.get('sufficient') is True and ability.get('maxVUs') == 512 and ability.get('exclusiveRelationships') == 512 and ability.get('classificationIncluded') is True and ability.get('evidence'), 'declared generator ability evidence required for every window')
    require(type(ability.get('estimatedInFlight')) in (int, float) and 0 <= ability['estimatedInFlight'] <= 512, 'estimated in-flight work exceeds exclusive VU pool')
    context = execution_context(root)
    if context:
        return authority_reservation(root, kind, rate, seconds, api_metadata, fixture, preflight, ability, freeze, current_tools, observer_hash, context)
    records = history(root)
    optimization = claim_optimization(root, records)
    expected_diagnostics = kind == 'diagnostic' or kind == 'comparison' and not optimization
    require(api_metadata.get('likeDiagnosticsRequested') is expected_diagnostics and api_metadata.get('tailEnabledRequested') is False, 'Like/Feed observation switches differ from purpose')
    budget = effective_work_budget(root, api_metadata)
    records = history(root)
    recovery = pre_load_recovery(root, records)
    repair = tool_repair_transition(root, records)
    delayed = delayed_recovery(root, records, optimization)
    shorts = [c for c in records if c['tag'].startswith('S')]; formals = [c for c in records if c['tag'].startswith('H')]
    effective_shorts = [c for c in shorts if (not recovery or c['tag'] != recovery['failedTag']) and (not repair or int(c['tag'][1:]) >= 5) and (not optimization or int(c['tag'][1:]) >= 6)]
    if optimization:
        final_identity = optimization['afterIdentity']
        require(kind != 'diagnostic' and source == final_identity['sourceSha'] and binary == final_identity['binarySHA256'] and environment == final_identity['environmentSHA256'] and fixture_hash == final_identity['fixtureSHA256'] and current_tools == final_identity['scriptHashes'] and observer_hash == final_identity['observerBinarySHA256'], 'one optimized Claim identity and unchanged comparison workload required')
    elif repair:
        new = repair['newIdentity']
        require(kind not in ('diagnostic', 'comparison') and source == repair['sourceSha'] and binary == repair['binarySHA256'] and environment == repair['environmentSHA256'] and fixture_hash == repair['fixtureSHA256'] and current_tools == new['scriptHashes'] and observer_hash == new['observerBinarySHA256'], 'tool repair cannot change business code or its fixed new tools')
    elif recovery and effective_shorts:
        require(current_tools == effective_shorts[0].get('scriptHashes') and observer_hash == effective_shorts[0].get('observerBinarySHA256'), 'recovered tools and observer are frozen from S2 onward')
    require(sum(c['seconds'] for c in records) + seconds <= 840, '840s total main-load budget exhausted')
    for c in records:
        if recovery and c['tag'] == recovery['failedTag'] or repair and c['tag'] == 'S4' or optimization and c['tag'] == 'S5':
            continue  # The immutable failed slot remains consumed, never passed.
        previous = stage(root, c)
        require(previous.get('childrenConverged') is True, 'prior owned children have not converged')
        require(previous.get('batchFatal') is not True, 'correctness/unknown/noop ended the batch')
        require(previous.get('outcome') in ('pass', 'performance_limited'), 'measurement/generator/environment pause: do not invent a service upper bound')
        require(previous.get('recoveryVerified') is True or delayed and c['tag'] == 'S7', 'previous window did not recover within its 60s budget')
    require(not any(c['kind'] == 'candidate' for c in shorts) or kind == 'formal', 'one candidate only; no later search or replacement')
    if kind == 'formal':
        if repair:
            require(freeze and freeze.get('toolRepairTransitionSHA256') == repair['sha256'], 'formal freeze must bind the one tool repair transition')
        if optimization:
            require(freeze and freeze.get('claimOptimizationSHA256') == optimization['sha256'], 'formal freeze must bind the one Claim optimization')
        if delayed:
            require(freeze and freeze.get('delayedRecoverySHA256') == delayed['sha256'], 'formal freeze must retain S7 delayed recovery without relabeling it')
        require(len(formals) < 2 and shorts, 'formal budget exhausted or no short evidence')
        require(all(stage(root, c).get('passed') is True for c in formals), 'first formal failure ended this group')
        require(isinstance(freeze, dict) and freeze.get('rate') == rate and freeze.get('sourceSha') == source and freeze.get('binarySHA256') == binary and freeze.get('environmentSHA256') == environment and freeze.get('fixtureSHA256') == fixture_hash, 'same immutable formal freeze required')
        require(freeze.get('sourceCIPassed') is True and freeze.get('relatedChecksPassed') is True and freeze.get('initialDistributionContract') == {'existing': 512, 'active': 0, 'relationships': 512}, 'formal checks/CI/initial distribution contract missing')
        require(isinstance(current_tools, dict) and current_tools and freeze.get('scriptHashes') == current_tools and freeze.get('observerBinarySHA256') == observer_hash, 'formal tool/observer bytes differ from freeze')
        candidates = [c for c in shorts if c['kind'] == 'candidate']
        require(len(candidates) == 1 and candidates[0]['rate'] == rate and stage(root, candidates[0]).get('candidateMarginPassed') is True and stage(root, candidates[0]).get('passed') is True, 'one passing candidate with 160/400 margins is required')
        for c in candidates + formals:
            require(c['apiSourceSha'] == source and c['binarySHA256'] == binary and c['environmentSHA256'] == environment and c['fixtureSHA256'] == fixture_hash, 'candidate/formal identity differs')
            require(c.get('scriptHashes') == current_tools and c.get('observerBinarySHA256') == observer_hash, 'candidate/formal tool bytes differ')
        for c in formals:
            require(c.get('freezeSHA256') == sha(root / 'freeze-like.json'), 'H2 cannot replace the H1 freeze')
        tag = 'H' + str(len(formals) + 1)
    else:
        require(not formals and len(shorts) < 8, 'short-window budget closed')
        tag = 'S' + str(len(shorts) + 1)
        if delayed:
            require(kind == 'candidate' and tag == 'S8', 'delayed S7 recovery permits only its reserved S8 candidate')
        if not effective_shorts:
            if optimization:
                require(kind == 'comparison' and tag == 'S6' and rate == 800, 'one S6 comparison at the unchanged S5 800/s rate is required')
            elif repair:
                require(kind == 'search' and tag == 'S5' and rate == 800, 'repaired tool starts one independent S5 800/s search')
            else:
                require(kind == 'diagnostic' and rate == 50, 'first effective window must be 50/s diagnostic')
                require(not recovery or tag == 'S2' and source == shorts[0]['apiSourceSha'] and binary == shorts[0]['binarySHA256'], 'startup recovery must use the same application in S2')
                require(prediction and prediction.get('observedEvidence') and prediction.get('differentPredictions'), 'diagnostic requires evidence and differentiating predictions before start')
        elif kind == 'comparison':
            require(len(effective_shorts) == 1 and effective_shorts[0]['kind'] == 'diagnostic' and rate == 50 and effective_shorts[0]['apiSourceSha'] != source and prediction and prediction.get('comparisonOf') == effective_shorts[0]['tag'] and prediction.get('singleVariableEvidence'), 'only the immediate one-variable 50/s comparison is allowed')
        else:
            require(kind in ('search', 'candidate'), 'no repeated diagnostic or second optimization comparison')
            final = read(root / 'final-application.json')
            require(final['sourceSha'] == source and final['binarySHA256'] == binary and final['environmentSHA256'] == environment and final['fixtureSHA256'] == fixture_hash, 'freeze final search application before continuing')
            if not optimization:
                require(any(c['kind'] == 'comparison' for c in effective_shorts) or effective_shorts[0]['apiSourceSha'] == source, 'changed version requires immediate comparison')
                comparisons = [c for c in effective_shorts if c['kind'] == 'comparison']
                require(source in {effective_shorts[0]['apiSourceSha'], *[c['apiSourceSha'] for c in comparisons]}, 'one optimization only; final source must be original or compared source')
                if comparisons and source == effective_shorts[0]['apiSourceSha']:
                    require(final.get('boundaryEvidenceAfter') == comparisons[0]['tag'] and final.get('rollbackEvidence'), 'rollback must reset old boundaries with explicit evidence')
            bound = search_state(root, source)
            if kind == 'candidate':
                require(bound['L'] is not None and rate == bound['candidateRate'], 'candidate must be L with margin or floor(.8L); never use S8 to find the first pass')
            else:
                require(len(shorts) < 7 and not bound['searchFinished'] and rate == bound['nextRate'], 'next search rate/remaining slot differs from declared algorithm')
                require(not any(c['rate'] == rate and c['tag'] in bound['boundaryTags'] for c in shorts), 'do not replay an already measured search rate')
    require(not (root / ('slot-' + tag + '.json')).exists() and not (root / (tag + '-command.json')).exists(), 'existing/orphan reservation is not reusable')
    return {'tag': tag, 'shortSlotsUsedAfter': len(shorts) + (kind != 'formal'), 'formalSlotsUsedAfter': len(formals) + (kind == 'formal'), 'mainSecondsReservedAfter': sum(c['seconds'] for c in records) + seconds,
            'shortMaximum': 8, 'formalMaximum': 2, 'mainSecondsMaximum': 840, 'S8ReservedForCandidate': True, 'elapsedConservativeSeconds': budget['effectiveSecondsUpperBound'], 'effectiveWorkBudget': budget, 'preLoadRecoverySHA256': recovery['sha256'] if recovery else None, 'toolRepairTransitionSHA256': repair['sha256'] if repair else None, 'claimOptimizationSHA256': optimization['sha256'] if optimization else None, 'delayedRecoverySHA256': delayed['sha256'] if delayed else None, 'failedStartsConsumeSlot': True, 'writesPerformed': False}

def _grade_tail15(rows, ended, vector, finished):
    start = ended - dt.timedelta(seconds=15)
    before = [r for r in rows if instant(r['at']) <= start]
    during = [r for r in rows if start <= instant(r['at']) <= ended]
    after = [r for r in rows if instant(r['at']) >= ended]
    inside = [r for r in rows if instant(r['sql']['startedAt']) >= start and instant(r['sql']['finishedAt']) <= ended]
    require(before and during and after and len(inside) >= 2, 'tail15 boundaries unavailable')
    require((start - instant(before[-1]['at'])).total_seconds() <= 7.5 and (instant(inside[0]['sql']['startedAt']) - start).total_seconds() <= 7.5 and (ended - instant(inside[-1]['sql']['finishedAt'])).total_seconds() <= 7.5 and (instant(after[0]['at']) - ended).total_seconds() <= 7.5, 'tail15 boundary gap exceeds 7.5s')
    events = inside[-1]['sql']['counts']['events'] - inside[0]['sql']['counts']['events']
    require(events > 0, 'tail15 lacks observed real event progress')
    a, b, c, z = (vector(r) for r in (before[-1], during[0], during[-1], after[0]))
    growth = {k: max(0, max(c[k], z[k]) - min(a[k], b[k])) for k in a}
    return {'passed': all(v <= .01 * events for v in growth.values()), 'eventsConservative': events, 'growthBudget': .01 * events, 'growthByQueue': growth,
            'firstInsideSQLStartedAt': inside[0]['sql']['startedAt'], 'lastInsideSQLFinishedAt': inside[-1]['sql']['finishedAt'], 'scope': 'tail15 gate in addition to the whole 60s gate; non-atomic conservative sample boundaries'}

def _number(value, label):
    require(type(value) in (int, float) and math.isfinite(value) and value >= 0, label + ' missing, negative or nonfinite')
    return value


def _finished(row):
    stamps = [instant(row['at']), instant(row['sql']['startedAt']), instant(row['sql']['finishedAt']),
              instant(row['kafka']['startedAt']), instant(row['kafka']['finishedAt'])]
    require(stamps == sorted(stamps), 'nonmonotone SQL/Kafka observation interval')
    return stamps[-1]


def _pipeline_grade(directory, report, seconds, chain):
    """Grade existing stage records only; missing evidence raises ValueError.

    An ordinary growth/recovery SLO miss returns passed=False. Feed has no LIKE
    events: its zero-event pass requires every sampled queue to stay zero.
    """
    require(chain == 'like' and seconds in (60, 180), 'CAP10 only measures 60/180s real likes')
    scenario = report['nativeScenario']
    require(type(scenario['complete']) is bool, 'native completion evidence missing')
    if not scenario['complete']:
        return {'passed': False, 'nativeScenarioComplete': False, 'nativeRecoveryBudgetVerified': False,
                'scope': 'failed/early-stop stage has no verified complete native offer window; raw scheduled timing is retained but no full-window pipeline claim is made'}
    require(all(type(scenario[key]) is int for key in ('startedAt', 'scheduledEndedAt', 'durationMs')) and
            scenario['durationMs'] == seconds * 1000 and scenario['scheduledEndedAt'] == scenario['startedAt'] + scenario['durationMs'], 'native scenario duration differs')
    require(scenario['startMetric']['min'] == scenario['startMetric']['max'] == scenario['startedAt'] and
            scenario['durationMetric']['min'] == scenario['durationMetric']['max'] == scenario['durationMs'], 'native scenario metrics differ')
    require(report['stableWrapperStartedAt'] <= scenario['startedAt'] <= scenario['scheduledEndedAt'] <= report['stableWrapperEndedAt'], 'native scenario outside process coverage')
    directory = Path(directory).resolve()
    require(directory.is_relative_to(ROOT), 'fixed CAP10 public evidence required')
    summary = read(directory / 'summary.json')
    rows = [json.loads(line) for line in (directory / 'timeline.ndjson').read_text(encoding='utf-8-sig').splitlines() if line]
    require(summary.get('collectionValid') is True and summary.get('semanticViolationCount') == 0 and rows, 'incomplete downstream collection')
    require(all(not row['errors'] and not row.get('semanticViolations') and row.get('sql') and row.get('kafka') for row in rows), 'missing/invalid downstream sample')
    starts = [instant(row['at']) for row in rows]
    require(starts == sorted(starts) and len(set(starts)) == len(starts), 'duplicate/nonmonotone sample timestamps')
    gaps = [(b - a).total_seconds() for a, b in zip(starts, starts[1:])]
    require(not gaps or max(gaps) <= 7.5, 'downstream interior observation gap exceeds 7.5s')
    for row in rows:
        _finished(row)
    started = dt.datetime.fromtimestamp(scenario['startedAt'] / 1000, dt.timezone.utc)
    ended = dt.datetime.fromtimestamp(scenario['scheduledEndedAt'] / 1000, dt.timezone.utc)
    require(ended >= started, 'native clock moved backwards')
    window_start = started if seconds == 60 else ended - dt.timedelta(seconds=60)
    before = [r for r in rows if instant(r['at']) <= window_start]
    during = [r for r in rows if window_start <= instant(r['at']) <= ended]
    after = [r for r in rows if instant(r['at']) >= ended]
    require(before and during and after, 'downstream boundaries unavailable')
    denominators = [r for r in rows if instant(r['sql']['startedAt']) >= window_start and instant(r['sql']['finishedAt']) <= ended]
    require(denominators, 'no SQL observation interval fully inside target window')
    first, inside_first, inside_last, post = before[-1], denominators[0], denominators[-1], after[0]
    queue_first, queue_last = during[0], during[-1]
    require((window_start - instant(first['at'])).total_seconds() <= 7.5 and
            (instant(inside_first['sql']['startedAt']) - window_start).total_seconds() <= 7.5 and
            (ended - instant(inside_last['sql']['finishedAt'])).total_seconds() <= 7.5 and
            (instant(post['at']) - ended).total_seconds() <= 7.5, 'downstream boundary gap too large')
    # The complete SQL intervals of both denominator endpoints lie inside the
    # target window. row.at alone is insufficient when a query crosses its end.
    events = _number(inside_last['sql']['counts']['events'], 'inside-last events') - _number(inside_first['sql']['counts']['events'], 'inside-first events')
    require(events >= 0, 'downstream event counter reset')

    def vector(row):
        c = row['sql']['counts']
        r = row['sql']['receipts']; k = row['kafka']['groups']
        totals = {name: _number(c[name], 'outbox ' + name) for name in ('events', 'pending', 'processing', 'done', 'needsReview', 'other')}
        require(totals['events'] == sum(totals[name] for name in ('pending', 'processing', 'done', 'needsReview', 'other')), 'outbox status counts do not sum to events')
        require({v['projection'] for v in r} == {'follow-v1', 'feed-v1', 'search-v1'} and len(r) == 3, 'incomplete receipt groups')
        require({v['group'] for v in k} == {'community.follow-v1', 'community.feed-v1', 'community.search-v1'} and len(k) == 3, 'incomplete broker groups')
        require(row['sql']['parked'] == 0 and c['needsReview'] == 0 and c['other'] == 0 and all(v['applied'] == 0 for v in r), 'new PARKED/review/invalid LIKE projection state')
        require(all(v['initialized'] is True for v in k), 'uninitialized Kafka group is not zero lag')
        require(all(sum(_number(v[name], 'receipt ' + name) for name in ('missing', 'applied', 'ignored')) == c['events'] for v in r), 'receipt counts do not cover the same event set')
        return {'outbox': _number(c['pending'], 'pending') + _number(c['processing'], 'processing'),
                **{'receipt-' + v['projection']: _number(v['missing'], 'receipt gap') for v in r},
                **{'lag-' + v['group']: _number(v['lag'], 'broker lag') for v in k}}

    vectors = [vector(row) for row in rows]
    event_counts = [row['sql']['counts']['events'] for row in rows]
    require(event_counts == sorted(event_counts), 'stage event counter reset')
    # Queue boundaries retain the last native-started sample even if its SQL
    # interval crosses nativeEnd; excluding that row could hide transient work.
    # Only the event denominator requires complete SQL intervals inside.
    a, inside_a, inside_b, z = (vector(row) for row in (first, queue_first, queue_last, post))
    growth = {key: max(0, max(inside_b[key], z[key]) - min(a[key], inside_a[key])) for key in a}

    def clean(row):
        c = row['sql']['counts']
        return (all(value == 0 for value in vector(row).values()) and
                all(r['ignored'] == c['events'] for r in row['sql']['receipts']))

    candidates = [(row, _finished(row)) for row in rows if instant(row['at']) >= ended and clean(row)]
    recovered = min(candidates, key=lambda item: item[1]) if candidates else None
    recovery = (recovered[1] - ended).total_seconds() if recovered else None
    require(events > 0 or chain == 'feed', 'like stage lacks observed real event progress')
    zero_event_clean = events != 0 or all(all(value == 0 for value in values.values()) for values in vectors)
    passed = (zero_event_clean and all(value <= events * .01 for value in growth.values()) and recovery is not None and 0 <= recovery <= 60)
    short_slopes = None
    if seconds == 60:
        recent = [(row, vector(row)) for row in rows if ended - dt.timedelta(seconds=15) <= instant(row['at']) <= ended]
        offsets = [(instant(row['at']) - ended).total_seconds() for row, _ in recent]
        mean = sum(offsets) / len(offsets) if offsets else 0
        variance = sum((x - mean) ** 2 for x in offsets)
        short_slopes = {'sampleCount': len(recent), 'firstSample': recent[0][0]['at'] if recent else None,
                        'lastSample': recent[-1][0]['at'] if recent else None,
                        'perSecond': {key: sum((x - mean) * values[key] for x, (_, values) in zip(offsets, recent)) / variance
                                      if variance else None for key in growth},
                        'scope': 'descriptive least-squares queue slope at sample starts in the final native 15s; underlying SQL/Kafka intervals are non-atomic. No additional pass/fail threshold.'}
    tail15 = None
    if seconds == 60:
        tail15 = _grade_tail15(rows, ended, vector, _finished)
        passed = passed and tail15['passed']
    return {'passed': passed, 'chain': chain, 'tail15': tail15, 'newEventsInConservativeSampledWindow': events,
            'maximumGrowthBudget': events * .01, 'growthByQueue': growth,
            'shortStageLast15SecondsQueueSlope': short_slopes,
            'windowStartTarget': window_start.isoformat(), 'firstBoundarySample': first['at'],
            'firstInsideSample': inside_first['at'], 'lastInsideSample': inside_last['at'],
            'firstInsideSQLStartedAt': inside_first['sql']['startedAt'], 'lastInsideSQLFinishedAt': inside_last['sql']['finishedAt'],
            'firstQueueInsideSample': queue_first['at'], 'lastQueueInsideSample': queue_last['at'],
            'firstPostEndSample': post['at'], 'nativeEndedAt': ended.isoformat(),
            'recoveryObservedCompletedAt': recovered[1].isoformat() if recovered else None,
            'recoverySecondsFromNativeEnd': recovery, 'maximumSampleStartGapSeconds': max(gaps, default=0),
            'nativeRecoveryBudgetVerified': True,
            'nativeScenarioComplete': True,
            'samplingScope': '5s non-atomic observations with every start gap/boundary <=7.5s. Event denominator is fully inside the target window; boundary queue min/max is conservative. Recovery uses latest SQL/Kafka completion, not sample start or control done. Not instantaneous backlog proof.'}


def pipeline_grade(directory, report, seconds, chain):
    try:
        return _pipeline_grade(directory, report, seconds, chain)
    except (KeyError, TypeError, IndexError, OSError, json.JSONDecodeError) as error:
        raise ValueError('missing/malformed downstream evidence: ' + type(error).__name__) from None
