import {
  STATUS,
  availableStock,
  initialState,
  latestOrder,
  money,
  restoreState,
  shopById,
  transition,
} from "../demo/model.js";
import { mountWorkspaceNavigation } from "./navigation.js";

const shop = shopById("coffee");
const storageKey = "fullstack-transaction-workspace-v1";
const helpKey = "fullstack-workspace-help-v1";
const pick = (id) => document.getElementById(id);
const text = (id, value) => {
  pick(id).textContent = value;
};

const stages = {
  idle: {
    progress: 0,
    actor: "顾客 · 小林",
    title: "抢一张到店券",
    copy: "提交抢券请求后，系统先预占 1 张券并返回受理编号。",
    note: "当前活动已发布，顾客可以抢券。",
    label: "抢券并受理",
    action: "BUY",
  },
  PROCESSING: {
    progress: 1,
    actor: "系统 · 异步成单",
    title: "查询这笔请求是否成单",
    copy: "已预占库存，但还没有订单。查询时可观察后台成单的模拟结果。",
    note: "已受理，订单还未生成，暂时不能支付。",
    label: "查询成单结果",
    action: "CREATE",
  },
  UNPAID: {
    progress: 2,
    actor: "顾客 · 小林",
    title: "支付这笔订单",
    copy: "订单已生成。模拟付款后，系统发给顾客一张核销凭证。",
    note: "已生成订单，等待顾客模拟支付。",
    label: `模拟支付 ¥${money(shop.price)}`,
    action: "PAY",
  },
  PAID: {
    progress: 3,
    actor: "授权店员 · 陈宁",
    title: "为到店顾客核销",
    copy: "以梧桐路店店员身份使用下方示例凭证，核对门店和订单状态后核销。",
    note: "模拟支付已完成，等待授权门店核销。",
    label: "确认到店核销",
    action: "REDEEM",
  },
  REDEEMED: {
    progress: 4,
    actor: "履约完成",
    title: "同一笔订单已到店核销",
    copy: "现在可以再发一次相同的抢券请求，核对受理编号、库存和订单数。",
    note: "门店完成核销，顾客也能看到同一订单已使用。",
    label: "重试同一请求",
    action: "BUY",
  },
};

function readWorkspace() {
  try {
    const saved = restoreState(sessionStorage.getItem(storageKey));
    if (
      saved?.shop === shop.id &&
      saved.merchantShop === shop.id &&
      saved.role === "STAFF" &&
      saved.orders.length <= 1 &&
      saved.orders.every(
        (order) =>
          order.shop === shop.id &&
          typeof order.id === "string" &&
          Object.hasOwn(stages, order.status),
      )
    ) {
      return saved;
    }
  } catch {
    // The workspace can run without browser storage.
  }
  return initialState();
}

let state = readWorkspace();
let helpSeen = false;
try {
  helpSeen = sessionStorage.getItem(helpKey) === "seen";
} catch {
  // Keep the first-visit hint in this page only.
}

const actionButton = pick("stage-action");
const guide = pick("workspace-guide");
const helpButton = pick("show-workspace-help");
const stepRows = [...pick("workspace-steps").children];

function saveWorkspace() {
  try {
    sessionStorage.setItem(storageKey, JSON.stringify(state));
  } catch {
    // The current page still retains progress.
  }
}

function getReceipt() {
  const order = latestOrder(state, shop.id);
  const status = order?.status ?? "idle";
  const created = order && status !== "PROCESSING";
  return {
    order,
    status,
    acceptance: order ? `AC-${order.id.slice(-4)}` : "未受理",
    orderId: created ? order.id : "未生成",
    orderCount: created ? 1 : 0,
    stock: availableStock(state, shop.id),
  };
}

function renderAudit() {
  const items = state.audit.map((entry) => {
    const row = document.createElement("li");
    const actor = document.createElement("strong");
    const detail = document.createElement("span");
    actor.textContent = entry.actor;
    detail.textContent = entry.text;
    row.append(actor, detail);
    return row;
  });
  const log = pick("workspace-audit-log");
  log.replaceChildren(...items);
  log.hidden = items.length === 0;
  pick("workspace-empty-audit").hidden = items.length > 0;
  text("workspace-audit-count", `${items.length} 条`);
}

function render(result = "") {
  const receipt = getReceipt();
  const { status, order, acceptance, orderId, orderCount, stock } = receipt;
  const stage = stages[status];
  text("stage-actor", stage.actor);
  text("stage-title", stage.title);
  text("stage-copy", stage.copy);
  actionButton.textContent = stage.label;
  pick("workspace-token").hidden = status !== "PAID";
  text("workspace-token-value", status === "PAID" ? order.token : "");
  pick("open-trade-lab").hidden = status !== "REDEEMED";

  text("receipt-acceptance", acceptance);
  text("receipt-order", orderId);
  text("receipt-order-count", orderCount);
  text("receipt-stock", `${stock} / ${shop.stock}`);
  text(
    "receipt-state",
    { idle: "待抢券", PROCESSING: "已受理" }[status] ?? STATUS[status],
  );
  pick("receipt-state").dataset.status = status;
  text("receipt-note", stage.note);
  text("workspace-progress", `${stage.progress} / ${stepRows.length}`);
  stepRows.forEach((row, index) => {
    const done = index < stage.progress;
    row.classList.toggle("is-done", done);
    row.querySelector(".workspace-step-number").textContent = done
      ? "✓"
      : String(index + 1);
    const isCurrent = index === stage.progress;
    if (isCurrent) row.setAttribute("aria-current", "step");
    else row.removeAttribute("aria-current");
  });
  renderAudit();

  pick("workspace-result").hidden = !result;
  text("workspace-result", result);
}

function showHelp() {
  if (!guide.open) guide.show();
  helpButton.setAttribute("aria-expanded", "true");
}

function dismissHelp() {
  if (!guide.open) return;
  guide.close();
  helpButton.setAttribute("aria-expanded", "false");
  helpSeen = true;
  try {
    sessionStorage.setItem(helpKey, "seen");
  } catch {
    // Dismissal still applies for the current page.
  }
}

actionButton.addEventListener("click", () => {
  dismissHelp();
  const before = getReceipt();
  const type = stages[before.status].action;
  const result = transition(state, {
    type,
    shop: shop.id,
    id: before.order?.id,
    token: before.order?.token,
  });
  state = result.state;
  if (result.changed) saveWorkspace();
  const after = getReceipt();
  const replayed =
    before.status === "REDEEMED" &&
    !result.changed &&
    after.acceptance === before.acceptance &&
    after.orderId === before.orderId &&
    after.orderCount === before.orderCount &&
    after.stock === before.stock;
  render(
    replayed
      ? `重试命中原受理编号 ${after.acceptance}。订单仍是 ${after.orderId}，订单数和剩余库存都没有变化。`
      : result.message,
  );
  actionButton.focus({ preventScroll: true });
});

pick("dismiss-workspace-help").addEventListener("click", () => {
  dismissHelp();
  actionButton.focus();
});
guide.addEventListener("keydown", (event) => {
  if (event.key !== "Escape") return;
  event.preventDefault();
  dismissHelp();
  actionButton.focus();
});
function resetTrade() {
  dismissHelp();
  state = initialState();
  saveWorkspace();
  render("演示已重置，可以重新抢券。");
  actionButton.focus();
}

render();
actionButton.disabled = false;
mountWorkspaceNavigation({
  enter() {
    if (!helpSeen) showHelp();
  },
  leave: dismissHelp,
  guide: showHelp,
  reset: resetTrade,
});
