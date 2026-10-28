// GO-074b: bounded real Provider + real Vue browser delta. No authenticated
// Playwright trace is saved. Setup credentials stay in this process only.
import assert from "node:assert/strict";
import { randomBytes, randomInt } from "node:crypto";
import { spawnSync } from "node:child_process";
import { createConnection } from "node:net";
import { mkdir, readdir, writeFile } from "node:fs/promises";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";
import { observeSSEReader } from "./lib/sse-reader-observer.mjs";

for (const key of [
  "API_BASE",
  "WEB_BASE",
  "MYSQL_HOST",
  "MYSQL_PORT",
  "MYSQL_DATABASE",
  "MYSQL_USER",
  "MYSQL_PASSWORD",
  "REDIS_HOST",
  "REDIS_PORT",
  "PLAYWRIGHT_MODULE",
  "CHROME_PATH",
  "AI_UI_OBSERVER_URL",
  "AI_UI_EVIDENCE_DIR",
])
  assert.ok(process.env[key], `Explicit isolated ${key} required`);
const API = process.env.API_BASE.replace(/\/$/, ""),
  WEB = process.env.WEB_BASE.replace(/\/$/, ""),
  OBS = process.env.AI_UI_OBSERVER_URL;
const observerBaseline = Number(process.env.AI_UI_OBSERVER_BASELINE || "0");
assert.ok(
  [0, 3].includes(observerBaseline),
  "explicit reviewed observer baseline must be 0 or 3",
);
for (const [raw, port] of [
  [API, "8121"],
  [WEB, "5221"],
  [OBS, "9291"],
]) {
  const u = new URL(raw);
  assert.ok(
    u.protocol === "http:" &&
      u.hostname === "127.0.0.1" &&
      u.port === port &&
      !u.username &&
      !u.password &&
      !u.search &&
      !u.hash,
    "exact dedicated loopback endpoint required",
  );
}
assert.equal(process.env.MYSQL_HOST, "127.0.0.1");
assert.equal(process.env.MYSQL_PORT, "3381");
assert.equal(process.env.REDIS_HOST, "127.0.0.1");
assert.equal(process.env.REDIS_PORT, "6451");
const out = resolve(process.env.AI_UI_EVIDENCE_DIR);
assert.equal(
  (
    await readdir(out).catch((e) => {
      if (e.code === "ENOENT") return [];
      throw e;
    })
  ).length,
  0,
  "retain previous evidence; use a new directory",
);
await mkdir(out, { recursive: true });
const suffix = randomBytes(4).toString("hex"),
  prefix = "GO074AI-" + suffix;
const base = 9007199254740993n + BigInt(randomInt(60000000, 70000000)) * 100n;
const users = [0n, 1n, 2n].map((n) => String(base + n)),
  [owner, reader, other] = users,
  merchant = String(base + 3n),
  shop = String(base + 4n),
  voucher = String(base + 5n),
  ownOrder = String(base + 6n),
  otherOrder = String(base + 7n);
const phones = users.map(() => `139${randomInt(10000000, 100000000)}`),
  tokens = [],
  issuedPhones = [],
  documents = [],
  conversations = [],
  requests = [],
  failures = [];
const title = prefix + " 晴川咖啡停车规则",
  publicTitle = prefix + " 平台公开规则",
  body =
    "晴川咖啡的公开停车规则：消费顾客可在东侧停车场免费停车两小时，超过两小时每小时收五元。";
const report = {
  task: "GO-074b",
  passed: false,
  cleanup: false,
  checks: [],
  turns: [],
  fixture: {
    users,
    merchant,
    shop,
    voucher,
    orders: [ownOrder, otherOrder],
    documents,
  },
  screenshots: [],
  provider: {
    baselineCalls: observerBaseline,
    maxCalls: 12,
    maxObservedTokens: 20000,
    cost: "unknown until observed usage is reviewed; cancelled settlement may be unknown",
  },
  instrumentation:
    "Test-only observer attaches read callbacks to bytes consumed by Vue; original fetch/read promises, Response, reader and read results are preserved. No tee or extra read; small callback overhead is present.",
  failures,
};
let browser,
  page,
  seeded = false,
  setupAttempted = false,
  loginPending = false,
  phase = "preflight",
  activeCase = "",
  requestSerial = 0,
  lastConversation = "",
  sourceDocument = "";
const started = Date.now(),
  deadline = started + 8 * 60_000;
