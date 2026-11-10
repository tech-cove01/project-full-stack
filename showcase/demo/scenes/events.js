// A small browser-only model of facts, durable events and a search projection.
// It explains the existing system; it does not call Kafka, MySQL or Elasticsearch.
export function initialEventsState() {
  return {
    fact: null,
    index: null,
    events: [],
    receipts: [],
    esUp: true,
    sequence: 1,
    query: "咖啡",
    searches: 0,
    indexWrites: 0,
    duplicates: 0,
    staleIgnored: 0,
    replayed: 0,
    log: [],
    message: "先发布一篇笔记：事实和待投递事件会同时出现。",
    search: { candidates: 0, filtered: 0, results: [], reason: "尚无索引候选" },
  };
}

export function searchEvents(state, query = state.query) {
  if (!state.esUp)
    return {
      candidates: 0,
      filtered: 0,
      results: [],
      reason: "搜索索引暂不可用，当前不能完成搜索",
    };
  const candidate =
    state.index?.visible &&
    state.index.title.toLowerCase().includes(query.trim().toLowerCase());
  if (!candidate)
    return {
      candidates: 0,
      filtered: 0,
      results: [],
      reason: "索引中尚无匹配的公开候选",
    };
  if (!state.fact?.visible)
    return {
      candidates: 1,
      filtered: 1,
      results: [],
      reason: "索引仍命中旧内容，当前事实已隐藏，返回前过滤",
    };
  if (state.index.version !== state.fact.version)
    return {
      candidates: 1,
      filtered: 1,
      results: [],
      reason: "索引版本落后于当前事实，返回前过滤",
    };
  return {
    candidates: 1,
    filtered: 0,
    results: [{ ...state.fact }],
    reason: "候选的公开状态与版本均通过当前事实复核",
  };
}

function appendLog(state, text) {
  state.log.unshift(text);
  state.log = state.log.slice(0, 12);
  state.message = text;
}

function deliver(state, event, manual = false) {
  if (!event) {
    appendLog(state, "没有可投递的事件，请先发布或修改笔记。");
    return;
  }
  if (event.status === "PARKED" && !manual) {
    appendLog(
      state,
      `事件 ${event.id} 已停车；普通投递不会重试，请恢复索引后明确点击人工重放。`,
    );
    return;
  }
  if (state.receipts.includes(event.id)) {
    state.duplicates++;
    appendLog(state, `事件 ${event.id} 已有收据，重复投递未重复修改索引。`);
    return;
  }
  if (!state.esUp) {
    event.status = "PARKED";
    event.attempts++;
    appendLog(
      state,
      `索引暂不可用，事件 ${event.id} 已持久保存为停车记录，未确认消费成功。`,
    );
    return;
  }
  if (state.index && event.version < state.index.version) {
    state.staleIgnored++;
    appendLog(
      state,
      `旧事件 ${event.id} 的 v${event.version} 落后于索引 v${state.index.version}，保留当前新版本并保存处理收据。`,
    );
  } else {
    state.index = {
      id: event.noteId,
      title: event.title,
      version: event.version,
      visible: event.visible,
    };
    state.indexWrites++;
    appendLog(
      state,
      `${manual ? "人工重放" : "投递"}事件 ${event.id}：索引更新至 v${event.version}${event.visible ? "，公开内容可供搜索" : "，内容不再作为公开候选"}。`,
    );
  }
  event.status = "DELIVERED";
  event.attempts++;
  state.receipts.push(event.id);
  if (manual) state.replayed++;
}

