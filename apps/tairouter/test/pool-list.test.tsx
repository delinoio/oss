// SPDX-License-Identifier: Apache-2.0
import { readFileSync } from "node:fs";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, it, expect, afterEach } from "@rstest/core";
import {
  createRouterTransport,
  Code,
  ConnectError,
  type ConnectRouter,
} from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";
import { QueryClient } from "@tanstack/react-query";
import {
  ProviderService,
  PoolService,
  PoolSchema,
  RoutingService,
  OrganizationSchema,
  ErrorDetailSchema,
  Role,
  ConnectionState,
} from "@tairouter/client";
import { ApiProvider } from "../src/api";
import { PoolsPage } from "../src/routing";
import {
  InventoryState,
  inventoryState,
  orderedMembers,
} from "../src/pool-list";

const organization = create(OrganizationSchema, {
  id: "org-1",
  name: "Acme Studio",
  role: Role.MEMBER,
});
const otherOrganization = create(OrganizationSchema, {
  id: "org-2",
  name: "Other Studio",
  role: Role.MEMBER,
});

type Member = { connectionId: string; priority: number; weight: number };
type PoolFixture = {
  id: string;
  name: string;
  strategy: number;
  members: Member[];
};
const member = (
  connectionId: string,
  priority: number,
  weight = 1,
): Member => ({ connectionId, priority, weight });
const pool = (
  id: string,
  name: string,
  members: Member[] = [],
  strategy = 1,
): PoolFixture => ({ id, name, strategy, members });
const connection = (id: string, name: string) => ({
  id,
  name,
  state: ConnectionState.READY,
});

type Page = { pools: PoolFixture[]; next?: string };
type Fixture = {
  pools?: (request: {
    organizationId?: string;
    page?: { pageSize?: number; pageToken?: string };
  }) => Page | Promise<Page>;
  connections?: (request: {
    organizationId?: string;
    page?: { pageToken?: string };
  }) => object | Promise<object>;
  organization?: typeof organization;
};
function mount(fixture: Fixture = {}) {
  const removed = new Set<string>();
  const cache = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const requests = {
    list: [] as { pageSize?: number; pageToken?: string }[],
    connections: 0,
    pools: 0,
    deletes: 0,
  };
  const transport = createRouterTransport((r: ConnectRouter) => {
    r.service(PoolService, {
      listPools: (async (request: {
        organizationId?: string;
        page?: { pageSize?: number; pageToken?: string };
      }) => {
        requests.list.push({
          pageSize: request.page?.pageSize,
          pageToken: request.page?.pageToken,
        });
        const page = fixture.pools
          ? await fixture.pools(request)
          : { pools: [] };
        return {
          pools: page.pools.filter((item) => !removed.has(item.id)),
          page: page.next ? { nextPageToken: page.next } : undefined,
        };
      }) as never,
      deletePool: ((request: { poolId: string }) => {
        requests.deletes += 1;
        removed.add(request.poolId);
        return {};
      }) as never,
    });
    r.service(ProviderService, {
      listProviderConnections: ((request: never) => {
        requests.connections += 1;
        return (
          fixture.connections?.(request) ?? {
            connections: [connection("c1", "Work account")],
          }
        );
      }) as never,
    });
    r.service(RoutingService, { listRoutes: () => ({ routes: [] }) });
  });
  const element = (org: typeof organization) => (
    <ApiProvider queryClient={cache} rpcTransport={transport}>
      <PoolsPage organization={org} session={undefined as never} />
    </ApiProvider>
  );
  const view = render(element(fixture.organization ?? organization));
  return {
    ...view,
    cache,
    requests,
    switchOrganization: (next: typeof organization) =>
      view.rerender(element(next)),
  };
}
const card = async (name: string) =>
  (await screen.findByRole("heading", { level: 2, name })).closest(
    "section",
  ) as HTMLElement;
const memberItems = (scope: HTMLElement) =>
  within(scope).getAllByRole("listitem");
function setOnline(value: boolean) {
  Object.defineProperty(navigator, "onLine", { configurable: true, value });
}
afterEach(() => {
  setOnline(true);
  window.dispatchEvent(new Event("online"));
});
const button = (name: string) =>
  screen.getByRole("button", { name }) as HTMLButtonElement;

