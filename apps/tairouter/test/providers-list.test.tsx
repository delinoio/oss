// SPDX-License-Identifier: Apache-2.0
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, it, expect, rs, afterEach } from "@rstest/core";
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
} from "@tairouter/client";
import { ApiProvider } from "../src/api";
import { ProvidersPage } from "../src/providers";
import type { ReactNode } from "react";

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
const created = { seconds: 1_792_833_600n }; // Oct 24, 2026 09:20 UTC
const cooldown = { seconds: 1_792_920_000n };

type Row = {
  id: string;
  name: string;
  providerId: string;
  state: number;
  authKind: number;
  cooldownUntil?: { seconds: bigint };
  createdAt?: { seconds: bigint };
};
function row(partial: Partial<Row> & Pick<Row, "id" | "name">): Row {
  return {
    providerId: "openai",
    state: ConnectionState.READY,
    authKind: ProviderAuthKind.API_KEY,
    createdAt: created,
    ...partial,
  };
}
function mount(
  ui: ReactNode,
  connections: (request: { page?: { pageToken?: string } }) => unknown,
  catalog: () => unknown = () => ({
    providers: [{ id: "openai", displayName: "OpenAI" }],
  }),
) {
  const cache = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const transport = createRouterTransport((r: ConnectRouter) => {
    r.service(ProviderService, {
      listProviderConnections: connections as never,
      listProviderCatalog: catalog as never,
    });
    r.service(PoolService, { listPools: () => ({ pools: [] }) });
    r.service(RoutingService, { listRoutes: () => ({ routes: [] }) });
  });
  const wrap = (node: ReactNode) => (
    <ApiProvider queryClient={cache} rpcTransport={transport}>
      {node}
    </ApiProvider>
  );
  const view = render(wrap(ui));
  return {
    ...view,
    rerenderUi: (node: ReactNode) => view.rerender(wrap(node)),
  };
}
function setOnline(value: boolean) {
  Object.defineProperty(navigator, "onLine", { configurable: true, value });
}
afterEach(() => {
  setOnline(true);
});

const fourAccounts = () => ({
  connections: [
    row({
      id: "a",
      name: "Work account",
      authKind: ProviderAuthKind.OAUTH,
      createdAt: created,
    }),
    row({
      id: "b",
      name: "Backup account",
      providerId: "anthropic",
      authKind: ProviderAuthKind.OAUTH,
      state: ConnectionState.AUTHORIZATION_REQUIRED,
    }),
    row({ id: "c", name: "Development", createdAt: created }),
    row({
      id: "d",
      name: "Local",
      authKind: ProviderAuthKind.NO_AUTH,
      providerId: "local",
    }),
  ],
});

