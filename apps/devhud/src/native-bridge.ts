import { normalizeLogtoIssuer } from "./identity-contract.ts";
import { ClassicPatCreationUrl, FineGrainedPatCreationUrl } from "./github-links.ts";
import { defaultDesktopShortcutBindings, parseDesktopShortcutBindings, type DesktopShortcutBindings, type ShortcutActionId, type ShortcutValidationCode } from "./shortcuts.ts";

export const NativeBridgeVersion = 1 as const;

export const RuntimePlatform = {
  Desktop: "desktop",
  Ios: "ios",
  Android: "android",
  Browser: "browser",
} as const;
export type RuntimePlatform = (typeof RuntimePlatform)[keyof typeof RuntimePlatform];

export const LifecycleState = {
  Active: "active",
  Inactive: "inactive",
  Background: "background",
} as const;
export type LifecycleState = (typeof LifecycleState)[keyof typeof LifecycleState];

export const SecureSettingKind = {
  LogtoSession: "logto-session",
  GithubPat: "github-pat",
  R2AccessKeyId: "r2-access-key-id",
  R2SecretAccessKey: "r2-secret-access-key",
} as const;
export type SecureSettingKind = (typeof SecureSettingKind)[keyof typeof SecureSettingKind];

export const NotificationPermission = {
  NotDetermined: "not-determined",
  Denied: "denied",
  Authorized: "authorized",
} as const;
export type NotificationPermission = (typeof NotificationPermission)[keyof typeof NotificationPermission];

export const NativeBridgeErrorCode = {
  InvalidArgument: "invalid-argument",
  PermissionDenied: "permission-denied",
  NotConfigured: "not-configured",
  Unsupported: "unsupported",
  StorageFailure: "storage-failure",
  PlatformFailure: "platform-failure",
} as const;
export type NativeBridgeErrorCode = (typeof NativeBridgeErrorCode)[keyof typeof NativeBridgeErrorCode];

export interface RuntimeSnapshot {
  readonly bridgeVersion: typeof NativeBridgeVersion;
  readonly platform: RuntimePlatform;
  readonly operatingSystem: "macos" | "windows" | "linux" | "ios" | "android" | "browser";
  readonly architecture: string;
  readonly osVersion: string;
  readonly appVersion: string;
  readonly buildId: string;
  readonly tauriRevision: string;
  readonly cefRevision: string;
  readonly lifecycle: LifecycleState;
  readonly capabilities: {
    readonly secureSettings: boolean;
    readonly notifications: boolean;
    readonly storeUpdates: boolean;
    readonly widgets: false;
  };
}

export type SecureSettingRef =
  | { readonly kind: typeof SecureSettingKind.GithubPat; readonly profileId: string; readonly scopeId: string }
  | { readonly kind: Exclude<SecureSettingKind, typeof SecureSettingKind.GithubPat>; readonly profileId: string };

export const DeckNotificationKind = {
  Review: "review",
  Checks: "checks",
  Merged: "merged",
  Closed: "closed",
} as const;
export type DeckNotificationKind = (typeof DeckNotificationKind)[keyof typeof DeckNotificationKind];

export interface DeckNotification {
  readonly id: string;
  readonly deckId: string;
  readonly kind: DeckNotificationKind;
  readonly title: string;
  readonly body: string;
}

export interface WidgetDeckSnapshot {
  readonly deckId: string;
  readonly updatedAt: string;
  readonly title: string;
  readonly pullRequests: readonly { readonly title: string; readonly url: string }[];
}

