// SPDX-License-Identifier: Apache-2.0
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, it, expect, rs } from "@rstest/core";
import {
  createRouterTransport,
  Code,
  ConnectError,
  type ConnectRouter,
} from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { QueryClient } from "@tanstack/react-query";
import {
  ProviderService,
  RoutingService,
  KeyService,
  UsageService,
  OrganizationSchema,
  GetSessionResponseSchema,
  Role,
  KeyState,
  KeyKind,
  ConnectionState,
} from "@tairouter/client";
import { ApiProvider } from "../src/api";
import { OverviewPage } from "../src/overview";

const orgA = create(OrganizationSchema, {
  id: "org-a",
  name: "Acme Studio",
  role: Role.MEMBER,
});
const orgB = create(OrganizationSchema, {
  id: "org-b",
  name: "Beta Lab",
  role: Role.MEMBER,
});
const session = create(GetSessionResponseSchema, {
  userId: "person",
  displayName: "Alex",
});

type Handlers = {
  providers?: (request: { organizationId: string }) => unknown;
  routes?: (request: { organizationId: string }) => unknown;
  keys?: (request: { organizationId: string }) => unknown;
  summary?: (request: unknown) => unknown;
  daily?: (request: unknown) => unknown;
};
const never = () => new Promise<never>(() => undefined);

/** Builds a router with one mock per Overview query; omitted ones are empty. */
function overviewRoutes(h: Handlers = {}) {
  return (r: ConnectRouter) => {
    r.service(ProviderService, {
      listProviderConnections: (h.providers ??
        (() => ({ connections: [] }))) as never,
    });
    r.service(RoutingService, {
      listRoutes: (h.routes ?? (() => ({ routes: [] }))) as never,
    });
    r.service(KeyService, {
      listKeys: (h.keys ?? (() => ({ keys: [] }))) as never,
    });
    r.service(UsageService, {
      getUsageSummary: (h.summary ?? (() => ({}))) as never,
      listDailyUsage: (h.daily ?? (() => ({ days: [] }))) as never,
    });
  };
}
function mount(h: Handlers = {}, organization = orgA) {
  const cache = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const transport = createRouterTransport(overviewRoutes(h));
  const ui = (org: typeof orgA) => (
    <ApiProvider queryClient={cache} rpcTransport={transport}>
      <OverviewPage organization={org} session={session} />
    </ApiProvider>
  );
  const view = render(ui(organization));
  return {
    ...view,
    cache,
    rerenderFor: (org: typeof orgA) => view.rerender(ui(org)),
  };
}
const fail =
  (message = "boom") =>
  () => {
    throw new ConnectError(message, Code.Unavailable);
  };
const checklist = () =>
  within(
    screen.getByRole("heading", { name: "Get connected" }).closest("section")!,
  );
const headerAction = () =>
  within(screen.getByRole("banner")).getByRole("link", {
    name: "Create API key",
  });
const step = (label: string) => {
  const link = checklist().getByRole("link", { name: label });
  return { link, item: link.closest("li") as HTMLElement };
};
const stepStatusText = (label: string) =>
  within(step(label).item).getByText(
    /^(Complete|Not yet complete|Checking…|Status unavailable)$/,
  ).textContent;
const connection = (id: string, state: ConnectionState, name = id) => ({
  id,
  providerId: "openai",
  name,
  state,
});
const route = (id: string, enabled: boolean, targetCount: number) => ({
  id,
  modelId: id,
  enabled,
  targets: Array.from({ length: targetCount }, (_, i) => ({
    connectionId: `c${i}`,
    upstreamModel: "m",
  })),
});
const key = (id: string, state: KeyState, kind: KeyKind, used = false) => ({
  id,
  name: id,
  state,
  kind,
  lastUsedAt: used
    ? timestampFromDate(new Date("2026-10-26T00:00:00Z"))
    : undefined,
});

