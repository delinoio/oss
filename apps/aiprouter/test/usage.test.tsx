// SPDX-License-Identifier: Apache-2.0
import { act, render, screen, waitFor, within } from "@testing-library/react";
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
  UsageService,
  UsageSummarySchema,
  DailyUsageSchema,
} from "@aiprouter/client";
import { ApiProvider } from "../src/api";
import { SummaryCards, UsageTrend } from "../src/usage";
import { Dialog, Button, Pagination } from "../src/ui";
import { useState, type ReactNode } from "react";

function mount(ui: ReactNode, routes: (r: ConnectRouter) => void) {
  const cache = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const transport = createRouterTransport(routes);
  return {
    ...render(
      <ApiProvider queryClient={cache} rpcTransport={transport}>
        {ui}
      </ApiProvider>,
    ),
    cache,
  };
}
const timeRange = {
  start: timestampFromDate(new Date("2026-10-21T00:00:00Z")),
  end: timestampFromDate(new Date("2026-10-27T23:59:59.999Z")),
};
const trend = (
  <UsageTrend organizationId="one" userId="person" timeRange={timeRange} />
);
const day = (
  date: string,
  modelId: string,
  summary?: Parameters<typeof create<typeof UsageSummarySchema>>[1],
) =>
  create(DailyUsageSchema, {
    date,
    modelId,
    summary: summary ? create(UsageSummarySchema, summary) : undefined,
  });

describe("SummaryCards shared layout", () => {
  it("lists every currency on its own line without converting or summing", () => {
    render(
      <SummaryCards
        summary={create(UsageSummarySchema, {
          requestCount: 18446744073709551615n,
          knownCosts: [
            { currency: "USD", decimalAmount: "0" },
            { currency: "EUR", decimalAmount: "0.30" },
            { currency: "JPY", decimalAmount: "12" },
          ],
        })}
      />,
    );
    expect(screen.getByText("18,446,744,073,709,551,615")).toBeTruthy();
    const list = screen.getByRole("list");
    expect(
      within(list)
        .getAllByRole("listitem")
        .map((item) => item.textContent),
    ).toEqual(["USD 0", "EUR 0.30", "JPY 12"]);
  });

  it("shows Unknown for empty known costs and a missing summary with coverage text", () => {
    const { rerender } = render(
      <SummaryCards
        summary={create(UsageSummarySchema, { requestCount: 0n })}
      />,
    );
    expect(screen.queryByRole("list")).toBeNull();
    expect(screen.getByText("0 requests with unknown cost")).toBeTruthy();
    rerender(<SummaryCards />);
    expect(screen.getByText("Unknown cost coverage")).toBeTruthy();
    expect(screen.getByText("Unknown usage coverage")).toBeTruthy();
    expect(screen.queryByText("0")).toBeNull();
  });

  it("keeps absent input tokens distinct from explicit zero output tokens", () => {
    render(
      <SummaryCards
        summary={create(UsageSummarySchema, {
          requestCount: 1n,
          knownUsage: { outputTokens: 0n },
        })}
      />,
    );
    expect(screen.getByText(/Unknown in \/\s+0 out/)).toBeTruthy();
  });

  it("labels loading, updating and failed refresh states", () => {
    const { rerender } = render(<SummaryCards loading />);
    expect(screen.getByText("Loading usage summary…")).toBeTruthy();
    expect(screen.queryByText("Requests")).toBeNull();
    rerender(<SummaryCards updating />);
    expect(screen.getByText("Updating…")).toBeTruthy();
    rerender(<SummaryCards refreshFailed updating />);
    expect(
      screen.getByText("Could not refresh. Showing previously loaded data."),
    ).toBeTruthy();
    expect(screen.queryByText("Updating…")).toBeNull();
  });
});

