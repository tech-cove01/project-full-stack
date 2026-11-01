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
    records = sorted((r for r in records if r.get('task') == 'CAP-10' and re.fullmatch(r'[SH][1-8]', r.get('tag', ''))), key=lambda r: r['at'])
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


def effective_work_budget(root, api_metadata, now=None):
    now = now or dt.datetime.now(dt.timezone.utc)
    if root == ROOT:
        require(api_metadata.get('executionPlatform') == 'windows', 'Windows metadata platform differs')
        require(not (ROOT / 'environment-transition.json').exists(), 'Windows execution closed after the single Linux switch')
        elapsed = (now - instant(read(ROOT / 'start.json')['effectiveWorkStartConservativeUTC'])).total_seconds()
        require(0 <= elapsed <= 10800, 'conservative three-hour development bound exhausted')
        return {'effectiveSecondsUpperBound': elapsed, 'provedLoadWaitSecondsDeducted': 0, 'maximumSeconds': 10800, 'platform': 'windows'}
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
    require(0 <= elapsed <= 10800, 'continued three-hour effective development bound exhausted')
    return {'effectiveSecondsUpperBound': elapsed, 'provedLoadWaitSecondsDeducted': deducted, 'maximumSeconds': 10800, 'platform': 'linux', 'usedBeforeSwitch': used, 'anchor': transition['recordedAt']}

def search_state(root, source_sha):
    records = history(root)
    boundary_records = records
    final_path = Path(root) / 'final-application.json'
    if final_path.is_file():
        final = read(final_path)
        after = final.get('boundaryEvidenceAfter') if final.get('sourceSha') == source_sha else None
        if after:
            indexes = [index for index, command in enumerate(records) if command['tag'] == after]
            require(len(indexes) == 1, 'rollback boundary reset must name a retained reservation')
            boundary_records = records[indexes[0] + 1:]
    relevant = [(c, stage(root, c)) for c in boundary_records if c['tag'].startswith('S') and c['kind'] != 'candidate' and c['apiSourceSha'] == source_sha]
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
        result['nextRate'] = 50
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
    require(api_metadata.get('likeDiagnosticsRequested') is (kind in ('diagnostic', 'comparison')) and api_metadata.get('tailEnabledRequested') is False, 'Like/Feed observation switches differ from purpose')
    budget = effective_work_budget(root, api_metadata)
    records = history(root)
    shorts = [c for c in records if c['tag'].startswith('S')]; formals = [c for c in records if c['tag'].startswith('H')]
    require(sum(c['seconds'] for c in records) + seconds <= 840, '840s total main-load budget exhausted')
    for c in records:
        previous = stage(root, c)
        require(previous.get('childrenConverged') is True, 'prior owned children have not converged')
        require(previous.get('batchFatal') is not True, 'correctness/unknown/noop ended the batch')
        require(previous.get('outcome') in ('pass', 'performance_limited'), 'measurement/generator/environment pause: do not invent a service upper bound')
        require(previous.get('recoveryVerified') is True, 'previous window did not recover within its 60s budget')
    require(not any(c['kind'] == 'candidate' for c in shorts) or kind == 'formal', 'one candidate only; no later search or replacement')
    if kind == 'formal':
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
        if not shorts:
            require(kind == 'diagnostic' and rate == 50, 'S1 must be 50/s diagnostic')
            require(prediction and prediction.get('observedEvidence') and prediction.get('differentPredictions'), 'S1 requires evidence and differentiating predictions before start')
        elif kind == 'comparison':
            require(len(shorts) == 1 and shorts[0]['kind'] == 'diagnostic' and rate == 50 and shorts[0]['apiSourceSha'] != source and prediction and prediction.get('comparisonOf') == 'S1' and prediction.get('singleVariableEvidence'), 'only the immediate one-variable 50/s comparison is allowed')
        else:
            require(kind in ('search', 'candidate'), 'no repeated diagnostic or second optimization comparison')
            final = read(root / 'final-application.json')
            require(final['sourceSha'] == source and final['binarySHA256'] == binary and final['environmentSHA256'] == environment and final['fixtureSHA256'] == fixture_hash, 'freeze final search application before continuing')
            require(any(c['kind'] == 'comparison' for c in shorts) or shorts[0]['apiSourceSha'] == source, 'changed version requires immediate comparison')
            comparisons = [c for c in shorts if c['kind'] == 'comparison']
            require(source in {shorts[0]['apiSourceSha'], *[c['apiSourceSha'] for c in comparisons]}, 'one optimization only; final source must be original or compared source')
            if comparisons and source == shorts[0]['apiSourceSha']:
                require(final.get('boundaryEvidenceAfter') == comparisons[0]['tag'] and final.get('rollbackEvidence'), 'rollback must reset old boundaries with explicit evidence')
            bound = search_state(root, source)
            if kind == 'candidate':
                require(bound['L'] is not None and rate == bound['candidateRate'], 'candidate must be L with margin or floor(.8L); never use S8 to find the first pass')
            else:
                require(len(shorts) < 7 and not bound['searchFinished'] and rate == bound['nextRate'], 'next search rate/remaining slot differs from declared algorithm')
                require(not any(c['rate'] == rate and c['tag'] in bound['boundaryTags'] for c in shorts), 'do not replay an already measured search rate')
    require(not (root / ('slot-' + tag + '.json')).exists() and not (root / (tag + '-command.json')).exists(), 'existing/orphan reservation is not reusable')
    return {'tag': tag, 'shortSlotsUsedAfter': len(shorts) + (kind != 'formal'), 'formalSlotsUsedAfter': len(formals) + (kind == 'formal'), 'mainSecondsReservedAfter': sum(c['seconds'] for c in records) + seconds,
            'shortMaximum': 8, 'formalMaximum': 2, 'mainSecondsMaximum': 840, 'S8ReservedForCandidate': True, 'elapsedConservativeSeconds': budget['effectiveSecondsUpperBound'], 'effectiveWorkBudget': budget, 'failedStartsConsumeSlot': True, 'writesPerformed': False}

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
