// SPDX-License-Identifier: Apache-2.0
import { useState } from "react";
import { act, fireEvent, render as rtlRender, screen, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { quotaColor } from "./quota-color";
import { SubscriptionBrand } from "./subscription-catalog";
import { QuotaObservationState as Quota, SubscriptionConnectionState as Connection, SubscriptionOperationState as Operation, SubscriptionReadState as Read, SubscriptionSettingsView, quotaPresentation, type SubscriptionAccountRow, type SubscriptionQuotaWindow } from "./subscription-settings";

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { SettingsTasks, SettingsTaskBackground } from "./settings-task";
function render(ui: React.ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return rtlRender(ui, { wrapper: ({ children }) => <QueryClientProvider client={client}><SettingsTasks><SettingsTaskBackground>{children}</SettingsTaskBackground></SettingsTasks></QueryClientProvider> });
}
const now = Date.parse("2026-09-30T02:00:00Z");
const observedAt = "2026-09-30T01:59:00Z", resetAt = "2026-09-30T05:00:00Z";
function row(id = "chatgpt", windows: SubscriptionQuotaWindow[] = []): SubscriptionAccountRow {
  return { id, alias: id === "chatgpt" ? "ChatGPT Personal" : "Claude Work", providerName: id === "chatgpt" ? "ChatGPT" : "Claude", brand: id === "chatgpt" ? SubscriptionBrand.ChatGPT : SubscriptionBrand.Claude, maskedIdentity: "f***@example.test", connection: Connection.Connected, health: "ready", enabled: true, providerState: "Enabled", confirmedExhausted: false, metadataAvailable: true, windows, refresh: vi.fn(), disconnect: vi.fn(), details: vi.fn(), edit: vi.fn(), delete: vi.fn() };
}
function window(id: string, remaining?: number, state = Quota.Observed): SubscriptionQuotaWindow { return { id, remaining, state, observedAt, resetAt }; }
function view(accounts: SubscriptionAccountRow[] = [], props: Partial<React.ComponentProps<typeof SubscriptionSettingsView>> = {}) {
  return <SubscriptionSettingsView accounts={accounts} state={Read.Ready} now={now} clearFilter={vi.fn()} advanced={<button>Metadata settings fixture</button>} {...props} />;
}

it("renders separate observed quota windows, explicit branding and masked fixture identities with exact callbacks", () => {
  const first = row("chatgpt", [window("five-hour", .68), window("weekly", .82), window("extra", .5)]);
  const second = row("claude", [window("five-hour", .41), window("weekly", .76)]);
  render(view([first, second]));
  expect(screen.getAllByRole("progressbar").map((bar) => bar.getAttribute("value"))).toEqual(["68", "82", "41", "76"]);
  expect(screen.getAllByText(/f\*\*\*@example.test/)).toHaveLength(2);
  const firstRow = screen.getByRole("article", { name: first.alias });
  expect(within(firstRow).getAllByTitle(observedAt)).toHaveLength(2);
  expect(within(firstRow).getAllByTitle(resetAt)).toHaveLength(2);
  fireEvent.click(screen.getByRole("button", { name: `Refresh ${first.alias}` }));
  expect(first.refresh).toHaveBeenCalledTimes(1); expect(second.refresh).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "View all 3 quota windows" }));
  expect(within(screen.getByRole("dialog", { name: "Account details" })).getByText("extra")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Close Account details" }));
  expect(screen.getAllByRole("heading", { level: 3 }).slice(-3).map((heading) => heading.textContent)).toEqual(["ChatGPT", "Claude", "Grok"]);
  expect(screen.getByText("For Codex")).toBeTruthy(); expect(screen.getByText("For Claude Code")).toBeTruthy(); expect(screen.getByText("For Grok Build")).toBeTruthy();
});

it("keeps zero, unknown, non-finite, stale, future, failed and unsupported observations distinct", () => {
  expect(quotaPresentation(window("zero", 0), now)).toEqual({ state: Quota.Observed, percent: 0 });
  for (const value of [undefined, NaN, Infinity, -1, 1.1]) expect(quotaPresentation(window("invalid", value), now)).toEqual({ state: Quota.Unknown, percent: undefined });
  expect(quotaPresentation({ ...window("old", .68), observedAt: "2026-09-30T01:54:59Z" }, now).state).toBe(Quota.Stale);
  expect(quotaPresentation({ ...window("future", .68), observedAt: "2026-09-30T02:01:00Z" }, now)).toEqual({ state: Quota.Stale, percent: undefined });
  expect(quotaPresentation({ ...window("missing", .68), observedAt: "invalid" }, now).percent).toBeUndefined();
  expect(quotaPresentation(window("failed", .68, Quota.Failed), now)).toEqual({ state: Quota.Failed, percent: 68 });
  expect(quotaPresentation(window("unsupported", .68, Quota.Unsupported), now).percent).toBeUndefined();
  const first = row("chatgpt", [window("zero", 0), { ...window("past reset", .68), resetAt: "2026-09-30T01:58:00Z" }]);
  render(view([first]));
  expect(screen.getByText("0% remaining")).toBeTruthy();
  expect(screen.getByText(/Elapsed; recovery unconfirmed/)).toBeTruthy();
  expect(screen.getByText(/Stale · Observed/)).toBeTruthy();
  expect(screen.queryByText(/Recovered/)).toBeNull();
});

