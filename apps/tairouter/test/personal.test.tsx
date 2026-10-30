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
} from "@tairouter/client";
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
    await user.click(
      await screen.findByRole("button", { name: "Revoke Laptop" }),
    );
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
    await user.click(
      screen.getByRole("button", { name: "Revoke Old personal key" }),
    );
    expect(screen.getByRole("dialog")).toBeTruthy();
    await user.click(
      screen.getAllByRole("button", { name: "Switch organization" })[0],
    );
    await user.click(screen.getByRole("button", { name: "Second Member" }));
    await waitFor(() => expect(secondRequested).toBe(true));
    expect(screen.queryByText("Old personal key")).toBeNull();
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(window.location.pathname).toBe("/organizations/two/keys");
    expect(screen.queryByRole("link", { name: "Members" })).toBeNull();
  });
});

const rawKey = "aipr_one-time-sensitive-key";
type Deferred = {
  promise: Promise<void>;
  resolve: () => void;
  reject: () => void;
};
function deferred(): Deferred {
  let resolve!: () => void;
  let reject!: () => void;
  const promise = new Promise<void>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}
function stubClipboard(writeText: (text: string) => Promise<void>) {
  const fn = rs.fn(writeText);
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: { writeText: fn },
  });
  return fn;
}
async function openResult(
  value = rawKey,
  organizationOverride = organization,
  cache?: { current?: QueryClient },
) {
  const user = userEvent.setup();
  const mounted = mount(
    <KeysPage {...props} organization={organizationOverride} />,
    (r) =>
      r.service(KeyService, {
        listKeys: () => ({ keys: [] }),
        createKey: () => ({ rawKey: value }),
      }),
  );
  if (cache) cache.current = mounted.cache;
  const trigger = screen.getByRole("button", { name: "Create API key" });
  await user.click(trigger);
  await user.type(screen.getByLabelText("Name"), "Laptop");
  await user.click(
    within(screen.getByRole("dialog")).getByRole("button", {
      name: "Create API key",
    }),
  );
  await screen.findByText("This key is shown only once.");
  return { user, trigger, ...mounted };
}

