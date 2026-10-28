// SPDX-License-Identifier: Apache-2.0
import { afterEach, describe, expect, it, rs } from "@rstest/core";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {
  Code,
  ConnectError,
  createRouterTransport,
  type ConnectRouter,
} from "@connectrpc/connect";
import { QueryClient } from "@tanstack/react-query";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import {
  AccountService,
  AuditAction,
  AuditOutcome,
  AuditService,
  ErrorDetailSchema,
  OperatorService,
  Role,
  type ListOrganizationAuditRequest,
} from "@tairouter/client";
import { ApiProvider } from "../api";
import { AdminAuthority } from "./common";
import { Audit } from "./reporting";

const originalMatchMedia = window.matchMedia;
afterEach(() => {
  window.matchMedia = originalMatchMedia;
});
// jsdom has no layout, so viewport-dependent rendering is driven by a width mock.
function mockViewport(initial: number) {
  let width = initial;
  const listeners = new Set<() => void>();
  const evaluate = (query: string) => {
    const min = /min-width:\s*([\d.]+)px/.exec(query);
    const max = /max-width:\s*([\d.]+)px/.exec(query);
    return (
      (!min || width >= Number(min[1])) && (!max || width <= Number(max[1]))
    );
  };
  window.matchMedia = ((query: string) => ({
    get matches() {
      return evaluate(query);
    },
    media: query,
    addEventListener: (_: string, fn: () => void) => listeners.add(fn),
    removeEventListener: (_: string, fn: () => void) => listeners.delete(fn),
  })) as unknown as typeof window.matchMedia;
  return {
    resize(next: number) {
      width = next;
      act(() => listeners.forEach((fn) => fn()));
    },
  };
}
function mount(
  routes: (router: ConnectRouter) => void,
  client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  }),
) {
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
        <Audit />
      </AdminAuthority.Provider>
    </ApiProvider>,
  );
  return client;
}
function auditRoutes(
  respond: (request: ListOrganizationAuditRequest) => object,
  calls = rs.fn<(request: ListOrganizationAuditRequest) => void>(),
) {
  const instance = rs.fn();
  return {
    calls,
    instance,
    routes: (router: ConnectRouter) => {
      router.service(AuditService, {
        listOrganizationAudit: (request) => {
          calls(request);
          return respond(request);
        },
      });
      router.service(OperatorService, {
        listInstanceAudit: () => {
          instance();
          return {};
        },
      });
    },
  };
}
const occurredAt = timestampFromDate(new Date("2026-10-27T09:42:00Z"));
const records = [
  {
    id: "1",
    occurredAt,
    actorUserId: "actor-demo-01",
    action: AuditAction.KEY_REVOKED,
    resourceId: "key-demo-04",
    outcome: AuditOutcome.SUCCEEDED,
    requestId: "req-demo-104",
  },
  {
    id: "2",
    occurredAt,
    action: AuditAction.UNSPECIFIED,
    outcome: AuditOutcome.UNSPECIFIED,
  },
  {
    id: "3",
    occurredAt,
    actorUserId: "actor-demo-02",
    action: AuditAction.KEY_REVOKED,
    resourceId: "<img src=x onerror=alert(1)>-" + "long".repeat(40),
    outcome: AuditOutcome.DENIED,
  },
  {
    id: "4",
    action: AuditAction.KEY_REVOKED,
    resourceId: "key-demo-05",
    outcome: AuditOutcome.FAILED,
  },
];

