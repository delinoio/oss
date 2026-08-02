import { describe, expect, it, vi } from "vitest";

import { createUuidV7 } from "./uuid";

describe("Deck request identities", () => {
  it("creates UUID v7 values with the current millisecond prefix", () => {
    vi.spyOn(Date, "now").mockReturnValue(1_722_470_400_000);
    const value = createUuidV7();
    expect(value).toMatch(
      /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/u,
    );
    expect(BigInt(`0x${value.replaceAll("-", "").slice(0, 12)}`)).toBe(
      1_722_470_400_000n,
    );
  });
});
