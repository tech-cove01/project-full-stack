const flows = {
  trade: {
    label: "交易与履约",
    title: "先受理，再成单",
    summary: "受理回执提供查询凭据。订单持久化后才进入支付与核销。",
    steps: [
      ["顾客操作", "抢券、查单与模拟支付"],
      ["Go 接口", "校验身份与业务条件"],
      ["Redis Lua / Stream", "原子预占与消息受理"],
      ["订单消费者", "MySQL 事务与幂等成单"],
      ["门店履约", "凭证核验、核销与审计"],
    ],
    checks: [
      "重试同一请求使用原受理编号。",
      "成单或保存待核对记录后确认消息。",
      "延迟补偿仅处理原来的预占。",
    ],
  },
  community: {
    label: "社区与搜索",
    title: "先提交业务事实，再更新查询投影",
    summary: "笔记、关注和点赞先写入 MySQL。独立消费者逐步更新查询所需的数据。",
    steps: [
      ["社区操作", "发布、关注与点赞"],
      ["MySQL + Outbox", "事实与事件同事务"],
      ["Relay / Kafka", "批量投递与失败重试"],
      ["独立消费组", "关注、Feed 与搜索投影"],
      ["公开查询", "缓存装配与事实复核"],
    ],
    checks: [
      "Feed 缓存公共正文，单独读取个人点赞与当前可见性。",
      "消费收据去重，版本检查保护新状态。",
      "返回搜索前复核 MySQL 的公开状态和业务版本。",
    ],
  },
  ai: {
    label: "只读业务 AI",
    title: "按当前身份检索并带回来源",
    summary: "业务助手串起知识检索和经授权的业务查询，结果随会话返回。",
    steps: [
      ["Vue 会话", "提问与查看来源"],
      ["Go 会话编排", "校验身份与会话归属"],
      ["知识 / 只读工具", "发布规则、本人订单与库存"],
      ["Chat / Embedding", "生成回答与检索向量"],
      ["应用层 SSE", "答案及业务卡片"],
    ],
    checks: [
      "仅检索已发布的知识版本。",
      "服务端按当前身份限定业务查询范围。",
      "回答附规则来源或可查看的只读业务结果。",
    ],
  },
};

const experiments = [
  ["#recovery", "抢券重试与恢复"],
  ["#feed", "Feed 缓存与回源"],
  ["#events", "事件投递与搜索"],
  ["#likes", "点赞写入与积压"],
];

const rounds = {
  H3: {
    label: "第一轮 · H3",
    p95: 62,
    p99: 102,
    recovery: "1.65",
    overview: {
      file: "h3-overview.png",
      alt: "H3 PTS 报告概览，含准备阶段，平均 2596 TPS，峰值 3074 TPS",
      width: 2132,
      height: 1221,
    },
    generator: {
      file: "h3-generator.png",
      alt: "H3 PTS 施压端集群的 CPU、内存和带宽监控",
      width: 2136,
      height: 1214,
    },
  },
  H4: {
    label: "第二轮 · H4",
    p95: 55,
    p99: 94,
    recovery: "1.88",
    overview: {
      file: "h4-overview.png",
      alt: "H4 PTS 报告概览，含准备阶段，平均 2608 TPS，峰值 3063 TPS",
      width: 2122,
      height: 1264,
    },
    generator: {
      file: "h4-generator.png",
      alt: "H4 PTS 施压端集群的 CPU、内存和带宽监控",
      width: 2117,
      height: 1212,
    },
  },
};

const reportModes = {
  overview: "报告概览",
  generator: "施压端监控",
};

function markChoice(buttons, key, value) {
  for (const button of buttons) {
    button.setAttribute("aria-pressed", String(button.dataset[key] === value));
  }
}

