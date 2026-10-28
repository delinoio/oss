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
import { QueryClient } from "@tanstack/react-query";
import {
  AccountService,
  ErrorDetailSchema,
  RequestOutcome,
  Role,
  UsageService,
  type GetUsageSummaryRequest,
  type ListDailyUsageRequest,
  type ListUsageRequest,
} from "@tairouter/client";
import { ApiProvider } from "../api";
import { AdminAuthority } from "./common";
import { OrganizationUsage } from "./reporting";

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
function spies() {
  return {
    list: rs.fn<(request: ListUsageRequest) => void>(),
    summary: rs.fn<(request: GetUsageSummaryRequest) => void>(),
    daily: rs.fn<(request: ListDailyUsageRequest) => void>(),
  };
}
type Spies = ReturnType<typeof spies>;
type SummaryResult = () => object;
function usageRoutes(calls: Spies, summary: SummaryResult = () => ({})) {
  return (router: ConnectRouter) => {
    router.service(UsageService, {
      listUsage: (request) => {
        calls.list(request);
        return { records: [], page: {} };
      },
      getUsageSummary: (request) => {
        calls.summary(request);
        return summary();
      },
      listDailyUsage: (request) => {
        calls.daily(request);
        return { days: [], page: {} };
      },
    });
  };
}
function dateInputs() {
  return [
    screen.getByLabelText("From (UTC)") as HTMLInputElement,
    screen.getByLabelText("Through (UTC)") as HTMLInputElement,
  ];
}

afterEach(() => {
  rs.useRealTimers();
});

