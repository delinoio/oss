// SPDX-License-Identifier: Apache-2.0
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import {
  SidebarPreference,
  SidebarProblem,
  SidebarProvider,
  memorySidebarBridge,
  parseSidebar,
  useSidebarPreference,
  type SidebarBridge,
  type SidebarSnapshot,
} from "./sidebar-preference";
import {
  PresentationOutcome,
  PresentationPhase,
} from "./presentation-diagnostics";
// The product window renders no sidebar notice; the controller state is read
// through test-local controls instead.
const shownProblem = () => screen.getByLabelText("Problem").textContent;
const problemSettled = () =>
  waitFor(() => expect(shownProblem()).not.toBe("none"));
function Controls() {
  const { snapshot, operation, select, reload } = useSidebarPreference();
  return (
    <>
      <button
        aria-disabled={Boolean(operation || snapshot.problem)}
        onClick={() =>
          select(
            snapshot.sidebar_preference === SidebarPreference.Expanded
              ? SidebarPreference.Collapsed
              : SidebarPreference.Expanded,
          )
        }
      >
        Toggle
      </button>
      <output>{snapshot.sidebar_preference}</output>
      <button onClick={reload}>Reload</button>
      <output aria-label="Problem">{snapshot.problem ?? "none"}</output>
      <textarea aria-label="Draft" defaultValue="retained" />
    </>
  );
}
function fixture() {
  let current: SidebarSnapshot = {
    revision: 1,
    sidebar_preference: SidebarPreference.Expanded,
    problem: null,
  };
  const listeners = new Set<(value: unknown) => void>();
  const publish = (next: SidebarSnapshot) => {
    current = next;
    listeners.forEach((changed) => changed(next));
  };
  const bridge: SidebarBridge = {
    read: vi.fn(async () => current),
    update: vi.fn(async (sidebar_preference, revision) => {
      const next = {
        revision: revision + 1,
        sidebar_preference,
        problem: null,
      };
      publish(next);
      return next;
    }),
    subscribe: async (changed) => {
      listeners.add(changed);
      return () => {
        listeners.delete(changed);
      };
    },
  };
  return { bridge, publish };
}
test("committed changes synchronize mounted and later windows without replacing drafts", async () => {
  const bridge = memorySidebarBridge();
  const first = render(
    <SidebarProvider bridge={bridge}>
      <Controls />
    </SidebarProvider>,
  );
  const second = render(
    <SidebarProvider bridge={bridge}>
      <Controls />
    </SidebarProvider>,
  );
  const draft = within(first.container).getByRole("textbox");
  await waitFor(() =>
    expect(
      within(first.container)
        .getByRole("button", { name: "Toggle" })
        .getAttribute("aria-disabled"),
    ).toBe("false"),
  );
  act(() => draft.focus());
  fireEvent.click(
    within(first.container).getByRole("button", { name: "Toggle" }),
  );
  await waitFor(() =>
    expect(within(second.container).getByText("collapsed")).toBeTruthy(),
  );
  expect(document.activeElement).toBe(draft);
  expect(draft).toHaveProperty("value", "retained");
  first.unmount();
  second.unmount();
  render(
    <SidebarProvider bridge={bridge}>
      <Controls />
    </SidebarProvider>,
  );
  await screen.findByText("collapsed");
});
test("pending and uncertain writes never replay; unrelated events cannot clear recovery", async () => {
  const value = fixture();
  let reject!: (error: Error) => void;
  vi.mocked(value.bridge.update).mockReturnValueOnce(
    new Promise((_, fail) => {
      reject = fail;
    }),
  );
  render(
    <SidebarProvider bridge={value.bridge}>
      <Controls />
    </SidebarProvider>,
  );
  const toggle = screen.getByRole("button", { name: "Toggle" });
  await waitFor(() =>
    expect(toggle.getAttribute("aria-disabled")).toBe("false"),
  );
  fireEvent.click(toggle);
  fireEvent.click(toggle);
  expect(value.bridge.update).toHaveBeenCalledExactlyOnceWith(
    SidebarPreference.Collapsed,
    1,
  );
  await act(async () => reject(new Error("private storage detail")));
  await problemSettled();
  const reload = screen.getByRole("button", { name: "Reload" });
  act(() =>
    value.publish({
      revision: 2,
      sidebar_preference: SidebarPreference.Collapsed,
      problem: null,
    }),
  );
  expect(screen.getByText("expanded")).toBeTruthy();
  fireEvent.click(toggle);
  fireEvent.focus(window);
  expect(value.bridge.update).toHaveBeenCalledTimes(1);
  expect(screen.queryByText(/private storage detail/)).toBeNull();
  fireEvent.click(reload);
  await screen.findByText("collapsed");
  expect(value.bridge.update).toHaveBeenCalledTimes(1);
});
test("old responses and contradictory same-generation events cannot overwrite verified preference", async () => {
  const value = fixture();
  render(
    <SidebarProvider bridge={value.bridge}>
      <Controls />
    </SidebarProvider>,
  );
  await waitFor(() =>
    expect(
      screen
        .getByRole("button", { name: "Toggle" })
        .getAttribute("aria-disabled"),
    ).toBe("false"),
  );
  act(() =>
    value.publish({
      revision: 7,
      sidebar_preference: SidebarPreference.Collapsed,
      problem: null,
    }),
  );
  act(() =>
    value.publish({
      revision: 2,
      sidebar_preference: SidebarPreference.Expanded,
      problem: null,
    }),
  );
  expect(screen.getByText("collapsed")).toBeTruthy();
  act(() =>
    value.publish({
      revision: 7,
      sidebar_preference: SidebarPreference.Expanded,
      problem: null,
    }),
  );
  expect(screen.getByText("collapsed")).toBeTruthy();
  await problemSettled();
});
test.each([
  SidebarProblem.Changed,
  SidebarProblem.WriteFailed,
  SidebarProblem.OutcomeUnknown,
])(
  "an older %s reply to the original save fences without regressing a newer healthy event",
  async (problem) => {
    const value = fixture();
    let resolve!: (reply: unknown) => void;
    vi.mocked(value.bridge.update).mockReturnValueOnce(
      new Promise((done) => {
        resolve = done;
      }),
    );
    render(
      <SidebarProvider bridge={value.bridge}>
        <Controls />
      </SidebarProvider>,
    );
    const toggle = screen.getByRole("button", { name: "Toggle" });
    await waitFor(() =>
      expect(toggle.getAttribute("aria-disabled")).toBe("false"),
    );
    fireEvent.click(toggle);
    expect(value.bridge.update).toHaveBeenCalledExactlyOnceWith(
      SidebarPreference.Collapsed,
      1,
    );
    act(() =>
      value.publish({
        revision: 3,
        sidebar_preference: SidebarPreference.Collapsed,
        problem: null,
      }),
    );
    await act(async () =>
      resolve({
        revision: 2,
        sidebar_preference: SidebarPreference.Expanded,
        problem,
      }),
    );
    await problemSettled();
    const reload = screen.getByRole("button", { name: "Reload" });
    expect(shownProblem()).toBe(problem);
    // The committed presentation keeps the newer revision.
    expect(screen.getByText("collapsed")).toBeTruthy();
    expect(toggle.getAttribute("aria-disabled")).toBe("true");
    const reads = vi.mocked(value.bridge.read).mock.calls.length;
    fireEvent.click(toggle);
    fireEvent.focus(window);
    document.dispatchEvent(new Event("visibilitychange"));
    expect(value.bridge.update).toHaveBeenCalledTimes(1);
    expect(value.bridge.read).toHaveBeenCalledTimes(reads);
    // A healthy event does not settle the failed original save.
    act(() =>
      value.publish({
        revision: 4,
        sidebar_preference: SidebarPreference.Collapsed,
        problem: null,
      }),
    );
    expect(shownProblem()).toBe(problem);
    fireEvent.click(reload);
    await waitFor(() => expect(shownProblem()).toBe("none"));
    expect(value.bridge.read).toHaveBeenCalledTimes(reads + 1);
    // Reload never replays the original save.
    expect(value.bridge.update).toHaveBeenCalledTimes(1);
    expect(screen.getByText("collapsed")).toBeTruthy();
    await waitFor(() =>
      expect(toggle.getAttribute("aria-disabled")).toBe("false"),
    );
    fireEvent.click(toggle);
    await waitFor(() => expect(value.bridge.update).toHaveBeenCalledTimes(2));
    expect(value.bridge.update).toHaveBeenLastCalledWith(
      SidebarPreference.Expanded,
      4,
    );
  },
);
test.each([
  SidebarProblem.Unavailable,
  SidebarProblem.ReadFailed,
  SidebarProblem.InvalidDocument,
  SidebarProblem.UnsupportedVersion,
])(
  "an older %s inspection reply to the original save stays an ignored stale snapshot",
  async (problem) => {
    const value = fixture();
    let resolve!: (reply: unknown) => void;
    vi.mocked(value.bridge.update).mockReturnValueOnce(
      new Promise((done) => {
        resolve = done;
      }),
    );
    render(
      <SidebarProvider bridge={value.bridge}>
        <Controls />
      </SidebarProvider>,
    );
    const toggle = screen.getByRole("button", { name: "Toggle" });
    await waitFor(() =>
      expect(toggle.getAttribute("aria-disabled")).toBe("false"),
    );
    fireEvent.click(toggle);
    act(() =>
      value.publish({
        revision: 3,
        sidebar_preference: SidebarPreference.Collapsed,
        problem: null,
      }),
    );
    await act(async () =>
      resolve({
        revision: 2,
        sidebar_preference: SidebarPreference.Expanded,
        problem,
      }),
    );
    expect(screen.getByText("collapsed")).toBeTruthy();
    expect(shownProblem()).toBe("none");
    await waitFor(() =>
      expect(toggle.getAttribute("aria-disabled")).toBe("false"),
    );
  },
);
test("an older healthy reply to the original save stays ignored and a disposed generation ignores late failures", async () => {
  const value = fixture();
  let resolve!: (reply: unknown) => void;
  vi.mocked(value.bridge.update).mockReturnValueOnce(
    new Promise((done) => {
      resolve = done;
    }),
  );
  const view = render(
    <SidebarProvider bridge={value.bridge}>
      <Controls />
    </SidebarProvider>,
  );
  const toggle = screen.getByRole("button", { name: "Toggle" });
  await waitFor(() =>
    expect(toggle.getAttribute("aria-disabled")).toBe("false"),
  );
  fireEvent.click(toggle);
  act(() =>
    value.publish({
      revision: 3,
      sidebar_preference: SidebarPreference.Collapsed,
      problem: null,
    }),
  );
  await act(async () =>
    resolve({
      revision: 2,
      sidebar_preference: SidebarPreference.Expanded,
      problem: null,
    }),
  );
  expect(screen.getByText("collapsed")).toBeTruthy();
  expect(shownProblem()).toBe("none");
  view.unmount();
  const late = fixture();
  let resolveLate!: (reply: unknown) => void;
  vi.mocked(late.bridge.update).mockReturnValueOnce(
    new Promise((done) => {
      resolveLate = done;
    }),
  );
  const second = render(
    <SidebarProvider bridge={late.bridge}>
      <Controls />
    </SidebarProvider>,
  );
  const lateToggle = within(second.container).getByRole("button", {
    name: "Toggle",
  });
  await waitFor(() =>
    expect(lateToggle.getAttribute("aria-disabled")).toBe("false"),
  );
  fireEvent.click(lateToggle);
  act(() =>
    late.publish({
      revision: 3,
      sidebar_preference: SidebarPreference.Collapsed,
      problem: null,
    }),
  );
  second.rerender(
    <SidebarProvider bridge={fixture().bridge}>
      <Controls />
    </SidebarProvider>,
  );
  await act(async () =>
    resolveLate({
      revision: 2,
      sidebar_preference: SidebarPreference.Expanded,
      problem: SidebarProblem.WriteFailed,
    }),
  );
  expect(shownProblem()).toBe("none");
});
test("closed snapshots reject malformed documents, revisions, enums and fields", () => {
  const valid = { revision: 1, sidebar_preference: "expanded", problem: null };
  for (const value of [
    null,
    {},
    { ...valid, revision: -1 },
    { ...valid, revision: 1.5 },
    { ...valid, revision: 2 ** 32 },
    { ...valid, sidebar_preference: "other" },
    { ...valid, problem: "private-path" },
    { ...valid, extra: true },
  ])
    expect(() => parseSidebar(value)).toThrow();
});

