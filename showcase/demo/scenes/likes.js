export function initialLikes() {
  return {
    liked: false,
    version: 0,
    events: [],
    mode: "batch",
    paused: false,
    publishRounds: 0,
    consumeRounds: 0,
    logs: [],
    comparison: null,
  };
}
export function likesStep(current, action) {
  const s = structuredClone(current);
  const log = (text) => {
    s.logs.unshift(text);
    s.logs = s.logs.slice(0, 12);
  };
  const write = (liked) => {
    if (s.liked === liked) {
      log("期望状态相同：没有重复计数，也不新增 Outbox 事件。");
      return;
    }
    s.liked = liked;
    s.version++;
    s.events.push({
      version: s.version,
      liked,
      published: false,
      receipt: false,
    });
    log(
      `业务事务提交：点赞=${liked ? 1 : 0}，版本${s.version}，事实和 Outbox 一起保存。`,
    );
  };
  const publish = () => {
    const batch = s.events
      .filter((e) => !e.published)
      .slice(0, s.mode === "batch" ? 4 : 1);
    if (!batch.length) {
      log("没有待发布的 Outbox。");
      return;
    }
    batch.forEach((e) => {
      e.published = true;
    });
    s.publishRounds++;
    log(
      `发布第${s.publishRounds}轮：${batch.length}条事件确认进入模拟消息队列。`,
    );
  };
  const consume = () => {
    if (s.paused) {
      log("消费已暂停：写入仍成功，等待收据的事件保留。");
      return;
    }
    const batch = s.events
      .filter((e) => e.published && !e.receipt)
      .slice(0, s.mode === "batch" ? 4 : 1);
    if (!batch.length) {
      log("没有待记录的消费收据。");
      return;
    }
    batch.forEach((e) => {
      e.receipt = true;
    });
    s.consumeRounds++;
    log(
      `消费第${s.consumeRounds}轮：记录${batch.length}条唯一收据。点赞事实不由消费链路写入。`,
    );
  };
  if (action.type === "WRITE") {
    if (s.events.length >= 64) {
      log("本组示例最多64条变更，请先重置场景。");
      return s;
    }
    write(action.liked);
  } else if (action.type === "GROUP") {
    if (s.events.length > 52) {
      log("请先重置，再生成新的示例组。");
      return s;
    }
    for (let i = 0; i < 12; i++) write(!s.liked);
    log("已完成12次交替状态变更；现在观察写入和下游是否同时完成。");
  } else if (
    action.type === "MODE" &&
    ["batch", "single"].includes(action.mode)
  )
    s.mode = action.mode;
  else if (action.type === "PAUSE") {
    s.paused = !s.paused;
    log(
      s.paused
        ? "已暂停消费，可继续点赞或发布事件观察积压。"
        : "消费恢复，已有积压仍需显式处理。",
    );
  } else if (action.type === "PUBLISH") publish();
  else if (action.type === "CONSUME") consume();
  else if (action.type === "DRAIN") {
    while (s.events.some((e) => !e.published)) publish();
    if (s.paused) log("消费者暂停，本组只完成发布，收据尚未齐全。");
    else while (s.events.some((e) => !e.receipt)) consume();
  } else if (action.type === "COMPARE") {
    s.comparison = ["single", "batch"].map((mode) => {
      let run = initialLikes();
      run.mode = mode;
      run = likesStep(run, { type: "GROUP" });
      run = likesStep(run, { type: "DRAIN" });
      return {
        mode,
        writes: run.version,
        receipts: run.events.filter((e) => e.receipt).length,
        publishRounds: run.publishRounds,
        consumeRounds: run.consumeRounds,
        count: Number(run.liked),
      };
    });
    log(
      "对同一组12次交替操作分别重放；结果一致，处理轮数不同。计数仅为浏览器机制示例。",
    );
  }
  return s;
}
const esc = (v) =>
  String(v).replace(
    /[&<>"']/g,
    (c) =>
      ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[
        c
      ],
  );
export function mount(container, { onChange = () => {} } = {}) {
  let state = initialLikes();
  function render() {
    const focus = document.activeElement?.id;
    const pending = state.events.filter((e) => !e.published).length,
      lag = state.events.filter((e) => e.published && !e.receipt).length,
      receipts = state.events.filter((e) => e.receipt).length;
    container.innerHTML = `<div class="scene-heading"><p class="eyebrow">04 / 互动与下游处理</p><h1>高并发点赞链路优化</h1><p>点赞成功以后，事件还需要发布和确认。先产生一组互动，再暂停消费观察积压，最后用有界批量处理并核对结果。</p></div><p class="scene-note">以下为浏览器机制模拟，批量上限4条只是便于观察；处理轮数不是数据库测量或QPS。</p>
    <div class="scene-actions"><button type="button" id="likes-write" class="button button-primary" data-like-action="like">点赞</button><button type="button" class="action-secondary" data-like-action="unlike">取消点赞</button><button type="button" class="action-secondary" data-like-action="retry">重试相同状态</button><button type="button" id="likes-group" class="action-secondary" data-like-action="group">生成一组互动</button></div>
    <div class="scene-stats"><div><span>当前点赞事实</span><strong id="likes-fact">${Number(state.liked)}</strong></div><div><span>成功变更 / 业务版本</span><strong>${state.version}</strong></div><div><span>待发布 Outbox</span><strong id="likes-pending">${pending}</strong></div><div><span>已发布待收据</span><strong id="likes-lag">${lag}</strong></div><div><span>唯一消费收据</span><strong id="likes-receipts">${receipts}</strong></div></div>
    <div class="scene-actions"><label>处理方式 <select id="likes-mode"><option value="single" ${state.mode === "single" ? "selected" : ""}>逐条处理</option><option value="batch" ${state.mode === "batch" ? "selected" : ""}>有界批量（最多4条）</option></select></label><button type="button" class="action-secondary" id="likes-pause" data-like-action="pause">${state.paused ? "恢复消费" : "暂停消费"}</button><button type="button" id="likes-publish" class="action-secondary" data-like-action="publish">发布一批事件</button><button type="button" id="likes-consume" class="action-secondary" data-like-action="consume">消费一批收据</button><button type="button" id="likes-drain" class="button button-primary" data-like-action="drain">处理本组积压</button></div>
    <div class="scene-result" id="likes-result" role="status"><h2>事实与下游核对</h2><p>${state.version === 0 ? "还没有状态变更。先点赞或生成一组互动。" : pending || lag ? `业务事实已提交；仍有${pending}条待发布、${lag}条待收据，不能只凭写入成功判断整条链路稳态。` : `本组${state.version}次变更已有${receipts}条唯一收据，待发布与待收据均为0。当前点赞数为${Number(state.liked)}，与最新期望状态一致。`}</p><p>本组发布${state.publishRounds}轮、消费${state.consumeRounds}轮。${state.paused ? "当前消费暂停。" : ""}</p></div>
    <div class="scene-actions"><button type="button" id="likes-compare" class="action-secondary" data-like-action="compare">对比同一组逐条与批量处理</button></div>
    ${state.comparison ? `<div class="scene-table-wrap" id="likes-comparison"><table><caption>同一组12次交替写入 · 模拟对照</caption><thead><tr><th>方式</th><th>成功变更/收据</th><th>发布轮数</th><th>消费轮数</th><th>最终点赞数</th></tr></thead><tbody>${state.comparison.map((r) => `<tr><th>${r.mode === "single" ? "逐条" : "有界批量"}</th><td>${r.writes}/${r.receipts}</td><td>${r.publishRounds}</td><td>${r.consumeRounds}</td><td>${r.count}</td></tr>`).join("")}</tbody></table></div>` : ""}
    <details class="scene-process"><summary>查看写入与下游处理过程</summary><p>点赞事实、计数与Outbox同事务写入；下游记录消费效果或忽略收据，不是异步落点赞计数。本模拟保留每个事件的唯一收据，再按上限分批处理。</p><ol class="scene-log">${state.logs.map((x) => `<li>${esc(x)}</li>`).join("")}</ol></details><div class="scene-evidence"><h2>真实实测与示例分开看</h2><p>16核32GB单机，两轮各180秒内网正式验证均为3000+成功QPS，P95≤62ms、P99≤102ms，并通过事实和下游稳态校验。</p><a class="text-link" href="../performance.html#screenshots">查看H3/H4原始报告与完整条件</a></div>`;
    if (focus)
      container
        .querySelector(`#${CSS.escape(focus)}`)
        ?.focus({ preventScroll: true });
    onChange();
  }
  container.addEventListener("click", (e) => {
    const action = e.target.closest("[data-like-action]")?.dataset.likeAction;
    if (!action) return;
    const actions = {
      like: { type: "WRITE", liked: true },
      unlike: { type: "WRITE", liked: false },
      retry: { type: "WRITE", liked: state.liked },
      group: { type: "GROUP" },
      pause: { type: "PAUSE" },
      publish: { type: "PUBLISH" },
      consume: { type: "CONSUME" },
      drain: { type: "DRAIN" },
      compare: { type: "COMPARE" },
    };
    state = likesStep(state, actions[action]);
    render();
  });
  container.addEventListener("change", (e) => {
    if (e.target.id === "likes-mode") {
      state = likesStep(state, { type: "MODE", mode: e.target.value });
      render();
    }
  });
  render();
  return {
    getSnapshot() {
      return structuredClone(state);
    },
    reset() {
      state = initialLikes();
      render();
    },
    getGuide() {
      return {
        title: "点赞成功后，下游跟上了吗？",
        text: "先点赞或生成一组互动，观察事实已更新而Outbox待处理；暂停消费继续发布，再恢复消费清空并核对。",
        steps: [
          "点赞/取消，查看事实与Outbox",
          "暂停消费，发布事件看待收据增长",
          "恢复并处理积压，核对唯一收据",
          "同组逐条/批量对比，另看真实报告",
        ],
        current: state.comparison
          ? 4
          : state.version && state.events.every((e) => e.receipt)
            ? 3
            : state.events.some((e) => e.published)
              ? 2
              : state.version
                ? 1
                : 0,
        label: "定位点赞操作",
        nextSelector: "#likes-write",
        design: "likes",
      };
    },
  };
}
