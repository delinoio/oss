import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport, type Transport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { AccountService, ConfigurationService, EntityKind, ProviderInventoryCapability, ProviderInventoryEntrySchema, ProviderPresetId, ProviderService, ResourceSchema, ResourceService, WorkerService, newRequestId, type ListResourcesRequest, type ProviderInventoryEntry, type Resource } from "@delinoio/delidev-api-client";
import { Settings, ConfigurationEditor } from "./settings";
import { AccountConnection } from "./account-connection";
import { ConfigurationDeletion, RoutingPreview } from "./configuration-actions";
import { MutationIntents } from "./mutation";
import { encode, type Document } from "./documents";

function resource(kind: EntityKind, value: Document, revision = 1n) { return create(ResourceSchema, { id: newRequestId(), kind, schemaVersion: 1, revision, documentJson: encode(value) }); }
function fixture(resources: Resource[], options: { providerEntries?: ProviderInventoryEntry[]; presets?: unknown[]; providerInventoryError?: ConnectError; readResources?: (kind: EntityKind, pageToken: string) => { resources: Resource[]; nextPageToken?: string } | Promise<{ resources: Resource[]; nextPageToken?: string }>;  readProviderInventory?: (pageToken: string, request: { query: string; enabledOnly: boolean; pageSize: number }) => { entries: ProviderInventoryEntry[]; capabilities: ProviderInventoryCapability[]; nextPageToken?: string }; readModelSearch?: (pageToken: string) => { models: Resource[]; providers: Resource[]; nextPageToken?: string } } = {}) {
  const save = vi.fn(async (_request: unknown): Promise<{ resource?: Resource; job?: Resource }> => ({ resource: resources[0] }));
  const remove = vi.fn(async (_request: unknown) => ({}));
  const preview = vi.fn(async (_request: unknown) => ({ routeJson: encode({ policy: "remaining-quota", selected: "", candidates: [] }) }));
  const inspect = vi.fn(async (_request: unknown) => ({ job: resources.find((row) => row.kind === EntityKind.JOB) }));
  const connect = vi.fn(async (_request: unknown) => ({ account: resources.find((row) => row.kind === EntityKind.ACCOUNT) }));
  const disconnect = vi.fn(async (_request: unknown) => ({ account: resources.find((row) => row.kind === EntityKind.ACCOUNT) }));
  const list = vi.fn((request: ListResourcesRequest) => options.readResources?.(request.filter?.kind ?? EntityKind.UNSPECIFIED, request.filter?.pageToken ?? "") ?? ({ resources: resources.filter((row) => row.kind === request.filter?.kind) }));
  const transport = createRouterTransport((router) => {
    router.service(ConfigurationService, { saveConfiguration: save, deleteConfiguration: remove, previewRouting: preview });
    router.service(WorkerService, { inspectRepository: inspect });
    router.service(ResourceService, { listResources: list, getResource: (request) => ({ resource: resources.find((row) => row.id === request.id) }) });
    router.service(AccountService, { getAccountStatus: (request) => ({ account: resources.find((row) => row.id === request.id) }), connectAccount: connect, disconnectAccount: disconnect });
    router.service(ProviderService, {
      listProviderPresets: () => ({ presetsJson: encode(options.presets ?? [{ id: "ollama", provider: { name: "Local provider", endpoint: "http://127.0.0.1:11434/v1", protocol: "openai-chat", authentication: "keyless", discovery: true }, key_guidance: "Run your local model server first.", compatibility: "Requires a compatible model." }]) }),
      listProviderInventory: (request) => {
        if (options.providerInventoryError) throw options.providerInventoryError;
        return options.readProviderInventory?.(request.pageToken, request) ?? ({ entries: options.providerEntries ?? [{ presetId: ProviderPresetId.OLLAMA, displayName: "Local provider", enabled: false, totalAccounts: 0n, connectedAccounts: 0n, accountCountsAvailable: true }], capabilities: [ProviderInventoryCapability.PROVIDER_ACTIVATION, ProviderInventoryCapability.ACTIVE_API_MODEL_FILTER, ProviderInventoryCapability.ACCOUNT_PROVIDER_FILTER, ...(options.providerEntries ? [ProviderInventoryCapability.ACCOUNT_TYPE_FILTER] : [])] });
      },
      searchModels: (request) => options.readModelSearch?.(request.pageToken) ?? ({ models: [], providers: [] }),
    });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false, gcTime: 0 } } });
  const view = (children: React.ReactNode) => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents>{children}</MutationIntents></QueryClientProvider></TransportProvider>;
  return { resources, save, remove, preview, inspect, connect, disconnect, client, transport, list, view };
}
function input(value: unknown) { return value as { mutation: { requestId: string; expectedRevision: bigint }; documentJson: Uint8Array }; }

it("renders the Agent-only empty inventory after the first read succeeds", async () => {
  let resolve!: (result: { resources: Resource[] }) => void;
  const pending = new Promise<{ resources: Resource[] }>((done) => { resolve = done; });
  const value = fixture([], { readResources: (kind) => kind === EntityKind.AGENT ? pending : { resources: [] } });
  render(value.view(<Settings close={() => {}} />));
  fireEvent.click(screen.getByRole("button", { name: "Agent Workers" }));
  expect(screen.getByRole("status").textContent).toBe("Loading agent workers…");
  expect(screen.queryByRole("region", { name: "No agent workers yet" })).toBeNull();
  resolve({ resources: [] });
  const panel = await screen.findByRole("region", { name: "No agent workers yet" });
  expect(screen.getAllByRole("heading", { level: 1, name: "Agent Workers" })).toHaveLength(1);
  expect(screen.getByText("Reusable configurations for your agents.")).toBeTruthy();
  expect(screen.getByText("Saved on the selected server.")).toBeTruthy();
  expect(within(panel).getByRole("heading", { name: "No agent workers yet" })).toBeTruthy();
  expect(within(panel).getByText("Define a harness, model, accounts, and instructions, then reuse them in new sessions.")).toBeTruthy();
  expect(panel.querySelector("svg")?.getAttribute("aria-hidden")).toBe("true");
  expect(within(panel).queryByRole("button")).toBeNull();
  expect(screen.getAllByRole("button", { name: "New Agent Worker" })).toHaveLength(1);
  expect(screen.queryByRole("navigation", { name: "Settings pages" })).toBeNull();
  expect(value.save).not.toHaveBeenCalled(); expect(value.remove).not.toHaveBeenCalled(); expect(value.preview).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Instructions" }));
  expect(screen.getByRole("heading", { level: 1, name: "Instructions" }).closest(".settings-agent-column")).toBeNull();
  expect(screen.queryByText("Reusable configurations for your agents.")).toBeNull();
});

it.each([Code.PermissionDenied, Code.Unavailable])("keeps an initial Agent read failure %s distinct from empty inventory", async (code) => {
  const value = fixture([], { readResources: (kind) => { if (kind === EntityKind.AGENT) throw new ConnectError("Synthetic read failure", code); return { resources: [] }; } });
  render(value.view(<Settings close={() => {}} />));
  fireEvent.click(screen.getByRole("button", { name: "Agent Workers" }));
  expect(await screen.findByRole("alert")).toBeTruthy();
  expect(screen.queryByRole("region", { name: "No agent workers yet" })).toBeNull();
  expect(screen.queryByText("Refresh failed. Showing the last successfully loaded results.")).toBeNull();
  expect(screen.queryByText("No agent workers on this page.")).toBeNull();
});

it.each([true, false])("preserves Agent opaque pages when the first page is empty: %s", async (firstEmpty) => {
  const agent = resource(EntityKind.AGENT, { name: "Paged Agent" });
  const value = fixture([agent], { readResources: (kind, token) => kind === EntityKind.AGENT ? { resources: token || firstEmpty ? [] : [agent], nextPageToken: token ? "" : "agent-page-2" } : { resources: [] } });
  render(value.view(<Settings close={() => {}} />));
  fireEvent.click(screen.getByRole("button", { name: "Agent Workers" }));
  if (firstEmpty) await screen.findByText("No agent workers on this page.");
  else await screen.findByRole("heading", { name: "Paged Agent" });
  expect(screen.queryByRole("region", { name: "No agent workers yet" })).toBeNull();
  const first = screen.getByRole("button", { name: "First page" }) as HTMLButtonElement;
  expect(first.disabled).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "Next page" }));
  await screen.findByText("No agent workers on this page.");
  await waitFor(() => expect(first.disabled).toBe(false));
  expect((screen.getByRole("button", { name: "Next page" }) as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(first);
  if (!firstEmpty) await screen.findByRole("heading", { name: "Paged Agent" });
  await waitFor(() => expect(value.list.mock.calls.filter(([request]) => request.filter?.kind === EntityKind.AGENT)).toHaveLength(3));
  expect(value.list.mock.calls.filter(([request]) => request.filter?.kind === EntityKind.AGENT).map(([request]) => request.filter)).toEqual([
    expect.objectContaining({ kind: EntityKind.AGENT, pageSize: 50, pageToken: "" }),
    expect.objectContaining({ kind: EntityKind.AGENT, pageSize: 50, pageToken: "agent-page-2" }),
    expect.objectContaining({ kind: EntityKind.AGENT, pageSize: 50, pageToken: "" }),
  ]);
  expect(value.save).not.toHaveBeenCalled();
});

