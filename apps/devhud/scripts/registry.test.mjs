import assert from "node:assert/strict";
import test from "node:test";
import { ActionId, PlatformCapability, actionRegistry, availableActions, completeOnboarding, desktopCapabilities, hasCompletedOnboarding } from "../src/shell.ts";

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

test("does not advertise native integrations the shell has not implemented", () => {
  const actions = availableActions(desktopCapabilities);
  assert(!desktopCapabilities.available.has(PlatformCapability.Capture));
  assert(!desktopCapabilities.available.has(PlatformCapability.LaunchAtLogin));
  assert(!actions.some(({ id }) => id.startsWith("realqa.capture.")));
  assert(!actions.some(({ id }) => id === ActionId.LaunchAtLogin));
});

test("keeps onboarding usable when persistent storage is unavailable", () => {
  const unavailableStorage = {
    getItem() { throw new Error("storage unavailable"); },
    setItem() { throw new Error("storage unavailable"); },
  };
  assert.equal(hasCompletedOnboarding(unavailableStorage), false);
  assert.doesNotThrow(() => completeOnboarding(unavailableStorage));
});
