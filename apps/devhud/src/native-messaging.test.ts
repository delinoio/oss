import { describe, expect, it, vi } from "vitest";
import { extensionConfiguration } from "./native-messaging.ts";
import { defaultDevHudSettings } from "./settings-contract.ts";

describe("Native Messaging extension configuration", () => {
  it("exposes only configured mapping origins and non-secret IDs", () => {
    vi.stubGlobal("navigator", { languages: ["en"] });
    const configuration = extensionConfiguration({ ...defaultDevHudSettings, urlMappings: [{ id: "01900000-0000-7000-8000-000000000001", pattern: "https://example.com/**", repository: { owner: "secret-owner", name: "secret-repository" }, credentialProfileRef: "secret-profile", priority: 1, chromeOrigin: "https://example.com", updatedAt: "2026-01-01T00:00:00Z" }] });
    expect(configuration).toEqual({ origins: [{ origin: "https://example.com", mappingId: "01900000-0000-7000-8000-000000000001" }], language: "en" });
    expect(JSON.stringify(configuration)).not.toMatch(/secret-owner|secret-repository|secret-profile/u);
  });
});
