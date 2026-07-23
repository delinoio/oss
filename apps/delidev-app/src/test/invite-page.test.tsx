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
import { InvitePage } from "../pages/InvitePage";

function connectJsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    headers: { "content-type": "application/json" },
    status: 200,
  });
}

describe("organization invitation", () => {
  it("keeps the bearer token out of React Query and formats the generated role", async () => {
    const token = "secret-invitation-token";
    const fetchMock = vi.fn<typeof fetch>(async (request, init) => {
      const url = String(request);
      const body = await new Response(
        init?.body ?? (request instanceof Request ? request.clone().body : null),
      ).json();
      expect(body.bearerToken.token).toBe(token);

      if (url.endsWith("/GetOrganizationInvitation")) {
        return connectJsonResponse({
          invitation: {
            organizationRole: "ORGANIZATION_ROLE_MEMBER",
            teamId: { value: "01912345-0000-7000-8000-000000000001" },
            teamRole: "TEAM_ROLE_ADMIN",
          },
          organizationName: "Acme",
          teamName: "General",
        });
      }
      return connectJsonResponse({
        organization: { name: "Acme", slug: "acme" },
      });
    });
    const transport = createAuthenticatedTransport({
      audience: canonicalAudience,
      baseUrl: canonicalAudience,
      fetch: fetchMock,
      getAccessToken: async () => "access-token",
    });

    render(
      <MemoryRouter initialEntries={[`/invite/${token}`]}>
        <AuthSessionProvider
          value={{
            signIn: async () => undefined,
            signOut: async () => undefined,
            status: AuthStatus.SignedIn,
            transport,
          }}
        >
          <Routes>
            <Route path="/invite/:token" element={<InvitePage />} />
            <Route path="/o/acme/apps" element={<p>Organization apps</p>} />
          </Routes>
        </AuthSessionProvider>
      </MemoryRouter>,
    );

    expect(
      await screen.findByRole("heading", { name: "Join Acme" }),
    ).toBeVisible();
    expect(
      screen.getByText("Your organization role will be Member."),
    ).toBeVisible();
    expect(screen.getByText(/team as Admin\./)).toBeVisible();

    await userEvent.click(
      screen.getByRole("button", { name: "Accept invitation" }),
    );
    expect(await screen.findByText("Organization apps")).toBeVisible();
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("does not show an empty team assignment for organization-only invitations", async () => {
    const fetchMock = vi.fn<typeof fetch>(async () =>
      connectJsonResponse({
        invitation: {
          organizationRole: "ORGANIZATION_ROLE_ADMIN",
        },
        organizationName: "Acme",
        teamName: "",
      }),
    );
    const transport = createAuthenticatedTransport({
      audience: canonicalAudience,
      baseUrl: canonicalAudience,
      fetch: fetchMock,
      getAccessToken: async () => "access-token",
    });

    render(
      <MemoryRouter initialEntries={["/invite/organization-admin-token"]}>
        <AuthSessionProvider
          value={{
            signIn: async () => undefined,
            signOut: async () => undefined,
            status: AuthStatus.SignedIn,
            transport,
          }}
        >
          <Routes>
            <Route path="/invite/:token" element={<InvitePage />} />
          </Routes>
        </AuthSessionProvider>
      </MemoryRouter>,
    );

    expect(
      await screen.findByText("Your organization role will be Admin."),
    ).toBeVisible();
    expect(screen.queryByText(/team as/)).not.toBeInTheDocument();
  });

  it("reuses the acceptance idempotency key after a lost response", async () => {
    const idempotencyKeys: string[] = [];
    let acceptanceAttempt = 0;
    const fetchMock = vi.fn<typeof fetch>(async (request, init) => {
      const url = String(request);
      if (url.endsWith("/GetOrganizationInvitation")) {
        return connectJsonResponse({
          invitation: {
            organizationRole: "ORGANIZATION_ROLE_MEMBER",
          },
          organizationName: "Acme",
        });
      }
      const body = await new Response(
        init?.body ?? (request instanceof Request ? request.clone().body : null),
      ).json();
      idempotencyKeys.push(body.idempotency.key);
      acceptanceAttempt += 1;
      if (acceptanceAttempt === 1) {
        return new Response(
          JSON.stringify({
            code: "unavailable",
            message: "The response was lost.",
          }),
          {
            headers: { "content-type": "application/json" },
            status: 503,
          },
        );
      }
      return connectJsonResponse({
        organization: { name: "Acme", slug: "acme" },
      });
    });
    const transport = createAuthenticatedTransport({
      audience: canonicalAudience,
      baseUrl: canonicalAudience,
      fetch: fetchMock,
      getAccessToken: async () => "access-token",
    });
    const user = userEvent.setup();

    render(
      <MemoryRouter initialEntries={["/invite/retry-token"]}>
        <AuthSessionProvider
          value={{
            signIn: async () => undefined,
            signOut: async () => undefined,
            status: AuthStatus.SignedIn,
            transport,
          }}
        >
          <Routes>
            <Route path="/invite/:token" element={<InvitePage />} />
            <Route path="/o/acme/apps" element={<p>Organization apps</p>} />
          </Routes>
        </AuthSessionProvider>
      </MemoryRouter>,
    );

    const accept = await screen.findByRole("button", {
      name: "Accept invitation",
    });
    await user.click(accept);
    await screen.findByRole("alert");
    await user.click(accept);

    expect(await screen.findByText("Organization apps")).toBeVisible();
    expect(idempotencyKeys).toHaveLength(2);
    expect(idempotencyKeys[1]).toBe(idempotencyKeys[0]);
  });
});
