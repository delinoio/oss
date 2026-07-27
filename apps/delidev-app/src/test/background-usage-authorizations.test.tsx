import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import axe from "axe-core";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { createAuthenticatedTransport } from "../api/transports";
import { BackgroundUsageAuthorizations } from "../components/BackgroundUsageAuthorizations";
import { canonicalAudience } from "../config";

function connectJsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    headers: { "content-type": "application/json" },
    status,
  });
}

function methodName(request: Parameters<typeof fetch>[0]): string {
  return String(request).split("/").at(-1) ?? "";
}

async function requestBody(
  request: Parameters<typeof fetch>[0],
  init: Parameters<typeof fetch>[1],
) {
  return (await new Response(
    init?.body ?? (request instanceof Request ? request.clone().body : null),
  ).json()) as Record<string, unknown>;
}

function authorizationView({
  authorizationId = "01900000-0000-7000-8000-000000000001",
  resourceId = "01900000-0000-7000-8000-000000000099",
  revision = "4",
  status = "BACKGROUND_USAGE_AUTHORIZATION_STATUS_ACTIVE",
} = {}) {
  return {
    authorization: {
      authorizationId: { value: authorizationId },
      authorizerAccountId: {
        value: "01900000-0000-7000-8000-000000000010",
      },
      featureResourceId: { value: resourceId },
      maximumUnits: { value: "1000" },
      meterId: { value: "01900000-0000-7000-8000-000000000020" },
      organizationId: {
        value: "01900000-0000-7000-8000-000000000030",
      },
      owner: {
        personalAccountId: {
          value: "01900000-0000-7000-8000-000000000010",
        },
      },
      period: "BACKGROUND_USAGE_PERIOD_UTC_DAY",
      purpose: "BACKGROUND_USAGE_PURPOSE_REALQA_STORAGE",
      revision,
      serviceIdentityId: {
        value: "01900000-0000-7000-8000-000000000040",
      },
      status,
      teamId: { value: "01900000-0000-7000-8000-000000000050" },
    },
    currentPeriodUsage: {
      committedUnits: { value: "300" },
      heldUnits: { value: "20" },
      maximumUnits: { value: "1000" },
      remainingUnits: { value: "680" },
    },
  };
}

function setOnline(online: boolean) {
  Object.defineProperty(navigator, "onLine", {
    configurable: true,
    value: online,
  });
  window.dispatchEvent(new Event(online ? "online" : "offline"));
}

function renderAuthorizations(
  fetchMock: typeof fetch,
  scope:
    | { kind: "account" }
    | {
        kind: "organization";
        organizationId: string;
        organizationName: string;
        showOrganizationWide: boolean;
      },
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
  return render(
    <QueryClientProvider client={queryClient}>
      <main id="main-content" tabIndex={-1}>
        <BackgroundUsageAuthorizations scope={scope} transport={transport} />
      </main>
    </QueryClientProvider>,
  );
}

afterEach(() => setOnline(true));

