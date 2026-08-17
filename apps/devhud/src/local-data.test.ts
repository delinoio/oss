import { describe, expect, it } from "vitest";
import { clearAllContractedLocalData, clearAuthenticatedOriginData, hasGuestSettings, readAuthenticatedSettingsCache, readCachedIdentityBootstrap, writeAuthenticatedSettingsCache, writeCachedIdentityBootstrap, writeGuestSettings } from "./local-data";
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
    const bootstrap = { issuer: "https://identity.example/", audience: "https://api.example", clientId: "desktop", redirectUri: "devhud://auth/callback" as const };
    writeCachedIdentityBootstrap(storage, apiOrigin, bootstrap);
    writeAuthenticatedSettingsCache(storage, apiOrigin, { settings: defaultDevHudSettings, revision: 7n, cachedAt: "2026-08-17T00:00:00.000Z" });

    expect(readCachedIdentityBootstrap(storage, apiOrigin)).toEqual(bootstrap);
    expect(readAuthenticatedSettingsCache(storage, apiOrigin)?.revision).toBe(7n);
    expect(readCachedIdentityBootstrap(storage, "https://other.example")).toBeNull();

    clearAuthenticatedOriginData(storage, apiOrigin);
    expect(readCachedIdentityBootstrap(storage, apiOrigin)).toBeNull();
    expect(readAuthenticatedSettingsCache(storage, apiOrigin)).toBeNull();
  });

  it("removes identity, guest, draft, cache, pairing, and permission data on logout", () => {
    const storage = new MemoryStorage();
    writeGuestSettings(storage, defaultDevHudSettings);
    for (const key of ["devhud.deck.v1", "devhud.draft.v1", "devhud.cache.v1", "devhud.pairing.v1", "devhud.permission.v1", "devhud-extension.session"]) storage.setItem(key, "sensitive");
    storage.setItem("devhud.shell.preferences.v1", "local-device-preferences");

    clearAllContractedLocalData(storage);

    expect(hasGuestSettings(storage)).toBe(false);
    expect(storage.length).toBe(1);
    expect(storage.getItem("devhud.shell.preferences.v1")).toBe("local-device-preferences");
  });
});
