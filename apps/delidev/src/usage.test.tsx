import { chooseScrollOption, waitScrollChoices } from "./test-scroll-picker";
import { clone, create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { expect, it, vi } from "vitest";
import {
  AccountingUnitKind,
  EstimateTotalsSchema,
  PricingUsageSchema,
  InputPricingMode,
  EntityKind,
  GetUsageSummaryResponseSchema,
  ModelIdentitySchema,
  ResourceSchema,
  ResourceService,
  UsageAnalyticsSchema,
  UsageCostState,
  UsageCoverage,
  UsageService,
  TokenPricingMode,
  SystemService,
  SubscriptionServiceIdentity,
  UsageTimeGranularity,
  UsageAccountingProfile,
  UsageTotalsSchema,
  newRequestId,
  type GetUsageSummaryRequest,
} from "@delinoio/delidev-api-client";
import { Usage } from "./usage";
import {
  fakeDestinationWindow,
  primaryClick,
} from "./destination-window-test-support";
import { Surface } from "./surface";
import { UsageTab } from "./usage-tab";
import { priceIdentity } from "./pricing";
import { i18n, SupportedLanguage } from "./localization";
import { SidebarOutletProvider } from "./sidebar-context";
import { encode } from "./documents";

function fixture() {
  const ids = {
    session: newRequestId(),
    account: newRequestId(),
    model: newRequestId(),
    provider: newRequestId(),
    project: newRequestId(),
  };
  const count = {
    knownTotal: "18446744073709551614",
    measuredResponses: 2,
    unavailableResponses: 1,
  };
  const totals = {
    responses: 3,
    total: count,
    input: { knownTotal: "0", measuredResponses: 2, unavailableResponses: 1 },
    output: count,
    cachedInput: {
      knownTotal: "",
      measuredResponses: 0,
      unavailableResponses: 3,
    },
    cacheWriteInput: {
      knownTotal: "",
      measuredResponses: 0,
      unavailableResponses: 3,
    },
    reasoningOutput: {
      knownTotal: "0",
      measuredResponses: 3,
      unavailableResponses: 0,
    },
  };
  const data = create(GetUsageSummaryResponseSchema, {
    fromUnixMs: BigInt(Date.UTC(2026, 8, 1)),
    untilUnixMs: BigInt(Date.UTC(2026, 8, 25)),
    totals,
    coverage: UsageCoverage.OBSERVED_ROOT_RESPONSES,
    actualCost: UsageCostState.UNAVAILABLE,
    estimatedCost: UsageCostState.UNAVAILABLE,
    acceptedExecutionsWithoutResponse: 2,
    groups: [
      {
        sessionId: ids.session,
        sessionName: "Retained session",
        accountId: ids.account,
        accountName: "Original account",
        model: { nativeId: ids.model, providerId: ids.provider },
        modelName: "Original model",
        providerId: ids.provider,
        providerName: "Original API",
        totals,
      },
    ],
  });
  const read = vi.fn(async (_request: GetUsageSummaryRequest) => data);
  const transport = createRouterTransport((router) => {
    router.service(UsageService, {
      getUsageSummary: read,
      getTokenPricing: () => ({
        providerRevision: 0n,
        policy: { mode: TokenPricingMode.AUTOMATIC },
        reference: { state: "unavailable" },
      }),
    });
    router.service(ResourceService, {
      getResource: (request) => ({
        resource: create(ResourceSchema, {
          id: request.id,
          kind: request.kind,
          revision: 1n,
          schemaVersion: 1,
          documentJson: encode({ alias: "Original account" }),
        }),
      }),
      listResources: (request) => ({
        resources:
          request.filter?.kind === EntityKind.ACCOUNT
            ? [
                create(ResourceSchema, {
                  id: ids.account,
                  kind: EntityKind.ACCOUNT,
                  revision: 1n,
                  schemaVersion: 1,
                  documentJson: encode({ alias: "Original account" }),
                }),
              ]
            : [],
      }),
    });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity } },
  });
  const open = vi.fn();
  const view = (active = true) => (
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <Usage active={active} open={open} />
      </QueryClientProvider>
    </TransportProvider>
  );
  return { ids, data, read, open, view };
}

it.each([
  { language: SupportedLanguage.English, historical: false },
  { language: SupportedLanguage.English, historical: true },
  { language: SupportedLanguage.Korean, historical: false },
  { language: SupportedLanguage.Korean, historical: true },
])(
  "guides future pricing through Usage in $language with historical basis=$historical",
  async ({ language, historical }) => {
    await i18n.changeLanguage(language);
    const f = fixture();
    if (historical) {
      f.data.pricing = [
        create(PricingUsageSchema, {
          pricing: {
            id: newRequestId(),
            model: { nativeId: f.ids.model, providerId: f.ids.provider },
            providerId: f.ids.provider,
            revision: 1n,
            basis: {
              currency: "USD",
              source: "Retained original price source",
              asOf: "2026-09-01",
              inputMode: InputPricingMode.UNIFORM,
              inputPerMillion: "1",
              exclusions: ["Original exclusion"],
            },
          },
        }),
      ];
    }
    render(f.view());
    const english = language === SupportedLanguage.English;
    const guidance = await screen.findByText(
      english
        ? /To configure future prices, open Model prices/
        : /향후 가격을 설정하려면 모델 단가를 열고/,
    );
    fireEvent.click(
      screen.getByRole("tab", {
        name: english ? "Usage history" : "사용 기록",
      }),
    );
    const panel = screen.getByRole("tabpanel", {
      name: english ? "Usage history" : "사용 기록",
    });
    expect(panel.contains(guidance)).toBe(true);
    expect(guidance.textContent).toContain(
      english ? "select the original model" : "원래 모델을 선택하세요",
    );
    expect(guidance.textContent).toContain(
      english ? "Currencies are separate" : "통화는 별도로 관리합니다",
    );
    expect(guidance.textContent).toContain(
      english
        ? "do not establish complete native usage, actual spend or budget compliance"
        : "전체 네이티브 사용량, 실제 지출 또는 예산 준수를 보장하지 않습니다",
    );
    expect(panel.textContent).not.toContain("Settings → Models");
    expect(panel.textContent).not.toContain("설정 → 모델");
    if (historical) {
      expect(
        within(panel).getByText("Retained original price source"),
      ).toBeTruthy();
      expect(within(panel).getByText("Original exclusion")).toBeTruthy();
    }
  },
);

it("shows exact known subtotals, missing fields and separate unavailable costs", async () => {
  const f = fixture();
  render(f.view());
  await screen.findByText("Incomplete coverage");
  fireEvent.click(screen.getByRole("tab", { name: "Usage history" }));
  expect(
    screen.getAllByText(BigInt("18446744073709551614").toLocaleString()).length,
  ).toBeGreaterThan(0);
  expect(screen.getAllByText("Unavailable").length).toBeGreaterThan(0);
  expect(screen.getByText(/2 accepted executions/)).toBeTruthy();
  expect(
    screen.getByText("Actual API cost:").parentElement!.textContent,
  ).toContain("Unavailable");
  expect(
    screen.getByText("Token-price estimate:").parentElement!.textContent,
  ).toContain("Unavailable");
  expect(
    screen.getByText(/charts are unavailable from this server version/),
  ).toBeTruthy();
  const table = screen.getByRole("table");
  expect(within(table).getByText("General Chat")).toBeTruthy();
  expect(within(table).queryByText(f.ids.account)).toBeNull();
  expect(within(table).getAllByText("Original model").length).toBeGreaterThan(
    0,
  );
  fireEvent.click(
    within(table).getByRole("button", { name: "Retained session" }),
  );
  expect(f.open).toHaveBeenCalledWith(f.ids.session);
  expect(f.read.mock.calls[0][0].fromUnixMs).toBe(0n);
  expect(f.read.mock.calls[0][0].accountingProfile).toBe(
    UsageAccountingProfile.NATIVE_UNITS_V1,
  );
});

