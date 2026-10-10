// SPDX-License-Identifier: Apache-2.0
import { expect, it } from "vitest";
import { nativeSleepDuration } from "./native-sleep";

const snapshot = (duration_ms: unknown, status = "running") => ({ kind: "sleep", status, sleep: { duration_ms } });
it("retains the native uint64 duration exactly across lifecycle", () => {
  expect(nativeSleepDuration(snapshot("18446744073709551615"), snapshot("18446744073709551615", "completed"))).toBe("18446744073709551615");
  expect(nativeSleepDuration(snapshot("0"), {})).toBe("0");
});
it("rejects rounded, malformed and substituted durations", () => {
  for (const value of [10, "-1", "1.5", "01", "18446744073709551616", null]) expect(nativeSleepDuration(snapshot(value), {})).toBe("");
  expect(nativeSleepDuration(snapshot("10"), snapshot("11", "completed"))).toBe("");
  expect(nativeSleepDuration(snapshot("10"), snapshot("10", "failed"))).toBe("");
});
