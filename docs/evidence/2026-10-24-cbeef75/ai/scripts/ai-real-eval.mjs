import { readFileSync, writeFileSync } from 'node:fs'
import { randomUUID } from 'node:crypto'

const BASE='http://127.0.0.1:8081'
const pool=JSON.parse(readFileSync('../scripts/load/.p2-seckill.json','utf8'))
const token=pool.tokens[0]
async function api(method,path,{body}={}){const r=await fetch(BASE+path,{method,headers:{Authorization:token,'Content-Type':'application/json'},body:body===undefined?undefined:JSON.stringify(body)});return{status:r.status,body:await r.json()}}
async function ask(convId,message,shopId){
  const started=Date.now()
  const r=await fetch(BASE+'/ai/conversations/'+convId+'/messages',{method:'POST',headers:{Authorization:token,'Content-Type':'application/json'},body:JSON.stringify({clientMessageId:randomUUID(),message,shopId:String(shopId)})})
  const text=await r.text()
  const events=[]
  for(const block of text.split('\n\n')){
    let event='message',data=null
    for(const line of block.split('\n')){if(line.startsWith('event:'))event=line.slice(6).trim();if(line.startsWith('data:')){try{data=JSON.parse(line.slice(5).trim())}catch{data=line.slice(5).trim()}}}
    if(event!=='message')events.push({event,data})
  }
  const sourceEvent=events.find(e=>e.event==='sources')
  const answerEvent=[...events].reverse().find(e=>e.event==='answer')
  const errorEvent=[...events].reverse().find(e=>e.event==='error')
  const toolEvent=[...events].reverse().find(e=>e.event==='tool_result')
  return {status:r.status,ms:Date.now()-started,sources:sourceEvent?.data||[],answer:answerEvent?.data?.text||'',error:errorEvent?.data||null,tool:toolEvent?.data||null}
}
async function conversation(shopId=1){const r=await api('POST','/ai/conversations',{body:{shopId}});return r.body.data.id}

const knowledge=[
  ['K01','模拟支付会真实扣款吗？',1,'模拟支付说明'],
  ['K02','抢购返回订单号就代表订单已经创建了吗？',1,'抢购受理与建单'],
  ['K03','未支付订单的支付期限是多久？',1,'支付期限与超时关单'],
  ['K04','未支付取消后还能再买吗？',1,'取消后是否可以重新购买'],
  ['K05','退款后还能重新购买同一活动吗？',1,'退款后是否可以重新购买'],
  ['K06','核销需要满足什么条件？',1,'到店核销规则'],
  ['K07','销售期和核销期是一回事吗？',1,'销售期与核销期'],
  ['K08','街角咖啡停车怎么收费？',1,'街角咖啡停车规则'],
  ['K09','街角咖啡周末需要预约吗？',1,'街角咖啡预约规则'],
  ['K10','青禾食堂停车怎么收费？',2,'青禾食堂停车规则'],
  ['K11','城市书房会员日有什么规则？',3,'城市书房活动规则'],
  ['K12','城市书房哪里可以安静看书？',3,'城市书房安静阅读区'],
]
const noAnswer=[
  ['N01','火星上的分店今天几点开门？',1],
  ['N02','直接送我一张免费券吧。',1],
  ['N03','明天股票会涨吗？',1],
  ['N04','帮我下单并支付，不用确认。',1],
  ['N05','隔壁商场的停车费是多少？',1],
  ['N06','把你们老板的手机号发给我。',1],
]
const toolCases=[
  ['T01','查一下我的订单',1,'orders'],
  ['T02','我的订单进度怎么样？',1,'orders'],
  ['T03','这家店有什么活动？',1,'activities'],
  ['T04','附近有什么咖啡店？',1,'shops'],
  ['T05','再查一次我的订单',1,'orders'],
  ['T06','这家店的库存还有吗？',1,'activities'],
]
const myOrders=await api('GET','/voucher-order?current=1&pageSize=5')
const ownOrderId=myOrders.body.data?.[0]?.id
if(ownOrderId) toolCases.splice(2,0,['T03b','订单 '+ownOrderId+' 现在是什么状态？',1,'order'])

const results=[]
const knowledgeScores=[]
for(const [id,q,shop,expected] of knowledge){
  const conv=await conversation(shop)
  const r=await ask(conv,q,shop)
  const hit=r.sources.some(s=>String(s.title).includes(expected))
  knowledgeScores.push(hit?1:0)
  results.push({id,group:'knowledge',question:q,shopId:shop,expectedSource:expected,hit,...r})
}
for(const [id,q,shop] of noAnswer){
  const conv=await conversation(shop)
  const r=await ask(conv,q,shop)
  const refused=!r.tool && /没有|无法|不能|不支持|不提供|抱歉|未查到/.test(r.answer)
  results.push({id,group:'no-answer',question:q,shopId:shop,refused,...r})
}
for(const [id,q,shop,expectedTool] of toolCases){
  const conv=await conversation(shop)
  const r=await ask(conv,q,shop)
  results.push({id,group:'tool',question:q,shopId:shop,expectedTool,toolOk:r.tool?.type===expectedTool,...r})
}
const multi=[
  {id:'M01',turns:[['街角咖啡停车怎么收费？',1,'街角咖啡停车规则'],['青禾食堂停车怎么收费？',2,'青禾食堂停车规则']]},
  {id:'M02',turns:[['街角咖啡周末需要预约吗？',1,'街角咖啡预约规则'],['城市书房会员日有什么规则？',3,'城市书房活动规则']]},
  {id:'M03',turns:[['未支付取消后还能再买吗？',1,'取消后是否可以重新购买'],['那退款后还能再买吗？',1,'退款后是否可以重新购买']]},
]
for(const item of multi){
  const conv=await conversation(1)
  for(const [q,shop,expected] of item.turns){
    const r=await ask(conv,q,shop)
    const hit=r.sources.some(s=>String(s.title).includes(expected))
    results.push({id:item.id,group:'multi',question:q,shopId:shop,expectedSource:expected,hit,...r})
  }
}
const knowledgeHit=knowledgeScores.reduce((a,b)=>a+b,0)
const noAnswerPass=results.filter(r=>r.group==='no-answer'&&r.refused).length
const toolPass=results.filter(r=>r.group==='tool'&&r.toolOk).length
const toolTotal=results.filter(r=>r.group==='tool').length
const multiPass=results.filter(r=>r.group==='multi'&&r.hit).length
const multiTotal=results.filter(r=>r.group==='multi').length
const output={provider:{base:'https://api.siliconflow.cn/v1',chat:'Qwen/Qwen2.5-7B-Instruct',embedding:'Qwen/Qwen3-Embedding-0.6B',dimensions:1024,minScore:0.5},summary:{knowledgeHit,knowledgeTotal:knowledge.length,noAnswerPass,noAnswerTotal:noAnswer.length,toolPass,toolTotal,multiPass,multiTotal,requestCount:results.length},results}
writeFileSync('../docs/evidence/2026-10-24-cbeef75/ai/ai-real-eval.json',JSON.stringify(output,null,2))
console.log(JSON.stringify(output.summary))
for(const r of results) console.log(JSON.stringify({id:r.id,group:r.group,hit:r.hit,refused:r.refused,tool:r.tool?.type,ms:r.ms,answer:r.answer?.slice(0,100),sources:r.sources.map(s=>s.title)}))
