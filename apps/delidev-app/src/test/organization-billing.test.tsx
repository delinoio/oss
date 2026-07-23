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
import { BillingPage } from "../pages/OrganizationPages";
import { OrganizationShell } from "../pages/OrganizationShell";

function connectJsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    headers: { "content-type": "application/json" },
    status,
  });
}

describe("organization billing", () => {
  it("reuses the subscription checkout key after a lost response", async () => {
    const checkoutKeys: string[] = [];
    const fetchMock = vi.fn<typeof fetch>(async (request, init) => {
      const url = String(request);
      if (url.endsWith("/ResolveOrganizationSlug")) {
        return connectJsonResponse({
          organization: {
            name: "Acme",
            organizationId: { value: "organization-id" },
            slug: "acme",
            status: "ORGANIZATION_STATUS_ACTIVE",
          },
        });
      }
      if (url.endsWith("/GetOrganization")) {
        return connectJsonResponse({
          callerRole: "ORGANIZATION_ROLE_ADMIN",
          organization: {
            name: "Acme",
            organizationId: { value: "organization-id" },
            slug: "acme",
            status: "ORGANIZATION_STATUS_ACTIVE",
          },
        });
      }
      if (url.endsWith("/GetBillingSummary")) {
        return connectJsonResponse({
          summary: {
            availableCredit: { value: "0" },
            heldCredit: { value: "0" },
            monthlyOverageLimit: { value: "0" },
            overageLimitConfigured: false,
            subscriptionStatus: "SUBSCRIPTION_STATUS_NONE",
          },
        });
      }
      if (url.endsWith("/ListLedgerEntries")) {
        return connectJsonResponse({ entries: [] });
      }
      if (url.endsWith("/CreateSubscriptionCheckout")) {
        const body = (await new Response(
          init?.body ??
            (request instanceof Request ? request.clone().body : null),
        ).json()) as { idempotency: { key: string } };
        checkoutKeys.push(body.idempotency.key);
        return connectJsonResponse(
          { code: "unavailable", message: "The response was lost." },
          503,
        );
      }
      throw new Error(`Unexpected request: ${url}`);
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
        <MemoryRouter initialEntries={["/o/acme/billing"]}>
          <AuthSessionProvider
            value={{
              signIn: async () => undefined,
              signOut: async () => undefined,
              status: AuthStatus.SignedIn,
              transport,
            }}
          >
            <Routes>
              <Route
                path="/o/:orgSlug/billing"
                element={
                  <OrganizationShell>
                    <BillingPage />
                  </OrganizationShell>
                }
              />
            </Routes>
          </AuthSessionProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );

    const startSubscription = await screen.findByRole("button", {
      name: "Start subscription",
    });
    await user.click(startSubscription);
    await screen.findByRole("alert");
    await user.click(startSubscription);
    await screen.findByRole("alert");

    expect(checkoutKeys).toHaveLength(2);
    expect(checkoutKeys[1]).toBe(checkoutKeys[0]);
  });
});