export function stepEvents(current, action) {
  const state = structuredClone(current);
  if (action.type === "PUBLISH") {
    const title = String(action.title || "").trim();
    if (!title || title.length > 60) {
      appendLog(state, "请填写1–60字的示例笔记标题。");
    } else if (state.events.length >= 20) {
      appendLog(state, "本轮已有20条示例事件，请重置场景后继续。");
    } else {
      const version = (state.fact?.version || 0) + 1;
      state.fact = { id: "note-coffee", title, version, visible: true };
      const event = {
        id: `E${state.sequence++}`,
        noteId: state.fact.id,
        title,
        version,
        visible: true,
        status: "PENDING",
        attempts: 0,
      };
      state.events.push(event);
      appendLog(
        state,
        `事实 v${version} 与 Outbox 事件 ${event.id} 已一起保存；索引仍需投递后更新。`,
      );
    }
  } else if (action.type === "HIDE") {
    if (!state.fact) appendLog(state, "请先发布一篇示例笔记。");
    else if (!state.fact.visible)
      appendLog(state, "该笔记已隐藏，无需重复变更。");
    else if (state.events.length >= 20)
      appendLog(state, "本轮事件较多，请先重置场景。");
    else {
      state.fact = {
        ...state.fact,
        visible: false,
        version: state.fact.version + 1,
      };
      const event = {
        id: `E${state.sequence++}`,
        noteId: state.fact.id,
        title: state.fact.title,
        version: state.fact.version,
        visible: false,
        status: "PENDING",
        attempts: 0,
      };
      state.events.push(event);
      appendLog(
        state,
        `当前事实已隐藏为 v${state.fact.version}，Outbox 已保存；即使旧索引仍命中，公开搜索也会过滤。`,
      );
    }
  } else if (action.type === "DELIVER") {
    deliver(
      state,
      state.events.find((event) => event.id === action.id) ||
        (!action.id ? state.events.at(-1) : null),
    );
  } else if (action.type === "DUPLICATE") {
    const confirmed = [...state.events]
      .reverse()
      .find((event) => state.receipts.includes(event.id));
    deliver(state, confirmed);
  } else if (action.type === "OLD") {
    const old = state.events.find(
      (event) =>
        state.index &&
        event.version < state.index.version &&
        !state.receipts.includes(event.id),
    );
    if (old) deliver(state, old);
    else
      appendLog(
        state,
        "先连续保存两个版本，只投递最新事件，再点这里投递尚未确认的旧事件。",
      );
  } else if (action.type === "AVAILABILITY") {
    state.esUp = !state.esUp;
    appendLog(
      state,
      state.esUp
        ? "索引连接已恢复；停车记录保留，须由你点击人工重放，不会自动追平。"
        : "已模拟索引暂不可用。继续保存新版本并投递，可以观察停车记录。",
    );
  } else if (action.type === "REPLAY") {
    const parked = state.events.filter((event) => event.status === "PARKED");
    if (!state.esUp) appendLog(state, "请先恢复搜索索引，再明确执行人工重放。");
    else if (!parked.length) appendLog(state, "没有停车事件需要人工重放。");
    else for (const event of parked) deliver(state, event, true);
  } else if (action.type === "SEARCH") {
    state.query = String(action.query ?? "").slice(0, 80);
    state.searches++;
  }
  state.search = searchEvents(state);
  if (action.type === "SEARCH")
    appendLog(
      state,
      `搜索“${state.query || "全部"}”：候选 ${state.search.candidates}，事实复核过滤 ${state.search.filtered}，返回 ${state.search.results.length} 条。${state.search.reason}。`,
    );
  return state;
}