type NativeBridgeRequestV1Base =
  | { readonly operation: "runtime.snapshot" }
  | { readonly operation: "session.configure-origins"; readonly apiOrigin: string; readonly logtoIssuer?: string }
  | { readonly operation: "lifecycle.open-external"; readonly target: "authentication" | "fine-grained-pat" | "classic-pat"; readonly apiOrigin: string }
  | { readonly operation: "auth.open-system-browser"; readonly url: string; readonly issuer: string }
  | { readonly operation: "auth.peek-pending-callback" }
  | { readonly operation: "auth.take-pending-callback" }
  | { readonly operation: "secure.read"; readonly setting: SecureSettingRef }
  | { readonly operation: "secure.write"; readonly setting: SecureSettingRef; readonly value: string }
  | { readonly operation: "secure.remove"; readonly setting: SecureSettingRef }
  | { readonly operation: "secure.reconcile-github-pats"; readonly scopeId: string; readonly profileIds: readonly string[] }
  | { readonly operation: "secure.purge"; readonly scope: "logout" | "account-deletion" | "api-change"; readonly profileId?: string }
  | { readonly operation: "diagnostics.export"; readonly suggestedName: string; readonly contents: string }
  | { readonly operation: "diagnostics.clear" }
  | { readonly operation: "notifications.permission" }
  | { readonly operation: "notifications.request-permission" }
  | { readonly operation: "notifications.publish-deck-change"; readonly notification: DeckNotification }
  | { readonly operation: "notifications.cancel-deck"; readonly deckId: string }
  | { readonly operation: "updates.status" }
  | { readonly operation: "updates.open-store" }
  | { readonly operation: "widgets.replace-deck-snapshot"; readonly snapshot: WidgetDeckSnapshot }
  | { readonly operation: "widgets.clear-deck-snapshot"; readonly deckId: string };

export type NativeShortcutPermission = "available" | "not-determined" | "denied" | "x11-unavailable" | "unsupported";
export type NativeShortcutPlatform = "macos" | "windows" | "x11" | "unsupported";

export type NativeBridgeRequestV1 = NativeBridgeRequestV1Base
  | { readonly operation: "shortcuts.status" }
  | { readonly operation: "shortcuts.request-permission" }
  | { readonly operation: "shortcuts.apply"; readonly bindings: DesktopShortcutBindings }
  | { readonly operation: "shortcuts.stage"; readonly bindings: DesktopShortcutBindings }
  | { readonly operation: "shortcuts.commit"; readonly bindings: DesktopShortcutBindings }
  | { readonly operation: "shortcuts.rollback" }
  | { readonly operation: "shortcuts.suspend" };

export type NativeBridgeResponseV1 =
  | { readonly kind: "runtime"; readonly snapshot: RuntimeSnapshot }
  | { readonly kind: "session-network-policy"; readonly changed: boolean }
  | { readonly kind: "auth-callback"; readonly url: string | null }
  | { readonly kind: "secure-value"; readonly value: string | null }
  | { readonly kind: "notification-permission"; readonly permission: NotificationPermission }
  | { readonly kind: "update-status"; readonly store: "app-store" | "play-store"; readonly installedVersion: string; readonly configured: boolean }
  | { readonly kind: "shortcut-status"; readonly platform: NativeShortcutPlatform; readonly permission: NativeShortcutPermission; readonly bindings: DesktopShortcutBindings; readonly error: ShortcutValidationCode | null }
  | { readonly kind: "unsupported"; readonly feature: "widgets" }
  | { readonly kind: "diagnostics-export"; readonly outcome: "saved" | "cancelled" | "initiated" }
  | { readonly kind: "ok" };

export type NativeBridgeEventV1 =
  | { readonly version: typeof NativeBridgeVersion; readonly kind: "lifecycle"; readonly state: LifecycleState }
  | { readonly version: typeof NativeBridgeVersion; readonly kind: "auth-callback"; readonly url: string }
  | { readonly version: typeof NativeBridgeVersion; readonly kind: "shortcut-triggered"; readonly action: ShortcutActionId }
  | { readonly version: typeof NativeBridgeVersion; readonly kind: "shortcut-status"; readonly platform: NativeShortcutPlatform; readonly permission: NativeShortcutPermission; readonly bindings: DesktopShortcutBindings; readonly error: ShortcutValidationCode | null };

interface TauriInternals {
  invoke(command: string, args?: Record<string, unknown>): Promise<unknown>;
}

