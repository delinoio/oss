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
import { describe, it, expect, rs } from "@rstest/core";
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
  GetSessionResponseSchema,
  Role,
  ProviderAuthKind,
  ConnectionState,
} from "@aiprouter/client";
import { ApiProvider } from "../src/api";
import { ProvidersPage } from "../src/providers";
import {
  DependencyState,
  dependencyState,
  poolRows,
  routeRows,
} from "../src/provider-disconnect";
import { ConfirmDialog } from "../src/ui";

const organization = create(OrganizationSchema, {
  id: "org-1",
  name: "Acme Studio",
  role: Role.MEMBER,
});
const session = create(GetSessionResponseSchema, {
  userId: "person",
  displayName: "Alex",
});
const connections = [
  {
    id: "c1",
    name: "Work account",
    providerId: "openai",
    authKind: ProviderAuthKind.API_KEY,
    state: ConnectionState.READY,
  },
  {
    id: "c2",
    name: "Work account",
    providerId: "openai",
    authKind: ProviderAuthKind.API_KEY,
    state: ConnectionState.READY,
  },
];
const pool = (id: string, name: string, connectionId: string) => ({
  id,
  name,
  members: [{ connectionId }],
});
const route = (id: string, modelId: string, connectionId: string) => ({
  id,
  modelId,
  targets: [{ target: { case: "connectionId" as const, value: connectionId } }],
});
const poolTarget = (id: string, modelId: string, poolId: string) => ({
  id,
  modelId,
  targets: [{ target: { case: "poolId" as const, value: poolId } }],
});
type Fixture = {
  listPools?: (request: { page?: { pageToken?: string } }) => object;
  listRoutes?: (request: { page?: { pageToken?: string } }) => object;
  deleteProviderConnection?: (request: unknown) => object | Promise<object>;
  removed?: Set<string>;
};
function mount(fixture: Fixture, org = organization) {
  const cache = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const transport = createRouterTransport((r: ConnectRouter) => {
    r.service(ProviderService, {
      listProviderConnections: () => ({
        connections: connections.filter((c) => !fixture.removed?.has(c.id)),
      }),
      listProviderCatalog: () => ({
        providers: [{ id: "openai", displayName: "OpenAI" }],
      }),
      deleteProviderConnection: (request) =>
        fixture.deleteProviderConnection?.(request) ?? {},
    });
    r.service(PoolService, {
      listPools: (request) =>
        (fixture.listPools?.(request) ?? { pools: [] }) as never,
    });
    r.service(RoutingService, {
      listRoutes: (request) =>
        (fixture.listRoutes?.(request) ?? { routes: [] }) as never,
    });
  });
  const ui = (value: typeof organization) => (
    <ApiProvider queryClient={cache} rpcTransport={transport}>
      <ProvidersPage organization={value} session={session} />
    </ApiProvider>
  );
  const view = render(ui(org));
  return {
    ...view,
    cache,
    rerenderWith: (value: typeof organization) => view.rerender(ui(value)),
  };
}
async function openDisconnect(user: ReturnType<typeof userEvent.setup>) {
  const buttons = await screen.findAllByRole("button", { name: "Disconnect" });
  await user.click(buttons[0]!);
  return screen.getByRole("dialog");
}

describe("dependency state helper", () => {
  const base = {
    data: [] as unknown[],
    isError: false,
    fetchStatus: "idle" as const,
  };
  it("only reports Ready for a settled successful result", () => {
    expect(dependencyState({ ...base, data: undefined })).toBe(
      DependencyState.Checking,
    );
    expect(dependencyState({ ...base, data: undefined, isError: true })).toBe(
      DependencyState.Failed,
    );
    expect(dependencyState(base)).toBe(DependencyState.Ready);
    expect(dependencyState({ ...base, fetchStatus: "fetching" })).toBe(
      DependencyState.Refreshing,
    );
    expect(dependencyState({ ...base, isError: true })).toBe(
      DependencyState.Stale,
    );
    expect(dependencyState({ ...base, fetchStatus: "paused" })).toBe(
      DependencyState.Stale,
    );
  });
  it("matches pools by member and routes by direct connection target only", () => {
    const pools = [
      pool("p1", "A", "c1"),
      pool("p2", "A", "c1"),
      pool("p3", "B", "c2"),
    ] as never;
    expect(poolRows(pools, "c1")).toEqual([
      { id: "p1", label: "A" },
      { id: "p2", label: "A" },
    ]);
    const routes = [
      route("r1", "chat", "c1"),
      poolTarget("r2", "pool-chat", "p1"),
    ] as never;
    expect(routeRows(routes, "c1")).toEqual([{ id: "r1", label: "chat" }]);
  });
});

