import { describe, expect, it } from "vitest";

import { formatCatalogPrice } from "../pages/CatalogPages";

describe("catalog pricing", () => {
  it("distinguishes missing prices from a zero-dollar price", () => {
    expect(formatCatalogPrice(undefined)).toBe("Price unavailable");
    expect(formatCatalogPrice(0n)).toBe("$0.0000");
  });
});
