import { create } from "@bufbuild/protobuf";
import { render, screen, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { AccountingUnitKind, GetUsageSummaryResponseSchema, NativeAccountingMeasureSchema, UsageAccountingProfile } from "@delinoio/delidev-api-client";
import { NativeAccounting, nativeMeasure } from "./native-accounting";

it("keeps exact native counts, zero, unavailable and partial estimates distinct", () => {
  const zero = create(NativeAccountingMeasureSchema, { knownTotal: "0", measuredUnits: 1 });
  expect(nativeMeasure(zero)).toBe("0");
  expect(nativeMeasure(create(NativeAccountingMeasureSchema))).toBe("Unavailable");
  const data = create(GetUsageSummaryResponseSchema, { accountingProfile: UsageAccountingProfile.NATIVE_UNITS_V1, nativeAccounting: [
    { totals: { kind: AccountingUnitKind.CLAUDE_MAIN_LOOP_INPUT, units: 1, input: { knownTotal: "9007199254740993", measuredUnits: 1 }, output: zero } },
    { totals: { kind: AccountingUnitKind.OPENCODE_STEP, units: 2, input: { knownTotal: "22", measuredUnits: 1, unavailableUnits: 1 }, currencies: [{ currency: "USD", knownAmount: "0.000055", completeUnits: 1, partialUnits: 1 }] } },
  ] });
  render(<NativeAccounting data={data} open={vi.fn()} />);
  const claude = screen.getByRole("region", { name: "Claude main-loop inputs" });
  expect(within(claude).getByText(BigInt("9007199254740993").toLocaleString())).toBeTruthy();
  const opencode = screen.getByRole("region", { name: "OpenCode steps" });
  expect(within(opencode).getByText("22 · 1 unavailable units")).toBeTruthy();
  expect(within(opencode).getByText(/USD 0.000055.*1 partial/)).toBeTruthy();
  expect(within(opencode).getByText(/Assistant summaries and inherited fork history are excluded/)).toBeTruthy();
});

it("provides explicit update guidance for a server without native input accounting", () => {
  render(<NativeAccounting data={create(GetUsageSummaryResponseSchema, { accountingProfile: UsageAccountingProfile.NATIVE_UNITS_V1 })} open={vi.fn()} />);
  expect(screen.getByRole("status").textContent).toContain("Update the server");
});

it.each([
  [AccountingUnitKind.CLAUDE_MAIN_LOOP_INPUT, AccountingUnitKind.CLAUDE_MAIN_LOOP_INPUT],
  [AccountingUnitKind.OPENCODE_STEP, AccountingUnitKind.OPENCODE_STEP],
  [AccountingUnitKind.CLAUDE_MAIN_LOOP_INPUT, 99],
])("rejects incomplete or duplicate native accounting families: %s / %s", (first, second) => {
  const data = create(GetUsageSummaryResponseSchema, { accountingProfile: UsageAccountingProfile.NATIVE_UNITS_V1, nativeAccounting: [
    { totals: { kind: first as AccountingUnitKind } }, { totals: { kind: second as AccountingUnitKind } },
  ] });
  render(<NativeAccounting data={data} open={vi.fn()} />);
  expect(screen.getByRole("status").textContent).toContain("Update the server");
  expect(screen.queryByRole("region", { name: "Claude main-loop inputs" })).toBeNull();
  expect(screen.queryByRole("region", { name: "OpenCode steps" })).toBeNull();
});
