const pending = (s) => s.events.filter((e) => !e.published).length;
const lag = (s) => s.events.filter((e) => e.published && !e.receipt).length;
const receipts = (s) => s.events.filter((e) => e.receipt).length;
const allProcessed = (s) =>
  s.version > 0 &&
  pending(s) === 0 &&
  lag(s) === 0 &&
  receipts(s) === s.version;
const seen = (history, match) =>
  (history || []).some((r) => match(r.action, r.before, r.after));
const liked = (a, b, s) =>
  a === "like" &&
  !b.liked &&
  s.liked &&
  s.version === b.version + 1 &&
  s.events.length === b.events.length + 1 &&
  !s.events.at(-1).published;
const paused = (a, b, s) => a === "pause" && !b.paused && s.paused;
const published = (a, b, s) =>
  a === "publish" &&
  b.paused &&
  s.paused &&
  lag(s) > lag(b) &&
  pending(s) < pending(b) &&
  receipts(s) === receipts(b);
const resumed = (a, b, s) =>
  a === "pause" &&
  b.paused &&
  !s.paused &&
  lag(b) > 0 &&
  lag(s) === lag(b) &&
  receipts(s) === receipts(b);
const drained = (a, b, s) =>
  a === "drain" &&
  !b.paused &&
  b.events.length > 0 &&
  receipts(b) < b.version &&
  pending(s) === 0 &&
  lag(s) === 0 &&
  receipts(s) === s.version &&
  s.version === b.version &&
  s.liked === b.liked;
const compared = (a, b, s) =>
  a === "compare" &&
  s.comparison?.length === 2 &&
  s.comparison.every((r) => r.writes === 12 && r.receipts === 12) &&
  s.comparison[0].count === s.comparison[1].count &&
  s.comparison[0].publishRounds > s.comparison[1].publishRounds &&
  s.comparison[0].consumeRounds > s.comparison[1].consumeRounds;

export function makeLikesTour(snapshot = {}, history = []) {
  return {
    id: "likes-main",
    title: "点赞成功后，下游也要跟得上",
    lesson:
      "写入成功和下游稳态要一起核对；相同操作可用有界批量减少处理轮数。浏览器轮数不是QPS，真实成绩另看实测。",
    blocked:
      snapshot.events?.length >= 64 ||
      (snapshot.events?.length > 0 &&
        pending(snapshot) === 0 &&
        lag(snapshot) > 0 &&
        !seen(history, published))
        ? {
            message:
              "当前处于自由操作中的已发布积压（或达到本组上限），与教程的暂停后发布顺序不同。可退出继续处理，或明确重置本场景后走完整教程；不会自动清空。",
          }
        : null,
    steps: [
      {
        id: "likes-write",
        target: "#likes-write",
        result: "#likes-fact",
        purpose: "点击“点赞”，先看业务事实与待发布消息有什么变化。",
        feedback:
          "点赞事实已经变为1，业务版本增加，同时有一条待发布Outbox。接口写入成功，但下游还没完成。",
        invalid:
          "这次没有新增状态变更。相同期望状态不会重复计数，这是预期保护；可重置后从未点赞开始体验。",
        matches: liked,
        done: (s, h) => s.version > 0 || seen(h, liked),
      },
      {
        id: "likes-pause",
        target: "#likes-pause",
        result: "#likes-result",
        purpose: "点击“暂停消费”，模拟下游暂时跟不上。业务事实仍保留。",
        feedback:
          "消费者已暂停，点赞事实没有回滚。下一步继续发布事件，观察等待收据的积压。",
        matches: paused,
        done: (s, h) => allProcessed(s) || s.paused || seen(h, published),
      },
      {
        id: "likes-publish",
        target: "#likes-publish",
        result: "#likes-lag",
        purpose: "点击“发布一批事件”，看看暂停的消费者有没有留下收据。",
        feedback:
          "事件已发布，但消费暂停，待收据数量增加。点赞成功不能代替整条链路已经稳定。",
        invalid:
          "需要有待发布事件且消费暂停，才能看到这次积压。当前数据可退出核对，或明确重置后重走。",
        matches: published,
        done: (s, h) => allProcessed(s) || seen(h, published),
      },
      {
        id: "likes-resume",
        target: "#likes-pause",
        result: "#likes-result",
        purpose: "点击“恢复消费”，留意积压是否会在这一下就消失。",
        feedback:
          "消费已恢复，积压仍等待处理。这一步只恢复开关，下一步由你明确处理本组消息。",
        invalid:
          "先暂停并发布形成积压，再恢复。若已在自由体验恢复过，可退出重开按现状继续，或重置本场景。",
        matches: resumed,
        done: (s, h) => allProcessed(s) || seen(h, resumed),
      },
      {
        id: "likes-drain",
        target: "#likes-drain",
        result: "#likes-result",
        purpose: "点击“处理本组积压”，把消息处理完，再核对唯一收据。",
        feedback:
          "待发布与待收据都归零，成功变更数和唯一收据数一致，当前点赞事实仍符合最后一次期望。",
        invalid:
          "尚未完成收据核对。请先恢复消费；若当前没有待处理消息，可退出重开或明确重置。",
        matches: drained,
        done: (s, h) => allProcessed(s) || seen(h, drained),
      },
      {
        id: "likes-compare",
        target: "#likes-compare",
        result: "#likes-comparison",
        purpose:
          "点击“对比同一组逐条与批量处理”，在相同12次操作下比较处理轮数。",
        feedback:
          "两种方式都保留12次变更和12条收据，最终点赞数一致；有界批量用更少轮次完成。这是机制对照，真实QPS请看原始实测报告。",
        matches: compared,
        done: (s, h) => Boolean(s.comparison) && seen(h, compared),
      },
    ],
  };
}
