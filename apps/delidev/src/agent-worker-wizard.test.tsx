// SPDX-License-Identifier: Apache-2.0
import { StrictMode } from "react";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport, type Transport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { AccountTypeFilter, ConfigurationService, EntityKind, ProviderInventoryCapability, ProviderService, ResourceSchema, ResourceService, SubscriptionServiceIdentity, SystemCapability, SystemService, newRequestId, type SaveAgentWorkerRequest, type SearchModelsRequest } from "@delinoio/delidev-api-client";
import { Settings } from "./settings";
import { document, encode } from "./documents";

function fixture(capabilities = [SystemCapability.AGENT_WORKER_WIZARD_V1]) {
  const row = (kind: EntityKind, data: Record<string, unknown>, schemaVersion = 1) => create(ResourceSchema, { kind, schemaVersion, id: newRequestId(), revision: 1n, documentJson: encode(data) });
  const provider = row(EntityKind.PROVIDER, { name: "OpenAI API", enabled: true, discovery: true, protocol: "openai-responses" });
  const otherProvider = row(EntityKind.PROVIDER, { name: "Other API", enabled: true, discovery: true, protocol: "openai-responses" });
  const accounts = ["Personal API", "Team API", "Backup API"].map((alias, i) => row(EntityKind.ACCOUNT, { alias, type: "api", provider_id: provider.id, enabled: true, health: i === 2 ? "disconnected" : "unverified", ...(i < 2 ? { connection: { authentication: "keyless" } } : {}) }));
  const subscription = row(EntityKind.ACCOUNT, { alias: "ChatGPT account", type: "subscription", subscription_service: "chatgpt", enabled: true, health: "ready", connection: { id: newRequestId() } }, 2);
  const models = ["Example A", "Example B"].map((name, i) => row(EntityKind.MODEL, { name, native_id: `example-${i}`, provider_id: provider.id, harnesses: [], hidden: i === 1, manual: false, new: true }));
  const agent = row(EntityKind.AGENT, { name: "Existing Worker", harness: "codex", model_id: models[0].id, accounts: [{ id: accounts[0].id, weight: 3 }], templates: [], options: { permission: "default", service_tier: "priority" }, effort: "high" });
  const records = [provider, otherProvider, ...accounts, subscription, ...models, agent];
  const save = vi.fn(async (request: SaveAgentWorkerRequest) => ({ requestId: request.mutation!.requestId, resource: row(EntityKind.AGENT, { ...JSON.parse(new TextDecoder().decode(request.documentJson)), model_id: models[0].id }) }));
  const search = vi.fn(async (_request: SearchModelsRequest) => ({ models, providers: [provider], nextPageToken: "model-page-2" }));
  const discover = vi.fn(async (request: { mutation?: { requestId: string; id: string } }) => ({ requestId: request.mutation!.requestId, account: accounts.find(value => value.id === request.mutation!.id) }));
  const list = vi.fn((request: { filter?: { kind: EntityKind; pageToken: string }; providerId: string; subscriptionService: SubscriptionServiceIdentity }) => ({ resources: request.filter?.kind === EntityKind.ACCOUNT ? request.subscriptionService === SubscriptionServiceIdentity.CHATGPT ? [subscription] : request.providerId === provider.id ? request.filter.pageToken ? [accounts[1], accounts[2]] : [accounts[0]] : [] : records.filter(value => value.kind === request.filter?.kind), nextPageToken: request.filter?.kind === EntityKind.ACCOUNT && request.providerId === provider.id && !request.filter.pageToken ? "account-page-2" : "" }));
  const get = vi.fn((request: { id: string }) => ({ resource: records.find(value => value.id === request.id) }));
  const transport = createRouterTransport(router => {
    router.service(SystemService, { getStatus: () => ({ capabilities }) });
    router.service(ProviderService, { listProviderInventory: () => ({ entries: [provider, otherProvider].map(value => ({ provider: value, providerId: value.id, displayName: document(value).name as string, enabled: true })), capabilities: [ProviderInventoryCapability.PROVIDER_ACTIVATION, ProviderInventoryCapability.ACTIVE_API_MODEL_FILTER, ProviderInventoryCapability.ACCOUNT_PROVIDER_FILTER, ProviderInventoryCapability.ACCOUNT_TYPE_FILTER] }), searchModels: search, discoverModels: discover });
    router.service(ResourceService, { listResources: list, getResource: get });
    router.service(ConfigurationService, { saveAgentWorker: save });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity }, mutations: { retry: false, gcTime: 0 } } });
  const view = (upstream: Transport = transport, visible = true) => <StrictMode><TransportProvider transport={upstream}><QueryClientProvider client={client}><Settings visible={visible} /></QueryClientProvider></TransportProvider></StrictMode>;
  return { provider, otherProvider, accounts, subscription, models, agent, records, save, search, discover, list, get, transport, client, view };
}
async function start(value: ReturnType<typeof fixture>, edit = false) {
  render(value.view());
  fireEvent.click(screen.getByRole("button", { name: "Agent Workers" }));
  fireEvent.click(edit ? await screen.findByRole("button", { name: "Edit Existing Worker" }) : screen.getByRole("button", { name: "New Agent Worker" }));
  await waitFor(() => expect((screen.getByRole("button", { name: "Next" }) as HTMLButtonElement).disabled).toBe(false));
}
const next = () => fireEvent.click(screen.getByRole("button", { name: "Next" }));
async function accounts(value: ReturnType<typeof fixture>, multi = false) {
  next();
  await screen.findByRole("option", { name: "OpenAI API" });
  fireEvent.change(screen.getByLabelText("Account source"), { target: { value: `api:${value.provider.id}` } });
  fireEvent.click(await screen.findByRole("checkbox", { name: /Personal API/ }));
  if (multi) { fireEvent.click(screen.getByRole("button", { name: "Next account page" })); fireEvent.click(await screen.findByRole("checkbox", { name: /Team API/ })); }
  await waitFor(() => expect(screen.getByText(`${multi ? 2 : 1} accounts selected`)).toBeTruthy());
  next();
  await screen.findByRole("combobox", { name: "Model" });
}

