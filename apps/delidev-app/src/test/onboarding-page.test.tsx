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
import { OnboardingPage } from "../pages/OnboardingPage";

function connectJsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    headers: { "content-type": "application/json" },
    status,
  });
}

describe("account onboarding", () => {
  it("reuses the idempotency key until an input changes", async () => {
    const idempotencyKeys: string[] = [];
    let attempt = 0;
    const fetchMock = vi.fn<typeof fetch>(async (request, init) => {
      const body = await new Response(
        init?.body ?? (request instanceof Request ? request.clone().body : null),
      ).json();
      idempotencyKeys.push(body.idempotency.key);
      attempt += 1;
      if (attempt < 3) {
        return connectJsonResponse(
          { code: "unavailable", message: "The response was lost." },
          503,
        );
      }
      return connectJsonResponse({
        organizationId: {
          value: "01912345-0000-7000-8000-000000000001",
        },
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
        <MemoryRouter initialEntries={["/onboarding"]}>
          <AuthSessionProvider
            value={{
              signIn: async () => undefined,
              signOut: async () => undefined,
              status: AuthStatus.SignedIn,
              transport,
            }}
          >
            <Routes>
              <Route path="/onboarding" element={<OnboardingPage />} />
              <Route path="/o/acme/apps" element={<p>Acme apps</p>} />
            </Routes>
          </AuthSessionProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );

    await user.type(
      screen.getByRole("textbox", { name: "Your name" }),
      "Deli Developer",
    );
    await user.type(
      screen.getByRole("textbox", { name: "Organization name" }),
      "Acme",
    );
    await user.type(
      screen.getByRole("textbox", { name: /^Organization URL/ }),
      "acme",
    );
    const submit = screen.getByRole("button", { name: "Create workspace" });

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
