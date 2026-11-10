import test from "node:test";
import assert from "node:assert/strict";
import { initialFeedState, feedStep } from "../showcase/demo/scenes/feed.js";
import { makeFeedTour } from "../showcase/demo/scenes/feed-tour.js";

const actions = [
  "READ",
  "FINISH",
  "READ",
  "LIKE",
  { type: "VISITOR", viewer: "B" },
  "READ",
  "CLEAR",
  "BATCH",
  "CANCEL_FIRST",
  "FINISH",
];
function session() {
  let state = initialFeedState();
  const history = [];
  return {
    history,
    get state() {
      return state;
    },
    act(value) {
      const action = typeof value === "string" ? { type: value } : value;
      const before = state;
      state =
        action.type === "RESET" ? initialFeedState() : feedStep(state, action);
      const entry = { action: action.type, before, after: state };
      history.push(entry);
      return entry;
    },
  };
}
const matches = (step, entry) =>
  step.matches(entry.action, entry.before, entry.after, entry);

test("ten real Feed actions explain cache reuse, isolated likes and waiter cancellation", () => {
  const run = session();
  const tour = makeFeedTour(run.state, run.history);
  assert.equal(tour.blocked, null);
  assert.equal(tour.steps.length, 10);
  for (const [index, action] of actions.entries()) {
    const step = tour.steps[index];
    assert.equal(step.done(run.state, run.history), false, step.id);
    assert.equal(matches(step, run.act(action)), true, step.id);
    assert.equal(step.done(run.state, run.history), true, step.id);
    const reopened = makeFeedTour(run.state, run.history);
    assert.equal(reopened.blocked, null, step.id);
    assert.equal(
      reopened.steps.findIndex((item) => !item.done(run.state, run.history)),
      index === actions.length - 1 ? -1 : index + 1,
      step.id,
    );
  }
  assert.equal(run.state.sourceReads, 2);
  assert.deepEqual(run.state.likes, { A: true, B: false });
  assert.deepEqual(
    run.state.requests.slice(-3).map((item) => item.status),
    ["已取消", "已完成", "已完成"],
  );
  assert.deepEqual(
    run.state.requests.slice(-2).map((item) => item.liked),
    [false, true],
  );
  assert.match(tour.steps[8].feedback, /这是预期保护/);
  assert.match(tour.lesson, /单进程/);
});

test("one cold READ or FINISH cannot stand in for a later warm or shared step", () => {
  const run = session();
  const tour = makeFeedTour();
  const cold = run.act("READ");
  assert.equal(matches(tour.steps[2], cold), false);
  assert.equal(matches(tour.steps[5], cold), false);
  const finish = run.act("FINISH");
  assert.equal(matches(tour.steps[9], finish), false);
  assert.equal(tour.steps[2].done(run.state, run.history), false);
  // Reading as B before A's like and visitor switch is not the personal-state
  // comparison, even when it is a valid warm read in free experience.
  run.act({ type: "VISITOR", viewer: "B" });
  const readB = run.act("READ");
  assert.equal(matches(tour.steps[5], readB), false);
  assert.equal(tour.steps[5].done(run.state, run.history), false);
});

test("actual free-experience history resumes, RESET discards reused request IDs", () => {
  const run = session();
  for (const action of actions) run.act(action);
  let reopened = makeFeedTour(run.state, run.history);
  assert.ok(reopened.steps.every((step) => step.done(run.state, run.history)));
  run.act("RESET");
  run.act("READ");
  reopened = makeFeedTour(run.state, run.history);
  assert.equal(reopened.blocked, null);
  assert.equal(reopened.steps[0].done(run.state, run.history), true);
  for (const step of reopened.steps.slice(1))
    assert.equal(step.done(run.state, run.history), false);
});

test("unknown existing branches require an explicit recovery choice and never reset", () => {
  for (const action of [
    "HIDE",
    "LIKE",
    "BATCH",
    { type: "VISITOR", viewer: "B" },
  ]) {
    const run = session();
    run.act(action);
    const before = structuredClone(run.state);
    assert.ok(makeFeedTour(run.state, run.history).blocked);
    assert.deepEqual(run.state, before);
  }
  const run = session();
  run.act("READ");
  run.act("VERSION");
  assert.ok(makeFeedTour(run.state, run.history).blocked);
});

test("failed cache clear, cancelled-only finish and unrelated queue cannot advance", () => {
  const run = session();
  const tour = makeFeedTour();
  assert.equal(matches(tour.steps[1], run.act("FINISH")), false);
  run.act("READ");
  assert.equal(matches(tour.steps[6], run.act("CLEAR")), false);
  assert.equal(matches(tour.steps[7], run.act("BATCH")), false);
  run.act("CANCEL_FIRST");
  assert.equal(matches(tour.steps[1], run.act("FINISH")), false);
  run.act("CLEAR");
  run.act("BATCH");
  run.act("QUEUE_OTHER");
  assert.ok(makeFeedTour(run.state, run.history).blocked);
});

test("reopening after extra free reads or cancellations offers recovery before a dead end", () => {
  const cold = session();
  cold.act("READ");
  cold.act("READ");
  assert.ok(makeFeedTour(cold.state, cold.history).blocked);
  const batch = session();
  for (const action of actions.slice(0, -1)) batch.act(action);
  assert.equal(makeFeedTour(batch.state, batch.history).blocked, null);
  batch.act("CANCEL_FIRST");
  assert.ok(makeFeedTour(batch.state, batch.history).blocked);
});