it("selects one harness card without advancing, saving or searching models", async () => {
  const value = fixture(); await start(value);
  const group = screen.getByRole("radiogroup", { name: "Harness" });
  const cards = within(group).getAllByRole("radio");
  expect(cards.map(card => card.getAttribute("aria-label"))).toEqual(["Codex", "Claude Code", "OpenCode", "Grok Build"]);
  expect(within(group).getByRole("radio", { checked: true })).toBe(cards[0]);
  expect(cards.map(card => card.tabIndex)).toEqual([0, -1, -1, -1]);
  fireEvent.click(cards[2]);
  expect(within(group).getByRole("radio", { checked: true })).toBe(cards[2]);
  expect(cards.map(card => card.tabIndex)).toEqual([-1, -1, 0, -1]);
  expect(screen.getByRole("heading", { name: "Harness", level: 3 })).toBeTruthy();
  expect(value.save).not.toHaveBeenCalled(); expect(value.discover).not.toHaveBeenCalled(); expect(value.search).not.toHaveBeenCalled();
  next();
  expect(screen.getByRole("heading", { name: "Accounts", level: 3 })).toBeTruthy();
});

it("moves harness selection and focus with arrows, Home and End", async () => {
  const value = fixture(); await start(value);
  const group = screen.getByRole("radiogroup", { name: "Harness" });
  const cards = within(group).getAllByRole("radio");
  const moves: [string, number][] = [["ArrowLeft", 3], ["ArrowRight", 0], ["ArrowDown", 1], ["ArrowUp", 0], ["End", 3], ["ArrowDown", 0], ["Home", 0]];
  cards[0].focus();
  for (const [key, index] of moves) {
    fireEvent.keyDown(globalThis.document.activeElement!, { key });
    expect(globalThis.document.activeElement).toBe(cards[index]);
    expect(within(group).getByRole("radio", { checked: true })).toBe(cards[index]);
    expect(cards.filter(card => card.tabIndex === 0)).toEqual([cards[index]]);
  }
  expect(value.save).not.toHaveBeenCalled(); expect(value.discover).not.toHaveBeenCalled(); expect(value.search).not.toHaveBeenCalled();
});

it("prefills the saved harness and focuses the first card for an unsupported saved value", async () => {
  const value = fixture();
  value.agent.documentJson = encode({ ...document(value.agent), harness: "unsupported-harness" });
  await start(value, true);
  const group = screen.getByRole("radiogroup", { name: "Harness" });
  expect(within(group).queryByRole("radio", { checked: true })).toBeNull();
  const codex = within(group).getByRole("radio", { name: "Codex" });
  expect(codex.tabIndex).toBe(0);
  next();
  expect(screen.getByRole("alert").textContent).toContain("Choose a supported harness.");
  expect(globalThis.document.activeElement).toBe(codex);
  fireEvent.click(within(group).getByRole("radio", { name: "Grok Build" }));
  next();
  expect(screen.getByRole("option", { name: "Grok subscription" })).toBeTruthy();
  expect(value.save).not.toHaveBeenCalled();
});

it("starts edits with the saved non-default harness selected", async () => {
  const value = fixture(); value.agent.documentJson = encode({ ...document(value.agent), harness: "opencode" });
  await start(value, true);
  expect(screen.getByRole("radio", { checked: true, name: "OpenCode" }).tabIndex).toBe(0);
  expect(screen.getAllByRole("radio", { checked: true })).toHaveLength(1);
});