describe("provider disconnect confirmation", () => {
  it("shows the selected identity, focuses Cancel and dismisses without a request", async () => {
    const user = userEvent.setup();
    const deleteProviderConnection = rs.fn(() => ({}));
    mount({ deleteProviderConnection });
    const trigger = (
      await screen.findAllByRole("button", { name: "Disconnect" })
    )[0]!;
    await user.click(trigger);
    const dialog = screen.getByRole("dialog", {
      name: "Disconnect Work account?",
    });
    expect(dialog.getAttribute("class")).toContain("disconnect-dialog");
    expect(within(dialog).getByText("OpenAI · Acme Studio")).toBeTruthy();
    expect(
      within(dialog).getByText("Only your connection in this organization."),
    ).toBeTruthy();
    expect(within(dialog).getByText("Connection ID: c1")).toBeTruthy();
    expect(
      within(dialog).getByText(
        "Future requests can no longer use this connection. Your external provider account is not deleted.",
      ),
    ).toBeTruthy();
    expect(dialog.getAttribute("aria-describedby")).toBeTruthy();
    expect(document.activeElement?.textContent).toBe("Cancel");
    const footer = within(dialog).getAllByRole("button").slice(-2);
    expect(footer.map((b) => b.textContent)).toEqual([
      "Cancel",
      "Disconnect provider",
    ]);
    await user.click(
      within(dialog).getByRole("button", { name: "Close dialog" }),
    );
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(document.activeElement).toBe(trigger);
    await user.click(trigger);
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    await user.click(trigger);
    fireEvent(
      screen.getByRole("dialog"),
      new Event("cancel", { cancelable: true }),
    );
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(deleteProviderConnection).not.toHaveBeenCalled();
  });

  it("lists pools and direct routes as real lists without merging duplicate labels", async () => {
    const user = userEvent.setup();
    mount({
      listPools: () => ({
        pools: [
          pool("p1", "Primary accounts", "c1"),
          pool("p2", "Primary accounts", "c1"),
          pool("p3", "Other", "c2"),
        ],
      }),
      listRoutes: () => ({
        routes: [
          route("r1", "chat-primary", "c1"),
          poolTarget("r2", "pool-chat", "p1"),
          route("r3", "other", "c2"),
        ],
      }),
    });
    const dialog = await openDisconnect(user);
    const poolsSection = within(dialog).getByRole("region", {
      name: "Account pools",
    });
    await waitFor(() =>
      expect(within(poolsSection).getAllByRole("listitem")).toHaveLength(2),
    );
    expect(
      within(poolsSection)
        .getAllByRole("listitem")
        .map((i) => i.textContent),
    ).toEqual(["Primary accounts", "Primary accounts"]);
    const routesSection = within(dialog).getByRole("region", {
      name: "Direct routes",
    });
    expect(
      within(routesSection)
        .getAllByRole("listitem")
        .map((i) => i.textContent),
    ).toEqual(["chat-primary"]);
    expect(within(dialog).queryByText("pool-chat")).toBeNull();
    expect(within(dialog).getByText("Affected configuration")).toBeTruthy();
    expect(
      within(dialog).getByText("Routes through these pools also need review."),
    ).toBeTruthy();
    expect(
      within(dialog).getByText(
        "Disconnecting does not remove or repair dependent routing.",
      ),
    ).toBeTruthy();
    expect(
      within(dialog)
        .getByRole("link", { name: "Review routing" })
        .getAttribute("href"),
    ).toBe("/organizations/org-1/routing");
  });

  it("enumerates later pages and shows None only for settled empty results", async () => {
    const user = userEvent.setup();
    mount({
      listPools: (request) =>
        request.page?.pageToken
          ? { pools: [pool("p2", "Fallback accounts", "c1")] }
          : {
              pools: [pool("p9", "Unrelated", "c2")],
              page: { nextPageToken: "next" },
            },
      listRoutes: (request) =>
        request.page?.pageToken
          ? { routes: [route("r1", "later-route", "c1")] }
          : { routes: [], page: { nextPageToken: "next" } },
    });
    const dialog = await openDisconnect(user);
    expect(await within(dialog).findByText("Fallback accounts")).toBeTruthy();
    expect(await within(dialog).findByText("later-route")).toBeTruthy();
    expect(within(dialog).queryByText("None")).toBeNull();
  });

  it("renders None for a complete empty result in each category", async () => {
    const user = userEvent.setup();
    mount({});
    const dialog = await openDisconnect(user);
    await waitFor(() =>
      expect(within(dialog).getAllByText("None")).toHaveLength(2),
    );
    expect(within(dialog).queryByText("Checking dependencies…")).toBeNull();
  });

  it("keeps categories independent while pending and on failure, and still allows disconnect once", async () => {
    const user = userEvent.setup();
    const deleteProviderConnection = rs.fn((_request: unknown) => ({}));
    mount({
      listPools: () => new Promise(() => undefined),
      listRoutes: () => {
        throw new ConnectError("boom", Code.Unavailable);
      },
      deleteProviderConnection,
    });
    const dialog = await openDisconnect(user);
    await within(dialog).findByText("Could not load direct routes.");
    expect(within(dialog).getByText("Checking dependencies…")).toBeTruthy();
    expect(within(dialog).queryByText("None")).toBeNull();
    expect(
      within(dialog).getByText(
        "Dependencies could not be loaded. Review routing before disconnecting.",
      ),
    ).toBeTruthy();
    const confirm = within(dialog).getByRole("button", {
      name: "Disconnect provider",
    });
    expect((confirm as HTMLButtonElement).disabled).toBe(false);
    await user.click(confirm);
    await waitFor(() =>
      expect(deleteProviderConnection).toHaveBeenCalledTimes(1),
    );
    expect(deleteProviderConnection.mock.calls[0]?.[0]).toMatchObject({
      organizationId: "org-1",
      connectionId: "c1",
    });
  });

  it("keeps successful pool data visible when routes fail and reports pool failure separately", async () => {
    const user = userEvent.setup();
    mount({
      listPools: () => ({ pools: [pool("p1", "Primary accounts", "c1")] }),
      listRoutes: () => {
        throw new ConnectError("boom", Code.Unavailable);
      },
    });
    const dialog = await openDisconnect(user);
    await within(dialog).findByText("Could not load direct routes.");
    expect(within(dialog).getByText("Primary accounts")).toBeTruthy();
    expect(
      within(dialog).queryByText("Could not load account pools."),
    ).toBeNull();
    expect(within(dialog).queryByText("None")).toBeNull();
  });

  it("reports account pool failure while routes stay visible", async () => {
    const user = userEvent.setup();
    mount({
      listPools: () => {
        throw new ConnectError("boom", Code.Unavailable);
      },
      listRoutes: () => ({ routes: [route("r1", "chat-primary", "c1")] }),
    });
    const dialog = await openDisconnect(user);
    await within(dialog).findByText("Could not load account pools.");
    expect(within(dialog).getByText("chat-primary")).toBeTruthy();
    expect(within(dialog).queryByText("None")).toBeNull();
  });

  it("retains last loaded data while refreshing and when the refresh fails", async () => {
    const user = userEvent.setup();
    let calls = 0;
    let release: (() => void) | undefined;
    const { cache } = mount({
      listPools: () => {
        calls += 1;
        if (calls === 1)
          return { pools: [pool("p1", "Primary accounts", "c1")] };
        if (calls === 2)
          return new Promise((_, reject) => {
            release = () =>
              reject(new ConnectError("offline", Code.Unavailable));
          });
        return { pools: [] };
      },
    });
    const dialog = await openDisconnect(user);
    await within(dialog).findByText("Primary accounts");
    void act(
      () => void cache.refetchQueries({ queryKey: ["all-pools", "org-1"] }),
    );
    await within(dialog).findByText("Refreshing dependencies…");
    expect(within(dialog).getByText("Primary accounts")).toBeTruthy();
    await act(async () => release?.());
    await within(dialog).findByText("Last loaded data; could not refresh.");
    expect(within(dialog).getByText("Primary accounts")).toBeTruthy();
    expect(
      within(dialog).getByText(
        "Dependency information may be out of date. Review routing before disconnecting.",
      ),
    ).toBeTruthy();
  });

  it("never turns an empty last result into None when the refresh fails", async () => {
    const user = userEvent.setup();
    let calls = 0;
    const { cache } = mount({
      listPools: () => {
        calls += 1;
        if (calls === 1) return { pools: [] };
        throw new ConnectError("offline", Code.Unavailable);
      },
    });
    const dialog = await openDisconnect(user);
    await waitFor(() =>
      expect(within(dialog).getAllByText("None")).toHaveLength(2),
    );
    await act(async () => {
      await cache.refetchQueries({ queryKey: ["all-pools", "org-1"] });
    });
    await within(dialog).findByText("Last loaded data; could not refresh.");
    expect(within(dialog).getAllByText("None")).toHaveLength(1);
  });

  it("prevents duplicate submission, survives dismissal and reports the confirmed result", async () => {
    const user = userEvent.setup();
    let resolve: (() => void) | undefined;
    const deleteProviderConnection = rs.fn(
      (_request: unknown) =>
        new Promise<object>((done) => {
          resolve = () => done({});
        }),
    );
    mount({ deleteProviderConnection });
    const dialog = await openDisconnect(user);
    const confirm = within(dialog).getByRole("button", {
      name: "Disconnect provider",
    });
    await user.click(confirm);
    const pending = await within(dialog).findByRole("button", {
      name: "Disconnecting…",
    });
    expect((pending as HTMLButtonElement).disabled).toBe(true);
    expect(
      within(dialog).getByText(
        "Closing this dialog does not cancel a submitted change.",
      ),
    ).toBeTruthy();
    fireEvent.click(pending);
    await user.keyboard("{Enter}");
    expect(deleteProviderConnection).toHaveBeenCalledTimes(1);
    expect(screen.queryByText("Provider disconnected.")).toBeNull();
    // Escape dismisses while the request keeps running.
    fireEvent(dialog, new Event("cancel", { cancelable: true }));
    expect(screen.queryByRole("dialog")).toBeNull();
    await act(async () => resolve?.());
    await waitFor(() =>
      expect(screen.getByText("Provider disconnected.")).toBeTruthy(),
    );
    expect(deleteProviderConnection).toHaveBeenCalledTimes(1);
  });

  it("keeps identity, dependencies and the safe error after failure and allows retry", async () => {
    const user = userEvent.setup();
    let attempt = 0;
    const deleteProviderConnection = rs.fn((_request: unknown) => {
      attempt += 1;
      if (attempt === 1)
        throw new ConnectError("sensitive detail", Code.Unavailable);
      return {};
    });
    mount({
      listPools: () => ({ pools: [pool("p1", "Primary accounts", "c1")] }),
      deleteProviderConnection,
    });
    const dialog = await openDisconnect(user);
    await within(dialog).findByText("Primary accounts");
    await user.click(
      within(dialog).getByRole("button", { name: "Disconnect provider" }),
    );
    await waitFor(() =>
      expect(within(dialog).getAllByRole("alert").length).toBeGreaterThan(0),
    );
    expect(screen.queryByText("sensitive detail")).toBeNull();
    expect(screen.queryByText("Provider disconnected.")).toBeNull();
    expect(within(dialog).getByText("Connection ID: c1")).toBeTruthy();
    expect(within(dialog).getByText("Primary accounts")).toBeTruthy();
    const retry = within(dialog).getByRole("button", {
      name: "Disconnect provider",
    });
    expect((retry as HTMLButtonElement).disabled).toBe(false);
    await user.click(retry);
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(deleteProviderConnection).toHaveBeenCalledTimes(2);
    for (const call of deleteProviderConnection.mock.calls)
      expect(call[0]).toMatchObject({
        organizationId: "org-1",
        connectionId: "c1",
      });
    expect(screen.getByText("Provider disconnected.")).toBeTruthy();
  });

  it("does not show a previous connection's error in another connection's dialog", async () => {
    const user = userEvent.setup();
    const deleteProviderConnection = rs.fn((_request: unknown) => {
      throw new ConnectError("denied", Code.Unavailable);
    });
    mount({ deleteProviderConnection });
    const first = await openDisconnect(user);
    await user.click(
      within(first).getByRole("button", { name: "Disconnect provider" }),
    );
    await waitFor(() =>
      expect(within(first).getAllByRole("alert").length).toBeGreaterThan(0),
    );
    await user.click(within(first).getByRole("button", { name: "Cancel" }));
    const buttons = screen.getAllByRole("button", { name: "Disconnect" });
    await user.click(buttons[1]!);
    const second = screen.getByRole("dialog");
    expect(within(second).getByText("Connection ID: c2")).toBeTruthy();
    expect(within(second).queryByRole("alert")).toBeNull();
  });

  it("keeps another connection's confirmation disabled while a dismissed disconnect is outstanding", async () => {
    const user = userEvent.setup();
    let fail: (() => void) | undefined;
    const deleteProviderConnection = rs.fn(
      (_request: unknown) =>
        new Promise<object>((_done, reject) => {
          fail = () => reject(new ConnectError("denied", Code.Unavailable));
        }),
    );
    mount({ deleteProviderConnection });
    const first = await openDisconnect(user);
    await user.click(
      within(first).getByRole("button", { name: "Disconnect provider" }),
    );
    await within(first).findByRole("button", { name: "Disconnecting…" });
    // Dismiss while the request keeps running, then open the other connection.
    fireEvent(first, new Event("cancel", { cancelable: true }));
    expect(screen.queryByRole("dialog")).toBeNull();
    const buttons = screen.getAllByRole("button", { name: "Disconnect" });
    await user.click(buttons[1]!);
    const second = screen.getByRole("dialog");
    expect(within(second).getByText("Connection ID: c2")).toBeTruthy();
    // The shared action is still busy, so confirmation must not be accepted.
    const confirm = within(second).getByRole("button", {
      name: "Disconnect provider",
    }) as HTMLButtonElement;
    expect(confirm.disabled).toBe(true);
    expect(
      within(second).queryByRole("button", { name: "Disconnecting…" }),
    ).toBeNull();
    fireEvent.click(confirm);
    expect(deleteProviderConnection).toHaveBeenCalledTimes(1);
    // The first request's failure is never attributed to the second connection.
    await act(async () => fail?.());
    await waitFor(() => expect(confirm.disabled).toBe(false));
    expect(within(second).queryByRole("alert")).toBeNull();
    await user.click(confirm);
    expect(deleteProviderConnection).toHaveBeenCalledTimes(2);
    expect(deleteProviderConnection.mock.calls[1]![0]).toMatchObject({
      connectionId: "c2",
    });
  });

  it("closes the confirmation when the organization changes", async () => {
    const user = userEvent.setup();
    const deleteProviderConnection = rs.fn(() => ({}));
    const view = mount({ deleteProviderConnection });
    await openDisconnect(user);
    view.rerenderWith(
      create(OrganizationSchema, {
        id: "org-2",
        name: "Other",
        role: Role.MEMBER,
      }),
    );
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(deleteProviderConnection).not.toHaveBeenCalled();
  });

  it("moves focus to the providers list when the trigger disappears after success", async () => {
    const user = userEvent.setup();
    const removed = new Set<string>();
    mount({
      removed,
      deleteProviderConnection: () => {
        removed.add("c1");
        return {};
      },
    });
    const dialog = await openDisconnect(user);
    await user.click(
      within(dialog).getByRole("button", { name: "Disconnect provider" }),
    );
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await waitFor(() =>
      expect(document.activeElement).toBe(
        screen.getByRole("group", { name: "Your providers" }),
      ),
    );
  });
});

describe("shared ConfirmDialog consumers", () => {
  it("keeps the generic confirmation markup unchanged", () => {
    render(
      <ConfirmDialog
        title="Revoke?"
        onClose={() => undefined}
        onConfirm={() => undefined}
      >
        Body
      </ConfirmDialog>,
    );
    const dialog = screen.getByRole("dialog");
    expect(dialog.className).toBe("");
    expect(dialog.getAttribute("aria-describedby")).toBeNull();
    expect(within(dialog).getByText("Body").tagName).toBe("P");
  });
});
