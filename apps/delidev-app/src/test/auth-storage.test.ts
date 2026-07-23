import { PersistKey } from "@logto/browser";
import { beforeEach, describe, expect, it } from "vitest";

import {
  clearPendingSealedSignInReturnPath,
  consumeSealedSignInReturnPath,
  prepareSealedSignInReturnPath,
  VolatileLogtoStorage,
} from "../auth/VolatileLogtoClient";

describe("Logto browser storage", () => {
  beforeEach(() => {
    localStorage.clear();
    sessionStorage.clear();
    clearPendingSealedSignInReturnPath();
    consumeSealedSignInReturnPath();
    window.history.replaceState({}, "", "/");
  });

  it("keeps auth tokens in memory and PKCE state in session storage", async () => {
    const appId = "storage-test";
    const storage = new VolatileLogtoStorage(appId);

    await storage.setItem(PersistKey.AccessToken, "access-token");
    await storage.setItem(PersistKey.RefreshToken, "refresh-token");
    await storage.setItem(PersistKey.IdToken, "id-token");
    await storage.setItem(PersistKey.SignInSession, "pkce-state");

    expect(localStorage.length).toBe(0);
    expect(
      sessionStorage.getItem(`logto:${appId}:${PersistKey.SignInSession}`),
    ).toBe("pkce-state");
    expect(await storage.getItem(PersistKey.AccessToken)).toBe("access-token");
    expect(await storage.getItem(PersistKey.RefreshToken)).toBe(
      "refresh-token",
    );
    expect(await storage.getItem(PersistKey.IdToken)).toBe("id-token");
  });

  it("removes auth state left by the default persistent browser client", () => {
    const appId = "legacy-storage-test";
    localStorage.setItem(`logto:${appId}:accessToken`, "legacy-access-token");
    localStorage.setItem("unrelated", "preserved");

    new VolatileLogtoStorage(appId);

    expect(localStorage.getItem(`logto:${appId}:accessToken`)).toBeNull();
    expect(localStorage.getItem("unrelated")).toBe("preserved");
  });

  it("seals invitation return paths until the matching callback", async () => {
    const appId = "invitation-handoff-test";
    const storage = new VolatileLogtoStorage(appId);
    const returnPath = "/invite/secret-bearer-token";
    const signInSession = JSON.stringify({
      codeVerifier: "pkce-code-verifier",
      redirectUri: "https://deli.dev/auth/callback",
      state: "oidc-state",
    });

    prepareSealedSignInReturnPath(returnPath);
    await storage.setItem(PersistKey.SignInSession, signInSession);

    const persisted = sessionStorage.getItem(
      `logto:${appId}:${PersistKey.SignInSession}`,
    );
    expect(persisted).not.toContain(returnPath);
    expect(persisted).not.toContain("secret-bearer-token");
    expect(persisted).not.toContain("oidc-state");

    window.history.replaceState(
      {},
      "",
      "/auth/callback?code=authorization-code&state=oidc-state",
    );
    expect(await storage.getItem(PersistKey.SignInSession)).toContain(
      '"state":"oidc-state"',
    );
    expect(
      sessionStorage.getItem(
        `logto:${appId}:${PersistKey.SignInSession}`,
      ),
    ).toBeNull();
    await storage.removeItem(PersistKey.SignInSession);
    expect(consumeSealedSignInReturnPath()).toBe(returnPath);
  });
});
