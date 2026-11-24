const demoURL = new URL("../demo/index.html", import.meta.url);
const freeHeadings = {
  customer: ["探索附近", "查看门店、优惠和顾客订单。"],
  merchant: ["商家工作台", "按门店和成员身份查看订单、核销与操作记录。"],
  community: ["探店社区", "浏览、搜索与发布探店笔记。"],
};

export async function mountEmbeddedDemo(host, { navigate, onReady } = {}) {
  const response = await fetch(demoURL);
  if (!response.ok) throw new Error(`演示面板加载失败（${response.status}）`);

  const source = new DOMParser().parseFromString(
    await response.text(),
    "text/html",
  );
  const main = source.querySelector("main.demo-layout");
  const intro = source.getElementById("demo-intro");
  const toast = source.getElementById("demo-toast");
  if (!main || !intro || !toast) throw new Error("演示面板内容不完整。");

  main
    .querySelectorAll("[data-view-panel] .view-heading")
    .forEach((heading) => {
      const view = heading.closest("[data-view-panel]").dataset.viewPanel;
      const [title, description] = freeHeadings[view] || [];
      if (!title) return;
      heading.querySelector(".eyebrow")?.remove();
      heading.querySelector("h1").textContent = title;
      heading.querySelector("p").textContent = description;
    });

  const root = document.createElement("div");
  root.className = "embedded-demo demo-body";
  root.dataset.embeddedDemo = "";

  // Reuse the standalone panel without copying its header or nesting a main.
  const layout = document.createElement("div");
  layout.className = "demo-layout";
  main.querySelector("#scene-selector")?.remove();
  layout.append(...main.children);
  root.append(layout, intro, toast);
  host.replaceChildren(root);

  const { connectEmbeddedDemo } = await import("../demo/app.js");
  const controller = connectEmbeddedDemo(navigate);
  onReady?.(controller);
  return controller;
}
