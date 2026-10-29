"""Summarize a completed event run and its separate Kafka offset samples."""
import json
import math
import sys
from collections import defaultdict
from datetime import datetime
from pathlib import Path

run = json.loads(Path(sys.argv[1]).read_text(encoding='utf-8'))
samples = json.loads(Path(sys.argv[2]).read_text(encoding='utf-8'))
assert run['success'] and not run['failures'] and not samples['failures']
stable = [note for note in run['notes'] if note['phase'] == 'stable']
stable_ids = {note['id'] for note in stable}
assert len(stable) == len(stable_ids) == 180


def distribution(values):
    ordered = sorted(values)
    assert ordered
    def p(value):
        return ordered[math.ceil(value * len(ordered)) - 1]
    return {'count': len(ordered), 'min': ordered[0], 'p50': p(.5), 'p95': p(.95), 'p99': p(.99), 'max': ordered[-1]}


def iso(value):
    return datetime.fromisoformat(value.replace('Z', '+00:00'))


projection = defaultdict(list)
for row in run['projectionLatencyRaw'].splitlines():
    event, kind, status, relay_us, name, state, projection_us = row.split('\t')
    if event.split(':')[1] not in stable_ids:
        continue
    assert status == 'DONE' and state in ('APPLIED', 'IGNORED')
    projection[f'{kind}/{name}/{state}'].append(int(projection_us) / 1000)
assert len(projection) == 6 and all(len(values) == 180 for values in projection.values())
assert all(value >= 0 for values in projection.values() for value in values)
visibility = [row['fromRequestStartMs'] for row in run['visibility'] if row['phase'] == 'stable']
assert len(visibility) == 180
lag = defaultdict(list)
for sample in samples['samples']:
    for group, item in sample['groups'].items():
        for row in item['raw'].splitlines():
            fields = row.split()
            if len(fields) >= 6 and fields[0] == group and fields[1] == 'community.events':
                assert all(x.isdecimal() for x in fields[2:6])
                lag[group].append(int(fields[5]))
assert len(lag) == 3 and all(len(values) == len(samples['samples']) for values in lag.values())
timings = run['timings']
stopped = next(s for s in run['snapshots'] if s['label'] == 'es-down-parked')
output = {
    'stableActualElapsedMs': timings['stableElapsedMs'],
    'stablePublished': len(stable),
    'stableLikes': sum(note['like'] for note in stable),
    'publishRequestLatencyMs': distribution([note['completed'] - note['started'] for note in stable]),
    'visibleFirstPollUpperBoundMs': distribution(visibility),
    'visibilityTailCount': sum(row['phase'] == 'stable' and row['observationPhase'] == 'tail' for row in run['visibility']),
    'projectionLatencyMsFromDatabaseTimestamps': {name: distribution(values) for name, values in projection.items()},
    'lagSamples': {name: {'count': len(values), 'maxSampled': max(values), 'values': values} for name, values in lag.items()},
    'sampleIntervalSeconds': samples['sampleIntervalSeconds'],
    'sampleStartedAt': samples['startedAt'], 'sampleEndedAt': samples['endedAt'],
    'stopRequestToParkedSnapshotMs': (iso(stopped['at']) - iso(timings['stopRequested'])).total_seconds() * 1000,
    'esRestartToHealthyMs': (iso(timings['esHealthy']) - iso(timings['startRequested'])).total_seconds() * 1000,
    'esRestartToVisibleAfterManualReplaysMs': (iso(timings['converged']) - iso(timings['startRequested'])).total_seconds() * 1000,
    'snapshots': [{'label': row['label'], 'at': row['at'], 'offsets': row['parsedOffsets'], 'sql': row['rawSQL']} for row in run['snapshots']],
    'reconciliation': run['reconciliation'],
}
print(json.dumps(output, indent=2))
