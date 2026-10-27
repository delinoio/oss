// SPDX-License-Identifier: Apache-2.0
import { describe, expect, it, rs } from "@rstest/core";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {
  createRouterTransport,
  Code,
  ConnectError,
  type ConnectRouter,
} from "@connectrpc/connect";
import { QueryClient } from "@tanstack/react-query";
import type { ReactNode } from "react";
import {
  AccountService,
  AuditAction,
  AuditOutcome,
  AuditService,
  HealthState,
  OperatorService,
  RequestOutcome,
  Role,
  UsageService,
  type ListUsageRequest,
  type GetUsageSummaryRequest,
  type ListDailyUsageRequest,
  type ListOrganizationAuditRequest,
  type ListInstanceAuditRequest,
} from "@aiprouter/client";
import { ApiProvider } from "../api";
import { AdminAuthority } from "./common";
import { Audit, OrganizationUsage } from "./reporting";
import { Health, Prices } from "./operator";
function mount(children: ReactNode, routes: (router: ConnectRouter) => void) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const transport = createRouterTransport((router) => {
    router.service(AccountService, {
      getSession: () => ({ isInstanceOperator: true }),
    });
    routes(router);
  });
  render(
    <ApiProvider queryClient={client} rpcTransport={transport}>
      <AdminAuthority.Provider
        value={{
          organizationId: "organization-a",
          role: Role.OWNER,
          onDenied: () => {},
        }}
      >
        {children}
      </AdminAuthority.Provider>
    </ApiProvider>,
  );
  return client;
}
describe("server-side reporting filters", () => {
  it("applies provider and outcome to requests, summaries and daily data; resets cursors", async () => {
    const calls = {
      list: rs.fn<(request: ListUsageRequest) => void>(),
      summary: rs.fn<(request: GetUsageSummaryRequest) => void>(),
      daily: rs.fn<(request: ListDailyUsageRequest) => void>(),
    };
    mount(<OrganizationUsage />, (router) => {
      router.service(UsageService, {
        listUsage: (request) => {
          calls.list(request);
          return {
            records: [
              {
                id: "returned",
                modelId: "Server-filtered result",
                providerId: "provider-a",
              },
            ],
            page: {
              nextPageToken: request.page?.pageToken ? "" : "next-requests",
            },
          };
        },
        getUsageSummary: (request) => {
          calls.summary(request);
          return {};
        },
        listDailyUsage: (request) => {
          calls.daily(request);
          return {
            days: [],
            page: { nextPageToken: request.page?.pageToken ? "" : "next-days" },
          };
        },
      });
    });
    const user = userEvent.setup();
    await screen.findByText("Server-filtered result");
    await user.type(
      screen.getByRole("textbox", { name: "Member ID" }),
      "member-a",
    );
    await user.type(screen.getByRole("textbox", { name: "Model" }), "model-a");
    await user.type(
      screen.getByRole("textbox", { name: "Provider" }),
      "provider-a",
    );
    await user.selectOptions(
      screen.getByRole("combobox", { name: "Outcome" }),
      String(RequestOutcome.RATE_LIMITED),
    );
    const filters = {
      organizationId: "organization-a",
      userId: "member-a",
      modelId: "model-a",
      providerId: "provider-a",
      outcome: RequestOutcome.RATE_LIMITED,
    };
    await waitFor(() => {
      for (const call of Object.values(calls))
        expect(call.mock.lastCall?.[0]).toMatchObject(filters);
    });
    expect(calls.list.mock.lastCall?.[0].timeRange?.start).toBeTruthy();
    // The server response is authoritative; the client must not filter it again.
    expect(await screen.findByText("Server-filtered result")).toBeTruthy();
    const requestSection = screen
      .getByRole("heading", { name: "Request metadata" })
      .closest("section")!;
    await user.click(
      within(requestSection).getByRole("button", { name: "Next page" }),
    );
    await waitFor(() =>
      expect(calls.list.mock.lastCall?.[0].page?.pageToken).toBe(
        "next-requests",
      ),
    );
    const dailySection = screen
      .getByRole("heading", { name: "Daily usage" })
      .closest("section")!;
    await user.click(
      within(dailySection).getByRole("button", { name: "Next page" }),
    );
    await waitFor(() =>
      expect(calls.daily.mock.lastCall?.[0].page?.pageToken).toBe("next-days"),
    );
    await user.selectOptions(
      screen.getByRole("combobox", { name: "Outcome" }),
      String(RequestOutcome.UNSPECIFIED),
    );
    await waitFor(() => {
      for (const call of Object.values(calls))
        expect(call.mock.lastCall?.[0].outcome).toBe(
          RequestOutcome.UNSPECIFIED,
        );
      expect(calls.list.mock.lastCall?.[0].page?.pageToken).toBe("");
      expect(calls.daily.mock.lastCall?.[0].page?.pageToken).toBe("");
    });
    await user.click(screen.getByRole("button", { name: "Clear filters" }));
    await waitFor(() => {
      for (const call of Object.values(calls)) {
        expect(call.mock.lastCall?.[0].outcome).toBeUndefined();
        expect(call.mock.lastCall?.[0].providerId).toBeUndefined();
      }
    });
  });
  it.each([false, true])(
    "sends conjunctive audit filters to the correct scope (operator=%s)",
    async (operator) => {
      const org = rs.fn<(request: ListOrganizationAuditRequest) => void>(),
        instance = rs.fn<(request: ListInstanceAuditRequest) => void>();
      mount(<Audit operator={operator} />, (router) => {
        router.service(AuditService, {
          listOrganizationAudit: (request) => {
            org(request);
            return {
              records: [],
              page: { nextPageToken: request.page?.pageToken ? "" : "next" },
            };
          },
        });
        router.service(OperatorService, {
          listInstanceAudit: (request) => {
            instance(request);
            return {
              records: [],
              page: { nextPageToken: request.page?.pageToken ? "" : "next" },
            };
          },
        });
      });
      const user = userEvent.setup();
      await screen.findByText(/No audit records match/);
      await user.type(
        screen.getByRole("textbox", { name: "Actor ID" }),
        "actor-a",
      );
      await user.selectOptions(
        screen.getByRole("combobox", { name: "Action" }),
        String(AuditAction.KEY_REVOKED),
      );
      await user.selectOptions(
        screen.getByRole("combobox", { name: "Outcome" }),
        String(AuditOutcome.DENIED),
      );
      const calls = operator ? instance : org;
      await waitFor(() =>
        expect(calls.mock.lastCall?.[0]).toMatchObject({
          actorUserId: "actor-a",
          action: AuditAction.KEY_REVOKED,
          outcome: AuditOutcome.DENIED,
        }),
      );
      await user.click(screen.getByRole("button", { name: "Next page" }));
      await waitFor(() =>
        expect(calls.mock.lastCall?.[0].page?.pageToken).toBe("next"),
      );
      await user.selectOptions(
        screen.getByRole("combobox", { name: "Outcome" }),
        String(AuditOutcome.UNSPECIFIED),
      );
      await waitFor(() =>
        expect(calls.mock.lastCall?.[0]).toMatchObject({
          outcome: AuditOutcome.UNSPECIFIED,
          page: { pageToken: "" },
        }),
      );
      expect(operator ? org : instance).not.toHaveBeenCalled();
      if (operator)
        expect(calls.mock.lastCall?.[0]).not.toHaveProperty("organizationId");
      await user.click(screen.getByRole("button", { name: "Clear filters" }));
      await waitFor(() => {
        expect(calls.mock.lastCall?.[0].action).toBeUndefined();
        expect(calls.mock.lastCall?.[0].actorUserId).toBeUndefined();
        expect(calls.mock.lastCall?.[0].outcome).toBeUndefined();
      });
    },
  );
  it("renders reported audit outcomes without treating unknown as success", async () => {
    mount(<Audit />, (router) =>
      router.service(AuditService, {
        listOrganizationAudit: () => ({
          records: [
            AuditOutcome.UNSPECIFIED,
            AuditOutcome.SUCCEEDED,
            AuditOutcome.FAILED,
            AuditOutcome.DENIED,
          ].map((outcome, index) => ({ id: String(index), outcome })),
        }),
      }),
    );
    for (const outcome of ["Unknown", "Succeeded", "Failed", "Denied"])
      expect(await screen.findByText(`Outcome: ${outcome}`)).toBeTruthy();
  });
  it.each([
    [HealthState.DEGRADED, "Degraded"],
    [HealthState.UNSPECIFIED, "Unknown"],
  ])("renders usage recording health %s", async (state, label) => {
    mount(<Health />, (router) =>
      router.service(OperatorService, {
        getInstanceHealth: () => ({ health: { usageRecording: state } }),
        getInstanceUsage: () => ({}),
      }),
    );
    expect(
      await screen.findByText(new RegExp(`Usage recording health: ${label}`)),
    ).toBeTruthy();
  });
});
function priceFixture({
  revisionMissing = false,
  pageConflict = false,
}: { revisionMissing?: boolean; pageConflict?: boolean } = {}) {
  let revision = "1";
  const set = rs.fn(),
    remove = rs.fn(),
    list = rs.fn();
  const price = {
    id: "override",
    providerId: "provider-a",
    modelId: "model-a",
    serviceTier: "",
    rates: { currency: "USD", input: "1", output: "2" },
  };
  const client = mount(<Prices />, (router) =>
    router.service(OperatorService, {
      listPriceOverrides: (request) => {
        list(request);
        if (pageConflict && request.page?.pageToken)
          throw new ConnectError("Stale cursor", Code.InvalidArgument);
        return {
          overrides: [{ ...price, rates: { ...price.rates, input: revision } }],
          revision: revisionMissing ? "" : revision,
          page: { nextPageToken: pageConflict ? "next" : "" },
        };
      },
      setPriceOverride: (request) => {
        set(request);
        if (request.expectedRevision !== revision)
          throw new ConnectError("Stale snapshot", Code.Aborted);
        return { revision };
      },
      deletePriceOverride: (request) => {
        remove(request);
        if (request.expectedRevision !== revision)
          throw new ConnectError("Stale snapshot", Code.Aborted);
        return { revision };
      },
    }),
  );
  return {
    client,
    set,
    remove,
    list,
    advance: () => {
      revision = "2";
    },
  };
}
describe("price collection compare-and-swap", () => {
  it("retains the original revision across refetches and requires explicit conflict review", async () => {
    const f = priceFixture();
    const user = userEvent.setup();
    await user.click(
      await screen.findByRole("button", { name: "Edit override" }),
    );
    const dialog = screen.getByRole("dialog");
    const input = within(dialog).getByRole("textbox", { name: "Input" });
    await user.clear(input);
    await user.type(input, "9.5");
    f.advance();
    await f.client.invalidateQueries();
    await user.click(
      within(dialog).getByRole("button", { name: "Save override" }),
    );
    await waitFor(() => expect(f.set).toHaveBeenCalledTimes(1));
    expect(f.set.mock.lastCall?.[0].expectedRevision).toBe("1");
    await within(dialog).findByText(/Prices changed since/);
    expect((input as HTMLInputElement).value).toBe("9.5");
    expect(
      (
        within(dialog).getByRole("button", {
          name: "Save override",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
    await user.click(
      within(dialog).getByRole("button", { name: "Refresh prices for review" }),
    );
    await within(dialog).findByRole("heading", { name: "Current saved price" });
    expect(
      within(dialog).getByText("Input: 2 per million tokens"),
    ).toBeTruthy();
    expect(f.set).toHaveBeenCalledTimes(1);
    await user.click(
      within(dialog).getByRole("button", { name: "Use reviewed revision" }),
    );
    await user.click(
      within(dialog).getByRole("button", { name: "Save override" }),
    );
    await waitFor(() => expect(f.set).toHaveBeenCalledTimes(2));
    expect(f.set.mock.lastCall?.[0]).toMatchObject({
      expectedRevision: "2",
      rates: { input: "9.5" },
    });
  });
  it("uses the delete-dialog snapshot and blocks stale deletion until review", async () => {
    const f = priceFixture();
    const user = userEvent.setup();
    await user.click(
      await screen.findByRole("button", { name: "Delete override" }),
    );
    const dialog = screen.getByRole("dialog");
    await user.type(within(dialog).getByRole("textbox"), "model-a");
    f.advance();
    await user.click(
      within(dialog).getByRole("button", { name: "Delete price override" }),
    );
    await within(dialog).findByText(/Prices changed since/);
    expect(f.remove.mock.lastCall?.[0]).toMatchObject({
      overrideId: "override",
      expectedRevision: "1",
    });
    expect(
      (
        within(dialog).getByRole("button", {
          name: "Delete price override",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
    await user.click(
      within(dialog).getByRole("button", { name: "Refresh prices for review" }),
    );
    await within(dialog).findByRole("heading", { name: "Current saved price" });
    await user.click(
      within(dialog).getByRole("button", { name: "Use reviewed revision" }),
    );
    await user.click(
      within(dialog).getByRole("button", { name: "Delete price override" }),
    );
    await waitFor(() => expect(f.remove).toHaveBeenCalledTimes(2));
    expect(f.remove.mock.lastCall?.[0].expectedRevision).toBe("2");
  });
  it("sends the list revision when adding an override", async () => {
    const f = priceFixture();
    const user = userEvent.setup();
    await screen.findByRole("button", { name: "Edit override" });
    await user.click(
      screen.getByRole("button", { name: "Add price override" }),
    );
    const dialog = screen.getByRole("dialog");
    await user.type(
      within(dialog).getByRole("textbox", { name: "Provider ID" }),
      "new-provider",
    );
    await user.type(
      within(dialog).getByRole("textbox", { name: "Model ID" }),
      "new-model",
    );
    await user.click(
      within(dialog).getByRole("button", { name: "Save override" }),
    );
    await waitFor(() => expect(f.set).toHaveBeenCalledTimes(1));
    expect(f.set.mock.lastCall?.[0].expectedRevision).toBe("1");
  });
  it("blocks mutations when the server did not supply a revision", async () => {
    priceFixture({ revisionMissing: true });
    await screen.findByRole("button", { name: "Edit override" });
    for (const name of [
      "Edit override",
      "Delete override",
      "Add price override",
    ])
      expect(
        (screen.getByRole("button", { name }) as HTMLButtonElement).disabled,
      ).toBe(true);
  });
  it("recovers stale pagination by requesting the first page explicitly", async () => {
    const f = priceFixture({ pageConflict: true });
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "Next page" }));
    await screen.findByRole("alert");
    await user.click(screen.getByRole("button", { name: "Try again" }));
    await screen.findByRole("button", { name: "Edit override" });
    expect(f.list.mock.lastCall?.[0].page.pageToken).toBe("");
  });
});
