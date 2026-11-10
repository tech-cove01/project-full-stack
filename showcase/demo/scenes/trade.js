// Deterministic browser demonstration of the documented admission/recovery rules.
// These counters are illustrative state, not measurements of Redis or MySQL.
export function initialTradeState() {
  return {
    stock: 5,
    next: 1,
    requests: [],
    orders: [],
    reservation: null,
    compensations: [],
    log: [],
    result: "先抢一张街角咖啡到店券，观察受理记录与库存。",
  };
}

const currentRequest = (state) => state.requests.at(-1);
const currentOrder = (state) =>
  state.orders.find((order) => order.request === currentRequest(state)?.id);
function report(state, message) {
  state.result = message;
  state.log.unshift(message);
  state.log = state.log.slice(0, 12);
  return state;
}
function createOrder(state, request) {
  let order = state.orders.find((item) => item.request === request.id);
  if (!order) {
    order = {
      id: `ORDER-${request.id.slice(-3)}`,
      request: request.id,
      status: "UNPAID",
    };
    state.orders.push(order);
  }
  request.phase = "CREATED";
  request.durable = "order";
  return order;
}

export function tradeStep(previous, action) {
  const state = structuredClone(previous);
  const request = currentRequest(state);
  const order = currentOrder(state);
  switch (action) {
    case "accept": {
      if (state.reservation)
        return report(
          state,
          `已有预占归属 ${state.reservation}。请重试原请求或继续现有订单，不再扣库存。`,
        );
      if (state.stock < 1) return report(state, "示例库存不足，未受理新请求。");
      const id = `ACCEPT-${String(state.next++).padStart(3, "0")}`;
      state.requests.push({
        id,
        phase: "ACCEPTED",
        durable: "none",
        review: "none",
        acknowledged: false,
      });
      state.reservation = id;
      state.stock -= 1;
      return report(
        state,
        `请求 ${id} 已受理，库存预占1份；订单尚未形成，不能当作成单成功。`,
      );
    }
    case "retry":
      if (!request) return report(state, "请先抢券，取得稳定受理记录。");
      return report(
        state,
        `同一请求仍返回 ${request.id}；${order ? `对应订单仍为 ${order.id}` : "尚无可确认的订单"}，库存没有再次扣减。`,
      );
    case "consume":
    case "duplicate": {
      if (!request) return report(state, "还没有待处理的受理消息，请先抢券。");
      if (order)
        return report(
          state,
          `消息再次到达，按受理标识查到 ${order.id}（${order.status === "CANCELLED" ? "已取消" : "待支付"}）；唯一订单不变，库存不再扣减。`,
        );
      if (request.phase === "UNCERTAIN")
        return report(
          state,
          "这次处理结果未知，不能直接宣告成单；先保存待核对记录，再明确核对。",
        );
      const created = createOrder(state, request);
      return report(
        state,
        `订单 ${created.id} 已持久化为待支付，绑定 ${request.id}；现在可以确认这条消息。`,
      );
    }
    case "unknown":
      if (!request)
        return report(state, "请先抢券，再体验处理结果待核对的分支。");
      if (order || request.durable !== "none")
        return report(
          state,
          "这条受理已有持久结果，不能把已知结果改成未知；重置场景可体验另一条分支。",
        );
      request.phase = "UNCERTAIN";
      return report(
        state,
        "模拟处理响应丢失：结果尚不能确认，消息保持未确认，订单也不冒称成功。",
      );
    case "persist-review":
      if (!request || request.phase !== "UNCERTAIN")
        return report(state, "只有结果未知的受理记录需要进入待核对分支。");
      if (request.review !== "none")
        return report(state, "该受理的待核对记录已经保存，不重复创建。");
      request.review = "OPEN";
      request.durable = "review";
      return report(
        state,
        `已为 ${request.id} 持久保存待核对任务，状态OPEN；它允许确认消息，但并不表示订单已经创建。`,
      );
    case "ack":
      if (!request) return report(state, "还没有消息可以确认。");
      if (request.acknowledged)
        return report(
          state,
          `${request.id} 已确认，重复确认不会新增订单或改变库存。`,
        );
      if (request.durable === "none")
        return report(
          state,
          "确认被阻止：订单结果和待核对任务都未持久化，消息必须保留待处理。",
        );
      request.acknowledged = true;
      return report(
        state,
        `已确认 ${request.id}：依据是${request.durable === "review" ? "持久待核对任务，不是订单成功" : "已保存的订单结果"}。`,
      );
    case "reconcile": {
      if (!request || request.review !== "OPEN")
        return report(state, "请先保存待核对任务，再由访客明确触发核对。");
      const created = createOrder(state, request);
      request.review = "RESOLVED";
      return report(
        state,
        `核对示例事实后，${created.id} 已确认存在且为唯一订单；待核对任务改为RESOLVED，库存仍只预占一次。`,
      );
    }
    case "cancel":
      if (!order || order.status !== "UNPAID")
        return report(state, "只有已生成的待支付订单可以取消。");
      order.status = "CANCELLED";
      state.compensations.push({ owner: request.id, applied: false });
      return report(
        state,
        `订单 ${order.id} 已取消，补偿任务已保存；库存仍待补偿处理，旧受理归属仍为 ${request.id}。`,
      );
    case "compensate": {
      const compensation = state.compensations.findLast(
        (item) => !item.applied,
      );
      if (!compensation)
        return report(state, "没有未完成的取消补偿；库存不会凭空增加。");
      if (state.reservation !== compensation.owner)
        return report(
          state,
          `补偿归属 ${compensation.owner} 与当前预占 ${state.reservation || "空"} 不符，拒绝释放库存。`,
        );
      compensation.applied = true;
      state.reservation = null;
      state.stock += 1;
      return report(
        state,
        `已核对补偿归属 ${compensation.owner}，恢复1份库存并释放本次购买资格；可点击“抢券并受理”再次购买。`,
      );
    }
    case "old-compensation": {
      const compensation = state.compensations[0];
      if (!compensation)
        return report(
          state,
          "先完成一次取消与补偿，再重新购买，才能观察旧补偿到达。",
        );
      if (state.reservation && state.reservation !== compensation.owner)
        return report(
          state,
          `旧补偿属于 ${compensation.owner}，当前预占属于 ${state.reservation}；归属不匹配，保留新预占，库存仍为 ${state.stock}。`,
        );
      if (compensation.applied)
        return report(
          state,
          `旧补偿 ${compensation.owner} 已处理，重复到达不再恢复库存。`,
        );
      return report(state, "这份补偿仍待处理，请先应用本次取消补偿。");
    }
    default:
      return report(state, "请选择页面上的一个示例操作。");
  }
}

