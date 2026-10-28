// SPDX-License-Identifier: Apache-2.0
import { afterEach, describe, expect, it, rs } from "@rstest/core";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createRouterTransport, type ConnectRouter } from "@connectrpc/connect";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { QueryClient } from "@tanstack/react-query";
import {
  AccountService,
  PriceSource,
  RequestOutcome,
  Role,
  UsageService,
} from "@aiprouter/client";
import { ApiProvider } from "../api";
import { AdminAuthority } from "./common";
import { OrganizationUsage } from "./reporting";

const originalMatchMedia = window.matchMedia;
afterEach(() => {
  window.matchMedia = originalMatchMedia;
});
// jsdom has no layout, so the table layout is selected through a width mock.
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
const longRequestId = `req-${"0123456789abcdef".repeat(8)}`;
const priced = {
  id: "record-priced",
  modelId: "model-a",
  userId: "member-a",
  occurredAt: timestampFromDate(new Date("2026-10-26T08:09:10Z")),
  outcome: RequestOutcome.SUCCEEDED,
  estimatedCost: { currency: "USD", decimalAmount: "0.014" },
  requestId: "req-demo-001",
  appliedPrice: {
    source: PriceSource.BUNDLED,
    sourceRevision: "demo-revision-2026-10",
    serviceTier: "default",
  },
};
const sparse = {
  id: "record-sparse",
  modelId: "model-b",
  outcome: RequestOutcome.UNSPECIFIED,
  requestId: longRequestId,
};
function mount(
  list: () => { records: object[]; page: object },
  extra: { daily?: () => object } = {},
) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const calls = {
    list: rs.fn(),
    daily: rs.fn(),
    summary: rs.fn(),
  };
  const transport = createRouterTransport((router: ConnectRouter) => {
    router.service(AccountService, {
      getSession: () => ({ isInstanceOperator: false }),
    });
    router.service(UsageService, {
      listUsage: () => {
        calls.list();
        return list();
      },
      getUsageSummary: () => {
        calls.summary();
        return { summary: { requestCount: 5n, knownCosts: [] } };
      },
      listDailyUsage: () => {
        calls.daily();
        return extra.daily?.() ?? { days: [], page: {} };
      },
    });
  });
  const tree = (role: Role) => (
    <ApiProvider queryClient={client} rpcTransport={transport}>
      <AdminAuthority.Provider
        value={{ organizationId: "organization-a", role, onDenied: () => {} }}
      >
        {/* Mirrors OrganizationBoundary, which keys the workspace by role. */}
        <div key={role}>
          <OrganizationUsage />
        </div>
      </AdminAuthority.Provider>
    </ApiProvider>
  );
  const view = render(tree(Role.OWNER));
  return { client, calls, view, tree };
}
async function openEstimate(index = 0) {
  const user = userEvent.setup();
  const buttons = await screen.findAllByRole("button", {
    name: "View estimate details",
  });
  await user.click(buttons[index]);
  const dialog = await screen.findByRole("dialog", {
    name: "Request estimate",
  });
  return { user, dialog, trigger: buttons[index] };
}

