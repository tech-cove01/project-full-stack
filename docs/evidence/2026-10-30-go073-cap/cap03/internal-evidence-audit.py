"""Offline CAP-03 cross-file audit. No network, services, credentials or workload.

--check-only checks finished stages without writing the final JSON. Final output
requires four --selected-hold names and the fixed --selected-api-source-sha.
It creates internal-evidence-audit.json exclusively after root releases data.
Exported quantiles are cross-checked, not reconstructed from missing HTTP bodies.
"""
import argparse
import datetime as dt
import hashlib
import json
import math
from pathlib import Path
import platform
import re


BASE = Path(__file__).resolve().parent
REPO = BASE.parents[3]
FINAL_API_SOURCE = '767c2fc8bdf1be08c5636143a71898eec25081a9'
ORIGINAL_FIXTURE_SOURCE = '3db296dd0beedd6f85d06cf5479d707b0498ce7e'
ORIGINAL_FIXTURE_MANIFEST_SHA256 = '7e3a9a9019c7fffc35dbbffd1c66f88ca65db2808faee1d263017698e2501754'
API_METADATA_SUFFIX = ('docs', 'evidence', '2026-10-30-go073-cap', 'cap03', 'api-pool-fix.json')
STATUS = (
    'Uptime Questions Queries Com_select Com_insert Com_update Com_delete '
    'Com_commit Com_stmt_execute Com_stmt_prepare Com_stmt_close '
    'Com_stmt_reprepare Performance_schema_digest_lost '
    'Performance_schema_prepared_statements_lost'
).split()
EVENTS = ['statement/com/Execute', 'statement/com/Prepare',
          'statement/sql/select', 'statement/sql/insert', 'statement/sql/update',
          'statement/sql/delete', 'statement/sql/commit']
INPUTS = {}


def read(path, ndjson=False):
    raw = path.read_bytes()
    INPUTS[str(path.relative_to(REPO)).replace('\\', '/')] = {
        'sha256': hashlib.sha256(raw).hexdigest(), 'bytes': len(raw)}
    text = raw.decode('utf-8-sig')
    return ([json.loads(line) for line in text.splitlines() if line.strip()]
            if ndjson else json.loads(text))


def stamp(value):
    return dt.datetime.fromisoformat(value.replace('Z', '+00:00')).timestamp() * 1000


def finite(value):
    return isinstance(value, (int, float)) and not isinstance(value, bool) and math.isfinite(value)


def equal(left, right):
    if finite(left) and finite(right):
        return math.isclose(left, right, rel_tol=1e-12, abs_tol=1e-9)
    return left == right


def mapped_api_metadata_path(captured):
    """Validate captured provenance lexically; never dereference its prefix."""
    if not isinstance(captured, str) or not captured or '\0' in captured or '://' in captured:
        raise ValueError('invalid captured API metadata path')
    if captured.endswith(('/', '\\')):
        raise ValueError('captured API metadata must name a file')
    parts = [part for part in captured.replace('\\', '/').split('/') if part]
    if any(part in ('.', '..') for part in parts):
        raise ValueError('captured API metadata traversal rejected')
    if any(':' in part and not (index == 0 and re.fullmatch('[A-Za-z]:', part))
           for index, part in enumerate(parts)):
        raise ValueError('captured API metadata path is not a filesystem path')
    if tuple(parts[-len(API_METADATA_SUFFIX):]) != API_METADATA_SUFFIX:
        raise ValueError('captured API metadata has an unexpected repository suffix')
    # Only this known local evidence file is read, even in a relocated checkout.
    local = BASE / API_METADATA_SUFFIX[-1]
    if local.is_symlink() or not local.resolve().is_relative_to(BASE):
        raise ValueError('local API metadata must stay inside the evidence directory')
    return local


