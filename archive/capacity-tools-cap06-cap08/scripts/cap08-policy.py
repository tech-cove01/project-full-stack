"""CAP08 immutable reservations and conservative development-budget accounting.

This module has no network/process-control operations. Gate statements are
explicit operator conclusions backed by public evidence, not inferred fixes.
"""
import argparse
import datetime as dt
import json
from pathlib import Path
import re


REPO = Path(__file__).resolve().parent.parent
ROOT = REPO / 'docs/evidence/2026-10-30-cap08'
MAX_DEVELOPMENT_SECONDS = 4 * 60 * 60
STAIRS = (150, 200, 300, 400)


def read(path):
    return json.loads(Path(path).read_text(encoding='utf-8-sig'))


def save(path, value):
    with Path(path).open('x', encoding='utf-8') as stream:
        json.dump(value, stream, ensure_ascii=False, indent=2)


def now():
    return dt.datetime.now(dt.timezone.utc).isoformat()


def instant(value):
    parsed = dt.datetime.fromisoformat(value.replace('Z', '+00:00'))
    assert parsed.tzinfo is not None, 'explicit UTC offset required'
    return parsed


def public_path(path):
    path = Path(path).resolve()
    assert path.is_relative_to(REPO / 'docs') and path.is_file(), 'existing public repository evidence required'
    return path


def budget(root=ROOT, at=None):
    record = read(root / 'budget-start.json')
    assert record['task'] == 'CAP-08' and record['maximumDevelopmentSeconds'] == MAX_DEVELOPMENT_SECONDS
    elapsed = ((at or dt.datetime.now(dt.timezone.utc)) - instant(record['startedAt'])).total_seconds()
    assert elapsed >= 0
    # Deliberately conservative: no unverified waiting subtraction. Each stage
    # separately records load/collector wall-clock intervals for later review.
    return {'elapsedConservativeSeconds': elapsed, 'maximumDevelopmentSeconds': MAX_DEVELOPMENT_SECONDS,
            'remainingConservativeSeconds': max(0, MAX_DEVELOPMENT_SECONDS - elapsed),
            'withinBudget': elapsed <= MAX_DEVELOPMENT_SECONDS,
            'accounting': 'all wall time since declared start; load/CI waits are separately recorded, not subtracted'}


def history(root=ROOT):
    return sorted((read(path) for path in root.glob('*-command.json')), key=lambda value: value['at'])


def passed(root, command):
    tag = command['tag']
    try:
        result = read(root / (tag + '-load/result.json'))
        stage = read(root / (tag + '-stage-exit.json'))
        control = read(root / (tag + '-control/exit-status.json'))
        return (result.get('meetsBudget') is True and stage.get('capacityWrapperExit') == 0 and
                stage.get('wrapperExited') is True and stage['failedStartOrException'] is None and
                stage.get('cardsEvidenceValid') is True and stage.get('toolBytesStable') is True and control.get('harness_exit') == 0 and
                control.get('sampler_exit') == 0 and not control.get('errors') and
                control.get('stop_signal') is False and control.get('forced') is False)
    except (OSError, ValueError, KeyError, TypeError):
        return False


def effective_diagnostic(root, command):
    try:
        stage = read(root / (command['tag'] + '-stage-exit.json'))
        collector = read(root / (command['tag'] + '-diagnostic/manifest.json'))
        return (stage.get('extraDiagnosticCollectorExit') == 0 and collector.get('stable_observed') is True and
                collector.get('poll_coverage_complete') is True and collector.get('stage_fields_valid') is True and
                collector.get('active_age_coverage_complete') is True and
                collector.get('slow_records_visibility_complete') is True and
                not collector.get('errors') and not any(capture.get('available') is not True or capture.get('truncated') for capture in collector.get('captures', [])))
    except (OSError, ValueError, KeyError, TypeError):
        return False


def measured_slo_failure(root, command):
    try:
        tag = command['tag']
        result = read(root / (tag + '-load/result.json'))
        stage = read(root / (tag + '-stage-exit.json'))
        control = read(root / (tag + '-control/exit-status.json'))
        return (result.get('meetsBudget') is False and result.get('result') and not result.get('failure') and
                result['result'].get('contentErrors') == 0 and result.get('warmExit') == 0 and
                result.get('stableExit') in (0, 99) and not result.get('warmExternalStop') and not result.get('stableExternalStop') and
                (root / (tag + '-load/stable-summary.json')).is_file() and
                stage.get('wrapperExited') is True and stage['failedStartOrException'] is None and
                stage.get('cardsEvidenceValid') is True and stage.get('toolBytesStable') is True and
                control.get('sampler_exit') == 0 and control.get('stop_signal') is False and
                not control.get('errors') and control.get('forced') is False)
    except (OSError, ValueError, KeyError, TypeError):
        return False