it("preserves edit selections on harness reselection and clears them only on a change", async () => {
  const value = fixture(); await start(value, true); next();
  await waitFor(() => expect((screen.getByLabelText("Account source") as HTMLSelectElement).value).toBe(`api:${value.provider.id}`));
  await screen.findByRole("checkbox", { name: /Personal API/ }); next();
  expect((screen.getByRole("combobox", { name: "Model" }) as HTMLInputElement).value).toBe("example-0");
  fireEvent.click(screen.getByRole("button", { name: "Back" })); fireEvent.click(screen.getByRole("button", { name: "Back" }));
  fireEvent.click(screen.getByRole("radio", { name: "Codex" })); next();
  expect((screen.getByLabelText("Account source") as HTMLSelectElement).value).toBe(`api:${value.provider.id}`);
  expect(screen.getByText("1 accounts selected")).toBeTruthy(); next();
  expect((screen.getByRole("combobox", { name: "Model" }) as HTMLInputElement).value).toBe("example-0");
  fireEvent.click(screen.getByRole("button", { name: "Back" })); fireEvent.click(screen.getByRole("button", { name: "Back" }));
  fireEvent.click(screen.getByRole("radio", { name: "Claude Code" })); next();
  expect((screen.getByLabelText("Account source") as HTMLSelectElement).value).toBe("");
  expect(screen.getByText("0 accounts selected")).toBeTruthy();
  expect(screen.queryByRole("option", { name: "ChatGPT subscription" })).toBeNull();
  fireEvent.change(screen.getByLabelText("Account source"), { target: { value: `api:${value.provider.id}` } });
  fireEvent.click(await screen.findByRole("checkbox", { name: /Personal API/ })); next();
  expect((screen.getByRole("combobox", { name: "Model" }) as HTMLInputElement).value).toBe("");
  expect(value.save).not.toHaveBeenCalled(); expect(value.discover).not.toHaveBeenCalled();
});

it("disables harness choices when the server lacks wizard support", async () => {
  const value = fixture([]); render(value.view());
  fireEvent.click(screen.getByRole("button", { name: "Agent Workers" }));
  fireEvent.click(screen.getByRole("button", { name: "New Agent Worker" }));
  await screen.findByText("Update the server to configure Agent Workers with this wizard.");
  const cards = screen.getAllByRole("radio");
  expect(cards.every(card => (card as HTMLButtonElement).disabled)).toBe(true);
  fireEvent.keyDown(cards[0], { key: "ArrowDown" });
  expect(screen.getByRole("radio", { checked: true, name: "Codex" })).toBeTruthy();
  expect(value.save).not.toHaveBeenCalled(); expect(value.discover).not.toHaveBeenCalled(); expect(value.search).not.toHaveBeenCalled();
});

