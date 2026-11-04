"""Offline audit of this closed CAP08 cohort; no HTTP, SQL, Docker or load.

The second formal result intentionally lacks result.result after the native
access-consistency rejection. This audit reports native counts separately and
never repairs the original report or promotes the failed formal group.
"""
import argparse
import datetime as dt
import hashlib
import importlib.util
import json
import math
from pathlib import Path
import re
import subprocess


REPO = Path(__file__).resolve().parent.parent
ROOT = REPO / 'docs/evidence/2026-10-30-cap08'
SOURCE = '46ddefc2a16d41e4bb01a3d20d08bdfcaeff465f'
TOOLS = '516d5342af95d51adf8f6ec9a8c3af321fefb4fa'
SCRIPT_NAMES = {'cap08-stage.py', 'cap08-policy.py', 'cap08-collector.py', 'cap08-env.py', 'cap06-env.py',
                'cap07-stage.py', 'capacity-stage.py', 'capacity-resources.py', 'go073-cap-read.mjs', 'load/go073-cap-read.js'}
EXPECTED = [('diagnostic-1', 'diagnostic', 'diagnostic-1'), ('formal-1-multi100', 'formal', 'formal-1'),
            ('formal-2-multi100', 'formal', 'formal-2')]


def module(name, filename):
    spec = importlib.util.spec_from_file_location(name, REPO / 'scripts' / filename)
    value = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    return value


prior = module('cap08_audit_native', 'cap07-audit.py')
observer = module('cap08_audit_observer', 'cap08-collector.py')


def read(path):
    return json.loads(Path(path).read_text(encoding='utf-8-sig'))


def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def require(condition, message):
    if not condition:
        raise ValueError(message)


def instant(value):
    return dt.datetime.fromisoformat(value.replace('Z', '+00:00'))


blob_hashes = {}


def git_sha(revision, path):
    key = (revision, path)
    if key not in blob_hashes:
        raw = subprocess.check_output(['git', 'show', revision + ':' + path], cwd=REPO)
        blob_hashes[key] = hashlib.sha256(raw).hexdigest()
    return blob_hashes[key]


def tool_hashes(command):
    require(command['toolSourceSha'] == TOOLS and set(command['scriptHashes']) == SCRIPT_NAMES, 'runtime tool source/set differs')
    results = []
    for name, digest in command['scriptHashes'].items():
        path = (REPO / 'scripts' / name).resolve()
        require(path.is_relative_to(REPO / 'scripts'), 'tool path outside scripts')
        current = sha(path)
        historical = git_sha(TOOLS, 'scripts/' + name)
        require(digest == historical, 'recorded runtime tool differs from its declared historical Git blob: ' + name)
        results.append({'name': name, 'recordedSHA256': digest, 'historicalGitBlobMatches': historical == digest,
                        'currentWorkingTreeMatches': current == digest,
                        'matchScope': 'recorded runtime Git blob; current bytes never substitute for historical provenance'})
    return results