function check(value, message) {
  assert.ok(value, message);
  report.checks.push(message);
}
function sql(statement) {
  const r = spawnSync(
    process.env.MYSQL_BIN || "mysql",
    [
      "--protocol=TCP",
      `--host=${process.env.MYSQL_HOST}`,
      `--port=${process.env.MYSQL_PORT}`,
      `--user=${process.env.MYSQL_USER}`,
      "--default-character-set=utf8mb4",
      "--batch",
      "--skip-column-names",
      process.env.MYSQL_DATABASE,
    ],
    {
      input: `SET NAMES utf8mb4; SET time_zone='+08:00'; ${statement}`,
      encoding: "utf8",
      env: { ...process.env, MYSQL_PWD: process.env.MYSQL_PASSWORD },
      timeout: 15000,
    },
  );
  if (r.error || r.status !== 0)
    throw new Error("Owned SQL failed; diagnostics omitted");
  return r.stdout.trim();
}
async function api(
  path,
  { who = 1, method = "GET", body: payload, allowFailure = false } = {},
) {
  const r = await fetch(API + path, {
    method,
    headers: {
      "Content-Type": "application/json",
      ...(tokens[who] ? { authorization: tokens[who] } : {}),
    },
    signal: AbortSignal.timeout(50000),
    ...(payload ? { body: JSON.stringify(payload) } : {}),
  });
  const b = await r.json();
  if (!allowFailure) assert.ok(r.ok && b.success, "real API request failed");
  return { status: r.status, body: b };
}
async function until(condition, message, timeout = 30000, cleanup = false) {
  const end = cleanup
    ? Date.now() + timeout
    : Math.min(deadline, Date.now() + timeout);
  while (Date.now() < end) {
    if (await condition()) return;
    await new Promise((r) => setTimeout(r, 50));
  }
  throw new Error(message);
}
function parseRESP(buffer, start = 0) {
  if (start >= buffer.length) return;
  const end = buffer.indexOf("\r\n", start);
  if (end < 0) return;
  const type = String.fromCharCode(buffer[start]),
    head = buffer.toString("utf8", start + 1, end);
  let at = end + 2;
  if (type === "+") return { value: head, next: at };
  if (type === "-") throw new Error("Owned Redis operation failed");
  if (type === ":") return { value: Number(head), next: at };
  const length = Number(head);
  if (type === "$") {
    if (length === -1) return { value: null, next: at };
    if (buffer.length < at + length + 2) return;
    return {
      value: buffer.toString("utf8", at, at + length),
      next: at + length + 2,
    };
  }
  if (type === "*") {
    const values = [];
    for (let i = 0; i < length; i++) {
      const child = parseRESP(buffer, at);
      if (!child) return;
      values.push(child.value);
      at = child.next;
    }
    return { value: values, next: at };
  }
  throw new Error("Unexpected Redis protocol");
}
function redis(command) {
  return new Promise((resolveResult, reject) => {
    const socket = createConnection({
      host: process.env.REDIS_HOST,
      port: Number(process.env.REDIS_PORT),
    });
    const commands = [
      ...(process.env.REDIS_PASSWORD
        ? [["AUTH", process.env.REDIS_PASSWORD]]
        : []),
      ["SELECT", process.env.REDIS_DATABASE || "0"],
      command,
    ];
    let buffer = Buffer.alloc(0),
      count = 0,
      done = false;
    const finish = (error, value) => {
      if (done) return;
      done = true;
      socket.destroy();
      error
        ? reject(new Error("Owned Redis operation failed"))
        : resolveResult(value);
    };
    socket.setTimeout(5000, () => finish(true));
    socket.once("error", () => finish(true));
    socket.once("connect", () =>
      socket.write(
        commands
          .map(
            (args) =>
              `*${args.length}\r\n${args.map((v) => `$${Buffer.byteLength(String(v))}\r\n${v}\r\n`).join("")}`,
          )
          .join(""),
      ),
    );
    socket.on("data", (chunk) => {
      buffer = Buffer.concat([buffer, chunk]);
      try {
        for (;;) {
          const parsed = parseRESP(buffer);
          if (!parsed) break;
          buffer = buffer.subarray(parsed.next);
          if (++count === commands.length) return finish(false, parsed.value);
        }
      } catch {
        finish(true);
      }
    });
  });
}
async function keysFor(pattern) {
  const keys = new Set();
  let cursor = "0";
  do {
    const next = await redis([
      "SCAN",
      cursor,
      "MATCH",
      pattern,
      "COUNT",
      "100",
    ]);
    cursor = next[0];
    next[1].forEach((key) => keys.add(key));
  } while (cursor !== "0");
  return [...keys];
}
async function budget() {
  const keys = await keysFor(`ai:budget:${reader}:*`);
  let value = 0;
  for (const key of keys) value += Number((await redis(["GET", key])) || 0);
  return value;
}
async function observer() {
  const r = await fetch(OBS, { signal: AbortSignal.timeout(3000) });
  assert.ok(r.ok, "trusted observer unavailable");
  const data = await r.json();
  const rows = Array.isArray(data) ? data : data.requests;
  assert.ok(Array.isArray(rows), "trusted observer schema invalid");
  return rows;
}
function normalizedObservation(row) {
  const usage = row.usage;
  const input = usage?.prompt_tokens ?? usage?.inputTokens,
    output =
      usage?.completion_tokens ??
      usage?.outputTokens ??
      (row.operation === "embedding" ? 0 : undefined),
    total = usage?.total_tokens ?? usage?.totalTokens;
  const known =
    Number.isSafeInteger(input) &&
    Number.isSafeInteger(output) &&
    Number.isSafeInteger(total) &&
    input >= 0 &&
    output >= 0 &&
    input + output === total;
  return {
    id: row.id,
    operation: row.operation,
    model: row.model,
    status: row.status,
    clientCancelled: !!row.clientCancelled,
    usage: known
      ? { known, inputTokens: input, outputTokens: output, totalTokens: total }
      : { known: false },
  };
}
async function withinBudget() {
  const rows = await observer(),
    safe = rows.map(normalizedObservation);
  assert.ok(safe.length <= 12, "bounded paid request ceiling exceeded");
  assert.ok(
    safe.reduce((n, r) => n + (r.usage.known ? r.usage.totalTokens : 0), 0) <=
      20000,
    "observed token ceiling exceeded",
  );
  return rows;
}
function parseEvents(raw) {
  const result = [];
  for (const block of raw.split("\n\n")) {
    const name = block.match(/^event:\s*(.+)$/m)?.[1],
      text = block.match(/^data:\s*(.+)$/m)?.[1];
    if (!name) continue;
    let data;
    try {
      data = JSON.parse(text);
    } catch {
      data = null;
    }
    result.push({ event: name, data });
  }
  return result;
}
function safeTurn(turn) {
  return {
    case: turn.case,
    method: "POST",
    authorizationPresent: turn.authorizationPresent,
    httpStatus: turn.httpStatus,
    events: turn.events?.map((e) => e.event) || [],
    answer:
      turn.answer
        ?.replaceAll(ownOrder, "{ownOrder}")
        .replaceAll(otherOrder, "{otherOrder}") || "",
    sources: turn.sources || [],
    tool: turn.tool || null,
    readFailed: !!turn.readFailed,
    captureError: !!turn.captureError,
    durationMs: turn.durationMs,
  };
}
async function screenshot(name) {
  const file = name + ".png";
  await page.screenshot({ path: resolve(out, file), fullPage: true });
  report.screenshots.push(file);
}
class AssistantPage {
  constructor(page) {
    this.page = page;
    this.prompt = page.getByPlaceholder(
      "例如：未支付取消后还能再买吗？街角咖啡停车怎么收费？",
    );
    this.send = page.getByRole("button", { name: "发送", exact: true });
    this.stop = page.getByRole("button", { name: "停止", exact: true });
  }
  async ask(label, text) {
    await withinBudget();
    activeCase = label;
    const before = requests.length;
    await this.prompt.fill(text);
    await this.send.click();
    await until(
      () => requests.length === before + 1,
      "browser did not issue exact authenticated turn",
    );
    const turn = requests.at(-1);
    await until(() => turn.finished, "actual SSE did not finish", 55000);
    await until(
      async () => !(await this.stop.count()),
      "UI did not leave loading state",
    );
    check(
      turn.httpStatus === 200 &&
        !turn.captureError &&
        turn.events?.some((e) => e.event === "answer") &&
        turn.events?.some((e) => e.event === "done"),
      "actual SSE completes " + label,
    );
    await this.last().getByText(turn.answer, { exact: true }).waitFor();
    check(true, "Vue renders completed answer " + label);
    return turn;
  }
  // Conversation messages are chronological; the final article is deliberately
  // selected because each send appends the new assistant article last.
  last() {
    return this.page.getByRole("article").last();
  }
}

