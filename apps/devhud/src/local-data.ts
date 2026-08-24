import type { StaticCapability } from "@delinoio/devhud-api-client";
import { clearInMemoryDiagnosticEvents } from "./diagnostics";
import { defaultDevHudSettings, parseDevHudSettings, type DevHudSettingsV1 } from "./settings-contract";
import { isValidLogtoAudience, normalizeLogtoIssuer, normalizeNetworkOrigin, normalizePublicAssetUrl } from "./identity-contract.ts";

const prefix = "devhud.identity.v1.";
const guestSettingsKey = `${prefix}guest-settings`;
const guestUsedKey = `${prefix}guest-used`;
const accountPrefix = `${prefix}account.`;
const invalidatedSettingsKeys = new Set<string>();
const clearedGuestSettings = new WeakSet<object>();
const inMemoryGuestSettings = new WeakMap<object, DevHudSettingsV1>();
export const DeviceLocalSettingsMaximumBytes = 1024 * 1024;

type ReadStorage = Pick<Storage, "getItem">;
type WriteStorage = Pick<Storage, "setItem">;
type MutableStorage = Pick<Storage, "getItem" | "setItem" | "removeItem" | "key" | "length">;

export function readGuestSettings(storage: ReadStorage): DevHudSettingsV1 {
  if (clearedGuestSettings.has(storage)) return defaultDevHudSettings;
  const inMemory = inMemoryGuestSettings.get(storage);
  if (inMemory !== undefined) return inMemory;
  try {
    const source = storage.getItem(guestSettingsKey);
    if (source === null) return defaultDevHudSettings;
    const parsed = parseDevHudSettings(JSON.parse(source));
    assertDeviceLocalSettingsPersistable(parsed);
    return parsed;
  } catch {
    return defaultDevHudSettings;
  }
}

export function writeGuestSettings(storage: WriteStorage, settings: DevHudSettingsV1): boolean {
  const parsed = parseDevHudSettings(settings);
  assertDeviceLocalSettingsPersistable(parsed);
  clearedGuestSettings.delete(storage);
  try {
    // The snapshot itself is the durable import marker so quota failures cannot split the two values.
    // Device-local shortcut bindings and repository prompts are deliberately
    // present here even though the synchronized canonical projection omits them.
    storage.setItem(guestSettingsKey, JSON.stringify(parsed));
    inMemoryGuestSettings.delete(storage);
    return true;
  } catch {
    // Preserve both the snapshot and its import marker for this session when persistence is unavailable.
    inMemoryGuestSettings.set(storage, parsed);
    return false;
  }
}

export function hasGuestSettings(storage: ReadStorage): boolean {
  if (clearedGuestSettings.has(storage)) return false;
  if (inMemoryGuestSettings.has(storage)) return true;
  try {
    return storage.getItem(guestSettingsKey) !== null || storage.getItem(guestUsedKey) === "true";
  } catch {
    return false;
  }
}

export function clearGuestImportMarker(storage: Pick<Storage, "removeItem">): void {
  clearedGuestSettings.add(storage);
  inMemoryGuestSettings.delete(storage);
  for (const key of [guestSettingsKey, guestUsedKey]) {
    try {
      storage.removeItem(key);
    } catch {
      // The in-memory tombstone prevents a failed removal from reopening the import choice this session.
    }
  }
}

export interface CachedSettings {
  readonly settings: DevHudSettingsV1;
  readonly revision: bigint;
  readonly contentSHA256?: Uint8Array;
  readonly cachedAt: string;
}

export interface CachedIdentityBootstrap {
  readonly issuer: string;
  readonly audience: string;
  readonly clientId: string;
  readonly redirectUri: "devhud://auth/callback";
  readonly publicAssetBaseUrl: string | null;
  readonly officialUploadOrigin: string | null;
  readonly capabilities: readonly StaticCapability[];
}

export function readCachedIdentityBootstrap(storage: ReadStorage, apiOrigin: string): CachedIdentityBootstrap | null {
  try {
    const value: unknown = JSON.parse(storage.getItem(accountKey(apiOrigin, "bootstrap")) ?? "null");
    if (value === null || typeof value !== "object" || Array.isArray(value)) return null;
    const record = value as Record<string, unknown>;
    if (record.redirectUri !== "devhud://auth/callback" || typeof record.clientId !== "string" || !/^[\x21-\x7e]{1,256}$/u.test(record.clientId)) return null;
    const issuer = normalizeLogtoIssuer(record.issuer);
    if (issuer === null || !isValidLogtoAudience(record.audience)) return null;
    const publicAssetBaseUrl = record.publicAssetBaseUrl === undefined ? null : normalizePublicAssetUrl(record.publicAssetBaseUrl);
    if (record.publicAssetBaseUrl !== undefined && publicAssetBaseUrl === null) return null;
    const capabilities = record.capabilities === undefined ? [] : record.capabilities;
    if (!Array.isArray(capabilities) || capabilities.some((capability) => !Number.isInteger(capability))) return null;
    const officialUploadOrigin = record.officialUploadOrigin === undefined || record.officialUploadOrigin === null ? null : normalizeNetworkOrigin(record.officialUploadOrigin);
    if (record.officialUploadOrigin !== undefined && record.officialUploadOrigin !== null && officialUploadOrigin === null) return null;
    return { issuer, audience: record.audience, clientId: record.clientId, redirectUri: record.redirectUri, publicAssetBaseUrl, officialUploadOrigin, capabilities: Object.freeze([...new Set(capabilities as StaticCapability[])]) };
  } catch {
    return null;
  }
}

