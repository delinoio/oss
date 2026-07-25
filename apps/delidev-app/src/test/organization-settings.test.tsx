import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {
  MemoryRouter,
  Route,
  Routes,
  useLocation,
} from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

import { createAuthenticatedTransport } from "../api/transports";
import {
  AuthSessionProvider,
  AuthStatus,
} from "../auth/AuthSession";
import { canonicalAudience } from "../config";
import { OrganizationSettingsPage } from "../pages/OrganizationPages";
import { OrganizationShell } from "../pages/OrganizationShell";
import { TestAccountStateProvider } from "./TestAccountStateProvider";

function connectJsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    headers: { "content-type": "application/json" },
    status,
  });
}

function LocationProbe() {
  const location = useLocation();
  return <span data-testid="location">{location.pathname}</span>;
}

function renderSettings(
  fetchMock: typeof fetch,
  refreshAccountState: () => Promise<void>,
) {
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

  render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={["/o/acme/settings"]}>
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
                path="/o/:orgSlug/settings"
                element={
                  <OrganizationShell>
                    <LocationProbe />
                    <OrganizationSettingsPage />
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
}

describe("organization settings", () => {
  it("reports a saved name when organization refresh fails", async () => {
    let getOrganizationCalls = 0;
    const refreshAccountState = vi.fn(async () => undefined);
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
        getOrganizationCalls += 1;
        if (getOrganizationCalls > 1) {
          return connectJsonResponse(
            { code: "unavailable", message: "Refresh failed." },
            503,
          );
        }
        return connectJsonResponse({
          callerRole: "ORGANIZATION_ROLE_OWNER",
          organization: {
            name: "Acme",
            organizationId: { value: "organization-id" },
            slug: "acme",
          },
        });
      }
      if (url.endsWith("/UpdateOrganization")) {
        return connectJsonResponse({
          organization: {
            name: "Acme Labs",
            organizationId: { value: "organization-id" },
            slug: "acme",
          },
        });
      }
      throw new Error(`Unexpected request: ${url}`);
    });
    const user = userEvent.setup();
    renderSettings(fetchMock, refreshAccountState);

    const nameInput = await screen.findByRole("textbox", {
      name: "Organization name",
    });
    await user.clear(nameInput);
    await user.type(nameInput, "Acme Labs");
    await user.click(
      screen.getByRole("button", { name: "Save changes" }),
    );

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "The organization name was saved, but current organization data could not be refreshed.",
    );
    expect(
      screen.queryByText("Organization settings updated."),
    ).not.toBeInTheDocument();
    expect(refreshAccountState).toHaveBeenCalledTimes(2);
  });

  it("does not navigate to a changed slug when account refresh fails", async () => {
    const refreshAccountState = vi.fn(async () => {
      throw new Error("Account refresh failed.");
    });
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
      if (url.endsWith("/UpdateOrganizationSlug")) {
        return connectJsonResponse({
          organization: {
            name: "Acme",
            organizationId: { value: "organization-id" },
            slug: "new-acme",
          },
        });
      }
      throw new Error(`Unexpected request: ${url}`);
    });
    const user = userEvent.setup();
    renderSettings(fetchMock, refreshAccountState);

    const slugInput = await screen.findByDisplayValue("acme");
    await user.clear(slugInput);
    await user.type(slugInput, "new-acme");
    await user.click(
      screen.getByRole("button", { name: "Save changes" }),
    );

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Account refresh failed.",
    );
    expect(refreshAccountState).toHaveBeenCalledOnce();
    expect(screen.getByTestId("location")).toHaveTextContent(
      "/o/acme/settings",
    );
  });

  it("retains the organization deletion key when confirmation is retyped", async () => {
    const deletionKeys: string[] = [];
    let deleteAttempt = 0;
    const refreshAccountState = vi.fn(async () => undefined);
    const fetchMock = vi.fn<typeof fetch>(async (request, init) => {
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
      if (url.endsWith("/DeleteOrganization")) {
        const body = (await new Response(
          init?.body ?? (request instanceof Request
            ? request.clone().body
            : null),
        ).json()) as { idempotency: { key: string } };
        deletionKeys.push(body.idempotency.key);
        deleteAttempt += 1;
        if (deleteAttempt === 1) {
          return connectJsonResponse(
            { code: "unavailable", message: "The response was lost." },
            503,
          );
        }
        return connectJsonResponse({});
      }
      throw new Error(`Unexpected request: ${url}`);
    });
    const user = userEvent.setup();
    renderSettings(fetchMock, refreshAccountState);

    await user.click(
      await screen.findByRole("button", {
        name: "Delete organization",
      }),
    );
    const confirmation = screen.getByRole("textbox", {
      name: "Enter Acme to confirm",
    });
    await user.type(confirmation, "Acme");
    await user.click(
      screen.getByRole("button", { name: "Delete organization" }),
    );
    await screen.findByRole("alert");

    await user.clear(confirmation);
    await user.type(confirmation, "Acme");
    await user.click(
      screen.getByRole("button", { name: "Delete organization" }),
    );

    await waitFor(() => expect(deletionKeys).toHaveLength(2));
    expect(deletionKeys[1]).toBe(deletionKeys[0]);
    expect(await screen.findByText("Account destination")).toBeVisible();
  });
});
