import test from "node:test";
import assert from "node:assert/strict";
import {
  initialEventsState,
  stepEvents,
} from "../showcase/demo/scenes/events.js";

test("publication commits fact and event, index advances only through delivery", () => {
  let state = stepEvents(initialEventsState(), {
    type: "PUBLISH",
    title: "咖啡周末",
  });
  assert.equal(state.fact.version, 1);
  assert.equal(state.events.length, 1);
  assert.equal(state.index, null);
  assert.equal(state.search.results.length, 0);
  state = stepEvents(state, { type: "DELIVER" });
  assert.equal(state.index.version, 1);
  assert.deepEqual(state.receipts, ["E1"]);
  assert.equal(state.search.results[0].title, "咖啡周末");
});
test("receipt makes duplicate delivery idempotent", () => {
  let state = stepEvents(initialEventsState(), {
    type: "PUBLISH",
    title: "咖啡周末",
  });
  state = stepEvents(state, { type: "DELIVER" });
  state = stepEvents(state, { type: "DUPLICATE" });
  assert.equal(state.indexWrites, 1);
  assert.equal(state.receipts.length, 1);
  assert.equal(state.duplicates, 1);
});
test("old unseen event cannot overwrite a newer projection", () => {
  let state = stepEvents(initialEventsState(), {
    type: "PUBLISH",
    title: "咖啡旧标题",
  });
  state = stepEvents(state, { type: "PUBLISH", title: "咖啡新标题" });
  state = stepEvents(state, { type: "DELIVER" });
  state = stepEvents(state, { type: "OLD" });
  assert.equal(state.index.version, 2);
  assert.equal(state.index.title, "咖啡新标题");
  assert.equal(state.indexWrites, 1);
  assert.equal(state.staleIgnored, 1);
  assert.equal(state.receipts.length, 2);
});
test("failed delivery persists, availability recovery does not replay", () => {
  let state = stepEvents(initialEventsState(), {
    type: "PUBLISH",
    title: "咖啡恢复",
  });
  state = stepEvents(state, { type: "AVAILABILITY" });
  state = stepEvents(state, { type: "DELIVER" });
  assert.equal(state.events[0].status, "PARKED");
  assert.equal(state.receipts.length, 0);
  assert.equal(state.index, null);
  state = stepEvents(state, { type: "REPLAY" });
  assert.equal(state.events[0].status, "PARKED");
  state = stepEvents(state, { type: "AVAILABILITY" });
  assert.equal(state.events[0].status, "PARKED");
  assert.equal(state.index, null);
  state = stepEvents(state, { type: "REPLAY" });
  assert.equal(state.events[0].status, "DELIVERED");
  assert.equal(state.index.version, 1);
  assert.equal(state.replayed, 1);
});
test("ordinary delivery cannot bypass manual replay for a parked event", () => {
  let state = stepEvents(initialEventsState(), {
    type: "PUBLISH",
    title: "咖啡停车重放",
  });
  state = stepEvents(state, { type: "AVAILABILITY" });
  state = stepEvents(state, { type: "DELIVER" });
  const attempts = state.events[0].attempts;
  state = stepEvents(state, { type: "AVAILABILITY" });
  state = stepEvents(state, { type: "DELIVER" });
  assert.equal(state.events[0].status, "PARKED");
  assert.equal(state.events[0].attempts, attempts);
  assert.equal(state.receipts.length, 0);
  assert.equal(state.index, null);
  assert.equal(state.replayed, 0);
  state = stepEvents(state, { type: "REPLAY" });
  assert.equal(state.events[0].status, "DELIVERED");
  assert.equal(state.index.version, 1);
  assert.equal(state.replayed, 1);
});
test("unavailable search does not return an existing indexed result", () => {
  let state = stepEvents(initialEventsState(), {
    type: "PUBLISH",
    title: "咖啡搜索故障",
  });
  state = stepEvents(state, { type: "DELIVER" });
  assert.equal(state.search.results.length, 1);
  state = stepEvents(state, { type: "AVAILABILITY" });
  state = stepEvents(state, { type: "SEARCH", query: "咖啡" });
  assert.equal(state.search.results.length, 0);
  assert.equal(state.search.candidates, 0);
  assert.match(state.search.reason, /暂不可用/);
  assert.equal(state.index.version, 1);
  state = stepEvents(state, { type: "AVAILABILITY" });
  state = stepEvents(state, { type: "SEARCH", query: "咖啡" });
  assert.equal(state.search.results.length, 1);
});
test("current hidden fact filters a stale visible index hit", () => {
  let state = stepEvents(initialEventsState(), {
    type: "PUBLISH",
    title: "咖啡公开笔记",
  });
  state = stepEvents(state, { type: "DELIVER" });
  state = stepEvents(state, { type: "HIDE" });
  assert.equal(state.index.visible, true);
  assert.equal(state.fact.visible, false);
  assert.equal(state.search.candidates, 1);
  assert.equal(state.search.filtered, 1);
  assert.equal(state.search.results.length, 0);
});
test("edited fact filters old version until index is updated; query uses actual title", () => {
  let state = stepEvents(initialEventsState(), {
    type: "PUBLISH",
    title: "咖啡旧版",
  });
  state = stepEvents(state, { type: "DELIVER" });
  state = stepEvents(state, { type: "PUBLISH", title: "咖啡新版" });
  assert.equal(state.search.filtered, 1);
  state = stepEvents(state, { type: "DELIVER" });
  state = stepEvents(state, { type: "SEARCH", query: "新版" });
  assert.equal(state.search.results.length, 1);
  state = stepEvents(state, { type: "SEARCH", query: "不存在" });
  assert.equal(state.search.results.length, 0);
});
