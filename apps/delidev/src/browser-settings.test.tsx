// SPDX-License-Identifier: Apache-2.0
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { BrowserSettings } from "./browser-settings";
import {
  BrowserChoiceState,
  BrowserPreferenceProblem,
  BrowserPreferenceProvider,
  parseBrowserInventory,
  parseBrowserSnapshot,
  type BrowserChoices,
  type BrowserPreferenceBridge,
  type BrowserPreferenceSnapshot,
} from "./browser-preference-controller";
import { SettingsActionScope } from "./settings-action";
import { i18n, SupportedLanguage } from "./localization";

const systemDefault = { state: BrowserChoiceState.SystemDefault } as const;
const installed = (identity: string) =>
  ({ state: BrowserChoiceState.Installed, identity }) as const;
const fixtureBrowsers = [
  { identity: "org.mozilla.firefox.desktop", name: "Firefox" },
  { identity: "com.google.Chrome", name: "Google Chrome" },
];

function fixture(
  initial: Partial<BrowserPreferenceSnapshot> = {},
  inventory: unknown = { browsers: fixtureBrowsers },
) {
  let snapshot: BrowserPreferenceSnapshot = {
    revision: 1,
    choices: { general: systemDefault, local: systemDefault },
    problem: null,
    ...initial,
  };
  const listeners = new Set<(raw: unknown) => void>();
  const publish = (next: BrowserPreferenceSnapshot) => {
    snapshot = next;
    for (const listener of listeners) listener(next);
  };
  const bridge: BrowserPreferenceBridge = {
    native: true,
    read: vi.fn(async () => snapshot),
    update: vi.fn(async (choices: BrowserChoices, revision: number) => {
      if (revision !== snapshot.revision)
        return { ...snapshot, problem: BrowserPreferenceProblem.Changed };
      const next = { revision: revision + 1, choices, problem: null };
      publish(next);
      return next;
    }),
    reset: vi.fn(async (revision: number) => {
      const next = {
        revision: revision + 1,
        choices: { general: systemDefault, local: systemDefault },
        problem: null,
      };
      publish(next);
      return next;
    }),
    inventory: vi.fn(async () => {
      if (inventory instanceof Error) throw inventory;
      return inventory;
    }),
    subscribe: vi.fn(async (listener) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    }),
  };
  return { bridge, publish, snapshot: () => snapshot };
}
function view(bridge: BrowserPreferenceBridge, active = true) {
  return (
    <SettingsActionScope>
      <BrowserPreferenceProvider bridge={bridge}>
        <BrowserSettings active={active} />
      </BrowserPreferenceProvider>
    </SettingsActionScope>
  );
}
const general = () =>
  screen.getByLabelText("General URL destination") as HTMLSelectElement;
const local = () =>
  screen.getByLabelText("Local URL destination") as HTMLSelectElement;

beforeEach(async () => {
  await i18n.changeLanguage(SupportedLanguage.English);
});
afterEach(cleanup);

it("lists system default plus discovered browsers for both independent destinations", async () => {
  const { bridge } = fixture();
  render(view(bridge));
  await waitFor(() =>
    expect(within(general()).getAllByRole("option")).toHaveLength(3),
  );
  for (const select of [general(), local()]) {
    expect(
      within(select)
        .getAllByRole("option")
        .map((option) => option.textContent),
    ).toEqual(["System default browser", "Firefox", "Google Chrome"]);
    expect(select.value).toBe("default");
    expect(select.disabled).toBe(false);
  }
  expect(
    screen.getByText(
      "Sign-in and account authorization links always open in the system default browser.",
    ),
  ).toBeTruthy();
});

it("persists the native identity, shows saving and reports success only after persistence", async () => {
  const { bridge, snapshot } = fixture();
  let finish!: () => void;
  const original = bridge.update;
  bridge.update = vi.fn(
    (choices, revision) =>
      new Promise((resolve) => {
        finish = () => resolve(original(choices, revision));
      }),
  );
  render(view(bridge));
  await waitFor(() =>
    expect(within(local()).getAllByRole("option")).toHaveLength(3),
  );
  fireEvent.change(local(), {
    target: { value: "installed:com.google.Chrome" },
  });
  await waitFor(() =>
    expect(screen.getByText("Saving browser setting…")).toBeTruthy(),
  );
  // The pending choice is not shown as saved and the controls stay locked.
  expect(screen.queryByText("Browser setting saved.")).toBeNull();
  expect(local().value).toBe("default");
  expect(local().disabled).toBe(true);
  expect(bridge.update).toHaveBeenCalledWith(
    {
      general: systemDefault,
      local: installed("com.google.Chrome"),
    },
    1,
  );
  await act(async () => finish());
  await waitFor(() =>
    expect(screen.getByText("Browser setting saved.")).toBeTruthy(),
  );
  expect(local().value).toBe("installed:com.google.Chrome");
  expect(general().value).toBe("default");
  expect(snapshot().choices.local).toEqual(installed("com.google.Chrome"));
});

