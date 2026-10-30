// SPDX-License-Identifier: Apache-2.0
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
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
  RoutingService,
  OrganizationSchema,
  ErrorDetailSchema,
  Role,
  RoutingStrategy,
  ConnectionState,
} from "@tairouter/client";
import { ApiProvider } from "../src/api";
import { RoutingPage } from "../src/routing";
import {
  DependencyStatus,
  resolveDependency,
  strategyLabel,
} from "../src/route-list";

const organization = create(OrganizationSchema, {
  id: "org-1",
  name: "Acme Studio",
  role: Role.MEMBER,
});

type Target = {
  target?: { case: "connectionId" | "poolId"; value: string };
  modelId: string;
  priority: number;
  weight: number;
};
type RouteFixture = {
  id: string;
  modelId: string;
  enabled: boolean;
  strategy: number;
  targets: Target[];
};
const route = (
  id: string,
  modelId: string,
  targets: Target[] = [],
  extra: Partial<RouteFixture> = {},
): RouteFixture => ({
  id,
  modelId,
  enabled: true,
  strategy: RoutingStrategy.FALLBACK,
  targets,
  ...extra,
});
const viaConnection = (
  id: string,
  modelId: string,
  priority: number,
  weight = 1,
): Target => ({
  target: { case: "connectionId", value: id },
  modelId,
  priority,
  weight,
});
const viaPool = (
  id: string,
  modelId: string,
  priority: number,
  weight = 1,
): Target => ({
  target: { case: "poolId", value: id },
  modelId,
  priority,
  weight,
});

type Page = { routes: RouteFixture[]; next?: string };
type Fixture = {
  /** Handler for the visible list and the editor enumeration (same RPC). */
  listRoutes?: (request: {
    page?: { pageSize?: number; pageToken?: string };
  }) => Page | Promise<Page>;
  connections?: (request: {
    page?: { pageToken?: string };
  }) => object | Promise<object>;
  pools?: (request: {
    page?: { pageToken?: string };
  }) => object | Promise<object>;
  deleteRoute?: (request: { routeId?: string }) => object;
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
    mutations: 0,
  };
  const transport = createRouterTransport((r: ConnectRouter) => {
    r.service(RoutingService, {
      listRoutes: (async (request: {
        page?: { pageSize?: number; pageToken?: string };
      }) => {
        requests.list.push({
          pageSize: request.page?.pageSize,
          pageToken: request.page?.pageToken,
        });
        const page = fixture.listRoutes
          ? await fixture.listRoutes(request)
          : { routes: [] };
        return {
          routes: page.routes.filter((item) => !removed.has(item.id)),
          page: page.next ? { nextPageToken: page.next } : undefined,
        };
      }) as never,
      deleteRoute: ((request: { routeId: string }) => {
        requests.mutations += 1;
        removed.add(request.routeId);
        return fixture.deleteRoute?.(request) ?? {};
      }) as never,
      createRoute: (() => {
        requests.mutations += 1;
        return {};
      }) as never,
      updateRoute: (() => {
        requests.mutations += 1;
        return {};
      }) as never,
    });
    r.service(ProviderService, {
      listProviderConnections: ((request: never) => {
        requests.connections += 1;
        return (
          fixture.connections?.(request) ?? {
            connections: [
              { id: "c1", name: "Work account", state: ConnectionState.READY },
            ],
          }
        );
      }) as never,
    });
    r.service(PoolService, {
      listPools: ((request: never) => {
        requests.pools += 1;
        return (
          fixture.pools?.(request) ?? {
            pools: [{ id: "p1", name: "Shared pool" }],
          }
        );
      }) as never,
    });
  });
  const view = render(
    <ApiProvider queryClient={cache} rpcTransport={transport}>
      <RoutingPage organization={organization} session={undefined as never} />
    </ApiProvider>,
  );
  return { ...view, requests };
}
const card = async (alias: string) =>
  (await screen.findByRole("heading", { name: alias })).closest(
    "section",
  ) as HTMLElement;
