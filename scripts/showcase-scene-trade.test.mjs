import test from "node:test";
import assert from "node:assert/strict";
import { initialTradeState, tradeStep } from "../showcase/demo/scenes/trade.js";

const apply = (...actions) =>
  actions.reduce(
    (state, action) => tradeStep(state, action),
    initialTradeState(),
  );

test("same request retries preserve acceptance, stock and unique order", () => {
  const accepted = apply("accept");
  const id = accepted.requests[0].id;
  const state = ["retry", "consume", "duplicate", "retry", "ack", "ack"].reduce(
    (s, action) => tradeStep(s, action),
    accepted,
  );
  assert.equal(state.stock, 4);
  assert.equal(state.requests.length, 1);
  assert.equal(state.requests[0].id, id);
  assert.equal(state.orders.length, 1);
  assert.equal(state.orders[0].request, id);
  assert.equal(state.requests[0].acknowledged, true);
});

test("unresolved result cannot ACK until review is durably recorded", () => {
  let state = apply("accept", "unknown", "ack");
  assert.equal(state.orders.length, 0);
  assert.equal(state.requests[0].acknowledged, false);
  assert.match(state.result, /确认被阻止/);
  state = tradeStep(state, "persist-review");
  assert.equal(state.requests[0].review, "OPEN");
  state = tradeStep(state, "ack");
  assert.equal(state.requests[0].acknowledged, true);
  assert.equal(state.orders.length, 0);
  state = tradeStep(state, "reconcile");
  assert.equal(state.orders.length, 1);
  assert.equal(state.requests[0].review, "RESOLVED");
  assert.equal(state.stock, 4);
});

test("cancel compensation restores once and a delayed old compensation cannot release the new reservation", () => {
  let state = apply("accept", "consume", "ack", "cancel");
  assert.equal(state.stock, 4);
  assert.equal(state.orders[0].status, "CANCELLED");
  state = tradeStep(state, "compensate");
  assert.equal(state.stock, 5);
  assert.equal(state.reservation, null);
  state = tradeStep(state, "old-compensation");
  assert.equal(state.stock, 5);
  state = tradeStep(state, "accept");
  const newOwner = state.reservation;
  assert.equal(state.stock, 4);
  state = tradeStep(state, "old-compensation");
  assert.equal(state.stock, 4);
  assert.equal(state.reservation, newOwner);
  assert.match(state.result, /归属不匹配/);
  state = tradeStep(state, "consume");
  assert.equal(state.orders.length, 2);
  assert.equal(
    state.orders.filter((order) => order.status !== "CANCELLED").length,
    1,
  );
});

test("invalid ordering cannot invent stock, ACK, orders or review resolution", () => {
  const state = apply(
    "ack",
    "compensate",
    "reconcile",
    "old-compensation",
    "consume",
  );
  assert.equal(state.stock, 5);
  assert.equal(state.orders.length, 0);
  assert.equal(state.requests.length, 0);
});
