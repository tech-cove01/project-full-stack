// Runs the unmodified public CLI with synthetic files and transport-only fakes.
// No VM source rewriting, database, listener, API, Docker or cloud calls.
import assert from 'node:assert/strict'
import { randomBytes, createHash } from 'node:crypto'
import { mkdir, mkdtemp, readFile, writeFile, unlink, rm, symlink } from 'node:fs/promises'
import { existsSync } from 'node:fs'
import { spawnSync } from 'node:child_process'
import { resolve, dirname, join, basename } from 'node:path'
import { pathToFileURL } from 'node:url'

const repo = resolve('.'), workspace = resolve('..'), evidenceRoot = join(repo, 'docs/evidence/2026-11-01-cap10', ...(process.platform === 'linux' ? ['linux'] : []))
const nonce = randomBytes(6).toString('hex'), privateRoot = join(workspace, '.runtime-fullstack-cap10-' + nonce)
const metadata = join(evidenceRoot, 'environment-' + nonce + '.json')
const sha = raw => createHash('sha256').update(raw).digest('hex')
await mkdir(evidenceRoot, { recursive: true })
const fixtureDir = await mkdtemp(join(evidenceRoot, '.qa-finished-reconcile-'))
await mkdir(privateRoot); await mkdir(join(privateRoot, 'tmp'))
const credentialFile = join(privateRoot, 'tmp', 'closed-cohort', 'input.json')
await mkdir(dirname(credentialFile))
const fixturePath = join(fixtureDir, 'fixture.json'), finishPath = join(fixtureDir, 'finish.json')
const transitionPath = join(repo, 'docs/evidence/2026-11-01-cap10/environment-transition.json')
const transitionSHA256 = process.platform === 'linux' ? sha(await readFile(transitionPath)) : null
const manifest = { project: 'project-full-stack-cap10', baseUrl: 'http://127.0.0.1:8281',
  executionPlatform: process.platform === 'linux' ? 'linux' : 'windows', transitionSHA256,
  status: 'READY', sourceSha: 'a'.repeat(40), expectedRevision: 'capacity-' + 'a'.repeat(40),
  fixture: 'synthetic-finished-fixture', poolSize: 512, users: 512, multiNotes: 512, multiRelations: 512,
  credentialFile, relationshipSeed: { version: 0 },
  userIds: Array.from({ length: 512 }, (_, i) => String(i + 1)),
  multiNoteIds: Array.from({ length: 512 }, (_, i) => String(i + 10001)) }
const manifestBytes = Buffer.from(JSON.stringify(manifest)), manifestSHA256 = sha(manifestBytes)
const receipt = { at: new Date().toISOString(), loggedOut: 512, credentialFileRemoved: true, sqlFixtureRetained: true,
  apiSourceSha: manifest.sourceSha, fixtureSourceSha: manifest.sourceSha, fixtureManifestSHA256: manifestSHA256 }