test("primary+B accepts ordinary inputs on each platform while retaining all dispatch exclusions", async () => {
  const {
    dispatchShortcut,
    globalShortcutBindings,
    ShortcutId,
    ShortcutInput,
    ShortcutPlatform,
    ShortcutScope,
  } = await import("./shortcuts");
  const { Surface } = await import("./surface");
  render(<textarea aria-label="Typing" defaultValue="draft" />);
  const input = screen.getByRole("textbox");
  const run = vi.fn();
  const definitions = [
    {
      id: ShortcutId.ToggleSidebar,
      scope: ShortcutScope.Global,
      label: "sidebar-preference.toggle" as const,
      bindings: globalShortcutBindings[ShortcutId.ToggleSidebar],
      input: ShortcutInput.Allow,
      run,
    },
  ];
  for (const platform of [ShortcutPlatform.Mac, ShortcutPlatform.Other]) {
    const primary =
      platform === ShortcutPlatform.Mac ? { metaKey: true } : { ctrlKey: true };
    const event = (options: KeyboardEventInit = {}) => {
      const value = new KeyboardEvent("keydown", {
        key: "b",
        cancelable: true,
        ...primary,
        ...options,
      });
      Object.defineProperty(value, "target", { value: input });
      return value;
    };
    run.mockClear();
    expect(
      dispatchShortcut(event(), definitions, Surface.Sessions, platform),
    ).toBe(true);
    expect(run).toHaveBeenCalledTimes(1);
    for (const options of [
      { repeat: true },
      { isComposing: true },
      { altKey: true },
      { shiftKey: true },
      { metaKey: true, ctrlKey: true },
      platform === ShortcutPlatform.Mac
        ? { metaKey: false, ctrlKey: true }
        : { ctrlKey: false, metaKey: true },
    ])
      expect(
        dispatchShortcut(
          event(options),
          definitions,
          Surface.Sessions,
          platform,
        ),
      ).toBe(false);
    const handled = event();
    handled.preventDefault();
    expect(
      dispatchShortcut(handled, definitions, Surface.Sessions, platform),
    ).toBe(false);
    input.setAttribute("data-shortcuts", "passthrough");
    expect(
      dispatchShortcut(event(), definitions, Surface.Sessions, platform),
    ).toBe(false);
    input.removeAttribute("data-shortcuts");
    input.setAttribute("inert", "");
    expect(
      dispatchShortcut(event(), definitions, Surface.Sessions, platform),
    ).toBe(false);
    input.removeAttribute("inert");
    expect(run).toHaveBeenCalledTimes(1);
  }
});

