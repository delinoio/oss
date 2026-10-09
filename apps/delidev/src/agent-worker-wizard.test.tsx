// SPDX-License-Identifier: Apache-2.0
import { chooseScrollOption, scrollChoiceValue, waitScrollChoices } from "./test-scroll-picker";
import { StrictMode } from "react";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport, type Transport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { AccountTypeFilter, ConfigurationQuery, KnownSubscriptionModelCatalogSource, ConfigurationService, EntityKind, ProviderInventoryCapability, ProviderService, ResourceSchema, ResourceService, SubscriptionServiceIdentity, SystemCapability, SystemService, newRequestId, type SaveAgentWorkerRequest, type ListKnownSubscriptionModelsRequest, type SearchModelsRequest, type Resource } from "@delinoio/delidev-api-client";
import { Settings } from "./settings";
import { document, encode } from "./documents";
import { AgentWorkerWizard } from "./agent-worker-wizard";
import { MutationIntents, useRetainedMutation } from "./mutation";
import { i18n } from "./localization";

function sourceChoice(label: string) {
  return screen.getByRole("combobox", { name: label });
}
async function refreshAccounts(value: ReturnType<typeof fixture>) {
  await act(async () => { await value.client.invalidateQueries({ refetchType: "active" }); });
}
function reorderSource(position: number, direction: "ArrowUp" | "ArrowDown") {
  const grip = screen.getByRole("button", { name: new RegExp(`^Reorder source ${position}:`) });
  grip.focus(); fireEvent.keyDown(grip, { key: " " }); fireEvent.keyDown(grip, { key: direction }); fireEvent.keyDown(grip, { key: "Enter" });
  return grip;
}
function fixture(capabilities = [SystemCapability.AGENT_WORKER_WIZARD_V1, SystemCapability.KNOWN_SUBSCRIPTION_MODELS_V1]) {
  const row = (kind: EntityKind, data: Record<string, unknown>, schemaVersion = 1) => create(ResourceSchema, { kind, schemaVersion, id: newRequestId(), revision: 1n, documentJson: encode(data) });
  const provider = row(EntityKind.PROVIDER, { name: "OpenAI API", enabled: true, discovery: true, protocol: "openai-responses", endpoint: "https://api.example.test/v1", authentication: "keyless" });
  const otherProvider = row(EntityKind.PROVIDER, { name: "Other API", enabled: true, discovery: true, protocol: "openai-responses", endpoint: "https://api.example.test/v1", authentication: "keyless" });
  const accounts = ["Personal API", "Team API", "Backup API"].map((alias, i) => row(EntityKind.ACCOUNT, { alias, type: "api", provider_id: provider.id, enabled: true, health: i === 2 ? "disconnected" : "unverified", ...(i < 2 ? { connection: { authentication: "keyless" } } : {}) }));
  const subscription = row(EntityKind.ACCOUNT, { alias: "ChatGPT account", type: "subscription", subscription_service: "chatgpt", enabled: true, health: "ready", connection: { id: newRequestId() } }, 2);
  const models = ["Example A", "Example B"].map((name, i) => row(EntityKind.MODEL, { name, native_id: `example-${i}`, provider_id: provider.id, harnesses: [], hidden: i === 1, manual: false, new: true }));
  const agent = row(EntityKind.AGENT, { name: "Existing Worker", harness: "codex", model_id: models[0].id, accounts: [{ id: accounts[0].id, weight: 3 }], templates: [], options: { permission: "default", service_tier: "priority" }, effort: "high" });
  const records = [provider, otherProvider, ...accounts, subscription, ...models, agent];
  const save = vi.fn(async (request: SaveAgentWorkerRequest) => {
    const data = JSON.parse(new TextDecoder().decode(request.documentJson));
    if (request.schemaVersion === 3) data.routes = data.routes.map((route: Record<string, unknown>, index: number) => ({ ...route, model_id: models[index]?.id || newRequestId() })); else data.model_id = models[0].id;
    return { requestId: request.mutation!.requestId, resource: row(EntityKind.AGENT, data, request.schemaVersion) };
  });
  const search = vi.fn(async (request: SearchModelsRequest) => ({ models, providers: [provider], nextPageToken: request.pageToken ? "" : "model-page-2" }));
  const known = vi.fn(async (request: ListKnownSubscriptionModelsRequest) => ({ subscriptionService: request.subscriptionService, models: [{ nativeId: "gpt-known-current", displayName: "GPT Known Current", order: 0 }], catalogVersion: `sha256:${"a".repeat(64)}`, updatedAt: "2026-10-06", source: KnownSubscriptionModelCatalogSource.BUNDLED }));
  const discover = vi.fn(async (request: { mutation?: { requestId: string; id: string } }) => ({ requestId: request.mutation!.requestId, account: accounts.find(value => value.id === request.mutation!.id) }));
  const list = vi.fn((request: { filter?: { kind: EntityKind; pageToken: string }; providerId: string; subscriptionService: SubscriptionServiceIdentity }) => ({ resources: request.filter?.kind === EntityKind.ACCOUNT ? request.subscriptionService === SubscriptionServiceIdentity.CHATGPT ? [subscription] : request.providerId === provider.id ? request.filter.pageToken ? [accounts[1], accounts[2]] : [accounts[0]] : [] : records.filter(value => value.kind === request.filter?.kind), nextPageToken: request.filter?.kind === EntityKind.ACCOUNT && request.providerId === provider.id && !request.filter.pageToken ? "account-page-2" : "" }));
  const get = vi.fn((request: { id: string }) => ({ resource: records.find(value => value.id === request.id) }));
  const transport = createRouterTransport(router => {
    router.service(SystemService, { getStatus: () => ({ capabilities }) });
    router.service(ProviderService, { listProviderInventory: () => ({ entries: [provider, otherProvider].map(value => ({ provider: value, providerId: value.id, displayName: document(value).name as string, enabled: true })), capabilities: [ProviderInventoryCapability.PROVIDER_ACTIVATION, ProviderInventoryCapability.ACTIVE_API_MODEL_FILTER, ProviderInventoryCapability.ACCOUNT_PROVIDER_FILTER, ProviderInventoryCapability.ACCOUNT_TYPE_FILTER] }), searchModels: search, discoverModels: discover, listKnownSubscriptionModels: known });
    router.service(ResourceService, { listResources: list, getResource: get });
    router.service(ConfigurationService, { saveAgentWorker: save });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity }, mutations: { retry: false, gcTime: 0 } } });
  const view = (upstream: Transport = transport, visible = true) => <StrictMode><TransportProvider transport={upstream}><QueryClientProvider client={client}><Settings visible={visible} /></QueryClientProvider></TransportProvider></StrictMode>;
  const wizardView = (active = true, upstream: Transport = transport) => <StrictMode><TransportProvider transport={upstream}><QueryClientProvider client={client}><MutationIntents><AgentWorkerWizard active={active} saved={() => {}} cancel={() => {}} /></MutationIntents></QueryClientProvider></TransportProvider></StrictMode>;
  return { provider, otherProvider, accounts, subscription, models, agent, records, save, search, known, discover, list, get, transport, client, capabilities, view, wizardView };
}
async function start(value: ReturnType<typeof fixture>, edit = false, transport = value.transport) {
  render(value.view(transport));
  fireEvent.click(screen.getByRole("button", { name: "Agent Workers" }));
  fireEvent.click(edit ? await screen.findByRole("button", { name: `Edit Existing Worker · ${value.agent.id}` }) : screen.getByRole("button", { name: "New Agent Worker" }));
  await waitFor(() => expect((screen.getByRole("radio", { name: "Codex" }) as HTMLButtonElement).disabled).toBe(false));
}
const confirmHarness = (name = "Codex") => fireEvent.click(screen.getByRole("radio", { name }));
const next = () => fireEvent.click(screen.getByRole("button", { name: "Next" }));
async function nextAfterAccountRead() {
  // Selection alone is presentation. Wait for the independent current-account
  // read before advancing, including slow CI transport/effect delivery.
  await waitFor(() => expect(screen.queryByText("Loading selected account…")).toBeNull());
  next();
}

async function nextAfterSourceProof(value: ReturnType<typeof fixture>, accounts: Resource[]) {
  // Source cards retain advisory labels immediately. Only the independent
  // exact GetResource result can establish each selected account's proof.
  await waitFor(() => {
    for (const account of accounts) expect(value.client.getQueryCache().getAll().some(query => {
      const resource = (query.state.data as { resource?: Resource } | undefined)?.resource;
      return query.state.status === "success" && query.state.fetchStatus === "idle" && resource?.id === account.id && resource.kind === EntityKind.ACCOUNT && resource.revision === account.revision;
    })).toBe(true);
  });
  await act(async () => {});
  await waitFor(() => expect(screen.getByRole("button", { name: "Next" }).matches(":disabled")).toBe(false));
  next();
  await screen.findByRole("heading", { name: "Model", level: 3 });
}

async function subscriptionModels(value: ReturnType<typeof fixture>) {
  await start(value); confirmHarness(); next();
  await chooseScrollOption(sourceChoice("Account source"), "subscription:chatgpt");
  fireEvent.click(await screen.findByRole("checkbox", { name: /ChatGPT account/ })); await nextAfterAccountRead();
  return screen.getByRole("combobox", { name: "Model" }) as HTMLInputElement;
}
async function accounts(value: ReturnType<typeof fixture>, multi = false) {
  confirmHarness();
  await waitScrollChoices(sourceChoice("Account source"));
  await chooseScrollOption(sourceChoice("Account source"), `api:${value.provider.id}`);
  fireEvent.click(await screen.findByRole("checkbox", { name: /Personal API/ }));
  if (multi) { fireEvent.click(screen.getByRole("button", { name: /^Load more.*[Aa]ccount/ })); fireEvent.click(await screen.findByRole("checkbox", { name: /Team API/ })); }
  await waitFor(() => expect(screen.getAllByText(`${multi ? 2 : 1} accounts selected`)[0]).toBeTruthy());
  await nextAfterAccountRead();
  await screen.findByRole("combobox", { name: "Model" });
}