it.each([true, false])("retains cached Agent results through a failed refresh, empty: %s", async (empty) => {
  const agent = resource(EntityKind.AGENT, { name: "Cached Agent" });
  let reject!: (error: ConnectError) => void, refresh = false;
  const pending = new Promise<{ resources: Resource[] }>((_resolve, fail) => { reject = fail; });
  const value = fixture([agent], { readResources: (kind) => kind === EntityKind.AGENT ? refresh ? pending : { resources: empty ? [] : [agent] } : { resources: [] } });
  render(value.view(<Settings close={() => {}} />));
  fireEvent.click(screen.getByRole("button", { name: "Agent Workers" }));
  if (empty) await screen.findByRole("region", { name: "No agent workers yet" });
  else await screen.findByRole("heading", { name: "Cached Agent" });
  refresh = true;
  fireEvent.click(screen.getByRole("button", { name: "Refresh settings" }));
  await waitFor(() => expect(value.list.mock.calls.filter(([request]) => request.filter?.kind === EntityKind.AGENT)).toHaveLength(2));
  const cachedRow = empty ? undefined : screen.getByRole("heading", { name: "Cached Agent" });
  expect(screen.queryByText("Loading agent workers…")).toBeNull();
  reject(new ConnectError("Refresh unavailable", Code.Unavailable));
  await screen.findByRole("alert");
  expect(screen.getByRole("status").textContent).toBe("Refresh failed. Showing the last successfully loaded results.");
  expect(screen.queryByRole("region", { name: "No agent workers yet" })).toBeNull();
  if (cachedRow) expect(screen.getByRole("heading", { name: "Cached Agent" })).toBe(cachedRow);
  expect(value.save).not.toHaveBeenCalled(); expect(value.remove).not.toHaveBeenCalled();
});

it("keeps Agent row content inert and actions scoped to exact supported configurations", async () => {
  const name = `<b>${"LongAgent".repeat(40)}</b>`;
  const agent = resource(EntityKind.AGENT, { name, harness: "codex", health: "saved" });
  const future = create(ResourceSchema, { ...resource(EntityKind.AGENT, { name: "Future Agent" }), schemaVersion: 2 });
  const value = fixture([agent, future]);
  render(value.view(<Settings close={() => {}} />));
  fireEvent.click(screen.getByRole("button", { name: "Agent Workers" }));
  const heading = await screen.findByRole("heading", { name });
  const panel = heading.closest(".settings-agent-list")!;
  expect(panel.querySelectorAll(".settings-agent-row")).toHaveLength(2);
  const row = within(heading.closest("article")!);
  expect(row.getByText(agent.id)).toBeTruthy();
  expect(row.getByText("Harness: codex")).toBeTruthy(); expect(row.getByText("Status: saved")).toBeTruthy();
  expect(heading.querySelector("b")).toBeNull();
  expect(row.getAllByRole("button").map((button) => button.textContent)).toEqual(["Edit", "Preview routing", "Delete"]);
  const futureRow = within(screen.getByRole("heading", { name: "Future Agent" }).closest("article")!);
  expect(futureRow.getByText(future.id)).toBeTruthy();
  expect(futureRow.queryByText(/Harness:|Status:/)).toBeNull();
  for (const button of futureRow.getAllByRole("button")) { expect((button as HTMLButtonElement).disabled).toBe(true); fireEvent.click(button); }
  expect(value.save).not.toHaveBeenCalled(); expect(value.remove).not.toHaveBeenCalled(); expect(value.preview).not.toHaveBeenCalled();
  fireEvent.click(row.getByRole("button", { name: `Preview routing for ${name}` }));
  await waitFor(() => expect(value.preview).toHaveBeenCalledWith(expect.objectContaining({ agentId: agent.id }), expect.anything()));
  fireEvent.click(screen.getByRole("button", { name: "Back to Agent Workers" }));
  fireEvent.click(screen.getByRole("button", { name: `Edit ${name}` }));
  expect((screen.getByRole("textbox", { name: "Name" }) as HTMLInputElement).value).toBe(name);
  expect(screen.queryByRole("button", { name: "New Agent Worker" })).toBeNull();
  expect((screen.getByRole("combobox", { name: "Settings category" }) as HTMLSelectElement).disabled).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "Cancel edit" }));
  fireEvent.click(screen.getByRole("button", { name: `Delete ${name}` }));
  expect(screen.getByText("Schedules using this configuration will be disabled for future runs. Already accepted sessions are retained.")).toBeTruthy();
  expect(value.remove).not.toHaveBeenCalled();
});

it.each([
  { field: "name", projected: "a".repeat(256), valid: true },
  { field: "name", projected: "a".repeat(257), valid: false },
  { field: "alias", projected: "😀".repeat(64), valid: true },
  { field: "alias", projected: "😀".repeat(65), valid: false },
  { field: "name", projected: "a".repeat(512 << 10), valid: false },
  { field: "alias", projected: "a".repeat(512 << 10), valid: false },
] as const)("bounds an unsupported Agent's $field display text before duplicating labels, valid: $valid", async ({ field, projected, valid }) => {
  const future = create(ResourceSchema, { ...resource(EntityKind.AGENT, { [field]: projected }), schemaVersion: 2 });
  const value = fixture([future]);
  render(value.view(<Settings close={() => {}} />));
  fireEvent.click(screen.getByRole("button", { name: "Agent Workers" }));
  const name = valid ? projected : "Unnamed";
  const heading = await screen.findByRole("heading", { name });
  expect(heading.textContent).toBe(name);
  const row = within(heading.closest("article")!);
  expect(row.getByText(future.id)).toBeTruthy();
  for (const label of [`Edit ${name}`, `Preview routing for ${name}`, `Delete ${name}`]) {
    expect((row.getByRole("button", { name: label }) as HTMLButtonElement).disabled).toBe(true);
  }
  expect(value.save).not.toHaveBeenCalled(); expect(value.remove).not.toHaveBeenCalled(); expect(value.preview).not.toHaveBeenCalled();
});

it("keeps the original Agent deletion revision and retry request within its opening", async () => {
  const agent = resource(EntityKind.AGENT, { name: "Retained Agent" }, 7n), value = fixture([agent]);
  value.remove.mockRejectedValueOnce(new ConnectError("Acknowledgment lost", Code.Unavailable));
  render(value.view(<Settings close={() => {}} />));
  fireEvent.click(screen.getByRole("button", { name: "Agent Workers" }));
  fireEvent.click(await screen.findByRole("button", { name: "Delete Retained Agent" }));
  expect(value.remove).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Confirm configuration deletion" }));
  fireEvent.click(await screen.findByRole("button", { name: "Retry the same deletion" }));
  await waitFor(() => expect(value.remove).toHaveBeenCalledTimes(2));
  expect(value.remove.mock.calls[1][0]).toEqual(value.remove.mock.calls[0][0]);
  expect(value.remove.mock.calls[0][0]).toMatchObject({ kind: EntityKind.AGENT, mutation: { id: agent.id, expectedRevision: 7n } });
});

it("retains an Agent draft at its captured revision when a peer changes the entry", async () => {
  const model = resource(EntityKind.MODEL, { name: "Fixture Model" });
  const agent = resource(EntityKind.AGENT, { name: "Original Agent", harness: "codex", model_id: model.id, accounts: [], templates: [], options: { permission: "default" } }, 3n);
  const value = fixture([agent, model]);
  render(value.view(<Settings close={() => {}} />));
  fireEvent.click(screen.getByRole("button", { name: "Agent Workers" }));
  await screen.findByRole("button", { name: "Edit Original Agent" });
  value.resources[0] = create(ResourceSchema, { ...agent, revision: 4n });
  fireEvent.click(screen.getByRole("button", { name: "Edit Original Agent" }));
  fireEvent.change(screen.getByRole("textbox", { name: "Name" }), { target: { value: "Retained draft" } });
  expect(await screen.findByText("This entry changed elsewhere. Your draft is retained. Cancel this edit and reopen the latest entry before saving.")).toBeTruthy();
  expect((screen.getByRole("textbox", { name: "Name" }) as HTMLInputElement).value).toBe("Retained draft");
  expect((screen.getByRole("button", { name: "Save Agent Worker" }) as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "Save Agent Worker" }));
  expect(value.save).not.toHaveBeenCalled();
});

