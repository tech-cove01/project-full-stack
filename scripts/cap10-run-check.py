"""Offline checks of the actual runner's classification and warm guard functions."""
import ast
import json
from pathlib import Path
from types import SimpleNamespace
import uuid

REPO = Path(__file__).resolve().parent.parent
source = ast.parse((REPO / 'scripts/cap10-run.py').read_text(encoding='utf-8'))
names = {'classify', 'pre_stable_guard', 'container_snapshot'}
isolated = ast.Module(body=[node for node in source.body if isinstance(node, ast.FunctionDef) and node.name in names], type_ignores=[])
checks = 0


def check(condition, label):
    global checks
    assert condition, label
    checks += 1
    print('PASS ' + label)


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
    scope = {'owned': owned, 'psutil': psutil, 'json': json, 'subprocess': SimpleNamespace(SubprocessError=RuntimeError)}
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
    finally:
        for path in files:
            if path.exists():
                path.unlink()
        temporary.rmdir()
    print(json.dumps({'checks': checks, 'failed': 0, 'servicesStarted': False, 'loadRuns': 0}))


if __name__ == '__main__':
    main()
