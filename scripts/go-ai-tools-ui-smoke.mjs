// Deterministic Go AITool business paths through the actual Vue SSE reader.
// No real Provider configuration, successful API mocks, authenticated trace/HAR.
import assert from "node:assert/strict";
import { randomBytes, randomInt } from "node:crypto";
import { spawnSync } from "node:child_process";
import { mkdir, writeFile } from "node:fs/promises";
import { createConnection } from "node:net";
import { dirname, resolve } from "node:path";
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
])
  if (!process.env[key]) throw new Error(`Explicit isolated ${key} required`);
const API = process.env.API_BASE.replace(/\/$/, ""),
  WEB = process.env.WEB_BASE.replace(/\/$/, "");
const output = resolve(process.env.UI_EVIDENCE || "ai-tools-ui.json");
await mkdir(dirname(output), { recursive: true });
const prefix = "GO074T" + randomBytes(5).toString("hex");
const base = 9007199254740993n + BigInt(randomInt(70000000, 80000000)) * 100n;
const owner = String(base),
  buyer = String(base + 1n),
  merchant = String(base + 2n),
  shop = String(base + 3n);
const shopName = `${prefix}-门店`,
  address = `桂花路88号合成地址-${prefix}`,
  title = `${prefix}-活动`;
const phones = [0, 1].map(() => `138${randomInt(10000000, 100000000)}`);
const tokens = [],
  conversations = new Set(),
  turns = [];
let browser,
  page,
  seeded = false,
  seedAttempted = false,
  voucher,
  order,
  activeCase = "",
  phase = "preflight";
const report = {
  task: "GO-074c",
  passed: false,
  cleanup: false,
  checks: [],
  turns: [],
  providerBoundary:
    "Configured stub only; all questions use actual Go AITool early-return paths, no knowledge publication/retrieval/generation or real Provider calls. This is not model-quality evidence.",
};
function check(value, message) {
  assert.ok(value, message);
  report.checks.push(message);
}
function canonical(value, maximum = 18446744073709551615n) {
  return (
    typeof value === "string" &&
    /^[1-9]\d{0,19}$/.test(value) &&
    BigInt(value) <= maximum
  );
}
function canonicalSource(value) {
  return (
    typeof value === "string" &&
    /^(?:0|[1-9]\d*)-(?:0|[1-9]\d*)$/.test(value) &&
    value.split("-").every((part) => BigInt(part) <= 18446744073709551615n)
  );
}
function sql(query) {
  const result = spawnSync(
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
      input: `SET NAMES utf8mb4; SET time_zone='+08:00'; ${query}`,
      encoding: "utf8",
      timeout: 15000,
      env: { ...process.env, MYSQL_PWD: process.env.MYSQL_PASSWORD },
    },
  );
  if (result.error || result.status !== 0)
    throw new Error("Owned SQL failed; diagnostics omitted");
  return result.stdout.trim();
}
async function api(path, who = 1, method = "GET", body, key) {
  const response = await fetch(API + path, {
    method,
    signal: AbortSignal.timeout(30000),
    headers: {
      "Content-Type": "application/json",
      ...(tokens[who] ? { authorization: tokens[who] } : {}),
      ...(key ? { "Idempotency-Key": key } : {}),
    },
    ...(body ? { body: JSON.stringify(body) } : {}),
  });
  const payload = await response.json();
  if (!response.ok || !payload.success)
    throw new Error("Actual API request failed");
  return payload.data;
}
async function until(read, accept, message, timeout = 45000) {
  const end = Date.now() + timeout;
  while (Date.now() < end) {
    const value = await read();
    if (accept(value)) return value;
    await new Promise((resolveWait) => setTimeout(resolveWait, 100));
  }
  throw new Error(message);
}
async function redis(...args) {
  const commands = [
    ...(process.env.REDIS_PASSWORD
      ? [["AUTH", process.env.REDIS_PASSWORD]]
      : []),
    ["SELECT", process.env.REDIS_DATABASE || "0"],
    args,
  ];
  return new Promise((resolveReply, reject) => {
    const socket = createConnection({
      host: process.env.REDIS_HOST,
      port: Number(process.env.REDIS_PORT),
    });
    let buffer = Buffer.alloc(0),
      replies = 0,
      settled = false;
    const fail = () => {
      if (!settled) {
        settled = true;
        socket.destroy();
        reject(new Error("Owned Redis command failed"));
      }
    };
    function parse(offset) {
      if (offset >= buffer.length) return null;
      const end = buffer.indexOf("\r\n", offset);
      if (end < 0) return null;
      const type = buffer[offset],
        line = buffer.subarray(offset + 1, end).toString();
      let next = end + 2;
      if (type === 45) throw new Error("Redis error");
      if (type === 43 || type === 58)
        return { value: type === 43 ? line : Number(line), next };
      if (type === 36) {
        const length = Number(line);
        if (length === -1) return { value: null, next };
        return buffer.length < next + length + 2
          ? null
          : {
              value: buffer.subarray(next, next + length).toString(),
              next: next + length + 2,
            };
      }
      if (type === 42) {
        const count = Number(line),
          value = [];
        if (count === -1) return { value: null, next };
        for (let i = 0; i < count; i++) {
          const child = parse(next);
          if (!child) return null;
          value.push(child.value);
          next = child.next;
        }
        return { value, next };
      }
      throw new Error("Unexpected Redis shape");
    }
    socket.setTimeout(5000, fail).on("error", fail);
    socket.on("close", () => {
      if (!settled) fail();
    });
    socket.on("connect", () =>
      socket.write(
        commands
          .map(
            (command) =>
              `*${command.length}\r\n${command
                .map((arg) => {
                  const text = String(arg);
                  return `$${Buffer.byteLength(text)}\r\n${text}\r\n`;
                })
                .join("")}`,
          )
          .join(""),
      ),
    );
    socket.on("data", (chunk) => {
      buffer = Buffer.concat([buffer, chunk]);
      try {
        while (buffer.length) {
          const reply = parse(0);
          if (!reply) return;
          buffer = buffer.subarray(reply.next);
          if (++replies === commands.length) {
            settled = true;
            socket.destroy();
            resolveReply(reply.value);
            return;
          }
        }
      } catch {
        fail();
      }
    });
  });
}
async function keys(pattern) {
  const found = new Set();
  let cursor = "0";
  do {
    const reply = await redis("SCAN", cursor, "MATCH", pattern, "COUNT", 100);
    cursor = reply[0];
    reply[1].forEach((key) => found.add(key));
  } while (cursor !== "0");
  return [...found];
}
const pairs = (values) =>
  Object.fromEntries(
    Array.from({ length: values.length / 2 }, (_, i) => [
      values[i * 2],
      values[i * 2 + 1],
    ]),
  );
