// SPDX-License-Identifier: Apache-2.0
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, it, expect, rs } from "@rstest/core";
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
  GetSessionResponseSchema,
  OrganizationSchema,
  PriceSource,
  RequestOutcome,
  Role,
  UsageRecordSchema,
  UsageService,
} from "@tairouter/client";
import { ApiProvider } from "../src/api";
import { UsagePage } from "../src/usage";

const organization = create(OrganizationSchema, {
  id: "one",
  name: "Studio",
  role: Role.MEMBER,
});
const session = create(GetSessionResponseSchema, {
  userId: "person",
  displayName: "Alex",
});
const props = { organization, session };
type RecordInit = Parameters<typeof create<typeof UsageRecordSchema>>[1];

// jsdom does not turn Escape into the dialog "cancel" event; dispatch it.
function cancelDialog(dialog: HTMLElement) {
  act(() => {
    dialog.dispatchEvent(new Event("cancel", { cancelable: true }));
  });
}

function setWidth(width: number) {
  Object.defineProperty(window, "innerWidth", {
    configurable: true,
    writable: true,
    value: width,
  });
}
afterEach(() => setWidth(1024));

const occurredAt = timestampFromDate(new Date("2026-10-27T10:20:00Z"));
const record = (init: RecordInit) => create(UsageRecordSchema, init);
const recordA = () =>
  record({
    id: "a",
    modelId: "chat-primary",
    requestId: "req_demo_01",
    outcome: RequestOutcome.SUCCEEDED,
    occurredAt,
    usage: { inputTokens: 1200n, outputTokens: 0n },
    estimatedCost: { currency: "USD", decimalAmount: "0.018" },
    appliedPrice: {
      source: PriceSource.BUNDLED,
      sourceRevision: "demo-snapshot-2026-10",
      serviceTier: "default",
      appliedAt: occurredAt,
    },
  });
const recordB = () =>
  record({
    id: "b",
    modelId: "chat-backup",
    outcome: RequestOutcome.UPSTREAM_ERROR,
  });

function mount(routes: (r: ConnectRouter) => void) {
  const cache = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const transport = createRouterTransport(routes);
  const wrap = (ui: React.ReactNode) => (
    <ApiProvider queryClient={cache} rpcTransport={transport}>
      {ui}
    </ApiProvider>
  );
  const view = render(wrap(<UsagePage {...props} />));
  return {
    cache,
    rerender: (ui: React.ReactNode) => view.rerender(wrap(ui)),
  };
}
function usageRoutes(
  listUsage: (request: any) => unknown,
  extra: Record<string, unknown> = {},
) {
  return (r: ConnectRouter) =>
    r.service(UsageService, {
      listUsage: listUsage as never,
      getUsageSummary: () => ({}),
      listDailyUsage: () => ({ days: [] }),
      ...extra,
    });
}

describe("request rows", () => {
  it("renders rows in server order with model first, UTC time, outcome text and labeled values", async () => {
    setWidth(1280);
    mount(usageRoutes(() => ({ records: [recordB(), recordA()] })));
    expect(
      await screen.findByRole("heading", { level: 2, name: "Requests" }),
    ).toBeTruthy();
    const rows = (await screen.findAllByRole("heading", { level: 3 })).map(
      (h) => h.textContent,
    );
    expect(rows).toEqual(["chat-backup", "chat-primary"]);
    const [rowB, rowA] = screen
      .getAllByRole("heading", { level: 3 })
      .map((h) => h.closest("section")!);
    expect(within(rowA).getByText("Oct 27, 2026, 10:20 AM UTC")).toBeTruthy();
    expect(within(rowA).getByText("Succeeded")).toBeTruthy();
    expect(within(rowA).getByText("Tokens: 1,200 in / 0 out")).toBeTruthy();
    expect(within(rowA).getByText("Estimated cost: USD 0.018")).toBeTruthy();
    expect(within(rowA).getByText("Request ID: req_demo_01")).toBeTruthy();
    expect(within(rowB).getByText("Upstream error")).toBeTruthy();
    expect(
      within(rowB).getByText("Tokens: Unknown in / Unknown out"),
    ).toBeTruthy();
    expect(within(rowB).getByText("Estimated cost: Unknown")).toBeTruthy();
    expect(within(rowB).getByText("Request ID: Unknown")).toBeTruthy();
    expect(
      within(rowB).getByText("Unknown", {
        selector: ".usage-record-meta span",
      }),
    ).toBeTruthy();
  });

  it("maps unspecified and future outcomes to Unknown and every known outcome to text", async () => {
    setWidth(1280);
    const outcomes = [
      RequestOutcome.SUCCEEDED,
      RequestOutcome.UPSTREAM_ERROR,
      RequestOutcome.GATEWAY_ERROR,
      RequestOutcome.CANCELED,
      RequestOutcome.RATE_LIMITED,
      RequestOutcome.UNSPECIFIED,
      99 as RequestOutcome,
    ];
    mount(
      usageRoutes(() => ({
        records: outcomes.map((outcome, i) =>
          record({ id: `r${i}`, modelId: `m${i}`, outcome }),
        ),
      })),
    );
    await screen.findByText("m0");
    const badges = Array.from(document.querySelectorAll(".badge")).map(
      (b) => b.textContent,
    );
    expect(badges).toEqual([
      "Succeeded",
      "Upstream error",
      "Gateway error",
      "Canceled",
      "Rate limited",
      "Unknown",
      "Unknown",
    ]);
  });

  it("gives each View estimate action a row-specific accessible name", async () => {
    setWidth(1280);
    mount(usageRoutes(() => ({ records: [recordA(), recordB()] })));
    expect(
      await screen.findByRole("button", {
        name: "View estimate for chat-primary, request req_demo_01",
      }),
    ).toBeTruthy();
    expect(
      screen.getByRole("button", {
        name: "View estimate for chat-backup, request with unknown ID",
      }),
    ).toBeTruthy();
  });
});

