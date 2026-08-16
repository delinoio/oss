import assert from "node:assert/strict";
import test from "node:test";
import { ActionId, PlatformCapability, actionRegistry, availableActions } from "../src/shell.ts";

test("registers exactly the five contracted RealQA capture actions", () => {
  const capture = actionRegistry.filter(({ id }) => id.startsWith("realqa.capture.")).map(({ id }) => id).toSorted();
  assert.deepEqual(capture, [ActionId.CaptureActiveWindow, ActionId.CaptureAllDisplays, ActionId.CaptureDisplay, ActionId.CaptureSelection, ActionId.CaptureToolbar].toSorted());
  assert.equal(new Set(actionRegistry.map(({ id }) => id)).size, actionRegistry.length);
});

test("filters unavailable native actions from the command registry", () => {
  const actions = availableActions({ available: new Set() });
  assert(!actions.some(({ id }) => id.startsWith("realqa.capture.")));
  assert(actions.some(({ id }) => id === ActionId.Home));
  assert.equal(PlatformCapability.Capture, "capture");
});
