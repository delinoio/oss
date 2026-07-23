import { describe, expect, it } from "vitest";

import { formatUsdMicros } from "../utils/format";

describe("USD micro-unit formatting", () => {
  it("formats the full signed int64 range without Number conversion", () => {
    expect(formatUsdMicros(9_223_372_036_854_775_807n)).toBe(
      "$9,223,372,036,854.78",
    );
    expect(formatUsdMicros(-9_223_372_036_854_775_808n)).toBe(
      "-$9,223,372,036,854.78",
    );
  });

  it("preserves micro-unit precision for sub-cent amounts", () => {
    expect(formatUsdMicros(1n)).toBe("$0.000001");
    expect(formatUsdMicros(1_000n)).toBe("$0.0010");
    expect(formatUsdMicros(10_000n)).toBe("$0.01");
  });
});