def access_score(load):
    before, after, recorded = (read(load / name) for name in ('before.json', 'after.json', 'access-delta.json'))
    commands = {key: after['globalStatus']['counters'][key] - value for key, value in before['globalStatus']['counters'].items()}
    require(commands == recorded['globalCommands'], 'instance counter arithmetic differs')
    elapsed = (after['globalStatus']['startedAt'] + after['globalStatus']['endedAt'] -
               before['globalStatus']['startedAt'] - before['globalStatus']['endedAt']) / 2
    uncertainty = (2000 + after['globalStatus']['endedAt'] - after['globalStatus']['startedAt'] +
                   before['globalStatus']['endedAt'] - before['globalStatus']['startedAt'])
    uptime = elapsed >= 0 and commands['Uptime'] >= 0 and abs(commands['Uptime'] * 1000 - elapsed) <= uncertainty
    require(elapsed == recorded['statusElapsedMs'] and uncertainty == recorded['statusUncertaintyMs'] and
            uptime is recorded['uptimeConsistent'], 'instance uptime-window arithmetic differs')
    event_before = {row['event']: row for row in before['statementEvents']}
    event_rows = [{**{key: row[key] - event_before[row['event']][key] for key in ('count', 'errors', 'examined', 'sent')},
                   'event': row['event'], 'timerPicoseconds': str(int(row['picoseconds']) - int(event_before[row['event']]['picoseconds']))}
                  for row in after['statementEvents']]
    require(event_rows == recorded['statementEvents'], 'statement event arithmetic differs')
    old = {row['digest']: row for row in before['digests']}
    sql_rows = []
    for row in after['digests']:
        previous = old.get(row['digest'], {})
        sql_rows.append({**row, **{key: row[key] - previous.get(key, 0) for key in ('count', 'examined', 'sent')},
                         'timerPicoseconds': str(int(row['picoseconds']) - int(previous.get('picoseconds', '0')))})
    require([row for row in sql_rows if row['count'] > 0] == recorded['sql'], 'digest arithmetic differs')
    redis = {name: {'calls': row['calls'] - before['redis'].get(name, {}).get('calls', 0),
                    'usec': row['usec'] - before['redis'].get(name, {}).get('usec', 0),
                    'failed': row.get('failed_calls', 0) - before['redis'].get(name, {}).get('failed_calls', 0)}
             for name, row in after['redis'].items()}
    require({key: row for key, row in redis.items() if row['calls'] > 0} == recorded['redis'], 'Redis command arithmetic differs')
    negative = (any(value < 0 for value in commands.values()) or
                any(any(row[key] < 0 for key in ('count', 'errors', 'examined', 'sent')) or int(row['timerPicoseconds']) < 0 for row in event_rows) or
                any(any(row[key] < 0 for key in ('count', 'examined', 'sent')) or int(row['timerPicoseconds']) < 0 for row in sql_rows) or
                any(any(value < 0 for value in row.values()) for row in redis.values()))
    resets = not uptime or negative
    missing = [row['digest'] for row in before['digests'] if row['digest'] not in {value['digest'] for value in after['digests']}]
    lost = after['digestLost'] - before['digestLost']
    prepared = commands['Performance_schema_prepared_statements_lost']
    require(resets is recorded['counterResets'] and missing == recorded['missingAfterDigests'] and
            lost == recorded['lostDigestsDelta'] and prepared == recorded['preparedStatementsLostDelta'], 'access validity classification differs')
    valid = not resets and not missing and lost == 0 and prepared == 0
    return {'validAccessCountClaim': valid, 'uptimeConsistent': uptime, 'statusElapsedMs': elapsed,
            'statusUncertaintyMs': uncertainty, 'originalCounterResetsFlag': resets, 'negativeCounterObserved': negative,
            'counterFlagScope': 'original flag includes uptime-window inconsistency; true alone does not prove a server restart or actual counter reset',
            'lostDigestsDelta': lost, 'preparedStatementsLostDelta': prepared,
            'missingAfterDigests': missing,
            'recordedOverlappingComSelectAndExecute': {key: commands[key] for key in ('Com_select', 'Com_stmt_execute')},
            'scope': 'whole instance between sequential access snapshots; overlapping counters are not added; invalid windows are not performance access-count results'}


def resources_score(control_path, api_pid):
    control = read(control_path / 'exit-status.json')
    rows = [json.loads(line) for line in (control_path / 'resources.ndjson').read_text(encoding='utf-8-sig').splitlines()]
    samples = [row for row in rows if row.get('kind') == 'sample']
    complete = [row for row in samples if not row.get('errors') and row.get('generators')]
    require(len(samples) == control['resource_samples'] and len(complete) == control['complete_resource_samples'], 'resource sample counts differ')
    require(samples and all(row['diagnostics']['pid'] == api_pid for row in complete), 'resource/API PID differs')
    require(control.get('sampler_exit') == 0 and not control.get('errors') and not control.get('forced') and control.get('stop_signal') is False,
            'resource supervision incomplete or stopped')
    return {'samples': len(samples), 'completeSamples': len(complete), 'samplerExit': control['sampler_exit'],
            'stopSignal': control['stop_signal'], 'harnessExit': control['harness_exit'],
            'hostCPUObservedMax': max(row['host']['cpu_percent'] for row in complete if row.get('cpu_interval_valid')),
            'hostMemoryObservedMax': max(row['host']['memory_percent'] for row in complete),
            'apiRSSObservedMaxBytes': max(row['api']['rss_bytes'] for row in complete),
            'limits': 'five-second observations, not a guarantee of instantaneous peaks or pure per-request waits'}


