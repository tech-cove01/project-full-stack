"""One-shot, read-only GO-073-CAP bottleneck evidence for owned local services.

No workload is generated and no process, connection pool, instrument, data row,
Redis key, or server configuration is changed. An explicitly requested standard
CPU profile runs for ten seconds and therefore has measurable observation cost.
"""
import argparse
import datetime as dt
import json
import os
from pathlib import Path
import re
import subprocess
import time
import urllib.request

import psutil


ENVIRONMENTS = {
    'project-full-stack-cap-trade': {'mysql': '3450', 'redis': '6550', 'api': 8250, 'diagnostics': 8350},
    'project-full-stack-cap-read': {'mysql': '3451', 'redis': '6551', 'api': 8251, 'diagnostics': 8351},
}
DATABASE = 'project_full_stack_go'

# DIGEST_TEXT is normalized by MySQL, with literal values replaced by markers.
# Never collect SQL_TEXT, processlist INFO, lock data values or INNODB STATUS.
QUERIES = {
    'mysql-digests.json': """SELECT JSON_OBJECT(
      'schema',SCHEMA_NAME,'digest',DIGEST,'normalized_text',DIGEST_TEXT,
      'count',COUNT_STAR,'sum_timer_wait_ps',SUM_TIMER_WAIT,
      'min_timer_wait_ps',MIN_TIMER_WAIT,'avg_timer_wait_ps',AVG_TIMER_WAIT,
      'max_timer_wait_ps',MAX_TIMER_WAIT,'sum_lock_time_ps',SUM_LOCK_TIME,
      'errors',SUM_ERRORS,'warnings',SUM_WARNINGS,'rows_affected',SUM_ROWS_AFFECTED,
      'rows_sent',SUM_ROWS_SENT,'rows_examined',SUM_ROWS_EXAMINED,
      'tmp_disk_tables',SUM_CREATED_TMP_DISK_TABLES,'tmp_tables',SUM_CREATED_TMP_TABLES,
      'select_full_join',SUM_SELECT_FULL_JOIN,'select_scan',SUM_SELECT_SCAN,
      'no_index_used',SUM_NO_INDEX_USED,'first_seen',FIRST_SEEN,'last_seen',LAST_SEEN)
      FROM performance_schema.events_statements_summary_by_digest
      WHERE SCHEMA_NAME='project_full_stack_go' AND DIGEST IS NOT NULL
      AND DIGEST_TEXT NOT LIKE '%performance_schema%'
      ORDER BY SUM_TIMER_WAIT DESC LIMIT 100""",
    'mysql-locks.json': """SELECT JSON_OBJECT('engine',ENGINE,
      'table',OBJECT_NAME,'lock_type',LOCK_TYPE,'lock_mode',LOCK_MODE,
      'status',LOCK_STATUS,'count',COUNT(*))
      FROM performance_schema.data_locks
      WHERE OBJECT_SCHEMA='project_full_stack_go'
      GROUP BY ENGINE,OBJECT_NAME,LOCK_TYPE,LOCK_MODE,LOCK_STATUS""",
    'mysql-lock-waits.json': """SELECT JSON_OBJECT('engine',ENGINE,
      'requesting_transaction',REQUESTING_ENGINE_TRANSACTION_ID,
      'blocking_transaction',BLOCKING_ENGINE_TRANSACTION_ID,
      'requesting_thread',REQUESTING_THREAD_ID,'blocking_thread',BLOCKING_THREAD_ID)
      FROM performance_schema.data_lock_waits LIMIT 100""",
    'mysql-consumers.json': """SELECT JSON_OBJECT('name',NAME,'enabled',ENABLED)
      FROM performance_schema.setup_consumers
      WHERE NAME IN ('statements_digest','events_statements_current',
      'events_waits_current','events_transactions_current')""",
    'mysql-instruments.json': """SELECT JSON_OBJECT('name',NAME,'enabled',ENABLED,'timed',TIMED)
      FROM performance_schema.setup_instruments
      WHERE NAME IN ('statement/sql/select','statement/sql/insert','statement/sql/update',
      'statement/sql/commit','wait/lock/table/sql/handler','wait/lock/metadata/sql/mdl')""",
    'mysql-status.json': """SELECT JSON_OBJECT('name',VARIABLE_NAME,'value',VARIABLE_VALUE)
      FROM performance_schema.global_status
      WHERE VARIABLE_NAME IN ('Threads_connected','Threads_running','Connections',
      'Innodb_row_lock_current_waits','Innodb_row_lock_waits','Innodb_row_lock_time',
      'Innodb_buffer_pool_reads','Innodb_buffer_pool_read_requests','Created_tmp_disk_tables',
      'Com_select','Com_insert','Com_update','Com_commit','Slow_queries',
      'Performance_schema_digest_lost')""",
    'mysql-settings.json': """SELECT JSON_OBJECT('name',VARIABLE_NAME,'value',VARIABLE_VALUE)
      FROM performance_schema.global_variables
      WHERE VARIABLE_NAME IN ('version','max_connections','innodb_buffer_pool_size',
      'innodb_flush_log_at_trx_commit','sync_binlog','innodb_lock_wait_timeout',
      'transaction_isolation','performance_schema')""",
}


