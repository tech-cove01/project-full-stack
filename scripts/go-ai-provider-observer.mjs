// Bounded local test helper. No request/response content or credential
// is persisted. This owns only loopback9291 and a fixed Dashscope HTTPS upstream.
import http from 'node:http'
import { existsSync, writeFileSync } from 'node:fs'
import { resolve } from 'node:path'
if(!process.env.AI_OBSERVER_EVIDENCE)throw new Error('Explicit new AI_OBSERVER_EVIDENCE file required')
const output=resolve(process.env.AI_OBSERVER_EVIDENCE)
if(existsSync(output))throw new Error('Retain previous observation journals; choose a new evidence file')
const rows=[],startedAt=new Date().toISOString(),limit=12
let accepted=0
function save(){writeFileSync(output,JSON.stringify({startedAt,recordedAt:new Date().toISOString(),upstream:'https://dashscope.aliyuncs.com/compatible-mode/v1',maxPaidRequests:limit,accepted,requests:rows,contentsPersisted:false,credentialsPersisted:false,notes:'Usage is only what completed upstream JSON reports. Cancelled requests may incur unobserved charges. This is request-count control, not a global currency/token budget.'},null,2)+'\n')}
const server=http.createServer(async(req,res)=>{
 const path=new URL(req.url,'http://127.0.0.1:9291').pathname
 if(req.method==='GET'&&path==='/metrics'){res.writeHead(200,{'content-type':'application/json','cache-control':'no-store'}).end(JSON.stringify({accepted,requests:rows}));return}
 if(req.method!=='POST'||!['/v1/chat/completions','/v1/embeddings'].includes(path)){res.writeHead(404).end();return}
 if(accepted>=limit){res.writeHead(429,{'content-type':'application/json'}).end('{"error":{"message":"Local experiment request limit reached"}}');return}
 let bytes=0,parts=[]
 try{for await(const chunk of req){bytes+=chunk.length;if(bytes>1048576)throw Error('limit');parts.push(chunk)}}catch{res.writeHead(400).end();return}
 const body=Buffer.concat(parts);parts=[]
 let request
 try{request=JSON.parse(body)}catch{res.writeHead(400).end();return}
 if(res.destroyed||req.aborted)return
 if(!['qwen3.8-flash','text-embedding-v4'].includes(request.model)||request.stream===true||!req.headers.authorization){res.writeHead(400).end();return}
 // Body reads yield; recheck synchronously with reserving the forwarding slot.
 if(accepted>=limit){res.writeHead(429,{'content-type':'application/json'}).end('{"error":{"message":"Local experiment request limit reached"}}');return}
 const row={id:++accepted,operation:path.endsWith('embeddings')?'embedding':'chat',model:request.model,startedAt:new Date().toISOString(),requestBytes:bytes,stream:false,status:'pending',usage:null,clientCancelled:false}
 rows.push(row);save()
 const abort=new AbortController(),begin=Date.now()
 res.on('close',()=>{if(!res.writableFinished){row.clientCancelled=true;abort.abort()}})
 try{
  const upstream=await fetch('https://dashscope.aliyuncs.com/compatible-mode'+path,{method:'POST',headers:{'content-type':'application/json',authorization:req.headers.authorization},body,signal:abort.signal,redirect:'error'})
  row.httpStatus=upstream.status
  const raw=await upstream.arrayBuffer()
  try{const response=JSON.parse(Buffer.from(raw));const usage=response.usage;if(usage&&typeof usage==='object'){row.usage={};for(const key of ['prompt_tokens','completion_tokens','total_tokens'])if(Number.isSafeInteger(usage[key])&&usage[key]>=0)row.usage[key]=usage[key]}}catch{}
  row.status=upstream.ok?'completed':'http_error'
  if(!res.destroyed){res.writeHead(upstream.status,{'content-type':'application/json'});res.end(Buffer.from(raw))}
 }catch{row.status=row.clientCancelled?'client_cancelled':'transport_error';if(!res.destroyed)res.writeHead(502,{'content-type':'application/json'}).end('{"error":{"message":"Local forwarding failed"}}')}
 finally{row.durationMs=Date.now()-begin;row.finishedAt=new Date().toISOString();save()}
})
server.listen(9291,'127.0.0.1',()=>{save();process.stdout.write('{"observerReady":true,"port":9291,"maxPaidRequests":12}\n')})
for(const signal of ['SIGINT','SIGTERM','SIGBREAK'])process.on(signal,()=>{save();server.close(()=>process.exit(0));setTimeout(()=>process.exit(1),3000).unref()})
