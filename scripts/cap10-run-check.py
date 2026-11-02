"""Offline checks of the actual runner's classification and warm guard functions."""
import ast
import datetime as dt
import hashlib
import json
from pathlib import Path
from types import SimpleNamespace
import uuid

REPO = Path(__file__).resolve().parent.parent
source = ast.parse((REPO / 'scripts/cap10-run.py').read_text(encoding='utf-8'))
names = {'classify', 'pre_stable_guard', 'container_snapshot', 'observer_terminal_outcome'}
isolated = ast.Module(body=[node for node in source.body if isinstance(node, ast.FunctionDef) and node.name in names], type_ignores=[])
checks = 0


def check(condition, label):
    global checks
    assert condition, label
    checks += 1
    print('PASS ' + label)


def authority_wiring(temporary, files):
    """Execute only the actual reservation/diagnostic AST with local doubles."""
    functions = {node.name: node for node in source.body if isinstance(node, ast.FunctionDef)}
    scope = {'Path': Path, 'hashlib': hashlib}
    selected = ast.Module(body=[functions[name] for name in ('sha', 'expected_diagnostics', 'reserve')], type_ignores=[])
    exec(compile(selected, 'cap10-run.py(actual reservation functions)', 'exec'), scope)
    body = functions['main'].body
    first = next(i for i, node in enumerate(body) if isinstance(node, ast.Assign)
                 and any(isinstance(target, ast.Name) and target.id == 'execution' for target in node.targets))
    diagnostic_ast = ast.Module(body=body[first:first + 2], type_ignores=[])
    def diagnostics(context, kind, enabled, optimization=None):
        calls = []
        grade = SimpleNamespace(execution_context=lambda _: calls.append('authority') or context,
            history=lambda _: calls.append('history') or [{'tag': 'S' + str(i)} for i in range(1, 6)],
            claim_optimization=lambda *_: calls.append('claim') or optimization)
        local = {**scope, 'grade': grade, 'ROOT': temporary, 'args': SimpleNamespace(kind=kind),
                 'api': {'likeDiagnosticsRequested': enabled}}
        exec(compile(diagnostic_ast, 'cap10-run.py(actual main diagnostic branch)', 'exec'), local)
        return calls
    for kind, enabled in (('comparison', False), ('diagnostic', True), ('search', False)):
        check(diagnostics({'authority': True}, kind, enabled) == ['authority'],
              'authority ' + kind + ' bypasses superseded Claim proof before load')
    try:
        diagnostics({'authority': True}, 'comparison', True)
    except AssertionError:
        rejected = True
    else:
        rejected = False
    check(rejected, 'authority comparison cannot silently enable diagnostics')
    check(diagnostics(None, 'comparison', False, {'sha256': 'c' * 64}) == ['authority', 'history', 'claim'],
          'legacy S6 Claim comparison keeps its diagnostics exception')
    check(diagnostics(None, 'comparison', True) == ['authority', 'history', 'claim'],
          'ordinary legacy comparison keeps diagnostics enabled')
    trace_node = next(node for node in ast.walk(functions['main']) if isinstance(node, ast.If)
                      and 'executionAuthoritySHA256' in ast.unparse(node.test)
                      and 'expected_trace' in ast.unparse(node))
    trace_ast = ast.Module(body=[trace_node], type_ignores=[])
    for authority, expected, kind, tag, claim in [(True, False, 'comparison', 'S10', None),
                                               (True, True, 'diagnostic', 'S11', None),
                                               (False, False, 'comparison', 'S6', 'c' * 64),
                                               (False, True, 'comparison', 'S3', None)]:
        reservation = {'claimOptimizationSHA256': claim}
        if authority:
            reservation.update(executionAuthoritySHA256='a' * 64, expectedDiagnostics=expected)
        local = {**scope, 'command': {'reservationCheck': reservation}, 'args': SimpleNamespace(kind=kind), 'tag': tag}
        exec(compile(trace_ast, 'cap10-run.py(actual trace branch)', 'exec'), local)
        check(local['expected_trace'] is expected, 'actual sample trace switch matches ' + tag + ' admission')
    local = {**scope, 'command': {'reservationCheck': {'executionAuthoritySHA256': 'a' * 64,
             'expectedDiagnostics': 'false'}}, 'args': SimpleNamespace(kind='search'), 'tag': 'S10'}
    try:
        exec(compile(trace_ast, 'cap10-run.py(actual trace branch)', 'exec'), local)
    except AssertionError:
        rejected = True
    else:
        rejected = False
    check(rejected, 'nonboolean authority trace expectation cannot be coerced')
    for name in ('preflight.json', 'ability.json', 'fixture.json', 'environment.json', 'observer'):
        path = temporary / name; path.write_text('{}'); files.append(path)
    saved = {}
    reservation = {'tag': 'S10', 'expectedDiagnostics': False,
        'executionAuthoritySHA256': 'a' * 64, 'executionAuthorityGeneration': 1,
        'deploymentId': 'pool8', 'activeApplicationSHA256': 'b' * 64, 'admissionSHA256': 'd' * 64}
    grade = SimpleNamespace(check_reservation=lambda *a, **kw: reservation)
    owned = SimpleNamespace(read=lambda p: json.loads(Path(p).read_bytes()), now=lambda: '2026-11-02T12:00:00Z',
        run=lambda *a: SimpleNamespace(stdout='e' * 40), save=lambda p, value: saved.update({p.name: value}))
    scope.update(ROOT=temporary, REPO=temporary, TOOL_NAMES=(), PTS_TOOLS=(), owned=owned, module=lambda *_: grade)
    args = SimpleNamespace(preflight=temporary / 'preflight.json', ability=temporary / 'ability.json', prediction=None,
        observer=temporary / 'observer', kind='search', rate=800, seconds=60)
    api = {'sourceSha': 'f' * 40, 'pid': 100, 'sha256': '9' * 64,
        'likeDiagnosticsRequested': False, 'tailEnabledRequested': False, 'executionPlatform': 'linux'}
    result = scope['reserve'](args, api, {'CAP_LIKE_FIXTURE': str(temporary / 'fixture.json')})
    keys = ('executionAuthoritySHA256', 'executionAuthorityGeneration', 'deploymentId',
            'activeApplicationSHA256', 'admissionSHA256')
    check(all(result[key] == reservation[key] for key in keys),
          'all five verified authority identities are retained at command top level')
    check(saved['slot-S10.json'] == saved['S10-command.json'] == result,
          'reservation and command retain the same authority and independent admission')
    for key in keys:
        reservation.pop(key)
    legacy = scope['reserve'](args, api, {'CAP_LIKE_FIXTURE': str(temporary / 'fixture.json')})
    check(not any(key in legacy for key in keys), 'legacy reservation gains no invented authority fields')