describe("UsageTrend shared layout", () => {
  it("shows only the returned page, in order, with an explicit Unknown gap and a known zero", async () => {
    const listDailyUsage = rs.fn((_request: unknown) => ({
      days: [
        day("2026-10-22", "gpt-a", {
          requestCount: 7n,
          knownCosts: [
            { currency: "USD", decimalAmount: "1.50" },
            { currency: "EUR", decimalAmount: "0.30" },
          ],
          missingCostCount: 2n,
        }),
        day("2026-10-22", "gpt-b", { requestCount: 0n }),
        day("2026-10-25", "", undefined),
        day("", "gpt-a", { requestCount: 18446744073709551615n }),
      ],
    }));
    const { container } = mount(trend, (r) =>
      r.service(UsageService, { listDailyUsage }),
    );
    const table = await screen.findByRole("table", { hidden: true });
    const rows = within(table)
      .getAllByRole("row", { hidden: true })
      .slice(1)
      .map((row) =>
        within(row)
          .getAllByRole("cell", { hidden: true })
          .map((cell) => cell.textContent),
      );
    const heads = within(table)
      .getAllByRole("rowheader", { hidden: true })
      .map((cell) => cell.textContent);
    // No zero days are inserted for 2026-10-23 or 2026-10-24.
    expect(heads).toEqual([
      "2026-10-22",
      "2026-10-22",
      "2026-10-25",
      "Unknown",
    ]);
    expect(rows).toEqual([
      ["gpt-a", "7", "USD 1.50EUR 0.30", "2"],
      ["gpt-b", "0", "Unknown", "0"],
      ["All models", "Unknown", "Unknown", "Unknown"],
      ["gpt-a", "18,446,744,073,709,551,615", "Unknown", "0"],
    ]);
    expect(within(table).getByText("Daily recorded usage (UTC)")).toBeTruthy();
    const bars = container.querySelector(".usage-bars")!;
    expect(bars.getAttribute("aria-hidden")).toBe("true");
    expect(bars.querySelectorAll(".usage-gap")).toHaveLength(1);
    const zero = bars.querySelectorAll(".usage-bar.zero");
    expect(zero).toHaveLength(1);
    expect(
      Array.from(bars.querySelectorAll(".usage-bar")).map(
        (bar) => (bar as HTMLElement).style.height,
      ),
    ).toEqual(["0%", "0%", "100%"]);
    expect(
      screen.getByRole("region", { name: "Daily usage table", hidden: true }),
    ).toBeTruthy();
  });

  it("pages daily usage through an independent labeled group with opaque tokens", async () => {
    const user = userEvent.setup();
    const listDailyUsage = rs.fn(
      (request: { page?: { pageToken?: string } }) =>
        request.page?.pageToken === "D2"
          ? { days: [day("2026-10-26", "m", { requestCount: 2n })] }
          : {
              days: [day("2026-10-21", "m", { requestCount: 1n })],
              page: { nextPageToken: "D2" },
            },
    );
    mount(trend, (r) => r.service(UsageService, { listDailyUsage }));
    const group = await screen.findByRole("group", {
      name: "Daily usage pages",
    });
    const first = within(group).getByRole("button", { name: "First page" });
    const next = await waitFor(() => {
      const button = within(group).getByRole("button", { name: "Next page" });
      expect((button as HTMLButtonElement).disabled).toBe(false);
      return button;
    });
    expect((first as HTMLButtonElement).disabled).toBe(true);
    expect(listDailyUsage.mock.lastCall?.[0]).toMatchObject({
      page: { pageSize: 100, pageToken: "" },
    });
    await user.click(next);
    await waitFor(() =>
      expect(listDailyUsage.mock.lastCall?.[0]).toMatchObject({
        organizationId: "one",
        userId: "person",
        page: { pageSize: 100, pageToken: "D2" },
      }),
    );
    await waitFor(() =>
      expect(
        (
          within(group).getByRole("button", {
            name: "First page",
          }) as HTMLButtonElement
        ).disabled,
      ).toBe(false),
    );
    await user.click(within(group).getByRole("button", { name: "First page" }));
    await waitFor(() =>
      expect(listDailyUsage.mock.lastCall?.[0]).toMatchObject({
        page: { pageToken: "" },
      }),
    );
  });

  it("shows the empty message only for a successful empty response", async () => {
    mount(trend, (r) =>
      r.service(UsageService, { listDailyUsage: () => ({ days: [] }) }),
    );
    expect(
      await screen.findByText("No daily records available for this range."),
    ).toBeTruthy();
  });

  it("never renders an error as empty and retries only the daily query", async () => {
    const user = userEvent.setup();
    let calls = 0;
    const listDailyUsage = rs.fn((_request: unknown) => {
      calls += 1;
      if (calls === 1) throw new ConnectError("daily failed", Code.Unavailable);
      return { days: [day("2026-10-21", "m", { requestCount: 3n })] };
    });
    mount(trend, (r) => r.service(UsageService, { listDailyUsage }));
    expect(await screen.findByText(/The server is unavailable/)).toBeTruthy();
    expect(
      screen.queryByText("No daily records available for this range."),
    ).toBeNull();
    await user.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByRole("table", { hidden: true })).toBeTruthy();
    expect(listDailyUsage).toHaveBeenCalledTimes(2);
  });

  it("labels refreshes and keeps previous data visible when a refresh fails", async () => {
    let release: () => void = () => undefined;
    let calls = 0;
    const listDailyUsage = rs.fn(async (_request: unknown) => {
      calls += 1;
      if (calls === 2) await new Promise<void>((r) => (release = r));
      if (calls === 3)
        throw new ConnectError("refresh failed", Code.Unavailable);
      return { days: [day("2026-10-21", "m", { requestCount: 3n })] };
    });
    const { cache } = mount(trend, (r) =>
      r.service(UsageService, { listDailyUsage }),
    );
    await screen.findByRole("table", { hidden: true });
    expect(screen.queryByText("Updating…")).toBeNull();
    await act(async () => {
      void cache.invalidateQueries();
    });
    expect(await screen.findByText("Updating…")).toBeTruthy();
    expect(screen.getByRole("table", { hidden: true })).toBeTruthy();
    await act(async () => {
      release();
    });
    await waitFor(() => expect(screen.queryByText("Updating…")).toBeNull());
    await act(async () => {
      void cache.invalidateQueries();
    });
    expect(
      await screen.findByText(
        "Could not refresh. Showing previously loaded data.",
      ),
    ).toBeTruthy();
    expect(screen.getByRole("table", { hidden: true })).toBeTruthy();
  });
});

