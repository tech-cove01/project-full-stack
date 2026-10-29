"""Read-only Kafka lag samples for the dedicated GO-073 event environment."""
import json
import subprocess
import sys
import time
from datetime import datetime, timezone
from pathlib import Path

container = 'project-full-stack-go073-independent-events-kafka-1'
groups = ('community.follow-v1', 'community.feed-v1', 'community.search-v1')
output = Path(sys.argv[1])
if output.exists():
    raise SystemExit('choose a new output file; preserve earlier results')


def timestamp():
    return datetime.now(timezone.utc).isoformat()


def run(*args):
    result = subprocess.run(['docker', *args], text=True, capture_output=True, timeout=30)
    if result.returncode:
        raise RuntimeError(f'docker {args[0]} failed: {result.stderr[:300]}')
    return result.stdout


labels = json.loads(run('inspect', '--format', '{{json .Config.Labels}}', container))
assert labels['com.docker.compose.project'] == 'project-full-stack-go073-independent-events'
assert labels['com.docker.compose.service'] == 'kafka'
report = {'startedAt': timestamp(), 'sampleIntervalSeconds': 20, 'samples': [], 'failures': []}
start = time.monotonic()
try:
    for iteration in range(7):
        wait = start + iteration * 20 - time.monotonic()
        if wait > 0:
            time.sleep(wait)
        sample = {'iteration': iteration, 'startedAt': timestamp(), 'groups': {}}
        for group in groups:
            group_start = timestamp()
            raw = run('exec', container, '/opt/kafka/bin/kafka-consumer-groups.sh', '--bootstrap-server', 'localhost:9094', '--group', group, '--describe')
            sample['groups'][group] = {'startedAt': group_start, 'endedAt': timestamp(), 'raw': raw}
        sample['endedAt'] = timestamp()
        report['samples'].append(sample)
except Exception as exc:
    report['failures'].append(str(exc))
    raise
finally:
    report['endedAt'] = timestamp()
    with output.open('x', encoding='utf-8') as handle:
        json.dump(report, handle, indent=2)
        handle.write('\n')
