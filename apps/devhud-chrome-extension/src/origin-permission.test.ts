import { describe, expect, it } from "vitest";
import { configuredOriginPermissionPattern } from "./origin-permission.js";

describe("configured origin permission patterns", () => {
  it.each([
    ["https://example.com", "https://example.com:443/*"],
    ["http://example.com", "http://example.com:80/*"],
    ["https://example.com:8443", "https://example.com:8443/*"],
    ["http://[::1]", "http://[::1]:80/*"],
  ])("scopes %s to its effective port", (origin, expected) => {
    expect(configuredOriginPermissionPattern(origin)).toBe(expected);
  });

  it.each(["https://example.com/path", "file:///tmp/page", "not-an-origin"])("rejects invalid configured origin %s", (origin) => {
    expect(configuredOriginPermissionPattern(origin)).toBeNull();
  });
});