async function ownedMessages() {
  if (!voucher) return [];
  const found = [];
  let start = "-";
  for (;;) {
    const batch = await redis(
      "XRANGE",
      "stream.orders",
      start,
      "+",
      "COUNT",
      1000,
    );
    for (const [sourceId, values] of batch) {
      const fields = pairs(values);
      if (fields.voucherId === voucher) {
        if (
          !canonicalSource(sourceId) ||
          !canonical(fields.id, 9223372036854775807n) ||
          fields.userId !== buyer ||
          (order && fields.id !== order)
        )
          throw new Error("Owned Stream binding failed");
        found.push({ sourceId, orderId: fields.id });
      }
    }
    if (batch.length < 1000) return found;
    start = `(${batch.at(-1)[0]}`;
  }
}
function events(raw) {
  return raw
    .split("\n\n")
    .map((block) => {
      const name = block.match(/^event:\s*(.+)$/m)?.[1],
        text = block.match(/^data:\s*(.+)$/m)?.[1];
      if (!name) return null;
      let data;
      try {
        data = JSON.parse(text);
      } catch {
        data = null;
      }
      return { event: name, data };
    })
    .filter(Boolean);
}
function safeTurn(turn) {
  return {
    case: turn.case,
    authorizationPresent: turn.authorizationPresent,
    httpStatus: turn.httpStatus,
    events: turn.events?.map((item) => item.event),
    stages: turn.events
      ?.filter((item) => item.event === "status")
      .map((item) => item.data.stage),
    tool: turn.tool,
    answer: turn.answer,
    captureError: turn.captureError,
  };
}
async function screenshot(name) {
  await page.screenshot({
    path: output.replace(/\.json$/, "") + "-" + name + ".png",
    fullPage: true,
    mask: [page.locator("input"), page.locator("textarea")],
  });
}
class ToolsPage {
  constructor(page) {
    this.page = page;
    this.prompt = page.getByPlaceholder(
      "例如：未支付取消后还能再买吗？街角咖啡停车怎么收费？",
    );
    this.send = page.getByRole("button", { name: "发送", exact: true });
    this.last = () => page.getByRole("article").last();
  }
  async ask(label, question) {
    activeCase = label;
    const before = turns.length;
    await this.prompt.fill(question);
    await this.send.click();
    await until(
      () => turns.length,
      (size) => size === before + 1,
      "Actual tool SSE request not observed",
    );
    const turn = turns.at(-1);
    await until(() => turn.finished, Boolean, "Actual tool SSE did not finish");
    await until(
      () =>
        this.page.getByRole("button", { name: "停止", exact: true }).count(),
      (count) => count === 0,
      "Tool UI remained pending",
    );
    check(
      turn.httpStatus === 200 &&
        !turn.captureError &&
        turn.events.some((item) => item.event === "answer") &&
        turn.events.some((item) => item.event === "done"),
      "real SSE completes " + label,
    );
    check(
      turn.events
        .filter((item) => item.event === "status")
        .every((item) => item.data.stage === "tool") &&
        !turn.events.some((item) => item.event === "sources"),
      "Go deterministic tool branch avoids knowledge/generation stages for " +
        label,
    );
    await this.last().getByText(turn.answer, { exact: true }).waitFor();
    return turn;
  }
}
try {
  const caps = await api("/ai/capabilities");
  check(
    caps.ready && caps.mode === "stub",
    "tools browser run is explicitly configured stub without a real Provider",
  );
  seedAttempted = true;
  sql(
    `START TRANSACTION; INSERT INTO tb_user(id,phone,password,nick_name,icon) VALUES(${owner},'${phones[0]}','x','${prefix}-owner',''),(${buyer},'${phones[1]}','x','${prefix}-buyer',''); INSERT INTO tb_merchant(id,name,status) VALUES(${merchant},'${prefix}','ACTIVE'); INSERT INTO tb_shop(id,merchant_id,name,type_id,address,x,y) VALUES(${shop},${merchant},'${shopName}',1,'${address}',0,0); INSERT INTO tb_merchant_member(merchant_id,user_id,role_code,status) VALUES(${merchant},${owner},'OWNER','ACTIVE'); COMMIT;`,
  );
  seeded = true;
  for (let i = 0; i < 2; i++) {
    const code = await api(`/user/code?phone=${phones[i]}`, i, "POST");
    if (code.mode !== "development")
      throw new Error("Development login required");
    tokens.push(
      await api("/user/login", i, "POST", {
        phone: phones[i],
        code: code.code,
      }),
    );
  }
  const format = new Intl.DateTimeFormat("en-CA", {
    timeZone: "Asia/Shanghai",
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hourCycle: "h23",
  });
  const at = (offset) => {
    const p = Object.fromEntries(
      format
        .formatToParts(Date.now() + offset)
        .map((part) => [part.type, part.value]),
    );
    return `${p.year}-${p.month}-${p.day}T${p.hour}:${p.minute}:${p.second}+08:00`;
  };
  phase = "actual activity publication and initial stock";
  const activity = await api(
    "/merchant/activities",
    0,
    "POST",
    {
      shopId: shop,
      title,
      subTitle: null,
      rules: null,
      payValue: "1900",
      actualValue: "2500",
      totalStock: 3,
      beginTime: at(-300000),
      endTime: at(86400000),
      redeemBeginTime: at(-300000),
      redeemEndTime: at(172800000),
    },
    prefix,
  );
  assert.match(activity.id, /^[1-9]\d*$/);
  voucher = activity.id;
  await until(
    () => api("/merchant/activities?current=1&pageSize=50", 0),
    (rows) =>
      rows.some(
        (item) =>
          item.id === voucher && item.ready && item.publishStatus === "ACTIVE",
      ),
    "Actual activity gate did not become ready",
  );
  assert.equal(
    sql(`SELECT stock FROM tb_seckill_voucher WHERE voucher_id=${voucher}`),
    "3",
  );
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
  await context.addInitScript(
    (token) => localStorage.setItem("cityloop.token", token),
    tokens[1],
  );
  page = await context.newPage();
  const pageErrors = [];
  page.on("pageerror", () => pageErrors.push(true));
  await observeSSEReader(page, {
    onStart(meta) {
      const request = JSON.parse(meta.postData),
        conversation = meta.pathname.split("/").at(-2);
      conversations.add(conversation);
      const turn = {
        case: activeCase,
        conversation,
        clientMessageId: request.clientMessageId,
        authorizationPresent: meta.authorizationPresent,
      };
      turns.push(turn);
      return turn;
    },
    onFinish(turn, text, state) {
      turn.events = events(text);
      turn.answer = turn.events.find(
        (item) => item.event === "answer",
      )?.data?.text;
      const tool = turn.events.find(
        (item) => item.event === "tool_result",
      )?.data;
      turn.tool = tool
        ? {
            type: tool.type,
            items: tool.items?.map((item) => ({
              id: item.id,
              shopId: item.shopId,
              name: item.name,
              address: item.address,
              title: item.title,
              stock: item.stock,
              payValue: item.payValue,
              href: item.href,
            })),
          }
        : null;
      turn.captureError = state.captureError;
      turn.finished = true;
    },
  });
  page.on("response", async (response) => {
    if (
      response.request().method() === "POST" &&
      new URL(response.url()).pathname === "/api/ai/conversations"
    ) {
      const body = await response.json().catch(() => null);
      if (body?.data?.id) conversations.add(body.data.id);
    }
  });
  await page.goto(WEB + "/assistant?shopId=" + shop);
  let ui = new ToolsPage(page);
  await ui.prompt.waitFor();
  const first = await ui.ask(
    "activity-stock-3",
    `活动 ${voucher} 的库存和价格`,
  );
  check(
    first.tool?.type === "activities" &&
      first.tool.items.length === 1 &&
      first.tool.items[0].stock === "3" &&
      first.tool.items[0].payValue === "1900" &&
      first.tool.items[0].href === "/shops/" + shop,
    "actual activity card has current string stock3, exact cents and its shop link",
  );
  await ui.last().getByText("库存 3", { exact: true }).waitFor();
  await ui.last().getByText("¥19.00", { exact: true }).waitFor();
  await screenshot("stock-3");
  phase =
    "real admission and Stream order then same-conversation stock refresh";
  order = await api(`/voucher-order/seckill/${voucher}`, 1, "POST");
  assert.match(order, /^[1-9]\d*$/);
  await until(
    () => api(`/voucher-order/${order}`),
    (row) => row.state === "CREATED" && row.orderStatus === 1,
    "Actual Stream order did not commit",
  );
  check(
    sql(`SELECT stock FROM tb_seckill_voucher WHERE voucher_id=${voucher}`) ===
      "2" && (await redis("GET", `seckill:stock:${voucher}`)) === "2",
    "real order admission and persistence reduce MySQL and Redis inventory from3 to2",
  );
  const second = await ui.ask(
    "activity-stock-2",
    `再查活动 ${voucher} 的库存和价格`,
  );
  check(
    first.conversation === second.conversation &&
      first.clientMessageId !== second.clientMessageId &&
      second.tool.items[0].stock === "2" &&
      second.tool.items[0].payValue === "1900",
    "same conversation uses a new message and refreshes string activity stock2 without replaying stock3",
  );
  report.sameConversationRefresh = true;
  await ui.last().getByText("库存 2", { exact: true }).waitFor();
  await screenshot("stock-2");
  const activityLink = ui.last().getByRole("link", { name: new RegExp(title) });
  check(
    (await activityLink.getAttribute("href")) === "/shops/" + shop,
    "activity tool detail href preserves the exact shop string ID",
  );
  await activityLink.click();
  await page.waitForURL((url) => url.pathname === "/shops/" + shop);
  await page.getByRole("heading", { name: shopName, exact: true }).waitFor();
  check(true, "actual activity card click opens the associated shop detail");
  phase = "shops card actual DTO and detail";
  await page.goto(WEB + "/assistant?shopId=" + shop);
  ui = new ToolsPage(page);
  await ui.prompt.waitFor();
  const store = await ui.ask("shop-card", `门店 ${shop} 的地址`);
  check(
    store.tool?.type === "shops" &&
      store.tool.items[0].id === shop &&
      store.tool.items[0].name === shopName &&
      store.tool.items[0].address === address,
    "actual shops DTO exposes public name/address and the exact large ID",
  );
  await ui.last().getByText(address, { exact: true }).waitFor();
  check(
    !/(?:undefined|\bNaN\b|\d+\s*分)/.test(await ui.last().innerText()),
    "shops card displays no invented score or missing fields",
  );
  const storeLink = ui.last().getByRole("link", { name: new RegExp(shopName) });
  check(
    (await storeLink.getAttribute("href")) === "/shops/" + shop,
    "missing shops DTO href falls back to its exact greater-than-2^53 shop path",
  );
  await screenshot("shop-card");
  await storeLink.click();
  await page.waitForURL((url) => url.pathname === "/shops/" + shop);
  await page.getByRole("heading", { name: shopName, exact: true }).waitFor();
  check(true, "actual shops fallback link opens the expected shop detail");
  phase = "unknown IDs have no card";
  const unknown = "18446744073709551614";
  assert.equal(
    sql(
      `SELECT (SELECT COUNT(*) FROM tb_shop WHERE id=${unknown})+(SELECT COUNT(*) FROM tb_voucher WHERE id=${unknown})`,
    ),
    "0",
  );
  await page.goto(WEB + "/assistant?shopId=" + shop);
  ui = new ToolsPage(page);
  await ui.prompt.waitFor();
  for (const kind of ["门店", "活动"]) {
    const turn = await ui.ask(
      kind === "门店" ? "unknown-shop" : "unknown-activity",
      `${kind} ${unknown}`,
    );
    check(
      turn.tool === null &&
        /没有找到/.test(turn.answer) &&
        (await ui.last().getByRole("link").count()) === 0,
      "unknown " +
        kind +
        " has an explicit deterministic refusal without a new card or link",
    );
  }
  await screenshot("unknown");
  check(
    pageErrors.length === 0,
    "tool page flow has no uncaught browser errors",
  );
  report.passed = true;
} catch (error) {
  report.failure = {
    phase,
    kind: ["TimeoutError", "AssertionError", "Error"].includes(error?.name)
      ? error.name
      : "verification error",
    message: "Tool UI check failed; raw request/driver diagnostics omitted",
  };
  if (page) await screenshot("failure").catch(() => {});
  process.exitCode = 1;
} finally {
  if (browser) await browser.close().catch(() => {});
  let clean = true;
  try {
    if (seedAttempted && !seeded) {
      const owned = Number(
        sql(
          `SELECT COUNT(*) FROM tb_user WHERE id IN(${owner},${buyer}) AND nick_name LIKE '${prefix}-%'`,
        ),
      );
      if (owned === 2) seeded = true;
      else if (owned !== 0)
        throw new Error("Unknown fixture ownership outcome");
    }
    if (seeded) {
      await until(
        async () => (await keys(`ai:lock:${buyer}:*`)).length,
        (count) => count === 0,
        "Owned AI lease remained active",
      );
      const catalog = sql(`SELECT id FROM tb_voucher WHERE shop_id=${shop}`)
        .split(/\s+/)
        .filter(Boolean);
      if (
        catalog.length > 1 ||
        (voucher && catalog.some((id) => id !== voucher))
      )
        throw new Error("Unexpected owned catalog binding");
      if (!voucher && catalog.length === 1) voucher = catalog[0];
      if (voucher && !order) {
        const facts = sql(
          `SELECT id FROM tb_voucher_order WHERE voucher_id=${voucher} AND user_id=${buyer}`,
        )
          .split(/\s+/)
          .filter(Boolean);
        if (facts.length > 1)
          throw new Error("Unexpected owned order multiplicity");
        if (facts.length === 1) order = facts[0];
        const accepted = await redis(
          "HGET",
          `seckill:accepted:${voucher}`,
          buyer,
        );
        if (accepted && order && accepted !== order)
          throw new Error("Accepted order binding changed");
        if (accepted && !order) order = accepted;
      }
      const messages = await ownedMessages();
      if (messages.length > 1)
        throw new Error("Unexpected owned Stream multiplicity");
      if (!order && messages.length === 1) order = messages[0].orderId;
      if (
        (voucher && !canonical(voucher)) ||
        (order && !canonical(order, 9223372036854775807n))
      )
        throw new Error("Invalid exact owned identifier");
      await until(
        async () => {
          if (
            order &&
            sql(
              `SELECT COUNT(*) FROM tb_voucher_order WHERE id=${order} AND user_id=${buyer} AND voucher_id=${voucher}`,
            ) !== "1"
          )
            return false;
          if (
            voucher &&
            sql(
              `SELECT COUNT(*) FROM tb_outbox WHERE ((aggregate_type='ACTIVITY' AND aggregate_id='${voucher}') OR (aggregate_type IN('ORDER','RESERVATION') AND aggregate_id='${order || "0"}')) AND delivery_status<>'DONE'`,
            ) !== "0"
          )
            return false;
          if (
            order &&
            sql(
              `SELECT COUNT(*) FROM tb_order_review WHERE order_id='${order}' AND status<>'RESOLVED'`,
            ) !== "0"
          )
            return false;
          for (const message of messages)
            if (
              (
                await redis(
                  "XPENDING",
                  "stream.orders",
                  "g1",
                  message.sourceId,
                  message.sourceId,
                  1,
                )
              ).length
            )
              return false;
          return true;
        },
        Boolean,
        "Owned trade worker barrier not reached",
      );
      report.workerBarrier = {
        ownedMessages: messages.length,
        pending: 0,
        unfinishedOutbox: 0,
        unresolvedReview: 0,
      };
      for (const cid of conversations) {
        await fetch(API + "/ai/conversations/" + cid, {
          method: "DELETE",
          headers: { authorization: tokens[1] },
          signal: AbortSignal.timeout(10000),
        });
      }
      const eventIDs = sql(
        `SELECT event_id FROM tb_outbox WHERE (aggregate_type='ACTIVITY' AND aggregate_id='${voucher || "0"}') OR (aggregate_type IN('ORDER','RESERVATION') AND aggregate_id='${order || "0"}')`,
      )
        .split(/\s+/)
        .filter(Boolean);
      sql(
        `START TRANSACTION; DELETE FROM tb_order_review WHERE order_id='${order || "0"}'; DELETE FROM tb_outbox WHERE (aggregate_type='ACTIVITY' AND aggregate_id='${voucher || "0"}') OR (aggregate_type IN('ORDER','RESERVATION') AND aggregate_id='${order || "0"}'); DELETE FROM tb_audit_log WHERE merchant_id=${merchant}; DELETE FROM tb_voucher_order WHERE id=${order || "0"} AND user_id=${buyer}; DELETE FROM tb_seckill_voucher WHERE voucher_id=${voucher || "0"}; DELETE FROM tb_voucher WHERE id=${voucher || "0"} AND shop_id=${shop}; DELETE FROM tb_merchant_member WHERE merchant_id=${merchant} AND user_id=${owner}; DELETE FROM tb_shop WHERE id=${shop} AND merchant_id=${merchant}; DELETE FROM tb_merchant WHERE id=${merchant}; DELETE FROM tb_user WHERE id IN(${owner},${buyer}); COMMIT;`,
      );
      for (const message of messages)
        await redis("XDEL", "stream.orders", message.sourceId);
      const ownedKeys = [
        ...(voucher
          ? ["gate", "stock", "begin", "end", "order", "accepted"].map(
              (part) => `seckill:${part}:${voucher}`,
            )
          : []),
        ...(order ? [`order:reservation:${order}`] : []),
        ...eventIDs.map((id) => `comp:done:${id}`),
      ];
      for (const user of [owner, buyer])
        for (const type of ["conv", "msgs", "lock", "dedupe", "rate", "budget"])
          ownedKeys.push(...(await keys(`ai:${type}:${user}:*`)));
      ownedKeys.push(
        ...tokens.map((token) => "login:token:" + token),
        ...phones.flatMap((phone) => [
          "login:code:" + phone,
          "login:code:throttle:" + phone,
        ]),
      );
      if (ownedKeys.length) {
        await redis("DEL", ...ownedKeys);
        if ((await redis("EXISTS", ...ownedKeys)) !== 0) clean = false;
      }
      clean &&=
        sql(
          `SELECT (SELECT COUNT(*) FROM tb_user WHERE id IN(${owner},${buyer}))+(SELECT COUNT(*) FROM tb_shop WHERE id=${shop})+(SELECT COUNT(*) FROM tb_voucher WHERE id=${voucher || "0"})+(SELECT COUNT(*) FROM tb_voucher_order WHERE id=${order || "0"})`,
        ) === "0" && (await ownedMessages()).length === 0;
    }
  } catch {
    clean = false;
    report.cleanupFailure =
      "Owned cleanup barrier or exact deletion failed; retain safe fixture IDs for controlled recovery";
  }
  report.fixture = { owner, buyer, merchant, shop, voucher, order };
  report.turns = turns.map(safeTurn);
  report.cleanup = clean;
  report.recordedAt = new Date().toISOString();
  if (!clean) {
    report.passed = false;
    process.exitCode = 1;
  }
  await writeFile(output, JSON.stringify(report, null, 2) + "\n");
  process.stdout.write(
    JSON.stringify({
      passed: report.passed,
      checks: report.checks.length,
      cleanup: clean,
      output,
    }) + "\n",
  );
}