const targetItems = (scope: HTMLElement) =>
  within(scope).getAllByRole("listitem");
/** Native Escape raises a cancel event on the open dialog. */
const escape = () =>
  fireEvent(
    screen.getByRole("dialog"),
    new Event("cancel", { cancelable: true }),
  );
function setOnline(value: boolean) {
  Object.defineProperty(navigator, "onLine", { configurable: true, value });
}
afterEach(() => {
  // The query client's online manager listens for the event, so restore both.
  setOnline(true);
  window.dispatchEvent(new Event("online"));
});

describe("routing list hierarchy", () => {
  it("renders one h1, the scope, one primary action, the policy sentence and no extra controls", async () => {
    mount({ listRoutes: () => ({ routes: [route("r1", "team-chat")] }) });
    await card("team-chat");
    expect(screen.getAllByRole("heading", { level: 1 })).toHaveLength(1);
    expect(
      screen.getByRole("heading", { level: 1, name: "Your routing" }),
    ).toBeTruthy();
    expect(screen.getByText("Only yours in Acme Studio.")).toBeTruthy();
    expect(
      screen.getByText(/Model aliases choose your primary and fallback/),
    ).toBeTruthy();
    expect(document.querySelectorAll(".button.primary")).toHaveLength(1);
    expect(screen.queryByRole("searchbox")).toBeNull();
    expect(screen.queryByRole("switch")).toBeNull();
    expect(screen.queryByRole("checkbox")).toBeNull();
    expect(screen.queryByText(/total/i)).toBeNull();
    expect(screen.queryByRole("button", { name: /previous/i })).toBeNull();
  });

  it("renders alias, saved status and strategy on each card with per-route action names", async () => {
    mount({
      listRoutes: () => ({
        routes: [
          route("r1", "team-chat", [], { enabled: true }),
          route("r2", "writing", [], {
            enabled: false,
            strategy: RoutingStrategy.WEIGHTED,
          }),
        ],
      }),
    });
    const first = await card("team-chat");
    expect(within(first).getByText("Enabled")).toBeTruthy();
    expect(within(first).getByText("Strategy: Fallback")).toBeTruthy();
    const second = await card("writing");
    expect(within(second).getByText("Disabled")).toBeTruthy();
    expect(within(second).getByText("Strategy: Weighted")).toBeTruthy();
    expect(
      within(second).getByRole("button", { name: "Edit route writing" }),
    ).toBeTruthy();
    expect(
      within(second).getByRole("button", { name: "Delete route writing" }),
    ).toBeTruthy();
  });

  it.each([
    [RoutingStrategy.DIRECT, "Direct"],
    [RoutingStrategy.FALLBACK, "Fallback"],
    [RoutingStrategy.WEIGHTED, "Weighted"],
    [RoutingStrategy.ROUND_ROBIN, "Round robin"],
    [RoutingStrategy.JEV, "JEV"],
    [RoutingStrategy.UNSPECIFIED, "Unspecified"],
    [99, "Unknown"],
  ])(
    "labels strategy %s as %s independent of the enabled flag",
    async (value, label) => {
      expect(strategyLabel(value)).toBe(label);
      mount({
        listRoutes: () => ({
          routes: [
            route("r1", "alias", [], { strategy: value, enabled: false }),
          ],
        }),
      });
      const scope = await card("alias");
      expect(within(scope).getByText(`Strategy: ${label}`)).toBeTruthy();
      expect(within(scope).getByText("Disabled")).toBeTruthy();
    },
  );
});