it("applies selections immediately and preserves invalid dates across navigation", async () => {
  const f = fixture();
  const view = render(f.view());
  await screen.findByText("Incomplete coverage");
  fireEvent.click(screen.getByRole("tab", { name: "Usage history" }));
  await chooseScrollOption(
    screen.getByRole("combobox", { name: "Account" }),
    f.ids.account,
  );
  fireEvent.click(screen.getByRole("checkbox", { name: "General Chat only" }));
  expect(
    (screen.getByRole("combobox", { name: "Project" }) as HTMLSelectElement)
      .disabled,
  ).toBe(true);
  await waitFor(() => expect(f.read).toHaveBeenCalledTimes(3));
  view.rerender(f.view(false));
  view.rerender(f.view());
  expect(
    (screen.getByRole("combobox", { name: "Account" }) as HTMLSelectElement)
      .dataset.value,
  ).toBe(f.ids.account);
  expect(screen.queryByRole("button", { name: "Apply filters" })).toBeNull();
  expect(f.read.mock.calls[2][0]).toMatchObject({
    accountId: f.ids.account,
    generalChat: true,
    projectId: "",
  });
  fireEvent.change(screen.getByLabelText(/^From \(/), {
    target: { value: "2026-09-25T10:00" },
  });
  fireEvent.change(screen.getByLabelText(/^Until \(/), {
    target: { value: "2026-09-24T10:00" },
  });
  await screen.findByRole("alert");
  expect(f.read).toHaveBeenCalledTimes(3);
});

it.each([false, true])(
  "keeps native-only groups and models out of response views with mixed responses=%s",
  async (mixed) => {
    const f = fixture();
    const native = create(GetUsageSummaryResponseSchema, {
      groups: [
        {
          sessionId: newRequestId(),
          sessionName: "Grok-only session",
          accountId: f.ids.account,
          providerId: f.ids.provider,
          model: { nativeId: newRequestId() },
          modelName: "Grok-only model",
          totals: {
            accounting: [
              {
                kind: AccountingUnitKind.GROK_CLOSED_INPUT,
                units: 1,
                knownTotal: "16",
                measuredUnits: 1,
              },
            ],
          },
        },
      ],
    }).groups[0];
    f.data.accountingProfile = UsageAccountingProfile.NATIVE_UNITS_V1;
    f.data.groups[0].totals = create(UsageTotalsSchema, {
      responses: 1,
      total: { unavailableResponses: 1 },
      accounting: [
        {
          kind: AccountingUnitKind.CODEX_RESPONSE,
          units: 1,
          unavailableUnits: 1,
        },
      ],
    });
    f.data.totals = create(UsageTotalsSchema, {
      responses: mixed ? 1 : 0,
      total: mixed ? { unavailableResponses: 1 } : undefined,
      accounting: [
        ...(mixed ? f.data.groups[0].totals.accounting : []),
        ...native.totals!.accounting,
      ],
    });
    f.data.groups = mixed ? [f.data.groups[0], native] : [native];
    f.data.analytics = create(UsageAnalyticsSchema, {
      granularity: UsageTimeGranularity.DAY,
      timeZone: "UTC",
      days: [
        {
          fromUnixMs: f.data.fromUnixMs,
          untilUnixMs: f.data.untilUnixMs,
          totals: f.data.totals,
        },
      ],
      models: f.data.groups.map(
        ({ providerId, providerName, model, modelName, totals }) => ({
          providerId,
          providerName,
          model,
          modelName,
          totals,
        }),
      ),
    });
    render(f.view());
    await screen.findByText("Incomplete coverage");
    fireEvent.click(screen.getByRole("tab", { name: "Usage history" }));
    const responseSection = screen.getByRole("region", {
      name: "Session, model and account details",
    });
    expect(within(responseSection).queryByText("Grok-only session")).toBeNull();
    if (mixed) {
      expect(within(responseSection).getByRole("table")).toBeTruthy();
      expect(
        within(responseSection).getAllByText("Retained session").at(0),
      ).toBeTruthy();
      expect(
        within(responseSection).getByText(/1 responses · 1 unavailable/),
      ).toBeTruthy();
    } else {
      expect(within(responseSection).queryByRole("table")).toBeNull();
      expect(
        within(responseSection).getByText(
          /No exact response usage is recorded/,
        ),
      ).toBeTruthy();
    }
    const responseChart = screen.getByRole("region", {
      name: "By model / API",
    });
    fireEvent.click(
      within(responseChart).getByRole("button", { name: "View data" }),
    );
    const responseModelTable = within(responseChart).getByRole("table");
    expect(
      within(responseModelTable).queryByText("Grok-only model"),
    ).toBeNull();
    expect(
      within(responseModelTable).queryByText(native.model!.nativeId),
    ).toBeNull();
    expect(within(responseModelTable).getAllByRole("row")).toHaveLength(
      mixed ? 2 : 1,
    );
    if (mixed)
      expect(
        within(responseModelTable).getByText("Original model"),
      ).toBeTruthy();
    fireEvent.click(screen.getByRole("tab", { name: "Usage history" }));
    const nativeSection = screen.getByRole("region", {
      name: "Verified Grok closed inputs",
    });
    expect(
      within(nativeSection).getAllByText("Grok-only session").length,
    ).toBeGreaterThan(0);
    const nativeModelTable = within(nativeSection).getByRole("table", {
      name: "Verified Grok inputs by model",
    });
    expect(
      within(nativeModelTable).getByRole("button", {
        name: "Grok-only model",
      }),
    ).toBeTruthy();
    expect(
      within(nativeModelTable).getAllByText("Grok-only model"),
    ).toHaveLength(1);
    expect(f.data.analytics.models).toHaveLength(mixed ? 2 : 1);
  },
);

it("explains separate response and Grok completion times across a day boundary", async () => {
  const f = fixture();
  const start = f.data.fromUnixMs;
  const day = 86_400_000n;
  const response = create(UsageTotalsSchema, {
    responses: 1,
    total: { knownTotal: "16", measuredResponses: 1 },
    accounting: [
      {
        kind: AccountingUnitKind.CODEX_RESPONSE,
        units: 1,
        knownTotal: "16",
        measuredUnits: 1,
      },
    ],
  });
  const closedInput = create(UsageTotalsSchema, {
    accounting: [
      {
        kind: AccountingUnitKind.GROK_CLOSED_INPUT,
        units: 1,
        knownTotal: "16",
        measuredUnits: 1,
      },
    ],
  });
  const totals = create(UsageTotalsSchema, {
    ...response,
    accounting: [...response.accounting, ...closedInput.accounting],
  });
  f.data.untilUnixMs = start + day * 2n;
  f.data.accountingProfile = UsageAccountingProfile.NATIVE_UNITS_V1;
  f.data.totals = totals;
  f.data.groups[0].totals = totals;
  f.data.analytics = create(UsageAnalyticsSchema, {
    granularity: UsageTimeGranularity.DAY,
    timeZone: "UTC",
    days: [
      { fromUnixMs: start, untilUnixMs: start + day, totals: response },
      {
        fromUnixMs: start + day,
        untilUnixMs: start + day * 2n,
        totals: closedInput,
      },
    ],
    models: [
      {
        providerId: f.ids.provider,
        model: { nativeId: f.ids.model, providerId: f.ids.provider },
        totals,
      },
    ],
  });
  render(f.view());
  await screen.findByText("Incomplete coverage");
  fireEvent.click(screen.getByRole("tab", { name: "Usage history" }));
  const applied = screen.getByRole("group", { name: "Applied conditions" });
  expect(
    within(applied).getByText(
      /^Response times show when the server first retained each response/,
    ),
  ).toBeTruthy();
  fireEvent.click(screen.getByRole("tab", { name: "Usage history" }));
  const nativeSection = screen.getByRole("region", {
    name: "Verified Grok closed inputs",
  });
  expect(
    within(nativeSection).getByText(
      /Grok input times use the server's first retention of verified completion after confirmed cleanup/,
    ),
  ).toBeTruthy();
  expect(
    within(nativeSection).getByText(/Filters and daily buckets use that time/),
  ).toBeTruthy();
  const nativeDays = within(nativeSection)
    .getByRole("table", { name: "Daily verified Grok inputs (UTC)" })
    .querySelectorAll("time");
  expect([...nativeDays].map((time) => time.dateTime)).toEqual([
    new Date(Number(start + day)).toISOString(),
    new Date(Number(start + day * 2n)).toISOString(),
  ]);
});

it("labels a new applied time scope while its result is still loading", async () => {
  const f = fixture();
  render(f.view());
  await screen.findByText("Incomplete coverage");
  fireEvent.click(screen.getByRole("tab", { name: "Usage history" }));
  f.read.mockImplementationOnce(() => new Promise(() => {}));
  fireEvent.change(screen.getByLabelText(/^From \(/), {
    target: { value: "2026-09-23T10:00" },
  });
  fireEvent.change(screen.getByLabelText(/^Until \(/), {
    target: { value: "2026-09-24T10:00" },
  });
  await screen.findByText("Loading token usage…");
  const applied = screen.getByRole("group", { name: "Applied conditions" });
  expect(applied.textContent).toContain("2026");
  expect(applied.textContent).not.toContain("Last 30 days · server time");
  expect(screen.getByRole("status").textContent).toBe("Loading token usage…");
});

function emptyTotals() {
  const empty = {
    knownTotal: "",
    measuredResponses: 0,
    unavailableResponses: 0,
  };
  return create(UsageTotalsSchema, {
    responses: 0,
    total: empty,
    input: empty,
    output: empty,
    cachedInput: empty,
    cacheWriteInput: empty,
    reasoningOutput: empty,
  });
}

it.each([
  [0, 0],
  [12, 0],
  [0, 3],
])(
  "shows six empty recorded summary zeros while preserving missing execution=%s and compaction=%s coverage",
  async (executions, compactions) => {
    const f = fixture();
    f.data.groups = [];
    f.data.totals = emptyTotals();
    f.data.acceptedExecutionsWithoutResponse = executions;
    f.data.acceptedCompactionsWithoutResponse = compactions;
    render(f.view());
    await screen.findByText("Incomplete coverage");
    fireEvent.click(screen.getByRole("tab", { name: "Usage history" }));
    const summary = screen.getByRole("region", { name: "Known token totals" });
    expect(
      [...summary.querySelectorAll("[data-token-measure] dd")].map(
        (value) => value.textContent,
      ),
    ).toEqual(Array(6).fill("0"));
    expect(
      within(summary).getAllByText("0 measured · 0 unavailable responses"),
    ).toHaveLength(6);
    expect(
      screen.getByText(new RegExp(`${executions} accepted executions`)),
    ).toBeTruthy();
    expect(
      screen.getByText(new RegExp(`${compactions} native context actions`)),
    ).toBeTruthy();
    expect(f.data.totals.total!.knownTotal).toBe("");
    expect(f.data.totals.total!.measuredResponses).toBe(0);
    f.read.mockRejectedValueOnce(
      new ConnectError("Fixture unavailable", Code.Unavailable),
    );
    fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
    await screen.findByText(/These are the last successfully retrieved values/);
    expect(
      [...summary.querySelectorAll("[data-token-measure] dd")].map(
        (value) => value.textContent,
      ),
    ).toEqual(Array(6).fill("0"));
    expect(
      screen.getByText(/Stale values from the last successful/),
    ).toBeTruthy();
  },
);

it.each([
  "missing measure",
  "unavailable count",
  "measured count",
  "nonempty subtotal",
  "malformed subtotal",
  "recorded response",
])("does not apply the empty summary exception with %s", async (invalid) => {
  const f = fixture();
  f.data.groups = [];
  const totals = emptyTotals();
  if (invalid === "missing measure") totals.reasoningOutput = undefined;
  if (invalid === "unavailable count")
    totals.reasoningOutput!.unavailableResponses = 1;
  if (invalid === "measured count")
    totals.reasoningOutput!.measuredResponses = 1;
  if (invalid === "nonempty subtotal") totals.reasoningOutput!.knownTotal = "0";
  if (invalid === "malformed subtotal")
    totals.reasoningOutput!.knownTotal = "invalid";
  if (invalid === "recorded response") {
    totals.responses = 1;
    for (const key of [
      "total",
      "input",
      "output",
      "cachedInput",
      "cacheWriteInput",
      "reasoningOutput",
    ] as const)
      totals[key]!.unavailableResponses = 1;
  }
  f.data.totals = totals;
  render(f.view());
  await screen.findByText("Incomplete coverage");
  fireEvent.click(screen.getByRole("tab", { name: "Usage history" }));
  const summary = screen.getByRole("region", { name: "Known token totals" });
  expect(
    [...summary.querySelectorAll("[data-token-measure] dd")].map(
      (value) => value.textContent,
    ),
  ).toEqual(Array(6).fill("Unavailable"));
});

it("does not invent zero for empty telemetry and marks retained data stale after a failed refresh", async () => {
  const f = fixture();
  f.data.groups = [];
  f.data.totals = undefined;
  render(f.view());
  await screen.findByText(/No exact response usage is recorded/);
  fireEvent.click(screen.getByRole("tab", { name: "Usage history" }));
  expect(
    screen
      .getByRole("region", { name: "Known token totals" })
      .querySelectorAll("[data-token-measure] dd"),
  ).toHaveLength(6);
  expect(
    [
      ...screen
        .getByRole("region", { name: "Known token totals" })
        .querySelectorAll("[data-token-measure] dd"),
    ].every((value) => value.textContent === "Unavailable"),
  ).toBe(true);
  f.read.mockRejectedValueOnce(
    new ConnectError("Fixture unavailable", Code.Unavailable),
  );
  fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
  await screen.findByText(/These are the last successfully retrieved values/);
  expect(f.read).toHaveBeenCalledTimes(2);
  expect(screen.getByRole("alert").textContent).toContain(
    "failed read does not establish zero usage or cost",
  );
});

it("renders daily zero, unavailable and empty evidence with keyboard detail and complete model tables", async () => {
  const f = fixture();
  const start = BigInt(Date.UTC(2026, 8, 1));
  const day = 86_400_000n;
  const unavailable = {
    responses: 1,
    total: { knownTotal: "", measuredResponses: 0, unavailableResponses: 1 },
  };
  const models = [100, 90, 80, 70, 60, 0, 0].map((total, index) => ({
    providerId: newRequestId(),
    model: { nativeId: newRequestId() },
    providerName: "Shared API",
    modelName: `Model ${index + 1}`,
    totals: {
      responses: 1,
      total: {
        knownTotal: String(total),
        measuredResponses: 1,
        unavailableResponses: 0,
      },
    },
  }));
  models.push({
    providerId: newRequestId(),
    model: { nativeId: newRequestId() },
    providerName: "Retained API",
    modelName: "Unmeasured model",
    totals: {
      responses: 1,
      total: { knownTotal: "", measuredResponses: 0, unavailableResponses: 1 },
    },
  });
  f.data.analytics = create(UsageAnalyticsSchema, {
    granularity: UsageTimeGranularity.DAY,
    timeZone: "UTC",
    days: [
      {
        fromUnixMs: start,
        untilUnixMs: start + day,
        totals: {
          responses: 1,
          total: {
            knownTotal: "0",
            measuredResponses: 1,
            unavailableResponses: 0,
          },
        },
      },
      {
        fromUnixMs: start + day,
        untilUnixMs: start + day * 2n,
        totals: unavailable,
      },
      {
        fromUnixMs: start + day * 2n,
        untilUnixMs: start + day * 3n,
        totals: {
          responses: 0,
          total: {
            knownTotal: "",
            measuredResponses: 0,
            unavailableResponses: 0,
          },
        },
      },
    ],
    models,
    otherModels: {
      modelCount: 2,
      totals: {
        responses: 2,
        total: {
          knownTotal: "0",
          measuredResponses: 2,
          unavailableResponses: 0,
        },
      },
    },
  });
  render(f.view());
  await screen.findByText("Incomplete coverage");
  fireEvent.click(screen.getByRole("tab", { name: "Usage history" }));
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
  expect(
    screen
      .getAllByRole("status")
      .some((element) =>
        element.textContent?.includes(
          "1 responses · 0 measured · 1 unavailable",
        ),
      ),
  ).toBe(true);
  fireEvent.keyDown(daily, { key: "End" });
  expect(screen.getByText(/No responses recorded/)).toBeTruthy();
  fireEvent.keyDown(daily, { key: "Escape" });
  expect(screen.getByText(/Move focus to a chart point/)).toBeTruthy();
  const toggles = screen.getAllByRole("button", { name: "View data" });
  fireEvent.click(toggles[0]);
  const dailyTable = screen.getByRole("table", {
    name: /All calendar-day intervals/,
  });
  expect(
    screen
      .getByRole("region", { name: /Daily usage data table/ })
      .getAttribute("tabindex"),
  ).toBe("0");
  expect(within(dailyTable).getAllByRole("row")).toHaveLength(4);
  for (const label of [
    "Known total",
    "Input",
    "Cached input",
    "Cache-write input",
    "Output",
    "Reasoning output",
  ])
    expect(
      within(dailyTable).getByRole("columnheader", { name: label }),
    ).toBeTruthy();
  const modelToggle = screen.getByRole("button", { name: "View data" });
  fireEvent.click(modelToggle);
  const modelTable = screen.getByRole("table", {
    name: /Every original provider and model group/,
  });
  expect(
    screen
      .getByRole("region", { name: /Model usage data table/ })
      .getAttribute("tabindex"),
  ).toBe("0");
  expect(within(modelTable).getAllByRole("row")).toHaveLength(10);
  for (const label of [
    "Known total",
    "Input",
    "Cached input",
    "Cache-write input",
    "Output",
    "Reasoning output",
  ])
    expect(
      within(modelTable).getByRole("columnheader", { name: label }),
    ).toBeTruthy();
  expect(within(modelTable).getByText("Unmeasured model")).toBeTruthy();
  expect(
    within(modelTable).getByText(/Other models \(2 measured groups\)/),
  ).toBeTruthy();
  expect(f.read.mock.calls[0][0]).toMatchObject({
    granularity: UsageTimeGranularity.DAY,
    timeZone: expect.any(String),
  });
});

it("keeps historical currency subtotals, partial coverage and source basis separate from actual cost", async () => {
  const f = fixture();
  f.data.estimates = create(EstimateTotalsSchema, {
    unpricedResponses: 1,
    currencies: [
      {
        currency: "USD",
        knownAmount: "9223.372036854775807",
        completeResponses: 1,
      },
      { currency: "EUR", knownAmount: "0", partialResponses: 1 },
    ],
  });
  f.data.pricing = [
    create(PricingUsageSchema, {
      pricing: {
        id: newRequestId(),
        model: { nativeId: f.ids.model, providerId: f.ids.provider },
        providerId: f.ids.provider,
        revision: 1n,
        basis: {
          currency: "USD",
          source: "Retained original source",
          asOf: "2026-09-01",
          inputMode: InputPricingMode.UNIFORM,
          inputPerMillion: "0.000000001",
          exclusions: ["Fixture fee excluded"],
        },
      },
      totals: {
        currency: "USD",
        knownAmount: "9223.372036854775807",
        completeResponses: 1,
      },
      input: {
        knownTokens: "9223372036854775807",
        knownAmount: "9223.372036854775807",
        pricedResponses: 1,
      },
      output: { missingPriceResponses: 1 },
    }),
  ];
  render(f.view());
  const overview = screen.getByRole("tabpanel", { name: "Overview" });
  await within(overview).findByText("USD 9,223.372036854775807");
  fireEvent.click(screen.getByRole("tab", { name: "Usage history" }));
  const history = screen.getByRole("tabpanel", { name: "Usage history" });
  expect(within(history).getByText("EUR 0")).toBeTruthy();
  expect(
    screen.getByText(/1 responses have no matching historical price/),
  ).toBeTruthy();
  expect(
    screen.getByText("Actual API cost:").parentElement!.textContent,
  ).toContain("Unavailable");
  fireEvent.click(screen.getByText(/Historical basis · USD/));
  expect(screen.getByText("Retained original source")).toBeTruthy();
  expect(screen.getByText("Fixture fee excluded")).toBeTruthy();
  expect(
    screen.getByText(BigInt("9223372036854775807").toLocaleString()),
  ).toBeTruthy();
});

it("consumes an account entry once, preserves its exact bounds and retains subsequent filter edits", async () => {
  const f = fixture();
  const entry = {
    key: newRequestId(),
    accountId: f.ids.account,
    fromUnixMs: f.data.fromUnixMs + 123n,
    untilUnixMs: f.data.untilUnixMs + 789n,
  };
  // A stable transport matches same-identity reconnect ownership.
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity } },
  });
  const transport = createRouterTransport((router) => {
    router.service(SystemService, { getStatus: () => ({}) });
    router.service(UsageService, { getUsageSummary: f.read });
    router.service(ResourceService, {
      listResources: () => ({ resources: [] }),
    });
  });
  const viewFor = (active = true, value = entry) => (
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <Usage active={active} open={f.open} entry={value} />
      </QueryClientProvider>
    </TransportProvider>
  );
  const view = render(viewFor());
  await screen.findByText("Incomplete coverage");
  fireEvent.click(screen.getByRole("tab", { name: "Usage history" }));
  expect(f.read).toHaveBeenCalledTimes(1);
  expect(f.read.mock.calls[0][0]).toMatchObject({
    accountId: entry.accountId,
    fromUnixMs: entry.fromUnixMs,
    untilUnixMs: entry.untilUnixMs,
  });
  fireEvent.change(screen.getByLabelText(/^From \(/), {
    target: { value: "2026-09-01T10:00" },
  });
  view.rerender(viewFor(false));
  view.rerender(viewFor());
  expect((screen.getByLabelText(/^From \(/) as HTMLInputElement).value).toBe(
    "2026-09-01T10:00",
  );
  expect(f.read).toHaveBeenCalledTimes(1);
  const next = { ...entry, key: newRequestId(), accountId: newRequestId() };
  view.rerender(viewFor(true, next));
  await waitFor(() => expect(f.read).toHaveBeenCalledTimes(2));
  expect(f.read.mock.calls[1][0].accountId).toBe(next.accountId);
});

it("retains the drawer and input focus on automatic dates and selections, while Refresh keeps the applied range", async () => {
  const f = fixture();
  const closeDrawer = vi.fn();
  render(
    <SidebarOutletProvider target={null} drawerOpen closeDrawer={closeDrawer}>
      {f.view()}
    </SidebarOutletProvider>,
  );
  await screen.findByText("Incomplete coverage");
  fireEvent.click(screen.getByRole("tab", { name: "Usage history" }));
  const input = screen.getByLabelText(/^From \(/) as HTMLInputElement;
  input.focus();
  fireEvent.change(input, { target: { value: "2026-09-01T10:00" } });
  expect(document.activeElement).toBe(input);
  expect(screen.getAllByText(/Waiting for date input/).length).toBeGreaterThan(
    0,
  );
  fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
  await waitFor(() => expect(f.read).toHaveBeenCalledTimes(2));
  expect(f.read.mock.calls[1][0].fromUnixMs).toBe(0n);
  fireEvent.change(screen.getByLabelText(/^Until \(/), {
    target: { value: "2026-09-02T10:00" },
  });
  await waitFor(() => expect(f.read).toHaveBeenCalledTimes(3));
  expect(closeDrawer).not.toHaveBeenCalled();
  expect(document.activeElement).toBe(input);
  fireEvent.click(screen.getByRole("checkbox", { name: "General Chat only" }));
  await waitFor(() => expect(f.read).toHaveBeenCalledTimes(4));
  expect(closeDrawer).not.toHaveBeenCalled();
  expect(screen.queryByText(/Waiting for date input/)).toBeNull();
});

it("keeps current query results when an older scope resolves later, without showing old data under new conditions", async () => {
  const f = fixture();
  render(f.view());
  await screen.findByText("Incomplete coverage");
  fireEvent.click(screen.getByRole("tab", { name: "Usage history" }));
  let finishA!: (data: typeof f.data) => void;
  let finishB!: (data: typeof f.data) => void;
  f.read.mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        finishA = resolve;
      }),
  );
  fireEvent.click(screen.getByRole("checkbox", { name: "General Chat only" }));
  await waitFor(() => expect(f.read).toHaveBeenCalledTimes(2));
  expect(screen.queryByText("Retained session")).toBeNull();
  expect(screen.getByText("Loading token usage…")).toBeTruthy();
  f.read.mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        finishB = resolve;
      }),
  );
  await chooseScrollOption(
    screen.getByRole("combobox", { name: "Account" }),
    f.ids.account,
  );
  await waitFor(() => expect(f.read).toHaveBeenCalledTimes(3));
  const b = create(GetUsageSummaryResponseSchema, f.data);
  b.groups[0].sessionName = "Latest scope";
  finishB(b);
  await screen.findAllByText("Latest scope");
  const a = create(GetUsageSummaryResponseSchema, f.data);
  a.groups[0].sessionName = "Earlier scope";
  finishA(a);
  await waitFor(() =>
    expect(screen.queryByText("Loading token usage…")).toBeNull(),
  );
  expect(screen.getAllByText("Latest scope").length).toBeGreaterThan(0);
  expect(screen.queryByText("Earlier scope")).toBeNull();
  expect(
    screen.getByRole("group", { name: "Applied conditions" }).textContent,
  ).toContain("Original account");
});