interface DiagnosticsWritableFile {
  write(data: Blob): Promise<void>;
  close(): Promise<void>;
  abort(): Promise<void>;
}

declare global {
  interface Window {
    __TAURI_INTERNALS__?: TauriInternals;
    showSaveFilePicker?: (options: { suggestedName: string; types: readonly { description: string; accept: Record<string, readonly string[]> }[] }) => Promise<{ createWritable(): Promise<DiagnosticsWritableFile> }>;
  }
}

const profilePattern = /^[a-zA-Z0-9._-]{1,128}$/u;
const secretLimit = 64 * 1024;
const diagnosticsExportLimit = 1024 * 1024;
const diagnosticsFileName = /^devhud-diagnostics-[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}\.json$/u;
const nativeBridgeErrorCodes = new Set<string>(Object.values(NativeBridgeErrorCode));

export class NativeBridgeError extends Error {
  readonly code: NativeBridgeErrorCode;

  constructor(code: NativeBridgeErrorCode) {
    super(code);
    this.name = "NativeBridgeError";
    this.code = code;
  }
}

export function validateSecureSettingRef(setting: SecureSettingRef) {
  if (!Object.values(SecureSettingKind).includes(setting.kind) || !profilePattern.test(setting.profileId)
    || (setting.kind === SecureSettingKind.GithubPat && !profilePattern.test(setting.scopeId))) {
    throw new NativeBridgeError(NativeBridgeErrorCode.InvalidArgument);
  }
}

export function validateSecretValue(value: string) {
  if (new TextEncoder().encode(value).byteLength > secretLimit) {
    throw new NativeBridgeError(NativeBridgeErrorCode.InvalidArgument);
  }
}

export function validateGitHubPatReconciliation(scopeId: string, profileIds: readonly string[]) {
  if (!profilePattern.test(scopeId) || profileIds.length > 25 || new Set(profileIds).size !== profileIds.length || profileIds.some((profileId) => !profilePattern.test(profileId))) {
    throw new NativeBridgeError(NativeBridgeErrorCode.InvalidArgument);
  }
}

export function validateExternalRequest(request: { readonly target: "authentication" | "fine-grained-pat" | "classic-pat"; readonly apiOrigin: string }) {
  if (!(new Set<string>(["authentication", "fine-grained-pat", "classic-pat"])).has(request.target)) throw new NativeBridgeError(NativeBridgeErrorCode.InvalidArgument);
  if (request.target !== "authentication") return;
  try {
    const url = new URL(request.apiOrigin);
    const octets = url.hostname.split(".");
    const loopback = url.hostname === "localhost" || url.hostname === "[::1]" || url.hostname === "::1" || (octets.length === 4 && octets[0] === "127" && octets.every((octet) => /^\d+$/u.test(octet) && Number(octet) <= 255));
    if (request.apiOrigin !== request.apiOrigin.trim() || url.username || url.password || url.search || url.hash || url.pathname !== "/" || (url.protocol !== "https:" && !(url.protocol === "http:" && loopback))) throw new Error();
  } catch {
    throw new NativeBridgeError(NativeBridgeErrorCode.InvalidArgument);
  }
}

export function validateAuthenticationBrowserRequest(request: { readonly url: string; readonly issuer: string }) {
  try {
    const normalizedIssuer = normalizeLogtoIssuer(request.issuer);
    if (normalizedIssuer === null) throw new Error();
    const issuer = new URL(normalizedIssuer);
    const destination = new URL(request.url);
    const issuerPath = issuer.pathname.replace(/\/+$/u, "");
    const withinIssuerPath = issuerPath === "" || destination.pathname === issuerPath || destination.pathname.startsWith(`${issuerPath}/`);
    if (request.url !== request.url.trim() || destination.origin !== issuer.origin || !withinIssuerPath || destination.username || destination.password || destination.hash) throw new Error();
  } catch {
    throw new NativeBridgeError(NativeBridgeErrorCode.InvalidArgument);
  }
}