def has_stage_observation(root, command):
    try:
        collector = read(root / (command['tag'] + '-diagnostic/manifest.json'))
        return (collector.get('stable_observed') is True and collector.get('api_source_sha') == command['apiSourceSha'] and
                type(collector.get('successful_polls')) is int and collector['successful_polls'] > 0 and
                (root / (command['tag'] + '-diagnostic/timeline.ndjson')).is_file())
    except (OSError, ValueError, KeyError, TypeError):
        return False


def treatment_proof(path, source_sha):
    proof = read(public_path(path))
    assert proof.get('task') == 'CAP-08' and proof.get('kind') == 'treatment-proof'
    assert proof.get('mechanismObserved') is True and isinstance(proof.get('mechanism'), str) and proof['mechanism']
    assert proof.get('method') in ('old-fail-new-pass', 'same-source-causal')
    before, after = proof['baseline'], proof['treated']
    assert before['result'] == 'FAIL' and after['result'] == 'PASS' and after['sourceSha'] == source_sha
    for item in (before, after):
        assert re.fullmatch(r'[a-f0-9]{40}', item['sourceSha'])
        public_path(item['configuration'])
        assert isinstance(item.get('evidence'), list) and item['evidence']
        for evidence in item['evidence']:
            public_path(evidence)
    assert isinstance(proof.get('siteEvidence'), list) and proof['siteEvidence']
    for evidence in proof['siteEvidence']:
        public_path(evidence)
    if proof['method'] == 'same-source-causal':
        assert before['sourceSha'] == after['sourceSha'] and proof.get('oneChangeOnly') is True
    else:
        assert public_path(before['configuration']).read_bytes() == public_path(after['configuration']).read_bytes(), 'old/new deterministic proof keeps public configuration fixed'
    return proof


def prediction(path, kind):
    value = read(public_path(path))
    assert value.get('task') == 'CAP-08' and value.get('kind') == kind
    for field in ('prediction', 'differentObservation'):
        assert isinstance(value.get(field), str) and 1 <= len(value[field]) <= 4000
    assert isinstance(value.get('evidence'), list) and value['evidence']
    for entry in value['evidence']:
        public_path(entry)
    return value


def formal_gate(path, source_sha, root=ROOT):
    path = public_path(path)
    gate = read(path)
    assert gate.get('task') == 'CAP-08' and gate.get('kind') == 'formal-gate'
    assert gate.get('sourceSha') == source_sha and re.fullmatch(r'[a-f0-9]{40}', source_sha)
    assert all(gate.get(field) is True for field in ('relatedChecksPassed', 'exactSourceCIPassed',
                                                   'provenIssuesHandled', 'workCompletedWithinBudget'))
    assert gate.get('diagnosticOutcome') in ('not-reproduced', 'treated')
    assert gate.get('unresolvedReproducedFailure') is False
    started = instant(read(root / 'budget-start.json')['startedAt'])
    assert 0 <= (instant(gate['workCompletedAt']) - started).total_seconds() <= MAX_DEVELOPMENT_SECONDS
    assert instant(gate['workCompletedAt']) <= instant(gate['at']) <= dt.datetime.now(dt.timezone.utc)
    assert isinstance(gate.get('evidence'), list) and gate['evidence']
    for entry in gate['evidence']:
        public_path(entry)
    diagnostics = [command for command in history(root) if command['kind'] == 'diagnostic']
    assert diagnostics, 'at least one actual diagnostic attempt required'
    if gate['diagnosticOutcome'] == 'not-reproduced':
        assert all(effective_diagnostic(root, command) for command in diagnostics), 'missing collection is not non-reproduction'
        assert all(passed(root, command) for command in diagnostics), 'failed diagnostic cannot be promoted to non-reproduction'
    else:
        assert any(has_stage_observation(root, command) for command in diagnostics), 'treated requires an actual site stage observation'
        treatment_proof(gate['treatmentEvidence'], source_sha)
    return gate


