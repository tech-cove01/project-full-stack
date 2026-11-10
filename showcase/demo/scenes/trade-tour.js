// Lessons follow the real browser model; opening a lesson never changes it.
const request = (state) => state?.requests?.at(-1);
const orderFor = (state, id = request(state)?.id) =>
  state?.orders?.find((order) => order.request === id);
const compensationFor = (state, owner) =>
  state?.compensations?.find((item) => item.owner === owner);
const activeRequest = (state) =>
  Boolean(request(state) && state.reservation === request(state).id);
const uniqueOrder = (state) => {
  const current = request(state);
  const orders = state?.orders?.filter(
    (order) => order.request === current?.id,
  );
  return Boolean(
    activeRequest(state) &&
    current.durable === "order" &&
    orders.length === 1 &&
    orders[0].status === "UNPAID",
  );
};
const sameBusinessState = (before, after) =>
  before.stock === after.stock &&
  before.next === after.next &&
  before.reservation === after.reservation &&
  JSON.stringify(before.requests) === JSON.stringify(after.requests) &&
  JSON.stringify(before.orders) === JSON.stringify(after.orders) &&
  JSON.stringify(before.compensations) === JSON.stringify(after.compensations);

function accepted(action, before, after) {
  return Boolean(
    action === "accept" &&
    !before.reservation &&
    before.stock > 0 &&
    activeRequest(after) &&
    after.requests.length === before.requests.length + 1 &&
    after.stock === before.stock - 1 &&
    after.orders.length === before.orders.length &&
    request(after).phase === "ACCEPTED" &&
    request(after).durable === "none" &&
    !request(after).acknowledged,
  );
}

function sameOrderProtected(actionName, action, before, after) {
  return Boolean(
    action === actionName &&
    uniqueOrder(before) &&
    uniqueOrder(after) &&
    request(before).id === request(after).id &&
    sameBusinessState(before, after),
  );
}

// Reset reuses ACCEPT-001, so evidence from a previous run must be discarded.
function historySinceReset(history = []) {
  const reset = history.findLastIndex((entry) => entry.action === "reset");
  return history.slice(reset + 1);
}

function witnessed(snapshot, history, matcher) {
  const id = request(snapshot)?.id;
  if (!id) return false;
  return historySinceReset(history).some(
    ({ action, before, after }) =>
      request(after)?.id === id &&
      after.reservation === snapshot.reservation &&
      matcher(action, before, after),
  );
}

export function makeTradeTour(snapshot = {}) {
  const current = request(snapshot);
  const incompatible =
    current?.phase === "UNCERTAIN" ||
    (activeRequest(snapshot) && orderFor(snapshot)?.status === "CANCELLED");
  return {
    id: "trade-main",
    title: "一张券，重复请求也只成一单",
    lesson:
      "受理与成单分两步；稳定受理ID和唯一订单使重试、重复消息都只产生一单。确认消息前，结果必须已经保存。",
    blocked: incompatible
      ? {
          message:
            "当前在待核对或取消分支。可退出教程继续处理，或明确重置后从抢券开始；已有操作不会自动清空。",
          actionLabel: "重置交易后从头体验",
        }
      : null,
    steps: [
      {
        id: "trade-accept",
        target: "#trade-accept",
        result: '[data-trade="acceptance"]',
        event: "click",
        purpose: "点击“抢券并受理”，看看系统先留下什么凭据。",
        invalid:
          "尚未产生新的受理记录。已有预占时会保护库存；可继续原订单，或明确重置后重新体验。",
        feedback:
          "受理ID已经生成，库存只预占一份。此时还没有订单，收到请求不等于成单成功。",
        matches: accepted,
        done: activeRequest,
      },
      {
        id: "trade-consume",
        target: "#trade-consume",
        result: '[data-trade="order"]',
        event: "click",
        purpose: "点击“消费成单”，把刚才的受理记录处理成订单。",
        invalid:
          "这次没有形成新订单。请先抢券；若已进入待核对分支，请退出教程完成核对，或明确重置。",
        feedback:
          "现在才有一张持久化的待支付订单，它绑定刚才的受理ID。库存仍只扣过一次。",
        matches: (action, before, after) =>
          Boolean(
            action === "consume" &&
            activeRequest(before) &&
            !orderFor(before) &&
            request(before).phase !== "UNCERTAIN" &&
            uniqueOrder(after) &&
            request(before).id === request(after).id &&
            after.orders.length === before.orders.length + 1 &&
            after.stock === before.stock,
          ),
        done: uniqueOrder,
      },
      {
        id: "trade-retry",
        target: "#trade-retry",
        result: '[data-trade="result"]',
        event: "click",
        purpose: "假设刚才没收到响应，点击“重试同一请求”。",
        invalid: "请在订单已经形成后重试，才能比较同一受理ID、订单和库存。",
        feedback:
          "这是预期保护：系统返回同一个受理ID和订单，库存不再扣减。顾客重试不会多买一张。",
        matches: (action, before, after) =>
          sameOrderProtected("retry", action, before, after),
        done: (state, history) =>
          uniqueOrder(state) &&
          witnessed(state, history, (action, before, after) =>
            sameOrderProtected("retry", action, before, after),
          ),
      },
      {
        id: "trade-duplicate",
        target: "#trade-duplicate",
        result: '[data-trade="result"]',
        event: "click",
        purpose: "点击“重复投递消息”，模拟同一条成单消息又到了一次。",
        invalid: "先确认已有一张订单，再重复投递，才能观察订单不会增加。",
        feedback:
          "这是预期保护：重复消息查回原订单，唯一订单总数和库存都不变，不会重复成单。",
        matches: (action, before, after) =>
          sameOrderProtected("duplicate", action, before, after),
        done: (state, history) =>
          uniqueOrder(state) &&
          witnessed(state, history, (action, before, after) =>
            sameOrderProtected("duplicate", action, before, after),
          ),
      },
      {
        id: "trade-ack",
        target: "#trade-ack",
        result: '[data-trade="pending"]',
        event: "click",
        purpose: "订单结果已经保存，点击“确认消息”，结束这次处理。",
        invalid:
          "没有持久订单结果时不能确认，这是预期保护。请先消费成单，再确认消息。",
        feedback:
          "有了持久订单结果，才确认这条消息。待确认消息减少，订单和库存不会因为确认而改变。",
        matches: (action, before, after) =>
          Boolean(
            action === "ack" &&
            uniqueOrder(before) &&
            !request(before).acknowledged &&
            uniqueOrder(after) &&
            request(after).id === request(before).id &&
            request(after).acknowledged &&
            after.stock === before.stock &&
            after.orders.length === before.orders.length,
          ),
        done: (state) => uniqueOrder(state) && request(state).acknowledged,
      },
    ],
    branch: { id: "trade-recovery", label: "取消重买与旧补偿" },
  };
}