const receiptBytes = Buffer.from(JSON.stringify(receipt))
await writeFile(fixturePath, manifestBytes); await writeFile(finishPath, receiptBytes)
await writeFile(metadata, JSON.stringify({ resourceProfile: 'baseline' }), { flag: 'wx' })
const preload = join(privateRoot, 'transport-preload.mjs')
await writeFile(preload, String.raw`
import assert from 'node:assert/strict'
import cp from 'node:child_process'
import net from 'node:net'
import { EventEmitter } from 'node:events'
import { PassThrough } from 'node:stream'
import { readFileSync, appendFileSync } from 'node:fs'
import { syncBuiltinESMExports } from 'node:module'
import { resolve } from 'node:path'
import { pathToFileURL } from 'node:url'
const { AUDIT_CONTRACT } = await import(pathToFileURL(resolve('scripts/load/cap10-full-audit.mjs')).href)
const manifest=JSON.parse(readFileSync(process.env.CAP_LIKE_FIXTURE,'utf8'))
const trace=row=>appendFileSync(process.env.CAP10_SYNTHETIC_TRACE,JSON.stringify(row)+'\n')
cp.spawn=(binary,args)=>{
 const child=new EventEmitter();child.stdout=new PassThrough();child.stderr=new PassThrough();child.pid=424242
 child.kill=()=>{throw new Error('unexpected process stop in read-only fixture audit')}
 child.stdin={end(input){queueMicrotask(()=>{
  try{
   let output
   if(binary==='docker'){
    assert.equal(args[0],'inspect');const service=args[1].endsWith('-mysql-1')?'mysql':args[1].endsWith('-redis-1')?'redis':null;assert.ok(service)
    const target=service==='mysql'?'3306/tcp':'6379/tcp',port=service==='mysql'?'3481':'6581'
    output='d'.repeat(64)+'|'+JSON.stringify({'com.docker.compose.project':'project-full-stack-cap10','com.docker.compose.service':service})+'|'+JSON.stringify({[target]:[{HostIp:'127.0.0.1',HostPort:port}]})+'|true\n'
    trace({kind:'docker-inspect',service})
   }else{
    assert.equal(binary,'synthetic-readonly-mysql');assert.ok(String(input).includes('READ ONLY'))
    assert.ok(!/\b(INSERT|UPDATE|DELETE|DROP|ALTER|TRUNCATE|CREATE|REPLACE)\b/i.test(String(input)))
    for(const id of manifest.multiNoteIds)assert.ok(String(input).includes("'"+id+"'"))
    let rows=manifest.userIds.map((userId,i)=>({_kind:'fact',noteId:manifest.multiNoteIds[i],userId,active:0,version:0}))
    if(process.env.CAP10_SYNTHETIC_BAD_FACT==='1')rows[0].active=1
    rows.push(...manifest.multiNoteIds.map(noteId=>({_kind:'note',noteId,liked:0,activeRelations:0})),
     {_kind:'control-projection',projection:'follow-v1',rebuiltAt:null},{_kind:'control-projection',projection:'feed-v1',rebuiltAt:null},
     {_kind:'control-search',name:'search-v1',indexAlias:'',activeGeneration:'',rebuiltAt:null},
     {_kind:'watermark',maxOutboxId:'0'},{_kind:'audit',...AUDIT_CONTRACT})
    output=rows.map(row=>JSON.stringify(row)).join('\n')+'\n';trace({kind:'sql',readOnly:true,notes:manifest.multiNoteIds.length})
   }
   child.stdout.end(output);child.stderr.end();child.emit('close',0)
  }catch(error){child.stdout.end();child.stderr.end(error.message);child.emit('close',1)}
 })}}
 return child
}
globalThis.fetch=async(url,options)=>{
 assert.equal(url,'http://127.0.0.1:8281/health/ready');assert.equal(options.method,'GET');assert.equal(options.headers.authorization,undefined)
 trace({kind:'health',authenticated:false});return{status:200,json:async()=>({status:'UP',service:'project-full-stack-go',version:process.env.CAP_READ_EXPECTED_REVISION})}
}
net.createConnection=options=>{
 assert.deepEqual(options,{host:'127.0.0.1',port:6581});const socket=new EventEmitter();socket.setTimeout=()=>socket
 socket.write=input=>{
  const lines=String(input).split('\r\n');let index=0,commands=0
  while(lines[index]){const n=Number(lines[index++].slice(1)),parts=[];assert.ok(Number.isSafeInteger(n)&&n>=1)
   for(let i=0;i<n;i++){const len=Number(lines[index++].slice(1)),value=lines[index++];assert.equal(Buffer.byteLength(value),len);parts.push(value)}
   assert.ok(['SMEMBERS','HGETALL'].includes(parts[0]));assert.equal(parts.length,2);commands++}
  trace({kind:'redis',readOnly:true,commands});queueMicrotask(()=>socket.emit('data',Buffer.from('*0\r\n'.repeat(commands))))
 }
 socket.end=()=>queueMicrotask(()=>socket.emit('end'));socket.destroy=error=>{throw error}
 queueMicrotask(()=>socket.emit('connect'));return socket
}
syncBuiltinESMExports()
`)
let checks = 0, runNumber = 0
const env = { ...process.env, CAP_COHORT: 'cap10', MYSQL_HOST: '127.0.0.1', MYSQL_PORT: '3481',
  MYSQL_DATABASE: 'project_full_stack_go', MYSQL_USER: 'synthetic-only', MYSQL_PASSWORD: 'synthetic-only',
  MYSQL_BIN: 'synthetic-readonly-mysql', CAP_READ_OBSERVER_USER: 'synthetic-observer', CAP_READ_OBSERVER_PASSWORD: 'synthetic-only',
  REDIS_HOST: '127.0.0.1', REDIS_PORT: '6581', API_BASE: 'http://127.0.0.1:8281',
  CAP_SOURCE_SHA: manifest.sourceSha, CAP_READ_EXPECTED_REVISION: manifest.expectedRevision,
  CAP10_PLATFORM: manifest.executionPlatform, CAP10_EVIDENCE: evidenceRoot, CAP10_ENVIRONMENT_METADATA: metadata,
  CAP10_RESOURCE_PROFILE: 'baseline', CAP10_RUNTIME: privateRoot, CAP_LIKE_FIXTURE: fixturePath,
  CAP10_OBSERVER_MODE: 'like-vector-v1', TEMP: join(privateRoot, 'tmp'), TMP: join(privateRoot, 'tmp'), TMPDIR: join(privateRoot, 'tmp') }
