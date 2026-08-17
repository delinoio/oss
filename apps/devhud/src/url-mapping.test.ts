import { describe, expect, it } from "vitest";
import { findMappingOverlaps, literalSpecificity, mappingMatches, parseUrlPattern, resolveRepositorySelection, selectUrlMapping, type UrlRepositoryMapping } from "./url-mapping";

const mapping = (overrides: Partial<UrlRepositoryMapping> = {}): UrlRepositoryMapping => ({
  id: "018f47a2-7b3c-7def-8abc-1234567890ab", pattern: "https://*.example.com:*/teams/**", repository: { owner: "delinoio", name: "oss" }, credentialProfileRef: "github.default", priority: 0, chromeOrigin: null, updatedAt: "2026-08-17T00:00:00.000Z", ...overrides,
});

describe("URL repository matcher", () => {
  it("matches separately parsed components and ignores query and fragment", () => {
    expect(mappingMatches(mapping(), "https://API.example.COM:444/teams/a/b?token=secret#fragment")).toBe(true);
    expect(mappingMatches(mapping({ pattern: "https://*.example.com:444/teams/**" }), "https://api.example.com/teams/a")).toBe(false);
    expect(mappingMatches(mapping({ pattern: "https://api.example.com/teams/*" }), "https://api.example.com/teams/A")).toBe(true);
    expect(mappingMatches(mapping({ pattern: "https://api.example.com/teams/A" }), "https://api.example.com/teams/a")).toBe(false);
  });

  it("treats explicit default ports as portless and preserves repeated path slashes", () => {
    expect(mappingMatches(mapping({ pattern: "https://example.com:443/**" }), "https://example.com/a")).toBe(true);
    expect(findMappingOverlaps([mapping({ pattern: "https://example.com:443/**" }), mapping({ id: "018f47a2-7b3c-7def-8abc-1234567890ac", pattern: "https://example.com/**" })])).toHaveLength(1);
    expect(mappingMatches(mapping({ pattern: "https://example.com/a/b" }), "https://example.com/a//b")).toBe(false);
  });

  it("rejects invalid wildcard placement and sensitive URL components", () => {
    for (const pattern of ["https://api*.example.com/", "https://api.example.com/**:123", "https://api.example.com/path?x=1", "https://user@api.example.com/"]) expect(() => parseUrlPattern(pattern)).toThrow();
    expect(parseUrlPattern("https://social.example/@alice").path).toEqual(["@alice"]);
  });

  it("chooses priority, literal specificity, and recency deterministically", () => {
    const broad = mapping({ id: "018f47a2-7b3c-7def-8abc-1234567890ab", pattern: "https://api.example.com/**", priority: 2 });
    const specific = mapping({ id: "018f47a2-7b3c-7def-8abc-1234567890ac", pattern: "https://api.example.com/projects/*", priority: 2 });
    const recent = mapping({ id: "018f47a2-7b3c-7def-8abc-1234567890ad", pattern: "https://api.example.com/projects/*", priority: 2, updatedAt: "2026-08-18T00:00:00.000Z" });
    expect(literalSpecificity(specific)).toBeGreaterThan(literalSpecificity(broad));
    expect(selectUrlMapping([broad, specific, recent], "https://api.example.com/projects/devhud")?.id).toBe(recent.id);
    expect(selectUrlMapping([broad], null)).toBeNull();
    expect(resolveRepositorySelection([broad], "https://other.example.com/")).toEqual({ kind: "manual-required" });
    expect(resolveRepositorySelection([broad], "not a URL")).toEqual({ kind: "manual-required" });
  });

  it("reports intersecting mappings before save", () => {
    expect(findMappingOverlaps([mapping({ pattern: "https://api.example.com/projects/**" }), mapping({ id: "018f47a2-7b3c-7def-8abc-1234567890ac", pattern: "https://api.example.com/projects/*" })])).toHaveLength(1);
    expect(findMappingOverlaps([mapping({ pattern: "https://api.example.com/a" }), mapping({ id: "018f47a2-7b3c-7def-8abc-1234567890ac", pattern: "https://api.example.com/b" })])).toHaveLength(0);
  });
});
