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

  it("publishes configuration with the current identity scope", async () => {
    window.__TAURI_INTERNALS__ = { invoke: vi.fn() };
    vi.stubGlobal("navigator", { languages: ["en"] });
    vi.mocked(invoke).mockResolvedValue(undefined);
    const scopeId = "01900000-0000-7000-8000-000000000001";

    await nativeMessaging.configure(defaultDevHudSettings, scopeId);

    expect(invoke).toHaveBeenCalledWith("native_messaging_replace_configuration", {
      configuration: { origins: [], language: "en" },
      scopeId,
    });
  });

  it("serializes configuration replacements in publication order", async () => {
    window.__TAURI_INTERNALS__ = { invoke: vi.fn() };
    vi.stubGlobal("navigator", { languages: ["en"] });
    let resolveFirst: (() => void) | undefined;
    vi.mocked(invoke)
      .mockImplementationOnce(() => new Promise<void>((resolve) => { resolveFirst = resolve; }))
      .mockResolvedValueOnce(undefined);
    const firstScope = "01900000-0000-7000-8000-000000000001";
    const secondScope = "01900000-0000-7000-8000-000000000002";

    const first = nativeMessaging.configure(defaultDevHudSettings, firstScope);
    const second = nativeMessaging.configure(defaultDevHudSettings, secondScope);
    await vi.waitFor(() => expect(invoke).toHaveBeenCalledTimes(1));
    expect(invoke).toHaveBeenNthCalledWith(1, "native_messaging_replace_configuration", expect.objectContaining({ scopeId: firstScope }));

    resolveFirst?.();
    await expect(Promise.all([first, second])).resolves.toEqual([undefined, undefined]);
    expect(invoke).toHaveBeenNthCalledWith(2, "native_messaging_replace_configuration", expect.objectContaining({ scopeId: secondScope }));
  });

  it("continues configuration publication after a failed replacement", async () => {
    window.__TAURI_INTERNALS__ = { invoke: vi.fn() };
    vi.stubGlobal("navigator", { languages: ["en"] });
    vi.mocked(invoke)
      .mockRejectedValueOnce(new Error("unavailable"))
      .mockResolvedValueOnce(undefined);

    const failed = nativeMessaging.configure(defaultDevHudSettings, "01900000-0000-7000-8000-000000000001");
    const current = nativeMessaging.configure(defaultDevHudSettings, "01900000-0000-7000-8000-000000000002");

    await expect(failed).rejects.toThrow("unavailable");
    await expect(current).resolves.toBeUndefined();
    expect(invoke).toHaveBeenCalledTimes(2);
  });
});
