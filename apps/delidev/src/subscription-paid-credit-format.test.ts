// SPDX-License-Identifier: Apache-2.0
import { describe, expect, it } from "vitest";
import { formatPaidCreditBalance } from "./subscription-paid-credit-format";
describe("paid-credit string formatting", () => {
 it.each([
  ["60961.1135370000", "60,961.11"], ["1.995", "2.00"], ["99.999", "100.00"],
  ["0", "0.00"], ["000.000", "0.00"], ["0.004", "<0.01"], ["0.00999", "<0.01"],
  ["0.01", "0.01"], ["0.01499", "0.01"], ["0.015", "0.02"], ["1.5", "1.50"],
  ["9007199254740993.995", "9,007,199,254,740,994.00"],
  ["0."+"0".repeat(61)+"1", "<0.01"],
  ["9".repeat(60)+".995", "1"+",000".repeat(20)+".00"],
 ])("formats %s without precision loss", (input, expected) => {
  expect(formatPaidCreditBalance(input,"en-US")).toBe(expected);
  expect(formatPaidCreditBalance(input,"ko-KR")).toBe(expected);
 });
 it("uses locale grouping and decimal punctuation", () => {
  expect(formatPaidCreditBalance("60961.115", "de-DE")).toBe("60.961,12");
  expect(formatPaidCreditBalance("0.004", "de-DE")).toBe("<0,01");
 });
});