describe("Overview composition", () => {
  it("orders header, note, usage, activity beside connections, then checklist and routing", async () => {
    const { container } = mount();
    expect(
      screen.getByRole("heading", { level: 1, name: "Your workspace" }),
    ).toBeTruthy();
    expect(screen.getByText("Your settings in Acme Studio.")).toBeTruthy();
    expect(headerAction().getAttribute("href")).toBe(
      "/organizations/org-a/keys",
    );
    expect(screen.getByText("Private to you")).toBeTruthy();
    await screen.findByText("No personal connections yet.");
    const order = [
      "Your workspace",
      "Last 7 days",
      "Request activity",
      "Personal connections",
      "Get connected",
      "Your routing",
    ].map((name) => {
      const heading = screen.getByRole("heading", { name });
      return { name, heading };
    });
    for (let i = 1; i < order.length; i++) {
      const before = order[i - 1]!.heading;
      const after = order[i]!.heading;
      expect(
        before.compareDocumentPosition(after) &
          Node.DOCUMENT_POSITION_FOLLOWING,
      ).toBeTruthy();
    }
    const primary = container.querySelector(".overview-primary")!;
    expect(
      within(primary as HTMLElement).getByRole("heading", {
        name: "Request activity",
      }),
    ).toBeTruthy();
    expect(
      within(primary as HTMLElement).getByRole("heading", {
        name: "Personal connections",
      }),
    ).toBeTruthy();
    const secondary = container.querySelector(".overview-secondary")!;
    expect(
      within(secondary as HTMLElement).getByRole("heading", {
        name: "Get connected",
      }),
    ).toBeTruthy();
    expect(
      within(secondary as HTMLElement).getByRole("heading", {
        name: "Your routing",
      }),
    ).toBeTruthy();
  });
});

describe("Overview checklist predicates", () => {
  it("applies the exact predicates and keeps completed steps navigable and scoped", async () => {
    mount({
      providers: () => ({
        connections: [
          connection("c1", ConnectionState.READY, "Work account"),
          connection("c2", ConnectionState.AUTHORIZATION_REQUIRED, "Backup"),
        ],
      }),
      routes: () => ({
        routes: [route("chat-primary", true, 2), route("chat-off", false, 1)],
      }),
      keys: () => ({
        keys: [
          key("ordinary", KeyState.ACTIVE, KeyKind.PERSONAL),
          key("cli", KeyState.ACTIVE, KeyKind.CLI, true),
        ],
      }),
    });
    await waitFor(() =>
      expect(stepStatusText("Create API key")).toBe("Complete"),
    );
    for (const label of [
      "Choose organization",
      "Connect personal providers",
      "Configure models/routing",
      "Create API key",
      "Connect a client",
    ])
      expect(stepStatusText(label)).toBe("Complete");
    const hrefs = [
      ["Choose organization", "/organizations/org-a/overview"],
      ["Connect personal providers", "/organizations/org-a/providers"],
      ["Configure models/routing", "/organizations/org-a/routing"],
      ["Create API key", "/organizations/org-a/keys"],
      ["Connect a client", "/organizations/org-a/connect"],
    ];
    for (const [label, href] of hrefs)
      expect(step(label!).link.getAttribute("href")).toBe(href);
  });

  it("does not complete steps from non-READY connections, disabled or target-less routes, or unused CLI keys", async () => {
    mount({
      providers: () => ({
        connections: [
          connection("c1", ConnectionState.AUTHORIZATION_REQUIRED),
          connection("c2", ConnectionState.ERROR),
        ],
      }),
      routes: () => ({
        routes: [route("disabled", false, 2), route("empty", true, 0)],
      }),
      keys: () => ({
        keys: [
          key("revoked", KeyState.REVOKED, KeyKind.PERSONAL),
          key("cli-new", KeyState.ACTIVE, KeyKind.CLI, false),
          key("api-used", KeyState.ACTIVE, KeyKind.PERSONAL, true),
        ],
      }),
    });
    await waitFor(() => {
      expect(stepStatusText("Connect personal providers")).toBe(
        "Not yet complete",
      );
      expect(stepStatusText("Configure models/routing")).toBe(
        "Not yet complete",
      );
      expect(stepStatusText("Create API key")).toBe("Complete");
    });
    expect(stepStatusText("Choose organization")).toBe("Complete");
    expect(stepStatusText("Configure models/routing")).toBe("Not yet complete");
    expect(stepStatusText("Create API key")).toBe("Complete");
    expect(stepStatusText("Connect a client")).toBe("Not yet complete");
  });

  it("completes Connect a client for an inactive CLI key that was used", async () => {
    mount({
      keys: () => ({
        keys: [key("old-cli", KeyState.REVOKED, KeyKind.CLI, true)],
      }),
    });
    await waitFor(() =>
      expect(stepStatusText("Connect a client")).toBe("Complete"),
    );
    expect(stepStatusText("Create API key")).toBe("Not yet complete");
  });

  it("never settles steps while queries are pending or failed", async () => {
    mount({
      providers: never,
      routes: fail("routes down"),
      keys: never,
    });
    await screen.findByText("Could not load routes.");
    expect(stepStatusText("Choose organization")).toBe("Complete");
    expect(stepStatusText("Connect personal providers")).toBe("Checking…");
    expect(stepStatusText("Configure models/routing")).toBe(
      "Status unavailable",
    );
    expect(stepStatusText("Create API key")).toBe("Checking…");
    expect(stepStatusText("Connect a client")).toBe("Checking…");
  });
});