describe("organization usage header and filters", () => {
  it("renders the header, range label and sections in reading order", async () => {
    rs.useFakeTimers({ toFake: ["Date"] });
    rs.setSystemTime(new Date("2026-10-27T00:30:00Z"));
    const calls = spies();
    mount(usageRoutes(calls));
    expect(
      screen.getByRole("heading", { level: 1, name: "Organization usage" }),
    ).toBeTruthy();
    expect(
      screen.getByText(
        "Per-member request metadata. No credentials, prompts or response bodies.",
      ),
    ).toBeTruthy();
    expect(screen.getByText("2026-10-20–2026-10-27 · UTC dates")).toBeTruthy();
    const [from, through] = dateInputs();
    expect(from.value).toBe("2026-10-20");
    expect(through.value).toBe("2026-10-27");
    await screen.findByRole("heading", { name: "Request metadata" });
    const order = ["Usage summary", "Daily usage", "Request metadata"].map(
      (name) => screen.getByRole("heading", { level: 2, name }),
    );
    const filters = screen.getByText("Filters");
    for (const heading of order)
      expect(
        filters.compareDocumentPosition(heading) &
          Node.DOCUMENT_POSITION_FOLLOWING,
      ).toBeTruthy();
    for (let i = 1; i < order.length; i++)
      expect(
        order[i - 1].compareDocumentPosition(order[i]) &
          Node.DOCUMENT_POSITION_FOLLOWING,
      ).toBeTruthy();
    // Every RPC receives the same UTC endpoints; no local date drift.
    for (const call of Object.values(calls)) {
      const range = call.mock.lastCall?.[0].timeRange;
      expect(
        range?.start && new Date(Number(range.start.seconds) * 1000),
      ).toEqual(new Date("2026-10-20T00:00:00.000Z"));
      expect(range?.end && new Date(Number(range.end.seconds) * 1000)).toEqual(
        new Date("2026-10-27T23:59:59Z"),
      );
      expect(range?.end?.nanos).toBe(999000000);
    }
    expect(screen.queryByText(/Last 7 days/)).toBeNull();
    expect(screen.queryByRole("button", { name: /apply/i })).toBeNull();
  });

  it("uses expanded free-text filters with Outcome as the only select", async () => {
    mount(usageRoutes(spies()));
    const details = screen.getByText("Filters").closest("details")!;
    expect(details.open).toBe(true);
    expect(
      (screen.getByRole("textbox", { name: "Member ID" }) as HTMLInputElement)
        .placeholder,
    ).toBe("All members");
    expect(
      (screen.getByRole("textbox", { name: "Model" }) as HTMLInputElement)
        .placeholder,
    ).toBe("All models");
    expect(
      (screen.getByRole("textbox", { name: "Provider" }) as HTMLInputElement)
        .placeholder,
    ).toBe("All providers");
    expect(screen.getAllByRole("combobox")).toHaveLength(1);
    expect(screen.getByRole("combobox", { name: "Outcome" })).toBeTruthy();
    const fieldLabels = within(details)
      .getAllByRole("textbox")
      .map((node) => node.getAttribute("placeholder"));
    expect(fieldLabels).toEqual(["All members", "All models", "All providers"]);
    // Clear filters comes after every field.
    const clear = within(details).getByRole("button", {
      name: "Clear filters",
    });
    expect(
      screen
        .getByRole("combobox", { name: "Outcome" })
        .compareDocumentPosition(clear) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    await screen.findByRole("heading", { name: "Request metadata" });
  });

  it("sends raw member IDs and explicit Unknown outcomes on a shared filter set", async () => {
    const calls = spies();
    mount(usageRoutes(calls));
    const user = userEvent.setup();
    await screen.findByRole("heading", { name: "Request metadata" });
    await user.type(
      screen.getByRole("textbox", { name: "Member ID" }),
      " Member-A ",
    );
    await user.selectOptions(
      screen.getByRole("combobox", { name: "Outcome" }),
      String(RequestOutcome.UNSPECIFIED),
    );
    await waitFor(() => {
      for (const call of Object.values(calls))
        expect(call.mock.lastCall?.[0]).toMatchObject({
          organizationId: "organization-a",
          userId: " Member-A ",
          outcome: RequestOutcome.UNSPECIFIED,
        });
    });
    for (const call of Object.values(calls)) {
      const last = call.mock.lastCall?.[0];
      expect(last?.modelId).toBeUndefined();
      expect(last?.providerId).toBeUndefined();
    }
    await user.selectOptions(
      screen.getByRole("combobox", { name: "Outcome" }),
      "",
    );
    await waitFor(() => {
      for (const call of Object.values(calls))
        expect(call.mock.lastCall?.[0].outcome).toBeUndefined();
    });
  });

  it("stops every request for invalid dates, announces the error and recovers", async () => {
    const calls = spies();
    mount(usageRoutes(calls));
    const user = userEvent.setup();
    await screen.findByRole("heading", { name: "Request metadata" });
    const before = Object.values(calls).map((call) => call.mock.calls.length);
    const [, through] = dateInputs();
    await user.clear(through);
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toBe(
      "Choose an end date on or after the start date.",
    );
    for (const input of dateInputs()) {
      expect(input.getAttribute("aria-invalid")).toBe("true");
      expect(input.getAttribute("aria-describedby")).toBe(alert.id);
    }
    expect(screen.queryByRole("heading", { name: "Usage summary" })).toBeNull();
    expect(screen.queryByRole("heading", { name: "Daily usage" })).toBeNull();
    expect(
      screen.queryByRole("heading", { name: "Request metadata" }),
    ).toBeNull();
    expect(Object.values(calls).map((call) => call.mock.calls.length)).toEqual(
      before,
    );
    // Reversed ranges are invalid too, and Clear filters stays available.
    await user.type(through, "2000-01-01");
    expect(screen.getByRole("alert")).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Clear filters" }));
    expect(screen.queryByRole("alert")).toBeNull();
    await screen.findByRole("heading", { name: "Usage summary" });
    expect(
      screen.getByRole("heading", { name: "Request metadata" }),
    ).toBeTruthy();
    for (const input of dateInputs())
      expect(input.getAttribute("aria-invalid")).toBeNull();
  });
});

describe("organization usage summary", () => {
  it("shows separate currencies, zero values and every coverage count", async () => {
    mount(
      usageRoutes(spies(), () => ({
        summary: {
          requestCount: 128n,
          knownCosts: [
            { currency: "USD", decimalAmount: "4.82" },
            { currency: "EUR", decimalAmount: "0.30" },
          ],
          knownUsage: { inputTokens: 428500n, outputTokens: 0n },
          missingCostCount: 3n,
          missingUsageCount: 2n,
          missingPriceCount: 1n,
          failedRequestCount: 5n,
        },
      })),
    );
    const region = (
      await screen.findByRole("heading", { name: "Usage summary" })
    ).closest("section")!;
    await within(region).findByText("USD 4.82 · EUR 0.30");
    expect(within(region).getByText("128")).toBeTruthy();
    expect(within(region).getByText("428,500")).toBeTruthy();
    // A confirmed zero stays zero rather than Unknown.
    expect(
      within(region).getByText("Confirmed output tokens").nextElementSibling
        ?.textContent,
    ).toBe("0");
    expect(
      within(region).getByText(
        "3 requests with unknown cost · 2 missing usage · 1 missing price",
      ),
    ).toBeTruthy();
    expect(
      within(region).getByText(
        "Failed requests: 5. Recorded metadata may be incomplete if usage recording failed.",
      ),
    ).toBeTruthy();
    expect(
      within(region).getByText(
        "Estimates, not billing. Currency is shown with each known cost. Price changes apply to new requests only.",
      ),
    ).toBeTruthy();
  });

  it("shows Unknown for absent optionals and an unavailable summary distinctly", async () => {
    mount(
      usageRoutes(spies(), () => ({
        summary: { requestCount: 0n, knownCosts: [] },
      })),
    );
    const region = (
      await screen.findByRole("heading", { name: "Usage summary" })
    ).closest("section")!;
    await waitFor(() =>
      expect(within(region).getAllByText("Unknown")).toHaveLength(3),
    );
    expect(
      within(region).getByText("Requests").nextElementSibling?.textContent,
    ).toBe("0");
  });

  it("shows Usage summary is unavailable when the response has no summary", async () => {
    mount(usageRoutes(spies()));
    expect(
      await screen.findByText("Usage summary is unavailable."),
    ).toBeTruthy();
    expect(
      screen.queryByText("No organization usage recorded for these dates."),
    ).toBeNull();
  });

  it("isolates a failed summary, labels its retry and shows the safe request ID", async () => {
    const calls = spies();
    let fail = true;
    mount(
      usageRoutes(calls, () => {
        if (fail)
          throw new ConnectError(
            "raw secret detail",
            Code.Unavailable,
            undefined,
            [{ desc: ErrorDetailSchema, value: { requestId: "req-safe-001" } }],
          );
        return {
          summary: { requestCount: 7n, knownCosts: [] },
        };
      }),
    );
    const user = userEvent.setup();
    expect(await screen.findByText(/The service is unavailable/)).toBeTruthy();
    expect(screen.queryByText(/raw secret detail/)).toBeNull();
    expect(screen.getByText("Request ID: req-safe-001")).toBeTruthy();
    // Sibling regions are not blocked and the summary is never shown as zero.
    expect(
      await screen.findByRole("heading", { name: "Daily usage" }),
    ).toBeTruthy();
    expect(
      screen.getByRole("heading", { name: "Request metadata" }),
    ).toBeTruthy();
    expect(screen.queryByText("Requests")).toBeNull();
    const listCalls = calls.list.mock.calls.length;
    fail = false;
    await user.click(
      screen.getByRole("button", { name: "Try again loading usage summary" }),
    );
    expect(await screen.findByText("7")).toBeTruthy();
    expect(calls.list.mock.calls.length).toBe(listCalls);
  });

  it("shows a loading status while the summary is pending", async () => {
    let release: () => void = () => {};
    const gate = new Promise<void>((resolve) => (release = resolve));
    mount((router) => {
      router.service(UsageService, {
        listUsage: () => ({ records: [], page: {} }),
        listDailyUsage: () => ({ days: [], page: {} }),
        getUsageSummary: async () => {
          await gate;
          return { summary: { requestCount: 1n, knownCosts: [] } };
        },
      });
    });
    expect(await screen.findByText("Loading usage summary…")).toBeTruthy();
    expect(
      screen.getByRole("heading", { name: "Request metadata" }),
    ).toBeTruthy();
    release();
    expect(await screen.findByText("1")).toBeTruthy();
  });

  it("labels same-filter data as refreshing and marks retained data stale after a failed refetch", async () => {
    let phase: "ok" | "slow" | "fail" = "ok";
    let release: () => void = () => {};
    const client = mount((router) => {
      router.service(UsageService, {
        listUsage: () => ({ records: [], page: {} }),
        listDailyUsage: () => ({ days: [], page: {} }),
        getUsageSummary: async () => {
          if (phase === "slow")
            await new Promise<void>((resolve) => (release = resolve));
          if (phase === "fail")
            throw new ConnectError("down", Code.Unavailable);
          return { summary: { requestCount: 9n, knownCosts: [] } };
        },
      });
    });
    expect(await screen.findByText("9")).toBeTruthy();
    expect(screen.queryByText("Refreshing…")).toBeNull();
    phase = "slow";
    void client.invalidateQueries();
    expect(await screen.findByText("Refreshing…")).toBeTruthy();
    expect(screen.getByText("9")).toBeTruthy();
    phase = "fail";
    release();
    expect(
      await screen.findByText(
        "Data may be out of date. Reconnect and try again.",
      ),
    ).toBeTruthy();
    expect(screen.getByText("9")).toBeTruthy();
    expect(
      screen.getByRole("button", { name: "Try again loading usage summary" }),
    ).toBeTruthy();
  });
});
