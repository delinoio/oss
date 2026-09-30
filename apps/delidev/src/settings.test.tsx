import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { AccountService, ConfigurationService, EntityKind, ProviderInventoryCapability, ProviderInventoryEntrySchema, ProviderPresetId, ProviderService, ResourceSchema, ResourceService, WorkerService, newRequestId, type ProviderInventoryEntry, type Resource } from "@delinoio/delidev-api-client";
import { Settings, ConfigurationEditor } from "./settings";
import { AccountConnection } from "./account-connection";
import { ConfigurationDeletion, RoutingPreview } from "./configuration-actions";
import { MutationIntents } from "./mutation";
import { encode, type Document } from "./documents";

function resource(kind: EntityKind, value: Document, revision = 1n) { return create(ResourceSchema, { id: newRequestId(), kind, schemaVersion: 1, revision, documentJson: encode(value) }); }
function fixture(resources: Resource[], options: { providerEntries?: ProviderInventoryEntry[]; presets?: unknown[]; providerInventoryError?: ConnectError; readResources?: (kind: EntityKind, pageToken: string) => { resources: Resource[]; nextPageToken?: string }; readProviderInventory?: (pageToken: string) => { entries: ProviderInventoryEntry[]; capabilities: ProviderInventoryCapability[]; nextPageToken?: string }; readModelSearch?: (pageToken: string) => { models: Resource[]; providers: Resource[]; nextPageToken?: string } } = {}) {
  const save = vi.fn(async (_request: unknown): Promise<{ resource?: Resource; job?: Resource }> => ({ resource: resources[0] }));
  const remove = vi.fn(async (_request: unknown) => ({}));
  const preview = vi.fn(async (_request: unknown) => ({ routeJson: encode({ policy: "remaining-quota", selected: "", candidates: [] }) }));
  const inspect = vi.fn(async (_request: unknown) => ({ job: resources.find((row) => row.kind === EntityKind.JOB) }));
  const connect = vi.fn(async (_request: unknown) => ({ account: resources.find((row) => row.kind === EntityKind.ACCOUNT) }));
  const disconnect = vi.fn(async (_request: unknown) => ({ account: resources.find((row) => row.kind === EntityKind.ACCOUNT) }));
  const transport = createRouterTransport((router) => {
    router.service(ConfigurationService, { saveConfiguration: save, deleteConfiguration: remove, previewRouting: preview });
    router.service(WorkerService, { inspectRepository: inspect });
    router.service(ResourceService, { listResources: (request) => options.readResources?.(request.filter?.kind ?? EntityKind.UNSPECIFIED, request.filter?.pageToken ?? "") ?? ({ resources: resources.filter((row) => row.kind === request.filter?.kind) }), getResource: (request) => ({ resource: resources.find((row) => row.id === request.id) }) });
    router.service(AccountService, { getAccountStatus: (request) => ({ account: resources.find((row) => row.id === request.id) }), connectAccount: connect, disconnectAccount: disconnect });
    router.service(ProviderService, {
      listProviderPresets: () => ({ presetsJson: encode(options.presets ?? [{ id: "ollama", provider: { name: "Local provider", endpoint: "http://127.0.0.1:11434/v1", protocol: "openai-chat", authentication: "keyless", discovery: true }, key_guidance: "Run your local model server first.", compatibility: "Requires a compatible model." }]) }),
      listProviderInventory: (request) => {
        if (options.providerInventoryError) throw options.providerInventoryError;
        return options.readProviderInventory?.(request.pageToken) ?? ({ entries: options.providerEntries ?? [{ presetId: ProviderPresetId.OLLAMA, displayName: "Local provider", enabled: false, totalAccounts: 0n, connectedAccounts: 0n, accountCountsAvailable: true }], capabilities: [ProviderInventoryCapability.PROVIDER_ACTIVATION, ProviderInventoryCapability.ACTIVE_API_MODEL_FILTER, ProviderInventoryCapability.ACCOUNT_PROVIDER_FILTER, ...(options.providerEntries ? [ProviderInventoryCapability.ACCOUNT_TYPE_FILTER] : [])] });
      },
      searchModels: (request) => options.readModelSearch?.(request.pageToken) ?? ({ models: [], providers: [] }),
    });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false, gcTime: 0 } } });
  const view = (children: React.ReactNode) => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents>{children}</MutationIntents></QueryClientProvider></TransportProvider>;
  return { resources, save, remove, preview, inspect, connect, disconnect, client, view };
}
function input(value: unknown) { return value as { mutation: { requestId: string; expectedRevision: bigint }; documentJson: Uint8Array }; }

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
  const labels = ["AI Subscription", "API Accounts", "API Providers", "Models", "Agent Workers", "Instructions", "Projects", "Repositories", "Execution Workers", "Paired devices", "Server preferences", "Integrations", "Diagnostics", "Notifications", "Import / Export", "Backups"];
  const values = ["subscription-accounts", "api-accounts", "providers", "models", "agent-workers", "instructions", "projects", "repositories", "execution-workers", "paired-devices", "server-preferences", "integrations", "diagnostics", "notifications", "transfer", "backups"];
  expect(Array.from(navigation.querySelectorAll(".settings-nav-group h2"), (heading) => heading.textContent)).toEqual(["AI & agents", "Workspace", "System"]);
  expect(within(navigation).getAllByRole("button").map((button) => button.textContent?.trim().replace(/\s+/g, " "))).toEqual(labels);
  const categorySelect = screen.getByRole("combobox", { name: "Settings category" }) as HTMLSelectElement;
  expect(categorySelect.options).toHaveLength(16);
  expect(Array.from(categorySelect.options, (option) => option.textContent)).toEqual(labels);
  expect(Array.from(categorySelect.options, (option) => option.value)).toEqual(values);
  expect(Array.from(categorySelect.querySelectorAll("optgroup"), (group) => group.label)).toEqual(["AI & agents", "Workspace", "System"]);
  expect(categorySelect.value).toBe("providers");
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
  expect(await screen.findByText(/Accounts: 0 connected · 0 total/)).toBeTruthy();
  expect(screen.queryByText("Account required")).toBeNull();
  expect(screen.queryByRole("alert")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Add account" }));
  expect(await screen.findByRole("button", { name: "Continue to account" })).toBeTruthy();
});