it("retains last successful observations and time after a failed refresh", async () => {
  const first = row("chatgpt", [window("five-hour", .68)]);
  const refresh = vi.fn(async () => { throw new Error("fixture refresh failure"); });
  function Fixture() {
    const [failed, setFailed] = useState(false);
    return view([{ ...first, refresh: () => { void refresh().catch(() => setFailed(true)); }, refreshOperation: failed ? { state: Operation.Failed, message: "Quota refresh failed; last successful observations retained." } : undefined }]);
  }
  render(<Fixture />);
  await act(async () => fireEvent.click(screen.getByRole("button", { name: `Refresh ${first.alias}` })));
  expect(screen.getByRole("alert").textContent).toContain("last successful observations retained");
  expect(screen.getByRole("progressbar").getAttribute("value")).toBe("68");
  expect(screen.getByTitle(observedAt)).toBeTruthy();
});

it("uses one global refresh action across hidden pages/filters and shows independent account outcomes", () => {
  const connected = [row("one"), row("two"), row("three")];
  const disconnected = { ...row("disconnected"), connection: Connection.Disconnected };
  const serverAction = vi.fn();
  const first = { ...connected[0], refreshOperation: { state: Operation.Failed, message: "Authentication expired" } };
  const screenView = render(view([first], { refreshAll: serverAction, activeFilter: "Selected native provider", refreshAllOperation: { state: Operation.Ready } }));
  fireEvent.click(screen.getByRole("button", { name: "Refresh all" }));
  expect(serverAction).toHaveBeenCalledTimes(1);
  for (const account of [...connected, disconnected]) expect(account.refresh).not.toHaveBeenCalled();
  screenView.rerender(view([first, { ...connected[1], refreshOperation: { state: Operation.Busy } }], { refreshAll: serverAction }));
  expect(screen.getByRole("alert").textContent).toContain("Authentication expired");
  expect(screen.getByRole("status").textContent).toContain("In progress");
});

it("confirms the exact disconnection, exposes original uncertain/cleanup retry and preserves another account", () => {
  const first = row(), second = row("claude"), retry = vi.fn();
  const rendered = render(view([first, second]));
  fireEvent.click(screen.getByRole("button", { name: `Disconnect ${first.alias}` }));
  expect(screen.getByRole("group", { name: `Confirm disconnection of ${first.alias}` }).textContent).toContain(first.alias);
  fireEvent.click(screen.getByRole("button", { name: "Confirm disconnection" }));
  expect(first.disconnect).toHaveBeenCalledTimes(1); expect(second.disconnect).not.toHaveBeenCalled();
  for (const state of [Operation.Uncertain, Operation.CleanupPending]) {
    rendered.rerender(view([{ ...first, disconnectOperation: { state, retry } }, second]));
    expect((screen.getByRole("button", { name: `Disconnect ${first.alias}` }) as HTMLButtonElement).disabled).toBe(true);
    fireEvent.click(screen.getByRole("button", { name: "Retry original disconnection" }));
  }
  expect(retry).toHaveBeenCalledTimes(2); expect(first.disconnect).toHaveBeenCalledTimes(1);
});

it("keeps the ellipsis keyboard accessible and contains Escape before the Settings dialog", () => {
  const first = row(), outerEscape = vi.fn();
  render(<div onKeyDown={outerEscape}>{view([first])}</div>);
  const opener = screen.getByRole("button", { name: `More actions for ${first.alias}` });
  opener.focus(); fireEvent.click(opener);
  expect(opener.getAttribute("aria-expanded")).toBe("true");
  const edit = screen.getByRole("button", { name: "Edit preferences" }); edit.focus();
  fireEvent.keyDown(edit, { key: "Escape" });
  expect(opener.getAttribute("aria-expanded")).toBe("false"); expect(document.activeElement).toBe(opener); expect(outerEscape).not.toHaveBeenCalled();
  fireEvent.click(opener); fireEvent.click(screen.getByRole("button", { name: "Edit preferences" })); expect(first.edit).toHaveBeenCalledTimes(1);
  fireEvent.click(opener); fireEvent.click(screen.getByRole("button", { name: "Delete account" })); expect(first.delete).toHaveBeenCalledTimes(1);
});

