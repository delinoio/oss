import { PersistKey } from "@logto/browser";
import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

const callbackState = vi.hoisted(() => ({
  error: undefined as Error | undefined,
  isAuthenticated: true,
  isLoading: false,
}));

vi.mock("@logto/react", async (importOriginal) => {
  const original = await importOriginal<typeof import("@logto/react")>();
  return {
    ...original,
    useHandleSignInCallback: () => callbackState,
  };
});

import { AuthCallbackPage } from "../pages/AuthCallbackPage";
import {
  clearPendingSealedSignInReturnPath,
  consumeSealedSignInReturnPath,
  prepareSealedSignInReturnPath,
  VolatileLogtoStorage,
} from "../auth/VolatileLogtoClient";

describe("Logto callback page", () => {
  beforeEach(() => {
    callbackState.error = undefined;
    callbackState.isAuthenticated = true;
    callbackState.isLoading = false;
    sessionStorage.clear();
    clearPendingSealedSignInReturnPath();
    consumeSealedSignInReturnPath();
    window.history.replaceState({}, "", "/");
  });

  it("returns to the guarded internal route after a successful callback", async () => {
    sessionStorage.setItem("delidev:return-to", "/account");
    render(
      <MemoryRouter initialEntries={["/auth/callback"]}>
        <Routes>
          <Route path="/auth/callback" element={<AuthCallbackPage />} />
          <Route path="/account" element={<h1>Account destination</h1>} />
        </Routes>
      </MemoryRouter>,
    );
    await waitFor(() =>
      expect(
        screen.getByRole("heading", { name: "Account destination" }),
      ).toBeVisible(),
    );
    expect(sessionStorage.getItem("delidev:return-to")).toBeNull();
  });

  it("resumes an invitation from its sealed one-shot handoff", async () => {
    const storage = new VolatileLogtoStorage("callback-invitation-test");
    prepareSealedSignInReturnPath("/invite/secret-bearer-token");
    await storage.setItem(
      PersistKey.SignInSession,
      JSON.stringify({
        codeVerifier: "pkce-code-verifier",
        redirectUri: "https://deli.dev/auth/callback",
        state: "invite-state",
      }),
    );
    window.history.replaceState(
      {},
      "",
      "/auth/callback?code=authorization-code&state=invite-state",
    );
    await storage.getItem(PersistKey.SignInSession);
    await storage.removeItem(PersistKey.SignInSession);

    render(
      <MemoryRouter initialEntries={["/auth/callback"]}>
        <Routes>
          <Route path="/auth/callback" element={<AuthCallbackPage />} />
          <Route
            path="/invite/:token"
            element={<h1>Invitation destination</h1>}
          />
        </Routes>
      </MemoryRouter>,
    );
    await waitFor(() =>
      expect(
        screen.getByRole("heading", { name: "Invitation destination" }),
      ).toBeVisible(),
    );
    expect(sessionStorage.getItem("delidev:return-to")).toBeNull();
  });

  it("renders a safe error state when Logto rejects the callback", () => {
    sessionStorage.setItem("delidev:return-to", "/account");
    callbackState.error = new Error("Invalid sign-in state.");
    callbackState.isAuthenticated = false;
    render(
      <MemoryRouter initialEntries={["/auth/callback"]}>
        <Routes>
          <Route path="/auth/callback" element={<AuthCallbackPage />} />
        </Routes>
      </MemoryRouter>,
    );
    expect(
      screen.getByRole("heading", {
        name: "We couldn’t complete sign-in",
      }),
    ).toBeVisible();
    expect(screen.getByText("Invalid sign-in state.")).toBeVisible();
    expect(sessionStorage.getItem("delidev:return-to")).toBeNull();
  });
});
