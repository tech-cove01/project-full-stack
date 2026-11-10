import test from "node:test";
import assert from "node:assert/strict";
import { initialLikes, likesStep } from "../showcase/demo/scenes/likes.js";
import { makeLikesTour } from "../showcase/demo/scenes/likes-tour.js";

test("likes prior pause without publication does not skip a new pause", () => {
  let state = likesStep(initialLikes(), { type: "WRITE", liked: true });
  const history = [];
  for (const action of [{ type: "PAUSE" }, { type: "PAUSE" }]) {
    const before = state;
    state = likesStep(state, action);
    history.push({ action: "pause", before, after: state });
  }
  assert.equal(
    makeLikesTour(state, history).steps[1].done(state, history),
    false,
  );
});
test("likes arbitrary already-published backlog offers explicit reset, canonical backlog resumes", () => {
  let before = likesStep(initialLikes(), { type: "WRITE", liked: true });
  let state = likesStep(before, { type: "PUBLISH" });
  assert(makeLikesTour(state, []).blocked);
  before = likesStep(before, { type: "PAUSE" });
  state = likesStep(before, { type: "PUBLISH" });
  const history = [{ action: "publish", before, after: state }];
  assert.equal(makeLikesTour(state, history).blocked, null);
});
const confirms = (step, r) => step.matches(r.action, r.before, r.after, r);
test("likes coach requires fact, backlog, resume, receipts and equal-work comparison", () => {
  let state = initialLikes();
  const history = [],
    plan = makeLikesTour(state);
  const actions = [
    ["like", { type: "WRITE", liked: true }],
    ["pause", { type: "PAUSE" }],
    ["publish", { type: "PUBLISH" }],
    ["pause", { type: "PAUSE" }],
    ["drain", { type: "DRAIN" }],
    ["compare", { type: "COMPARE" }],
  ];
  actions.forEach(([action, model], i) => {
    const step = plan.steps[i];
    assert.equal(step.done(state, history), false, step.id);
    const before = state;
    state = likesStep(state, model);
    const record = { action, before, after: state };
    history.push(record);
    assert.equal(confirms(step, record), true, step.id);
    assert.equal(step.done(state, history), true, step.id);
    if (step.id === "likes-resume")
      assert.equal(state.events[0].receipt, false);
  });
  assert.equal(state.comparison[0].receipts, 12);
  assert.equal(state.comparison[1].receipts, 12);
  assert.equal(state.comparison[0].publishRounds, 12);
  assert.equal(state.comparison[1].publishRounds, 3);
});
test("likes repeat and paused drain do not fake completion", () => {
  const plan = makeLikesTour(initialLikes());
  let state = likesStep(initialLikes(), { type: "WRITE", liked: true });
  let after = likesStep(state, { type: "WRITE", liked: true });
  assert.equal(
    confirms(plan.steps[0], { action: "like", before: state, after }),
    false,
  );
  state = likesStep(state, { type: "PAUSE" });
  after = likesStep(state, { type: "DRAIN" });
  assert.equal(
    confirms(plan.steps[4], { action: "drain", before: state, after }),
    false,
  );
  assert.equal(after.events[0].receipt, false);
});
test("likes completed real group resumes at comparison without clearing facts", () => {
  let state = likesStep(initialLikes(), { type: "GROUP" });
  state = likesStep(state, { type: "DRAIN" });
  const original = structuredClone(state),
    plan = makeLikesTour(state);
  assert.equal(plan.blocked, null);
  assert.equal(
    plan.steps.findIndex((step) => !step.done(state, [])),
    5,
  );
  assert.deepEqual(state, original);
});
