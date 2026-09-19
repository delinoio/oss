// @vitest-environment jsdom

import { describe, expect, it, vi } from "vitest";
import { ProjectId, StaticCapability, type GetBootstrapResponse } from "@delinoio/devhud-api-client";
import { LogtoClientError, LogtoRequestError } from "@logto/client";
import { authCallbackBindingMatches, BootstrapContractError, clearAuthCallbackBinding, createIdentitySession, isTerminalAccessTokenError, recordAuthCallbackBinding, SecureLogtoStorage, sessionProfileId, validateBootstrap } from "./identity-client";
import { logtoEndpointFromIssuer } from "./identity-contract";
import { LifecycleState, RuntimePlatform, type NativeBridgeRequestV1, type NativeBridgeResponseV1, type NativeBridgeV1 } from "./native-bridge";

function memoryBridge(): NativeBridgeV1 & { readonly values: Map<string, string>; readonly requests: NativeBridgeRequestV1[] } {
  const values = new Map<string, string>();
  const requests: NativeBridgeRequestV1[] = [];
  return {
    values,
    requests,
    async request(request): Promise<NativeBridgeResponseV1> {
      requests.push(request);
      if (request.operation === "secure.read") return { kind: "secure-value", value: values.get(`${request.setting.kind}:${request.setting.profileId}`) ?? null };
      if (request.operation === "secure.write") { values.set(`${request.setting.kind}:${request.setting.profileId}`, request.value); return { kind: "ok" }; }
      if (request.operation === "secure.remove") { values.delete(`${request.setting.kind}:${request.setting.profileId}`); return { kind: "ok" }; }
      if (request.operation === "runtime.snapshot") return { kind: "runtime", snapshot: { bridgeVersion: 1, platform: RuntimePlatform.Desktop, operatingSystem: "linux", architecture: "x86_64", osVersion: "test", appVersion: "0.1.0", buildId: "test", tauriRevision: "4af26a3f7f8b692d62cca549bbacd93f5ce90b41", cefRevision: "150.0.10+g8042e43+chromium-150.0.7871.101", lifecycle: LifecycleState.Active, capabilities: { secureSettings: true, notifications: false, storeUpdates: false, widgets: false } } };
      return { kind: "ok" };
    },
    async listen() { return () => {}; },
  };
}

const bootstrap = {
  projectId: ProjectId.DEVHUD,
  protocolSchemaVersion: 2,
  apiVersion: "0.1.0-dev",
  logtoIssuer: "https://identity.example/oidc",
  logtoAudience: "https://api.example/api",
  logtoClients: { desktop: "desktop-client", ios: "ios-client", android: "android-client", admin: "admin-client" },
  logtoRedirects: { native: "devhud://auth/callback", admin: "https://admin.example/callback" },
  publicAssetBaseUrl: "https://images.example/devhud",
  capabilities: [StaticCapability.SETTINGS_SYNC, StaticCapability.CRASH_REPORTS],
} as GetBootstrapResponse;

