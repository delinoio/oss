import { describe, expect, it } from "vitest";
import { queryFragmentWarningRequired, sanitizeChromeContext } from "./browser-context";

describe("Chrome browser context privacy", () => {
  const input = { url: "https://user:password@Example.com:444/a/reset-token/?secret=value#fragment", title: "Page", viewport: { width: 100, height: 50 }, userAgent: "test", selectedBounds: null, accessibility: {}, outerHtml: "<main>safe</main>" };
  it("removes credentials, query, and fragments and redacts persisted paths", () => {
    const result = sanitizeChromeContext(input);
    expect(result).toMatchObject({ kind: "sanitized", context: { url: "https://example.com:444/<redacted>/<redacted>/" } });
    expect(JSON.stringify(result)).not.toContain("selector");
  });
  it("returns malformed rather than retaining an unsupported URL", () => expect(sanitizeChromeContext({ ...input, url: "file:///secret" })).toEqual({ kind: "malformed" }));
  it("permits a warning only for an explicitly permitted non-Chrome source", () => {
    expect(queryFragmentWarningRequired("contract-permitted-other", true)).toBe(true);
    expect(() => queryFragmentWarningRequired("chrome", true)).toThrow(/cannot include/u);
  });
});
