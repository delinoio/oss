// SPDX-License-Identifier: Apache-2.0
import { afterEach, describe, expect, it, rs } from "@rstest/core";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createRouterTransport, type ConnectRouter } from "@connectrpc/connect";
import { QueryClient } from "@tanstack/react-query";
import {
  AccountService,
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

const originalMatchMedia = window.matchMedia;
afterEach(() => {
  window.matchMedia = originalMatchMedia;
  rs.useRealTimers();
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
function spies() {
  return {
    list: rs.fn<(request: ListUsageRequest) => void>(),
    summary: rs.fn<(request: GetUsageSummaryRequest) => void>(),
    daily: rs.fn<(request: ListDailyUsageRequest) => void>(),
  };
}
type Spies = ReturnType<typeof spies>;
function usageRoutes(calls: Spies) {
  return (router: ConnectRouter) => {
    router.service(UsageService, {
      listUsage: (request) => {
        calls.list(request);
        return {
          records: [],
          page: { nextPageToken: request.page?.pageToken ? "" : "next-r" },
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
          page: { nextPageToken: request.page?.pageToken ? "" : "next-d" },
        };
      },
    });
  };
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
  const tree = (scope: string) => (
    <ApiProvider queryClient={client} rpcTransport={transport}>
      <AdminAuthority.Provider
        value={{ organizationId: scope, role: Role.OWNER, onDenied: () => {} }}
      >
        <div key={scope}>
          <OrganizationUsage />
        </div>
      </AdminAuthority.Provider>
    </ApiProvider>
  );
  const view = render(tree("organization-a"));
  return { rerenderScope: (scope: string) => view.rerender(tree(scope)) };
}
async function openSheet(user: ReturnType<typeof userEvent.setup>) {
  await user.click(await screen.findByRole("button", { name: /^Filters/ }));
  return screen.findByRole("dialog", { name: "Usage filters" });
}

describe("organization usage filters sheet", () => {
  it("shows a Filters button with the UTC range below 768px and keeps inline filters at 768px", async () => {
    rs.useFakeTimers({ toFake: ["Date"] });
    rs.setSystemTime(new Date("2026-10-27T00:30:00Z"));
    const phone = mockViewport(767);
    mount(usageRoutes(spies()));
    const trigger = await screen.findByRole("button", { name: "Filters" });
    expect(trigger.getAttribute("aria-haspopup")).toBe("dialog");
    // The range is visible beside the button even when no filter is active.
    expect(screen.getByText("2026-10-20–2026-10-27 · UTC dates")).toBeTruthy();
    expect(screen.queryByLabelText("From (UTC)")).toBeNull();
    expect(screen.queryByRole("textbox", { name: "Member ID" })).toBeNull();
    phone.resize(768);
    expect(await screen.findByLabelText("From (UTC)")).toBeTruthy();
    expect(screen.queryByRole("button", { name: /^Filters/ })).toBeNull();
    expect(screen.getByText("Filters").closest("details")!.open).toBe(true);
  });

  it("opens a modal Usage filters sheet with live controls and focus on the heading", async () => {
    mockViewport(390);
    const calls = spies();
    mount(usageRoutes(calls));
    const user = userEvent.setup();
    const trigger = await screen.findByRole("button", { name: "Filters" });
    await user.click(trigger);
    const dialog = await screen.findByRole("dialog", { name: "Usage filters" });
    expect(document.activeElement).toBe(
      within(dialog).getByRole("heading", { name: "Usage filters" }),
    );
    expect(
      within(dialog).getByText(
        "UTC dates · Changes update the report immediately.",
      ),
    ).toBeTruthy();
    expect(
      within(dialog).getByRole("button", { name: "Close usage filters" }),
    ).toBeTruthy();
    expect(within(dialog).queryByRole("button", { name: /apply/i })).toBeNull();
    const member = within(dialog).getByRole("textbox", { name: "Member ID" });
    expect(member.getAttribute("placeholder")).toBe("All members");
    expect(
      within(dialog)
        .getByRole("textbox", { name: "Model" })
        .getAttribute("placeholder"),
    ).toBe("All models");
    expect(
      within(dialog)
        .getByRole("textbox", { name: "Provider" })
        .getAttribute("placeholder"),
    ).toBe("All providers");
    expect(within(dialog).getAllByRole("combobox")).toHaveLength(1);
    await user.type(member, "member-a");
    await user.selectOptions(
      within(dialog).getByRole("combobox", { name: "Outcome" }),
      String(RequestOutcome.UNSPECIFIED),
    );
    // Edits take effect immediately on the shared filter set, before any dismissal.
    await waitFor(() => {
      for (const call of Object.values(calls))
        expect(call.mock.lastCall?.[0]).toMatchObject({
          userId: "member-a",
          outcome: RequestOutcome.UNSPECIFIED,
        });
    });
    await user.click(within(dialog).getByRole("button", { name: "Done" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(document.activeElement).toBe(
      screen.getByRole("button", { name: "Filters (2)" }),
    );
  });

  it("counts Member ID, Model, Provider, Outcome and a changed date pair once", async () => {
    mockViewport(390);
    mount(usageRoutes(spies()));
    const user = userEvent.setup();
    const dialog = await openSheet(user);
    const count = () =>
      screen.getAllByRole("button", { name: /^Filters/ })[0].textContent;
    expect(count()).toBe("Filters");
    await user.type(
      within(dialog).getByRole("textbox", { name: "Model" }),
      "m",
    );
    expect(count()).toBe("Filters (1)");
    await user.type(
      within(dialog).getByRole("textbox", { name: "Provider" }),
      "p",
    );
    await user.type(
      within(dialog).getByRole("textbox", { name: "Member ID" }),
      "u",
    );
    await user.selectOptions(
      within(dialog).getByRole("combobox", { name: "Outcome" }),
      String(RequestOutcome.UNSPECIFIED),
    );
    expect(count()).toBe("Filters (4)");
    // Changing either date, or both, adds one.
    const from = within(dialog).getByLabelText("From (UTC)");
    await user.clear(from);
    await user.type(from, "2026-01-01");
    expect(count()).toBe("Filters (5)");
    const through = within(dialog).getByLabelText("Through (UTC)");
    await user.clear(through);
    await user.type(through, "2026-01-02");
    expect(count()).toBe("Filters (5)");
    // Clear filters supplies a fresh default pair and returns the count to zero.
    await user.click(
      within(dialog).getByRole("button", { name: "Clear filters" }),
    );
    expect(screen.getByRole("dialog", { name: "Usage filters" })).toBeTruthy();
    expect(count()).toBe("Filters");
    expect(
      (
        within(dialog).getByRole("textbox", {
          name: "Model",
        }) as HTMLInputElement
      ).value,
    ).toBe("");
  });

  it("counts an invalid date change and shows the announced error tied to both dates", async () => {
    mockViewport(390);
    const calls = spies();
    mount(usageRoutes(calls));
    const user = userEvent.setup();
    const dialog = await openSheet(user);
    await screen.findByRole("dialog", { name: "Usage filters" });
    await waitFor(() => expect(calls.daily).toHaveBeenCalled());
    const before = Object.values(calls).map((call) => call.mock.calls.length);
    await user.clear(within(dialog).getByLabelText("Through (UTC)"));
    const alert = within(dialog).getByRole("alert");
    expect(alert.textContent).toBe(
      "Choose an end date on or after the start date.",
    );
    for (const name of ["From (UTC)", "Through (UTC)"]) {
      const input = within(dialog).getByLabelText(name);
      expect(input.getAttribute("aria-invalid")).toBe("true");
      expect(input.getAttribute("aria-describedby")).toBe(alert.id);
    }
    expect(Object.values(calls).map((call) => call.mock.calls.length)).toEqual(
      before,
    );
    await user.click(within(dialog).getByRole("button", { name: "Done" }));
    expect(screen.getByRole("button", { name: "Filters (1)" })).toBeTruthy();
    expect(
      screen.getByText("Choose an end date on or after the start date."),
    ).toBeTruthy();
    expect(screen.queryByRole("heading", { name: "Usage summary" })).toBeNull();
    // Clear stays available from the sheet and restores every region.
    await user.click(screen.getByRole("button", { name: "Filters (1)" }));
    await user.click(
      await screen.findByRole("button", { name: "Clear filters" }),
    );
    await user.click(screen.getByRole("button", { name: "Done" }));
    expect(screen.getByRole("button", { name: "Filters" })).toBeTruthy();
    await screen.findByRole("heading", { name: "Request metadata" });
  });

  it("dismisses with X and Escape without reverting edits and traps Tab", async () => {
    mockViewport(390);
    const calls = spies();
    mount(usageRoutes(calls));
    const user = userEvent.setup();
    let dialog = await openSheet(user);
    await user.type(
      within(dialog).getByRole("textbox", { name: "Provider" }),
      "prov",
    );
    const close = within(dialog).getByRole("button", {
      name: "Close usage filters",
    });
    const done = within(dialog).getByRole("button", { name: "Done" });
    await act(async () =>
      within(dialog).getByRole("heading", { name: "Usage filters" }).focus(),
    );
    await user.tab({ shift: true });
    expect(document.activeElement).toBe(done);
    await user.tab();
    expect(document.activeElement).toBe(close);
    await user.click(close);
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(document.activeElement).toBe(
      screen.getByRole("button", { name: "Filters (1)" }),
    );
    dialog = await openSheet(user);
    expect(
      (
        within(dialog).getByRole("textbox", {
          name: "Provider",
        }) as HTMLInputElement
      ).value,
    ).toBe("prov");
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("dialog")).toBeNull();
    await waitFor(() =>
      expect(calls.list.mock.lastCall?.[0].providerId).toBe("prov"),
    );
    expect(document.activeElement).toBe(
      screen.getByRole("button", { name: "Filters (1)" }),
    );
  });

  it("resets both cursors on a sheet edit and closes safely when the viewport grows", async () => {
    const viewport = mockViewport(390);
    const calls = spies();
    mount(usageRoutes(calls));
    const user = userEvent.setup();
    await screen.findByRole("navigation", { name: "Request metadata pages" });
    await user.click(screen.getByRole("button", { name: "Next requests" }));
    await user.click(screen.getByRole("button", { name: "Next daily usage" }));
    await waitFor(() => {
      expect(calls.list.mock.lastCall?.[0].page?.pageToken).toBe("next-r");
      expect(calls.daily.mock.lastCall?.[0].page?.pageToken).toBe("next-d");
    });
    const dialog = await openSheet(user);
    await user.type(
      within(dialog).getByRole("textbox", { name: "Member ID" }),
      "u",
    );
    await waitFor(() => {
      expect(calls.list.mock.lastCall?.[0].page?.pageToken).toBe("");
      expect(calls.daily.mock.lastCall?.[0].page?.pageToken).toBe("");
    });
    viewport.resize(1024);
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(
      (screen.getByRole("textbox", { name: "Member ID" }) as HTMLInputElement)
        .value,
    ).toBe("u");
    expect(document.activeElement).toBe(screen.getByText("Filters"));
  });

  it("closes the sheet when the organization scope changes and drops its filters", async () => {
    mockViewport(390);
    const view = mount(usageRoutes(spies()));
    const user = userEvent.setup();
    const dialog = await openSheet(user);
    await user.type(
      within(dialog).getByRole("textbox", { name: "Member ID" }),
      "u",
    );
    view.rerenderScope("organization-b");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(screen.getByRole("button", { name: "Filters" })).toBeTruthy();
  });
});