it("never shows success when persistence fails and keeps the saved choice", async () => {
  const { bridge } = fixture();
  bridge.update = vi.fn(async () => ({
    revision: 1,
    choices: { general: systemDefault, local: systemDefault },
    problem: BrowserPreferenceProblem.WriteFailed,
  }));
  render(view(bridge));
  await waitFor(() =>
    expect(within(general()).getAllByRole("option")).toHaveLength(3),
  );
  fireEvent.change(general(), {
    target: { value: "installed:org.mozilla.firefox.desktop" },
  });
  const alert = await screen.findByRole("alert");
  expect(alert.textContent).toContain("was not saved");
  expect(screen.queryByText("Browser setting saved.")).toBeNull();
  expect(general().value).toBe("default");
  expect(
    screen.getByRole("button", { name: "Reload browser settings" }),
  ).toBeTruthy();
});

it("shows an unconfirmed outcome when the native call itself rejects", async () => {
  const { bridge } = fixture();
  bridge.update = vi.fn(async () => {
    throw new Error("ipc down");
  });
  render(view(bridge));
  await waitFor(() =>
    expect(within(general()).getAllByRole("option")).toHaveLength(3),
  );
  fireEvent.change(general(), {
    target: { value: "installed:com.google.Chrome" },
  });
  expect((await screen.findByRole("alert")).textContent).toContain(
    "could not confirm",
  );
  expect(screen.queryByText("Browser setting saved.")).toBeNull();
});

it("reports a browser that disappeared before selection and refreshes the list", async () => {
  const { bridge } = fixture();
  bridge.update = vi.fn(async () => {
    throw "browser-unavailable";
  });
  render(view(bridge));
  await waitFor(() =>
    expect(within(general()).getAllByRole("option")).toHaveLength(3),
  );
  fireEvent.change(general(), {
    target: { value: "installed:com.google.Chrome" },
  });
  expect((await screen.findByRole("alert")).textContent).toContain(
    "no longer installed",
  );
  expect(general().value).toBe("default");
  await waitFor(() => expect(bridge.inventory).toHaveBeenCalledTimes(2));
  expect(
    screen.queryByRole("button", { name: "Reset browser settings" }),
  ).toBeNull();
});

it("keeps an unavailable saved identity visible and selected", async () => {
  const { bridge } = fixture({
    choices: {
      general: installed("gone.example.Browser"),
      local: systemDefault,
    },
  });
  render(view(bridge));
  await waitFor(() =>
    expect(screen.getByText(/A saved browser is not installed/)).toBeTruthy(),
  );
  expect(general().value).toBe("installed:gone.example.Browser");
  expect(
    within(general()).getByRole("option", {
      name: "gone.example.Browser (not available)",
    }),
  ).toBeTruthy();
  // The other choice is unaffected and the user may replace the missing one.
  expect(local().value).toBe("default");
  expect(general().disabled).toBe(false);
});

it("distinguishes discovery failure from no installed handlers and can refresh", async () => {
  const failing = fixture({}, new Error("discovery"));
  const first = render(view(failing.bridge));
  expect(
    (await screen.findByText(/could not be listed/)).textContent,
  ).toContain("system default browser is still available");
  expect(within(general()).getAllByRole("option")).toHaveLength(1);
  expect(screen.queryByText(/No other browsers/)).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Refresh browser list" }));
  await waitFor(() =>
    expect(failing.bridge.inventory).toHaveBeenCalledTimes(2),
  );
  first.unmount();

  const empty = fixture({}, { browsers: [] });
  render(view(empty.bridge));
  expect((await screen.findByText(/No other browsers/)).textContent).toContain(
    "system default browser is still available",
  );
  expect(screen.queryByText(/could not be listed/)).toBeNull();
  expect(within(general()).getAllByRole("option")).toHaveLength(1);
});

it("refreshes discovery each time the page opens", async () => {
  const { bridge } = fixture();
  const first = render(view(bridge));
  await waitFor(() => expect(bridge.inventory).toHaveBeenCalledTimes(1));
  first.unmount();
  render(view(bridge));
  await waitFor(() => expect(bridge.inventory).toHaveBeenCalledTimes(2));
});