describe("Request estimate dialog", () => {
  it("shows the recorded snapshot, focuses the heading and returns focus on Done, X and Escape", async () => {
    setWidth(1280);
    const user = userEvent.setup();
    mount(usageRoutes(() => ({ records: [recordA()] })));
    const trigger = await screen.findByRole("button", {
      name: /View estimate for chat-primary/,
    });
    for (const close of ["Done", "X", "Escape"]) {
      await user.click(trigger);
      const dialog = await screen.findByRole("dialog", {
        name: "Request estimate",
      });
      expect(document.activeElement).toBe(
        within(dialog).getByRole("heading", { name: "Request estimate" }),
      );
      expect(dialog.classList.contains("usage-estimate-dialog")).toBe(true);
      const terms = Array.from(dialog.querySelectorAll("dt")).map(
        (t) => t.textContent,
      );
      expect(terms).toEqual([
        "Request ID",
        "Price source",
        "Price revision",
        "Applied (UTC)",
        "Service tier",
      ]);
      const values = Array.from(dialog.querySelectorAll("dd")).map(
        (d) => d.textContent,
      );
      expect(values).toEqual([
        "req_demo_01",
        "Bundled snapshot",
        "demo-snapshot-2026-10",
        "Oct 27, 2026, 10:20 AM UTC",
        "default",
      ]);
      expect(within(dialog).getByText("USD 0.018")).toBeTruthy();
      expect(within(dialog).getByText("Estimated cost")).toBeTruthy();
      expect(
        within(dialog).getByText(
          "The applied price and estimate are fixed for this request. Later price changes do not change this estimate.",
        ),
      ).toBeTruthy();
      if (close === "Done")
        await user.click(within(dialog).getByRole("button", { name: "Done" }));
      else if (close === "X")
        await user.click(
          within(dialog).getByRole("button", { name: "Close dialog" }),
        );
      else cancelDialog(dialog);
      await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
      expect(document.activeElement).toBe(trigger);
    }
  });

  it("labels each price source and shows Unknown for missing or unrecognized values", async () => {
    setWidth(1280);
    const user = userEvent.setup();
    const sourceOf = (id: string, source: PriceSource, revision = "") =>
      record({
        id,
        modelId: id,
        appliedPrice: { source, sourceRevision: revision },
      });
    mount(
      usageRoutes(() => ({
        records: [
          sourceOf("override", PriceSource.OPERATOR_OVERRIDE, "ops-7"),
          sourceOf("unspecified", PriceSource.UNSPECIFIED),
          sourceOf("future", 42 as PriceSource),
          record({ id: "none", modelId: "none" }),
        ],
      })),
    );
    const open = async (model: string) => {
      await user.click(
        await screen.findByRole("button", {
          name: new RegExp(`View estimate for ${model},`),
        }),
      );
      const dialog = await screen.findByRole("dialog");
      const values = Array.from(dialog.querySelectorAll("dd")).map(
        (d) => d.textContent,
      );
      cancelDialog(dialog);
      await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
      return values;
    };
    expect(await open("override")).toEqual([
      "Unknown",
      "Operator override",
      "ops-7",
      "Unknown",
      "Unknown",
    ]);
    for (const model of ["unspecified", "future", "none"])
      expect(await open(model)).toEqual([
        "Unknown",
        "Unknown",
        "Unknown",
        "Unknown",
        "Unknown",
      ]);
  });

  it("keeps the recorded snapshot when later data for the same request changes", async () => {
    setWidth(1280);
    const user = userEvent.setup();
    let revision = "demo-snapshot-2026-10";
    const { cache } = mount(
      usageRoutes(() => {
        const a = recordA();
        a.appliedPrice!.sourceRevision = revision;
        return { records: [a] };
      }),
    );
    await user.click(
      await screen.findByRole("button", { name: /View estimate for/ }),
    );
    revision = "operator-override-2026-11";
    await cache.invalidateQueries();
    await waitFor(() => expect(cache.isFetching()).toBe(0));
    const dialog = screen.getByRole("dialog");
    expect(within(dialog).getByText("demo-snapshot-2026-10")).toBeTruthy();
    expect(within(dialog).queryByText("operator-override-2026-11")).toBeNull();
  });

  it("closes when a filter edit replaces the owning collection", async () => {
    setWidth(1280);
    const user = userEvent.setup();
    mount(usageRoutes(() => ({ records: [recordA()] })));
    await user.click(
      await screen.findByRole("button", { name: /View estimate for/ }),
    );
    await screen.findByRole("dialog");
    // The modal makes page content inert for users; drive the filter directly.
    await user.type(screen.getByLabelText("Model ID"), "x");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });

  it("closes when record paging replaces the collection", async () => {
    setWidth(1280);
    const user = userEvent.setup();
    const listUsage = rs.fn((request: { page?: { pageToken?: string } }) =>
      request.page?.pageToken
        ? { records: [recordB()] }
        : { records: [recordA()], page: { nextPageToken: "R2" } },
    );
    mount(usageRoutes(listUsage));
    await user.click(
      await screen.findByRole("button", { name: /View estimate for/ }),
    );
    await screen.findByRole("dialog");
    const next = screen.getAllByRole("button", {
      name: "Next page",
      hidden: true,
    })[1];
    await user.click(next);
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await screen.findByText("chat-backup");
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("closes when the organization scope changes", async () => {
    setWidth(1280);
    const user = userEvent.setup();
    const { rerender } = mount(usageRoutes(() => ({ records: [recordA()] })));
    await user.click(
      await screen.findByRole("button", { name: /View estimate for/ }),
    );
    await screen.findByRole("dialog");
    rerender(
      <UsagePage
        organization={create(OrganizationSchema, { id: "two", name: "Other" })}
        session={session}
      />,
    );
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });
});

describe("request states", () => {
  it("shows a labeled loading status before the first response", async () => {
    setWidth(1280);
    let release!: () => void;
    const gate = new Promise<void>((resolve) => (release = resolve));
    mount(
      usageRoutes(async () => {
        await gate;
        return { records: [recordA()] };
      }),
    );
    expect(await screen.findByText("Loading requests…")).toBeTruthy();
    release();
    await screen.findByText("chat-primary");
    expect(screen.queryByText("Loading requests…")).toBeNull();
  });

  it("shows the empty state only for a successful empty response and Clear filters resets", async () => {
    setWidth(1280);
    const user = userEvent.setup();
    mount(usageRoutes(() => ({ records: [] })));
    expect(await screen.findByText("No matching requests")).toBeTruthy();
    expect(
      screen.getByText("Requests appear after a client uses your routing."),
    ).toBeTruthy();
    await user.type(screen.getByLabelText("Model ID"), "gpt");
    await user.click(
      within(
        screen.getByText("No matching requests").closest("section")!,
      ).getByRole("button", { name: "Clear filters" }),
    );
    expect((screen.getByLabelText("Model ID") as HTMLInputElement).value).toBe(
      "",
    );
  });

  it("shows a failed query as an error with retry, never as empty, and retries only records", async () => {
    setWidth(1280);
    const user = userEvent.setup();
    let fail = true;
    const listUsage = rs.fn(() => {
      if (fail) throw new ConnectError("records down", Code.Unavailable);
      return { records: [recordA()] };
    });
    const getUsageSummary = rs.fn(() => ({}));
    const listDailyUsage = rs.fn(() => ({ days: [] }));
    mount(usageRoutes(listUsage, { getUsageSummary, listDailyUsage }));
    await screen.findByText(/server is unavailable/);
    expect(screen.queryByText("No matching requests")).toBeNull();
    const summaryCalls = getUsageSummary.mock.calls.length;
    const dailyCalls = listDailyUsage.mock.calls.length;
    fail = false;
    await user.click(screen.getByRole("button", { name: "Try again" }));
    await screen.findByText("chat-primary");
    expect(getUsageSummary.mock.calls.length).toBe(summaryCalls);
    expect(listDailyUsage.mock.calls.length).toBe(dailyCalls);
  });

  it("labels refreshing and keeps previous records when a refresh fails", async () => {
    setWidth(1280);
    let phase: "ok" | "slow" | "fail" = "ok";
    let release!: () => void;
    const gate = new Promise<void>((resolve) => (release = resolve));
    const { cache } = mount(
      usageRoutes(async () => {
        if (phase === "slow") await gate;
        if (phase === "fail")
          throw new ConnectError("refresh down", Code.Unavailable);
        return { records: [recordA()] };
      }),
    );
    await screen.findByText("chat-primary");
    phase = "slow";
    void cache.invalidateQueries();
    expect(await screen.findByText("Updating…")).toBeTruthy();
    expect(screen.getByText("chat-primary")).toBeTruthy();
    phase = "fail";
    release();
    await waitFor(() => expect(screen.queryByText("Updating…")).toBeNull());
    void cache.invalidateQueries();
    await screen.findByText(
      "Could not refresh. Showing previously loaded data.",
    );
    expect(screen.getByText("chat-primary")).toBeTruthy();
    expect(screen.queryByText("No matching requests")).toBeNull();
  });
});