export function writeCachedIdentityBootstrap(storage: WriteStorage, apiOrigin: string, bootstrap: Omit<CachedIdentityBootstrap, "officialUploadOrigin"> & { readonly officialUploadOrigin?: string | null }): void {
  try {
    storage.setItem(accountKey(apiOrigin, "bootstrap"), JSON.stringify({ ...bootstrap, officialUploadOrigin: bootstrap.officialUploadOrigin ?? null }));
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
    const contentSHA256 = record.contentSHA256 === undefined ? new Uint8Array() : decodeDigest(record.contentSHA256);
    if (contentSHA256 === null) return null;
    const settings = parseDevHudSettings(record.settings);
    assertDeviceLocalSettingsPersistable(settings);
    return { settings, revision: BigInt(record.revision), contentSHA256, cachedAt: record.cachedAt };
  } catch {
    return null;
  }
}

export function writeAuthenticatedSettingsCache(storage: WriteStorage, apiOrigin: string, cache: CachedSettings): boolean {
  const key = accountKey(apiOrigin, "settings");
  try {
    assertDeviceLocalSettingsPersistable(cache.settings);
    const contentSHA256 = cache.contentSHA256;
    if (contentSHA256 !== undefined && contentSHA256.byteLength !== 0 && contentSHA256.byteLength !== 32) throw new TypeError("settings content digest must contain 32 raw bytes");
    storage.setItem(key, JSON.stringify({ settings: cache.settings, revision: cache.revision.toString(), ...(contentSHA256?.byteLength === 32 ? { contentSHA256: encodeDigest(contentSHA256) } : {}), cachedAt: cache.cachedAt }));
    invalidatedSettingsKeys.delete(key);
    return true;
  } catch {
    // Settings caching is best-effort; a valid service response must remain usable without Web Storage.
    return false;
  }
}

export function assertDeviceLocalSettingsPersistable(settings: DevHudSettingsV1): void {
  if (deviceLocalSettingsJSON(settings).byteLength > DeviceLocalSettingsMaximumBytes) {
    throw new TypeError("device-local settings exceed the 1 MiB aggregate limit");
  }
}

export function deviceLocalSettingsEqual(left: DevHudSettingsV1, right: DevHudSettingsV1): boolean {
  const leftBytes = deviceLocalSettingsJSON(left);
  const rightBytes = deviceLocalSettingsJSON(right);
  if (leftBytes.byteLength !== rightBytes.byteLength) return false;
  return leftBytes.every((byte, index) => byte === rightBytes[index]);
}

function deviceLocalSettingsJSON(settings: DevHudSettingsV1): Uint8Array {
  return new TextEncoder().encode(JSON.stringify({ shortcuts: settings.shortcuts, agents: settings.agents }));
}

function encodeDigest(value: Uint8Array): string {
  return Array.from(value, (byte) => byte.toString(16).padStart(2, "0")).join("");
}

function decodeDigest(value: unknown): Uint8Array | null {
  if (typeof value !== "string" || !/^[0-9a-f]{64}$/u.test(value)) return null;
  return Uint8Array.from(value.match(/.{2}/gu) ?? [], (octet) => Number.parseInt(octet, 16));
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

export function clearAllContractedLocalData(storage: MutableStorage): boolean {
  clearedGuestSettings.add(storage);
  inMemoryGuestSettings.delete(storage);
  clearInMemoryDiagnosticEvents(storage);
  return removeMatching(storage, (key) => key.startsWith(prefix) || /^(?:devhud\.(?:deck|draft|clone|cache|permission|pairing|diagnostics|local-agent)|devhud-extension\.)/u.test(key));
}

function accountKey(apiOrigin: string, suffix: string): string {
  const encoded = new TextEncoder().encode(new URL(apiOrigin).origin);
  let binary = "";
  for (const byte of encoded) binary += String.fromCharCode(byte);
  return `${accountPrefix}${btoa(binary).replaceAll("+", "-").replaceAll("/", "_").replace(/=+$/u, "")}.${suffix}`;
}

function removeMatching(storage: MutableStorage, predicate: (key: string) => boolean): boolean {
  const keys: string[] = [];
  let complete = true;
  try {
    for (let index = 0; index < storage.length; index += 1) {
      const key = storage.key(index);
      if (key !== null && predicate(key)) keys.push(key);
    }
  } catch {
    complete = false;
    // Callers decide whether incomplete Web Storage cleanup defers their native purge.
  }
  for (const key of keys) {
    try {
      storage.removeItem(key);
    } catch {
      complete = false;
      // Continue removing independent entries when one Web Storage operation fails.
    }
  }
  return complete;
}
