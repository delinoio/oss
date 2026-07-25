import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

import { createAuthenticatedTransport } from "../api/transports";
import {
  AuthSessionProvider,
  AuthStatus,
} from "../auth/AuthSession";
import { canonicalAudience } from "../config";
import { MembersPage } from "../pages/OrganizationPages";
import { OrganizationShell } from "../pages/OrganizationShell";
import { TestAccountStateProvider } from "./TestAccountStateProvider";

function connectJsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    headers: { "content-type": "application/json" },
  });
}

describe("organization member management", () => {
  it("refreshes account state before navigating after self-departure", async () => {
    let resolveRefresh: () => void = () => undefined;
    const refreshRelease = new Promise<void>((resolve) => {
      resolveRefresh = resolve;
    });
    const refreshAccountState = vi.fn(() => refreshRelease);
    const fetchMock = vi.fn<typeof fetch>(async (request) => {
      const url = String(request);
      if (url.endsWith("/ResolveOrganizationSlug")) {
        return connectJsonResponse({
          organization: {
            name: "Acme",
            organizationId: { value: "organization-id" },
            slug: "acme",
          },
        });
      }
      if (url.endsWith("/GetOrganization")) {
        return connectJsonResponse({
          callerRole: "ORGANIZATION_ROLE_OWNER",
          organization: {
            name: "Acme",
            organizationId: { value: "organization-id" },
            slug: "acme",
          },
        });
      }
      if (url.endsWith("/ListOrganizationMembers")) {
        return connectJsonResponse({
          members: [
            {
              accountId: { value: "account-id" },
              displayName: "Deli Developer",
              role: "ORGANIZATION_ROLE_OWNER",
            },
          ],
        });
      }
      if (url.endsWith("/ListTeams")) {
        return connectJsonResponse({ teams: [] });
      }
      if (url.endsWith("/ListOrganizationInvitations")) {
        return connectJsonResponse({ invitations: [] });
      }
      if (url.endsWith("/LeaveOrganization")) {
        return connectJsonResponse({});
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
        <MemoryRouter initialEntries={["/o/acme/members"]}>
          <AuthSessionProvider
            value={{
              signIn: async () => undefined,
              signOut: async () => undefined,
              status: AuthStatus.SignedIn,
              transport,
            }}
          >
            <TestAccountStateProvider
              refreshAccountState={refreshAccountState}
            >
              <Routes>
                <Route
                  path="/o/:orgSlug/members"
                  element={
                    <OrganizationShell>
                      <MembersPage />
                    </OrganizationShell>
                  }
                />
                <Route path="/account" element={<p>Account destination</p>} />
              </Routes>
            </TestAccountStateProvider>
          </AuthSessionProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );

    await user.click(
      await screen.findByRole("button", {
        name: "Manage Deli Developer",
      }),
    );
    await user.click(
      screen.getByRole("button", { name: "Leave organization" }),
    );

    await waitFor(() => expect(refreshAccountState).toHaveBeenCalledOnce());
    expect(screen.queryByText("Account destination")).not.toBeInTheDocument();

    resolveRefresh();
    expect(await screen.findByText("Account destination")).toBeVisible();
  });
});
