// SPDX-License-Identifier: Apache-2.0
import { StrictMode, useEffect, type ReactNode } from "react";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { AccountingUnitKind, EntityKind, GetUsageSummaryResponseSchema, ResourceSchema, UsageAccountingProfile, UsageCostState, UsageService, newRequestId, type GetUsageSummaryRequest } from "@delinoio/delidev-api-client";
import { i18n, formatNumber, formatTimestamp } from "./localization";
import { SettingsTasks, SettingsTaskBackground } from "./settings-task";
import { ApiEntryRow } from "./api-entry-row";
import { encode } from "./documents";

function fixture() {
  const id = newRequestId();
  const row = create(ResourceSchema, { id, kind: EntityKind.ACCOUNT, schemaVersion: 1, revision: 1n, documentJson: encode({ alias: "OpenRouter", type: "api", provider_id: newRequestId(), enabled: true, health: "unverified", connection: { id: newRequestId() }, quota: [] }) });
  const data = create(GetUsageSummaryResponseSchema, { fromUnixMs: 1780000000123n, untilUnixMs: 1782592000123n, accountingProfile: UsageAccountingProfile.NATIVE_UNITS_V1, totals: { responses: 126, total: { knownTotal: "18446744073709551614", measuredResponses: 124, unavailableResponses: 2 } }, estimatedCost: UsageCostState.KNOWN_SUBTOTAL, estimates: { currencies: [{ currency: "USD", knownAmount: "12.48", partialResponses: 2 }, { currency: "KRW", knownAmount: "0", completeResponses: 1 }] }, acceptedExecutionsWithoutResponse: 3 });
  const read = vi.fn((_request: GetUsageSummaryRequest) => Promise.resolve(data));
  const transport = createRouterTransport(router => router.service(UsageService, { getUsageSummary: read }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  const callbacks = { manage: vi.fn(), edit: vi.fn(), remove: vi.fn(), openUsage: vi.fn() };
  const view = (active = true, verification?: ReactNode) => <TransportProvider transport={transport}><QueryClientProvider client={client}><SettingsTasks><SettingsTaskBackground><ApiEntryRow row={row} provider={{ displayName: "OpenRouter", enabled: true }} active={active} verification={verification} {...callbacks} /></SettingsTaskBackground></SettingsTasks></QueryClientProvider></TransportProvider>;

  return { id, row, data, read, client, callbacks, view };
}
function emptyFixture() {
  const f = fixture();
  Object.assign(f.data, create(GetUsageSummaryResponseSchema, {
    fromUnixMs: f.data.fromUnixMs, untilUnixMs: f.data.untilUnixMs,
    accountingProfile: UsageAccountingProfile.NATIVE_UNITS_V1,
    totals: { total: {} }, estimatedCost: UsageCostState.UNAVAILABLE,
    nativeAccounting: [
      { totals: { kind: AccountingUnitKind.CLAUDE_MAIN_LOOP_INPUT } },
      { totals: { kind: AccountingUnitKind.OPENCODE_STEP } },
    ],
  }));
  return f;
}
it.each(["ready", "failed"])("shows an empty usage interval as zero independently of account health: %s", async health => {
  const f = emptyFixture();
  f.row.documentJson = encode({ alias: "OpenRouter", type: "api", enabled: true, health, connection: { id: newRequestId() }, quota: [] });
  render(f.view());
  await screen.findByText("$0", { selector: "strong" });
  expect(screen.getByText("0", { selector: "strong" })).toBeTruthy();
  expect(screen.getAllByText("No usage in this period")).toHaveLength(2);
  expect(screen.queryByText("No historical estimate")).toBeNull();
  expect(screen.queryByText("No exact response records")).toBeNull();
  expect(screen.getByText("Not reported", { selector: "strong" })).toBeTruthy();
  expect(f.read).toHaveBeenCalledTimes(1);
  for (const callback of Object.values(f.callbacks)) expect(callback).not.toHaveBeenCalled();
});
it.each([
  "missing totals", "older accounting profile", "execution without usage", "compaction without usage",
  "response without tokens", "response without prices", "Grok input without tokens", "Claude input without total", "OpenCode step without total", "missing native totals",
])("keeps incomplete usage unavailable: %s", async scenario => {
  const f = emptyFixture();
  switch (scenario) {
    case "missing totals": f.data.totals = undefined; break;
    case "older accounting profile": f.data.accountingProfile = UsageAccountingProfile.UNSPECIFIED; break;
    case "execution without usage": f.data.acceptedExecutionsWithoutResponse = 1; break;
    case "compaction without usage": f.data.acceptedCompactionsWithoutResponse = 1; break;
    case "response without tokens": f.data.totals!.responses = 1; f.data.totals!.total!.unavailableResponses = 1; break;
    case "response without prices": f.data.totals!.responses = 1; f.data.totals!.total!.knownTotal = "25"; f.data.totals!.total!.measuredResponses = 1; break;
    case "Grok input without tokens": f.data.totals!.accounting = create(GetUsageSummaryResponseSchema, { totals: { accounting: [{ kind: AccountingUnitKind.GROK_CLOSED_INPUT, units: 1, unavailableUnits: 1 }] } }).totals!.accounting; break;
    case "Claude input without total": f.data.nativeAccounting[0].totals!.units = 1; break;
    case "OpenCode step without total": f.data.nativeAccounting[1].totals!.units = 1; break;
    case "missing native totals": f.data.nativeAccounting[0].totals = undefined; break;
  }
  render(f.view());
  await waitFor(() => expect(screen.queryByText("Loading usage…")).toBeNull());
  expect(screen.getAllByText("Unavailable", { selector: "strong" }).length).toBeGreaterThan(0);
  expect(screen.queryByText("$0", { selector: "strong" })).toBeNull();
  expect(screen.queryByText("0", { selector: "strong" })).toBeNull();
  expect(screen.queryByText("No usage in this period")).toBeNull();
});
it("does not display zero while the initial usage read is pending", async () => {
  const f = emptyFixture();
  f.read.mockImplementation(() => new Promise(() => {}));
  render(f.view());
  await screen.findByText("Loading usage…");
  expect(screen.queryByText("$0", { selector: "strong" })).toBeNull();
  expect(screen.queryByText("0", { selector: "strong" })).toBeNull();
  expect(screen.queryByText("No usage in this period")).toBeNull();
});
it("retains stale empty usage after a failed refresh", async () => {
  const f = emptyFixture(); render(f.view());
  await screen.findByText("$0", { selector: "strong" });
  f.read.mockRejectedValueOnce(new ConnectError("Read denied", Code.PermissionDenied));
  await f.client.refetchQueries();
  await screen.findByText(/Showing stale usage/);
  expect(screen.getByText("$0", { selector: "strong" })).toBeTruthy();
  expect(screen.getByText("0", { selector: "strong" })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Retry usage for OpenRouter" }));
  await waitFor(() => expect(screen.queryByText(/Showing stale usage/)).toBeNull());
  expect(f.read).toHaveBeenCalledTimes(3);
});
it("keeps measured zero tokens and zero estimates distinct from an empty interval", async () => {
  const f = emptyFixture();
  f.data.totals!.responses = 1;
  f.data.totals!.total!.knownTotal = "0";
  f.data.totals!.total!.measuredResponses = 1;
  f.data.estimatedCost = UsageCostState.KNOWN_SUBTOTAL;
  f.data.estimates = create(GetUsageSummaryResponseSchema, { estimates: { currencies: [{ currency: "USD", knownAmount: "0", completeResponses: 1 }] } }).estimates;
  render(f.view()); await screen.findAllByText("USD 0");
  expect(screen.getByText("0", { selector: "strong" })).toBeTruthy();
  expect(screen.getByText("1 observed responses")).toBeTruthy();
  expect(screen.queryByText("$0", { selector: "strong" })).toBeNull();
  expect(screen.queryByText("No usage in this period")).toBeNull();
});
it("reads one exact account and preserves decimal precision, currencies and the original navigation range", async () => {
  const f = fixture(); const view = render(f.view());
  await screen.findAllByText("USD 12.48");
  expect(screen.getAllByText("KRW 0").length).toBeGreaterThan(0);
  expect(screen.getByText(BigInt("18446744073709551614").toLocaleString())).toBeTruthy();
  expect(screen.getByText("126 observed responses")).toBeTruthy();
  expect(f.read).toHaveBeenCalledTimes(1);
  expect(f.read.mock.calls[0][0]).toMatchObject({ accountId: f.id, fromUnixMs: 0n, untilUnixMs: 0n, accountingProfile: UsageAccountingProfile.NATIVE_UNITS_V1, granularity: 0 });
  fireEvent.click(screen.getByRole("button", { name: "View usage" }));
  expect(f.callbacks.openUsage).toHaveBeenCalledWith({ key: expect.any(String), accountId: f.id, fromUnixMs: f.data.fromUnixMs, untilUnixMs: f.data.untilUnixMs });
  view.rerender(f.view(false)); view.rerender(f.view());
  expect(f.read).toHaveBeenCalledTimes(1);
  expect(f.callbacks.manage).not.toHaveBeenCalled();
});
it("keeps metadata actions in a keyboard-owned disclosure and restores focus on Escape", async () => {
  const f = fixture(); render(f.view()); await screen.findAllByText("USD 12.48");
  expect(screen.queryByRole("button", { name: "Delete entry" })).toBeNull();
  const opener = screen.getByRole("button", { name: "More actions for OpenRouter" });
  fireEvent.click(opener);
  const actions = screen.getByRole("group", { name: "Actions for OpenRouter" });
  fireEvent.keyDown(within(actions).getByRole("button", { name: "Delete entry" }), { key: "Escape" });
  expect(document.activeElement).toBe(opener); expect(screen.queryByRole("group", { name: "Actions for OpenRouter" })).toBeNull();
  fireEvent.click(opener); fireEvent.click(screen.getByRole("button", { name: "Edit preferences" }));
  expect(f.callbacks.edit).toHaveBeenCalledTimes(1);
  fireEvent.click(opener); fireEvent.click(screen.getByRole("button", { name: "Details" }));
  const details = screen.getByRole("dialog", { name: "Details" });
  expect(within(details).getByText(/3 accepted executions/)).toBeTruthy();
  fireEvent(details,new Event("cancel",{bubbles:true,cancelable:true}));
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull()); expect(document.activeElement).toBe(opener);
  expect(f.read).toHaveBeenCalledTimes(1);
});
it("separates native totals and estimates without summing, and keeps an absent Claude total unavailable", async () => {
  const f = fixture();
  f.data.nativeAccounting = create(GetUsageSummaryResponseSchema, { nativeAccounting: [
    { totals: { kind: AccountingUnitKind.CLAUDE_MAIN_LOOP_INPUT, units: 4, input: { knownTotal: "100", measuredUnits: 4 }, output: { knownTotal: "20", measuredUnits: 4 }, currencies: [{ currency: "USD", knownAmount: "5.01" }] } },
    { totals: { kind: AccountingUnitKind.OPENCODE_STEP, units: 2, total: { knownTotal: "0", measuredUnits: 1, unavailableUnits: 1 }, currencies: [{ currency: "USD", knownAmount: "0" }] } },
  ] }).nativeAccounting;
  render(f.view()); await screen.findByText("USD 5.01");
  expect(screen.getAllByText("Claude main-loop inputs").length).toBeGreaterThan(0);
  expect(screen.getByText("0", { selector: "strong" })).toBeTruthy();
  expect(screen.getAllByText("Unavailable").length).toBeGreaterThan(0);
  expect(screen.queryByText("120")).toBeNull(); expect(screen.queryByText("USD 17.49")).toBeNull();
});
it("preserves stale results on refresh failure and retries only the usage read", async () => {
  const f = fixture(); render(f.view()); await screen.findAllByText("USD 12.48");
  f.read.mockRejectedValueOnce(new ConnectError("Read denied", Code.PermissionDenied));
  await f.client.refetchQueries();
  await screen.findByText(/Showing stale usage/);
  expect(screen.getAllByText("USD 12.48").length).toBeGreaterThan(0);
  fireEvent.click(screen.getByRole("button", { name: "Retry usage for OpenRouter" }));
  await waitFor(() => expect(screen.queryByText(/Showing stale usage/)).toBeNull());
  expect(f.read).toHaveBeenCalledTimes(3);
  expect(f.callbacks.manage).not.toHaveBeenCalled(); expect(f.callbacks.openUsage).not.toHaveBeenCalled();
});
it("does not turn missing evidence into zero, and preserves exhaustion without a quota observation", async () => {
  const f = fixture();
  f.data.totals = undefined; f.data.estimates = undefined; f.data.estimatedCost = UsageCostState.UNAVAILABLE;
  f.row.documentJson = encode({ alias: "OpenRouter", type: "api", enabled: false, health: "disconnected", confirmed_exhausted: true, quota: [] });
  render(f.view()); await waitFor(() => expect(f.read).toHaveBeenCalledTimes(1));
  expect(screen.getByText("Confirmed exhausted")).toBeTruthy();
  expect(screen.getAllByText("Unavailable").length).toBeGreaterThan(0);
  expect(screen.queryByText("0", { selector: "strong" })).toBeNull();
});
it("retains failed reads as unavailable without disabling connection management", async () => {
  const f = fixture(); f.read.mockRejectedValue(new ConnectError("Read denied", Code.PermissionDenied));
  render(f.view()); await screen.findByText(/Entry controls remain available/);
  expect(screen.queryByText("$0", { selector: "strong" })).toBeNull();
  expect(screen.queryByText("0", { selector: "strong" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Manage connection" }));
  expect(f.callbacks.manage).toHaveBeenCalledTimes(1);
});

it("keeps Strict Mode replay read-only", async () => {
  const f = fixture(); render(<StrictMode>{f.view()}</StrictMode>);
  await screen.findAllByText("USD 12.48");
  expect(f.read.mock.calls.every(([request]) => request.accountId === f.id)).toBe(true);
  for (const callback of Object.values(f.callbacks)) expect(callback).not.toHaveBeenCalled();
});

it.each([
  { state: "observed", remaining: 0, age: 0, value: "0% remaining", status: "Observed" },
  { state: "observed", remaining: 0.5, age: 360000, value: "50% remaining", status: "Stale observation" },
  { state: "failed", remaining: 0.75, age: 0, value: "75% remaining", status: "Observation failed" },
  { state: "observed", remaining: 2, age: 0, value: "Not reported", status: "Unknown" },
])("retains quota observation provenance and validates remaining fractions: $status", async ({ state, remaining, age, value, status }) => {
  const f = fixture();
  f.row.documentJson = encode({ alias: "OpenRouter", type: "api", enabled: true, health: "unverified", quota: [{ id: "Daily", state, remaining, observed_at: new Date(Date.now() - age).toISOString() }] });
  render(f.view()); await screen.findAllByText("USD 12.48");
  expect(screen.getAllByText(value).length).toBeGreaterThan(0);
  expect(screen.getAllByText(status).length).toBeGreaterThan(0);
  expect(f.read).toHaveBeenCalledTimes(1);
  expect(f.callbacks.manage).not.toHaveBeenCalled();
});

it("updates the retained usage disclosure without extra reads, focus changes or new navigation bounds", async () => {
  const f = fixture();
  f.data.totals!.responses = 1;
  f.data.estimates!.currencies[0].knownAmount = "12345678901234567890.0000123400";
  f.row.documentJson = encode({ alias: "My original API account", type: "api", enabled: true, health: "unverified", quota: [{ id: "Original window", state: "observed", remaining: 0.5, observed_at: "2026-10-06T01:02:03.123456789+09:00", reset_at: "2026-10-07T01:02:03.123456789+09:00" }] });
  render(f.view());
  await screen.findByText("1 observed response");
  const opener=screen.getByRole("button",{name:"More actions for My original API account"});
  fireEvent.click(opener); fireEvent.click(screen.getByRole("button",{name:"Details"}));
  const dialog=screen.getByRole("dialog",{name:"Details"}), heading=within(dialog).getByRole("heading",{name:"Details"});
  await waitFor(()=>expect(document.activeElement).toBe(heading));
  await act(() => i18n.changeLanguage("ko"));
  expect(screen.getByRole("dialog",{name:"상세"})).toBe(dialog);
  expect(within(dialog).getByRole("heading",{name:"상세"})).toBe(heading);
  expect(document.activeElement).toBe(heading);
  expect(within(dialog).getByText("My original API account")).toBeTruthy();
  expect(screen.getByText("관측된 응답 1개")).toBeTruthy();
  expect(screen.getByText(formatNumber(18446744073709551614n))).toBeTruthy();
  expect(screen.getAllByText("USD 12,345,678,901,234,567,890.0000123400").length).toBeGreaterThan(0);
  expect(screen.getByText(formatTimestamp("2026-10-07T01:02:03.123456789+09:00"))).toBeTruthy();
  expect(screen.getByTitle(new Date(Number(f.data.untilUnixMs)).toISOString())).toBeTruthy();
  expect(f.read).toHaveBeenCalledTimes(1);
  for (const callback of Object.values(f.callbacks)) expect(callback).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "닫기 상세" }));
  await waitFor(()=>expect(document.activeElement).toBe(opener));
  fireEvent.click(screen.getByRole("button", { name: "사용량 보기" }));
  expect(f.callbacks.openUsage).toHaveBeenCalledWith({ key: expect.any(String), accountId: f.id, fromUnixMs: f.data.fromUnixMs, untilUnixMs: f.data.untilUnixMs });
});

it("reuses stale row evidence through dialog reopen and disposes Details on category departure",async()=>{
 const f=fixture(),mounted=render(f.view()); await screen.findAllByText("USD 12.48");
 const open=()=>{fireEvent.click(screen.getByRole("button",{name:"More actions for OpenRouter"}));fireEvent.click(screen.getByRole("button",{name:"Details"}));};
 f.read.mockRejectedValueOnce(new ConnectError("Read denied",Code.PermissionDenied)); await act(async()=>{await f.client.refetchQueries();});
 open(); const dialog=screen.getByRole("dialog",{name:"Details"}); expect(within(dialog).getByText(/Showing stale usage/)).toBeTruthy(); expect(within(dialog).getByText("USD 12.48")).toBeTruthy(); expect(within(dialog).getByText(f.id)).toBeTruthy();
 fireEvent.click(within(dialog).getByRole("button",{name:"Close Details"})); await waitFor(()=>expect(screen.queryByRole("dialog")).toBeNull()); open(); expect(f.read).toHaveBeenCalledTimes(2);
 mounted.rerender(f.view(false)); await waitFor(()=>expect(screen.queryByRole("dialog")).toBeNull()); mounted.rerender(f.view()); expect(screen.queryByRole("dialog")).toBeNull(); expect(f.read).toHaveBeenCalledTimes(2);
 for(const callback of Object.values(f.callbacks)) expect(callback).not.toHaveBeenCalled();
});
it("keeps unknown schemas read-only while allowing their full Details identity",async()=>{
 const f=fixture(); f.row.schemaVersion=4; render(f.view()); const opener=screen.getByRole("button",{name:"More actions for Unnamed"}); fireEvent.click(opener);
 expect(screen.getByRole("button",{name:"Edit preferences"})).toHaveProperty("disabled",true); expect(screen.getByRole("button",{name:"Delete entry"})).toHaveProperty("disabled",true); expect(screen.getByRole("button",{name:"Manage connection"})).toHaveProperty("disabled",true); expect(screen.getByRole("button",{name:"View usage"})).toHaveProperty("disabled",true);
 fireEvent.click(screen.getByRole("button",{name:"Details"})); expect(within(screen.getByRole("dialog",{name:"Details"})).getByText(f.id)).toBeTruthy(); expect(f.read).not.toHaveBeenCalled();
});

it("places the retained verification owner after the full summary without remounting it", async () => {
  const f = fixture(), mounted = vi.fn(), disposed = vi.fn();
  function RetainedVerification() { useEffect(() => { mounted(); return disposed; }, []); return <section aria-label="Retained verification">Complete verification evidence</section>; }
  const verification = <RetainedVerification />;
  const view = render(f.view(true, verification));
  const section = screen.getByRole("region", { name: "Retained verification" });
  const summary = window.document.querySelector(".api-usage-main")!;
  expect(summary.contains(section)).toBe(false);
  expect(summary.nextElementSibling).toBe(section);
  expect(mounted).toHaveBeenCalledTimes(1);
  view.rerender(f.view(false, verification));
  expect(screen.getByRole("region", { name: "Retained verification" })).toBe(section);
  expect(mounted).toHaveBeenCalledTimes(1);
  expect(disposed).not.toHaveBeenCalled();
});

it.each([
  [0, "var(--danger-text)"],
  [25, "color-mix(in srgb, var(--danger-text) 50%, var(--warning-text) 50%)"],
  [50, "var(--warning-text)"],
  [67, "color-mix(in srgb, var(--warning-text) 66%, var(--success-text) 34%)"],
  [75, "color-mix(in srgb, var(--warning-text) 50%, var(--success-text) 50%)"],
  [100, "var(--success-text)"],
] as const)("uses percentage color in compact and detailed API quota at %i", async (percent, color) => {
  const f = fixture();
  f.row.documentJson = encode({ alias: "OpenRouter", type: "api", enabled: true, health: "unverified", quota: [{ id: "Daily", state: "observed", remaining: percent / 100, observed_at: new Date().toISOString() }] });
  render(f.view()); await screen.findAllByText("USD 12.48");
  for (const bar of screen.getAllByRole("progressbar")) {
    expect(bar.getAttribute("value")).toBe(String(percent));
    expect(bar.style.getPropertyValue("--quota-fill")).toBe(color);
  }
  expect(f.read).toHaveBeenCalledTimes(1);
  expect(f.callbacks.manage).not.toHaveBeenCalled();
});

it("renders independent future reset countdowns in Details without additional usage reads", async () => {
  await i18n.changeLanguage("en");
  const f = fixture(), observed = new Date().toISOString();
  const first = new Date(Date.now() + 529200000 + 120000).toISOString(), second = new Date(Date.now() + 12000000 + 30000).toISOString();
  f.row.documentJson = encode({ alias: "Countdown fixture", type: "api", enabled: true, health: "unverified", quota: [
    { id: "weekly", state: "failed", remaining: .5, observed_at: observed, reset_at: first },
    { id: "daily", state: "stale", remaining: .25, observed_at: observed, reset_at: second },
  ] });
  render(f.view()); await waitFor(() => expect(f.read).toHaveBeenCalledTimes(1));
  fireEvent.click(screen.getByRole("button", { name: "More actions for Countdown fixture" })); fireEvent.click(screen.getByRole("button", { name: "Details" }));
  const dialog = screen.getByRole("dialog");
  expect(within(dialog).getByText("Resets in 6 days 3 hours")).toBeTruthy(); expect(within(dialog).getByText("Resets in 3 hours 20 minutes")).toBeTruthy();
  expect(within(dialog).getByTitle(first).getAttribute("datetime")).toBe(first);
  for (const time of within(dialog).getAllByTitle(observed)) expect(time.textContent).not.toMatch(/Resets/);
  await act(() => i18n.changeLanguage("ko")); expect(within(dialog).getByText("6일 3시간 뒤 리셋")).toBeTruthy();
  expect(f.read).toHaveBeenCalledTimes(1); for (const callback of Object.values(f.callbacks)) expect(callback).not.toHaveBeenCalled();
});