describe("Overview usage summary", () => {
  it("keeps the rolling seven-day RPC inputs and shows UTC, currencies and coverage", async () => {
    const summary = rs.fn((_request: unknown) => ({
      summary: {
        requestCount: 1284n,
        knownCosts: [
          { currency: "USD", decimalAmount: "4.82" },
          { currency: "EUR", decimalAmount: "0" },
        ],
        missingCostCount: 3n,
        missingUsageCount: 2n,
        missingPriceCount: 1n,
        knownUsage: { inputTokens: 428500n, outputTokens: 96200n },
      },
    }));
    const daily = rs.fn((_request: unknown) => ({ days: [] }));
    const before = Date.now();
    mount({ summary, daily });
    await screen.findByText("1,284");
    expect(screen.getByText(/Times shown in UTC/)).toBeTruthy();
    expect(screen.getByText("USD 4.82")).toBeTruthy();
    expect(screen.getByText("EUR 0")).toBeTruthy();
    expect(screen.getByText("3 requests with unknown cost")).toBeTruthy();
    expect(screen.getByText("2 missing usage · 1 missing prices")).toBeTruthy();
    expect(screen.getByText(/428,500 in \/\s+96,200 out/)).toBeTruthy();
    const request = summary.mock.calls[0]![0] as {
      organizationId: string;
      userId: string;
      timeRange: { start: { seconds: bigint }; end: { seconds: bigint } };
    };
    expect(request.organizationId).toBe("org-a");
    expect(request.userId).toBe("person");
    const span = Number(
      request.timeRange.end.seconds - request.timeRange.start.seconds,
    );
    expect(span).toBe(7 * 86400);
    expect(Number(request.timeRange.end.seconds) * 1000).toBeGreaterThanOrEqual(
      Math.floor(before / 1000) * 1000,
    );
    const dailyRequest = daily.mock.calls[0]![0] as {
      organizationId: string;
      userId: string;
      timeRange: unknown;
    };
    expect(dailyRequest.organizationId).toBe("org-a");
    expect(dailyRequest.timeRange).toEqual(request.timeRange);
  });

  it("keeps Unknown distinct from a known zero when the summary is missing or zero", async () => {
    const { unmount } = mount({ summary: () => ({}) });
    await screen.findByText("Unknown cost coverage");
    expect(screen.getAllByText("Unknown").length).toBeGreaterThan(0);
    unmount();
    mount({
      summary: () => ({
        summary: {
          requestCount: 0n,
          knownCosts: [{ currency: "USD", decimalAmount: "0" }],
        },
      }),
    });
    await screen.findByText("USD 0");
    expect(screen.getByText("0 requests with unknown cost")).toBeTruthy();
  });
});

