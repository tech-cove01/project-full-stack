// Non-paid transport probe, not a business/model acceptance fixture. Exercise
// the exact passive collector while the application's reader cancels at done.
import assert from 'node:assert/strict'
import http from 'node:http'
import { pathToFileURL } from 'node:url'
import { resolve } from 'node:path'
import { passiveSSECapture } from './sse-cdp-capture-initial.mjs'
const { chromium } = await import(pathToFileURL(resolve('backend/bin/go076-tools/node_modules/playwright-core/index.mjs')).href)
const results = [], closed = new Set()
const server = http.createServer(async (req,res) => {
  if (req.url === '/') { res.end('<!doctype html><title>Local SSE transport probe</title>'); return }
  if (req.url !== '/probe' || req.method !== 'POST') { res.writeHead(404).end(); return }
  let raw=''; for await (const chunk of req) raw+=chunk
  const {mode}=JSON.parse(raw)
  res.on('close',()=>{if(!res.writableFinished)closed.add(mode)})
  res.writeHead(200,{'Content-Type':'text/event-stream','Cache-Control':'no-cache'})
  res.write('event: meta\ndata: {}\n\n')
  if(mode==='cancel')return
  const finish=()=>{res.write('event: answer\ndata: {"text":"probe"}\n\nevent: done\ndata: {}\n\n');if(mode==='fast')res.end();else setTimeout(()=>res.end(),300)}
  if(mode==='fast')finish();else setTimeout(finish,30)
})
await new Promise(resolveReady=>server.listen(0,'127.0.0.1',resolveReady))
const browser=await chromium.launch({executablePath:'C:/Program Files/Google/Chrome/Application/chrome.exe',headless:true})
try{
  const context=await browser.newContext(),page=await context.newPage()
  await passiveSSECapture(context,page,{onStart(meta){if(meta.pathname!=='/probe')return null;return{mode:JSON.parse(meta.postData).mode}},onFinish(value,text,state){results.push({...value,events:[...text.matchAll(/^event: (.+)$/gm)].map(m=>m[1]),...state})}})
  await page.goto(`http://127.0.0.1:${server.address().port}/`)
  for(const mode of ['fast','done-cancel','cancel']){
    await page.evaluate(async mode=>{const abort=new AbortController();try{const response=await fetch('/probe',{method:'POST',body:JSON.stringify({mode}),signal:abort.signal});const reader=response.body.getReader();let text='';while(true){const{done,value}=await reader.read();if(done)break;text+=new TextDecoder().decode(value);if(mode==='cancel'){abort.abort();break}if(text.includes('event: done')){await reader.cancel();break}}}catch{}},mode)
    const deadline=Date.now()+3000
    while(results.length<['fast','done-cancel','cancel'].indexOf(mode)+1&&Date.now()<deadline)await new Promise(r=>setTimeout(r,10))
    const result=results.find(r=>r.mode===mode)
    assert.ok(result&&!result.captureError,`passive capture failed for ${mode}`)
    assert.deepEqual(result.events,mode==='cancel'?['meta']:['meta','answer','done'])
  }
  assert.ok(closed.has('cancel'),'passive observer must not keep cancelled HTTP stream alive')
  console.log(JSON.stringify({passed:true,paidCalls:0,cases:results}))
}finally{await browser.close();await new Promise(resolveClose=>server.close(resolveClose))}