describe("routing targets", () => {
  it("sorts a copy of the targets stably by priority and keeps fields attached", async () => {
    const targets = [
      viaConnection("c1", "model-eight", 8, 5),
      viaPool("p1", "model-zero-a", 0, 0),
      viaConnection("c1", "model-three", 3, 7),
      viaPool("p1", "model-zero-b", 0, 2),
    ];
    const original = route("r1", "ties", targets);
    mount({ listRoutes: () => ({ routes: [original] }) });
    const scope = await card("ties");
    await within(scope).findAllByText("Shared pool");
    const items = targetItems(scope);
    expect(
      items.map(
        (item) =>
          item.querySelector(".route-target-model span:last-child")
            ?.textContent,
      ),
    ).toEqual(["model-zero-a", "model-zero-b", "model-three", "model-eight"]);
    expect(
      items.map(
        (item) =>
          item.querySelector(".route-target-order span:last-child")
            ?.textContent,
      ),
    ).toEqual(["1", "2", "3", "4"]);
    expect(within(items[0]!).getByText("Weight 0")).toBeTruthy();
    expect(within(items[0]!).getByText("Pool")).toBeTruthy();
    expect(within(items[1]!).getByText("Weight 2")).toBeTruthy();
    expect(within(items[2]!).getByText("Connection")).toBeTruthy();
    expect(within(items[2]!).getByText("Weight 7")).toBeTruthy();
    expect(within(items[3]!).getByText("Weight 5")).toBeTruthy();
    // The visible list is an ordered list with a visible column header.
    expect(
      within(scope).getByRole("list", { name: "Targets for route ties" })
        .tagName,
    ).toBe("OL");
    for (const heading of [
      "Order",
      "Connection or pool",
      "Target model",
      "Weight",
    ])
      expect(
        scope.querySelector(".route-targets-header")?.textContent,
      ).toContain(heading);
    expect(targets.map((t) => t.priority)).toEqual([8, 0, 3, 0]);
  });

  it("distinguishes a connection and a pool that share a display name", async () => {
    mount({
      listRoutes: () => ({
        routes: [
          route("r1", "same", [
            viaConnection("c1", "m1", 0),
            viaPool("p1", "m2", 1),
          ]),
        ],
      }),
      connections: () => ({ connections: [{ id: "c1", name: "Primary" }] }),
      pools: () => ({ pools: [{ id: "p1", name: "Primary" }] }),
    });
    const scope = await card("same");
    await waitFor(() =>
      expect(within(scope).getAllByText("Primary")).toHaveLength(2),
    );
    const items = targetItems(scope);
    expect(within(items[0]!).getByText("Connection")).toBeTruthy();
    expect(within(items[1]!).getByText("Pool")).toBeTruthy();
  });

  it("shows an empty target array and a missing oneof explicitly", async () => {
    mount({
      listRoutes: () => ({
        routes: [
          route("r1", "bare"),
          route("r2", "orphan", [
            { modelId: "kept-model", priority: 0, weight: 9 },
          ]),
        ],
      }),
    });
    const bare = await card("bare");
    expect(within(bare).getByText("No targets configured")).toBeTruthy();
    const orphan = await card("orphan");
    const item = targetItems(orphan)[0]!;
    expect(within(item).getByText("Unavailable target")).toBeTruthy();
    expect(within(item).getByText("kept-model")).toBeTruthy();
    expect(within(item).getByText("Weight 9")).toBeTruthy();
    expect(item.className).toContain("route-target-missing");
  });
});

