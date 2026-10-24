import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { randomUUID } from 'node:crypto'
const BASE='http://127.0.0.1:8081'
const owner=JSON.parse(readFileSync('.qa/tokens.json','utf8')).owner
async function api(method,path,{token,body}={}){const h={'Content-Type':'application/json'};if(token)h.authorization=token;const r=await fetch(BASE+path,{method,headers:h,body:body===undefined?undefined:JSON.stringify(body)});const t=await r.text();let j;try{j=t?JSON.parse(t):null}catch{j=t}return{status:r.status,body:j}}
async function sse(token,conv,id,msg){const r=await fetch(BASE+'/ai/conversations/'+conv+'/messages',{method:'POST',headers:{authorization:token,'Content-Type':'application/json'},body:JSON.stringify({clientMessageId:id,message:msg,shopId:'1'})});if(!r.ok)return{status:r.status,text:await r.text()};const text=await r.text();return{status:r.status,text}}
const c=await api('POST','/ai/conversations',{token:owner,body:{shopId:'1'}})
const id=randomUUID()
const first=await sse(owner,c.body.data.id,id,'未支付取消后还能再买吗？')
assert.equal(first.status,200)
assert.ok(first.text.includes('event:answer'),first.text)
const second=await sse(owner,c.body.data.id,id,'未支付取消后还能再买吗？')
assert.equal(second.status,200)
assert.ok(second.text.includes('stage":"cached'),second.text)
const conflict=await sse(owner,c.body.data.id,id,'退款后还能再买吗？')
assert.equal(conflict.status,409,JSON.stringify(conflict))
console.log(JSON.stringify({pass:true,firstAnswer:first.text.includes('取消后是否可以重新购买'),cached:second.text.includes('cached'),conflict:conflict.status}))
