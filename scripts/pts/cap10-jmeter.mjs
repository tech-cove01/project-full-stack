// Offline PTS bundle generation only. This module does not start JMeter or HTTP.
import assert from 'node:assert/strict'
import { readFile, writeFile, mkdir } from 'node:fs/promises'
import { resolve, dirname, join, basename } from 'node:path'
import { createHash } from 'node:crypto'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const xml = text => String(text).replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;').replaceAll('"', '&quot;')
const sha = bytes => createHash('sha256').update(bytes).digest('hex')
export async function bundle(directory, { collectorUrl, controlToken } = {}) {
  const contract = await readFile(join(here, 'cap10-pts-contract.groovy'), 'utf8')
  const runtime = await readFile(join(here, 'cap10-jmeter.groovy'), 'utf8')
  const script = contract + '\n' + runtime, scriptSHA256 = sha(script)
  const jsr = (action, processor = false) => {
    const type = processor === 'pre' ? 'JSR223PreProcessor' : processor ? 'JSR223PostProcessor' : 'JSR223Sampler'
    return `<${type} guiclass="TestBeanGUI" testclass="${type}" testname="CAP10 ${action}" enabled="true"><stringProp name="scriptLanguage">groovy</stringProp><stringProp name="parameters">${action}</stringProp><stringProp name="filename"></stringProp><stringProp name="cacheKey">true</stringProp><stringProp name="script">${xml(script)}</stringProp></${type}>`
  }
  const thread = (type, name, count, body) => `<${type} guiclass="${type === 'ThreadGroup' ? 'ThreadGroupGui' : 'PostThreadGroupGui'}" testclass="${type}" testname="${name}" enabled="true"><stringProp name="ThreadGroup.on_sample_error">continue</stringProp><elementProp name="ThreadGroup.main_controller" elementType="LoopController" guiclass="LoopControlPanel" testclass="LoopController" testname="one outer iteration"><boolProp name="LoopController.continue_forever">false</boolProp><stringProp name="LoopController.loops">1</stringProp></elementProp><stringProp name="ThreadGroup.num_threads">${count}</stringProp><stringProp name="ThreadGroup.ramp_time">0</stringProp><boolProp name="ThreadGroup.scheduler">false</boolProp></${type}><hashTree>${body}</hashTree>`
  const csv = `<CSVDataSet guiclass="TestBeanGUI" testclass="CSVDataSet" testname="Exclusive slot, once per outer iteration" enabled="true"><stringProp name="filename">slots.csv</stringProp><stringProp name="fileEncoding">UTF-8</stringProp><stringProp name="variableNames"></stringProp><stringProp name="delimiter">,</stringProp><boolProp name="quotedData">false</boolProp><boolProp name="recycle">false</boolProp><boolProp name="stopThread">true</boolProp><stringProp name="shareMode">shareMode.all</stringProp><boolProp name="ignoreFirstLine">false</boolProp></CSVDataSet><hashTree/>`
  const headers = `<HeaderManager guiclass="HeaderPanel" testclass="HeaderManager" testname="Private headers" enabled="true"><collectionProp name="HeaderManager.headers"><elementProp name="" elementType="Header"><stringProp name="Header.name">authorization</stringProp><stringProp name="Header.value">\${cap10.token}</stringProp></elementProp><elementProp name="" elementType="Header"><stringProp name="Header.name">Content-Type</stringProp><stringProp name="Header.value">application/json</stringProp></elementProp></collectionProp></HeaderManager><hashTree/>`
  const http = `<HTTPSamplerProxy guiclass="HttpTestSampleGui" testclass="HTTPSamplerProxy" testname="CAP10 like-write" enabled="true"><elementProp name="HTTPsampler.Arguments" elementType="Arguments"><collectionProp name="Arguments.arguments"><elementProp name="" elementType="HTTPArgument"><boolProp name="HTTPArgument.always_encode">false</boolProp><stringProp name="Argument.value">{"liked":\${cap10.wanted}}</stringProp><stringProp name="Argument.metadata">=</stringProp></elementProp></collectionProp></elementProp><stringProp name="HTTPSampler.domain">\${cap10.apiHost}</stringProp><stringProp name="HTTPSampler.port">\${cap10.apiPort}</stringProp><stringProp name="HTTPSampler.protocol">http</stringProp><stringProp name="HTTPSampler.contentEncoding">UTF-8</stringProp><stringProp name="HTTPSampler.path">/notes/\${cap10.noteId}/like</stringProp><stringProp name="HTTPSampler.method">PUT</stringProp><boolProp name="HTTPSampler.follow_redirects">false</boolProp><boolProp name="HTTPSampler.auto_redirects">false</boolProp><boolProp name="HTTPSampler.use_keepalive">true</boolProp><boolProp name="HTTPSampler.postBodyRaw">true</boolProp><stringProp name="HTTPSampler.connect_timeout">5000</stringProp><stringProp name="HTTPSampler.response_timeout">5000</stringProp></HTTPSamplerProxy><hashTree>${headers}${jsr('post', true)}<hashTree/></hashTree>`
  const loop = `<WhileController guiclass="WhileControllerGui" testclass="WhileController" testname="Fixed relation, absolute ordinal deadlines" enabled="true"><stringProp name="WhileController.condition">\${cap10.continue}</stringProp></WhileController><hashTree>${jsr('gate')}<hashTree/><IfController guiclass="IfControllerPanel" testclass="IfController" testname="Only a scheduled offer" enabled="true"><stringProp name="IfController.condition">\${cap10.doWrite}</stringProp><boolProp name="IfController.evaluateAll">false</boolProp><boolProp name="IfController.useExpression">true</boolProp></IfController><hashTree>${http}</hashTree></hashTree>`
  const sampledHTTP = http.replace('</HTTPSamplerProxy><hashTree>', () => '</HTTPSamplerProxy><hashTree>' + jsr('pre', 'pre') + '<hashTree/>')
  const normal = csv + jsr('bind') + '<hashTree/>' + loop.replace(http, () => sampledHTTP) + jsr('done') + '<hashTree/>'
  const plan = `<?xml version="1.0" encoding="UTF-8"?><jmeterTestPlan version="1.2" properties="5.0" jmeter="5.6.3"><hashTree><TestPlan guiclass="TestPlanGui" testclass="TestPlan" testname="CAP10 authenticated mutations with owned PTS slots" enabled="true"><boolProp name="TestPlan.serialize_threadgroups">false</boolProp><boolProp name="TestPlan.tearDown_on_shutdown">true</boolProp><elementProp name="TestPlan.user_defined_variables" elementType="Arguments" guiclass="ArgumentsPanel" testclass="Arguments"><collectionProp name="Arguments.arguments"/></elementProp></TestPlan><hashTree><Arguments guiclass="ArgumentsPanel" testclass="Arguments" testname="CAP10 script identity"><collectionProp name="Arguments.arguments"><elementProp name="cap10.scriptSHA256" elementType="Argument"><stringProp name="Argument.name">cap10.scriptSHA256</stringProp><stringProp name="Argument.value">${scriptSHA256}</stringProp><stringProp name="Argument.metadata">=</stringProp></elementProp></collectionProp></Arguments><hashTree/>${thread('ThreadGroup','512 fixed exclusive relationships',512,normal)}${thread('PostThreadGroup','Terminal durable ACK',1,jsr('finish')+'<hashTree/>')}</hashTree></hashTree></jmeterTestPlan>\n`
  // User-defined variables, unlike JMeter properties, are transported by PTS.
  const finalPlan = plan
  directory = resolve(directory)
  await mkdir(directory, { recursive: false, mode: 0o700 })
  const jmx = join(directory, 'cap10-jmeter.jmx'), slots = join(directory, 'slots.csv')
  await writeFile(jmx, finalPlan, { flag: 'wx' })
  await writeFile(slots, 'slot\n' + Array.from({ length: 512 }, (_, n) => n).join('\n') + '\n', { flag: 'wx' })
  if (collectorUrl !== undefined || controlToken !== undefined) {
    const uri = new URL(collectorUrl)
    assert.equal(uri.protocol, 'http:'); assert.ok(!uri.username && !uri.password && !uri.search && !uri.hash)
    assert.match(uri.hostname, /^(127\.0\.0\.1|10\.\d+\.\d+\.\d+|192\.168\.\d+\.\d+|172\.(1[6-9]|2\d|3[01])\.\d+\.\d+)$/)
    assert.equal(typeof controlToken, 'string'); assert.ok(controlToken.length >= 24)
    await writeFile(join(directory, 'cap10-pts-private.json'), JSON.stringify({ collectorUrl, controlToken }) + '\n', { mode: 0o600, flag: 'wx' })
  }
  const manifest = { generator: 'pts-jmeter', version: 1, scriptSHA256, jmxSHA256: sha(finalPlan), slotsSHA256: sha(await readFile(slots)),
    normalThreads: 512, setupThreads: 0, teardownThreads: 1, ptsConcurrency: 513, csvSplitRequired: true,
    standardJMeter: '5.6.3', plugins: [], artifacts: ['cap10-jmeter.jmx','slots.csv','cap10-pts-private.json'],
    privacy: 'private config has only collector control credential; slots.csv contains no user/note/token. Do not publish raw PTS sampled logs without redaction.' }
  await writeFile(join(directory, 'cap10-jmeter-manifest.json'), JSON.stringify(manifest, null, 2) + '\n', { flag: 'wx' })
  return manifest
}
if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  assert.equal(process.argv[2], 'bundle'); assert.equal(process.argv.length, 4, 'use bundle <new-directory>; private config is created by the root controller')
  console.log(JSON.stringify(await bundle(process.argv[3])))
}
