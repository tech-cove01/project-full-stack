import test from "node:test";
import assert from "node:assert/strict";
import {
  initialEventsState,
  stepEvents,
} from "../showcase/demo/scenes/events.js";
import { makeEventsTour } from "../showcase/demo/scenes/events-tour.js";

test("events old delivery does not skip an undelivered current version", () => {
  const run = session();
  run.act("publish", { type: "PUBLISH", title: "咖啡旧版" });
  run.act("deliver", { type: "DELIVER" });
  run.act("publish", { type: "PUBLISH", title: "咖啡新版" });
  const plan = makeEventsTour(run.state);
  assert.equal(plan.steps[2].done(run.state, run.history), false);
  assert.equal(
    confirms(plan.steps[2], run.act("deliver", { type: "DELIVER" })),
    true,
  );
});

function session() {
  let state = initialEventsState();
  const history = [];
  return {
    get state() {
      return state;
    },
    history,
    act(action, model, extra = {}) {
      const before = state;
      state = model ? stepEvents(state, model) : state;
      const r = {
        action,
        before,
        after: state,
        event: extra.event || "click",
        ...extra,
      };
      history.push(r);
      return r;
    },
  };
}
const confirms = (step, r) => step.matches(r.action, r.before, r.after, r);
test("events lesson follows actual publication, failed delivery and explicit replay", () => {
  const run = session(),
    plan = makeEventsTour(run.state);
  const records = [
    () => run.act("events-title", null, { event: "change", value: "我的周末" }),
    () => run.act("publish", { type: "PUBLISH", title: "我的周末" }),
    () => run.act("deliver", { type: "DELIVER" }),
    () => run.act("events-query", null, { event: "change", value: "" }),
    () => run.act("search", { type: "SEARCH", query: "" }),
    () => run.act("availability", { type: "AVAILABILITY" }),
    () =>
      run.act("events-title", null, {
        event: "change",
        value: "我的周末·新版本",
      }),
    () => run.act("publish", { type: "PUBLISH", title: "我的周末·新版本" }),
    () => run.act("deliver", { type: "DELIVER" }),
    () => run.act("availability", { type: "AVAILABILITY" }),
    () => run.act("replay", { type: "REPLAY" }),
    () => run.act("search", { type: "SEARCH", query: "" }),
  ];
  plan.steps.forEach((step, index) => {
    assert.equal(
      step.done(run.state, run.history),
      false,
      `${step.id} not done before action`,
    );
    const record = records[index]();
    assert.equal(confirms(step, record), true, step.id);
    assert.equal(step.done(run.state, run.history), true, `${step.id} resumes`);
    if (step.id === "events-restore") {
      assert.equal(run.state.events.at(-1).status, "PARKED");
      assert.equal(run.state.replayed, 0);
    }
  });
  assert.equal(run.state.search.results[0].title, "我的周末·新版本");
  assert.equal(run.state.replayed, 1);
});
test("events recovery cannot be completed by availability or invalid publication", () => {
  const run = session(),
    plan = makeEventsTour(run.state);
  const invalid = run.act("publish", { type: "PUBLISH", title: "" });
  assert.equal(confirms(plan.steps[1], invalid), false);
  run.act("publish", { type: "PUBLISH", title: "咖啡" });
  run.act("availability", { type: "AVAILABILITY" });
  run.act("deliver", { type: "DELIVER" });
  const restore = run.act("availability", { type: "AVAILABILITY" });
  assert.equal(confirms(plan.steps[10], restore), false);
  assert.equal(plan.steps[10].done(run.state, run.history), false);
  assert.equal(run.state.events[0].status, "PARKED");
  const parkedDelivery = run.act("deliver", { type: "DELIVER" });
  assert.equal(confirms(plan.steps[10], parkedDelivery), false);
});
test("events opening preserves state and offers reset for incompatible free branches", () => {
  let state = stepEvents(initialEventsState(), {
    type: "PUBLISH",
    title: "咖啡",
  });
  state = stepEvents(state, { type: "HIDE" });
  const original = structuredClone(state);
  assert(makeEventsTour(state).blocked);
  assert.deepEqual(state, original);
});
