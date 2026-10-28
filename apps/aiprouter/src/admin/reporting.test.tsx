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
      within(requestSection).getByRole("button", { name: "Next requests" }),
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
      within(dailySection).getByRole("button", {
        name: "Next daily usage",
      }),
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
      await screen.findByText(
        operator ? /No audit records match/ : "No audit events available.",
      );
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
  it("keeps instance audit rows unchanged", async () => {
    const org = rs.fn();
    mount(<Audit operator />, (router) => {
      router.service(AuditService, {
        listOrganizationAudit: () => {
          org();
          return {};
        },
      });
      router.service(OperatorService, {
        listInstanceAudit: () => ({
          records: [
            AuditOutcome.UNSPECIFIED,
            AuditOutcome.SUCCEEDED,
            AuditOutcome.FAILED,
            AuditOutcome.DENIED,
          ].map((outcome, index) => ({ id: String(index), outcome })),
        }),
      });
    });
    expect(
      await screen.findByRole("heading", { level: 1, name: "Instance audit" }),
    ).toBeTruthy();
    for (const outcome of ["Unknown", "Succeeded", "Failed", "Denied"])
      expect(await screen.findByText(`Outcome: ${outcome}`)).toBeTruthy();
    expect(screen.queryByRole("table")).toBeNull();
    expect(org).not.toHaveBeenCalled();
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
    const block = (await screen.findByText("Usage recording")).closest("div");
    expect(within(block as HTMLElement).getByText(label)).toBeTruthy();
    expect(
      screen.getByText(
        "Recording failures can leave request totals incomplete.",
      ),
    ).toBeTruthy();
  });
});
function priceFixture({
  revisionMissing = false,
  pageConflict = false,
  overrides,
}: {
  revisionMissing?: boolean;
  pageConflict?: boolean;
  overrides?: Record<string, unknown>[];
} = {}) {
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
          overrides: overrides ?? [
            { ...price, rates: { ...price.rates, input: revision } },
          ],
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
      await screen.findByRole("button", {
        name: /^Edit override for provider-a/,
      }),
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
    const reviewPanel = within(dialog).getByRole("region", {
      name: "Current price review",
    });
    expect(
      reviewPanel.querySelector("dt")?.nextElementSibling?.textContent,
    ).toBe("2");
    expect(
      within(reviewPanel).getByText("USD per million tokens"),
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
      await screen.findByRole("button", {
        name: /^Delete override for provider-a/,
      }),
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
    await screen.findByRole("button", {
      name: /^Edit override for provider-a/,
    });
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
    await screen.findByRole("button", {
      name: /^Edit override for provider-a/,
    });
    for (const name of [
      /^Edit override for provider-a/,
      /^Delete override for provider-a/,
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
    await screen.findByRole("button", {
      name: /^Edit override for provider-a/,
    });
    expect(f.list.mock.lastCall?.[0].page.pageToken).toBe("");
  });
});
describe("price override list presentation", () => {
  const rates = (extra: Record<string, string> = {}) => ({
    currency: "USD",
    ...extra,
  });
  it("shows the page header, scope note and a record with all six rates", async () => {
    priceFixture({
      overrides: [
        {
          id: "o1",
          providerId: "provider-with-a-very-long-identifier-that-must-wrap",
          modelId: "model-x",
          serviceTier: "",
          rates: rates({ input: "0", output: "0.000001234567890123456789" }),
        },
      ],
    });
    expect(
      screen.getByRole("heading", { level: 1, name: "Price overrides" }),
    ).toBeTruthy();
    expect(
      screen.getByText("Common prices in currency units per million tokens."),
    ).toBeTruthy();
    expect(
      screen.getByText(
        "Applies to new requests only. Historical estimates remain unchanged.",
      ),
    ).toBeTruthy();
    const record = (
      await screen.findByRole("heading", { level: 2, name: "model-x" })
    ).closest("article") as HTMLElement;
    expect(
      within(record).getByText(
        "Provider ID: provider-with-a-very-long-identifier-that-must-wrap",
      ),
    ).toBeTruthy();
    expect(within(record).getByText("All tiers (fallback)")).toBeTruthy();
    expect(within(record).getByText("Currency: USD")).toBeTruthy();
    expect(
      within(record).getByText("Provenance: Instance operator override"),
    ).toBeTruthy();
    expect(within(record).getByText("USD per million tokens")).toBeTruthy();
    const value = (name: string) =>
      within(record).getByText(name).closest("div")?.querySelector("dd")
        ?.textContent;
    expect(value("Input")).toBe("0");
    expect(value("Output")).toBe("0.000001234567890123456789");
    for (const name of [
      "Cache read",
      "Cache write",
      "Short cache write",
      "Long cache write",
    ])
      expect(value(name)).toBe("Unknown");
    for (const dd of record.querySelectorAll("dd"))
      expect(dd.getAttribute("aria-describedby")).toBeTruthy();
  });
  it("distinguishes empty, literal default and named tiers", async () => {
    priceFixture({
      overrides: [
        { id: "a", providerId: "p", modelId: "m-all", serviceTier: "" },
        {
          id: "b",
          providerId: "p",
          modelId: "m-default",
          serviceTier: "default",
        },
        {
          id: "c",
          providerId: "p",
          modelId: "m-priority",
          serviceTier: "priority",
        },
      ],
    });
    const tier = async (model: string) =>
      (
        (await screen.findByRole("heading", { name: model })).closest(
          "article",
        ) as HTMLElement
      ).textContent;
    expect(await tier("m-all")).toContain("All tiers (fallback)");
    expect(await tier("m-default")).toContain("Tier: default");
    expect(await tier("m-default")).not.toContain("All tiers");
    expect(await tier("m-priority")).toContain("Tier: priority");
  });
  it("shows Unknown for an absent currency and names actions per record", async () => {
    priceFixture({
      overrides: [
        { id: "a", providerId: "p1", modelId: "m1", serviceTier: "priority" },
        { id: "b", providerId: "p2", modelId: "m2", serviceTier: "" },
      ],
    });
    const first = (await screen.findByRole("heading", { name: "m1" })).closest(
      "article",
    ) as HTMLElement;
    expect(within(first).getByText("Currency: Unknown")).toBeTruthy();
    expect(within(first).getByText("Unknown per million tokens")).toBeTruthy();
    expect(
      within(first).getByRole("button", {
        name: "Edit override for p1 / m1 (Tier: priority)",
      }),
    ).toBeTruthy();
    expect(
      screen.getByRole("button", {
        name: "Delete override for p2 / m2 (All tiers (fallback))",
      }),
    ).toBeTruthy();
  });
  it("keeps Add available with the empty state when a revision is present", async () => {
    priceFixture({ overrides: [] });
    expect(
      await screen.findByText(
        "No price overrides. Bundled prices apply where available.",
      ),
    ).toBeTruthy();
    await waitFor(() =>
      expect(
        (
          screen.getByRole("button", {
            name: "Add price override",
          }) as HTMLButtonElement
        ).disabled,
      ).toBe(false),
    );
  });
  it("shows paging controls only when tokens exist and sends page size 100", async () => {
    const f = priceFixture({ pageConflict: true });
    const user = userEvent.setup();
    expect(screen.queryByRole("button", { name: "First page" })).toBeNull();
    await user.click(await screen.findByRole("button", { name: "Next page" }));
    expect(f.list.mock.calls[0][0].page).toMatchObject({ pageSize: 100 });
    expect(f.list.mock.lastCall?.[0].page).toMatchObject({
      pageSize: 100,
      pageToken: "next",
    });
  });
});

