import { mountProjectViews } from "./project-views.js";

const routes = {
  trade: { title: "一笔到店交易", panel: "trade-workspace" },
  business: { title: "探索附近", scene: "free", view: "customer" },
  merchant: { title: "商家工作台", scene: "free", view: "merchant" },
  community: { title: "探店社区", scene: "free", view: "community" },
  recovery: { title: "抢券重试与恢复", scene: "trade" },
  feed: { title: "关注动态与缓存", scene: "feed" },
  events: { title: "事件投递与搜索", scene: "events" },
  likes: { title: "点赞写入与积压", scene: "likes" },
  design: { title: "设计详解", panel: "workspace-design-panel" },
  performance: { title: "点赞实测", panel: "workspace-performance-panel" },
};

function readRoute() {
  const name = location.hash.slice(1);
  return Object.hasOwn(routes, name) ? name : "trade";
}

export function mountWorkspaceNavigation(trade) {
  const pick = (id) => document.getElementById(id);
  const main = pick("workspace-main");
  const nav = pick("workspace-nav");
  const navToggle = pick("workspace-nav-toggle");
  const helpButton = pick("show-workspace-help");
  const resetButton = pick("reset-workspace");
  const tools = pick("workspace-tools");
  const embeddedPanel = pick("workspace-embedded-panel");
  const embeddedContent = pick("workspace-embedded-content");
  const status = pick("workspace-module-status");
  const retry = pick("retry-workspace-module");
  const panels = [
    pick("trade-workspace"),
    embeddedPanel,
    pick("workspace-design-panel"),
    pick("workspace-performance-panel"),
  ];
  const project = mountProjectViews(panels[2], panels[3]);
  let active;
  let internalRoute;
  let embedding;
  let embedded;
  let revision = 0;

  const closeMenu = () => navToggle.setAttribute("aria-expanded", "false");
  function navigateFromDemo(scene, view) {
    const next = Object.keys(routes).find(
      (key) => routes[key].scene === scene && routes[key].view === view,
    );
    if (!next || next === readRoute()) return;
    internalRoute = next;
    location.hash = next;
  }

  function getEmbedded() {
    if (!embedding) {
      embedding = import("./embedded-demo.js")
        .then(({ mountEmbeddedDemo }) =>
          mountEmbeddedDemo(embeddedContent, { navigate: navigateFromDemo }),
        )
        .then((controller) => {
          embedded = controller;
          pick("demo-intro")?.addEventListener("close", () => {
            helpButton.setAttribute("aria-expanded", "false");
          });
          return embedded;
        })
        .catch((error) => {
          embedding = undefined;
          throw error;
        });
    }
    return embedding;
  }

  async function showRoute(name, { focus = false } = {}) {
    const change = ++revision;
    const prev = active;
    const current = routes[name];

    if (prev !== name) {
      if (prev === "trade") trade.leave();
      if (prev === "performance") project.closeReport();
      if (prev && routes[prev].scene) embedded?.deactivate();
    }

    active = name;
    for (const panel of panels) {
      panel.hidden = panel.id !== (current.panel ?? embeddedPanel.id);
    }
    pick("workspace-location").textContent = current.title;
    document.title = `${current.title} · Full Stack 演示台`;
    for (const link of nav.querySelectorAll("[data-workspace-route]")) {
      if (link.dataset.workspaceRoute === name)
        link.setAttribute("aria-current", "page");
      else link.removeAttribute("aria-current");
    }
    closeMenu();
    if (focus) {
      window.scrollTo({ top: 0, behavior: "instant" });
      main.focus({ preventScroll: true });
    }

    tools.hidden = !current.scene && name !== "trade";
    resetButton.textContent = name === "trade" ? "重置演示" : "重置此处";
    helpButton.disabled = Boolean(current.scene) && !embedded;
    resetButton.disabled = helpButton.disabled;
    helpButton.setAttribute("aria-expanded", "false");
    if (name === "trade") {
      helpButton.setAttribute("aria-controls", "workspace-guide");
      trade.enter();
    }
    if (!current.scene) return;

    retry.hidden = true;
    status.hidden = Boolean(embedded);
    status.textContent = embedded ? "" : "正在加载演示内容…";
    embeddedPanel.setAttribute("aria-busy", String(!embedded));

    try {
      const controller = embedded ?? (await getEmbedded());
      if (revision !== change) return;
      controller.select(current.scene, current.view);
      helpButton.disabled = false;
      resetButton.disabled = false;
      helpButton.setAttribute("aria-controls", "demo-intro");
      status.hidden = true;
      status.textContent = "";
    } catch (error) {
      if (revision !== change) return;
      console.error("Unable to load workspace module", error);
      status.hidden = false;
      status.textContent = "暂时无法加载演示内容。";
      retry.hidden = false;
    } finally {
      if (revision === change) embeddedPanel.setAttribute("aria-busy", "false");
    }
  }

  navToggle.addEventListener("click", () => {
    const expanded = navToggle.getAttribute("aria-expanded") === "true";
    navToggle.setAttribute("aria-expanded", String(!expanded));
  });
  nav.closest(".workspace-sidebar").addEventListener("keydown", (event) => {
    if (
      event.key !== "Escape" ||
      navToggle.getAttribute("aria-expanded") !== "true"
    )
      return;
    event.preventDefault();
    closeMenu();
    navToggle.focus();
  });
  nav.addEventListener("click", (event) => {
    const link = event.target.closest("[data-workspace-route]");
    if (
      link?.dataset.workspaceRoute === active &&
      link.hash === location.hash
    ) {
      closeMenu();
      main.focus({ preventScroll: true });
    }
  });
  document.querySelector(".skip-link").addEventListener("click", (event) => {
    event.preventDefault();
    main.focus();
  });
  helpButton.addEventListener("click", () => {
    if (active === "trade") trade.guide();
    else if (routes[active]?.scene && embedded) {
      embedded.guide();
      helpButton.setAttribute(
        "aria-expanded",
        String(Boolean(pick("demo-intro")?.open)),
      );
    }
  });
  resetButton.addEventListener("click", () => {
    if (active === "trade") trade.reset();
    else if (routes[active]?.scene) embedded?.reset();
  });
  retry.addEventListener("click", () => {
    void showRoute(readRoute());
  });
  window.addEventListener("hashchange", () => {
    const next = readRoute();
    const focus = next !== internalRoute;
    internalRoute = undefined;
    if (next !== active) void showRoute(next, { focus });
  });

  navToggle.hidden = false;
  void showRoute(readRoute());
}
