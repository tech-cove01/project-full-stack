const currentCache = (state, entry = state?.l1) =>
  Boolean(entry && entry.version === state.fact?.version);
const freshMain = (state) =>
  Boolean(
    state?.active?.key === "main" &&
    state.fact?.visible &&
    state.active.snapshot.version === state.fact.version &&
    state.active.epoch === state.epoch,
  );
const activeRequests = (state) =>
  (state?.requests || []).filter((item) => state.active?.ids.includes(item.id));
const readFinished = (state, viewer, source) =>
  Boolean(
    state.result?.status === "已完成" &&
    state.result.viewer === viewer &&
    state.result.source === source &&
    state.result.version === state.fact.version &&
    state.result.liked === state.likes[viewer],
  );
const coldRead = (action, before, after) =>
  Boolean(
    action === "READ" &&
    before.viewer === "A" &&
    !before.likes.A &&
    !before.active &&
    !before.l1 &&
    !before.redis &&
    !before.queue.length &&
    freshMain(after) &&
    after.active.ids.length === 1 &&
    after.sequence === before.sequence + 1 &&
    after.sourceReads === before.sourceReads + 1,
  );
const firstFinish = (action, before, after) =>
  Boolean(
    action === "FINISH" &&
    freshMain(before) &&
    before.active.ids.length === 1 &&
    activeRequests(before)[0]?.viewer === "A" &&
    !before.active.ids.some(
      (id) =>
        before.requests.find((item) => item.id === id)?.status === "已取消",
    ) &&
    !after.active &&
    currentCache(after) &&
    currentCache(after, after.redis) &&
    readFinished(after, "A", "共享回源") &&
    after.sourceReads === before.sourceReads,
  );
const warmRead = (viewer, action, before, after) =>
  Boolean(
    action === "READ" &&
    before.viewer === viewer &&
    before.fact.visible &&
    currentCache(before) &&
    !before.active &&
    after.sequence === before.sequence + 1 &&
    after.l1Hits === before.l1Hits + 1 &&
    after.sourceReads === before.sourceReads &&
    readFinished(after, viewer, "进程内缓存"),
  );

// There are two READs and two FINISHes. An ordered witness prevents one old
// click from completing several different lessons when the coach is reopened.
function progress(steps, snapshot, history = []) {
  const reset = history.findLastIndex((entry) => entry.action === "RESET");
  let next = 0;
  for (const entry of history.slice(reset + 1)) {
    if (
      next < steps.length &&
      entry.after?.sequence <= snapshot.sequence &&
      steps[next].matches(entry.action, entry.before, entry.after, entry)
    )
      next++;
  }
  return next;
}

