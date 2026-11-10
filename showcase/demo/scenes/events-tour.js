// Actions and postconditions are observed by the shared coach, never simulated here.
const latest = (s) => s.events.at(-1);
const parked = (s) => s.events.filter((e) => e.status === "PARKED");
const seen = (history, matches) =>
  (history || []).some((r) => matches(r.action, r.before, r.after, r));
const hasResult = (s) =>
  s.esUp &&
  s.search.results.some(
    (n) => n.version === s.fact?.version && n.title === s.fact.title,
  );
const titleEntered = (a, b, s, r) =>
  a === "events-title" &&
  r.event === "change" &&
  r.value.trim().length > 0 &&
  r.value.trim().length <= 60;
const published = (a, b, s) =>
  a === "publish" &&
  s.fact?.version === (b.fact?.version || 0) + 1 &&
  s.events.length === b.events.length + 1 &&
  latest(s).status === "PENDING";
const firstDelivery = (a, b, s) =>
  a === "deliver" &&
  b.esUp &&
  latest(b)?.status === "PENDING" &&
  latest(s)?.status === "DELIVERED" &&
  s.index?.version === s.fact?.version &&
  s.receipts.includes(latest(s).id);
const searched = (a, b, s) =>
  a === "search" && s.searches === b.searches + 1 && hasResult(s);
const failed = (a, b, s) =>
  a === "deliver" &&
  !b.esUp &&
  latest(b)?.status === "PENDING" &&
  latest(s)?.status === "PARKED" &&
  s.receipts.length === b.receipts.length &&
  s.index?.version === b.index?.version;
const replayed = (a, b, s) =>
  a === "replay" &&
  b.esUp &&
  parked(b).length > 0 &&
  parked(s).length === 0 &&
  s.replayed > b.replayed &&
  s.index?.version === s.fact?.version;