describe("dependency name resolution", () => {
  it("maps query states without proving absence from pending or failed queries", () => {
    const data = [{ id: "a", name: "A" }];
    expect(
      resolveDependency({ isPending: true, isError: false }, "a").status,
    ).toBe(DependencyStatus.Loading);
    expect(
      resolveDependency({ data, isPending: false, isError: false }, "a"),
    ).toEqual({
      status: DependencyStatus.Found,
      name: "A",
    });
    expect(
      resolveDependency({ data, isPending: false, isError: false }, "z").status,
    ).toBe(DependencyStatus.Missing);
    expect(
      resolveDependency({ isPending: false, isError: true }, "a").status,
    ).toBe(DependencyStatus.Failed);
    expect(
      resolveDependency({ data, isPending: false, isError: true }, "a"),
    ).toEqual({
      status: DependencyStatus.Failed,
      name: "A",
      stale: true,
    });
    expect(
      resolveDependency({ data, isPending: false, isError: true }, "z"),
    ).toEqual({
      status: DependencyStatus.Failed,
      name: undefined,
      stale: false,
    });
  });

  it("shows Loading while enumeration is pending, then the missing state after it completes", async () => {
    let release: () => void = () => undefined;
    const gate = new Promise<void>((resolve) => (release = resolve));
    mount({
      listRoutes: () => ({
        routes: [
          route("r1", "deps", [
            viaConnection("gone-c", "m1", 0),
            viaPool("gone-p", "m2", 1),
          ]),
        ],
      }),
      connections: async () => {
        await gate;
        return { connections: [] };
      },
      pools: async () => {
        await gate;
        return { pools: [] };
      },
    });
    const scope = await card("deps");
    expect(within(scope).getByText("Loading connection…")).toBeTruthy();
    expect(within(scope).getByText("Loading pool…")).toBeTruthy();
    expect(within(scope).queryByText("Unavailable connection")).toBeNull();
    expect(within(scope).queryByText("Unavailable pool")).toBeNull();
    release();
    expect(
      await within(scope).findByText("Unavailable connection"),
    ).toBeTruthy();
    expect(within(scope).getByText("Unavailable pool")).toBeTruthy();
    const row = targetItems(scope)[0]!;
    expect(row.className).toContain("route-target-missing");
    expect(row.querySelector("[aria-hidden='true']")?.textContent).toContain(
      "⚠",
    );
    // Raw identifiers are never shown.
    expect(scope.textContent).not.toContain("gone-c");
  });

  it("reports a failed enumeration, retries only that query and then resolves", async () => {
    const user = userEvent.setup();
    let connectionCalls = 0;
    const { requests } = mount({
      listRoutes: () => ({
        routes: [
          route("r1", "deps", [
            viaConnection("c1", "m1", 0),
            viaPool("p1", "m2", 1),
          ]),
        ],
      }),
      connections: () => {
        connectionCalls += 1;
        if (connectionCalls === 1)
          throw new ConnectError("down", Code.Unavailable);
        return { connections: [{ id: "c1", name: "Work account" }] };
      },
    });
    const scope = await card("deps");
    expect(
      await within(scope).findByText("Connection details unavailable"),
    ).toBeTruthy();
    expect(within(scope).queryByText("Unavailable connection")).toBeNull();
    await within(scope).findAllByText("Shared pool");
    const poolCalls = requests.pools;
    await user.click(
      within(scope).getByRole("button", {
        name: "Try again loading connections",
      }),
    );
    expect(await within(scope).findByText("Work account")).toBeTruthy();
    expect(connectionCalls).toBe(2);
    expect(requests.pools).toBe(poolCalls);
  });

  it("resolves a dependency that lives beyond the first 100-row page", async () => {
    const first = Array.from({ length: 100 }, (_, i) => ({
      id: `c${i}`,
      name: `Account ${i}`,
    }));
    mount({
      listRoutes: () => ({
        routes: [route("r1", "late", [viaConnection("late-id", "m1", 0)])],
      }),
      connections: (request) =>
        request.page?.pageToken === "more"
          ? { connections: [{ id: "late-id", name: "Late account" }] }
          : { connections: first, page: { nextPageToken: "more" } },
    });
    const scope = await card("late");
    expect(await within(scope).findByText("Late account")).toBeTruthy();
    expect(within(scope).queryByText("Unavailable connection")).toBeNull();
  });
});