export function makeFeedTour(snapshot = {}, history = []) {
  const steps = [
    {
      id: "feed-cold-read",
      target: "#feed-read",
      result: "#feed-source-count",
      purpose: "点击“读取这篇笔记”，从一篇咖啡探店笔记的冷读开始。",
      feedback:
        "缓存还没有正文，因此启动了一次回源。页面先等待，正文尚未拿到，不会把加载中当作读取成功。",
      invalid:
        "这一步需要访客A从空缓存开始。若已经走了其它分支，可退出继续自由体验，或明确重置本场景。",
      matches: coldRead,
    },
    {
      id: "feed-first-finish",
      target: "#feed-finish",
      result: "#feed-result",
      purpose: "点击“完成当前回源”，看看正文和缓存如何就绪。",
      feedback:
        "咖啡笔记已返回，两级缓存同时留下公共正文。访客A的点赞状态单独读取，没有塞进共享正文。",
      invalid: "需要先开始一次冷读，且当前笔记仍公开、回源版本仍有效。",
      matches: firstFinish,
    },
    {
      id: "feed-warm-read",
      target: "#feed-read",
      result: "#feed-result",
      purpose: "再点“读取这篇笔记”，比较这次的内容来源。",
      feedback:
        "本次来自进程内缓存，回源批次没有增加。同一份公共正文可以复用，减少重复查询。",
      invalid:
        "请在访客A下读取已有的当前版本缓存；若只剩Redis缓存，可再读一次。",
      matches: (action, before, after) =>
        !before.likes.A && warmRead("A", action, before, after),
    },
    {
      id: "feed-like-a",
      target: "#feed-like",
      result: "#feed-current-viewer",
      purpose: "点击“当前访客点赞”，让访客A喜欢这篇笔记。",
      feedback:
        "只有访客A变为已点赞。共享正文缓存没有增加个人字段，接下来换人验证状态不会串用。",
      invalid: "这一步需要访客A从未点赞变成已点赞，访客B保持未点赞。",
      matches: (action, before, after) =>
        Boolean(
          action === "LIKE" &&
          before.viewer === "A" &&
          !before.likes.A &&
          !before.likes.B &&
          after.likes.A &&
          !after.likes.B &&
          JSON.stringify(before.l1) === JSON.stringify(after.l1) &&
          JSON.stringify(before.redis) === JSON.stringify(after.redis),
        ),
    },
    {
      id: "feed-visitor-b",
      target: "#feed-visitor-b",
      result: "#feed-current-viewer",
      purpose: "点击“访客B”，换一个没有点过赞的人。",
      feedback:
        "现在是访客B，显示未点赞；访客A的点赞依然保留。两个人的状态各自独立。",
      invalid: "请从访客A切换到访客B；A已点赞，B未点赞。",
      matches: (action, before, after) =>
        Boolean(
          action === "VISITOR" &&
          before.viewer === "A" &&
          after.viewer === "B" &&
          before.likes.A &&
          !before.likes.B &&
          after.likes.A &&
          !after.likes.B,
        ),
    },
    {
      id: "feed-read-b",
      target: "#feed-read",
      result: "#feed-result",
      purpose: "点击“读取这篇笔记”，让访客B读同一份缓存正文。",
      feedback:
        "正文仍来自进程内缓存，访客B却显示未点赞。公共内容可共享，个人状态按当前访客读取。",
      invalid: "请保持访客B未点赞、访客A已点赞，并读取当前版本缓存。",
      matches: (action, before, after) =>
        before.likes.A &&
        !before.likes.B &&
        warmRead("B", action, before, after),
    },
    {
      id: "feed-clear-cache",
      target: "#feed-clear",
      result: "#feed-result",
      purpose: "点击“清空两级缓存”，准备模拟三个人同时遇到缓存缺失。",
      feedback:
        "两级公共缓存已清空，个人点赞仍保留。下一步的三次冷请求会争取复用同一次回源。",
      invalid: "请先完成正在进行的回源，再清空缓存。",
      matches: (action, before, after) =>
        Boolean(
          action === "CLEAR" &&
          !before.active &&
          (before.l1 || before.redis) &&
          !after.l1 &&
          !after.redis &&
          after.epoch === before.epoch + 1 &&
          after.sourceReads === before.sourceReads &&
          JSON.stringify(before.likes) === JSON.stringify(after.likes),
        ),
    },
    {
      id: "feed-batch-cold",
      target: "#feed-batch",
      result: "#feed-requests",
      purpose: "点击“发起同批冷请求”，一次加入三个等待者。",
      feedback:
        "三个等待者共享一批回源，另外两个加入正在加载的批次。相同缺失内容在单进程内合并，避免同时重复查询。",
      invalid: "请先清空两级缓存，并确保没有尚未完成的回源。",
      matches: (action, before, after) =>
        Boolean(
          action === "BATCH" &&
          !before.active &&
          !before.l1 &&
          !before.redis &&
          !before.queue.length &&
          freshMain(after) &&
          after.active.ids.length === 3 &&
          activeRequests(after).every((item) => item.status === "加载中") &&
          after.sequence === before.sequence + 3 &&
          after.sourceReads === before.sourceReads + 1 &&
          after.merged === before.merged + 2,
        ),
    },
    {
      id: "feed-cancel-first",
      target: "#feed-cancel",
      result: "#feed-requests",
      purpose: "点击“取消一个等待者”，模拟其中一个人不等了。",
      feedback:
        "这是预期保护：只有一个请求取消，共享回源仍在运行，另外两个人继续等待，不会被一起中断。",
      invalid: "先发起三个同批冷请求，保留它们的加载中状态，再取消一个等待者。",
      matches: (action, before, after) =>
        Boolean(
          action === "CANCEL_FIRST" &&
          freshMain(before) &&
          before.active.ids.length === 3 &&
          activeRequests(before).every((item) => item.status === "加载中") &&
          after.active?.batchKey === before.active.batchKey &&
          activeRequests(after).filter((item) => item.status === "已取消")
            .length === 1 &&
          activeRequests(after).filter((item) => item.status === "加载中")
            .length === 2 &&
          after.sourceReads === before.sourceReads,
        ),
    },
    {
      id: "feed-finish-shared",
      target: "#feed-finish",
      result: "#feed-requests",
      purpose: "点击“完成当前回源”，检查剩下两个人的结果。",
      feedback:
        "取消者仍是已取消，其余两人正常拿到正文：A已点赞，B未点赞。一次共享回源完成，取消和个人状态都没有串给别人。",
      invalid: "请保留一个已取消、两个加载中的同批请求，再完成当前有效回源。",
      matches: (action, before, after) => {
        if (
          action !== "FINISH" ||
          !freshMain(before) ||
          before.active.ids.length !== 3 ||
          activeRequests(before).filter((item) => item.status === "已取消")
            .length !== 1 ||
          after.active ||
          !currentCache(after) ||
          !currentCache(after, after.redis) ||
          after.sourceReads !== before.sourceReads
        )
          return false;
        return activeRequests(before).every((item) => {
          const result = after.requests.find((next) => next.id === item.id);
          return item.status === "已取消"
            ? result?.status === "已取消"
            : result?.status === "已完成" &&
                result.version === after.fact.version &&
                result.liked === after.likes[item.viewer];
        });
      },
    },
  ];
  for (const [index, step] of steps.entries()) {
    step.event = "click";
    step.done = (state, records) => progress(steps, state, records) > index;
  }
  const next = progress(steps, snapshot, history);
  const activeExpected = [1, 8, 9].includes(next);
  const waiting = activeRequests(snapshot);
  const batchChanged =
    (next === 1 && (waiting.length !== 1 || waiting[0].status !== "加载中")) ||
    (next === 8 &&
      (waiting.length !== 3 ||
        waiting.some((item) => item.status !== "加载中"))) ||
    (next === 9 &&
      (waiting.length !== 3 ||
        waiting.filter((item) => item.status === "已取消").length !== 1 ||
        waiting.filter((item) => item.status === "加载中").length !== 2));
  const incompatible = Boolean(
    snapshot.fact &&
    (!snapshot.fact.visible ||
      snapshot.queue.length ||
      snapshot.likes.B ||
      snapshot.likes.A !== next >= 4 ||
      snapshot.viewer !== (next >= 5 ? "B" : "A") ||
      Boolean(snapshot.active) !== activeExpected ||
      batchChanged ||
      (snapshot.active && !freshMain(snapshot)) ||
      (next === 0 && (snapshot.l1 || snapshot.redis || snapshot.sourceReads)) ||
      (next >= 2 && next <= 6 && !currentCache(snapshot)) ||
      (next === 7 && (snapshot.l1 || snapshot.redis))),
  );
  return {
    id: "feed-main",
    title: "同一篇笔记，缓存共享、个人状态独立",
    lesson:
      "你已经验证了三件事：热正文不重复回源，个人点赞不进入共享缓存，同批缺失只加载一次且单个取消不影响其他等待者。这里的合并只在单进程内生效。",
    blocked: incompatible
      ? {
          message:
            "当前已进入其它读取、点赞或版本分支，与这条逐步对比的进度不同。可以退出继续自由体验，或明确重置本场景再开始；不会自动清空已有操作。",
          actionLabel: "重置Feed后从冷读开始",
        }
      : null,
    steps,
  };
}
