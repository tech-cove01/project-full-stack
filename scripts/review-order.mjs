#!/usr/bin/env node
/*
 * Controlled replay for accepted orders whose DB outcome stayed unknown.
 *
 * The consumer only acknowledges such a message after writing tb_order_review (see
 * OrderReviewRepository), so this record is the durable source for recovery.
 *
 * Usage (PowerShell, MySQL on 3307):
 *   $env:MYSQL_PORT='3307'
 *   node scripts/review-order.mjs list
 *   node scripts/review-order.mjs replay 652313753106251777 --confirm
 *
 * Replay re-appends the original message to stream.orders. The consumer is idempotent: if the
 * order actually committed, it reports the existing row instead of creating a second one.
 */
import { spawnSync } from 'node:child_process'

const host = process.env.MYSQL_HOST || '127.0.0.1'
const port = process.env.MYSQL_PORT || '3306'
const database = process.env.MYSQL_DATABASE || 'project_full_stack'
const user = process.env.MYSQL_USER || 'fullstack'
const password = process.env.MYSQL_PASSWORD || 'fullstack_dev_only'
const mysql = process.env.MYSQL_BIN || 'mysql'
const redisContainer = process.env.REDIS_CONTAINER || 'project-full-stack-redis-1'
const stream = process.env.ORDER_STREAM || 'stream.orders'
const base = ['--protocol=TCP', '--host=' + host, '--port=' + port, '--user=' + user,
  '--default-character-set=utf8mb4']
const env = { ...process.env, MYSQL_PWD: password }

function query(sql) {
  const result = spawnSync(mysql, [...base, '--batch', '--skip-column-names', database, '-e', sql],
    { env, encoding: 'utf8' })
  if (result.error) throw result.error
  if (result.status !== 0) throw new Error(result.stderr || 'query failed')
  return result.stdout.trim()
}

function exec(sql) {
  const result = spawnSync(mysql, [...base, database, '-e', sql], { env, encoding: 'utf8' })
  if (result.error) throw result.error
  if (result.status !== 0) throw new Error(result.stderr || 'update failed')
}

function list() {
  const rows = query(`SELECT order_id, voucher_id, user_id, attempts, status, reason
                      FROM tb_order_review ORDER BY created_at DESC LIMIT 50`)
  if (!rows) {
    console.log('no review records')
    return
  }
  console.log('order_id\tvoucher\tuser\tattempts\tstatus\treason')
  for (const line of rows.split('\n')) console.log(line.replace(/\t/g, '\t'))
}

function replay(orderId) {
  if (!/^\d+$/.test(orderId)) throw new Error('order id must be numeric')
  const row = query(`SELECT voucher_id, user_id, status FROM tb_order_review
                     WHERE order_id = '${orderId}'`)
  if (!row) throw new Error('no review record for order ' + orderId)
  const [voucherId, userId, status] = row.split('\t')
  if (status !== 'OPEN') throw new Error('review record is ' + status + ', only OPEN can be replayed')

  // Claim before publishing. The consumer can only react after XADD, so a failure it records
  // afterwards (REQUEUED -> OPEN) must not be overwritten by a late CLI update.
  const claimed = Number(query(`UPDATE tb_order_review
      SET status = 'REQUEUED', resolved_at = NULL, updated_at = NOW(3)
      WHERE order_id = '${orderId}' AND status = 'OPEN'; SELECT ROW_COUNT();`).trim())
  if (claimed !== 1) throw new Error('review row could not be claimed; it may have changed meanwhile')

  const added = spawnSync('docker',
    ['exec', redisContainer, 'redis-cli', 'XADD', stream, '*',
      'id', orderId, 'userId', userId, 'voucherId', voucherId],
    { encoding: 'utf8' })
  if (added.error || added.status !== 0) {
    // Nothing was published: put the row back so the operator can retry.
    exec(`UPDATE tb_order_review SET status = 'OPEN', updated_at = NOW(3)
          WHERE order_id = '${orderId}' AND status = 'REQUEUED'`)
    throw new Error(added.error ? String(added.error) : (added.stderr || 'XADD failed'))
  }
  console.log('requeued ' + orderId + ' as stream entry ' + added.stdout.trim())
  console.log('the consumer now decides: existing row -> reports it, unknown -> retries creation')
}

const [command, orderId, flag] = process.argv.slice(2)
try {
  if (command === 'list') {
    list()
  } else if (command === 'replay') {
    if (flag !== '--confirm') throw new Error('replay requires the --confirm flag')
    replay(orderId)
  } else {
    console.log('usage: node scripts/review-order.mjs list | replay <orderId> --confirm')
    process.exitCode = 1
  }
} catch (error) {
  console.error('error: ' + error.message)
  process.exitCode = 1
}
