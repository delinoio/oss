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

describe("Logto callback page", () => {
  beforeEach(() => {
    callbackState.error = undefined;
    callbackState.isAuthenticated = true;
    callbackState.isLoading = false;
    sessionStorage.clear();
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

  it("renders a safe error state when Logto rejects the callback", () => {
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
  });
});
