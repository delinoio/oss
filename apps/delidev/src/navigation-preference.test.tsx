// SPDX-License-Identifier: Apache-2.0
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { expect, test, vi } from "vitest";
import {
  NavigationProblem,
  NavigationProvider,
  NavigationPreferenceNotice,
  memoryNavigationBridge,
  parseNavigation,
  useNavigationPreference,
  type NavigationBridge,
  type NavigationSnapshot,
} from "./navigation-preference";
function Controls() {
  const { snapshot, operation, select } = useNavigationPreference();
  return (
    <>
      <button
        aria-disabled={Boolean(operation || snapshot.problem)}
        onClick={() => select(!snapshot.usage_pinned)}
      >
        Toggle
      </button>
      <output>{snapshot.usage_pinned ? "pinned" : "unpinned"}</output>
      <NavigationPreferenceNotice />
      <textarea aria-label="Draft" defaultValue="retained" />
    </>
  );
}
function fixture() {
  let current: NavigationSnapshot = {
    revision: 1,
    usage_pinned: false,
    problem: null,
  };
  const listeners = new Set<(value: unknown) => void>();
  const publish = (next: NavigationSnapshot) => {
    current = next;
    listeners.forEach((changed) => changed(next));
  };
  const bridge: NavigationBridge = {
    read: vi.fn(async () => current),
    update: vi.fn(async (usage_pinned, revision) => {
      const next = {
        revision: revision + 1,
        usage_pinned,
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
  const bridge = memoryNavigationBridge();
  const first = render(
    <NavigationProvider bridge={bridge}>
      <Controls />
    </NavigationProvider>,
  );
  const second = render(
    <NavigationProvider bridge={bridge}>
      <Controls />
    </NavigationProvider>,
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
    expect(within(second.container).getByText("pinned")).toBeTruthy(),
  );
  expect(document.activeElement).toBe(draft);
  expect(draft).toHaveProperty("value", "retained");
  first.unmount();
  second.unmount();
  render(
    <NavigationProvider bridge={bridge}>
      <Controls />
    </NavigationProvider>,
  );
  await screen.findByText("pinned");
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
    <NavigationProvider bridge={value.bridge}>
      <Controls />
    </NavigationProvider>,
  );
  const toggle = screen.getByRole("button", { name: "Toggle" });
  await waitFor(() =>
    expect(toggle.getAttribute("aria-disabled")).toBe("false"),
  );
  fireEvent.click(toggle);
  fireEvent.click(toggle);
  expect(value.bridge.update).toHaveBeenCalledExactlyOnceWith(
    true,
    1,
  );
  await act(async () => reject(new Error("private storage detail")));
  const reload = await screen.findByRole("button", {
    name: "Reload navigation preference",
  });
  act(() =>
    value.publish({
      revision: 2,
      usage_pinned: true,
      problem: null,
    }),
  );
  expect(screen.getByText("unpinned")).toBeTruthy();
  fireEvent.click(toggle);
  fireEvent.focus(window);
  expect(value.bridge.update).toHaveBeenCalledTimes(1);
  expect(screen.queryByText(/private storage detail/)).toBeNull();
  fireEvent.click(reload);
  await screen.findByText("pinned");
  expect(value.bridge.update).toHaveBeenCalledTimes(1);
});
test("old responses and contradictory same-generation events cannot overwrite verified pin state", async () => {
  const value = fixture();
  render(
    <NavigationProvider bridge={value.bridge}>
      <Controls />
    </NavigationProvider>,
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
      usage_pinned: true,
      problem: null,
    }),
  );
  act(() =>
    value.publish({
      revision: 2,
      usage_pinned: false,
      problem: null,
    }),
  );
  expect(screen.getByText("pinned")).toBeTruthy();
  act(() =>
    value.publish({
      revision: 7,
      usage_pinned: false,
      problem: null,
    }),
  );
  expect(screen.getByText("pinned")).toBeTruthy();
  await screen.findByRole("button", { name: "Reload navigation preference" });
});
test.each([
  NavigationProblem.Changed,
  NavigationProblem.WriteFailed,
  NavigationProblem.OutcomeUnknown,
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
      <NavigationProvider bridge={value.bridge}>
        <Controls />
      </NavigationProvider>,
    );
    const toggle = screen.getByRole("button", { name: "Toggle" });
    await waitFor(() =>
      expect(toggle.getAttribute("aria-disabled")).toBe("false"),
    );
    fireEvent.click(toggle);
    expect(value.bridge.update).toHaveBeenCalledExactlyOnceWith(
      true,
      1,
    );
    act(() =>
      value.publish({
        revision: 3,
        usage_pinned: true,
        problem: null,
      }),
    );
    await act(async () =>
      resolve({
        revision: 2,
        usage_pinned: false,
        problem,
      }),
    );
    const reload = await screen.findByRole("button", {
      name: "Reload navigation preference",
    });
    expect(screen.getByRole("alert")).toBeTruthy();
    // The committed presentation keeps the newer revision.
    expect(screen.getByText("pinned")).toBeTruthy();
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
        usage_pinned: true,
        problem: null,
      }),
    );
    expect(screen.getByRole("alert")).toBeTruthy();
    fireEvent.click(reload);
    await waitFor(() => expect(screen.queryByRole("alert")).toBeNull());
    expect(value.bridge.read).toHaveBeenCalledTimes(reads + 1);
    // Reload never replays the original save.
    expect(value.bridge.update).toHaveBeenCalledTimes(1);
    expect(screen.getByText("pinned")).toBeTruthy();
    await waitFor(() =>
      expect(toggle.getAttribute("aria-disabled")).toBe("false"),
    );
    fireEvent.click(toggle);
    await waitFor(() => expect(value.bridge.update).toHaveBeenCalledTimes(2));
    expect(value.bridge.update).toHaveBeenLastCalledWith(
      false,
      4,
    );
  },
);
test.each([
  NavigationProblem.Unavailable,
  NavigationProblem.ReadFailed,
  NavigationProblem.InvalidDocument,
  NavigationProblem.UnsupportedVersion,
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
      <NavigationProvider bridge={value.bridge}>
        <Controls />
      </NavigationProvider>,
    );
    const toggle = screen.getByRole("button", { name: "Toggle" });
    await waitFor(() =>
      expect(toggle.getAttribute("aria-disabled")).toBe("false"),
    );
    fireEvent.click(toggle);
    act(() =>
      value.publish({
        revision: 3,
        usage_pinned: true,
        problem: null,
      }),
    );
    await act(async () =>
      resolve({
        revision: 2,
        usage_pinned: false,
        problem,
      }),
    );
    expect(screen.getByText("pinned")).toBeTruthy();
    expect(screen.queryByRole("alert")).toBeNull();
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
    <NavigationProvider bridge={value.bridge}>
      <Controls />
    </NavigationProvider>,
  );
  const toggle = screen.getByRole("button", { name: "Toggle" });
  await waitFor(() =>
    expect(toggle.getAttribute("aria-disabled")).toBe("false"),
  );
  fireEvent.click(toggle);
  act(() =>
    value.publish({
      revision: 3,
      usage_pinned: true,
      problem: null,
    }),
  );
  await act(async () =>
    resolve({
      revision: 2,
      usage_pinned: false,
      problem: null,
    }),
  );
  expect(screen.getByText("pinned")).toBeTruthy();
  expect(screen.queryByRole("alert")).toBeNull();
  view.unmount();
  const late = fixture();
  let resolveLate!: (reply: unknown) => void;
  vi.mocked(late.bridge.update).mockReturnValueOnce(
    new Promise((done) => {
      resolveLate = done;
    }),
  );
  const second = render(
    <NavigationProvider bridge={late.bridge}>
      <Controls />
    </NavigationProvider>,
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
      usage_pinned: true,
      problem: null,
    }),
  );
  second.rerender(
    <NavigationProvider bridge={fixture().bridge}>
      <Controls />
    </NavigationProvider>,
  );
  await act(async () =>
    resolveLate({
      revision: 2,
      usage_pinned: false,
      problem: NavigationProblem.WriteFailed,
    }),
  );
  expect(screen.queryByRole("alert")).toBeNull();
});
test("closed snapshots reject malformed documents, revisions, booleans and fields", () => {
  const valid = { revision: 1, usage_pinned: false, problem: null };
  for (const value of [
    null,
    {},
    { ...valid, revision: -1 },
    { ...valid, revision: 1.5 },
    { ...valid, revision: 2 ** 32 },
    { ...valid, usage_pinned: "yes" },
    { ...valid, problem: "private-path" },
    { ...valid, extra: true },
  ])
    expect(() => parseNavigation(value)).toThrow();
});