describe("Overview request activity", () => {
  const days = [140, 180, 160, 220, 190, 210, 184].map((n, i) => ({
    date: `2026-10-${21 + i}`,
    modelId: "",
    summary: { requestCount: BigInt(n) },
  }));

  it("shows daily counts in an accessible table, paginates and links to scoped Usage", async () => {
    const user = userEvent.setup();
    const daily = rs.fn((request: unknown) =>
      (request as { page?: { pageToken?: string } }).page?.pageToken
        ? { days: [] }
        : { days, page: { nextPageToken: "page-2" } },
    );
    mount({ daily });
    const table = await screen.findByRole("table", { hidden: true });
    const counts = within(table)
      .getAllByRole("row", { hidden: true })
      .slice(1)
      .map(
        (row) =>
          within(row).getAllByRole("cell", { hidden: true })[1]!.textContent,
      );
    expect(counts).toEqual(["140", "180", "160", "220", "190", "210", "184"]);
    expect(
      screen.getByRole("link", { name: "View usage" }).getAttribute("href"),
    ).toBe("/organizations/org-a/usage");
    const group = screen.getByRole("group", { name: "Daily usage pages" });
    await user.click(within(group).getByRole("button", { name: "Next page" }));
    await waitFor(() => expect(daily).toHaveBeenCalledTimes(2));
    expect(
      (daily.mock.calls[1]![0] as { page: { pageToken: string } }).page
        .pageToken,
    ).toBe("page-2");
  });

  it("does not synthesize zero days for absent dates and keeps unknown days Unknown", async () => {
    mount({
      daily: () => ({
        days: [
          { date: "2026-10-21", summary: { requestCount: 5n } },
          { date: "2026-10-24", summary: undefined },
        ],
      }),
    });
    const table = await screen.findByRole("table", { hidden: true });
    const rows = within(table).getAllByRole("row", { hidden: true }).slice(1);
    expect(rows).toHaveLength(2);
    expect(
      within(rows[1]!).getAllByRole("cell", { hidden: true })[1]!.textContent,
    ).toBe("Unknown");
  });

  it("points an empty activity region at connecting a client", async () => {
    mount();
    await screen.findByText(/No daily records available for this range\./);
    const activity = screen
      .getByRole("heading", { name: "Request activity" })
      .closest("section")!;
    expect(
      within(activity)
        .getByRole("link", { name: "Connect a client" })
        .getAttribute("href"),
    ).toBe("/organizations/org-a/connect");
  });
});

describe("Overview personal connections and routing", () => {
  it("shows at most four connections with identity, status text and scoped links", async () => {
    mount({
      providers: () => ({
        connections: [
          connection("c1", ConnectionState.READY, "Work account"),
          connection(
            "c2",
            ConnectionState.AUTHORIZATION_REQUIRED,
            "Backup account",
          ),
          connection("c3", ConnectionState.COOLDOWN, "Third"),
          connection("c4", ConnectionState.ERROR, "Fourth"),
          connection("c5", ConnectionState.READY, "Fifth"),
        ],
      }),
    });
    const card = (
      await screen.findByRole("heading", { name: "Personal connections" })
    ).closest("section")!;
    await within(card).findByText("Work account");
    expect(within(card).getAllByRole("heading", { level: 3 })).toHaveLength(4);
    expect(within(card).queryByText("Fifth")).toBeNull();
    expect(within(card).getByText("Connected")).toBeTruthy();
    expect(within(card).getByText("Reconnect required")).toBeTruthy();
    expect(within(card).getByText("Cooling down")).toBeTruthy();
    expect(within(card).getByText("Connection failed")).toBeTruthy();
    expect(within(card).queryByText(/verified/i)).toBeNull();
    for (const link of within(card).getAllByRole("link"))
      expect(link.getAttribute("href")).toBe("/organizations/org-a/providers");
    expect(
      within(card).getByRole("link", {
        name: "Manage connection: Work account",
      }),
    ).toBeTruthy();
    expect(
      within(card).getByRole("link", { name: "Manage providers" }),
    ).toBeTruthy();
  });

  it("shows at most four routes with target counts, health qualification and View all", async () => {
    mount({
      routes: () => ({
        routes: [
          route("r1", true, 2),
          route("r2", true, 1),
          route("r3", false, 1),
          route("r4", true, 3),
          route("r5", true, 1),
        ],
      }),
    });
    const card = (
      await screen.findByRole("heading", { name: "Your routing" })
    ).closest("section")!;
    await within(card).findByText("r1");
    expect(within(card).getAllByRole("heading", { level: 3 })).toHaveLength(4);
    expect(within(card).queryByText("r5")).toBeNull();
    expect(within(card).getByText("2 targets")).toBeTruthy();
    expect(within(card).getAllByText("1 target")).toHaveLength(2);
    expect(
      within(card).getAllByText("Enabled · check connection health"),
    ).toHaveLength(3);
    expect(within(card).getByText("Disabled")).toBeTruthy();
    expect(
      within(card).getByRole("link", { name: "View all" }).getAttribute("href"),
    ).toBe("/organizations/org-a/routing");
  });
});

