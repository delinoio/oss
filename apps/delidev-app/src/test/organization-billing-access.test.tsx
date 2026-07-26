import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import axe from "axe-core";
import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

import { createAuthenticatedTransport } from "../api/transports";
import {
  AuthSessionProvider,
  AuthStatus,
} from "../auth/AuthSession";
import { canonicalAudience } from "../config";
import {
  BillingPage,
  UsagePage,
} from "../pages/OrganizationPages";
import { OrganizationShell } from "../pages/OrganizationShell";
import * as hostedBilling from "../utils/hostedBilling";
import { TestAccountStateProvider } from "./TestAccountStateProvider";

function connectJsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    headers: { "content-type": "application/json" },
    status,
  });
}

function methodName(request: Parameters<typeof fetch>[0]): string {
  return String(request).split("/").at(-1) ?? "";
}

function organizationResponse(role: "ADMIN" | "MEMBER" | "OWNER") {
  return {
    callerRole: `ORGANIZATION_ROLE_${role}`,
    organization: {
      name: "Acme",
      organizationId: { value: "organization-id" },
      slug: "acme",
      status: "ORGANIZATION_STATUS_ACTIVE",
    },
  };
}

function renderOrganizationPage({
  fetch,
  page,
  path,
}: {
  fetch: typeof globalThis.fetch;
  page: ReactNode;
  path: string;
}) {
  const transport = createAuthenticatedTransport({
    audience: canonicalAudience,
    baseUrl: canonicalAudience,
    fetch,
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
      <MemoryRouter initialEntries={[path]}>
        <AuthSessionProvider
          value={{
            signIn: async () => undefined,
            signOut: async () => undefined,
            status: AuthStatus.SignedIn,
            transport,
          }}
        >
          <TestAccountStateProvider>
            <Routes>
              <Route
                element={
                  <main id="main-content" tabIndex={-1}>
                    <OrganizationShell>{page}</OrganizationShell>
                  </main>
                }
                path="/o/:orgSlug/:section"
              />
            </Routes>
          </TestAccountStateProvider>
        </AuthSessionProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("organization billing access", () => {
  it("gives Members only the shared balance and never requests manager data", async () => {
    const methods: string[] = [];
    const fetchMock = vi.fn<typeof fetch>(async (request) => {
      const method = methodName(request);
      methods.push(method);
      if (method === "ResolveOrganizationSlug") {
        return connectJsonResponse({
          organization: organizationResponse("MEMBER").organization,
        });
      }
      if (method === "GetOrganization") {
        return connectJsonResponse(organizationResponse("MEMBER"));
      }
      if (method === "GetBillingSummary") {
        return connectJsonResponse({
          summary: { availableCredit: { value: "7250000" } },
        });
      }
      throw new Error(`Member client requested forbidden method ${method}`);
    });

    const { container } = renderOrganizationPage({
      fetch: fetchMock,
      page: <BillingPage />,
      path: "/o/acme/billing",
    });

    expect(await screen.findByText("$7.25")).toBeInTheDocument();
    expect(screen.getByText("Your billing access")).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Start subscription" }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Invoices and payment" }),
    ).not.toBeInTheDocument();
    expect(screen.queryByText("Held credit")).not.toBeInTheDocument();
    expect(screen.queryByText("Committed overage")).not.toBeInTheDocument();
    expect(screen.queryByText("Credit ledger")).not.toBeInTheDocument();
    expect(methods).not.toContain("ListLedgerEntries");
    expect(methods).not.toContain("CreateSubscriptionCheckout");
    expect(methods).not.toContain("CreateBillingPortalSession");
    expect(methods).not.toContain("UpdateOverageLimit");
    const result = await axe.run(container, {
      runOnly: {
        type: "tag",
        values: ["wcag2a", "wcag2aa", "wcag21aa", "wcag22aa"],
      },
    });
    expect(result.violations).toEqual([]);
  });

  it("shows manager payment, period, overage, policy, and ledger context", async () => {
    const fetchMock = vi.fn<typeof fetch>(async (request) => {
      const method = methodName(request);
      if (method === "ResolveOrganizationSlug") {
        return connectJsonResponse({
          organization: organizationResponse("ADMIN").organization,
        });
      }
      if (method === "GetOrganization") {
        return connectJsonResponse(organizationResponse("ADMIN"));
      }
      if (method === "GetBillingSummary") {
        return connectJsonResponse({
          summary: {
            availableCredit: { value: "19000000" },
            committedOverage: { value: "2000000" },
            currentPeriod: {
              endsAt: "2026-07-31T00:00:00Z",
              startsAt: "2026-06-30T00:00:00Z",
              status: "BILLING_PERIOD_STATUS_OPEN",
            },
            heldCredit: { value: "1000000" },
            heldOverage: { value: "500000" },
            monthlyOverageLimit: { value: "3000000" },
            newOverageAllowed: true,
            overageLimitConfigured: true,
            subscriptionStatus: "SUBSCRIPTION_STATUS_ACTIVE",
          },
        });
      }
      if (method === "ListLedgerEntries") {
        return connectJsonResponse({
          entries: [
            {
              amount: { value: "10000000" },
              balanceAfter: { value: "20000000" },
              ledgerEntryId: { value: "entry-id" },
              operation: "LEDGER_OPERATION_CREDIT_GRANT",
              teamNameSnapshot: "General",
            },
          ],
        });
      }
      throw new Error(`Unexpected method ${method}`);
    });

    renderOrganizationPage({
      fetch: fetchMock,
      page: <BillingPage />,
      path: "/o/acme/billing",
    });

    expect(
      await screen.findByRole("heading", { name: "Active" }),
    ).toBeInTheDocument();
    expect(screen.getByText(/10,000,000 USD micro-units/)).toBeInTheDocument();
    expect(screen.getByText("Committed + held overage")).toBeInTheDocument();
    expect(screen.getByText("Allowed")).toBeInTheDocument();
    expect(screen.getByText("Refunds and chargebacks")).toBeInTheDocument();
    expect(screen.getByText("Polar outage")).toBeInTheDocument();
    expect(
      await screen.findByText("Credit grant"),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Invoices and payment" }),
    ).toBeInTheDocument();
  });

  it.each([
    {
      action: "Start subscription",
      method: "CreateSubscriptionCheckout",
      response: {
        checkoutUrl: "https://checkout.polar.sh/session/checkout-id",
      },
      status: "SUBSCRIPTION_STATUS_NONE",
      url: "https://checkout.polar.sh/session/checkout-id",
    },
    {
      action: "Start subscription",
      method: "CreateSubscriptionCheckout",
      response: {
        checkoutUrl: "https://checkout.polar.sh/session/canceled-id",
      },
      status: "SUBSCRIPTION_STATUS_CANCELED",
      url: "https://checkout.polar.sh/session/canceled-id",
    },
    {
      action: "Start subscription",
      method: "CreateSubscriptionCheckout",
      response: {
        checkoutUrl: "https://checkout.polar.sh/session/revoked-id",
      },
      status: "SUBSCRIPTION_STATUS_REVOKED",
      url: "https://checkout.polar.sh/session/revoked-id",
    },
    {
      action: "Invoices and payment",
      method: "CreateBillingPortalSession",
      response: {
        portalUrl: "https://polar.sh/customer-portal/session-id",
      },
      status: "SUBSCRIPTION_STATUS_ACTIVE",
      url: "https://polar.sh/customer-portal/session-id",
    },
  ])("opens Polar for $action from $status", async ({
    action,
    method: billingMethod,
    response,
    status,
    url,
  }) => {
    const navigate = vi
      .spyOn(hostedBilling, "navigateToPolarHostedPage")
      .mockReturnValue(true);
    const fetchMock = vi.fn<typeof fetch>(async (request) => {
      const method = methodName(request);
      if (method === "ResolveOrganizationSlug") {
        return connectJsonResponse({
          organization: organizationResponse("OWNER").organization,
        });
      }
      if (method === "GetOrganization") {
        return connectJsonResponse(organizationResponse("OWNER"));
      }
      if (method === "GetBillingSummary") {
        return connectJsonResponse({
          summary: {
            availableCredit: { value: "0" },
            committedOverage: { value: "0" },
            heldCredit: { value: "0" },
            heldOverage: { value: "0" },
            monthlyOverageLimit: { value: "0" },
            overageLimitConfigured: false,
            subscriptionStatus: status,
          },
        });
      }
      if (method === "ListLedgerEntries") {
        return connectJsonResponse({ entries: [] });
      }
      if (method === billingMethod) {
        return connectJsonResponse(response);
      }
      throw new Error(`Unexpected method ${method}`);
    });
    const user = userEvent.setup();
    renderOrganizationPage({
      fetch: fetchMock,
      page: <BillingPage />,
      path: "/o/acme/billing",
    });

    await user.click(await screen.findByRole("button", { name: action }));
    await waitFor(() => expect(navigate).toHaveBeenCalledWith(url));
    navigate.mockRestore();
  });

  it("shows the latest billing action error and renews portal keys after success", async () => {
    const portalKeys: string[] = [];
    let portalAttempts = 0;
    const navigate = vi
      .spyOn(hostedBilling, "navigateToPolarHostedPage")
      .mockReturnValue(true);
    const fetchMock = vi.fn<typeof fetch>(async (request, init) => {
      const method = methodName(request);
      if (method === "ResolveOrganizationSlug") {
        return connectJsonResponse({
          organization: organizationResponse("OWNER").organization,
        });
      }
      if (method === "GetOrganization") {
        return connectJsonResponse(organizationResponse("OWNER"));
      }
      if (method === "GetBillingSummary") {
        return connectJsonResponse({
          summary: {
            availableCredit: { value: "0" },
            committedOverage: { value: "0" },
            heldCredit: { value: "0" },
            heldOverage: { value: "0" },
            monthlyOverageLimit: { value: "0" },
            overageLimitConfigured: false,
            subscriptionStatus: "SUBSCRIPTION_STATUS_CANCELED",
          },
        });
      }
      if (method === "ListLedgerEntries") {
        return connectJsonResponse({ entries: [] });
      }
      if (method === "CreateSubscriptionCheckout") {
        return connectJsonResponse(
          { code: "permission_denied", message: "Checkout denied." },
          403,
        );
      }
      if (method === "CreateBillingPortalSession") {
        const body = (await new Response(
          init?.body ??
            (request instanceof Request ? request.clone().body : null),
        ).json()) as { idempotency: { key: string } };
        portalKeys.push(body.idempotency.key);
        portalAttempts += 1;
        if (portalAttempts === 1) {
          return connectJsonResponse(
            { code: "unavailable", message: "The response was lost." },
            503,
          );
        }
        return connectJsonResponse({
          portalUrl: `https://polar.sh/customer-portal/session-${portalAttempts}`,
        });
      }
      throw new Error(`Unexpected method ${method}`);
    });
    const user = userEvent.setup();
    renderOrganizationPage({
      fetch: fetchMock,
      page: <BillingPage />,
      path: "/o/acme/billing",
    });

    await user.click(
      await screen.findByRole("button", { name: "Start subscription" }),
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Checkout denied.",
    );

    const openPortal = screen.getByRole("button", {
      name: "Invoices and payment",
    });
    await user.click(openPortal);
    await waitFor(() =>
      expect(screen.getByRole("alert")).toHaveTextContent(
        "The service is temporarily unavailable.",
      ),
    );

    await user.click(openPortal);
    await waitFor(() =>
      expect(navigate).toHaveBeenCalledWith(
        "https://polar.sh/customer-portal/session-2",
      ),
    );
    await user.click(openPortal);
    await waitFor(() =>
      expect(navigate).toHaveBeenCalledWith(
        "https://polar.sh/customer-portal/session-3",
      ),
    );

    expect(portalKeys).toHaveLength(3);
    expect(portalKeys[1]).toBe(portalKeys[0]);
    expect(portalKeys[2]).not.toBe(portalKeys[1]);
    navigate.mockRestore();
  });

  it("disables checkout and overage mutations while offline", async () => {
    Object.defineProperty(navigator, "onLine", {
      configurable: true,
      value: false,
    });
    window.dispatchEvent(new Event("offline"));
    const methods: string[] = [];
    const fetchMock = vi.fn<typeof fetch>(async (request) => {
      const method = methodName(request);
      methods.push(method);
      if (method === "ResolveOrganizationSlug") {
        return connectJsonResponse({
          organization: organizationResponse("OWNER").organization,
        });
      }
      if (method === "GetOrganization") {
        return connectJsonResponse(organizationResponse("OWNER"));
      }
      if (method === "GetBillingSummary") {
        return connectJsonResponse({
          summary: {
            availableCredit: { value: "0" },
            committedOverage: { value: "0" },
            heldCredit: { value: "0" },
            heldOverage: { value: "0" },
            monthlyOverageLimit: { value: "0" },
            overageLimitConfigured: false,
            subscriptionStatus: "SUBSCRIPTION_STATUS_NONE",
          },
        });
      }
      if (method === "ListLedgerEntries") {
        return connectJsonResponse({ entries: [] });
      }
      throw new Error(`Offline mutation reached ${method}`);
    });
    const user = userEvent.setup();

    try {
      renderOrganizationPage({
        fetch: fetchMock,
        page: <BillingPage />,
        path: "/o/acme/billing",
      });
      expect(
        await screen.findByRole("button", { name: "Start subscription" }),
      ).toBeDisabled();
      await user.click(
        screen.getByRole("button", { name: "Change monthly limit" }),
      );
      expect(
        screen.getByRole("button", { name: "Update limit" }),
      ).toBeDisabled();
      expect(screen.getAllByText("Reconnect to use this action.")).toHaveLength(
        2,
      );
      expect(methods).not.toContain("CreateSubscriptionCheckout");
      expect(methods).not.toContain("UpdateOverageLimit");
    } finally {
      Object.defineProperty(navigator, "onLine", {
        configurable: true,
        value: true,
      });
      window.dispatchEvent(new Event("online"));
    }
  });

  it("validates overage in a focus-managed dialog and reuses safe retry identity", async () => {
    const updateKeys: string[] = [];
    const fetchMock = vi.fn<typeof fetch>(async (request, init) => {
      const method = methodName(request);
      if (method === "ResolveOrganizationSlug") {
        return connectJsonResponse({
          organization: organizationResponse("OWNER").organization,
        });
      }
      if (method === "GetOrganization") {
        return connectJsonResponse(organizationResponse("OWNER"));
      }
      if (method === "GetBillingSummary") {
        return connectJsonResponse({
          summary: {
            availableCredit: { value: "0" },
            committedOverage: { value: "0" },
            heldCredit: { value: "0" },
            heldOverage: { value: "0" },
            monthlyOverageLimit: { value: "0" },
            overageLimitConfigured: false,
            subscriptionStatus: "SUBSCRIPTION_STATUS_NONE",
          },
        });
      }
      if (method === "ListLedgerEntries") {
        return connectJsonResponse({ entries: [] });
      }
      if (method === "UpdateOverageLimit") {
        const body = (await new Response(
          init?.body ??
            (request instanceof Request ? request.clone().body : null),
        ).json()) as {
          idempotency: { key: string };
          monthlyLimit: { value: string };
        };
        updateKeys.push(body.idempotency.key);
        expect(body.monthlyLimit.value).toBe("1250000");
        return connectJsonResponse(
          { code: "unavailable", message: "retry safely" },
          503,
        );
      }
      throw new Error(`Unexpected method ${method}`);
    });
    const user = userEvent.setup();

    renderOrganizationPage({
      fetch: fetchMock,
      page: <BillingPage />,
      path: "/o/acme/billing",
    });

    const opener = await screen.findByRole("button", {
      name: "Change monthly limit",
    });
    await user.click(opener);
    const input = screen.getByRole("spinbutton", { name: "Limit in USD" });
    expect(input).toHaveFocus();
    await user.clear(input);
    await user.type(input, "1.250000");
    await user.click(screen.getByRole("button", { name: "Update limit" }));
    await screen.findByRole("alert");
    await user.click(screen.getByRole("button", { name: "Update limit" }));
    await screen.findByRole("alert");
    expect(updateKeys[1]).toBe(updateKeys[0]);

    fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    await waitFor(() => expect(opener).toHaveFocus());
  });

  it("keeps the overage dialog open while an update is pending", async () => {
    let resolveUpdate: (response: Response) => void = () => undefined;
    const updateResponse = new Promise<Response>((resolve) => {
      resolveUpdate = resolve;
    });
    const fetchMock = vi.fn<typeof fetch>(async (request) => {
      const method = methodName(request);
      if (method === "ResolveOrganizationSlug") {
        return connectJsonResponse({
          organization: organizationResponse("OWNER").organization,
        });
      }
      if (method === "GetOrganization") {
        return connectJsonResponse(organizationResponse("OWNER"));
      }
      if (method === "GetBillingSummary") {
        return connectJsonResponse({
          summary: {
            availableCredit: { value: "0" },
            committedOverage: { value: "0" },
            heldCredit: { value: "0" },
            heldOverage: { value: "0" },
            monthlyOverageLimit: { value: "0" },
            overageLimitConfigured: false,
            subscriptionStatus: "SUBSCRIPTION_STATUS_NONE",
          },
        });
      }
      if (method === "ListLedgerEntries") {
        return connectJsonResponse({ entries: [] });
      }
      if (method === "UpdateOverageLimit") {
        return updateResponse;
      }
      throw new Error(`Unexpected method ${method}`);
    });
    const user = userEvent.setup();

    renderOrganizationPage({
      fetch: fetchMock,
      page: <BillingPage />,
      path: "/o/acme/billing",
    });

    await user.click(
      await screen.findByRole("button", {
        name: "Change monthly limit",
      }),
    );
    await user.click(screen.getByRole("button", { name: "Update limit" }));
    expect(
      await screen.findByRole("button", { name: "Updating…" }),
    ).toBeDisabled();

    fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    fireEvent.mouseDown(document.querySelector(".dialog-backdrop")!);
    expect(screen.getByRole("dialog")).toBeInTheDocument();

    resolveUpdate(connectJsonResponse({}));
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
    );
  });
});

