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
import { TeamsPage } from "../pages/OrganizationPages";
import { OrganizationShell } from "../pages/OrganizationShell";

function connectJsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    headers: { "content-type": "application/json" },
    status,
  });
}

describe("organization team management", () => {
  it("reuses a subtree deletion key and hides the row if refresh fails", async () => {
    const deletionKeys: string[] = [];
    let deleteAttempt = 0;
    let deletionCommitted = false;
    const fetchMock = vi.fn<typeof fetch>(async (request, init) => {
      const url = String(request);
      const body = (await new Response(
        init?.body ?? (request instanceof Request ? request.clone().body : null),
      ).json()) as Record<string, unknown>;

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
      if (url.endsWith("/ListTeams")) {
        if (deletionCommitted) {
          return connectJsonResponse(
            { code: "unavailable", message: "The refresh failed." },
            503,
          );
        }
        return connectJsonResponse({
          teams: [
            {
              depth: 0,
              name: "Platform",
              organizationId: { value: "organization-id" },
              protectedGeneral: false,
              teamId: { value: "team-id" },
            },
          ],
        });
      }
      if (url.endsWith("/DeleteTeamSubtree")) {
        deletionKeys.push(
          (body.idempotency as { key: string }).key,
        );
        deleteAttempt += 1;
        if (deleteAttempt === 1) {
          return connectJsonResponse(
            { code: "unavailable", message: "The response was lost." },
            503,
          );
        }
        deletionCommitted = true;
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
        <MemoryRouter initialEntries={["/o/acme/teams"]}>
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
                path="/o/:orgSlug/teams"
                element={
                  <OrganizationShell>
                    <TeamsPage />
                  </OrganizationShell>
                }
              />
            </Routes>
          </AuthSessionProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );

    await user.click(
      await screen.findByRole("button", { name: "Manage" }),
    );
    await user.click(
      screen.getByRole("button", { name: "Delete subtree" }),
    );
    await screen.findByRole("alert");
    await user.click(
      screen.getByRole("button", { name: "Delete subtree" }),
    );

    await waitFor(() => expect(deletionKeys).toHaveLength(2));
    expect(deletionKeys[1]).toBe(deletionKeys[0]);
    await waitFor(() =>
      expect(screen.getByText("No teams found")).toBeVisible(),
    );
  });
});
