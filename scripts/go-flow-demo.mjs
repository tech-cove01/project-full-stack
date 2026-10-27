// Three product flows against the Go backend: trade (publish -> seckill -> pay ->
// redeem -> audit), community (note -> like -> follow -> feed) and AI (FAQ ->
// publish -> question -> cited answer). The script owns exactly the rows it
// inserts and deletes them again; it never truncates a table.
//
// Required: API_BASE, MYSQL_HOST, MYSQL_PORT, MYSQL_DATABASE, MYSQL_USER,
// MYSQL_PASSWORD, REDIS_HOST, REDIS_PORT. Usage: node scripts/go-flow-demo.mjs <flow>
import { randomBytes, randomInt } from 'node:crypto'
import { spawnSync } from 'node:child_process'
import { mkdir, writeFile } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'

for (const key of ['MYSQL_HOST', 'MYSQL_PORT', 'MYSQL_DATABASE', 'MYSQL_USER', 'MYSQL_PASSWORD', 'REDIS_HOST', 'REDIS_PORT']) {
  if (!process.env[key]) throw new Error(`Explicit isolated ${key} is required`)
}
const API = process.env.API_BASE || 'http://127.0.0.1:8082'
const output = resolve(process.env.FLOW_EVIDENCE || 'go-flow-demo-result.json')
await mkdir(dirname(output), { recursive: true })
const suffix = randomBytes(4).toString('hex')
const phones = [`138${randomInt(10000000, 100000000)}`, `137${randomInt(10000000, 100000000)}`]
const report = { task: 'GO-070', passed: false, flows: {}, checks: [], cleanup: [] }

function sql(statement) {
  const result = spawnSync(process.env.MYSQL_BIN || 'mysql', [
    '--protocol=TCP', `--host=${process.env.MYSQL_HOST}`, `--port=${process.env.MYSQL_PORT}`,
    `--user=${process.env.MYSQL_USER}`, '--default-character-set=utf8mb4', '--batch', '--skip-column-names',
    process.env.MYSQL_DATABASE,
  ], {
    input: `SET NAMES utf8mb4; SET time_zone='+08:00'; ${statement}`,
    encoding: 'utf8', env: { ...process.env, MYSQL_PWD: process.env.MYSQL_PASSWORD },
  })
  if (result.error || result.status !== 0) {
    throw new Error('Isolated fixture SQL failed; SQL and credential diagnostics omitted')
  }
  return result.stdout.trim()
}

async function api(method, path, body, token, extraHeaders) {
  const headers = { 'Content-Type': 'application/json', ...(extraHeaders || {}) }
  if (token) headers.authorization = token
  const response = await fetch(API + path, {
    method, headers, body: body === undefined ? undefined : JSON.stringify(body),
  })
  const text = await response.text()
  let payload = {}
  try { payload = JSON.parse(text) } catch { payload = { errorMsg: text } }
  return { status: response.status, body: payload }
}

function check(value, description) {
  if (!value) throw new Error(`Check failed: ${description}`)
  report.checks.push(description)
}

// The dev profile sends one code per phone per minute, so a session is reused
// across the flows instead of asking for a second code.
const sessions = new Map()
async function login(phone) {
  if (sessions.has(phone)) return sessions.get(phone)
  const code = await api('POST', `/user/code?phone=${phone}`)
  if (code.body?.data?.mode !== 'development') {
    throw new Error(`Development profile required for the demo (${code.body?.errorMsg || code.status})`)
  }
  const session = await api('POST', '/user/login', { phone, code: code.body.data.code })
  if (!session.body?.data) throw new Error('Login failed')
  sessions.set(phone, session.body.data)
  return session.body.data
}

// A merchant, a shop and its owner, plus the shopper who buys and writes.
// The insert and LAST_INSERT_ID() share one mysql call: a new process has its
// own session, so the id would otherwise be lost.
function seedMerchant() {
  const merchantUser = Number(sql(`INSERT INTO tb_user (phone,password,nick_name,icon)
    VALUES ('${phones[0]}','x','演示商家',''); SELECT LAST_INSERT_ID();`))
  const shopper = Number(sql(`INSERT INTO tb_user (phone,password,nick_name,icon)
    VALUES ('${phones[1]}','x','演示用户',''); SELECT LAST_INSERT_ID();`))
  const merchant = Number(sql(`INSERT INTO tb_merchant (name,status)
    VALUES ('演示商家-${suffix}','ACTIVE'); SELECT LAST_INSERT_ID();`))
  const shop = Number(sql(`INSERT INTO tb_shop (merchant_id,name,type_id,images,address,x,y,open_hours)
    VALUES (${merchant},'演示门店-${suffix}',1,'','演示地址',0,0,'09:00-21:00'); SELECT LAST_INSERT_ID();`))
  sql(`INSERT INTO tb_merchant_member (merchant_id,user_id,role_code,status)
       VALUES (${merchant},${merchantUser},'OWNER','ACTIVE')`)
  return { merchantUser, shopper, merchant, shop }
}