def diagnostic_score(command):
    root = ROOT / (command['tag'] + '-diagnostic')
    manifest, slow = read(root / 'manifest.json'), read(root / 'slow-records.json')
    rows = [json.loads(line) for line in (root / 'timeline.ndjson').read_text(encoding='utf-8-sig').splitlines()]
    require(len(rows) == manifest['samples'] and manifest['script_sha256'] == command['scriptHashes']['cap08-collector.py'], 'diagnostic sample/script differs')
    require(manifest['api_source_sha'] == command['apiSourceSha'] and manifest['api_metadata_sha256'] == command['apiMetadataSHA256'], 'diagnostic identity differs')
    previous = None
    incomplete = False
    for row in rows:
        require(not row['errors'] and row['diagnostics']['pid'] == command['apiPid'], 'diagnostic sample unavailable or PID differs')
        require(row['observation']['available'] is True and row['observation']['truncated'] is False and
                re.fullmatch(r'[a-f0-9]{64}', row['observation']['sha256']), 'raw response hash/availability absent')
        reasons, missing = observer.triggers(previous, row['diagnostics'])
        require(reasons == row['trigger_reasons'] and missing == row['oldest_incomplete_stages'], 'diagnostic trigger arithmetic differs')
        require('slow_traces' not in row['diagnostics']['tail'] and row['diagnostics']['tail'].get('slow_traces_omitted') is True, 'timeline unexpectedly repeats slow payloads')
        incomplete |= bool(missing)
        previous = row['diagnostics']
    gap = max([0] + [(right['elapsed_seconds'] - left['elapsed_seconds']) * 1000 for left, right in zip(rows, rows[1:])])
    require(math.isclose(gap, manifest['maximum_sample_start_gap_ms'], abs_tol=1e-6), 'diagnostic maximum gap differs')
    done = read(ROOT / (command['tag'] + '-control/done.json'))
    observed = observer.coverage([row['at'] for row in rows], manifest['stable_created_unix_ms'], done['at'], gap)
    require(observed == manifest['window_coverage'] and observed['complete'], 'diagnostic stable/done coverage differs or incomplete')
    require(manifest['successful_polls'] == len(rows) and manifest['failed_polls'] == 0 and
            manifest['poll_coverage_complete'] is True and manifest['stage_fields_valid'] is True and
            manifest['active_age_coverage_complete'] is (not incomplete) and not incomplete and
            manifest['slow_records_visibility_complete'] is True and not manifest['errors'], 'diagnostic validity differs')
    require(len(manifest['captures']) <= 3, 'too many diagnostic captures')
    previous_end = None
    for capture in manifest['captures']:
        require(capture.get('available') is True and capture.get('truncated') is False, 'diagnostic capture missing/truncated')
        path = root / capture['file']
        require(path.resolve().parent == root and path.stat().st_size <= 1 << 20 and sha(path) == capture['sha256'], 'bounded capture bytes differ')
        if previous_end:
            require((instant(capture['started_at']) - previous_end).total_seconds() >= 15, 'capture completion spacing under fifteen seconds')
        previous_end = instant(capture['finished_at'])
    require(len(slow['retained']) <= 32 and len({row['sequence'] for row in slow['retained']}) == len(slow['retained']), 'slow archive exceeds distinct bound')
    require(slow['retained'] == sorted(slow['retained'], key=lambda value: (value['total_ns'], value['sequence']), reverse=True), 'slow archive selection ordering differs')
    visible = {sequence for row in rows for sequence in row['diagnostics']['tail']['visible_slow_sequences']}
    require(set(slow['retained_sequences']).issubset(visible) and slow['retained_sequences'] == [row['sequence'] for row in slow['retained']], 'slow archive references differ')
    return {'validDiagnostic': True, 'samples': len(rows), 'captures': len(manifest['captures']),
            'windowCoverage': observed, 'retainedSlowRecords': len(slow['retained']),
            'scope': 'parsed subset hashes recorded from original HTTP bytes; full raw responses and discarded slow payloads cannot be reconstructed'}