if (transitionSHA256) env.CAP10_TRANSITION_SHA256 = transitionSHA256
async function invoke(action = 'reconcile', args = ['--finished-fixture-sha256', manifestSHA256], extra = {}) {
  const label = String(++runNumber), trace = join(privateRoot, 'trace-' + label + '.ndjson'), output = join(fixtureDir, 'result-' + label)
  const result = spawnSync(process.execPath, ['--import', pathToFileURL(preload).href, 'scripts/cap10-like.mjs', action, ...args],
    { cwd: repo, env: { ...env, ...extra, CAP10_SYNTHETIC_TRACE: trace, CAP_LIKE_EVIDENCE: output }, encoding: 'utf8', timeout: 15000 })
  assert.equal(result.error, undefined)
  const calls = existsSync(trace) ? (await readFile(trace, 'utf8')).trim().split('\n').filter(Boolean).map(JSON.parse) : []
  return { ...result, calls, output }
}
async function rejectBeforeAudit(label, action, args) {
  const result = await invoke(action, args)
  assert.notEqual(result.status, 0, label)
  assert.ok(result.calls.every(row => ['health', 'docker-inspect'].includes(row.kind)), label)
  assert.equal(existsSync(join(result.output, 'reconcile.json')), false)
  checks++
}
try {
  const accepted = await invoke()
  assert.equal(accepted.status, 0, accepted.stderr)
  const report = JSON.parse(await readFile(join(accepted.output, 'reconcile.json')))
  assert.equal(report.valid, true); assert.equal(report.facts.facts.length, 512)
  assert.equal(report.facts.observerPolicy.fullAuditTimeoutMs, 120000)
  assert.equal(report.finishedFixtureAudit.fixtureManifestSHA256, manifestSHA256)
  assert.equal(report.finishedFixtureAudit.finish.sha256, sha(receiptBytes))
  assert.equal(report.finishedFixtureAudit.tokenAuthenticationAttempted, false)
  assert.equal(report.finishedFixtureAudit.capacityEligible, false)
  assert.ok(accepted.calls.some(row => row.kind === 'sql' && row.readOnly))
  assert.ok(accepted.calls.some(row => row.kind === 'redis' && row.readOnly))
  assert.equal(existsSync(credentialFile), false); checks++
  await rejectBeforeAudit('default loader still requires the private input', 'reconcile', [])
  await rejectBeforeAudit('wrong manifest bytes cannot use closed receipt', 'reconcile', ['--finished-fixture-sha256', '0'.repeat(64)])
  for (const action of ['prepare', 'run', 'probe', 'inspect', 'finish']) await rejectBeforeAudit('closed mode cannot enter ' + action, action)
  for (const change of [{ fixtureManifestSHA256: '0'.repeat(64) }, { loggedOut: 511 }, { loggedOut: '512' },
    { credentialFileRemoved: false }, { sqlFixtureRetained: false }]) {
    await writeFile(finishPath, JSON.stringify({ ...receipt, ...change }))
    await rejectBeforeAudit('invalid exact finish receipt ' + Object.keys(change)[0], 'reconcile')
  }
  await writeFile(finishPath, receiptBytes)
  await writeFile(credentialFile, 'SYNTHETIC-SECRET-MUST-NOT-BE-READ')
  const present = await invoke(); assert.notEqual(present.status, 0)
  assert.equal((present.stdout + present.stderr).includes('SYNTHETIC-SECRET-MUST-NOT-BE-READ'), false)
  assert.ok(!present.calls.some(row => row.kind === 'sql')); checks++
  await unlink(credentialFile)
  const alias = { ...manifest, userIds: [...manifest.userIds] }; alias.userIds[511] = alias.userIds[0]
  const aliasBytes = Buffer.from(JSON.stringify(alias)), aliasSHA = sha(aliasBytes)
  await writeFile(fixturePath, aliasBytes); await writeFile(finishPath, JSON.stringify({ ...receipt, fixtureManifestSHA256: aliasSHA }))
  await rejectBeforeAudit('resealed duplicate relation identity cannot pass', 'reconcile', ['--finished-fixture-sha256', aliasSHA])
  await writeFile(fixturePath, manifestBytes); await writeFile(finishPath, receiptBytes)
  await symlink(join(dirname(credentialFile), 'absent-target'), credentialFile, 'file')
  await rejectBeforeAudit('dangling credential symlink is still an existing input', 'reconcile')
  await unlink(credentialFile)
  await unlink(finishPath)
  const alternateReceipt = join(fixtureDir, 'original-finish.json'); await writeFile(alternateReceipt, receiptBytes)
  await symlink(alternateReceipt, finishPath, 'file')
  await rejectBeforeAudit('finish receipt symlink cannot substitute the original regular receipt', 'reconcile')
  await unlink(finishPath); await writeFile(finishPath, receiptBytes)
  const invalid = await invoke('reconcile', undefined, { CAP10_SYNTHETIC_BAD_FACT: '1' })
  assert.notEqual(invalid.status, 0)
  const failed = JSON.parse(await readFile(join(invalid.output, 'reconcile.json')))
  assert.equal(failed.valid, false); assert.equal(failed.factsValid, false)
  assert.equal(failed.finishedFixtureAudit.contract, 'finished-fixture-read-only-v1'); checks++
  assert.deepEqual(await readFile(fixturePath), manifestBytes)
  assert.equal(existsSync(credentialFile), false)
  console.log(JSON.stringify({ passed: true, checks, scope: 'Actual unchanged public CLI, synthetic files and transport-only fakes; no VM source rewrite, services, SQL server, cloud or capacity window',
    originalManifestBytesRetained: true, tokenRestored: false, fullAuditBudgetMs: 120000 }))
} finally {
  assert.equal(dirname(privateRoot), workspace); assert.match(basename(privateRoot), /^\.runtime-fullstack-cap10-[a-f0-9]{12}$/)
  assert.equal(dirname(fixtureDir), evidenceRoot); assert.ok(basename(fixtureDir).startsWith('.qa-finished-reconcile-'))
  await rm(fixtureDir, { recursive: true, force: true })
  await rm(privateRoot, { recursive: true, force: true })
  await unlink(metadata)
}
