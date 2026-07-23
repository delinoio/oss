import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { createAuthenticatedTransport } from "../api/transports";
import { canonicalAudience } from "../config";
import { OrganizationInvitationManagement } from "../pages/OrganizationPages";

function connectJsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    headers: { "content-type": "application/json" },
    status,
  });
}

describe("organization invitation management", () => {
  it("creates, lists, and replay-safely revokes invitations", async () => {
    const revocationKeys: string[] = [];
    const createRequests: Record<string, unknown>[] = [];
    let revokeAttempt = 0;
    let activeInvitation = true;
    let failInvitationRefresh = false;
    let invitationRefreshFailures = 0;
    const fetchMock = vi.fn<typeof fetch>(async (request, init) => {
      const url = String(request);
      const body = (await new Response(
        init?.body ?? (request instanceof Request ? request.clone().body : null),
      ).json()) as Record<string, unknown>;

      if (url.endsWith("/ListTeams")) {
        return connectJsonResponse({
          teams: [
            {
              depth: 0,
              name: "General",
              organizationId: { value: "organization-id" },
              protectedGeneral: true,
              teamId: { value: "team-id" },
            },
          ],
        });
      }
      if (url.endsWith("/ListOrganizationInvitations")) {
        if (failInvitationRefresh) {
          failInvitationRefresh = false;
          invitationRefreshFailures += 1;
          return connectJsonResponse(
            { code: "unavailable", message: "The refresh failed." },
            503,
          );
        }
        return connectJsonResponse({
          invitations: activeInvitation
            ? [
                {
                  invitationId: { value: "invitation-id" },
                  organizationId: { value: "organization-id" },
                  organizationRole: "ORGANIZATION_ROLE_MEMBER",
                  status: "INVITATION_STATUS_ACTIVE",
                  teamId: { value: "team-id" },
                  teamRole: "TEAM_ROLE_MEMBER",
                },
              ]
            : [],
        });
      }
      if (url.endsWith("/CreateOrganizationInvitation")) {
        createRequests.push(body);
        return connectJsonResponse({
          bearerToken: { token: "one-time-bearer-token" },
          invitation: {
            invitationId: { value: "created-invitation-id" },
            organizationId: { value: "organization-id" },
            organizationRole: "ORGANIZATION_ROLE_MEMBER",
            status: "INVITATION_STATUS_ACTIVE",
            teamId: { value: "team-id" },
            teamRole: "TEAM_ROLE_MEMBER",
          },
        });
      }
      if (url.endsWith("/RevokeOrganizationInvitation")) {
        revocationKeys.push(
          (body.idempotency as { key: string }).key,
        );
        revokeAttempt += 1;
        if (revokeAttempt === 1) {
          return connectJsonResponse(
            { code: "unavailable", message: "The response was lost." },
            503,
          );
        }
        activeInvitation = false;
        if (revokeAttempt === 2) {
          failInvitationRefresh = true;
        }
        return connectJsonResponse({
          invitation: {
            invitationId: { value: "invitation-id" },
            status: "INVITATION_STATUS_REVOKED",
          },
        });
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
        <OrganizationInvitationManagement
          organization={
            {
              $typeName: "delibase.v1.Organization",
              name: "Acme",
              organizationId: {
                $typeName: "delibase.v1.UuidV7",
                value: "organization-id",
              },
              slug: "acme",
              status: 1,
            }
          }
          transport={transport}
        />
      </QueryClientProvider>,
    );

    await screen.findByRole("heading", { name: "Create an invitation" });
    await user.selectOptions(
      await screen.findByRole("combobox", { name: "Team" }),
      "team-id",
    );
    await user.click(
      screen.getByRole("button", { name: "Create invitation" }),
    );

    expect(
      await screen.findByLabelText(/Invitation link/),
    ).toHaveValue(
      "http://localhost:3000/invite/one-time-bearer-token",
    );
    expect(createRequests).toHaveLength(1);
    expect(createRequests[0]).toMatchObject({
      organizationRole: "ORGANIZATION_ROLE_MEMBER",
      teamId: { value: "team-id" },
      teamRole: "TEAM_ROLE_MEMBER",
    });
    expect(
      queryClient
        .getMutationCache()
        .getAll()
        .map((mutation) => mutation.state.data),
    ).not.toContainEqual(
      expect.objectContaining({
        bearerToken: expect.anything(),
      }),
    );

    const revoke = await screen.findByRole("button", { name: "Revoke" });
    await user.click(revoke);
    await screen.findByRole("alert");
    await user.click(screen.getByRole("button", { name: "Revoke" }));

    await waitFor(() => expect(invitationRefreshFailures).toBe(1));
    await user.click(screen.getByRole("button", { name: "Revoke" }));

    await waitFor(() => expect(revocationKeys).toHaveLength(3));
    expect(revocationKeys[1]).toBe(revocationKeys[0]);
    expect(revocationKeys[2]).toBe(revocationKeys[0]);
    await waitFor(() =>
      expect(
        screen.getByText("There are no active invitations."),
      ).toBeVisible(),
    );
  });
});
