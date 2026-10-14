// SPDX-License-Identifier: Apache-2.0
import { act, cleanup, render, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import {
  BrowserChoiceState,
  BrowserPreferenceProblem,
  BrowserPreferenceProvider,
  BrowserSaveResult,
  useBrowserPreferences,
  type BrowserPreferenceBridge,
  type BrowserPreferenceSnapshot,
} from "./browser-preference-controller";

const systemDefault = { state: BrowserChoiceState.SystemDefault } as const;
const chrome = {
  state: BrowserChoiceState.Installed,
  identity: "com.google.Chrome",
} as const;
const verified: BrowserPreferenceSnapshot = {
  revision: 5,
  choices: { general: systemDefault, local: systemDefault },
  problem: null,
};

function fixture(
  update: BrowserPreferenceBridge["update"],
  initial = verified,
) {
  const bridge: BrowserPreferenceBridge = {
    native: true,
    read: vi.fn(async () => initial),
    update: vi.fn(update),
    reset: vi.fn(async () => initial),
    inventory: vi.fn(async () => ({ browsers: [] })),
    subscribe: vi.fn(async () => () => {}),
  };
  let controller!: ReturnType<typeof useBrowserPreferences>;
  function Probe() {
    controller = useBrowserPreferences();
    return null;
  }
  render(
    <BrowserPreferenceProvider bridge={bridge}>
      <Probe />
    </BrowserPreferenceProvider>,
  );
  return { bridge, controller: () => controller };
}

afterEach(cleanup);

it.each([
  ["browser-discovery-failed", BrowserSaveResult.DiscoveryFailed],
  ["invalid-input", BrowserSaveResult.InvalidSelection],
  ["browser-unavailable", BrowserSaveResult.NotInstalled],
])("keeps verified preferences after pre-write %s", async (code, expected) => {
  const { bridge, controller } = fixture(async () => {
    throw code;
  });
  await waitFor(() => expect(controller().snapshot).toEqual(verified));
  let result: BrowserSaveResult | undefined;
  await act(async () => {
    result = await controller().save(
      { general: chrome, local: systemDefault },
      5,
    );
  });
  expect(result).toBe(expected);
  expect(controller().snapshot).toEqual(verified);
  expect(controller().operation).toBeUndefined();
  expect(bridge.update).toHaveBeenCalledTimes(1);
});

it.each([
  [
    "transport rejection",
    async () => {
      throw new Error("ipc down");
    },
  ],
  ["malformed successful response", async () => ({ revision: 6 })],
])("requires reload after %s", async (_case, update) => {
  const { bridge, controller } = fixture(update);
  await waitFor(() => expect(controller().snapshot).toEqual(verified));
  let result: BrowserSaveResult | undefined;
  await act(async () => {
    result = await controller().save(
      { general: chrome, local: systemDefault },
      5,
    );
  });
  expect(result).toBe(BrowserSaveResult.Failed);
  expect(controller().snapshot).toEqual({
    ...verified,
    problem: BrowserPreferenceProblem.OutcomeUnknown,
  });
  await act(async () => {
    expect(
      await controller().save({ general: chrome, local: systemDefault }, 5),
    ).toBe(BrowserSaveResult.Failed);
  });
  expect(bridge.update).toHaveBeenCalledTimes(1);
  act(() => controller().reload());
  await waitFor(() => expect(controller().snapshot).toEqual(verified));
  expect(bridge.read).toHaveBeenCalledTimes(2);
});

it("shows a new choice only after an accepted native snapshot", async () => {
  const accepted: BrowserPreferenceSnapshot = {
    revision: 6,
    choices: { general: chrome, local: systemDefault },
    problem: null,
  };
  const { controller } = fixture(async () => accepted);
  await waitFor(() => expect(controller().snapshot).toEqual(verified));
  let result: BrowserSaveResult | undefined;
  await act(async () => {
    result = await controller().save(accepted.choices, 5);
  });
  expect(result).toBe(BrowserSaveResult.Saved);
  expect(controller().snapshot).toEqual(accepted);
});

it("retains changed-revision protection", async () => {
  const changed: BrowserPreferenceSnapshot = {
    ...verified,
    problem: BrowserPreferenceProblem.Changed,
  };
  const { bridge, controller } = fixture(async () => changed);
  await waitFor(() => expect(controller().snapshot).toEqual(verified));
  await act(async () => {
    expect(
      await controller().save({ general: chrome, local: systemDefault }, 5),
    ).toBe(BrowserSaveResult.Failed);
  });
  expect(controller().snapshot).toEqual(changed);
  await act(async () => {
    expect(
      await controller().save({ general: chrome, local: systemDefault }, 5),
    ).toBe(BrowserSaveResult.Failed);
  });
  expect(bridge.update).toHaveBeenCalledTimes(1);
});

it.each([
  BrowserPreferenceProblem.InvalidDocument,
  BrowserPreferenceProblem.UnsupportedVersion,
])("blocks save for %s storage", async (problem) => {
  const initial: BrowserPreferenceSnapshot = { ...verified, problem };
  const { bridge, controller } = fixture(async () => verified, initial);
  await waitFor(() => expect(controller().snapshot).toEqual(initial));
  await act(async () => {
    expect(
      await controller().save({ general: chrome, local: systemDefault }, 5),
    ).toBe(BrowserSaveResult.Failed);
  });
  expect(bridge.update).not.toHaveBeenCalled();
});
