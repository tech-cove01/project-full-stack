"""Summarize one saved read stage; no network, credentials or workload actions."""
import datetime as dt
import json
import sys
from pathlib import Path


def read(path):
    return json.loads(path.read_text(encoding='utf-8-sig'))


def stamp(value):
    return dt.datetime.fromisoformat(value.replace('Z', '+00:00')).timestamp() * 1000


def main():
    stage = Path(sys.argv[1])
    load, control = Path(str(stage) + '-load'), Path(str(stage) + '-control')
    report, access, supervisor = read(load / 'result.json'), read(load / 'access-delta.json'), read(control / 'exit-status.json')
    metrics = read(load / 'stable-summary.json')['metrics']
    # Preserve legacy raw files. Their terminal Rate samples establish the
    # classified count before cap_read_completed was explicitly instrumented.
    classification = metrics.get('cap_read_failure_ratio', {})
    classified = metrics.get('cap_read_completed', {}).get('count')
    source = 'cap_read_completed'
    if classified is None:
        classified = classification.get('passes', 0) + classification.get('fails', 0)
        source = 'legacy cap_read_failure_ratio passes+fails; original files unchanged'
    attempted = metrics['cap_read_attempted']['count']
    http_completed = metrics['http_reqs']['count']
    completion = {'attempted': attempted, 'completedHttpRequests': http_completed, 'classified': classified,
        'incompleteOrUnclassified': attempted - classified, 'completedButUnclassified': http_completed - classified,
        'classifiedSource': source, 'systemErrorRatePerClassified': metrics['cap_read_system_errors']['count'] / classified if classified else None}
    resource_rows = [json.loads(line) for line in (control / 'resources.ndjson').read_text(encoding='utf-8-sig').splitlines()]
    configuration = next((row for row in resource_rows if row.get('kind') == 'configuration'), {})
    samples = [row for row in resource_rows if row.get('kind') == 'sample']
    complete = [row for row in samples if not row.get('errors') and row.get('generators')]
    containers = {}
    for row in samples:
        for item in row.get('containers', []):
            values = containers.setdefault(item['Name'], {'cpuPercentMax': 0, 'memoryPercentMax': 0})
            values['cpuPercentMax'] = max(values['cpuPercentMax'], float(item['CPUPerc'].rstrip('%')))
            values['memoryPercentMax'] = max(values['memoryPercentMax'], float(item['MemPerc'].rstrip('%')))
    def maximum(section, key, rows=None):
        return max((row[section][key] for row in (samples if rows is None else rows) if section in row), default=None)
    # psutil is primed immediately before the first sample. Its millisecond
    # CPU interval is subject to CPU-time quantization; retain it separately
    # instead of interpreting it as a five-second peak. Memory stays valid.
    cpu_samples = [row for i, row in enumerate(samples) if row.get('cpu_interval_valid', i > 0)]
    generator_cpu = [item['cpu_percent_one_core'] for i, row in enumerate(samples)
        for item in row.get('generators', []) if item.get('cpu_interval_valid', i > 0)]
    first, last = samples[0] if samples else None, samples[-1] if samples else None
    resources = {
        'samples': len(samples), 'completeSamples': len(complete),
        'samplerSha256': configuration.get('sampler_sha256'), 'validCpuSamples': len(cpu_samples),
        'observationErrors': [error for row in samples for error in row.get('errors', [])],
        'firstSampleAt': first['at'] if first else None, 'lastSampleAt': last['at'] if last else None,
        'firstSampleAfterStableSpawnMs': stamp(first['at']) - report['stableStartedAt'] if first else None,
        'stableProcessEndAfterLastSampleMs': report['stableEndedAt'] - stamp(last['at']) if last else None,
        'maxSampleGapMs': max((stamp(b['at']) - stamp(a['at']) for a, b in zip(samples, samples[1:])), default=0),
        'cpuScope': 'sampler cpu_interval_valid intervals (at least one second); legacy samples exclude the first psutil priming interval; raw initial CPU retained separately',
        'initialCpu': {'host': maximum('host', 'cpu_percent', samples[:1]), 'apiOneCore': maximum('api', 'cpu_percent_one_core', samples[:1]), 'generatorOneCore': max((item['cpu_percent_one_core'] for row in samples[:1] for item in row.get('generators', [])), default=None)},
        'hostCpuPercentMax': maximum('host', 'cpu_percent', cpu_samples), 'hostMemoryPercentMax': maximum('host', 'memory_percent'),
        'apiCpuOneCorePercentMax': maximum('api', 'cpu_percent_one_core', cpu_samples), 'apiRssBytesMax': maximum('api', 'rss_bytes'),
        'generatorCpuOneCorePercentMax': max(generator_cpu, default=None),
        'generatorRssBytesMax': max((item['rss_bytes'] for row in samples for item in row.get('generators', [])), default=None),
        'containers': containers,
    }
    if first and last and 'diagnostics' in first and 'diagnostics' in last:
        for section, names in {'sql': ['wait_count', 'wait_duration_ns'], 'redis': ['wait_count', 'wait_duration_ns', 'timeouts'], 'runtime': ['num_gc', 'pause_total_ns', 'total_alloc_bytes']}.items():
            resources[section + 'SampledDelta'] = {name: last['diagnostics'][section][name] - first['diagnostics'][section][name] for name in names}
        resources['sqlInUseMax'] = max(row.get('diagnostics', {}).get('sql', {}).get('in_use', 0) for row in samples)
        resources['goGoroutinesMax'] = max(row.get('diagnostics', {}).get('runtime', {}).get('goroutines', 0) for row in samples)
    output = {
        'stage': stage.name, 'mode': report['mode'], 'rate': report['rate'], 'seconds': report['seconds'],
        'meetsBudget': report['meetsBudget'], 'failure': report.get('failure'),
        'result': report.get('result'), 'stableCommandElapsedMs': report.get('stableCommandElapsedMs'),
        'completionAudit': completion,
        'supervisionOk': supervisor['completed_without_supervision_failure'], 'stopSignal': supervisor['stop_signal'],
        'resources': resources, 'mysqlGlobalDelta': access['globalCommands'],
        'mysqlEventDelta': access['statementEvents'], 'redisDelta': access['redis'],
        'counterResets': access['counterResets'], 'uptimeConsistent': access['uptimeConsistent'],
        'scope': access['scope'], 'counterOverlap': access['commandCountersOverlap'],
    }
    (stage.parent / (stage.name + '-summary.json')).write_text(json.dumps(output, ensure_ascii=False, indent=2), encoding='utf-8')
    print(json.dumps(output, ensure_ascii=False))


if __name__ == '__main__':
    main()
