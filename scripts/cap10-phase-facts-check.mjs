// Four public synthetic goldens were exported by original Go df0 pure functions; no native/private data.
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { createHash } from 'node:crypto'
import vm from 'node:vm'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { PHASE_FACTS, DIGEST, goJSON, vectorIdentity, controlsIdentity, phaseSQL, phaseErrors, makePhaseFacts, acceptedSample, recoveryEvidence } from './load/cap10-phase-facts.mjs'
import { auditSQL } from './load/cap10-full-audit.mjs'
import { nativeScenarioWindow } from './load/cap10-native-window.mjs'
const here = dirname(fileURLToPath(import.meta.url)), sha = raw => createHash('sha256').update(raw).digest('hex')
let checks = 0
async function check(name, fn) { await fn(); checks++; console.log('PASS ' + name) }
const goldenRaw = await readFile(join(here, 'load/fixtures/cap10-phase-facts-go-goldens.json'))
assert.equal(sha(goldenRaw), 'bd1df87197151b84ac10c8941d70b95288c08f02addf08461264e156ff71ac56')
const pack = JSON.parse(goldenRaw)
await check('exact original-Go4case canonical byte/hash goldens including Unicode HTML and same-total swaps', () => {
 for (const value of pack.cases) {
  const manifest = { multiNoteIds: value.facts.map(row => row.noteId), userIds: value.facts.map(row => row.userId) }
  const vector = vectorIdentity(manifest, value.facts), controls = controlsIdentity(value.controlRows)
  assert.equal(vector.canonical, value.factCanonicalJSON); assert.equal(vector.sha256, value.factVectorSHA256)
  assert.deepEqual(controls.canonical, value.controlCanonicalJSON); assert.deepEqual(controls.inner, value.controlInnerSHA256)
  assert.equal(controls.outer, value.controlsCanonicalJSON); assert.equal(controls.sha256, value.controlsSHA256)
 }
 assert.notEqual(pack.cases[0].factVectorSHA256, pack.cases[1].factVectorSHA256)
 assert.throws(()=>goJSON({field:'\ud800'}),/unpaired surrogate/)
})
const manifest = { multiNoteIds: ['101', '102'], userIds: ['1', '2'] }, fixtureSHA = 'f'.repeat(64)
const controls = pack.cases[0].controlRows
function rawRows() { return [
 {_kind:'fact',noteId:'101',userId:'1',version:4,active:0}, {_kind:'fact',noteId:'102',userId:'2',version:0,active:0},
 ...manifest.multiNoteIds.map(noteId=>({_kind:'note',noteId,liked:0,activeRelations:0})),
 ...controls.projections.map(row=>({_kind:'control-projection',...row})), {_kind:'control-search',...controls.search} ] }
function capture() {return makePhaseFacts(manifest,rawRows(),{label:'synthetic-only',startedAt:1,endedAt:2,fixtureSHA,expectedControls:controls})}
await check('phase-only RR query contains exactly fact/note/control reads and never source history or inferred eventCount',()=>{
 const query=phaseSQL(manifest);assert.match(query,/WITH CONSISTENT SNAPSHOT, READ ONLY/)
 assert.equal((query.match(/COMMIT;/g)||[]).length,1)
 for(const name of ['tb_community_outbox','tb_community_projection_event','SHA2','MAX(id)'])assert.equal(query.includes(name),false)
 const state=capture();assert.equal(state.captureKind,PHASE_FACTS);assert.equal(state.fullAuditPerformed,false)
 assert.equal(state.transitions,4);for(const field of ['eventCount','events','receipts','delivery'])assert.equal(Object.hasOwn(state,field),false)
 assert.deepEqual(phaseErrors(manifest,state,fixtureSHA,controls),[])
 const normalize=value=>value.replace(/\s+/g,' ').trim()
 const queries=value=>value.split(';').filter(part=>part.includes('SELECT JSON_OBJECT')).map(normalize)
 const original=queries(auditSQL(manifest.multiNoteIds)),light=queries(query)
 for(const kind of ['fact','note','control-projection','control-search'])assert.equal(light.find(part=>part.includes("'_kind','"+kind+"'")),original.find(part=>part.includes("'_kind','"+kind+"'")))
})
await check('exact pair version note-count and control validation cannot hide behind same totals or aliases',()=>{
 for(const mutate of [rows=>{rows[0].userId='9'},rows=>{rows[0].active=1},rows=>{rows[0].version=2.5},rows=>{rows[1].noteId='101'},
  rows=>{rows[2].liked=1},rows=>{rows[4].projection+=' '},rows=>{rows[5].rebuiltAt='2026-02-30 12:30:01.123000'}]){
  const rows=rawRows();mutate(rows);assert.throws(()=>makePhaseFacts(manifest,rows,{fixtureSHA,expectedControls:controls}))
 }
 const state=capture();state.eventCount=4;assert.ok(phaseErrors(manifest,state,fixtureSHA,controls).length)
})
const end=Date.UTC(2026,10,3,0,0,0), baselineRef={path:'baseline/reconcile.json',sha256:'a'.repeat(64)}, E=2
function sample(state=capture(),start=end+1000){return {at:new Date(start).toISOString(),durationMs:20,errors:[],semanticViolations:[],vectorSql:{SQLCommitted:true},
 sql:{startedAt:new Date(start).toISOString(),finishedAt:new Date(start+10).toISOString(),digestContract:DIGEST,factVectorSHA256:state.factVectorSHA256,controlsSHA256:state.controlsSHA256,
  counts:{events:2,pending:0,processing:0,done:2,needsReview:0,other:0},parked:0,receipts:['follow-v1','feed-v1','search-v1'].map(projection=>({projection,ignored:2,missing:0,applied:0}))},
 kafka:{finishedAt:new Date(start+20).toISOString(),groups:['follow-v1','feed-v1','search-v1'].map(name=>({group:'community.'+name,initialized:true,lag:0}))}}}
