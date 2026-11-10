import test from "node:test";
import assert from "node:assert/strict";
import { initialFeedState, feedStep } from "../showcase/demo/scenes/feed.js";
const run = (state, ...actions) =>
  actions.reduce(
    (s, action) =>
      feedStep(s, typeof action === "string" ? { type: action } : action),
    state,
  );

test("cold read fills public levels; warm and Redis reuse avoid another source read", () => {
  let state = run(initialFeedState(), "READ", "FINISH");
  assert.equal(state.sourceReads, 1);
  assert.equal(state.result.source, "共享回源");
  assert.deepEqual(Object.keys(state.l1).sort(), [
    "content",
    "title",
    "version",
  ]);
  state = run(state, "READ", "EVICT_L1", "READ");
  assert.equal(state.sourceReads, 1);
  assert.equal(state.l1Hits, 1);
  assert.equal(state.redisHits, 1);
});
test("two visitors share public content but keep independent likes", () => {
  let state = run(initialFeedState(), "LIKE", "READ", "FINISH");
  assert.equal(state.result.liked, true);
  state = run(state, { type: "VISITOR", viewer: "B" }, "READ");
  assert.equal(state.result.liked, false);
  assert.equal(state.sourceReads, 1);
  assert.equal(state.likes.A, true);
});
test("same missing batch shares source; cancelled waiter leaves other callers alive", () => {
  let state = run(initialFeedState(), "BATCH");
  assert.equal(state.sourceReads, 1);
  assert.equal(state.merged, 2);
  state = run(state, "CANCEL_FIRST", "FINISH");
  assert.equal(state.requests[0].status, "已取消");
  assert.deepEqual(
    state.requests.slice(1).map((r) => r.status),
    ["已完成", "已完成"],
  );
  assert.equal(state.sourceReads, 1);
});
test("different batch queues behind the bounded loader", () => {
  let state = run(initialFeedState(), "BATCH", "QUEUE_OTHER");
  assert.equal(state.sourceReads, 1);
  assert.equal(state.queue.length, 1);
  state = run(state, "FINISH");
  assert.equal(state.sourceReads, 2);
  assert.equal(state.active.key, "other");
  state = run(state, "FINISH");
  assert.equal(state.active, null);
  assert.equal(state.maxActive, 1);
});
test("old in-flight version is neither returned nor cached", () => {
  let state = run(initialFeedState(), "BATCH", "VERSION", "FINISH");
  assert.equal(state.fact.version, 2);
  assert.equal(state.result.status, "旧版本被拒绝");
  assert.equal(state.l1, null);
  assert.equal(state.redis, null);
  state = run(state, "READ", "FINISH");
  assert.equal(state.result.version, 2);
  assert.equal(state.l1.version, 2);
});
test("current visibility filters existing cache and in-flight content", () => {
  let state = run(initialFeedState(), "READ", "FINISH", "HIDE", "READ");
  assert(state.redis);
  assert.equal(state.result.status, "已过滤");
  assert.equal(state.sourceReads, 1);
  state = run(initialFeedState(), "BATCH", "HIDE", "FINISH");
  assert.equal(state.result.status, "已过滤");
  assert.equal(state.l1, null);
});