const escape = (value) =>
  String(value).replace(
    /[&<>"']/g,
    (char) =>
      ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[
        char
      ],
  );
const eventStatus = {
  PENDING: "待投递",
  PARKED: "停车待人工重放",
  DELIVERED: "已确认",
};

export function mount(container, { onChange = () => {} } = {}) {
  let state = initialEventsState();
  function render() {
    const focused = document.activeElement?.id;
    const parked = state.events.filter((event) => event.status === "PARKED");
    container.innerHTML = `<div class="scene-heading"><p class="eyebrow">可靠事件与 ES 搜索一致性</p><h1>笔记变了，搜索怎样跟上？</h1><p>发布一篇咖啡笔记，手动推进索引；再试试重复消息、乱序和搜索服务暂不可用。</p></div>
      <p class="scene-note">以下事实、消息、索引与计数均为可重置的浏览器示例，不连接真实数据库或搜索服务。</p>
      <label class="scene-field" for="events-title">示例笔记标题</label><input id="events-title" maxlength="60" value="${escape(state.fact?.title || "梧桐路咖啡周末")}">
      <div class="scene-actions"><button class="button button-primary" type="button" id="events-publish" data-event-action="publish">${state.fact ? "保存新版本" : "发布示例笔记"}</button><button class="action-secondary" type="button" id="events-deliver" data-event-action="deliver" ${state.events.length ? "" : "disabled"}>投递最新事件</button><button class="action-secondary" type="button" id="events-hide" data-event-action="hide" ${state.fact?.visible ? "" : "disabled"}>隐藏这篇笔记</button></div>
      <div class="scene-stats"><div><span>当前事实</span><strong id="events-fact-version">${state.fact ? `v${state.fact.version} · ${state.fact.visible ? "公开" : "隐藏"}` : "尚未发布"}</strong></div><div><span>搜索索引</span><strong id="events-index-version">${state.index ? `v${state.index.version} · ${state.index.visible ? "公开" : "隐藏"}` : "尚无索引"}</strong></div><div><span>停车记录 / 收据</span><strong id="events-counts">${parked.length} / ${state.receipts.length}</strong></div></div>
      <p class="scene-status" id="events-message" role="status">${escape(state.message)}</p>
      <div class="scene-actions"><button class="action-secondary" type="button" id="events-duplicate" data-event-action="duplicate" ${state.receipts.length ? "" : "disabled"}>重复投递已确认事件</button><button class="action-secondary" type="button" id="events-old" data-event-action="old">投递旧版本事件</button><button class="action-secondary" type="button" id="events-offline" data-event-action="availability">${state.esUp ? "让搜索索引暂不可用" : "恢复搜索索引"}</button><button class="action-secondary" type="button" id="events-replay" data-event-action="replay" ${state.esUp && parked.length ? "" : "disabled"}>人工重放停车事件</button></div>
      <div class="scene-result" id="events-search-results"><h2>公开搜索结果</h2><label class="scene-field" for="events-query">按标题查找</label><input type="search" id="events-query" maxlength="80" value="${escape(state.query)}"><button class="action-secondary" type="button" id="events-search" data-event-action="search">查询搜索结果</button><p id="events-search-summary">索引候选 ${state.search.candidates} · 事实过滤 ${state.search.filtered} · 返回 ${state.search.results.length} 条</p>${state.search.results.length ? state.search.results.map((note) => `<article><h3>${escape(note.title)}</h3><p>公开版本 v${note.version} · 街角咖啡</p></article>`).join("") : `<p>${escape(state.search.reason)}</p>`}</div>
      <details class="scene-process"><summary>查看消息、索引与停车过程</summary><p>示例索引写入 ${state.indexWrites} 次；重复投递 ${state.duplicates} 次；旧版本拒绝 ${state.staleIgnored} 次；人工重放 ${state.replayed} 条。</p><ul class="scene-log" id="events-event-list">${state.events.length ? state.events.map((event) => `<li><strong>${event.id} · v${event.version} · ${eventStatus[event.status]}</strong><span>${escape(event.title)} · ${event.visible ? "公开" : "隐藏"} · 尝试 ${event.attempts} 次</span></li>`).join("") : "<li>发布时，事实和Outbox事件一起出现。</li>"}</ul><ol class="scene-log">${state.log.map((text) => `<li>${escape(text)}</li>`).join("")}</ol></details>
      <p class="scene-note">乱序路径：连续保存两个版本 → 投递最新事件 → 投递旧版本事件。故障路径：索引不可用 → 保存新版本并投递 → 恢复索引 → 人工重放。</p>`;
    if (focused?.startsWith("events-"))
      container.querySelector(`#${focused}`)?.focus({ preventScroll: true });
  }
  function handleClick(event) {
    const action = event.target.closest("button[data-event-action]");
    if (!action || action.disabled) return;
    const type = action.dataset.eventAction.toUpperCase();
    const detailsOpen = container.querySelector("details")?.open;
    state = stepEvents(state, {
      type,
      title: container.querySelector("#events-title")?.value,
      query: container.querySelector("#events-query")?.value,
    });
    render();
    if (detailsOpen) container.querySelector("details").open = true;
    onChange();
  }
  container.addEventListener("click", handleClick);
  render();
  return {
    getSnapshot() {
      return structuredClone(state);
    },
    reset() {
      state = initialEventsState();
      render();
      onChange();
    },
    getGuide() {
      const current = !state.fact
        ? 0
        : !state.receipts.length
          ? 1
          : !state.searches
            ? 2
            : state.replayed ||
                state.duplicates ||
                state.staleIgnored ||
                !state.fact.visible
              ? 4
              : 3;
      return {
        title:
          current === 4 ? "事实、索引与失败记录都能解释" : "让笔记进入公开搜索",
        text: "先发布并投递，再查询结果。随后让索引不可用，观察停车记录；恢复连接后仍由你明确点击人工重放。",
        steps: [
          "发布笔记，观察事实与事件",
          "投递最新事件，查看索引版本",
          "查询并核对公开搜索结果",
          "体验重复、乱序、隐藏或人工恢复",
        ],
        current,
        label: [
          "定位发布笔记",
          "定位投递事件",
          "定位搜索结果",
          "定位异常体验",
          "继续查看事件过程",
        ][current],
        nextSelector: [
          "#events-publish",
          "#events-deliver",
          "#events-search",
          "#events-offline",
          "#events-search",
        ][current],
        design: "events",
        evidence:
          "https://github.com/tech-cove01/project-full-stack/blob/main/docs/reviews/2026-10-29/go073-399705a/events/report.md",
      };
    },
    destroy() {
      container.removeEventListener("click", handleClick);
    },
  };
}