it.each([Read.Loading, Read.Failed, Read.PermissionDenied, Read.AuthenticationExpired, Read.Unsupported])("keeps %s distinct from a successful empty read", (state) => {
  render(view([], { state, retryRead: vi.fn() }));
  expect(screen.queryByRole("heading", { name: "No subscriptions yet" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Retry subscription read" }) !== null).toBe(![Read.Loading, Read.Unsupported].includes(state));
});

it("keeps retained data visible on read failure, filter clearing outside collapsed Advanced and all production actions disabled", () => {
  const clearFilter = vi.fn(), first = { ...row(), brand: undefined, maskedIdentity: undefined, refresh: undefined, disconnect: undefined };
  render(view([first], { state: Read.Failed, activeFilter: "Configured provider", clearFilter }));
  expect(screen.getByText("Showing the last successfully loaded subscriptions.")).toBeTruthy();
  expect(screen.getByRole("article", { name: first.alias }).querySelector("img")).toBeNull();
  expect((screen.getByText("Advanced settings").closest("details") as HTMLDetailsElement).open).toBe(false);
  fireEvent.click(screen.getByRole("button", { name: "Clear provider filter" })); expect(clearFilter).toHaveBeenCalledTimes(1);
  for (const name of ["Refresh all", `Refresh ${first.alias}`, `Disconnect ${first.alias}`, "ChatGPT · Coming soon", "Claude · Coming soon", "Grok · Coming soon"]) expect((screen.getByRole("button", { name }) as HTMLButtonElement).disabled).toBe(true);
  expect(screen.queryByRole("navigation", { name: "Account pages" })).toBeNull();
});


it("expires visible observations without requests and stops its one-shot timer when inactive", () => {
  vi.useFakeTimers(); vi.setSystemTime(now);
  const first = row("chatgpt", [window("five-hour", .68)]);
  const rendered = render(view([first], { now: undefined }));
  try {
    expect(screen.getByText(/Observed · Observed/)).toBeTruthy();
    act(() => vi.advanceTimersByTime(4 * 60 * 1000 + 1));
    expect(screen.getByText(/Stale · Observed/)).toBeTruthy();
    expect(first.refresh).not.toHaveBeenCalled();
    rendered.rerender(view([first], { now: undefined, active: false }));
    expect(vi.getTimerCount()).toBe(0);
  } finally { rendered.unmount(); vi.useRealTimers(); }
});


it.each([Operation.Uncertain, Operation.CleanupPending, Operation.Failed])("blocks an original %s retry while the sibling account operation is busy", (state) => {
  const first = row(), retry = vi.fn();
  const rendered = render(view([first]));
  for (const owner of ["refresh", "disconnection"] as const) {
    const retained = { state, retry }, busy = { state: Operation.Busy };
    const pending = owner === "refresh" ? { refreshOperation: retained, disconnectOperation: busy } : { refreshOperation: busy, disconnectOperation: retained };
    rendered.rerender(view([{ ...first, ...pending }]));
    const button = screen.getByRole("button", { name: `Retry original ${owner}` }) as HTMLButtonElement;
    expect(button.disabled).toBe(true);
    fireEvent.click(button);
    expect(retry).toHaveBeenCalledTimes(owner === "refresh" ? 0 : 1);
    const settled = owner === "refresh" ? { refreshOperation: retained } : { disconnectOperation: retained };
    rendered.rerender(view([{ ...first, ...settled }]));
    const available = screen.getByRole("button", { name: `Retry original ${owner}` }) as HTMLButtonElement;
    expect(available.disabled).toBe(false);
    fireEvent.click(available);
  }
  expect(retry).toHaveBeenCalledTimes(2);
  expect(first.refresh).not.toHaveBeenCalled();
  expect(first.disconnect).not.toHaveBeenCalled();
});


it.each([0, 1, 2, 3])("renders each of %i quota windows once when account details are open", (count) => {
  const first = row("chatgpt", Array.from({ length: count }, (_, index) => window(`window-${index}`, .5)));
  render(view([first]));
  expect(screen.queryAllByRole("progressbar")).toHaveLength(Math.min(2, count));
  fireEvent.click(screen.getByRole("button", { name: `More actions for ${first.alias}` }));
  fireEvent.click(screen.getByRole("button", { name: "Account details" }));
  expect(screen.queryAllByRole("progressbar", { hidden: true })).toHaveLength(count);
  for (const quota of first.windows) expect(screen.getAllByLabelText(`${quota.id} remaining`)).toHaveLength(1);
  fireEvent.click(screen.getByRole("button", { name: "Close Account details" }));
  expect(screen.queryAllByRole("progressbar")).toHaveLength(Math.min(2, count));
  if (count > 2) {
    fireEvent.click(screen.getByRole("button", { name: `View all ${count} quota windows` }));
    expect(screen.queryAllByRole("progressbar", { hidden: true })).toHaveLength(count);
    for (const quota of first.windows) expect(screen.getAllByLabelText(`${quota.id} remaining`)).toHaveLength(1);
  }
});


it.each([Quota.Stale, Quota.Failed])("updates reset warnings for retained %s windows with independent business and shared presentation clocks", (state) => {
  vi.useFakeTimers(); vi.setSystemTime(now);
  const first = row("chatgpt", [
    { ...window("first-reset", .68, state), resetAt: new Date(now + 60_000).toISOString() },
    { ...window("second-reset", .82, state), resetAt: new Date(now + 120_000).toISOString() },
  ]);
  const rendered = render(view([first], { now: undefined }));
  try {
    expect(screen.queryByText(/Elapsed; recovery unconfirmed/)).toBeNull();
    expect(screen.getByText("Resets in 1 minute")).toBeTruthy();
    expect(screen.getByText("Resets in 2 minutes")).toBeTruthy();
    expect(vi.getTimerCount()).toBe(2);
    act(() => vi.advanceTimersByTime(60_000));
    expect(screen.getAllByText(/Elapsed; recovery unconfirmed/)).toHaveLength(1);
    expect(vi.getTimerCount()).toBe(2);
    rendered.rerender(view([first], { now: undefined, active: false }));
    expect(vi.getTimerCount()).toBe(0);
    rendered.rerender(view([first], { now: undefined }));
    expect(vi.getTimerCount()).toBe(2);
    act(() => vi.advanceTimersByTime(60_000));
    expect(screen.getAllByText(/Elapsed; recovery unconfirmed/)).toHaveLength(2);
    // Only the shared label clock remains; the business reset clock is exhausted.
    expect(vi.getTimerCount()).toBe(1);
    expect(screen.getAllByText(state === Quota.Stale ? /Stale · Observed/ : /Observation failed · Observed/)).toHaveLength(2);
    expect(first.refresh).not.toHaveBeenCalled();
    expect(first.disconnect).not.toHaveBeenCalled();
  } finally { rendered.unmount(); vi.useRealTimers(); }
});

it.each([
  [0, "var(--danger-text)"],
  [25, "color-mix(in srgb, var(--danger-text) 50%, var(--warning-text) 50%)"],
  [49, "color-mix(in srgb, var(--danger-text) 2%, var(--warning-text) 98%)"],
  [50, "var(--warning-text)"],
  [51, "color-mix(in srgb, var(--warning-text) 98%, var(--success-text) 2%)"],
  [67, "color-mix(in srgb, var(--warning-text) 66%, var(--success-text) 34%)"],
  [75, "color-mix(in srgb, var(--warning-text) 50%, var(--success-text) 50%)"],
  [100, "var(--success-text)"],
] as const)("uses semantic interpolation for displayed quota %i", (percent, color) => {
  render(view([row("chatgpt", [window("primary", percent / 100), window("extra", percent / 100, Quota.Failed), window("additional", percent / 100, Quota.Stale)])]));
  fireEvent.click(screen.getByRole("button", { name: "View all 3 quota windows" }));
  const bars = [...document.querySelectorAll("progress")];
  expect(bars).toHaveLength(3);
  for (const bar of bars) {
    expect(bar.getAttribute("value")).toBe(String(percent));
    expect(bar.style.getPropertyValue("--quota-fill")).toBe(color);
  }
  expect(screen.getByText(/Observation failed/)).toBeTruthy();
  expect(screen.getByText(/Stale/)).toBeTruthy();
});

it("colors equivalent rounded percentages identically without changing quota provenance", () => {
  const first = quotaPresentation(window("first", .6651), now);
  const second = quotaPresentation(window("second", .6749, Quota.Failed), now);
  expect(first.percent).toBe(67);
  expect(second.percent).toBe(67);
  expect(quotaColor(first.percent!)).toBe(quotaColor(second.percent!));
  expect(first.state).not.toBe(second.state);
});

it("retains the exact metadata projection after payload eviction, updates valid revisions and closes on confirmed removal", () => {
  const first = { ...row("chatgpt", [window("first", .1), window("second", .2), window("third", .3)]), revision: 4n };
  const manage = vi.fn();
  const rendered = render(view([first], { accountIds: [first.id], manageDetails: manage }));
  const opener = screen.getByRole("button", { name: "View all 3 quota windows" });
  fireEvent.click(opener);
  const dialog = screen.getByRole("dialog", { name: "Account details" });
  expect(dialog.getAttribute("data-size")).toBe("form");
  expect(within(dialog).getByText("third")).toBeTruthy();
  expect(within(dialog).queryByText("first")).toBeNull();
  rendered.rerender(view([], { accountIds: [first.id], manageDetails: manage }));
  expect(screen.getByRole("dialog")).toBe(dialog);
  expect(within(dialog).getByText("third")).toBeTruthy();
  expect(screen.getByRole("button", { name: "Manage metadata" }).matches(":disabled")).toBe(false);
  rendered.rerender(view([{ ...first, alias: "Updated exact account", enabled: false, revision: 5n }], { accountIds: [first.id], manageDetails: manage }));
  expect(within(dialog).getByText("Updated exact account · ChatGPT")).toBeTruthy();
  expect(within(dialog).getByText("Disabled")).toBeTruthy();
  rendered.rerender(view([{ ...first, alias: "Stale account", revision: 3n }], { accountIds: [first.id], manageDetails: manage }));
  expect(within(dialog).queryByText("Stale account · ChatGPT")).toBeNull();
  rendered.rerender(view([], { accountIds: [], manageDetails: manage }));
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(manage).not.toHaveBeenCalled();
  for (const action of [first.refresh, first.disconnect, first.details, first.edit, first.delete]) expect(action).not.toHaveBeenCalled();
});

it.each(["X", "Escape"])("details %s dismissal preserves inventory/disclosure and returns the exact additional-window opener", async method => {
  const first = row("chatgpt", [window("one", .1), window("two", .2), window("three", .3)]);
  render(view([first]));
  const inventory = screen.getByRole("article", { name: first.alias });
  const advanced = screen.getByText("Advanced settings").closest("details")!;
  fireEvent.click(advanced.querySelector("summary")!);
  const opener = screen.getByRole("button", { name: "View all 3 quota windows" });
  fireEvent.click(opener);
  if (method === "X") fireEvent.click(screen.getByRole("button", { name: "Close Account details" }));
  else fireEvent(screen.getByRole("dialog"), new Event("cancel", { cancelable: true }));
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(screen.getByRole("article", { name: first.alias })).toBe(inventory);
  expect(advanced.open).toBe(true);
  expect(document.activeElement).toBe(opener);
  for (const action of [first.refresh, first.disconnect, first.details, first.edit, first.delete]) expect(action).not.toHaveBeenCalled();
});

it("hands metadata to the original controller once after closing details without retaining row callbacks", () => {
  const first = row(), manage = vi.fn();
  const rendered = render(view([first], { accountIds: [first.id], manageDetails: manage }));
  const opener = screen.getByRole("button", { name: `More actions for ${first.alias}` });
  fireEvent.click(opener); fireEvent.click(screen.getByRole("button", { name: "Account details" }));
  rendered.rerender(view([], { accountIds: [first.id], manageDetails: manage }));
  fireEvent.click(screen.getByRole("button", { name: "Manage metadata" }));
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(manage).toHaveBeenCalledTimes(1);
  const [snapshot, originalOpener] = manage.mock.calls[0];
  expect(snapshot.id).toBe(first.id); expect(snapshot.details).toBeUndefined(); expect(snapshot.refresh).toBeUndefined(); expect(originalOpener).toBe(opener);
  expect(first.details).not.toHaveBeenCalled();
});

it.each(["unavailable", "cleanup"])("blocks metadata handoff when %s changes without performing work on departure", reason => {
  const first = row(), manage = vi.fn();
  const rendered = render(view([first], { manageDetails: manage }));
  fireEvent.click(screen.getByRole("button", { name: `More actions for ${first.alias}` }));
  fireEvent.click(screen.getByRole("button", { name: "Account details" }));
  rendered.rerender(view([{ ...first, metadataAvailable: reason !== "unavailable" }], { manageDetails: manage, actionsBlocked: reason === "cleanup" }));
  const button = screen.getByRole("button", { name: "Manage metadata" });
  expect(button.matches(":disabled")).toBe(true); fireEvent.click(button);
  rendered.rerender(view([first], { manageDetails: manage, active: false }));
  expect(screen.queryByRole("dialog")).toBeNull(); expect(manage).not.toHaveBeenCalled();
});