def stage_score(command, fixture_hash):
    out = {key: command[key] for key in ('tag', 'kind', 'slot', 'apiSourceSha', 'toolSourceSha', 'mode', 'rate', 'seconds')}
    out.update(errors=[], capacityPassed=False)
    try:
        require(command['apiSourceSha'] == SOURCE and (command['mode'], command['rate'], command['seconds']) == ('multi', 100, 180), 'fixed source/workload differs')
        require((command['warmSeconds'], command['warmRate'], command['preVUs'], command['maxVUs']) == (10, 25, 256, 512), 'warmup/VUs differ')
        slot = read(ROOT / ('slot-' + command['slot'] + '.json'))
        require(slot['stage'] == command['tag'] and slot['failedStartsConsumeSlot'] is True, 'immutable slot differs')
        api_path = Path(command['apiMetadataPath']).resolve()
        require(api_path.parent == ROOT and sha(api_path) == command['apiMetadataSHA256'], 'API metadata path/hash differs')
        api = read(api_path)
        require(api['pid'] == command['apiPid'] and api['sourceSha'] == SOURCE and api['expectedVersion'] == 'capacity-' + SOURCE and
                api['phase'] == ('diagnostic' if command['kind'] == 'diagnostic' else 'formal') and
                api['tailEnabledRequested'] is (command['kind'] == 'diagnostic'), 'API phase/source/identity differs')
        require(sha(Path(api['exe'])) == api['sha256'], 'retained executable hash differs or file absent')
        build = (ROOT / ('api-' + api['label'] + '-build.txt')).read_text(encoding='utf-8-sig')
        require('vcs.revision=' + SOURCE in build and 'vcs.modified=false' in build, 'clean build metadata absent')
        out['toolHashes'] = tool_hashes(command)
        report = read(ROOT / (command['tag'] + '-load/result.json'))
        require(report['apiSourceSha'] == report['fixtureSourceSha'] == SOURCE and report['fixtureManifestSHA256'] == fixture_hash and
                report['apiExpectedRevision'] == report['fixtureExpectedRevision'] == 'capacity-' + SOURCE, 'native source/fixture differs')
        require(all(report[key] == command[key] for key in ('mode', 'rate', 'seconds', 'warmSeconds', 'warmRate', 'preVUs', 'maxVUs')) and
                (report['pageSize'], report['activeReaders'], report['activeCountKeys']) == (20, 32, 640), 'native workload differs')
        require(report['scriptSHA256'] == {'scripts/' + name: command['scriptHashes'][name] for name in ('go073-cap-read.mjs', 'load/go073-cap-read.js')}, 'native classifier hash differs')
        summary = read(ROOT / (command['tag'] + '-load/stable-summary.json'))
        native = prior.native_score(summary, {**report, 'failure': None}, command)
        out['nativeSummaryIndependent'] = native
        out['nativeSummaryScope'] = 'original summary counts/latencies plus recorded warm/stable exits; separate access rejection is not used to change these native metrics'
        classified = prior.native_score(summary, report, command)
        require(report.get('meetsBudget') is classified['meetsBudget'], 'native plus original report failure classification differs')
        original = report.get('result')
        out['originalResultPresent'] = isinstance(original, dict)
        out['originalFailure'] = report.get('failure')
        if isinstance(original, dict):
            for key, saved in (('attempted', 'attempted'), ('http', 'completedHttpRequests'), ('completed', 'completed'),
                               ('successful', 'successful'), ('unknown', 'incompleteOrUnclassified'), ('dropped', 'dropped'),
                               ('contentErrors', 'contentErrors'), ('systemErrors', 'systemErrors'), ('httpErrors', 'httpErrors')):
                require(native[key] == original[saved], 'native/result count differs: ' + key)
            require(native['allP95Ms'] == original['allLatency']['p(95)'] and native['allP99Ms'] == original['allLatency']['p(99)'], 'native/result percentiles differ')
            if native['successful']:
                require(native['successfulP95Ms'] == original['successfulLatency']['p(95)'] and
                        native['successfulP99Ms'] == original['successfulLatency']['p(99)'], 'native/result success percentiles differ')
        else:
            require(command['tag'] == 'formal-2-multi100' and report.get('meetsBudget') is False and report.get('failure'), 'unexpected missing original classified result')
            out['missingResultScope'] = 'original result.result is absent and stays absent; native summary is a separate independent failure observation, not a patched report'
        out['contentLoaderDeltaIncludingWarmup'] = prior.loader_score(ROOT, {**command, 'variant': 'B'})
        out['instanceAccess'] = access_score(ROOT / (command['tag'] + '-load'))
        require(out['instanceAccess']['validAccessCountClaim'] is (not bool(report.get('failure'))), 'native access rejection differs from reconstructed window')
        stage = read(ROOT / (command['tag'] + '-stage-exit.json'))
        require(stage['wrapperExited'] is True and stage['failedStartOrException'] is None and stage['toolBytesStable'] is True and
                stage['cardsEvidenceValid'] is True, 'actual wrapper/tool/exception terminal differs')
        out['actualTerminal'] = {key: stage[key] for key in ('wrapperExited', 'failedStartOrException', 'toolBytesStable', 'capacityWrapperExit', 'extraDiagnosticCollectorExit')}
        out['resources'] = resources_score(ROOT / (command['tag'] + '-control'), command['apiPid'])
        out['capacityPassed'] = native['meetsBudget'] and out['instanceAccess']['validAccessCountClaim'] and not report.get('failure') and stage['capacityWrapperExit'] == 0 and out['resources']['harnessExit'] == 0
        if command['kind'] == 'diagnostic':
            out['diagnostic'] = diagnostic_score(command)
            require(stage['extraDiagnosticCollectorExit'] == 0, 'diagnostic collector terminal failed')
        else:
            require(stage['extraDiagnosticCollectorExit'] is None and not (ROOT / (command['tag'] + '-diagnostic')).exists(), 'formal group unexpectedly used extra collector')
        for suffix in ('-capacity-before.json', '-capacity-after.json'):
            endpoint = read(ROOT / (command['tag'] + suffix))
            require(endpoint['capacity']['tail']['enabled'] is (command['kind'] == 'diagnostic'), 'endpoint heavy switch differs')
            if command['kind'] == 'diagnostic':
                require(endpoint['rawResponseArchived'] is False and 'slow_traces' not in endpoint['capacity']['tail'] and
                        endpoint['fetch']['available'] is True and endpoint['fetch']['truncated'] is False and
                        re.fullmatch(r'[a-f0-9]{64}', endpoint['fetch']['sha256']), 'diagnostic endpoint subset/hash declaration differs')
    except (OSError, ValueError, KeyError, TypeError, AssertionError, subprocess.CalledProcessError) as error:
        out['errors'].append(str(error) if isinstance(error, ValueError) else 'missing/malformed public evidence: ' + type(error).__name__)
    return out


