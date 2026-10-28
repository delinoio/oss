// SPDX-License-Identifier: Apache-2.0
import { describe, expect, it, rs } from "@rstest/core";
import { render, screen, waitFor, within } from "@testing-library/react";
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
  OperatorService,
  Role,
  type ListInstanceAuditRequest,
} from "@tairouter/client";
import { ApiProvider } from "../api";
import { AdminAuthority } from "./common";
import { Audit } from "./reporting";

function mount(
  respond: (request: ListInstanceAuditRequest) => object,
  calls = rs.fn<(request: ListInstanceAuditRequest) => void>(),
) {
  const organization = rs.fn();
  const transport = createRouterTransport((router: ConnectRouter) => {
    router.service(AccountService, {
      getSession: () => ({ isInstanceOperator: true }),
    });
    router.service(AuditService, {
      listOrganizationAudit: () => {
        organization();
        return {};
      },
    });
    router.service(OperatorService, {
      listInstanceAudit: (request) => {
        calls(request);
        return respond(request);
      },
    });
  });
  render(
    <ApiProvider
      queryClient={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
      rpcTransport={transport}
    >
      <AdminAuthority.Provider
        value={{
          organizationId: "organization-a",
          role: Role.OWNER,
          onDenied: () => {},
        }}
      >
        <Audit operator />
      </AdminAuthority.Provider>
    </ApiProvider>,
  );
  return { calls, organization };
}
const occurredAt = timestampFromDate(new Date("2026-10-27T09:42:00Z"));
const rangeError = "Choose both dates with the end on or after the start.";

describe("instance audit presentation", () => {
  it("renders the header, scope line and separate Filters and Audit records cards", async () => {
    const f = mount(() => ({ records: [] }));
    expect(
      await screen.findByRole("heading", { level: 1, name: "Instance audit" }),
    ).toBeTruthy();
    expect(screen.getAllByRole("heading", { level: 1 })).toHaveLength(1);
    expect(
      screen.getByText("Instance-setting events. Safe metadata only."),
    ).toBeTruthy();
    expect(
      screen.getByText("Instance operators only · No organization drilldown"),
    ).toBeTruthy();
    const filters = screen.getByRole("region", { name: "Filters" });
    const records = screen.getByRole("region", { name: "Audit records" });
    expect(filters.contains(records)).toBe(false);
    expect(
      within(records).getByRole("heading", { level: 2, name: "Audit records" }),
    ).toBeTruthy();
    expect(filters.querySelector("details")?.hasAttribute("open")).toBe(true);
    expect(
      within(filters)
        .getAllByRole("textbox")
        .map((el) => el.tagName),
    ).toEqual(["INPUT"]);
    expect(
      Array.from(filters.querySelectorAll("label")).map((l) =>
        l.textContent?.replace(/All .*|Exact actor ID/g, "").slice(0, 20),
      ),
    ).toEqual(
      expect.arrayContaining([
        expect.stringContaining("Actor ID"),
        expect.stringContaining("Action"),
        expect.stringContaining("Outcome"),
        expect.stringContaining("From (UTC)"),
        expect.stringContaining("Through (UTC)"),
      ]),
    );
    expect(
      screen.getByText("Leave both dates empty for all retained records."),
    ).toBeTruthy();
    expect(screen.queryByRole("button", { name: /apply/i })).toBeNull();
    expect(
      within(filters).getByRole("button", { name: "Clear filters" }),
    ).toBeTruthy();
    // Action options keep every enum value, including UNSPECIFIED.
    const options = within(
      within(filters).getByRole("combobox", { name: "Action" }),
    ).getAllByRole("option");
    expect(options.length).toBe(
      1 +
        Object.values(AuditAction).filter((v) => typeof v === "number").length,
    );
    expect(options.map((o) => o.textContent)).toEqual(
      expect.arrayContaining([
        "Instance settings updated",
        "Price override set",
      ]),
    );
    expect(
      within(within(filters).getByRole("combobox", { name: "Outcome" }))
        .getAllByRole("option")
        .map((o) => o.textContent),
    ).toEqual(["All outcomes", "Unknown", "Succeeded", "Failed", "Denied"]);
    expect(f.organization).not.toHaveBeenCalled();
    expect(f.calls.mock.lastCall?.[0]).toMatchObject({
      page: { pageSize: 100, pageToken: "" },
    });
    expect(f.calls.mock.lastCall?.[0]).not.toHaveProperty("organizationId");
  });

  it("renders records as articles with badges, a metadata grid and safe fallbacks", async () => {
    const longId = "resource-" + "x".repeat(120);
    mount(() => ({
      records: [
        {
          id: "1",
          occurredAt,
          actorUserId: "actor-a",
          action: AuditAction.INSTANCE_SETTINGS_UPDATED,
          resourceId: longId,
          outcome: AuditOutcome.SUCCEEDED,
          requestId: "req-1",
        },
        { id: "2", action: AuditAction.PRICE_OVERRIDE_SET },
        {
          id: "3",
          action: 9999 as AuditAction,
          outcome: 9999 as AuditOutcome,
        },
        {
          id: "4",
          action: AuditAction.PRICE_OVERRIDE_DELETED,
          outcome: AuditOutcome.DENIED,
        },
        {
          id: "5",
          action: AuditAction.UNSPECIFIED,
          outcome: AuditOutcome.FAILED,
        },
      ],
    }));
    const rows = await screen.findAllByRole("article");
    expect(rows).toHaveLength(5);
    expect(
      within(rows[0]).getByRole("heading", {
        level: 3,
        name: "Instance settings updated",
      }),
    ).toBeTruthy();
    expect(within(rows[0]).getByText("Succeeded")).toBeTruthy();
    for (const term of ["Time", "Actor", "Target", "Request ID"])
      expect(within(rows[0]).getByText(term).tagName).toBe("DT");
    expect(within(rows[0]).getByText(longId)).toBeTruthy();
    expect(within(rows[0]).getByText("req-1")).toBeTruthy();
    expect(within(rows[0]).queryByText(/Unknown$/)).toBeNull();
    expect(within(rows[0]).getByText(/\(.+\)$/).textContent).toContain(
      Intl.DateTimeFormat().resolvedOptions().timeZone,
    );
    // Absent values use fallbacks, never success or a guessed identity.
    expect(
      within(rows[1]).getByRole("heading", { name: "Price override set" }),
    ).toBeTruthy();
    expect(within(rows[1]).getByText("Deleted user")).toBeTruthy();
    expect(within(rows[1]).getAllByText("Unknown")).toHaveLength(3);
    expect(within(rows[1]).queryByText("Succeeded")).toBeNull();
    expect(within(rows[1]).queryByText("Request ID")).toBeNull();
    expect(
      within(rows[2]).getByRole("heading", { name: "Unknown" }),
    ).toBeTruthy();
    expect(within(rows[2]).queryByText("Succeeded")).toBeNull();
    expect(
      within(rows[3]).getByRole("heading", { name: "Price override deleted" }),
    ).toBeTruthy();
    expect(within(rows[3]).getByText("Denied")).toBeTruthy();
    expect(
      within(rows[4]).getByRole("heading", { name: "Unknown action" }),
    ).toBeTruthy();
    expect(within(rows[4]).getByText("Failed")).toBeTruthy();
    // Server order is kept and there is no drilldown or copy control.
    expect(rows.map((r) => r.querySelector("h3")?.textContent)).toEqual([
      "Instance settings updated",
      "Price override set",
      "Unknown",
      "Price override deleted",
      "Unknown action",
    ]);
    expect(screen.queryByRole("link")).toBeNull();
    expect(screen.queryByRole("button", { name: /copy/i })).toBeNull();
  });

  it("sends conjunctive filters, resets the cursor and clears predicates", async () => {
    const f = mount((request) => ({
      records: [{ id: "unexpected", outcome: AuditOutcome.SUCCEEDED }],
      page: { nextPageToken: request.page?.pageToken ? "" : "next-1" },
    }));
    const user = userEvent.setup();
    await screen.findByRole("article");
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
    await waitFor(() =>
      expect(f.calls.mock.lastCall?.[0]).toMatchObject({
        actorUserId: "actor-a",
        action: AuditAction.KEY_REVOKED,
        outcome: AuditOutcome.DENIED,
      }),
    );
    // A returned row stays visible even when it would not match the filters.
    expect(await screen.findByRole("article")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "First page" })).toBeNull();
    await user.click(await screen.findByRole("button", { name: "Next page" }));
    await waitFor(() =>
      expect(f.calls.mock.lastCall?.[0].page?.pageToken).toBe("next-1"),
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
      expect(request.action).toBeUndefined();
      expect(request.outcome).toBeUndefined();
      expect(request.actorUserId).toBeUndefined();
      expect(request.timeRange).toBeUndefined();
      expect(request.page?.pageToken).toBe("");
    });
    expect(screen.getByRole("textbox", { name: "Actor ID" })).toHaveProperty(
      "value",
      "",
    );
    expect(f.organization).not.toHaveBeenCalled();
  });

  it("validates the date pair, blocks queries and resumes when corrected", async () => {
    const f = mount(() => ({ records: [{ id: "1" }] }));
    const user = userEvent.setup();
    await screen.findByRole("article");
    expect(f.calls.mock.lastCall?.[0].timeRange).toBeUndefined();
    const before = f.calls.mock.calls.length;
    await user.type(screen.getByLabelText("From (UTC)"), "2026-10-01");
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toBe(rangeError);
    expect(screen.queryByRole("article")).toBeNull();
    for (const name of ["From (UTC)", "Through (UTC)"]) {
      const input = screen.getByLabelText(name);
      expect(input.getAttribute("aria-invalid")).toBe("true");
      expect(input.getAttribute("aria-describedby")).toContain(alert.id);
    }
    expect(f.calls.mock.calls.length).toBe(before);
    await user.type(screen.getByLabelText("Through (UTC)"), "2026-10-27");
    await waitFor(() => {
      const range = f.calls.mock.lastCall![0].timeRange!;
      expect(range.start?.seconds).toBe(BigInt(Date.UTC(2026, 9, 1) / 1000));
      expect(Number(range.end!.seconds) * 1000 + range.end!.nanos / 1e6).toBe(
        Date.UTC(2026, 9, 27, 23, 59, 59, 999),
      );
    });
    expect(await screen.findByRole("article")).toBeTruthy();
    expect(screen.queryByRole("alert")).toBeNull();
    expect(
      screen.getByLabelText("From (UTC)").getAttribute("aria-invalid"),
    ).toBeNull();
    // A reversed range blocks again and preserves the drafts.
    const blocked = f.calls.mock.calls.length;
    await user.clear(screen.getByLabelText("Through (UTC)"));
    await user.type(screen.getByLabelText("Through (UTC)"), "2026-09-01");
    expect((await screen.findByRole("alert")).textContent).toBe(rangeError);
    expect(screen.getByLabelText("From (UTC)")).toHaveProperty(
      "value",
      "2026-10-01",
    );
    expect(f.calls.mock.calls.length).toBe(blocked);
    await user.click(screen.getByRole("button", { name: "Clear filters" }));
    await waitFor(() =>
      expect(f.calls.mock.lastCall?.[0].timeRange).toBeUndefined(),
    );
    expect(screen.getByLabelText("From (UTC)")).toHaveProperty("value", "");
    expect(screen.getByLabelText("Through (UTC)")).toHaveProperty("value", "");
  });

  it("shows distinct empty states with recovery controls", async () => {
    const f = mount((request) => ({
      records: [],
      page: { nextPageToken: request.page?.pageToken ? "" : "next-1" },
    }));
    const user = userEvent.setup();
    expect(await screen.findByText("No audit records yet.")).toBeTruthy();
    expect(
      screen.getByText("Instance-setting events appear here when available."),
    ).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Next page" }));
    expect(
      await screen.findByText(
        "No audit records match. Clear filters or check another page.",
      ),
    ).toBeTruthy();
    expect(screen.getByRole("button", { name: "First page" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Clear filters" })).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "First page" }));
    await waitFor(() =>
      expect(f.calls.mock.lastCall?.[0].page?.pageToken).toBe(""),
    );
    await user.selectOptions(
      screen.getByRole("combobox", { name: "Outcome" }),
      String(AuditOutcome.FAILED),
    );
    expect(
      await screen.findByText(
        "No audit records match. Clear filters or check another page.",
      ),
    ).toBeTruthy();
  });

  it("shows a loading status without fake rows, then distinct failure states", async () => {
    let release: () => void = () => {};
    const gate = new Promise<void>((resolve) => (release = resolve));
    mount(async () => {
      await gate;
      return { records: [{ id: "1" }] };
    });
    expect((await screen.findByRole("status")).textContent).toBe("Loading…");
    expect(screen.queryByRole("article")).toBeNull();
    release();
    expect(await screen.findByRole("article")).toBeTruthy();
    expect(screen.queryByText("No audit records yet.")).toBeNull();
  });

  it.each([
    [
      Code.Unimplemented,
      "This capability is not available on this server yet.",
    ],
    [
      Code.PermissionDenied,
      "Your permissions changed. This action is no longer available.",
    ],
    [
      Code.Unauthenticated,
      "Your session expired. Sign in again before continuing.",
    ],
    [
      Code.Unavailable,
      "The service is unavailable. No change is confirmed. Try again when connected.",
    ],
  ])(
    "shows a safe error for code %s instead of an empty list",
    async (code, message) => {
      let fail = true;
      const f = mount(() => {
        if (fail) throw new ConnectError("internal detail", code);
        return { records: [{ id: "1" }] };
      });
      const user = userEvent.setup();
      const alert = await screen.findByRole("alert");
      expect(alert.textContent).toContain(message);
      expect(alert.textContent).not.toContain("internal detail");
      expect(screen.queryByText("No audit records yet.")).toBeNull();
      expect(screen.queryByRole("article")).toBeNull();
      await user.selectOptions(
        screen.getByRole("combobox", { name: "Outcome" }),
        String(AuditOutcome.DENIED),
      );
      fail = false;
      await user.click(
        await screen.findByRole("button", { name: "Try again" }),
      );
      expect(await screen.findByRole("article")).toBeTruthy();
      expect(f.calls.mock.lastCall?.[0].outcome).toBe(AuditOutcome.DENIED);
      expect(f.organization).not.toHaveBeenCalled();
    },
  );
});
