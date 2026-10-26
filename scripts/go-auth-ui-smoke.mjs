// Real Vue -> Vite proxy -> Go -> MySQL/Redis. No API mocking.
import assert from 'node:assert/strict'
import { randomInt } from 'node:crypto'
import { mkdir, writeFile } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'
import { pathToFileURL } from 'node:url'

const moduleName = process.env.PLAYWRIGHT_MODULE || 'playwright'
const { chromium } = await import(moduleName.includes(':') || !moduleName.startsWith('.')
  ? moduleName
  : pathToFileURL(resolve(moduleName)).href)
const WEB = process.env.WEB_BASE || 'http://127.0.0.1:5174'
const API = process.env.API_BASE || 'http://127.0.0.1:8082'
const output = resolve(process.env.UI_EVIDENCE || 'go-auth-ui-result.json')
await mkdir(dirname(output), { recursive: true })

const browser = await chromium.launch({
  ...(process.env.CHROME_PATH ? { executablePath: process.env.CHROME_PATH } : {}),
  headless: true,
})
const context = await browser.newContext({ viewport: { width: 1280, height: 900 } })
const page = await context.newPage()
const errors = []
page.on('pageerror', () => errors.push('browser pageerror'))
const report = { task: 'GO-010', acceptance: ['MIG-GO-02', 'MIG-GO-03'], passed: false, checks: [] }
const check = (value, description) => {
  assert.ok(value, description)
  report.checks.push(description)
}
async function json(path, token, method = 'GET') {
  const response = await fetch(`${API}${path}`, {
    method,
    headers: token ? { authorization: token } : {},
  })
  return { status: response.status, body: await response.json() }
}
async function loginThroughPage() {
  const phone = `137${String(randomInt(10000000, 100000000))}`
  await page.goto(`${WEB}/login`)
  await page.getByPlaceholder('请输入手机号').fill(phone)
  await page.getByRole('button', { name: '获取验证码', exact: true }).click()
  await page.waitForFunction(() => /^\d{6}$/.test(document.querySelector('input[placeholder="6 位验证码"]')?.value || ''))
  const me = page.waitForResponse(r => new URL(r.url()).pathname === '/api/user/me')
  await page.getByRole('button', { name: '登录并继续', exact: true }).click()
  const profile = await (await me).json()
  check(profile.success && typeof profile.data?.id === 'string' && /^\d+$/.test(profile.data.id), 'profile ID is a decimal string')
  await page.locator('.nickname').filter({ hasText: profile.data.nickName }).waitFor()
  const token = await page.evaluate(() => localStorage.getItem('cityloop.token'))
  check(typeof token === 'string' && /^[a-f0-9]{32}$/.test(token), 'UI stores a valid opaque session')
  return { token, nickname: profile.data.nickName }
}

try {
  const direct = await json('/actuator/health')
  const proxied = await (await context.request.get(`${WEB}/api/actuator/health`)).json()
  check(direct.status === 200 && direct.body.runtime === 'go' && proxied.runtime === 'go'
    && proxied.service === 'project-full-stack-go' && proxied.version === direct.body.version,
  'Vite proxy and direct API both identify the Go service')
  report.service = proxied
  check((await json('/user/me')).status === 401, 'anonymous protected request is 401')

  const first = await loginThroughPage()
  const reloadedMe = page.waitForResponse(r => new URL(r.url()).pathname === '/api/user/me')
  await page.reload()
  await reloadedMe
  await page.locator('.nickname').filter({ hasText: first.nickname }).waitFor()
  check(true, 'page refresh restores the authenticated profile')
  await page.evaluate(() => localStorage.setItem('cityloop.last-order', '9007199254740993'))
  await page.getByRole('button', { name: '退出', exact: true }).click()
  await page.getByRole('button', { name: '登录 / 注册', exact: true }).waitFor()
  check(await page.evaluate(() => !localStorage.getItem('cityloop.token') && !localStorage.getItem('cityloop.last-order')), 'UI logout clears session and remembered order')
  check((await json('/user/me', first.token)).status === 401, 'logout revokes the previous session on the server')

  const second = await loginThroughPage()
  await page.evaluate(() => localStorage.setItem('cityloop.last-order', '9007199254740993'))
  check((await json('/user/logout', second.token, 'POST')).body.success, 'session is revoked remotely while the page remains open')
  await page.reload()
  await page.waitForURL(/\/login(?:\?|$)/)
  check(await page.evaluate(() => !localStorage.getItem('cityloop.token') && !localStorage.getItem('cityloop.last-order')), '401 on reload redirects to login and clears client identity')
  check(errors.length === 0, 'no uncaught browser errors')
  report.passed = true
  await page.screenshot({ path: output.replace(/\.json$/, '') + '.png', fullPage: true })
} catch (error) {
  // Never dump the body, request headers, phone, development code or session.
  report.failure = error instanceof Error ? error.message.split('\n')[0].slice(0, 180) : 'UI check failed'
  process.exitCode = 1
} finally {
  await browser.close()
  report.recordedAt = new Date().toISOString()
  await writeFile(output, JSON.stringify(report, null, 2) + '\n')
  console.log(JSON.stringify({ passed: report.passed, checks: report.checks.length, output }))
}