describe("pool list hierarchy", () => {
  it("renders one h1, the scope, the notice, one primary action and one card per pool", async () => {
    mount({
      pools: () => ({
        pools: [pool("p1", "Team pool"), pool("p2", "Other pool")],
      }),
    });
    await card("Team pool");
    expect(screen.getAllByRole("heading", { level: 1 })).toHaveLength(1);
    expect(
      screen.getByRole("heading", { level: 1, name: "Account pools" }),
    ).toBeTruthy();
    expect(screen.getByText("Only yours in Acme Studio.")).toBeTruthy();
    expect(
      screen.getByText(/Pools group your own connections\. Strategy/),
    ).toBeTruthy();
    expect(document.querySelectorAll(".button.primary")).toHaveLength(1);
    expect(button("Create pool").className).toContain("primary");
    expect(document.querySelectorAll(".pool-card")).toHaveLength(2);
    expect(screen.queryByText(/enabled|healthy|disabled/i)).toBeNull();
    expect(screen.queryByText(/\d+ pools?/i)).toBeNull();
  });

  it("describes Edit pool and Delete with the pool name without changing their visible labels", async () => {
    mount({
      pools: () => ({
        pools: [pool("p1", "Team pool"), pool("p2", "Other pool")],
      }),
    });
    const second = await card("Other pool");
    const edit = within(second).getByRole("button", { name: "Edit pool" });
    const remove = within(second).getByRole("button", { name: "Delete" });
    expect(edit.textContent).toBe("Edit pool");
    expect(remove.textContent).toBe("Delete");
    expect(remove.className).toContain("danger");
    for (const control of [edit, remove]) {
      const id = control.getAttribute("aria-describedby")!;
      expect(document.getElementById(id)!.textContent).toBe("Other pool");
    }
    expect(within(second).getByRole("heading", { level: 2 }).textContent).toBe(
      "Other pool",
    );
  });

  it("labels every strategy, including the zero enum and an unmapped number", async () => {
    mount({
      pools: () => ({
        pools: [
          pool("p1", "a", [], 1),
          pool("p2", "b", [], 2),
          pool("p3", "c", [], 3),
          pool("p4", "d", [], 0),
          pool("p5", "e", [], 99),
        ],
      }),
    });
    const expected: [string, string][] = [
      ["a", "Priority"],
      ["b", "Round robin"],
      ["c", "Least recently used"],
      ["d", "Unspecified"],
      ["e", "Unknown"],
    ];
    for (const [name, label] of expected) {
      const scope = await card(name);
      expect(scope.querySelector(".badge")!.textContent).toBe(label);
      expect(scope.querySelector(".badge")!.className).toContain("neutral");
    }
  });
});

describe("pool members", () => {
  it("lists a sorted copy with ordinals by position, raw weights, headings and the helper text", async () => {
    mount({
      pools: () => ({
        pools: [
          pool("p1", "Team pool", [
            member("c1", 8, 2),
            member("c2", 0, 1),
            member("c3", 8, 0),
          ]),
        ],
      }),
      connections: () => ({
        connections: [
          connection("c1", "First 8"),
          connection("c2", "Negative"),
          connection("c3", "Second 8"),
        ],
      }),
    });
    const scope = await card("Team pool");
    await within(scope).findByText("Negative");
    expect(within(scope).getByText("Members in configured order")).toBeTruthy();
    const list = within(scope).getByRole("list", {
      name: "Members in configured order",
    });
    expect(list.tagName).toBe("OL");
    const rows = memberItems(scope).map((item) => item.textContent);
    expect(rows).toEqual([
      "Order1ConnectionNegativeWeight 1",
      "Order2ConnectionFirst 8Weight 2",
      "Order3ConnectionSecond 8Weight 0",
    ]);
    expect(
      Array.from(scope.querySelector(".pool-members-header")!.children).map(
        (item) => item.textContent,
      ),
    ).toEqual(["Order", "Connection", "Weight"]);
    expect(
      within(scope).getByText(
        "Order follows configured priority; weight does not represent a percentage.",
      ),
    ).toBeTruthy();
    expect(scope.textContent).not.toContain("%.");
  });

  it("shows No connections configured for a zero-member pool and keeps Edit pool", async () => {
    mount({ pools: () => ({ pools: [pool("p1", "Empty pool")] }) });
    const scope = await card("Empty pool");
    expect(within(scope).getByText("No connections configured")).toBeTruthy();
    expect(within(scope).queryByRole("list")).toBeNull();
    expect(
      (within(scope).getByRole("button", { name: "Edit pool" }) as HTMLElement)
        .tagName,
    ).toBe("BUTTON");
    expect(
      within(scope).getByText(/Order follows configured priority/),
    ).toBeTruthy();
  });

  it("resolves names from the complete connection enumeration beyond the first page", async () => {
    mount({
      pools: () => ({ pools: [pool("p1", "Team pool", [member("c2", 0)])] }),
      connections: (request) =>
        request.page?.pageToken === "more"
          ? { connections: [connection("c2", "Second page name")] }
          : {
              connections: [connection("c1", "First page name")],
              page: { nextPageToken: "more" },
            },
    });
    const scope = await card("Team pool");
    await within(scope).findByText("Second page name");
  });
});

