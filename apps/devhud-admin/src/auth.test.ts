import { beforeEach, describe, expect, it, vi } from "vitest";

const methods = vi.hoisted(() => ({
  constructorConfig: vi.fn(),
  signIn: vi.fn(),
  isSignInRedirected: vi.fn(),
  handleSignInCallback: vi.fn(),
  getIdTokenClaims: vi.fn(),
  clearAllTokens: vi.fn(),
}));

vi.mock("@logto/browser", () => ({
  default: class {
    constructor(config: unknown) {
      methods.constructorConfig(config);
    }
    signIn = methods.signIn;
    isSignInRedirected = methods.isSignInRedirected;
    handleSignInCallback = methods.handleSignInCallback;
    getIdTokenClaims = methods.getIdTokenClaims;
    clearAllTokens = methods.clearAllTokens;
  },
}));

import { AdminAuth, authStorage } from "./auth";
import type { GetBootstrapResponse } from "@delinoio/devhud-api-client";

describe("AdminAuth", () => {
  beforeEach(() => {
    history.replaceState(null, "", "/admin/");
    sessionStorage.clear();
    methods.constructorConfig.mockReset();
    methods.signIn.mockReset().mockResolvedValue(undefined);
    methods.isSignInRedirected.mockReset().mockResolvedValue(true);
    methods.handleSignInCallback.mockReset().mockResolvedValue(undefined);
    methods.getIdTokenClaims.mockReset();
    methods.clearAllTokens.mockReset().mockResolvedValue(undefined);
  });

  it("selects the bootstrap admin public client and exact redirect", () => {
    const auth = AdminAuth.fromBootstrap({
      logtoIssuer: "https://identity.example",
      logtoAudience: "https://api.example",
      logtoClients: { admin: "admin-public-client" },
      logtoRedirects: { admin: "http://localhost:46306/auth/callback" },
    } as GetBootstrapResponse);

    expect(auth.redirectUri).toBe("http://localhost:46306/auth/callback");
    expect(methods.constructorConfig).toHaveBeenCalledWith(
      expect.objectContaining({
        appId: "admin-public-client",
        endpoint: "https://identity.example",
        resources: ["https://api.example"],
      }),
    );
  });

  it("uses the exact bootstrap redirect and adds nonce to Logto PKCE/state sign-in", async () => {
    const auth = new AdminAuth(
      "https://identity.example",
      "https://api.example",
      "admin-public-client",
      "http://localhost:46306/auth/callback",
    );
    await auth.begin();
    const options = methods.signIn.mock.calls[0]?.[0];
    expect(options.redirectUri).toBe(
      "http://localhost:46306/auth/callback",
    );
    expect(options.extraParams.nonce).toMatch(/^[A-Za-z0-9_-]{43}$/);
    expect(sessionStorage.getItem(authStorage.nonceKey)).toBe(
      options.extraParams.nonce,
    );
  });

  it("does not start sign-in when the nonce cannot be stored", async () => {
    const setItem = vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new DOMException("Storage is unavailable", "SecurityError");
    });
    const auth = new AdminAuth(
      "https://identity.example",
      "https://api.example",
      "admin-public-client",
      "http://localhost:46306/auth/callback",
    );
    try {
      await expect(auth.begin()).rejects.toThrow("Storage is unavailable");
      expect(methods.signIn).not.toHaveBeenCalled();
    } finally {
      setItem.mockRestore();
    }
  });

  it("rejects an ID token nonce mismatch after SDK state and PKCE validation", async () => {
    sessionStorage.setItem(authStorage.nonceKey, "expected");
    methods.getIdTokenClaims.mockResolvedValue({ nonce: "different" });
    const auth = new AdminAuth(
      "https://identity.example",
      "https://api.example",
      "admin-public-client",
      "http://localhost:46306/auth/callback",
    );
    await expect(auth.completeCallback(window.location.href)).rejects.toThrow(
      "nonce did not match",
    );
    expect(methods.handleSignInCallback).toHaveBeenCalledOnce();
    expect(methods.clearAllTokens).toHaveBeenCalledOnce();
  });

  it("discards callbacks whose nonce state is unavailable", async () => {
    history.replaceState(null, "", "/admin/auth/callback?code=code&state=state#fragment");
    const auth = new AdminAuth(
      "https://identity.example",
      "https://api.example",
      "admin-public-client",
      "http://localhost:46306/auth/callback",
    );

    await expect(auth.completeCallback(window.location.href)).resolves.toBe(false);
    expect(methods.handleSignInCallback).not.toHaveBeenCalled();
    expect(methods.clearAllTokens).toHaveBeenCalledOnce();
    expect(window.location.pathname).toBe("/admin/");
    expect(window.location.search).toBe("");
    expect(window.location.hash).toBe("");
  });
});
