import {
  SHOPS,
  STATUS,
  initialState,
  latestOrder,
  availableStock,
  canManage,
  canAccess,
  shopById,
  money,
  transition,
  restoreState,
} from "./model.js";
import { mount as mountTrade } from "./scenes/trade.js";
import { mount as mountFeed } from "./scenes/feed.js";
import { mount as mountEvents } from "./scenes/events.js";
import { mount as mountLikes } from "./scenes/likes.js";
import { createTour } from "./tour.js";
import { makeTradeTour, makeTradeRecoveryTour } from "./scenes/trade-tour.js";

const sceneControllers = {};
const tourControllers = {};
let selectedScene = "free";
const sceneKeys = ["trade", "feed", "events", "likes"];
const sceneEvidence = {
  trade:
    "https://github.com/tech-cove01/project-full-stack/blob/main/docs/transaction-design.md",
  feed: "https://github.com/tech-cove01/project-full-stack/blob/main/docs/backend-highlights-plan.md",
  events:
    "https://github.com/tech-cove01/project-full-stack/blob/main/docs/reviews/2026-10-29/go073-399705a/events/report.md",
  likes: "../performance.html#screenshots",
};

const storageKey = "fullstack-business-demo-v1";
let state;
try {
  state = restoreState(sessionStorage.getItem(storageKey));
} catch {
  /* Memory mode remains usable. */
}
state ||= initialState();
const $ = (selector) => document.querySelector(selector);
const escape = (value) =>
  String(value).replace(
    /[&<>"']/g,
    (char) =>
      ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[
        char
      ],
  );
const pending = new Map();
let toastTimer;
function toast(message) {
  if (!message) return;
  clearTimeout(toastTimer);
  $("#demo-toast").textContent = message;
  $("#demo-toast").hidden = false;
  toastTimer = setTimeout(() => {
    $("#demo-toast").hidden = true;
  }, 4500);
}
function save() {
  try {
    sessionStorage.setItem(storageKey, JSON.stringify(state));
  } catch {
    /* Storage is optional. */
  }
}
function act(action) {
  const result = transition(state, action);
  if (result.changed) {
    state = result.state;
    save();
    render();
    scheduleOrders();
  }
  toast(result.message);
  return result;
}
function scheduleOrders() {
  for (const order of state.orders.filter((o) => o.status === "PROCESSING")) {
    if (pending.has(order.id)) continue;
    pending.set(
      order.id,
      setTimeout(() => {
        pending.delete(order.id);
        act({ type: "CREATE", id: order.id });
      }, 900),
    );
  }
}
function art(shop, small = false) {
  const title = shop === "coffee" ? "咖啡杯与可颂插画" : "轻食餐盘插画";
  return `<div class="shop-art ${shop}${small ? " note-cover" : ""}"><svg viewBox="0 0 360 180" role="img" aria-label="${title}"><circle cx="270" cy="35" r="62" fill="currentColor" opacity=".10"/><path d="M0 150Q100 110 200 147T360 140V180H0Z" fill="currentColor" opacity=".09"/>${shop === "coffee" ? '<ellipse cx="162" cy="151" rx="95" ry="10" fill="currentColor" opacity=".13"/><path d="M100 65h115l-13 61q-4 20-40 20t-48-20Z" fill="#fcf9f2" stroke="currentColor" stroke-width="3"/><path d="M215 75q49-5 26 36l-34 7" fill="none" stroke="currentColor" stroke-width="8"/><ellipse cx="158" cy="65" rx="57" ry="10" fill="#997254"/><path d="M138 48q-16-17 0-30m22 30q-16-17 0-30m22 30q-16-17 0-30" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" opacity=".55"/><path d="m255 117 18-19 22 12 17 24-24 8-30-5-15-12Z" fill="#dbaf70"/><path d="m271 103 7 32m13-24 6 23" stroke="#a87742" stroke-width="3"/>' : '<ellipse cx="180" cy="101" rx="89" ry="57" fill="#fffaf3" stroke="currentColor" stroke-width="3"/><ellipse cx="180" cy="102" rx="66" ry="38" fill="none" stroke="currentColor" opacity=".25"/><path d="M130 82q-18-27 19-21 0-22 23-6 17-20 30 7 28-12 23 15" fill="#91ad77"/><path d="M135 92q18-20 38 1t40-1v32h-80Z" fill="#d9b989"/><circle cx="154" cy="106" r="10" fill="#e3a18c"/><circle cx="190" cy="109" r="12" fill="#b6c48d"/><path d="M72 51v93m-9-93v32h18V51m207 0v93m-9-93v53h9" stroke="currentColor" stroke-width="4" fill="none" stroke-linecap="round"/>'}</svg></div>`;
}
function statusBadge(order) {
  return `<span class="order-status" data-state="${escape(order.status)}">${STATUS[order.status]}</span>`;
}
function renderShop() {
  $("#shop-list").innerHTML = SHOPS.map(
    (shop) =>
      `<button type="button" class="shop-card${state.shop === shop.id ? " selected" : ""}" data-action="shop" data-shop="${shop.id}" aria-pressed="${state.shop === shop.id}" data-focus-key="shop-${shop.id}">${art(shop.id)}<span class="shop-info"><strong>${shop.name} · ${shop.branch}</strong><span class="shop-meta">${shop.category} · ${shop.location}</span></span></button>`,
  ).join("");
  const shop = shopById(state.shop),
    order = latestOrder(state, shop.id);
  const blocked = order && order.status !== "CANCELLED";
  const buttonText = !blocked
    ? "抢这张到店券"
    : order.status === "REFUNDED"
      ? "已退款，同一活动不可重买"
      : "已有订单，请在下方继续";
  $("#activity-panel").innerHTML =
    `<div class="activity-top"><div><p class="eyebrow">${shop.name} · 正在进行的示例活动</p><h2>${shop.offer}</h2></div><span class="stock-pill">剩余 <strong>${availableStock(state, shop.id)}</strong> / ${shop.stock} 份</span></div><p>${shop.description}</p><div class="activity-price"><span>购买价</span><strong>¥${money(shop.price)}</strong><small>可抵 ¥${money(shop.value)}</small></div><ul class="activity-rules"><li>示例活动处于有效期，每人限购1张。</li><li>未支付可取消重买；未核销可由店主退款，退款后不可重买同一活动。</li></ul><button type="button" class="button button-primary" id="buy-button" data-action="buy" ${blocked ? "disabled" : ""}>${buttonText}</button>`;
  if (!order) {
    $("#customer-order").innerHTML =
      '<div class="empty-state"><h2>你的到店券，会出现在这里。</h2><p>先抢一张示例优惠券，看看从受理到核销的完整过程。</p></div>';
    return;
  }
  let actions = "",
    detail = "";
  if (order.status === "PROCESSING")
    detail =
      '<p class="order-progress">请求已受理，正在模拟异步生成订单……此时还没有成单或核销凭证。</p>';
  if (order.status === "UNPAID")
    actions = `<button type="button" class="button button-primary" id="pay-order" data-action="pay" data-id="${order.id}">模拟支付 ¥${money(shop.price)}</button><button type="button" class="action-secondary" data-action="cancel" data-id="${order.id}">取消订单</button>`;
  if (order.status === "PAID") {
    detail = `<div class="voucher-code"><span>示例核销凭证</span><strong>${escape(order.token)}</strong><p>到店后向店员出示。演示中可切换到商家工作台继续。</p></div>`;
    actions = `<button type="button" class="button button-primary" data-action="to-merchant">切换${shop.id === "coffee" ? "店员" : "店主"}去核销</button>`;
  }
  if (order.status === "REDEEMED") {
    detail =
      '<p class="order-progress">到店核销完成。这张券已经使用，不能再退款。</p>';
    actions =
      '<button type="button" class="button button-primary" data-action="compose">去分享探店体验</button>';
  }
  if (order.status === "CANCELLED")
    detail =
      '<p class="order-progress">库存已恢复，购买资格已释放，可以再抢一次。</p>';
  if (order.status === "REFUNDED")
    detail =
      '<p class="order-progress">模拟退款已完成、库存已恢复。同一活动的购买记录保留，不能重新购买。</p>';
  $("#customer-order").innerHTML =
    `<div class="order-heading"><h2 tabindex="-1">我的到店券</h2>${statusBadge(order)}</div><div class="order-summary"><p><span>示例订单</span><strong>${order.id}</strong></p><p><span>门店与优惠</span><strong>${shop.name} · ${shop.offer}</strong></p><p><span>应付金额</span><strong>¥${money(shop.price)}</strong></p></div>${detail}<div class="order-actions">${actions}</div>`;
}
function renderMerchant() {
  $("#merchant-role").value = state.role;
  $("#merchant-shop").value = state.merchantShop;
  const shop = shopById(state.merchantShop);
  if (!canAccess(state, shop.id)) {
    $("#merchant-panel").innerHTML =
      '<div class="empty-state"><h2>此门店不在店员授权范围内</h2><p>陈宁仅负责街角咖啡。切回街角咖啡查看订单，或选择店主体验多门店管理。</p></div>';
    return;
  }
  const orders = state.orders.filter((o) => o.shop === shop.id);
  const order = latestOrder(state, shop.id);
  const audits = state.audit
    .filter(
      (a) => a.shop === shop.id && (canManage(state) || a.actor === "店员"),
    )
    .slice(-8)
    .reverse();
  let card =
    '<div class="empty-state"><h2>还没有顾客订单</h2><p>先到顾客视角，抢券并完成模拟支付，再回来处理履约。</p><button type="button" class="action-secondary" data-action="to-customer">去顾客视角买券</button></div>';
  if (order) {
    const controls =
      order.status === "PAID"
        ? `<label class="redeem-label">顾客出示的示例凭证<input id="redeem-token" autocomplete="off" placeholder="填写顾客订单中的凭证" maxlength="40"></label><div class="order-actions"><button type="button" class="action-secondary" data-action="fill-token" data-id="${order.id}">填入示例凭证</button><button type="button" class="button button-primary" id="redeem-order" data-action="redeem" data-id="${order.id}">确认到店核销</button><button type="button" class="action-danger" data-action="refund" data-id="${order.id}" ${canManage(state) ? "" : "disabled"}>${canManage(state) ? "模拟退款" : "仅店主可退款"}</button></div><p class="form-note">真实门店需顾客出示凭证；这里可一键填入示例。核销与退款互斥。</p>`
        : order.status === "REDEEMED"
          ? `<p class="order-progress">已完成到店履约，不能退款。</p><button type="button" class="action-secondary" data-action="redeem" data-id="${order.id}">再次核销，看看重复处理</button>`
          : order.status === "CANCELLED"
            ? '<p class="order-progress">顾客已取消，库存已恢复，可以重新购买。</p>'
            : order.status === "REFUNDED"
              ? '<p class="order-progress">模拟退款已完成，库存已恢复，该顾客不能重买同一活动。</p>'
              : '<p class="order-progress">等待顾客成单与支付，可回到顾客视角继续。</p>';
    card = `<article class="merchant-order"><div class="order-heading"><h2>小林的到店订单</h2>${statusBadge(order)}</div><div class="order-summary"><p><span>订单编号</span><strong>${order.id}</strong></p><p><span>优惠与金额</span><strong>${shop.offer} · ¥${money(shop.price)}</strong></p></div>${controls}</article>`;
  }
  $("#merchant-panel").innerHTML =
    `<p class="workspace-context">${canManage(state) ? "店主 · 可管理两家门店、核销与退款，查看操作审计。" : "店员 · 仅授权街角咖啡，可核销，不能退款或查看管理审计。"}</p><div class="merchant-stats"><div><span>待核销</span><strong>${orders.filter((o) => o.status === "PAID").length}</strong></div><div><span>已核销</span><strong>${orders.filter((o) => o.status === "REDEEMED").length}</strong></div><div><span>活动余量</span><strong>${availableStock(state, shop.id)}</strong></div></div>${card}<section class="audit-card"><h2>${canManage(state) ? "门店操作审计 · 示例" : "本次店员操作回执"}</h2>${audits.length ? `<ol class="audit-list">${audits.map((a) => `<li><span>${escape(a.actor)} · ${escape(a.orderId)}</span><strong>${escape(a.text)}</strong></li>`).join("")}</ol>` : `<p class="form-note">${canManage(state) ? "业务操作发生后，这里显示对应示例记录。" : "核销完成后显示本次店员回执；完整审计仅店主可见。"}</p>`}</section>`;
}
function renderNotes() {
  const query = $("#note-search").value.trim().toLowerCase();
  const notes = state.notes.filter((n) =>
    `${n.title} ${n.content} ${shopById(n.shop).name}`
      .toLowerCase()
      .includes(query),
  );
  $("#note-list").innerHTML = notes.length
    ? notes
        .map(
          (note) =>
            `<article class="note-card">${art(note.shop, true)}<div class="note-body"><p class="note-meta">${escape(note.author)}${note.mine ? " · 你的示例笔记" : ""} · ${shopById(note.shop).name}</p><h2>${escape(note.title)}</h2><p>${escape(note.content)}</p><div class="note-actions"><button type="button" class="${note.liked ? "is-liked" : ""}" data-action="like" data-id="${escape(note.id)}" data-focus-key="like-${escape(note.id)}" aria-pressed="${note.liked}" aria-label="${note.liked ? "取消点赞" : "点赞"}：${escape(note.title)}"><span aria-hidden="true">${note.liked ? "♥" : "♡"}</span> ${note.likes} <span>${note.liked ? "已点赞" : "点赞"}</span></button></div></div></article>`,
        )
        .join("")
    : '<div class="empty-state"><h2>还没找到相关笔记</h2><p>试试“咖啡”“花园”，或清空搜索看看全部内容。</p></div>';
}
const steps = [
  "挑选优惠并抢券",
  "受理后生成订单",
  "顾客模拟支付",
  "店员到店核销",
  "发布探店分享",
];
function guideStep() {
  const order = latestOrder(state, "coffee");
  if (!order || ["CANCELLED", "REFUNDED"].includes(order.status)) return 0;
  if (order.status === "PROCESSING") return 1;
  if (order.status === "UNPAID") return 2;
  if (order.status === "PAID") return 3;
  return state.notes.some((n) => n.mine && n.shop === "coffee") ? 5 : 4;
}
function renderGuide() {
  const tour = tourControllers[selectedScene]?.view();
  if (tour) {
    $("#guide-title").textContent = tour.plan.title;
    $("#guide-text").textContent = tour.step
      ? tour.phase === "result"
        ? tour.step.feedback
        : tour.step.purpose
      : tour.plan.lesson;
    $("#guide-next").textContent =
      tour.phase === "result" ? "看懂了，继续" : "定位当前操作";
    $("#guide-next").disabled = !tour.step;
    $("#guide-progress").textContent =
      `已完成 ${tour.index} / ${tour.plan.steps.length} 步`;
    return;
  }
  if (selectedScene !== "free" && sceneControllers[selectedScene]) {
    const guide = sceneControllers[selectedScene].getGuide();
    $("#guide-title").textContent = guide.title;
    $("#guide-text").textContent = guide.text;
    $("#guide-steps").innerHTML = guide.steps
      .map(
        (text, i) =>
          `<li class="${i < guide.current ? "is-done" : i === guide.current ? "is-current" : ""}"><span>${i < guide.current ? "✓" : i + 1}</span>${escape(text)}</li>`,
      )
      .join("");
    $("#guide-next").textContent = guide.label;
    $("#guide-next").disabled = false;
    $("#guide-progress").textContent =
      `已完成 ${Math.min(guide.current, guide.steps.length)} / ${guide.steps.length} 步`;
    $("#implementation-note").textContent =
      "场景中的状态与计数用于说明业务机制。真实实现与测试条件请从对应设计和证据入口查看。";
    $(".guide-explanation a").href = `../details.html#${selectedScene}`;
    return;
  }
  $(".guide-explanation a").href = "../details.html";
  const step = guideStep();
  const titles = [
    "先挑一张街角咖啡券",
    "已受理，不等于已成单",
    "付完款，才有到店凭证",
    "现在，换店员接待顾客",
    "到店完成，把体验分享出去",
    "一笔交易，已经走完！",
  ];
  const texts = [
    "跟着小林走一遍：选街角咖啡，用19元购买30元到店券，看看后续会发生什么。",
    "库存先预占，订单随后生成。此处用短暂等待演示两个阶段，不代表真实后端耗时。",
    "在顾客订单里点击模拟支付，不会发生真实扣款。你会得到一张示例核销凭证。",
    "陈宁是街角咖啡的店员。切换商家视角，填入凭证核销，再看看顾客订单是否同步改变。",
    "写一篇探店笔记，发布后会进入社区。真实系统也允许不下单直接发笔记，这里只是顺着故事体验。",
    "从顾客抢券，到门店履约，再到探店分享，三种视角已连成同一段业务。还可以体验取消、店主退款和门店权限。",
  ];
  const explanations = [
    "真实系统通过 Redis Lua 校验活动与库存，保存稳定受理标识并写入 Stream。",
    "真实后台将订单、审计与交易 Outbox 一起提交到 MySQL；持久结果明确后再确认消息。",
    "项目资金流程本身为模拟支付；业务服务检查订单状态，支付后才提供核销凭证。",
    "真实核销在事务内锁定并复核当前门店授权和订单，状态变化与审计共同保存，重复核销保持幂等。",
    "真实笔记写入与社区 Outbox 同事务；关注动态和搜索经独立消费组更新，点赞按期望状态写入。",
    "本演示在浏览器模拟状态变化。真实事务与权限能力请阅读源码，性能成绩以独立实测报告为准。",
  ];
  $("#guide-title").textContent = titles[step];
  $("#guide-text").textContent = texts[step];
  $("#guide-steps").innerHTML = steps
    .map(
      (text, i) =>
        `<li data-step="${i}" class="${i < step ? "is-done" : i === step ? "is-current" : ""}" ${i === step ? 'aria-current="step"' : ""}><span>${i < step ? "✓" : i + 1}</span>${text}</li>`,
    )
    .join("");
  $("#guide-next").textContent = [
    "开始逛店抢券",
    "正在生成订单……",
    "去顾客订单支付",
    "切换店员去核销",
    "去写探店笔记",
    "以店主回顾操作记录",
  ][step];
  $("#guide-next").disabled = step === 1;
  $("#guide-progress").textContent = `已完成 ${step} / 5 步`;
  $("#implementation-note").textContent = explanations[step];
}
function render() {
  const active = document.activeElement;
  const focusId = active?.id,
    focusKey = active?.dataset.focusKey;
  document
    .querySelectorAll("[data-view]")
    .forEach((b) =>
      b.setAttribute("aria-pressed", String(b.dataset.view === state.view)),
    );
  document.querySelectorAll("[data-view-panel]").forEach((p) => {
    p.hidden = p.dataset.viewPanel !== state.view;
  });
  renderShop();
  renderMerchant();
  renderNotes();
  renderGuide();
  const restoreFocus = focusId
    ? document.getElementById(focusId)
    : [...document.querySelectorAll("[data-focus-key]")].find(
        (e) => e.dataset.focusKey === focusKey,
      );
  if (
    restoreFocus &&
    !restoreFocus.disabled &&
    !restoreFocus.closest("[hidden]")
  )
    restoreFocus.focus({ preventScroll: true });
}
function go(view, selector) {
  act({ type: "VIEW", view });
  requestAnimationFrame(() => {
    const target = $(selector);
    if (target) {
      target.scrollIntoView({ behavior: "auto", block: "center" });
      target.focus({ preventScroll: true });
    }
  });
}
function toMerchant() {
  const order = latestOrder(state, state.shop);
  const shop = order?.shop || "coffee";
  act({ type: "WORKSPACE", role: shop === "coffee" ? "STAFF" : "OWNER", shop });
  go("merchant", "#redeem-token");
}
function compose() {
  go("community", "#note-title");
  $("#compose-note").hidden = false;
  $("#note-shop").value = state.shop;
  requestAnimationFrame(() => {
    $("#note-title").scrollIntoView({ block: "center" });
    $("#note-title").focus();
  });
}
document.addEventListener("click", (event) => {
  const button = event.target.closest("button");
  if (!button || button.disabled) return;
  if (event.composedPath().includes($("#scene-workspace"))) return;
  if (button.dataset.view) {
    act({ type: "VIEW", view: button.dataset.view });
    return;
  }
  const id = button.dataset.id;
  switch (button.dataset.action) {
    case "shop":
      act({ type: "SHOP", shop: button.dataset.shop });
      break;
    case "buy":
      act({ type: "BUY", shop: state.shop });
      break;
    case "pay":
      act({ type: "PAY", id });
      break;
    case "cancel":
      act({ type: "CANCEL", id });
      break;
    case "to-merchant":
      toMerchant();
      break;
    case "to-customer":
      go("customer", "#buy-button");
      break;
    case "fill-token":
      $("#redeem-token").value =
        state.orders.find((o) => o.id === id)?.token || "";
      $("#redeem-token").focus();
      break;
    case "redeem":
      act({ type: "REDEEM", id, token: $("#redeem-token")?.value });
      break;
    case "refund":
      act({ type: "REFUND", id });
      break;
    case "compose":
      compose();
      break;
    case "like": {
      const note = state.notes.find((n) => n.id === id);
      if (note) act({ type: "LIKE", id, liked: !note.liked });
      break;
    }
  }
});
$("#merchant-role").addEventListener("change", () =>
  act({
    type: "WORKSPACE",
    role: $("#merchant-role").value,
    shop: $("#merchant-shop").value,
  }),
);
$("#merchant-shop").addEventListener("change", () =>
  act({
    type: "WORKSPACE",
    role: $("#merchant-role").value,
    shop: $("#merchant-shop").value,
  }),
);
$("#note-search").addEventListener("input", renderNotes);
$("#open-compose").addEventListener("click", compose);
$("#close-compose").addEventListener("click", () => {
  $("#compose-note").hidden = true;
  $("#open-compose").focus();
});
$("#compose-note").addEventListener("submit", (event) => {
  event.preventDefault();
  const result = act({
    type: "PUBLISH",
    shop: $("#note-shop").value,
    title: $("#note-title").value,
    content: $("#note-content").value,
  });
  $("#compose-error").textContent = result.changed ? "" : result.message;
  if (result.changed) {
    $("#compose-note").hidden = true;
    $("#note-search").value = "";
    renderNotes();
    $("#open-compose").focus();
  }
});
$("#guide-next").addEventListener("click", () => {
  if (tourControllers[selectedScene]?.view()) {
    tourControllers[selectedScene].continue();
    return;
  }
  if (selectedScene !== "free") {
    const selector = sceneControllers[selectedScene]?.getGuide().nextSelector;
    const target = selector ? document.querySelector(selector) : null;
    if (target) {
      target.scrollIntoView({ block: "center" });
      target.focus({ preventScroll: true });
    }
    return;
  }
  const step = guideStep();
  if (step < 3) {
    act({ type: "SHOP", shop: "coffee" });
    go("customer", step === 0 ? "#buy-button" : "#pay-order");
  } else if (step === 3) {
    act({ type: "SHOP", shop: "coffee" });
    toMerchant();
  } else if (step === 4) {
    act({ type: "SHOP", shop: "coffee" });
    compose();
  } else {
    act({ type: "WORKSPACE", role: "OWNER", shop: "coffee" });
    go("merchant", "#merchant-role");
  }
});
$("#reset-demo").addEventListener("click", () => {
  Object.values(tourControllers).forEach((t) => {
    t.stop();
    t.reset();
  });
  pending.forEach(clearTimeout);
  pending.clear();
  state = initialState();
  save();
  $("#note-search").value = "";
  $("#compose-note").reset();
  $("#compose-note").hidden = true;
  $("#compose-error").textContent = "";
  render();
  Object.values(sceneControllers).forEach((controller) => controller.reset());
  toast("示例已恢复，可以重新体验。点击其他页面仍保留本次进度。");
  $("[data-view=customer]").focus();
  showIntro();
});

const introKey = "fullstack-demo-intro-v1";
const intro = $("#demo-intro");
const compactLayout = matchMedia("(max-width: 900px)");
let followGuide = false;
let highlightTimer;
function updateIntroPosition() {
  $("#intro-description").textContent = compactLayout.matches
    ? "页面上方有一组操作步骤。按顺序跟着做，就能体验一笔完整的到店消费。"
    : "页面右侧有一组操作步骤。按顺序跟着做，就能体验一笔完整的到店消费。";
  if (selectedScene !== "free") {
    const names = {
      trade: "抢券与故障恢复",
      feed: "公共内容读取与个人状态",
      events: "笔记发布与搜索恢复",
      likes: "点赞写入与下游积压",
    };
    $("#intro-description").textContent =
      `当前体验${names[selectedScene]}。${compactLayout.matches ? "上方" : "右侧"}步骤会告诉你从哪里开始、点击什么和结果看哪里。`;
  }
  $("#intro-path").textContent =
    selectedScene === "free"
      ? "从顾客抢券开始，再体验支付、店员核销和探店分享。"
      : "跟随当前场景的操作顺序，也可以随时重置并切换其他场景。";
  $("#intro-description").textContent =
    selectedScene === "free"
      ? "建议先从抢券开始。引导会高亮要点击的按钮，做完后带你看结果，再继续下一步。"
      : "从当前场景开始。跟着高亮按钮操作，做完后看结果；随时可以退出，自由探索。";
}
function focusGuide() {
  const guide = $(".guide-card");
  clearTimeout(highlightTimer);
  guide.classList.add("guide-highlight");
  guide.scrollIntoView({ behavior: "auto", block: "start" });
  const target = $("#guide-next").disabled ? guide : $("#guide-next");
  target.focus({ preventScroll: true });
  highlightTimer = setTimeout(
    () => guide.classList.remove("guide-highlight"),
    4000,
  );
}
function showIntro() {
  tourControllers[selectedScene]?.stop();
  updateIntroPosition();
  followGuide = false;
  if (typeof intro.showModal !== "function") {
    focusGuide();
    return;
  }
  if (!intro.open) intro.showModal();
  document.body.classList.add("demo-intro-open");
}
$("#show-demo-intro").addEventListener("click", showIntro);
$("#intro-start").addEventListener("click", () => {
  followGuide = true;
  intro.close();
});
$("#intro-explore").addEventListener("click", () => {
  tourControllers[selectedScene]?.stop();
  intro.close();
});
intro.addEventListener("close", () => {
  document.body.classList.remove("demo-intro-open");
  try {
    sessionStorage.setItem(introKey, "seen");
  } catch {
    /* Intro still works without persistence. */
  }
  if (followGuide) startTour();
  else $("#show-demo-intro").focus({ preventScroll: true });
});
compactLayout.addEventListener("change", updateIntroPosition);
function selectScene(key, push = false) {
  tourControllers[selectedScene]?.stop();
  selectedScene = sceneKeys.includes(key) ? key : "free";
  $("#scene-workspace").hidden = selectedScene === "free";
  $("#free-business").hidden = selectedScene !== "free";
  document
    .querySelectorAll("[data-scene]")
    .forEach((b) =>
      b.setAttribute("aria-pressed", String(b.dataset.scene === selectedScene)),
    );
  document.querySelectorAll("[data-scene-panel]").forEach((p) => {
    p.hidden = p.dataset.scenePanel !== selectedScene;
  });
  if (selectedScene !== "free") {
    $("#scene-design-link").href = `../details.html#${selectedScene}`;
    $("#scene-evidence-link").href =
      sceneControllers[selectedScene]?.getGuide().evidence ||
      sceneEvidence[selectedScene];
  }
  if (push) {
    const url = new URL(location.href);
    if (selectedScene === "free") url.searchParams.delete("scene");
    else url.searchParams.set("scene", selectedScene);
    history.pushState(null, "", url);
  }
  renderGuide();
  updateIntroPosition();
}
document
  .querySelectorAll("[data-scene]")
  .forEach((b) =>
    b.addEventListener("click", () => selectScene(b.dataset.scene, true)),
  );
function resetScene() {
  const t = tourControllers[selectedScene];
  const running = Boolean(t?.view());
  t?.stop();
  t?.reset();
  sceneControllers[selectedScene]?.reset();
  if (running) t.start();
}
$("#reset-scene").addEventListener("click", resetScene);
function startTour() {
  if (selectedScene === "free") selectScene("trade", true);
  tourControllers[selectedScene]?.start();
}
$("#start-tour").addEventListener("click", startTour);
$("#explore-tour").addEventListener("click", () =>
  tourControllers[selectedScene]?.stop(),
);
window.addEventListener("popstate", () =>
  selectScene(new URL(location.href).searchParams.get("scene")),
);
for (const [key, mount] of Object.entries({
  trade: mountTrade,
  feed: mountFeed,
  events: mountEvents,
  likes: mountLikes,
})) {
  sceneControllers[key] = mount(document.querySelector(`#scene-${key}`), {
    onChange: () => renderGuide(),
  });
}
const tourFactories = {
  trade: (s, branch) => (branch ? makeTradeRecoveryTour(s) : makeTradeTour(s)),
};
for (const [key, factory] of Object.entries(tourFactories)) {
  tourControllers[key] = createTour({
    workspace: () => key,
    panel: () => (selectedScene === key ? $(`#scene-${key}`) : null),
    snapshot: () => sceneControllers[key].getSnapshot(),
    factory,
    onUpdate: renderGuide,
    onNext: (action) => {
      if (action === "reset") resetScene();
      else {
        selectScene(
          sceneKeys[(sceneKeys.indexOf(key) + 1) % sceneKeys.length],
          true,
        );
        startTour();
      }
    },
  });
}
selectScene(new URL(location.href).searchParams.get("scene"));
render();
scheduleOrders();
let introSeen = false;
try {
  introSeen = sessionStorage.getItem(introKey) === "seen";
} catch {
  /* Show instructions for this visit. */
}
if (!introSeen) showIntro();
else if (selectedScene !== "free") startTour();
