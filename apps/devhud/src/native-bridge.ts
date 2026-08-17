import { normalizeLogtoIssuer } from "./identity-contract.ts";

export const NativeBridgeVersion = 1 as const;

export const RuntimePlatform = {
  Desktop: "desktop",
  Ios: "ios",
  Android: "android",
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
  readonly architecture: string;
  readonly osVersion: string;
  readonly lifecycle: LifecycleState;
  readonly capabilities: {
    readonly secureSettings: boolean;
    readonly notifications: boolean;
    readonly storeUpdates: boolean;
    readonly widgets: false;
  };
}

export interface SecureSettingRef {
  readonly kind: SecureSettingKind;
  readonly profileId: string;
}

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

export type NativeBridgeRequestV1 =
  | { readonly operation: "runtime.snapshot" }
  | { readonly operation: "session.configure-origins"; readonly apiOrigin: string; readonly logtoIssuer?: string }
  | { readonly operation: "lifecycle.open-external"; readonly target: "authentication" | "pat"; readonly apiOrigin: string }
  | { readonly operation: "auth.open-system-browser"; readonly url: string; readonly issuer: string }
  | { readonly operation: "auth.peek-pending-callback" }
  | { readonly operation: "auth.take-pending-callback" }
  | { readonly operation: "secure.read"; readonly setting: SecureSettingRef }
  | { readonly operation: "secure.write"; readonly setting: SecureSettingRef; readonly value: string }
  | { readonly operation: "secure.remove"; readonly setting: SecureSettingRef }
  | { readonly operation: "secure.purge"; readonly scope: "logout" | "account-deletion" | "api-change"; readonly profileId?: string }
  | { readonly operation: "notifications.permission" }
  | { readonly operation: "notifications.request-permission" }
  | { readonly operation: "notifications.publish-deck-change"; readonly notification: DeckNotification }
  | { readonly operation: "notifications.cancel-deck"; readonly deckId: string }
  | { readonly operation: "updates.status" }
  | { readonly operation: "updates.open-store" }
  | { readonly operation: "widgets.replace-deck-snapshot"; readonly snapshot: WidgetDeckSnapshot }
  | { readonly operation: "widgets.clear-deck-snapshot"; readonly deckId: string };

export type NativeBridgeResponseV1 =
  | { readonly kind: "runtime"; readonly snapshot: RuntimeSnapshot }
  | { readonly kind: "session-network-policy"; readonly changed: boolean }
  | { readonly kind: "auth-callback"; readonly url: string | null }
  | { readonly kind: "secure-value"; readonly value: string | null }
  | { readonly kind: "notification-permission"; readonly permission: NotificationPermission }
  | { readonly kind: "update-status"; readonly store: "app-store" | "play-store"; readonly installedVersion: string; readonly configured: boolean }
  | { readonly kind: "unsupported"; readonly feature: "widgets" }
  | { readonly kind: "ok" };

export type NativeBridgeEventV1 =
  | { readonly version: typeof NativeBridgeVersion; readonly kind: "lifecycle"; readonly state: LifecycleState }
  | { readonly version: typeof NativeBridgeVersion; readonly kind: "auth-callback"; readonly url: string };

interface TauriInternals {
  invoke(command: string, args?: Record<string, unknown>): Promise<unknown>;
}

declare global {
  interface Window {
    __TAURI_INTERNALS__?: TauriInternals;
  }
}

const profilePattern = /^[a-zA-Z0-9._-]{1,128}$/u;
const secretLimit = 64 * 1024;
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
  if (!Object.values(SecureSettingKind).includes(setting.kind) || !profilePattern.test(setting.profileId)) {
    throw new NativeBridgeError(NativeBridgeErrorCode.InvalidArgument);
  }
}

export function validateSecretValue(value: string) {
  if (new TextEncoder().encode(value).byteLength > secretLimit) {
    throw new NativeBridgeError(NativeBridgeErrorCode.InvalidArgument);
  }
}

export function validateExternalRequest(request: { readonly target: "authentication" | "pat"; readonly apiOrigin: string }) {
  if (!(new Set<string>(["authentication", "pat"])).has(request.target)) throw new NativeBridgeError(NativeBridgeErrorCode.InvalidArgument);
  if (request.target === "pat") return;
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

function desktopSnapshot(): RuntimeSnapshot {
  return {
    bridgeVersion: NativeBridgeVersion,
    platform: RuntimePlatform.Desktop,
    architecture: "native",
    osVersion: "native",
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
    if (request.operation === "lifecycle.open-external") validateExternalRequest(request);
    if (request.operation === "auth.open-system-browser") validateAuthenticationBrowserRequest(request);
    if (!window.__TAURI_INTERNALS__) {
      if (request.operation === "runtime.snapshot") return { kind: "runtime", snapshot: desktopSnapshot() };
      if (request.operation === "session.configure-origins") return { kind: "session-network-policy", changed: false };
      if (request.operation === "auth.peek-pending-callback") return { kind: "auth-callback", url: null };
      if (request.operation === "auth.take-pending-callback") return { kind: "auth-callback", url: null };
      if (request.operation === "auth.open-system-browser") { window.open(request.url, "_blank", "noopener,noreferrer"); return { kind: "ok" }; }
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
import { invoke as invokeTauri } from "@tauri-apps/api/core";
import { listen as listenTauri } from "@tauri-apps/api/event";