function mountDesign(root) {
  root.innerHTML = `
    <header class="workspace-heading">
      <h1>设计详解</h1>
      <p>切换链路，查看操作写到哪里、何时形成结果，以及读取时怎样核对事实。</p>
    </header>
    <div class="work-panel project-controls">
      <div class="project-choices" role="group" aria-label="选择设计链路">
        ${Object.entries(flows)
          .map(
            ([id, flow]) => `
          <button type="button" data-project-flow="${id}" aria-controls="project-flow-detail">${flow.label}</button>
        `,
          )
          .join("")}
      </div>
      <a class="workspace-quiet-button project-link" href="../details.html">打开完整设计文档</a>
    </div>
    <div class="project-grid">
      <section class="work-panel project-card" id="project-flow-detail" aria-live="polite"></section>
      <aside class="work-panel project-card">
        <h2>操作验证</h2>
        <p>在工作区里打开机制实验，核对每次操作对应的状态。</p>
        <ul class="project-experiment-list">
          ${experiments.map(([href, label]) => `<li><a href="${href}">${label}</a></li>`).join("")}
        </ul>
      </aside>
    </div>
  `;
  const buttons = [...root.querySelectorAll("[data-project-flow]")];
  const detail = root.querySelector("#project-flow-detail");

  function render(flowId) {
    const flow = flows[flowId];
    markChoice(buttons, "projectFlow", flowId);
    detail.innerHTML = `
      <h2>${flow.title}</h2>
      <p>${flow.summary}</p>
      <ol class="project-flow-track" aria-label="${flow.label}的处理顺序">
        ${flow.steps.map(([title, desc]) => `<li><strong>${title}</strong><span>${desc}</span></li>`).join("")}
      </ol>
      <div class="project-checks">
        <h3>关键检查</h3>
        <ul>${flow.checks.map((item) => `<li>${item}</li>`).join("")}</ul>
      </div>
    `;
  }

  for (const button of buttons) {
    button.addEventListener("click", () => render(button.dataset.projectFlow));
  }
  render("trade");
}

