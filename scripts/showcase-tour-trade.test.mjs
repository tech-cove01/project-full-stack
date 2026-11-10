import test from "node:test";
import assert from "node:assert/strict";
import { initialTradeState, tradeStep } from "../showcase/demo/scenes/trade.js";
import {
  makeTradeTour,
  makeTradeRecoveryTour,
} from "../showcase/demo/scenes/trade-tour.js";

function session() {
  let state = initialTradeState();
  const history = [];
  return {
    get state() {
      return state;
    },
    history,
    act(action) {
      const before = state;
      state =
        action === "reset" ? initialTradeState() : tradeStep(state, action);
      const entry = {
        action,
        target: `#trade-${action}`,
        before,
        after: state,
      };
      history.push(entry);
      return entry;
    },
  };
}

const matches = (step, entry) =>
  step.matches(entry.action, entry.before, entry.after);
const finishMain = (run) => {
  for (const action of ["accept", "consume", "retry", "duplicate", "ack"])
    run.act(action);
};

test("trade main requires each real action and explains its changed result", () => {
  const run = session();
  const tour = makeTradeTour(run.state);
  assert.equal(tour.blocked, null);
  assert.deepEqual(
    tour.steps.map((step) => step.target),
    [
      "#trade-accept",
      "#trade-consume",
      "#trade-retry",
      "#trade-duplicate",
      "#trade-ack",
    ],
  );
  for (const [index, action] of [
    "accept",
    "consume",
    "retry",
    "duplicate",
    "ack",
  ].entries()) {
    const step = tour.steps[index];
    assert.equal(step.done(run.state, run.history), false, step.id);
    const entry = run.act(action);
    assert.equal(matches(step, entry), true, step.id);
    assert.equal(step.done(run.state, run.history), true, step.id);
    assert.ok(step.purpose.length && step.feedback.length && step.result);
  }
  assert.equal(run.state.stock, 4);
  assert.equal(run.state.orders.length, 1);
  assert.equal(tour.branch.id, "trade-recovery");
});

test("retry and duplicate need witnessed idempotence, not just an existing order", () => {
  const run = session();
  const tour = makeTradeTour();
  run.act("accept");
  assert.equal(matches(tour.steps[4], run.act("ack")), false);
  // A duplicate button can create the first order in the model; that is not
  // evidence of protecting an already existing order from duplicate creation.
  assert.equal(matches(tour.steps[3], run.act("duplicate")), false);
  assert.equal(tour.steps[3].done(run.state, run.history), false);
  assert.equal(tour.steps[2].done(run.state, run.history), false);
  assert.equal(matches(tour.steps[2], run.act("consume")), false);
  assert.equal(matches(tour.steps[2], run.act("retry")), true);
  assert.equal(tour.steps[2].done(run.state, run.history), true);
  assert.equal(tour.steps[3].done(run.state, run.history), false);
  assert.equal(matches(tour.steps[3], run.act("duplicate")), true);
  assert.match(tour.steps[2].feedback, /这是预期保护/);
  assert.match(tour.steps[3].feedback, /这是预期保护/);
});

test("main resumes actual free-experience progress and discards pre-reset evidence", () => {
  const run = session();
  finishMain(run);
  let tour = makeTradeTour(run.state);
  assert.ok(tour.steps.every((step) => step.done(run.state, run.history)));
  run.act("reset");
  run.act("accept");
  run.act("consume");
  tour = makeTradeTour(run.state);
  assert.equal(tour.steps[0].done(run.state, run.history), true);
  assert.equal(tour.steps[1].done(run.state, run.history), true);
  assert.equal(tour.steps[2].done(run.state, run.history), false);
  assert.equal(tour.steps[3].done(run.state, run.history), false);
  assert.equal(tour.steps[4].done(run.state, run.history), false);
});

test("cancel, compensate, rebuy and delayed old compensation preserve new ownership", () => {
  const run = session();
  finishMain(run);
  const tour = makeTradeRecoveryTour(run.state);
  const owner = run.state.reservation;
  assert.equal(tour.blocked, null);
  for (const [index, action] of [
    "cancel",
    "compensate",
    "accept",
    "old-compensation",
  ].entries()) {
    const step = tour.steps[index];
    assert.equal(step.done(run.state, run.history), false, step.id);
    assert.equal(matches(step, run.act(action)), true, step.id);
    assert.equal(step.done(run.state, run.history), true, step.id);
    // Closing and reopening a partially completed branch must preserve progress.
    const reopened = makeTradeRecoveryTour(run.state);
    assert.equal(reopened.blocked, null);
    for (let done = 0; done <= index; done++)
      assert.equal(
        reopened.steps[done].done(run.state, run.history),
        true,
        reopened.steps[done].id,
      );
  }
  assert.equal(run.state.stock, 4);
  assert.notEqual(run.state.reservation, owner);
  assert.equal(run.state.orders[0].status, "CANCELLED");
  assert.match(tour.steps[3].feedback, /这是预期保护/);
});

test("recovery refuses wrong ordering and needs the new reservation", () => {
  const run = session();
  finishMain(run);
  const tour = makeTradeRecoveryTour(run.state);
  assert.equal(matches(tour.steps[1], run.act("compensate")), false);
  run.act("cancel");
  assert.equal(matches(tour.steps[2], run.act("accept")), false);
  run.act("compensate");
  assert.equal(matches(tour.steps[3], run.act("old-compensation")), false);
  assert.equal(tour.steps[3].done(run.state, run.history), false);
  run.act("accept");
  assert.equal(tour.steps[3].done(run.state, run.history), false);
  assert.equal(matches(tour.steps[3], run.act("old-compensation")), true);
  run.act("reset");
  finishMain(run);
  run.act("cancel");
  run.act("compensate");
  run.act("accept");
  assert.equal(
    makeTradeRecoveryTour(run.state).steps[3].done(run.state, run.history),
    false,
  );
});

test("incompatible branches require an explicit recovery choice without changing state", () => {
  const run = session();
  assert.ok(makeTradeRecoveryTour().blocked);
  run.act("accept");
  run.act("unknown");
  const before = structuredClone(run.state);
  assert.ok(makeTradeTour(run.state).blocked);
  assert.ok(makeTradeRecoveryTour(run.state).blocked);
  assert.deepEqual(run.state, before);
  run.act("persist-review");
  run.act("reconcile");
  run.act("cancel");
  assert.ok(makeTradeTour(run.state).blocked);
  assert.equal(makeTradeRecoveryTour(run.state).blocked, null);
});
