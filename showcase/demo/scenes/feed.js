// A deterministic browser simulation. These counters are not backend measurements.
const copy = (value) => structuredClone(value);
const escape = (value) =>
  String(value).replace(
    /[&<>"']/g,
    (c) =>
      ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[
        c
      ],
  );
const validCache = (entry, fact) => entry && entry.version === fact.version;
const publicEntry = (fact) => ({
  version: fact.version,
  title: fact.title,
  content: fact.content,
});
export function initialFeedState() {
  return {
    viewer: "A",
    likes: { A: false, B: false },
    fact: {
      version: 1,
      title: "梧桐路的咖啡香",
      content: "窗边的拿铁和可颂，适合慢慢过周末。",
      visible: true,
    },
    otherFact: {
      version: 1,
      title: "花园旁的轻食午餐",
      content: "另一组缺失内容，用来观察加载上限。",
      visible: true,
    },
    l1: null,
    redis: null,
    epoch: 0,
    active: null,
    queue: [],
    requests: [],
    sequence: 0,
    sourceReads: 0,
    l1Hits: 0,
    redisHits: 0,
    merged: 0,
    maxActive: 0,
    batchEver: false,
    cancelled: false,
    versionChanged: false,
    hiddenChecked: false,
    visited: ["A"],
    log: [],
    result: null,
  };
}
function log(state, message) {
  state.log.unshift(message);
  state.log = state.log.slice(0, 12);
}
function request(state, viewer, key, status) {
  const item = {
    id: `请求${++state.sequence}`,
    viewer,
    key,
    status,
    source: "",
    version: null,
    liked: null,
  };
  state.requests.push(item);
  state.requests = state.requests.slice(-16);
  return item;
}
function start(state, batch) {
  state.active = batch;
  state.sourceReads++;
  state.maxActive = Math.max(state.maxActive, 1);
  log(
    state,
    `${batch.key === "main" ? "咖啡笔记" : "午餐笔记"}批次开始回源；当前运行1批，其余批次排队。`,
  );
}
function finishResult(state, item, fact, source) {
  item.status = "已完成";
  item.source = source;
  item.version = fact.version;
  item.liked = state.likes[item.viewer];
  state.result = { ...item, title: fact.title, content: fact.content };
}
function loadOrJoin(state, viewer, key = "main") {
  const fact = key === "main" ? state.fact : state.otherFact;
  const item = request(state, viewer, key, "加载中");
  if (!fact.visible) {
    item.status = "已过滤";
    state.hiddenChecked = true;
    state.result = { ...item };
    log(state, "当前事实已隐藏笔记，即使旧缓存仍在，也不返回正文。");
    return;
  }
  if (key === "main" && validCache(state.l1, fact)) {
    state.l1Hits++;
    finishResult(state, item, fact, "进程内缓存");
    log(state, `${viewer}复用进程内公共正文，个人点赞仍独立读取。`);
    return;
  }
  if (key === "main" && validCache(state.redis, fact)) {
    state.redisHits++;
    state.l1 = copy(state.redis);
    finishResult(state, item, fact, "Redis缓存");
    log(state, "进程内缓存缺失，从Redis复用当前版本并填充进程内缓存。");
    return;
  }
  const batchKey = `${key}:v${fact.version}:e${state.epoch}`;
  if (state.active?.batchKey === batchKey) {
    state.active.ids.push(item.id);
    state.merged++;
    log(state, `${item.id}加入相同缺失批次，共享正在进行的回源。`);
    return;
  }
  const queued = state.queue.find((batch) => batch.batchKey === batchKey);
  if (queued) {
    queued.ids.push(item.id);
    item.status = "排队中";
    state.merged++;
    return;
  }
  const batch = {
    batchKey,
    key,
    snapshot: copy(fact),
    epoch: state.epoch,
    ids: [item.id],
  };
  if (state.active) {
    state.queue.push(batch);
    item.status = "排队中";
    log(state, "另一缺失批次排队；加载上限保持1批。");
  } else start(state, batch);
}
export function feedStep(previous, action) {
  const state = copy(previous);
  switch (action.type) {
    case "VISITOR":
      if (!["A", "B"].includes(action.viewer)) return previous;
      state.viewer = action.viewer;
      if (!state.visited.includes(action.viewer))
        state.visited.push(action.viewer);
      state.result = null;
      log(
        state,
        `已切换访客${action.viewer}，个人点赞为${state.likes[action.viewer] ? "已点赞" : "未点赞"}。`,
      );
      break;
    case "LIKE":
      state.likes[state.viewer] = !state.likes[state.viewer];
      log(
        state,
        `仅改变访客${state.viewer}的个人点赞，共享缓存中不保存此状态。`,
      );
      break;
    case "READ":
      loadOrJoin(state, state.viewer);
      break;
    case "CLEAR":
      if (state.active) {
        log(state, "请先完成当前批次，或重置场景后再做冷读。");
        break;
      }
      state.l1 = null;
      state.redis = null;
      state.epoch++;
      state.result = null;
      log(state, "已清空两级示例缓存；下一次读取将需要回源。");
      break;
    case "EVICT_L1":
      state.l1 = null;
      log(state, "只清除进程内缓存，保留Redis当前副本。");
      break;
    case "BATCH":
      if (state.active || state.l1 || state.redis) {
        log(state, "先清空两级缓存，再发起同批冷请求。");
        break;
      }
      if (!state.fact.visible) {
        log(state, "笔记已隐藏，请恢复公开后体验同批读取。");
        break;
      }
      state.batchEver = true;
      loadOrJoin(state, "A");
      loadOrJoin(state, "B");
      loadOrJoin(state, "A");
      break;
    case "CANCEL_FIRST": {
      const item = state.requests.find(
        (r) => state.active?.ids.includes(r.id) && r.status === "加载中",
      );
      if (!item) {
        log(state, "当前没有可取消的等待者。");
        break;
      }
      item.status = "已取消";
      state.cancelled = true;
      log(state, `${item.id}取消等待，共享回源继续服务其他等待者。`);
      break;
    }
    case "QUEUE_OTHER":
      if (!state.active) {
        log(state, "先开始同批冷请求，再加入另一批次观察排队。");
        break;
      }
      loadOrJoin(state, "B", "other");
      break;
    case "FINISH": {
      if (!state.active) {
        log(state, "当前没有进行中的回源。");
        break;
      }
      const batch = state.active,
        fact = batch.key === "main" ? state.fact : state.otherFact;
      const fresh =
        batch.snapshot.version === fact.version && batch.epoch === state.epoch;
      if (batch.key === "main" && fresh && fact.visible) {
        state.l1 = publicEntry(fact);
        state.redis = publicEntry(fact);
      }
      for (const id of batch.ids) {
        const item = state.requests.find((r) => r.id === id);
        if (!item || item.status === "已取消") continue;
        if (!fact.visible) {
          item.status = "已过滤";
          state.hiddenChecked = true;
          state.result = { ...item };
        } else if (!fresh) {
          item.status = "旧版本被拒绝";
          state.result = { ...item };
        } else finishResult(state, item, fact, "共享回源");
      }
      log(
        state,
        !fact.visible
          ? "回源完成，但当前可见性复核不通过，正文不返回。"
          : fresh
            ? "本批回源完成：未取消的等待者得到当前正文；个人点赞分别读取。"
            : "回源拿到旧版本，拒绝写回两级缓存；请重新读取当前版本。",
      );
      state.active = null;
      if (state.queue.length) {
        const next = state.queue.shift();
        next.ids.forEach((id) => {
          const item = state.requests.find((r) => r.id === id);
          if (item) item.status = "加载中";
        });
        start(state, next);
      }
      break;
    }
    case "VERSION":
      state.fact.version++;
      state.fact.title = `梧桐路的咖啡香 · 新版${state.fact.version}`;
      state.fact.content = "新版笔记补充了到店体验，旧正文不能覆盖这次修改。";
      state.l1 = null;
      state.redis = null;
      state.versionChanged = true;
      state.result = null;
      log(
        state,
        `事实已改为v${state.fact.version}，旧缓存失效；进行中的旧版本回源仍须复核。`,
      );
      break;
    case "HIDE":
      state.fact.visible = false;
      state.result = null;
      log(
        state,
        "笔记已隐藏，特意保留可能存在的旧缓存，下一次读取仍须检查当前可见性。",
      );
      break;
    case "SHOW":
      state.fact.visible = true;
      state.result = null;
      log(state, "笔记恢复公开，可以重新读取。");
      break;
    default:
      return previous;
  }
  return state;
}

export function mount(container, { onChange = () => {} } = {}) {
  let state = initialFeedState();
  function render() {
    const focusId = document.activeElement?.id;
    const result = state.result;
    const cards = state.requests
      .map(
        (r) =>
          `<li><strong>${escape(r.id)} · 访客${escape(r.viewer)}</strong> ${escape(r.status)}${r.version ? ` · v${r.version} · ${r.liked ? "已点赞" : "未点赞"}` : ""}</li>`,
      )
      .join("");
    container.innerHTML = `<div class="scene-heading"><p class="eyebrow">同一篇探店笔记，不同的个人状态</p><h1>Feed 分层缓存与并发回源控制</h1><p>先读取笔记，再切换访客。公共正文可以复用，个人点赞和当前可见性每次单独检查。</p></div>
      <p class="scene-note">以下缓存、请求与计数均为浏览器机制模拟，不是后端测量；相同缺失批次只在单进程内合并。</p>
      <div class="scene-actions" role="group" aria-label="选择Feed访客"><button id="feed-visitor-a" class="action-secondary" data-action="VISITOR" data-viewer="A" aria-pressed="${state.viewer === "A"}">访客A</button><button id="feed-visitor-b" class="action-secondary" data-action="VISITOR" data-viewer="B" aria-pressed="${state.viewer === "B"}">访客B</button><button id="feed-like" class="action-secondary" data-action="LIKE" aria-pressed="${state.likes[state.viewer]}">${state.likes[state.viewer] ? "取消当前访客点赞" : "当前访客点赞"}</button></div>
      <div class="scene-stats"><div><span>当前访客</span><strong>${state.viewer} · ${state.likes[state.viewer] ? "已点赞" : "未点赞"}</strong></div><div><span>事实版本与可见性</span><strong>v${state.fact.version} · ${state.fact.visible ? "公开" : "隐藏"}</strong></div><div><span>示例源读取批次</span><strong>${state.sourceReads}</strong></div><div><span>运行 / 排队批次</span><strong>${state.active ? 1 : 0} / ${state.queue.length}</strong></div></div>
      <div class="scene-actions"><button id="feed-read" class="button button-primary" data-action="READ">读取这篇笔记</button><button id="feed-finish" class="action-secondary" data-action="FINISH" ${state.active ? "" : "disabled"}>完成当前回源</button><button id="feed-l1" class="action-secondary" data-action="EVICT_L1">只清进程内缓存</button><button id="feed-clear" class="action-secondary" data-action="CLEAR" ${state.active ? "disabled" : ""}>清空两级缓存</button></div>
      <section class="scene-result" aria-label="Feed读取结果" aria-live="polite"><h2>本次看到的笔记</h2>${result?.status === "已完成" && state.fact.visible && result.version === state.fact.version ? `<h3>${escape(result.title)}</h3><p>${escape(result.content)}</p><p>来自${escape(result.source)} · v${result.version}；访客${escape(result.viewer)}的点赞：${result.liked ? "已点赞" : "未点赞"}</p>` : `<p>${result?.status === "已过滤" ? "当前笔记已隐藏，旧缓存不能让它重新出现。" : result?.status === "旧版本被拒绝" ? "旧回源没有被返回或写入缓存，请再读当前版本。" : state.active ? "正在等待回源，点击“完成当前回源”观察结果。" : "点击读取后，这里显示正文、来源和个人状态。"}</p>`}</section>
      <div class="scene-actions"><button id="feed-batch" class="action-secondary" data-action="BATCH">发起同批冷请求</button><button id="feed-cancel" class="action-secondary" data-action="CANCEL_FIRST" ${state.active ? "" : "disabled"}>取消一个等待者</button><button id="feed-queue" class="action-secondary" data-action="QUEUE_OTHER" ${state.active ? "" : "disabled"}>加入另一缺失批次</button></div>
      <p class="scene-note">对比分支：清空两级缓存 → 发起同批冷请求 → 取消一个等待者 → 完成回源。也可以在完成前修改版本或隐藏笔记，再检查旧回源是否被拒绝。</p>
      <div class="scene-actions"><button id="feed-version" class="action-secondary" data-action="VERSION">修改笔记版本</button><button id="feed-hide" class="action-secondary" data-action="${state.fact.visible ? "HIDE" : "SHOW"}">${state.fact.visible ? "隐藏笔记" : "恢复公开"}</button><button id="feed-reset" class="action-secondary" data-action="RESET">重置此场景</button></div>
      <details class="scene-process"><summary>查看请求、缓存与合并过程</summary><div class="scene-stats"><div><span>进程内命中</span><strong>${state.l1Hits}</strong></div><div><span>Redis命中</span><strong>${state.redisHits}</strong></div><div><span>加入共享加载</span><strong>${state.merged}</strong></div><div><span>最大同时加载</span><strong>${state.maxActive} / 1</strong></div></div><p>进程内缓存：${state.l1 ? `v${state.l1.version}` : "空"}；Redis：${state.redis ? `v${state.redis.version}` : "空"}。两级仅保存公共正文，不保存访客点赞。</p><ol class="scene-log">${cards || "<li>还没有读取请求。</li>"}</ol><ul class="scene-log">${state.log.map((item) => `<li>${escape(item)}</li>`).join("")}</ul></details>`;
    if (focusId) {
      const target = container.querySelector(`#${CSS.escape(focusId)}`);
      if (target && !target.disabled) target.focus({ preventScroll: true });
    }
  }
  function dispatch(action) {
    state =
      action.type === "RESET" ? initialFeedState() : feedStep(state, action);
    render();
    onChange();
  }
  container.addEventListener("click", (event) => {
    const button = event.target.closest("button[data-action]");
    if (button && container.contains(button) && !button.disabled)
      dispatch({ type: button.dataset.action, viewer: button.dataset.viewer });
  });
  render();
  return {
    reset() {
      state = initialFeedState();
      render();
      onChange();
    },
    getGuide() {
      let current = 0,
        nextSelector = "#feed-read",
        label = "先读取这篇笔记";
      if (state.sourceReads && !state.active) {
        if (!state.l1Hits) {
          label = "再读一次，看热缓存";
        } else if (state.visited.length < 2) {
          current = 1;
          nextSelector = "#feed-visitor-b";
          label = "切换访客B";
        } else if (!state.batchEver) {
          current = 2;
          nextSelector =
            state.l1 || state.redis ? "#feed-clear" : "#feed-batch";
          label = state.l1 || state.redis ? "先清空两级缓存" : "开始同批冷请求";
        } else {
          current = 3;
          nextSelector = state.versionChanged ? "#feed-hide" : "#feed-version";
          label = state.versionChanged ? "隐藏后再读取" : "试试修改版本";
        }
      }
      if (state.active) {
        current = state.batchEver ? 2 : 0;
        nextSelector =
          state.batchEver && !state.cancelled ? "#feed-cancel" : "#feed-finish";
        label =
          state.batchEver && !state.cancelled
            ? "取消一个等待者"
            : "完成当前回源";
      }
      if (state.hiddenChecked && state.versionChanged && !state.active) {
        current = 4;
        nextSelector = "#feed-reset";
        label = "重新体验此场景";
      }
      return {
        title: "同一正文可以复用，个人状态各自读取",
        text: "先点读取，再完成回源；热读后切换访客，最后试试同批取消和版本/可见性分支。所有计数都是本页机制模拟。",
        steps: [
          "冷读和热读，比较内容来源",
          "切换访客，个人点赞不串用",
          "同批合并，取消单个等待者",
          "修改版本或隐藏，拒绝旧内容",
        ],
        current,
        label,
        nextSelector,
        design: "feed",
        evidence:
          "https://github.com/tech-cove01/project-full-stack/blob/main/docs/reviews/2026-10-30/cap07-6693539/report.md",
      };
    },
  };
}