const line=value=>Buffer.from(JSON.stringify(value)+'\n')
await check('complete accepted sample binds exact original line bytes and both same-RR digests',()=>{
 const row=sample(),raw=line(row),proof=acceptedSample(raw,capture(),{baselineEvents:E,notBeforeMs:end})
 assert.equal(proof.lineSHA256,sha(raw));assert.equal(proof.offsetBytes,0);assert.equal(proof.lengthBytes,raw.length);assert.equal(proof.stageEvents,2)
 const prefixed=Buffer.concat([line({...row,errors:['earlier incomplete']}),raw,Buffer.from('{"unfinished":')])
 assert.equal(acceptedSample(prefixed,capture(),{baselineEvents:E,notBeforeMs:end}).offsetBytes,prefixed.length-raw.length-Buffer.byteLength('{"unfinished":'))
 assert.equal(acceptedSample(raw.subarray(0,-1),capture(),{baselineEvents:E,notBeforeMs:end}),null)
})
await check('failed unaccepted stale-digest dirty queues duplicate Kafka/receipt groups never establish setup',()=>{
 for(const mutate of [row=>{delete row.sql.digestContract},row=>{row.sql.factVectorSHA256=pack.cases[1].factVectorSHA256},
 row=>{row.vectorSql.SQLCommitted=false},
 row=>{row.sql.controlsSHA256='b'.repeat(64)},row=>{row.errors=['SQL failed']},row=>{row.semanticViolations=['bad']},
 row=>{row.sql.counts.pending=1},row=>{row.sql.counts.events=3},row=>{row.sql.receipts[1]=row.sql.receipts[0]},
 row=>{row.kafka.groups[1]=row.kafka.groups[0]},row=>{row.kafka.groups[0].initialized=false},row=>{row.kafka.groups[0].lag=1},
 row=>{row.durationMs=4000.1},row=>{row.sql.startedAt=new Date(end-1).toISOString()}]){
  const row=sample();mutate(row);assert.equal(acceptedSample(line(row),capture(),{baselineEvents:E,notBeforeMs:end}),null)
 }
})
function summary(){return {collectionValid:true,allSamplesComplete:true,failedSamples:0,semanticViolationCount:0,valid:true,baselineAudit:baselineRef,baselineAuditEventCount:E,baselineCountSource:'bound-node-full-reconciliation'}}
await check('native recovery remains60s independently of final audit or delayed observer summary',()=>{
 const raw=line(sample()),value=recoveryEvidence(summary(),raw,end+.25,baselineRef,E)
 assert.equal(value.nativeRecoveryVerified,true);assert.equal(value.recovered.recoveryMsFromNativeEnd,1019.75)
 assert.equal(recoveryEvidence(summary(),line(sample(capture(),end+60001)),end,baselineRef,E).nativeRecoveryVerified,false)
 assert.equal(recoveryEvidence({...summary(),collectionValid:false,failedSamples:1},raw,end,baselineRef,E).nativeRecoveryVerified,false)
 assert.equal(recoveryEvidence(summary(),raw,null,baselineRef,E).nativeRecoveryVerified,false)
 assert.throws(()=>recoveryEvidence({...summary(),baselineAudit:{...baselineRef,sha256:'0'.repeat(64)}},raw,end,baselineRef,E))
 const edge=sample(capture(),end+59990)
 edge.sql.finishedAt='2026-11-03T00:00:59.999998999Z';edge.kafka.finishedAt='2026-11-03T00:00:59.999999999Z'
 assert.equal(recoveryEvidence(summary(),line(edge),end,baselineRef,E).nativeRecoveryVerified,true)
 edge.kafka.finishedAt='2026-11-03T00:01:00.000000001Z'
 assert.equal(recoveryEvidence(summary(),line(edge),end,baselineRef,E).nativeRecoveryVerified,false)
 const before=sample();before.sql.startedAt='2026-11-02T23:59:59.999999999Z'
 assert.equal(recoveryEvidence(summary(),line(before),end,baselineRef,E).nativeRecoveryVerified,false)
})
const actual=await readFile(join(here,'cap10-like.mjs'),'utf8')
await check('legacy complete SQL literal and normalization transaction/Lua literals are unchanged',()=>{
 const extract=s=>s.match(/const legacyStatement = (`[^]*?COMMIT;`)/)[1]
 assert.equal(sha(extract(actual)),'c8d0ff84604c7523fcaee21e1cd55440d1d82ad57f8731db46d35302aa7329cb')
 const tx=s=>s.match(/const raw = rows\(await sql\((`SET TRANSACTION ISOLATION LEVEL READ COMMITTED;[^]*?COMMIT;`)/)[1]
 assert.equal(sha(tx(actual)),'00255bbab5ffdac393be3ccb8687e14b1a29a5e13de983f30e75bb9a602baefb');assert.equal(actual.includes("finalFullAudit('after-native-recovery-evidence'"),true)
})
await check('actual warm/prestart failure final-audit path never waits stable recovery and cannot retry failed SQL',async()=>{
 const raw=actual.match(/^    async function finalFullAudit[^]*?^    }/m)[0]
 const call=[]
 const context={report:{},windowBaseline:{ref:baselineRef},Date,assert,dir:'/virtual',join:(...args)=>args.join('/'),
  save:async()=>{call.push('marker')},waitObserverRecovery:async()=>{call.push('wait');throw Error('not allowed')},reconcile:async()=>{call.push('full');throw Error('SQL timeout')},manifest,mode:'multi'}
 vm.runInNewContext('let finalFullAuditAttempted=false,stableControllerStarted=false;'+raw+';globalThis.once=finalFullAudit',context)
 await assert.rejects(context.once('failed-warm-once'),/SQL timeout/);await assert.rejects(context.once('again'),/one final full-audit attempt/)
 assert.deepEqual(call,['marker','full'])
 const intent={...context,report:{stableStartedAt:1},waitObserverRecovery:async()=>{throw Error('prestart must not wait')}}
 vm.runInNewContext('let finalFullAuditAttempted=false,stableControllerStarted=false;'+raw+';globalThis.once=finalFullAudit',intent)
 await assert.rejects(intent.once('privateinput-failed'),/SQL timeout/)
 assert.equal(intent.report.nativeRecoveryEvidence.nativeRecoveryVerified,false)
 const stable={...context,report:{stableStartedAt:1,nativeScenario:{complete:true,scheduledEndedAt:end}},waitObserverRecovery:async()=>{throw Error('summary absent')}}
 vm.runInNewContext('let finalFullAuditAttempted=false,stableControllerStarted=true;'+raw+';globalThis.once=finalFullAudit',stable)
 await assert.rejects(stable.once('stable',end),/summary absent/);await assert.rejects(stable.once('retry',end),/one final full-audit attempt/)
})
await check('actual onStart proves positive PID only after both original metadata writes succeed',async()=>{
 const start=actual.indexOf('onStart(pid) {'),finish=actual.indexOf('\n        } })',start)
 assert.ok(start>0&&finish>start)
 const body=actual.slice(start,finish)+'\n        }'
 for(const [pid,fail,expected]of [[123,false,true],[undefined,false,false],[0,false,false],[123,true,false]]){
  const context={vectorMode:true,phase:'stable',pts:true,process:{pid:900},Date,dir:'/virtual',json:JSON.stringify,join:(...v)=>v.join('/'),
   save:async()=>{if(fail)throw Error('metadata failed')},writeFile:async()=>{}}
  vm.runInNewContext('let stableControllerStarted=false;const hooks={'+body+'};globalThis.start=hooks.onStart;globalThis.started=()=>stableControllerStarted',context)
  if(fail)await assert.rejects(context.start(pid),/metadata failed/);else await context.start(pid)
  assert.equal(context.started(),expected)
 }
})
await check('actual early vector scenario uses original PTS or k6 window validator before final recovery wait',()=>{
 const begin=actual.indexOf('        vectorScenario = '),endAt=actual.indexOf('\n        report.nativeScenario = vectorScenario',begin)
 const assignment=actual.slice(begin,endAt)
 const stamp=end+1000,metrics={cap_native_scenario_start_unix_ms:{min:stamp,max:stamp},cap_native_scenario_duration_ms:{min:60000,max:60000}}
 for(const backend of ['pts','k6']){
  const ptsScenario={complete:true,scheduledEndedAt:stamp+60000}
  const context={process:{env:{CAP10_LOAD_BACKEND:backend}},vectorNative:{metrics,...(backend==='pts'?{nativeScenario:ptsScenario}:{})},nativeScenarioWindow,seconds:60,
   report:{stableWrapperStartedAt:stamp-100,stableWrapperEndedAt:stamp+60100},stable:{code:0,externalStop:null}}
  vm.runInNewContext('let vectorScenario;'+assignment+';globalThis.scenario=vectorScenario',context)
  assert.equal(context.scenario.complete,true);assert.equal(context.scenario.scheduledEndedAt,stamp+60000)
 }
})
console.log(JSON.stringify({checks,failed:0,originalGoGoldens:pack.cases.length,sqlExecuted:false,nativeExecuted:false,publicModified:false}))
