// SPDX-License-Identifier: Apache-2.0
import { afterEach, describe, expect, it, rs } from "@rstest/core";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {
  createRouterTransport,
  Code,
  ConnectError,
  type ConnectRouter,
} from "@connectrpc/connect";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { QueryClient } from "@tanstack/react-query";
import {
  AccountService,
  ErrorDetailSchema,
  RequestOutcome,
  Role,
  UsageService,
  type ListDailyUsageRequest,
  type ListUsageRequest,
} from "@tairouter/client";
import { ApiProvider } from "../api";
import { AdminAuthority, Pager, date } from "./common";
import { OrganizationUsage } from "./reporting";

const originalMatchMedia = window.matchMedia;
afterEach(() => {
  window.matchMedia = originalMatchMedia;
});
// jsdom has no layout, so viewport-dependent rendering is driven by a width mock.
function mockViewport(width: number) {
  window.matchMedia = ((query: string) => {
    const min = /min-width:\s*([\d.]+)px/.exec(query);
    const max = /max-width:\s*([\d.]+)px/.exec(query);
    return {
      matches:
        (!min || width >= Number(min[1])) && (!max || width <= Number(max[1])),
      media: query,
      addEventListener: () => {},
      removeEventListener: () => {},
    };
  }) as unknown as typeof window.matchMedia;
}
function mount(routes: (router: ConnectRouter) => void) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const transport = createRouterTransport((router) => {
    router.service(AccountService, {
      getSession: () => ({ isInstanceOperator: false }),
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
        <OrganizationUsage />
      </AdminAuthority.Provider>
    </ApiProvider>,
  );
  return client;
}
type Daily = Awaited<ReturnType<typeof dailyFixture>>;
function dailyFixture() {
  return Promise.resolve({
    days: [
      {
        date: "2026-10-26",
        userId: "member-a",
        modelId: "model-a",
        summary: {
          requestCount: 3n,
          knownCosts: [
            { currency: "USD", decimalAmount: "0.001200" },
            { currency: "EUR", decimalAmount: "0" },
          ],
          missingCostCount: 1n,
          missingUsageCount: 2n,
          missingPriceCount: 0n,
        },
      },
      // Absent member, model and counts keep their fallbacks; 10-25 is not synthesized.
      { date: "2026-10-24", modelId: "" },
    ],
    page: {},
  });
}
const occurredAt = timestampFromDate(new Date("2026-10-26T08:09:10Z"));
function requestFixture() {
  return {
    records: [
      {
        id: "record-1",
        modelId: "model-a",
        userId: "member-a",
        occurredAt,
        outcome: RequestOutcome.RATE_LIMITED,
        usage: { inputTokens: 12n, outputTokens: 0n },
        estimatedCost: { currency: "USD", decimalAmount: "0.5" },
        requestId: "request-1",
      },
      {
        id: "record-2",
        modelId: "model-b",
        outcome: RequestOutcome.UNSPECIFIED,
      },
    ],
    page: {},
  };
}
function routes(
  overrides: {
    list?: (request: ListUsageRequest) => object;
    daily?: (request: ListDailyUsageRequest) => object | Promise<object>;
  } = {},
) {
  return (router: ConnectRouter) => {
    router.service(UsageService, {
      listUsage: (request) =>
        overrides.list ? overrides.list(request) : requestFixture(),
      getUsageSummary: () => ({
        summary: { requestCount: 5n, knownCosts: [] },
      }),
      listDailyUsage: (request) =>
        overrides.daily ? overrides.daily(request) : dailyFixture(),
    });
  };
}
function unavailable(requestId: string) {
  return new ConnectError("raw detail", Code.Unavailable, undefined, [
    { desc: ErrorDetailSchema, value: { requestId } },
  ]);
}

describe("organization usage daily records", () => {
  it("renders a semantic table at wide widths with every returned row", async () => {
    mockViewport(1440);
    mount(routes());
    const table = await screen.findByRole("table", { name: "Daily usage" });
    expect(
      within(table)
        .getAllByRole("columnheader")
        .map((th) => th.textContent),
    ).toEqual([
      "Date (UTC)",
      "Member ID",
      "Model",
      "Requests",
      "Known estimated cost",
      "Coverage",
    ]);
    const rows = within(table).getAllByRole("row").slice(1);
    expect(rows).toHaveLength(2);
    const first = within(rows[0])
      .getAllByRole("cell")
      .map((td) => td.textContent);
    expect(first).toEqual([
      "2026-10-26",
      "member-a",
      "model-a",
      "3",
      "USD 0.001200 · EUR 0",
      "Unknown cost: 1 · Missing usage: 2 · Missing price: 0",
    ]);
    const second = within(rows[1])
      .getAllByRole("cell")
      .map((td) => td.textContent);
    expect(second).toEqual([
      "2026-10-24",
      "Deleted user",
      "All models",
      "Unknown",
      "Unknown",
      "Unknown cost: Unknown · Missing usage: Unknown · Missing price: Unknown",
    ]);
    expect(within(table).queryByText("2026-10-25")).toBeNull();
  });

  it("renders labeled cards on narrow viewports with the same values", async () => {
    mockViewport(390);
    mount(routes());
    const card = await screen.findByRole("article", {
      name: "2026-10-26 UTC · model-a",
    });
    expect(screen.queryByRole("table", { name: "Daily usage" })).toBeNull();
    const terms = within(card)
      .getAllByRole("term")
      .map((dt) => dt.textContent);
    expect(terms).toEqual([
      "Member ID",
      "Requests",
      "Known estimated cost",
      "Coverage",
    ]);
    expect(within(card).getByText("USD 0.001200 · EUR 0")).toBeTruthy();
    expect(
      screen.getByRole("article", { name: "2026-10-24 UTC · All models" }),
    ).toBeTruthy();
    expect(
      within(
        screen.getByRole("article", { name: "2026-10-24 UTC · All models" }),
      ).getByText("Deleted user"),
    ).toBeTruthy();
  });
});

describe("organization usage request records", () => {
  it("renders the request table with local time, outcomes and fallbacks", async () => {
    mockViewport(1440);
    mount(routes());
    const table = await screen.findByRole("table", {
      name: "Request metadata",
    });
    expect(
      within(table)
        .getAllByRole("columnheader")
        .map((th) => th.textContent),
    ).toEqual([
      "Model",
      "Time (local)",
      "Member ID",
      "Outcome",
      "Input / Output",
      "Estimated cost",
      "Request ID",
      "Details",
    ]);
    const rows = within(table).getAllByRole("row").slice(1);
    const first = within(rows[0])
      .getAllByRole("cell")
      .map((td) => td.textContent);
    expect(first.slice(0, 7)).toEqual([
      "model-a",
      date(occurredAt),
      "member-a",
      "Rate limited",
      "Input: 12 · Output: 0",
      "USD 0.5",
      "request-1",
    ]);
    const second = within(rows[1])
      .getAllByRole("cell")
      .map((td) => td.textContent);
    expect(second.slice(0, 7)).toEqual([
      "model-b",
      "Unknown",
      "Deleted user",
      "Unknown outcome",
      "Input: Unknown · Output: Unknown",
      "Unknown",
      "Unknown",
    ]);
    expect(
      within(table).getAllByRole("button", { name: "View estimate details" }),
    ).toHaveLength(2);
  });

  it("renders labeled request cards with a full-width estimate action", async () => {
    mockViewport(390);
    mount(routes());
    const card = await screen.findByRole("article", { name: "model-a" });
    expect(
      within(card)
        .getAllByRole("term")
        .map((dt) => dt.textContent),
    ).toEqual([
      "Member ID",
      "Outcome",
      "Input / Output",
      "Estimated cost",
      "Request ID",
    ]);
    expect(within(card).getByText(date(occurredAt))).toBeTruthy();
    const user = userEvent.setup();
    await user.click(
      within(card).getByRole("button", { name: "View estimate details" }),
    );
    // The detail dialog opens locally from the selected record.
    expect(
      await screen.findByRole("dialog", { name: "Request estimate" }),
    ).toBeTruthy();
  });
});

describe("organization usage pagination", () => {
  it("keeps two independent cursors with distinct navigation names", async () => {
    mockViewport(1440);
    const list = rs.fn<(request: ListUsageRequest) => void>();
    const daily = rs.fn<(request: ListDailyUsageRequest) => void>();
    mount(
      routes({
        list: (request) => {
          list(request);
          return {
            records: [],
            page: { nextPageToken: request.page?.pageToken ? "" : "r-next" },
          };
        },
        daily: (request) => {
          daily(request);
          return {
            days: [],
            page: { nextPageToken: request.page?.pageToken ? "" : "d-next" },
          };
        },
      }),
    );
    const user = userEvent.setup();
    const dailyNav = await screen.findByRole("navigation", {
      name: "Daily usage pages",
    });
    const requestNav = screen.getByRole("navigation", {
      name: "Request metadata pages",
    });
    expect(within(dailyNav).queryByRole("button", { name: "First page" })).toBe(
      null,
    );
    expect(within(requestNav).getAllByRole("button")).toHaveLength(1);
    await user.click(
      within(dailyNav).getByRole("button", { name: "Next daily usage" }),
    );
    await waitFor(() =>
      expect(daily.mock.lastCall?.[0].page?.pageToken).toBe("d-next"),
    );
    await user.click(
      within(requestNav).getByRole("button", { name: "Next requests" }),
    );
    await waitFor(() =>
      expect(list.mock.lastCall?.[0].page?.pageToken).toBe("r-next"),
    );
    // Last pages expose First page only, with no Next and no disabled buttons.
    const dailyNav2 = await screen.findByRole("navigation", {
      name: "Daily usage pages",
    });
    expect(
      within(dailyNav2).queryByRole("button", { name: "Next daily usage" }),
    ).toBeNull();
    await user.click(
      within(dailyNav2).getByRole("button", { name: "First page" }),
    );
    await waitFor(() =>
      expect(daily.mock.lastCall?.[0].page?.pageToken).toBe(""),
    );
    // Resetting the daily cursor leaves the request cursor on its token.
    expect(list.mock.lastCall?.[0].page?.pageToken).toBe("r-next");
    const requestNav2 = await screen.findByRole("navigation", {
      name: "Request metadata pages",
    });
    expect(
      within(requestNav2).getByRole("button", { name: "First page" }),
    ).toBeTruthy();
    for (const button of screen.getAllByRole("button"))
      expect((button as HTMLButtonElement).disabled).toBe(false);
  });

  it("keeps the default Pager output for other consumers", () => {
    render(<Pager token="t" next="n" onChange={() => {}} />);
    const nav = screen.getByRole("navigation", { name: "Record pages" });
    expect(
      within(nav)
        .getAllByRole("button")
        .map((b) => b.textContent),
    ).toEqual(["First page", "Next page"]);
  });
});

describe("organization usage record regions", () => {
  it("isolates a failed daily region and labels its retry", async () => {
    mockViewport(1440);
    let fail = true;
    mount(
      routes({
        daily: async () => {
          if (fail) throw unavailable("req-daily-9");
          return dailyFixture();
        },
      }),
    );
    const user = userEvent.setup();
    expect(await screen.findByText("Request ID: req-daily-9")).toBeTruthy();
    expect(screen.queryByText(/raw detail/)).toBeNull();
    // The requests region is not blocked and the daily region shows no zeros.
    expect(await screen.findByText("model-a")).toBeTruthy();
    expect(screen.queryByRole("table", { name: "Daily usage" })).toBeNull();
    fail = false;
    await user.click(
      screen.getByRole("button", { name: "Try again loading daily usage" }),
    );
    expect(
      await screen.findByRole("table", { name: "Daily usage" }),
    ).toBeTruthy();
  });

  it("isolates a failed request region and shows a loading status while pending", async () => {
    mockViewport(1440);
    let release: () => void = () => {};
    const gate = new Promise<void>((resolve) => (release = resolve));
    let fail = true;
    mount(
      routes({
        daily: async () => {
          await gate;
          return dailyFixture();
        },
        list: () => {
          if (fail) throw unavailable("req-list-3");
          return requestFixture();
        },
      }),
    );
    expect(await screen.findByText("Loading daily usage…")).toBeTruthy();
    expect(await screen.findByText("Request ID: req-list-3")).toBeTruthy();
    release();
    expect(
      await screen.findByRole("table", { name: "Daily usage" }),
    ).toBeTruthy();
    fail = false;
    const user = userEvent.setup();
    await user.click(
      screen.getByRole("button", { name: "Try again loading requests" }),
    );
    expect(
      await screen.findByRole("table", { name: "Request metadata" }),
    ).toBeTruthy();
  });

  it("labels refreshing data and marks it stale after a failed refetch", async () => {
    mockViewport(1440);
    let phase: "ok" | "slow" | "fail" = "ok";
    let release: () => void = () => {};
    const client = mount(
      routes({
        daily: async () => {
          if (phase === "slow")
            await new Promise<void>((resolve) => (release = resolve));
          if (phase === "fail")
            throw new ConnectError("down", Code.Unavailable);
          return dailyFixture();
        },
      }),
    );
    await screen.findByRole("table", { name: "Daily usage" });
    phase = "slow";
    void client.invalidateQueries();
    expect(await screen.findByText("Refreshing…")).toBeTruthy();
    expect(screen.getByRole("table", { name: "Daily usage" })).toBeTruthy();
    phase = "fail";
    release();
    expect(
      await screen.findByText(
        "Data may be out of date. Reconnect and try again.",
      ),
    ).toBeTruthy();
    expect(screen.getByRole("table", { name: "Daily usage" })).toBeTruthy();
  });
});

describe("organization usage empty states", () => {
  const empty = {
    list: () => ({ records: [], page: {} }),
    daily: () => ({ days: [], page: {} }),
  };
  function emptyRoutes(summary: object) {
    return (router: ConnectRouter) => {
      router.service(UsageService, {
        listUsage: empty.list,
        listDailyUsage: empty.daily,
        getUsageSummary: () => summary,
      });
    };
  }
  it("shows a neutral first-use message for a successful empty report", async () => {
    mockViewport(1440);
    mount(emptyRoutes({ summary: { requestCount: 0n, knownCosts: [] } }));
    const message = await screen.findByText(
      "No organization usage recorded for these dates.",
    );
    expect(message.closest("[role=alert]")).toBeNull();
    expect(screen.queryByRole("alert")).toBeNull();
    expect(screen.queryByText(/No requests match/)).toBeNull();
  });

  it("keeps an unavailable summary distinct from an empty report", async () => {
    mockViewport(1440);
    mount(emptyRoutes({}));
    expect(
      await screen.findByText("Usage summary is unavailable."),
    ).toBeTruthy();
    expect(
      screen.queryByText("No organization usage recorded for these dates."),
    ).toBeNull();
  });

  it("offers Clear filters from the filtered empty request state", async () => {
    mockViewport(1440);
    const list = rs.fn<(request: ListUsageRequest) => void>();
    mount((router) => {
      router.service(UsageService, {
        listUsage: (request) => {
          list(request);
          return { records: [], page: {} };
        },
        listDailyUsage: empty.daily,
        getUsageSummary: () => ({
          summary: { requestCount: 0n, knownCosts: [] },
        }),
      });
    });
    const user = userEvent.setup();
    await screen.findByText("No organization usage recorded for these dates.");
    // Without active filters the filter-specific empty copy is not shown.
    expect(screen.queryByText(/No requests match/)).toBeNull();
    await user.type(screen.getByRole("textbox", { name: "Model" }), "model-x");
    expect(
      await screen.findByText(/No requests match these filters/),
    ).toBeTruthy();
    expect(
      screen.getByText("No daily usage matches these filters."),
    ).toBeTruthy();
    const section = screen
      .getByRole("heading", { name: "Request metadata" })
      .closest("section")!;
    await user.click(
      within(section).getByRole("button", { name: "Clear filters" }),
    );
    await waitFor(() =>
      expect(list.mock.lastCall?.[0].modelId).toBeUndefined(),
    );
    expect(
      (screen.getByRole("textbox", { name: "Model" }) as HTMLInputElement)
        .value,
    ).toBe("");
  });
});