describe("request estimate dialog", () => {
  it("shows the stored estimate as a definition list without extra requests", async () => {
    mockViewport(1440);
    const { calls } = mount(() => ({ records: [priced, sparse], page: {} }));
    await screen.findByRole("heading", { name: "Request metadata" });
    await screen.findAllByRole("button", { name: "View estimate details" });
    const before = {
      list: calls.list.mock.calls.length,
      daily: calls.daily.mock.calls.length,
      summary: calls.summary.mock.calls.length,
    };
    const { dialog } = await openEstimate();
    // Opening reads the selected record locally and sends no further request.
    expect(calls.list.mock.calls.length).toBe(before.list);
    expect(calls.daily.mock.calls.length).toBe(before.daily);
    expect(calls.summary.mock.calls.length).toBe(before.summary);
    expect(dialog.classList.contains("admin-dialog-wide")).toBe(true);
    expect(within(dialog).getByText("Estimated cost")).toBeTruthy();
    expect(within(dialog).getByText("USD 0.014")).toBeTruthy();
    const terms = within(dialog)
      .getAllByRole("term")
      .map((term) => term.textContent);
    expect(terms).toEqual([
      "Request ID",
      "Price provenance",
      "Revision",
      "Service tier",
      "Error code",
    ]);
    const definitions = within(dialog)
      .getAllByRole("definition")
      .map((definition) => definition.textContent);
    expect(definitions).toEqual([
      "req-demo-001",
      "Bundled",
      "demo-revision-2026-10",
      "default",
      "None reported",
    ]);
    const message =
      "This estimate was fixed when the request was processed. Later price updates do not change it.";
    const note = within(dialog).getByText(message);
    expect(dialog.getAttribute("aria-describedby")).toBe(note.id);
    expect(document.activeElement).toBe(
      within(dialog).getByRole("button", { name: "Close" }),
    );
  });

  it("falls back to Unknown for a missing price and keeps long request IDs", async () => {
    mockViewport(1440);
    mount(() => ({ records: [priced, sparse], page: {} }));
    const { dialog } = await openEstimate(1);
    const definitions = within(dialog)
      .getAllByRole("definition")
      .map((definition) => definition.textContent);
    expect(definitions).toEqual([
      longRequestId,
      "Unknown",
      "Unknown",
      "Unknown",
      "None reported",
    ]);
    // An unknown estimate is never rendered as a zero cost.
    expect(
      within(dialog).getByText("Unknown", { selector: "strong" }),
    ).toBeTruthy();
    expect(within(dialog).queryByText(/0\.0+/)).toBeNull();
  });

  it("shows a recorded error code", async () => {
    mockViewport(1440);
    mount(() => ({
      records: [{ ...priced, errorCode: "upstream_timeout" }],
      page: {},
    }));
    const { dialog } = await openEstimate();
    expect(within(dialog).getByText("upstream_timeout")).toBeTruthy();
    expect(within(dialog).queryByText("None reported")).toBeNull();
  });

  it("traps Tab, closes with Escape and returns focus to the row action", async () => {
    mockViewport(1440);
    mount(() => ({ records: [priced, sparse], page: {} }));
    const { user, dialog, trigger } = await openEstimate(1);
    const close = within(dialog).getByRole("button", { name: "Close" });
    const x = within(dialog).getByRole("button", { name: "Close dialog" });
    expect(document.activeElement).toBe(close);
    // Close is the last control, so Tab wraps to the first one.
    await user.tab();
    expect(document.activeElement).toBe(x);
    await user.tab({ shift: true });
    expect(document.activeElement).toBe(close);
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(document.activeElement).toBe(trigger);
  });

  it("closes from the accessible X and Close buttons", async () => {
    mockViewport(1440);
    mount(() => ({ records: [priced], page: {} }));
    const first = await openEstimate();
    await first.user.click(
      within(first.dialog).getByRole("button", { name: "Close dialog" }),
    );
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    const second = await openEstimate();
    await second.user.click(
      within(second.dialog).getByRole("button", { name: "Close" }),
    );
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });

  it("returns focus to the Request metadata heading when the row is gone", async () => {
    mockViewport(1440);
    let records: object[] = [priced, sparse];
    const { client } = mount(() => ({ records, page: {} }));
    const { user, dialog } = await openEstimate();
    // A refetch removes the invoking row while the dialog still shows its record.
    records = [sparse];
    await act(async () => {
      await client.invalidateQueries();
    });
    await waitFor(() =>
      expect(document.querySelectorAll("tbody tr").length).toBe(1),
    );
    expect(within(dialog).getByText("req-demo-001")).toBeTruthy();
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    const heading = screen.getByRole("heading", { name: "Request metadata" });
    expect(heading.getAttribute("tabindex")).toBe("-1");
    expect(document.activeElement).toBe(heading);
  });

  it("closes the dialog when the boundary remounts the workspace", async () => {
    mockViewport(1440);
    const { view, tree } = mount(() => ({ records: [priced], page: {} }));
    await openEstimate();
    view.rerender(tree(Role.ADMINISTRATOR));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });
});