try {
  const baseline = await observer();
  check(
    baseline.length === observerBaseline &&
      !baseline.some((row) => row.status === "pending"),
    "bounded journal matches explicit settled continuation baseline",
  );
  report.provider.baselineObservations = baseline.map(normalizedObservation);
  setupAttempted = true;
  sql(
    `START TRANSACTION; INSERT INTO tb_user(id,phone,nick_name) VALUES ${users.map((id, i) => `(${id},'${phones[i]}','${prefix}-${i}')`).join(",")}; INSERT INTO tb_merchant(id,name,status) VALUES(${merchant},'${prefix}','ACTIVE'); INSERT INTO tb_shop(id,merchant_id,name,type_id,address,x,y) VALUES(${shop},${merchant},'晴川咖啡-${prefix}',1,'合成地址',0,0); INSERT INTO tb_merchant_member(merchant_id,user_id,role_code,status) VALUES(${merchant},${owner},'OWNER','ACTIVE'); INSERT INTO tb_voucher(id,shop_id,title,pay_value,actual_value,type,status) VALUES(${voucher},${shop},'${prefix}',2500,3000,1,1); INSERT INTO tb_voucher_order(id,user_id,voucher_id,shop_id,pay_type,pay_amount,status,pay_deadline_at) VALUES(${ownOrder},${reader},${voucher},${shop},1,2500,1,NOW()+INTERVAL 2 HOUR),(${otherOrder},${other},${voucher},${shop},1,3000,2,NOW()+INTERVAL 2 HOUR); COMMIT;`,
  );
  seeded = true;
  for (let i = 0; i < users.length; i++) {
    issuedPhones.push(phones[i]);
    const code = (
      await api(`/user/code?phone=${phones[i]}`, { who: i, method: "POST" })
    ).body.data;
    assert.equal(code.mode, "development");
    loginPending = true;
    const logged = (
      await api("/user/login", {
        who: i,
        method: "POST",
        body: { phone: phones[i], code: code.code },
      })
    ).body;
    assert.equal(typeof logged.data, "string");
    tokens.push(logged.data);
    loginPending = false;
  }
  const caps = (await api("/ai/capabilities")).body.data;
  check(
    caps.ready && caps.mode === "openai-compatible",
    "actual capabilities identify configured real Provider",
  );
  report.capabilities = {
    mode: caps.mode,
    chatModel: caps.chatModel,
    embeddingModel: caps.embeddingModel,
    embeddingDimensions: caps.embeddingDimensions,
  };
  phase = "publish-own-public-faq";
  const draft = (
    await api("/merchant/knowledge", {
      who: 0,
      method: "POST",
      body: { shopId: shop, title, body },
    })
  ).body.data;
  documents.push(draft.id);
  sourceDocument = draft.id;
  await api(`/merchant/knowledge/${draft.id}/publish`, {
    who: 0,
    method: "POST",
  });
  // A controlled platform fixture reuses the exact same public body/vector;
  // there is no additional model input, model switch, or platform write API.
  const publicId = sql(
    `START TRANSACTION; INSERT INTO tb_ai_document(scope,title,body,status,published_version,epoch,disabled) SELECT 'PUBLIC','${publicTitle}',body,'PUBLISHED',published_version,epoch,0 FROM tb_ai_document WHERE id=${sourceDocument}; SET @public_ai_id=LAST_INSERT_ID(); INSERT INTO tb_ai_document_version(document_id,version,epoch,title,body,content_hash,status,embedding_model,embedding_dimension) SELECT @public_ai_id,version,epoch,'${publicTitle}',body,content_hash,status,embedding_model,embedding_dimension FROM tb_ai_document_version WHERE document_id=${sourceDocument} AND version=1; INSERT INTO tb_ai_chunk(document_id,version,epoch,ordinal,content,embedding,embedding_model,embedding_dimension) SELECT @public_ai_id,version,epoch,ordinal,content,embedding,embedding_model,embedding_dimension FROM tb_ai_chunk WHERE document_id=${sourceDocument} AND version=1; SELECT @public_ai_id; COMMIT;`,
  );
  assert.match(publicId, /^[1-9]\d*$/);
  documents.push(publicId);
  const module = process.env.PLAYWRIGHT_MODULE;
  const { chromium } = await import(
    /^[A-Za-z]:[\\/]/.test(module) || module.startsWith(".")
      ? pathToFileURL(resolve(module)).href
      : module
  );
  browser = await chromium.launch({
    executablePath: process.env.CHROME_PATH,
    headless: true,
  });
  const context = await browser.newContext({
    viewport: { width: 1360, height: 1000 },
    timezoneId: "Asia/Shanghai",
  });
  page = await context.newPage();
  page.setDefaultTimeout(25000);
  // Never start authenticated tracing: Playwright network traces retain headers.
  const browserErrors = [];
  page.on("pageerror", () => browserErrors.push("uncaught browser error"));
  await observeSSEReader(page, {
    onStart(meta) {
      const payload = JSON.parse(meta.postData);
      lastConversation = meta.pathname.split("/").at(-2);
      const turn = {
        serial: ++requestSerial,
        case: activeCase,
        conversation: lastConversation,
        clientMessageId: payload.clientMessageId,
        authorizationPresent: meta.authorizationPresent,
        started: Date.now(),
        finished: false,
      };
      requests.push(turn);
      return turn;
    },
    onFinish(turn, text, state) {
      turn.events = parseEvents(text);
      turn.answer = turn.events.find((e) => e.event === "answer")?.data?.text;
      turn.sources = turn.events
        .find((e) => e.event === "sources")
        ?.data?.map((s) => ({
          documentId: s.documentId,
          title: s.title,
          scope: s.scope,
          version: s.version,
          href: s.href,
          excerpt: s.excerpt,
        }));
      const tool = turn.events.find((e) => e.event === "tool_result")?.data;
      if (tool)
        turn.tool = {
          type: tool.type,
          status: tool.status,
          payAmount: tool.payAmount,
          count: tool.items?.length,
          id: tool.id === ownOrder ? "{ownOrder}" : undefined,
        };
      turn.readFailed = state.loadingFailed;
      turn.captureError = state.captureError;
      turn.durationMs = Date.now() - turn.started;
      turn.finished = true;
    },
  });
  page.on("response", async (response) => {
    const path = new URL(response.url()).pathname;
    // Normal JSON metadata only, never a second reader for an SSE response.
    if (
      response.request().method() === "POST" &&
      path === "/api/ai/conversations"
    ) {
      const result = await response.json().catch(() => null);
      if (result?.data?.id) {
        lastConversation = result.data.id;
        conversations.push(result.data.id);
      }
    }
  });
  phase = "real-assistant-empty-and-sources";
  await page.goto(WEB);
  await page.evaluate(
    (token) => localStorage.setItem("cityloop.token", token),
    tokens[1],
  );
  await page.goto(WEB + "/assistant?shopId=" + shop);
  const ui = new AssistantPage(page);
  await ui.prompt.waitFor();
  await page
    .getByText("问门店规则、查本人订单，或了解当前活动", { exact: true })
    .waitFor();
  const countBefore = requests.length;
  await ui.prompt.fill("   ");
  await ui.send.click();
  check(
    requests.length === countBefore,
    "blank input keeps empty state and sends no model turn",
  );
  await screenshot("empty");
  const grounded = await ui.ask(
    "grounded",
    "晴川咖啡停车能免费多久，超过后每小时多少钱？",
  );
  check(
    grounded.authorizationPresent &&
      /(两|2)\s*小时/.test(grounded.answer) &&
      /(五|5)\s*元/.test(grounded.answer),
    "real model answer is grounded in two hours and five yuan",
  );
  check(
    grounded.sources.some(
      (s) =>
        s.documentId === sourceDocument &&
        s.version === 1 &&
        s.scope === "SHOP_PUBLIC",
    ),
    "current merchant-public source is returned",
  );
  await ui.last().getByText("平台规则", { exact: false }).waitFor();
  await ui.last().getByText("门店公开规则", { exact: false }).waitFor();
  check(
    true,
    "PUBLIC and SHOP_PUBLIC source cards display distinct correct labels",
  );
  await screenshot("grounded-sources");
  for (const [id, sourceTitle, scope] of [
    [sourceDocument, title, "SHOP_PUBLIC"],
    [publicId, publicTitle, "PUBLIC"],
  ]) {
    const popupPromise = context.waitForEvent("page");
    await ui
      .last()
      .getByRole("link")
      .filter({ hasText: sourceTitle })
      .click({
        modifiers: [process.platform === "darwin" ? "Meta" : "Control"],
      });
    const popup = await popupPromise;
    await popup.waitForURL(new RegExp("/ai/knowledge/" + id + "\\?version=1$"));
    await popup
      .getByRole("heading", { name: sourceTitle, exact: true })
      .waitFor();
    await popup.getByText(body, { exact: true }).waitFor();
    const detail = (await api(`/ai/knowledge/${id}?version=1`)).body.data;
    check(
      detail.scope === scope && detail.body === body,
      "actual source click opens correct public version and scope " + scope,
    );
    await popup.close();
  }
  // Remove only the controlled duplicate platform context before invalidation
  // checks, so disabling the merchant document leaves no legitimate old rule.
  sql(
    `UPDATE tb_ai_document SET disabled=1,status='DISABLED',epoch=epoch+1 WHERE id=${publicId} AND scope='PUBLIC'`,
  );
  phase = "real-read-only-tools";
  const paidBeforeTools = (await observer()).length;
  const own = await ui.ask("own-order", `查询订单 ${ownOrder}`);
  check(
    own.tool?.type === "order" &&
      own.tool.status === 1 &&
      own.tool.payAmount === "2500" &&
      /待支付/.test(own.answer),
    "own order card matches actual SQL pending status and amount",
  );
  const ownCard = ui.last().getByRole("link").filter({ hasText: ownOrder });
  await ownCard.getByText("待支付", { exact: true }).waitFor();
  await ownCard.getByText("¥25.00", { exact: true }).waitFor();
  check(true, "Vue own-order card displays current status and monetary amount");
  const foreign = await ui.ask("foreign-order", `查询订单 ${otherOrder}`);
  check(
    /没有找到/.test(foreign.answer) && !foreign.tool,
    "foreign order is hidden without private tool card",
  );
  check(
    (await ui.last().locator(".tool-card").count()) === 0,
    "Vue foreign-order rejection exposes no private tool card",
  );
  check(
    (await observer()).length === paidBeforeTools,
    "read-only order and cross-user denial make no paid provider calls",
  );
  const blocked = await api("/ai/conversations/" + lastConversation, {
    who: 2,
    allowFailure: true,
  });
  check(
    blocked.status === 404 && !blocked.body.success,
    "other user cannot read current user conversation",
  );
  await screenshot("read-only-tools");
  const unknown = await ui.ask("unknown", "晴川咖啡允许携带宠物吗？");
  check(
    /没有|未查|无法|未提供|不清楚/.test(unknown.answer),
    "unsupported pet policy is explicitly unknown",
  );
  phase = "actual-chat-stop";
  const beforeHistory = (
      await api("/ai/conversations/" + lastConversation)
    ).body.data.messages.filter((m) => m.role === "assistant").length,
    beforeBudget = await budget(),
    beforeRows = await observer();
  activeCase = "cancel";
  const beforeRequest = requests.length;
  await ui.prompt.fill(
    "请根据晴川咖啡公开停车规则完整解释免费时长和超时收费，并用三点说明注意事项。",
  );
  await ui.send.click();
  await until(
    () => requests.length === beforeRequest + 1,
    "cancel turn did not start",
  );
  const cancelled = requests.at(-1);
  let inflight;
  await until(
    async () => {
      const rows = await observer();
      inflight = rows.find(
        (row) =>
          row.operation === "chat" &&
          row.status === "pending" &&
          !beforeRows.some((old) => old.id === row.id),
      );
      return !!inflight;
    },
    "actual upstream chat was not observed in flight",
    45000,
  );
  await ui.stop.click();
  await ui.last().getByText("已停止本次查询。", { exact: true }).waitFor();
  await until(
    async () =>
      !(await redis(["EXISTS", `ai:lock:${reader}:${cancelled.conversation}`])),
    "cancelled conversation lease was not released",
  );
  await until(
    async () =>
      !(await redis([
        "EXISTS",
        `ai:dedupe:${reader}:${cancelled.conversation}:${cancelled.clientMessageId}`,
      ])),
    "cancelled message dedupe was not released",
  );
  let observedCancel;
  await until(async () => {
    observedCancel = (await observer()).find((row) => row.id === inflight.id);
    return (
      observedCancel?.clientCancelled === true &&
      observedCancel.status !== "pending"
    );
  }, "actual upstream cancellation was not observed");
  check(
    observedCancel?.clientCancelled === true,
    "Vue stop aborts a real in-flight transparent upstream request",
  );
  const afterHistory = (
    await api("/ai/conversations/" + cancelled.conversation)
  ).body.data.messages.filter((m) => m.role === "assistant").length;
  check(
    afterHistory === beforeHistory && (await budget()) === beforeBudget,
    "cancelled turn persists no assistant and refunds logical daily budget",
  );
  report.cancel = {
    observationId: inflight.id,
    assistantBefore: beforeHistory,
    assistantAfter: afterHistory,
    logicalBudgetBefore: beforeBudget,
    logicalBudgetAfter: await budget(),
    usageKnown: normalizedObservation(observedCancel).usage.known,
  };
  await screenshot("stopped");
  phase = "recovery-and-knowledge-disable";
  const recovered = await ui.ask("recovery", "晴川咖啡停车免费多长时间？");
  check(
    /(两|2)\s*小时/.test(recovered.answer) &&
      recovered.sources.some((s) => s.documentId === sourceDocument),
    "same Vue conversation successfully recovers after stop",
  );
  check(
    (
      await api("/ai/conversations/" + lastConversation)
    ).body.data.messages.filter((m) => m.role === "assistant").length ===
      beforeHistory + 1,
    "recovery adds exactly one completed assistant history message",
  );
  await api(`/merchant/knowledge/${sourceDocument}/disable`, {
    who: 0,
    method: "POST",
  });
  const disabled = await ui.ask(
    "disabled-knowledge",
    "刚才晴川咖啡停车免费的时长是多少？",
  );
  check(
    /没有|无法|未查/.test(disabled.answer) &&
      !/(两|2)\s*小时|五元|5\s*元/.test(disabled.answer) &&
      !(disabled.sources || []).some((s) => s.documentId === sourceDocument),
    "disabled FAQ does not return through old conversation memory or sources",
  );
  const stale = await api(`/ai/knowledge/${sourceDocument}?version=1`, {
    allowFailure: true,
  });
  check(
    stale.status === 404 && !stale.body.success,
    "disabled old source detail is no longer publicly readable",
  );
  await screenshot("disabled-knowledge");
  check(
    sql(
      `SELECT CONCAT((SELECT status FROM tb_voucher_order WHERE id=${ownOrder}),':',(SELECT status FROM tb_voucher_order WHERE id=${otherOrder}))`,
    ) === "1:2",
    "all assistant flows leave both business orders unchanged",
  );
  check(
    browserErrors.length === 0,
    "no uncaught browser exceptions in actual AI flow",
  );
  report.passed = true;
} catch (error) {
  failures.push({
    phase,
    kind: error?.name || "Error",
    message:
      "Verification failed; authentication and driver diagnostics omitted",
  });
  if (page) await screenshot("failure").catch(() => {});
  process.exitCode = 1;
} finally {
  if (browser) await browser.close().catch(() => {});
  const cleanupErrors = [];
  let safeCleanup = false;
  try {
    await until(
      async () => !(await observer()).some((row) => row.status === "pending"),
      "remaining upstream request did not terminate",
      45000,
      true,
    );
    report.provider.observations = (await withinBudget()).map(
      normalizedObservation,
    );
    report.provider.calls = report.provider.observations.length;
    report.provider.currentRoundCalls =
      report.provider.calls - observerBaseline;
    report.provider.knownTokens = report.provider.observations.reduce(
      (n, row) => n + (row.usage.known ? row.usage.totalTokens : 0),
      0,
    );
    report.provider.unknownUsage = report.provider.observations.filter(
      (row) => !row.usage.known,
    ).length;
  } catch {
    cleanupErrors.push("safe observation finalization failed");
  }
  await until(
    () => requests.every((request) => request.finished),
    "browser request journal did not settle",
    5000,
    true,
  ).catch(() => cleanupErrors.push("request journal incomplete"));
  report.turns = requests.map(safeTurn);
  if (loginPending)
    cleanupErrors.push(
      "login settlement unknown; exact fixture owner IDs retained for controlled session cleanup",
    );
  if (setupAttempted && !seeded)
    try {
      const owned = Number(
        sql(
          `SELECT COUNT(*) FROM tb_user WHERE id IN(${users.join(",")}) AND nick_name LIKE '${prefix}-%'`,
        ),
      );
      if (owned === 3) seeded = true;
      else if (owned !== 0)
        cleanupErrors.push(
          "fixture ownership incomplete; preserve rows for controlled recovery",
        );
    } catch {
      cleanupErrors.push("fixture commit outcome could not be checked");
    }
  if (seeded)
    try {
      await until(
        async () => {
          for (const user of users)
            if ((await keysFor(`ai:lock:${user}:*`)).length) return false;
          return true;
        },
        "own AI lease remains active",
        45000,
        true,
      );
      safeCleanup = true;
      for (const cid of new Set(conversations)) {
        const deleted = await api("/ai/conversations/" + cid, {
          method: "DELETE",
          allowFailure: true,
        });
        assert.ok([200, 404].includes(deleted.status));
      }
      for (let i = 0; i < tokens.length; i++) {
        const loggedOut = await api("/user/logout", {
          who: i,
          method: "POST",
          allowFailure: true,
        });
        assert.ok([200, 401].includes(loggedOut.status));
      }
      const keys = [];
      for (const user of users)
        for (const type of ["conv", "msgs", "lock", "dedupe", "rate", "budget"])
          keys.push(...(await keysFor(`ai:${type}:${user}:*`)));
      keys.push(
        ...tokens.map((t) => "login:token:" + t),
        ...issuedPhones.flatMap((p) => [
          "login:code:" + p,
          "login:code:throttle:" + p,
        ]),
      );
      if (keys.length) await redis(["DEL", ...new Set(keys)]);
      let aiRemaining = 0;
      for (const user of users)
        aiRemaining += (await keysFor(`ai:*:${user}:*`)).length;
      assert.equal(aiRemaining, 0);
      report.ownedAiKeysRemaining = aiRemaining;
    } catch {
      cleanupErrors.push("owned AI/session/code cleanup failed");
    }
  if (seeded && safeCleanup)
    try {
      for (const id of sql(
        `SELECT id FROM tb_ai_document WHERE shop_id=${shop} OR (scope='PUBLIC' AND title='${publicTitle}')`,
      )
        .split(/\s+/)
        .filter(Boolean)) {
        assert.match(id, /^[1-9]\d*$/);
        if (!documents.includes(id)) documents.push(id);
      }
      sql(
        `START TRANSACTION; DELETE FROM tb_ai_chunk WHERE document_id IN(SELECT id FROM tb_ai_document WHERE shop_id=${shop} OR id IN(${documents.join(",") || "0"})); DELETE FROM tb_ai_document_version WHERE document_id IN(SELECT id FROM tb_ai_document WHERE shop_id=${shop} OR id IN(${documents.join(",") || "0"})); DELETE FROM tb_ai_document WHERE shop_id=${shop} OR id IN(${documents.join(",") || "0"}); DELETE FROM tb_audit_log WHERE merchant_id=${merchant}; DELETE FROM tb_voucher_order WHERE id IN(${ownOrder},${otherOrder}) AND user_id IN(${reader},${other}); DELETE FROM tb_voucher WHERE id=${voucher} AND shop_id=${shop}; DELETE FROM tb_merchant_member WHERE merchant_id=${merchant} AND user_id=${owner}; DELETE FROM tb_shop WHERE id=${shop} AND merchant_id=${merchant}; DELETE FROM tb_merchant WHERE id=${merchant}; DELETE FROM tb_user WHERE id IN(${users.join(",")}); COMMIT;`,
      );
      report.ownedRowsRemaining = Number(
        sql(
          `SELECT (SELECT COUNT(*) FROM tb_user WHERE id IN(${users.join(",")}))+(SELECT COUNT(*) FROM tb_shop WHERE id=${shop})+(SELECT COUNT(*) FROM tb_ai_document WHERE id IN(${documents.join(",") || "0"}))+(SELECT COUNT(*) FROM tb_voucher_order WHERE id IN(${ownOrder},${otherOrder}))`,
        ),
      );
      assert.equal(report.ownedRowsRemaining, 0);
    } catch {
      cleanupErrors.push("owned SQL cleanup failed; safe fixture IDs retained");
    }
  report.cleanupErrors = cleanupErrors;
  report.cleanup = cleanupErrors.length === 0;
  if (!report.cleanup) {
    report.passed = false;
    process.exitCode = 1;
  }
  report.recordedAt = new Date().toISOString();
  report.durationMs = Date.now() - started;
  await writeFile(
    resolve(out, "result.json"),
    JSON.stringify(report, null, 2) + "\n",
    { mode: 0o600 },
  );
  console.log(
    JSON.stringify({
      passed: report.passed,
      checks: report.checks.length,
      cleanup: report.cleanup,
      phase,
    }),
  );
}