it.each(["Codex", "Claude Code", "OpenCode", "Grok Build"])("confirms %s and immediately focuses Accounts without saving or searching models", async name => {
  const value = fixture(); await start(value);
  const group = screen.getByRole("radiogroup", { name: "Harness" });
  const cards = within(group).getAllByRole("radio");
  expect(cards.map(card => card.getAttribute("aria-label"))).toEqual(["Codex", "Claude Code", "OpenCode", "Grok Build"]);
  expect(within(group).getByRole("radio", { checked: true })).toBe(cards[0]);
  expect(cards.map(card => card.tabIndex)).toEqual([0, -1, -1, -1]);
  expect(screen.queryByRole("button", { name: "Next" })).toBeNull();
  expect(group.getAttribute("aria-describedby")!.split(" ").map(id => globalThis.document.getElementById(id)?.textContent)).toContain("Choose a harness to continue to Accounts.");
  fireEvent.submit(group.closest("form")!);
  expect(screen.getByRole("heading", { name: "Harness", level: 3 })).toBeTruthy();
  confirmHarness(name);
  expect(screen.getByRole("heading", { name: "Accounts", level: 3 })).toBe(globalThis.document.activeElement);
  expect(value.save).not.toHaveBeenCalled(); expect(value.discover).not.toHaveBeenCalled(); expect(value.search).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Back" }));
  expect(screen.getByRole("radio", { checked: true, name }).tabIndex).toBe(0);
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
    expect(screen.getByRole("heading", { name: "Harness", level: 3 })).toBeTruthy();
  }
  expect(value.save).not.toHaveBeenCalled(); expect(value.discover).not.toHaveBeenCalled(); expect(value.search).not.toHaveBeenCalled();
});

it("keeps an unsupported saved harness on its step until a supported card is confirmed", async () => {
  const value = fixture();
  value.agent.documentJson = encode({ ...document(value.agent), harness: "unsupported-harness" });
  await start(value, true);
  const group = screen.getByRole("radiogroup", { name: "Harness" });
  expect(within(group).queryByRole("radio", { checked: true })).toBeNull();
  const codex = within(group).getByRole("radio", { name: "Codex" });
  expect(codex.tabIndex).toBe(0);
  fireEvent.submit(codex.closest("form")!);
  expect(screen.getByRole("heading", { name: "Harness", level: 3 })).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Next" })).toBeNull();
  confirmHarness("Grok Build");
  fireEvent.click(sourceChoice("Account source"));
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
  const value = fixture(); await start(value, true); confirmHarness();
  await waitFor(() => expect(scrollChoiceValue(sourceChoice("Account source"))).toBe(`api:${value.provider.id}`));
  await screen.findByRole("checkbox", { name: /Personal API/ }); await nextAfterAccountRead();
  expect((screen.getByRole("combobox", { name: "Model" }) as HTMLInputElement).value).toBe("example-0");
  fireEvent.click(screen.getByRole("button", { name: "Back" })); fireEvent.click(screen.getByRole("button", { name: "Back" }));
  confirmHarness();
  expect(scrollChoiceValue(sourceChoice("Account source"))).toBe(`api:${value.provider.id}`);
  expect(screen.getAllByText("1 accounts selected")[0]).toBeTruthy(); await nextAfterAccountRead();
  expect((screen.getByRole("combobox", { name: "Model" }) as HTMLInputElement).value).toBe("example-0");
  fireEvent.click(screen.getByRole("button", { name: "Back" })); fireEvent.click(screen.getByRole("button", { name: "Back" }));
  confirmHarness("Claude Code");
  expect(scrollChoiceValue(sourceChoice("Account source"))).toBe("");
  expect(screen.getAllByText("0 accounts selected")[0]).toBeTruthy();
  expect(screen.queryByRole("option", { name: "ChatGPT subscription" })).toBeNull();
  await chooseScrollOption(sourceChoice("Account source"), `api:${value.provider.id}`);
  await waitFor(() => expect(screen.queryByRole("checkbox", { name: /Personal API/ })).toBeNull());
  next();
  expect(screen.getByRole("heading", { name: "Accounts", level: 3 })).toBeTruthy();
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
  confirmHarness();
  expect(screen.getByRole("radio", { checked: true, name: "Codex" })).toBeTruthy();
  expect(screen.getByRole("heading", { name: "Harness", level: 3 })).toBeTruthy();
  expect(value.save).not.toHaveBeenCalled(); expect(value.discover).not.toHaveBeenCalled(); expect(value.search).not.toHaveBeenCalled();
});

it("waits for server support and blocks confirmation while inactive", async () => {
  const value = fixture(); let release!: () => void;
  const pending = new Promise<void>(resolve => { release = resolve; });
  const transport: Transport = { ...value.transport, async unary(...args) { if (args[0].name === "GetStatus") await pending; return value.transport.unary(...args); } };
  const view = render(value.wizardView(true, transport));
  await screen.findByText("Checking server support…");
  confirmHarness("Claude Code");
  expect(screen.getByRole("heading", { name: "Harness", level: 3 })).toBeTruthy();
  await act(async () => release());
  await waitFor(() => expect((screen.getByRole("radio", { name: "Codex" }) as HTMLButtonElement).disabled).toBe(false));
  expect(screen.getByRole("heading", { name: "Harness", level: 3 })).toBeTruthy();
  view.rerender(value.wizardView(false, transport));
  expect(screen.getAllByRole("radio").every(card => (card as HTMLButtonElement).disabled)).toBe(true);
  confirmHarness("Claude Code"); fireEvent.keyDown(screen.getByRole("radio", { name: "Codex" }), { key: "End" });
  expect(screen.getByRole("radio", { name: "Codex", checked: true })).toBeTruthy();
  expect(screen.getByRole("heading", { name: "Harness", level: 3 })).toBeTruthy();
  expect(value.save).not.toHaveBeenCalled(); expect(value.discover).not.toHaveBeenCalled(); expect(value.search).not.toHaveBeenCalled();
});

