import test from "node:test";
import assert from "node:assert/strict";
import {
  initialState,
  transition,
  availableStock,
  latestOrder,
  restoreState,
} from "../showcase/demo/model.js";

function perform(state, action) {
  const result = transition(state, action);
  assert.equal(result.changed, true, result.message);
  return result.state;
}
function unpaid(shop = "coffee") {
  let state = perform(initialState(), { type: "BUY", shop });
  return perform(state, { type: "CREATE", id: state.orders[0].id });
}
function paid() {
  let state = unpaid();
  return perform(state, { type: "PAY", id: state.orders[0].id });
}

test("acceptance reserves once and is not yet an unpaid order", () => {
  const state = perform(initialState(), { type: "BUY", shop: "coffee" });
  assert.equal(state.orders[0].status, "PROCESSING");
  assert.equal(state.orders[0].token, "");
  assert.equal(availableStock(state, "coffee"), 4);
  const duplicate = transition(state, { type: "BUY", shop: "coffee" });
  assert.equal(duplicate.changed, false);
  assert.equal(duplicate.state.orders.length, 1);
  assert.equal(
    transition(state, { type: "PAY", id: state.orders[0].id }).changed,
    false,
  );
});
test("customer payment and authorized staff redemption share the same order", () => {
  let state = paid(),
    id = state.orders[0].id;
  assert.equal(state.orders[0].status, "PAID");
  assert(state.orders[0].token.startsWith("DEMO-"));
  assert.equal(
    transition(state, { type: "REDEEM", id, token: "wrong" }).changed,
    false,
  );
  state = perform(state, { type: "REDEEM", id, token: state.orders[0].token });
  assert.equal(state.orders[0].status, "REDEEMED");
  assert.equal(availableStock(state, "coffee"), 4);
  const duplicate = transition(state, {
    type: "REDEEM",
    id,
    token: state.orders[0].token,
  });
  assert.equal(duplicate.changed, false);
  assert.equal(duplicate.state.audit.length, state.audit.length);
});
test("cancel restores once and allows another purchase", () => {
  let state = unpaid(),
    id = state.orders[0].id;
  state = perform(state, { type: "CANCEL", id });
  assert.equal(availableStock(state, "coffee"), 5);
  assert.equal(transition(state, { type: "CANCEL", id }).changed, false);
  state = perform(state, { type: "BUY", shop: "coffee" });
  assert.equal(state.orders.length, 2);
  assert.equal(availableStock(state, "coffee"), 4);
});
test("refund requires owner, restores stock, and keeps repurchase blocked", () => {
  let state = paid(),
    id = state.orders[0].id;
  assert.equal(transition(state, { type: "REFUND", id }).changed, false);
  state = perform(state, { type: "WORKSPACE", role: "OWNER", shop: "coffee" });
  state = perform(state, { type: "REFUND", id });
  assert.equal(availableStock(state, "coffee"), 5);
  assert.equal(transition(state, { type: "REFUND", id }).changed, false);
  assert.equal(
    transition(state, { type: "BUY", shop: "coffee" }).changed,
    false,
  );
});
test("redemption and refund exclude one another", () => {
  let state = paid(),
    id = state.orders[0].id;
  state = perform(state, { type: "REDEEM", id, token: state.orders[0].token });
  state = perform(state, { type: "WORKSPACE", role: "OWNER", shop: "coffee" });
  assert.equal(transition(state, { type: "REFUND", id }).changed, false);
});
test("wrong store cannot redeem even with the correct example token", () => {
  let state = paid();
  const order = state.orders[0];
  state = perform(state, { type: "WORKSPACE", role: "STAFF", shop: "food" });
  assert.equal(
    transition(state, { type: "REDEEM", id: order.id, token: order.token })
      .changed,
    false,
  );
});
test("publishing is independent from buying, like uses desired state", () => {
  let state = perform(initialState(), {
    type: "PUBLISH",
    shop: "coffee",
    title: "周末体验",
    content: "只是一段虚构体验",
  });
  const id = state.notes[0].id;
  assert.equal(state.orders.length, 0);
  state = perform(state, { type: "LIKE", id, liked: true });
  assert.equal(state.notes[0].likes, 1);
  assert.equal(
    transition(state, { type: "LIKE", id, liked: true }).changed,
    false,
  );
  state = perform(state, { type: "LIKE", id, liked: false });
  assert.equal(state.notes[0].likes, 0);
});
test("storage restores progress and rejects invalid snapshots", () => {
  const state = paid();
  assert.deepEqual(restoreState(JSON.stringify(state)), state);
  assert.equal(restoreState("bad-json"), null);
  const invalid = structuredClone(state);
  invalid.orders[0].status = "PAID<script>";
  assert.equal(restoreState(JSON.stringify(invalid)), null);
  const badToken = structuredClone(state);
  badToken.orders[0].token = "<img src=x onerror=alert(1)>";
  assert.equal(restoreState(JSON.stringify(badToken)), null);
  const badLike = structuredClone(state);
  badLike.notes[0].liked = true;
  badLike.notes[0].likes = 0;
  assert.equal(restoreState(JSON.stringify(badLike)), null);
  assert.equal(latestOrder(state, "coffee").status, "PAID");
});