it("removes Models and saves an ordered multi-account Worker only at the last step", async () => {
  const value = fixture(); await start(value);
  expect(screen.queryByRole("button", { name: "Models" })).toBeNull();
  expect(within(screen.getByRole("navigation", { name: "Settings categories" })).getAllByRole("button")).toHaveLength(17);
  await accounts(value, true);
  expect(value.save).not.toHaveBeenCalled(); expect(value.discover).not.toHaveBeenCalled();
  expect(value.list.mock.calls.some(([request]) => request.providerId === value.provider.id)).toBe(true);
  fireEvent.focus(screen.getByRole("combobox", { name: "Model" }));
  await screen.findByRole("option", { name: /Example A/ });
  fireEvent.click(screen.getByRole("option", { name: /Example A/ }));
  next();
  fireEvent.change(screen.getByRole("textbox", { name: "Name" }), { target: { value: "Multi-account Worker" } });
  fireEvent.click(screen.getByRole("button", { name: "Back" }));
  expect((screen.getByRole("combobox", { name: "Model" }) as HTMLInputElement).value).toBe("example-0");
  next();
  expect((screen.getByRole("textbox", { name: "Name" }) as HTMLInputElement).value).toBe("Multi-account Worker");
  await waitFor(() => expect((screen.getByRole("button", { name: "Save Agent Worker" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Save Agent Worker" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  const input = value.save.mock.calls[0][0];
  expect(input.model).toMatchObject({ selection: { case: "modelId", value: value.models[0].id }, expectedModelRevision: 1n });
  expect(JSON.parse(new TextDecoder().decode(input.documentJson))).toMatchObject({ name: "Multi-account Worker", accounts: value.accounts.slice(0, 2).map(row => ({ id: row.id, weight: 1 })) });
}, 15000);

it("supports keyboard autocomplete and direct IDs without endpoint requests", async () => {
  const value = fixture(); await start(value); await accounts(value);
  const input = screen.getByRole("combobox", { name: "Model" }); fireEvent.focus(input);
  await screen.findByRole("option", { name: /Example A/ });
  fireEvent.keyDown(input, { key: "ArrowDown" });
  expect(input.getAttribute("aria-activedescendant")).toBeTruthy();
  fireEvent.keyDown(input, { key: "Enter" });
  expect((input as HTMLInputElement).value).toBe("example-0");
  expect(screen.queryByRole("listbox")).toBeNull();
  fireEvent.change(input, { target: { value: "unlisted-exact-ID" } });
  fireEvent.keyDown(input, { key: "Escape" });
  expect(screen.queryByRole("listbox")).toBeNull();
  expect(screen.getByRole("heading", { name: "Model", level: 3 })).toBeTruthy();
  next();
  expect(screen.getByRole("heading", { name: "Configure", level: 3 })).toBe(globalThis.document.activeElement);
  fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Direct model" } });
  fireEvent.click(screen.getByRole("button", { name: "Save Agent Worker" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  expect(value.save.mock.calls[0][0].model).toMatchObject({ selection: { case: "nativeId", value: "unlisted-exact-ID" }, expectedModelRevision: 0n });
  expect(value.discover).not.toHaveBeenCalled();
});

it("requires accounts, restricts Fixed routing and resets incompatible source selections", async () => {
  const value = fixture(); await start(value); next(); next();
  expect(screen.getByRole("heading", { name: "Accounts", level: 3 })).toBeTruthy();
  expect(value.save).not.toHaveBeenCalled();
  await screen.findByRole("option", { name: "OpenAI API" });
  fireEvent.change(screen.getByLabelText("Account source"), { target: { value: `api:${value.provider.id}` } }); next();
  expect(screen.getByRole("alert").textContent).toContain("Select at least one");
  fireEvent.click(await screen.findByRole("checkbox", { name: /Personal API/ }));
  fireEvent.click(screen.getByRole("button", { name: "Next account page" }));
  fireEvent.click(await screen.findByRole("checkbox", { name: /Team API/ }));
  fireEvent.click(screen.getByText("Routing options"));
  expect((screen.getByRole("option", { name: "fixed" }) as HTMLOptionElement).disabled).toBe(true);
  fireEvent.change(screen.getByLabelText("Weight for account 2"), { target: { value: "5" } });
  fireEvent.click(screen.getByRole("button", { name: "Move account 2 up" }));
  expect((screen.getByLabelText("Weight for account 1") as HTMLInputElement).value).toBe("5");
  next(); fireEvent.change(screen.getByRole("combobox", { name: "Model" }), { target: { value: "retained-input" } });
  fireEvent.click(screen.getByRole("button", { name: "Back" }));
  fireEvent.change(screen.getByLabelText("Account source"), { target: { value: `api:${value.otherProvider.id}` } });
  expect(screen.getByText("0 accounts selected")).toBeTruthy();
  next(); expect(screen.getByRole("heading", { name: "Accounts", level: 3 })).toBeTruthy();
});

it("uses service-scoped subscription reads and keeps manual input available when discovery is unsupported", async () => {
  const value = fixture(); await start(value); next();
  fireEvent.change(screen.getByLabelText("Account source"), { target: { value: "subscription:chatgpt" } });
  fireEvent.click(await screen.findByRole("checkbox", { name: /ChatGPT account/ })); next();
  await screen.findByText(/Subscription model discovery is unsupported/);
  expect(value.list.mock.calls.some(([request]) => request.subscriptionService === SubscriptionServiceIdentity.CHATGPT)).toBe(true);
  await waitFor(() => expect(value.search.mock.calls.some(([request]) => request.subscriptionService === SubscriptionServiceIdentity.CHATGPT && request.providerId === "")).toBe(true));
  expect(screen.queryByRole("button", { name: "Refresh models from endpoint" })).toBeNull();
});

it.each(["api", "subscription"] as const)("shows connected ready/unverified %s choices without changing enablement", async type => {
  const value = fixture();
  const original = type === "api" ? value.accounts[0] : value.subscription;
  const states = [
    { alias: "Ready choice", health: "ready", enabled: false },
    { alias: "Unverified choice", health: "unverified" },
    { alias: "Disconnected choice", health: "disconnected", connection: null },
    { alias: "Failed choice", health: "failed" },
    { alias: "Expired choice", health: "expired" },
    { alias: "Revoked choice", health: "revoked" },
    { alias: "Cleanup choice", health: "ready", removal: { id: newRequestId() } },
    { alias: "Missing connection choice", health: "ready", connection: null },
    { alias: "Invalid connection choice", health: "unverified", connection: "disconnected" },
    { alias: "Unknown health choice", health: "unknown" },
  ];
  const choices = states.map(state => create(ResourceSchema, { ...original, id: newRequestId(), documentJson: encode({ ...document(original), ...state }) }));
  value.list.mockImplementation(request => ({ resources: request.filter?.kind === EntityKind.ACCOUNT ? choices : value.records.filter(row => row.kind === request.filter?.kind), nextPageToken: "" }));
  await start(value); next();
  await screen.findByRole("option", { name: "OpenAI API" });
  fireEvent.change(screen.getByLabelText("Account source"), { target: { value: type === "api" ? `api:${value.provider.id}` : "subscription:chatgpt" } });
  const section = screen.getByRole("region", { name: "Choose accounts" });
  await within(section).findByRole("checkbox", { name: /Ready choice.*Disabled/ });
  expect(within(section).getByRole("checkbox", { name: /Unverified choice/ })).toBeTruthy();
  expect(within(section).getAllByRole("checkbox")).toHaveLength(2);
  for (const state of states.slice(2)) expect(within(section).queryByText(state.alias)).toBeNull();
  expect(value.save).not.toHaveBeenCalled(); expect(value.discover).not.toHaveBeenCalled();
});

it("keeps pagination and explicit refresh when a complete page has only hidden choices", async () => {
  const value = fixture();
  let connected = false;
  value.list.mockImplementation(request => ({
    resources: request.filter?.kind === EntityKind.ACCOUNT ? request.filter.pageToken ? [value.accounts[1]] : connected ? [value.accounts[0]] : [value.accounts[2]] : value.records.filter(row => row.kind === request.filter?.kind),
    nextPageToken: request.filter?.kind === EntityKind.ACCOUNT && !request.filter.pageToken ? "account-page-2" : "",
  }));
  await start(value); next();
  await screen.findByRole("option", { name: "OpenAI API" });
  fireEvent.change(screen.getByLabelText("Account source"), { target: { value: `api:${value.provider.id}` } });
  await screen.findByText("No accounts to select on this page.");
  expect(screen.getByText("Connect an account in AI Subscription or AI API Keys, then refresh.")).toBeTruthy();
  expect(screen.queryByRole("checkbox", { name: /Backup API/ })).toBeNull();
  expect(value.list.mock.calls.filter(([request]) => request.filter?.kind === EntityKind.ACCOUNT)).toHaveLength(1);
  expect((screen.getByRole("button", { name: "Next account page" }) as HTMLButtonElement).disabled).toBe(false);
  fireEvent.click(screen.getByRole("button", { name: "Next account page" }));
  await screen.findByRole("checkbox", { name: /Team API/ });
  expect(value.list.mock.calls.at(-1)![0].filter?.pageToken).toBe("account-page-2");
  fireEvent.click(screen.getByRole("button", { name: "First account page" }));
  await screen.findByText("No accounts to select on this page.");
  connected = true;
  fireEvent.click(screen.getByRole("button", { name: "Refresh accounts" }));
  await screen.findByRole("checkbox", { name: /Personal API/ });
  expect(screen.queryByText("No accounts to select on this page.")).toBeNull();
  expect(value.save).not.toHaveBeenCalled(); expect(value.discover).not.toHaveBeenCalled();
});

it.each(["initial", "refresh"])("keeps an %s account read failure distinct from hidden-only emptiness", async state => {
  const value = fixture();
  const loaded = value.list.getMockImplementation()!;
  value.list.mockImplementation(request => request.filter?.kind === EntityKind.ACCOUNT ? { resources: [value.accounts[2]], nextPageToken: "" } : loaded(request));
  const failRead = () => value.list.mockImplementation(request => {
    if (request.filter?.kind === EntityKind.ACCOUNT) throw new ConnectError("Account read unavailable", Code.Unavailable);
    return loaded(request);
  });
  if (state === "initial") failRead();
  await start(value); next();
  await screen.findByRole("option", { name: "OpenAI API" });
  fireEvent.change(screen.getByLabelText("Account source"), { target: { value: `api:${value.provider.id}` } });
  if (state === "refresh") {
    await screen.findByText("No accounts to select on this page.");
    failRead(); fireEvent.click(screen.getByRole("button", { name: "Refresh accounts" }));
    await screen.findByText("Refresh failed. Showing the last successfully loaded accounts.");
  }
  await within(screen.getByRole("region", { name: "Choose accounts" })).findByRole("alert");
  expect(screen.queryByText("No accounts to select on this page.")).toBeNull();
  expect(screen.queryByText(/No accounts for this source/)).toBeNull();
  expect((screen.getByRole("button", { name: "Refresh accounts" }) as HTMLButtonElement).disabled).toBe(false);
});

it.each(["edit", "refresh"])("preserves hidden selected account order, weights and save bytes after %s", async state => {
  const value = fixture();
  const expected = state === "edit" ? [{ id: value.accounts[2].id, weight: 5 }, { id: value.accounts[0].id, weight: 3 }] : [{ id: value.accounts[0].id, weight: 3 }, { id: value.accounts[1].id, weight: 5 }];
  if (state === "edit") {
    value.agent.documentJson = encode({ ...document(value.agent), accounts: expected });
    value.accounts[0].documentJson = encode({ ...document(value.accounts[0]), health: "failed" });
    value.save.mockImplementation(async request => ({ requestId: request.mutation!.requestId, resource: create(ResourceSchema, { ...value.agent, revision: 2n, documentJson: request.documentJson }) }));
    await start(value, true); next();
    await screen.findByText("No accounts to select on this page.");
  } else {
    await start(value); await accounts(value, true);
    fireEvent.click(screen.getByRole("button", { name: "Back" }));
  }
  fireEvent.click(screen.getByText("Routing options"));
  if (state === "refresh") {
    fireEvent.change(screen.getByLabelText("Weight for account 1"), { target: { value: "3" } });
    fireEvent.change(screen.getByLabelText("Weight for account 2"), { target: { value: "5" } });
    for (const index of [0, 1]) {
      const original = value.accounts[index];
      const changed = create(ResourceSchema, { ...original, revision: 2n, documentJson: encode({ ...document(original), health: "failed" }) });
      value.accounts[index] = changed; value.records[value.records.findIndex(row => row.id === original.id)] = changed;
    }
    await act(async () => { await value.client.invalidateQueries(); });
    await screen.findByText("No accounts to select on this page.");
  }
  await screen.findByText("2 accounts selected");
  expect((screen.getByLabelText("Weight for account 1") as HTMLInputElement).value).toBe(String(expected[0].weight));
  expect((screen.getByLabelText("Weight for account 2") as HTMLInputElement).value).toBe(String(expected[1].weight));
  expect(screen.queryByRole("checkbox", { name: /Personal API|Team API|Backup API/ })).toBeNull();
  next();
  fireEvent.change(await screen.findByRole("combobox", { name: "Model" }), { target: { value: "exact-model" } }); next();
  fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Retained selection Worker" } });
  fireEvent.click(screen.getByRole("button", { name: "Save Agent Worker" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  expect(JSON.parse(new TextDecoder().decode(value.save.mock.calls[0][0].documentJson)).accounts).toEqual(expected);
  expect(value.discover).not.toHaveBeenCalled();
});

it("removes a hidden selection only through its explicit Routing options action", async () => {
  const value = fixture();
  value.accounts[0].documentJson = encode({ ...document(value.accounts[0]), health: "failed" });
  await start(value, true); next();
  await screen.findByText("No accounts to select on this page.");
  await screen.findByText("1 accounts selected");
  fireEvent.click(screen.getByText("Routing options"));
  expect(screen.getByRole("button", { name: "Remove account 1" })).toBeTruthy();
  expect(within(screen.getByRole("region", { name: "Choose accounts" })).getByText("Personal API")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Remove account 1" }));
  expect(screen.getByText("0 accounts selected")).toBeTruthy();
  next();
  expect(screen.getByRole("heading", { name: "Accounts", level: 3 })).toBeTruthy();
  expect(value.save).not.toHaveBeenCalled();
});

it.each(["failed", "empty"])("accepts direct IDs with a %s catalog and retains earlier selections across pagination", async state => {
  const value = fixture();
  if (state === "failed") value.search.mockRejectedValue(new ConnectError("Catalog unavailable", Code.Unavailable));
  else value.search.mockResolvedValue({ models: [], providers: [], nextPageToken: "" });
  await start(value); await accounts(value);
  await screen.findByText(state === "failed" ? /Catalog lookup failed/ : /No saved models match/);
  const input = screen.getByRole("combobox", { name: "Model" });
  fireEvent.keyDown(input, { key: "ArrowDown" });
  expect(input.hasAttribute("aria-activedescendant")).toBe(false);
  fireEvent.change(screen.getByRole("combobox", { name: "Model" }), { target: { value: "exact" } }); next();
  expect(screen.getByRole("heading", { name: "Configure", level: 3 })).toBeTruthy();
});

it("prefills edits, preserves hidden options and exact uncertain requests across reconnect", async () => {
  const value = fixture();
  value.save.mockImplementationOnce(async () => { throw new ConnectError("Lost acknowledgement", Code.Unavailable); });
  value.save.mockImplementation(async request => ({ requestId: request.mutation!.requestId, resource: value.agent }));
  const view = render(value.view());
  fireEvent.click(screen.getByRole("button", { name: "Agent Workers" })); fireEvent.click(await screen.findByRole("button", { name: "Edit Existing Worker" }));
  await waitFor(() => expect((screen.getByRole("button", { name: "Next" }) as HTMLButtonElement).disabled).toBe(false));
  next(); await waitFor(() => expect((screen.getByLabelText("Account source") as HTMLSelectElement).value).toBe(`api:${value.provider.id}`));
  await screen.findByText("1 accounts selected"); next(); next();
  const name = screen.getByRole("textbox", { name: "Name" });
  expect((name as HTMLInputElement).value).toBe("Existing Worker");
  fireEvent.change(name, { target: { value: "Edited Worker" } });
  fireEvent.click(screen.getByRole("button", { name: "Save Agent Worker" }));
  await screen.findByRole("button", { name: "Retry the same Worker save" });
  const replacement: Transport = { ...value.transport, unary: (...args) => value.transport.unary(...args) };
  view.rerender(value.view(replacement)); fireEvent(window, new Event("resize"));
  expect(screen.getByRole("textbox", { name: "Name" })).toBe(name);
  fireEvent.click(screen.getByRole("button", { name: "Retry the same Worker save" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(2));
  expect(value.save.mock.calls[1][0]).toEqual(value.save.mock.calls[0][0]);
  expect(JSON.parse(new TextDecoder().decode(value.save.mock.calls[0][0].documentJson))).toMatchObject({ effort: "high", options: { service_tier: "priority" }, accounts: [{ id: value.accounts[0].id, weight: 3 }] });
});

it("preserves accountless legacy Workers but requires adding an account to save them", async () => {
  const value = fixture(); value.records[value.records.indexOf(value.agent)] = create(ResourceSchema, { ...value.agent, documentJson: encode({ ...document(value.agent), accounts: [] }) });
  await start(value, true); next(); next();
  expect(screen.getByRole("heading", { name: "Accounts", level: 3 })).toBeTruthy(); expect(value.save).not.toHaveBeenCalled();
});

it("blocks stale Worker saves and ignores late successful results after Settings departure", async () => {
  const value = fixture(); let resolve!: (response: Awaited<ReturnType<typeof value.save>>) => void;
  value.save.mockImplementation(() => new Promise(done => { resolve = done; }));
  await start(value); await accounts(value); fireEvent.change(screen.getByRole("combobox", { name: "Model" }), { target: { value: "direct" } }); next();
  fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Late Worker" } }); fireEvent.click(screen.getByRole("button", { name: "Save Agent Worker" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  fireEvent.click(screen.getByRole("button", { name: "Instructions" }));
  await act(async () => resolve({ requestId: value.save.mock.calls[0][0].mutation!.requestId, resource: value.agent }));
  expect(screen.getByRole("heading", { name: "Instructions", level: 1 })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Agent Workers" })); fireEvent.click(screen.getByRole("button", { name: "New Agent Worker" }));
  expect(screen.queryByRole("button", { name: "Retry the same Worker save" })).toBeNull();
});

it("retains a stale edit and returns empty-name validation to its input", async () => {
  const value = fixture(); await start(value, true); next();
  await screen.findByRole("checkbox", { name: /Personal API/ }); next(); next();
  fireEvent.change(screen.getByLabelText("Name"), { target: { value: "" } });
  fireEvent.click(screen.getByRole("button", { name: "Save Agent Worker" }));
  expect(globalThis.document.activeElement).toBe(screen.getByLabelText("Name"));
  expect(value.save).not.toHaveBeenCalled();
  (screen.getByLabelText("Permission mode") as HTMLSelectElement).focus();
  fireEvent.click(screen.getByRole("button", { name: "Save Agent Worker" }));
  expect(globalThis.document.activeElement).toBe(screen.getByLabelText("Name"));
  fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Retained draft" } });
  expect(globalThis.document.activeElement).toBe(screen.getByLabelText("Name"));
  value.get.mockImplementation(request => ({ resource: request.id === value.agent.id ? create(ResourceSchema, { ...value.agent, revision: 2n }) : value.records.find(row => row.id === request.id) }));
  await act(async () => { await value.client.invalidateQueries(); });
  await screen.findByText(/This Worker changed elsewhere/);
  expect((screen.getByRole("button", { name: "Save Agent Worker" }) as HTMLButtonElement).disabled).toBe(true);
  expect((screen.getByLabelText("Name") as HTMLInputElement).value).toBe("Retained draft");
});

it("retains a selected model across model pages and runs discovery only on an explicit action", async () => {
  const value = fixture(); await start(value); await accounts(value);
  const input = screen.getByRole("combobox", { name: "Model" }); fireEvent.focus(input);
  fireEvent.click(await screen.findByRole("option", { name: /Example A/ }));
  await waitFor(() => expect((screen.getByRole("button", { name: "Next model page" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Next model page" }));
  await waitFor(() => expect(value.search.mock.calls.some(([request]) => request.pageToken === "model-page-2")).toBe(true));
  expect((input as HTMLInputElement).value).toBe("example-0");
  expect(value.discover).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Refresh models from endpoint" }));
  await waitFor(() => expect(value.discover).toHaveBeenCalledTimes(1));
  expect(value.discover.mock.calls[0][0]).toMatchObject({ mutation: { id: value.accounts[0].id, expectedRevision: 1n } });
});

it("returns a changed canonical model to its input before saving", async () => {
  const value = fixture(); await start(value); await accounts(value);
  fireEvent.focus(screen.getByRole("combobox", { name: "Model" }));
  fireEvent.click(await screen.findByRole("option", { name: /Example A/ })); next();
  fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Model revision draft" } });
  await waitFor(() => expect((screen.getByRole("button", { name: "Save Agent Worker" }) as HTMLButtonElement).disabled).toBe(false));
  value.get.mockImplementation(request => ({ resource: request.id === value.models[0].id ? create(ResourceSchema, { ...value.models[0], revision: 2n }) : value.records.find(row => row.id === request.id) }));
  await act(async () => { await value.client.invalidateQueries(); });
  await screen.findByText("The selected model changed. Return to Model and explicitly reselect it.");
  fireEvent.click(screen.getByRole("button", { name: "Save Agent Worker" }));
  expect(screen.getByRole("heading", { name: "Model", level: 3 })).toBeTruthy();
  expect(globalThis.document.activeElement).toBe(screen.getByRole("combobox", { name: "Model" }));
  expect(screen.getByText(/The selected model is unavailable or changed/)).toBeTruthy();
  expect(value.save).not.toHaveBeenCalled();
}, 15000);

it("rejects a mismatched source page as a whole instead of filtering a loaded page", async () => {
  const value = fixture();
  value.subscription.documentJson = encode({ ...document(value.subscription), health: "failed" });
  value.list.mockImplementation(request => ({ resources: request.filter?.kind === EntityKind.ACCOUNT ? [value.accounts[0], value.subscription] : value.records.filter(row => row.kind === request.filter?.kind), nextPageToken: "" }));
  await start(value); next();
  await screen.findByRole("option", { name: "OpenAI API" });
  fireEvent.change(screen.getByLabelText("Account source"), { target: { value: `api:${value.provider.id}` } });
  await screen.findByText(/This account page includes unsupported or mismatched source data/);
  expect(screen.queryByRole("checkbox", { name: /Personal API/ })).toBeNull();
  expect(screen.queryByText("No accounts to select on this page.")).toBeNull();
  next();
  expect(screen.getByRole("heading", { name: "Accounts", level: 3 })).toBeTruthy();
  expect(value.save).not.toHaveBeenCalled();
});

it("refreshes and focuses a model changed between the last read and server save", async () => {
  const value = fixture(); await start(value); await accounts(value);
  fireEvent.focus(screen.getByRole("combobox", { name: "Model" }));
  fireEvent.click(await screen.findByRole("option", { name: /Example A/ })); next();
  fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Concurrent model revision" } });
  await waitFor(() => expect((screen.getByRole("button", { name: "Save Agent Worker" }) as HTMLButtonElement).disabled).toBe(false));
  value.save.mockImplementationOnce(async () => {
    value.get.mockImplementation(request => ({ resource: request.id === value.models[0].id ? create(ResourceSchema, { ...value.models[0], revision: 2n }) : value.records.find(row => row.id === request.id) }));
    throw new ConnectError("Revision conflict", Code.Aborted);
  });
  fireEvent.click(screen.getByRole("button", { name: "Save Agent Worker" }));
  await screen.findByRole("heading", { name: "Model", level: 3 });
  expect(globalThis.document.activeElement).toBe(screen.getByRole("combobox", { name: "Model" }));
  expect(value.save).toHaveBeenCalledTimes(1);
  expect(value.discover).not.toHaveBeenCalled();
}, 15000);


it("keeps one autocomplete selection and valid active identity across empty pages", async () => {
  const value = fixture();
  value.search.mockImplementation(async request => ({ models: request.pageToken ? [] : value.models, providers: [value.provider], nextPageToken: request.pageToken ? "" : "model-page-2" }));
  await start(value); await accounts(value);
  const input = screen.getByRole("combobox", { name: "Model" }); fireEvent.focus(input);
  fireEvent.click(await screen.findByRole("option", { name: /Example B/ })); fireEvent.focus(input);
  await waitFor(() => expect(value.search.mock.calls.some(([request]) => request.query === "example-1")).toBe(true));
  await screen.findByRole("option", { name: /Example A/ });
  fireEvent.keyDown(input, { key: "ArrowDown" });
  expect(screen.getAllByRole("option", { selected: true })).toHaveLength(1);
  fireEvent.keyDown(input, { key: "ArrowUp" });
  expect(input.getAttribute("aria-activedescendant")).toMatch(/-2$/);
  fireEvent.click(screen.getByRole("button", { name: "Next model page" }));
  await waitFor(() => expect(screen.queryByRole("option", { name: /Example A/ })).toBeNull());
  expect(input.getAttribute("aria-activedescendant")).toBeNull();
  expect((input as HTMLInputElement).value).toBe("example-1");
  expect(value.save).not.toHaveBeenCalled(); expect(value.discover).not.toHaveBeenCalled();
});