const escape = (value) =>
  String(value).replace(
    /[&<>"']/g,
    (char) =>
      ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[
        char
      ],
  );
const buttons = [
  ["accept", "trade-accept", "抢券并受理"],
  ["consume", "trade-consume", "消费成单"],
  ["ack", "trade-ack", "确认消息"],
  ["retry", "trade-retry", "重试同一请求"],
  ["duplicate", "trade-duplicate", "重复投递消息"],
  ["unknown", "trade-unknown", "模拟结果待核对"],
  ["persist-review", "trade-review", "保存待核对任务"],
  ["reconcile", "trade-reconcile", "核对并恢复"],
  ["cancel", "trade-cancel", "取消待支付订单"],
  ["compensate", "trade-compensate", "应用取消补偿"],
  ["old-compensation", "trade-old-compensation", "投递旧补偿"],
];

export function mount(container, { onChange = () => {} } = {}) {
  let state = initialTradeState();
  function render() {
    const activeId = container.contains(document.activeElement)
      ? document.activeElement.id
      : "";
    const request = currentRequest(state),
      order = currentOrder(state);
    container.innerHTML = `<div class="scene-heading"><p class="eyebrow">场景一 · 一张咖啡到店券</p><h1>异步交易一致性与故障恢复</h1><p>网络重试、重复消息和延迟补偿到来时，库存和订单应该仍能对应起来。先抢券，再消费成单；也可以在成单前体验待核对分支。</p></div>
      <p class="scene-note">以下为可重置的浏览器机制模拟，不连接Redis、Stream或MySQL。原顾客支付、商家核销与退款流程仍可从自由体验进入。</p>
      <div class="scene-stats"><div><span>剩余库存</span><strong data-trade="stock">${state.stock} / 5</strong></div><div><span>受理记录</span><strong data-trade="requests">${state.requests.length}</strong></div><div><span>唯一订单总数</span><strong data-trade="orders">${state.orders.length}</strong></div><div><span>待确认消息</span><strong data-trade="pending">${state.requests.filter((item) => !item.acknowledged).length}</strong></div></div>
      <div class="scene-actions">${buttons
        .slice(0, 3)
        .map(
          ([action, id, label]) =>
            `<button type="button" id="${id}" data-action="${action}" class="${action === "accept" ? "button button-primary" : "action-secondary"}">${label}</button>`,
        )
        .join(
          "",
        )}<button type="button" id="trade-reset" data-action="reset" class="action-secondary">重置交易场景</button></div>
      <div class="scene-result" role="status" aria-live="polite"><strong>当前结果</strong><p data-trade="result">${escape(state.result)}</p></div>
      <dl class="scene-facts"><div><dt>当前受理ID</dt><dd data-trade="acceptance">${escape(request?.id || "尚未受理")}</dd></div><div><dt>当前订单</dt><dd data-trade="order">${order ? `${escape(order.id)} · ${order.status === "CANCELLED" ? "已取消" : "待支付"}` : "尚未形成可确认的订单"}</dd></div><div><dt>预占归属</dt><dd data-trade="owner">${escape(state.reservation || "无")}</dd></div><div><dt>待核对任务</dt><dd data-trade="review">${escape(request?.review === "none" || !request ? "无" : request.review)}</dd></div></dl>
      <div class="scene-actions">${buttons
        .slice(3)
        .map(
          ([action, id, label]) =>
            `<button type="button" id="${id}" data-action="${action}" class="action-secondary">${label}</button>`,
        )
        .join("")}</div>
      <p class="scene-note">正常：抢券 → 消费成单 → 确认消息。待核对：抢券 → 模拟结果待核对 → 先试确认消息 → 保存待核对任务 → 确认消息 → 核对并恢复。旧补偿：取消待支付订单 → 应用取消补偿 → 再次抢券 → 投递旧补偿。</p>
      <details class="scene-process"><summary>查看库存、唯一订单与消息确认的过程</summary><p>受理映射使同一请求返回同一ID；示例订单按受理ID唯一。结果未知时不先宣布成单，持久待核对任务与明确订单结果都可作为确认消息的依据。补偿先核对预占归属，再改变库存。</p><ol class="scene-log">${state.log.map((line) => `<li>${escape(line)}</li>`).join("") || "<li>等待第一次操作。</li>"}</ol></details>`;
    if (activeId)
      container.querySelector(`#${activeId}`)?.focus({ preventScroll: true });
  }
  function reset() {
    state = initialTradeState();
    render();
    onChange();
  }
  container.addEventListener("click", (event) => {
    const button = event.target.closest("button[data-action]");
    if (!button || !container.contains(button)) return;
    if (button.dataset.action === "reset") {
      reset();
      return;
    }
    state = tradeStep(state, button.dataset.action);
    render();
    onChange();
  });
  render();
  return {
    reset,
    getSnapshot() {
      return structuredClone(state);
    },
    getGuide() {
      const request = currentRequest(state),
        order = currentOrder(state);
      let current = !request ? 0 : !order ? 1 : !request.acknowledged ? 2 : 3;
      if (request?.phase === "UNCERTAIN")
        current = request.review === "none" ? 1 : 2;
      const selector = !request
        ? "#trade-accept"
        : request.phase === "UNCERTAIN"
          ? request.review === "none"
            ? "#trade-review"
            : !request.acknowledged
              ? "#trade-ack"
              : "#trade-reconcile"
          : !order
            ? "#trade-consume"
            : !request.acknowledged
              ? "#trade-ack"
              : "#trade-retry";
      return {
        title: "先观察受理，再确认唯一订单",
        text: "主操作在场景卡片上方，结果在“当前结果”和库存/订单卡。成单前可尝试结果待核对；取消后重买，再投递旧补偿观察归属校验。",
        steps: [
          "抢券，取得稳定受理ID",
          "消费成单，或保存待核对任务",
          "持久结果后确认消息",
          "重试、取消重买与旧补偿对比",
        ],
        current,
        label: "定位下一步操作",
        nextSelector: selector,
        design: "trade",
        evidence:
          "https://github.com/tech-cove01/project-full-stack/blob/main/docs/reviews/2026-10-28/batch-3583818/trade-review.md",
      };
    },
  };
}
