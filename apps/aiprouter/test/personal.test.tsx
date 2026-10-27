// SPDX-License-Identifier: Apache-2.0
import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
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
  AccountService,
  OrganizationService,
  ProviderService,
  PoolService,
  RoutingService,
  KeyService,
  UsageService,
  OrganizationSchema,
  GetSessionResponseSchema,
  Role,
  ProviderAuthKind,
  KeyState,
  UsageSummarySchema,
} from "@aiprouter/client";
import { ApiProvider } from "../src/api";
import { KeysPage } from "../src/keys";
import { ProvidersPage } from "../src/providers";
import { SummaryCards } from "../src/usage";
import { Dialog, Button } from "../src/ui";
import { App } from "../src/app";
import { useState, type ReactNode } from "react";
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
afterEach(() => window.history.replaceState(null, "", "/"));
describe("personal key lifecycle", () => {
  it("issues a scoped key only on submit and removes the one-time secret when Done closes", async () => {
    const user = userEvent.setup();
    const createKey = rs.fn((_request: unknown) => ({
      rawKey: "one-time-sensitive-key",
    }));
    mount(<KeysPage {...props} />, (r) =>
      r.service(KeyService, { listKeys: () => ({ keys: [] }), createKey }),
    );
    await user.click(screen.getByRole("button", { name: "Create API key" }));
    expect(createKey).not.toHaveBeenCalled();
    await user.type(screen.getByLabelText("Name"), "Laptop");
    await user.selectOptions(screen.getByLabelText("Expiry"), "0");
    await user.click(
      within(screen.getByRole("dialog")).getByRole("button", {
        name: "Create API key",
      }),
    );
    await screen.findByText("This key is shown only once.");
    expect(createKey.mock.calls[0]?.[0]).toMatchObject({
      organizationId: "one",
      name: "Laptop",
    });
    expect(
      (createKey.mock.calls[0]?.[0] as { expiresAt?: unknown }).expiresAt,
    ).toBeUndefined();
    expect(screen.getByLabelText("API key").getAttribute("value")).toBe(
      "one-time-sensitive-key",
    );
    await user.click(screen.getByRole("button", { name: "Done" }));
    expect(screen.queryByDisplayValue("one-time-sensitive-key")).toBeNull();
    expect(localStorage.length).toBe(0);
    expect(sessionStorage.length).toBe(0);
  });
  it("focuses Cancel and never claims failed revocation succeeded", async () => {
    const user = userEvent.setup();
    const revokeKey = rs.fn(() => {
      throw new ConnectError("sensitive server detail", Code.Unavailable);
    });
    mount(<KeysPage {...props} />, (r) =>
      r.service(KeyService, {
        listKeys: () => ({
          keys: [
            {
              id: "key",
              name: "Laptop",
              state: KeyState.ACTIVE,
              prefix: "aipr_x",
            },
          ],
        }),
        revokeKey,
      }),
    );
    await user.click(await screen.findByRole("button", { name: "Revoke" }));
    expect(document.activeElement?.textContent).toBe("Cancel");
    expect(revokeKey).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "Revoke key" }));
    await waitFor(() =>
      expect(screen.getAllByRole("alert").length).toBeGreaterThan(0),
    );
    expect(screen.queryByText("Key revoked.")).toBeNull();
    expect(screen.queryByText("sensitive server detail")).toBeNull();
    expect(screen.getByRole("dialog")).toBeTruthy();
  });
});
describe("catalog and provider mutations", () => {
  it("uses the full RPC catalog and selected authentication metadata", async () => {
    const user = userEvent.setup();
    const createProviderConnection = rs.fn((_request: unknown) => ({}));
    mount(<ProvidersPage {...props} />, (r) => {
      r.service(ProviderService, {
        listProviderConnections: () => ({ connections: [] }),
        listProviderCatalog: () => ({
          providers: Array.from({ length: 106 }, (_, i) => ({
            id: `provider-${i}`,
            displayName: `Provider ${i}`,
            authKinds: [ProviderAuthKind.API_KEY],
          })),
        }),
        createProviderConnection,
      });
      r.service(PoolService, { listPools: () => ({ pools: [] }) });
      r.service(RoutingService, { listRoutes: () => ({ routes: [] }) });
    });
    await user.click(screen.getByRole("button", { name: "Connect provider" }));
    await waitFor(() =>
      expect(
        within(screen.getByLabelText("Provider")).getAllByRole("option"),
      ).toHaveLength(107),
    );
    await user.selectOptions(screen.getByLabelText("Provider"), "provider-105");
    await user.type(screen.getByLabelText("Account label"), "Personal");
    await user.type(screen.getByLabelText("API key"), "private-secret");
    expect(screen.getByLabelText("API key").getAttribute("type")).toBe(
      "password",
    );
    await user.click(screen.getByRole("button", { name: "Save connection" }));
    await waitFor(() =>
      expect(createProviderConnection).toHaveBeenCalledOnce(),
    );
    expect(createProviderConnection.mock.calls[0]?.[0]).toMatchObject({
      organizationId: "one",
      providerId: "provider-105",
      apiKey: "private-secret",
    });
    expect(screen.queryByDisplayValue("private-secret")).toBeNull();
  });
});
describe("unknown values and dialogs", () => {
  it("preserves missing estimates and known zero separately", () => {
    const { rerender } = render(<SummaryCards />);
    expect(screen.getAllByText("Unknown").length).toBeGreaterThan(0);
    rerender(
      <SummaryCards
        summary={create(UsageSummarySchema, {
          requestCount: 3n,
          knownCosts: [{ currency: "USD", decimalAmount: "0" }],
          missingCostCount: 3n,
          missingUsageCount: 2n,
          missingPriceCount: 1n,
        })}
      />,
    );
    expect(screen.getByText("USD 0")).toBeTruthy();
    expect(screen.getByText("3 requests with unknown cost")).toBeTruthy();
    expect(screen.getByText("2 missing usage · 1 missing prices")).toBeTruthy();
  });
  it("names the dialog, focuses its critical input, supports cancel, and restores trigger focus", async () => {
    const user = userEvent.setup();
    function Fixture() {
      const [open, setOpen] = useState(false);
      return (
        <>
          <Button onClick={() => setOpen(true)}>Open editor</Button>
          {open && (
            <Dialog title="Edit name" onClose={() => setOpen(false)}>
              <label>
                Name
                <input autoFocus />
              </label>
            </Dialog>
          )}
        </>
      );
    }
    render(<Fixture />);
    const trigger = screen.getByRole("button", { name: "Open editor" });
    await user.click(trigger);
    expect(document.activeElement).toBe(screen.getByLabelText("Name"));
    fireEvent(
      screen.getByRole("dialog", { name: "Edit name" }),
      new Event("cancel", { bubbles: false, cancelable: true }),
    );
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(document.activeElement).toBe(trigger);
  });
});
describe("organization boundary", () => {
  it("retains the first key-name input when the shell becomes dirty", async () => {
    window.history.replaceState(null, "", "/keys?organization=one");
    const user = userEvent.setup();
    mount(<App />, (r) => {
      r.service(AccountService, { getSession: () => session });
      r.service(OrganizationService, {
        listOrganizations: () => ({ organizations: [organization] }),
        getOrganization: () => ({ organization }),
      });
      r.service(KeyService, { listKeys: () => ({ keys: [] }) });
    });
    await user.click(
      await screen.findByRole("button", { name: "Create API key" }),
    );
    const input = screen.getByLabelText("Name");
    await user.type(input, "a");
    expect((input as HTMLInputElement).value).toBe("a");
    expect(
      within(screen.getByRole("dialog"))
        .getByRole("button", { name: "Create API key" })
        .hasAttribute("disabled"),
    ).toBe(false);
    const beforeUnload = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(beforeUnload);
    expect(beforeUnload.defaultPrevented).toBe(true);
  });
  it("removes previous keys and open dialogs immediately when switching organization", async () => {
    window.history.replaceState(null, "", "/keys?organization=one");
    const user = userEvent.setup();
    let secondRequested = false;
    mount(<App />, (r) => {
      r.service(AccountService, { getSession: () => session });
      r.service(OrganizationService, {
        listOrganizations: () => ({
          organizations: [
            organization,
            { id: "two", name: "Second", role: Role.MEMBER },
          ],
        }),
        getOrganization: (req) => ({
          organization:
            req.organizationId === "one"
              ? organization
              : { id: "two", name: "Second", role: Role.MEMBER },
        }),
      });
      r.service(KeyService, {
        listKeys: async (req) => {
          if (req.organizationId === "two") {
            secondRequested = true;
            return await new Promise(() => {});
          }
          return {
            keys: [
              { id: "old", name: "Old personal key", state: KeyState.ACTIVE },
            ],
          };
        },
      });
    });
    await screen.findByText("Old personal key");
    await user.click(screen.getByRole("button", { name: "Revoke" }));
    expect(screen.getByRole("dialog")).toBeTruthy();
    await user.click(
      screen.getAllByRole("button", { name: "Switch organization" })[0],
    );
    await user.click(screen.getByRole("button", { name: "Second Member" }));
    await waitFor(() => expect(secondRequested).toBe(true));
    expect(screen.queryByText("Old personal key")).toBeNull();
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(window.location.search).toContain("organization=two");
    expect(screen.queryByRole("link", { name: "Members" })).toBeNull();
  });
});

