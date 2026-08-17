// @vitest-environment jsdom

import { describe, expect, it, vi } from "vitest";
import type { GetBootstrapResponse } from "@delinoio/devhud-api-client";
import { BootstrapContractError, SecureLogtoStorage, sessionProfileId, validateBootstrap } from "./identity-client";
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
      if (request.operation === "runtime.snapshot") return { kind: "runtime", snapshot: { bridgeVersion: 1, platform: RuntimePlatform.Desktop, architecture: "x64", osVersion: "test", lifecycle: LifecycleState.Active, capabilities: { secureSettings: true, notifications: false, storeUpdates: false, widgets: false } } };
      return { kind: "ok" };
    },
    async listen() { return () => {}; },
  };
}

const bootstrap = {
  protocolSchemaVersion: 1,
  apiVersion: "0.1.0-dev",
  logtoIssuer: "https://identity.example/oidc",
  logtoAudience: "https://api.example/api",
  logtoClients: { desktop: "desktop-client", ios: "ios-client", android: "android-client", admin: "admin-client" },
  logtoRedirects: { native: "devhud://auth/callback", admin: "https://admin.example/callback" },
} as GetBootstrapResponse;

describe("identity client boundary", () => {
  it("selects the platform public client and exact native callback", () => {
    expect(validateBootstrap(bootstrap, RuntimePlatform.Desktop).clientId).toBe("desktop-client");
    expect(validateBootstrap(bootstrap, RuntimePlatform.Ios).clientId).toBe("ios-client");
    expect(validateBootstrap(bootstrap, RuntimePlatform.Android).redirectUri).toBe("devhud://auth/callback");
    expect(validateBootstrap({ ...bootstrap, apiVersion: "2026.08.17" } as GetBootstrapResponse, RuntimePlatform.Desktop).audience).toBe("https://api.example/api");
    expect(validateBootstrap({ ...bootstrap, logtoIssuer: "http://127.0.0.1:46307/oidc" } as GetBootstrapResponse, RuntimePlatform.Desktop).issuer).toBe("http://127.0.0.1:46307/oidc");
  });

  it("rejects insecure discovery and callback substitution", () => {
    expect(() => validateBootstrap({ ...bootstrap, logtoIssuer: "http://identity.example/" } as GetBootstrapResponse, RuntimePlatform.Desktop)).toThrow(BootstrapContractError);
    expect(() => validateBootstrap({ ...bootstrap, logtoAudience: "  " } as GetBootstrapResponse, RuntimePlatform.Desktop)).toThrow(BootstrapContractError);
    expect(() => validateBootstrap({ ...bootstrap, logtoRedirects: { ...bootstrap.logtoRedirects!, native: "https://attacker.example" } } as GetBootstrapResponse, RuntimePlatform.Desktop)).toThrow(BootstrapContractError);
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