function pagedPriceFixture(
  pages: Record<
    string,
    { overrides: Record<string, unknown>[]; revision: string; next?: string }
  >,
) {
  const set = rs.fn(),
    list = rs.fn();
  mount(<Prices />, (router) =>
    router.service(OperatorService, {
      listPriceOverrides: (request) => {
        list(request);
        const page = pages[request.page?.pageToken ?? ""];
        if (!page) throw new ConnectError("Unavailable", Code.Unavailable);
        return {
          overrides: page.overrides,
          revision: page.revision,
          page: { nextPageToken: page.next ?? "" },
        };
      },
      setPriceOverride: (request) => {
        set(request);
        return { revision: "1" };
      },
    }),
  );
  return { set, list };
}
const identity = (modelId: string, serviceTier: string, rates = {}) => ({
  id: `${modelId}-${serviceTier}`,
  providerId: "example-ai",
  modelId,
  serviceTier,
  rates: { currency: "USD", ...rates },
});
describe("price editor structure", () => {
  it("groups identity and rates with helpers, keeps edit identity read-only and focuses Currency", async () => {
    pagedPriceFixture({
      "": {
        revision: "1",
        overrides: [identity("atlas-chat", "priority", { input: "1.25" })],
      },
    });
    const user = userEvent.setup();
    await user.click(
      await screen.findByRole("button", { name: /^Edit override for/ }),
    );
    const dialog = screen.getByRole("dialog", { name: "Edit price override" });
    expect(
      within(dialog).getByRole("group", { name: "Price identity" }),
    ).toBeTruthy();
    expect(
      within(dialog).getByRole("group", { name: "Token rates" }),
    ).toBeTruthy();
    for (const name of ["Provider ID", "Model ID", "Service tier"])
      expect(
        (within(dialog).getByRole("textbox", { name }) as HTMLInputElement)
          .readOnly,
      ).toBe(true);
    expect(
      within(dialog).getByRole("textbox", { name: "Service tier" }),
    ).toHaveProperty("value", "priority");
    expect(
      within(dialog).getByText(
        "Leave empty to apply when no tier-specific override matches.",
      ),
    ).toBeTruthy();
    expect(
      within(dialog).getByText("USD is currently supported."),
    ).toBeTruthy();
    expect(
      within(dialog).getByText(
        "Blank means unknown. Enter 0 only for a confirmed zero price.",
      ),
    ).toBeTruthy();
    expect(document.activeElement).toBe(
      within(dialog).getByRole("textbox", { name: "Currency" }),
    );
    expect(
      within(dialog).getByRole("button", { name: "Close dialog" }),
    ).toBeTruthy();
    expect(
      within(dialog).getByRole("button", { name: "Refresh prices for review" }),
    ).toBeTruthy();
  });
  it("focuses Provider ID on Add, marks no rate as required and submits exact optional decimals", async () => {
    const f = pagedPriceFixture({ "": { revision: "1", overrides: [] } });
    const user = userEvent.setup();
    await waitFor(() =>
      expect(
        (
          screen.getByRole("button", {
            name: "Add price override",
          }) as HTMLButtonElement
        ).disabled,
      ).toBe(false),
    );
    await user.click(
      screen.getByRole("button", { name: "Add price override" }),
    );
    const dialog = screen.getByRole("dialog", { name: "Add price override" });
    const field = (name: string) =>
      within(dialog).getByRole("textbox", { name }) as HTMLInputElement;
    expect(document.activeElement).toBe(field("Provider ID"));
    expect(field("Currency").value).toBe("USD");
    for (const name of ["Provider ID", "Model ID", "Currency"])
      expect(field(name).required).toBe(true);
    for (const name of [
      "Service tier",
      "Input",
      "Output",
      "Cache read",
      "Cache write",
      "Short cache write",
      "Long cache write",
    ])
      expect(field(name).required).toBe(false);
    await user.type(field("Provider ID"), "example-ai");
    await user.type(field("Model ID"), "atlas-chat");
    await user.clear(field("Currency"));
    await user.type(field("Currency"), "usd");
    expect(field("Currency").value).toBe("USD");
    await user.type(field("Input"), "1.25");
    await user.type(field("Output"), "5.00");
    await user.type(field("Cache read"), "0");
    await user.type(field("Short cache write"), "1.50");
    await user.type(field("Long cache write"), "2.00");
    await user.click(
      within(dialog).getByRole("button", { name: "Save override" }),
    );
    await waitFor(() => expect(f.set).toHaveBeenCalledTimes(1));
    const request = f.set.mock.lastCall?.[0];
    expect(request).toMatchObject({
      expectedRevision: "1",
      providerId: "example-ai",
      modelId: "atlas-chat",
      serviceTier: "",
      rates: {
        currency: "USD",
        input: "1.25",
        output: "5.00",
        cacheRead: "0",
        cacheWriteShort: "1.50",
        cacheWriteLong: "2.00",
      },
    });
    expect(request.rates.cacheWrite).toBeUndefined();
  });
});
describe("price conflict review panel", () => {
  it("shows the separate current saved price, announces adoption and moves focus to Save without submitting", async () => {
    const f = pagedPriceFixture({
      "": {
        revision: "1",
        overrides: [identity("atlas-chat", "", { input: "2", cacheRead: "0" })],
      },
    });
    const user = userEvent.setup();
    await user.click(
      await screen.findByRole("button", { name: /^Edit override for/ }),
    );
    const dialog = screen.getByRole("dialog");
    const input = within(dialog).getByRole("textbox", {
      name: "Input",
    }) as HTMLInputElement;
    await user.clear(input);
    await user.type(input, "9.5");
    await user.click(
      within(dialog).getByRole("button", { name: "Refresh prices for review" }),
    );
    const panel = await within(dialog).findByRole("region", {
      name: "Current price review",
    });
    expect(
      within(panel).getByRole("heading", { name: "Current saved price" }),
    ).toBeTruthy();
    expect(within(panel).getByText("Currency: USD")).toBeTruthy();
    expect(
      within(panel).getByText("example-ai / atlas-chat (All tiers (fallback))"),
    ).toBeTruthy();
    const values = [...panel.querySelectorAll("dd")].map(
      (dd) => dd.textContent,
    );
    expect(values).toEqual([
      "2",
      "Unknown",
      "0",
      "Unknown",
      "Unknown",
      "Unknown",
    ]);
    expect(
      within(panel).getByText(
        "Your entered rates have not changed. Saving replaces this current price for future requests only.",
      ),
    ).toBeTruthy();
    expect(
      within(dialog).getByRole("heading", { name: "Your entered rates" }),
    ).toBeTruthy();
    expect(input.value).toBe("9.5");
    expect(
      (
        within(dialog).getByRole("button", {
          name: "Save override",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
    await user.click(
      within(dialog).getByRole("button", { name: "Use reviewed revision" }),
    );
    const save = within(dialog).getByRole("button", { name: "Save override" });
    expect((save as HTMLButtonElement).disabled).toBe(false);
    expect(document.activeElement).toBe(save);
    expect(
      within(dialog).getByText(
        "Reviewed revision selected. Your entries are unchanged.",
      ),
    ).toBeTruthy();
    expect(input.value).toBe("9.5");
    expect(f.set).not.toHaveBeenCalled();
  });
  it("distinguishes empty, default and priority selectors when matching the review", async () => {
    pagedPriceFixture({
      "": {
        revision: "1",
        overrides: [
          identity("atlas-chat", "default", { input: "2.00" }),
          identity("atlas-chat", "priority", { input: "7.00" }),
        ],
      },
    });
    const user = userEvent.setup();
    await waitFor(() =>
      expect(
        (
          screen.getByRole("button", {
            name: "Add price override",
          }) as HTMLButtonElement
        ).disabled,
      ).toBe(false),
    );
    await user.click(
      screen.getByRole("button", { name: "Add price override" }),
    );
    const dialog = screen.getByRole("dialog");
    await user.type(
      within(dialog).getByRole("textbox", { name: "Provider ID" }),
      "example-ai",
    );
    await user.type(
      within(dialog).getByRole("textbox", { name: "Model ID" }),
      "atlas-chat",
    );
    await user.click(
      within(dialog).getByRole("button", { name: "Refresh prices for review" }),
    );
    expect(
      await within(dialog).findByText(
        "No override exists for this provider, model and tier.",
      ),
    ).toBeTruthy();
    // An identity change invalidates the displayed review.
    await user.type(
      within(dialog).getByRole("textbox", { name: "Service tier" }),
      "default",
    );
    expect(
      within(dialog).queryByRole("region", { name: "Current price review" }),
    ).toBeNull();
    await user.click(
      within(dialog).getByRole("button", { name: "Refresh prices for review" }),
    );
    const panel = await within(dialog).findByRole("region", {
      name: "Current price review",
    });
    expect(
      within(panel).getByText("example-ai / atlas-chat (Tier: default)"),
    ).toBeTruthy();
    expect(panel.querySelector("dd")?.textContent).toBe("2.00");
  });
  it("finds a later-page match with one stable revision across pages", async () => {
    const f = pagedPriceFixture({
      "": { revision: "5", overrides: [identity("other", "")], next: "t2" },
      t2: {
        revision: "5",
        overrides: [identity("atlas-chat", "", { input: "3" })],
      },
    });
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "Next page" }));
    await user.click(
      await screen.findByRole("button", {
        name: /^Edit override for example-ai \/ atlas-chat/,
      }),
    );
    const dialog = screen.getByRole("dialog");
    f.list.mockClear();
    await user.click(
      within(dialog).getByRole("button", { name: "Refresh prices for review" }),
    );
    const panel = await within(dialog).findByRole("region", {
      name: "Current price review",
    });
    expect(panel.querySelector("dd")?.textContent).toBe("3");
    // The scan always starts from the empty token with 100 records per page.
    expect(f.list.mock.calls.map((call) => call[0].page)).toMatchObject([
      { pageSize: 100, pageToken: "" },
      { pageSize: 100, pageToken: "t2" },
    ]);
  });
  it("rejects a snapshot whose revision changes across pages", async () => {
    pagedPriceFixture({
      "": { revision: "5", overrides: [identity("a", "")], next: "t2" },
      t2: { revision: "6", overrides: [identity("b", "")] },
    });
    const user = userEvent.setup();
    await waitFor(() =>
      expect(
        (
          screen.getByRole("button", {
            name: "Add price override",
          }) as HTMLButtonElement
        ).disabled,
      ).toBe(false),
    );
    await user.click(
      screen.getByRole("button", { name: "Add price override" }),
    );
    const dialog = screen.getByRole("dialog");
    await user.type(
      within(dialog).getByRole("textbox", { name: "Provider ID" }),
      "example-ai",
    );
    await user.type(
      within(dialog).getByRole("textbox", { name: "Model ID" }),
      "missing",
    );
    await user.click(
      within(dialog).getByRole("button", { name: "Refresh prices for review" }),
    );
    await waitFor(() =>
      expect(within(dialog).getAllByRole("alert").length).toBeGreaterThan(0),
    );
    expect(
      within(dialog).queryByRole("region", { name: "Current price review" }),
    ).toBeNull();
    expect(
      (
        within(dialog).getByRole("button", {
          name: "Refresh prices for review",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(false);
  });
  it("rejects a repeated page token", async () => {
    pagedPriceFixture({
      "": { revision: "5", overrides: [identity("a", "")], next: "loop" },
      loop: { revision: "5", overrides: [identity("b", "")], next: "loop" },
    });
    const user = userEvent.setup();
    await waitFor(() =>
      expect(
        (
          screen.getByRole("button", {
            name: "Add price override",
          }) as HTMLButtonElement
        ).disabled,
      ).toBe(false),
    );
    await user.click(
      screen.getByRole("button", { name: "Add price override" }),
    );
    const dialog = screen.getByRole("dialog");
    await user.type(
      within(dialog).getByRole("textbox", { name: "Provider ID" }),
      "example-ai",
    );
    await user.type(
      within(dialog).getByRole("textbox", { name: "Model ID" }),
      "missing",
    );
    await user.click(
      within(dialog).getByRole("button", { name: "Refresh prices for review" }),
    );
    await waitFor(() =>
      expect(within(dialog).getAllByRole("alert").length).toBeGreaterThan(0),
    );
    expect(
      within(dialog).queryByRole("region", { name: "Current price review" }),
    ).toBeNull();
  });
  it("shows the delete-specific review helper and focuses the delete action after adoption", async () => {
    const f = pagedPriceFixture({
      "": { revision: "1", overrides: [identity("atlas-chat", "priority")] },
    });
    const user = userEvent.setup();
    await user.click(
      await screen.findByRole("button", { name: /^Delete override for/ }),
    );
    const dialog = screen.getByRole("dialog");
    await user.type(within(dialog).getByRole("textbox"), "atlas-chat");
    await user.click(
      within(dialog).getByRole("button", { name: "Refresh prices for review" }),
    );
    const panel = await within(dialog).findByRole("region", {
      name: "Current price review",
    });
    expect(
      within(panel).getByText(
        "Review does not delete this override. Deletion affects future requests only.",
      ),
    ).toBeTruthy();
    expect(within(dialog).getByRole("textbox")).toHaveProperty(
      "value",
      "atlas-chat",
    );
    await user.click(
      within(dialog).getByRole("button", { name: "Use reviewed revision" }),
    );
    expect(document.activeElement).toBe(
      within(dialog).getByRole("button", { name: "Delete price override" }),
    );
    expect(f.set).not.toHaveBeenCalled();
  });
});
describe("price deletion dialog", () => {
  async function openDelete(overrides: Record<string, unknown>[]) {
    const f = priceFixture({ overrides });
    const user = userEvent.setup();
    await user.click(
      await screen.findByRole("button", { name: /^Delete override for/ }),
    );
    return { f, user, dialog: screen.getByRole("dialog") };
  }
  const definition = (dialog: HTMLElement, term: string) =>
    within(dialog)
      .getByText(term, { selector: "dt" })
      .parentElement?.querySelector("dd")?.textContent;

  it("identifies provider, model, tier and provenance and states the consequence", async () => {
    const { dialog } = await openDelete([
      identity("atlas-chat", "", { input: "1.25" }),
    ]);
    expect(
      within(dialog).getByRole("heading", { name: "Delete price override" }),
    ).toBeTruthy();
    expect(definition(dialog, "Provider")).toBe("example-ai");
    expect(definition(dialog, "Model")).toBe("atlas-chat");
    expect(definition(dialog, "Service tier")).toBe("All tiers (fallback)");
    expect(definition(dialog, "Provenance")).toBe("Instance operator override");
    expect(
      within(dialog).getByText(
        "Future requests use any remaining applicable override, then bundled pricing where available. Cost remains unknown when required usage or prices are unavailable. Historical estimates remain unchanged.",
      ),
    ).toBeTruthy();
  });
  it("keeps the literal default tier distinct from the empty tier", async () => {
    const { dialog } = await openDelete([identity("atlas-chat", "default")]);
    expect(definition(dialog, "Service tier")).toBe("Tier: default");
  });
  it("focuses the typed confirmation input on open", async () => {
    const { dialog } = await openDelete([identity("atlas-chat", "priority")]);
    expect(document.activeElement).toBe(
      within(dialog).getByLabelText("Type atlas-chat to confirm"),
    );
    expect(definition(dialog, "Service tier")).toBe("Tier: priority");
  });
  it("enables deletion only for the exact case-sensitive model name and sends only the override ID and revision", async () => {
    const { f, user, dialog } = await openDelete([
      identity("atlas-chat", "priority"),
    ]);
    const button = () =>
      within(dialog).getByRole("button", {
        name: "Delete price override",
      }) as HTMLButtonElement;
    const input = within(dialog).getByLabelText("Type atlas-chat to confirm");
    expect(button().disabled).toBe(true);
    for (const wrong of ["Atlas-Chat", "atlas", "atlas-chat ", " atlas-chat"]) {
      await user.clear(input);
      await user.type(input, wrong);
      expect(button().disabled).toBe(true);
    }
    await user.clear(input);
    await user.type(input, "atlas-chat");
    expect(button().disabled).toBe(false);
    await user.click(button());
    await waitFor(() => expect(f.remove).toHaveBeenCalledTimes(1));
    expect(f.remove.mock.lastCall?.[0]).toMatchObject({
      overrideId: "atlas-chat-priority",
      expectedRevision: "1",
    });
    expect(f.set).not.toHaveBeenCalled();
  });
  it("moves focus to the page heading and reports success after a confirmed deletion", async () => {
    const { user, dialog } = await openDelete([identity("atlas-chat", "")]);
    await user.type(
      within(dialog).getByLabelText("Type atlas-chat to confirm"),
      "atlas-chat",
    );
    await user.click(
      within(dialog).getByRole("button", { name: "Delete price override" }),
    );
    await screen.findByText("Price override deleted.");
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(document.activeElement).toBe(
      screen.getByRole("heading", { level: 1, name: "Price overrides" }),
    );
  });
  it("keeps the typed text and returns focus to the trigger when cancelled", async () => {
    const { f, user, dialog } = await openDelete([identity("atlas-chat", "")]);
    await user.type(
      within(dialog).getByLabelText("Type atlas-chat to confirm"),
      "atlas-chat",
    );
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(f.remove).not.toHaveBeenCalled();
    expect(document.activeElement).toBe(
      screen.getByRole("button", { name: /^Delete override for/ }),
    );
  });
});