describe("organization usage access and pagination", () => {
  it("renders organization attribution, outage state, and opaque cursor pages", async () => {
    const usageBodies: Array<{ page?: { cursor?: string } }> = [];
    const fetchMock = vi.fn<typeof fetch>(async (request, init) => {
      const method = methodName(request);
      if (method === "ResolveOrganizationSlug") {
        return connectJsonResponse({
          organization: organizationResponse("ADMIN").organization,
        });
      }
      if (method === "GetOrganization") {
        return connectJsonResponse(organizationResponse("ADMIN"));
      }
      if (method === "GetBillingSummary") {
        return connectJsonResponse({
          summary: {
            availableCredit: { value: "8000000" },
            committedOverage: { value: "500000" },
            currentPeriod: {
              endsAt: "2026-07-31T00:00:00Z",
              startsAt: "2026-06-30T00:00:00Z",
            },
            heldOverage: { value: "250000" },
          },
        });
      }
      if (method === "ListUsageRecords") {
        const body = (await new Response(
          init?.body ??
            (request instanceof Request ? request.clone().body : null),
        ).json()) as { page?: { cursor?: string } };
        usageBodies.push(body);
        const secondPage = body.page?.cursor === "next-usage";
        return connectJsonResponse({
          page: { nextCursor: secondPage ? "" : "next-usage" },
          records: [
            {
              clientReference: secondPage ? "job-2" : "job-1",
              committedAt: "2026-07-01T00:00:00Z",
              creditApplied: { value: secondPage ? "100000" : "0" },
              meterId: { value: "meter-image-generation" },
              overageApplied: { value: secondPage ? "0" : "500000" },
              serviceIdentityId: { value: "service-image-worker" },
              status: secondPage
                ? "USAGE_RECORD_STATUS_COMMITTED"
                : "USAGE_RECORD_STATUS_POLAR_PENDING",
              teamNameSnapshot: secondPage ? "General" : "Design",
              units: { value: secondPage ? "5" : "25" },
              usageRecordId: { value: secondPage ? "usage-2" : "usage-1" },
              usdMicrosPerUnit: { value: "20000" },
              userAccountId: { value: "account-id" },
            },
          ],
        });
      }
      throw new Error(`Unexpected method ${method}`);
    });
    const user = userEvent.setup();

    renderOrganizationPage({
      fetch: fetchMock,
      page: <UsagePage />,
      path: "/o/acme/usage",
    });

    expect(
      await screen.findByText(/Some overage usage is queued for Polar/),
    ).toBeInTheDocument();
    const table = screen.getByRole("table", { name: "Usage records" });
    expect(within(table).getByText("You")).toBeInTheDocument();
    expect(within(table).getByText("Queued for Polar")).toBeInTheDocument();
    expect(within(table).getByText("job-1")).toBeInTheDocument();
    await user.click(
      screen.getByRole("button", { name: "Load more usage" }),
    );
    expect(await within(table).findByText("job-2")).toBeInTheDocument();
    expect(usageBodies[1]?.page?.cursor).toBe("next-usage");
    expect(
      screen.getByText(/never performs charging/),
    ).toBeInTheDocument();
  });
});