// Cells of the sidebar rows in the diagnostics table of the desktop contract.
// The provider records phase transitions only and never its loading placeholder.
afterEach(() => vi.restoreAllMocks());
const sidebarRecords = (spy: { mock: { calls: unknown[][] } }) =>
  spy.mock.calls
    .filter(([name]) => name === "delidev.presentation.outcome")
    .map(([, record]) => record);
const sidebarFailed = (phase: PresentationPhase) => ({
  phase,
  outcome: PresentationOutcome.Failed,
});
const sidebarRecovered = (phase: PresentationPhase) => ({
  phase,
  outcome: PresentationOutcome.Recovered,
});
const healthy = (revision: number): SidebarSnapshot => ({
  revision,
  sidebar_preference: SidebarPreference.Expanded,
  problem: null,
});
const withProblem = (problem: SidebarProblem): SidebarSnapshot => ({
  revision: 1,
  sidebar_preference: SidebarPreference.Expanded,
  problem,
});
function diagnosticsFixture(
  read: () => Promise<unknown>,
  update: () => Promise<unknown> = async () => healthy(2),
) {
  const bridge: SidebarBridge = {
    read: vi.fn(read),
    update: vi.fn(update),
    subscribe: async () => () => {},
  };
  const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
  const info = vi.spyOn(console, "info").mockImplementation(() => {});
  const view = render(
    <SidebarProvider bridge={bridge}>
      <Controls />
    </SidebarProvider>,
  );
  return { bridge, warn, info, view };
}
test("a healthy first read records nothing", async () => {
  const value = diagnosticsFixture(async () => healthy(1));
  const toggle = screen.getByRole("button", { name: "Toggle" });
  await waitFor(() =>
    expect(toggle.getAttribute("aria-disabled")).toBe("false"),
  );
  expect(value.warn).not.toHaveBeenCalled();
  expect(value.info).not.toHaveBeenCalled();
});
test.each([
  SidebarProblem.Unavailable,
  SidebarProblem.ReadFailed,
  SidebarProblem.InvalidDocument,
  SidebarProblem.UnsupportedVersion,
])(
  "a first native read that returns the real %s problem records one sidebar-read warning",
  async (problem) => {
    const value = diagnosticsFixture(async () => withProblem(problem));
    await waitFor(() => expect(value.warn).toHaveBeenCalledTimes(1));
    await problemSettled();
    expect(shownProblem()).toBe(problem);
    expect(sidebarRecords(value.warn)).toEqual([
      sidebarFailed(PresentationPhase.SidebarRead),
    ]);
    expect(JSON.stringify(value.warn.mock.calls)).not.toContain(problem);
    // Reload that returns the same problem adds no record.
    vi.mocked(value.bridge.read).mockResolvedValueOnce(withProblem(problem));
    fireEvent.click(screen.getByRole("button", { name: "Reload" }));
    await waitFor(() => expect(value.bridge.read).toHaveBeenCalledTimes(2));
    await act(async () => {});
    expect(value.warn).toHaveBeenCalledTimes(1);
    // A healthy Reload records one recovery.
    vi.mocked(value.bridge.read).mockResolvedValueOnce(healthy(2));
    fireEvent.click(screen.getByRole("button", { name: "Reload" }));
    await waitFor(() => expect(shownProblem()).toBe("none"));
    expect(sidebarRecords(value.info)).toEqual([
      sidebarRecovered(PresentationPhase.SidebarRead),
    ]);
  },
);
test("a rejected first read records one sidebar-read warning without raw error text", async () => {
  const value = diagnosticsFixture(async () => {
    throw new Error("private storage detail");
  });
  await waitFor(() => expect(value.warn).toHaveBeenCalledTimes(1));
  expect(sidebarRecords(value.warn)).toEqual([
    sidebarFailed(PresentationPhase.SidebarRead),
  ]);
  expect(JSON.stringify(value.warn.mock.calls)).not.toContain("private");
});
test.each([
  [
    "a rejected save",
    () => Promise.reject(new Error("private storage detail")),
  ],
  [
    "a WriteFailed reply",
    async () => ({ ...healthy(1), problem: SidebarProblem.WriteFailed }),
  ],
  [
    "a Changed reply",
    async () => ({ ...healthy(1), problem: SidebarProblem.Changed }),
  ],
])(
  "%s records one sidebar-save warning and its recovery",
  async (_name, update) => {
    const value = diagnosticsFixture(async () => healthy(1), update);
    const toggle = screen.getByRole("button", { name: "Toggle" });
    await waitFor(() =>
      expect(toggle.getAttribute("aria-disabled")).toBe("false"),
    );
    fireEvent.click(toggle);
    await waitFor(() => expect(value.warn).toHaveBeenCalledTimes(1));
    expect(sidebarRecords(value.warn)).toEqual([
      sidebarFailed(PresentationPhase.SidebarSave),
    ]);
    expect(JSON.stringify(value.warn.mock.calls)).not.toContain("private");
    await waitFor(() => expect(shownProblem()).not.toBe("none"));
    fireEvent.click(screen.getByRole("button", { name: "Reload" }));
    await waitFor(() => expect(shownProblem()).toBe("none"));
    expect(sidebarRecords(value.info)).toEqual([
      sidebarRecovered(PresentationPhase.SidebarSave),
    ]);
  },
);
test("a failed Reload after a save problem records the read phase and a healthy Reload recovers both", async () => {
  const value = diagnosticsFixture(
    async () => healthy(1),
    async () => ({ ...healthy(1), problem: SidebarProblem.OutcomeUnknown }),
  );
  const toggle = screen.getByRole("button", { name: "Toggle" });
  await waitFor(() =>
    expect(toggle.getAttribute("aria-disabled")).toBe("false"),
  );
  fireEvent.click(toggle);
  await waitFor(() => expect(value.warn).toHaveBeenCalledTimes(1));
  vi.mocked(value.bridge.read).mockRejectedValueOnce(new Error("busy"));
  fireEvent.click(screen.getByRole("button", { name: "Reload" }));
  await waitFor(() => expect(value.warn).toHaveBeenCalledTimes(2));
  expect(sidebarRecords(value.warn)).toEqual([
    sidebarFailed(PresentationPhase.SidebarSave),
    sidebarFailed(PresentationPhase.SidebarRead),
  ]);
  fireEvent.click(screen.getByRole("button", { name: "Reload" }));
  await waitFor(() => expect(value.info).toHaveBeenCalledTimes(2));
  expect(sidebarRecords(value.info)).toEqual([
    sidebarRecovered(PresentationPhase.SidebarRead),
    sidebarRecovered(PresentationPhase.SidebarSave),
  ]);
});
test("a read that settles after the provider unmounts records nothing", async () => {
  let reject!: (error: Error) => void;
  const value = diagnosticsFixture(
    () =>
      new Promise((_, fail) => {
        reject = fail;
      }),
  );
  await waitFor(() => expect(value.bridge.read).toHaveBeenCalledTimes(1));
  value.view.unmount();
  await act(async () => reject(new Error("late")));
  expect(value.warn).not.toHaveBeenCalled();
});
