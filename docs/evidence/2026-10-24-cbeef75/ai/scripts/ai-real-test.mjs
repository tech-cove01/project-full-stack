import { readFileSync } from 'node:fs'
import { randomUUID } from 'node:crypto'
const BASE='http://127.0.0.1:8081'
const owner=JSON.parse(readFileSync('.qa/tokens.json','utf8')).owner
async function api(method,path,{token,body}={}){const h={'Content-Type':'application/json'};if(token)h.authorization=token;const r=await fetch(BASE+path,{method,headers:h,body:body===undefined?undefined:JSON.stringify(body)});const t=await r.text();let j;try{j=t?JSON.parse(t):null}catch{j=t}return{status:r.status,body:j}}
async function ask(token,conv,msg){
  const started=Date.now()
  const r=await fetch(BASE+'/ai/conversations/'+conv+'/messages',{method:'POST',headers:{authorization:token,'Content-Type':'application/json'},body:JSON.stringify({clientMessageId:randomUUID(),message:msg,shopId:'1'})})
  const text=await r.text()
  const events=[]
  for(const block of text.split('\n\n')){
    let event='message',data=null
    for(const line of block.split('\n')){if(line.startsWith('event:'))event=line.slice(6).trim();if(line.startsWith('data:')){try{data=JSON.parse(line.slice(5).trim())}catch{data=line.slice(5).trim()}}}
    if(event!=='message')events.push({event,data})
  }
  return {status:r.status,ms:Date.now()-started,events}
}
const caps=await api('GET','/ai/capabilities',{token:owner})
console.log('caps',JSON.stringify(caps.body.data))
const conv=(await api('POST','/ai/conversations',{token:owner,body:{shopId:'1'}})).body.data.id
for(const question of ['未支付订单取消后，我还能再买同一张券吗？','街角咖啡停车怎么收费？','火星上的分店今天几点开门？']){
  const result=await ask(owner,conv,question)
  const sources=(result.events.find(e=>e.event==='sources')||{}).data||[]
  const answer=(result.events.filter(e=>e.event==='answer').pop()||{}).data||{}
  const error=(result.events.filter(e=>e.event==='error').pop()||{}).data
  console.log(JSON.stringify({question,status:result.status,ms:result.ms,eventNames:result.events.map(e=>e.event),sources:sources.map(s=>({title:s.title,score:s.score})),answer:answer.text,error}))
}