export function isAuthCallback(value: string) {
  if (value !== value.trim() || !value.startsWith("devhud://")) return false;
  try {
    const url = new URL(value);
    return url.protocol === "devhud:" && url.hostname === "auth" && url.pathname === "/callback" && url.port === "" && url.username === "" && url.password === "" && url.hash === "";
  } catch {
    return false;
  }
}

interface NavigatorUserAgentData {
  getHighEntropyValues(hints: readonly string[]): Promise<{ readonly architecture?: string; readonly bitness?: string }>;
}

async function browserArchitecture(): Promise<string> {
  const userAgentData = (navigator as Navigator & { readonly userAgentData?: NavigatorUserAgentData }).userAgentData;
  if (!userAgentData) return "unknown";
  try {
    const hints = await userAgentData.getHighEntropyValues(["architecture", "bitness"]);
    const architecture = hints.architecture?.trim().toLowerCase();
    const bitness = hints.bitness?.trim();
    if (architecture === "arm64" || architecture === "aarch64" || (architecture === "arm" && bitness === "64")) return "arm64";
    // The diagnostics wire contract has no browser-safe ARM32 classification.
    if (architecture === "arm" && bitness === "32") return "unknown";
    if (architecture === "x86_64" || architecture === "amd64" || (architecture === "x86" && bitness === "64")) return "x86_64";
  } catch { /* Unsupported or denied high-entropy hints leave the browser architecture unknown. */ }
  return "unknown";
}

async function browserSnapshot(): Promise<RuntimeSnapshot> {
  return {
    bridgeVersion: NativeBridgeVersion,
    platform: RuntimePlatform.Browser,
    operatingSystem: "browser",
    architecture: await browserArchitecture(),
    osVersion: "browser",
    appVersion: "0.1.0",
    buildId: "browser-development",
    tauriRevision: "",
    cefRevision: "",
    lifecycle: document.visibilityState === "hidden" ? LifecycleState.Background : LifecycleState.Active,
    capabilities: { secureSettings: false, notifications: false, storeUpdates: false, widgets: false },
  };
}

export interface NativeBridgeV1 {
  request(request: NativeBridgeRequestV1): Promise<NativeBridgeResponseV1>;
  listen(listener: (event: NativeBridgeEventV1) => void): Promise<() => void>;
}

export const nativeBridge: NativeBridgeV1 = {
  async request(request) {
    if ("setting" in request) validateSecureSettingRef(request.setting);
    if (request.operation === "secure.write") validateSecretValue(request.value);
    if (request.operation === "secure.reconcile-github-pats") validateGitHubPatReconciliation(request.scopeId, request.profileIds);
    if (request.operation === "lifecycle.open-external") validateExternalRequest(request);
    if (request.operation === "auth.open-system-browser") validateAuthenticationBrowserRequest(request);
    if (request.operation === "diagnostics.export") validateDiagnosticsExport(request);
    if (request.operation === "shortcuts.apply" || request.operation === "shortcuts.stage" || request.operation === "shortcuts.commit") parseDesktopShortcutBindings(request.bindings);
    if (!window.__TAURI_INTERNALS__) {
      if (request.operation === "runtime.snapshot") return { kind: "runtime", snapshot: await browserSnapshot() };
      if (request.operation === "session.configure-origins") return { kind: "session-network-policy", changed: false };
      if (request.operation === "auth.peek-pending-callback") return { kind: "auth-callback", url: null };
      if (request.operation === "auth.take-pending-callback") return { kind: "auth-callback", url: null };
      if (request.operation === "auth.open-system-browser") { window.open(request.url, "_blank", "noopener,noreferrer"); return { kind: "ok" }; }
      if (request.operation === "diagnostics.export") return exportDiagnosticsInBrowser(request);
      if (request.operation === "diagnostics.clear") return { kind: "ok" };
      if (request.operation === "shortcuts.status" || request.operation === "shortcuts.request-permission" || request.operation === "shortcuts.apply" || request.operation === "shortcuts.stage" || request.operation === "shortcuts.commit" || request.operation === "shortcuts.rollback" || request.operation === "shortcuts.suspend") {
        return { kind: "shortcut-status", platform: "unsupported", permission: "unsupported", bindings: "bindings" in request ? request.bindings : defaultDesktopShortcutBindings, error: null };
      }
      if (request.operation === "lifecycle.open-external" && request.target !== "authentication") { window.open(request.target === "fine-grained-pat" ? FineGrainedPatCreationUrl : ClassicPatCreationUrl, "_blank", "noopener,noreferrer"); return { kind: "ok" }; }
      if (request.operation.startsWith("widgets.")) return { kind: "unsupported", feature: "widgets" };
      throw new NativeBridgeError(NativeBridgeErrorCode.Unsupported);
    }
    try {
      return await invokeTauri<NativeBridgeResponseV1>("native_bridge_v1", { request });
    } catch (error) {
      if (typeof error === "string" && nativeBridgeErrorCodes.has(error)) {
        throw new NativeBridgeError(error as NativeBridgeErrorCode);
      }
      throw error;
    }
  },
  async listen(listener) {
    if (window.__TAURI_INTERNALS__) {
      return await listenTauri<NativeBridgeEventV1>("devhud:native-event:v1", ({ payload }) => listener(payload));
    }
    const visibility = () => listener({ version: NativeBridgeVersion, kind: "lifecycle", state: document.visibilityState === "hidden" ? LifecycleState.Background : LifecycleState.Active });
    document.addEventListener("visibilitychange", visibility);
    return () => document.removeEventListener("visibilitychange", visibility);
  },
};