function mountPerformance(root) {
  root.innerHTML = `
    <header class="workspace-heading">
      <h1>点赞实测</h1>
      <p>16 vCPU / 32 GiB 单机，独立 PTS 内网发压。查看两轮历史点赞与取消结果。</p>
    </header>
    <div class="work-panel project-controls">
      <div class="project-choices" role="group" aria-label="选择实测轮次">
        ${Object.entries(rounds)
          .map(
            ([id, round]) => `
          <button type="button" data-project-round="${id}" aria-controls="workspace-report-panel workspace-round-metrics">${round.label}</button>
        `,
          )
          .join("")}
      </div>
      <a class="workspace-quiet-button project-link" href="../performance.html">打开完整实测报告</a>
    </div>
    <div class="project-grid">
      <section class="work-panel project-card" id="workspace-report-panel" aria-labelledby="workspace-report-heading">
        <div class="project-report-header">
          <h2 id="workspace-report-heading">原始报告</h2>
          <div class="project-choices" role="group" aria-label="选择报告图片">
            ${Object.entries(reportModes)
              .map(
                ([id, label]) => `
              <button type="button" data-project-report="${id}" aria-controls="workspace-report-figure">${label}</button>
            `,
              )
              .join("")}
          </div>
        </div>
        <figure class="project-report-figure" id="workspace-report-figure">
          <a id="workspace-report-preview" aria-describedby="workspace-report-caption" aria-label="放大原始报告图片">
            <img loading="lazy" alt="" />
          </a>
          <figcaption id="workspace-report-caption"></figcaption>
        </figure>
      </section>
      <aside class="work-panel project-card" id="workspace-round-metrics" aria-live="polite"></aside>
    </div>
    <dialog class="project-report-dialog" id="workspace-report-dialog" aria-labelledby="workspace-report-dialog-title">
      <div class="project-dialog-head">
        <h2 id="workspace-report-dialog-title">原始报告</h2>
        <div class="project-choices">
          <button id="workspace-report-zoom" type="button" aria-pressed="false">原尺寸查看</button>
          <button id="workspace-report-close" type="button" autofocus>关闭</button>
        </div>
      </div>
      <div class="project-dialog-image"><img alt="" /></div>
    </dialog>
  `;
  const roundButtons = [...root.querySelectorAll("[data-project-round]")];
  const modeButtons = [...root.querySelectorAll("[data-project-report]")];
  const metrics = root.querySelector("#workspace-round-metrics");
  const preview = root.querySelector("#workspace-report-preview");
  const image = preview.querySelector("img");
  const caption = root.querySelector("#workspace-report-caption");
  const dialog = root.querySelector("#workspace-report-dialog");
  const dialogTitle = dialog.querySelector("h2");
  const viewport = dialog.querySelector(".project-dialog-image");
  const dialogImage = viewport.querySelector("img");
  const zoomButton = dialog.querySelector("#workspace-report-zoom");
  let activeRound = "H3";
  let activeMode = "overview";
  let restoreFocus = true;

  function renderImage() {
    const report = rounds[activeRound][activeMode];
    markChoice(modeButtons, "projectReport", activeMode);
    preview.href = new URL(`../assets/${report.file}`, import.meta.url).href;
    image.width = report.width;
    image.height = report.height;
    image.alt = report.alt;
    image.src = preview.href;
    caption.textContent =
      activeMode === "overview"
        ? `${activeRound} · PTS 全报告概览，含准备阶段。点击放大。`
        : `${activeRound} · 独立 PTS 施压端集群资源。点击放大。`;
  }

  function renderMetrics() {
    const round = rounds[activeRound];
    markChoice(roundButtons, "projectRound", activeRound);
    metrics.innerHTML = `
      <h2>${round.label} 正式主窗</h2>
      <dl class="project-metrics">
        <div><dt>成功变更 QPS</dt><dd>3008.33</dd></div>
        <div><dt>成功变更 / 180 秒</dt><dd>541,499</dd></div>
        <div><dt>P95</dt><dd>${round.p95} ms</dd></div>
        <div><dt>P99</dt><dd>${round.p99} ms</dd></div>
      </dl>
      <p>正式主窗错误、未知结果、丢弃均为 0。结束后恢复约 ${round.recovery} 秒。</p>
      <p class="project-scope">PTS 全报告约 210 秒。每轮 543,495 次请求含 1,996 次准备阶段的无变化请求，成功变更 QPS 按 180 秒正式主窗计算。</p>
    `;
  }

  function setZoom(zoomed) {
    viewport.classList.toggle("is-zoomed", zoomed);
    zoomButton.setAttribute("aria-pressed", String(zoomed));
    zoomButton.textContent = zoomed ? "适应窗口" : "原尺寸查看";
    viewport.scrollTo(0, 0);
  }

  for (const button of roundButtons) {
    button.addEventListener("click", () => {
      activeRound = button.dataset.projectRound;
      renderMetrics();
      renderImage();
    });
  }
  for (const button of modeButtons) {
    button.addEventListener("click", () => {
      activeMode = button.dataset.projectReport;
      renderImage();
    });
  }

  preview.addEventListener("click", (event) => {
    if (event.ctrlKey || event.metaKey || event.shiftKey || event.altKey)
      return;
    if (typeof dialog.showModal !== "function") return;
    event.preventDefault();
    restoreFocus = true;
    dialogImage.src = preview.href;
    dialogImage.alt = image.alt;
    dialogTitle.textContent = `${activeRound} · ${reportModes[activeMode]}`;
    setZoom(false);
    dialog.showModal();
  });
  zoomButton.addEventListener("click", () => {
    setZoom(zoomButton.getAttribute("aria-pressed") !== "true");
  });
  dialog
    .querySelector("#workspace-report-close")
    .addEventListener("click", () => dialog.close());
  dialog.addEventListener("close", () => {
    if (restoreFocus && !root.closest("[hidden]"))
      preview.focus({ preventScroll: true });
  });

  renderImage();
  renderMetrics();
  return () => {
    if (!dialog.open) return;
    restoreFocus = false;
    dialog.close();
  };
}

export function mountProjectViews(designRoot, performanceRoot) {
  mountDesign(designRoot);
  return { closeReport: mountPerformance(performanceRoot) };
}