def main():
    temporary = REPO.parent / ('.qa-cap10-run-' + uuid.uuid4().hex)
    assert temporary.parent == REPO.parent and not temporary.exists()
    temporary.mkdir()
    files = []
    class ProcessGone(Exception):
        pass
    class Process:
        def __init__(self, pid):
            self.pid = pid
        def create_time(self):
            return 10
        def cmdline(self):
            return ['node', 'scripts/cap10-like.mjs']
        def memory_info(self):
            return SimpleNamespace(rss=100)
    expected = {name: {'id': str(index) * 64, 'name': '/cap10-' + name, 'service': name, 'volumes': []}
                for index, name in enumerate(('mysql', 'redis', 'kafka', 'elasticsearch'), 1)}
    stats = [{'ID': value['id'][:12], 'Name': value['name'][1:], 'MemPerc': '20.00%'} for value in expected.values()]
    owned = SimpleNamespace(read=lambda p: json.loads(p.read_text()), now=lambda: '2026-11-01T08:00:00Z',
        api_identity=lambda _: Process(1),
        run=lambda *a, **k: SimpleNamespace(stdout='\n'.join(json.dumps(item) for item in stats)),
        container=lambda name, cid, running: {**expected[name], 'restartCount': 0, 'oomKilled': False, 'healthStatus': 'healthy'})
    psutil = SimpleNamespace(Process=Process, NoSuchProcess=ProcessGone, Error=ProcessGone,
        virtual_memory=lambda: SimpleNamespace(percent=70), cpu_percent=lambda _: 20)
    scope = {'owned': owned, 'psutil': psutil, 'json': json, 'dt': dt,
             'subprocess': SimpleNamespace(SubprocessError=RuntimeError)}
    exec(compile(isolated, 'cap10-run.py(actual pure functions)', 'exec'), scope)
    counters = {'cap_like_attempted': 10, 'cap_like_completed': 10, 'cap_like_noop': 0,
                'cap_like_effect_unknown': 0, 'cap_like_content_errors': 0, 'cap_like_rejected': 0}
    def summaries(changes=None):
        for phase in ('warm', 'stable'):
            path = temporary / (phase + '-summary.json')
            if path not in files:
                files.append(path)
            values = {**counters, **(changes or {})}
            path.write_text(json.dumps({'metrics': {key: {'count': value} for key, value in values.items()}}))
    def classified(report_changes=None, state_changes=None, kind='search'):
        state = {'passed': False, 'resourcesPassed': True, 'toolBytesStable': True, 'collectionValid': True,
                 'traceSwitchesValid': True, 'containersValid': True, 'powerEvidence': {'valid': True},
                 'failedStartOrException': None, 'pipeline': {'recoverySecondsFromNativeEnd': 5}}
        state.update(state_changes or {})
        report = {'finalFactsValid': True, 'warmExit': 0, 'setup': {'generatedFactTransitions': 5},
                  'nativeScenario': {'complete': True}, 'result': {'dropped': 0, 'actualOfferedRatio': 1}}
        report.update(report_changes or {})
        scope['classify'](state, report, temporary, kind)
        return state
    try:
        summaries()
        check(classified(state_changes={'passed': True})['outcome'] == 'pass', 'complete valid native artifacts can pass')
        check(classified()['boundaryEligible'] is True, 'complete fact-correct latency or backlog miss can form U')
        done = {'at': '2026-11-02T10:27:04.828487+00:00', 'source': 'cap10-stage',
                'reason': 'verified owned generator exited; harness reconciliation continues'}
        recovered = {'valid': False, 'collectionValid': True, 'allSamplesComplete': True, 'failedSamples': 0,
            'semanticViolationCount': 0, 'samples': 63, 'postControlDoneConverged': False, 'recoveredAt': None,
            'startedAt': '2026-11-02T10:22:57.5110783Z', 'doneObservedAt': '2026-11-02T10:27:07.908360981Z',
            'at': '2026-11-02T10:28:08.015878806Z'}
        terminal = scope['observer_terminal_outcome']
        check(terminal(1, recovered, done) == 'recovery_limited',
              'complete observed recovery timeout is a trusted limited terminal, not an exception')
        limited = classified(state_changes={'observerExit': 1, 'pipeline': {'recoverySecondsFromNativeEnd': None}})
        check(limited['outcome'] == 'performance_limited' and not limited['batchFatal']
              and limited['boundaryEligible'] and not limited['recoveryVerified'],
              'observer exit1 alone does not invalidate otherwise complete performance evidence')
        check(terminal(0, {'valid': True, 'collectionValid': True}, done) == 'converged',
              'legacy converged observer exit0 stays trusted')
        for changes, label in [({'collectionValid': False}, 'missing collection'),
                               ({'allSamplesComplete': False}, 'incomplete samples'),
                               ({'failedSamples': 1}, 'failed observation'),
                               ({'semanticViolationCount': 1}, 'semantic violation'),
                               ({'samples': 12}, 'too few recovery samples'),
                               ({'doneObservedAt': None}, 'no observed controller done'),
                               ({'at': '2026-11-02T10:28:00Z'}, 'recovery less than60 seconds'),
                               ({'at': '2026-11-02T10:28:20Z'}, 'unbounded recovery observation gap'),
                               ({'recoveredAt': recovered['at']}, 'contradictory convergence')]:
            check(terminal(1, {**recovered, **changes}, done) is None,
                  label + ' remains an invalid observer terminal')
        check(terminal(2, recovered, done) is None, 'unexpected observer exit code remains invalid')
        check(terminal(1, recovered, {**done, 'source': 'unknown'}) is None,
              'unverified controller done source cannot explain observer exit')
        for key in ('cap_like_noop', 'cap_like_effect_unknown', 'cap_like_content_errors', 'cap_like_rejected'):
            summaries({key: 1})
            check(classified()['batchFatal'], key + ' ends the whole batch')
        summaries({'cap_like_completed': 9})
        check(classified()['batchFatal'], 'missing terminal sample remains unknown even after reconciliation')
        summaries()
        check(classified({'phaseAccountingValid': False})['batchFatal'], 'warm/setup/main accounting mismatch is fatal')
        check(classified({'finalFactsValid': False})['batchFatal'], 'real SQL/Redis fact difference is fatal')
        check(classified({'result': {'mutationCounterMatchesDB': False}})['batchFatal'], 'native mutation versus DB transition mismatch ends the batch')
        check(classified({'nativeScenario': {'complete': False}})['boundaryEligible'] is False, 'early stop is never a service U')
        check(classified({'result': {'dropped': 1, 'actualOfferedRatio': .9}})['outcome'] == 'generator_limited', 'drop is a generator limit and cannot form U')
        check(classified(state_changes={'containersValid': False})['outcome'] == 'measurement_invalid', 'restart/OOM/container identity failure is not a service U')
        check(classified(kind='formal')['boundaryEligible'] is False, 'formal failure cannot reopen the search')
        (temporary / 'stable-summary.json').unlink()
        check(classified()['outcome'] == 'measurement_invalid', 'missing native summary never establishes a valid boundary')
        identity = {'2:10': {'pid': 2, 'created': 10}}
        guard = scope['pre_stable_guard']({}, temporary, identity, expected, .1)
        check(not guard['hostCPUIntervalValid'] and not guard['errors'], 'first CPU interval is explicitly invalid without fabricating a CPU sample')
        check(guard['ownedLoadRSSBytes'] == 100 and guard['phase'] == 'setup-or-harness', 'setup guard includes verified helper and no imaginary live warm k6')
        stats[0]['MemPerc'] = '85.0%'
        check(bool(scope['pre_stable_guard']({}, temporary, identity, expected, 5)['violations']), 'warm/setup container memory uses unchanged 85 percent stop')
        stats[0]['ID'] = 'unknown'
        check(bool(scope['pre_stable_guard']({}, temporary, identity, expected, 5)['errors']), 'unverified container sample is incomplete rather than zero')
        check(scope['container_snapshot'](expected)['valid'], 'all four healthy unchanged containers can satisfy boundary inspection')
        previous = owned.container
        owned.container = lambda *a: {**previous(*a), 'restartCount': 1}
        check(not scope['container_snapshot'](expected)['valid'], 'fast restart is detected even after container health recovers')
        authority_wiring(temporary, files)
    finally:
        for path in files:
            if path.exists():
                path.unlink()
        temporary.rmdir()
    print(json.dumps({'checks': checks, 'failed': 0, 'servicesStarted': False, 'loadRuns': 0}))


if __name__ == '__main__':
    main()