export function validateDiagnosticsExport(request: { readonly suggestedName: string; readonly contents: string }): void {
  if (!diagnosticsFileName.test(request.suggestedName) || new TextEncoder().encode(request.contents).byteLength > diagnosticsExportLimit) throw new NativeBridgeError(NativeBridgeErrorCode.InvalidArgument);
  try {
    const parsed: unknown = JSON.parse(request.contents);
    if (parsed === null || typeof parsed !== "object" || Array.isArray(parsed)) throw new Error();
  } catch {
    throw new NativeBridgeError(NativeBridgeErrorCode.InvalidArgument);
  }
}

async function exportDiagnosticsInBrowser(request: { readonly suggestedName: string; readonly contents: string }): Promise<NativeBridgeResponseV1> {
  const blob = new Blob([request.contents], { type: "application/json" });
  if (window.showSaveFilePicker) {
    let handle: Awaited<ReturnType<NonNullable<Window["showSaveFilePicker"]>>>;
    try {
      handle = await window.showSaveFilePicker({ suggestedName: request.suggestedName, types: [{ description: "Redacted DevHUD diagnostics", accept: { "application/json": [".json"] } }] });
    } catch (reason) {
      if (reason instanceof DOMException && reason.name === "AbortError") return { kind: "diagnostics-export", outcome: "cancelled" };
      throw new NativeBridgeError(NativeBridgeErrorCode.StorageFailure);
    }
    let writable: DiagnosticsWritableFile | undefined;
    try {
      writable = await handle.createWritable();
      await writable.write(blob);
      await writable.close();
      writable = undefined;
      return { kind: "diagnostics-export", outcome: "saved" };
    } catch (reason) {
      if (writable) {
        try { await writable.abort(); } catch { /* Preserve the stable export failure classification. */ }
      }
      throw new NativeBridgeError(NativeBridgeErrorCode.StorageFailure);
    }
  }
  const link = document.createElement("a");
  const url = URL.createObjectURL(blob);
  try {
    link.href = url;
    link.download = request.suggestedName;
    link.click();
  } finally {
    URL.revokeObjectURL(url);
  }
  return { kind: "diagnostics-export", outcome: "initiated" };
}
import { invoke as invokeTauri } from "@tauri-apps/api/core";
import { listen as listenTauri } from "@tauri-apps/api/event";
