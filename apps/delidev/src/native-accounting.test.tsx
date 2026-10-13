import { create } from "@bufbuild/protobuf";
import { act, cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import {
  AccountingUnitKind,
  GetUsageSummaryResponseSchema,
  NativeAccountingMeasureSchema,
  SubscriptionServiceIdentity,
  UsageAccountingProfile,
} from "@delinoio/delidev-api-client";
import { i18n } from "./localization";
import { NativeAccounting, nativeMeasure } from "./native-accounting";

afterEach(async () => {
  cleanup();
  await act(async () => {
    await i18n.changeLanguage("en");
  });
});

it("keeps exact native counts, zero, unavailable and partial estimates distinct", () => {
  const zero = create(NativeAccountingMeasureSchema, {
    knownTotal: "0",
    measuredUnits: 1,
  });
  expect(nativeMeasure(zero)).toBe("0");
  expect(nativeMeasure(create(NativeAccountingMeasureSchema))).toBe(
    "Unavailable",
  );
  const data = create(GetUsageSummaryResponseSchema, {
    accountingProfile: UsageAccountingProfile.NATIVE_UNITS_V1,
    nativeAccounting: [
      {
        totals: {
          kind: AccountingUnitKind.CLAUDE_MAIN_LOOP_INPUT,
          units: 1,
          input: { knownTotal: "9007199254740993", measuredUnits: 1 },
          output: zero,
        },
      },
      {
        totals: {
          kind: AccountingUnitKind.OPENCODE_STEP,
          units: 2,
          input: { knownTotal: "22", measuredUnits: 1, unavailableUnits: 1 },
          currencies: [
            {
              currency: "USD",
              knownAmount: "0.000055",
              completeUnits: 1,
              partialUnits: 1,
            },
          ],
        },
      },
    ],
  });
  render(<NativeAccounting data={data} open={vi.fn()} />);
  const claude = screen.getByRole("region", {
    name: "Claude main-loop inputs",
  });
  expect(
    within(claude).getByText(BigInt("9007199254740993").toLocaleString()),
  ).toBeTruthy();
  const opencode = screen.getByRole("region", { name: "OpenCode steps" });
  expect(within(opencode).getByText("22 · 1 unavailable unit")).toBeTruthy();
  expect(within(opencode).getByText(/USD 0.000055.*1 partial/)).toBeTruthy();
  expect(
    within(opencode).getByText(
      /Assistant summaries and inherited fork history are excluded/,
    ),
  ).toBeTruthy();
});

it("keeps unsupported evidence distinct from a diagnosed server-version failure", () => {
  render(
    <NativeAccounting
      data={create(GetUsageSummaryResponseSchema, {
        accountingProfile: UsageAccountingProfile.NATIVE_UNITS_V1,
      })}
      open={vi.fn()}
    />,
  );
  expect(screen.getByRole("status").textContent).toContain(
    "unsupported or incomplete",
  );
});

it("labels each historical category with its original currency without inventing missing amounts", () => {
  const data = create(GetUsageSummaryResponseSchema, {
    accountingProfile: UsageAccountingProfile.NATIVE_UNITS_V1,
    nativeAccounting: [
      {
        totals: { kind: AccountingUnitKind.CLAUDE_MAIN_LOOP_INPUT },
        groups: [{ sessionId: "session" }],
        pricing: [
          {
            pricing: {
              id: "usd-price",
              revision: 1n,
              basis: { currency: "USD" },
            },
            input: { knownAmount: "0.25", pricedUnits: 1 },
          },
          {
            pricing: {
              id: "eur-price",
              revision: 2n,
              basis: { currency: "EUR" },
            },
            input: { knownAmount: "0.25", pricedUnits: 1 },
            output: { knownAmount: "0", pricedUnits: 1 },
          },
        ],
      },
      { totals: { kind: AccountingUnitKind.OPENCODE_STEP } },
    ],
  });
  render(<NativeAccounting data={data} open={vi.fn()} />);
  const table = screen.getByRole("table", {
    name: "Original price versions for Claude main-loop inputs",
  });
  expect(within(table).getByText(/input: USD 0.25/)).toBeTruthy();
  expect(within(table).getByText(/input: EUR 0.25/)).toBeTruthy();
  expect(within(table).getByText(/output: EUR 0 · 1 priced/)).toBeTruthy();
  expect(within(table).getByText(/output: Unavailable/)).toBeTruthy();
});

it.each([
  [
    AccountingUnitKind.CLAUDE_MAIN_LOOP_INPUT,
    AccountingUnitKind.CLAUDE_MAIN_LOOP_INPUT,
  ],
  [AccountingUnitKind.OPENCODE_STEP, AccountingUnitKind.OPENCODE_STEP],
  [AccountingUnitKind.CLAUDE_MAIN_LOOP_INPUT, 99],
])(
  "rejects incomplete or duplicate native accounting families: %s / %s",
  (first, second) => {
    const data = create(GetUsageSummaryResponseSchema, {
      accountingProfile: UsageAccountingProfile.NATIVE_UNITS_V1,
      nativeAccounting: [
        { totals: { kind: first as AccountingUnitKind } },
        { totals: { kind: second as AccountingUnitKind } },
      ],
    });
    render(<NativeAccounting data={data} open={vi.fn()} />);
    expect(screen.getByRole("status").textContent).toContain(
      "unsupported or incomplete",
    );
    expect(
      screen.queryByRole("region", { name: "Claude main-loop inputs" }),
    ).toBeNull();
    expect(screen.queryByRole("region", { name: "OpenCode steps" })).toBeNull();
  },
);

const apiProviderId = "0d5f1a52-6e2b-4b57-9a47-1f4f7b6d9c11";

function mixedSourceSummary() {
  const sharedModel = { nativeId: "shared-model" };
  return create(GetUsageSummaryResponseSchema, {
    accountingProfile: UsageAccountingProfile.NATIVE_UNITS_V1,
    nativeAccounting: [
      { totals: { kind: AccountingUnitKind.CLAUDE_MAIN_LOOP_INPUT } },
      {
        totals: { kind: AccountingUnitKind.OPENCODE_STEP, units: 3 },
        groups: [
          {
            sessionId: "session-go",
            accountId: "account-go",
            providerId: "",
            subscriptionService: SubscriptionServiceIdentity.OPENCODE_GO,
            model: sharedModel,
            totals: { kind: AccountingUnitKind.OPENCODE_STEP, units: 1 },
          },
          {
            sessionId: "session-api",
            accountId: "account-api",
            providerId: apiProviderId,
            subscriptionService: SubscriptionServiceIdentity.UNSPECIFIED,
            model: sharedModel,
            totals: { kind: AccountingUnitKind.OPENCODE_STEP, units: 2 },
          },
        ],
        models: [
          {
            providerId: "",
            subscriptionService: SubscriptionServiceIdentity.OPENCODE_GO,
            model: sharedModel,
            totals: { kind: AccountingUnitKind.OPENCODE_STEP, units: 1 },
          },
          {
            providerId: apiProviderId,
            subscriptionService: SubscriptionServiceIdentity.UNSPECIFIED,
            model: sharedModel,
            totals: { kind: AccountingUnitKind.OPENCODE_STEP, units: 2 },
          },
        ],
      },
    ],
  });
}

it.each(["en", "ko"])(
  "labels OpenCode Go rows in both native tables and keeps API rows distinct (%s)",
  async (language) => {
    await act(async () => {
      await i18n.changeLanguage(language);
    });
    const select = vi.fn();
    render(
      <NativeAccounting
        data={mixedSourceSummary()}
        open={vi.fn()}
        selectPrice={select}
      />,
    );
    const tables = screen
      .getAllByRole("table")
      .filter((table) => within(table).queryAllByText("OpenCode Go").length);
    // Session/account/model table and by-model table each show the Go source once.
    expect(tables).toHaveLength(2);
    for (const table of tables) {
      expect(within(table).getAllByText("OpenCode Go")).toHaveLength(1);
      const rows = within(table).getAllByRole("row").slice(1);
      expect(rows).toHaveLength(2);
      const goRow = rows.find((row) => within(row).queryByText("OpenCode Go"))!;
      const apiRow = rows.find((row) => row !== goRow)!;
      expect(within(apiRow).queryByText("OpenCode Go")).toBeNull();
      // The API row keeps the Provider label that the Go row never receives.
      const goSource = within(goRow).getByText("OpenCode Go");
      const goCell = goSource.closest("td")!;
      const cellIndex = Array.from(goRow.children).indexOf(goCell);
      const apiCell = apiRow.children[cellIndex] as HTMLElement;
      const apiSource = apiCell.querySelector("p");
      expect(apiSource?.textContent).toBeTruthy();
      expect(apiSource?.textContent).not.toBe(goSource.textContent);
      // Selection passes the original identity unchanged.
      const goButton = goCell.querySelector("button")!;
      const apiButton = apiCell.querySelector("button")!;
      select.mockClear();
      act(() => goButton.click());
      expect(select).toHaveBeenLastCalledWith({
        nativeId: "shared-model",
        providerId: "",
        subscriptionService: SubscriptionServiceIdentity.OPENCODE_GO,
      });
      act(() => apiButton.click());
      expect(select).toHaveBeenLastCalledWith({
        nativeId: "shared-model",
        providerId: apiProviderId,
        subscriptionService: SubscriptionServiceIdentity.UNSPECIFIED,
      });
    }
  },
);

it("keeps the generic provider fallback for empty provider rows without a service", () => {
  const data = create(GetUsageSummaryResponseSchema, {
    accountingProfile: UsageAccountingProfile.NATIVE_UNITS_V1,
    nativeAccounting: [
      { totals: { kind: AccountingUnitKind.CLAUDE_MAIN_LOOP_INPUT } },
      {
        totals: { kind: AccountingUnitKind.OPENCODE_STEP, units: 1 },
        groups: [
          {
            sessionId: "session",
            providerId: "",
            model: { nativeId: "model" },
          },
        ],
      },
    ],
  });
  render(<NativeAccounting data={data} open={vi.fn()} />);
  expect(screen.queryByText("OpenCode Go")).toBeNull();
});