export function makeTradeRecoveryTour(snapshot = {}) {
  const current = request(snapshot);
  const currentOrder = orderFor(snapshot);
  const owner =
    currentOrder?.status === "UNPAID"
      ? current.id
      : snapshot.compensations?.at(-1)?.owner;
  const cancelled = (state) =>
    Boolean(
      owner &&
      orderFor(state, owner)?.status === "CANCELLED" &&
      compensationFor(state, owner),
    );
  const compensated = (state) =>
    cancelled(state) &&
    compensationFor(state, owner).applied &&
    state.reservation !== owner;
  const repurchased = (state) =>
    Boolean(
      compensated(state) && activeRequest(state) && request(state).id !== owner,
    );
  const oldProtected = (action, before, after) =>
    Boolean(
      action === "old-compensation" &&
      repurchased(before) &&
      before.compensations[0]?.owner !== before.reservation &&
      repurchased(after) &&
      sameBusinessState(before, after),
    );
  return {
    id: "trade-recovery",
    title: "取消后重买，旧补偿不能误释放新库存",
    lesson:
      "补偿先核对购买归属，再归还库存。即使旧补偿晚到，也不能释放新购买已经占用的库存。",
    blocked:
      !owner || (current?.phase === "UNCERTAIN" && !compensated(snapshot))
        ? {
            message:
              "这条分支需要一张待支付订单。请先回到主线完成抢券和消费成单，再选择本分支；不会自动补操作或重置。",
            actionLabel: "回到受理与成单主线",
          }
        : null,
    steps: [
      {
        id: "trade-cancel",
        target: "#trade-cancel",
        result: '[data-trade="result"]',
        event: "click",
        purpose: "点击“取消待支付订单”，留意库存是否马上归还。",
        invalid: "只有待支付订单可以取消。可回到主线先完成抢券和成单。",
        feedback:
          "订单已取消，补偿任务已保存，库存暂时不变。取消订单和真正归还库存是两个步骤。",
        matches: (action, before, after) =>
          Boolean(
            action === "cancel" &&
            orderFor(before, owner)?.status === "UNPAID" &&
            request(before)?.id === owner &&
            cancelled(after) &&
            after.compensations.length === before.compensations.length + 1 &&
            after.stock === before.stock &&
            after.reservation === before.reservation,
          ),
        done: cancelled,
      },
      {
        id: "trade-compensate",
        target: "#trade-compensate",
        result: '[data-trade="stock"]',
        event: "click",
        purpose: "点击“应用取消补偿”，核对归属后归还这份库存。",
        invalid:
          "需要先取消订单，并保留这次购买的预占。没有待处理补偿时不会凭空增加库存，这是预期保护。",
        feedback:
          "补偿确认库存属于这次取消的购买，才恢复一份库存并释放旧预占。现在可以再次购买。",
        matches: (action, before, after) =>
          Boolean(
            action === "compensate" &&
            cancelled(before) &&
            !compensationFor(before, owner).applied &&
            before.reservation === owner &&
            compensated(after) &&
            after.reservation === null &&
            after.stock === before.stock + 1 &&
            after.requests.length === before.requests.length &&
            after.orders.length === before.orders.length,
          ),
        done: compensated,
      },
      {
        id: "trade-repurchase",
        target: "#trade-accept",
        result: '[data-trade="owner"]',
        event: "click",
        purpose: "再点一次“抢券并受理”，开始一笔新的购买。",
        invalid: "先应用取消补偿，归还旧库存并释放旧预占，再重新购买。",
        feedback:
          "新购买拿到新的受理ID，库存重新预占一份。预占归属已经换人，旧购买的补偿不能动它。",
        matches: (action, before, after) =>
          compensated(before) &&
          accepted(action, before, after) &&
          repurchased(after),
        done: repurchased,
      },
      {
        id: "trade-old-compensation",
        target: "#trade-old-compensation",
        result: '[data-trade="result"]',
        event: "click",
        purpose: "点击“投递旧补偿”，让旧购买的补偿晚到一次。",
        invalid: "先取消、补偿并重新抢券，才能对比旧补偿与新预占的归属。",
        feedback:
          "这是预期保护：旧补偿与新预占的归属不同，系统拒绝释放。新购买的库存保留，库存不会凭空增加。",
        matches: oldProtected,
        done: (state, history) =>
          repurchased(state) && witnessed(state, history, oldProtected),
      },
    ],
  };
}