def provenance(name, report, config, supervision, check, notes):
    source, revision = report.get('apiSourceSha'), report.get('apiExpectedRevision')
    result = {'status': 'legacy_without_API_provenance', 'api_source_sha': source,
              'api_expected_revision': revision, 'fixture_source_sha': report.get('fixtureSourceSha'),
              'fixture_expected_revision': report.get('fixtureExpectedRevision'),
              'fixture_manifest_sha256': report.get('fixtureManifestSHA256'),
              'eligible_API_provenance_for_final_holds': False}
    if source is None and revision is None:
        notes.append('Legacy result lacks explicit API source/revision; fixtureSourceSha is never substituted and this stage cannot enter the final four holds.')
        return result
    check('API_source_is_full_SHA', isinstance(source, str) and re.fullmatch('[0-9a-f]{40}', source) is not None)
    check('API_revision_binds_API_source', revision == 'capacity-' + str(source))
    fixture_path = BASE / 'fixture-01/fixture.json'
    fixture = read(fixture_path)
    fixture_hash = INPUTS[str(fixture_path.relative_to(REPO)).replace('\\', '/')]['sha256']
    check('original_fixture_bytes_preserved', fixture_hash == ORIGINAL_FIXTURE_MANIFEST_SHA256)
    check('original_fixture_source_preserved', fixture['sourceSha'] == ORIGINAL_FIXTURE_SOURCE)
    check('original_fixture_revision_preserved', fixture['expectedRevision'] == 'capacity-' + ORIGINAL_FIXTURE_SOURCE)
    check('stage_fixture_source_matches_original', report.get('fixtureSourceSha') == fixture['sourceSha'])
    check('stage_fixture_revision_matches_original', report.get('fixtureExpectedRevision') == fixture['expectedRevision'])
    check('stage_fixture_byte_hash_matches_original', report.get('fixtureManifestSHA256') == fixture_hash)
    check('stage_fixture_identity_matches_original', report.get('fixture') == fixture['fixture'])
    command = read(BASE / (name + '-command.json'))
    check('command_API_source_matches_result', command['sourceSha'] == source)
    check('command_fixture_source_matches_original', command['fixtureSourceSha'] == fixture['sourceSha'])
    check('command_PID_matches_sampled_API', command['apiPid'] == config['pid'] == supervision['api_pid'])
    check('command_workload_matches_result', all(command[key] == report[key] for key in ('mode', 'rate', 'seconds')))
    metadata_path = mapped_api_metadata_path(command['apiMetadataFile'])
    metadata = read(metadata_path)
    metadata_hash = INPUTS[str(metadata_path.relative_to(REPO)).replace('\\', '/')]['sha256']
    check('API_metadata_bytes_match_command_hash', metadata_hash == command['apiMetadataSHA256'].lower())
    check('API_metadata_source_matches_result', metadata['sourceSha'] == source)
    check('API_metadata_revision_matches_result', metadata['expectedVersion'] == revision)
    check('API_metadata_PID_matches_samples', metadata['pid'] == config['pid'])
    result.update({'status': 'explicit_API_and_fixture_provenance',
                   'API_metadata_captured_path': command['apiMetadataFile'],
                   'API_metadata_path': str(metadata_path.relative_to(REPO)).replace('\\', '/'),
                   'API_metadata_mapping': 'fixed repository suffix mapped to current BASE; captured absolute prefix is not dereferenced',
                   'API_metadata_sha256': metadata_hash, 'api_pid': metadata['pid'],
                   'binary_sha256': metadata['sha256'], 'original_fixture_actual_sha256': fixture_hash})
    if source == FINAL_API_SOURCE:
        build = read(BASE / 'build-767c2fc/build.json')
        check('selected_API_build_source', build['sourceSha'] == FINAL_API_SOURCE)
        check('selected_API_build_revision', build['expectedRevision'] == revision)
        check('selected_API_build_binary_matches_runtime_metadata', build['binarySHA256'].lower() == metadata['sha256'].lower())
        check('selected_API_build_clean_and_successful', build['buildExit'] == 0 and build['vcsModified'] is False and build['gitStatusPorcelain'] == '')
        result['eligible_API_provenance_for_final_holds'] = (
            revision == 'capacity-' + FINAL_API_SOURCE
            and fixture_hash == ORIGINAL_FIXTURE_MANIFEST_SHA256
            and report.get('fixtureManifestSHA256') == fixture_hash
            and report.get('fixtureSourceSha') == ORIGINAL_FIXTURE_SOURCE)
    return result


