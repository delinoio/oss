import { canonicalDevHudSettings, defaultDevHudSettings, parseDevHudSettings, type DevHudSettingsV1 } from "./settings-contract";
import { isValidLogtoAudience, normalizeLogtoIssuer } from "./identity-contract.ts";

const prefix = "devhud.identity.v1.";
const guestSettingsKey = `${prefix}guest-settings`;
const guestUsedKey = `${prefix}guest-used`;
const accountPrefix = `${prefix}account.`;
const invalidatedSettingsKeys = new Set<string>();

type ReadStorage = Pick<Storage, "getItem">;
type WriteStorage = Pick<Storage, "setItem">;
type MutableStorage = Pick<Storage, "getItem" | "setItem" | "removeItem" | "key" | "length">;

export function readGuestSettings(storage: ReadStorage): DevHudSettingsV1 {
  try {
    const source = storage.getItem(guestSettingsKey);
    return source === null ? defaultDevHudSettings : parseDevHudSettings(JSON.parse(source));
  } catch {
    return defaultDevHudSettings;
  }
}

export function writeGuestSettings(storage: WriteStorage, settings: DevHudSettingsV1): void {
  try {
    storage.setItem(guestSettingsKey, canonicalDevHudSettings(settings));
    storage.setItem(guestUsedKey, "true");
  } catch {
    // Guest settings remain usable in memory when Web Storage becomes unavailable.
  }
}

export function hasGuestSettings(storage: ReadStorage): boolean {
  try {
    return storage.getItem(guestUsedKey) === "true";
  } catch {
    return false;
  }
}

export function clearGuestImportMarker(storage: Pick<Storage, "removeItem">): void {
  storage.removeItem(guestUsedKey);
}

export interface CachedSettings {
  readonly settings: DevHudSettingsV1;
  readonly revision: bigint;
  readonly cachedAt: string;
}

export interface CachedIdentityBootstrap {
  readonly issuer: string;
  readonly audience: string;
  readonly clientId: string;
  readonly redirectUri: "devhud://auth/callback";
}

export function readCachedIdentityBootstrap(storage: ReadStorage, apiOrigin: string): CachedIdentityBootstrap | null {
  try {
    const value: unknown = JSON.parse(storage.getItem(accountKey(apiOrigin, "bootstrap")) ?? "null");
    if (value === null || typeof value !== "object" || Array.isArray(value)) return null;
    const record = value as Record<string, unknown>;
    if (record.redirectUri !== "devhud://auth/callback" || typeof record.clientId !== "string" || !/^[\x21-\x7e]{1,256}$/u.test(record.clientId)) return null;
    const issuer = normalizeLogtoIssuer(record.issuer);
    if (issuer === null || !isValidLogtoAudience(record.audience)) return null;
    return { issuer, audience: record.audience, clientId: record.clientId, redirectUri: record.redirectUri };
  } catch {
    return null;
  }
}

export function writeCachedIdentityBootstrap(storage: WriteStorage, apiOrigin: string, bootstrap: CachedIdentityBootstrap): void {
  try {
    storage.setItem(accountKey(apiOrigin, "bootstrap"), JSON.stringify(bootstrap));
  } catch {
    // Identity bootstrap caching is optional; the online session remains usable when persistence is unavailable.
  }
}

export function readAuthenticatedSettingsCache(storage: ReadStorage, apiOrigin: string): CachedSettings | null {
  const key = accountKey(apiOrigin, "settings");
  if (invalidatedSettingsKeys.has(key)) return null;
  try {
    const value: unknown = JSON.parse(storage.getItem(key) ?? "null");
    if (value === null || typeof value !== "object" || Array.isArray(value)) return null;
    const record = value as Record<string, unknown>;
    if (typeof record.revision !== "string" || !/^\d+$/u.test(record.revision) || typeof record.cachedAt !== "string") return null;
    return { settings: parseDevHudSettings(record.settings), revision: BigInt(record.revision), cachedAt: record.cachedAt };
  } catch {
    return null;
  }
}

export function writeAuthenticatedSettingsCache(storage: WriteStorage, apiOrigin: string, cache: CachedSettings): void {
  const key = accountKey(apiOrigin, "settings");
  try {
    storage.setItem(key, JSON.stringify({ settings: cache.settings, revision: cache.revision.toString(), cachedAt: cache.cachedAt }));
    invalidatedSettingsKeys.delete(key);
  } catch {
    // Settings caching is best-effort; a valid service response must remain usable without Web Storage.
  }
}

export function clearAuthenticatedSettingsCache(storage: Pick<Storage, "removeItem">, apiOrigin: string): void {
  const key = accountKey(apiOrigin, "settings");
  invalidatedSettingsKeys.add(key);
  try {
    storage.removeItem(key);
  } catch {
    // The in-memory tombstone prevents this session from reading a stale persisted snapshot.
  }
}

export function clearAuthenticatedOriginData(storage: MutableStorage, apiOrigin: string): void {
  invalidatedSettingsKeys.add(accountKey(apiOrigin, "settings"));
  const originPrefix = accountKey(apiOrigin, "");
  removeMatching(storage, (key) => key.startsWith(originPrefix));
}

export function clearAllContractedLocalData(storage: MutableStorage): void {
  removeMatching(storage, (key) => key.startsWith(prefix) || /^(?:devhud\.(?:deck|draft|clone|cache|permission|pairing)|devhud-extension\.)/u.test(key));
}

function accountKey(apiOrigin: string, suffix: string): string {
  const encoded = new TextEncoder().encode(new URL(apiOrigin).origin);
  let binary = "";
  for (const byte of encoded) binary += String.fromCharCode(byte);
  return `${accountPrefix}${btoa(binary).replaceAll("+", "-").replaceAll("/", "_").replace(/=+$/u, "")}.${suffix}`;
}

function removeMatching(storage: MutableStorage, predicate: (key: string) => boolean): void {
  const keys: string[] = [];
  for (let index = 0; index < storage.length; index += 1) {
    const key = storage.key(index);
    if (key !== null && predicate(key)) keys.push(key);
  }
  for (const key of keys) storage.removeItem(key);
}
