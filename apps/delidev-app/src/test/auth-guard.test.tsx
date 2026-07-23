import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

import {
  AuthSessionProvider,
  AuthStatus,
  safeReturnPath,
  type AuthSessionValue,
} from "../auth/AuthSession";
import { ProtectedRoute } from "../components/ProtectedRoute";

function renderGuard(
  value: AuthSessionValue,
  initialEntry = "/account?from=test",
) {
  return render(
    <MemoryRouter initialEntries={[initialEntry]}>
      <AuthSessionProvider value={value}>
        <Routes>
          <Route
            path="/account"
            element={
              <ProtectedRoute>
                <p>Private account</p>
              </ProtectedRoute>
            }
          />
          <Route
            path="/invite/:token"
            element={
              <ProtectedRoute checkOnboarding={false}>
                <p>Private invitation</p>
              </ProtectedRoute>
            }
          />
        </Routes>
      </AuthSessionProvider>
    </MemoryRouter>,
  );
}

describe("protected route guard", () => {
  it("offers sign-in and retains the protected return path", async () => {
    const signIn = vi.fn(async () => undefined);
    renderGuard({
      signIn,
      signOut: async () => undefined,
      status: AuthStatus.SignedOut,
    });
    await userEvent.click(
      screen.getByRole("button", { name: "Sign in with Logto" }),
    );
    expect(signIn).toHaveBeenCalledWith("/account?from=test");
    expect(screen.queryByText("Private account")).not.toBeInTheDocument();
  });

  it("fails closed when Logto configuration is unavailable", () => {
    renderGuard({
      error: "Authentication is not configured.",
      signIn: async () => undefined,
      signOut: async () => undefined,
      status: AuthStatus.Unavailable,
    });
    expect(screen.getByText("Authentication is not configured.")).toBeVisible();
    expect(
      screen.getByRole("button", { name: "Sign in with Logto" }),
    ).toBeDisabled();
  });

  it("retains an invitation route for the one-shot sign-in handoff", async () => {
    const signIn = vi.fn(async () => undefined);
    renderGuard(
      {
        signIn,
        signOut: async () => undefined,
        status: AuthStatus.SignedOut,
      },
      "/invite/secret-bearer-token",
    );
    await userEvent.click(
      screen.getByRole("button", { name: "Sign in with Logto" }),
    );
    expect(signIn).toHaveBeenCalledWith("/invite/secret-bearer-token");
  });

  it("accepts only internal callback return paths", () => {
    expect(safeReturnPath("/o/acme/apps")).toBe("/o/acme/apps");
    expect(safeReturnPath("/invite/secret-bearer-token")).toBe(
      "/invite/secret-bearer-token",
    );
    expect(safeReturnPath("//attacker.example")).toBe("/account");
    expect(safeReturnPath("https://attacker.example")).toBe("/account");
  });
});
