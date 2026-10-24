import { chromium } from 'file:///C:/Users/19083/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright/index.mjs'
import assert from 'node:assert/strict'
import { existsSync, readFileSync, writeFileSync } from 'node:fs'

const UI = 'http://127.0.0.1:5173'
const API = 'http://127.0.0.1:8081'
const CACHE = '.qa/tokens.json'
const cache = existsSync(CACHE) ? JSON.parse(readFileSync(CACHE, 'utf8')) : {}
async function api(method, path, { token, body } = {}) {
  const headers = { 'Content-Type': 'application/json' }
  if (token) headers.authorization = token
  const response = await fetch(API + path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) })
  return { status: response.status, body: await response.json() }
}
async function ensureToken(phone, key) {
  if (cache[key]) {
    const me = await api('GET', '/user/me', { token: cache[key] })
    if (me.body.success) return cache[key]
  }
  const code = await api('POST', '/user/code?phone=' + phone)
  if (code.body.success === false && String(code.body.errorMsg).includes('频繁')) {
    await new Promise(resolve => setTimeout(resolve, 61000))
    return ensureToken(phone, key)
  }
  const login = await api('POST', '/user/login', { body: { phone, code: code.body.data.code } })
  assert.equal(login.body.success, true, JSON.stringify(login.body))
  cache[key] = login.body.data
  writeFileSync(CACHE, JSON.stringify(cache))
  return cache[key]
}

const ownerToken = await ensureToken('13800000000', 'owner')
const staffToken = await ensureToken('13900000000', 'staff')
const title = 'UI 全链路券 ' + Date.now()
const userPhone = '134' + String(Date.now()).slice(-8)
const browser = await chromium.launch({
  executablePath: 'C:/Program Files/Google/Chrome/Application/chrome.exe',
  headless: true,
})
const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } })
const page = await context.newPage()
const errors = []
page.on('pageerror', error => errors.push(error.message))
let userToken = ''

try {
  // 1. Owner publishes an activity through the UI.
  await page.goto(UI + '/')
  await page.evaluate(token => localStorage.setItem('cityloop.token', token), ownerToken)
  await page.goto(UI + '/merchant/activities')
  await page.getByRole('heading', { name: '活动管理' }).waitFor()
  await page.getByRole('button', { name: '发布活动' }).first().click()
  const dialog = page.locator('.el-dialog')
  await dialog.getByPlaceholder('例如：周末咖啡体验券').fill(title)
  await dialog.getByRole('button', { name: '发布', exact: true }).click()
  await page.getByText(title).first().waitFor({ timeout: 20000 })
  const row = page.locator('tr', { hasText: title })
  await row.getByText('已发布', { exact: true }).waitFor({ timeout: 30000 })
  await page.screenshot({ path: '.qa/p1-owner-published.png', fullPage: true })

  // 2. User buys and pays through the UI.
  await page.evaluate(() => localStorage.removeItem('cityloop.token'))
  await page.goto(UI + '/')
  await page.getByRole('heading', { name: '街角咖啡' }).waitFor()
  await page.getByRole('link', { name: /街角咖啡/ }).click()
  const card = page.locator('article.voucher-card', { hasText: title })
  await card.waitFor({ timeout: 15000 })
  await card.getByRole('button', { name: '登录后抢购' }).click()
  await page.getByRole('heading', { name: '手机号登录' }).waitFor()
  await page.getByPlaceholder('请输入手机号').fill(userPhone)
  await page.getByRole('button', { name: '获取验证码' }).click()
  await page.waitForFunction(() => {
    const input = document.querySelector('input[placeholder="6 位验证码"]')
    return input && input.value.length === 6
  })
  await page.getByRole('button', { name: '登录并继续' }).click()
  await card.getByRole('button', { name: '立即抢购' }).waitFor({ timeout: 15000 })
  await card.getByRole('button', { name: '立即抢购' }).click()
  await page.waitForURL(/\/orders\/\d+$/, { timeout: 15000 })
  const orderId = page.url().split('/').pop()
  await page.getByRole('heading', { name: '待支付' }).waitFor({ timeout: 30000 })
  userToken = await page.evaluate(() => localStorage.getItem('cityloop.token'))
  assert.ok(userToken)
  await page.getByRole('button', { name: '模拟支付' }).click()
  await page.locator('.redeem-token code').waitFor({ timeout: 15000 })
  const redeemToken = (await page.locator('.redeem-token code').innerText()).trim()
  assert.match(redeemToken, /^[a-f0-9]{64}$/)
  assert.equal(await page.evaluate(() => localStorage.getItem('cityloop.last-order')), orderId)
  await page.screenshot({ path: '.qa/p1-user-paid.png', fullPage: true })

  // 3. Staff redeems the token through the merchant UI.
  await page.evaluate(token => localStorage.setItem('cityloop.token', token), staffToken)
  await page.goto(UI + '/merchant/orders')
  await page.getByRole('heading', { name: '订单与核销' }).waitFor()
  await page.getByPlaceholder('粘贴 64 位核销码').fill(redeemToken)
  await page.getByRole('button', { name: '核销', exact: true }).click()
  await page.getByText(/核销成功/).waitFor({ timeout: 15000 })
  await page.screenshot({ path: '.qa/p1-staff-redeemed.png', fullPage: true })

  const paid = await api('GET', '/voucher-order/' + orderId, { token: userToken })
  assert.equal(paid.body.data.orderStatus, 3, JSON.stringify(paid.body))
  assert.deepEqual(errors, [])
  console.log(JSON.stringify({ pass: true, title, orderId, redeemTokenLength: redeemToken.length, finalStatus: paid.body.data.orderStatus }))
} catch (error) {
  await page.screenshot({ path: '.qa/p1-ui-failure.png', fullPage: true }).catch(() => {})
  console.error(await page.locator('body').innerText().catch(() => 'body unavailable'))
  throw error
} finally {
  await browser.close()
}