describe("organization audit layout", () => {
  it("renders header, stacked cards and a semantic table in server order on desktop", async () => {
    mockViewport(1440);
    const f = auditRoutes(() => ({ records }));
    mount(f.routes);
    expect(
      await screen.findByRole("heading", {
        level: 1,
        name: "Organization audit",
      }),
    ).toBeTruthy();
    expect(screen.getAllByRole("heading", { level: 1 })).toHaveLength(1);
    expect(
      screen.getByText(
        "Permission, lifecycle and configuration events. Safe metadata only.",
      ),
    ).toBeTruthy();
    const filters = screen.getByRole("region", { name: "Filters" });
    const events = screen.getByRole("region", { name: "Audit events" });
    expect(
      filters.compareDocumentPosition(events) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    expect(within(filters).getAllByRole("combobox")).toHaveLength(2);
    expect(
      within(filters).getByRole("button", { name: "Clear filters" }),
    ).toBeTruthy();
    const table = await screen.findByRole("table");
    expect(
      screen.getByText(
        `Times shown in ${Intl.DateTimeFormat().resolvedOptions().timeZone}.`,
      ),
    ).toBeTruthy();
    expect(
      within(table)
        .getAllByRole("columnheader")
        .map((h) => h.textContent),
    ).toEqual([
      "Time",
      "Actor",
      "Action",
      "Safe target",
      "Outcome",
      "Request ID",
    ]);
    const rows = within(table).getAllByRole("row").slice(1);
    expect(rows).toHaveLength(4);
    expect(within(rows[0]).getByText("actor-demo-01")).toBeTruthy();
    expect(within(rows[0]).getByText("Key revoked")).toBeTruthy();
    expect(within(rows[0]).getByText("Succeeded")).toBeTruthy();
    expect(within(rows[0]).getByText("req-demo-104")).toBeTruthy();
    // Missing actor, action, target and request ID use safe fallbacks.
    expect(within(rows[1]).getByText("Deleted user")).toBeTruthy();
    expect(within(rows[1]).getByText("Unknown action")).toBeTruthy();
    expect(within(rows[1]).getAllByText("Unknown").length).toBeGreaterThan(0);
    expect(within(rows[1]).getByText("Not provided")).toBeTruthy();
    expect(within(rows[2]).getByText("Denied")).toBeTruthy();
    expect(within(rows[3]).getByText("Failed")).toBeTruthy();
    expect(within(rows[3]).getAllByText("Unknown").length).toBeGreaterThan(0);
    // Markup-like strings are plain text.
    expect(table.querySelector("img")).toBeNull();
    expect(rows[2].textContent).toContain("<img src=x onerror=alert(1)>-");
    expect(f.instance).not.toHaveBeenCalled();
  });

  it("uses labeled event cards below 1024px and omits an absent request ID", async () => {
    mockViewport(768);
    const f = auditRoutes(() => ({ records }));
    mount(f.routes);
    const cards = await screen.findAllByRole("article");
    expect(screen.queryByRole("table")).toBeNull();
    expect(cards).toHaveLength(4);
    expect(
      within(cards[0]).getByRole("heading", { name: "Key revoked" }),
    ).toBeTruthy();
    expect(within(cards[0]).getByText("Request ID")).toBeTruthy();
    expect(within(cards[0]).getByText("req-demo-104")).toBeTruthy();
    expect(
      within(cards[1]).getByRole("heading", { name: "Unknown action" }),
    ).toBeTruthy();
    expect(within(cards[1]).getByText("Deleted user")).toBeTruthy();
    expect(within(cards[1]).queryByText("Request ID")).toBeNull();
    expect(within(cards[1]).queryByText("Not provided")).toBeNull();
    expect(cards[2].querySelector("img")).toBeNull();
    expect(within(cards[3]).getByText("Failed")).toBeTruthy();
  });
});

describe("organization audit requests", () => {
  it("keeps conjunctive server-side filters, exact UTC bounds and cursor resets", async () => {
    mockViewport(1440);
    const f = auditRoutes((request) => ({
      records: [records[0]],
      page: { nextPageToken: request.page?.pageToken ? "" : "next-demo" },
    }));
    mount(f.routes);
    const user = userEvent.setup();
    await screen.findByRole("table");
    expect(f.calls.mock.lastCall?.[0]).toMatchObject({
      organizationId: "organization-a",
      page: { pageSize: 100, pageToken: "" },
    });
    expect(f.calls.mock.lastCall?.[0].timeRange).toBeUndefined();
    await user.type(
      screen.getByRole("textbox", { name: "Actor ID" }),
      "actor-demo-01",
    );
    await user.selectOptions(
      screen.getByRole("combobox", { name: "Action" }),
      String(AuditAction.KEY_REVOKED),
    );
    await user.selectOptions(
      screen.getByRole("combobox", { name: "Outcome" }),
      String(AuditOutcome.DENIED),
    );
    await user.type(screen.getByLabelText("From (UTC)"), "2026-10-20");
    await user.type(screen.getByLabelText("Through (UTC)"), "2026-10-27");
    await waitFor(() => {
      const request = f.calls.mock.lastCall![0];
      expect(request).toMatchObject({
        organizationId: "organization-a",
        actorUserId: "actor-demo-01",
        action: AuditAction.KEY_REVOKED,
        outcome: AuditOutcome.DENIED,
        page: { pageSize: 100, pageToken: "" },
      });
      expect(request.timeRange?.start?.seconds).toBe(
        BigInt(Date.UTC(2026, 9, 20) / 1000),
      );
      const end = request.timeRange!.end!;
      expect(Number(end.seconds) * 1000 + end.nanos / 1e6).toBe(
        Date.UTC(2026, 9, 27, 23, 59, 59, 999),
      );
    });
    // The returned record is shown without client-side refiltering.
    expect(
      within(screen.getByRole("table")).getByText("Succeeded"),
    ).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Previous page" })).toBeNull();
    await user.click(screen.getByRole("button", { name: "Next page" }));
    await waitFor(() =>
      expect(f.calls.mock.lastCall?.[0].page?.pageToken).toBe("next-demo"),
    );
    expect(
      await screen.findByRole("button", { name: "First page" }),
    ).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Next page" })).toBeNull();
    await user.selectOptions(
      screen.getByRole("combobox", { name: "Outcome" }),
      String(AuditOutcome.UNSPECIFIED),
    );
    await waitFor(() =>
      expect(f.calls.mock.lastCall?.[0]).toMatchObject({
        outcome: AuditOutcome.UNSPECIFIED,
        page: { pageToken: "" },
      }),
    );
    await user.selectOptions(
      screen.getByRole("combobox", { name: "Action" }),
      String(AuditAction.UNSPECIFIED),
    );
    await waitFor(() =>
      expect(f.calls.mock.lastCall?.[0].action).toBe(AuditAction.UNSPECIFIED),
    );
    await user.click(screen.getByRole("button", { name: "Clear filters" }));
    await waitFor(() => {
      const request = f.calls.mock.lastCall![0];
      expect(request.actorUserId).toBeUndefined();
      expect(request.action).toBeUndefined();
      expect(request.outcome).toBeUndefined();
      expect(request.timeRange).toBeUndefined();
      expect(request.page?.pageToken).toBe("");
    });
    expect(f.instance).not.toHaveBeenCalled();
  });

  it("pauses requests for an incomplete or reversed date pair and hides stale rows", async () => {
    mockViewport(1440);
    const f = auditRoutes(() => ({ records: [records[0]] }));
    mount(f.routes);
    const user = userEvent.setup();
    await screen.findByRole("table");
    const count = f.calls.mock.calls.length;
    const from = screen.getByLabelText("From (UTC)");
    const through = screen.getByLabelText("Through (UTC)");
    await user.type(from, "2026-10-20");
    const message = await screen.findByText(
      "Choose both dates with the end on or after the start.",
    );
    expect(message.getAttribute("role")).toBe("alert");
    expect(
      screen.getByText("Results pause until the date range is valid."),
    ).toBeTruthy();
    for (const input of [from, through]) {
      expect(input.getAttribute("aria-invalid")).toBe("true");
      const described = input.getAttribute("aria-describedby")!.split(" ");
      expect(described).toContain(message.id);
      expect(described).toHaveLength(2);
    }
    expect(screen.queryByRole("table")).toBeNull();
    expect(f.calls.mock.calls.length).toBe(count);
    await user.type(through, "2026-10-10");
    expect(screen.queryByRole("table")).toBeNull();
    expect(f.calls.mock.calls.length).toBe(count);
    await user.clear(through);
    await user.type(through, "2026-10-20");
    await screen.findByRole("table");
    expect(from.getAttribute("aria-invalid")).toBeNull();
    expect(f.calls.mock.lastCall?.[0].timeRange?.start?.seconds).toBe(
      BigInt(Date.UTC(2026, 9, 20) / 1000),
    );
  });
});

describe("organization audit states", () => {
  it("shows a loading skeleton, then the exact unfiltered empty copy with no pager", async () => {
    mockViewport(1440);
    let release: () => void = () => {};
    const gate = new Promise<void>((resolve) => (release = resolve));
    const calls = rs.fn<(request: ListOrganizationAuditRequest) => void>();
    mount((router) =>
      router.service(AuditService, {
        listOrganizationAudit: async (request) => {
          calls(request);
          await gate;
          return { records: [] };
        },
      }),
    );
    const loading = await screen.findByRole("status");
    expect(loading.textContent).toBe("Loading audit events…");
    expect(loading.getAttribute("aria-busy")).toBe("true");
    act(() => release());
    expect(await screen.findByText("No audit events available.")).toBeTruthy();
    expect(
      screen.getByText(
        "Events will appear here when organization activity is recorded.",
      ),
    ).toBeTruthy();
    expect(screen.queryByRole("navigation")).toBeNull();
    expect(screen.queryByText(/never|no events/i)).toBeNull();
  });

  it("explains filtered and later-page empties without claiming a lifetime", async () => {
    mockViewport(1440);
    const f = auditRoutes((request) => ({
      records: [],
      page: {
        nextPageToken:
          request.page?.pageToken === "" && request.actorUserId ? "more" : "",
      },
    }));
    mount(f.routes);
    const user = userEvent.setup();
    await screen.findByText("No audit events available.");
    await user.type(screen.getByRole("textbox", { name: "Actor ID" }), "x");
    expect(
      await screen.findByText(
        "No audit records match. Clear filters or check another page.",
      ),
    ).toBeTruthy();
    expect(screen.getByRole("button", { name: "Clear filters" })).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Next page" }));
    expect(
      await screen.findByRole("button", { name: "First page" }),
    ).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Next page" })).toBeNull();
    expect(screen.queryByText(/total|page \d/i)).toBeNull();
    await user.click(screen.getByRole("button", { name: "First page" }));
    await waitFor(() =>
      expect(f.calls.mock.lastCall?.[0].page?.pageToken).toBe(""),
    );
  });

  it("keeps filters on error, shows the request ID and retries", async () => {
    mockViewport(1440);
    let fail = true;
    const f = auditRoutes(() => {
      if (fail)
        throw new ConnectError(
          "raw secret detail",
          Code.Unavailable,
          undefined,
          [{ desc: ErrorDetailSchema, value: { requestId: "req-safe-1" } }],
        );
      return { records: [records[0]] };
    });
    mount(f.routes);
    const user = userEvent.setup();
    expect(await screen.findByText(/The service is unavailable/)).toBeTruthy();
    expect(screen.queryByText(/raw secret detail/)).toBeNull();
    expect(screen.getByText("Request ID: req-safe-1")).toBeTruthy();
    await user.type(screen.getByRole("textbox", { name: "Actor ID" }), "keep");
    fail = false;
    await user.click(await screen.findByRole("button", { name: "Try again" }));
    await screen.findByRole("table");
    expect(
      (screen.getByRole("textbox", { name: "Actor ID" }) as HTMLInputElement)
        .value,
    ).toBe("keep");
  });

  it("marks same-scope rows stale when a refresh fails and hides other-filter rows", async () => {
    mockViewport(1440);
    let fail = false;
    const f = auditRoutes((request) => {
      if (request.actorUserId === "other") return { records: [] };
      if (fail) throw new ConnectError("down", Code.Unavailable);
      return { records: [records[0]] };
    });
    const client = mount(f.routes);
    const user = userEvent.setup();
    await screen.findByRole("table");
    fail = true;
    await act(() => client.invalidateQueries());
    expect(
      await screen.findByText(
        "Audit events may be out of date. Reconnect to refresh.",
      ),
    ).toBeTruthy();
    expect(screen.getByRole("table")).toBeTruthy();
    await user.type(screen.getByRole("textbox", { name: "Actor ID" }), "other");
    await screen.findByText(/No audit records match/);
    expect(screen.queryByRole("table")).toBeNull();
  });
});

describe("organization audit filters sheet", () => {
  it("opens a focused modal sheet whose edits stay live and counts active filters", async () => {
    mockViewport(390);
    const f = auditRoutes(() => ({ records: [records[0]] }));
    mount(f.routes);
    const user = userEvent.setup();
    const trigger = await screen.findByRole("button", { name: "Filters" });
    expect(screen.queryByRole("textbox", { name: "Actor ID" })).toBeNull();
    await user.click(trigger);
    const dialog = await screen.findByRole("dialog", { name: "Filters" });
    expect(document.activeElement).toBe(
      within(dialog).getByRole("heading", { name: "Filters" }),
    );
    expect(
      within(dialog).getByText(
        "Filters update automatically. Dates are optional; choose both or leave both empty.",
      ),
    ).toBeTruthy();
    await user.selectOptions(
      within(dialog).getByRole("combobox", { name: "Outcome" }),
      String(AuditOutcome.UNSPECIFIED),
    );
    await user.type(within(dialog).getByLabelText("From (UTC)"), "2026-10-20");
    // Incomplete date pair counts once; the invalid range is explained in the sheet.
    expect(
      within(dialog).getByText(
        "Choose both dates with the end on or after the start.",
      ),
    ).toBeTruthy();
    expect(
      within(dialog)
        .getByLabelText("Through (UTC)")
        .getAttribute("aria-describedby"),
    ).toBeTruthy();
    // Clear filters keeps the sheet open.
    await user.click(
      within(dialog).getByRole("button", { name: "Clear filters" }),
    );
    expect(screen.getByRole("dialog", { name: "Filters" })).toBeTruthy();
    expect(
      (
        within(dialog).getByRole("combobox", {
          name: "Outcome",
        }) as HTMLSelectElement
      ).value,
    ).toBe("");
    await user.selectOptions(
      within(dialog).getByRole("combobox", { name: "Outcome" }),
      String(AuditOutcome.UNSPECIFIED),
    );
    await waitFor(() =>
      expect(f.calls.mock.lastCall?.[0].outcome).toBe(AuditOutcome.UNSPECIFIED),
    );
    await user.click(within(dialog).getByRole("button", { name: "Done" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    const active = screen.getByRole("button", { name: "Filters (1)" });
    expect(document.activeElement).toBe(active);
    expect(screen.getByText("Outcome: Unknown")).toBeTruthy();
    expect(f.calls.mock.lastCall?.[0].outcome).toBe(AuditOutcome.UNSPECIFIED);
  });

  it("closes with the X control and Escape without changing filters, and traps Tab", async () => {
    mockViewport(390);
    const f = auditRoutes(() => ({ records: [] }));
    mount(f.routes);
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "Filters" }));
    let dialog = await screen.findByRole("dialog", { name: "Filters" });
    await user.type(
      within(dialog).getByRole("textbox", { name: "Actor ID" }),
      "a",
    );
    const close = within(dialog).getByRole("button", { name: "Close filters" });
    const done = within(dialog).getByRole("button", { name: "Done" });
    // Shift+Tab from the heading wraps to the last control; Tab from it wraps to the first.
    await act(async () =>
      within(dialog).getByRole("heading", { name: "Filters" }).focus(),
    );
    await user.tab({ shift: true });
    expect(document.activeElement).toBe(done);
    await user.tab();
    expect(document.activeElement).toBe(close);
    await user.click(close);
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(screen.getByRole("button", { name: "Filters (1)" })).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Filters (1)" }));
    dialog = await screen.findByRole("dialog", { name: "Filters" });
    expect(
      (
        within(dialog).getByRole("textbox", {
          name: "Actor ID",
        }) as HTMLInputElement
      ).value,
    ).toBe("a");
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(f.calls.mock.lastCall?.[0].actorUserId).toBe("a");
    expect(document.activeElement).toBe(
      screen.getByRole("button", { name: "Filters (1)" }),
    );
  });

  it("shows the range error outside the sheet and closes safely when the viewport grows", async () => {
    const viewport = mockViewport(390);
    const f = auditRoutes(() => ({ records: [records[0]] }));
    mount(f.routes);
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "Filters" }));
    const dialog = await screen.findByRole("dialog", { name: "Filters" });
    await user.type(within(dialog).getByLabelText("From (UTC)"), "2026-10-20");
    await user.click(within(dialog).getByRole("button", { name: "Done" }));
    expect(
      screen.getByText("Choose both dates with the end on or after the start."),
    ).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Filters (1)" }));
    await screen.findByRole("dialog", { name: "Filters" });
    viewport.resize(768);
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(
      (screen.getByLabelText("From (UTC)") as HTMLInputElement).value,
    ).toBe("2026-10-20");
    expect(document.activeElement).toBe(
      screen.getByRole("heading", { name: "Filters" }),
    );
  });
});