it("retains exact Agent save bytes and navigation locks through reflow and reconnect", async () => {
  const provider = resource(EntityKind.PROVIDER, { name: "Fixture Provider", enabled: true });
  const model = resource(EntityKind.MODEL, { name: "Fixture Model", provider_id: provider.id });
  const agent = resource(EntityKind.AGENT, { name: "Original Agent", harness: "codex", model_id: model.id, accounts: [], templates: [], options: { permission: "default" } }, 3n);
  const value = fixture([agent, model, provider], { readModelSearch: () => ({ models: [model], providers: [provider] }) });
  value.save.mockRejectedValueOnce(new ConnectError("Acknowledgment unavailable", Code.Unavailable));
  const view = render(value.view(<Settings close={() => {}} />));
  fireEvent.click(screen.getByRole("button", { name: "Agent Workers" }));
  fireEvent.click(await screen.findByRole("button", { name: "Edit Original Agent" }));
  const name = screen.getByRole("textbox", { name: "Name" });
  fireEvent.change(name, { target: { value: "Retained draft" } });
  await screen.findByRole("option", { name: "Fixture Model" });
  fireEvent.change(screen.getByRole("combobox", { name: "Model" }), { target: { value: model.id } });
  fireEvent.click(screen.getByRole("button", { name: "Save Agent Worker" }));
  await screen.findByRole("button", { name: "Retry the same configuration" });
  expect((screen.getByRole("combobox", { name: "Settings category" }) as HTMLSelectElement).disabled).toBe(true);
  expect((screen.getByRole("button", { name: "Instructions" }) as HTMLButtonElement).disabled).toBe(true);
  const replacementSave = vi.fn();
  const replacement: Transport = { ...value.transport, unary: (...args) => { if (args[0].name === "SaveConfiguration") replacementSave(); return value.transport.unary(...args); } };
  view.rerender(<TransportProvider transport={replacement}><QueryClientProvider client={value.client}><MutationIntents><Settings close={() => {}} /></MutationIntents></QueryClientProvider></TransportProvider>);
  fireEvent(window, new Event("resize"));
  expect(screen.getByRole("textbox", { name: "Name" })).toBe(name);
  expect((name as HTMLInputElement).value).toBe("Retained draft");
  expect(value.save).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole("button", { name: "Retry the same configuration" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(2));
  expect(replacementSave).toHaveBeenCalledTimes(1);
  expect(value.save.mock.calls[1][0]).toEqual(value.save.mock.calls[0][0]);
  expect(input(value.save.mock.calls[0][0]).mutation).toMatchObject({ id: agent.id, expectedRevision: 3n });
  expect(JSON.parse(new TextDecoder().decode(input(value.save.mock.calls[0][0]).documentJson))).toEqual({ name: "Retained draft", harness: "codex", model_id: model.id, accounts: [], templates: [], options: { permission: "default" } });
});

it("pages native subscription providers independently of active API providers", async () => {
  const subscriptions = Array.from({ length: 51 }, (_, index) => resource(EntityKind.PROVIDER, { name: `Subscription ${String(index + 1).padStart(2, "0")}`, protocol: "native-subscription", authentication: "subscription" }));
  const capabilities = [ProviderInventoryCapability.PROVIDER_ACTIVATION, ProviderInventoryCapability.ACTIVE_API_MODEL_FILTER, ProviderInventoryCapability.ACCOUNT_PROVIDER_FILTER];
  const value = fixture(subscriptions, {
    readResources: (_kind, pageToken) => pageToken ? { resources: [subscriptions[50]] } : { resources: subscriptions.slice(0, 50), nextPageToken: "subscription-page-2" },
    readProviderInventory: (pageToken) => ({ entries: [], capabilities, ...(pageToken ? {} : { nextPageToken: "active-api-page-2" }) }),
  });
  render(value.view(<ConfigurationEditor kind={EntityKind.ACCOUNT} active saved={() => {}} cancel={() => {}} />));
  await screen.findByRole("option", { name: "Subscription 50" });
  expect(screen.queryByRole("option", { name: "Subscription 51" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "More subscription providers" }));
  await screen.findByRole("option", { name: "Subscription 51" });
  expect((screen.getByRole("button", { name: "More API providers" }) as HTMLButtonElement).disabled).toBe(false);
});

it("keeps model-search cursors out of provider inventory requests", async () => {
  const provider = resource(EntityKind.PROVIDER, { name: "API provider", enabled: true });
  const firstModel = resource(EntityKind.MODEL, { name: "First model", provider_id: provider.id });
  const secondModel = resource(EntityKind.MODEL, { name: "Second model", provider_id: provider.id });
  const providerEntry = create(ProviderInventoryEntrySchema, { presetId: ProviderPresetId.UNSPECIFIED, providerId: provider.id, displayName: "API provider", enabled: true, totalAccounts: 0n, connectedAccounts: 0n, accountCountsAvailable: true, provider });
  const inventoryTokens: string[] = [];
  const searchTokens: string[] = [];
  const capabilities = [ProviderInventoryCapability.PROVIDER_ACTIVATION, ProviderInventoryCapability.ACTIVE_API_MODEL_FILTER, ProviderInventoryCapability.ACCOUNT_PROVIDER_FILTER];
  const value = fixture([provider], {
    providerEntries: [providerEntry],
    readProviderInventory: (pageToken) => {
      inventoryTokens.push(pageToken);
      if (pageToken) throw new ConnectError("Provider inventory received another query's cursor", Code.InvalidArgument);
      return { entries: [providerEntry], capabilities };
    },
    readModelSearch: (pageToken) => {
      searchTokens.push(pageToken);
      return pageToken ? { models: [secondModel], providers: [provider] } : { models: [firstModel], providers: [provider], nextPageToken: "model-page-2" };
    },
  });
  render(value.view(<ConfigurationEditor kind={EntityKind.AGENT} active saved={() => {}} cancel={() => {}} />));
  await screen.findByRole("option", { name: "First model" });
  fireEvent.click(screen.getByRole("button", { name: "More choices" }));
  await screen.findByRole("option", { name: "Second model" });
  expect(searchTokens).toEqual(["", "model-page-2"]);
  expect(inventoryTokens.every((pageToken) => pageToken === "")).toBe(true);
});

it("shows the complete grouped navigation once and keeps its selected category in sync", async () => {
  const value = fixture([]);
  render(value.view(<Settings visible close={() => {}} />));
  const navigation = screen.getByRole("navigation", { name: "Settings categories" });
  const labels = ["AI Subscription", "AI API Keys", "API Providers", "Models", "Agent Workers", "Instructions", "Projects", "Repositories", "Runner Devices", "Paired devices", "Server preferences", "Integrations", "Connection & diagnostics", "Notifications", "Import / Export", "Backups"];
  const values = ["subscription-accounts", "api-accounts", "providers", "models", "agent-workers", "instructions", "projects", "repositories", "execution-workers", "paired-devices", "server-preferences", "integrations", "diagnostics", "notifications", "transfer", "backups"];
  expect(Array.from(navigation.querySelectorAll(".settings-nav-group h2"), (heading) => heading.textContent)).toEqual(["AI & agents", "Workspace", "System"]);
  expect(within(navigation).getAllByRole("button").map((button) => button.textContent?.trim().replace(/\s+/g, " "))).toEqual(labels);
  const categorySelect = screen.getByRole("combobox", { name: "Settings category" }) as HTMLSelectElement;
  expect(categorySelect.options).toHaveLength(16);
  expect(Array.from(categorySelect.options, (option) => option.textContent)).toEqual(labels);
  expect(Array.from(categorySelect.options, (option) => option.value)).toEqual(values);
  expect(Array.from(categorySelect.querySelectorAll("optgroup"), (group) => group.label)).toEqual(["AI & agents", "Workspace", "System"]);
  expect(categorySelect.value).toBe("subscription-accounts");
  for (const [index, label] of labels.entries()) {
    const button = within(navigation).getByRole("button", { name: label });
    fireEvent.click(button);
    expect(button.getAttribute("aria-current")).toBe("page");
    expect(screen.getByRole("heading", { level: 1, name: label })).toBeTruthy();
    expect(categorySelect.value).toBe(values[index]);
  }
  expect(categorySelect.value).toBe("backups");
  fireEvent.change(categorySelect, { target: { value: "subscription-accounts" } });
  expect(categorySelect.selectedOptions[0].textContent).toBe("AI Subscription");
  expect(categorySelect.value).toBe("subscription-accounts");
  expect(screen.getByRole("heading", { level: 1, name: "AI Subscription" })).toBeTruthy();
  expect(within(navigation).getByRole("button", { name: "AI Subscription" }).getAttribute("aria-current")).toBe("page");
});

it("keeps API provider accounts optional when none are connected", async () => {
  const provider = resource(EntityKind.PROVIDER, { name: "OpenAI", endpoint: "https://api.openai.com/v1", protocol: "openai-responses", authentication: "bearer", discovery: true, enabled: true, preset_id: "openai" });
  const value = fixture([], { providerEntries: [create(ProviderInventoryEntrySchema, { presetId: ProviderPresetId.OPENAI, providerId: provider.id, displayName: "OpenAI", enabled: true, totalAccounts: 0n, connectedAccounts: 0n, provider, accountCountsAvailable: true })] });
  render(value.view(<Settings visible close={() => {}} />));
  fireEvent.click(screen.getByRole("button", { name: "API Providers" }));
  expect(await screen.findByText(/Entries: 0 connected · 0 total/)).toBeTruthy();
  expect(screen.queryByText("Account required")).toBeNull();
  expect(screen.queryByRole("alert")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Add AI API key" }));
  expect(await screen.findByRole("heading", { name: "Connect your entry" })).toBeTruthy();
});

it("keeps provider-row entry fields disabled when the server lacks account-type filtering", async () => {
  const provider = resource(EntityKind.PROVIDER, { name: "OpenAI", endpoint: "https://api.openai.com/v1", protocol: "openai-responses", authentication: "bearer", enabled: true, preset_id: "openai" });
  const entry = create(ProviderInventoryEntrySchema, { presetId: ProviderPresetId.OPENAI, providerId: provider.id, displayName: "OpenAI", enabled: true, provider, accountCountsAvailable: true });
  const capabilities = [ProviderInventoryCapability.PROVIDER_ACTIVATION, ProviderInventoryCapability.ACTIVE_API_MODEL_FILTER, ProviderInventoryCapability.ACCOUNT_PROVIDER_FILTER];
  const value = fixture([provider], { readProviderInventory: () => ({ entries: [entry], capabilities }) });
  render(value.view(<Settings visible close={() => {}} />));
  fireEvent.click(screen.getByRole("button", { name: "API Providers" }));
  fireEvent.click(await screen.findByRole("button", { name: "Add AI API key" }));
  await screen.findByRole("heading", { name: "Connect your entry" });
  expect(screen.getByLabelText("Entry name").matches(":disabled")).toBe(true);
  expect(screen.getByLabelText("API key").matches(":disabled")).toBe(true);
  expect((screen.getByRole("button", { name: "Add and connect" }) as HTMLButtonElement).disabled).toBe(true);
  expect(screen.getByText(/server no longer reports/)).toBeTruthy();
  expect(value.save).not.toHaveBeenCalled();
  expect(value.connect).not.toHaveBeenCalled();
});

it("still reports a real provider inventory read failure", async () => {
  const value = fixture([], { providerInventoryError: new ConnectError("Inventory unavailable", Code.Unavailable) });
  render(value.view(<Settings visible close={() => {}} />));
  fireEvent.click(screen.getByRole("button", { name: "API Providers" }));
  expect(await screen.findByRole("alert")).toBeTruthy();
});

it("retains the exact first-activation retry after inventory reveals the saved preset", async () => {
  const provider = resource(EntityKind.PROVIDER, { name: "Local provider", endpoint: "http://127.0.0.1:11434/v1", protocol: "openai-chat", authentication: "keyless", discovery: true, enabled: true, preset_id: "ollama" });
  let entry = create(ProviderInventoryEntrySchema, { presetId: ProviderPresetId.OLLAMA, displayName: "Local provider", enabled: false, accountCountsAvailable: true });
  const capabilities = [ProviderInventoryCapability.PROVIDER_ACTIVATION, ProviderInventoryCapability.ACTIVE_API_MODEL_FILTER, ProviderInventoryCapability.ACCOUNT_PROVIDER_FILTER];
  const value = fixture([provider], { readProviderInventory: () => ({ entries: [entry], capabilities }) });
  value.save.mockImplementationOnce(async () => {
    entry = create(ProviderInventoryEntrySchema, { ...entry, providerId: provider.id, provider, enabled: true });
    throw new ConnectError("Activation acknowledgment lost", Code.Unavailable);
  });
  render(value.view(<Settings visible close={() => {}} />));
  fireEvent.click(screen.getByRole("button", { name: "API Providers" }));
  const originalSwitch = await screen.findByRole("switch", { name: "Turn on Local provider" });
  fireEvent.click(originalSwitch);
  const savedSwitch = await screen.findByRole("switch", { name: "Turn off Local provider" });
  expect(savedSwitch).toBe(originalSwitch);
  expect((savedSwitch as HTMLButtonElement).disabled).toBe(true);
  expect(value.save).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole("button", { name: "Models" }));
  fireEvent.click(screen.getByRole("button", { name: "API Providers" }));
  fireEvent.click(await screen.findByRole("button", { name: "Retry the same change" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(2));
  expect(value.save.mock.calls[1][0]).toEqual(value.save.mock.calls[0][0]);
  expect(input(value.save.mock.calls[1][0]).mutation).toMatchObject({ id: "", expectedRevision: 0n });
  await waitFor(() => expect((screen.getByRole("switch", { name: "Turn off Local provider" }) as HTMLButtonElement).disabled).toBe(false));
  expect(screen.queryByRole("button", { name: "Retry the same change" })).toBeNull();
});

it("uses server-owned preset key guidance and inert documentation in the API account wizard", async () => {
  const provider = resource(EntityKind.PROVIDER, { name: "OpenAI", endpoint: "https://api.openai.com/v1", protocol: "openai-responses", authentication: "bearer", discovery: true, enabled: true, preset_id: "openai" });
  const value = fixture([], {
    providerEntries: [create(ProviderInventoryEntrySchema, { presetId: ProviderPresetId.OPENAI, providerId: provider.id, displayName: "OpenAI", enabled: true, totalAccounts: 0n, connectedAccounts: 0n, provider, accountCountsAvailable: true })],
    presets: [{ id: "openai", provider: { name: "OpenAI", endpoint: "https://api.openai.com/v1", protocol: "openai-responses", authentication: "bearer", discovery: true }, key_guidance: "Create a project key for the selected workspace.", documentation: "https://developers.openai.com/api/reference/overview" }],
  });
  render(value.view(<Settings visible close={() => {}} />));
  fireEvent.click(await screen.findByRole("button", { name: "AI API Keys" }));
  fireEvent.click(await screen.findByRole("button", { name: "Add AI API key" }));
  fireEvent.click(await screen.findByRole("button", { name: "OpenAI API key" }));
  fireEvent.click(screen.getByText("Where to get an API key"));
  expect(screen.getByText("Create a project key for the selected workspace.")).toBeTruthy();
  expect(screen.getByText("https://developers.openai.com/api/reference/overview")).toBeTruthy();
  expect(screen.queryByRole("link", { name: "https://developers.openai.com/api/reference/overview" })).toBeNull();
});

it("discards account filters, later pages, wizard input and configuration deletion confirmations on close", async () => {
  const provider = resource(EntityKind.PROVIDER, { name: "OpenAI", endpoint: "https://api.openai.com/v1", protocol: "openai-responses", authentication: "bearer", discovery: true, enabled: true });
  const instructions = resource(EntityKind.TEMPLATE, { name: "Saved instructions", contents: "Server contents" });
  const entry = create(ProviderInventoryEntrySchema, { providerId: provider.id, displayName: "OpenAI", enabled: true, provider, accountCountsAvailable: true });
  const tokens: string[] = [];
  const value = fixture([provider, instructions], { providerEntries: [entry], readResources: (kind, token) => {
    if (kind === EntityKind.ACCOUNT) { tokens.push(token); return { resources: [], nextPageToken: token ? "" : "account-page-2" }; }
    return { resources: kind === EntityKind.TEMPLATE ? [instructions] : [] };
  } });
  const view = render(value.view(<Settings close={() => {}} />));
  fireEvent.click(screen.getByRole("button", { name: "AI API Keys" }));
  const search = await screen.findByRole("searchbox", { name: "Search providers" });
  fireEvent.change(search, { target: { value: "OpenAI" } });
  fireEvent.change(await screen.findByRole("combobox", { name: "Filter entries by provider" }), { target: { value: provider.id } });
  const next = screen.getByRole("button", { name: "Next page" });
  await waitFor(() => expect((next as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(next);
  await waitFor(() => expect(tokens).toContain("account-page-2"));
  fireEvent.click(screen.getByRole("button", { name: "Add AI API key" }));
  fireEvent.click(await screen.findByRole("button", { name: "OpenAI API key" }));
  fireEvent.change(screen.getByRole("textbox", { name: "Entry name" }), { target: { value: "Abandoned account" } });
  fireEvent.change(screen.getByLabelText("API key"), { target: { value: "fixture-transient-key" } });
  view.rerender(value.view(<Settings visible={false} close={() => {}} />));
  view.rerender(value.view(<Settings close={() => {}} />));
  fireEvent.click(screen.getByRole("button", { name: "AI API Keys" }));
  expect((await screen.findByRole("searchbox", { name: "Search providers" }) as HTMLInputElement).value).toBe("");
  expect((screen.getByRole("combobox", { name: "Filter entries by provider" }) as HTMLSelectElement).value).toBe("");
  await waitFor(() => expect(tokens.at(-1)).toBe(""));
  expect(screen.queryByRole("textbox", { name: "Entry name" })).toBeNull();
  expect(screen.queryByLabelText("API key")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Instructions" }));
  fireEvent.click(await screen.findByRole("button", { name: "Delete Saved instructions" }));
  expect(screen.getByRole("button", { name: "Confirm configuration deletion" })).toBeTruthy();
  view.rerender(value.view(<Settings visible={false} close={() => {}} />));
  view.rerender(value.view(<Settings close={() => {}} />));
  fireEvent.click(screen.getByRole("button", { name: "Instructions" }));
  expect(await screen.findByRole("button", { name: "Delete Saved instructions" })).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Confirm configuration deletion" })).toBeNull();
  expect(value.save).not.toHaveBeenCalled();
  expect(value.connect).not.toHaveBeenCalled();
  expect(value.remove).not.toHaveBeenCalled();
});

it("keeps exact retries within an opening and discards its provider draft on close", async () => {
  const value = fixture([]);
  value.save.mockRejectedValueOnce(new ConnectError("acknowledgement lost", Code.Unavailable));
  const view = render(value.view(<Settings visible close={() => {}} />));
  fireEvent.click(screen.getByRole("button", { name: "API Providers" }));
  const create = await screen.findByRole("button", { name: "Custom provider" });
  await waitFor(() => expect((create as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(create);
  fireEvent.change(screen.getByRole("textbox", { name: "Name" }), { target: { value: "My local provider" } });
  fireEvent.change(screen.getByRole("textbox", { name: "API base URL" }), { target: { value: "http://127.0.0.1:11434/v1" } });
  expect((screen.getByRole("textbox", { name: "Name" }) as HTMLInputElement).value).toBe("My local provider");
  fireEvent.click(screen.getByRole("button", { name: "Save Provider" }));
  fireEvent.click(await screen.findByRole("button", { name: "Retry the same configuration" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(2));
  expect(value.save.mock.calls[0][0]).toEqual(value.save.mock.calls[1][0]);
  const request = input(value.save.mock.calls[0][0]);
  expect(request.mutation.expectedRevision).toBe(0n);
  expect(JSON.parse(new TextDecoder().decode(request.documentJson))).toEqual({ name: "My local provider", endpoint: "http://127.0.0.1:11434/v1", protocol: "openai-responses", authentication: "bearer", discovery: true, enabled: true });
  view.rerender(value.view(<Settings visible={false} close={() => {}} />));
  view.rerender(value.view(<Settings visible close={() => {}} />));
  expect((screen.getByRole("combobox", { name: "Settings category" }) as HTMLSelectElement).value).toBe("subscription-accounts");
  expect(screen.queryByRole("textbox", { name: "Name" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Retry the same configuration" })).toBeNull();
  expect(value.save).toHaveBeenCalledTimes(2);
});

it("preserves server-owned account observations during a preference edit", async () => {
  const provider = resource(EntityKind.PROVIDER, { name: "Provider" });
  const observed = { alias: "Original", provider_id: provider.id, type: "api", enabled: true, exclude_automatic: false, recovery_notifications: true, health: "ready", connection: { id: newRequestId(), authentication: "bearer", connected_at: "2026-09-25T00:00:00Z" }, quota: [{ id: "window", remaining: 0 }], confirmed_exhausted: true, validation: { state: "observed" }, catalog: { state: "stale" } };
  const account = resource(EntityKind.ACCOUNT, observed, 7n);
  const value = fixture([account, provider]);
  render(value.view(<ConfigurationEditor kind={EntityKind.ACCOUNT} initial={account} active saved={() => {}} cancel={() => {}} />));
  fireEvent.change(screen.getByRole("textbox", { name: "Entry name" }), { target: { value: "Renamed" } });
  fireEvent.click(screen.getByRole("button", { name: "Save AI API key entry" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  const request = input(value.save.mock.calls[0][0]);
  expect(request.mutation.expectedRevision).toBe(7n);
  expect(JSON.parse(new TextDecoder().decode(request.documentJson))).toEqual({ ...observed, alias: "Renamed" });
});

it("blocks stale settings writes without erasing the staged instructions", async () => {
  const initial = resource(EntityKind.TEMPLATE, { name: "Instructions", contents: "Original instructions" }, 3n);
  const value = fixture([create(ResourceSchema, { ...initial, revision: 4n })]);
  render(value.view(<ConfigurationEditor kind={EntityKind.TEMPLATE} initial={initial} active saved={() => {}} cancel={() => {}} />));
  fireEvent.change(screen.getByRole("textbox", { name: "Instructions" }), { target: { value: "My staged instructions" } });
  await screen.findByText(/This entry changed elsewhere/);
  fireEvent.submit(screen.getByRole("button", { name: "Save Instructions" }).closest("form")!);
  expect(value.save).not.toHaveBeenCalled();
  expect((screen.getByRole("textbox", { name: "Instructions" }) as HTMLTextAreaElement).value).toBe("My staged instructions");
});

it("retains a secret only for its exact uncertain connection and excludes it from read cache keys", async () => {
  const provider = resource(EntityKind.PROVIDER, { name: "API provider", authentication: "bearer" });
  const account = resource(EntityKind.ACCOUNT, { alias: "API account", provider_id: provider.id, type: "api", health: "disconnected" }, 5n);
  const value = fixture([account, provider]);
  value.connect.mockRejectedValueOnce(new ConnectError("response lost", Code.Unavailable));
  render(value.view(<AccountConnection initial={account} active close={() => {}} />));
  const key = screen.getByLabelText("API key");
  await waitFor(() => expect((key as HTMLInputElement).disabled).toBe(false));
  fireEvent.change(key, { target: { value: "fixture-only-secret" } });
  fireEvent.click(screen.getByRole("button", { name: "Connect API key" }));
  const retry = await screen.findByRole("button", { name: "Retry the same connection" });
  expect((key as HTMLInputElement).value).toBe("");
  expect(JSON.stringify(value.client.getQueryCache().getAll().map((query) => query.queryKey))).not.toContain("fixture-only-secret");
  fireEvent.click(retry);
  await waitFor(() => expect(value.connect).toHaveBeenCalledTimes(2));
  expect(value.connect.mock.calls[0][0]).toEqual(value.connect.mock.calls[1][0]);
  expect(value.connect.mock.calls[0][0]).toMatchObject({ mutation: { expectedRevision: 5n }, keyless: false });
  expect(new TextDecoder().decode((value.connect.mock.calls[0][0] as { apiKey: Uint8Array }).apiKey)).toBe("fixture-only-secret");
  await waitFor(() => expect(value.client.getMutationCache().getAll()).toHaveLength(0));
});

it("resumes pending credential cleanup using the original server-retained mutation", async () => {
  const provider = resource(EntityKind.PROVIDER, { name: "Provider", authentication: "bearer" });
  const requestId = newRequestId();
  const account = resource(EntityKind.ACCOUNT, { alias: "Cleanup", provider_id: provider.id, type: "api", health: "disconnected", removal: { request_id: requestId, expected_revision: 8 } }, 12n);
  const value = fixture([account, provider]);
  render(value.view(<AccountConnection initial={account} active close={() => {}} />));
  fireEvent.click(screen.getByRole("button", { name: "Retry original credential cleanup" }));
  await waitFor(() => expect(value.disconnect).toHaveBeenCalledTimes(1));
  expect(value.disconnect.mock.calls[0][0]).toMatchObject({ mutation: { id: account.id, requestId, expectedRevision: 8n } });
  expect(value.connect).not.toHaveBeenCalled();
});

it("preserves explicit empty restrictions and requires a primary repository after removal", async () => {
  const first = resource(EntityKind.REPOSITORY, { name: "First" }), second = resource(EntityKind.REPOSITORY, { name: "Second" });
  const project = resource(EntityKind.PROJECT, { name: "Project", repositories: [first.id, second.id], primary_repository: first.id, agents: { configured: false, ids: [] }, accounts: { configured: false, ids: [] } });
  const value = fixture([project, first, second]);
  render(value.view(<ConfigurationEditor kind={EntityKind.PROJECT} initial={project} active saved={() => {}} cancel={() => {}} />));
  fireEvent.click(screen.getByRole("checkbox", { name: "Restrict ai accounts" }));
  fireEvent.click(screen.getByRole("button", { name: "Remove entry 1" }));
  expect((screen.getByRole("combobox", { name: "Primary repository" }) as HTMLSelectElement).value).toBe("");
  fireEvent.change(screen.getByRole("combobox", { name: "Primary repository" }), { target: { value: second.id } });
  fireEvent.click(screen.getByRole("button", { name: "Save Project" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  expect(JSON.parse(new TextDecoder().decode(input(value.save.mock.calls[0][0]).documentJson))).toMatchObject({ repositories: [second.id], primary_repository: second.id, accounts: { configured: true, ids: [] }, agents: { configured: false, ids: [] } });
});

it("keeps repository save acknowledgment separate from completed Worker validation", async () => {
  const repository = resource(EntityKind.REPOSITORY, { name: "Repository", checkouts: [{ machine_id: newRequestId(), path: "/owned/checkout" }], base: {}, starting: {}, auto_fetch: true });
  const job = resource(EntityKind.JOB, { type: "save-repository", state: "queued" });
  const value = fixture([repository, job]), saved = vi.fn();
  value.save.mockResolvedValue({ job });
  render(value.view(<ConfigurationEditor kind={EntityKind.REPOSITORY} initial={repository} active saved={saved} cancel={() => {}} />));
  fireEvent.click(screen.getByRole("button", { name: "Save Repository" }));
  await screen.findByText("Worker operation: queued");
  expect(saved).not.toHaveBeenCalled();
  expect(screen.queryByRole("button", { name: "Done" })).toBeNull();
  await waitFor(() => expect((screen.getByRole("button", { name: "Refresh operation" }) as HTMLButtonElement).disabled).toBe(false));
  value.resources[1] = create(ResourceSchema, { ...job, revision: 2n, documentJson: encode({ type: "save-repository", state: "uncertain", problem: { message: "Owned operation needs recovery" } }) });
  fireEvent.click(screen.getByRole("button", { name: "Refresh operation" }));
  await screen.findByText("Worker operation: uncertain");
  expect(value.save).toHaveBeenCalledTimes(1);
  expect(screen.queryByRole("button", { name: "Return to retained draft" })).toBeNull();
  await waitFor(() => expect((screen.getByRole("button", { name: "Refresh operation" }) as HTMLButtonElement).disabled).toBe(false));
  value.resources[1] = create(ResourceSchema, { ...job, revision: 3n, documentJson: encode({ type: "save-repository", state: "succeeded", output: { id: repository.id, revision: 2 } }) });
  fireEvent.click(screen.getByRole("button", { name: "Refresh operation" }));
  fireEvent.click(await screen.findByRole("button", { name: "Done" }));
  expect(saved).toHaveBeenCalledTimes(1);
});

it("retries an original checkout inspection and uses only its owning Worker's canonical root", async () => {
  const machine = resource(EntityKind.MACHINE, { name: "Owned Worker" });
  const job = resource(EntityKind.JOB, { type: "inspect-repository", state: "succeeded", machine_id: machine.id, output: { root: "/canonical/checkout", remotes: ["origin"], default_refs: { origin: "main" } } });
  const value = fixture([machine, job]);
  value.inspect.mockRejectedValueOnce(new ConnectError("ack lost", Code.Unavailable));
  render(value.view(<ConfigurationEditor kind={EntityKind.REPOSITORY} active saved={() => {}} cancel={() => {}} />));
  fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Repository" } });
  await screen.findByRole("option", { name: "Owned Worker" });
  fireEvent.change(screen.getByLabelText("Runner Device"), { target: { value: machine.id } });
  fireEvent.change(screen.getByLabelText("Absolute checkout path on this Worker"), { target: { value: "/alias/checkout" } });
  fireEvent.click(screen.getByRole("button", { name: "Inspect checkout" }));
  const retry = await screen.findByRole("button", { name: "Retry the same inspection" });
  expect((screen.getByRole("button", { name: "Cancel edit" }) as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(retry);
  fireEvent.click(await screen.findByRole("button", { name: "Add inspected checkout" }));
  fireEvent.click(screen.getByRole("button", { name: "Save Repository" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  expect(value.inspect.mock.calls[0][0]).toEqual(value.inspect.mock.calls[1][0]);
  expect(JSON.parse(new TextDecoder().decode(input(value.save.mock.calls[0][0]).documentJson))).toMatchObject({ checkouts: [{ machine_id: machine.id, path: "/canonical/checkout" }], auto_fetch: true });
});

it("retains the original revision and identity when a configuration deletion acknowledgment is lost", async () => {
  const project = resource(EntityKind.PROJECT, { name: "Retained history" }, 7n), value = fixture([project]);
  value.remove.mockRejectedValueOnce(new ConnectError("ack lost", Code.Unavailable));
  render(value.view(<ConfigurationDeletion initial={project} deleted={() => {}} close={() => {}} />));
  fireEvent.click(screen.getByRole("button", { name: "Confirm configuration deletion" }));
  fireEvent.click(await screen.findByRole("button", { name: "Retry the same deletion" }));
  await waitFor(() => expect(value.remove).toHaveBeenCalledTimes(2));
  expect(value.remove.mock.calls[0][0]).toEqual(value.remove.mock.calls[1][0]);
  expect(value.remove.mock.calls[0][0]).toMatchObject({ mutation: { id: project.id, expectedRevision: 7n } });
});

it("renders unknown quota and server candidate reasons without performing selection mutations", async () => {
  const agent = resource(EntityKind.AGENT, { name: "Agent" }), project = resource(EntityKind.PROJECT, { name: "Restricted project" }), value = fixture([agent, project]);
  value.preview.mockResolvedValue({ routeJson: encode({ policy: "remaining-quota", fallback: true, candidates: [{ id: newRequestId(), weight: 1, eligibility: "project-restricted", quota_state: "unknown" }] }) });
  render(value.view(<RoutingPreview agent={agent} active close={() => {}} />));
  await screen.findByText(/Quota: unknown/);
  fireEvent.change(screen.getByLabelText("Project"), { target: { value: project.id } });
  await waitFor(() => expect(value.preview).toHaveBeenLastCalledWith(expect.objectContaining({ agentId: agent.id, projectId: project.id }), expect.anything()));
  expect(screen.getByText(/None eligible/)).toBeTruthy();
  expect(value.save).not.toHaveBeenCalled(); expect(value.connect).not.toHaveBeenCalled();
});

it("edits global routing and fetch preferences without rewriting unrelated policy or creating another singleton", async () => {
  const original = { default_routing: "sequential-exhaustion", automatic_fetch: true, notifications: false, remediation: { ci_failure: true, review_feedback: false, merge_conflict: true, conflict_strategy: "rebase", session_strategy: "dedicated", attempt_limit: 9, agent_id: newRequestId(), machine_id: newRequestId() } };
  const preferences = resource(EntityKind.SETTINGS, original, 8n);
  const value = fixture([preferences]);
  value.save.mockRejectedValueOnce(new ConnectError("lost response", Code.Unavailable));
  render(value.view(<Settings close={() => {}} />));
  fireEvent.click(screen.getByRole("button", { name: "Server preferences" }));
  fireEvent.click(await screen.findByRole("button", { name: "Edit Server preferences" }));
  expect(screen.queryByRole("button", { name: "New Server preferences" })).toBeNull();
  expect(screen.queryByRole("button", { name: /Delete Server preferences/ })).toBeNull();
  fireEvent.change(screen.getByLabelText("Default account routing"), { target: { value: "priority" } });
  fireEvent.click(screen.getByRole("checkbox", { name: "Allow automatic fetch before Worktree preparation" }));
  fireEvent.click(screen.getByRole("button", { name: "Save Server preferences" }));
  fireEvent.click(await screen.findByRole("button", { name: "Retry the same configuration" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(2));
  expect(value.save.mock.calls[0][0]).toEqual(value.save.mock.calls[1][0]);
  const request = input(value.save.mock.calls[0][0]);
  expect(request.mutation.expectedRevision).toBe(8n);
  expect(JSON.parse(new TextDecoder().decode(request.documentJson))).toEqual({ ...original, default_routing: "priority", automatic_fetch: false });
});

it("retains incompatible permission selections across harness changes until explicit clearing", async () => {
  const model = resource(EntityKind.MODEL, { name: "Fixture model" });
  const original = { name: "Original agent", harness: "codex", model_id: model.id, accounts: [], templates: [], options: { permission: "workspace-write", approval_policy: "on-request", future_option: "retained" } };
  const agent = resource(EntityKind.AGENT, original, 3n);
  const value = fixture([agent, model]);
  render(value.view(<ConfigurationEditor kind={EntityKind.AGENT} initial={agent} active saved={() => {}} cancel={() => {}} />));
  fireEvent.change(screen.getByRole("combobox", { name: "Harness" }), { target: { value: "claude-code" } });
  expect(screen.getByRole("alert").textContent).toContain("workspace-write · on-request");
  expect(screen.queryByRole("combobox", { name: "Permission mode" })).toBeNull();
  expect(screen.queryByRole("textbox", { name: "Approval policy" })).toBeNull();
  fireEvent.change(screen.getByRole("combobox", { name: "Claude permission mode" }), { target: { value: "acceptEdits" } });
  fireEvent.change(screen.getByRole("combobox", { name: "Harness" }), { target: { value: "codex" } });
  expect((screen.getByRole("combobox", { name: "Permission mode" }) as HTMLSelectElement).value).toBe("workspace-write");
  expect((screen.getByRole("textbox", { name: "Approval policy" }) as HTMLInputElement).value).toBe("on-request");
  expect(screen.getByRole("alert").textContent).toContain("acceptEdits");
  fireEvent.change(screen.getByRole("combobox", { name: "Harness" }), { target: { value: "claude-code" } });
  fireEvent.click(screen.getByRole("button", { name: "Clear incompatible permission settings" }));
  fireEvent.click(screen.getByRole("button", { name: "Save Agent Worker" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  const request = input(value.save.mock.calls[0][0]);
  expect(request.mutation.expectedRevision).toBe(3n);
  expect(JSON.parse(new TextDecoder().decode(request.documentJson))).toEqual({ ...original, harness: "claude-code", options: { permission: "default", claude_permission: "acceptEdits", future_option: "retained" } });
});

it("shows unsupported stored Claude modes without replacing the retained draft", async () => {
  const model = resource(EntityKind.MODEL, { name: "Fixture model" });
  const agent = resource(EntityKind.AGENT, { name: "Future agent", harness: "claude-code", model_id: model.id, options: { permission: "default", claude_permission: "future-mode" } });
  const value = fixture([agent, model]);
  render(value.view(<ConfigurationEditor kind={EntityKind.AGENT} initial={agent} active saved={() => {}} cancel={() => {}} />));
  const selector = screen.getByRole("combobox", { name: "Claude permission mode" }) as HTMLSelectElement;
  expect(selector.value).toBe("future-mode");
  expect(screen.getByRole("option", { name: "Unsupported selection · future-mode" })).toBeTruthy();
  fireEvent.change(selector, { target: { value: "bypassPermissions" } });
  expect(screen.getByText(/Bypass skips native permission prompts/)).toBeTruthy();
  expect(value.save).not.toHaveBeenCalled();
});

it("saves remediation switches, exact reviewer IDs and explicit execution choices through configuration", async () => {
  const agent = resource(EntityKind.AGENT, { name: "Fix agent" });
  const machine = resource(EntityKind.MACHINE, { name: "Fix machine" });
  const value = fixture([agent, machine]);
  render(value.view(<ConfigurationEditor kind={EntityKind.SETTINGS} active saved={() => {}} cancel={() => {}} />));
  expect(screen.getByText(/Automatic execution is not available yet/)).toBeTruthy();
  for (const name of ["Automatically fix required CI failures", "Automatically handle matching published feedback", "Automatically resolve verified merge conflicts"]) {
    expect((screen.getByRole("checkbox", { name }) as HTMLInputElement).checked).toBe(false);
    fireEvent.click(screen.getByRole("checkbox", { name }));
  }
  screen.getByText("Remediation details").closest("details")!.open = true;
  await screen.findByRole("option", { name: "Fix agent" });
  fireEvent.change(screen.getByLabelText("Remediation Agent Worker"), { target: { value: agent.id } });
  fireEvent.change(screen.getByLabelText("Remediation Runner Device"), { target: { value: machine.id } });
  fireEvent.change(screen.getByLabelText("Remediation session strategy"), { target: { value: "dedicated" } });
  fireEvent.change(screen.getByLabelText("Conflict resolution strategy"), { target: { value: "rebase" } });
  fireEvent.change(screen.getByLabelText("Consecutive automatic attempt limit"), { target: { value: "7" } });
  fireEvent.click(screen.getByRole("button", { name: "Add reviewer selector" }));
  fireEvent.change(screen.getByLabelText("Selector 1 type"), { target: { value: "bot" } });
  fireEvent.change(screen.getByLabelText("Selector 1 GitHub numeric ID"), { target: { value: "9007199254740993" } });
  fireEvent.change(screen.getByLabelText("Selector 1 GitHub node ID"), { target: { value: "BOT_exact" } });
  fireEvent.click(screen.getByRole("button", { name: "Add reviewer selector" }));
  fireEvent.change(screen.getByLabelText("Selector 2 type"), { target: { value: "minimum-permission" } });
  fireEvent.change(screen.getByLabelText("Selector 2 minimum permission"), { target: { value: "MAINTAIN" } });
  fireEvent.click(screen.getByRole("button", { name: "Save Server preferences" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  const saved = JSON.parse(new TextDecoder().decode(input(value.save.mock.calls[0][0]).documentJson));
  expect(saved.remediation).toEqual({ ci_failure: true, review_feedback: true, merge_conflict: true, conflict_strategy: "rebase", session_strategy: "dedicated", attempt_limit: 7, agent_id: agent.id, machine_id: machine.id, reviewer_selectors: [{ kind: "bot", id: "9007199254740993", node_id: "BOT_exact" }, { kind: "minimum-permission", permission: "MAINTAIN" }] });
});

it("starts a repository override with automation off and removes it only through explicit inheritance", async () => {
  const repository = resource(EntityKind.REPOSITORY, { name: "Repository", checkouts: [], base: {}, starting: {}, auto_fetch: true });
  const value = fixture([repository]);
  render(value.view(<ConfigurationEditor kind={EntityKind.REPOSITORY} initial={repository} active saved={() => {}} cancel={() => {}} />));
  expect(screen.getByText(/inherits the complete server remediation policy/)).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Set repository policy with automation off" }));
  expect((screen.getByLabelText("Automatically fix required CI failures") as HTMLInputElement).checked).toBe(false);
  expect((screen.getByLabelText("Consecutive automatic attempt limit") as HTMLInputElement).value).toBe("3");
  expect(screen.queryByText("Remediation details")).toBeNull();
  fireEvent.click(screen.getByLabelText("Automatically fix required CI failures"));
  fireEvent.click(screen.getByRole("button", { name: "Use server remediation policy" }));
  expect(screen.queryByLabelText("Automatically fix required CI failures")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Save Repository" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  const saved = JSON.parse(new TextDecoder().decode(input(value.save.mock.calls[0][0]).documentJson));
  expect(saved).toEqual({ name: "Repository", checkouts: [], base: {}, starting: {}, auto_fetch: true });
});

it("retains empty repository selectors and clears incompatible identity fields when changing selector type", async () => {
  const policy = { ci_failure: false, review_feedback: true, merge_conflict: false, conflict_strategy: "merge", session_strategy: "reuse", attempt_limit: 3, reviewer_selectors: [{ kind: "app", id: "42", node_id: "A_exact" }] };
  const repository = resource(EntityKind.REPOSITORY, { name: "Repository", checkouts: [], base: {}, starting: {}, auto_fetch: true, remediation: policy }, 4n);
  const value = fixture([repository]);
  render(value.view(<ConfigurationEditor kind={EntityKind.REPOSITORY} initial={repository} active saved={() => {}} cancel={() => {}} />));
  fireEvent.change(screen.getByLabelText("Selector 1 type"), { target: { value: "minimum-permission" } });
  expect(screen.queryByLabelText("Selector 1 GitHub numeric ID")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Remove reviewer selector 1" }));
  expect(screen.getByText(/No reviewer selectors/)).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Save Repository" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  const request = input(value.save.mock.calls[0][0]);
  expect(request.mutation.expectedRevision).toBe(4n);
  expect(JSON.parse(new TextDecoder().decode(request.documentJson)).remediation).toEqual({ ...policy, reviewer_selectors: [] });
});


it("keeps the unfiltered picker cursor independent and retains an exact provider absent from account filters", async () => {
  const openai = resource(EntityKind.PROVIDER, { name: "OpenAI", authentication: "bearer", protocol: "openai-responses", endpoint: "https://api.example.test/v1", enabled: true });
  const anthropic = resource(EntityKind.PROVIDER, { name: "Anthropic", authentication: "bearer", protocol: "anthropic-messages", endpoint: "https://api.example.test/v1", enabled: true });
  const custom = resource(EntityKind.PROVIDER, { name: "Custom API", authentication: "bearer", protocol: "openai-chat", endpoint: "https://custom.example.test/v1", enabled: true });
  const entry = (provider: Resource, displayName: string) => create(ProviderInventoryEntrySchema, { providerId: provider.id, displayName, enabled: true, provider, accountCountsAvailable: true });
  const capabilities = [ProviderInventoryCapability.PROVIDER_ACTIVATION, ProviderInventoryCapability.ACTIVE_API_MODEL_FILTER, ProviderInventoryCapability.ACCOUNT_PROVIDER_FILTER, ProviderInventoryCapability.ACCOUNT_TYPE_FILTER];
  const requests: { query: string; enabledOnly: boolean; pageSize: number; pageToken: string }[] = [];
  let failLater = true;
  const value = fixture([openai, anthropic, custom], { readProviderInventory: (pageToken, request) => {
    requests.push({ ...request, pageToken });
    if (!request.enabledOnly) return { entries: [entry(anthropic, "Anthropic")], capabilities };
    if (!pageToken) return { entries: [entry(openai, "OpenAI")], capabilities, nextPageToken: "enabled-page-2" };
    if (failLater) { failLater = false; throw new ConnectError("Temporary fixture failure", Code.Unavailable); }
    return { entries: [entry(custom, "Custom API")], capabilities };
  } });
  const savedAccount = resource(EntityKind.ACCOUNT, { alias: "Custom key", provider_id: custom.id, type: "api", enabled: true, health: "disconnected" }, 4n);
  value.save.mockImplementation(async (request: unknown) => ({ resource: savedAccount, requestId: input(request).mutation.requestId }));
  value.connect.mockImplementation(async (request: unknown) => ({ account: create(ResourceSchema, { ...savedAccount, revision: 5n, documentJson: encode({ alias: "Custom key", provider_id: custom.id, type: "api", enabled: true, health: "unverified", connection: { id: newRequestId() } }) }), requestId: input(request).mutation.requestId }));
  render(value.view(<Settings close={() => {}} />));
  fireEvent.click(screen.getByRole("button", { name: "AI API Keys" }));
  fireEvent.change(await screen.findByRole("searchbox", { name: "Search providers" }), { target: { value: "Anthropic" } });
  await waitFor(() => expect(requests.some((request) => !request.enabledOnly && request.query === "Anthropic")).toBe(true));
  fireEvent.click(screen.getByRole("button", { name: "Add AI API key" }));
  expect(await screen.findByRole("button", { name: "OpenAI API key" })).toBeTruthy();
  expect(screen.queryByRole("searchbox")).toBeNull();
  expect(screen.queryByRole("button", { name: "First page" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Next page" }));
  fireEvent.click(await screen.findByRole("button", { name: "Retry providers" }));
  fireEvent.click(await screen.findByRole("button", { name: "Custom API API key" }));
  expect(screen.getByRole("heading", { name: "Connect your entry" })).toBe(window.document.activeElement);
  expect(screen.getByText("Custom API", { selector: "strong" })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Back to provider" }));
  expect(screen.getByRole("button", { name: "Custom API API key" })).toBe(window.document.activeElement);
  expect(screen.getByRole("button", { name: "First page" })).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Next page" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Back to AI API Keys" }));
  expect((screen.getByRole("searchbox", { name: "Search providers" }) as HTMLInputElement).value).toBe("Anthropic");
  fireEvent.click(screen.getByRole("button", { name: "Add AI API key" }));
  fireEvent.click(screen.getByRole("button", { name: "Custom API API key" }));
  fireEvent.change(screen.getByLabelText("Entry name"), { target: { value: "Custom key" } });
  fireEvent.change(screen.getByLabelText("API key"), { target: { value: "fixture-only-secret" } });
  fireEvent.click(screen.getByRole("button", { name: "Add and connect" }));
  await screen.findByRole("heading", { name: "Custom key" });
  await waitFor(() => expect(value.connect).toHaveBeenCalledTimes(1));
  expect(JSON.parse(new TextDecoder().decode(input(value.save.mock.calls[0][0]).documentJson)).provider_id).toBe(custom.id);
  const choices = requests.filter((request) => request.enabledOnly);
  expect(choices.every((request) => request.query === "" && request.pageSize === 50)).toBe(true);
  expect(choices.filter((request) => request.pageToken === "enabled-page-2")).toHaveLength(2);
});

it.each([
  ProviderInventoryCapability.PROVIDER_ACTIVATION,
  ProviderInventoryCapability.ACTIVE_API_MODEL_FILTER,
  ProviderInventoryCapability.ACCOUNT_PROVIDER_FILTER,
  ProviderInventoryCapability.ACCOUNT_TYPE_FILTER,
])("requires every picker inventory capability (%s)", async (missing) => {
  const provider = resource(EntityKind.PROVIDER, { name: "OpenAI", authentication: "bearer", protocol: "openai-chat", endpoint: "https://api.example.test/v1", enabled: true });
  const entry = create(ProviderInventoryEntrySchema, { providerId: provider.id, displayName: "OpenAI", enabled: true, provider, accountCountsAvailable: true });
  const capabilities = [ProviderInventoryCapability.PROVIDER_ACTIVATION, ProviderInventoryCapability.ACTIVE_API_MODEL_FILTER, ProviderInventoryCapability.ACCOUNT_PROVIDER_FILTER, ProviderInventoryCapability.ACCOUNT_TYPE_FILTER];
  const value = fixture([provider], { readProviderInventory: (_, request) => ({ entries: [entry], capabilities: request.enabledOnly ? capabilities.filter((capability) => capability !== missing) : capabilities }) });
  render(value.view(<Settings close={() => {}} />));
  fireEvent.click(screen.getByRole("button", { name: "AI API Keys" }));
  const add = screen.getByRole("button", { name: "Add AI API key" });
  await waitFor(() => expect((add as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(add);
  expect(await screen.findByText(/Provider choices are unavailable/)).toBeTruthy();
  expect(screen.queryByRole("button", { name: "OpenAI API key" })).toBeNull();
  expect(value.save).not.toHaveBeenCalled();
  expect(value.connect).not.toHaveBeenCalled();
});

it("scopes shared preference and deletion terminology to API documents and preserves subscription aliases", async () => {
  for (const type of ["api", "subscription"] as const) {
    const account = resource(EntityKind.ACCOUNT, { alias: "Original API account alias", type, provider_id: newRequestId(), enabled: true, health: "disconnected" }, 7n);
    const value = fixture([account]);
    const view = render(value.view(<ConfigurationEditor kind={EntityKind.ACCOUNT} initial={account} active saved={() => {}} cancel={() => {}} />));
    expect(screen.getByRole("heading", { name: type === "api" ? "Edit AI API key entry" : "Edit AI account" })).toBeTruthy();
    expect((screen.getByLabelText(type === "api" ? "Entry name" : "Account alias") as HTMLInputElement).value).toBe("Original API account alias");
    expect(screen.getByRole("checkbox", { name: type === "api" ? "Enable this entry" : "Enable this account" })).toBeTruthy();
    expect(screen.getByRole("checkbox", { name: type === "api" ? "Exclude from automatic entry selection" : "Exclude from automatic account selection" })).toBeTruthy();
    expect(screen.getByRole("checkbox", { name: type === "api" ? "Notify when entry quota recovers" : "Notify when account quota recovers" })).toBeTruthy();
    view.rerender(value.view(<ConfigurationDeletion initial={account} deleted={() => {}} close={() => {}} />));
    expect(screen.getByRole("heading", { name: "Delete Original API account alias?" })).toBeTruthy();
    expect(screen.getByText(type === "api" ? "Disconnect the entry and finish credential cleanup before deleting it." : "Disconnect the account and finish credential cleanup before deleting it.")).toBeTruthy();
    view.unmount();
    value.client.clear();
  }
});

it("keeps API connection actions separate from validation and preserves server-owned diagnostic wording", async () => {
  const provider = resource(EntityKind.PROVIDER, { name: "Hosted provider", authentication: "bearer", enabled: true });
  const account = resource(EntityKind.ACCOUNT, { alias: "API account alias", provider_id: provider.id, type: "api", enabled: true, health: "unverified", connection: { id: newRequestId(), authentication: "bearer" }, validation: { state: "failed", problem: { message: "Server account diagnostic", guidance: "Original account guidance" } } }, 5n);
  const value = fixture([provider, account]);
  const view = render(value.view(<AccountConnection initial={account} active close={() => {}} />));
  await screen.findByText(/Hosted provider/);
  expect(screen.getByRole("heading", { name: "API account alias" })).toBeTruthy();
  expect(screen.getByText("Server account diagnostic Original account guidance")).toBeTruthy();
  expect(screen.getByRole("button", { name: "Validate connection" })).toBeTruthy();
  expect(value.connect).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Disconnect" }));
  fireEvent.click(screen.getByRole("button", { name: "Confirm disconnection" }));
  await waitFor(() => expect(value.disconnect).toHaveBeenCalledTimes(1));
  expect(value.disconnect.mock.calls[0][0]).toMatchObject({ mutation: { id: account.id, expectedRevision: 5n } });
  view.unmount();
  value.client.clear();
  const subscription = resource(EntityKind.ACCOUNT, { alias: "Subscription alias", provider_id: provider.id, type: "subscription", health: "disconnected" });
  render(value.view(<AccountConnection initial={subscription} active close={() => {}} />));
  expect(screen.getByRole("button", { name: "Back to accounts" })).toBeTruthy();
  expect(screen.getByText(/Subscription login is not implemented yet/)).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Connect API key" })).toBeNull();
  expect(screen.queryByLabelText("API key")).toBeNull();
});

it("scopes Transfer presentation and retains its navigation locks until the Settings opening is discarded", async () => {
  const value = fixture([]); const close = vi.fn();
  const rendered = render(value.view(<Settings visible close={close} />));
  fireEvent.click(screen.getByRole("button", { name: "Import / Export" }));
  const heading = screen.getByRole("heading", { name: "Import / Export", level: 1 });
  expect(heading.closest(".settings-transfer-column")).toBeTruthy();
  expect(heading.getAttribute("aria-live")).toBe("polite");
  expect(screen.getByText("Move configuration between DeliDev servers.")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Instructions" }));
  expect(screen.getByRole("heading", { name: "Instructions", level: 1 }).closest(".settings-transfer-column")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Import / Export" }));
  const json = screen.getByRole("textbox", { name: "Configuration JSON" }) as HTMLTextAreaElement;
  const raw = '{"version":1,"entries":[],"machines":[]}';
  fireEvent.change(json, { target: { value: raw } });
  for (const button of within(screen.getByRole("navigation", { name: "Settings categories" })).getAllByRole("button")) expect((button as HTMLButtonElement).disabled).toBe(true);
  expect((screen.getByRole("combobox", { name: "Settings category" }) as HTMLSelectElement).disabled).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "Load configuration document" }));
  rendered.rerender(value.view(<Settings visible close={close} />));
  expect(screen.getByRole("textbox", { name: "Configuration JSON" })).toBe(json);
  expect(json.value).toBe(raw);
  fireEvent.click(screen.getByRole("button", { name: "Close Settings" }));
  expect(close).toHaveBeenCalledTimes(1);
  rendered.rerender(value.view(<Settings visible={false} close={close} />));
  rendered.rerender(value.view(<Settings visible close={close} />));
  expect(screen.getByRole("heading", { name: "AI Subscription", level: 1 })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Import / Export" }));
  expect((screen.getByRole("textbox", { name: "Configuration JSON" }) as HTMLTextAreaElement).value).toBe("");
  expect(screen.queryByRole("button", { name: "Preview configuration changes" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Retry the same configuration import" })).toBeNull();
  expect((screen.getByRole("combobox", { name: "Settings category" }) as HTMLSelectElement).disabled).toBe(false);
});
