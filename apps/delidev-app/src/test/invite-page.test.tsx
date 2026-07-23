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
    expect(screen.getByText(/team as Member\./)).toBeVisible();

    await userEvent.click(
      screen.getByRole("button", { name: "Accept invitation" }),
    );
    expect(await screen.findByText("Organization apps")).toBeVisible();
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });
});
