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
    expect(mappingMatches(mapping({ pattern: "https://example.com/docs/" }), "https://example.com/docs/")).toBe(true);
    expect(mappingMatches(mapping({ pattern: "https://example.com/a//b" }), "https://example.com/a//b")).toBe(true);
    expect(mappingMatches(mapping({ pattern: "https://example.com/a//b" }), "https://example.com/a/b")).toBe(false);
  });

  it("normalizes wildcard-scheme ports against each concrete scheme", () => {
    expect(mappingMatches(mapping({ pattern: "*://example.com:443/**" }), "https://example.com/a")).toBe(true);
    expect(mappingMatches(mapping({ pattern: "*://example.com:443/**" }), "http://example.com/a")).toBe(false);
    expect(mappingMatches(mapping({ pattern: "*://example.com:80/**" }), "http://example.com/a")).toBe(true);
    expect(findMappingOverlaps([mapping({ pattern: "*://example.com:443/**" }), mapping({ id: "018f47a2-7b3c-7def-8abc-1234567890ac", pattern: "https://example.com/**" })])).toHaveLength(1);
    expect(findMappingOverlaps([mapping({ pattern: "*://example.com:80/**" }), mapping({ id: "018f47a2-7b3c-7def-8abc-1234567890ac", pattern: "https://example.com/**" })])).toHaveLength(0);
  });

  it("matches bracketed IPv6 literals as concrete hosts", () => {
    expect(parseUrlPattern("http://[::1]:3000/app").host).toEqual(["[::1]"]);
    expect(mappingMatches(mapping({ pattern: "http://[::1]:3000/app" }), "http://[::1]:3000/app")).toBe(true);
    expect(mappingMatches(mapping({ pattern: "http://[::ffff:192.0.2.1]/**" }), "http://[::FFFF:192.0.2.1]/app")).toBe(true);
    for (const pattern of ["http://[::1/app", "http://[*]/app", "http://[not-an-ip]/app"]) expect(() => parseUrlPattern(pattern)).toThrow();
  });

  it("keeps DNS wildcards separate from IP literals and canonicalizes IPv4 patterns", () => {
    expect(mappingMatches(mapping({ pattern: "http://*:3000/**" }), "http://[::1]:3000/app")).toBe(false);
    expect(findMappingOverlaps([mapping({ pattern: "http://*:3000/**" }), mapping({ id: "018f47a2-7b3c-7def-8abc-1234567890ac", pattern: "http://[::1]:3000/**" })])).toHaveLength(0);
    expect(mappingMatches(mapping({ pattern: "http://127.1/**" }), "http://127.0.0.1/app")).toBe(true);
    expect(mappingMatches(mapping({ pattern: "http://0x7f000001/**" }), "http://127.0.0.1/app")).toBe(true);
  });

  it("canonicalizes internationalized pattern hosts with URL semantics", () => {
    expect(parseUrlPattern("https://예시.한국/path").host).toEqual(["xn--vv4b11d", "xn--3e0b707e"]);
    expect(mappingMatches(mapping({ pattern: "https://예시.한국/path" }), "https://xn--vv4b11d.xn--3e0b707e/path")).toBe(true);
  });

  it("canonicalizes literal path segments with URL semantics", () => {
    expect(mappingMatches(mapping({ pattern: "https://example.com/한글" }), "https://example.com/%ED%95%9C%EA%B8%80")).toBe(true);
    expect(mappingMatches(mapping({ pattern: "https://example.com/hello world" }), "https://example.com/hello%20world")).toBe(true);
    expect(mappingMatches(mapping({ pattern: "https://example.com/%ed%95%9c%ea%b8%80" }), "https://example.com/한글")).toBe(true);
    expect(mappingMatches(mapping({ pattern: "https://example.com/teams/../projects" }), "https://example.com/projects")).toBe(true);
    expect(mappingMatches(mapping({ pattern: "https://example.com/%2a" }), "https://example.com/%2A")).toBe(true);
    expect(mappingMatches(mapping({ pattern: "https://example.com/%2A" }), "https://example.com/%252A")).toBe(false);
    expect(mappingMatches(mapping({ pattern: "https://example.com/%252A" }), "https://example.com/%252A")).toBe(true);
    expect(mappingMatches(mapping({ pattern: "https://example.com/%2a" }), "https://example.com/value")).toBe(false);
    expect(parseUrlPattern("https://example.com/%ff").path).toEqual(["%FF"]);
    expect(mappingMatches(mapping({ pattern: "https://example.com/%FF" }), "https://example.com/%ff")).toBe(true);
  });

  it("bounds multi-segment wildcard matching", () => {
    const wildcards = Array.from({ length: 24 }, () => "**").join("/");
    const path = Array.from({ length: 24 }, () => "value").join("/");
    expect(mappingMatches(mapping({ pattern: `https://example.com/${wildcards}/missing` }), `https://example.com/${path}`)).toBe(false);
  });

  it("rejects invalid wildcard placement and sensitive URL components", () => {
    for (const pattern of ["https://api*.example.com/", "https://api.example.com/**:123", "https://api.example.com/path?x=1", "https://user@api.example.com/", "https://example.com\\evil/path"]) expect(() => parseUrlPattern(pattern)).toThrow();
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