it("still reports a real provider inventory read failure", async () => {
  const value = fixture([], { providerInventoryError: new ConnectError("Inventory unavailable", Code.Unavailable) });
  render(value.view(<Settings visible close={() => {}} />));
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
  fireEvent.click(await screen.findByRole("button", { name: "API Accounts" }));
  fireEvent.click(await screen.findByRole("button", { name: "Add API account" }));
  fireEvent.click(await screen.findByRole("radio", { name: "OpenAI" }));
  fireEvent.click(screen.getByRole("button", { name: "Continue to account" }));
  fireEvent.click(screen.getByText("Where to get an API key"));
  expect(screen.getByText("Create a project key for the selected workspace.")).toBeTruthy();
  expect(screen.getByText("https://developers.openai.com/api/reference/overview")).toBeTruthy();
  expect(screen.queryByRole("link", { name: "https://developers.openai.com/api/reference/overview" })).toBeNull();
});

it("keeps a settings draft across closing the modal and retries the original provider document", async () => {
  const value = fixture([]);
  value.save.mockRejectedValueOnce(new ConnectError("acknowledgement lost", Code.Unavailable));
  const view = render(value.view(<Settings visible close={() => {}} />));
  const create = await screen.findByRole("button", { name: "Custom provider" });
  await waitFor(() => expect((create as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(create);
  fireEvent.change(screen.getByRole("textbox", { name: "Name" }), { target: { value: "My local provider" } });
  fireEvent.change(screen.getByRole("textbox", { name: "API base URL" }), { target: { value: "http://127.0.0.1:11434/v1" } });
  view.rerender(value.view(<Settings visible={false} close={() => {}} />));
  view.rerender(value.view(<Settings visible close={() => {}} />));
  expect((screen.getByRole("textbox", { name: "Name" }) as HTMLInputElement).value).toBe("My local provider");
  fireEvent.click(screen.getByRole("button", { name: "Save Provider" }));
  fireEvent.click(await screen.findByRole("button", { name: "Retry the same configuration" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(2));
  expect(value.save.mock.calls[0][0]).toEqual(value.save.mock.calls[1][0]);
  const request = input(value.save.mock.calls[0][0]);
  expect(request.mutation.expectedRevision).toBe(0n);
  expect(JSON.parse(new TextDecoder().decode(request.documentJson))).toEqual({ name: "My local provider", endpoint: "http://127.0.0.1:11434/v1", protocol: "openai-responses", authentication: "bearer", discovery: true, enabled: true });
});

it("preserves server-owned account observations during a preference edit", async () => {
  const provider = resource(EntityKind.PROVIDER, { name: "Provider" });
  const observed = { alias: "Original", provider_id: provider.id, type: "api", enabled: true, exclude_automatic: false, recovery_notifications: true, health: "ready", connection: { id: newRequestId(), authentication: "bearer", connected_at: "2026-09-25T00:00:00Z" }, quota: [{ id: "window", remaining: 0 }], confirmed_exhausted: true, validation: { state: "observed" }, catalog: { state: "stale" } };
  const account = resource(EntityKind.ACCOUNT, observed, 7n);
  const value = fixture([account, provider]);
  render(value.view(<ConfigurationEditor kind={EntityKind.ACCOUNT} initial={account} active saved={() => {}} cancel={() => {}} />));
  fireEvent.change(screen.getByRole("textbox", { name: "Account alias" }), { target: { value: "Renamed" } });
  fireEvent.click(screen.getByRole("button", { name: "Save AI account" }));
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
  fireEvent.click(screen.getByRole("button", { name: "Connect account" }));
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
  fireEvent.change(screen.getByLabelText("Execution Worker"), { target: { value: machine.id } });
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
  await screen.findByRole("option", { name: "Fix agent" });
  fireEvent.change(screen.getByLabelText("Remediation Agent Worker"), { target: { value: agent.id } });
  fireEvent.change(screen.getByLabelText("Remediation execution Worker"), { target: { value: machine.id } });
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
