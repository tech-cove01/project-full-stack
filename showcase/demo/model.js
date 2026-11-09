// Presentation-only state machine, mirroring the documented business rules.
export const SHOPS = [
  {
    id: "coffee",
    name: "街角咖啡",
    branch: "梧桐路店",
    category: "咖啡 · 烘焙",
    location: "梧桐路 28 号",
    description: "一杯拿铁，一份可颂，给周末留一点慢下来的时间。",
    offer: "30元到店代金券",
    price: 1900,
    value: 3000,
    stock: 5,
  },
  {
    id: "food",
    name: "青禾食堂",
    branch: "花园店",
    category: "简餐 · 轻食",
    location: "花园街 16 号",
    description: "在花园旁好好吃一餐，尝尝当季蔬菜和热腾腾的饭。",
    offer: "20元到店代金券",
    price: 990,
    value: 2000,
    stock: 5,
  },
];
export const STATUS = {
  PROCESSING: "受理中",
  UNPAID: "待支付",
  PAID: "待核销",
  REDEEMED: "已核销",
  CANCELLED: "已取消",
  REFUNDED: "已退款",
};
const views = ["customer", "merchant", "community"];
const shopExists = (id) => SHOPS.some((s) => s.id === id);
export const shopById = (id) => SHOPS.find((s) => s.id === id);
export const money = (cents) => (cents / 100).toFixed(cents % 100 ? 2 : 0);
export function initialState() {
  return {
    version: 1,
    view: "customer",
    shop: "coffee",
    role: "STAFF",
    merchantShop: "coffee",
    nextId: 1,
    orders: [],
    audit: [],
    notes: [
      {
        id: "seed-1",
        shop: "coffee",
        title: "梧桐路的午后，咖啡和可颂刚刚好",
        content:
          "喜欢这家临街的小店。窗边晒得到阳光，拿铁口感很柔和，适合和朋友慢慢聊天。",
        author: "阿橙",
        mine: false,
        likes: 18,
        liked: false,
      },
      {
        id: "seed-2",
        shop: "food",
        title: "花园边的小食堂，一份认真做的午餐",
        content:
          "青禾食堂的时蔬很新鲜，吃完还能顺路逛逛花园。一个人的周末，也可以好好吃饭。",
        author: "一颗青豆",
        mine: false,
        likes: 12,
        liked: false,
      },
    ],
  };
}
export const latestOrder = (state, shop) =>
  [...state.orders].reverse().find((o) => o.shop === shop);
export const availableStock = (state, shop) =>
  shopById(shop).stock -
  state.orders.filter(
    (o) => o.shop === shop && !["CANCELLED", "REFUNDED"].includes(o.status),
  ).length;
