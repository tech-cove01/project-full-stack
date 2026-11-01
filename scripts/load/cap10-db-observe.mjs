// Fixed read-only counters around the native window; not per-request SQL time.
export const statusNames = [
  'Uptime', 'Questions', 'Threads_running', 'Com_begin', 'Com_commit', 'Com_rollback',
  'Innodb_data_fsyncs', 'Innodb_data_pending_fsyncs', 'Innodb_log_waits',
  'Innodb_log_writes', 'Innodb_log_write_requests', 'Innodb_os_log_written',
  'Innodb_os_log_fsyncs', 'Innodb_os_log_pending_fsyncs', 'Innodb_row_lock_waits',
  'Innodb_row_lock_time', 'Innodb_row_lock_current_waits', 'Innodb_buffer_pool_reads',
  'Innodb_buffer_pool_read_requests', 'Innodb_buffer_pool_wait_free',
  'Performance_schema_digest_lost', 'Performance_schema_prepared_statements_lost',
]

export async function collectDatabase(sql) {
  const startedAt = Date.now()
  const names = statusNames.map(name => `'${name}'`).join(',')
  const raw = await sql(`
SELECT JSON_OBJECT('kind','config','flushLogAtCommit',@@global.innodb_flush_log_at_trx_commit,
  'syncBinlog',@@global.sync_binlog,'binlog',@@global.log_bin,'isolation',@@global.transaction_isolation);
SELECT JSON_OBJECT('kind','status','name',VARIABLE_NAME,'value',VARIABLE_VALUE)
  FROM performance_schema.global_status WHERE VARIABLE_NAME IN (${names}) ORDER BY VARIABLE_NAME;
SELECT JSON_OBJECT('kind','wait','name',EVENT_NAME,'count',CAST(COUNT_STAR AS CHAR),
  'totalPicoseconds',CAST(SUM_TIMER_WAIT AS CHAR),'maxPicoseconds',CAST(MAX_TIMER_WAIT AS CHAR))
  FROM performance_schema.events_waits_summary_global_by_event_name
  WHERE EVENT_NAME LIKE 'wait/io/file/innodb/%' OR EVENT_NAME LIKE 'wait/io/file/sql/binlog%'
  ORDER BY EVENT_NAME;
SELECT JSON_OBJECT('kind','instrument','name',NAME,'enabled',ENABLED,'timed',TIMED)
  FROM performance_schema.setup_instruments
  WHERE NAME='transaction' OR NAME LIKE 'wait/io/file/innodb/%' OR NAME LIKE 'wait/io/file/sql/binlog%'
    OR NAME LIKE 'statement/sql/%'
  ORDER BY NAME;
SELECT JSON_OBJECT('kind','consumer','name',NAME,'enabled',ENABLED)
  FROM performance_schema.setup_consumers
  WHERE NAME IN ('global_instrumentation','thread_instrumentation','statements_digest',
    'events_statements_current','events_waits_current','events_transactions_current') ORDER BY NAME;
SELECT JSON_OBJECT('kind','transaction','name',EVENT_NAME,'count',CAST(COUNT_STAR AS CHAR),
  'totalPicoseconds',CAST(SUM_TIMER_WAIT AS CHAR),'maxPicoseconds',CAST(MAX_TIMER_WAIT AS CHAR))
  FROM performance_schema.events_transactions_summary_global_by_event_name ORDER BY EVENT_NAME;
SELECT JSON_OBJECT('kind','digest','digest',DIGEST,'text',DIGEST_TEXT,'count',CAST(COUNT_STAR AS CHAR),
  'totalPicoseconds',CAST(SUM_TIMER_WAIT AS CHAR),'lockPicoseconds',CAST(SUM_LOCK_TIME AS CHAR),
  'rowsExamined',CAST(SUM_ROWS_EXAMINED AS CHAR),'rowsAffected',CAST(SUM_ROWS_AFFECTED AS CHAR))
  FROM performance_schema.events_statements_summary_by_digest
  WHERE SCHEMA_NAME='project_full_stack_go' ORDER BY DIGEST;
SELECT JSON_OBJECT('kind','currentLockWaitRows','count',COUNT(*)) FROM performance_schema.data_lock_waits;
`)
  const rows = raw.split(/\r?\n/).filter(Boolean).map(line => JSON.parse(line))
  const kinds = new Set(['config', 'status', 'wait', 'instrument', 'consumer', 'transaction', 'digest', 'currentLockWaitRows'])
  if (!rows.every(row => kinds.has(row.kind))) throw new Error('unexpected database observation kind')
  const present = new Set(rows.filter(row => row.kind === 'status').map(row => row.name.toLowerCase()))
  if (rows.filter(row => row.kind === 'config').length !== 1 || !present.has('uptime')) {
    throw new Error('required database observation missing')
  }
  return {
    startedAt, endedAt: Date.now(), rows,
    missingStatusNames: statusNames.filter(name => !present.has(name.toLowerCase())),
    scope: 'Sequential read-only global/Performance Schema counters around the native window, including background/observer work. String integer timers are picoseconds; absent or disabled metrics are not zero. Transaction, statement and file-wait envelopes overlap and cannot be added. SUM_LOCK_TIME is not a complete InnoDB row-lock timer; inspect row-lock counters separately. Maxima are cumulative and cannot be differenced. Threads_running, current lock waits and pending fsyncs are endpoint gauges, not cumulative deltas. Instrument and consumer states must be checked before interpreting timing. No request IDs, parameters or SQL values are captured.',
  }
}