describe("one-time API key result dialog", () => {
  it("renders the specified content in order with described, read-only input", async () => {
    await openResult();
    const dialog = screen.getByRole("dialog", { name: "Your new API key" });
    expect(dialog.className).toContain("key-result");
    const text = dialog.textContent ?? "";
    const order = [
      "Your new API key",
      "Your key in Studio.",
      "This key is shown only once.",
      "Store it securely before closing this dialog.",
      "Copy API key",
      "You cannot view this key again after closing.",
      "Done",
    ].map((part) => text.indexOf(part));
    expect(order.every((index) => index >= 0)).toBe(true);
    expect([...order].sort((a, b) => a - b)).toEqual(order);
    expect(
      within(dialog).getByRole("button", { name: "Close dialog" }),
    ).toBeTruthy();
    expect(within(dialog).getAllByRole("button", { name: "Done" }).length).toBe(
      1,
    );
    expect(screen.getByText("This key is shown only once.").tagName).toBe(
      "STRONG",
    );
    const input = screen.getByLabelText("API key") as HTMLInputElement;
    expect(input.readOnly).toBe(true);
    expect(input.type).toBe("text");
    expect(input.value).toBe(rawKey);
    const described = (input.getAttribute("aria-describedby") ?? "")
      .split(" ")
      .map((id) => document.getElementById(id)?.textContent ?? "");
    expect(described.join(" ")).toContain("Store it securely");
    expect(described.join(" ")).toContain("cannot view this key again");
    expect(described.join(" ")).not.toContain(rawKey);
    expect(document.activeElement).toBe(input);
  });

  it("keeps the copy status empty and in one polite region that never contains the key", async () => {
    const { user } = await openResult();
    const writeText = stubClipboard(async () => {});
    const status = screen.getAllByRole("status")[0]!;
    const regions = within(screen.getByRole("dialog")).getAllByRole("status");
    expect(regions.length).toBe(1);
    expect(regions[0]!.textContent).toBe("");
    expect(writeText).not.toHaveBeenCalled();
    const copy = screen.getByRole("button", { name: "Copy API key" });
    expect(copy.getAttribute("aria-describedby")).toBe(regions[0]!.id);
    await user.click(copy);
    await waitFor(() => expect(regions[0]!.textContent).toBe("Copied."));
    expect(writeText).toHaveBeenCalledTimes(1);
    expect(writeText).toHaveBeenCalledWith(rawKey);
    expect(status.textContent).not.toContain(rawKey);
    expect(document.activeElement).toBe(copy);
  });

  it("reports rejection, keeps the dialog open and allows retry", async () => {
    const { user } = await openResult();
    let fail = true;
    const writeText = stubClipboard(async () => {
      if (fail) throw new Error("denied");
    });
    const copy = screen.getByRole("button", { name: "Copy API key" });
    await user.click(copy);
    const status = within(screen.getByRole("dialog")).getByRole("status");
    await waitFor(() =>
      expect(status.textContent).toBe(
        "Copy failed. Select and copy the value manually.",
      ),
    );
    expect(screen.getByRole("dialog")).toBeTruthy();
    expect((screen.getByLabelText("API key") as HTMLInputElement).value).toBe(
      rawKey,
    );
    expect(status.textContent).not.toContain(rawKey);
    expect((copy as HTMLButtonElement).disabled).toBe(false);
    fail = false;
    await user.click(copy);
    await waitFor(() => expect(status.textContent).toBe("Copied."));
    expect(writeText).toHaveBeenCalledTimes(2);
    expect(writeText).toHaveBeenNthCalledWith(2, rawKey);
  });

  it("does not require copying before Done and never touches the clipboard automatically", async () => {
    const { user } = await openResult();
    const writeText = stubClipboard(async () => {});
    await user.click(screen.getByRole("button", { name: "Done" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(writeText).not.toHaveBeenCalled();
  });

  it("contains Tab in the order Close, input, Copy, Done", async () => {
    const { user } = await openResult();
    const close = screen.getByRole("button", { name: "Close dialog" });
    const input = screen.getByLabelText("API key");
    const copy = screen.getByRole("button", { name: "Copy API key" });
    const done = screen.getByRole("button", { name: "Done" });
    expect(document.activeElement).toBe(input);
    await user.tab();
    expect(document.activeElement).toBe(copy);
    await user.tab();
    expect(document.activeElement).toBe(done);
    await user.tab();
    expect(document.activeElement).toBe(close);
    await user.tab();
    expect(document.activeElement).toBe(input);
    await user.tab({ shift: true });
    expect(document.activeElement).toBe(close);
    await user.tab({ shift: true });
    expect(document.activeElement).toBe(done);
  });

  it.each([
    [
      "Done",
      async () =>
        userEvent.setup().click(screen.getByRole("button", { name: "Done" })),
    ],
    [
      "Close",
      async () =>
        userEvent
          .setup()
          .click(screen.getByRole("button", { name: "Close dialog" })),
    ],
    [
      "Escape",
      async () => {
        fireEvent(
          screen.getByRole("dialog"),
          new Event("cancel", { cancelable: true }),
        );
      },
    ],
  ])(
    "%s clears the raw value, dismisses and returns focus to the trigger",
    async (_name, dismiss) => {
      const { trigger } = await openResult();
      await dismiss();
      await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
      expect(screen.queryByDisplayValue(rawKey)).toBeNull();
      expect(document.body.textContent).not.toContain(rawKey);
      expect(document.activeElement).toBe(trigger);
    },
  );

  it("shows nothing when the clipboard settles after dismissal", async () => {
    const { user } = await openResult();
    const pending = deferred();
    const writeText = stubClipboard(() => pending.promise);
    await user.click(screen.getByRole("button", { name: "Copy API key" }));
    expect(writeText).toHaveBeenCalledTimes(1);
    await user.click(screen.getByRole("button", { name: "Done" }));
    pending.resolve();
    await pending.promise;
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(document.body.textContent).not.toContain("Copied.");
    expect(document.body.textContent).not.toContain("Copy failed");
  });

  it("shows nothing when the clipboard rejects after dismissal", async () => {
    const { user } = await openResult();
    const pending = deferred();
    stubClipboard(() => pending.promise);
    await user.click(screen.getByRole("button", { name: "Copy API key" }));
    await user.click(screen.getByRole("button", { name: "Close dialog" }));
    pending.reject();
    await pending.promise.catch(() => {});
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(document.body.textContent).not.toContain("Copy failed");
  });

  it("keeps the raw key out of storage, history, URL, cache and the later list", async () => {
    const holder: { current?: QueryClient } = {};
    const { user } = await openResult(rawKey, organization, holder);
    expect(localStorage.length).toBe(0);
    expect(sessionStorage.length).toBe(0);
    expect(window.location.href).not.toContain(rawKey);
    expect(JSON.stringify(window.history.state ?? null)).not.toContain(rawKey);
    const snapshot = () =>
      JSON.stringify(
        holder
          .current!.getQueryCache()
          .getAll()
          .map((query) => query.state.data),
        (_key, value) => (typeof value === "bigint" ? value.toString() : value),
      );
    expect(snapshot()).not.toContain(rawKey);
    await user.click(screen.getByRole("button", { name: "Done" }));
    expect(snapshot()).not.toContain(rawKey);
    expect(document.body.textContent).not.toContain(rawKey);
    expect(localStorage.length).toBe(0);
    expect(sessionStorage.length).toBe(0);
  });

  it("renders a 256-character key and 100-character organization name unchanged", async () => {
    const longKey = `aipr_${"k".repeat(251)}`;
    const longOrganization = create(OrganizationSchema, {
      id: "one",
      name: "O".repeat(100),
      role: Role.MEMBER,
    });
    await openResult(longKey, longOrganization);
    expect(longKey.length).toBe(256);
    expect((screen.getByLabelText("API key") as HTMLInputElement).value).toBe(
      longKey,
    );
    expect(
      within(screen.getByRole("dialog")).getByText(
        `Your key in ${"O".repeat(100)}.`,
      ),
    ).toBeTruthy();
  });

  it("leaves other dialogs without result-scoped styling hooks", async () => {
    const user = userEvent.setup();
    mount(<KeysPage {...props} />, (r) =>
      r.service(KeyService, { listKeys: () => ({ keys: [] }) }),
    );
    await user.click(screen.getByRole("button", { name: "Create API key" }));
    expect(screen.getByRole("dialog").className).not.toContain("key-result");
  });

  it("removes the dialog, status and key when the organization scope changes", async () => {
    window.history.replaceState(null, "", "/keys?organization=one");
    const user = userEvent.setup();
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
        listKeys: () => ({ keys: [] }),
        createKey: () => ({ rawKey }),
      });
    });
    await user.click(
      await screen.findByRole("button", { name: "Create API key" }),
    );
    await user.type(screen.getByLabelText("Name"), "Laptop");
    await user.click(
      within(screen.getByRole("dialog")).getByRole("button", {
        name: "Create API key",
      }),
    );
    await screen.findByText("This key is shown only once.");
    stubClipboard(async () => {});
    await user.click(screen.getByRole("button", { name: "Copy API key" }));
    await screen.findByText("Copied.");
    await user.click(
      screen.getAllByRole("button", { name: "Switch organization" })[0]!,
    );
    await user.click(screen.getByRole("button", { name: "Second Member" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(screen.queryByText("Copied.")).toBeNull();
    expect(document.body.textContent).not.toContain(rawKey);
    expect(screen.queryByDisplayValue(rawKey)).toBeNull();
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
    await user.click(screen.getByRole("button", { name: "Move up Second" }));
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
      await import("@tairouter/client");
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
    const { ProviderOAuthMethod: Method } = await import("@tairouter/client");
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
    const { OAuthState } = await import("@tairouter/client");
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

describe("provider OAuth setup dialog", () => {
  type SetupOptions = {
    methods?: number[];
    begin?: (request: unknown) => unknown;
    reconnect?: boolean;
  };
  async function openSetup(options: SetupOptions = {}) {
    const { ProviderOAuthMethod: Method, OAuthState } =
      await import("@tairouter/client");
    const user = userEvent.setup();
    const begin = rs.fn(
      options.begin ??
        ((_request: unknown) => ({
          authorization: {
            id: "auth",
            state: OAuthState.PENDING,
            authorizationUrl: "https://provider.example.test/login",
            callbackState: "safe-state",
          },
        })),
    );
    const ui = mount(<ProvidersPage {...props} />, (r) => {
      r.service(ProviderService, {
        listProviderConnections: () => ({
          connections: options.reconnect
            ? [
                {
                  id: "conn-1",
                  providerId: "catalog-provider",
                  name: "Saved login",
                  authKind: ProviderAuthKind.OAUTH,
                },
              ]
            : [],
        }),
        listProviderCatalog: () => ({
          providers: [
            {
              id: "catalog-provider",
              displayName: "Catalog provider",
              authKinds: [ProviderAuthKind.OAUTH],
              oauthMethods: options.methods ?? [],
            },
          ],
        }),
        beginProviderOAuth: begin as never,
        getProviderOAuth: () => ({
          authorization: { id: "auth", state: OAuthState.PENDING },
        }),
      });
      r.service(PoolService, { listPools: () => ({ pools: [] }) });
      r.service(RoutingService, { listRoutes: () => ({ routes: [] }) });
    });
    if (options.reconnect) {
      await user.click(
        await screen.findByRole("button", { name: /^Reconnect/ }),
      );
    } else {
      await user.click(
        screen.getByRole("button", { name: "Connect provider" }),
      );
      await user.selectOptions(
        await screen.findByLabelText("Provider"),
        "catalog-provider",
      );
      await user.type(screen.getByLabelText("Account label"), "Personal login");
      await user.click(
        screen.getByRole("button", { name: "Continue with OAuth" }),
      );
    }
    const dialog = await screen.findByRole("dialog", {
      name: "Authorize Catalog provider",
    });
    return { user, begin, dialog, Method, ...ui };
  }

  it("shows the approved hierarchy, copy and initial focus", async () => {
    const { dialog } = await openSetup({ methods: [1] });
    const view = within(dialog);
    expect(view.getByRole("button", { name: "Close dialog" })).toBeTruthy();
    expect(
      view.getByText(
        "Use the callback and regional settings required by your provider.",
      ),
    ).toBeTruthy();
    expect(
      view.getByText(
        "Leave optional settings empty to use the provider defaults.",
      ),
    ).toBeTruthy();
    expect(view.getByRole("group", { name: "Authorization" })).toBeTruthy();
    const callback = view.getByLabelText("Callback URI") as HTMLInputElement;
    expect(callback.type).toBe("url");
    expect(callback.autocomplete).toBe("off");
    expect(callback.required).toBe(false);
    expect(document.activeElement).toBe(callback);
    const helper = document.getElementById(
      callback.getAttribute("aria-describedby")!.split(" ")[0],
    )!;
    expect(helper.textContent).toContain("exact registered callback URI");
    expect(helper.textContent).toContain("does not start a local listener");
    expect(helper.textContent).toContain(
      "Paste the resulting callback in the next step",
    );
    const before = view.getByRole("region", { name: "Before you continue" });
    expect(before.textContent).toContain(
      "Starting authorization does not mean the connection is complete.",
    );
    expect(dialog.className).toContain("oauth-setup");
    const advanced = dialog.querySelector("details")!;
    expect(advanced.open).toBe(false);
    expect(advanced.textContent).toContain(
      "Regional and advanced provider settings",
    );
    const footer = dialog.querySelector(".oauth-setup-footer")!;
    expect(within(footer as HTMLElement).getAllByRole("button")).toHaveLength(
      2,
    );
  });

  it("offers Provider default plus only advertised non-unspecified methods", async () => {
    const { ProviderOAuthMethod: Method } = await import("@tairouter/client");
    const { user, begin, dialog } = await openSetup({
      methods: [Method.UNSPECIFIED, Method.PKCE, Method.DEVICE],
    });
    const select = within(dialog).getByLabelText("Authorization method");
    expect(
      within(select)
        .getAllByRole("option")
        .map((o) => o.textContent),
    ).toEqual(["Provider default", "Browser sign-in (PKCE)", "Device code"]);
    await user.click(
      within(dialog).getByRole("button", { name: "Start authorization" }),
    );
    await screen.findByRole("dialog", { name: "Provider authorization" });
    expect(begin.mock.calls[0][0]).not.toHaveProperty("method", Method.PKCE);
    expect(
      (begin.mock.calls[0][0] as { method?: number }).method,
    ).toBeUndefined();
  });

  it("serializes the selected method exactly and omits the group select when none are advertised", async () => {
    const { ProviderOAuthMethod: Method } = await import("@tairouter/client");
    const first = await openSetup({ methods: [Method.PKCE, Method.DEVICE] });
    await first.user.selectOptions(
      within(first.dialog).getByLabelText("Authorization method"),
      String(Method.DEVICE),
    );
    await first.user.click(
      within(first.dialog).getByRole("button", { name: "Start authorization" }),
    );
    await screen.findByRole("dialog", { name: "Provider authorization" });
    expect(first.begin.mock.calls[0][0]).toMatchObject({
      method: Method.DEVICE,
    });
    first.unmount();
    const second = await openSetup({ methods: [] });
    expect(
      within(second.dialog).queryByLabelText("Authorization method"),
    ).toBeNull();
    expect(
      within(second.dialog).getByRole("group", { name: "Authorization" }),
    ).toBeTruthy();
  });

  it("sends all nine trimmed optional fields and omits blank values", async () => {
    const { user, begin, dialog } = await openSetup();
    const view = within(dialog);
    await user.click(view.getByText("Regional and advanced provider settings"));
    const filled: Record<string, string> = {
      "Login provider": "  github  ",
      "SSO region": " us-east-1 ",
      "API region": " eu-west-1 ",
      "Start URL": " https://start.example.test/ ",
      "API base URL": " https://api.example.test ",
      "Authentication base URL": " https://auth.example.test ",
      "Portal URL": " https://portal.example.test ",
      "Registration URL": " https://register.example.test ",
      "Client ID": " client-1 ",
    };
    for (const [label, value] of Object.entries(filled))
      await user.type(view.getByLabelText(label), value);
    await user.type(view.getByLabelText("Callback URI"), "   ");
    await user.click(view.getByRole("button", { name: "Start authorization" }));
    await screen.findByRole("dialog", { name: "Provider authorization" });
    expect(begin).toHaveBeenCalledOnce();
    const request = begin.mock.calls[0][0] as Record<string, unknown>;
    expect(request).toMatchObject({
      loginProvider: "github",
      ssoRegion: "us-east-1",
      apiRegion: "eu-west-1",
      startUrl: "https://start.example.test/",
      baseUrl: "https://api.example.test",
      authBaseUrl: "https://auth.example.test",
      portalUrl: "https://portal.example.test",
      registerUrl: "https://register.example.test",
      clientId: "client-1",
    });
    expect(request.redirectUri ?? "").toBe("");
  });

  it("keeps the disclosure from submitting and blocks invalid URLs without a request", async () => {
    const { user, begin, dialog } = await openSetup();
    const view = within(dialog);
    const advanced = dialog.querySelector("details")!;
    await user.click(view.getByText("Regional and advanced provider settings"));
    expect(advanced.open).toBe(true);
    await user.click(view.getByText("Regional and advanced provider settings"));
    expect(advanced.open).toBe(false);
    expect(begin).not.toHaveBeenCalled();
    await user.click(view.getByText("Regional and advanced provider settings"));
    await user.type(view.getByLabelText("Portal URL"), "not a url");
    await user.type(view.getByLabelText("Callback URI"), "also bad");
    await user.click(view.getByRole("button", { name: "Start authorization" }));
    expect(begin).not.toHaveBeenCalled();
    expect(view.getAllByText("Enter a valid URL.")).toHaveLength(2);
    expect(
      view.getByLabelText("Callback URI").getAttribute("aria-invalid"),
    ).toBe("true");
    expect(view.getByLabelText("Portal URL").getAttribute("aria-invalid")).toBe(
      "true",
    );
    expect(document.activeElement).toBe(view.getByLabelText("Callback URI"));
  });

  it("keeps focus on the field being corrected while another URL stays invalid", async () => {
    const { user, begin, dialog } = await openSetup();
    const view = within(dialog);
    await user.click(view.getByText("Regional and advanced provider settings"));
    await user.type(view.getByLabelText("Portal URL"), "not a url");
    await user.type(view.getByLabelText("Callback URI"), "also bad");
    await user.click(view.getByRole("button", { name: "Start authorization" }));
    const callback = view.getByLabelText("Callback URI");
    expect(document.activeElement).toBe(callback);
    // Keystrokes go to the focused element, so a focus jump mid-edit would
    // send the remaining characters to Portal URL instead of Callback URI.
    await user.keyboard(
      "{Control>}a{/Control}https://app.example.test/callback",
    );
    expect(callback.getAttribute("aria-invalid")).toBeNull();
    expect(view.getByLabelText("Portal URL").getAttribute("aria-invalid")).toBe(
      "true",
    );
    expect((callback as HTMLInputElement).value).toBe(
      "https://app.example.test/callback",
    );
    expect((view.getByLabelText("Portal URL") as HTMLInputElement).value).toBe(
      "not a url",
    );
    expect(document.activeElement).toBe(callback);
    expect(begin).not.toHaveBeenCalled();
    await user.click(view.getByRole("button", { name: "Start authorization" }));
    expect(begin).not.toHaveBeenCalled();
    expect(document.activeElement).toBe(view.getByLabelText("Portal URL"));
  });

  it("opens the collapsed disclosure and focuses an invalid advanced URL", async () => {
    const { user, begin, dialog } = await openSetup();
    const view = within(dialog);
    const advanced = dialog.querySelector("details")!;
    await user.click(view.getByText("Regional and advanced provider settings"));
    await user.type(view.getByLabelText("Start URL"), "nope");
    await user.click(view.getByText("Regional and advanced provider settings"));
    expect(advanced.open).toBe(false);
    await user.click(view.getByRole("button", { name: "Start authorization" }));
    expect(begin).not.toHaveBeenCalled();
    await waitFor(() => expect(advanced.open).toBe(true));
    expect(document.activeElement).toBe(view.getByLabelText("Start URL"));
  });

  it("hands new-account and reconnect identity unchanged to beginProviderOAuth", async () => {
    const created = await openSetup();
    await created.user.click(
      within(created.dialog).getByRole("button", {
        name: "Start authorization",
      }),
    );
    await screen.findByRole("dialog", { name: "Provider authorization" });
    expect(created.begin).toHaveBeenCalledOnce();
    expect(created.begin.mock.calls[0][0]).toMatchObject({
      organizationId: "one",
      providerId: "catalog-provider",
      name: "Personal login",
    });
    expect(created.begin.mock.calls[0][0]).not.toHaveProperty(
      "connectionId",
      "conn-1",
    );
    expect(screen.queryByText("Connected")).toBeNull();
    created.unmount();
    const reconnect = await openSetup({ reconnect: true });
    await reconnect.user.click(
      within(reconnect.dialog).getByRole("button", {
        name: "Start authorization",
      }),
    );
    await screen.findByRole("dialog", { name: "Provider authorization" });
    expect(reconnect.begin.mock.calls[0][0]).toMatchObject({
      organizationId: "one",
      providerId: "catalog-provider",
      name: "Saved login",
      connectionId: "conn-1",
    });
    expect(
      screen.getAllByText("Authorization started.").length,
    ).toBeGreaterThan(0);
  });

  it("blocks duplicate submits while pending and shows Starting", async () => {
    const pending = deferred();
    const { user, begin, dialog } = await openSetup({
      begin: async () => {
        await pending.promise;
        return { authorization: { id: "auth", state: 1 } };
      },
    });
    const submit = within(dialog).getByRole("button", {
      name: "Start authorization",
    });
    await user.click(submit);
    await waitFor(() =>
      expect(
        within(dialog).getByRole("button", { name: "Starting…" }),
      ).toHaveProperty("disabled", true),
    );
    fireEvent.submit(dialog.querySelector("form")!);
    fireEvent.submit(dialog.querySelector("form")!);
    expect(begin).toHaveBeenCalledOnce();
    pending.resolve();
    await screen.findByRole("dialog", { name: "Provider authorization" });
    expect(begin).toHaveBeenCalledOnce();
  });

  it("keeps entered options after a recoverable failure and shows the safe request ID", async () => {
    const { ErrorDetailSchema } = await import("@tairouter/client");
    let attempts = 0;
    const { user, begin, dialog } = await openSetup({
      begin: () => {
        attempts += 1;
        if (attempts === 1)
          throw new ConnectError(
            "Provider unavailable",
            Code.Unavailable,
            undefined,
            [
              {
                desc: ErrorDetailSchema,
                value: create(ErrorDetailSchema, { requestId: "req-123" }),
              },
            ],
          );
        return { authorization: { id: "auth", state: 1 } };
      },
    });
    const view = within(dialog);
    await user.type(
      view.getByLabelText("Callback URI"),
      "https://app.example.test/cb",
    );
    await user.click(view.getByRole("button", { name: "Start authorization" }));
    const alert = await view.findByRole("alert");
    expect(alert.textContent).toContain("The server is unavailable");
    expect(alert.textContent).toContain("req-123");
    const callback = view.getByLabelText("Callback URI") as HTMLInputElement;
    expect(callback.value).toBe("https://app.example.test/cb");
    expect(callback.getAttribute("aria-describedby")).toContain(
      alert.parentElement!.id,
    );
    await user.click(view.getByRole("button", { name: "Start authorization" }));
    await screen.findByRole("dialog", { name: "Provider authorization" });
    expect(begin).toHaveBeenCalledTimes(2);
  });

  it.each([
    [
      "Cancel",
      (view: ReturnType<typeof within>) =>
        userEvent.setup().click(view.getByRole("button", { name: "Cancel" })),
    ],
    [
      "Close",
      (view: ReturnType<typeof within>) =>
        userEvent
          .setup()
          .click(view.getByRole("button", { name: "Close dialog" })),
    ],
    [
      "Escape",
      (view: ReturnType<typeof within>) =>
        fireEvent(
          view.getByLabelText("Callback URI").closest("dialog")!,
          new Event("cancel", { cancelable: true }),
        ),
    ],
  ])("%s before submit issues no request", async (_name, dismiss) => {
    const { begin, dialog } = await openSetup();
    await dismiss(within(dialog));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(begin).not.toHaveBeenCalled();
  });

  it("drops scoped setup state when the page unmounts and never replays the request", async () => {
    const { begin, dialog, unmount } = await openSetup();
    expect(dialog).toBeTruthy();
    unmount();
    expect(screen.queryByRole("dialog")).toBeNull();
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(begin).not.toHaveBeenCalled();
  });
});
