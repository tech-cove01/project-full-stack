#!/usr/bin/env node
/* Versioned MySQL migration runner. Keeps DDL out of application startup. */
import { createHash } from 'node:crypto'
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, basename } from 'node:path'
import { spawnSync } from 'node:child_process'

const host = process.env.MYSQL_HOST || '127.0.0.1'
const port = process.env.MYSQL_PORT || '3306'
const database = process.env.MYSQL_DATABASE || 'project_full_stack'
const user = process.env.MYSQL_USER || 'fullstack'
const password = process.env.MYSQL_PASSWORD || 'fullstack_dev_only'
const mysql = process.env.MYSQL_BIN || 'mysql'
const dir = process.env.MIGRATIONS_DIR || 'infra/mysql/migrations'
const env = { ...process.env, MYSQL_PWD: password }
const base = ['--protocol=TCP', '--host=' + host, '--port=' + port, '--user=' + user, '--default-character-set=utf8mb4']

function run(args, input) {
  const result = spawnSync(mysql, args, { input, env, stdio: ['pipe', 'inherit', 'inherit'] })
  if (result.error) throw result.error
  if (result.status !== 0) throw new Error('mysql exited with ' + result.status)
}
function query(sql) {
  const result = spawnSync(mysql, [...base, '--batch', '--skip-column-names', database, '-e', sql], { env, encoding: 'utf8' })
  if (result.status !== 0) throw new Error(result.stderr || 'query failed')
  return result.stdout
}

run(['--protocol=TCP', '--host=' + host, '--port=' + port, '--user=' + user, '-e',
  'CREATE DATABASE IF NOT EXISTS `' + database + '` CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci'])
run([...base, database, '-e', `CREATE TABLE IF NOT EXISTS tb_schema_migration (
  version VARCHAR(64) NOT NULL PRIMARY KEY,
  checksum CHAR(64) NOT NULL,
  applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`])

const applied = new Map()
for (const line of query('SELECT version, checksum FROM tb_schema_migration').trim().split('\n')) {
  if (!line) continue
  const [version, checksum] = line.split('\t')
  applied.set(version, checksum)
}

const files = readdirSync(dir)
  .filter(name => name.endsWith('.sql') && statSync(join(dir, name)).isFile())
  .sort()

let changed = false
for (const file of files) {
  const version = basename(file, '.sql')
  const content = readFileSync(join(dir, file))
  const checksum = createHash('sha256').update(content).digest('hex')
  if (applied.has(version)) {
    if (applied.get(version) !== checksum) throw new Error('checksum mismatch for applied migration ' + version)
    console.log('skip  ' + version)
    continue
  }
  console.log('apply ' + version)
  run([...base, database], content)
  run([...base, database, '-e', `INSERT INTO tb_schema_migration(version, checksum) VALUES ('${version}', '${checksum}')`])
  changed = true
}
console.log(changed ? 'migrations applied' : 'database is up to date')
