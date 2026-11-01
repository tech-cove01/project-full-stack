"""Pure CAP09 public-artifact budgets and arithmetic; no process/network APIs.

The caller owns exclusive reservation writes and process supervision. Positive
pipeline grades describe bounded 5s observations, never instantaneous backlog.
"""
import datetime as dt
import hashlib
import json
import math
from pathlib import Path
import re

REPO = Path(__file__).resolve().parent.parent
ROOT = REPO / 'docs/evidence/2026-11-01-cap09'
LIMITS = {'probe': 1, 'baseline-stair': 7, 'baseline-hot': 1,
          'diagnostic': 2, 'final-stair': 7, 'hold': 2, 'final-hot': 1}


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


def history(root, chain, kind):
    rows = [read(path) for path in Path(root).glob('*-command.json')]
    return sorted((r for r in rows if r.get('chain') == chain and r.get('kind') == kind), key=lambda r: r['at'])


def check_reservation(root, chain, kind, rate, seconds, mode, source_sha,
                      api_metadata, fixture, freeze=None, prediction=None):
    """Validate one next slot without writing it; a failed start still uses it.

    The caller must use the returned deterministic tag for an exclusive slot
    and command write before any start. It must not delete orphan reservations.
    """
    root, fixture = Path(root).resolve(), Path(fixture).resolve()
    require(root == ROOT and fixture.is_file() and fixture.is_relative_to(ROOT), 'fixed CAP09 public paths required')
    require(chain in ('feed', 'like') and kind in LIMITS, 'unknown chain or purpose')
    require(type(rate) is int and 0 < rate <= 5000, 'rate outside declared bound')
    require(seconds == (180 if kind == 'hold' else 8 if kind == 'probe' else 45), 'duration differs from purpose')
    require(mode == ('hot' if kind in ('baseline-hot', 'final-hot') else 'multi'), 'mode differs from purpose')
    require(re.fullmatch(r'[a-f0-9]{40}', source_sha or '') and api_metadata['sourceSha'] == source_sha, 'fixed clean application source required')
    require(api_metadata.get('expectedVersion') == 'capacity-' + source_sha, 'API expected version differs')
    require(sha(Path(api_metadata['exe'])) == api_metadata['sha256'], 'fixed API binary bytes differ')
    environment_sha, fixture_sha = sha(root / 'environment.json'), sha(fixture)
    if kind in ('final-stair', 'final-hot', 'hold'):
        final = read(root / 'final-application.json')
        require(final['sourceSha'] == source_sha and final['binarySHA256'] == api_metadata['sha256'], 'both chains must share the final clean application')
    started = instant(read(ROOT / 'start.json')['conservativeStartedAt'])
    elapsed = (dt.datetime.now(dt.timezone.utc) - started).total_seconds()
    require(0 <= elapsed <= 8 * 3600, 'conservative development upper bound exhausted')
    prior = history(root, chain, kind)
    regression = isinstance(prediction, dict) and prediction.get('probeRegressionOf') is not None
    require(not regression or kind == 'probe', 'tool-repair regression is only for the fixed low-speed probe')
    if regression:
        original_tag = chain + '-probe-1'
        require(len(prior) == 1 and prior[0].get('tag') == original_tag and
                prediction.get('probeRegressionOf') == original_tag, 'exactly one original probe is required for one repair regression')
        require(prediction.get('sameLowSpeedCase') is True and prediction.get('observedEvidence') and
                prediction.get('differentPredictions') and re.fullmatch(r'[a-f0-9]{40}', prediction.get('repairCommit') or ''),
                'probe regression requires a committed repair, observed evidence and the same low-speed case')
        original = prior[0]
        require(original['apiSourceSha'] == source_sha and original['binarySHA256'] == api_metadata['sha256'] and
                original['fixtureSHA256'] == fixture_sha and original['mode'] == mode and
                original['rate'] == rate and original['seconds'] == seconds, 'probe regression must keep the original application, binary, fixture and workload')
        require(prediction.get('originalEnvironmentSHA256') == original['environmentSHA256'] and
                original['environmentSHA256'] != environment_sha, 'environment-only repair must retain and change the original environment hash')
    else:
        require(len(prior) < LIMITS[kind], 'purpose budget exhausted')
    for command in prior:
        tag = command.get('tag', '')
        require(re.fullmatch(re.escape(chain + '-' + kind + '-') + r'[1-9]\d*', tag), 'malformed prior slot')
        terminal = root / (tag + '-stage.json')
        require(terminal.is_file(), 'previous attempt lacks terminal artifact; preserve it')
        if regression:
            old_stage = read(terminal)
            require(old_stage.get('passed') is False and old_stage.get('childrenConverged') is True and
                    old_stage.get('probeExpectedStopConfirmed') is not True, 'only a failed converged probe may receive an environment-repair regression')
        if kind in ('baseline-stair', 'final-stair', 'hold'):
            require(read(terminal).get('passed') is True, 'first failure ended this group')
        if kind in ('baseline-stair', 'final-stair', 'hold'):
            require(command['apiSourceSha'] == source_sha and command['binarySHA256'] == api_metadata['sha256'], 'one staircase/hold group must use one application source and binary')
            require(command['environmentSHA256'] == environment_sha and command['fixtureSHA256'] == fixture_sha, 'one staircase/hold group must use one environment and fixture')
        if kind == 'hold':
            require(command['rate'] == rate, 'both holds must use the same frozen rate')
    if prior and kind in ('baseline-stair', 'final-stair'):
        require(rate > prior[-1]['rate'], 'a staircase must advance, not replay or descend')
    tag = chain + '-probe-1-regression-1' if regression else chain + '-' + kind + '-' + str(len(prior) + 1)
    require(not (root / ('slot-' + tag + '.json')).exists() and not (root / (tag + '-command.json')).exists(), 'orphan or existing slot is not reusable')
    if kind == 'diagnostic':
        require(isinstance(prediction, dict) and prediction.get('observedEvidence') and prediction.get('differentPredictions'), 'diagnostic requires evidence and differentiating predictions')
    if kind == 'final-hot':
        old_hot = history(root, chain, 'baseline-hot')
        require(not any(c['apiSourceSha'] == source_sha for c in old_hot), 'reuse the same-source hot attempt; do not replay it')
        require(old_hot and prediction and prediction.get('relatedCodeChangeEvidence'), 'hot rerun requires actual related-code-change evidence')
    if kind == 'hold':
        require(isinstance(freeze, dict) and freeze.get('sourceSha') == source_sha and freeze.get('rate') == rate, 'freeze the selected stable target before starting')
        require(freeze.get('environmentSHA256') == environment_sha and freeze.get('binarySHA256') == api_metadata['sha256'], 'frozen environment/binary differs')
        require(freeze.get('fixtureSHA256') == fixture_sha, 'frozen fixture differs')
        require(freeze.get('shortStageEvidence') and freeze.get('relatedChecksPassed') is True and freeze.get('sourceCIPassed') is True, 'stable target lacks checks/CI/short-stage evidence')
        for stage in freeze['shortStageEvidence']:
            require(isinstance(stage, str) and re.fullmatch(r'[a-z0-9-]+', stage), 'invalid short-stage reference')
            command = read(root / (stage + '-command.json'))
            require(command['chain'] == chain and command['apiSourceSha'] == source_sha and command['rate'] >= rate and command['mode'] == 'multi' and command['seconds'] == 45, 'short-stage chain/source/rate/duration differs')
            require(command['fixtureSHA256'] == sha(fixture) and command['environmentSHA256'] == freeze['environmentSHA256'] and command['binarySHA256'] == api_metadata['sha256'], 'short-stage fixture/environment/binary differs')
            require(command['kind'] in ('baseline-stair', 'final-stair') and read(root / (stage + '-stage.json')).get('passed') is True, 'stable target exceeds a complete passing exploratory stage')
    return {'tag': tag, 'slot': 1 if regression else len(prior) + 1, 'maximumSlots': LIMITS[kind],
            'isToolRepairRegression': regression, 'toolRepairRegressionSlot': 1 if regression else None,
            'maximumToolRepairRegressions': 1 if kind == 'probe' else 0,
            'probeRegressionOf': prediction['probeRegressionOf'] if regression else None,
            'originalEnvironmentSHA256': prior[0]['environmentSHA256'] if regression else None,
            'elapsedConservativeSeconds': elapsed, 'maximumConservativeSeconds': 8 * 3600,
            'failedStartsConsumeSlot': True, 'writesPerformed': False}


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
    require(chain in ('feed', 'like') and type(seconds) is int and 1 <= seconds <= 180, 'invalid chain/window')
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
    require(directory.is_relative_to(ROOT), 'fixed CAP09 public evidence required')
    summary = read(directory / 'summary.json')
    rows = [json.loads(line) for line in (directory / 'timeline.ndjson').read_text(encoding='utf-8-sig').splitlines() if line]
    require(summary.get('valid') is True and summary.get('collectionValid') is True and rows, 'incomplete downstream collection')
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
    window_start = max(started, ended - dt.timedelta(seconds=min(seconds, 60)))
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
    if seconds == 45:
        recent = [(row, vector(row)) for row in rows if ended - dt.timedelta(seconds=15) <= instant(row['at']) <= ended]
        offsets = [(instant(row['at']) - ended).total_seconds() for row, _ in recent]
        mean = sum(offsets) / len(offsets) if offsets else 0
        variance = sum((x - mean) ** 2 for x in offsets)
        short_slopes = {'sampleCount': len(recent), 'firstSample': recent[0][0]['at'] if recent else None,
                        'lastSample': recent[-1][0]['at'] if recent else None,
                        'perSecond': {key: sum((x - mean) * values[key] for x, (_, values) in zip(offsets, recent)) / variance
                                      if variance else None for key in growth},
                        'scope': 'descriptive least-squares queue slope at sample starts in the final native 15s; underlying SQL/Kafka intervals are non-atomic. No additional pass/fail threshold.'}
    return {'passed': passed, 'chain': chain, 'newEventsInConservativeSampledWindow': events,
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
