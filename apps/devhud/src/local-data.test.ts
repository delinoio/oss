import { describe, expect, it } from "vitest";
import { StaticCapability } from "@delinoio/devhud-api-client";
import { clearAllContractedLocalData, clearAuthenticatedOriginData, clearAuthenticatedSettingsCache, clearGuestImportMarker, hasGuestSettings, readAuthenticatedSettingsCache, readCachedIdentityBootstrap, readGuestSettings, writeAuthenticatedSettingsCache, writeCachedIdentityBootstrap, writeGuestSettings } from "./local-data";
import { defaultDevHudSettings } from "./settings-contract";

class MemoryStorage implements Storage {
  readonly #values = new Map<string, string>();
  get length() { return this.#values.size; }
  clear() { this.#values.clear(); }
  getItem(key: string) { return this.#values.get(key) ?? null; }
  key(index: number) { return [...this.#values.keys()][index] ?? null; }
  removeItem(key: string) { this.#values.delete(key); }
  setItem(key: string, value: string) { this.#values.set(key, value); }
}

describe("local identity data lifecycle", () => {
  it("isolates cached bootstrap and settings by API origin", () => {
    const storage = new MemoryStorage();
    const apiOrigin = "https://api.example";
    const bootstrap = { issuer: "https://identity.example/", audience: "https://api.example", clientId: "desktop", redirectUri: "devhud://auth/callback" as const, publicAssetBaseUrl: "https://images.example/", capabilities: [StaticCapability.CRASH_REPORTS] };
    writeCachedIdentityBootstrap(storage, apiOrigin, bootstrap);
    writeAuthenticatedSettingsCache(storage, apiOrigin, { settings: defaultDevHudSettings, revision: 7n, cachedAt: "2026-08-17T00:00:00.000Z" });

    expect(readCachedIdentityBootstrap(storage, apiOrigin)).toEqual(bootstrap);
    expect(readAuthenticatedSettingsCache(storage, apiOrigin)?.revision).toBe(7n);
    expect(readCachedIdentityBootstrap(storage, "https://other.example")).toBeNull();

    clearAuthenticatedOriginData(storage, apiOrigin);
    expect(readCachedIdentityBootstrap(storage, apiOrigin)).toBeNull();
    expect(readAuthenticatedSettingsCache(storage, apiOrigin)).toBeNull();
  });

  it("preserves legacy bootstrap caches for offline identity restoration", () => {
    const storage = new MemoryStorage();
    const apiOrigin = "http://127.0.0.1:46307";
    const legacy = {
      issuer: "http://127.0.0.1:46307/oidc",
      audience: "devhud-api",
      clientId: "desktop-client",
      redirectUri: "devhud://auth/callback" as const,
      capabilities: [StaticCapability.SETTINGS_SYNC],
    };
    writeCachedIdentityBootstrap(storage, apiOrigin, { ...legacy, publicAssetBaseUrl: "http://127.0.0.1:9000" });
    const cacheKey = storage.key(0);
    if (cacheKey === null) throw new Error("bootstrap cache key was not written");
    storage.setItem(cacheKey, JSON.stringify(legacy));

    expect(readCachedIdentityBootstrap(storage, apiOrigin)).toMatchObject({
      issuer: "http://127.0.0.1:46307/oidc",
      publicAssetBaseUrl: null,
      capabilities: [StaticCapability.SETTINGS_SYNC],
    });
  });

  it("removes identity, guest, draft, cache, pairing, permission, and local-agent data on logout", () => {
    const storage = new MemoryStorage();
    writeGuestSettings(storage, defaultDevHudSettings);
    for (const key of ["devhud.deck.v1", "devhud.draft.v1", "devhud.cache.v1", "devhud.pairing.v1", "devhud.permission.v1", "devhud.diagnostics.v1.events", "devhud.diagnostics.v1.correlations", "devhud.local-agent-consent.v1", "devhud.local-agent-executables.v1", "devhud-extension.session"]) storage.setItem(key, "sensitive");
    storage.setItem("devhud.shell.preferences.v1", "local-device-preferences");

    expect(clearAllContractedLocalData(storage)).toBe(true);

    expect(hasGuestSettings(storage)).toBe(false);
    expect(storage.length).toBe(1);
    expect(storage.getItem("devhud.shell.preferences.v1")).toBe("local-device-preferences");
  });

  it("keeps bulk cleanup best-effort when Web Storage enumeration or removal fails", () => {
    const enumerationFailure = {
      get length(): number { throw new DOMException("denied", "SecurityError"); },
      getItem: () => null,
      key: () => null,
      removeItem: () => {},
      setItem: () => {},
    };
    const removalFailure = {
      length: 1,
      getItem: () => null,
      key: () => "devhud.identity.v1.guest-settings",
      removeItem: () => { throw new DOMException("denied", "SecurityError"); },
      setItem: () => {},
    };

    expect(clearAllContractedLocalData(enumerationFailure)).toBe(false);
    expect(clearAllContractedLocalData(removalFailure)).toBe(false);
  });

  it("keeps authenticated cache writes best-effort when persistence rejects writes", () => {
    const storage = { setItem: () => { throw new DOMException("quota exceeded", "QuotaExceededError"); } };
    expect(() => writeCachedIdentityBootstrap(storage, "https://api.example", { issuer: "https://identity.example/", audience: "https://api.example", clientId: "desktop", redirectUri: "devhud://auth/callback", publicAssetBaseUrl: "https://images.example/", capabilities: [StaticCapability.CRASH_REPORTS] })).not.toThrow();
    expect(() => writeAuthenticatedSettingsCache(storage, "https://api.example", { settings: defaultDevHudSettings, revision: 1n, cachedAt: "2026-08-17T00:00:00.000Z" })).not.toThrow();
  });

  it("treats an unavailable guest marker as absent", () => {
    const storage = { getItem: () => { throw new DOMException("denied", "SecurityError"); } };
    expect(hasGuestSettings(storage)).toBe(false);
  });

  it("uses the guest snapshot as one durable import record", () => {
    const storage = new MemoryStorage();
    writeGuestSettings(storage, defaultDevHudSettings);

    expect(storage.getItem("devhud.identity.v1.guest-settings")).not.toBeNull();
    expect(storage.getItem("devhud.identity.v1.guest-used")).toBeNull();
    expect(hasGuestSettings(storage)).toBe(true);
  });

  it("retains the guest snapshot and import marker in memory when persistence rejects writes", () => {
    const storage = {
      getItem: () => null,
      removeItem: () => {},
      setItem: () => { throw new DOMException("quota exceeded", "QuotaExceededError"); },
    };
    const settings = { ...defaultDevHudSettings, appearance: { ...defaultDevHudSettings.appearance, theme: "dark" as const } };

    writeGuestSettings(storage, settings);

    expect(hasGuestSettings(storage)).toBe(true);
    expect(readGuestSettings(storage)).toEqual(settings);
    clearGuestImportMarker(storage);
    expect(hasGuestSettings(storage)).toBe(false);
    expect(readGuestSettings(storage)).toEqual(defaultDevHudSettings);
  });

  it("keeps guest-marker removal best-effort when Web Storage throws", () => {
    const storage = {
      getItem: () => null,
      removeItem: () => { throw new DOMException("denied", "SecurityError"); },
    };

    expect(() => clearGuestImportMarker(storage)).not.toThrow();
    expect(hasGuestSettings(storage)).toBe(false);
  });

  it("clears only the authenticated settings snapshot when a session becomes invalid", () => {
    const storage = new MemoryStorage();
    const apiOrigin = "https://api.example";
    const bootstrap = { issuer: "https://identity.example/", audience: "https://api.example", clientId: "desktop", redirectUri: "devhud://auth/callback" as const, publicAssetBaseUrl: "https://images.example/", capabilities: [StaticCapability.CRASH_REPORTS] };
    writeCachedIdentityBootstrap(storage, apiOrigin, bootstrap);
    writeAuthenticatedSettingsCache(storage, apiOrigin, { settings: defaultDevHudSettings, revision: 7n, cachedAt: "2026-08-17T00:00:00.000Z" });

    clearAuthenticatedSettingsCache(storage, apiOrigin);

    expect(readAuthenticatedSettingsCache(storage, apiOrigin)).toBeNull();
    expect(readCachedIdentityBootstrap(storage, apiOrigin)).toEqual(bootstrap);
  });

  it("tombstones an invalid session cache when Web Storage rejects removal", () => {
    const storage = new MemoryStorage();
    const apiOrigin = "https://removal-failure.example";
    writeAuthenticatedSettingsCache(storage, apiOrigin, { settings: defaultDevHudSettings, revision: 7n, cachedAt: "2026-08-17T00:00:00.000Z" });

    clearAuthenticatedSettingsCache({ removeItem: () => { throw new DOMException("denied", "SecurityError"); } }, apiOrigin);

    expect(readAuthenticatedSettingsCache(storage, apiOrigin)).toBeNull();
  });
});
