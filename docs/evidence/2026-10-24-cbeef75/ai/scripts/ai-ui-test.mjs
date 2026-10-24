import { chromium } from 'file:///C:/Users/19083/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright/index.mjs'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'

const UI='http://127.0.0.1:5173'
const cache=JSON.parse(readFileSync('.qa/tokens.json','utf8'))
const pool=JSON.parse(readFileSync('../scripts/load/.p2-seckill.json','utf8'))
const owner=cache.owner
const userToken=pool.tokens[0]
const title='UI 助手停车规则 '+Date.now()
const browser=await chromium.launch({executablePath:'C:/Program Files/Google/Chrome/Application/chrome.exe',headless:true})
const context=await browser.newContext({viewport:{width:1440,height:1000}})
const page=await context.newPage()
const errors=[]
page.on('pageerror', e=>errors.push(e.message))
async function setToken(token){await page.goto(UI+'/');await page.evaluate(t=>localStorage.setItem('cityloop.token',t),token)}
try{
  await setToken(owner)
  await page.goto(UI+'/merchant/knowledge')
  await page.getByRole('heading',{name:'门店公开规则'}).waitFor()
  await page.getByRole('button',{name:'新建公开规则'}).click()
  const dialog=page.locator('.el-dialog')
  await dialog.getByPlaceholder('例如：街角咖啡停车规则').fill(title)
  await dialog.getByPlaceholder('只写可以被顾客公开看到的规则；不要填写私密资料。').fill('街角咖啡夜间停车规则：20:00 后凭消费小票可在 B2 车库免费停车 3 小时，其他时段按商场标准收费。')
  await dialog.getByRole('button',{name:'保存草稿'}).click()
  await page.getByText(title).first().waitFor({timeout:15000})
  const row=page.locator('tr',{hasText:title})
  await row.getByRole('button',{name:'发布'}).click()
  await row.getByText(/已发布 v\d+/).waitFor({timeout:20000})
  await page.screenshot({path:'.qa/ai-merchant-knowledge.png',fullPage:true})

  await setToken(userToken)
  await page.goto(UI+'/assistant?shopId=1')
  await page.getByRole('heading',{name:'店铺助手'}).waitFor()
  const input=page.getByPlaceholder('例如：未支付取消后还能再买吗？街角咖啡停车怎么收费？')
  await input.fill('夜间停车可以免费停多久？')
  await page.getByRole('button',{name:'发送'}).click()
  await page.locator('.source-card').first().waitFor({timeout:30000})
  const sourceText=await page.locator('.source-card').first().innerText()
  assert.ok(sourceText.includes('UI 助手停车规则'), sourceText)
  await page.screenshot({path:'.qa/ai-assistant-answer.png',fullPage:true})

  await page.locator('.source-card').first().click()
  await page.waitForURL(/\/ai\/knowledge\//,{timeout:10000})
  await page.getByRole('heading',{name:title}).waitFor()
  await page.goBack()
  await page.getByRole('heading',{name:'店铺助手'}).waitFor()

  await page.getByPlaceholder('例如：未支付取消后还能再买吗？街角咖啡停车怎么收费？').fill('查一下我的订单')
  await page.getByRole('button',{name:'发送'}).click()
  await page.locator('.tool-card').waitFor({timeout:30000})
  await page.screenshot({path:'.qa/ai-assistant-tool.png',fullPage:true})
  assert.deepEqual(errors,[])
  console.log(JSON.stringify({pass:true,title,source:sourceText.split('\n')[0],tool:true}))
}catch(error){
  await page.screenshot({path:'.qa/ai-ui-failure.png',fullPage:true}).catch(()=>{})
  console.error(await page.locator('body').innerText().catch(()=>'no body'))
  throw error
}finally{await browser.close()}