describe("providers list redesign", () => {
  it("orders the body as search, connections, pagination, then secondary entry cards", async () => {
    mount(<ProvidersPage {...props} />, fourAccounts);
    const search = await screen.findByLabelText("Search your connections");
    expect(
      screen.getByText("Search this page by account or provider"),
    ).toBeTruthy();
    const list = await screen.findByRole("list", {
      name: "Your provider connections",
    });
    const first = screen.getByRole("button", { name: "First page" });
    const pools = screen.getByRole("heading", { name: "Account pools" });
    const hosted = screen.getByRole("heading", { name: "Hosted helpers" });
    const before = (a: Node, b: Node) =>
      Boolean(a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING);
    expect(
      before(screen.getByRole("heading", { name: "Your providers" }), search),
    ).toBe(true);
    expect(before(search, list)).toBe(true);
    expect(before(list, first)).toBe(true);
    expect(before(first, pools)).toBe(true);
    expect(before(pools, hosted)).toBe(true);
    expect(
      screen
        .getByRole("link", { name: "Manage account pools" })
        .getAttribute("href"),
    ).toContain("pools");
    expect(
      screen.getByRole("button", { name: "Configure hosted helpers" }),
    ).toBeTruthy();
  });

  it("shows identity, provider name fallback, authentication and UTC timestamps without secrets", async () => {
    mount(<ProvidersPage {...props} />, fourAccounts);
    const rows = await screen.findAllByRole("listitem");
    expect(rows).toHaveLength(4);
    const work = within(rows[0]!);
    expect(work.getByRole("heading", { name: "Work account" })).toBeTruthy();
    expect(work.getByText("OpenAI")).toBeTruthy();
    expect(work.getByText("Connected")).toBeTruthy();
    expect(work.getByText("OAuth")).toBeTruthy();
    expect(work.getByText(/UTC$/)).toBeTruthy();
    expect(work.getByText("Added")).toBeTruthy();
    // Provider absent from the catalog falls back to its ID.
    expect(within(rows[1]!).getByText("anthropic")).toBeTruthy();
    expect(within(rows[2]!).getByText("API key")).toBeTruthy();
    expect(within(rows[3]!).getByText("No authentication")).toBeTruthy();
    expect(screen.queryByText(/last verified/i)).toBeNull();
  });

  it("offers Reconnect only for OAuth, emphasized when required, with account-specific names", async () => {
    mount(<ProvidersPage {...props} />, fourAccounts);
    await screen.findAllByRole("listitem");
    expect(screen.getAllByRole("button", { name: /^Reconnect / })).toHaveLength(
      2,
    );
    expect(
      screen.queryByRole("button", { name: "Reconnect Development" }),
    ).toBeNull();
    expect(
      screen.getByRole("button", { name: "Reconnect Backup account" })
        .className,
    ).toContain("primary");
    expect(
      screen.getByRole("button", { name: "Reconnect Work account" }).className,
    ).not.toContain("primary");
    for (const label of [
      "Edit connection for Development",
      "View models for Development",
      "Disconnect Development",
    ])
      expect(screen.getByRole("button", { name: label })).toBeTruthy();
    expect(
      screen.getByRole("button", { name: "Disconnect Development" }).className,
    ).toContain("danger");
  });

  it("renders every state as text and treats unknown values as unknown, never connected", async () => {
    mount(<ProvidersPage {...props} />, () => ({
      connections: [
        row({ id: "1", name: "S1", state: ConnectionState.READY }),
        row({
          id: "2",
          name: "S2",
          state: ConnectionState.AUTHORIZATION_REQUIRED,
        }),
        row({ id: "3", name: "S3", state: ConnectionState.DISABLED }),
        row({
          id: "4",
          name: "S4",
          state: ConnectionState.COOLDOWN,
          cooldownUntil: cooldown,
        }),
        row({ id: "5", name: "S5", state: ConnectionState.ERROR }),
        row({ id: "6", name: "S6", state: 999 }),
      ],
    }));
    const rows = await screen.findAllByRole("listitem");
    const expected = [
      "Connected",
      "Reconnect required",
      "Not connected",
      "Cooling down",
      "Connection failed",
      "Unknown status",
    ];
    rows.forEach((item, i) => {
      expect(within(item).getByText(expected[i]!).className).toContain("badge");
    });
    expect(
      within(rows[5]!).getByText("Unknown status").className,
    ).not.toContain("success");
    expect(within(rows[3]!).getByText("Cooling down until")).toBeTruthy();
    expect(within(rows[3]!).getByText(/Oct 25, 2026.*UTC/)).toBeTruthy();
  });

  it("opens the disconnect confirmation only from the explicit button", async () => {
    const user = userEvent.setup();
    mount(<ProvidersPage {...props} />, fourAccounts);
    await screen.findAllByRole("listitem");
    await user.click(screen.getByRole("heading", { name: "Development" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    await user.click(
      screen.getByRole("button", { name: "Disconnect Development" }),
    );
    expect(await screen.findByRole("dialog")).toBeTruthy();
  });

  it("searches the current page case-insensitively, clears filters, and keeps the cursor", async () => {
    const user = userEvent.setup();
    const tokens: string[] = [];
    mount(<ProvidersPage {...props} />, (request) => {
      const token = request.page?.pageToken ?? "";
      tokens.push(token);
      return token
        ? {
            connections: [row({ id: "n", name: "Second page" })],
            page: { nextPageToken: "" },
          }
        : {
            connections: [row({ id: "m", name: "First page account" })],
            page: { nextPageToken: "cursor-2" },
          };
    });
    await screen.findByText("First page account");
    const next = screen.getByRole("button", { name: "Next page" });
    expect(next.hasAttribute("disabled")).toBe(false);
    expect(
      screen
        .getByRole("button", { name: "First page" })
        .hasAttribute("disabled"),
    ).toBe(true);
    await user.click(next);
    await screen.findByText("Second page");
    expect(tokens).toContain("cursor-2");
    await user.type(screen.getByLabelText("Search your connections"), "OPENAI");
    expect(screen.getByText("Second page")).toBeTruthy();
    await user.clear(screen.getByLabelText("Search your connections"));
    await user.type(screen.getByLabelText("Search your connections"), "zzz");
    expect(
      screen.getByRole("heading", { name: "No matching connections" }),
    ).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Clear filters" }));
    expect(screen.getByText("Second page")).toBeTruthy();
    // Clearing the page-local filter keeps the cursor: First page is still available.
    expect(
      screen
        .getByRole("button", { name: "First page" })
        .hasAttribute("disabled"),
    ).toBe(false);
  });

  it("shows first-use guidance with Connect provider", async () => {
    mount(<ProvidersPage {...props} />, () => ({ connections: [] }));
    const empty = await screen.findByRole("heading", {
      name: "Connect your first provider",
    });
    const card = empty.closest("section")!;
    expect(
      within(card).getByRole("button", { name: "Connect provider" }),
    ).toBeTruthy();
  });

  it("labels loading and never turns a list error into an empty success", async () => {
    const user = userEvent.setup();
    let fail = true;
    mount(<ProvidersPage {...props} />, () => {
      if (fail) throw new ConnectError("secret detail", Code.Unavailable);
      return { connections: [row({ id: "r", name: "Recovered" })] };
    });
    expect(screen.getByText("Loading your connections…")).toBeTruthy();
    const alert = await screen.findByText(/server is unavailable/i);
    expect(alert).toBeTruthy();
    expect(screen.queryByText("secret detail")).toBeNull();
    expect(screen.queryByText("Connect your first provider")).toBeNull();
    expect(screen.queryByText("No matching connections")).toBeNull();
    fail = false;
    await user.click(screen.getByRole("button", { name: "Try again" }));
    await screen.findByText("Recovered");
  });

  it("shows a distinct access-denied notice without retry or empty success", async () => {
    mount(<ProvidersPage {...props} />, () => {
      throw new ConnectError("denied", Code.PermissionDenied);
    });
    await screen.findByText(/no longer have access to these providers/i);
    expect(screen.queryByRole("button", { name: "Try again" })).toBeNull();
    expect(screen.queryByText("Connect your first provider")).toBeNull();
  });

  it("falls back to provider IDs with a retryable notice when the catalog fails", async () => {
    mount(<ProvidersPage {...props} />, fourAccounts, () => {
      throw new ConnectError("catalog down", Code.Unavailable);
    });
    await screen.findAllByRole("listitem");
    await screen.findByText(/Provider names could not be loaded/);
    expect(screen.getAllByText("openai").length).toBeGreaterThan(0);
    expect(screen.getByRole("button", { name: "Try again" })).toBeTruthy();
  });

  it("flags offline data as possibly stale while keeping the records", async () => {
    setOnline(false);
    mount(<ProvidersPage {...props} />, fourAccounts);
    await screen.findAllByRole("listitem");
    expect(
      screen.getByText(
        "You are offline. These connections may be out of date.",
      ),
    ).toBeTruthy();
  });

  it("clears records and open editors when the organization scope changes", async () => {
    const user = userEvent.setup();
    const other = create(OrganizationSchema, {
      id: "two",
      name: "Other",
      role: Role.MEMBER,
    });
    const calls: string[] = [];
    const { rerenderUi } = mount(
      <ProvidersPage key="one" {...props} />,
      (request) => {
        const id =
          (request as { organizationId?: string }).organizationId ?? "";
        calls.push(id);
        return {
          connections: [
            row({
              id: `${id}-1`,
              name: id === "one" ? "Org one only" : "Org two only",
            }),
          ],
        };
      },
    );
    await screen.findByText("Org one only");
    await user.click(screen.getByRole("button", { name: "Connect provider" }));
    expect(screen.getByRole("dialog")).toBeTruthy();
    rerenderUi(
      <ProvidersPage key="two" organization={other} session={session} />,
    );
    await screen.findByText("Org two only");
    expect(screen.queryByText("Org one only")).toBeNull();
    expect(screen.queryByRole("dialog")).toBeNull();
    await waitFor(() => expect(calls).toContain("two"));
  });
});
