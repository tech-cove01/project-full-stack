import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { randomUUID } from 'node:crypto'

const BASE='http://127.0.0.1:8081'
const cache=JSON.parse(readFileSync('.qa/tokens.json','utf8'))
const owner=cache.owner
const pool=JSON.parse(readFileSync('../scripts/load/.p2-seckill.json','utf8'))
const userToken=pool.tokens[0]
async function api(method,path,{token,body}={}){const h={'Content-Type':'application/json'};if(token)h.authorization=token;const r=await fetch(BASE+path,{method,headers:h,body:body===undefined?undefined:JSON.stringify(body)});const t=await r.text();let j;try{j=t?JSON.parse(t):null}catch{j=t}return{status:r.status,body:j}}
async function conversation(token,shopId='1'){const r=await api('POST','/ai/conversations',{token,body:{shopId}});assert.equal(r.body.success,true,JSON.stringify(r.body));return r.body.data.id}
async function ask(token,convId,message,shopId='1'){
  const res=await fetch(BASE+'/ai/conversations/'+convId+'/messages',{method:'POST',headers:{authorization:token,'Content-Type':'application/json'},body:JSON.stringify({clientMessageId:randomUUID(),message,shopId})})
  if(!res.ok) return {status:res.status,text:await res.text(),events:[]}
  assert.equal(res.headers.get('content-type')?.includes('text/event-stream'),true)
  const reader=res.body.getReader(); const decoder=new TextDecoder(); let buffer=''; const events=[]
  while(true){const {done,value}=await reader.read(); if(done)break; buffer+=decoder.decode(value,{stream:true});
    let idx; while((idx=buffer.indexOf('\n\n'))>=0){const block=buffer.slice(0,idx);buffer=buffer.slice(idx+2);const ev={event:'message',data:null};for(const line of block.split('\n')){if(line.startsWith('event:'))ev.event=line.slice(6).trim();if(line.startsWith('data:')){const raw=line.slice(5).trim();try{ev.data=JSON.parse(raw)}catch{ev.data=raw}}events.push(ev)} }
  }
  return {status:res.status,events}
}
function byName(events,name){return events.filter(e=>e.event===name)}
function answerOf(events){const a=byName(events,'answer');return a.length?a[a.length-1].data:null}

const caps=await api('GET','/ai/capabilities',{token:owner})
assert.equal(caps.body.data.mode,'stub')
const ownerConv=await conversation(owner)
const q1=await ask(owner,ownerConv,'未支付订单取消后，我还能再买同一张券吗？')
assert.equal(q1.status,200)
assert.ok(byName(q1.events,'meta').length>=1)
assert.ok(byName(q1.events,'sources').length>=1, 'expected sources')
const sources1=byName(q1.events,'sources')[0].data
assert.ok(sources1.some(s=>s.title.includes('取消后是否可以重新购买')),JSON.stringify(sources1))
assert.ok(answerOf(q1.events).text.includes('重新购买')||answerOf(q1.events).text.includes('取消'),JSON.stringify(answerOf(q1.events)))

const q2=await ask(owner,ownerConv,'街角咖啡停车怎么收费？')
const sources2=byName(q2.events,'sources')[0].data
assert.ok(sources2.some(s=>s.title.includes('街角咖啡停车规则')),JSON.stringify(sources2))
assert.ok(!sources2.some(s=>s.title.includes('青禾食堂停车规则')),'shop filter must not mix A2')

const q3=await ask(owner,ownerConv,'火星上的分店今天几点开门？')
const a3=answerOf(q3.events)
assert.ok(a3.text.includes('没有查到')||a3.text.includes('无法'),JSON.stringify(a3))
assert.equal(byName(q3.events,'sources').length,0,'unsupported question must not emit sources')

const userConv=await conversation(userToken)
const q4=await ask(userToken,userConv,'查一下我的订单')
const tool4=byName(q4.events,'tool_result')
assert.ok(tool4.length>=1,JSON.stringify(q4.events.map(e=>e.event)))
assert.equal(tool4[tool4.length-1].data.type,'orders')

const q5=await ask(userToken,userConv,'请问订单 999999999999999999 现在是什么状态')
const a5=answerOf(q5.events)
assert.ok(a5.text.includes('没有找到'),JSON.stringify(a5))
assert.equal(byName(q5.events,'tool_result').length,0)

const sensitive=await ask(owner,ownerConv,'我的手机号是 13800000000，帮我看订单')
assert.equal(sensitive.status,400,JSON.stringify(sensitive))
const cross=await api('GET','/ai/conversations/'+userConv,{token:owner})
assert.equal(cross.status,404,JSON.stringify(cross))

const p1=ask(owner,ownerConv,'再解释一下退款后能不能重新购买？')
const p2=ask(owner,ownerConv,'再解释一下逾期关单的规则？')
const [r1,r2]=await Promise.all([p1,p2])
assert.ok([r1.status,r2.status].includes(409)||[r1.status,r2.status].includes(200),'concurrent requests must be serialized or rejected')
console.log(JSON.stringify({pass:true,conversation:ownerConv,sources: sources1.map(s=>s.title),answer1:answerOf(q1.events).text.slice(0,80),q3:answerOf(q3.events).text.slice(0,40),toolTypes:tool4.map(t=>t.data.type),crossStatus:cross.status,sensitiveStatus:sensitive.status,concurrent:[r1.status,r2.status]}))