describe("background usage authorization management", () => {
  it("uses server role filtering, opaque pagination, safe detail fields, and replay-safe revocation", async () => {
    const first = authorizationView();
    const second = authorizationView({
      authorizationId: "01900000-0000-7000-8000-000000000002",
      resourceId: "01900000-0000-7000-8000-000000000098",
    });
    const listBodies: Record<string, unknown>[] = [];
    const revokeBodies: Record<string, unknown>[] = [];
    let revoked = false;
    let revokeAttempts = 0;
    const fetchMock = vi.fn<typeof fetch>(async (request, init) => {
      const method = methodName(request);
      const body = await requestBody(request, init);
      if (method === "ListBackgroundUsageAuthorizations") {
        listBodies.push(body);
        const cursor = (body.page as { cursor?: string } | undefined)?.cursor;
        if (cursor === "next-page") {
          return connectJsonResponse({ authorizations: [second] });
        }
        return connectJsonResponse({
          authorizations: [
            revoked
              ? authorizationView({
                  revision: "5",
                  status:
                    "BACKGROUND_USAGE_AUTHORIZATION_STATUS_REVOKED",
                })
              : first,
          ],
          page: revoked ? {} : { nextCursor: "next-page" },
        });
      }
      if (method === "GetBackgroundUsageAuthorization") {
        return connectJsonResponse({
          authorization: revoked
            ? authorizationView({
                revision: "5",
                status: "BACKGROUND_USAGE_AUTHORIZATION_STATUS_REVOKED",
              })
            : first,
        });
      }
      if (method === "RevokeBackgroundUsageAuthorization") {
        revokeBodies.push(body);
        revokeAttempts += 1;
        if (revokeAttempts === 1) {
          return connectJsonResponse(
            { code: "unavailable", message: "Response lost." },
            503,
          );
        }
        revoked = true;
        return connectJsonResponse({
          authorization: authorizationView({
            revision: "5",
            status: "BACKGROUND_USAGE_AUTHORIZATION_STATUS_REVOKED",
          }),
          idempotency: { replayed: true },
        });
      }
      throw new Error(`Unexpected method ${method}`);
    });
    const user = userEvent.setup();
    const { container } = renderAuthorizations(fetchMock, {
      kind: "organization",
      organizationId: "organization-id",
      organizationName: "Acme",
      showOrganizationWide: true,
    });

    expect(
      await screen.findByText(/review and revoke every grant paid by Acme/),
    ).toBeInTheDocument();
    expect(listBodies[0]).toMatchObject({
      organizationId: { value: "organization-id" },
      page: { pageSize: 25 },
    });
    expect(listBodies[0]).not.toHaveProperty("owner");

    await user.click(
      await screen.findByRole("button", {
        name: "Load more authorizations",
      }),
    );
    await waitFor(() =>
      expect(screen.getAllByRole("button", { name: /View details/ })).toHaveLength(
        2,
      ),
    );
    expect(listBodies.at(-1)).toMatchObject({
      page: { cursor: "next-page", pageSize: 25 },
    });

    const detailTrigger = screen.getAllByRole("button", {
      name: /View details/,
    })[0]!;
    await user.click(detailTrigger);
    const dialog = await screen.findByRole("dialog", {
      name: "Background usage authorization",
    });
    expect(dialog).toHaveTextContent("Personal account …00000010");
    expect(dialog).toHaveTextContent("Organization …00000030");
    expect(dialog).toHaveTextContent("…00000050");
    expect(dialog).toHaveTextContent("RealQA storage");
    expect(dialog).toHaveTextContent("…00000099");
    expect(dialog).toHaveTextContent("Current-period units320");
    expect(dialog).toHaveTextContent("Maximum units per UTC day1,000");
    expect(dialog).toHaveTextContent("Billing failureNone reported");
    expect(dialog).toHaveTextContent("Revision4");
    expect(dialog).not.toHaveTextContent(/secret|credential value|provider id/i);

    await user.click(
      screen.getByRole("button", { name: "Review revocation" }),
    );
    expect(
      screen.getByRole("button", { name: "Keep authorization" }),
    ).toHaveFocus();
    await user.keyboard("{Escape}");
    expect(
      screen.getByRole("dialog", {
        name: "Background usage authorization",
      }),
    ).toBeVisible();
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(detailTrigger).toHaveFocus();

    await user.click(detailTrigger);
    await user.click(
      await screen.findByRole("button", { name: "Review revocation" }),
    );
    setOnline(false);
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "Revoke authorization" }),
      ).toBeDisabled(),
    );
    setOnline(true);
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "Revoke authorization" }),
      ).toBeEnabled(),
    );
    await user.click(
      screen.getByRole("button", { name: "Revoke authorization" }),
    );
    expect(
      await screen.findByText(/temporarily unavailable/),
    ).toBeInTheDocument();
    await user.click(
      screen.getByRole("button", { name: "Revoke authorization" }),
    );
    expect(
      await screen.findByText(
        "Revocation confirmed from the original safe retry.",
      ),
    ).toBeInTheDocument();
    expect(revokeBodies).toHaveLength(2);
    expect(revokeBodies[0]).toMatchObject({ expectedRevision: "4" });
    expect(
      (revokeBodies[0]!.idempotency as { key: string }).key,
    ).toBe((revokeBodies[1]!.idempotency as { key: string }).key);
    expect(
      screen.queryByRole("button", { name: "Review revocation" }),
    ).not.toBeInTheDocument();

    const result = await axe.run(container, {
      runOnly: {
        type: "tag",
        values: ["wcag2a", "wcag2aa", "wcag21aa", "wcag22aa"],
      },
    });
    expect(result.violations).toEqual([]);
  });

  it("uses the account-wide server filter and explains member visibility", async () => {
    const bodies: Record<string, unknown>[] = [];
    const fetchMock = vi.fn<typeof fetch>(async (request, init) => {
      bodies.push(await requestBody(request, init));
      return connectJsonResponse({ authorizations: [] });
    });

    const { rerender } = renderAuthorizations(fetchMock, {
      kind: "account",
    });
    expect(
      await screen.findByText(/grants you created plus organization-wide grants/),
    ).toBeInTheDocument();
    expect(bodies[0]).not.toHaveProperty("owner");
    expect(bodies[0]).not.toHaveProperty("organizationId");

    rerender(<></>);
    renderAuthorizations(fetchMock, {
      kind: "organization",
      organizationId: "organization-id",
      organizationName: "Acme",
      showOrganizationWide: false,
    });
    expect(
      await screen.findByText(/Only grants you created and their summarized usage/),
    ).toBeInTheDocument();
  });

  it("renders access-loss recovery without offering an invalid revoke", async () => {
    const accessLost = authorizationView({
      revision: "7",
      status: "BACKGROUND_USAGE_AUTHORIZATION_STATUS_ACCESS_LOST",
    });
    const fetchMock = vi.fn<typeof fetch>(async (request) => {
      if (methodName(request) === "GetBackgroundUsageAuthorization") {
        return connectJsonResponse({ authorization: accessLost });
      }
      return connectJsonResponse({ authorizations: [accessLost] });
    });
    const user = userEvent.setup();
    renderAuthorizations(fetchMock, {
      kind: "organization",
      organizationId: "organization-id",
      organizationName: "Acme",
      showOrganizationWide: false,
    });

    await user.click(
      await screen.findByRole("button", { name: /View details/ }),
    );
    const dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveTextContent("Access lost");
    expect(dialog).toHaveTextContent(
      "Restore the authorizer’s organization and team access",
    );
    expect(
      screen.queryByRole("button", { name: "Review revocation" }),
    ).not.toBeInTheDocument();
  });

  it("distinguishes offline and dependency failures without issuing offline requests", async () => {
    setOnline(false);
    const fetchMock = vi.fn<typeof fetch>();
    const offlineView = renderAuthorizations(fetchMock, {
      kind: "account",
    });
    expect(
      screen.getByText(/Reconnect to load network-only authorization/),
    ).toBeInTheDocument();
    expect(fetchMock).not.toHaveBeenCalled();
    offlineView.unmount();

    setOnline(true);
    const unavailable = vi.fn<typeof fetch>(async () =>
      connectJsonResponse(
        { code: "unavailable", message: "Dependency unavailable." },
        503,
      ),
    );
    renderAuthorizations(unavailable, {
      kind: "account",
    });
    expect(
      await screen.findByRole("heading", {
        name: "Background authorizations unavailable",
      }),
    ).toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveTextContent(
      "temporarily unavailable",
    );
  });
});
