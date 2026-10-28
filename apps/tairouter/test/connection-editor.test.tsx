// SPDX-License-Identifier: Apache-2.0
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, it, expect, rs } from "@rstest/core";
import {
  createRouterTransport,
  ConnectError,
  Code,
  type ConnectRouter,
} from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";
import { QueryClient } from "@tanstack/react-query";
import {
  ProviderService,
  PoolService,
  RoutingService,
  ProviderConnectionSchema,
  OrganizationSchema,
  GetSessionResponseSchema,
  ErrorDetailSchema,
  ProviderAuthKind,
  Role,
} from "@tairouter/client";
import { ApiProvider } from "../src/api";
import { ProvidersPage } from "../src/providers";
import type { ReactNode } from "react";
const organization = create(OrganizationSchema, {
  id: "org",
  name: "Acme Studio",
  role: Role.MEMBER,
});
const session = create(GetSessionResponseSchema, { userId: "person" });
const catalog = [
  {
    id: "openai",
    displayName: "OpenAI",
    authKinds: [ProviderAuthKind.API_KEY, ProviderAuthKind.NO_AUTH],
  },
  {
    id: "anthropic",
    displayName: "Anthropic",
    authKinds: [ProviderAuthKind.OAUTH],
  },
];
function mount(ui: ReactNode, routes: (r: ConnectRouter) => void) {
  render(
    <ApiProvider
      rpcTransport={createRouterTransport(routes)}
      queryClient={
        new QueryClient({
          defaultOptions: {
            queries: { retry: false },
            mutations: { retry: false },
          },
        })
      }
    >
      {ui}
    </ApiProvider>,
  );
}
function page(
  r: ConnectRouter,
  service: Record<string, (request: never) => unknown> = {},
  providers: () => unknown = () => ({ providers: catalog }),
  connections: unknown[] = [],
) {
  r.service(ProviderService, {
    listProviderConnections: () => ({ connections }),
    listProviderCatalog: providers,
    ...service,
  } as never);
  r.service(PoolService, { listPools: () => ({ pools: [] }) });
  r.service(RoutingService, { listRoutes: () => ({ routes: [] }) });
}
const renderPage = () => (
  <ProvidersPage organization={organization} session={session} />
);
const providerOption = (name: string) =>
  within(screen.getByLabelText("Provider")).findByRole("option", { name });