export const canManage = (state) => state.role === "OWNER";
export const canAccess = (state, shop) => canManage(state) || shop === "coffee";
function record(state, order, actor, text) {
  state.audit.push({ orderId: order.id, shop: order.shop, actor, text });
  if (state.audit.length > 100) state.audit.shift();
}
export function transition(current, action) {
  const state = structuredClone(current);
  const fail = (message) => ({ state: current, message, changed: false });
  let message = "";
  if (action.type === "VIEW") {
    if (!views.includes(action.view)) return fail("请选择有效页面。");
    state.view = action.view;
  } else if (action.type === "SHOP") {
    if (!shopExists(action.shop)) return fail("示例门店不存在。");
    state.shop = action.shop;
  } else if (action.type === "WORKSPACE") {
    if (!["OWNER", "STAFF"].includes(action.role) || !shopExists(action.shop))
      return fail("请选择有效的示例身份与门店。");
    state.role = action.role;
    state.merchantShop = action.shop;
  } else if (action.type === "BUY") {
    if (!shopExists(action.shop)) return fail("示例活动不存在。");
    const existing = state.orders.find(
      (o) => o.shop === action.shop && o.status !== "CANCELLED",
    );
    if (existing)
      return fail(
        existing.status === "REFUNDED"
          ? "这张券已退款，同一活动不能再次购买。可重新体验来恢复示例。"
          : "这张券已经受理过了，请继续处理已有订单。",
      );
    if (availableStock(state, action.shop) < 1) return fail("示例库存不足。");
    if (state.orders.length >= 40)
      return fail("体验次数较多，请重新体验以恢复示例。");
    const order = {
      id: String(10000000 + state.nextId++),
      shop: action.shop,
      status: "PROCESSING",
      token: "",
    };
    state.orders.push(order);
    record(state, order, "顾客", "抢券请求已受理，预占1份库存");
    message = "请求已受理，正在模拟异步成单。";
  } else if (
    ["CREATE", "PAY", "CANCEL", "REDEEM", "REFUND"].includes(action.type)
  ) {
    const order = state.orders.find((o) => o.id === action.id);
    if (!order) return fail("示例订单不存在。");
    if (action.type === "CREATE") {
      if (order.status !== "PROCESSING") return fail("");
      order.status = "UNPAID";
      record(state, order, "系统", "订单生成，等待模拟支付");
      message = "订单已生成，可以模拟支付。";
    } else if (action.type === "PAY") {
      if (order.status !== "UNPAID")
        return fail("只有待支付订单可以模拟支付。");
      order.status = "PAID";
      order.token = `DEMO-${order.id.slice(-4)}-CAFE`;
      record(state, order, "顾客", "模拟支付完成，生成核销凭证");
      message = "模拟支付成功。现在可以切换店员核销。";
    } else if (action.type === "CANCEL") {
      if (order.status === "CANCELLED")
        return fail("该订单已取消，库存无需重复恢复。");
      if (order.status !== "UNPAID") return fail("只有待支付订单可以取消。");
      order.status = "CANCELLED";
      record(state, order, "顾客", "订单取消，恢复1份库存，可重新购买");
      message = "已取消，库存已恢复，可以重新购买。";
    } else {
      if (!canAccess(state, order.shop) || state.merchantShop !== order.shop)
        return fail("当前示例身份没有此门店的操作权限。");
      if (action.type === "REDEEM") {
        if (order.status === "REDEEMED")
          return fail("该订单已经核销，无需重复处理。");
        if (order.status !== "PAID")
          return fail("只有已支付、未核销订单可以核销。");
        if (String(action.token || "").trim() !== order.token)
          return fail("核销凭证无效，请使用顾客订单中的示例凭证。");
        order.status = "REDEEMED";
        record(
          state,
          order,
          state.role === "OWNER" ? "店主" : "店员",
          "凭证核验通过，到店核销完成",
        );
        message = "核销成功，顾客订单也已更新。";
      } else {
        if (!canManage(state))
          return fail("当前为店员身份，退款需要店主权限。");
        if (order.status === "REFUNDED")
          return fail("该订单已退款，库存无需重复恢复。");
        if (order.status !== "PAID")
          return fail("仅已支付且未核销的订单可以退款。");
        order.status = "REFUNDED";
        record(
          state,
          order,
          "店主",
          "模拟退款完成，恢复1份库存，保留购买资格记录",
        );
        message = "已模拟退款，库存恢复；同一活动不能再次购买。";
      }
    }
  } else if (action.type === "PUBLISH") {
    const title = String(action.title || "").trim(),
      content = String(action.content || "").trim();
    if (
      !title ||
      title.length > 40 ||
      !content ||
      content.length > 500 ||
      !shopExists(action.shop)
    )
      return fail("请填写1–40字标题、1–500字体验，并选择门店。");
    if (state.notes.filter((n) => n.mine).length >= 10)
      return fail("本次已有10篇示例笔记，可以重新体验后继续。");
    state.notes.unshift({
      id: `note-${state.nextId++}`,
      shop: action.shop,
      title,
      content,
      author: "小林",
      mine: true,
      likes: 0,
      liked: false,
    });
    message = "示例笔记已发布，已加入探店列表。";
  } else if (action.type === "LIKE") {
    const note = state.notes.find((n) => n.id === action.id);
    if (!note || typeof action.liked !== "boolean") return fail("笔记不存在。");
    if (note.liked === action.liked) return fail("");
    note.liked = action.liked;
    note.likes += action.liked ? 1 : -1;
  } else return fail("未知的示例操作。");
  return { state, message, changed: true };
}
export function restoreState(raw) {
  try {
    const state = JSON.parse(raw);
    if (
      !state ||
      state.version !== 1 ||
      !views.includes(state.view) ||
      !shopExists(state.shop) ||
      !shopExists(state.merchantShop) ||
      !["OWNER", "STAFF"].includes(state.role) ||
      !Number.isInteger(state.nextId) ||
      state.nextId < 1 ||
      state.nextId > 10000
    )
      return null;
    if (
      !Array.isArray(state.orders) ||
      state.orders.length > 40 ||
      !Array.isArray(state.notes) ||
      state.notes.length > 12 ||
      !Array.isArray(state.audit) ||
      state.audit.length > 100
    )
      return null;
    if (
      state.orders.some(
        (o) =>
          !o ||
          !/^\d{8}$/.test(o.id) ||
          !shopExists(o.shop) ||
          !Object.hasOwn(STATUS, o.status) ||
          typeof o.token !== "string" ||
          o.token.length > 40 ||
          (["PAID", "REDEEMED", "REFUNDED"].includes(o.status)
            ? !/^DEMO-\d{4}-CAFE$/.test(o.token)
            : o.token !== ""),
      )
    )
      return null;
    if (
      new Set(state.orders.map((o) => o.id)).size !== state.orders.length ||
      SHOPS.some((s) => availableStock(state, s.id) < 0)
    )
      return null;
    if (
      state.notes.some(
        (n) =>
          !n ||
          typeof n.id !== "string" ||
          n.id.length > 30 ||
          !shopExists(n.shop) ||
          typeof n.title !== "string" ||
          n.title.length > 40 ||
          typeof n.content !== "string" ||
          n.content.length > 500 ||
          typeof n.author !== "string" ||
          n.author.length > 30 ||
          typeof n.mine !== "boolean" ||
          typeof n.liked !== "boolean" ||
          !Number.isInteger(n.likes) ||
          n.likes < (n.liked ? 1 : 0) ||
          n.likes > 1000,
      )
    )
      return null;
    if (
      state.audit.some(
        (a) =>
          !a ||
          !shopExists(a.shop) ||
          typeof a.orderId !== "string" ||
          typeof a.actor !== "string" ||
          a.actor.length > 20 ||
          typeof a.text !== "string" ||
          a.text.length > 200,
      )
    )
      return null;
    return state;
  } catch {
    return null;
  }
}