describe("shared dialog and pagination additions", () => {
  function Harness({ focusHeading }: { focusHeading?: boolean }) {
    const [open, setOpen] = useState(false);
    return (
      <>
        <Button onClick={() => setOpen(true)}>Open</Button>
        {open && (
          <Dialog
            title="Details"
            focusHeading={focusHeading}
            className="sized"
            onClose={() => setOpen(false)}
          >
            <input aria-label="Field" />
          </Dialog>
        )}
      </>
    );
  }
  it("focuses the heading when requested, applies className, and restores trigger focus", async () => {
    const user = userEvent.setup();
    render(<Harness focusHeading />);
    const trigger = screen.getByRole("button", { name: "Open" });
    await user.click(trigger);
    const dialog = screen.getByRole("dialog");
    expect(document.activeElement).toBe(
      within(dialog).getByRole("heading", { name: "Details" }),
    );
    expect(dialog.className).toContain("sized");
    await user.click(
      within(dialog).getByRole("button", { name: "Close dialog" }),
    );
    expect(document.activeElement).toBe(trigger);
  });
  it("keeps focusing the first control by default", async () => {
    const user = userEvent.setup();
    render(<Harness />);
    await user.click(screen.getByRole("button", { name: "Open" }));
    expect(document.activeElement).toBe(screen.getByLabelText("Field"));
  });
  it("names the pagination group only when a label is given", () => {
    const { rerender } = render(
      <Pagination
        label="Daily usage pages"
        onNext={() => undefined}
        onFirst={() => undefined}
      />,
    );
    expect(
      screen.getByRole("group", { name: "Daily usage pages" }),
    ).toBeTruthy();
    rerender(<Pagination onNext={() => undefined} onFirst={() => undefined} />);
    expect(screen.queryByRole("group")).toBeNull();
  });
});