async function openCreate(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole("button", { name: "Connect provider" }));
  await screen.findByRole("dialog", { name: "Connect provider" });
}
describe("create variant", () => {
  it("shows the grouped layout, focuses the provider search and scopes the subtitle", async () => {
    const user = userEvent.setup();
    mount(renderPage(), (r) => page(r));
    await openCreate(user);
    const dialog = screen.getByRole("dialog", { name: "Connect provider" });
    expect(within(dialog).getByText("Only yours in Acme Studio.")).toBeTruthy();
    for (const group of [
      "Provider and authentication",
      "Account details",
      "Connection settings",
    ])
      expect(within(dialog).getByRole("heading", { name: group })).toBeTruthy();
    expect(document.activeElement).toBe(
      screen.getByLabelText("Find a provider"),
    );
    expect(
      screen.getByLabelText("Find a provider").getAttribute("placeholder"),
    ).toBe("Search supported providers");
    expect(
      (
        within(dialog).getByText("Advanced connection settings")
          .parentElement as HTMLDetailsElement
      ).open,
    ).toBe(false);
    expect(
      within(dialog).getByText("Your credentials stay private to you."),
    ).toBeTruthy();
    expect(
      within(dialog).getAllByRole("button", { name: "Cancel" }),
    ).toHaveLength(1);
    expect(
      within(dialog).getAllByRole("button", { name: "Save connection" }),
    ).toHaveLength(1);
  });
  it("resets configuration and secret on provider change and selects the first auth kind", async () => {
    const user = userEvent.setup();
    const createProviderConnection = rs.fn((_request: unknown) => ({}));
    mount(renderPage(), (r) => page(r, { createProviderConnection }));
    await openCreate(user);
    await providerOption("Anthropic");
    await user.selectOptions(screen.getByLabelText("Provider"), "openai");
    expect(
      (screen.getByLabelText("Authentication") as HTMLSelectElement).value,
    ).toBe(String(ProviderAuthKind.API_KEY));
    await user.type(screen.getByLabelText("API key"), "key-for-openai");
    await user.selectOptions(screen.getByLabelText("Provider"), "anthropic");
    expect(
      (screen.getByLabelText("Authentication") as HTMLSelectElement).value,
    ).toBe(String(ProviderAuthKind.OAUTH));
    await user.selectOptions(screen.getByLabelText("Provider"), "openai");
    expect((screen.getByLabelText("API key") as HTMLInputElement).value).toBe(
      "",
    );
    expect(createProviderConnection).not.toHaveBeenCalled();
  });
  it("blocks an empty key with associated errors, then sends only the typed trimmed values", async () => {
    const user = userEvent.setup();
    const createProviderConnection = rs.fn((_request: unknown) => ({}));
    mount(renderPage(), (r) => page(r, { createProviderConnection }));
    await openCreate(user);
    await providerOption("OpenAI");
    await user.click(screen.getByRole("button", { name: "Save connection" }));
    const summary = screen
      .getByText("Fix these items before saving.")
      .closest("[role=alert]") as HTMLElement;
    expect(document.activeElement).toBe(summary);
    expect(
      within(summary).getByText("Choose a supported provider."),
    ).toBeTruthy();
    expect(within(summary).getByText("Enter an account label.")).toBeTruthy();
    const provider = screen.getByLabelText("Provider");
    expect(provider.getAttribute("aria-invalid")).toBe("true");
    expect(provider.getAttribute("aria-describedby")).toBeTruthy();
    await user.click(
      within(summary).getByRole("link", {
        name: "Choose a supported provider.",
      }),
    );
    expect(document.activeElement).toBe(provider);
    await user.selectOptions(provider, "openai");
    expect(provider.getAttribute("aria-invalid")).toBeNull();
    await user.type(screen.getByLabelText("Account label"), "   ");
    await user.click(screen.getByRole("button", { name: "Save connection" }));
    const label = screen.getByLabelText("Account label");
    expect(label.getAttribute("aria-invalid")).toBe("true");
    expect(
      document.getElementById(label.getAttribute("aria-describedby")!)
        ?.textContent,
    ).toBe("Enter an account label.");
    const key = screen.getByLabelText("API key");
    expect(key.getAttribute("aria-invalid")).toBe("true");
    expect(createProviderConnection).not.toHaveBeenCalled();
    await user.clear(label);
    await user.type(label, "  Work account  ");
    await user.type(key, "typed-secret");
    await user.click(screen.getByRole("button", { name: "Save connection" }));
    await waitFor(() =>
      expect(createProviderConnection).toHaveBeenCalledOnce(),
    );
    expect(createProviderConnection.mock.calls[0]![0]).toMatchObject({
      organizationId: "org",
      providerId: "openai",
      name: "Work account",
      baseUrl: "",
      authKind: ProviderAuthKind.API_KEY,
      apiKey: "typed-secret",
    });
  });
  it("rejects a malformed base URL and exposes Reveal key state", async () => {
    const user = userEvent.setup();
    const createProviderConnection = rs.fn((_request: unknown) => ({}));
    mount(renderPage(), (r) => page(r, { createProviderConnection }));
    await openCreate(user);
    await providerOption("OpenAI");
    await user.selectOptions(screen.getByLabelText("Provider"), "openai");
    await user.type(screen.getByLabelText("Account label"), "Work");
    await user.type(screen.getByLabelText("API key"), "secret");
    const reveal = screen.getByRole("button", { name: "Reveal key" });
    expect(reveal.getAttribute("aria-pressed")).toBe("false");
    expect(screen.getByLabelText("API key").getAttribute("type")).toBe(
      "password",
    );
    await user.click(reveal);
    const hide = screen.getByRole("button", { name: "Hide key" });
    expect(hide.getAttribute("aria-pressed")).toBe("true");
    expect(screen.getByLabelText("API key").getAttribute("type")).toBe("text");
    await user.type(screen.getByLabelText("Base URL (optional)"), "not a url");
    await user.click(screen.getByRole("button", { name: "Save connection" }));
    expect(
      screen.getAllByText("Enter a full URL, such as https://api.example.com.")
        .length,
    ).toBeGreaterThan(0);
    expect(createProviderConnection).not.toHaveBeenCalled();
  });
  it("does not ask for or send a key for NO_AUTH, even after one was typed", async () => {
    const user = userEvent.setup();
    const createProviderConnection = rs.fn((_request: unknown) => ({}));
    mount(renderPage(), (r) => page(r, { createProviderConnection }));
    await openCreate(user);
    await providerOption("OpenAI");
    await user.selectOptions(screen.getByLabelText("Provider"), "openai");
    await user.type(screen.getByLabelText("API key"), "discarded-secret");
    await user.selectOptions(
      screen.getByLabelText("Authentication"),
      String(ProviderAuthKind.NO_AUTH),
    );
    expect(screen.queryByLabelText("API key")).toBeNull();
    expect(screen.queryByRole("button", { name: "Reveal key" })).toBeNull();
    await user.type(screen.getByLabelText("Account label"), "Local");
    await user.click(screen.getByRole("button", { name: "Save connection" }));
    await waitFor(() =>
      expect(createProviderConnection).toHaveBeenCalledOnce(),
    );
    expect(
      (createProviderConnection.mock.calls[0]![0] as { apiKey?: string })
        .apiKey,
    ).toBeUndefined();
    expect(
      (createProviderConnection.mock.calls[0]![0] as { authKind: number })
        .authKind,
    ).toBe(ProviderAuthKind.NO_AUTH);
  });
  it("shows the OAuth handoff instead of format settings and continues into setup", async () => {
    const user = userEvent.setup();
    const createProviderConnection = rs.fn((_request: unknown) => ({}));
    mount(renderPage(), (r) => page(r, { createProviderConnection }));
    await openCreate(user);
    await providerOption("Anthropic");
    await user.selectOptions(screen.getByLabelText("Provider"), "anthropic");
    expect(screen.queryByLabelText("API format")).toBeNull();
    expect(screen.queryByText("Advanced connection settings")).toBeNull();
    expect(screen.queryByLabelText("Base URL (optional)")).toBeNull();
    expect(screen.queryByLabelText("API key")).toBeNull();
    expect(
      screen.getByText(/After authorization, edit this connection/),
    ).toBeTruthy();
    await user.type(screen.getByLabelText("Account label"), "Login");
    await user.click(
      screen.getByRole("button", { name: "Continue with OAuth" }),
    );
    await screen.findByRole("button", { name: "Start authorization" });
    expect(createProviderConnection).not.toHaveBeenCalled();
  });
});
describe("catalog states", () => {
  it("blocks creation while the catalog loads", async () => {
    const user = userEvent.setup();
    let release: (value: unknown) => void = () => {};
    const gate = new Promise((resolve) => {
      release = resolve;
    });
    mount(renderPage(), (r) =>
      page(r, {}, () => gate.then(() => ({ providers: catalog }))),
    );
    await openCreate(user);
    expect(screen.getByText("Loading supported providers…")).toBeTruthy();
    expect(
      (
        screen.getByRole("button", {
          name: "Save connection",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
    expect(
      (screen.getByLabelText("Provider") as HTMLSelectElement).disabled,
    ).toBe(true);
    release(undefined);
    await waitFor(() =>
      expect(screen.queryByText("Loading supported providers…")).toBeNull(),
    );
    expect(
      (
        screen.getByRole("button", {
          name: "Save connection",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(false);
  });
  it("shows an unavailable catalog with retry and offers no provider choice", async () => {
    const user = userEvent.setup();
    let calls = 0;
    mount(renderPage(), (r) =>
      page(r, {}, () => {
        calls += 1;
        if (calls <= 1) throw new ConnectError("down", Code.Unavailable);
        return { providers: catalog };
      }),
    );
    await openCreate(user);
    const dialog = screen.getByRole("dialog", { name: "Connect provider" });
    const retry = await within(dialog).findByRole("button", {
      name: "Try again",
    });
    expect(
      within(screen.getByLabelText("Provider")).getAllByRole("option"),
    ).toHaveLength(1);
    expect(
      (
        within(dialog).getByRole("button", {
          name: "Save connection",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
    await user.click(retry);
    await providerOption("OpenAI");
    expect(
      within(dialog).queryByRole("button", { name: "Try again" }),
    ).toBeNull();
  });
  it("explains an empty catalog and a search with no match, with retry and clear", async () => {
    const user = userEvent.setup();
    let empty = true;
    mount(renderPage(), (r) =>
      page(r, {}, () => ({ providers: empty ? [] : catalog })),
    );
    await openCreate(user);
    await screen.findByText("No supported providers are available right now.");
    expect(
      (
        screen.getByRole("button", {
          name: "Save connection",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
    empty = false;
    await user.click(
      within(
        screen.getByRole("dialog", { name: "Connect provider" }),
      ).getByRole("button", {
        name: "Try again",
      }),
    );
    await providerOption("OpenAI");
    await user.type(screen.getByLabelText("Find a provider"), "zzz");
    expect(
      screen.getByText("No supported providers match “zzz”."),
    ).toBeTruthy();
    expect(
      within(screen.getByLabelText("Provider")).getAllByRole("option"),
    ).toHaveLength(1);
    await user.click(screen.getByRole("button", { name: "Clear search" }));
    expect(
      (screen.getByLabelText("Find a provider") as HTMLInputElement).value,
    ).toBe("");
    expect(
      within(screen.getByLabelText("Provider")).getAllByRole("option"),
    ).toHaveLength(3);
  });
});
describe("edit variant", () => {
  const saved = create(ProviderConnectionSchema, {
    id: "connection",
    providerId: "openai",
    name: "Work account",
    baseUrl: "",
    authKind: ProviderAuthKind.API_KEY,
  });
  it("shows an immutable identity summary, focuses the label and keeps the key when empty", async () => {
    const user = userEvent.setup();
    const updateProviderConnection = rs.fn((_request: unknown) => ({}));
    mount(renderPage(), (r) =>
      page(r, { updateProviderConnection }, undefined, [saved]),
    );
    await user.click(
      await screen.findByRole("button", { name: /^Edit connection/ }),
    );
    const dialog = await screen.findByRole("dialog", {
      name: "Edit connection",
    });
    expect(
      within(dialog).getByText("Work account · OpenAI · Acme Studio"),
    ).toBeTruthy();
    expect(within(dialog).queryByLabelText("Find a provider")).toBeNull();
    expect(within(dialog).queryByLabelText("Provider")).toBeNull();
    expect(within(dialog).queryByLabelText("Authentication")).toBeNull();
    expect(document.activeElement).toBe(
      within(dialog).getByLabelText("Account label"),
    );
    const key = within(dialog).getByLabelText("Replacement API key (optional)");
    expect((key as HTMLInputElement).value).toBe("");
    expect(
      within(dialog).getByText(
        "Leave empty to keep the current key. Stored secrets are never shown again.",
      ),
    ).toBeTruthy();
    await user.click(
      within(dialog).getByRole("button", { name: "Save connection" }),
    );
    await waitFor(() =>
      expect(updateProviderConnection).toHaveBeenCalledOnce(),
    );
    expect(
      (updateProviderConnection.mock.calls[0]![0] as { apiKey?: string })
        .apiKey,
    ).toBeUndefined();
  });
  it("sends a replacement key only when typed", async () => {
    const user = userEvent.setup();
    const updateProviderConnection = rs.fn((_request: unknown) => ({}));
    mount(renderPage(), (r) =>
      page(r, { updateProviderConnection }, undefined, [saved]),
    );
    await user.click(
      await screen.findByRole("button", { name: /^Edit connection/ }),
    );
    await user.type(
      await screen.findByLabelText("Replacement API key (optional)"),
      "new-secret",
    );
    await user.click(screen.getByRole("button", { name: "Save connection" }));
    await waitFor(() =>
      expect(updateProviderConnection).toHaveBeenCalledOnce(),
    );
    expect(updateProviderConnection.mock.calls[0]![0]).toMatchObject({
      connectionId: "connection",
      apiKey: "new-secret",
    });
  });
  it("exposes configuration without key or base URL fields for OAuth", async () => {
    const user = userEvent.setup();
    const oauth = create(ProviderConnectionSchema, {
      ...saved,
      providerId: "anthropic",
      authKind: ProviderAuthKind.OAUTH,
    });
    mount(renderPage(), (r) => page(r, {}, undefined, [oauth]));
    await user.click(
      await screen.findByRole("button", { name: /^Edit connection/ }),
    );
    const dialog = await screen.findByRole("dialog", {
      name: "Edit connection",
    });
    expect(within(dialog).getByLabelText("API format")).toBeTruthy();
    expect(within(dialog).queryByLabelText("Base URL (optional)")).toBeNull();
    expect(
      within(dialog).queryByLabelText("Replacement API key (optional)"),
    ).toBeNull();
    expect(
      within(dialog).queryByRole("button", { name: "Reveal key" }),
    ).toBeNull();
  });
});
describe("save lifecycle", () => {
  it("keeps the safe draft after a rejected save, shows the request ID and retries without duplicates", async () => {
    const user = userEvent.setup();
    let attempts = 0;
    let release: (value: unknown) => void = () => {};
    const gate = new Promise((resolve) => {
      release = resolve;
    });
    const createProviderConnection = rs.fn(async (_request: unknown) => {
      attempts += 1;
      if (attempts === 1)
        throw new ConnectError("rejected", Code.InvalidArgument, undefined, [
          { desc: ErrorDetailSchema, value: { requestId: "req-4382" } },
        ]);
      await gate;
      return {};
    });
    mount(renderPage(), (r) => page(r, { createProviderConnection }));
    await openCreate(user);
    await providerOption("OpenAI");
    await user.selectOptions(screen.getByLabelText("Provider"), "openai");
    await user.type(screen.getByLabelText("Account label"), "Work");
    await user.type(screen.getByLabelText("API key"), "draft-secret");
    await user.click(screen.getByRole("button", { name: "Save connection" }));
    const dialog = screen.getByRole("dialog", { name: "Connect provider" });
    await within(dialog).findByText("Request ID: req-4382");
    expect(
      (screen.getByLabelText("Account label") as HTMLInputElement).value,
    ).toBe("Work");
    expect((screen.getByLabelText("API key") as HTMLInputElement).value).toBe(
      "draft-secret",
    );
    await user.click(within(dialog).getByRole("button", { name: "Try again" }));
    const saving = await within(dialog).findByRole("button", {
      name: "Saving…",
    });
    expect((saving as HTMLButtonElement).disabled).toBe(true);
    expect(
      within(dialog).getByText(
        /Closing this dialog does not cancel a submitted change/,
      ),
    ).toBeTruthy();
    await user.click(saving);
    expect(createProviderConnection).toHaveBeenCalledTimes(2);
    release(undefined);
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await screen.findByText("Connection saved.");
  });
  it("reports the final result on the page when the dialog is dismissed during a save", async () => {
    const user = userEvent.setup();
    let release: (value: unknown) => void = () => {};
    const gate = new Promise((resolve) => {
      release = resolve;
    });
    const createProviderConnection = rs.fn(async (_request: unknown) => {
      await gate;
      return {};
    });
    mount(renderPage(), (r) => page(r, { createProviderConnection }));
    await openCreate(user);
    await providerOption("OpenAI");
    await user.selectOptions(screen.getByLabelText("Provider"), "openai");
    await user.type(screen.getByLabelText("Account label"), "Work");
    await user.type(screen.getByLabelText("API key"), "secret");
    await user.click(screen.getByRole("button", { name: "Save connection" }));
    await screen.findByRole("button", { name: "Saving…" });
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(screen.queryByDisplayValue("secret")).toBeNull();
    release(undefined);
    await screen.findByText("Connection saved.");
  });
});