describe("identity client boundary", () => {
  it("selects the platform public client and exact native callback", () => {
    expect(validateBootstrap(bootstrap, RuntimePlatform.Desktop).clientId).toBe("desktop-client");
    expect(validateBootstrap(bootstrap, RuntimePlatform.Ios).clientId).toBe("ios-client");
    expect(validateBootstrap(bootstrap, RuntimePlatform.Android).redirectUri).toBe("devhud://auth/callback");
    expect(validateBootstrap(bootstrap, RuntimePlatform.Desktop).publicAssetBaseUrl).toBe("https://images.example/devhud");
    expect(validateBootstrap({ ...bootstrap, apiVersion: "2026.08.17" } as GetBootstrapResponse, RuntimePlatform.Desktop).audience).toBe("https://api.example/api");
    expect(validateBootstrap({ ...bootstrap, logtoIssuer: "http://127.0.0.1:46307/oidc" } as GetBootstrapResponse, RuntimePlatform.Desktop).issuer).toBe("http://127.0.0.1:46307/oidc");
    expect(validateBootstrap({ ...bootstrap, publicAssetBaseUrl: "http://127.0.0.1:9000" } as GetBootstrapResponse, RuntimePlatform.Desktop).publicAssetBaseUrl).toBe("http://127.0.0.1:9000/");
    expect(validateBootstrap({ ...bootstrap, publicAssetBaseUrl: "http://[::1]:9000" } as GetBootstrapResponse, RuntimePlatform.Desktop).publicAssetBaseUrl).toBe("http://[::1]:9000/");
    expect(validateBootstrap(bootstrap, RuntimePlatform.Desktop).capabilities).toEqual([StaticCapability.SETTINGS_SYNC, StaticCapability.CRASH_REPORTS]);
  });

  it("rejects insecure discovery and callback substitution", () => {
    expect(() => validateBootstrap({ ...bootstrap, logtoIssuer: "http://identity.example/" } as GetBootstrapResponse, RuntimePlatform.Desktop)).toThrow(BootstrapContractError);
    expect(() => validateBootstrap({ ...bootstrap, logtoAudience: "  " } as GetBootstrapResponse, RuntimePlatform.Desktop)).toThrow(BootstrapContractError);
    expect(() => validateBootstrap({ ...bootstrap, logtoRedirects: { ...bootstrap.logtoRedirects!, native: "https://attacker.example" } } as GetBootstrapResponse, RuntimePlatform.Desktop)).toThrow(BootstrapContractError);
    expect(() => validateBootstrap({ ...bootstrap, publicAssetBaseUrl: "http://images.example" } as GetBootstrapResponse, RuntimePlatform.Desktop)).toThrow(BootstrapContractError);
  });

  it("derives the Logto SDK endpoint from a terminal OIDC issuer segment", () => {
    expect(logtoEndpointFromIssuer("https://identity.example/oidc")).toBe("https://identity.example/");
    expect(logtoEndpointFromIssuer("https://identity.example/tenant/oidc/")).toBe("https://identity.example/tenant");
    expect(logtoEndpointFromIssuer("https://identity.example/tenant")).toBe("https://identity.example/tenant");
    expect(logtoEndpointFromIssuer("http://127.0.0.1:3001/oidc")).toBe("http://127.0.0.1:3001/");
    expect(logtoEndpointFromIssuer("http://identity.example/oidc")).toBeNull();
  });

  it("rejects Bootstrap discovery for another project", () => {
    expect(() => validateBootstrap({ ...bootstrap, projectId: ProjectId.UNSPECIFIED }, RuntimePlatform.Desktop)).toThrow(BootstrapContractError);
    expect(() => validateBootstrap({ ...bootstrap, projectId: 999 as ProjectId }, RuntimePlatform.Desktop)).toThrow(BootstrapContractError);
  });

  it("classifies only terminal access-token failures", () => {
    expect(isTerminalAccessTokenError(new LogtoClientError("not_authenticated"))).toBe(true);
    expect(isTerminalAccessTokenError(new LogtoRequestError("invalid_grant", "refresh token revoked"))).toBe(true);
    expect(isTerminalAccessTokenError(new LogtoRequestError("temporarily_unavailable", "retry later"))).toBe(false);
    expect(isTerminalAccessTokenError(new TypeError("network unavailable"))).toBe(false);
  });

  it("serializes concurrent Logto writes into one non-enumerable secure value", async () => {
    const bridge = memoryBridge();
    const storage = new SecureLogtoStorage(bridge, "origin.test");
    await Promise.all([storage.setItem("idToken", "id"), storage.setItem("refreshToken", "refresh"), storage.setItem("signInSession", "pkce")]);
    expect(await storage.getItem("idToken")).toBe("id");
    expect(await storage.getItem("refreshToken")).toBe("refresh");
    expect(await storage.getItem("signInSession")).toBe("pkce");
    expect(bridge.values.size).toBe(1);
    await storage.clear();
    expect(bridge.values.size).toBe(0);
  });

  it("derives stable origin-scoped, bridge-safe profile IDs", async () => {
    const first = await sessionProfileId("https://api.example/");
    expect(first).toBe(await sessionProfileId("https://api.example"));
    expect(first).not.toBe(await sessionProfileId("https://other.example"));
    expect(first).toMatch(/^origin\.[A-Za-z0-9_-]{43}$/u);
  });

  it("retains a browser callback binding only for its originating API", () => {
    expect(recordAuthCallbackBinding(localStorage, "https://origin-a.example")).toBe(true);
    expect(recordAuthCallbackBinding(localStorage, "https://origin-b.example")).toBe(false);
    expect(authCallbackBindingMatches(localStorage, "https://origin-a.example")).toBe(true);
    expect(authCallbackBindingMatches(localStorage, "https://origin-b.example")).toBe(false);
    clearAuthCallbackBinding(localStorage);
    expect(authCallbackBindingMatches(localStorage, "https://origin-a.example")).toBe(false);
  });

  it("does not start a second browser transaction while a callback is outstanding", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({
      authorization_endpoint: "https://identity.example/oidc/auth",
      token_endpoint: "https://identity.example/oidc/token",
      userinfo_endpoint: "https://identity.example/oidc/userinfo",
      end_session_endpoint: "https://identity.example/oidc/session/end",
      revocation_endpoint: "https://identity.example/oidc/revoke",
      jwks_uri: "https://identity.example/oidc/jwks",
      issuer: "https://identity.example/oidc",
    }), { headers: { "Content-Type": "application/json" } })));
    const bridge = memoryBridge();
    try {
      const session = await createIdentitySession(validateBootstrap(bootstrap, RuntimePlatform.Desktop), "https://api.example", bridge);
      await session.signIn();
      await expect(session.signIn()).rejects.toThrow("auth-callback-pending");
      expect(bridge.requests.filter((request) => request.operation === "auth.open-system-browser")).toHaveLength(1);
    } finally {
      clearAuthCallbackBinding(localStorage);
      vi.unstubAllGlobals();
    }
  });

  it("fails closed when the secure store fails", async () => {
    const bridge = memoryBridge();
    bridge.request = vi.fn(async () => { throw new Error("unavailable"); });
    await expect(new SecureLogtoStorage(bridge, "origin.test").getItem("idToken")).rejects.toThrow("unavailable");
  });

  it("recovers its serialized queue after one secure mutation fails", async () => {
    const bridge = memoryBridge();
    const request = bridge.request.bind(bridge);
    let failed = false;
    bridge.request = vi.fn(async (value) => {
      if (!failed && value.operation === "secure.write") {
        failed = true;
        throw new Error("unavailable");
      }
      return request(value);
    });
    const storage = new SecureLogtoStorage(bridge, "origin.test");
    await expect(storage.setItem("idToken", "first")).rejects.toThrow("unavailable");
    await expect(storage.setItem("idToken", "second")).resolves.toBeUndefined();
    await expect(storage.getItem("idToken")).resolves.toBe("second");
  });
});