def stage_slot(args, commands, root=ROOT):
    diagnostics = [row for row in commands if row['kind'] == 'diagnostic']
    formal = [row for row in commands if row['kind'] == 'formal']
    if args.kind == 'diagnostic':
        assert budget(root)['withinBudget'] and (args.mode, args.rate, args.seconds) == ('multi', 100, 180)
        assert len(diagnostics) < 2 and not formal, 'two CAP08 diagnostics maximum, none after formal begins'
        predicted = prediction(args.prediction, 'diagnostic')
        assert (root / 'slot-controlled-1.json').is_file() and public_path(predicted['wiringEvidence'])
        if diagnostics:
            change = predicted.get('change', {})
            assert change.get('type') in ('candidate', 'proven-fix')
            assert change.get('baselineTag') == diagnostics[0]['tag'] and isinstance(change.get('description'), str) and change['description']
            assert change.get('oneChangeOnly') is True
            assert read(root / (diagnostics[0]['tag'] + '-stage-exit.json'))['wrapperExited'] is True
        return 'diagnostic-' + str(len(diagnostics) + 1)
    formal_gate(args.gate, args.source_sha, root)
    expected = (('multi', 100, 180), ('multi', 100, 180), ('hot', 200, 180))
    if args.kind == 'formal':
        assert len(formal) < 3 and (args.mode, args.rate, args.seconds) == expected[len(formal)]
        assert all(passed(root, row) for row in formal), 'first failed formal stage ends the group'
        assert all(row['apiSourceSha'] == args.source_sha for row in formal), 'fixed final source required'
        return 'formal-' + str(len(formal) + 1)
    assert len(formal) == 3 and all(passed(root, row) for row in formal), 'complete formal group before optional ascent'
    assert all(row['apiSourceSha'] == args.source_sha for row in commands if row['kind'] != 'diagnostic')
    stairs = [row for row in commands if row['kind'] == 'stair']
    neighbors = [row for row in commands if row['kind'] == 'neighbor']
    holds = [row for row in commands if row['kind'] == 'hold']
    failed_optional = [row for row in stairs + neighbors if not passed(root, row)]
    assert all(measured_slo_failure(root, row) for row in failed_optional), 'resource, collection or correctness failure ends optional work'
    if args.kind == 'stair':
        assert args.mode == 'multi' and args.seconds == 45 and len(stairs) < 4 and args.rate == STAIRS[len(stairs)]
        assert not neighbors and not holds and all(passed(root, row) for row in stairs), 'first limited stair ends ascent'
        return 'stair-' + str(len(stairs) + 1)
    failed = [row for row in stairs if not passed(root, row)]
    high = max([100] + [row['rate'] for row in stairs + neighbors if passed(root, row)])
    if args.kind == 'neighbor':
        assert args.mode == 'multi' and args.seconds == 45 and len(failed) == 1 and not neighbors and not holds
        assert high < args.rate < failed[0]['rate']
        assert measured_slo_failure(root, failed[0]), 'only a measured SLO failure defines a service-capacity bracket'
        return 'neighbor-1'
    assert args.kind == 'hold' and args.mode == 'multi' and args.seconds == 180 and args.rate == high
    assert (failed or len(stairs) == 4) and high > 100 and len(holds) < 2
    assert all(passed(root, row) for row in holds), 'first failed high hold ends optional ascent; do not backfill'
    return 'hold-' + str(len(holds) + 1)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest='action', required=True)
    start = commands.add_parser('init-budget')
    start.add_argument('--started-at', required=True)
    start.add_argument('--source-sha', required=True)
    reserve = commands.add_parser('reserve')
    reserve.add_argument('--kind', choices=('controlled', 'repair'), required=True)
    reserve.add_argument('--prediction', type=Path, required=True)
    reserve.add_argument('--tag', required=True)
    commands.add_parser('budget')
    args = parser.parse_args()
    assert Path.cwd().resolve() == REPO and ROOT.is_dir()
    if args.action == 'init-budget':
        assert re.fullmatch(r'[a-f0-9]{40}', args.source_sha)
        assert 0 <= (dt.datetime.now(dt.timezone.utc) - instant(args.started_at)).total_seconds() <= MAX_DEVELOPMENT_SECONDS
        save(ROOT / 'budget-start.json', {'task': 'CAP-08', 'startedAt': args.started_at, 'recordedAt': now(),
             'sourceSha': args.source_sha, 'maximumDevelopmentSeconds': MAX_DEVELOPMENT_SECONDS,
             'accounting': 'conservative all-wall-time; load and CI waits are recorded separately, not subtracted'})
    elif args.action == 'reserve':
        assert budget()['withinBudget'] and re.fullmatch(r'[a-z0-9][a-z0-9-]{1,60}', args.tag)
        predicted = prediction(args.prediction, args.kind)
        if args.kind == 'controlled' and (ROOT / 'slot-controlled-1.json').exists():
            assert predicted.get('observedCandidate') is True, 'second controlled scenario requires an observed candidate'
        existing = list(ROOT.glob('slot-' + args.kind + '-*.json'))
        assert len(existing) < 2, 'two reservations maximum; failed starts consume a slot'
        slot = args.kind + '-' + str(len(existing) + 1)
        save(ROOT / ('slot-' + slot + '.json'), {'task': 'CAP-08', 'kind': args.kind, 'slot': slot,
             'tag': args.tag, 'at': now(), 'predictionPath': str(args.prediction.resolve()), 'failedStartsConsumeSlot': True})
        print(json.dumps({'reserved': slot, 'tag': args.tag}))
    else:
        print(json.dumps(budget()))


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        print('cap08-policy: ' + type(error).__name__ + '; preserve reservations and public evidence')
        raise SystemExit(1)