describe("connection name lookup states", () => {
  it("shows a loading label with the ID while names are pending and never a missing state", async () => {
    let release: () => void = () => undefined;
    const gate = new Promise<void>((resolve) => (release = resolve));
    mount({
      pools: () => ({ pools: [pool("p1", "Team pool", [member("c1", 0)])] }),
      connections: async () => {
        await gate;
        return { connections: [connection("c1", "Work account")] };
      },
    });
    const scope = await card("Team pool");
    expect(
      within(scope).getByText("Loading connection name (c1)"),
    ).toBeTruthy();
    expect(screen.queryByText(/Unavailable connection/)).toBeNull();
    release();
    await within(scope).findByText("Work account");
  });

  it("shows a name-unavailable label with one page notice and refetches only connections on retry", async () => {
    const user = userEvent.setup();
    let calls = 0;
    const { requests } = mount({
      pools: () => ({
        pools: [
          pool("p1", "A", [member("c1", 0), member("c2", 1)]),
          pool("p2", "B", [member("c1", 0)]),
        ],
      }),
      connections: () => {
        calls += 1;
        if (calls === 1) throw new ConnectError("down", Code.Unavailable);
        return {
          connections: [connection("c1", "One"), connection("c2", "Two")],
        };
      },
    });
    await card("A");
    await screen.findAllByText("Connection name unavailable (c1)");
    expect(
      screen.getAllByText("Connection name unavailable (c2)"),
    ).toHaveLength(1);
    expect(screen.queryByText(/Unavailable connection/)).toBeNull();
    expect(
      screen.getAllByText("Connection names could not be loaded."),
    ).toHaveLength(1);
    // Rows, order and weights stay put.
    expect(document.querySelectorAll(".pool-member")).toHaveLength(3);
    const before = { list: requests.list.length, pools: requests.pools };
    await user.click(button("Retry connections"));
    await screen.findAllByText("One");
    expect(requests.list.length).toBe(before.list);
    expect(
      screen.queryByText("Connection names could not be loaded."),
    ).toBeNull();
  });

  it("keeps retained names under a stale marker when a refresh fails", async () => {
    let calls = 0;
    const { cache } = mount({
      pools: () => ({ pools: [pool("p1", "Team pool", [member("c1", 0)])] }),
      connections: () => {
        calls += 1;
        if (calls > 1) throw new ConnectError("down", Code.Unavailable);
        return { connections: [connection("c1", "Work account")] };
      },
    });
    const scope = await card("Team pool");
    await within(scope).findByText("Work account");
    await act(async () => {
      await cache.invalidateQueries({ queryKey: ["all-connections", "org-1"] });
    });
    await screen.findByText("Connection names could not be loaded.");
    expect(within(scope).getByText("Work account")).toBeTruthy();
    expect(
      within(scope).getByText("Last loaded name; it may be out of date."),
    ).toBeTruthy();
    expect(within(scope).queryByText(/Unavailable connection/)).toBeNull();
  });

  it("shows a pale warning row only when the completed inventory lacks the ID", async () => {
    const longId = "c-" + "x".repeat(80);
    mount({
      pools: () => ({
        pools: [pool("p1", "Team pool", [member(longId, 0), member("c1", 1)])],
      }),
    });
    const scope = await card("Team pool");
    await within(scope).findByText(`Unavailable connection (${longId})`);
    const rows = memberItems(scope);
    expect(rows[0]!.className).toContain("pool-member-missing");
    expect(rows[0]!.textContent).toContain("⚠");
    expect(within(rows[0]!).getByText("Connection not found")).toBeTruthy();
    expect(rows[1]!.className).not.toContain("pool-member-missing");
    expect(
      rows[0]!.querySelector("[aria-hidden='true']")!.textContent,
    ).toContain("⚠");
    // The member stays in the roster; nothing mutates automatically.
    expect(rows).toHaveLength(2);
  });

  it("sorts a copy ascending by priority, keeping source order for ties and never mutating the pool", () => {
    const source = create(PoolSchema, {
      members: [
        { connectionId: "a", priority: 8, weight: 2 },
        { connectionId: "b", priority: 3, weight: 1 },
        { connectionId: "c", priority: 8, weight: 0 },
      ],
    });
    // Generic priority is compared numerically, so a negative value sorts first.
    source.members[1]!.priority = -1;
    expect(orderedMembers(source).map((item) => item.connectionId)).toEqual([
      "b",
      "a",
      "c",
    ]);
    expect(source.members.map((item) => item.connectionId)).toEqual([
      "a",
      "b",
      "c",
    ]);
  });

  it("classifies the inventory without reporting failures as empty", () => {
    expect(inventoryState({ data: undefined, isError: false })).toBe(
      InventoryState.Loading,
    );
    expect(inventoryState({ data: [], isError: true })).toBe(
      InventoryState.Failed,
    );
    expect(inventoryState({ data: [], isError: false })).toBe(
      InventoryState.Empty,
    );
    expect(
      inventoryState({ data: [{ id: "a", name: "A" }], isError: false }),
    ).toBe(InventoryState.Populated);
  });
});

