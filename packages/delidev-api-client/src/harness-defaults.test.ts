// SPDX-License-Identifier: Apache-2.0
import { describe, expect, it } from "vitest";
import { HarnessInheritanceState, validHarnessSelection, validInheritedHarnessValue } from "./harness-defaults.js";

describe("typed harness inheritance", () => {
  it("retains empty and zero overrides", () => {
    expect(validHarnessSelection({ effort: { state: HarnessInheritanceState.Override, value: "" }, max_concurrency: { state: HarnessInheritanceState.Override, value: 0 } })).toBe(true);
    expect(validHarnessSelection({ effort: { state: HarnessInheritanceState.Inherit } })).toBe(true);
  });
  it("rejects incomplete, ambiguous and foreign option shapes", () => {
    for (const value of [{ effort: { state: "override" } }, { effort: { state: "inherit", value: "" } }, { max_concurrency: { state: "override", value: "0" } }, { permission: { state: "override", value: 0 } }, { future_option: { state: "inherit" } }]) expect(validHarnessSelection(value)).toBe(false);
    expect(validInheritedHarnessValue({ state: "override", value: null }, true)).toBe(false);
  });
});
