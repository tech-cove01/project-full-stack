// Local evidence script. Point these at the local Playwright install and Chrome.
const playwrightModule = process.env.PLAYWRIGHT_MODULE || 'file:///C:/Users/19083/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright/index.mjs'
const chromePath = process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe'
const { chromium } = await import(playwrightModule)
import assert from 'node:assert/strict'

const BASE = process.env.API_BASE || 'http://127.0.0.1:8081'
const browser = await chromium.launch({
  executablePath: chromePath,
  headless: true,
})
const context = await browser.newContext({ viewport: { width: 1280, height: 900 } })
const page = await context.newPage()
const browserErrors = []
page.on('pageerror', error => browserErrors.push(error.message))
const phone = '137' + String(Date.now()).slice(-8)

async function apiCode(phone) {
  const response = await fetch(BASE + '/user/code?phone=' + encodeURIComponent(phone), { method: 'POST' })
  const body = await response.json()
  assert.equal(body.success, true)
  return body.data.code
}
async function apiLogin(phone) {
  const code = await apiCode(phone)
  const response = await fetch(BASE + '/user/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ phone, code }),
  })
  const body = await response.json()
  assert.equal(body.success, true)
  return body.data
}

try {
  await page.goto('http://127.0.0.1:5173/')
  await page.getByRole('heading', { name: '街角咖啡' }).waitFor({ timeout: 15000 })
  await page.getByRole('link', { name: /街角咖啡/ }).click()
  await page.getByRole('button', { name: '登录后抢购' }).click()
  await page.getByRole('heading', { name: '手机号登录' }).waitFor()
  await page.getByPlaceholder('请输入手机号').fill(phone)
  await page.getByRole('button', { name: '获取验证码' }).click()
  await page.waitForFunction(() => {
    const input = document.querySelector('input[placeholder="6 位验证码"]')
    return input && input.value.length === 6
  })
  await page.getByRole('button', { name: '登录并继续' }).click()
  await page.getByRole('button', { name: '立即抢购' }).waitFor({ timeout: 15000 })
  await page.getByRole('button', { name: '立即抢购' }).click()
  await page.waitForURL(/\/orders\/\d+$/, { timeout: 15000 })
  const orderId = page.url().split('/').pop()
  await page.getByRole('heading', { name: '抢购成功，订单已创建' }).waitFor({ timeout: 30000 })
  assert.match(orderId, /^\d+$/)
  assert.equal(await page.evaluate(() => localStorage.getItem('cityloop.last-order')), orderId)
  const storedToken = await page.evaluate(() => localStorage.getItem('cityloop.token'))
  assert.ok(storedToken)

  const otherToken = await apiLogin('138' + String(Date.now() + 7).slice(-8))
  const otherResponse = await fetch(BASE + '/voucher-order/' + orderId, { headers: { authorization: otherToken } })
  const otherBody = await otherResponse.json()
  assert.equal(otherBody.success, false)
  const anonymousResponse = await fetch(BASE + '/voucher-order/' + orderId)
  assert.equal(anonymousResponse.status, 401)

  await page.reload()
  await page.getByRole('heading', { name: '抢购成功，订单已创建' }).waitFor({ timeout: 30000 })
  await page.screenshot({ path: '.qa/real-order.png', fullPage: true })
  assert.deepEqual(browserErrors, [])
  console.log(JSON.stringify({ pass: true, phone, orderId, otherUserRejected: !otherBody.success, anonymousStatus: anonymousResponse.status }))
} catch (error) {
  await page.screenshot({ path: '.qa/real-failure.png', fullPage: true }).catch(() => {})
  console.error(await page.locator('body').innerText().catch(() => 'body unavailable'))
  throw error
} finally {
  await browser.close()
}