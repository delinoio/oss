import { create } from "@bufbuild/protobuf";
import { fireEvent, render, screen, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { AccountingUnitKind, GetUsageSummaryResponseSchema, UsageAccountingProfile, UsageCostState, UsageTimeGranularity } from "@delinoio/delidev-api-client";
import { GrokAccounting } from "./grok-accounting";

it("shows distinct exact Grok totals in summary, day, model and session views without pricing", () => {
  const grok = { kind: AccountingUnitKind.GROK_CLOSED_INPUT, units: 1, knownTotal: "18446744073709551615", measuredUnits: 1, actualCost: UsageCostState.UNAVAILABLE, estimatedCost: UsageCostState.UNAVAILABLE };
  const totals = { responses: 1, total: { knownTotal: "20", measuredResponses: 1 }, accounting: [{ kind: AccountingUnitKind.CODEX_RESPONSE, units: 1, knownTotal: "20", measuredUnits: 1 }, grok] };
  const data = create(GetUsageSummaryResponseSchema, {
    accountingProfile: UsageAccountingProfile.NATIVE_UNITS_V1, totals,
    groups: [{ sessionId: "original-session", sessionName: "Original session", accountId: "original-account", accountName: "Original account", modelId: "original-model", modelName: "Original model", providerId: "original-provider", totals }],
    analytics: { granularity: UsageTimeGranularity.DAY, timeZone: "UTC", days: [{ fromUnixMs: 1788220800000n, untilUnixMs: 1788307200000n, totals }], models: [{ modelId: "original-model", modelName: "Original model", providerId: "original-provider", totals }] },
  });
  const open = vi.fn(); render(<GrokAccounting data={data} open={open} />);
  expect(screen.getByRole("heading", { name: "Verified Grok closed inputs" })).toBeTruthy();
  expect(screen.getAllByText(BigInt(grok.knownTotal).toLocaleString())).toHaveLength(3);
  expect(screen.getByText(/1 closed inputs/).textContent).toContain(BigInt(grok.knownTotal).toLocaleString());
  expect(screen.queryByText("20")).toBeNull();
  expect(screen.getByText(/estimated-budget contribution are unavailable/)).toBeTruthy();
  const tables = screen.getAllByRole("table"); expect(tables).toHaveLength(3);
  expect(within(tables[0]).getByText("original-account")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Original session" })); expect(open).toHaveBeenCalledWith("original-session");
});

it("distinguishes measured zero, missing units and an unnegotiated older server", () => {
  const data = create(GetUsageSummaryResponseSchema, { accountingProfile: UsageAccountingProfile.NATIVE_UNITS_V1 });
  const view = render(<GrokAccounting data={data} open={() => {}} />);
  expect(screen.getByText(/No verified Grok closed inputs/)).toBeTruthy();
  data.totals = create(GetUsageSummaryResponseSchema, { totals: { accounting: [{ kind: AccountingUnitKind.GROK_CLOSED_INPUT, units: 1, knownTotal: "0", measuredUnits: 1 }] } }).totals;
  view.rerender(<GrokAccounting data={data} open={() => {}} />);
  expect(screen.getByText("1 closed inputs · 0 known total tokens")).toBeTruthy();
  data.accountingProfile = UsageAccountingProfile.UNSPECIFIED;
  view.rerender(<GrokAccounting data={data} open={() => {}} />);
  expect(screen.getByRole("status").textContent).toContain("unavailable from this server version");
  expect(screen.queryByText(/1 closed inputs/)).toBeNull();
});

it("shows both localized endpoints of a clipped daily interval with an exclusive end", () => {
  const totals = { accounting: [{ kind: AccountingUnitKind.GROK_CLOSED_INPUT, units: 1, knownTotal: "16", measuredUnits: 1 }] };
  const from = new Date("2026-09-01T03:04:05Z");
  const until = new Date("2026-09-01T09:10:11Z");
  const data = create(GetUsageSummaryResponseSchema, {
    accountingProfile: UsageAccountingProfile.NATIVE_UNITS_V1, totals,
    groups: [{ sessionId: "original-session", totals }],
    analytics: { granularity: UsageTimeGranularity.DAY, timeZone: "Asia/Seoul", days: [{ fromUnixMs: BigInt(from.getTime()), untilUnixMs: BigInt(until.getTime()), totals }] },
  });
  render(<GrokAccounting data={data} open={() => {}} />);
  const daily = screen.getByRole("table", { name: "Daily verified Grok inputs (Asia/Seoul)" });
  expect(within(daily).getByRole("columnheader", { name: "From" })).toBeTruthy();
  expect(within(daily).getByRole("columnheader", { name: "Until" })).toBeTruthy();
  const endpoints = daily.querySelectorAll("time");
  expect(endpoints).toHaveLength(2);
  expect(endpoints[0].dateTime).toBe(from.toISOString());
  expect(endpoints[1].dateTime).toBe(until.toISOString());
  expect(endpoints[0].textContent).toMatch(/12:04:05/);
  expect(endpoints[1].textContent).toMatch(/(?:0?6|18):10:11/);
  expect(within(daily).getByText("(exclusive)")).toBeTruthy();
});
