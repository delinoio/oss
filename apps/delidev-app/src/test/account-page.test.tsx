import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

import { createAuthenticatedTransport } from "../api/transports";
import {
  AuthSessionProvider,
  AuthStatus,
} from "../auth/AuthSession";
import { canonicalAudience } from "../config";
import { AccountPage } from "../pages/AccountPage";

function connectJsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    headers: { "content-type": "application/json" },
    status,
  });
}

describe("account organization creation", () => {
  it("reuses the idempotency key until an input changes", async () => {
    const idempotencyKeys: string[] = [];
    let createAttempt = 0;
    const fetchMock = vi.fn<typeof fetch>(async (request, init) => {
      const url = String(request);
      if (url.endsWith("/GetAccountState")) {
        return connectJsonResponse({
          account: { displayName: "Deli Developer" },
          organizations: [],
        });
      }
      const body = await new Response(
        init?.body ?? (request instanceof Request ? request.clone().body : null),
      ).json();
      idempotencyKeys.push(body.idempotency.key);
      createAttempt += 1;
      if (createAttempt < 3) {
        return connectJsonResponse(
          { code: "unavailable", message: "The response was lost." },
          503,
        );
      }
      return connectJsonResponse({
        organization: { name: body.name, slug: body.slug },
      });
    });
    const transport = createAuthenticatedTransport({
      audience: canonicalAudience,
      baseUrl: canonicalAudience,
      fetch: fetchMock,
      getAccessToken: async () => "access-token",
    });
    const queryClient = new QueryClient({
      defaultOptions: {
        mutations: { retry: false },
        queries: { retry: false },
      },
    });
    const user = userEvent.setup();

    render(
      <QueryClientProvider client={queryClient}>
        <MemoryRouter initialEntries={["/account"]}>
          <AuthSessionProvider
            value={{
              signIn: async () => undefined,
              signOut: async () => undefined,
              status: AuthStatus.SignedIn,
              transport,
            }}
          >
            <Routes>
              <Route path="/account" element={<AccountPage />} />
              <Route path="/o/acme/apps" element={<p>Acme apps</p>} />
            </Routes>
          </AuthSessionProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );

    await screen.findByRole("heading", { name: "Account" });
    await user.type(
      screen.getByRole("textbox", { name: "Organization name" }),
      "Acme",
    );
    await user.type(
      screen.getByRole("textbox", { name: "Organization URL" }),
      "acme",
    );
    const submit = screen.getByRole("button", {
      name: "Create organization",
    });

    await user.click(submit);
    await screen.findByRole("alert");
    await user.click(submit);
    await screen.findByRole("alert");
    await user.type(
      screen.getByRole("textbox", { name: "Organization name" }),
      " Labs",
    );
    await user.click(submit);

    expect(await screen.findByText("Acme apps")).toBeVisible();
    expect(idempotencyKeys).toHaveLength(3);
    expect(idempotencyKeys[1]).toBe(idempotencyKeys[0]);
    expect(idempotencyKeys[2]).not.toBe(idempotencyKeys[1]);
  });
});

describe("account deletion", () => {
  it("reuses the pending key across retries and resets it on cancellation", async () => {
    const idempotencyKeys: string[] = [];
    let deleteAttempt = 0;
    const fetchMock = vi.fn<typeof fetch>(async (request, init) => {
      const url = String(request);
      if (url.endsWith("/GetAccountState")) {
        return connectJsonResponse({
          account: { displayName: "Deli Developer" },
          organizations: [],
        });
      }
      if (url.endsWith("/GetAccountDeletionImpact")) {
        return connectJsonResponse({ blockers: [], canDelete: true });
      }
      const body = await new Response(
        init?.body ?? (request instanceof Request ? request.clone().body : null),
      ).json();
      idempotencyKeys.push(body.idempotency.key);
      deleteAttempt += 1;
      if (deleteAttempt < 3) {
        return connectJsonResponse(
          { code: "unavailable", message: "The response was lost." },
          503,
        );
      }
      return connectJsonResponse({});
    });
    const transport = createAuthenticatedTransport({
      audience: canonicalAudience,
      baseUrl: canonicalAudience,
      fetch: fetchMock,
      getAccessToken: async () => "access-token",
    });
    const queryClient = new QueryClient({
      defaultOptions: {
        mutations: { retry: false },
        queries: { retry: false },
      },
    });
    const signOut = vi.fn(async () => undefined);
    const user = userEvent.setup();

    render(
      <QueryClientProvider client={queryClient}>
        <MemoryRouter initialEntries={["/account"]}>
          <AuthSessionProvider
            value={{
              signIn: async () => undefined,
              signOut,
              status: AuthStatus.SignedIn,
              transport,
            }}
          >
            <Routes>
              <Route path="/account" element={<AccountPage />} />
            </Routes>
          </AuthSessionProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );

    await screen.findByRole("heading", { name: "Account" });
    await user.click(
      screen.getByRole("button", { name: "Review deletion" }),
    );
    const deleteAccount = await screen.findByRole("button", {
      name: "Delete account",
    });
    await user.click(deleteAccount);
    await screen.findByRole("alert");
    await user.click(deleteAccount);
    await screen.findByRole("alert");
    await user.click(screen.getByRole("button", { name: "Keep account" }));
    await user.click(
      screen.getByRole("button", { name: "Review deletion" }),
    );
    await user.click(
      await screen.findByRole("button", { name: "Delete account" }),
    );

    expect(signOut).toHaveBeenCalledOnce();
    expect(idempotencyKeys).toHaveLength(3);
    expect(idempotencyKeys[1]).toBe(idempotencyKeys[0]);
    expect(idempotencyKeys[2]).not.toBe(idempotencyKeys[1]);
  });
});