export function makeEventsTour(snapshot = {}) {
  const steps = [
    {
      id: "events-title",
      target: "#events-title",
      result: "#events-title",
      event: "change",
      example: "梧桐路咖啡周末",
      purpose:
        "先填一个示例笔记标题，再点引导里的“填好了”；也可填入示例。这一步不会发布。",
      feedback:
        "标题已经填入。事实、消息和搜索索引都还没有因为输入而改变，下一步由你确认发布。",
      invalid: "请填写1–60字的示例标题，再继续发布。",
      matches: titleEntered,
      done: (s, h) =>
        Boolean(s.fact) ||
        seen(h, (a, b, n, r) => titleEntered(a, b, n, r) && !b.fact),
    },
    {
      id: "events-publish",
      target: "#events-publish",
      result: "#events-fact-version",
      purpose: "点击“发布示例笔记”，观察笔记事实先保存在哪里。",
      feedback:
        "笔记事实和待投递事件已一起保存。搜索索引还没更新，发布成功和能搜到是两个阶段。",
      invalid:
        "尚未保存新版本。请检查标题不为空、长度不超过60字；已有较多事件时可明确重置本场景。",
      matches: published,
      done: (s) => Boolean(s.fact),
    },
    {
      id: "events-deliver",
      target: "#events-deliver",
      result: "#events-index-version",
      purpose: "点击“投递最新事件”，把已保存的笔记更新到搜索索引。",
      feedback:
        "搜索索引已经跟上当前事实版本，处理收据也已保存。现在再查公开搜索。",
      invalid:
        "当前没有完成正常索引更新。请确认已发布且索引可用；可退出处理现有分支，或明确重置后从头体验。",
      matches: firstDelivery,
      done: (s, h) =>
        Boolean(
          s.index &&
          s.index.version === s.fact?.version &&
          latest(s)?.status === "DELIVERED",
        ) ||
        (seen(h, searched) && seen(h, firstDelivery)),
    },
    {
      id: "events-query",
      target: "#events-query",
      result: "#events-query",
      event: "change",
      example: " ",
      purpose:
        "清空搜索词，再点“填好了”，查看全部公开笔记；也可填入空白示例，查询仍由你点击。",
      feedback:
        "筛选词已经清空，下一步查询全部公开笔记，避免自定义标题被原来的“咖啡”筛选挡住。",
      invalid: "本步先清空搜索词；查询后可在自由体验中再试自己的关键词。",
      matches: (a, b, s, r) =>
        a === "events-query" && r.event === "change" && !r.value.trim(),
      done: (s, h) =>
        !s.query.trim() ||
        seen(
          h,
          (a, b, n, r) =>
            a === "events-query" && r.event === "change" && !r.value.trim(),
        ),
    },
    {
      id: "events-search",
      target: "#events-search",
      result: "#events-search-results",
      purpose: "点击“查询搜索结果”，看刚才的公开笔记。",
      feedback:
        "查到了当前公开版本。搜索候选会再核对当前事实的版本和公开状态，才交给读者。",
      invalid:
        "尚未查到当前公开版本。请确认标题已投递、搜索词为空；也可以退出检查当前状态或明确重置。",
      matches: searched,
      done: (s, h) =>
        seen(h, (a, b, n) => searched(a, b, n) && n.replayed === 0),
    },
    {
      id: "events-offline",
      target: "#events-offline",
      result: "#events-message",
      purpose: "点击“让搜索索引暂不可用”，接下来试一次故障中的修改。",
      feedback:
        "搜索索引已暂停服务。业务事实仍可保存，接下来观察消息失败后会去哪里。",
      matches: (a, b, s) => a === "availability" && b.esUp && !s.esUp,
      done: (s, h) =>
        !s.esUp ||
        seen(h, (a, b, n) => a === "availability" && b.esUp && !n.esUp),
    },
    {
      id: "events-edit",
      target: "#events-title",
      result: "#events-title",
      event: "change",
      example: "梧桐路咖啡周末·新增露台",
      purpose:
        "修改标题，加一点新内容，再点“填好了”（也可填入示例）。现在还没保存新版本。",
      feedback:
        "新标题已经填好。索引仍不可用，下一步亲手保存，看看事实能否独立提交。",
      invalid: "请填写与当前标题不同的1–60字标题；这一步需要真实修改。",
      matches: (a, b, s, r) =>
        titleEntered(a, b, s, r) && !b.esUp && r.value.trim() !== b.fact?.title,
      done: (s, h) =>
        seen(
          h,
          (a, b, n, r) =>
            titleEntered(a, b, n, r) &&
            !b.esUp &&
            r.value.trim() !== b.fact?.title,
        ) || seen(h, (a, b, n) => published(a, b, n) && !b.esUp),
    },
    {
      id: "events-save",
      target: "#events-publish",
      result: "#events-fact-version",
      purpose: "点击“保存新版本”，索引故障期间也先保存笔记和事件。",
      feedback:
        "新事实和事件已保存，旧索引没有假装更新成功。下一步投递会留下可恢复的失败记录。",
      invalid:
        "请在索引不可用时保存有效的新标题；若当前状态属于另一条分支，可明确重置本场景。",
      matches: (a, b, s) => !b.esUp && published(a, b, s),
      done: (s, h) => seen(h, (a, b, n) => !b.esUp && published(a, b, n)),
    },
    {
      id: "events-fail",
      target: "#events-deliver",
      result: "#events-event-list",
      purpose: "点击“投递最新事件”，观察索引不可用时的处理结果。",
      feedback:
        "这是预期故障演示：事件被保存为停车记录，没有错误确认成功。失败事件仍可查看和人工恢复。",
      invalid:
        "尚未产生停车记录。需要先让索引不可用并保存新版本，再投递该事件；可退出核对或重置。",
      matches: failed,
      done: (s, h) => parked(s).length > 0 || seen(h, failed),
    },
    {
      id: "events-restore",
      target: "#events-offline",
      result: "#events-counts",
      purpose: "点击“恢复搜索索引”，留意停车记录有没有自动消失。",
      feedback:
        "连接恢复了，停车记录仍在。恢复服务不等于已经重放失败消息，下一步需要你明确执行。",
      invalid:
        "请先保留停车记录，再从不可用切回恢复；如果已恢复，可退出后重开按现状继续。",
      matches: (a, b, s) =>
        a === "availability" &&
        !b.esUp &&
        s.esUp &&
        parked(b).length > 0 &&
        parked(s).length === parked(b).length &&
        s.replayed === b.replayed,
      done: (s, h) =>
        seen(
          h,
          (a, b, n) =>
            a === "availability" && !b.esUp && n.esUp && parked(b).length > 0,
        ),
    },
    {
      id: "events-replay",
      target: "#events-replay",
      result: "#events-index-version",
      purpose: "点击“人工重放停车事件”，明确处理刚才保留的失败消息。",
      feedback:
        "人工重放已完成，停车事件有了处理收据，索引更新到新版本。下一步重新查询确认读者看到的内容。",
      invalid:
        "人工重放需要索引已恢复且有停车记录；请核对上一步，或退出重开按实际状态继续。",
      matches: replayed,
      done: (s, h) => seen(h, replayed),
    },
    {
      id: "events-search-recovered",
      target: "#events-search",
      result: "#events-search-results",
      purpose: "再次点击“查询搜索结果”，确认修改后的标题已经恢复可见。",
      feedback:
        "新标题与当前公开版本一致。故障期间没丢事实，也没把未处理的消息冒称成功，恢复由明确重放完成。",
      invalid:
        "尚未查到恢复后的当前版本。请确认已人工重放且搜索词为空，再查一次。",
      matches: (a, b, s) => s.replayed > 0 && searched(a, b, s),
      done: (s, h) => seen(h, (a, b, n) => n.replayed > 0 && searched(a, b, n)),
    },
  ];
  return {
    id: "events-main",
    title: "从发布笔记到搜索故障恢复",
    lesson:
      "事实与事件先可靠保存，搜索随后更新；故障留记录，恢复连接后仍需明确人工重放。",
    blocked:
      snapshot.fact?.visible === false || snapshot.events?.length >= 20
        ? {
            message:
              "当前是隐藏或事件已满的自由分支。可退出继续探索，或明确重置本场景后走发布与恢复教程；不会自动清空。",
          }
        : null,
    steps,
  };
}