it.each(["pending", "uncertain"])("blocks harness confirmation while a retained save is %s", async state => {
  const value = fixture(); let release!: () => void;
  const pending = new Promise<void>(resolve => { release = resolve; });
  value.save.mockImplementation(async () => { await pending; throw new ConnectError("Lost acknowledgement", Code.Unavailable); });
  function RetainedSave() {
    const mutation = useRetainedMutation("agent-worker-wizard:new", ConfigurationQuery.saveAgentWorker);
    return <button onClick={() => void mutation.send({ mutation: { requestId: newRequestId() }, schemaVersion: 1, documentJson: encode(document(value.agent)) })}>Start retained save</button>;
  }
  render(<StrictMode><TransportProvider transport={value.transport}><QueryClientProvider client={value.client}><MutationIntents><RetainedSave /><AgentWorkerWizard active saved={() => {}} cancel={() => {}} /></MutationIntents></QueryClientProvider></TransportProvider></StrictMode>);
  await waitFor(() => expect((screen.getByRole("radio", { name: "Codex" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Start retained save" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  if (state === "uncertain") { await act(async () => release()); await screen.findByRole("button", { name: "Retry the same Worker save" }); }
  expect(screen.getAllByRole("radio").every(card => (card as HTMLButtonElement).disabled)).toBe(true);
  confirmHarness("Claude Code"); fireEvent.keyDown(screen.getByRole("radio", { name: "Codex" }), { key: "End" });
  expect(screen.getByRole("radio", { name: "Codex", checked: true })).toBeTruthy();
  expect(screen.getByRole("heading", { name: "Harness", level: 3 })).toBeTruthy();
  expect(value.save).toHaveBeenCalledTimes(1); expect(value.discover).not.toHaveBeenCalled(); expect(value.search).not.toHaveBeenCalled();
  if (state === "pending") await act(async () => release());
});

it("rejects a harness change exceeding the draft limit without advancing or changing the saved harness", async () => {
  const value = fixture();
  const draft = { ...document(value.agent), accounts: [], model_id: "", options: { retained: "" } };
  draft.options.retained = "x".repeat((1 << 20) - 4 - encode(draft).byteLength);
  value.agent.documentJson = encode(draft);
  expect(value.agent.documentJson.byteLength).toBe((1 << 20) - 4);
  await start(value, true);
  confirmHarness("Claude Code");
  expect(screen.getByText("This configuration is too large.")).toBeTruthy();
  expect(screen.getByRole("radio", { name: "Codex", checked: true })).toBeTruthy();
  expect(screen.getByRole("heading", { name: "Harness", level: 3 })).toBeTruthy();
  confirmHarness();
  expect(screen.getByRole("heading", { name: "Accounts", level: 3 })).toBe(globalThis.document.activeElement);
  expect(screen.getAllByText("0 accounts selected")[0]).toBeTruthy();
  expect(value.save).not.toHaveBeenCalled(); expect(value.discover).not.toHaveBeenCalled();
});

it("removes Models and saves an ordered multi-account Worker only at the last step", async () => {
  const value = fixture(); await start(value);
  expect(screen.queryByRole("button", { name: "Models" })).toBeNull();
  expect(within(screen.getByRole("navigation", { name: "Settings categories" })).getAllByRole("button")).toHaveLength(18);
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
  const value = fixture(); await start(value); confirmHarness(); next();
  expect(screen.getByRole("heading", { name: "Accounts", level: 3 })).toBeTruthy();
  expect(value.save).not.toHaveBeenCalled();
  await waitScrollChoices(sourceChoice("Account source"));
  await chooseScrollOption(sourceChoice("Account source"), `api:${value.provider.id}`); next();
  expect(screen.getByRole("alert").textContent).toContain("Select at least one");
  fireEvent.click(await screen.findByRole("checkbox", { name: /Personal API/ }));
  fireEvent.click(screen.getByRole("button", { name: /^Load more.*[Aa]ccount/ }));
  fireEvent.click(await screen.findByRole("checkbox", { name: /Team API/ }));
  fireEvent.click(screen.getByText("Routing options"));
  expect((screen.getByRole("option", { name: "fixed" }) as HTMLOptionElement).disabled).toBe(true);
  fireEvent.change(screen.getByLabelText("Weight for account 2"), { target: { value: "5" } });
  fireEvent.click(screen.getByRole("button", { name: "Move account 2 up" }));
  expect((screen.getByLabelText("Weight for account 1") as HTMLInputElement).value).toBe("5");
  await nextAfterAccountRead(); fireEvent.change(screen.getByRole("combobox", { name: "Model" }), { target: { value: "retained-input" } });
  fireEvent.click(screen.getByRole("button", { name: "Back" }));
  await chooseScrollOption(sourceChoice("Account source"), `api:${value.otherProvider.id}`);
  expect(screen.getAllByText("0 accounts selected")[0]).toBeTruthy();
  next(); expect(screen.getByRole("heading", { name: "Accounts", level: 3 })).toBeTruthy();
});

it("autocompletes known subscription models with an empty saved list and saves through native ID", async () => {
  const value = fixture(); value.search.mockResolvedValue({ models: [], providers: [], nextPageToken: "" }); await start(value); confirmHarness(); next();
  await chooseScrollOption(sourceChoice("Account source"), "subscription:chatgpt");
  fireEvent.click(await screen.findByRole("checkbox", { name: /ChatGPT account/ })); await nextAfterAccountRead();
  await screen.findByText(/Known models · Catalog updated Oct 6, 2026/);
  const input = screen.getByRole("combobox", { name: "Model" });
  fireEvent.focus(input); fireEvent.change(input, { target: { value: "gpt" } });
  await screen.findByRole("option", { name: /GPT Known Current.*Known/ });
  fireEvent.keyDown(input, { key: "ArrowDown" }); fireEvent.keyDown(input, { key: "Enter" });
  expect((input as HTMLInputElement).value).toBe("gpt-known-current");
  expect(screen.queryByRole("button", { name: "Load more Model suggestions" })).toBeNull();
  expect(screen.getByText("Availability depends on your plan and installed harness.")).toBeTruthy();
  next(); fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Known model Worker" } });
  fireEvent.click(screen.getByRole("button", { name: "Save Agent Worker" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  expect(value.save.mock.calls[0][0].model).toMatchObject({ selection: { case: "nativeId", value: "gpt-known-current" }, expectedModelRevision: 0n });
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
  await start(value); confirmHarness();
  await waitScrollChoices(sourceChoice("Account source"));
  await chooseScrollOption(sourceChoice("Account source"), type === "api" ? `api:${value.provider.id}` : "subscription:chatgpt");
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
  await start(value); confirmHarness();
  await waitScrollChoices(sourceChoice("Account source"));
  await chooseScrollOption(sourceChoice("Account source"), `api:${value.provider.id}`);
  await screen.findByText("No accounts to select on this page.");
  expect(screen.getByText("Connect an account in AI Subscription or AI API Keys.")).toBeTruthy();
  expect(screen.queryByRole("checkbox", { name: /Backup API/ })).toBeNull();
  expect(value.list.mock.calls.filter(([request]) => request.filter?.kind === EntityKind.ACCOUNT)).toHaveLength(1);
  expect((screen.getByRole("button", { name: /^Load more.*[Aa]ccount/ }) as HTMLButtonElement).disabled).toBe(false);
  fireEvent.click(screen.getByRole("button", { name: /^Load more.*[Aa]ccount/ }));
  await screen.findByRole("checkbox", { name: /Team API/ });
  expect(value.list.mock.calls.at(-1)![0].filter?.pageToken).toBe("account-page-2");
  expect(screen.queryByRole("button", { name: "First account page" })).toBeNull();
  expect(screen.getByRole("checkbox", { name: /Team API/ })).toBeTruthy();
  connected = true;
  await refreshAccounts(value);
  await screen.findByRole("button", { name: /^Reload/ });
  fireEvent.click(screen.getByRole("button", { name: /^Reload/ }));
  await screen.findByRole("checkbox", { name: /Personal API/ });
  expect(screen.queryByText("No accounts to select on this page.")).toBeNull();
  expect(value.save).not.toHaveBeenCalled(); expect(value.discover).not.toHaveBeenCalled();
});

it.each(["choices", "hidden-only", "failed", "invalid"] as const)("hides hidden-only emptiness during a pending refresh that returns %s", async result => {
  const value = fixture();
  const loaded = value.list.getMockImplementation()!;
  const hiddenPage = { resources: [value.accounts[2]], nextPageToken: "account-page-2" };
  value.list.mockImplementation(request => request.filter?.kind === EntityKind.ACCOUNT ? hiddenPage : loaded(request));
  let refreshing = false;
  let release!: () => void;
  const pending = new Promise<void>(resolve => { release = resolve; });
  const transport: Transport = { ...value.transport, async unary(...args) { if (refreshing && args[0].name === "ListResources") await pending; return value.transport.unary(...args); } };
  await start(value, false, transport);
  confirmHarness();
  await waitScrollChoices(sourceChoice("Account source"));
  await chooseScrollOption(sourceChoice("Account source"), `api:${value.provider.id}`);
  await screen.findByText("No accounts to select on this page.");
  refreshing = true;
  await refreshAccounts(value);
  await waitFor(() => expect(globalThis.document.querySelector(".worker-account-list [data-continuation] [role=status]")).not.toBeNull());
  expect((screen.getByRole("button", { name: /^Load more.*[Aa]ccount/ }) as HTMLButtonElement).disabled).toBe(true);
  expect(screen.queryByText("No accounts to select on this page.")).toBeNull();
  expect(screen.queryByText("Connect an account in AI Subscription or AI API Keys.")).toBeNull();
  expect(screen.queryByText("Loading accounts…")).toBeNull();
  value.list.mockImplementation(request => {
    if (request.filter?.kind !== EntityKind.ACCOUNT) return loaded(request);
    if (result === "failed") throw new ConnectError("Account read unavailable", Code.Unavailable);
    return result === "choices" ? { ...hiddenPage, resources: [value.accounts[0]] } : result === "invalid" ? { ...hiddenPage, resources: [value.subscription] } : hiddenPage;
  });
  await act(async () => release());
  await waitFor(() => expect(globalThis.document.querySelector(".worker-account-list [data-continuation] [role=status]")).toBeNull());
  if (result === "hidden-only") {
    expect(screen.getByText("No accounts to select on this page.")).toBeTruthy();
    expect(screen.getByText("Connect an account in AI Subscription or AI API Keys.")).toBeTruthy();
  } else {
    expect(screen.queryByText("No accounts to select on this page.")).toBeNull();
    if (result === "choices") { fireEvent.click(screen.getByRole("button", { name: /^Reload/ })); await screen.findByRole("checkbox", { name: /Personal API/ }); }
    else if (result === "failed") expect(screen.getByText("Refresh failed. Showing the last successfully loaded accounts.")).toBeTruthy();
    else expect(screen.getByText(/This account page includes unsupported or mismatched source data/)).toBeTruthy();
  }
  if (result === "failed" || result === "invalid") expect(screen.queryByRole("button", { name: /^Load more.*[Aa]ccount/ })).toBeNull();
  else expect((screen.getByRole("button", { name: /^Load more.*[Aa]ccount/ }) as HTMLButtonElement).disabled).toBe(false);
  expect(screen.getAllByText("0 accounts selected")[0]).toBeTruthy();
  expect(value.save).not.toHaveBeenCalled(); expect(value.discover).not.toHaveBeenCalled();
});

it("retains cached choices and selected account order and weights while refresh is pending", async () => {
  const value = fixture();
  const expected = [{ id: value.accounts[0].id, weight: 3 }, { id: value.accounts[1].id, weight: 5 }];
  value.agent.documentJson = encode({ ...document(value.agent), accounts: expected });
  let refreshing = false;
  let release!: () => void;
  const pending = new Promise<void>(resolve => { release = resolve; });
  const transport: Transport = { ...value.transport, async unary(...args) { if (refreshing && args[0].name === "ListResources") await pending; return value.transport.unary(...args); } };
  await start(value, true, transport);
  confirmHarness();
  await screen.findByRole("checkbox", { name: /Personal API/ });
  fireEvent.click(screen.getByText("Routing options"));
  refreshing = true;
  await refreshAccounts(value);
  await waitFor(() => expect(globalThis.document.querySelector(".worker-account-list [data-continuation] [role=status]")).not.toBeNull());
  expect((screen.getByRole("checkbox", { name: /Personal API/ }) as HTMLInputElement).checked).toBe(true);
  expect(screen.getAllByText("2 accounts selected")[0]).toBeTruthy();
  const selected = globalThis.document.querySelector(".worker-routing ol")!;
  expect(within(selected as HTMLElement).getAllByRole("listitem").map(row => row.querySelector("strong")!.textContent)).toEqual(["Personal API", "Team API"]);
  expect((screen.getByLabelText("Weight for account 1") as HTMLInputElement).value).toBe("3");
  expect((screen.getByLabelText("Weight for account 2") as HTMLInputElement).value).toBe("5");
  expect(screen.queryByText("No accounts to select on this page.")).toBeNull();
  await act(async () => release());
  await waitFor(() => expect(globalThis.document.querySelector(".worker-account-list [data-continuation] [role=status]")).toBeNull());
  next(); await screen.findByRole("combobox", { name: "Model" }); next();
  fireEvent.click(screen.getByRole("button", { name: "Save Agent Worker" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  expect(JSON.parse(new TextDecoder().decode(value.save.mock.calls[0][0].documentJson)).accounts).toEqual(expected);
  expect(value.discover).not.toHaveBeenCalled();
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
  await start(value); confirmHarness();
  await waitScrollChoices(sourceChoice("Account source"));
  await chooseScrollOption(sourceChoice("Account source"), `api:${value.provider.id}`);
  if (state === "refresh") {
    await screen.findByText("No accounts to select on this page.");
    failRead(); await refreshAccounts(value);
    await screen.findByText("Refresh failed. Showing the last successfully loaded accounts.");
  }
  await within(screen.getByRole("region", { name: "Choose accounts" })).findByRole("alert");
  expect(screen.queryByText("No accounts to select on this page.")).toBeNull();
  expect(screen.queryByText(/No accounts for this source/)).toBeNull();
  expect((screen.getByRole("button", { name: "Retry accounts" }) as HTMLButtonElement).disabled).toBe(false);
});

it.each(["edit", "refresh"])("preserves hidden selected account order, weights and save bytes after %s", async state => {
  const value = fixture();
  const expected = state === "edit" ? [{ id: value.accounts[2].id, weight: 5 }, { id: value.accounts[0].id, weight: 3 }] : [{ id: value.accounts[0].id, weight: 3 }, { id: value.accounts[1].id, weight: 5 }];
  if (state === "edit") {
    value.agent.documentJson = encode({ ...document(value.agent), accounts: expected });
    value.accounts[0].documentJson = encode({ ...document(value.accounts[0]), health: "failed" });
    value.save.mockImplementation(async request => ({ requestId: request.mutation!.requestId, resource: create(ResourceSchema, { ...value.agent, revision: 2n, documentJson: request.documentJson }) }));
    await start(value, true); confirmHarness();
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
  await screen.findAllByText("2 accounts selected");
  expect((screen.getByLabelText("Weight for account 1") as HTMLInputElement).value).toBe(String(expected[0].weight));
  expect((screen.getByLabelText("Weight for account 2") as HTMLInputElement).value).toBe(String(expected[1].weight));
  expect(screen.getAllByRole("checkbox", { name: /Personal API|Team API|Backup API/ }).every(row => (row as HTMLInputElement).checked)).toBe(true);
  next();
  fireEvent.change(await screen.findByRole("combobox", { name: "Model" }), { target: { value: "exact-model" } }); next();
  fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Retained selection Worker" } });
  fireEvent.click(screen.getByRole("button", { name: "Save Agent Worker" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  expect(JSON.parse(new TextDecoder().decode(value.save.mock.calls[0][0].documentJson)).accounts).toEqual(expected);
  expect(value.discover).not.toHaveBeenCalled();
});

it("retains an ineligible selection and removes it through its explicit Routing options action", async () => {
  const value = fixture();
  value.accounts[0].documentJson = encode({ ...document(value.accounts[0]), health: "failed" });
  await start(value, true); confirmHarness();
  await screen.findByText("No accounts to select on this page.");
  await screen.findAllByText("1 accounts selected");
  fireEvent.click(screen.getByText("Routing options"));
  expect(screen.getByRole("button", { name: "Remove account 1" })).toBeTruthy();
  expect(within(screen.getByRole("region", { name: "Choose accounts" })).getAllByText("Personal API").length).toBeGreaterThan(0);
  fireEvent.click(screen.getByRole("button", { name: "Remove account 1" }));
  expect(screen.getAllByText("0 accounts selected")[0]).toBeTruthy();
  next();
  expect(screen.getByRole("heading", { name: "Accounts", level: 3 })).toBeTruthy();
  expect(value.save).not.toHaveBeenCalled();
});

it("rejects an invalid known catalog date without crashing the wizard", async () => {
  const value = fixture(); value.search.mockResolvedValue({ models: [], providers: [], nextPageToken: "" }); value.known.mockResolvedValue({ subscriptionService: SubscriptionServiceIdentity.CHATGPT, models: [{ nativeId: "gpt-known-current", displayName: "GPT Known Current", order: 0 }], catalogVersion: `sha256:${"a".repeat(64)}`, updatedAt: "2026-99-99", source: KnownSubscriptionModelCatalogSource.BUNDLED });
  await subscriptionModels(value);
  await screen.findByText("The known model catalog is unavailable. Reload models or enter an exact model ID.");
  expect(screen.queryByRole("option", { name: /GPT Known Current/ })).toBeNull();
});

it("drops cached known candidates when the server loses catalog capability", async () => {
  const value = fixture(); value.search.mockResolvedValue({ models: [], providers: [], nextPageToken: "" });
  const input = await subscriptionModels(value); fireEvent.focus(input);
  await screen.findByRole("option", { name: /GPT Known Current.*Known/ });
  value.capabilities.splice(value.capabilities.indexOf(SystemCapability.KNOWN_SUBSCRIPTION_MODELS_V1), 1);
  await act(async () => { await value.client.invalidateQueries(); });
  await waitFor(() => expect(screen.queryByRole("option", { name: /GPT Known Current.*Known/ })).toBeNull());
  expect(screen.getByText("Update the server to search known models. Saved models and exact model IDs remain available.")).toBeTruthy();
});

it("prefers a saved subscription model over its known duplicate and retains its revision", async () => {
  const value = fixture();
  const saved = create(ResourceSchema, { kind: EntityKind.MODEL, schemaVersion: 2, id: newRequestId(), revision: 4n, documentJson: encode({ name: "Saved GPT", native_id: "gpt-known-current", source_kind: "subscription", subscription_service: "chatgpt", harnesses: ["codex"], hidden: false }) });
  value.records.push(saved); value.search.mockResolvedValue({ models: [saved], providers: [], nextPageToken: "" });
  const input = await subscriptionModels(value); fireEvent.focus(input);
  await screen.findByRole("option", { name: /Saved GPT.*Saved/ });
  expect(screen.getAllByRole("option")).toHaveLength(1);
  fireEvent.keyDown(input, { key: "ArrowDown" }); fireEvent.keyDown(input, { key: "Enter" }); next();
  fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Saved subscription model" } });
  await waitFor(() => expect((screen.getByRole("button", { name: "Save Agent Worker" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Save Agent Worker" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  expect(value.save.mock.calls[0][0].model).toMatchObject({ selection: { case: "modelId", value: saved.id }, expectedModelRevision: 4n });
});

it("drops saved IDs that disappear after a model reload", async () => {
  const value = fixture();
  const saved = create(ResourceSchema, { kind: EntityKind.MODEL, schemaVersion: 2, id: newRequestId(), revision: 4n, documentJson: encode({ name: "Saved GPT", native_id: "gpt-known-current", source_kind: "subscription", subscription_service: "chatgpt", harnesses: ["codex"], hidden: false }) });
  value.records.push(saved);
  value.search.mockResolvedValue({ models: [saved], providers: [], nextPageToken: "" });
  const input = await subscriptionModels(value); fireEvent.focus(input);
  await screen.findByRole("option", { name: /Saved GPT.*Saved/ });
  value.search.mockResolvedValue({ models: [], providers: [], nextPageToken: "" });
  fireEvent.click(screen.getByRole("button", { name: "Reload models" }));
  await screen.findByRole("option", { name: /GPT Known Current.*Known/ });
});

it("does not offer a known duplicate before all saved pages are visited", async () => {
  const value = fixture();
  const first = create(ResourceSchema, { kind: EntityKind.MODEL, schemaVersion: 2, id: newRequestId(), revision: 2n, documentJson: encode({ name: "First saved", native_id: "saved-first", source_kind: "subscription", subscription_service: "chatgpt", harnesses: ["codex"], hidden: false }) });
  const later = create(ResourceSchema, { kind: EntityKind.MODEL, schemaVersion: 2, id: newRequestId(), revision: 4n, documentJson: encode({ name: "Saved GPT", native_id: "gpt-known-current", source_kind: "subscription", subscription_service: "chatgpt", harnesses: ["codex"], hidden: false }) });
  value.records.push(first, later); value.search.mockImplementation(async request => ({ models: request.pageToken ? [later] : [first], providers: [], nextPageToken: request.pageToken ? "" : "model-page-2" }));
  const input = await subscriptionModels(value); fireEvent.focus(input);
  await screen.findByRole("option", { name: /First saved.*Saved/ });
  expect(screen.queryByRole("option", { name: /GPT Known Current.*Known/ })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Load more Model suggestions" }));
  await screen.findByRole("option", { name: /Saved GPT.*Saved/ });
  expect(screen.queryByRole("option", { name: /GPT Known Current.*Known/ })).toBeNull();
  fireEvent.click(screen.getByRole("option", { name: /Saved GPT.*Saved/ })); next();
  fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Later saved model Worker" } });
  await waitFor(() => expect((screen.getByRole("button", { name: "Save Agent Worker" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Save Agent Worker" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  expect(value.save.mock.calls[0][0].model).toMatchObject({ selection: { case: "modelId", value: later.id }, expectedModelRevision: 4n });
});

it("retains known candidates and typed input after reload failure, with name search and exact-ID fallback", async () => {
  const value = fixture(); value.search.mockResolvedValue({ models: [], providers: [], nextPageToken: "" });
  const input = await subscriptionModels(value);
  await screen.findByText(/Known models · Catalog updated/);
  fireEvent.change(input, { target: { value: "Known Current" } });
  await screen.findByRole("option", { name: /GPT Known Current/ });
  value.known.mockRejectedValue(new ConnectError("Unavailable", Code.Unavailable));
  await waitFor(() => expect((screen.getByRole("button", { name: "Reload models" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Reload models" }));
  await screen.findByText(/Showing the last successfully loaded models/);
  expect(input.value).toBe("Known Current");
  fireEvent.focus(input); expect(screen.getByRole("option", { name: /GPT Known Current/ })).toBeTruthy();
  fireEvent.change(input, { target: { value: "unlisted-native-id" } });
  await waitFor(() => expect(screen.queryByRole("option", { name: /GPT Known Current/ })).toBeNull());
  fireEvent.click(screen.getByRole("option", { name: /Use exact ID/ })); next();
  expect(screen.getByRole("heading", { name: "Configure", level: 3 })).toBeTruthy();
});

it("gates known reads on capability 35 and rejects mismatched service data", async () => {
  const value = fixture([SystemCapability.AGENT_WORKER_WIZARD_V1]);
  value.search.mockResolvedValue({ models: [], providers: [], nextPageToken: "" });
  const input = await subscriptionModels(value);
  await screen.findByText(/Update the server to search known models/);
  expect(value.known).not.toHaveBeenCalled();
  fireEvent.change(input, { target: { value: "exact" } }); next();
  expect(screen.getByRole("heading", { name: "Configure", level: 3 })).toBeTruthy();
});

it("does not retain known candidates after changing harness and service", async () => {
  const value = fixture(); value.search.mockResolvedValue({ models: [], providers: [], nextPageToken: "" });
  const input = await subscriptionModels(value); fireEvent.focus(input);
  await screen.findByRole("option", { name: /GPT Known Current/ });
  fireEvent.click(screen.getByRole("button", { name: "Back" }));
  fireEvent.click(screen.getByRole("button", { name: "Change harness" }));
  fireEvent.click(screen.getByRole("radio", { name: "Claude Code" })); next();
  await chooseScrollOption(sourceChoice("Account source"), "subscription:claude");
  expect(screen.queryByRole("option", { name: /GPT Known Current/ })).toBeNull();
  expect(screen.getAllByText("0 accounts selected")[0]).toBeTruthy();
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
  fireEvent.click(screen.getByRole("button", { name: "Agent Workers" })); fireEvent.click(await screen.findByRole("button", { name: `Edit Existing Worker · ${value.agent.id}` }));
  await waitFor(() => expect((screen.getByRole("radio", { name: "Codex" }) as HTMLButtonElement).disabled).toBe(false));
  confirmHarness(); await waitFor(() => expect(scrollChoiceValue(sourceChoice("Account source"))).toBe(`api:${value.provider.id}`));
  (await screen.findAllByText("1 accounts selected"))[0]; next(); next();
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
  await start(value, true); confirmHarness(); next();
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
  const value = fixture(); await start(value, true); confirmHarness();
  await screen.findByRole("checkbox", { name: /Personal API/ }); await waitFor(() => expect(scrollChoiceValue(sourceChoice("Account source"))).toBe(`api:${value.provider.id}`)); await waitFor(() => expect((screen.getByRole("button", { name: "Next" }) as HTMLButtonElement).disabled).toBe(false)); next(); await screen.findByRole("combobox", { name: "Model" }); await waitFor(() => expect((screen.getByRole("button", { name: "Next" }) as HTMLButtonElement).disabled).toBe(false)); next();
  fireEvent.change(screen.getByLabelText("Name"), { target: { value: "" } });
  fireEvent.click(screen.getByRole("button", { name: "Save Agent Worker" }));
  expect(globalThis.document.activeElement).toBe(screen.getByLabelText("Name"));
  expect(value.save).not.toHaveBeenCalled();
  screen.getByRole("combobox", { name: "Permission mode" }).focus();
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
  fireEvent.click(await screen.findByRole("option", { name: /Example A/ })); fireEvent.focus(screen.getByRole("combobox", { name: "Model" }));
  await waitFor(() => expect((screen.getByRole("button", { name: "Load more Model suggestions" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Load more Model suggestions" }));
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
  fireEvent.click(await screen.findByRole("option", { name: /Example A/ })); fireEvent.focus(screen.getByRole("combobox", { name: "Model" })); next();
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
  await start(value); confirmHarness();
  await waitScrollChoices(sourceChoice("Account source"));
  await chooseScrollOption(sourceChoice("Account source"), `api:${value.provider.id}`);
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
  fireEvent.click(await screen.findByRole("option", { name: /Example A/ })); fireEvent.focus(screen.getByRole("combobox", { name: "Model" })); next();
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
  fireEvent.click(screen.getByRole("button", { name: "Load more Model suggestions" }));
  await waitFor(() => expect(value.search.mock.calls.some(([request]) => request.pageToken === "model-page-2")).toBe(true));
  expect(screen.getByRole("option", { name: /Example A/ })).toBeTruthy();
  expect(globalThis.document.getElementById(input.getAttribute("aria-activedescendant")!)).toBeTruthy();
  expect((input as HTMLInputElement).value).toBe("example-1");
  expect(value.save).not.toHaveBeenCalled(); expect(value.discover).not.toHaveBeenCalled();
});

it("saves committed reordered source models atomically and preserves drafts across Back", async () => {
  const value = fixture([SystemCapability.AGENT_WORKER_WIZARD_V1, SystemCapability.AGENT_WORKER_SOURCE_ROUTES_V1]); await start(value); confirmHarness();
  await chooseScrollOption(sourceChoice("Account source 1"), "subscription:chatgpt");
  fireEvent.click(await screen.findByRole("checkbox", { name: /ChatGPT account/ }));
  fireEvent.click(screen.getByRole("button", { name: "+ Add account source" }));
  await chooseScrollOption(sourceChoice("Account source 2"), `api:${value.provider.id}`);
  fireEvent.click(await screen.findByRole("checkbox", { name: /Personal API/ }));
  await waitFor(() => expect(screen.getByRole("checkbox", { name: "Select Personal API" })).toBeTruthy());
  reorderSource(2, "ArrowUp");
  await nextAfterSourceProof(value, [value.subscription, value.accounts[0]]);
  const subscriptionModel = screen.getByRole("combobox", { name: "Model for ChatGPT subscription" });
  const apiModel = screen.getByRole("combobox", { name: "Model for OpenAI API" });
  fireEvent.change(subscriptionModel, { target: { value: "subscription-exact" } }); fireEvent.keyDown(subscriptionModel, { key: "Escape" });
  fireEvent.focus(apiModel); await screen.findByRole("option", { name: /Example A/ }); fireEvent.keyDown(apiModel, { key: "ArrowDown" }); fireEvent.keyDown(apiModel, { key: "Enter" });
  await waitFor(() => { expect((apiModel as HTMLInputElement).value).toBe("example-0"); expect((screen.getByRole("button", { name: "Next" }) as HTMLButtonElement).disabled).toBe(false); });
  await waitFor(() => expect((screen.getByRole("button", { name: "Next" }) as HTMLButtonElement).disabled).toBe(false)); next(); await screen.findByRole("heading", { name: "Configure", level: 3 });
  fireEvent.change(screen.getByRole("textbox", { name: "Name" }), { target: { value: "Subscription priority" } });
  fireEvent.click(screen.getByRole("button", { name: "Back" })); expect((subscriptionModel as HTMLInputElement).value).toBe("subscription-exact");
  await waitFor(() => expect((screen.getByRole("button", { name: "Next" }) as HTMLButtonElement).disabled).toBe(false)); next(); fireEvent.click(screen.getByRole("button", { name: "Save Agent Worker" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  const request = value.save.mock.calls[0][0];
  expect(request.schemaVersion).toBe(3); expect(request.model).toBeUndefined();
  expect(request.routeModels).toMatchObject([{ selection: { case: "modelId", value: value.models[0].id }, expectedModelRevision: 1n }, { selection: { case: "nativeId", value: "subscription-exact" }, expectedModelRevision: 0n }]);
  const saved = JSON.parse(new TextDecoder().decode(request.documentJson));
  expect(saved.routes).toMatchObject([{ accounts: [{ id: value.accounts[0].id, weight: 1 }], routing: "priority" }, { accounts: [{ id: value.subscription.id, weight: 1 }], routing: "priority" }]);
  expect(saved.model_id).toBeUndefined(); expect(saved.accounts).toBeUndefined(); expect(value.discover).not.toHaveBeenCalled();
}, 15000);

it("reorders sources with keyboard grips and resets only a changed source", async () => {
  const value = fixture([SystemCapability.AGENT_WORKER_WIZARD_V1, SystemCapability.AGENT_WORKER_SOURCE_ROUTES_V1]); await start(value); confirmHarness();
  await chooseScrollOption(sourceChoice("Account source 1"), "subscription:chatgpt"); fireEvent.click(await screen.findByRole("checkbox", { name: /ChatGPT account/ }));
  fireEvent.click(screen.getByRole("button", { name: "+ Add account source" })); await chooseScrollOption(sourceChoice("Account source 2"), `api:${value.provider.id}`); fireEvent.click(await screen.findByRole("checkbox", { name: /Personal API/ }));
  await waitFor(() => expect(screen.getByRole("checkbox", { name: "Select Personal API" })).toBeTruthy());
  const personal = screen.getByRole("checkbox", { name: "Select Personal API" });
  const panel = personal.closest("section")!;
  reorderSource(2, "ArrowUp");
  expect(scrollChoiceValue(sourceChoice("Account source 1"))).toBe(`api:${value.provider.id}`);
  expect(screen.queryByRole("checkbox", { name: "Select ChatGPT account" })).toBeNull();
  await chooseScrollOption(sourceChoice("Account source 1"), `api:${value.otherProvider.id}`);
  expect(screen.queryByRole("checkbox", { name: "Select Personal API" })).toBeNull(); expect(screen.queryByRole("checkbox", { name: "Select ChatGPT account" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Remove source 1" })); expect(scrollChoiceValue(sourceChoice("Account source 1"))).toBe("subscription:chatgpt");
  await waitFor(() => expect((screen.getByRole("button", { name: "Next" }) as HTMLButtonElement).disabled).toBe(false)); next(); expect(screen.getByRole("combobox", { name: "Model for ChatGPT subscription" })).toBeTruthy();
}, 15000);

it("keeps Harness confirmation and account visibility on source-route servers", async () => {
  const value = fixture([SystemCapability.AGENT_WORKER_WIZARD_V1, SystemCapability.AGENT_WORKER_SOURCE_ROUTES_V1]);
  await start(value);
  expect(screen.queryByRole("button", { name: "Next" })).toBeNull();
  const codex = screen.getByRole("radio", { name: "Codex" });
  fireEvent.keyDown(codex, { key: "ArrowRight" });
  expect(screen.getByRole("heading", { name: "Harness", level: 3 })).toBeTruthy();
  expect(screen.getByRole("radio", { name: "Claude Code", checked: true })).toBeTruthy();
  fireEvent.keyDown(screen.getByRole("radio", { name: "Claude Code" }), { key: "Home" });
  confirmHarness();
  expect(globalThis.document.activeElement).toBe(screen.getByRole("heading", { name: "Accounts", level: 3 }));
  await waitScrollChoices(sourceChoice("Account source 1"));
  await chooseScrollOption(sourceChoice("Account source 1"), `api:${value.provider.id}`);
  await screen.findByRole("checkbox", { name: /Personal API/ });
  fireEvent.click(screen.getByRole("button", { name: /^Load more.*[Aa]ccount/ }));
  await screen.findByRole("checkbox", { name: /Team API/ });
  expect(screen.queryByRole("checkbox", { name: /Backup API/ })).toBeNull();
  expect(value.save).not.toHaveBeenCalled(); expect(value.discover).not.toHaveBeenCalled(); expect(value.search).not.toHaveBeenCalled();
});

it("saves known candidates as exact native IDs on source-route servers", async () => {
  const value = fixture([SystemCapability.AGENT_WORKER_WIZARD_V1, SystemCapability.AGENT_WORKER_SOURCE_ROUTES_V1, SystemCapability.KNOWN_SUBSCRIPTION_MODELS_V1]);
  value.search.mockResolvedValue({ models: [], providers: [], nextPageToken: "" });
  await start(value); confirmHarness();
  await chooseScrollOption(sourceChoice("Account source 1"), "subscription:chatgpt");
  fireEvent.click(await screen.findByRole("checkbox", { name: /ChatGPT account/ })); await screen.findByRole("checkbox", { name: "Select ChatGPT account" }); await nextAfterSourceProof(value, [value.subscription]);
  const input = await screen.findByRole("combobox", { name: "Model for ChatGPT subscription" });
  fireEvent.focus(input); fireEvent.click(await screen.findByRole("option", { name: /GPT Known Current/ }));
  expect(value.known).toHaveBeenCalledTimes(1); expect(value.save).not.toHaveBeenCalled();
  await waitFor(() => expect((screen.getByRole("button", { name: "Next" }) as HTMLButtonElement).disabled).toBe(false)); next(); fireEvent.change(screen.getByRole("textbox", { name: "Name" }), { target: { value: "Known route" } });
  fireEvent.click(screen.getByRole("button", { name: "Save Agent Worker" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  expect(value.save.mock.calls[0][0].model).toMatchObject({ selection: { case: "nativeId", value: "gpt-known-current" }, expectedModelRevision: 0n });
  expect(value.discover).not.toHaveBeenCalled();
});

it("keeps later-page saved revisions ahead of known duplicates for each source", async () => {
  const value = fixture([SystemCapability.AGENT_WORKER_WIZARD_V1, SystemCapability.AGENT_WORKER_SOURCE_ROUTES_V1, SystemCapability.KNOWN_SUBSCRIPTION_MODELS_V1]);
  const saved = create(ResourceSchema, { kind: EntityKind.MODEL, schemaVersion: 2, id: newRequestId(), revision: 4n, documentJson: encode({ name: "Saved GPT", native_id: "gpt-known-current", source_kind: "subscription", subscription_service: "chatgpt", harnesses: ["codex"], hidden: false }) });
  value.records.push(saved);
  value.search.mockImplementation(async request => ({ models: request.pageToken ? [saved] : [], providers: [], nextPageToken: request.pageToken ? "" : "later" }));
  await start(value); confirmHarness();
  await chooseScrollOption(sourceChoice("Account source 1"), "subscription:chatgpt");
  fireEvent.click(await screen.findByRole("checkbox", { name: /ChatGPT account/ })); await screen.findByRole("checkbox", { name: "Select ChatGPT account" }); await nextAfterSourceProof(value, [value.subscription]);
  const input = await screen.findByRole("combobox", { name: "Model for ChatGPT subscription" }); fireEvent.focus(input);
  await waitFor(() => expect(value.known).toHaveBeenCalledTimes(1));
  expect(screen.queryByRole("option", { name: /GPT Known Current/ })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Load more Source 1 model pages" })); fireEvent.focus(input);
  fireEvent.click(await screen.findByRole("option", { name: /Saved GPT/ }));
  await waitFor(() => expect(input).toHaveProperty("value", "gpt-known-current"));
  await waitFor(() => expect((screen.getByRole("button", { name: "Next" }) as HTMLButtonElement).disabled).toBe(false));
  expect(screen.queryByRole("option", { name: /GPT Known Current/ })).toBeNull(); await waitFor(() => expect((screen.getByRole("button", { name: "Next" }) as HTMLButtonElement).disabled).toBe(false)); next();
  fireEvent.change(screen.getByRole("textbox", { name: "Name" }), { target: { value: "Saved route" } });
  fireEvent.click(screen.getByRole("button", { name: "Save Agent Worker" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  expect(value.save.mock.calls[0][0].model).toMatchObject({ selection: { case: "modelId", value: saved.id }, expectedModelRevision: 4n });
});

it("changes source-route language without replacing drafts, focus or read identities", async () => {
  const value = fixture([SystemCapability.AGENT_WORKER_WIZARD_V1, SystemCapability.AGENT_WORKER_SOURCE_ROUTES_V1]);
  await start(value); confirmHarness();
  await chooseScrollOption(sourceChoice("Account source 1"), "subscription:chatgpt");
  fireEvent.click(await screen.findByRole("checkbox", { name: /ChatGPT account/ })); await screen.findByRole("checkbox", { name: "Select ChatGPT account" }); await nextAfterSourceProof(value, [value.subscription]);
  const input = await screen.findByRole("combobox", { name: "Model for ChatGPT subscription" });
  fireEvent.change(input, { target: { value: "retained-exact-model" } }); input.focus();
  await waitFor(() => expect(value.search.mock.calls.at(-1)?.[0].query).toBe("retained-exact-model"));
  const searches = value.search.mock.calls.length;
  await act(() => i18n.changeLanguage("ko"));
  expect(screen.getByRole("combobox", { name: "ChatGPT 구독 모델" })).toBe(input);
  expect(globalThis.document.activeElement).toBe(input); expect(input).toHaveProperty("value", "retained-exact-model");
  expect(value.search).toHaveBeenCalledTimes(searches); expect(value.known).not.toHaveBeenCalled();
  expect(value.save).not.toHaveBeenCalled(); expect(value.discover).not.toHaveBeenCalled();
  await act(() => i18n.changeLanguage("en"));
});

it.each([false, true])("keeps navigation in the fixed task footer with its original form (routes=%s)", async routes => {
  const value = fixture([SystemCapability.AGENT_WORKER_WIZARD_V1, ...(routes ? [SystemCapability.AGENT_WORKER_SOURCE_ROUTES_V1] : [])]);
  await start(value);
  const dialog = screen.getByRole("dialog");
  expect(within(dialog).getAllByRole("heading", { name: "New Agent Worker" })).toHaveLength(1);
  expect(within(dialog).queryByRole("button", { name: "Cancel" })).toBeNull();
  expect(dialog.querySelector(".settings-task-footer")?.childElementCount).toBe(0);
  confirmHarness();
  const nextButton = screen.getByRole("button", { name: "Next" }) as HTMLButtonElement;
  const form = dialog.querySelector<HTMLFormElement>("form.worker-wizard")!;
  expect(nextButton.closest(".settings-task-footer")).toBeTruthy();
  expect(nextButton.form).toBe(form);
  expect(form.contains(nextButton)).toBe(false);
  expect(dialog.querySelector(".worker-accounts-workspace")).toBeTruthy();
  next();
  expect(screen.getByRole("heading", { name: "Accounts", level: 3 })).toBeTruthy();
  expect(value.save).not.toHaveBeenCalled();
});

it("switches only displayed source panels and retains mounted drafts and routing controls", async () => {
  const value = fixture([SystemCapability.AGENT_WORKER_WIZARD_V1, SystemCapability.AGENT_WORKER_SOURCE_ROUTES_V1]);
  await start(value); confirmHarness();
  await chooseScrollOption(sourceChoice("Account source 1"), "subscription:chatgpt");
  fireEvent.click(await screen.findByRole("checkbox", { name: /ChatGPT account/ }));
  const firstPanel = (await screen.findByRole("checkbox", { name: "Select ChatGPT account" })).closest("section")!;
  const disclosure = firstPanel.querySelector(".worker-routing")!;
  fireEvent.change(within(firstPanel).getByLabelText("Weight for account 1"), { target: { value: "9" } });
  fireEvent.click(screen.getByRole("button", { name: "+ Add account source" }));
  expect(screen.queryByRole("checkbox", { name: "Select ChatGPT account" })).toBeNull();
  await chooseScrollOption(sourceChoice("Account source 2"), `api:${value.provider.id}`);
  fireEvent.click(await screen.findByRole("checkbox", { name: /Personal API/ }));
  const rail = screen.getByRole("complementary", { name: "Account sources" });
  fireEvent.click(within(rail).getByRole("button", { name: /1 · ChatGPT subscription/ }));
  expect(screen.getByRole("checkbox", { name: "Select ChatGPT account" }).closest("section")).toBe(firstPanel);
  expect(firstPanel.querySelector(".worker-routing")).toBe(disclosure);
  expect((within(firstPanel).getByLabelText("Weight for account 1") as HTMLInputElement).value).toBe("9");
  expect(screen.queryByRole("checkbox", { name: "Select Personal API" })).toBeNull();
  const movedGrip = reorderSource(1, "ArrowDown");
  expect(screen.getByRole("checkbox", { name: "Select ChatGPT account" }).closest("section")).toBe(firstPanel);
  expect(globalThis.document.activeElement).toBe(movedGrip);
  expect(value.save).not.toHaveBeenCalled(); expect(value.discover).not.toHaveBeenCalled();
});

it("focuses account validation and the exact later invalid weight in the displayed source", async () => {
  const value = fixture([SystemCapability.AGENT_WORKER_WIZARD_V1, SystemCapability.AGENT_WORKER_SOURCE_ROUTES_V1]);
  await start(value); confirmHarness();
  await chooseScrollOption(sourceChoice("Account source 1"), `api:${value.provider.id}`);
  const personal = await screen.findByRole("checkbox", { name: /Personal API/ });
  next(); expect(globalThis.document.activeElement).toBe(personal);
  fireEvent.click(personal);
  fireEvent.click(screen.getByRole("button", { name: /^Load more.*[Aa]ccount/ }));
  fireEvent.click(await screen.findByRole("checkbox", { name: /Team API/ }));
  await screen.findByRole("checkbox", { name: "Select Team API" });
  expect(screen.getByRole("heading", { name: "Routing options" })).toBeTruthy();
  const later = screen.getByLabelText("Weight for account 2");
  fireEvent.change(later, { target: { value: "1001" } });
  await waitFor(() => expect((later as HTMLInputElement).validity.valid).toBe(false));
  next(); expect(globalThis.document.activeElement).toBe(later);
  expect(value.save).not.toHaveBeenCalled(); expect(value.discover).not.toHaveBeenCalled();
});


it("retains selected legacy account status after its independent read fails", async () => {
  const value = fixture(); await start(value); confirmHarness();
  await chooseScrollOption(sourceChoice("Account source"), `api:${value.provider.id}`);
  fireEvent.click(await screen.findByRole("checkbox", { name: /Personal API/ }));
  await waitFor(() => expect(value.get.mock.calls.some(([request]) => request.id === value.accounts[0].id)).toBe(true));
  const selected = screen.getByRole("checkbox", { name: /Personal API/ }).closest("label")!;
  expect(selected.textContent).toContain("Connected · Health: unverified");
  expect(selected.textContent).toContain("Execution eligibility: Checked when execution starts");
  value.get.mockImplementation(request => { if (request.id === value.accounts[0].id) throw new ConnectError("Unavailable", Code.Unavailable); return { resource: value.records.find(row => row.id === request.id) }; });
  await act(async () => { await value.client.invalidateQueries({ refetchType: "active" }); });
  await waitFor(() => expect(screen.getByRole("checkbox", { name: /Personal API/ }).closest("label")!.textContent).toContain("Connected · Health: unverified"));
  expect(value.save).not.toHaveBeenCalled(); expect(value.discover).not.toHaveBeenCalled();
});


it("focuses legacy account choices when a valid source has no selected account", async () => {
  const value = fixture(); await start(value); confirmHarness();
  await chooseScrollOption(sourceChoice("Account source"), `api:${value.provider.id}`);
  const account = await screen.findByRole("checkbox", { name: /Personal API/ });
  next();
  expect(globalThis.document.activeElement).toBe(account);
  expect(screen.getByRole("combobox", { name: "Account source" })).toBeTruthy();
  expect(screen.getByRole("heading", { name: "Accounts", level: 3 })).toBeTruthy();
  expect(value.save).not.toHaveBeenCalled();
});


it.each([false, true])("keeps resolved edit source visible through deferred proofs (reflow=%s)", async reflow => {
  const value = fixture([SystemCapability.AGENT_WORKER_WIZARD_V1, SystemCapability.AGENT_WORKER_SOURCE_ROUTES_V1]);
  let release!: () => void;
  const pending = new Promise<void>(resolve => { release = resolve; });
  const transport: Transport = { ...value.transport, async unary(...args) { if (args[0].name === "GetResource") await pending; return value.transport.unary(...args); } };
  await start(value, true, transport); confirmHarness();
  expect(screen.queryByRole("button", { name: "Change source" })).toBeNull();
  expect(screen.getByRole("combobox", { name: "Account source 1" })).toBeTruthy();
  if (reflow) fireEvent(window, new Event("resize"));
  await act(async () => release());
  await screen.findByRole("checkbox", { name: "Select Personal API" });
  expect(screen.getByRole("combobox", { name: "Account source 1" })).toBeTruthy();
  expect(value.save).not.toHaveBeenCalled(); expect(value.discover).not.toHaveBeenCalled();
});


it("waits for independent account proof before the legacy fixture advances", async () => {
  const value = fixture();
  let release!: () => void;
  const pending = new Promise<void>(resolve => { release = resolve; });
  const transport: Transport = { ...value.transport, async unary(...args) { if (args[0].name === "GetResource" && (args[4] as { id?: string }).id === value.accounts[0].id) await pending; return value.transport.unary(...args); } };
  await start(value, false, transport); confirmHarness();
  await chooseScrollOption(sourceChoice("Account source"), `api:${value.provider.id}`);
  fireEvent.click(await screen.findByRole("checkbox", { name: /Personal API/ }));
  await screen.findByText("Loading selected account…");
  const advancing = nextAfterAccountRead();
  expect(screen.queryByRole("combobox", { name: "Model" })).toBeNull();
  await act(async () => release());
  await advancing;
  expect(screen.getByRole("combobox", { name: "Model" })).toBeTruthy();
  expect(value.save).not.toHaveBeenCalled(); expect(value.discover).not.toHaveBeenCalled();
});

it("scrolls the exact highlighted model option through payload wrappers", async () => {
  const value = fixture();
  await start(value); await accounts(value);
  const input = screen.getByRole("combobox", { name: "Model" });
  fireEvent.focus(input);
  const first = await screen.findByRole("option", { name: /Example A/ });
  const second = screen.getByRole("option", { name: /Example B/ });
  expect(first.parentElement?.getAttribute("role")).not.toBe("listbox");
  const region = first.closest<HTMLElement>(".worker-model-results")!;
  region.getBoundingClientRect = () => ({ top: 100, bottom: 380 } as DOMRect);
  first.getBoundingClientRect = () => ({ top: 390, bottom: 450 } as DOMRect);
  second.getBoundingClientRect = () => ({ top: 460, bottom: 520 } as DOMRect);
  fireEvent.keyDown(input, { key: "ArrowDown" });
  expect(input.getAttribute("aria-activedescendant")).toBe(first.id);
  expect(region.scrollTop).toBe(70);
  fireEvent.keyDown(input, { key: "ArrowDown" });
  expect(input.getAttribute("aria-activedescendant")).toBe(second.id);
  expect(region.scrollTop).toBe(210);
  expect(second.getAttribute("aria-selected")).toBe("true");
  expect(value.save).not.toHaveBeenCalled(); expect(value.discover).not.toHaveBeenCalled();
});

it("translates retained routed account status after its independent read fails", async () => {
  const value = fixture([SystemCapability.AGENT_WORKER_WIZARD_V1, SystemCapability.AGENT_WORKER_SOURCE_ROUTES_V1]);
  await start(value); confirmHarness();
  await chooseScrollOption(sourceChoice("Account source 1"), "subscription:chatgpt");
  fireEvent.click(await screen.findByRole("checkbox", { name: /ChatGPT account/ }));
  await screen.findByRole("checkbox", { name: "Select ChatGPT account" });
  await waitFor(() => expect(screen.getByRole("checkbox", { name: "Select ChatGPT account" }).closest("label")!.textContent).toContain("Connected · Quota unknown"));
  value.get.mockImplementation(request => { if (request.id === value.subscription.id) throw new ConnectError("Unavailable", Code.Unavailable); return { resource: value.records.find(row => row.id === request.id) }; });
  await act(async () => { await value.client.invalidateQueries({ refetchType: "active" }); });
  await screen.findByRole("alert");
  const reads = value.get.mock.calls.length;
  await act(() => i18n.changeLanguage("ko"));
  const selected = screen.getByRole("checkbox", { name: /ChatGPT account/ }).closest("label")!;
  expect(selected.textContent).toContain("연결됨 · 할당량 알 수 없음");
  expect(selected.textContent).not.toContain("Connected");
  expect(value.get).toHaveBeenCalledTimes(reads); expect(value.save).not.toHaveBeenCalled(); expect(value.discover).not.toHaveBeenCalled();
  await act(() => i18n.changeLanguage("en"));
});


it("seeds routed account display metadata without replacing failed independent proof", async () => {
  const value = fixture([SystemCapability.AGENT_WORKER_WIZARD_V1, SystemCapability.AGENT_WORKER_SOURCE_ROUTES_V1]);
  value.get.mockImplementation(request => { if (request.id === value.subscription.id) throw new ConnectError("Unavailable", Code.Unavailable); return { resource: value.records.find(row => row.id === request.id) }; });
  await start(value); confirmHarness();
  await chooseScrollOption(sourceChoice("Account source 1"), "subscription:chatgpt");
  fireEvent.click(await screen.findByRole("checkbox", { name: /ChatGPT account/ }));
  await screen.findByRole("alert");
  const selected = screen.getByRole("checkbox", { name: "Select ChatGPT account" });
  expect(selected).toHaveProperty("checked", true);
  expect(selected.closest("label")!.textContent).toContain("ChatGPT accountConnected · Quota unknown");
  next();
  expect(screen.queryByRole("combobox", { name: "Model for ChatGPT subscription" })).toBeNull();
  expect(screen.getByRole("heading", { name: "Accounts", level: 3 })).toBeTruthy();
  expect(value.save).not.toHaveBeenCalled(); expect(value.discover).not.toHaveBeenCalled();
});


it.each([false, true])("shows initial selected-provider failure beside the visible source picker (routes=%s)", async routes => {
  const value = fixture([SystemCapability.AGENT_WORKER_WIZARD_V1, ...(routes ? [SystemCapability.AGENT_WORKER_SOURCE_ROUTES_V1] : [])]);
  let unavailable = true;
  value.get.mockImplementation(request => { if (unavailable && request.id === value.provider.id) throw new ConnectError("Unavailable", Code.Unavailable); return { resource: value.records.find(row => row.id === request.id) }; });
  await start(value); confirmHarness();
  await chooseScrollOption(sourceChoice(routes ? "Account source 1" : "Account source"), `api:${value.provider.id}`);
  await screen.findAllByRole("alert");
  const retry = screen.getByRole("button", { name: "Retry source details" });
  expect(screen.queryByRole("button", { name: "Change source" })).toBeNull();
  expect(screen.getByRole("combobox", { name: routes ? "Account source 1" : "Account source" })).toBeTruthy();
  const providerReads = value.get.mock.calls.filter(([request]) => request.id === value.provider.id).length;
  unavailable = false; fireEvent.click(retry);
  await screen.findByRole("checkbox", { name: /Personal API/ });
  expect(value.get.mock.calls.filter(([request]) => request.id === value.provider.id)).toHaveLength(providerReads + 1);
  expect(screen.queryByRole("button", { name: "Retry source details" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Change source" })).toBeNull();
  expect(value.save).not.toHaveBeenCalled(); expect(value.discover).not.toHaveBeenCalled();
});

it.each([false, true])("retains account choice and weight while retrying selected-provider details (routes=%s)", async routes => {
  const value = fixture([SystemCapability.AGENT_WORKER_WIZARD_V1, ...(routes ? [SystemCapability.AGENT_WORKER_SOURCE_ROUTES_V1] : [])]);
  await start(value); confirmHarness();
  await chooseScrollOption(sourceChoice(routes ? "Account source 1" : "Account source"), `api:${value.provider.id}`);
  fireEvent.click(await screen.findByRole("checkbox", { name: /Personal API/ }));
  await waitFor(() => expect(screen.queryByText("Loading selected account…")).toBeNull());
  fireEvent.click(screen.getByText(/^Routing options/));
  const weight = screen.getByLabelText("Weight for account 1");
  fireEvent.change(weight, { target: { value: "9" } });
  let unavailable = true;
  value.get.mockImplementation(request => { if (unavailable && request.id === value.provider.id) throw new ConnectError("Unavailable", Code.Unavailable); return { resource: value.records.find(row => row.id === request.id) }; });
  await act(async () => { await value.client.invalidateQueries({ refetchType: "active" }); });
  const retry = await screen.findByRole("button", { name: "Retry source details" });
  expect(screen.getByRole("checkbox", { name: /Personal API/ })).toHaveProperty("checked", true);
  expect(weight).toHaveProperty("value", "9");
  unavailable = false; fireEvent.click(retry);
  await waitFor(() => expect(screen.queryByRole("button", { name: "Retry source details" })).toBeNull());
  expect(screen.getByRole("checkbox", { name: /Personal API/ })).toHaveProperty("checked", true);
  expect(screen.getByLabelText("Weight for account 1")).toBe(weight);
  expect(weight).toHaveProperty("value", "9");
  expect(value.save).not.toHaveBeenCalled(); expect(value.discover).not.toHaveBeenCalled();
});

it.each([false, true])("opens the selected provider's account task only for complete empty inventory (ordered=%s)", async ordered => {
 const value = fixture([SystemCapability.AGENT_WORKER_WIZARD_V1, ...(ordered ? [SystemCapability.AGENT_WORKER_SOURCE_ROUTES_V1] : [])]);
 await start(value); confirmHarness(); await chooseScrollOption(sourceChoice(ordered ? "Account source 1" : "Account source"), `api:${value.otherProvider.id}`);
 const link = await screen.findByRole("link", { name: "Add an account in AI API Keys" });
 expect(screen.getByRole("heading", { name: "Routing options", level: 4 })).toBeTruthy();
 expect(screen.queryByRole("button", { name: "Change source" })).toBeNull(); expect(screen.queryByRole("button", { name: "Refresh accounts" })).toBeNull();
 fireEvent.click(link); await screen.findByRole("heading", { name: "AI API Keys", level: 1 });
 expect(screen.queryByRole("heading", { name: "Accounts", level: 3 })).toBeNull(); expect(value.save).not.toHaveBeenCalled(); expect(value.discover).not.toHaveBeenCalled();
 expect((await screen.findAllByText("Other API", { exact: true })).length).toBeGreaterThan(0);
 await screen.findByRole("textbox", { name: "Entry name" });
});
it.each([false, true])("navigates empty subscription inventory and disposes the wizard (ordered=%s)", async ordered => {
 const value = fixture([SystemCapability.AGENT_WORKER_WIZARD_V1, ...(ordered ? [SystemCapability.AGENT_WORKER_SOURCE_ROUTES_V1] : [])]);
 const original = value.list.getMockImplementation()!; value.list.mockImplementation(request => request.subscriptionService === SubscriptionServiceIdentity.CHATGPT ? { resources: [], nextPageToken: "" } : original(request));
 await start(value); confirmHarness(); await chooseScrollOption(sourceChoice(ordered ? "Account source 1" : "Account source"), "subscription:chatgpt");
 fireEvent.click(await screen.findByRole("link", { name: "Add an account in AI Subscription" })); await screen.findByRole("heading", { name: "AI Subscription", level: 1 });
 expect(screen.queryByRole("heading", { name: "Accounts", level: 3 })).toBeNull(); expect(value.save).not.toHaveBeenCalled(); expect(value.discover).not.toHaveBeenCalled();
 fireEvent.click(screen.getByRole("button", { name: "Agent Workers" })); fireEvent.click(screen.getByRole("button", { name: "New Agent Worker" }));
 expect(screen.getByRole("heading", { name: "Harness", level: 3 })).toBeTruthy();
});
it.each([false, true])("does not turn an incomplete empty account page into add-account authority (ordered=%s)", async ordered => {
 const value = fixture([SystemCapability.AGENT_WORKER_WIZARD_V1, ...(ordered ? [SystemCapability.AGENT_WORKER_SOURCE_ROUTES_V1] : [])]);
 const original = value.list.getMockImplementation()!; value.list.mockImplementation(request => request.providerId === value.otherProvider.id ? { resources: [], nextPageToken: "unread-tail" } : original(request));
 await start(value); confirmHarness(); await chooseScrollOption(sourceChoice(ordered ? "Account source 1" : "Account source"), `api:${value.otherProvider.id}`);
 await waitFor(() => expect(value.list.mock.calls.some(([request]) => request.providerId === value.otherProvider.id)).toBe(true)); await act(async () => {}); expect(screen.queryByRole("link", { name: "Add an account in AI API Keys" })).toBeNull();
});

it.each([false, true])("retains one model-result region through pending, empty, failed and closed queries (routes=%s)", async routes => {
  const value = fixture([SystemCapability.AGENT_WORKER_WIZARD_V1, ...(routes ? [SystemCapability.AGENT_WORKER_SOURCE_ROUTES_V1] : [])]);
  await start(value);
  if (routes) {
    confirmHarness(); await chooseScrollOption(sourceChoice("Account source 1"), `api:${value.provider.id}`);
    fireEvent.click(await screen.findByRole("checkbox", { name: /Personal API/ }));
    await nextAfterSourceProof(value, [value.accounts[0]]);
  } else await accounts(value);
  const input = screen.getByRole("combobox", { name: routes ? "Model for OpenAI API" : "Model" }) as HTMLInputElement;
  input.focus(); await screen.findByRole("option", { name: /Example A/ });
  const region = input.parentElement!.querySelector<HTMLElement>(".worker-model-results")!;
  const reload = screen.getByRole("button", { name: "Reload models" });
  expect(region.contains(reload)).toBe(false);
  let resolve!: (response: Awaited<ReturnType<typeof value.search>>) => void;
  value.search.mockImplementation(async request => request.query === "pending-original" ? await new Promise(done => { resolve = done; }) : { models: [], providers: [], nextPageToken: "" });
  fireEvent.change(input, { target: { value: "pending-original" } });
  await waitFor(() => expect(resolve).toBeTypeOf("function"));
  expect(screen.getByText("Loading model catalog…").closest(".worker-model-results")).toBe(region);
  expect(globalThis.document.activeElement).toBe(input);
  await act(async () => resolve({ models: [], providers: [], nextPageToken: "" }));
  await waitFor(() => expect(within(region).getByText("No saved models match. Enter an exact model ID.")).toBeTruthy());
  fireEvent.keyDown(input, { key: "Escape" });
  expect(input.getAttribute("aria-expanded")).toBe("false"); expect(region.isConnected).toBe(true);
  input.focus(); fireEvent.focus(input);
  fireEvent.blur(input, { relatedTarget: region }); region.focus();
  expect(input.getAttribute("aria-expanded")).toBe("true");
  fireEvent.keyDown(region, { key: "Escape" });
  expect(globalThis.document.activeElement).toBe(input); expect(input.getAttribute("aria-expanded")).toBe("false");
  value.search.mockRejectedValue(new ConnectError("Unavailable", Code.Unavailable));
  input.focus(); fireEvent.change(input, { target: { value: "failed-original" } });
  await waitFor(() => expect(within(region).getByText(/Catalog lookup failed/)).toBeTruthy());
  const reads = value.search.mock.calls.length;
  value.search.mockResolvedValue({ models: [], providers: [], nextPageToken: "" });
  fireEvent.click(reload); await waitFor(() => expect(value.search.mock.calls.length).toBeGreaterThan(reads));
  expect(value.search.mock.calls.at(-1)?.[0].query).toBe("failed-original");
  fireEvent.focus(input); fireEvent.keyDown(input, { key: "ArrowUp" }); fireEvent.keyDown(input, { key: "Enter" });
  expect(input.value).toBe("failed-original"); expect(input.getAttribute("aria-expanded")).toBe("false");
  expect(input.parentElement!.querySelector(".worker-model-results")).toBe(region);
  expect(value.discover).not.toHaveBeenCalled(); expect(value.save).not.toHaveBeenCalled();
});

it.each([false, true])("saves explicit Fast mode for a connected ChatGPT subscription Worker (routes=%s)", async routes => {
 const value = fixture([SystemCapability.AGENT_WORKER_WIZARD_V1, SystemCapability.KNOWN_SUBSCRIPTION_MODELS_V1, ...(routes ? [SystemCapability.AGENT_WORKER_SOURCE_ROUTES_V1] : [])]);
 let model: HTMLElement;
 if (routes) { await start(value); confirmHarness(); await chooseScrollOption(sourceChoice("Account source 1"), "subscription:chatgpt"); fireEvent.click(await screen.findByRole("checkbox", { name: /ChatGPT account/ })); await screen.findByRole("checkbox", { name: "Select ChatGPT account" }); await nextAfterSourceProof(value, [value.subscription]); model = await screen.findByRole("combobox", { name: "Model for ChatGPT subscription" }); }
 else model = await subscriptionModels(value);
 fireEvent.change(model,{target:{value:"exact-native-model"}});next();
 await screen.findByRole("heading",{name:"Configure",level:3});
 fireEvent.change(screen.getByRole("textbox",{name:"Name"}),{target:{value:"Fast subscription Worker"}});
 fireEvent.click(screen.getByText("Native harness options"));
 fireEvent.change(screen.getByRole("combobox",{name:"Service tier"}),{target:{value:"fast"}});
 fireEvent.click(screen.getByRole("button",{name:"Save Agent Worker"}));await waitFor(()=>expect(value.save).toHaveBeenCalledOnce());
 const saved = JSON.parse(new TextDecoder().decode(value.save.mock.calls[0][0].documentJson));
 expect(saved.options.service_tier).toBe("fast");expect(saved.harness).toBe("codex");expect(value.save.mock.calls[0][0].model?.selection).toEqual({ case: "nativeId", value: "exact-native-model" });
});
