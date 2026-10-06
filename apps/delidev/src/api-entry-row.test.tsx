// SPDX-License-Identifier: Apache-2.0
import { StrictMode } from "react";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { AccountingUnitKind, EntityKind, GetUsageSummaryResponseSchema, ResourceSchema, UsageAccountingProfile, UsageCostState, UsageService, newRequestId, type GetUsageSummaryRequest } from "@delinoio/delidev-api-client";
import { i18n, formatNumber, formatTimestamp } from "./localization";
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
  const view = (active = true) => <TransportProvider transport={transport}><QueryClientProvider client={client}><ApiEntryRow row={row} provider={{ displayName: "OpenRouter", enabled: true }} active={active} {...callbacks} /></QueryClientProvider></TransportProvider>;
  return { id, row, data, read, client, callbacks, view };
}
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
  expect(document.activeElement).toBe(opener); expect(screen.queryByRole("group")).toBeNull();
  fireEvent.click(opener); fireEvent.click(screen.getByRole("button", { name: "Edit preferences" }));
  expect(f.callbacks.edit).toHaveBeenCalledTimes(1);
  const details = screen.getByRole("button", { name: "Details" });
  fireEvent.click(details);
  expect(screen.getByText(/3 accepted executions/)).toBeTruthy();
  fireEvent.keyDown(screen.getByText(/3 accepted executions/), { key: "Escape" });
  expect(details.getAttribute("aria-expanded")).toBe("false"); expect(document.activeElement).toBe(details);
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
  const details = screen.getByRole("button", { name: "Details" });
  fireEvent.click(details); details.focus();
  const article = details.closest("article");
  await act(() => i18n.changeLanguage("ko"));
  expect(screen.getByRole("button", { name: "상세" })).toBe(details);
  expect(details.closest("article")).toBe(article);
  expect(details.getAttribute("aria-expanded")).toBe("true");
  expect(document.activeElement).toBe(details);
  expect(screen.getByText("My original API account")).toBeTruthy();
  expect(screen.getByText("관측된 응답 1개")).toBeTruthy();
  expect(screen.getByText(formatNumber(18446744073709551614n))).toBeTruthy();
  expect(screen.getAllByText("USD 12,345,678,901,234,567,890.0000123400").length).toBeGreaterThan(0);
  expect(screen.getByText(formatTimestamp("2026-10-07T01:02:03.123456789+09:00"))).toBeTruthy();
  expect(screen.getByText(formatTimestamp(new Date(Number(f.data.untilUnixMs)).toISOString()))).toBeTruthy();
  expect(f.read).toHaveBeenCalledTimes(1);
  for (const callback of Object.values(f.callbacks)) expect(callback).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "사용량 보기" }));
  expect(f.callbacks.openUsage).toHaveBeenCalledWith({ key: expect.any(String), accountId: f.id, fromUnixMs: f.data.fromUnixMs, untilUnixMs: f.data.untilUnixMs });
});