describe("routing page states", () => {
  it("shows a busy loading status with hidden skeletons and no empty state while pending", async () => {
    let release: () => void = () => undefined;
    const gate = new Promise<void>((resolve) => (release = resolve));
    mount({
      listRoutes: async () => {
        await gate;
        return { routes: [] };
      },
    });
    const status = screen
      .getByText("Loading routing…")
      .closest("[role='status']")!;
    expect(status.textContent).toContain("Loading routing…");
    expect(status.getAttribute("aria-busy")).toBe("true");
    expect(status.querySelector("[aria-hidden='true']")).not.toBeNull();
    expect(screen.queryByText("Configure your first model route")).toBeNull();
    expect(screen.queryByText("No routes on this page")).toBeNull();
    release();
    await screen.findByText("Configure your first model route");
  });

  it("shows first-use guidance with the organization-scoped providers link on an empty first page", async () => {
    mount({ listRoutes: () => ({ routes: [] }) });
    const heading = await screen.findByRole("heading", {
      name: "Configure your first model route",
    });
    expect(
      screen.getByText(
        "Add a provider connection, then map a model alias to it.",
      ),
    ).toBeTruthy();
    expect(
      screen.getByRole("link", { name: "View providers" }).getAttribute("href"),
    ).toBe("/organizations/org-1/providers");
    expect(document.querySelectorAll(".button.primary")).toHaveLength(1);
    expect(heading.closest("section")).not.toBeNull();
  });

  it("keeps failed dependency queries visible instead of implying nothing is connected", async () => {
    mount({
      listRoutes: () => ({ routes: [] }),
      connections: () => {
        throw new ConnectError("down", Code.Unavailable);
      },
    });
    await screen.findByText("Configure your first model route");
    expect(
      await screen.findByText(/Connection details unavailable/),
    ).toBeTruthy();
  });

  it("shows read failures with the request ID and retry, and recovers on Try again", async () => {
    const user = userEvent.setup();
    let calls = 0;
    mount({
      listRoutes: () => {
        calls += 1;
        if (calls === 1)
          throw new ConnectError("secret detail", Code.Unavailable, undefined, [
            {
              desc: ErrorDetailSchema,
              value: create(ErrorDetailSchema, {
                requestId: "req-77",
                retryAfterSeconds: 5,
              }),
            },
          ]);
        return { routes: [route("r1", "recovered")] };
      },
    });
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("req-77");
    expect(alert.textContent).toContain("Try again after 5 seconds.");
    expect(alert.textContent).not.toContain("secret detail");
    expect(screen.queryByText("Configure your first model route")).toBeNull();
    await user.click(within(alert).getByRole("button", { name: "Try again" }));
    await card("recovered");
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("treats permission denial differently: no retry and no cached rows", async () => {
    mount({
      listRoutes: () => {
        throw new ConnectError("denied", Code.PermissionDenied);
      },
    });
    const alert = await screen.findByRole("alert");
    expect(
      within(alert).queryByRole("button", { name: "Try again" }),
    ).toBeNull();
    expect(screen.queryByRole("list", { name: "Your routes" })).toBeNull();
  });

  it("keeps last loaded rows under a stale warning when a refresh fails", async () => {
    const user = userEvent.setup();
    let calls = 0;
    mount({
      listRoutes: () => {
        calls += 1;
        // The first calls load the page and the editor enumeration; later calls are refreshes.
        if (calls > 2) throw new ConnectError("down", Code.Unavailable);
        return { routes: [route("r1", "cached")] };
      },
    });
    await card("cached");
    await waitFor(() => expect(calls).toBeGreaterThanOrEqual(2));
    await user.click(
      screen.getByRole("button", { name: "Delete route cached" }),
    );
    await user.click(
      within(await screen.findByRole("dialog")).getByRole("button", {
        name: "Delete route",
      }),
    );
    expect(
      await screen.findByText(
        "Routing may be out of date. Reconnect to refresh.",
      ),
    ).toBeTruthy();
    expect(screen.getByRole("heading", { name: "cached" })).toBeTruthy();
  });

  it("warns that loaded rows may be out of date while offline", async () => {
    mount({ listRoutes: () => ({ routes: [route("r1", "cached")] }) });
    await card("cached");
    setOnline(false);
    act(() => {
      window.dispatchEvent(new Event("offline"));
    });
    expect(
      await screen.findByText(
        "Routing may be out of date. Reconnect to refresh.",
      ),
    ).toBeTruthy();
    expect(screen.getByRole("heading", { name: "cached" })).toBeTruthy();
  });
});

describe("routing pagination", () => {
  const pages = (request: { page?: { pageToken?: string } }): Page => {
    switch (request.page?.pageToken) {
      case "page-b":
        return { routes: [route("r2", "second-page")] };
      case "empty-page":
        return { routes: [] };
      default:
        return { routes: [route("r1", "first-page")], next: "page-b" };
    }
  };

  it("uses page size 100 and the opaque token, announces the load and focuses the list heading once", async () => {
    const user = userEvent.setup();
    const { requests } = mount({ listRoutes: pages });
    await card("first-page");
    expect(requests.list[0]).toMatchObject({ pageSize: 100, pageToken: "" });
    const first = screen.getByRole("button", {
      name: "First page",
    }) as HTMLButtonElement;
    expect(first.disabled).toBe(true);
    await user.click(screen.getByRole("button", { name: "Next page" }));
    await card("second-page");
    expect(requests.list.some((r) => r.pageToken === "page-b")).toBe(true);
    const heading = screen.getByRole("heading", {
      level: 1,
      name: "Your routing",
    });
    await waitFor(() => expect(document.activeElement).toBe(heading));
    expect(
      screen
        .getByText("Routing page loaded")
        .closest("[role='status']")
        ?.getAttribute("aria-live"),
    ).toBe("polite");
    expect(
      (screen.getByRole("button", { name: "Next page" }) as HTMLButtonElement)
        .disabled,
    ).toBe(true);
    await user.click(screen.getByRole("button", { name: "First page" }));
    await card("first-page");
    expect(requests.list.at(-1)?.pageToken).toBe("");
    expect(screen.queryByText(/page \d|of \d|total/i)).toBeNull();
  });

  it("disables both page controls while a page loads", async () => {
    const user = userEvent.setup();
    let release: () => void = () => undefined;
    const gate = new Promise<void>((resolve) => (release = resolve));
    mount({
      listRoutes: async (request) => {
        if (request.page?.pageToken === "page-b") await gate;
        return pages(request);
      },
    });
    await card("first-page");
    await user.click(screen.getByRole("button", { name: "Next page" }));
    await waitFor(() =>
      expect(
        (
          screen.getByRole("button", {
            name: "First page",
          }) as HTMLButtonElement
        ).disabled,
      ).toBe(true),
    );
    expect(
      (screen.getByRole("button", { name: "Next page" }) as HTMLButtonElement)
        .disabled,
    ).toBe(true);
    expect(screen.queryByText("Routing page loaded")).toBeNull();
    release();
    await card("second-page");
    await screen.findByText("Routing page loaded");
  });

  it("shows No routes on this page with First page on an empty later page, never first-use copy", async () => {
    const user = userEvent.setup();
    mount({
      listRoutes: (request) =>
        request.page?.pageToken === "empty-page"
          ? { routes: [] }
          : { routes: [route("r1", "only")], next: "empty-page" },
    });
    await card("only");
    await user.click(screen.getByRole("button", { name: "Next page" }));
    const heading = await screen.findByRole("heading", {
      name: "No routes on this page",
    });
    expect(screen.queryByText("Configure your first model route")).toBeNull();
    expect(screen.queryByRole("link", { name: "View providers" })).toBeNull();
    await waitFor(() => expect(document.activeElement).toBe(heading));
    expect(
      (screen.getByRole("button", { name: "First page" }) as HTMLButtonElement)
        .disabled,
    ).toBe(false);
  });
});

describe("routing interactions and focus", () => {
  it("opens Create, Edit and Delete from the keyboard without mutating, and restores focus", async () => {
    const user = userEvent.setup();
    const { requests } = mount({
      listRoutes: () => ({
        routes: [route("r1", "team-chat", [viaConnection("c1", "gpt", 0, 3)])],
      }),
    });
    await card("team-chat");
    const create = screen.getByRole("button", { name: "Create route" });
    create.focus();
    await user.keyboard("{Enter}");
    expect(
      await screen.findByRole("dialog", { name: "Create route" }),
    ).toBeTruthy();
    escape();
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(document.activeElement).toBe(create);

    const edit = screen.getByRole("button", { name: "Edit route team-chat" });
    edit.focus();
    await user.keyboard("{Enter}");
    const editor = await screen.findByRole("dialog", { name: "Edit routing" });
    expect(within(editor).getByDisplayValue("team-chat")).toBeTruthy();
    escape();
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(document.activeElement).toBe(edit);

    const remove = screen.getByRole("button", {
      name: "Delete route team-chat",
    });
    remove.focus();
    await user.keyboard("{Enter}");
    const confirm = await screen.findByRole("dialog", {
      name: "Delete route?",
    });
    await user.click(within(confirm).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(document.activeElement).toBe(remove);
    expect(requests.mutations).toBe(0);
  });

  it("focuses the next route heading after a confirmed deletion", async () => {
    const user = userEvent.setup();
    mount({
      listRoutes: () => ({
        routes: [route("r1", "one"), route("r2", "two"), route("r3", "three")],
      }),
    });
    await card("one");
    await user.click(screen.getByRole("button", { name: "Delete route two" }));
    const dialog = await screen.findByRole("dialog");
    await user.click(
      within(dialog).getByRole("button", { name: "Delete route" }),
    );
    await screen.findByText("Route deleted.");
    await waitFor(() =>
      expect(screen.queryByRole("heading", { name: "two" })).toBeNull(),
    );
    const next = screen.getByRole("heading", { name: "three" });
    await waitFor(() => expect(document.activeElement).toBe(next));
  });

  it("focuses the list heading when the last route is deleted and the first-use heading when none remain", async () => {
    const user = userEvent.setup();
    mount({
      listRoutes: () => ({ routes: [route("r1", "one"), route("r2", "two")] }),
    });
    await card("one");
    await user.click(screen.getByRole("button", { name: "Delete route two" }));
    await user.click(
      within(await screen.findByRole("dialog")).getByRole("button", {
        name: "Delete route",
      }),
    );
    const list = screen.getByRole("heading", {
      level: 1,
      name: "Your routing",
    });
    await waitFor(() =>
      expect(screen.queryByRole("heading", { name: "two" })).toBeNull(),
    );
    await waitFor(() => expect(document.activeElement).toBe(list));
    await user.click(screen.getByRole("button", { name: "Delete route one" }));
    await user.click(
      within(await screen.findByRole("dialog")).getByRole("button", {
        name: "Delete route",
      }),
    );
    const firstUse = await screen.findByRole("heading", {
      name: "Configure your first model route",
    });
    await waitFor(() => expect(document.activeElement).toBe(firstUse));
  });
});

describe("long content", () => {
  it("renders 300-character aliases and model IDs with 40 targets in full", async () => {
    const alias = "a".repeat(300);
    const model = "m".repeat(300);
    const targets = Array.from({ length: 40 }, (_, i) =>
      viaConnection("c1", `${model}-${i}`, i, i * 10),
    );
    mount({ listRoutes: () => ({ routes: [route("r1", alias, targets)] }) });
    const scope = await card(alias);
    expect(targetItems(scope)).toHaveLength(40);
    expect(within(scope).getByText(`${model}-39`)).toBeTruthy();
    expect(within(scope).getByText("Weight 390")).toBeTruthy();
    expect(
      within(scope).getByRole("button", { name: `Edit route ${alias}` }),
    ).toBeTruthy();
  });
});