it("does not reuse old scope data when a new valid selection fails", async () => {
  const f = fixture();
  render(f.view());
  await screen.findByText("Incomplete coverage");
  fireEvent.click(screen.getByRole("tab", { name: "Usage history" }));
  f.read.mockRejectedValueOnce(
    new ConnectError("Fixture unavailable", Code.Unavailable),
  );
  fireEvent.click(screen.getByRole("checkbox", { name: "General Chat only" }));
  await waitFor(() => expect(f.read).toHaveBeenCalledTimes(2));
  await screen.findByRole("alert");
  expect(screen.queryByText("Retained session")).toBeNull();
  expect(
    screen.queryByText(/These are the last successfully retrieved values/),
  ).toBeNull();
});

it("defaults to Overview, keeps three topmost semantic tabs and retains state without summary reads", async () => {
  const f = fixture();
  const view = render(f.view());
  await screen.findByText("Incomplete coverage");
  const tabs = screen.getAllByRole("tab");
  expect(document.querySelector(".usage-page")!.firstElementChild).toBe(
    tabs[0].parentElement,
  );
  expect(tabs.map((tab) => tab.textContent)).toEqual([
    "Overview",
    "Usage history",
    "Model prices",
  ]);
  expect(tabs.map((tab) => tab.getAttribute("aria-selected"))).toEqual([
    "true",
    "false",
    "false",
  ]);
  expect(tabs.map((tab) => tab.tabIndex)).toEqual([0, -1, -1]);
  expect(tabs[0].parentElement!.classList.contains("tab-strip")).toBe(true);
  expect(
    tabs.map((tab) => [
      tab.classList.contains("tab-item"),
      tab.classList.contains("tab-label"),
      tab.classList.contains("is-selected"),
    ]),
  ).toEqual([
    [true, true, true],
    [true, true, false],
    [true, true, false],
  ]);
  expect(
    screen.queryByRole("button", { name: "Model details and token pricing" }),
  ).toBeNull();
  tabs[0].focus();
  fireEvent.keyDown(tabs[0], { key: "ArrowLeft" });
  expect(document.activeElement).toBe(tabs[2]);
  expect(screen.getByRole("tabpanel", { name: "Model prices" })).toBeTruthy();
  fireEvent.keyDown(tabs[2], { key: "Home" });
  expect(document.activeElement).toBe(tabs[0]);
  fireEvent.keyDown(tabs[0], { key: "ArrowRight" });
  expect(document.activeElement).toBe(tabs[1]);
  expect(
    screen.getByRole("region", { name: "Known token totals" }),
  ).toBeTruthy();
  fireEvent.keyDown(tabs[1], { key: "End" });
  expect(document.activeElement).toBe(tabs[2]);
  view.rerender(f.view(false));
  view.rerender(f.view());
  expect(
    screen
      .getByRole("tab", { name: "Model prices" })
      .getAttribute("aria-selected"),
  ).toBe("true");
  expect(f.read).toHaveBeenCalledTimes(1);
});

