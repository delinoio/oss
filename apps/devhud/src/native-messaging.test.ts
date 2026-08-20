// @vitest-environment jsdom

import { invoke } from "@tauri-apps/api/core";
import { afterEach, describe, expect, it, vi } from "vitest";
import { extensionConfiguration, nativeMessaging } from "./native-messaging.ts";
import { defaultDevHudSettings } from "./settings-contract.ts";

vi.mock("@tauri-apps/api/core", () => ({ invoke: vi.fn() }));

afterEach(() => {
  delete window.__TAURI_INTERNALS__;
  vi.clearAllMocks();
});

describe("Native Messaging extension configuration", () => {
  it("groups origins and orders non-secret matchers by mapping precedence", () => {
    vi.stubGlobal("navigator", { languages: ["en"] });
    const configuration = extensionConfiguration({ ...defaultDevHudSettings, urlMappings: [
      { id: "01900000-0000-7000-8000-000000000001", pattern: "https://example.com/**", repository: { owner: "secret-owner", name: "secret-repository" }, credentialProfileRef: "secret-profile", priority: 1, chromeOrigin: "https://example.com", updatedAt: "2026-01-01T00:00:00.000Z" },
      { id: "01900000-0000-7000-8000-000000000002", pattern: "https://example.com/docs/**", repository: { owner: "other-secret-owner", name: "other-secret-repository" }, credentialProfileRef: "other-secret-profile", priority: 2, chromeOrigin: "https://example.com", updatedAt: "2026-01-02T00:00:00.000Z" },
    ] });
    expect(configuration.origins).toHaveLength(1);
    expect(configuration.origins[0]?.mappings.map((mapping) => mapping.mappingId)).toEqual([
      "01900000-0000-7000-8000-000000000002",
      "01900000-0000-7000-8000-000000000001",
    ]);
    expect(configuration.origins[0]?.mappings[0]?.matcher).toEqual({ scheme: "https", host: ["example", "com"], hostIsIpLiteral: false, port: "", path: ["docs", "**"] });
    expect(JSON.stringify(configuration)).not.toMatch(/secret-owner|secret-repository|secret-profile/u);
  });

  it("attaches the latest context to the captured draft revision", async () => {
    window.__TAURI_INTERNALS__ = { invoke: vi.fn() };
    vi.mocked(invoke).mockResolvedValue(null);

    await expect(nativeMessaging.takeContext("019b0000-0000-7000-8000-000000000001", 4)).resolves.toBeNull();

    expect(invoke).toHaveBeenCalledWith("native_messaging_take_context", {
      draftId: "019b0000-0000-7000-8000-000000000001",
      expectedRevision: 4,
    });
  });
});
