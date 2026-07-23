import { OrganizationRole } from "@delinoio/delibase-connect";
import { describe, expect, it } from "vitest";

import {
  canManageOrganization,
  parseUsdMicros,
} from "../pages/OrganizationPages";

describe("organization billing inputs", () => {
  it("converts exact USD input to signed 64-bit micro-units", () => {
    expect(parseUsdMicros("0")).toBe(0n);
    expect(parseUsdMicros("12.345678")).toBe(12_345_678n);
    expect(parseUsdMicros("9223372036854.775807")).toBe(
      9_223_372_036_854_775_807n,
    );
  });

  it("rejects negative, over-precise, and overflowing limits", () => {
    expect(parseUsdMicros("-1")).toBeUndefined();
    expect(parseUsdMicros("1.0000001")).toBeUndefined();
    expect(parseUsdMicros("9223372036854.775808")).toBeUndefined();
  });

  it("limits organization management to owners and admins", () => {
    expect(canManageOrganization(OrganizationRole.OWNER)).toBe(true);
    expect(canManageOrganization(OrganizationRole.ADMIN)).toBe(true);
    expect(canManageOrganization(OrganizationRole.MEMBER)).toBe(false);
    expect(canManageOrganization(OrganizationRole.UNSPECIFIED)).toBe(false);
  });
});
