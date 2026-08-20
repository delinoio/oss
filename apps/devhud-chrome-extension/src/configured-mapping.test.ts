import { describe, expect, it } from "vitest";
import { matcherMatches, selectConfiguredMapping, type ConfiguredUrlMatcher, type ExtensionConfiguration } from "./configured-mapping.js";

const matcher = (path: readonly string[]): ConfiguredUrlMatcher => ({
  scheme: "https",
  host: ["example", "com"],
  hostIsIpLiteral: false,
  port: "",
  path,
});

describe("configured URL mapping selection", () => {
  it("selects the ordered mapping whose pattern matches the full captured URL", () => {
    const configuration: ExtensionConfiguration = {
      origins: [{
        origin: "https://example.com",
        mappings: [
          { mappingId: "docs", matcher: matcher(["docs", "**"]) },
          { mappingId: "teams", matcher: matcher(["teams", "**"]) },
          { mappingId: "fallback", matcher: matcher(["**"]) },
        ],
      }],
      language: "en",
    };

    expect(selectConfiguredMapping(configuration, "https://example.com/docs/guide?token=ignored#fragment")).toBe("docs");
    expect(selectConfiguredMapping(configuration, "https://example.com/teams/platform")).toBe("teams");
    expect(selectConfiguredMapping(configuration, "https://example.com/other")).toBe("fallback");
    expect(selectConfiguredMapping(configuration, "https://other.example/docs/guide")).toBeNull();
  });

  it("preserves canonical path and host matching semantics", () => {
    expect(matcherMatches(matcher(["한글"]), "https://example.com/%ED%95%9C%EA%B8%80")).toBe(true);
    expect(matcherMatches({ ...matcher(["**"]), host: ["*", "example", "com"] }, "https://api.example.com/path")).toBe(true);
    expect(matcherMatches({ ...matcher(["**"]), host: ["*", "example", "com"] }, "https://127.0.0.1/path")).toBe(false);
  });

  it("keeps configured custom ports exact after host permission is granted", () => {
    const configuration: ExtensionConfiguration = {
      origins: [{
        origin: "https://example.com:8443",
        mappings: [{ mappingId: "custom-port", matcher: { ...matcher(["**"]), port: "8443" } }],
      }],
      language: "en",
    };

    expect(selectConfiguredMapping(configuration, "https://example.com:8443/page")).toBe("custom-port");
    expect(selectConfiguredMapping(configuration, "https://example.com:9443/page")).toBeNull();
  });
});
