import assert from "node:assert/strict";
import test from "node:test";
import { ActionId, PlatformCapability, actionRegistry, availableActions, completeOnboarding, defaultPreferences, desktopCapabilities, getLocalStorage, hasCompletedOnboarding, writePreferences } from "../src/shell.ts";

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

test("keeps preference changes usable when persistent storage is unavailable", () => {
  const unavailableStorage = { setItem() { throw new Error("storage unavailable"); } };
  assert.doesNotThrow(() => writePreferences(unavailableStorage, defaultPreferences));
});

test("falls back to session storage when the localStorage getter throws", () => {
  const originalWindow = globalThis.window;
  Object.defineProperty(globalThis, "window", {
    configurable: true,
    value: { get localStorage() { throw new Error("storage unavailable"); } },
  });
  try {
    const storage = getLocalStorage();
    completeOnboarding(storage);
    assert.equal(hasCompletedOnboarding(storage), true);
    storage.setItem("remove-me", "value");
    assert.equal(storage.length >= 2, true);
    assert.equal(typeof storage.key(0), "string");
    storage.removeItem("remove-me");
    assert.equal(storage.getItem("remove-me"), null);
  } finally {
    Object.defineProperty(globalThis, "window", { configurable: true, value: originalWindow });
  }
});