describe("pool list load and failure states", () => {
  it("shows card skeletons and Loading pools… without flashing the first-use state", async () => {
    let release: () => void = () => undefined;
    const gate = new Promise<void>((resolve) => (release = resolve));
    mount({
      pools: async () => {
        await gate;
        return { pools: [pool("p1", "Team pool")] };
      },
    });
    const status = screen
      .getByText("Loading pools…")
      .closest("[role='status']")!;
    expect(status.getAttribute("aria-busy")).toBe("true");
    expect(
      status.querySelector(".pool-skeletons")!.getAttribute("aria-hidden"),
    ).toBe("true");
    expect(screen.queryByText("Create your first pool")).toBeNull();
    expect(screen.getByRole("heading", { level: 1 })).toBeTruthy();
    release();
    await card("Team pool");
    expect(screen.queryByText("Loading pools…")).toBeNull();
  });

  it("shows a safe error with the request ID and recovers on Try again, never first-use copy", async () => {
    const user = userEvent.setup();
    let calls = 0;
    mount({
      pools: () => {
        calls += 1;
        if (calls <= 2)
          throw new ConnectError("secret detail", Code.Unavailable, undefined, [
            {
              desc: ErrorDetailSchema,
              value: create(ErrorDetailSchema, { requestId: "req-77" }),
            },
          ]);
        return { pools: [pool("p1", "Recovered")] };
      },
    });
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("req-77");
    expect(alert.textContent).not.toContain("secret detail");
    expect(screen.queryByText("Create your first pool")).toBeNull();
    expect(button("Create pool").disabled).toBe(true);
    await user.click(within(alert).getByRole("button", { name: "Try again" }));
    await card("Recovered");
    expect(screen.queryByRole("alert")).toBeNull();
    expect(button("Create pool").disabled).toBe(false);
  });

  it("hides cached rows when access is denied and offers no retry", async () => {
    mount({
      pools: () => {
        throw new ConnectError("denied", Code.PermissionDenied);
      },
    });
    const alert = await screen.findByRole("alert");
    expect(
      within(alert).queryByRole("button", { name: "Try again" }),
    ).toBeNull();
    expect(screen.queryByRole("list", { name: "Your pools" })).toBeNull();
  });

  it("keeps cached rows under a stale warning and disables mutating controls after a failed refresh", async () => {
    let calls = 0;
    const { cache } = mount({
      pools: () => {
        calls += 1;
        if (calls > 2) throw new ConnectError("down", Code.Unavailable);
        return { pools: [pool("p1", "Cached pool")] };
      },
    });
    const scope = await card("Cached pool");
    expect(button("Create pool").disabled).toBe(false);
    await act(async () => {
      await cache.invalidateQueries();
    });
    await screen.findByText("Pool list may be out of date.");
    expect(
      within(scope).getByRole("heading", { name: "Cached pool" }),
    ).toBeTruthy();
    expect(button("Create pool").disabled).toBe(true);
    expect(
      (
        within(scope).getByRole("button", {
          name: "Edit pool",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
    expect(
      (
        within(scope).getByRole("button", {
          name: "Delete",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
  });

  it("warns and disables mutating controls while offline", async () => {
    mount({ pools: () => ({ pools: [pool("p1", "Cached pool")] }) });
    await card("Cached pool");
    setOnline(false);
    act(() => {
      window.dispatchEvent(new Event("offline"));
    });
    await screen.findByText("Pool list may be out of date.");
    expect(button("Create pool").disabled).toBe(true);
    expect(button("Delete").disabled).toBe(true);
  });
});

describe("pool list empty states", () => {
  it("links to Providers with a primary action when the complete inventory is empty", async () => {
    mount({ connections: () => ({ connections: [] }) });
    await screen.findByText("Create your first pool");
    await screen.findByText("Connect providers before adding them to a pool.");
    expect(screen.getByText("Pools group your own connections.")).toBeTruthy();
    const link = screen.getByRole("link", { name: "Connect providers" });
    expect(link.getAttribute("href")).toBe("/organizations/org-1/providers");
    expect(link.className).toContain("primary");
    expect(button("Create pool").className).toContain("secondary");
    expect(document.querySelectorAll(".button.primary")).toHaveLength(1);
  });

  it("puts the primary Create pool in the panel when connections exist", async () => {
    const user = userEvent.setup();
    mount();
    await screen.findByText("Create your first pool");
    await waitFor(() =>
      expect(
        screen.getAllByRole("button", { name: "Create pool" }),
      ).toHaveLength(2),
    );
    const [header, panel] = screen.getAllByRole("button", {
      name: "Create pool",
    });
    expect(header!.className).toContain("secondary");
    expect(panel!.className).toContain("primary");
    expect(
      screen.queryByRole("link", { name: "Connect providers" }),
    ).toBeNull();
    await user.click(panel!);
    expect(await screen.findByRole("dialog")).toBeTruthy();
  });

  it("shows Loading connections… while the lookup is pending and never claims an empty inventory", async () => {
    let release: () => void = () => undefined;
    const gate = new Promise<void>((resolve) => (release = resolve));
    mount({
      connections: async () => {
        await gate;
        return { connections: [] };
      },
    });
    await screen.findByText("Create your first pool");
    expect(screen.getByText("Loading connections…")).toBeTruthy();
    expect(
      screen.queryByText("Connect providers before adding them to a pool."),
    ).toBeNull();
    expect(
      screen.queryByRole("link", { name: "Connect providers" }),
    ).toBeNull();
    expect(button("Create pool").className).toContain("primary");
    release();
    await screen.findByRole("link", { name: "Connect providers" });
  });

  it("reports a failed lookup with Retry connections and keeps Create pool available", async () => {
    const user = userEvent.setup();
    let calls = 0;
    mount({
      connections: () => {
        calls += 1;
        if (calls === 1) throw new ConnectError("down", Code.Unavailable);
        return { connections: [] };
      },
    });
    await screen.findByText("Connections could not be loaded.");
    expect(
      screen.queryByText("Connect providers before adding them to a pool."),
    ).toBeNull();
    expect(
      screen.queryByRole("link", { name: "Connect providers" }),
    ).toBeNull();
    expect(button("Create pool").disabled).toBe(false);
    await user.click(button("Retry connections"));
    await screen.findByRole("link", { name: "Connect providers" });
    expect(screen.queryByText("Connections could not be loaded.")).toBeNull();
  });

  it("shows No pools on this page with First page on a later empty page", async () => {
    const user = userEvent.setup();
    mount({
      pools: (request) =>
        request.page?.pageToken === "empty-page"
          ? { pools: [] }
          : { pools: [pool("p1", "Only pool")], next: "empty-page" },
    });
    await card("Only pool");
    await user.click(button("Next page"));
    await screen.findByText("No pools on this page");
    expect(screen.queryByText("Create your first pool")).toBeNull();
    expect(
      screen.queryByRole("link", { name: "Connect providers" }),
    ).toBeNull();
    expect(button("First page").disabled).toBe(false);
    await user.click(button("First page"));
    await card("Only pool");
  });
});

describe("pool list pagination", () => {
  const pages = (request: { page?: { pageToken?: string } }): Page => {
    switch (request.page?.pageToken) {
      case "page-b":
        return { pools: [pool("p2", "Second page")], next: "page-c" };
      case "page-c":
        return { pools: [pool("p3", "Third page")] };
      default:
        return { pools: [pool("p1", "First page pool")], next: "page-b" };
    }
  };

  it("uses page size 100 and exact tokens, labels the controls and announces the load once", async () => {
    const user = userEvent.setup();
    const { requests } = mount({ pools: pages });
    await card("First page pool");
    expect(requests.list[0]).toMatchObject({ pageSize: 100, pageToken: "" });
    expect(button("First page").disabled).toBe(true);
    expect(screen.queryByRole("button", { name: "Next" })).toBeNull();
    await user.click(button("Next page"));
    await card("Second page");
    expect(requests.list.some((item) => item.pageToken === "page-b")).toBe(
      true,
    );
    const announcement = await screen.findByText("Pools loaded.");
    expect(
      announcement.closest("[role='status']")!.getAttribute("aria-live"),
    ).toBe("polite");
    expect(screen.getAllByText("Pools loaded.")).toHaveLength(1);
    await user.click(button("First page"));
    await card("First page pool");
    expect(requests.list.at(-1)?.pageToken).toBe("");
    expect(screen.queryByText(/page \d|of \d|total/i)).toBeNull();
  });

  it("disables both controls while a page loads and announces only after it settles", async () => {
    const user = userEvent.setup();
    let release: () => void = () => undefined;
    const gate = new Promise<void>((resolve) => (release = resolve));
    mount({
      pools: async (request) => {
        if (request.page?.pageToken === "page-b") await gate;
        return pages(request);
      },
    });
    await card("First page pool");
    await user.click(button("Next page"));
    await waitFor(() => expect(button("First page").disabled).toBe(true));
    expect(button("Next page").disabled).toBe(true);
    expect(screen.queryByText("Pools loaded.")).toBeNull();
    release();
    await card("Second page");
    await screen.findByText("Pools loaded.");
  });

  it("retains focus on a paging control that stays usable", async () => {
    const user = userEvent.setup();
    mount({ pools: pages });
    await card("First page pool");
    await user.click(button("Next page"));
    await card("Second page");
    await screen.findByText("Pools loaded.");
    expect(document.activeElement).toBe(button("Next page"));
  });

  it("moves focus to the collection heading when the activating control becomes unusable", async () => {
    const user = userEvent.setup();
    mount({ pools: pages });
    await card("First page pool");
    await user.click(button("Next page"));
    await card("Second page");
    await user.click(button("Next page"));
    await card("Third page");
    await screen.findByText("Pools loaded.");
    const heading = screen.getByRole("heading", {
      level: 1,
      name: "Account pools",
    });
    await waitFor(() => expect(document.activeElement).toBe(heading));
    expect(button("Next page").disabled).toBe(true);
  });
});

describe("pool actions and focus after deletion", () => {
  it("opens the editor and delete confirmation for the exact record", async () => {
    const user = userEvent.setup();
    mount({
      pools: () => ({
        pools: [
          pool("p1", "Team pool", [member("c1", 0)]),
          pool("p2", "Other pool", [member("c1", 0)]),
        ],
      }),
    });
    const second = await card("Other pool");
    await user.click(within(second).getByRole("button", { name: "Edit pool" }));
    const editor = await screen.findByRole("dialog");
    expect(
      (within(editor).getByLabelText("Pool name") as HTMLInputElement).value,
    ).toBe("Other pool");
    await user.click(
      within(editor).getByRole("button", { name: /Close dialog/ }),
    );
    await user.click(within(second).getByRole("button", { name: "Delete" }));
    expect(
      await screen.findByRole("dialog", { name: "Delete Other pool?" }),
    ).toBeTruthy();
  });

  const remove = async (
    user: ReturnType<typeof userEvent.setup>,
    name: string,
  ) => {
    const scope = await card(name);
    await user.click(within(scope).getByRole("button", { name: "Delete" }));
    const dialog = await screen.findByRole("dialog");
    await user.click(
      within(dialog).getByRole("button", { name: "Delete pool" }),
    );
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await waitFor(() => expect(screen.queryByText(name)).toBeNull());
  };
  const three = () => ({
    pools: [pool("p1", "One"), pool("p2", "Two"), pool("p3", "Three")],
  });

  it("focuses the next card's first action when a middle record disappears", async () => {
    const user = userEvent.setup();
    mount({ pools: three });
    await remove(user, "Two");
    const next = within(await card("Three")).getByRole("button", {
      name: "Edit pool",
    });
    await waitFor(() => expect(document.activeElement).toBe(next));
  });

  it("falls back to the preceding card when the last record disappears", async () => {
    const user = userEvent.setup();
    mount({ pools: three });
    await remove(user, "Three");
    const previous = within(await card("Two")).getByRole("button", {
      name: "Edit pool",
    });
    await waitFor(() => expect(document.activeElement).toBe(previous));
  });

  it("falls back to Create pool when no card remains", async () => {
    const user = userEvent.setup();
    mount({ pools: () => ({ pools: [pool("p1", "Solo")] }) });
    await remove(user, "Solo");
    await screen.findByText("Create your first pool");
    await waitFor(() =>
      expect(document.activeElement).toBe(
        screen.getAllByRole("button", { name: "Create pool" })[0],
      ),
    );
  });

  it("clears the prior organization's content and dialogs on an organization switch", async () => {
    const user = userEvent.setup();
    const { switchOrganization } = mount({
      pools: (request) =>
        request.organizationId === "org-2"
          ? { pools: [pool("q1", "Org two pool")] }
          : { pools: [pool("p1", "Org one pool")] },
    });
    const scope = await card("Org one pool");
    await user.click(within(scope).getByRole("button", { name: "Delete" }));
    await screen.findByRole("dialog");
    switchOrganization(otherOrganization);
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(screen.queryByText("Org one pool")).toBeNull();
    await card("Org two pool");
    expect(screen.getByText("Only yours in Other Studio.")).toBeTruthy();
    expect(screen.queryByText("Org one pool")).toBeNull();
  });
});

describe("pool list responsive styles", () => {
  const css = readFileSync(
    new URL("../src/styles.css", import.meta.url),
    "utf8",
  );
  const mobile = css.slice(css.lastIndexOf(".pool-card {\n    padding: 16px;"));
  it("stacks member rows below 768px, keeps labels visible and wraps actions", () => {
    expect(css).toContain("@media (max-width: 767px)");
    expect(mobile).toContain(".pool-members-header {\n    display: none;");
    expect(mobile).toContain(".pool-member-label {\n    position: static;");
    expect(mobile).toContain("grid-template-columns: minmax(0, 1fr);");
    expect(css).toMatch(/\.pool-card-actions \{[^}]*flex-wrap: wrap/);
  });
  it("wraps long names and IDs instead of scrolling horizontally", () => {
    expect(css).toMatch(/\.pool-card-title h2 \{[^}]*overflow-wrap: anywhere/);
    expect(css).toMatch(/\.pool-member \{[^}]*overflow-wrap: anywhere/);
  });
});
