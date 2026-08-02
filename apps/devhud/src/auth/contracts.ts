export enum AuthFeature {
  Deck = "deck",
  RealQa = "real-qa",
}

export type NativeSessionSnapshot =
  | { readonly status: "signed-out" }
  | { readonly status: "authenticating" }
  | {
      readonly status: "signed-in";
      readonly subject: string;
      readonly features: readonly AuthFeature[];
    }
  | { readonly status: "prior-session-offline" }
  | { readonly status: "cleanup-required" };

export type AuthErrorCode =
  | "configuration-unavailable"
  | "invalid-configuration"
  | "invalid-callback"
  | "callback-state-mismatch"
  | "callback-already-consumed"
  | "callback-timed-out"
  | "callback-listener-unavailable"
  | "browser-unavailable"
  | "authorization-rejected"
  | "transport-unavailable"
  | "token-exchange-failed"
  | "token-invalid"
  | "token-expired"
  | "audience-mismatch"
  | "subject-mismatch"
  | "scope-mismatch"
  | "secure-vault-unavailable"
  | "secure-vault-write-failed"
  | "secure-vault-delete-failed"
  | "account-switch-requires-logout"
  | "first-time-offline"
  | "reauthentication-required"
  | "sign-in-already-active";

export interface AuthFailure {
  readonly code: AuthErrorCode;
  readonly guidance: string;
}

/**
 * The bridge deliberately has no token getter, URL parameter, generic request,
 * or generic browser operation. Feature RPCs use a separate closed native
 * transport that obtains memory-only bearers without crossing this interface.
 */
export interface NativeSessionBridge {
  restore(): Promise<NativeSessionSnapshot>;
  start(feature: AuthFeature): Promise<NativeSessionSnapshot>;
  logout(): Promise<NativeSessionSnapshot>;
}

const safeGuidance: Record<AuthErrorCode, string> = {
  "configuration-unavailable":
    "DeliDev sign-in is not configured for this build. Local DevHud tools remain available.",
  "invalid-configuration":
    "DeliDev sign-in configuration is invalid. Local DevHud tools remain available.",
  "invalid-callback": "The sign-in response was invalid. Start sign-in again.",
  "callback-state-mismatch":
    "The sign-in response did not match this request. Start sign-in again.",
  "callback-already-consumed":
    "That sign-in response was already used. Start sign-in again.",
  "callback-timed-out": "Sign-in timed out. Start sign-in again.",
  "callback-listener-unavailable":
    "DevHud could not start its private sign-in callback. Try again.",
  "browser-unavailable": "DevHud could not open the system browser. Try again.",
  "authorization-rejected": "Sign-in was cancelled or rejected.",
  "transport-unavailable":
    "DevHud could not reach DeliDev sign-in. Check your connection and try again.",
  "token-exchange-failed": "DevHud could not finish sign-in. Try again.",
  "token-invalid": "The sign-in response could not be verified. Try again.",
  "token-expired": "The sign-in response expired. Start sign-in again.",
  "audience-mismatch": "The sign-in response was not issued for this feature.",
  "subject-mismatch": "The feature credentials did not belong to the same account.",
  "scope-mismatch": "The feature credentials did not include the required permission.",
  "secure-vault-unavailable":
    "The operating system secure vault is unavailable. Sign-in was not retained.",
  "secure-vault-write-failed":
    "DevHud could not save the session in the operating system secure vault.",
  "secure-vault-delete-failed":
    "DevHud locked the session but could not clear the operating system secure vault. Retry logout or Reset DevHud.",
  "account-switch-requires-logout":
    "Log out of the active DeliDev account before signing in with another account.",
  "first-time-offline":
    "Connect to the internet and sign in once before using authenticated offline features.",
  "reauthentication-required":
    "Connect to the internet and sign in again before contacting this feature.",
  "sign-in-already-active": "A sign-in request is already active.",
};

export function safeAuthFailure(value: unknown): AuthFailure {
  const code =
    typeof value === "string" && value in safeGuidance
      ? (value as AuthErrorCode)
      : "token-exchange-failed";
  return { code, guidance: safeGuidance[code] };
}

export function isNativeSessionSnapshot(
  value: unknown,
): value is NativeSessionSnapshot {
  if (typeof value !== "object" || value === null || !("status" in value)) {
    return false;
  }
  if (
    value.status === "signed-out" ||
    value.status === "authenticating" ||
    value.status === "prior-session-offline" ||
    value.status === "cleanup-required"
  ) {
    return true;
  }
  return (
    value.status === "signed-in" &&
    "subject" in value &&
    typeof value.subject === "string" &&
    value.subject.length > 0 &&
    value.subject.length <= 512 &&
    "features" in value &&
    Array.isArray(value.features) &&
    value.features.length <= Object.values(AuthFeature).length &&
    value.features.every(
      (feature) => feature === AuthFeature.Deck || feature === AuthFeature.RealQa,
    ) &&
    new Set(value.features).size === value.features.length
  );
}
