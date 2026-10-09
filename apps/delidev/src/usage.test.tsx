import { chooseScrollOption, waitScrollChoices } from "./test-scroll-picker";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { AccountingUnitKind, EstimateTotalsSchema, PricingUsageSchema, InputPricingMode, EntityKind, GetUsageSummaryResponseSchema, ResourceSchema, ResourceService, UsageAnalyticsSchema, UsageCostState, UsageCoverage, UsageService, SystemService, UsageTimeGranularity, UsageAccountingProfile, UsageTotalsSchema, newRequestId, type GetUsageSummaryRequest } from "@delinoio/delidev-api-client";
import { Usage } from "./usage";
import { i18n, SupportedLanguage } from "./localization";
import { SidebarOutletProvider } from "./sidebar-context";
import { encode } from "./documents";

function fixture() {
  const ids = { session: newRequestId(), account: newRequestId(), model: newRequestId(), provider: newRequestId(), project: newRequestId() };
  const count = { knownTotal: "18446744073709551614", measuredResponses: 2, unavailableResponses: 1 };
  const totals = { responses: 3, total: count, input: { knownTotal: "0", measuredResponses: 2, unavailableResponses: 1 }, output: count, cachedInput: { knownTotal: "", measuredResponses: 0, unavailableResponses: 3 }, cacheWriteInput: { knownTotal: "", measuredResponses: 0, unavailableResponses: 3 }, reasoningOutput: { knownTotal: "0", measuredResponses: 3, unavailableResponses: 0 } };
  const data = create(GetUsageSummaryResponseSchema, { fromUnixMs: BigInt(Date.UTC(2026, 8, 1)), untilUnixMs: BigInt(Date.UTC(2026, 8, 25)), totals, coverage: UsageCoverage.OBSERVED_ROOT_RESPONSES, actualCost: UsageCostState.UNAVAILABLE, estimatedCost: UsageCostState.UNAVAILABLE, acceptedExecutionsWithoutResponse: 2, groups: [{ sessionId: ids.session, sessionName: "Retained session", accountId: ids.account, accountName: "Original account", modelId: ids.model, modelName: "Original model", providerId: ids.provider, providerName: "Original API", totals }] });
  const read = vi.fn(async (_request: GetUsageSummaryRequest) => data);
  const transport = createRouterTransport((router) => {
    router.service(UsageService, { getUsageSummary: read });
    router.service(ResourceService, { getResource: request => ({ resource: create(ResourceSchema, { id: request.id, kind: request.kind, revision: 1n, schemaVersion: 1, documentJson: encode({ alias: "Original account" }) }) }), listResources: (request) => ({ resources: request.filter?.kind === EntityKind.ACCOUNT ? [create(ResourceSchema, { id: ids.account, kind: EntityKind.ACCOUNT, revision: 1n, schemaVersion: 1, documentJson: encode({ alias: "Original account" }) })] : [] }) });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  const open = vi.fn();
  const view = (active = true) => <TransportProvider transport={transport}><QueryClientProvider client={client}><Usage active={active} open={open} /></QueryClientProvider></TransportProvider>;
  return { ids, data, read, open, view };
}

it.each([
  { language: SupportedLanguage.English, historical: false },
  { language: SupportedLanguage.English, historical: true },
  { language: SupportedLanguage.Korean, historical: false },
  { language: SupportedLanguage.Korean, historical: true },
])("guides future pricing through Usage in $language with historical basis=$historical", async ({ language, historical }) => {
  await i18n.changeLanguage(language);
  const f = fixture();
  if (historical) {
    f.data.pricing = [create(PricingUsageSchema, { pricing: { id: newRequestId(), modelId: f.ids.model, providerId: f.ids.provider, revision: 1n, basis: { currency: "USD", source: "Retained original price source", asOf: "2026-09-01", inputMode: InputPricingMode.UNIFORM, inputPerMillion: "1", exclusions: ["Original exclusion"] } } })];
  }
  render(f.view());
  const english = language === SupportedLanguage.English;
  const guidance = await screen.findByText(english ? /To configure future prices, open Model prices/ : /향후 가격을 설정하려면 모델 단가를 열고/);
  fireEvent.click(screen.getByRole("tab", { name: english ? "Usage history" : "사용 기록" }));
  const panel = screen.getByRole("tabpanel", { name: english ? "Usage history" : "사용 기록" });
  expect(panel.contains(guidance)).toBe(true);
  expect(guidance.textContent).toContain(english ? "select the original model" : "원래 모델을 선택하세요");
  expect(guidance.textContent).toContain(english ? "Currencies are separate" : "통화는 별도로 관리합니다");
  expect(guidance.textContent).toContain(english ? "do not establish complete native usage, actual spend or budget compliance" : "전체 네이티브 사용량, 실제 지출 또는 예산 준수를 보장하지 않습니다");
  expect(panel.textContent).not.toContain("Settings → Models");
  expect(panel.textContent).not.toContain("설정 → 모델");
  if (historical) {
    expect(within(panel).getByText("Retained original price source")).toBeTruthy();
    expect(within(panel).getByText("Original exclusion")).toBeTruthy();
  }
});

it("shows exact known subtotals, missing fields and separate unavailable costs", async () => {
  const f = fixture(); render(f.view());
  await screen.findByText("Incomplete coverage"); fireEvent.click(screen.getByRole("tab", { name: "Usage history" }));
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

it("applies selections immediately and preserves invalid dates across navigation", async () => {
  const f = fixture(); const view = render(f.view());
  await screen.findByText("Incomplete coverage"); fireEvent.click(screen.getByRole("tab", { name: "Usage history" }));
  await chooseScrollOption(screen.getByRole("combobox", { name: "Account" }), f.ids.account);
  fireEvent.click(screen.getByRole("checkbox", { name: "General Chat only" }));
  expect((screen.getByRole("combobox", { name: "Project" }) as HTMLSelectElement).disabled).toBe(true);
  await waitFor(() => expect(f.read).toHaveBeenCalledTimes(3));
  view.rerender(f.view(false)); view.rerender(f.view());
  expect((screen.getByRole("combobox", { name: "Account" }) as HTMLSelectElement).dataset.value).toBe(f.ids.account);
  expect(screen.queryByRole("button", { name: "Apply filters" })).toBeNull();
  expect(f.read.mock.calls[2][0]).toMatchObject({ accountId: f.ids.account, generalChat: true, projectId: "" });
  fireEvent.change(screen.getByLabelText(/^From \(/), { target: { value: "2026-09-25T10:00" } });
  fireEvent.change(screen.getByLabelText(/^Until \(/), { target: { value: "2026-09-24T10:00" } });
  await screen.findByRole("alert"); expect(f.read).toHaveBeenCalledTimes(3);
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
  await screen.findByText("Incomplete coverage"); fireEvent.click(screen.getByRole("tab", { name: "Usage history" }));
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
  fireEvent.click(screen.getByRole("tab", { name: "Usage history" }));
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
  await screen.findByText("Incomplete coverage"); fireEvent.click(screen.getByRole("tab", { name: "Usage history" }));
  const applied = screen.getByRole("group", { name: "Applied conditions" });
  expect(within(applied).getByText(/^Response times show when the server first retained each response/)).toBeTruthy();
  fireEvent.click(screen.getByRole("tab", { name: "Usage history" }));
  const nativeSection = screen.getByRole("region", { name: "Verified Grok closed inputs" });
  expect(within(nativeSection).getByText(/Grok input times use the server's first retention of verified completion after confirmed cleanup/)).toBeTruthy();
  expect(within(nativeSection).getByText(/Filters and daily buckets use that time/)).toBeTruthy();
  const nativeDays = within(nativeSection).getByRole("table", { name: "Daily verified Grok inputs (UTC)" }).querySelectorAll("time");
  expect([...nativeDays].map((time) => time.dateTime)).toEqual([new Date(Number(start + day)).toISOString(), new Date(Number(start + day * 2n)).toISOString()]);
});

it("labels a new applied time scope while its result is still loading", async () => {
  const f = fixture(); render(f.view());
  await screen.findByText("Incomplete coverage"); fireEvent.click(screen.getByRole("tab", { name: "Usage history" }));
  f.read.mockImplementationOnce(() => new Promise(() => {}));
  fireEvent.change(screen.getByLabelText(/^From \(/), { target: { value: "2026-09-23T10:00" } });
  fireEvent.change(screen.getByLabelText(/^Until \(/), { target: { value: "2026-09-24T10:00" } });
  await screen.findByText("Loading token usage…");
  const applied = screen.getByRole("group", { name: "Applied conditions" });
  expect(applied.textContent).toContain("2026");
  expect(applied.textContent).not.toContain("Last 30 days · server time");
  expect(screen.getByRole("status").textContent).toBe("Loading token usage…");
});

function emptyTotals() {
  const empty = { knownTotal: "", measuredResponses: 0, unavailableResponses: 0 };
  return create(UsageTotalsSchema, { responses: 0, total: empty, input: empty, output: empty, cachedInput: empty, cacheWriteInput: empty, reasoningOutput: empty });
}

it.each([[0, 0], [12, 0], [0, 3]])("shows six empty recorded summary zeros while preserving missing execution=%s and compaction=%s coverage", async (executions, compactions) => {
  const f = fixture();
  f.data.groups = [];
  f.data.totals = emptyTotals();
  f.data.acceptedExecutionsWithoutResponse = executions;
  f.data.acceptedCompactionsWithoutResponse = compactions;
  render(f.view());
  await screen.findByText("Incomplete coverage"); fireEvent.click(screen.getByRole("tab", { name: "Usage history" }));
  const summary = screen.getByRole("region", { name: "Known token totals" });
  expect([...summary.querySelectorAll("[data-token-measure] dd")].map((value) => value.textContent)).toEqual(Array(6).fill("0"));
  expect(within(summary).getAllByText("0 measured · 0 unavailable responses")).toHaveLength(6);
  expect(screen.getByText(new RegExp(`${executions} accepted executions`))).toBeTruthy();
  expect(screen.getByText(new RegExp(`${compactions} native context actions`))).toBeTruthy();
  expect(f.data.totals.total!.knownTotal).toBe("");
  expect(f.data.totals.total!.measuredResponses).toBe(0);
  f.read.mockRejectedValueOnce(new ConnectError("Fixture unavailable", Code.Unavailable));
  fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
  await screen.findByText(/These are the last successfully retrieved values/);
  expect([...summary.querySelectorAll("[data-token-measure] dd")].map((value) => value.textContent)).toEqual(Array(6).fill("0"));
  expect(screen.getByText(/Stale values from the last successful/)).toBeTruthy();
});

it.each(["missing measure", "unavailable count", "measured count", "nonempty subtotal", "malformed subtotal", "recorded response"])("does not apply the empty summary exception with %s", async (invalid) => {
  const f = fixture();
  f.data.groups = [];
  const totals = emptyTotals();
  if (invalid === "missing measure") totals.reasoningOutput = undefined;
  if (invalid === "unavailable count") totals.reasoningOutput!.unavailableResponses = 1;
  if (invalid === "measured count") totals.reasoningOutput!.measuredResponses = 1;
  if (invalid === "nonempty subtotal") totals.reasoningOutput!.knownTotal = "0";
  if (invalid === "malformed subtotal") totals.reasoningOutput!.knownTotal = "invalid";
  if (invalid === "recorded response") {
    totals.responses = 1;
    for (const key of ["total", "input", "output", "cachedInput", "cacheWriteInput", "reasoningOutput"] as const) totals[key]!.unavailableResponses = 1;
  }
  f.data.totals = totals;
  render(f.view());
  await screen.findByText("Incomplete coverage"); fireEvent.click(screen.getByRole("tab", { name: "Usage history" }));
  const summary = screen.getByRole("region", { name: "Known token totals" });
  expect([...summary.querySelectorAll("[data-token-measure] dd")].map((value) => value.textContent)).toEqual(Array(6).fill("Unavailable"));
});

it("does not invent zero for empty telemetry and marks retained data stale after a failed refresh", async () => {
  const f = fixture(); f.data.groups = []; f.data.totals = undefined;
  render(f.view());
  await screen.findByText(/No exact response usage is recorded/); fireEvent.click(screen.getByRole("tab", { name: "Usage history" }));
  expect(screen.getByRole("region", { name: "Known token totals" }).querySelectorAll("[data-token-measure] dd")).toHaveLength(6);
  expect([...screen.getByRole("region", { name: "Known token totals" }).querySelectorAll("[data-token-measure] dd")].every(value => value.textContent === "Unavailable")).toBe(true);
  f.read.mockRejectedValueOnce(new ConnectError("Fixture unavailable", Code.Unavailable));
  fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
  await screen.findByText(/These are the last successfully retrieved values/);
  expect(f.read).toHaveBeenCalledTimes(2);
  expect(screen.getByRole("alert").textContent).toContain("failed read does not establish zero usage or cost");
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
  await screen.findByText("Incomplete coverage"); fireEvent.click(screen.getByRole("tab", { name: "Usage history" }));
  await screen.findByRole("group", { name: /Daily usage chart/ });
  const daily = screen.getByRole("group", { name: /Daily usage chart/ });
  const points = daily.querySelectorAll("circle");
  expect(points).toHaveLength(2); // No marker for the empty third day.
  expect(points[0].getAttribute("cy")).toBe(points[1].getAttribute("cy")); // Measured zero is on the baseline.
  expect(points[0].classList.contains("usage-point-unavailable")).toBe(false);
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
  render(f.view()); await screen.findByText("USD 9,223.372036854775807"); fireEvent.click(screen.getByRole("tab", { name: "Usage history" }));
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
  const view = render(viewFor()); await screen.findByText("Incomplete coverage"); fireEvent.click(screen.getByRole("tab", { name: "Usage history" }));
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

it("retains the drawer and input focus on automatic dates and selections, while Refresh keeps the applied range", async () => {
  const f = fixture(); const closeDrawer = vi.fn();
  render(<SidebarOutletProvider target={null} drawerOpen closeDrawer={closeDrawer}>{f.view()}</SidebarOutletProvider>);
  await screen.findByText("Incomplete coverage"); fireEvent.click(screen.getByRole("tab", { name: "Usage history" }));
  const input = screen.getByLabelText(/^From \(/) as HTMLInputElement;
  input.focus();
  fireEvent.change(input, { target: { value: "2026-09-01T10:00" } });
  expect(document.activeElement).toBe(input);
  expect(screen.getAllByText(/Waiting for date input/).length).toBeGreaterThan(0);
  fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
  await waitFor(() => expect(f.read).toHaveBeenCalledTimes(2));
  expect(f.read.mock.calls[1][0].fromUnixMs).toBe(0n);
  fireEvent.change(screen.getByLabelText(/^Until \(/), { target: { value: "2026-09-02T10:00" } });
  await waitFor(() => expect(f.read).toHaveBeenCalledTimes(3));
  expect(closeDrawer).not.toHaveBeenCalled();
  expect(document.activeElement).toBe(input);
  fireEvent.click(screen.getByRole("checkbox", { name: "General Chat only" }));
  await waitFor(() => expect(f.read).toHaveBeenCalledTimes(4));
  expect(closeDrawer).not.toHaveBeenCalled();
  expect(screen.queryByText(/Waiting for date input/)).toBeNull();
});

it("keeps current query results when an older scope resolves later, without showing old data under new conditions", async () => {
  const f = fixture(); render(f.view());
  await screen.findByText("Incomplete coverage"); fireEvent.click(screen.getByRole("tab", { name: "Usage history" }));
  let finishA!: (data: typeof f.data) => void;
  let finishB!: (data: typeof f.data) => void;
  f.read.mockImplementationOnce(() => new Promise(resolve => { finishA = resolve; }));
  fireEvent.click(screen.getByRole("checkbox", { name: "General Chat only" }));
  await waitFor(() => expect(f.read).toHaveBeenCalledTimes(2));
  expect(screen.queryByText("Retained session")).toBeNull();
  expect(screen.getByText("Loading token usage…")).toBeTruthy();
  f.read.mockImplementationOnce(() => new Promise(resolve => { finishB = resolve; }));
  await chooseScrollOption(screen.getByRole("combobox", { name: "Account" }), f.ids.account);
  await waitFor(() => expect(f.read).toHaveBeenCalledTimes(3));
  const b = create(GetUsageSummaryResponseSchema, f.data);
  b.groups[0].sessionName = "Latest scope";
  finishB(b);
  await screen.findByText("Latest scope");
  const a = create(GetUsageSummaryResponseSchema, f.data);
  a.groups[0].sessionName = "Earlier scope";
  finishA(a);
  await waitFor(() => expect(screen.queryByText("Loading token usage…")).toBeNull());
  expect(screen.getByText("Latest scope")).toBeTruthy();
  expect(screen.queryByText("Earlier scope")).toBeNull();
  expect(screen.getByRole("group", { name: "Applied conditions" }).textContent).toContain(f.ids.account);
});

it("does not reuse old scope data when a new valid selection fails", async () => {
  const f = fixture(); render(f.view()); await screen.findByText("Incomplete coverage"); fireEvent.click(screen.getByRole("tab", { name: "Usage history" }));
  f.read.mockRejectedValueOnce(new ConnectError("Fixture unavailable", Code.Unavailable));
  fireEvent.click(screen.getByRole("checkbox", { name: "General Chat only" }));
  await waitFor(() => expect(f.read).toHaveBeenCalledTimes(2));
  await screen.findByRole("alert");
  expect(screen.queryByText("Retained session")).toBeNull();
  expect(screen.queryByText(/These are the last successfully retrieved values/)).toBeNull();
});

it("defaults to Overview, keeps three topmost semantic tabs and retains state without summary reads", async () => {
 const f=fixture(); const view=render(f.view()); await screen.findByText("Incomplete coverage");
 const tabs=screen.getAllByRole("tab");
 expect(document.querySelector(".usage-page")!.firstElementChild).toBe(tabs[0].parentElement);
 expect(tabs.map(tab=>tab.textContent)).toEqual(["Overview","Usage history","Model prices"]);
 expect(tabs.map(tab=>tab.getAttribute("aria-selected"))).toEqual(["true","false","false"]);
 expect(tabs.map(tab=>tab.tabIndex)).toEqual([0,-1,-1]);
 expect(screen.queryByRole("button",{name:"Model details and token pricing"})).toBeNull();
 tabs[0].focus(); fireEvent.keyDown(tabs[0],{key:"ArrowLeft"}); expect(document.activeElement).toBe(tabs[2]);
 expect(screen.getByRole("tabpanel",{name:"Model prices"})).toBeTruthy();
 fireEvent.keyDown(tabs[2],{key:"Home"}); expect(document.activeElement).toBe(tabs[0]);
 fireEvent.keyDown(tabs[0],{key:"ArrowRight"}); expect(document.activeElement).toBe(tabs[1]);
 expect(screen.getByRole("region",{name:"Known token totals"})).toBeTruthy();
 fireEvent.keyDown(tabs[1],{key:"End"}); expect(document.activeElement).toBe(tabs[2]);
 view.rerender(f.view(false));view.rerender(f.view());
 expect(screen.getByRole("tab",{name:"Model prices"}).getAttribute("aria-selected")).toBe("true");
 expect(f.read).toHaveBeenCalledTimes(1);
});

it("shows supported empty source rows collapsed and retains a user source choice across navigation and refresh", async () => {
  const f = fixture();
  f.data.accountingProfile = UsageAccountingProfile.NATIVE_UNITS_V1;
  f.data.nativeAccounting = create(GetUsageSummaryResponseSchema, { nativeAccounting: [
    { totals: { kind: AccountingUnitKind.CLAUDE_MAIN_LOOP_INPUT } },
    { totals: { kind: AccountingUnitKind.OPENCODE_STEP, units: 1, input: { knownTotal: "9007199254740993", measuredUnits: 1 } } },
  ] }).nativeAccounting;
  const view = render(f.view()); await screen.findByText("Incomplete coverage"); fireEvent.click(screen.getByRole("tab", { name: "Usage history" }));
  fireEvent.click(screen.getByRole("tab", { name: "Usage history" }));
  const panel = screen.getByRole("tabpanel", { name: "Usage history" });
  const claude = within(panel).getByRole("region", { name: "Claude main-loop inputs" });
  const grok = within(panel).getByRole("region", { name: "Verified Grok closed inputs" });
  const opencode = within(panel).getByRole("region", { name: "OpenCode steps" });
  expect(claude.querySelector("details")!.open).toBe(false);
  expect(grok.querySelector("details")!.open).toBe(false);
  expect(opencode.querySelector("details")!.open).toBe(true);
  expect(within(claude).getAllByText("No records does not establish zero usage or cost").length).toBeGreaterThan(0);
  const disclosure = opencode.querySelector("details")!;
  disclosure.open = false; fireEvent(disclosure, new Event("toggle"));
  view.rerender(f.view(false)); view.rerender(f.view());
  expect(screen.getByRole("tab", { name: "Usage history" }).getAttribute("aria-selected")).toBe("true");
  expect(disclosure.open).toBe(false);
  fireEvent.click(screen.getByRole("button", { name: "Refresh" })); await waitFor(() => expect(f.read).toHaveBeenCalledTimes(2));
  expect(disclosure.open).toBe(false);
  expect(within(panel).getByText(BigInt("9007199254740993").toLocaleString())).toBeTruthy();
});
it("keeps original identities and complete counter/estimate evidence in mounted compact row Details", async () => {
 const f = fixture(); f.data.groups[0].projectId=newRequestId(); f.data.groups[0].projectName="Original project";
 f.data.groups[0].estimates=create(EstimateTotalsSchema,{currencies:[{currency:"USD",knownAmount:"0.002",partialResponses:1},{currency:"EUR",knownAmount:"0",completeResponses:1}],unpricedResponses:1});
 const view=render(f.view()); await screen.findByText("Incomplete coverage"); fireEvent.click(screen.getByRole("tab", { name: "Usage history" }));
 const row=document.querySelector<HTMLTableRowElement>(".usage-detail tbody tr")!, detail=row.querySelector<HTMLDetailsElement>("details")!, summary=detail.querySelector("summary")!;
 expect(row.querySelectorAll("td")).toHaveLength(9); for(const cell of [...row.cells].slice(0,3)) for(const id of Object.values(f.ids)) expect(cell.textContent).not.toContain(id);
 fireEvent.click(summary); expect(detail.open).toBe(true); expect(detail.querySelectorAll("[data-token-measure]")).toHaveLength(6);
 for(const id of [f.ids.session,f.ids.account,f.ids.model,f.ids.provider,f.data.groups[0].projectId]) expect(detail.textContent).toContain(id);
 expect(detail.textContent).toContain("USD 0.002"); expect(detail.textContent).toContain("EUR 0"); expect(f.read).toHaveBeenCalledTimes(1);
 view.rerender(f.view(false)); view.rerender(f.view()); expect(row.querySelector("details")).toBe(detail); expect(detail.open).toBe(true);
 fireEvent.click(screen.getByRole("button",{name:"Refresh"})); await waitFor(()=>expect(f.read).toHaveBeenCalledTimes(2)); expect(row.querySelector("details")).toBe(detail); expect(detail.open).toBe(true);
});

it("shows independent source totals and exact retained estimates without manufacturing Claude or Grok totals", async () => {
 const f = fixture();
 f.data.accountingProfile = UsageAccountingProfile.NATIVE_UNITS_V1;
 f.data.totals = create(UsageTotalsSchema, { responses: 16, total: { knownTotal: "269410", measuredResponses: 16 }, accounting: [{ kind: AccountingUnitKind.GROK_CLOSED_INPUT, units: 1, knownTotal: "125", measuredUnits: 1 }] });
 f.data.estimates = create(EstimateTotalsSchema, { currencies: [{ currency: "USD", knownAmount: "0.270966", completeResponses: 16 }, { currency: "EUR", knownAmount: "0", completeResponses: 1 }] });
 f.data.nativeAccounting = create(GetUsageSummaryResponseSchema, { nativeAccounting: [
  { totals: { kind: AccountingUnitKind.CLAUDE_MAIN_LOOP_INPUT, units: 1, input: { knownTotal: "456", measuredUnits: 1 } } },
  { totals: { kind: AccountingUnitKind.OPENCODE_STEP, units: 1, total: { knownTotal: "0", measuredUnits: 1 } } }
 ] }).nativeAccounting;
 render(f.view()); const panel = await screen.findByRole("tabpanel", { name: "Overview" });
 await within(panel).findByText("USD 0.270966");
 const row = (name: string) => within(panel).getByRole("rowheader", { name }).closest("tr")!;
 expect(row("Codex").textContent).toContain("269,410");expect(row("Codex").textContent).toContain("EUR 0");
 expect(row("Claude Code").textContent).toContain("The tool did not report a total.");expect(row("Claude Code").textContent).not.toContain("456");
 expect(row("OpenCode").textContent).toContain("0Native-reported total");
 expect(row("Grok Build").textContent).toContain("125Input tokens");expect(row("Grok Build").textContent).not.toContain("USD");
 expect(f.read).toHaveBeenCalledTimes(1);
});

it("opens exact historical pricing without changing filters and retains read-only deleted evidence", async () => {
 const f = fixture(); const version = newRequestId();
 f.data.pricing = [create(PricingUsageSchema, { pricing: { id: version, modelId: f.ids.model, providerId: f.ids.provider, revision: 1n, basis: { currency: "USD", source: "Original deleted price", asOf: "2026-09-01", inputMode: InputPricingMode.UNIFORM, inputPerMillion: "0" } } })];
 render(f.view()); await screen.findByText("Incomplete coverage");fireEvent.click(screen.getByRole("tab", { name: "Usage history" }));
 fireEvent.click(screen.getByRole("button", { name: "Original model" }));
 const panel = screen.getByRole("tabpanel", { name: "Model prices" });
 await within(panel).findByText("This original identity has no editable current model. Historical price evidence is read-only.");
 expect(within(panel).getByText("Original deleted price")).toBeTruthy();expect(within(panel).getByText(new RegExp(version))).toBeTruthy();
 expect(within(panel).queryByRole("button", { name: "Edit token pricing" })).toBeNull();expect(f.read).toHaveBeenCalledTimes(1);
 expect((screen.getByRole("combobox", { name: "Model", hidden: true }) as HTMLElement).dataset.value).toBe("");
});

it("retains manual price draft and exact uncertain retry across tabs and dashboard Refresh", async () => {
 const f=fixture();
 const model=create(ResourceSchema,{id:f.ids.model,kind:EntityKind.MODEL,schemaVersion:1,revision:1n,documentJson:encode({name:"Original model",provider_id:f.ids.provider,native_id:"native-original",harnesses:["codex"],manual:true,metadata_source:"user-declared"})});
 const basis={currency:"USD",source:"Original source",asOf:"2026-09-01",inputMode:InputPricingMode.UNIFORM,inputPerMillion:"1"};
 const pricing=create(PricingUsageSchema,{pricing:{id:newRequestId(),modelId:f.ids.model,providerId:f.ids.provider,revision:1n,basis}}).pricing!;
 const write=vi.fn().mockRejectedValueOnce(new ConnectError("Lost price response",Code.Unavailable)).mockResolvedValue({pricing});
 const transport=createRouterTransport(router=>{
  router.service(UsageService,{getUsageSummary:f.read,getModelPricing:()=>({pricing,modelRevision:1n}),setModelPricing:write});
  router.service(ResourceService,{getResource:()=>({resource:model}),listResources:request=>({resources:request.filter?.kind===EntityKind.MODEL?[model]:[]})});
 });
 const client=new QueryClient({defaultOptions:{queries:{retry:false,staleTime:Infinity}}});
 render(<TransportProvider transport={transport}><QueryClientProvider client={client}><Usage active open={f.open}/></QueryClientProvider></TransportProvider>);
 await screen.findByText("Incomplete coverage");fireEvent.click(screen.getByRole("tab",{name:"Usage history"}));fireEvent.click(screen.getByRole("button",{name:"Original model"}));
 await waitFor(()=>expect((screen.getByRole("button",{name:"Edit token pricing"}) as HTMLButtonElement).disabled).toBe(false));
 fireEvent.click(screen.getByRole("button",{name:"Edit token pricing"}));
 fireEvent.change(screen.getByLabelText("Input rate per million"),{target:{value:"3"}});
 fireEvent.click(screen.getByRole("tab",{name:"Overview"}));fireEvent.click(screen.getByRole("button",{name:"Refresh"}));
 await waitFor(()=>expect(f.read).toHaveBeenCalledTimes(2));
 fireEvent.click(screen.getByRole("tab",{name:"Model prices"}));expect((screen.getByLabelText("Input rate per million") as HTMLInputElement).value).toBe("3");
 fireEvent.click(screen.getByRole("button",{name:"Save pricing version"}));await screen.findByRole("button",{name:"Retry the same price"});
 const original=write.mock.calls[0][0];
 fireEvent.click(screen.getByRole("tab",{name:"Usage history"}));fireEvent.click(screen.getByRole("tab",{name:"Model prices"}));
 fireEvent.click(screen.getByRole("button",{name:"Retry the same price"}));await waitFor(()=>expect(write).toHaveBeenCalledTimes(2));
 expect(write.mock.calls[1][0]).toEqual(original);expect(f.read).toHaveBeenCalledTimes(2);
});