def audit():
    fixture = ROOT / 'fixture/fixture.json'
    manifest = read(fixture)
    errors = []
    def check(condition, message):
        if not condition:
            errors.append(message)
    check((manifest['status'], manifest['sourceSha'], manifest['authors'], manifest['readers'], manifest['notesPerAuthor'], manifest['noteCount'], manifest['pageSize']) ==
          ('READY', SOURCE, 32, 32, 200, 6400, 20), 'fresh fixture shape/source differs')
    commands = sorted((read(path) for path in ROOT.glob('*-command.json')), key=lambda value: value['at'])
    check([(row['tag'], row['kind'], row['slot']) for row in commands] == EXPECTED, 'actual closed cohort stage order/count differs')
    stages = [stage_score(command, sha(fixture)) for command in commands]
    build = read(ROOT / 'build-46ddefc.json')
    check(build['sourceSha'] == SOURCE and build['buildExitCode'] == 0 and build['worktreeCleanBefore'] is True and
          build['worktreeCleanAfter'] is True and build['vcsRevisionMatches'] is True and build['vcsModifiedFalse'] is True,
          'frozen clean binary build differs')
    check(all(read(Path(command['apiMetadataPath']))['sha256'] == build['sha256'] and
              Path(read(Path(command['apiMetadataPath']))['exe']).resolve() == Path(build['exe']).resolve() for command in commands),
          'application binary changed between diagnostic/formal stages')
    environment = read(ROOT / 'environment.json')
    check(environment['task'] == 'CAP-08' and environment['project'] == 'project-full-stack-cap-read' and environment['oldVolumesRetained'] is True, 'cohort environment differs')
    for service, cpu, memory, port in (('mysql', 4_000_000_000, 2 << 30, '3451'), ('redis', 2_000_000_000, 512 << 20, '6551')):
        own = environment['containers'][service]
        check(own['id'] != environment['oldContainers'][service]['id'] and re.fullmatch(r'[a-f0-9]{64}', own['id']) is not None, 'new immutable container identity differs: ' + service)
        check(own['cpuNano'] == cpu and own['memoryBytes'] == memory and
              re.fullmatch(r'project-full-stack-cap-read-cap08-[a-f0-9]{12}-' + service, environment['newVolumes'][service]) is not None,
              'fixed container limits/new volume differs: ' + service)
        check(any(binding['HostIp'] == '127.0.0.1' and binding['HostPort'] == port for bindings in own['ports'].values() for binding in (bindings or [])), 'loopback binding differs: ' + service)
    slots = {path.name for path in ROOT.glob('slot-*.json')}
    check(slots == {'slot-controlled-1.json', 'slot-diagnostic-1.json', 'slot-formal-1.json', 'slot-formal-2.json'}, 'reservation budget/orphan differs')
    wiring = read(ROOT / 'wiring-result.json')
    check(wiring['testExit'] == 0 and wiring['capturedBeforeRelease'] is True and wiring['stageSeparationValidated'] is True and
          wiring['allActiveConverged'] is True and wiring['profileBytes'] <= 1 << 20, 'controlled wiring did not meet declared gate')
    for path, digest in wiring['sourceFileSHA256'].items():
        revision = TOOLS if path.startswith('scripts/') else SOURCE
        check(git_sha(revision, path) == digest, 'controlled worktree bytes differ from subsequent immutable source: ' + path)
    gate = read(ROOT / 'formal-gate.json')
    check(gate['sourceSha'] == SOURCE and gate['diagnosticOutcome'] == 'not-reproduced' and gate['unresolvedReproducedFailure'] is False and
          gate['historicalB1CauseStillUnknown'] is True and gate['businessFixApplied'] is False, 'formal operator conclusion differs')
    ci = read(ROOT / 'ci-46ddefc.json')
    check(ci['headSha'] == SOURCE and ci['conclusion'] == 'success' and
          {job['name'] for job in ci['jobs']} == {'backend-go', 'backend', 'frontend', 'community-kafka'} and
          all(job['conclusion'] == 'success' for job in ci['jobs']), 'exact source CI not passed')
    budget = read(ROOT / 'budget-start.json')
    check(budget['maximumDevelopmentSeconds'] == 14400 and budget['sourceSha'] == '1de173517a54c3be46748779437b592f7bcdbc58', 'original development budget/source differs')
    start = instant(budget['startedAt'])
    check(0 <= (instant(gate['workCompletedAt']) - start).total_seconds() <= 14400, 'formal work gate exceeded development upper bound')
    for command in commands:
        check(0 <= (instant(command['at']) - start).total_seconds() <= 14400, 'stage reservation outside conservative development clock')
        reference, digest = (command['predictionPath'], command['predictionSHA256']) if command['kind'] == 'diagnostic' else (command['gatePath'], command['gateSHA256'])
        check(Path(reference).resolve().is_relative_to(ROOT) and sha(Path(reference)) == digest, 'prediction/gate hash differs')
    if len(stages) == 3:
        check(stages[0].get('capacityPassed') is True and stages[0].get('diagnostic', {}).get('validDiagnostic') is True, 'formal not-reproduction gate lacks valid preceding diagnostic')
        check(stages[1].get('capacityPassed') is True and stages[2].get('capacityPassed') is False, 'formal cohort terminal differs from one-pass/one-failure')
    closure = read(ROOT / 'closure.json')
    check(closure['batchStopped'] is True and closure['capacityProtocolPassed'] is False and closure['noFurtherLoad'] is True and
          closure['applicationSourceSha'] == SOURCE and closure['measuredToolSourceSha'] == TOOLS and
          closure['used'] == {'controlled': 1, 'diagnostic': 1, 'businessRepair': 0, 'formal': 2, 'hot': 0, 'stair': 0, 'neighbor': 0, 'hold': 0},
          'declared closed budget/terminal differs')
    protected = ['backend', 'scripts/cap08-stage.py', 'scripts/cap08-collector.py', 'scripts/capacity-stage.py',
                 'scripts/capacity-resources.py', 'scripts/go073-cap-read.mjs', 'scripts/load/go073-cap-read.js']
    unchanged = subprocess.run(['git', 'diff', '--quiet', TOOLS, closure['postGroupGuardSha'], '--', *protected], cwd=REPO).returncode == 0
    check(unchanged, 'post-group guard unexpectedly changed measured application or measurement chain')
    errors.extend(error for stage in stages for error in stage['errors'])
    return {'task': 'CAP-08', 'applicationSourceSha': SOURCE, 'measurementToolSourceSha': TOOLS,
            'binarySHA256': build['sha256'], 'goVersion': build['goVersion'],
            'postGroupGuardSha': closure['postGroupGuardSha'], 'measurementInputsUnchangedByPostGroupGuard': unchanged,
            'auditScriptSHA256': sha(Path(__file__)), 'fixtureManifestSHA256': sha(fixture), 'stages': stages,
            'auditDependencySHA256': {name: sha(REPO / 'scripts' / name) for name in ('cap07-audit.py', 'cap08-collector.py')},
            'errors': errors, 'capacityProtocolPassed': False,
            'protocol': {'controlledReserved': 1, 'diagnosticReserved': 1, 'businessRepairReserved': 0,
                         'formalReserved': 2, 'hotExecuted': False, 'optionalAscentExecuted': False,
                         'firstFormalFailureEndedGroup': True},
            'postMeasurementGuardScope': 'current policy late-interruption guard may differ; runtime script hashes are checked against retained historical Git blobs, not ignored or rewritten',
            'sourceOfPercentiles': 'native per-stage stable-summary.json; no per-request distributions or percentile merging',
            'unfinishedLatencyScope': 'native latency summaries contain recorded completed observations; unknown requests have no reconstructed outcome or latency and are never assigned zero',
            'loaderScope': 'warmup through post-wrapper reconciliation, independent of instance-access snapshot window; invalid formal2 access counts stay invalid'}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    group = parser.add_mutually_exclusive_group(required=True)
    group.add_argument('--check', action='store_true')
    group.add_argument('--output', type=Path)
    args = parser.parse_args()
    require(Path.cwd().resolve() == REPO, 'run from repository root')
    if args.output:
        require(args.output.resolve().parent == ROOT and re.fullmatch(r'audit-[a-z0-9-]+\.json', args.output.name), 'exclusive audit path inside fixed CAP08 evidence required')
    result = audit()
    if args.output:
        with args.output.open('x', encoding='utf-8') as stream:
            json.dump(result, stream, ensure_ascii=False, indent=2)
    print(json.dumps({'stages': len(result['stages']), 'auditErrors': result['errors'],
                      'capacityProtocolPassed': result['capacityProtocolPassed']}, ensure_ascii=False))
    return 0 if not result['errors'] else 1


if __name__ == '__main__':
    try:
        raise SystemExit(main())
    except Exception as error:
        print('cap08-audit: ' + type(error).__name__ + '; public artifact unavailable/inconsistent; no live sources used')
        raise SystemExit(1)
