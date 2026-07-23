import { PersistKey } from "@logto/browser";
import { beforeEach, describe, expect, it } from "vitest";

import { VolatileLogtoStorage } from "../auth/VolatileLogtoClient";

describe("Logto browser storage", () => {
  beforeEach(() => {
    localStorage.clear();
    sessionStorage.clear();
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
});
