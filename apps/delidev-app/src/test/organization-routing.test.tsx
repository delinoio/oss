import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { OrganizationRole } from "@delinoio/delibase-connect";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { describe, expect, it } from "vitest";

import { createAuthenticatedTransport } from "../api/transports";
import {
  AuthSessionProvider,
  AuthStatus,
} from "../auth/AuthSession";
import { canonicalAudience } from "../config";
import { OrganizationShell } from "../pages/OrganizationShell";
import { TestAccountStateProvider } from "./TestAccountStateProvider";

function LocationProbe() {
  const location = useLocation();
  return (
    <p data-testid="location">
      {location.pathname}
      {location.search}
      {location.hash}
    </p>
  );
}

describe("organization routing", () => {
  it("canonicalizes old slugs and switches using current server slugs", async () => {
    const transport = createAuthenticatedTransport({
      audience: canonicalAudience,
      baseUrl: canonicalAudience,
      fetch: async (request) => {
        const url = String(request);
        if (url.endsWith("/ResolveOrganizationSlug")) {
          return new Response(
            JSON.stringify({
              matchedAlias: true,
              organization: {
                name: "Acme",
                organizationId: { value: "organization-id" },
                slug: "acme",
              },
            }),
            { headers: { "content-type": "application/json" } },
          );
        }
        return new Response(
          JSON.stringify({
            callerRole: "ORGANIZATION_ROLE_OWNER",
            organization: {
              name: "Acme",
              organizationId: { value: "organization-id" },
              slug: "acme",
            },
          }),
          { headers: { "content-type": "application/json" } },
        );
      },
      getAccessToken: async () => "access-token",
    });
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    const user = userEvent.setup();

    render(
      <QueryClientProvider client={queryClient}>
        <MemoryRouter initialEntries={["/o/old/settings?tab=name#slug"]}>
          <AuthSessionProvider
            value={{
              signIn: async () => undefined,
              signOut: async () => undefined,
              status: AuthStatus.SignedIn,
              transport,
            }}
          >
            <TestAccountStateProvider
              organizations={[
                {
                  name: "Acme",
                  organizationId: { value: "organization-id" },
                  role: OrganizationRole.OWNER,
                  slug: "acme",
                },
                {
                  name: "Beta",
                  organizationId: { value: "beta-id" },
                  role: OrganizationRole.MEMBER,
                  slug: "beta",
                },
              ]}
            >
              <Routes>
                <Route
                  path="/o/:orgSlug/settings"
                  element={
                    <OrganizationShell>
                      <LocationProbe />
                    </OrganizationShell>
                  }
                />
                <Route
                  path="/o/:orgSlug/apps"
                  element={<LocationProbe />}
                />
              </Routes>
            </TestAccountStateProvider>
          </AuthSessionProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );

    expect(await screen.findByTestId("location")).toHaveTextContent(
      "/o/acme/settings?tab=name#slug",
    );
    await user.selectOptions(
      screen.getByRole("combobox", { name: "Switch organization" }),
      "beta",
    );
    expect(await screen.findByTestId("location")).toHaveTextContent(
      "/o/beta/apps",
    );
  });
});
