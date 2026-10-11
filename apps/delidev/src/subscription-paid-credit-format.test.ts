// SPDX-License-Identifier: Apache-2.0
import { describe, expect, it } from "vitest";
import { formatPaidCreditBalance } from "./subscription-paid-credit-format";

describe("paid-credit display rounding", () => {
  it.each([
    ["60961.1135370000", "60,961.11"], ["1.995", "2.00"], ["999.995", "1,000.00"],
    ["0", "0.00"], ["0.000", "0.00"], ["0.004", "<0.01"], ["0.0099", "<0.01"],
    ["0.01", "0.01"], ["0.0149", "0.01"], ["0.015", "0.02"], ["0001.2", "1.20"],
    ["9007199254740993.125", "9,007,199,254,740,993.13"],
  ])("formats %s without losing decimal precision", (source, expected) => {
    expect(formatPaidCreditBalance(source, "en-US")).toBe(expected);
    expect(formatPaidCreditBalance(source, "ko-KR")).toBe(expected);
  });
  it("supports the complete 64-character balance bound", () => {
    const source = "9".repeat(61) + ".99";
    expect(formatPaidCreditBalance(source, "en-US").replaceAll(",", "")).toBe(source);
    expect(source).toHaveLength(64);
  });
  it.each(["-1", "1e4", "1.", "", "1".repeat(65)])("rejects unvalidated input %s", source => {
    expect(() => formatPaidCreditBalance(source, "en-US")).toThrow("Invalid paid-credit balance");
  });
});