function cleanupFixtures(ids) {
  const statements = [
    `DELETE FROM tb_ai_chunk WHERE document_id IN (SELECT id FROM tb_ai_document WHERE shop_id=${ids.shop})`,
    `DELETE FROM tb_ai_document_version WHERE document_id IN (SELECT id FROM tb_ai_document WHERE shop_id=${ids.shop})`,
    `DELETE FROM tb_ai_document WHERE shop_id=${ids.shop}`,
    `DELETE FROM tb_community_outbox WHERE aggregate_id IN (${ids.shop},${ids.shopper})`,
    `DELETE FROM tb_blog_like WHERE blog_id IN (SELECT id FROM tb_blog WHERE shop_id=${ids.shop})`,
    `DELETE FROM tb_blog WHERE shop_id=${ids.shop} OR user_id=${ids.shopper}`,
    `DELETE FROM tb_follow WHERE user_id=${ids.shopper} OR follow_user_id=${ids.shopper}`,
    `DELETE FROM tb_voucher_order WHERE user_id=${ids.shopper}`,
    `DELETE FROM tb_outbox WHERE aggregate_id IN (${ids.shop},${ids.shopper})`,
    `DELETE FROM tb_seckill_voucher WHERE voucher_id IN (SELECT id FROM tb_voucher WHERE shop_id=${ids.shop})`,
    `DELETE FROM tb_voucher WHERE shop_id=${ids.shop}`,
    `DELETE FROM tb_merchant_member WHERE user_id IN (${ids.merchantUser},${ids.shopper})`,
    `DELETE FROM tb_shop WHERE id=${ids.shop}`,
    `DELETE FROM tb_merchant WHERE id=${ids.merchant}`,
    `DELETE FROM tb_user WHERE id IN (${ids.merchantUser},${ids.shopper})`,
  ]
  for (const statement of statements) {
    try { sql(statement) } catch (error) { report.cleanup.push(String(error.message)) }
  }
}

async function waitForOrder(token, orderId, attempts = 40) {
  for (let attempt = 0; attempt < attempts; attempt++) {
    const reply = await api('GET', `/voucher-order/${orderId}`, undefined, token)
    if (reply.body?.data?.state === 'CREATED' && reply.body.data.orderStatus === 1) return reply.body.data
    await new Promise(resolveWait => setTimeout(resolveWait, 250))
  }
  throw new Error('The accepted order never became a durable row')
}

async function tradeFlow(ids) {
  const owner = await login(phones[0])
  const shopper = await login(phones[1])
  const begin = new Date(Date.now() - 3600_000).toISOString().replace(/\.\d{3}Z$/, '.000Z')
  const end = new Date(Date.now() + 3600_000).toISOString().replace(/\.\d{3}Z$/, '.000Z')
  const redeemEnd = new Date(Date.now() + 7200_000).toISOString().replace(/\.\d{3}Z$/, '.000Z')
  const activity = await api('POST', '/merchant/activities', {
    shopId: String(ids.shop),
    title: `演示活动-${suffix}`,
    payValue: '1900',
    actualValue: '2500',
    totalStock: 5,
    beginTime: begin,
    endTime: end,
    redeemBeginTime: begin,
    redeemEndTime: redeemEnd,
  }, owner, { 'Idempotency-Key': `demo-activity-${suffix}` })
  check(activity.status === 200 && activity.body?.success,
    `the merchant can publish an activity (${activity.status} ${activity.body?.errorMsg || ''})`)
  const voucherId = activity.body.data.id
  let published = null
  for (let attempt = 0; attempt < 40; attempt++) {
    const reply = await api('GET', '/merchant/activities?current=1&pageSize=50', undefined, owner)
    published = (reply.body?.data || []).find(item => item.id === voucherId)
    if (published?.publishStatus === 'ACTIVE' && published.ready) break
    await new Promise(resolveWait => setTimeout(resolveWait, 250))
  }
  check(published?.publishStatus === 'ACTIVE' && published?.ready === true,
    'the activity becomes ACTIVE with a ready gate')
  const accepted = await api('POST', `/voucher-order/seckill/${voucherId}`, undefined, shopper)
  check(accepted.status === 200 && typeof accepted.body?.data === 'string', 'the seckill request is accepted')
  const orderId = accepted.body.data
  const order = await waitForOrder(shopper, orderId)
  check(order.orderStatus === 1, 'the accepted order becomes a durable pending row')
  const paid = await api('POST', `/voucher-order/${orderId}/pay`, undefined, shopper)
  check(paid.status === 200 && paid.body?.data?.orderStatus === 2, 'the shopper can pay the order')
  const redeemToken = paid.body.data.redeemToken
  check(typeof redeemToken === 'string' && redeemToken.length === 64,
    'the paid order exposes its redeem token to its owner')
  const redeemed = await api('POST', '/merchant/orders/redeem', { shopId: String(ids.shop), token: redeemToken }, owner)
  check(redeemed.status === 200 && redeemed.body?.data?.order?.status === 3, 'the merchant can redeem the order')
  check(redeemed.body?.data?.alreadyRedeemed === false, 'the first redemption is not a replay')
  const audits = await api('GET', '/merchant/audits?current=1&pageSize=50', undefined, owner)
  const actions = (audits.body?.data || []).map(item => item.action)
  check(actions.includes('ORDER_REDEEMED'), 'the redemption is visible in the audit trail')
  report.flows.trade = { activityId: voucherId, orderId, orderStatus: 3, auditActions: actions.length }
}