it("shows supported empty source rows collapsed and retains a user source choice across navigation and refresh", async () => {
  const f = fixture();
  f.data.accountingProfile = UsageAccountingProfile.NATIVE_UNITS_V1;
  f.data.nativeAccounting = create(GetUsageSummaryResponseSchema, {
    nativeAccounting: [
      { totals: { kind: AccountingUnitKind.CLAUDE_MAIN_LOOP_INPUT } },
      {
        totals: {
          kind: AccountingUnitKind.OPENCODE_STEP,
          units: 1,
          input: { knownTotal: "9007199254740993", measuredUnits: 1 },
        },
      },
    ],
  }).nativeAccounting;
  const view = render(f.view());
  await screen.findByText("Incomplete coverage");
  fireEvent.click(screen.getByRole("tab", { name: "Usage history" }));
  fireEvent.click(screen.getByRole("tab", { name: "Usage history" }));
  const panel = screen.getByRole("tabpanel", { name: "Usage history" });
  const claude = within(panel).getByRole("region", {
    name: "Claude main-loop inputs",
  });
  const grok = within(panel).getByRole("region", {
    name: "Verified Grok closed inputs",
  });
  const opencode = within(panel).getByRole("region", {
    name: "OpenCode steps",
  });
  expect(claude.querySelector("details")!.open).toBe(false);
  expect(grok.querySelector("details")!.open).toBe(false);
  expect(opencode.querySelector("details")!.open).toBe(true);
  expect(
    within(claude).getAllByText(
      "No records does not establish zero usage or cost",
    ).length,
  ).toBeGreaterThan(0);
  const disclosure = opencode.querySelector("details")!;
  disclosure.open = false;
  fireEvent(disclosure, new Event("toggle"));
  view.rerender(f.view(false));
  view.rerender(f.view());
  expect(
    screen
      .getByRole("tab", { name: "Usage history" })
      .getAttribute("aria-selected"),
  ).toBe("true");
  expect(disclosure.open).toBe(false);
  fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
  await waitFor(() => expect(f.read).toHaveBeenCalledTimes(2));
  expect(disclosure.open).toBe(false);
  expect(
    within(panel).getByText(BigInt("9007199254740993").toLocaleString()),
  ).toBeTruthy();
});
it("keeps original identities and complete counter/estimate evidence in mounted compact row Details", async () => {
  const f = fixture();
  f.data.groups[0].projectId = newRequestId();
  f.data.groups[0].projectName = "Original project";
  f.data.groups[0].estimates = create(EstimateTotalsSchema, {
    currencies: [
      { currency: "USD", knownAmount: "0.002", partialResponses: 1 },
      { currency: "EUR", knownAmount: "0", completeResponses: 1 },
    ],
    unpricedResponses: 1,
  });
  const view = render(f.view());
  await screen.findByText("Incomplete coverage");
  fireEvent.click(screen.getByRole("tab", { name: "Usage history" }));
  const row = document.querySelector<HTMLTableRowElement>(
      ".usage-detail tbody tr",
    )!,
    detail = row.querySelector<HTMLDetailsElement>("details")!,
    summary = detail.querySelector("summary")!;
  expect(row.querySelectorAll("td")).toHaveLength(9);
  for (const cell of [...row.cells].slice(0, 3))
    for (const id of Object.values(f.ids))
      expect(cell.textContent).not.toContain(id);
  fireEvent.click(summary);
  expect(detail.open).toBe(true);
  expect(detail.querySelectorAll("[data-token-measure]")).toHaveLength(6);
  for (const id of [
    f.ids.session,
    f.ids.account,
    f.ids.provider,
    f.data.groups[0].projectId,
  ])
    expect(detail.textContent).not.toContain(id);
  // The native model ID is original source identity and stays verbatim.
  expect(detail.textContent).toContain(f.ids.model);
  expect(detail.textContent).toContain("USD 0.002");
  expect(detail.textContent).toContain("EUR 0");
  expect(f.read).toHaveBeenCalledTimes(1);
  view.rerender(f.view(false));
  view.rerender(f.view());
  expect(row.querySelector("details")).toBe(detail);
  expect(detail.open).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
  await waitFor(() => expect(f.read).toHaveBeenCalledTimes(2));
  expect(row.querySelector("details")).toBe(detail);
  expect(detail.open).toBe(true);
});