def now():
    return dt.datetime.now(dt.timezone.utc).isoformat()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--project', choices=ENVIRONMENTS, required=True)
    parser.add_argument('--api-pid', type=int, required=True)
    parser.add_argument('--label', required=True)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--profile', action='store_true', help='CPU 10 seconds and a binary goroutine profile')
    args = parser.parse_args()
    if not re.fullmatch(r'[A-Za-z0-9._-]{1,80}', args.label):
        parser.error('label must be a short non-secret experiment identifier')
    user = os.environ.get('CAP_MYSQL_OBSERVER_USER')
    password = os.environ.get('CAP_MYSQL_OBSERVER_PASSWORD')
    if not user or not password:
        parser.error('explicit CAP_MYSQL_OBSERVER_USER and CAP_MYSQL_OBSERVER_PASSWORD required')
    if args.output.exists():
        parser.error('output must not exist; preserve earlier evidence')
    owned = ENVIRONMENTS[args.project]
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    api = psutil.Process(args.api_pid)
    identity = api.create_time()
    endpoint = f"http://127.0.0.1:{owned['diagnostics']}"

    def fetch(path, maximum=131072, timeout=3):
        if not api.is_running() or api.create_time() != identity:
            raise RuntimeError('API process identity changed')
        with opener.open(endpoint + path, timeout=timeout) as response:
            body = response.read(maximum + 1)
        if len(body) > maximum:
            raise RuntimeError('bounded diagnostic response exceeded')
        return body

    def snapshot():
        body = json.loads(fetch('/debug/capacity'))
        if body.get('pid') != args.api_pid:
            raise RuntimeError('diagnostic PID does not match owned API')
        return body

    def docker(command, input_text=None, environment=None):
        result = subprocess.run(['docker', *command], input=input_text,
                                capture_output=True, text=True, encoding='utf-8',
                                timeout=20, env=environment)
        if result.returncode != 0:
            # Driver text can carry SQL or credentials; do not return it.
            raise RuntimeError('owned Docker command failed; diagnostics suppressed')
        return result.stdout

    # Inspect narrow fields only; never serialize Config.Env or credentials.
    containers = {}
    for service, internal in [('mysql', '3306/tcp'), ('redis', '6379/tcp')]:
        name = f'{args.project}-{service}-1'
        raw = docker(['inspect', '--format', '{{json .Config.Labels}}|{{json .NetworkSettings.Ports}}|{{.Id}}|{{.State.Running}}', name])
        labels_raw, ports_raw, identifier, running = raw.strip().split('|')
        labels, ports = json.loads(labels_raw), json.loads(ports_raw)
        if labels.get('com.docker.compose.project') != args.project or labels.get('com.docker.compose.service') != service or running != 'true':
            raise RuntimeError('owned container identity mismatch')
        if not any(binding['HostIp'] == '127.0.0.1' and binding['HostPort'] == owned[service] for binding in ports.get(internal, [])):
            raise RuntimeError('owned container loopback port mismatch')
        if not re.fullmatch('[0-9a-f]{64}', identifier):
            raise RuntimeError('invalid immutable container identifier')
        containers[service] = {'name': name, 'id': identifier}

    first = snapshot()
    args.output.mkdir(parents=True, exist_ok=False)
    manifest = {'task': 'GO-073-CAP-04', 'label': args.label, 'project': args.project,
                'api_pid': args.api_pid, 'api_created_unix_seconds': identity,
                'started_at': now(), 'profiles_requested': args.profile,
                'containers': containers, 'sections': [], 'errors': [],
                'limitations': [
                    'one-shot cumulative counters; take separate before/after captures and check resets',
                    'normalized SQL digests include background workers/helper traffic and are capped to the top 100 by cumulative wait',
                    'lock snapshots are transient and an empty snapshot does not prove absence of contention',
                    'no instruments or profiling rates are enabled by this collector',
                    'CPU profiling and metadata queries consume resources; compare with unprofiled stages',
                    'no workload control; a failing collector does not stop or alter the API',
                ]}

    def json_file(name, value):
        with (args.output / name).open('x', encoding='utf-8') as stream:
            json.dump(value, stream, ensure_ascii=False, indent=2)
            stream.write('\n')

    def section(name, action):
        start = time.monotonic()
        entry = {'file': name, 'started_at': now()}
        try:
            value = action()
            if isinstance(value, bytes):
                with (args.output / name).open('xb') as stream:
                    stream.write(value)
                entry['bytes'] = len(value)
            else:
                json_file(name, value)
                if isinstance(value, list):
                    entry['rows'] = len(value)
            entry['available'] = True
        except Exception as error:
            entry['available'] = False
            # Only error class, not raw query/tool diagnostics, is persisted.
            entry['error_class'] = type(error).__name__
            manifest['errors'].append(name)
        entry['elapsed_ms'] = (time.monotonic() - start) * 1000
        entry['finished_at'] = now()
        manifest['sections'].append(entry)

    json_file('diagnostics-before.json', first)
    mysql_env = {**os.environ, 'MYSQL_PWD': password}
    for filename, query in QUERIES.items():
        def collect(statement=query):
            raw = docker(['exec', '-i', '-e', 'MYSQL_PWD', containers['mysql']['id'],
                          'mysql', '--batch', '--raw', '--skip-column-names',
                          '--default-character-set=utf8mb4', f'--user={user}', DATABASE],
                         input_text=statement + ';\n', environment=mysql_env)
            return [json.loads(line) for line in raw.splitlines() if line.strip()]
        section(filename, collect)

    # INFO contains counters/settings, not key values or command argument text.
    for part in ['server', 'memory', 'cpu', 'stats', 'commandstats', 'clients', 'persistence', 'keyspace']:
        def collect_redis(part=part):
            raw = docker(['exec', containers['redis']['id'], 'redis-cli', '--raw', 'INFO', part]).strip()
            if not raw.startswith('# '):
                raise RuntimeError('Redis INFO unavailable')
            return {'at': now(), 'info': raw}
        section(f'redis-{part}.json', collect_redis)

    if args.profile:
        section('cpu-10s.pprof', lambda: fetch('/debug/pprof/profile?seconds=10', maximum=8 << 20, timeout=15))
        section('goroutine.pprof', lambda: fetch('/debug/pprof/goroutine', maximum=8 << 20, timeout=5))
    section('diagnostics-after.json', snapshot)
    manifest['finished_at'] = now()
    manifest['all_sections_available'] = not manifest['errors']
    json_file('manifest.json', manifest)
    print(json.dumps({'label': args.label, 'all_sections_available': not manifest['errors'],
                      'unavailable_sections': manifest['errors'], 'output': str(args.output)}))
    return 0 if not manifest['errors'] else 1


if __name__ == '__main__':
    try:
        raise SystemExit(main())
    except Exception as error:
        # Do not leak subprocess/connection diagnostics at this outer boundary.
        print(json.dumps({'collector_started': False, 'error_class': type(error).__name__}))
        raise SystemExit(1)
