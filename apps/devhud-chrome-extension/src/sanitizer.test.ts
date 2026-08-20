// @vitest-environment jsdom
import { describe, expect, it } from "vitest";
import { MaximumOuterHtmlBytes, normalizeCapturedUrl, sanitizeOuterHtml } from "./sanitizer.js";

describe("browser-context sanitizer", () => {
  it("redacts credentials, query, fragment, and every path segment", () => {
    expect(normalizeCapturedUrl("https://user:password@Example.com:444/reset/token/?secret=x#fragment")).toBe("https://example.com:444/%3Credacted%3E/%3Credacted%3E/");
    expect(() => normalizeCapturedUrl("file:///credential")).toThrow();
  });
  it("keeps only allowlisted elements and accessibility attributes", () => {
    const html = sanitizeOuterHtml('<main aria-label="safe" onclick="steal()" data-token="secret"><a href="https://example.com/?token=secret">safe</a><img src="data:image/png;base64,secret" alt="preview"><div hidden>hidden credential</div><div aria-hidden="true">password</div><form action="https://evil"><input type="password" value="secret"></form><style>.x{}</style><script>steal()</script></main>');
    expect(html).toBe('<main aria-label="safe"><a>safe</a><img alt="preview"></main>');
    expect(html).not.toMatch(/href|src|onclick|data-|form|input|style|script|secret|password/u);
  });
  it("caps complete UTF-8 markup without byte slicing", () => {
    const html = sanitizeOuterHtml(`<main>${"한".repeat(MaximumOuterHtmlBytes)}</main>`);
    expect(new TextEncoder().encode(html).byteLength).toBeLessThanOrEqual(MaximumOuterHtmlBytes);
    expect(() => new TextDecoder("utf-8", { fatal: true }).decode(new TextEncoder().encode(html))).not.toThrow();
  });
  it("drops unknown containers rather than traversing their descendants", () => {
    expect(sanitizeOuterHtml("<template><p>credential</p></template>")).toBe("");
  });
});
