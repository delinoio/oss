// SPDX-License-Identifier: Apache-2.0
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { i18n, SupportedLanguage } from "./localization";
import { DateFormatPreference } from "./timestamp-format";
import { TrayQuotaState, TraySubscriptionService } from "./tray-types";
import {
  TrayPanelAction,
  parseTrayPanel,
  type TrayPanelSnapshot,
} from "./tray-status-model";
import { TrayStatus, type TrayPanelBridge } from "./tray-status";
const instance = "11111111-1111-4111-8111-111111111111",
  first = "22222222-2222-4222-8222-222222222222",
  second = "33333333-3333-4333-8333-333333333333",
  third = "55555555-5555-4555-8555-555555555555",
  scope = "44444444-4444-4444-8444-444444444444";
function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: Error) => void;
  const promise = new Promise<T>((complete, fail) => {
    resolve = complete;
    reject = fail;
  });
  return { promise, resolve, reject };
}
function fixture(): TrayPanelSnapshot {
  const observed_at = new Date(Date.now() - 60000).toISOString();
  return {
    instance,
    more: false,
    recent: first,
    theme: "light",
    language: SupportedLanguage.English,
    date_format: DateFormatPreference.Ymd,
    windows: [
      {
        target: { label: "main", instance: first, scope, revision: 7 },
        name: "This computer · Window 1",
        stale: false,
        observed_age_ms: 0,
        summary: {
          overview: {
            observed_at,
            stale: false,
            active_sessions: "3",
            pending_interactions: "2",
            connected_workers: "1",
            registered_workers: "2",
          },
          usage: {
            known_tokens: "90071992547409930000",
            incomplete: true,
            estimates: [
              { currency: "USD", known_amount: "0.000000001" },
              { currency: "KRW", known_amount: "123.000" },
            ],
          },
          accounts: {
            more: false,
            entries: [
              {
                alias: "Personal",
                subscription_service: TraySubscriptionService.ChatGPT,
                more: false,
                windows: [
                  {
                    id: "codex:primary",
                    state: TrayQuotaState.Observed,
                    remaining_basis_points: 5700,
                    observed_at,
                    reset_at: new Date(Date.now() + 3600000).toISOString(),
                  },
                  {
                    state: TrayQuotaState.Stale,
                    remaining_basis_points: 2401,
                    observed_at,
                    reset_at: null,
                  },
                ],
              },
            ],
          },
        },
      },
      {
        target: { label: "local-second", instance: second, scope, revision: 1 },
        name: "This computer · Window 2",
        stale: true,
        observed_age_ms: 46000,
        summary: null,
      },
    ],
  };
}
function secondWithSummary(): TrayPanelSnapshot {
  const snapshot = fixture();
  const observed_at = new Date(Date.now() - 60000).toISOString();
  snapshot.windows[1] = {
    ...snapshot.windows[1],
    stale: false,
    observed_age_ms: 0,
    summary: {
      overview: {
        observed_at,
        stale: false,
        active_sessions: "5",
        pending_interactions: "4",
        connected_workers: "2",
        registered_workers: "2",
      },
      usage: null,
      accounts: {
        more: false,
        entries: [
          {
            alias: "Second account",
            subscription_service: TraySubscriptionService.Claude,
            more: false,
            windows: [
              {
                id: "claude:primary",
                state: TrayQuotaState.Observed,
                remaining_basis_points: 8800,
                observed_at,
                reset_at: null,
              },
            ],
          },
        ],
      },
    },
  };
  return snapshot;
}
function thirdWithSummary(): TrayPanelSnapshot {
  const snapshot = secondWithSummary();
  snapshot.recent = third;
  snapshot.windows[1].target.instance = third;
  snapshot.windows[1].summary!.overview!.active_sessions = "7";
  snapshot.windows[1].summary!.accounts!.entries[0].alias = "Third account";
  return snapshot;
}
function controller(initial = fixture()) {
  let current = initial,
    changed = () => {};
  const bridge: TrayPanelBridge = {
    read: vi.fn(async () => current),
    subscribe: vi.fn(async (callback) => {
      changed = callback;
      return vi.fn();
    }),
    activate: vi.fn(async () => {}),
    dismiss: vi.fn(async () => {}),
  };
  return {
    bridge,
    update: (snapshot: TrayPanelSnapshot) => {
      current = snapshot;
      changed();
    },
  };
}
afterEach(async () => {
  cleanup();
  await i18n.changeLanguage("en");
});
describe("isolated tray panel", () => {
  it("shows independent quotas before exact metrics, focuses Close, renders no window selector and only navigates", async () => {
    const { bridge } = controller();
    render(<TrayStatus instance={instance} bridge={bridge} />);
    await screen.findByText("57.00% remaining");
    expect(screen.getByText("24.01% remaining")).toBeTruthy();
    expect(screen.getByText("codex:primary")).toBeTruthy();
    expect(screen.getByText("Quota 2")).toBeTruthy();
    expect(screen.getByText("90,071,992,547,409,930,000")).toBeTruthy();
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Close status" })).toBe(
        document.activeElement,
      ),
    );
    expect(screen.queryByRole("combobox")).toBeNull();
    expect(screen.queryByText("Product window")).toBeNull();
    expect(screen.getByText("Usage details").closest("details")?.open).toBe(
      false,
    );
    expect(
      screen
        .getByText("Account quotas")
        .compareDocumentPosition(screen.getByText("Active sessions")) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    fireEvent.click(
      screen.getByRole("button", { name: "Manage subscriptions" }),
    );
    await waitFor(() =>
      expect(bridge.activate).toHaveBeenCalledWith(
        TrayPanelAction.Settings,
        fixture().windows[0].target,
      ),
    );
    expect(bridge.read).toHaveBeenCalledTimes(1);
  });
  it("keeps the shared usage disclosure collapsed and changes no retained observation authority", async () => {
    const { bridge } = controller();
    render(<TrayStatus instance={instance} bridge={bridge} />);
    await screen.findByText("57.00% remaining");
    const header = screen.getByText("Usage details").closest("summary")!;
    const details = header.closest("details")!;
    expect(details.open).toBe(false);
    expect(details.dataset.disclosureDensity).toBe("settings");
    expect(header.getAttribute("aria-controls")).toBeTruthy();
    act(() => {
      details.open = true;
    });
    await waitFor(() =>
      expect(header.getAttribute("aria-expanded")).toBe("true"),
    );
    const content = document.getElementById(
      header.getAttribute("aria-controls")!,
    )!;
    const paragraph = content.querySelector("p")!;
    paragraph.tabIndex = -1;
    paragraph.focus();
    act(() => {
      details.open = false;
    });
    expect(document.activeElement).toBe(header);
    expect(bridge.read).toHaveBeenCalledTimes(1);
    expect(bridge.activate).not.toHaveBeenCalled();
    expect(bridge.dismiss).not.toHaveBeenCalled();
  });
  it("presents only the native recent window, never merging or summing windows", async () => {
    const snapshot = secondWithSummary();
    snapshot.recent = second;
    const { bridge } = controller(snapshot);
    render(<TrayStatus instance={instance} bridge={bridge} />);
    await screen.findByText("88.00% remaining");
    expect(screen.queryByRole("combobox")).toBeNull();
    expect(screen.queryByText("57.00% remaining")).toBeNull();
    expect(screen.queryByText("Personal")).toBeNull();
    expect(screen.getAllByRole("article")).toHaveLength(1);
    expect(screen.getByText("5")).toBeTruthy();
    expect(screen.queryByText("8")).toBeNull();
    expect(screen.queryByText("3")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Active sessions 5" }));
    await waitFor(() =>
      expect(bridge.activate).toHaveBeenCalledWith(
        TrayPanelAction.Sessions,
        snapshot.windows[1].target,
      ),
    );
    expect(bridge.read).toHaveBeenCalledTimes(1);
  });
  it("falls back to the first ready window when the recent window is absent", async () => {
    const snapshot = secondWithSummary();
    snapshot.recent = null;
    const { bridge } = controller(snapshot);
    render(<TrayStatus instance={instance} bridge={bridge} />);
    await screen.findByText("57.00% remaining");
    expect(screen.queryByText("88.00% remaining")).toBeNull();
  });
  it("retains the representative window during a visit and replaces it only when it disappears", async () => {
    const start = secondWithSummary();
    start.recent = second;
    const { bridge, update } = controller(start);
    render(<TrayStatus instance={instance} bridge={bridge} />);
    await screen.findByText("88.00% remaining");
    const changed = secondWithSummary();
    changed.recent = first;
    update(changed);
    await waitFor(() => expect(bridge.read).toHaveBeenCalledTimes(2));
    expect(screen.getByText("88.00% remaining")).toBeTruthy();
    expect(screen.queryByText("57.00% remaining")).toBeNull();
    const removed = secondWithSummary();
    removed.windows.splice(1, 1);
    update(removed);
    await screen.findByText("57.00% remaining");
    fireEvent.click(
      screen.getByRole("button", { name: "Manage subscriptions" }),
    );
    await waitFor(() =>
      expect(bridge.activate).toHaveBeenCalledWith(
        TrayPanelAction.Settings,
        removed.windows[0].target,
      ),
    );
  });
  it("reselects the recent window and refocuses Close on panel opening but not on updates", async () => {
    const start = secondWithSummary();
    start.recent = second;
    const { bridge, update } = controller(start);
    render(<TrayStatus instance={instance} bridge={bridge} />);
    await screen.findByText("88.00% remaining");
    const close = screen.getByRole("button", { name: "Close status" });
    await waitFor(() => expect(close).toBe(document.activeElement));
    const open = screen.getByRole("button", { name: "Open DeliDev" });
    open.focus();
    const changed = secondWithSummary();
    changed.recent = first;
    update(changed);
    await waitFor(() => expect(bridge.read).toHaveBeenCalledTimes(2));
    expect(document.activeElement).toBe(open);
    act(() => {
      window.dispatchEvent(new Event("focus"));
    });
    await screen.findByText("57.00% remaining");
    await waitFor(() => expect(close).toBe(document.activeElement));
  });
  it("waits for a post-focus read before selecting and navigating to a new recent window", async () => {
    const prior = deferred<TrayPanelSnapshot>();
    const opening = deferred<TrayPanelSnapshot>();
    const reads = [Promise.resolve(fixture()), prior.promise, opening.promise];
    let changed = () => {};
    let active = 0;
    let maximumActive = 0;
    const bridge: TrayPanelBridge = {
      read: vi.fn(async () => {
        active += 1;
        maximumActive = Math.max(maximumActive, active);
        try {
          return await reads.shift()!;
        } finally {
          active -= 1;
        }
      }),
      subscribe: vi.fn(async (callback) => {
        changed = callback;
        return vi.fn();
      }),
      activate: vi.fn(async () => {}),
      dismiss: vi.fn(async () => {}),
    };
    render(<TrayStatus instance={instance} bridge={bridge} />);
    await screen.findByText("57.00% remaining");
    const close = screen.getByRole("button", { name: "Close status" });
    await waitFor(() => expect(close).toBe(document.activeElement));
    const open = screen.getByRole("button", { name: "Open DeliDev" });
    open.focus();
    act(() => changed());
    await waitFor(() => expect(bridge.read).toHaveBeenCalledTimes(2));
    act(() => window.dispatchEvent(new Event("focus")));
    act(() => prior.resolve(fixture()));
    await waitFor(() => expect(bridge.read).toHaveBeenCalledTimes(3));
    expect(screen.getByText("57.00% remaining")).toBeTruthy();
    expect(document.activeElement).toBe(open);
    const recent = secondWithSummary();
    recent.recent = second;
    act(() => opening.resolve(recent));
    await screen.findByText("88.00% remaining");
    await waitFor(() => expect(close).toBe(document.activeElement));
    expect(maximumActive).toBe(1);
    expect(screen.queryByText("57.00% remaining")).toBeNull();
    for (const [name, action] of [
      ["Manage subscriptions", TrayPanelAction.Settings],
      ["Active sessions 5", TrayPanelAction.Sessions],
      ["Awaiting response 4", TrayPanelAction.Inbox],
      ["Today tokens · UTC Unavailable", TrayPanelAction.Usage],
      ["Open DeliDev", TrayPanelAction.Show],
    ] as const) {
      fireEvent.click(screen.getByRole("button", { name }));
      await waitFor(() =>
        expect(bridge.activate).toHaveBeenCalledWith(
          action,
          recent.windows[1].target,
        ),
      );
    }
  });
  it("ignores a superseded opening read and focuses Close only for the latest one", async () => {
    const older = deferred<TrayPanelSnapshot>();
    const latest = deferred<TrayPanelSnapshot>();
    const reads = [Promise.resolve(fixture()), older.promise, latest.promise];
    const bridge: TrayPanelBridge = {
      read: vi.fn(() => reads.shift()!),
      subscribe: vi.fn(async () => vi.fn()),
      activate: vi.fn(async () => {}),
      dismiss: vi.fn(async () => {}),
    };
    render(<TrayStatus instance={instance} bridge={bridge} />);
    await screen.findByText("57.00% remaining");
    const close = screen.getByRole("button", { name: "Close status" });
    await waitFor(() => expect(close).toBe(document.activeElement));
    const open = screen.getByRole("button", { name: "Open DeliDev" });
    open.focus();
    act(() => window.dispatchEvent(new Event("focus")));
    await waitFor(() => expect(bridge.read).toHaveBeenCalledTimes(2));
    act(() => window.dispatchEvent(new Event("focus")));
    const olderSnapshot = secondWithSummary();
    olderSnapshot.recent = second;
    act(() => older.resolve(olderSnapshot));
    await waitFor(() => expect(bridge.read).toHaveBeenCalledTimes(3));
    expect(screen.getByText("57.00% remaining")).toBeTruthy();
    expect(document.activeElement).toBe(open);
    act(() => latest.resolve(thirdWithSummary()));
    await screen.findByRole("heading", { name: "Claude · Third account" });
    await waitFor(() => expect(close).toBe(document.activeElement));
    expect(
      screen.getByRole("button", { name: "Active sessions 7" }),
    ).toBeTruthy();
  });
  it("keeps a failed opening eligible for selection on Reload", async () => {
    const failed = deferred<TrayPanelSnapshot>();
    const retry = deferred<TrayPanelSnapshot>();
    const reads = [Promise.resolve(fixture()), failed.promise, retry.promise];
    const bridge: TrayPanelBridge = {
      read: vi.fn(() => reads.shift()!),
      subscribe: vi.fn(async () => vi.fn()),
      activate: vi.fn(async () => {}),
      dismiss: vi.fn(async () => {}),
    };
    render(<TrayStatus instance={instance} bridge={bridge} />);
    await screen.findByText("57.00% remaining");
    const close = screen.getByRole("button", { name: "Close status" });
    await waitFor(() => expect(close).toBe(document.activeElement));
    const open = screen.getByRole("button", { name: "Open DeliDev" });
    open.focus();
    act(() => window.dispatchEvent(new Event("focus")));
    await waitFor(() => expect(bridge.read).toHaveBeenCalledTimes(2));
    act(() => failed.reject(new Error("read failed")));
    await screen.findByRole("alert");
    expect(document.activeElement).toBe(open);
    fireEvent.click(screen.getByRole("button", { name: "Reload status" }));
    await waitFor(() => expect(bridge.read).toHaveBeenCalledTimes(3));
    const recent = secondWithSummary();
    recent.recent = second;
    act(() => retry.resolve(recent));
    await screen.findByText("88.00% remaining");
    await waitFor(() => expect(close).toBe(document.activeElement));
    expect(screen.queryByRole("alert")).toBeNull();
  });
  it("shows localized loading through subscription and first read, then genuine observation states", async () => {
    await i18n.changeLanguage(SupportedLanguage.Korean);
    const subscription = deferred<() => void>();
    const initial = deferred<TrayPanelSnapshot>();
    const bridge: TrayPanelBridge = {
      read: vi.fn(() => initial.promise),
      subscribe: vi.fn(() => subscription.promise),
      activate: vi.fn(async () => {}),
      dismiss: vi.fn(async () => {}),
    };
    render(<TrayStatus instance={instance} bridge={bridge} />);
    expect(screen.getAllByText("상태를 불러오는 중…")).toHaveLength(2);
    expect(screen.queryByText("계정 관측을 사용할 수 없습니다.")).toBeNull();
    expect(bridge.read).not.toHaveBeenCalled();
    act(() => subscription.resolve(vi.fn()));
    await waitFor(() => expect(bridge.read).toHaveBeenCalledOnce());
    await act(async () => new Promise((resolve) => setTimeout(resolve, 0)));
    expect(screen.getAllByText("상태를 불러오는 중…")).toHaveLength(2);
    const observed = fixture();
    observed.language = SupportedLanguage.Korean;
    act(() => initial.resolve(observed));
    await screen.findByText("57.00% 남음");
    expect(screen.queryByText("상태를 불러오는 중…")).toBeNull();
    expect(screen.queryByText("계정 관측을 사용할 수 없습니다.")).toBeNull();
  });
  it("distinguishes initial loading, unavailable and empty accounts, and recovers after a failed read", async () => {
    const firstRead = deferred<TrayPanelSnapshot>();
    const retry = deferred<TrayPanelSnapshot>();
    const reads = [firstRead.promise, retry.promise];
    const bridge: TrayPanelBridge = {
      read: vi.fn(() => reads.shift()!),
      subscribe: vi.fn(async () => vi.fn()),
      activate: vi.fn(async () => {}),
      dismiss: vi.fn(async () => {}),
    };
    render(<TrayStatus instance={instance} bridge={bridge} />);
    await waitFor(() => expect(bridge.read).toHaveBeenCalledOnce());
    expect(screen.getAllByText("Loading status…")).toHaveLength(2);
    expect(
      screen.queryByText("Account observations are unavailable."),
    ).toBeNull();
    act(() => firstRead.reject(new Error("read failed")));
    await screen.findByRole("alert");
    fireEvent.click(screen.getByRole("button", { name: "Reload status" }));
    await waitFor(() => expect(bridge.read).toHaveBeenCalledTimes(2));
    const observed = fixture();
    observed.windows[0].summary!.accounts = null;
    act(() => retry.resolve(observed));
    await screen.findByText("Account observations are unavailable.");
    expect(screen.queryByRole("alert")).toBeNull();
  });
  it("retains quota state, supports locale updates, and Escape hides without activating product windows", async () => {
    const { bridge, update } = controller();
    render(<TrayStatus instance={instance} bridge={bridge} />);
    await screen.findByText("57.00% remaining");
    const next = fixture();
    next.language = SupportedLanguage.Korean;
    next.theme = "dark";
    next.windows[0].stale = true;
    next.windows[0].summary!.accounts!.entries[0].windows[0].state =
      TrayQuotaState.Failed;
    update(next);
    await screen.findByText("관측 실패");
    expect(screen.queryByText("57.00% 남음")).toBeNull();
    expect(document.documentElement.dataset.theme).toBe("dark");
    fireEvent.keyDown(screen.getByRole("main"), { key: "Escape" });
    await waitFor(() => expect(bridge.dismiss).toHaveBeenCalledOnce());
    expect(bridge.activate).not.toHaveBeenCalled();
  });
  it("keeps empty accounts distinct from unavailable observations and preserves recovery", async () => {
    const snapshot = fixture();
    snapshot.windows[0].summary!.accounts = { entries: [], more: false };
    const { bridge, update } = controller(snapshot);
    render(<TrayStatus instance={instance} bridge={bridge} />);
    await screen.findByText("No accounts in this observation.");
    update({ ...snapshot, windows: [], recent: null });
    await waitFor(() =>
      expect(screen.getAllByText("Unavailable").length).toBeGreaterThan(0),
    );
    fireEvent.click(screen.getByRole("button", { name: "Open DeliDev" }));
    await waitFor(() =>
      expect(bridge.activate).toHaveBeenCalledWith(
        TrayPanelAction.Recovery,
        undefined,
      ),
    );
  });
});
it("rejects foreign instances, authority fields, oversized data, duplicates and impossible quota", () => {
  const valid = fixture();
  expect(parseTrayPanel(valid, instance)).toBe(valid);
  expect(() =>
    parseTrayPanel({ ...valid, endpoint: "https://private.test" }, instance),
  ).toThrow();
  expect(() => parseTrayPanel(valid, second)).toThrow();
  expect(() =>
    parseTrayPanel(
      { ...valid, windows: [...valid.windows, valid.windows[0]] },
      instance,
    ),
  ).toThrow();
  const bad = fixture();
  bad.windows[0].summary!.accounts!.entries[0].windows[0].remaining_basis_points = 10001;
  expect(() => parseTrayPanel(bad, instance)).toThrow();
});