async function communityFlow(ids) {
  const shopper = await login(phones[1])
  const owner = await login(phones[0])
  const note = await api('POST', '/notes', {
    shopId: String(ids.shop),
    title: `演示笔记-${suffix}`,
    content: '到店体验很好，核销很快。',
    images: [],
  }, shopper)
  check(note.status === 200 && note.body?.success, 'a shopper can publish a note')
  const noteId = note.body.data.id
  const liked = await api('PUT', `/notes/${noteId}/like`, { liked: true }, shopper)
  check(liked.status === 200 && liked.body?.data?.liked === true && liked.body?.data?.likes === 1,
    'the note shows the expected-state like')
  const followed = await api('PUT', `/authors/${ids.shopper}/follow`, { follow: true }, owner)
  check(followed.status === 200 && followed.body?.data?.following === true, 'a user can follow an author')
  const ownerLike = await api('PUT', `/notes/${noteId}/like`, { liked: true }, owner)
  check(ownerLike.status === 200 && ownerLike.body?.data?.liked === true, 'the follower can like the note')
  const feed = await api('GET', '/feed/notes?current=1&pageSize=10', undefined, owner)
  const items = feed.body?.data?.items || []
  check(items.some(item => item.id === noteId), 'the followed note appears in the follow feed')
  check(items.some(item => item.likedByMe === true), "the feed reports the caller's own like")
  report.flows.community = { noteId, likes: liked.body.data.likes, feedItems: items.length }
}

async function aiFlow(ids) {
  const owner = await login(phones[0])
  const draft = await api('POST', '/merchant/knowledge', {
    shopId: String(ids.shop),
    title: `演示规则-${suffix}`,
    body: '核销之后不可退款，核销之前可以联系门店处理。',
  }, owner)
  check(draft.status === 200 && draft.body?.success, 'the merchant can create a FAQ draft')
  const documentId = draft.body.data.id
  const published = await api('POST', `/merchant/knowledge/${documentId}/publish`, undefined, owner)
  check(published.status === 200 && published.body?.data?.status === 'PUBLISHED', 'the FAQ can be published')
  const conversation = await api('POST', '/ai/conversations', { shopId: String(ids.shop) }, owner)
  check(conversation.status === 200 && conversation.body?.data?.id, 'a conversation can be opened')
  const conversationId = conversation.body.data.id
  const response = await fetch(`${API}/ai/conversations/${conversationId}/messages`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', authorization: owner },
    body: JSON.stringify({ clientMessageId: `demo-${suffix}`, shopId: String(ids.shop), message: '核销之后可以退款吗？' }),
  })
  const text = await response.text()
  const events = {}
  for (const block of text.split('\n\n')) {
    const name = block.match(/^event: (.+)$/m)
    const data = block.match(/^data: (.+)$/m)
    if (name && data) events[name[1]] = JSON.parse(data[1])
  }
  check(events.meta !== undefined, 'the turn starts with a meta event')
  check(Array.isArray(events.sources) && events.sources.length > 0, 'the answer cites published sources')
  check(typeof events.answer?.text === 'string' && events.answer.text.length > 0, 'the turn answers')
  check(events.done !== undefined, 'the turn closes with a done event')
  report.flows.ai = { documentId, conversationId, sources: events.sources.length }
}

const flow = process.argv[2] || 'all'
const ids = seedMerchant()
try {
  if (flow === 'trade' || flow === 'all') await tradeFlow(ids)
  if (flow === 'community' || flow === 'all') await communityFlow(ids)
  if (flow === 'ai' || flow === 'all') await aiFlow(ids)
  report.passed = true
} catch (error) {
  report.error = String(error.message)
} finally {
  cleanupFixtures(ids)
}
await writeFile(output, JSON.stringify(report, null, 2) + '\n', 'utf8')
console.log(JSON.stringify({ passed: report.passed, flows: report.flows, checks: report.checks.length, error: report.error || null }))
process.exit(report.passed ? 0 : 1)