def audit_unscored(name):
    """Require preserved process evidence, never infer zero business counts."""
    load, control = BASE / (name + '-load'), BASE / (name + '-control')
    marker = read(load / 'unscored-failure.json')
    exit_path = control / 'exit-status.json'
    supervision = read(exit_path)
    checks = []

    def check(label, condition):
        checks.append({'check': label, 'passed': bool(condition)})

    code = supervision.get('harness_exit')
    check('marker_binds_exact_stage', marker.get('stage') == name)
    check('recognized_pre_stable_classification', marker.get('classification') in (
        'native_harness_abort_before_stable', 'setup_abort_before_stable'))
    check('preserved_nonzero_harness_exit', isinstance(code, int) and not isinstance(code, bool) and code != 0)
    check('marker_exit_matches_original', marker.get('harnessExit') == code)
    if marker.get('classification') == 'native_harness_abort_before_stable':
        check('native_exit_has_exception_status', isinstance(code, int) and (code & 0xFFFFFFFF) >= 0x80000000)
    if marker.get('exitHex') is not None:
        check('marker_hex_matches_original_exit', isinstance(code, int)
              and marker['exitHex'].upper() == f'0x{code & 0xFFFFFFFF:08X}'.upper())
    check('marker_explicitly_unscored_unknown', marker.get('scored') is False and marker.get('counts') is None)
    check('marker_does_not_claim_stable_or_sampler', marker.get('stablePhaseStarted') is False and marker.get('samplerStarted') is False)
    check('original_read_sampler_never_started', supervision.get('mode') == 'read' and supervision.get('sampler_started') is False)
    check('original_resource_samples_zero', supervision.get('resource_samples') == 0 and supervision.get('complete_resource_samples') == 0)
    check('not_resource_or_forced_stop', supervision.get('stop_signal') is False and supervision.get('forced') is False and not supervision.get('forced_owned_processes'))
    check('original_supervision_not_successful', supervision.get('completed_without_supervision_failure') is False)
    absent = ['result.json', 'stable-process.json', 'stable-summary.json']
    check('no_stable_result_process_or_summary', all(not (load / filename).exists() for filename in absent))
    # The harness awaits this durable checkpoint before spawning stable k6.
    # PID files alone cannot exclude a native abort in its async onStart hook.
    check('no_before_stable_durable_checkpoint', not (load / 'before.json').exists())
    process = read(load / 'process.json') if (load / 'process.json').exists() else None
    check('no_stable_current_process', process is None or process.get('phase') == 'warm')
    check('no_sampler_resource_file', not (control / 'resources.ndjson').exists())
    relative_exit = str(exit_path.relative_to(REPO)).replace('\\', '/')
    exit_hash = INPUTS[relative_exit]['sha256']
    check('marker_reference_binds_original_exit_bytes', any(
        item.get('path', '').replace('\\', '/') == relative_exit and item.get('sha256') == exit_hash
        for item in marker.get('evidence', [])))
    command_path = BASE / (name + '-command.json')
    command = read(command_path) if command_path.exists() else {}
    return {'stage': name, 'classification': marker.get('classification'),
            'scored': False, 'business_failure': None, 'capacity_stage_candidate': False,
            'counts': {key: None for key in ('started', 'native_http', 'terminal', 'success',
                                             'unknown', 'system_errors', 'http_errors',
                                             'content_errors', 'dropped')},
            'raw_harness_exit': code, 'exit_status_source': relative_exit,
            'exit_status_sha256': exit_hash, 'sampler_started': supervision.get('sampler_started'),
            'known_resource_sample_count': supervision.get('resource_samples'),
            'stable_result_absent': not (load / 'result.json').exists(),
            'before_stable_checkpoint_absent': not (load / 'before.json').exists(),
            'planned_API_source_sha': command.get('sourceSha'),
            'planned_API_pid': command.get('apiPid'),
            'provenance_scope': 'Launcher declaration only; no stable business measurement was observed.',
            'reason': 'Preserved native/setup abort before stable/sampler; cause and business request counts are unknown. Separate retry does not replace this record.',
            'checks': checks, 'proof_valid': all(item['passed'] for item in checks)}


