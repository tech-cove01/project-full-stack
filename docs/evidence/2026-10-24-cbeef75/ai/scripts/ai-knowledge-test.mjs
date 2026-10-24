import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
const BASE='http://127.0.0.1:8081'
const cache=JSON.parse(readFileSync('.qa/tokens.json','utf8'))
const owner=cache.owner, staff=cache.staff
async function api(method,path,{token,body}={}){const h={'Content-Type':'application/json'};if(token)h.authorization=token;const r=await fetch(BASE+path,{method,headers:h,body:body===undefined?undefined:JSON.stringify(body)});const t=await r.text();let j;try{j=t?JSON.parse(t):null}catch{j=t}return{status:r.status,body:j}}
const caps=await api('GET','/ai/capabilities',{token:owner})
assert.equal(caps.body.data.mode,'stub',JSON.stringify(caps.body))
assert.equal(caps.body.data.ready,true)
const list=await api('GET','/merchant/knowledge?shopId=1',{token:owner})
assert.equal(list.body.success,true,JSON.stringify(list.body))
const seed=list.body.data.find(x=>x.seedKey==='shop-a1-parking')
assert.ok(seed,'seed missing')
assert.equal(seed.publishedVersion,1,JSON.stringify(seed))
const pub=await api('GET','/ai/knowledge/'+seed.id+'?version=1',{token:owner})
assert.equal(pub.body.success,true,JSON.stringify(pub.body))
assert.ok(pub.body.data.body.includes('B2'))

const created=await api('POST','/merchant/knowledge',{token:owner,body:{shopId:'1',title:'P1D 测试门店规则',body:'测试门店规则：工作日 10:00 前到店可免费领取一杯柠檬水，节假日不适用。'}})
assert.equal(created.body.success,true,JSON.stringify(created.body))
const id=created.body.data.id
const published=await api('POST','/merchant/knowledge/'+id+'/publish',{token:owner})
assert.equal(published.body.success,true,JSON.stringify(published.body))
assert.equal(published.body.data.latestStatus,'READY',JSON.stringify(published.body.data))
const pub2=await api('GET','/ai/knowledge/'+id+'?version=1',{token:owner})
assert.equal(pub2.body.success,true,JSON.stringify(pub2.body))
const updatePublished=await api('PUT','/merchant/knowledge/'+id,{token:owner,body:{title:'x',body:'y'}})
assert.equal(updatePublished.status,409,JSON.stringify(updatePublished))
const staffList=await api('GET','/merchant/knowledge?shopId=1',{token:staff})
assert.equal(staffList.body.success,true,JSON.stringify(staffList.body))
assert.ok(staffList.body.data.every(x=>x.publishedVersion),'staff must only see published docs')
const cross=await api('GET','/merchant/knowledge?shopId=2',{token:staff})
assert.equal(cross.status,403,JSON.stringify(cross))
const disabled=await api('POST','/merchant/knowledge/'+id+'/disable',{token:owner})
assert.equal(disabled.body.success,true,JSON.stringify(disabled.body))
const gone=await api('GET','/ai/knowledge/'+id+'?version=1',{token:owner})
assert.equal(gone.status,404,JSON.stringify(gone))
console.log(JSON.stringify({pass:true,seedId:seed.id,createdId:id,caps:caps.body.data.mode,staffPublishedOnly:true,crossStatus:cross.status,disabledStatus:gone.status}))