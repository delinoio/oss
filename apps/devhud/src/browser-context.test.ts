// @vitest-environment jsdom

import { describe, expect, it } from "vitest";
import { queryFragmentWarningRequired, sanitizeChromeContext } from "./browser-context";

describe("Chrome browser context privacy", () => {
  const input = { url: "https://user:password@Example.com:444/a/reset-token/?secret=value#fragment", title: "Page", viewport: { width: 100, height: 50 }, userAgent: "test", selectedBounds: null, accessibility: {}, outerHtml: "<main>safe</main>" };
  it("removes credentials, query, and fragments and redacts persisted paths", () => {
    const result = sanitizeChromeContext(input);
    expect(result).toMatchObject({ kind: "sanitized", context: { url: "https://example.com:444/<redacted>/<redacted>/" } });
    expect(JSON.stringify(result)).not.toContain("selector");
  });
  it("drops undeclared runtime fields from the sanitized context", () => {
    const result = sanitizeChromeContext({
      ...input,
      viewport: { ...input.viewport, deviceMemory: 16 },
      selectedBounds: { x: 1, y: 2, width: 3, height: 4, selector: "#payment-form" },
      cookie: "session=secret",
    } as unknown as typeof input);
    expect(result).toMatchObject({ kind: "sanitized", context: { viewport: { width: 100, height: 50 }, selectedBounds: { x: 1, y: 2, width: 3, height: 4 } } });
    expect(JSON.stringify(result)).not.toContain("deviceMemory");
    expect(JSON.stringify(result)).not.toContain("selector");
    expect(JSON.stringify(result)).not.toContain("session=secret");
  });
  it("returns malformed for invalid runtime context values", () => {
    for (const malformedInput of [
      { ...input, title: { cookie: "secret" } },
      { ...input, viewport: { width: Number.NaN, height: 50 } },
      { ...input, viewport: { width: -100, height: -50 } },
      { ...input, selectedBounds: { x: 1, y: 2, width: Number.POSITIVE_INFINITY, height: 4 } },
      { ...input, selectedBounds: { x: 1, y: 2, width: 0, height: 4 } },
      { ...input, selectedBounds: { x: 1, y: 2, width: 4, height: -1 } },
      { ...input, accessibility: { "aria-label": { cookie: "secret" } } },
    ]) expect(sanitizeChromeContext(malformedInput)).toEqual({ kind: "malformed" });
  });
  it("keeps valid selections that extend off-screen", () => expect(sanitizeChromeContext({ ...input, selectedBounds: { x: -1, y: -2, width: 3, height: 4 } })).toMatchObject({ kind: "sanitized", context: { selectedBounds: { x: -1, y: -2, width: 3, height: 4 } } }));
  it("returns malformed rather than retaining an unsupported URL", () => expect(sanitizeChromeContext({ ...input, url: "file:///secret" })).toEqual({ kind: "malformed" }));
  it("keeps only allowlisted DOM and accessibility data within the DOM cap", () => {
    const result = sanitizeChromeContext({
      ...input,
      accessibility: { "aria-label": "safe", onclick: "steal()", "data-token": "secret" },
      outerHtml: '<main aria-label="safe" onclick="steal()"><a href="https://example.com/?token=secret">safe</a><input value="secret"><script>steal()</script></main>',
    });
    expect(result).toMatchObject({ kind: "sanitized", context: { accessibility: { "aria-label": "safe" }, outerHtml: '<main aria-label="safe"><a>safe</a></main>' } });
    expect(sanitizeChromeContext({ ...input, outerHtml: `<main>${"x".repeat((128 * 1024) + 1)}</main>` })).toMatchObject({ kind: "sanitized", context: { outerHtml: "" } });
  });
  it("permits a warning only for an explicitly permitted non-Chrome source", () => {
    expect(queryFragmentWarningRequired("contract-permitted-other", true)).toBe(true);
    expect(() => queryFragmentWarningRequired("chrome", true)).toThrow(/cannot include/u);
  });
});
