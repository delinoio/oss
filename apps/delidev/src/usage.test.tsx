import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { AccountingUnitKind, EstimateTotalsSchema, PricingUsageSchema, InputPricingMode, EntityKind, GetUsageSummaryResponseSchema, ResourceSchema, ResourceService, UsageAnalyticsSchema, UsageCostState, UsageCoverage, UsageService, SystemService, UsageTimeGranularity, UsageAccountingProfile, UsageTotalsSchema, newRequestId, type GetUsageSummaryRequest } from "@delinoio/delidev-api-client";
import { Usage } from "./usage";
import { encode } from "./documents";

function fixture() {
  const ids = { session: newRequestId(), account: newRequestId(), model: newRequestId(), provider: newRequestId(), project: newRequestId() };
  const count = { knownTotal: "18446744073709551614", measuredResponses: 2, unavailableResponses: 1 };
  const totals = { responses: 3, total: count, input: { knownTotal: "0", measuredResponses: 2, unavailableResponses: 1 }, output: count, cachedInput: { knownTotal: "", measuredResponses: 0, unavailableResponses: 3 }, cacheWriteInput: { knownTotal: "", measuredResponses: 0, unavailableResponses: 3 }, reasoningOutput: { knownTotal: "0", measuredResponses: 3, unavailableResponses: 0 } };
  const data = create(GetUsageSummaryResponseSchema, { fromUnixMs: BigInt(Date.UTC(2026, 8, 1)), untilUnixMs: BigInt(Date.UTC(2026, 8, 25)), totals, coverage: UsageCoverage.OBSERVED_ROOT_RESPONSES, actualCost: UsageCostState.UNAVAILABLE, estimatedCost: UsageCostState.UNAVAILABLE, acceptedExecutionsWithoutResponse: 2, groups: [{ sessionId: ids.session, sessionName: "Retained session", accountId: ids.account, accountName: "Original account", modelId: ids.model, modelName: "Original model", providerId: ids.provider, providerName: "Original API", totals }] });
  const read = vi.fn(async (_request: GetUsageSummaryRequest) => data);
  const transport = createRouterTransport((router) => {
    router.service(UsageService, { getUsageSummary: read });
    router.service(ResourceService, { listResources: (request) => ({ resources: request.filter?.kind === EntityKind.ACCOUNT ? [create(ResourceSchema, { id: ids.account, kind: EntityKind.ACCOUNT, revision: 1n, documentJson: encode({ alias: "Original account" }) })] : [] }) });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  const open = vi.fn();
  const view = (active = true) => <TransportProvider transport={transport}><QueryClientProvider client={client}><Usage active={active} open={open} /></QueryClientProvider></TransportProvider>;
  return { ids, data, read, open, view };
}

it("shows exact known subtotals, missing fields and separate unavailable costs", async () => {
  const f = fixture(); render(f.view());
  await screen.findByText("Incomplete coverage");
  expect(screen.getAllByText(BigInt("18446744073709551614").toLocaleString()).length).toBeGreaterThan(0);
  expect(screen.getAllByText("Unavailable").length).toBeGreaterThan(0);
  expect(screen.getByText(/2 accepted executions/)).toBeTruthy();
  expect(screen.getByText("Actual API cost:").parentElement!.textContent).toContain("Unavailable");
  expect(screen.getByText("Token-price estimate:").parentElement!.textContent).toContain("Unavailable");
  expect(screen.getByText(/charts are unavailable from this server version/)).toBeTruthy();
  const table = screen.getByRole("table");
  expect(within(table).getByText("General Chat")).toBeTruthy();
  expect(within(table).getByText(f.ids.account)).toBeTruthy();
  expect(within(table).getByText("Original model")).toBeTruthy();
  fireEvent.click(within(table).getByRole("button", { name: "Retained session" }));
  expect(f.open).toHaveBeenCalledWith(f.ids.session);
  expect(f.read.mock.calls[0][0].fromUnixMs).toBe(0n);
  expect(f.read.mock.calls[0][0].accountingProfile).toBe(UsageAccountingProfile.NATIVE_UNITS_V1);
});

it("applies filters explicitly and preserves a draft across navigation", async () => {
  const f = fixture(); const view = render(f.view());
  await screen.findByText("Incomplete coverage");
  fireEvent.change(screen.getByRole("combobox", { name: "Account" }), { target: { value: f.ids.account } });
  fireEvent.click(screen.getByRole("checkbox", { name: "General Chat only" }));
  expect((screen.getByRole("combobox", { name: "Project" }) as HTMLSelectElement).disabled).toBe(true);
  expect(f.read).toHaveBeenCalledTimes(1);
  view.rerender(f.view(false)); view.rerender(f.view());
  expect((screen.getByRole("combobox", { name: "Account" }) as HTMLSelectElement).value).toBe(f.ids.account);
  fireEvent.click(screen.getByRole("button", { name: "Apply filters" }));
  await waitFor(() => expect(f.read).toHaveBeenCalledTimes(2));
  expect(f.read.mock.calls[1][0]).toMatchObject({ accountId: f.ids.account, generalChat: true, projectId: "" });
  fireEvent.change(screen.getByLabelText(/^From \(/), { target: { value: "2026-09-25T10:00" } });
  fireEvent.change(screen.getByLabelText(/^Until \(/), { target: { value: "2026-09-24T10:00" } });
  fireEvent.click(screen.getByRole("button", { name: "Apply filters" }));
  await screen.findByRole("alert"); expect(f.read).toHaveBeenCalledTimes(2);
});

it.each([false, true])("keeps native-only groups and models out of response views with mixed responses=%s", async (mixed) => {
  const f = fixture();
  const native = create(GetUsageSummaryResponseSchema, { groups: [{ sessionId: newRequestId(), sessionName: "Grok-only session", accountId: f.ids.account, providerId: f.ids.provider, modelId: newRequestId(), modelName: "Grok-only model", totals: { accounting: [{ kind: AccountingUnitKind.GROK_CLOSED_INPUT, units: 1, knownTotal: "16", measuredUnits: 1 }] } }] }).groups[0];
  f.data.accountingProfile = UsageAccountingProfile.NATIVE_UNITS_V1;
  f.data.groups[0].totals = create(UsageTotalsSchema, { responses: 1, total: { unavailableResponses: 1 }, accounting: [{ kind: AccountingUnitKind.CODEX_RESPONSE, units: 1, unavailableUnits: 1 }] });
  f.data.totals = create(UsageTotalsSchema, { responses: mixed ? 1 : 0, total: mixed ? { unavailableResponses: 1 } : undefined, accounting: [...(mixed ? f.data.groups[0].totals.accounting : []), ...native.totals!.accounting] });
  f.data.groups = mixed ? [f.data.groups[0], native] : [native];
  f.data.analytics = create(UsageAnalyticsSchema, {
    granularity: UsageTimeGranularity.DAY, timeZone: "UTC",
    days: [{ fromUnixMs: f.data.fromUnixMs, untilUnixMs: f.data.untilUnixMs, totals: f.data.totals }],
    models: f.data.groups.map(({ providerId, providerName, modelId, modelName, totals }) => ({ providerId, providerName, modelId, modelName, totals })),
  });
  render(f.view());
  await screen.findByText("Incomplete coverage");
  const responseSection = screen.getByRole("region", { name: "Session, model and account details" });
  expect(within(responseSection).queryByText("Grok-only session")).toBeNull();
  if (mixed) {
    expect(within(responseSection).getByRole("table")).toBeTruthy();
    expect(within(responseSection).getByText("Retained session")).toBeTruthy();
    expect(within(responseSection).getByText(/1 responses · 1 unavailable/)).toBeTruthy();
  } else {
    expect(within(responseSection).queryByRole("table")).toBeNull();
    expect(within(responseSection).getByText(/No exact response usage is recorded/)).toBeTruthy();
  }
  const responseChart = screen.getByRole("region", { name: "By model / API" });
  fireEvent.click(within(responseChart).getByRole("button", { name: "View data" }));
  const responseModelTable = within(responseChart).getByRole("table");
  expect(within(responseModelTable).queryByText("Grok-only model")).toBeNull();
  expect(within(responseModelTable).queryByText(native.modelId)).toBeNull();
  expect(within(responseModelTable).getAllByRole("row")).toHaveLength(mixed ? 2 : 1);
  if (mixed) expect(within(responseModelTable).getByText("Original model")).toBeTruthy();
  const nativeSection = screen.getByRole("region", { name: "Verified Grok closed inputs" });
  expect(within(nativeSection).getByText("Grok-only session")).toBeTruthy();
  const nativeModelTable = within(nativeSection).getByRole("table", { name: "Verified Grok inputs by model" });
  expect(within(nativeModelTable).getByText("Grok-only model")).toBeTruthy();
  expect(f.data.analytics.models).toHaveLength(mixed ? 2 : 1);
});

it("explains separate response and Grok completion times across a day boundary", async () => {
  const f = fixture();
  const start = f.data.fromUnixMs;
  const day = 86_400_000n;
  const response = create(UsageTotalsSchema, { responses: 1, total: { knownTotal: "16", measuredResponses: 1 }, accounting: [{ kind: AccountingUnitKind.CODEX_RESPONSE, units: 1, knownTotal: "16", measuredUnits: 1 }] });
  const closedInput = create(UsageTotalsSchema, { accounting: [{ kind: AccountingUnitKind.GROK_CLOSED_INPUT, units: 1, knownTotal: "16", measuredUnits: 1 }] });
  const totals = create(UsageTotalsSchema, { ...response, accounting: [...response.accounting, ...closedInput.accounting] });
  f.data.untilUnixMs = start + day * 2n;
  f.data.accountingProfile = UsageAccountingProfile.NATIVE_UNITS_V1;
  f.data.totals = totals;
  f.data.groups[0].totals = totals;
  f.data.analytics = create(UsageAnalyticsSchema, {
    granularity: UsageTimeGranularity.DAY, timeZone: "UTC",
    days: [{ fromUnixMs: start, untilUnixMs: start + day, totals: response }, { fromUnixMs: start + day, untilUnixMs: start + day * 2n, totals: closedInput }],
    models: [{ providerId: f.ids.provider, modelId: f.ids.model, totals }],
  });
  render(f.view());
  await screen.findByText("Incomplete coverage");
  const applied = screen.getByRole("group", { name: "Applied conditions" });
  expect(within(applied).getByText(/^Response times show when the server first retained each response/)).toBeTruthy();
  const nativeSection = screen.getByRole("region", { name: "Verified Grok closed inputs" });
  expect(within(nativeSection).getByText(/Grok input times use the server's first retention of verified completion after confirmed cleanup/)).toBeTruthy();
  expect(within(nativeSection).getByText(/Filters and daily buckets use that time/)).toBeTruthy();
  const nativeDays = within(nativeSection).getByRole("table", { name: "Daily verified Grok inputs (UTC)" }).querySelectorAll("time");
  expect([...nativeDays].map((time) => time.dateTime)).toEqual([new Date(Number(start + day)).toISOString(), new Date(Number(start + day * 2n)).toISOString()]);
});

it("labels a new applied time scope while its result is still loading", async () => {
  const f = fixture(); render(f.view());
  await screen.findByText("Incomplete coverage");
  f.read.mockImplementationOnce(() => new Promise(() => {}));
  fireEvent.change(screen.getByLabelText(/^From \(/), { target: { value: "2026-09-23T10:00" } });
  fireEvent.change(screen.getByLabelText(/^Until \(/), { target: { value: "2026-09-24T10:00" } });
  fireEvent.click(screen.getByRole("button", { name: "Apply filters" }));
  const applied = screen.getByRole("group", { name: "Applied conditions" });
  expect(applied.textContent).toContain("2026");
  expect(applied.textContent).not.toContain("Last 30 days · server time");
  expect(screen.getByRole("status").textContent).toBe("Loading token usage…");
});

it("does not invent zero for empty telemetry and marks retained data stale after a failed refresh", async () => {
  const f = fixture(); f.data.groups = []; f.data.totals = undefined;
  render(f.view());
  await screen.findByText(/No exact response usage is recorded/);
  expect(screen.getAllByText("Unavailable").length).toBe(6);
  f.read.mockRejectedValueOnce(new ConnectError("Fixture unavailable", Code.Unavailable));
  fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
  await screen.findByText(/These are the last successfully retrieved values/);
  expect(f.read).toHaveBeenCalledTimes(2);
});

it("renders daily zero, unavailable and empty evidence with keyboard detail and complete model tables", async () => {
  const f = fixture();
  const start = BigInt(Date.UTC(2026, 8, 1));
  const day = 86_400_000n;
  const unavailable = { responses: 1, total: { knownTotal: "", measuredResponses: 0, unavailableResponses: 1 } };
  const models = [100, 90, 80, 70, 60, 0, 0].map((total, index) => ({ providerId: newRequestId(), modelId: newRequestId(), providerName: "Shared API", modelName: `Model ${index + 1}`, totals: { responses: 1, total: { knownTotal: String(total), measuredResponses: 1, unavailableResponses: 0 } } }));
  models.push({ providerId: newRequestId(), modelId: newRequestId(), providerName: "Retained API", modelName: "Unmeasured model", totals: { responses: 1, total: { knownTotal: "", measuredResponses: 0, unavailableResponses: 1 } } });
  f.data.analytics = create(UsageAnalyticsSchema, {
    granularity: UsageTimeGranularity.DAY,
    timeZone: "UTC",
    days: [
      { fromUnixMs: start, untilUnixMs: start + day, totals: { responses: 1, total: { knownTotal: "0", measuredResponses: 1, unavailableResponses: 0 } } },
      { fromUnixMs: start + day, untilUnixMs: start + day * 2n, totals: unavailable },
      { fromUnixMs: start + day * 2n, untilUnixMs: start + day * 3n, totals: { responses: 0, total: { knownTotal: "", measuredResponses: 0, unavailableResponses: 0 } } },
    ],
    models,
    otherModels: { modelCount: 2, totals: { responses: 2, total: { knownTotal: "0", measuredResponses: 2, unavailableResponses: 0 } } },
  });
  render(f.view());
  await screen.findByRole("group", { name: /Daily usage chart/ });
  const daily = screen.getByRole("group", { name: /Daily usage chart/ });
  fireEvent.focus(daily);
  expect(screen.getByText(/0 known tokens/)).toBeTruthy();
  fireEvent.keyDown(daily, { key: "ArrowRight" });
  expect(screen.getByText(/Unavailable total/)).toBeTruthy();
  expect(screen.getAllByRole("status").some((element) => element.textContent?.includes("1 responses · 0 measured · 1 unavailable"))).toBe(true);
  fireEvent.keyDown(daily, { key: "End" });
  expect(screen.getByText(/No responses recorded/)).toBeTruthy();
  fireEvent.keyDown(daily, { key: "Escape" });
  expect(screen.getByText(/Move focus to a chart point/)).toBeTruthy();
  const toggles = screen.getAllByRole("button", { name: "View data" });
  fireEvent.click(toggles[0]);
  const dailyTable = screen.getByRole("table", { name: /All calendar-day intervals/ });
  expect(screen.getByRole("region", { name: /Daily usage data table/ }).getAttribute("tabindex")).toBe("0");
  expect(within(dailyTable).getAllByRole("row")).toHaveLength(4);
  for (const label of ["Known total", "Input", "Cached input", "Cache-write input", "Output", "Reasoning output"]) expect(within(dailyTable).getByRole("columnheader", { name: label })).toBeTruthy();
  const modelToggle = screen.getByRole("button", { name: "View data" });
  fireEvent.click(modelToggle);
  const modelTable = screen.getByRole("table", { name: /Every original provider and model group/ });
  expect(screen.getByRole("region", { name: /Model usage data table/ }).getAttribute("tabindex")).toBe("0");
  expect(within(modelTable).getAllByRole("row")).toHaveLength(10);
  for (const label of ["Known total", "Input", "Cached input", "Cache-write input", "Output", "Reasoning output"]) expect(within(modelTable).getByRole("columnheader", { name: label })).toBeTruthy();
  expect(within(modelTable).getByText("Unmeasured model")).toBeTruthy();
  expect(within(modelTable).getByText(/Other models \(2 measured groups\)/)).toBeTruthy();
  expect(f.read.mock.calls[0][0]).toMatchObject({ granularity: UsageTimeGranularity.DAY, timeZone: expect.any(String) });
});

it("keeps historical currency subtotals, partial coverage and source basis separate from actual cost", async () => {
  const f = fixture();
  f.data.estimates = create(EstimateTotalsSchema, { unpricedResponses: 1, currencies: [{ currency: "USD", knownAmount: "9223.372036854775807", completeResponses: 1 }, { currency: "EUR", knownAmount: "0", partialResponses: 1 }] });
  f.data.pricing = [create(PricingUsageSchema, { pricing: { id: newRequestId(), modelId: f.ids.model, providerId: f.ids.provider, revision: 1n, basis: { currency: "USD", source: "Retained original source", asOf: "2026-09-01", inputMode: InputPricingMode.UNIFORM, inputPerMillion: "0.000000001", exclusions: ["Fixture fee excluded"] } }, totals: { currency: "USD", knownAmount: "9223.372036854775807", completeResponses: 1 }, input: { knownTokens: "9223372036854775807", knownAmount: "9223.372036854775807", pricedResponses: 1 }, output: { missingPriceResponses: 1 } })];
  render(f.view()); await screen.findByText("USD 9223.372036854775807");
  expect(screen.getByText("EUR 0")).toBeTruthy();
  expect(screen.getByText(/1 responses have no matching historical price/)).toBeTruthy();
  expect(screen.getByText("Actual API cost:").parentElement!.textContent).toContain("Unavailable");
  fireEvent.click(screen.getByText(/Historical basis · USD/));
  expect(screen.getByText("Retained original source")).toBeTruthy();
  expect(screen.getByText("Fixture fee excluded")).toBeTruthy();
  expect(screen.getByText(BigInt("9223372036854775807").toLocaleString())).toBeTruthy();
});

it("consumes an account entry once, preserves its exact bounds and retains subsequent filter edits", async () => {
  const f = fixture();
  const entry = { key: newRequestId(), accountId: f.ids.account, fromUnixMs: f.data.fromUnixMs + 123n, untilUnixMs: f.data.untilUnixMs + 789n };
  // A stable transport matches same-identity reconnect ownership.
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  const transport = createRouterTransport(router => { router.service(SystemService, { getStatus: () => ({}) }); router.service(UsageService, { getUsageSummary: f.read }); router.service(ResourceService, { listResources: () => ({ resources: [] }) }); });
  const viewFor = (active = true, value = entry) => <TransportProvider transport={transport}><QueryClientProvider client={client}><Usage active={active} open={f.open} entry={value} /></QueryClientProvider></TransportProvider>;
  const view = render(viewFor()); await screen.findByText("Incomplete coverage");
  expect(f.read).toHaveBeenCalledTimes(1);
  expect(f.read.mock.calls[0][0]).toMatchObject({ accountId: entry.accountId, fromUnixMs: entry.fromUnixMs, untilUnixMs: entry.untilUnixMs });
  fireEvent.change(screen.getByLabelText(/^From \(/), { target: { value: "2026-09-01T10:00" } });
  view.rerender(viewFor(false)); view.rerender(viewFor());
  expect((screen.getByLabelText(/^From \(/) as HTMLInputElement).value).toBe("2026-09-01T10:00");
  expect(f.read).toHaveBeenCalledTimes(1);
  const next = { ...entry, key: newRequestId(), accountId: newRequestId() };
  view.rerender(viewFor(true, next));
  await waitFor(() => expect(f.read).toHaveBeenCalledTimes(2));
  expect(f.read.mock.calls[1][0].accountId).toBe(next.accountId);
});