describe("routing and pools", () => {
  it("sends ordered personal pool members and rejects duplicate names before submission", async () => {
    const { PoolsPage } = await import("../src/routing");
    const user = userEvent.setup();
    const createPool = rs.fn((_request: unknown) => ({}));
    mount(<PoolsPage {...props} />, (r) => {
      r.service(ProviderService, {
        listProviderConnections: () => ({
          connections: [
            { id: "a", name: "First", state: 1 },
            { id: "b", name: "Second", state: 1 },
          ],
        }),
      });
      r.service(PoolService, {
        listPools: () => ({
          pools: [{ id: "existing", name: "Taken", strategy: 1, members: [] }],
        }),
        createPool,
      });
      r.service(RoutingService, { listRoutes: () => ({ routes: [] }) });
    });
    await user.click(screen.getByRole("button", { name: "Create pool" }));
    await user.type(screen.getByLabelText("Pool name"), "Taken");
    await user.selectOptions(screen.getByLabelText("Add a connection"), "a");
    await user.selectOptions(screen.getByLabelText("Add a connection"), "b");
    expect(
      (screen.getByRole("button", { name: "Save pool" }) as HTMLButtonElement)
        .disabled,
    ).toBe(true);
    expect(createPool).not.toHaveBeenCalled();
    await user.clear(screen.getByLabelText("Pool name"));
    await user.type(screen.getByLabelText("Pool name"), "New pool");
    await user.click(screen.getAllByRole("button", { name: "Move up" })[1]);
    await user.click(screen.getByRole("button", { name: "Save pool" }));
    await waitFor(() => expect(createPool).toHaveBeenCalledOnce());
    expect(createPool.mock.calls[0][0]).toMatchObject({
      organizationId: "one",
      members: [
        { connectionId: "b", priority: 0 },
        { connectionId: "a", priority: 1 },
      ],
    });
  });
  it("creates a real route with an explicit model and protobuf connection target", async () => {
    const { RoutingPage } = await import("../src/routing");
    const user = userEvent.setup();
    const createRoute = rs.fn((_request: unknown) => ({}));
    mount(<RoutingPage {...props} />, (r) => {
      r.service(ProviderService, {
        listProviderConnections: () => ({
          connections: [{ id: "a", name: "First", state: 1 }],
        }),
      });
      r.service(PoolService, { listPools: () => ({ pools: [] }) });
      r.service(RoutingService, {
        listRoutes: () => ({ routes: [] }),
        createRoute,
      });
    });
    await user.click(screen.getByRole("button", { name: "Create route" }));
    await user.type(screen.getByLabelText("Model alias"), "coding");
    expect(
      (
        screen.getByRole("button", {
          name: "Save routing",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
    await user.click(screen.getByRole("button", { name: "Add target" }));
    await user.selectOptions(
      screen.getByLabelText("Connection or pool"),
      "connectionId:a",
    );
    await user.type(
      screen.getByLabelText("Provider model ID"),
      "model-from-provider",
    );
    await user.click(screen.getByRole("button", { name: "Save routing" }));
    await waitFor(() => expect(createRoute).toHaveBeenCalledOnce());
    expect(createRoute.mock.calls[0][0]).toMatchObject({
      organizationId: "one",
      modelId: "coding",
      targets: [
        {
          target: { case: "connectionId", value: "a" },
          modelId: "model-from-provider",
          priority: 0,
          weight: 1,
        },
      ],
    });
  });
});

it("clears privileged navigation after a scoped read loses permission without ending login", async () => {
  window.history.replaceState(null, "", "/keys?organization=one");
  mount(<App />, (r) => {
    r.service(AccountService, { getSession: () => session });
    r.service(OrganizationService, {
      listOrganizations: () => ({
        organizations: [{ ...organization, role: Role.OWNER }],
      }),
      getOrganization: () => ({
        organization: { ...organization, role: Role.OWNER },
      }),
    });
    r.service(KeyService, {
      listKeys: () => {
        throw new ConnectError("membership removed", Code.PermissionDenied);
      },
    });
  });
  await screen.findByRole("heading", { name: "Refresh your permissions" });
  expect(screen.queryByRole("link", { name: "Members" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Create API key" })).toBeNull();
  expect(
    screen.getAllByRole("button", { name: "Sign out" }).length,
  ).toBeGreaterThan(0);
  expect(screen.queryByText("Continue with your identity provider")).toBeNull();
});

it("sends provider and explicit unknown-outcome filters to records, summary, and daily RPCs", async () => {
  const { UsagePage } = await import("../src/usage");
  const user = userEvent.setup();
  const listUsage = rs.fn((_request: unknown) => ({ records: [] }));
  const getUsageSummary = rs.fn((_request: unknown) => ({}));
  const listDailyUsage = rs.fn((_request: unknown) => ({ days: [] }));
  mount(<UsagePage {...props} />, (r) =>
    r.service(UsageService, { listUsage, getUsageSummary, listDailyUsage }),
  );
  await user.type(screen.getByLabelText("Provider ID"), "openai");
  await user.selectOptions(screen.getByLabelText("Outcome"), "0");
  await waitFor(() => {
    for (const mock of [listUsage, getUsageSummary, listDailyUsage])
      expect(mock.mock.lastCall?.[0]).toMatchObject({
        organizationId: "one",
        userId: "person",
        providerId: "openai",
        outcome: 0,
      });
  });
  expect(
    screen.queryByText(/Outcome filtering applies to this page/),
  ).toBeNull();
});

describe("provider OAuth integration", () => {
  it("uses server-advertised methods, passes callback options, and correlates pasted PKCE completion", async () => {
    const { ProviderOAuthMethod: Method, OAuthState } =
      await import("@aiprouter/client");
    const user = userEvent.setup();
    const begin = rs.fn((_request: unknown) => ({
      authorization: {
        id: "auth",
        state: OAuthState.PENDING,
        authorizationUrl: "https://provider.example.test/login",
        callbackState: "safe-state",
      },
    }));
    const complete = rs.fn((_request: unknown) => ({}));
    mount(<ProvidersPage {...props} />, (r) => {
      r.service(ProviderService, {
        listProviderConnections: () => ({ connections: [] }),
        listProviderCatalog: () => ({
          providers: [
            {
              id: "catalog-provider",
              displayName: "Catalog provider",
              authKinds: [ProviderAuthKind.OAUTH],
              oauthMethods: [Method.PKCE],
            },
          ],
        }),
        beginProviderOAuth: begin,
        getProviderOAuth: () => ({
          authorization: {
            id: "auth",
            state: OAuthState.PENDING,
            callbackState: "safe-state",
          },
        }),
        completeProviderOAuth: complete,
      });
      r.service(PoolService, { listPools: () => ({ pools: [] }) });
      r.service(RoutingService, { listRoutes: () => ({ routes: [] }) });
    });
    await user.click(screen.getByRole("button", { name: "Connect provider" }));
    await user.selectOptions(
      screen.getByLabelText("Provider"),
      "catalog-provider",
    );
    await user.type(screen.getByLabelText("Account label"), "Personal login");
    await user.click(
      screen.getByRole("button", { name: "Continue with OAuth" }),
    );
    expect(begin).not.toHaveBeenCalled();
    expect(
      within(screen.getByLabelText("Authorization method")).getAllByRole(
        "option",
      ),
    ).toHaveLength(2);
    await user.selectOptions(
      screen.getByLabelText("Authorization method"),
      String(Method.PKCE),
    );
    await user.type(
      screen.getByLabelText("Callback URI"),
      "http://127.0.0.1:9999/callback",
    );
    await user.click(
      screen.getByRole("button", { name: "Start authorization" }),
    );
    await screen.findByRole("dialog", { name: "Provider authorization" });
    expect(begin.mock.calls[0][0]).toMatchObject({
      organizationId: "one",
      providerId: "catalog-provider",
      method: Method.PKCE,
      redirectUri: "http://127.0.0.1:9999/callback",
    });
    const input = screen.getByLabelText(
      "Authorization code, token, or callback URL",
    );
    expect(input.getAttribute("type")).toBe("password");
    await user.type(
      input,
      "http://127.0.0.1:9999/callback?code=one-time-code&state=safe-state",
    );
    await user.click(
      screen.getByRole("button", { name: "Complete authorization" }),
    );
    await waitFor(() => expect(complete).toHaveBeenCalledOnce());
    expect(complete.mock.calls[0][0]).toMatchObject({
      organizationId: "one",
      authorizationId: "auth",
      code: "one-time-code",
      state: "safe-state",
    });
    expect(window.location.href).not.toContain("one-time-code");
    expect(screen.queryByRole("dialog")).toBeNull();
  });
  it("rejects a mismatched PKCE callback and preserves native callback payloads", async () => {
    const { ProviderOAuthMethod: Method } = await import("@aiprouter/client");
    const { oauthCompletion } = await import("../src/providers");
    expect(() =>
      oauthCompletion(
        "https://callback.example.test/?code=x&state=wrong",
        Method.PKCE,
        "expected",
      ),
    ).toThrow();
    const native = "zed://login?token=private-token";
    expect(oauthCompletion(native, Method.NATIVE_CALLBACK, "expected")).toEqual(
      { code: native, state: "expected" },
    );
  });
  it("uses server default when methods are absent and shows polled completion without a manual token form", async () => {
    const { OAuthState } = await import("@aiprouter/client");
    const user = userEvent.setup();
    const begin = rs.fn((_request: unknown) => ({
      authorization: {
        id: "device-auth",
        state: OAuthState.PENDING,
        userCode: "ABCD",
        pollIntervalSeconds: 7,
        callbackState: "safe",
      },
    }));
    mount(<ProvidersPage {...props} />, (r) => {
      r.service(ProviderService, {
        listProviderConnections: () => ({ connections: [] }),
        listProviderCatalog: () => ({
          providers: [
            {
              id: "default-provider",
              displayName: "Default provider",
              authKinds: [ProviderAuthKind.OAUTH],
            },
          ],
        }),
        beginProviderOAuth: begin,
        getProviderOAuth: () => ({
          authorization: {
            id: "device-auth",
            state: OAuthState.COMPLETED,
            connectionId: "created-connection",
          },
        }),
      });
      r.service(PoolService, { listPools: () => ({ pools: [] }) });
      r.service(RoutingService, { listRoutes: () => ({ routes: [] }) });
    });
    await user.click(screen.getByRole("button", { name: "Connect provider" }));
    await user.selectOptions(
      screen.getByLabelText("Provider"),
      "default-provider",
    );
    await user.type(screen.getByLabelText("Account label"), "Default login");
    await user.click(
      screen.getByRole("button", { name: "Continue with OAuth" }),
    );
    expect(screen.queryByLabelText("Authorization method")).toBeNull();
    await user.click(
      screen.getByRole("button", { name: "Start authorization" }),
    );
    await screen.findByText(
      "Provider connected. Your connection is ready in this organization.",
    );
    expect(
      (begin.mock.calls[0][0] as { method?: unknown }).method,
    ).toBeUndefined();
    expect(
      screen.queryByLabelText("Authorization code, token, or callback URL"),
    ).toBeNull();
  });
});

it("preserves existing pool affinity and explicitly sends opt-out updates", async () => {
  const { PoolsPage } = await import("../src/routing");
  const user = userEvent.setup();
  const updatePool = rs.fn((_request: unknown) => ({}));
  mount(<PoolsPage {...props} />, (r) => {
    r.service(ProviderService, {
      listProviderConnections: () => ({
        connections: [{ id: "a", name: "First", state: 1 }],
      }),
    });
    r.service(PoolService, {
      listPools: () => ({
        pools: [
          {
            id: "pool",
            name: "Session pool",
            strategy: 1,
            affinityEnabled: true,
            members: [{ connectionId: "a", priority: 0, weight: 1 }],
          },
        ],
      }),
      updatePool,
    });
    r.service(RoutingService, { listRoutes: () => ({ routes: [] }) });
  });
  await user.click(await screen.findByRole("button", { name: "Edit pool" }));
  const checkbox = screen.getByRole("checkbox", {
    name: "Keep generic OAuth sessions on the same account",
  });
  expect((checkbox as HTMLInputElement).checked).toBe(true);
  await user.click(checkbox);
  await user.click(screen.getByRole("button", { name: "Save pool" }));
  await waitFor(() => expect(updatePool).toHaveBeenCalledOnce());
  expect(updatePool.mock.calls[0][0]).toMatchObject({
    organizationId: "one",
    poolId: "pool",
    affinityEnabled: false,
  });
});
