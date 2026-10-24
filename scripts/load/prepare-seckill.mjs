import { writeFileSync } from 'node:fs'

const BASE = process.env.API_BASE || 'http://127.0.0.1:8081'
const COUNT = Number(process.env.COUNT || 200)
const OWNER_PHONE = process.env.OWNER_PHONE || '13800000000'
const OUT = process.env.OUT || 'scripts/load/.p2-seckill.json'
const now = Date.now()
const iso = ms => new Date(ms).toISOString()

async function api(method, path, { token, body, key } = {}) {
  const headers = { 'Content-Type': 'application/json' }
  if (token) headers.authorization = token
  if (key) headers['Idempotency-Key'] = key
  const response = await fetch(BASE + path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) })
  const text = await response.text()
  let json
  try { json = text ? JSON.parse(text) : null } catch { json = text }
  return { status: response.status, body: json }
}

async function login(phone) {
  const code = await api('POST', '/user/code?phone=' + phone)
  if (code.body && code.body.success === false && String(code.body.errorMsg).includes('频繁')) {
    console.log('throttled, waiting 61s for ' + phone)
    await new Promise(resolve => setTimeout(resolve, 61000))
    return login(phone)
  }
  if (!code.body || code.body.success !== true) throw new Error(JSON.stringify(code))
  const session = await api('POST', '/user/login', { body: { phone, code: code.body.data.code } })
  if (!session.body || session.body.success !== true) throw new Error(JSON.stringify(session))
  return session.body.data
}

async function createActivity(label, stock) {
  const body = {
    shopId: '1',
    title: 'P2 ' + label + ' ' + now,
    subTitle: 'P2 并发验收',
    rules: '仅用于本地压测',
    payValue: '100',
    actualValue: '200',
    totalStock: stock,
    beginTime: iso(now - 60_000),
    endTime: iso(now + 10 * 86400_000),
    redeemBeginTime: iso(now - 60_000),
    redeemEndTime: iso(now + 20 * 86400_000),
  }
  const created = await api('POST', '/merchant/activities', { token: owner, key: 'p2-' + now + '-' + label, body })
  if (!created.body.success) throw new Error(JSON.stringify(created.body))
  const id = created.body.data.id
  for (let i = 0; i < 40; i++) {
    const list = await api('GET', '/merchant/activities?current=1&pageSize=50', { token: owner })
    const view = (list.body.data || []).find(item => item.id === id)
    if (view && view.ready) return id
    await new Promise(resolve => setTimeout(resolve, 500))
  }
  throw new Error('activity not ready ' + id)
}

const owner = await login(OWNER_PHONE)
const multiActivityId = await createActivity('multi', 100)
const duplicateActivityId = await createActivity('duplicate', 100)
const restartActivityId = await createActivity('restart', 20)

const phones = Array.from({ length: COUNT }, (_, i) => '132' + String(now + i).slice(-8))
const tokens = []
for (let start = 0; start < phones.length; start += 20) {
  const batch = phones.slice(start, start + 20)
  const batchTokens = await Promise.all(batch.map(phone => login(phone)))
  tokens.push(...batchTokens)
  process.stdout.write('.')
}
console.log('')

const payload = { baseUrl: BASE, createdAt: new Date(now).toISOString(), multiActivityId, duplicateActivityId, restartActivityId, phones, tokens }
writeFileSync(OUT, JSON.stringify(payload, null, 2))
console.log(JSON.stringify({ pass: true, COUNT, multiActivityId, duplicateActivityId, restartActivityId, out: OUT }))