def audit_stage(load):
    name = load.name[:-5]
    control = BASE / (name + '-control')
    report = read(load / 'result.json')
    metrics = read(load / 'stable-summary.json')['metrics']
    before, after = read(load / 'before.json'), read(load / 'after.json')
    access = read(load / 'access-delta.json')
    supervision = read(control / 'exit-status.json')
    records = read(control / 'resources.ndjson', ndjson=True)
    checks = []
    notes = []

    def check(label, condition):
        checks.append({'check': label, 'passed': bool(condition)})

    def count(metric):
        value = metrics[metric]['count']
        check('integer_counter:' + metric,
              finite(value) and value >= 0 and int(value) == value and value <= 2**53 - 1)
        return value

    started, native = count('cap_read_attempted'), count('http_reqs')
    success = count('cap_read_success')
    terminal_source = 'cap_read_completed'
    if 'cap_read_completed' in metrics:
        terminal = count('cap_read_completed')
    else:
        rate = metrics['cap_read_failure_ratio']
        terminal = rate['passes'] + rate['fails']
        terminal_source = 'legacy failure-ratio passes+fails; earlier instrumentation point'
        notes.append('Legacy Rate-derived classification is not identical to the later terminal instruction.')
    system = count('cap_read_system_errors')
    http = count('cap_read_http_errors')
    content = count('cap_read_content_errors')
    dropped = count('dropped_iterations')
    check('started_native_terminal_success_order', started >= native >= terminal >= success)
    check('errors_fit_classified_denominator', all(0 <= x <= terminal for x in (system, http, content)))
    check('positive_attempted_count', started > 0)
    unknown, native_unclassified = started - terminal, native - terminal
    actual = report.get('result') or {}
    expected_fields = {
        'attempted': started, 'completedHttpRequests': native, 'completed': terminal,
        'successful': success, 'incompleteOrUnclassified': unknown,
        'completedButUnclassified': native_unclassified,
        'classificationCoverage': terminal / started if started else None,
        'systemErrors': system, 'httpErrors': http, 'contentErrors': content,
        'dropped': dropped, 'targetOffers': report['rate'] * report['seconds'],
        'actualOfferedRatio': started / (report['rate'] * report['seconds']),
        'issuedPerNominalSecond': started / report['seconds'],
        'successfulPerNominalSecond': success / report['seconds'],
        'systemErrorRate': system / started if started else None,
        'systemErrorRatePerCompleted': system / terminal if terminal else None,
    }
    for key, value in expected_fields.items():
        if key in actual:
            check('result_matches_export:' + key, equal(actual[key], value))
        elif terminal_source == 'cap_read_completed':
            check('result_required_field_present:' + key, False)
    for report_key, metric_name in [('allLatency', 'cap_read_all_ms'),
                                    ('successfulLatency', 'cap_read_success_ms')]:
        trend = metrics.get(metric_name) or {}
        for quantile in ('p(95)', 'p(99)'):
            value = trend.get(quantile)
            check('finite_export:' + metric_name + ':' + quantile, finite(value))
            check('result_quantile_matches_export:' + metric_name + ':' + quantile,
                  equal((actual.get(report_key) or {}).get(quantile), value))

    commands = {}
    for label, snapshot in [('before', before), ('after', after)]:
        status = snapshot['globalStatus']
        parsed = {}
        rows = []
        for line in status['raw'].splitlines():
            if not line:
                continue
            fields = line.split('\t')
            if len(fields) != 2:
                raise ValueError('invalid public SHOW counter row')
            key, value = fields
            check(label + ':unique_canonical_status:' + key,
                  key not in parsed and value.isascii() and value.isdecimal())
            parsed[key] = int(value)
            rows.append({'name': key, 'value': value})
        check(label + ':complete_status_names', sorted(parsed) == sorted(STATUS))
        check(label + ':raw_status_matches_stored_map', parsed == status['counters'])
        check(label + ':raw_status_matches_stored_rows', rows == status['rawRows'])
    for key in STATUS:
        commands[key] = after['globalStatus']['counters'][key] - before['globalStatus']['counters'][key]
    check('global_command_deltas_match', commands == access['globalCommands'])
    begin, end = before['globalStatus'], after['globalStatus']
    elapsed = (end['startedAt'] + end['endedAt'] - begin['startedAt'] - begin['endedAt']) / 2
    uncertainty = 2000 + end['endedAt'] - end['startedAt'] + begin['endedAt'] - begin['startedAt']
    uptime_ok = elapsed >= 0 and commands['Uptime'] >= 0 and abs(commands['Uptime'] * 1000 - elapsed) <= uncertainty
    check('uptime_consistency_recomputed', uptime_ok == access['uptimeConsistent'])
    check('status_elapsed_recomputed', equal(elapsed, access['statusElapsedMs']))
    check('status_uncertainty_recomputed', equal(uncertainty, access['statusUncertaintyMs']))

    def event_delta():
        prior = {row['event']: row for row in before['statementEvents']}
        check('before_event_classes_complete', sorted(prior) == sorted(EVENTS))
        check('after_event_classes_complete', sorted(row['event'] for row in after['statementEvents']) == sorted(EVENTS))
        return [{'event': row['event'],
                 **{key: row[key] - prior[row['event']][key]
                    for key in ('count', 'errors', 'examined', 'sent')},
                 'timerPicoseconds': str(int(row['picoseconds']) - int(prior[row['event']]['picoseconds']))}
                for row in after['statementEvents']]

    events = event_delta()
    check('event_deltas_match', events == access['statementEvents'])
    prior = {row['digest']: row for row in before['digests']}
    digest_deltas = []
    for row in after['digests']:
        old = prior.get(row['digest'], {})
        digest_deltas.append({'digest': row['digest'],
                              **{key: row[key] - old.get(key, 0) for key in ('count', 'examined', 'sent')},
                              'timerPicoseconds': str(int(row['picoseconds']) - int(old.get('picoseconds', '0')))})
    projected = lambda rows: sorted(({key: row[key] for key in ('digest', 'count', 'examined', 'sent', 'timerPicoseconds')}
                                      for row in rows if row['count'] > 0), key=lambda row: row['digest'])
    check('auxiliary_digest_deltas_match', projected(digest_deltas) == projected(access['sql']))
    missing = [row['digest'] for row in before['digests'] if row['digest'] not in {x['digest'] for x in after['digests']}]
    check('missing_digests_match', missing == access['missingAfterDigests'])
    redis = {key: {'calls': row['calls'] - before['redis'].get(key, {}).get('calls', 0),
                   'usec': row['usec'] - before['redis'].get(key, {}).get('usec', 0),
                   'failed': row.get('failed_calls', 0) - before['redis'].get(key, {}).get('failed_calls', 0)}
             for key, row in after['redis'].items()}
    check('redis_deltas_match', {key: row for key, row in redis.items() if row['calls'] > 0} == access['redis'])
    negatives = any(value < 0 for value in commands.values())
    for rows in (events, digest_deltas, list(redis.values())):
        negatives = negatives or any(int(value) < 0 for row in rows for key, value in row.items()
                                     if key not in ('event', 'digest'))
    reset = not uptime_ok or negatives
    check('counter_reset_flag_recomputed', reset == access['counterResets'])
    check('digest_lost_delta_recomputed', commands['Performance_schema_digest_lost'] == access['lostDigestsDelta'])
    check('prepared_lost_delta_recomputed', commands['Performance_schema_prepared_statements_lost'] == access['preparedStatementsLostDelta'])
    check('public_count_loads_are_unknown', access['publicCountDatabaseLoads'] is None)
    counters_valid = (not reset and commands['Performance_schema_digest_lost'] == 0
                      and commands['Performance_schema_prepared_statements_lost'] == 0 and not missing)
    all_trend = metrics.get('cap_read_all_ms') or {}
    success_trend = metrics.get('cap_read_success_ms') or {}
    budget = bool(report.get('stableExit') == 0 and not report.get('stableExternalStop')
                  and started > 0 and terminal == native == started
                  and started / (report['rate'] * report['seconds']) >= .99
                  and success / started >= .99 and dropped == 0
                  and system / started < .01 and content == 0 and http / started < .01
                  and all_trend.get('p(95)', math.inf) <= 200
                  and all_trend.get('p(99)', math.inf) <= 500
                  and success_trend.get('p(95)', math.inf) <= 200
                  and success_trend.get('p(99)', math.inf) <= 500 and counters_valid)
    check('meets_budget_recomputed_from_numbers_exit_and_coverage', bool(report['meetsBudget']) == budget)
    check('meets_budget_is_boolean', isinstance(report['meetsBudget'], bool))

    samples = [row for row in records if row.get('kind') == 'sample']
    config = next(row for row in records if row.get('kind') == 'configuration')
    complete = [row for row in samples if not row.get('errors')
                and all(row.get(key) for key in ('api', 'diagnostics', 'containers', 'generators'))]
    cpu = [row for index, row in enumerate(samples) if row.get('cpu_interval_valid', index > 0)]
    generator_cpu = [item['cpu_percent_one_core'] for index, row in enumerate(samples)
                     for item in row.get('generators', []) if item.get('cpu_interval_valid', index > 0)]
    for index, row in enumerate(samples):
        if 'cpu_interval_seconds' in row:
            check('parent_cpu_validity:' + str(index), row['cpu_interval_valid'] == (row['cpu_interval_seconds'] >= 1))
        for item in row.get('generators', []):
            if 'cpu_interval_seconds' in item:
                check('generator_cpu_validity:' + str(index), item['cpu_interval_valid'] == (item['cpu_interval_seconds'] >= 1))
        if 'diagnostics' in row:
            check('api_diagnostic_identity:' + str(index), row['diagnostics']['pid'] == config['pid'] == supervision['api_pid'])
    check('supervisor_sample_count', supervision['resource_samples'] == len(samples))
    check('supervisor_complete_sample_count', supervision['complete_resource_samples'] == len(complete))
    caps = config['stop_caps']
    busy = 0
    budget_hits = []
    for index, row in enumerate(samples):
        busy = (busy + 1 if row.get('cpu_interval_valid', index > 0)
                and row['host']['cpu_percent'] >= caps['host_cpu_percent_for_three_samples'] else 0)
        reasons = []
        if busy >= 3:
            reasons.append('three_valid_host_CPU_samples')
        if row['host']['memory_percent'] >= caps['host_memory_percent']:
            reasons.append('host_memory')
        if row.get('api', {}).get('rss_bytes', 0) >= caps['api_rss_bytes']:
            reasons.append('API_RSS')
        if sum(item['rss_bytes'] for item in row.get('generators', [])) >= caps['generator_rss_bytes']:
            reasons.append('generator_RSS')
        if any(float(item['MemPerc'].rstrip('%')) >= caps['container_memory_percent']
               for item in row.get('containers', [])):
            reasons.append('container_memory')
        if reasons:
            budget_hits.append({'at': row['at'], 'reasons': reasons})
    check('observed_resource_budget_has_stop_signal', not budget_hits or supervision['stop_signal'])
    maximum = lambda section, key, rows: max((row[section][key] for row in rows if section in row), default=None)
    pool_delta = {}
    if complete:
        for section, keys in {'sql': ('wait_count', 'wait_duration_ns'),
                              'redis': ('wait_count', 'wait_duration_ns', 'timeouts'),
                              'runtime': ('num_gc', 'pause_total_ns', 'total_alloc_bytes')}.items():
            pool_delta[section] = {key: complete[-1]['diagnostics'][section][key] - complete[0]['diagnostics'][section][key] for key in keys}
            check('sampled_counters_monotonic:' + section, all(value >= 0 for value in pool_delta[section].values()))
    resource = {
        'samples': len(samples), 'complete_samples': len(complete), 'valid_parent_cpu_samples': len(cpu),
        'sampler_sha256_declared': config.get('sampler_sha256'),
        'legacy_cpu_first_sample_excluded': 'cpu_interval_valid' not in samples[0] if samples else None,
        'first_sample_at': samples[0]['at'] if samples else None,
        'last_sample_at': samples[-1]['at'] if samples else None,
        'valid_api_cpu_one_core_peak_percent': maximum('api', 'cpu_percent_one_core', cpu),
        'valid_host_cpu_peak_percent': maximum('host', 'cpu_percent', cpu),
        'valid_generator_cpu_one_core_peak_percent': max(generator_cpu, default=None),
        'host_memory_peak_percent': maximum('host', 'memory_percent', samples),
        'observed_pool_runtime_deltas_first_to_last_complete_sample': pool_delta,
        'complete_delta_window_seconds': (stamp(complete[-1]['at']) - stamp(complete[0]['at'])) / 1000 if complete else None,
        'last_complete_sample_before_stable_command_end_ms': report['stableEndedAt'] - stamp(complete[-1]['at']) if complete else None,
        'last_complete_sample_elapsed_ms': complete[-1]['sampler']['sample_elapsed_ms'] if complete else None,
        'sampler_ends_after_generator_exit': supervision.get('sampling_ended_after_generator_exit'),
        'resource_stop_signal': supervision['stop_signal'],
        'independently_observed_resource_budget_hits': budget_hits,
        'supervision_ok': supervision['completed_without_supervision_failure'],
        'errors': [{'at': row['at'], 'errors': row['errors']} for row in samples if row.get('errors')],
        'coverage_limit': 'Sequential 5-second snapshots, first CPU interval excluded when invalid; no full request-window or post-exit server-drain coverage is implied.'}
    diagnostic = name.startswith(('profile-', 'probe-')) or report['seconds'] < 30
    provenance_check_start = len(checks)
    stage_provenance = provenance(name, report, config, supervision, check, notes)
    stage_provenance['all_provenance_checks_passed'] = all(item['passed'] for item in checks[provenance_check_start:])
    stage_provenance['eligible_API_provenance_for_final_holds'] = (
        stage_provenance['eligible_API_provenance_for_final_holds']
        and stage_provenance['all_provenance_checks_passed'])
    return {'stage': name, 'mode': report['mode'], 'rate': report['rate'], 'nominal_seconds': report['seconds'],
            'provenance': stage_provenance,
            'script_hashes_declared_by_stage': report.get('scriptSHA256'),
            'counts': {'started': started, 'native_http': native, 'terminal': terminal,
                       'terminal_source': terminal_source, 'success': success, 'unknown': unknown,
                       'native_but_unclassified': native_unclassified, 'system_errors': system,
                       'http_errors': http, 'content_errors': content, 'dropped': dropped},
            'numerical_quantiles_ms': {'all_p95': all_trend.get('p(95)'), 'all_p99': all_trend.get('p(99)'),
                                       'success_p95': success_trend.get('p(95)'), 'success_p99': success_trend.get('p(99)')},
            'raw_export_threshold_booleans_ignored': all_trend.get('thresholds'),
            'raw_meets_budget': report['meetsBudget'], 'recomputed_meets_budget': budget,
            'diagnostic_or_short_probe_only': diagnostic,
            'capacity_stage_candidate': budget and not diagnostic and supervision['completed_without_supervision_failure'] and bool(complete),
            'hold_duration_candidate': budget and not diagnostic and report['seconds'] >= 180 and supervision['completed_without_supervision_failure'],
            'global_commands': commands, 'counter_resets': reset, 'uptime_consistent': uptime_ok,
            'resources': resource, 'checks': checks, 'notes': notes}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--check-only', action='store_true')
    parser.add_argument('--selected-hold', action='append', default=[], metavar='STAGE',
                        help='exact final-report stage name without -load; repeat for its four selected holds')
    parser.add_argument('--selected-api-source-sha',
                        help='fixed new API source SHA for all four final holds, independent of fixture source')
    args = parser.parse_args()
    if not args.check_only and (len(args.selected_hold) != 4 or len(set(args.selected_hold)) != 4):
        parser.error('final JSON requires exactly four distinct final-report --selected-hold names')
    if (not args.check_only or args.selected_hold or args.selected_api_source_sha) and args.selected_api_source_sha != FINAL_API_SOURCE:
        parser.error('final hold selection requires --selected-api-source-sha ' + FINAL_API_SOURCE)
    stages, unscored, pending, errors = [], [], [], []
    names = {path.name[:-5] for path in BASE.glob('*-load') if path.is_dir()}
    names.update(path.name[:-8] for path in BASE.glob('*-control') if path.is_dir())
    for name in sorted(names):
        load, control = BASE / (name + '-load'), BASE / (name + '-control')
        if not (control / 'exit-status.json').exists():
            pending.append(name + '-load')
            continue
        try:
            if (load / 'result.json').exists():
                stages.append(audit_stage(load))
            elif (load / 'unscored-failure.json').exists():
                unscored.append(audit_unscored(name))
            else:
                pending.append(name + '-load')
        except Exception as error:
            errors.append({'stage': name, 'error_class': type(error).__name__})
    failures = [{'stage': stage['stage'], 'check': check['check']}
                for stage in stages + unscored for check in stage['checks'] if not check['passed']]
    selected_checks = []
    selected = {}
    if args.selected_hold:
        by_name = {stage['stage']: stage for stage in stages}
        selected_checks.append({'check': 'exactly_four_distinct_explicit_holds',
                                'passed': len(args.selected_hold) == len(set(args.selected_hold)) == 4})
        selected_checks.append({'check': 'explicit_new_API_source_required',
                                'passed': args.selected_api_source_sha == FINAL_API_SOURCE})
        for name in args.selected_hold:
            stage = by_name.get(name)
            valid = bool(stage and stage['nominal_seconds'] == 180
                         and stage['hold_duration_candidate']
                         and not stage['diagnostic_or_short_probe_only']
                         and stage['counts']['unknown'] == 0
                         and stage['resources']['supervision_ok']
                         and not stage['resources']['resource_stop_signal']
                         and stage['resources']['complete_samples'] > 0
                         and stage['provenance']['eligible_API_provenance_for_final_holds']
                         and stage['provenance']['api_source_sha'] == args.selected_api_source_sha
                         and stage['provenance']['api_expected_revision'] == 'capacity-' + str(args.selected_api_source_sha)
                         and all(item['passed'] for item in stage['checks']))
            selected_checks.append({'check': 'selected_180_second_complete_pass:' + name,
                                    'passed': valid})
            if stage:
                selected.setdefault(stage['mode'], []).append({
                    'stage': name, 'result_path': 'cap03/' + name + '-load/result.json',
                    'rate': stage['rate'], 'nominal_seconds': stage['nominal_seconds'],
                    'provenance': stage['provenance'],
                    'counts': stage['counts'], 'numerical_quantiles_ms': stage['numerical_quantiles_ms'],
                    'complete_resource_samples': stage['resources']['complete_samples'],
                    'recomputed_meets_budget': stage['recomputed_meets_budget'],
                    'qualifies_as_selected_hold': valid})
        selected_checks.append({'check': 'selected_modes_are_hot_and_multi',
                                'passed': set(selected) == {'hot', 'multi'}})
        for mode in ('hot', 'multi'):
            holds = selected.get(mode, [])
            selected_checks.append({'check': 'two_same_rate_180_second_holds:' + mode,
                                    'passed': len(holds) == 2 and len({item['rate'] for item in holds}) == 1})
        failures.extend({'stage': 'explicit_final_hold_selection', 'check': item['check']}
                        for item in selected_checks if not item['passed'])
    current_sources = {}
    for source in [Path(__file__), REPO / 'scripts/go073-cap-read.mjs',
                   REPO / 'scripts/load/go073-cap-read.js', REPO / 'scripts/capacity-resources.py',
                   REPO / 'scripts/capacity-stage.py', BASE / 'summarize.py']:
        raw = source.read_bytes()
        current_sources[str(source.relative_to(REPO)).replace('\\', '/')] = {
            'sha256': hashlib.sha256(raw).hexdigest(), 'bytes': len(raw)}
    result = {'task': 'GO-073-CAP-03-internal-evidence-audit',
              'generated_at': dt.datetime.now(dt.timezone.utc).isoformat(),
              'internal_only': True, 'external_acceptance': False,
              'source': {'python_version': platform.python_version(), 'current_source_bytes': current_sources,
                         'command': 'python -X utf8 docs/evidence/2026-10-30-go073-cap/cap03/internal-evidence-audit.py' + (' --check-only' if args.check_only else '') + (' --selected-api-source-sha ' + args.selected_api_source_sha if args.selected_api_source_sha else '') + ''.join(' --selected-hold ' + name for name in args.selected_hold),
                         'scope': 'Local JSON/NDJSON only; no private configuration, credentials, network, services, SQL, load or process control.'},
              'counts': {'audited_stages': len(stages), 'unscored_native_setup_stages': len(unscored),
                         'pending_stages': len(pending),
                         'checks': sum(len(stage['checks']) for stage in stages + unscored) + len(selected_checks),
                         'failed_checks': len(failures), 'read_or_schema_errors': len(errors)},
              'pending_stages': pending, 'errors': errors, 'failures': failures,
              'checks_passed_for_finished_stages': not errors and not failures,
              'final_hold_selection': {'source': 'explicit --selected-hold names supplied by root from the final report; no automatic best-stage selection',
                                      'selected_API_source_sha': args.selected_api_source_sha,
                                      'required_API_source_sha': FINAL_API_SOURCE,
                                      'required_original_fixture_source_sha': ORIGINAL_FIXTURE_SOURCE,
                                      'required_original_fixture_manifest_sha256': ORIGINAL_FIXTURE_MANIFEST_SHA256,
                                      'requested_stages': args.selected_hold, 'by_mode': selected,
                                      'checks': selected_checks},
              'all_stage_files_finished': not pending, 'stages': stages,
              'unscored_stages': unscored, 'input_files': INPUTS,
              'limits': ['Internal cross-file reproduction, not external QA or a live API verification.',
                         'Quantiles are checked against native exported numbers, not independently rebuilt from unavailable response samples.',
                         'Raw legacy threshold booleans are not interpreted as passed; numerical quantiles, counts and exit/stop status determine budgets.',
                         'System/HTTP error categories overlap; unknown calls remain unknown and are not zero-error completions.',
                         'SQL/Redis deltas include background/helper/observer and sequential snapshot boundaries; Com_select/Com_stmt_execute overlap and are never summed.',
                         'A script SHA declared by an older stage identifies its captured revision; current source hashes are recorded separately and need not equal older script versions.',
                         'Legacy stages without explicit apiSourceSha/apiExpectedRevision remain legacy; fixture source is never inferred as runtime API source and cannot satisfy final new-API selection.',
                         'Native/setup aborts without stable result are separately unscored only with matching preserved nonzero exit and no stable/sampler proof; business counts remain null and these stages cannot enter final selection.',
                         'CPU sampling is coarse and shared-host interference is not excluded; sampled counter deltas cover their saved endpoints, not necessarily the whole nominal duration.']}
    summary = {**result['counts'], 'checks_passed_for_finished_stages': result['checks_passed_for_finished_stages'],
               'pending_stages': pending, 'failures': failures, 'errors': errors,
               'final_json_written': not args.check_only}
    if not args.check_only:
        with (BASE / 'internal-evidence-audit.json').open('x', encoding='utf-8', newline='\n') as stream:
            json.dump(result, stream, ensure_ascii=False, indent=2)
            stream.write('\n')
    print(json.dumps(summary, ensure_ascii=False))
    return 0 if not errors and not failures and (args.check_only or not pending) else 1


if __name__ == '__main__':
    raise SystemExit(main())