it.each([
  {
    language: SupportedLanguage.English,
    profile: UsageAccountingProfile.UNSPECIFIED,
  },
  {
    language: SupportedLanguage.Korean,
    profile: UsageAccountingProfile.UNSPECIFIED,
  },
  {
    language: SupportedLanguage.English,
    profile: UsageAccountingProfile.NATIVE_UNITS_V1,
  },
  {
    language: SupportedLanguage.Korean,
    profile: UsageAccountingProfile.NATIVE_UNITS_V1,
  },
])(
  "preserves response-ledger Overview values in $language with profile=$profile",
  async ({ language, profile }) => {
    await i18n.changeLanguage(language);
    const f = fixture();
    f.data.accountingProfile = profile;
    f.data.estimates = create(EstimateTotalsSchema, {
      currencies: [
        { currency: "USD", knownAmount: "0.270966", completeResponses: 2 },
        { currency: "EUR", knownAmount: "0", completeResponses: 1 },
      ],
    });
    render(f.view());
    const english = language === SupportedLanguage.English;
    const panel = await screen.findByRole("tabpanel", {
      name: english ? "Overview" : "개요",
    });
    await within(panel).findByText("USD 0.270966");
    const row = within(panel)
      .getByRole("rowheader", { name: "Codex" })
      .closest("tr")!;
    expect(row.cells[1].textContent).toContain(
      BigInt("18446744073709551614").toLocaleString(
        english ? "en-US" : "ko-KR",
      ),
    );
    expect(row.cells[2].textContent).toContain("EUR 0");
    expect(row.cells[3].textContent).toBe(
      english ? "3 recorded units" : "기록 3개",
    );
    for (const name of ["Claude Code", "OpenCode"])
      expect(
        within(panel).getByRole("rowheader", { name }).closest("tr")!.cells[3]
          .textContent,
      ).toBe(
        english
          ? "Unavailable · update or inspect source evidence"
          : "사용 불가 · 업데이트하거나 원본 근거를 확인하세요",
      );
    expect(f.read).toHaveBeenCalledTimes(1);
  },
);

it.each([
  {
    evidence: "absent totals",
    tokens: "—",
    status: "Unavailable · update or inspect source evidence",
    missingTotal: false,
  },
  {
    evidence: "unmeasured total",
    tokens: "—",
    status: "3 recorded units",
    missingTotal: true,
  },
  {
    evidence: "absent total measure",
    tokens: "—",
    status: "3 recorded units",
    missingTotal: true,
  },
  {
    evidence: "measured zero",
    tokens: "0",
    status: "3 recorded units",
    missingTotal: false,
  },
  {
    evidence: "empty interval",
    tokens: "—",
    status: "No recorded usage",
    missingTotal: false,
  },
])(
  "keeps legacy Overview $evidence distinct from measured usage",
  async ({ evidence, tokens, status, missingTotal }) => {
    const f = fixture();
    if (evidence === "absent totals") f.data.totals = undefined;
    if (evidence === "unmeasured total")
      f.data.totals!.total!.measuredResponses = 0;
    if (evidence === "absent total measure") f.data.totals!.total = undefined;
    if (evidence === "measured zero") f.data.totals!.total!.knownTotal = "0";
    if (evidence === "empty interval")
      f.data.totals = create(UsageTotalsSchema);
    render(f.view());
    await screen.findByText("Incomplete coverage");
    const row = screen.getByRole("rowheader", { name: "Codex" }).closest("tr")!;
    expect(row.cells[1].firstChild!.textContent).toBe(tokens);
    expect(row.cells[2].firstChild!.textContent).toBe("—");
    expect(
      row.cells[2].textContent.includes("No retained price estimate."),
    ).toBe(f.data.totals !== undefined && f.data.totals.responses > 0);
    expect(row.cells[3].firstChild!.textContent).toBe(status);
    expect(
      row.cells[3].textContent.includes("The tool did not report a total."),
    ).toBe(missingTotal);
  },
);

it("retains legacy Codex Overview values and coverage after a failed refresh", async () => {
  const f = fixture();
  f.data.estimates = create(EstimateTotalsSchema, {
    currencies: [
      { currency: "USD", knownAmount: "2.50", completeResponses: 2 },
    ],
  });
  render(f.view());
  const overview = screen.getByRole("tabpanel", { name: "Overview" });
  await within(overview).findByText("USD 2.50");
  const row = within(overview)
    .getByRole("rowheader", { name: "Codex" })
    .closest("tr")!;
  const retained = row.textContent;
  f.read.mockRejectedValueOnce(
    new ConnectError("Fixture unavailable", Code.Unavailable),
  );
  fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
  await screen.findByText(/These are the last successfully retrieved values/);
  expect(row.textContent).toBe(retained);
  expect(screen.getByText("Incomplete coverage")).toBeTruthy();
  expect(screen.getByRole("alert").textContent).toContain(
    "failed read does not establish zero usage or cost",
  );
  expect(f.read).toHaveBeenCalledTimes(2);
});

