// SPDX-License-Identifier: Apache-2.0
import { useState } from "react";
import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { SubscriptionBrand } from "./subscription-catalog";
import { QuotaObservationState as Quota, SubscriptionConnectionState as Connection, SubscriptionOperationState as Operation, SubscriptionReadState as Read, SubscriptionSettingsView, quotaPresentation, type SubscriptionAccountRow, type SubscriptionQuotaWindow } from "./subscription-settings";

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
  expect(within(firstRow).getAllByText(observedAt)).toHaveLength(2);
  expect(within(firstRow).getAllByText(resetAt)).toHaveLength(2);
  fireEvent.click(screen.getByRole("button", { name: `Refresh ${first.alias}` }));
  expect(first.refresh).toHaveBeenCalledTimes(1); expect(second.refresh).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Show all 3 quota windows" }));
  expect(within(firstRow).getByText("extra")).toBeTruthy();
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
  expect(screen.getByText(observedAt)).toBeTruthy();
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


it.each([1, 2, 3])("renders each of %i quota windows once when account details are open", (count) => {
  const first = row("chatgpt", Array.from({ length: count }, (_, index) => window(`window-${index}`, .5)));
  render(view([first]));
  expect(screen.getAllByRole("progressbar")).toHaveLength(Math.min(2, count));
  fireEvent.click(screen.getByRole("button", { name: `More actions for ${first.alias}` }));
  fireEvent.click(screen.getByRole("button", { name: "Account details" }));
  expect(screen.getAllByRole("progressbar")).toHaveLength(count);
  for (const quota of first.windows) expect(screen.getAllByRole("progressbar", { name: `${quota.id} remaining` })).toHaveLength(1);
  fireEvent.click(screen.getByRole("button", { name: "Close account details" }));
  expect(screen.getAllByRole("progressbar")).toHaveLength(Math.min(2, count));
  if (count > 2) {
    fireEvent.click(screen.getByRole("button", { name: `Show all ${count} quota windows` }));
    expect(screen.getAllByRole("progressbar")).toHaveLength(count);
    for (const quota of first.windows) expect(screen.getAllByRole("progressbar", { name: `${quota.id} remaining` })).toHaveLength(1);
  }
});