describe("Overview independent region states", () => {
  it("shows a labeled skeleton per pending region without blocking others", async () => {
    mount({
      providers: never,
      routes: never,
      keys: never,
      summary: never,
      daily: never,
    });
    expect(screen.getByText("Loading personal connections…")).toBeTruthy();
    expect(screen.getByText("Loading routes…")).toBeTruthy();
    expect(screen.getByText("Checking API keys…")).toBeTruthy();
    expect(screen.getByText("Loading usage summary…")).toBeTruthy();
    expect(screen.getByText("Loading daily usage…")).toBeTruthy();
    expect(
      screen.getByRole("heading", { name: "Your workspace" }),
    ).toBeTruthy();
  });

  it("shows one labeled error with Retry per failed region and retries only that query", async () => {
    const user = userEvent.setup();
    let providerCalls = 0;
    const routes = rs.fn(fail("routes unavailable"));
    mount({
      providers: () => {
        providerCalls += 1;
        if (providerCalls === 1)
          throw new ConnectError("providers unavailable", Code.Unavailable);
        return {
          connections: [connection("c1", ConnectionState.READY, "Recovered")],
        };
      },
      routes,
      keys: fail("keys unavailable"),
      summary: fail("summary unavailable"),
      daily: fail("daily unavailable"),
    });
    for (const label of [
      "Could not load personal connections.",
      "Could not load routes.",
      "Could not load API keys.",
      "Could not load usage summary.",
    ])
      await screen.findByText(label);
    expect(
      screen.getAllByRole("button", { name: "Try again" }).length,
    ).toBeGreaterThanOrEqual(5);
    const routeCalls = routes.mock.calls.length;
    const connections = screen
      .getByRole("heading", { name: "Personal connections" })
      .closest("section")!;
    await user.click(
      within(connections).getByRole("button", { name: "Try again" }),
    );
    await screen.findByText("Recovered");
    expect(routes.mock.calls.length).toBe(routeCalls);
    expect(screen.getByText("Could not load routes.")).toBeTruthy();
  });

  it("points first-use regions at existing setup actions without fabricated records", async () => {
    mount();
    await screen.findByText("No personal connections yet.");
    await screen.findByText(/No model routes yet\./);
    expect(
      screen
        .getByRole("link", { name: "Connect a provider" })
        .getAttribute("href"),
    ).toBe("/organizations/org-a/providers");
    expect(
      screen
        .getByRole("link", { name: "Configure routing" })
        .getAttribute("href"),
    ).toBe("/organizations/org-a/routing");
    expect(stepStatusText("Connect personal providers")).toBe(
      "Not yet complete",
    );
    expect(stepStatusText("Create API key")).toBe("Not yet complete");
  });
});

describe("Overview scope changes", () => {
  it("drops organization A content immediately and requests organization B scoped data", async () => {
    const providers = rs.fn((request: { organizationId: string }) => {
      if (request.organizationId === "org-a")
        return {
          connections: [
            connection("a1", ConnectionState.READY, "A private account"),
          ],
        };
      return never();
    });
    const { rerenderFor } = mount({ providers });
    await screen.findByText("A private account");
    rerenderFor(orgB);
    expect(screen.queryByText("A private account")).toBeNull();
    expect(screen.getByText("Your settings in Beta Lab.")).toBeTruthy();
    expect(screen.getByText("Loading personal connections…")).toBeTruthy();
    expect(stepStatusText("Connect personal providers")).toBe("Checking…");
    expect(headerAction().getAttribute("href")).toBe(
      "/organizations/org-b/keys",
    );
    expect(step("Choose organization").link.getAttribute("href")).toBe(
      "/organizations/org-b/overview",
    );
    await waitFor(() =>
      expect(
        providers.mock.calls.some(
          (call) =>
            (call[0] as { organizationId: string }).organizationId === "org-b",
        ),
      ).toBe(true),
    );
  });
});