it.each(["response", "native"] as const)(
  "hands focus to Model prices once per explicit %s model activation",
  async (source) => {
    const f = fixture();
    if (source === "native") {
      f.data.accountingProfile = UsageAccountingProfile.NATIVE_UNITS_V1;
      f.data.nativeAccounting = create(GetUsageSummaryResponseSchema, {
        nativeAccounting: [
          { totals: { kind: AccountingUnitKind.CLAUDE_MAIN_LOOP_INPUT } },
          {
            totals: { kind: AccountingUnitKind.OPENCODE_STEP, units: 1 },
            models: [
              {
                model: {
                  nativeId: f.ids.model,
                  subscriptionService: SubscriptionServiceIdentity.OPENCODE_GO,
                },
                subscriptionService: SubscriptionServiceIdentity.OPENCODE_GO,
                totals: { kind: AccountingUnitKind.OPENCODE_STEP, units: 1 },
              },
            ],
          },
        ],
      }).nativeAccounting;
    }
    const view = render(f.view());
    await screen.findByText("Incomplete coverage");
    const historyTab = screen.getByRole("tab", { name: "Usage history" });
    const pricesTab = screen.getByRole("tab", { name: "Model prices" });
    const focus = vi.spyOn(pricesTab, "focus");
    for (let activation = 1; activation <= 2; activation++) {
      fireEvent.click(historyTab);
      const history = screen.getByRole("tabpanel", { name: "Usage history" });
      const model = within(history).getByRole("button", {
        name:
          source === "response" ? "Original model" : "Model unavailable · 1",
      });
      model.focus();
      fireEvent.click(model);
      expect(document.activeElement).toBe(pricesTab);
      expect(pricesTab.getAttribute("aria-selected")).toBe("true");
      expect(history.hidden).toBe(true);
      expect(focus).toHaveBeenCalledTimes(activation);
      const panel = screen.getByRole("tabpanel", { name: "Model prices" });
      const selector = within(panel).getByRole("combobox", {
        name: "Pricing model",
      });
      expect(selector.getAttribute("data-value")).toBe(
        priceIdentity({
          nativeId: f.ids.model,
          providerId: source === "response" ? f.ids.provider : "",
          subscriptionService:
            source === "response"
              ? SubscriptionServiceIdentity.UNSPECIFIED
              : SubscriptionServiceIdentity.OPENCODE_GO,
        }),
      );
      expect(selector.textContent).not.toContain(f.ids.model);
      expect(selector.textContent).toContain(
        source === "response" ? "Original API" : "OpenCode Go",
      );
    }
    const reset = screen.getByRole("button", { name: "Reset to last 30 days" });
    reset.focus();
    view.rerender(f.view());
    expect(document.activeElement).toBe(reset);
    fireEvent.click(historyTab);
    fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
    await waitFor(() => expect(f.read).toHaveBeenCalledTimes(2));
    expect(document.activeElement).toBe(reset);
    fireEvent.click(pricesTab);
    expect(document.activeElement).toBe(reset);
    expect(focus).toHaveBeenCalledTimes(2);
    expect(f.read).toHaveBeenCalledTimes(2);
  },
);

it.each(["inactive", "hidden"] as const)(
  "discards a model focus handoff when Usage becomes %s before commit",
  async (departure) => {
    const f = fixture();
    const viewFor = (departed = false) => (
      <>
        <button type="button">Outside Usage</button>
        <div hidden={departed && departure === "hidden"}>
          {f.view(!(departed && departure === "inactive"))}
        </div>
      </>
    );
    const view = render(viewFor());
    await screen.findByText("Incomplete coverage");
    fireEvent.click(screen.getByRole("tab", { name: "Usage history" }));
    const model = screen.getByRole("button", { name: "Original model" });
    const pricesTab = screen.getByRole("tab", { name: "Model prices" });
    const focus = vi.spyOn(pricesTab, "focus");
    const outside = screen.getByRole("button", { name: "Outside Usage" });
    outside.focus();
    act(() => {
      fireEvent.click(model);
      view.rerender(viewFor(true));
    });
    expect(document.activeElement).toBe(outside);
    expect(focus).not.toHaveBeenCalled();
    view.rerender(viewFor());
    expect(document.activeElement).toBe(outside);
    expect(focus).not.toHaveBeenCalled();
    expect(pricesTab.getAttribute("aria-selected")).toBe("true");
  },
);

it.each([
  { profile: UsageAccountingProfile.UNSPECIFIED, completeNative: true },
  { profile: UsageAccountingProfile.NATIVE_UNITS_V1, completeNative: true },
  { profile: UsageAccountingProfile.NATIVE_UNITS_V1, completeNative: false },
])(
  "evaluates independent source evidence with profile=$profile and complete native summaries=$completeNative",
  async ({ profile, completeNative }) => {
    const f = fixture();
    f.data.accountingProfile = profile;
    f.data.totals = create(UsageTotalsSchema, {
      responses: 16,
      total: { knownTotal: "269410", measuredResponses: 16 },
      accounting: [
        {
          kind: AccountingUnitKind.GROK_CLOSED_INPUT,
          units: 1,
          knownTotal: "125",
          measuredUnits: 1,
        },
      ],
    });
    f.data.estimates = create(EstimateTotalsSchema, {
      currencies: [
        { currency: "USD", knownAmount: "0.270966", completeResponses: 16 },
        { currency: "EUR", knownAmount: "0", completeResponses: 1 },
      ],
    });
    f.data.nativeAccounting = create(GetUsageSummaryResponseSchema, {
      nativeAccounting: [
        {
          totals: {
            kind: AccountingUnitKind.CLAUDE_MAIN_LOOP_INPUT,
            units: 1,
            input: { knownTotal: "456", measuredUnits: 1 },
          },
        },
        {
          totals: {
            kind: AccountingUnitKind.OPENCODE_STEP,
            units: 1,
            total: { knownTotal: "0", measuredUnits: 1 },
          },
        },
      ],
    }).nativeAccounting;
    if (!completeNative) f.data.nativeAccounting.pop();
    render(f.view());
    const panel = await screen.findByRole("tabpanel", { name: "Overview" });
    await within(panel).findByText("USD 0.270966");
    const row = (name: string) =>
      within(panel).getByRole("rowheader", { name }).closest("tr")!;
    expect(row("Codex").textContent).toContain("269,410");
    expect(row("Codex").textContent).toContain("EUR 0");
    expect(row("Codex").textContent).toContain("16 recorded units");
    expect(row("Claude Code").textContent).not.toContain("456");
    if (profile === UsageAccountingProfile.NATIVE_UNITS_V1 && completeNative) {
      expect(row("Claude Code").textContent).toContain(
        "The tool did not report a total.",
      );
      expect(row("OpenCode").textContent).toContain("0Native-reported total");
    } else {
      for (const name of ["Claude Code", "OpenCode"])
        expect(row(name).textContent).toContain(
          "Unavailable · update or inspect source evidence",
        );
    }
    if (profile === UsageAccountingProfile.NATIVE_UNITS_V1)
      expect(row("Grok Build").textContent).toContain("125Input tokens");
    else
      expect(row("Grok Build").textContent).toContain(
        "Unavailable · update or inspect source evidence",
      );
    expect(row("Grok Build").textContent).not.toContain("USD");
    expect(f.read).toHaveBeenCalledTimes(1);
  },
);

it("opens exact historical pricing without changing filters and retains read-only deleted evidence", async () => {
  const f = fixture();
  const version = newRequestId();
  f.data.pricing = [
    create(PricingUsageSchema, {
      pricing: {
        id: version,
        model: { nativeId: f.ids.model, providerId: f.ids.provider },
        providerId: f.ids.provider,
        revision: 1n,
        basis: {
          currency: "USD",
          source: "Original deleted price",
          asOf: "2026-09-01",
          inputMode: InputPricingMode.UNIFORM,
          inputPerMillion: "0",
        },
      },
    }),
  ];
  render(f.view());
  await screen.findByText("Incomplete coverage");
  fireEvent.click(screen.getByRole("tab", { name: "Usage history" }));
  const model = screen.getByRole("button", { name: "Original model" });
  model.focus();
  fireEvent.click(model);
  expect(document.activeElement).toBe(
    screen.getByRole("tab", { name: "Model prices" }),
  );
  const panel = screen.getByRole("tabpanel", { name: "Model prices" });
  await within(panel).findByText(
    "This original identity has no editable current model. Historical price evidence is read-only.",
  );
  expect(within(panel).getByText("Original deleted price")).toBeTruthy();
  expect(panel.textContent).toContain("Model price · 1");
  expect(panel.textContent).not.toContain(version);
  expect(
    within(panel).getByRole("button", { name: "Edit token pricing" }),
  ).toHaveProperty("disabled", true);
  expect(f.read).toHaveBeenCalledTimes(1);
  expect(
    screen.getByRole("textbox", {
      name: "Exact native model ID",
      hidden: true,
    }),
  ).toHaveProperty("value", "");
});