it("surfaces corrupt storage, disables selection and resets only on explicit request", async () => {
  const { bridge } = fixture({
    problem: BrowserPreferenceProblem.InvalidDocument,
  });
  render(view(bridge));
  expect((await screen.findByRole("alert")).textContent).toContain("damaged");
  expect(general().disabled).toBe(true);
  expect(local().disabled).toBe(true);
  expect(bridge.reset).not.toHaveBeenCalled();
  fireEvent.click(
    screen.getByRole("button", { name: "Reset browser settings" }),
  );
  await waitFor(() => expect(bridge.reset).toHaveBeenCalledWith(1));
  await waitFor(() => expect(general().disabled).toBe(false));
  expect(
    screen.queryByRole("button", { name: "Reset browser settings" }),
  ).toBeNull();
  expect(general().value).toBe("default");
});

it("offers reload instead of reset for transient storage problems", async () => {
  const { bridge } = fixture({ problem: BrowserPreferenceProblem.ReadFailed });
  render(view(bridge));
  await screen.findByRole("alert");
  expect(
    screen.queryByRole("button", { name: "Reset browser settings" }),
  ).toBeNull();
  fireEvent.click(
    screen.getByRole("button", { name: "Reload browser settings" }),
  );
  await waitFor(() => expect(bridge.read).toHaveBeenCalledTimes(2));
});

it("follows revisioned changes published by another window", async () => {
  const { bridge, publish } = fixture();
  render(view(bridge));
  await waitFor(() =>
    expect(within(general()).getAllByRole("option")).toHaveLength(3),
  );
  act(() =>
    publish({
      revision: 2,
      choices: {
        general: installed("com.google.Chrome"),
        local: installed("org.mozilla.firefox.desktop"),
      },
      problem: null,
    }),
  );
  await waitFor(() =>
    expect(general().value).toBe("installed:com.google.Chrome"),
  );
  expect(local().value).toBe("installed:org.mozilla.firefox.desktop");
  // An older revision cannot move the visible choices backward.
  act(() =>
    publish({
      revision: 1,
      choices: { general: systemDefault, local: systemDefault },
      problem: null,
    }),
  );
  expect(general().value).toBe("installed:com.google.Chrome");
});

it("reports native capability as unavailable on web and mobile", () => {
  const { bridge } = fixture();
  render(view({ ...bridge, native: false }));
  expect(
    screen.getByText(
      "Browser selection is available only in the DeliDev desktop app.",
    ),
  ).toBeTruthy();
  expect(general().disabled).toBe(true);
  expect(bridge.read).not.toHaveBeenCalled();
  expect(bridge.inventory).not.toHaveBeenCalled();
  expect(
    screen.queryByRole("button", { name: "Refresh browser list" }),
  ).toBeNull();
});

it("renders the Korean catalog with 40px-capable native controls", async () => {
  await i18n.changeLanguage(SupportedLanguage.Korean);
  const { bridge } = fixture();
  render(view(bridge));
  await waitFor(() =>
    expect(screen.getByLabelText("일반 URL 열기 대상")).toBeTruthy(),
  );
  expect(screen.getByLabelText("로컬 URL 열기 대상")).toBeTruthy();
  expect(screen.getAllByText("시스템 기본 브라우저").length).toBeGreaterThan(0);
  expect(
    screen.getByText(
      "로그인 및 계정 인증 링크는 항상 시스템 기본 브라우저로 열립니다.",
    ),
  ).toBeTruthy();
});

it("validates native snapshots and inventories strictly", () => {
  expect(
    parseBrowserSnapshot({
      revision: 3,
      choices: {
        general: { state: "system-default" },
        local: { state: "installed", identity: "a.b" },
      },
      problem: null,
    }).choices.local,
  ).toEqual(installed("a.b"));
  for (const bad of [
    {
      revision: 1,
      choices: {
        general: { state: "installed", identity: "/bin/sh" },
        local: { state: "system-default" },
      },
      problem: null,
    },
    {
      revision: 1,
      choices: {
        general: { state: "installed", identity: "a", path: "/x" },
        local: { state: "system-default" },
      },
      problem: null,
    },
    {
      revision: -1,
      choices: {
        general: { state: "system-default" },
        local: { state: "system-default" },
      },
      problem: null,
    },
    {
      revision: 1,
      choices: { general: { state: "system-default" } },
      problem: null,
    },
    {
      revision: 1,
      choices: {
        general: { state: "system-default" },
        local: { state: "system-default" },
      },
      problem: "other",
    },
  ])
    expect(() => parseBrowserSnapshot(bad)).toThrow();
  expect(
    parseBrowserInventory({ browsers: [{ identity: "a", name: "A" }] }),
  ).toHaveLength(1);
  expect(() =>
    parseBrowserInventory({
      browsers: [{ identity: "a", name: "A", path: "/x" }],
    }),
  ).toThrow();
  expect(() =>
    parseBrowserInventory({ browsers: [{ identity: "../a", name: "A" }] }),
  ).toThrow();
  expect(() => parseBrowserInventory({ browsers: "x" })).toThrow();
});
