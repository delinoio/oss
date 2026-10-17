import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import test from "node:test";
import { attachProbeCancellation } from "./native-cancellation.mjs";

test("probe worker cancels on owner disconnect and explicit interruption", () => {
  for (const [event, value] of [
    ["disconnect"],
    ["message", { type: "abort" }],
    ["SIGINT"],
    ["SIGTERM"],
  ]) {
    const owner = new EventEmitter();
    const controller = new AbortController();
    const detach = attachProbeCancellation(controller, owner);
    owner.emit(event, value);
    assert.equal(controller.signal.aborted, true);
    detach();
    assert.deepEqual(owner.eventNames(), []);
  }
});