it("retains manual price draft and exact uncertain retry across tabs and dashboard Refresh", async () => {
  const f = fixture();
  const model = create(ResourceSchema, {
    id: f.ids.model,
    kind: EntityKind.MODEL,
    schemaVersion: 1,
    revision: 1n,
    documentJson: encode({
      name: "Original model",
      provider_id: f.ids.provider,
      native_id: "native-original",
      harnesses: ["codex"],
      manual: true,
      metadata_source: "user-declared",
    }),
  });
  const basis = {
    currency: "USD",
    source: "Original source",
    asOf: "2026-09-01",
    inputMode: InputPricingMode.UNIFORM,
    inputPerMillion: "1",
  };
  const pricing = create(PricingUsageSchema, {
    pricing: {
      id: newRequestId(),
      model: { nativeId: f.ids.model, providerId: f.ids.provider },
      providerId: f.ids.provider,
      revision: 1n,
      basis,
    },
  }).pricing!;
  const write = vi
    .fn()
    .mockRejectedValueOnce(
      new ConnectError("Lost price response", Code.Unavailable),
    )
    .mockImplementation((request) => ({
      pricing,
      requestId: request.requestId,
      policy: { mode: TokenPricingMode.MANUAL, revision: 1n },
    }));
  const transport = createRouterTransport((router) => {
    router.service(UsageService, {
      getUsageSummary: f.read,
      getTokenPricing: () => ({
        pricing,
        providerRevision: 1n,
        policy: { mode: TokenPricingMode.MANUAL, revision: 0n },
        reference: { state: "current" },
      }),
      setTokenPricing: write,
    });
    router.service(ResourceService, {
      getResource: () => ({ resource: model }),
      listResources: (request) => ({
        resources: request.filter?.kind === EntityKind.MODEL ? [model] : [],
      }),
    });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity } },
  });
  render(
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <Usage active open={f.open} />
      </QueryClientProvider>
    </TransportProvider>,
  );
  await screen.findByText("Incomplete coverage");
  fireEvent.click(screen.getByRole("tab", { name: "Usage history" }));
  fireEvent.click(screen.getByRole("button", { name: "Original model" }));
  await waitFor(() =>
    expect(
      (
        screen.getByRole("button", {
          name: "Edit token pricing",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(false),
  );
  fireEvent.click(screen.getByRole("button", { name: "Edit token pricing" }));
  fireEvent.change(screen.getByLabelText("Input rate per million"), {
    target: { value: "3" },
  });
  fireEvent.click(screen.getByRole("tab", { name: "Overview" }));
  fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
  await waitFor(() => expect(f.read).toHaveBeenCalledTimes(2));
  fireEvent.click(screen.getByRole("tab", { name: "Model prices" }));
  expect(
    (screen.getByLabelText("Input rate per million") as HTMLInputElement).value,
  ).toBe("3");
  fireEvent.click(screen.getByRole("button", { name: "Save pricing version" }));
  await screen.findByRole("button", { name: "Retry the same price" });
  const original = write.mock.calls[0][0];
  fireEvent.click(screen.getByRole("tab", { name: "Usage history" }));
  fireEvent.click(screen.getByRole("tab", { name: "Model prices" }));
  fireEvent.click(screen.getByRole("button", { name: "Retry the same price" }));
  await waitFor(() => expect(write).toHaveBeenCalledTimes(2));
  expect(write.mock.calls[1][0]).toEqual(original);
  expect(f.read).toHaveBeenCalledTimes(2);
});

it("links every tab to a mounted panel while the initial summary is pending", () => {
  const f = fixture();
  f.read.mockImplementationOnce(() => new Promise(() => {}));
  render(f.view());
  for (const tab of screen.getAllByRole("tab"))
    expect(
      document.getElementById(tab.getAttribute("aria-controls")!),
    ).toBeTruthy();
  fireEvent.click(screen.getByRole("tab", { name: "Usage history" }));
  expect(screen.getByRole("tabpanel", { name: "Usage history" })).toBeTruthy();
});

it.each(
  [SupportedLanguage.English, SupportedLanguage.Korean].flatMap((language) =>
    ["Asia/Seoul", "UTC", "America/Argentina/Buenos_Aires"].map((timeZone) => ({
      language,
      timeZone,
    })),
  ),
)(
  "shows the exact noninteractive timezone chip in $language for $timeZone",
  async ({ language, timeZone }) => {
    const resolved = Intl.DateTimeFormat.prototype.resolvedOptions;
    vi.spyOn(
      Intl.DateTimeFormat.prototype,
      "resolvedOptions",
    ).mockImplementation(function (this: Intl.DateTimeFormat) {
      return { ...resolved.call(this), timeZone };
    });
    await i18n.changeLanguage(language);
    const f = fixture();
    const view = render(f.view());
    await waitFor(() => expect(f.read).toHaveBeenCalled());
    const chip = view.container.querySelector<HTMLElement>(
      ".usage-timezone-chip",
    )!;
    expect(chip.querySelector('[aria-hidden="true"]')?.textContent).toBe(
      timeZone,
    );
    expect(chip.querySelector(".usage-sr-only")?.textContent).toBe(
      `${language === SupportedLanguage.Korean ? "시간대" : "Timezone"}: ${timeZone}`,
    );
    expect(chip.tabIndex).toBe(-1);
    expect(chip.hasAttribute("role")).toBe(false);
    expect(chip.hasAttribute("tabindex")).toBe(false);
    expect(chip.querySelector("button,input,a,svg")).toBeNull();
    expect(view.container.textContent).not.toContain(
      "Last 30 days by default · Times use",
    );
    expect(view.container.textContent).not.toContain(
      "기본 범위는 최근 30일 · 시간대",
    );
    const labels = [
      ...view.container.querySelectorAll(".usage-sidebar label"),
    ].map((label) => label.textContent);
    expect(labels.some((label) => label?.includes(timeZone))).toBe(true);
    expect(f.read.mock.calls[0][0].timeZone).toBe(timeZone);
  },
);

it.each(["loading", "empty", "error"] as const)(
  "retains the selected timezone chip during %s",
  async (state) => {
    const resolved = Intl.DateTimeFormat.prototype.resolvedOptions;
    vi.spyOn(
      Intl.DateTimeFormat.prototype,
      "resolvedOptions",
    ).mockImplementation(function (this: Intl.DateTimeFormat) {
      return { ...resolved.call(this), timeZone: "Asia/Seoul" };
    });
    const f = fixture();
    if (state === "loading")
      f.read.mockImplementation(() => new Promise(() => {}));
    if (state === "empty")
      f.read.mockResolvedValue(
        create(GetUsageSummaryResponseSchema, {
          fromUnixMs: f.data.fromUnixMs,
          untilUnixMs: f.data.untilUnixMs,
        }),
      );
    if (state === "error")
      f.read.mockRejectedValue(
        new ConnectError("Unavailable", Code.Unavailable),
      );
    const view = render(f.view());
    await waitFor(() => expect(f.read).toHaveBeenCalled());
    expect(
      view.container.querySelector('.usage-timezone-chip [aria-hidden="true"]')
        ?.textContent,
    ).toBe("Asia/Seoul");
  },
);

it("keeps the timezone pinned across navigation and language changes until the existing Reset action", async () => {
  let timeZone = "Asia/Seoul";
  const resolved = Intl.DateTimeFormat.prototype.resolvedOptions;
  vi.spyOn(Intl.DateTimeFormat.prototype, "resolvedOptions").mockImplementation(
    function (this: Intl.DateTimeFormat) {
      return { ...resolved.call(this), timeZone };
    },
  );
  const f = fixture();
  const view = render(f.view());
  await waitFor(() => expect(f.read).toHaveBeenCalled());
  timeZone = "UTC";
  view.rerender(f.view(false));
  view.rerender(f.view());
  expect(
    view.container.querySelector('.usage-timezone-chip [aria-hidden="true"]')
      ?.textContent,
  ).toBe("Asia/Seoul");
  await i18n.changeLanguage(SupportedLanguage.Korean);
  await waitFor(() =>
    expect(
      view.container.querySelector(".usage-timezone-chip .usage-sr-only")
        ?.textContent,
    ).toBe("시간대: Asia/Seoul"),
  );
  fireEvent.click(screen.getByRole("button", { name: "최근 30일로 초기화" }));
  await waitFor(() =>
    expect(
      view.container.querySelector('.usage-timezone-chip [aria-hidden="true"]')
        ?.textContent,
    ).toBe("UTC"),
  );
});

function openRowDetails(index: number) {
  fireEvent.click(screen.getByRole("tab", { name: "Usage history" }));
  const rows = document.querySelectorAll<HTMLTableRowElement>(
    ".usage-detail tbody tr",
  );
  const detail = rows[index].querySelector<HTMLDetailsElement>("details")!;
  fireEvent.click(detail.querySelector("summary")!);
  return detail;
}

function detailValue(detail: HTMLElement, term: string) {
  const dt = [...detail.querySelectorAll("dt")].find(
    (entry) => entry.textContent === term,
  )!;
  return dt.nextElementSibling!.textContent;
}

it.each([
  { modelName: "Claude Opus", nativeId: "claude-opus-4-6" },
  { modelName: "", nativeId: "claude-opus-4-6" },
  { modelName: "Claude Opus", nativeId: "" },
])(
  "shows the exact native model ID in row Details for name=$modelName id=$nativeId",
  async ({ modelName, nativeId }) => {
    await i18n.changeLanguage(SupportedLanguage.English);
    const f = fixture();
    f.data.groups[0].modelName = modelName;
    f.data.groups[0].model = create(ModelIdentitySchema, {
      nativeId,
      providerId: f.ids.provider,
    });
    render(f.view());
    await screen.findByText("Incomplete coverage");
    const detail = openRowDetails(0);
    const value = detailValue(detail, "Model");
    if (nativeId) {
      expect(value).toBe(nativeId);
      expect(value).not.toMatch(/Model \u00b7 \d+/);
    } else {
      expect(value).toBe("Unavailable");
    }
    expect(f.read).toHaveBeenCalledTimes(1);
  },
);

it("numbers duplicate observed names on the first committed render for every identity kind", async () => {
  await i18n.changeLanguage(SupportedLanguage.English);
  const f = fixture();
  const base = f.data.groups[0];
  const second = clone(GetUsageSummaryResponseSchema, f.data).groups[0];
  second.sessionId = newRequestId();
  second.projectId = newRequestId();
  second.accountId = newRequestId();
  second.providerId = newRequestId();
  second.model = create(ModelIdentitySchema, {
    nativeId: "second-native-model",
    providerId: second.providerId,
  });
  for (const group of [base, second]) {
    group.sessionName = "Same session";
    group.projectName = "Same project";
    group.accountName = "Same account";
    group.providerName = "Same provider";
    group.modelName = "Same model";
  }
  base.projectId = newRequestId();
  f.data.groups = [base, second];
  // Capture the first committed table so later re-renders cannot mask a
  // missing pre-observation.
  let firstCommit: string[] | undefined;
  const observer = new MutationObserver(() => {
    const cells = document.querySelectorAll(
      ".usage-original-identities dd:first-child, .usage-original-identities > div:first-child dd",
    );
    if (!firstCommit && cells.length >= 2)
      firstCommit = [...cells].map((cell) => cell.textContent ?? "");
  });
  observer.observe(document.body, { childList: true, subtree: true });
  render(f.view());
  await screen.findByText("Incomplete coverage");
  observer.disconnect();
  expect(firstCommit).toEqual([
    "Same session \u00b7 1",
    "Same session \u00b7 2",
  ]);
  const labels = [0, 1].map((index) => {
    const detail = openRowDetails(index);
    return ["Session", "Project", "Account", "Provider"].map((term) =>
      detailValue(detail, term),
    );
  });
  for (const [column, name] of [
    "Same session",
    "Same project",
    "Same account",
    "Same provider",
  ].entries()) {
    expect(labels[0][column]).toBe(`${name} \u00b7 1`);
    expect(labels[1][column]).toBe(`${name} \u00b7 2`);
  }
  const buttons = screen.getAllByRole("button", { name: /^Same model/ });
  expect(buttons.map((button) => button.textContent)).toEqual([
    "Same model \u00b7 1",
    "Same model \u00b7 2",
  ]);
  expect(f.read).toHaveBeenCalledTimes(1);
});

it("scopes model presentation identities by provider and subscription source", async () => {
  await i18n.changeLanguage(SupportedLanguage.English);
  const f = fixture();
  const base = f.data.groups[0];
  const otherProvider = clone(GetUsageSummaryResponseSchema, f.data).groups[0];
  const otherSubscription = clone(GetUsageSummaryResponseSchema, f.data)
    .groups[0];
  const unnamedA = clone(GetUsageSummaryResponseSchema, f.data).groups[0];
  const unnamedB = clone(GetUsageSummaryResponseSchema, f.data).groups[0];
  // The same native model ID under different providers or subscription
  // services is two distinct models that merely share an observed name.
  base.model = create(ModelIdentitySchema, {
    nativeId: "shared-native-model",
    providerId: base.providerId,
  });
  otherProvider.providerId = newRequestId();
  otherProvider.model = create(ModelIdentitySchema, {
    nativeId: "shared-native-model",
    providerId: otherProvider.providerId,
  });
  otherSubscription.subscriptionService =
    SubscriptionServiceIdentity.OPENCODE_GO;
  otherSubscription.model = create(ModelIdentitySchema, {
    nativeId: "shared-native-model",
    providerId: otherSubscription.providerId,
  });
  for (const group of [base, otherProvider, otherSubscription])
    group.modelName = "Same model";
  for (const group of [unnamedA, unnamedB]) group.modelName = "";
  unnamedA.model = create(ModelIdentitySchema, {
    nativeId: "unnamed-native-model",
    providerId: unnamedA.providerId,
  });
  unnamedB.providerId = newRequestId();
  unnamedB.model = create(ModelIdentitySchema, {
    nativeId: "unnamed-native-model",
    providerId: unnamedB.providerId,
  });
  f.data.groups = [base, otherProvider, otherSubscription, unnamedA, unnamedB];
  render(f.view());
  await screen.findByText("Incomplete coverage");
  fireEvent.click(screen.getByRole("tab", { name: "Usage history" }));
  const named = screen.getAllByRole("button", { name: /^Same model/ });
  expect(named.map((button) => button.textContent)).toEqual([
    "Same model \u00b7 1",
    "Same model \u00b7 2",
    "Same model \u00b7 3",
  ]);
  // Unnamed groups under different providers receive different fallbacks.
  const rows = [
    ...document.querySelectorAll<HTMLTableRowElement>(".usage-detail tbody tr"),
  ];
  const buttonText = (row: HTMLTableRowElement) =>
    [...row.querySelectorAll("button")].map((button) => button.textContent);
  expect(buttonText(rows[3])).not.toEqual(buttonText(rows[4]));
  expect(f.read).toHaveBeenCalledTimes(1);
});

it("keeps unique names unnumbered and absent names on localized fallbacks", async () => {
  await i18n.changeLanguage(SupportedLanguage.English);
  const f = fixture();
  const base = f.data.groups[0];
  const second = clone(GetUsageSummaryResponseSchema, f.data).groups[0];
  second.sessionId = newRequestId();
  second.sessionName = "";
  second.accountId = newRequestId();
  second.accountName = "";
  second.model = create(ModelIdentitySchema, {
    nativeId: "other-native-model",
    providerId: second.providerId,
  });
  second.modelName = "";
  f.data.groups = [base, second];
  const view = render(f.view());
  await screen.findByText("Incomplete coverage");
  const first = openRowDetails(0);
  expect(detailValue(first, "Session")).toBe("Retained session");
  expect(detailValue(first, "Account")).toBe("Original account");
  const other = openRowDetails(1);
  expect(detailValue(other, "Session")).not.toMatch(/^Retained session/);
  expect(detailValue(other, "Session")).toMatch(/\u00b7 \d+$/);
  expect(detailValue(other, "Model")).toBe("other-native-model");
  // Rerender keeps labels stable.
  const before = [first, other].map((entry) => entry.textContent);
  view.rerender(f.view());
  expect([first, other].map((entry) => entry.textContent)).toEqual(before);
});

it("keeps duplicate numbering stable across reorder and updates labels on locale change", async () => {
  await i18n.changeLanguage(SupportedLanguage.English);
  const f = fixture();
  const base = f.data.groups[0];
  const second = clone(GetUsageSummaryResponseSchema, f.data).groups[0];
  const unnamed = clone(GetUsageSummaryResponseSchema, f.data).groups[0];
  second.sessionId = newRequestId();
  unnamed.sessionId = newRequestId();
  base.sessionName = "Same session";
  second.sessionName = "Same session";
  unnamed.sessionName = "";
  f.data.groups = [base, second, unnamed];
  const reordered = clone(GetUsageSummaryResponseSchema, f.data);
  reordered.groups = [
    reordered.groups[1],
    reordered.groups[0],
    reordered.groups[2],
  ];
  render(f.view());
  await screen.findByText("Incomplete coverage");
  expect(detailValue(openRowDetails(0), "Session")).toBe(
    "Same session \u00b7 1",
  );
  expect(detailValue(openRowDetails(1), "Session")).toBe(
    "Same session \u00b7 2",
  );
  f.read.mockResolvedValue(reordered);
  fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
  await waitFor(() => expect(f.read).toHaveBeenCalledTimes(2));
  const sessionLabels = () =>
    [
      ...document.querySelectorAll<HTMLTableRowElement>(
        ".usage-detail tbody tr",
      ),
    ].map(
      (row) => row.querySelector(".usage-original-identities dd")!.textContent,
    );
  await waitFor(() =>
    expect(sessionLabels().slice(0, 2)).toEqual([
      "Same session \u00b7 2",
      "Same session \u00b7 1",
    ]),
  );
  const english = sessionLabels()[2];
  expect(english).toMatch(/^Session unavailable/);
  await act(async () => {
    await i18n.changeLanguage(SupportedLanguage.Korean);
  });
  await waitFor(() => expect(sessionLabels()[2]).not.toBe(english));
  expect(sessionLabels().slice(0, 2)).toEqual([
    "Same session \u00b7 2",
    "Same session \u00b7 1",
  ]);
  await i18n.changeLanguage(SupportedLanguage.English);
});

it("opens a Usage tab in a new window on a primary modified click and keeps the selected tab here", async () => {
  const destination = fakeDestinationWindow();
  const f = fixture();
  render(destination.wrap(f.view()));
  await screen.findByText("Incomplete coverage");
  const overview = screen.getByRole("tab", { name: "Overview" });
  const history = screen.getByRole("tab", { name: "Usage history" });
  expect(overview.getAttribute("aria-selected")).toBe("true");
  fireEvent.click(history, primaryClick);
  expect(destination.opened).toHaveBeenCalledExactlyOnceWith({
    surface: Surface.Usage,
    tab: UsageTab.History,
  });
  expect(overview.getAttribute("aria-selected")).toBe("true");
  expect(history.getAttribute("aria-selected")).toBe("false");
  // A plain click and a keyboard-style click select the tab in place.
  fireEvent.click(history, { detail: 0, ctrlKey: true });
  expect(history.getAttribute("aria-selected")).toBe("true");
  expect(destination.opened).toHaveBeenCalledTimes(1);
});
