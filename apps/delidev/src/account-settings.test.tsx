import { StrictMode } from "react";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { SystemService, SystemCapability, configurationSchemaVersion, AccountService, ConfigurationService, EntityKind, ProviderService, ResourceSchema, ResourceService, newRequestId, type Resource, type GetUsageSummaryRequest, UsageService, GetUsageSummaryResponseSchema } from "@delinoio/delidev-api-client";
import { AccountSettings, AccountSettingsSection, type AccountProviderSummary } from "./account-settings";
import { MutationIntents } from "./mutation";
import { Authentication } from "./configuration-fields";
import { encode, type Document } from "./documents";

function resource(kind: EntityKind, value: Document, revision = 1n) {
  return create(ResourceSchema, { id: newRequestId(), kind, schemaVersion: configurationSchemaVersion(kind, value), revision, documentJson: encode(value) });
}

async function chooseAPIFormat(protocol?: string) {
  const select = screen.getByLabelText("API format") as HTMLSelectElement;
  await waitFor(() => expect(select.matches(":disabled")).toBe(false));
  if (select.tagName === "SELECT") fireEvent.change(select, { target: { value: protocol ?? select.options[1].value } });
  else { expect(select.tagName).toBe("OUTPUT"); if (protocol) expect(select.textContent).toBe(protocol === "openai-chat" ? "OpenAI Chat Completions" : protocol === "openai-responses" ? "OpenAI Responses" : "Anthropic Messages"); }
}

function requestId(value: unknown): string {
  return (value as { mutation?: { requestId?: string } }).mutation?.requestId ?? "";
}

function fixture(args: { resources?: Resource[]; save?: (request: unknown) => Promise<{ resource?: Resource; requestId?: string }>; connect?: (request: unknown) => Promise<{ account?: Resource; requestId?: string }>; providerId?: string; currentProvider?: Resource; listPage?: (request: { filter?: { kind?: EntityKind; pageToken?: string }; providerId?: string; accountType?: number }) => { resources: Resource[]; nextPageToken?: string } } = {}) {
  const providerId = args.providerId ?? newRequestId();
  const provider = create(ResourceSchema, { ...resource(EntityKind.PROVIDER, { name: "API provider", protocol: "openai-responses", authentication: "api-key", endpoint: "https://api.example.test/v1" }), id: providerId });
  const providerOption: AccountProviderSummary = { providerId, displayName: "API provider", enabled: true, provider, keyGuidance: "Create a scoped provider key.", documentationUrl: "https://docs.example.test/keys" };
  const resources = args.resources ?? [];
  const list = vi.fn(async (request: { filter?: { kind?: EntityKind; pageToken?: string }; providerId?: string; accountType?: number }) => ({
    ...(args.listPage ? args.listPage(request) : { resources: resources.filter((row) => row.kind === request.filter?.kind && (!request.providerId || JSON.parse(new TextDecoder().decode(row.documentJson)).provider_id === request.providerId) && JSON.parse(new TextDecoder().decode(row.documentJson)).type === (request.accountType === 1 ? "api" : "subscription")) }),
  }));
  const save = vi.fn(args.save ?? (async (request: unknown) => ({ resource: resources[0], requestId: requestId(request) })));
  const connect = vi.fn(args.connect ?? (async (request: unknown) => ({ account: resources.find((row) => row.kind === EntityKind.ACCOUNT), requestId: requestId(request) })));
  const other = vi.fn(async () => ({}));
  const doctor = vi.fn(async () => ({ reportJson: encode({ schema_version: 2, version: "fixture", credentials: [] }) }));
  const status = vi.fn(() => ({ capabilities: [SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1] as SystemCapability[] }));
  const usage = vi.fn((_request: GetUsageSummaryRequest) => create(GetUsageSummaryResponseSchema, { fromUnixMs: 1780000000000n, untilUnixMs: 1782592000000n }));
  const transport = createRouterTransport((router) => {
    router.service(UsageService, { getUsageSummary: usage });
    router.service(SystemService, { getStatus: status, getDoctor: doctor });
    router.service(ResourceService, { listResources: list, getResource: async (request) => ({ resource: resources.find((row) => row.id === request.id) ?? (request.kind === EntityKind.PROVIDER && request.id === providerId ? args.currentProvider ?? provider : undefined) }) });
    router.service(ConfigurationService, { saveConfiguration: save });
    router.service(AccountService, { connectAccount: connect, getAccountStatus: async (request) => ({ account: resources.find((row) => row.id === request.id) }), disconnectAccount: other, validateAccount: other });
    router.service(ProviderService, { discoverModels: other });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false, gcTime: 0 } } });
  const view = (children: React.ReactNode) => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents>{children}</MutationIntents></QueryClientProvider></TransportProvider>;
  const callbacks = {
    clearProviderFilter: vi.fn(),
    openApiProviders: vi.fn(),
    manageAccount: vi.fn(),
    editAccount: vi.fn(),
    deleteAccount: vi.fn(),
    setProviderSearch: vi.fn(),
    setProviderFilter: vi.fn(),
    loadMoreProviders: vi.fn(),
  };
  const settings = (section: AccountSettingsSection, overrides: Partial<React.ComponentProps<typeof AccountSettings>> = {}) => <AccountSettings
    section={section}
    active
    accountTypeFilteringReady
    apiFormatSelectingReady
    clearProviderFilter={callbacks.clearProviderFilter}
    providers={[providerOption]}
    eligibleProviders={[providerOption]}
    providerSearch=""
    setProviderSearch={callbacks.setProviderSearch}
    setProviderFilter={callbacks.setProviderFilter}
    providerSearchLoading={false}
    providerPicker={{ ready: true, loaded: true, fetching: false, pageToken: "", nextPageToken: "", retry: vi.fn(), next: callbacks.loadMoreProviders, first: vi.fn() }}
    subscriptionProviderResources={[]}
    subscriptionProviderManagement={<button type="button">Add native subscription provider</button>}
    openApiProviders={callbacks.openApiProviders}
    manageAccount={callbacks.manageAccount}
    editAccount={callbacks.editAccount}
    deleteAccount={callbacks.deleteAccount}
    {...overrides}
  />;
  return { usage, doctor, status, providerId, provider, providerOption, resources, list, save, connect, other, client, callbacks, view, settings };
}

it("uses server-side account type and provider filters and keeps the split view disabled without its capability", async () => {
  const apiProviderId = newRequestId(), subscriptionProviderId = newRequestId();
  const api = resource(EntityKind.ACCOUNT, { alias: "API", provider_id: apiProviderId, type: "api", enabled: true, health: "unverified" });
  const apiLater = resource(EntityKind.ACCOUNT, { alias: "Second API", provider_id: apiProviderId, type: "api", enabled: true, health: "unverified" });
  const subscription = resource(EntityKind.ACCOUNT, { alias: "Subscription", provider_id: subscriptionProviderId, type: "subscription", enabled: true, health: "disconnected" });
  const value = fixture({ resources: [api, apiLater, subscription], providerId: apiProviderId, listPage: (request) => request.filter?.pageToken ? { resources: [apiLater] } : { resources: [api], nextPageToken: "api-provider-cursor" } });
  const first = render(value.view(value.settings(AccountSettingsSection.Api, { providerIdFilter: apiProviderId })));
  expect(await screen.findByRole("heading", { name: "API" })).toBeTruthy();
  expect(screen.queryByRole("heading", { name: "Subscription" })).toBeNull();
  await waitFor(() => expect(value.list).toHaveBeenCalled());
  expect(value.list.mock.calls[0][0]).toMatchObject({ providerId: apiProviderId, accountType: 1, filter: { kind: EntityKind.ACCOUNT, pageSize: 50 } });
  expect(screen.getByRole("button", { name: "Clear provider filter" })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Load more Entry pages" }));
  expect(await screen.findByRole("heading", { name: "Second API" })).toBeTruthy();
  expect(value.list.mock.calls.at(-1)?.[0]).toMatchObject({ providerId: apiProviderId, accountType: 1, filter: { kind: EntityKind.ACCOUNT, pageToken: "api-provider-cursor" } });

  first.unmount();
  value.client.clear();
  value.list.mockClear();
  value.status.mockReturnValue({ capabilities: [] });
  const view = render(value.view(value.settings(AccountSettingsSection.Subscription)));
  expect(await screen.findByText(/supports service-native subscription accounts/)).toBeTruthy();
  expect(value.list).not.toHaveBeenCalled();
  expect(screen.queryByRole("button", { name: "Add native subscription provider" })).toBeNull();
  view.unmount();

  value.client.clear();
  value.list.mockClear();
  render(value.view(value.settings(AccountSettingsSection.Api, { accountTypeFilteringReady: false })));
  expect((screen.getByRole("button", { name: "Add AI API key" }) as HTMLButtonElement).disabled).toBe(true);
  expect(value.list).not.toHaveBeenCalled();
});

it("locks settings navigation and pauses inventory reads while the API account wizard is open", async () => {
  const value = fixture();
  const workflow = vi.fn();
  render(value.view(value.settings(AccountSettingsSection.Api, { onWorkflowReadyChange: workflow })));
  await waitFor(() => expect(value.list).toHaveBeenCalled());
  fireEvent.click(screen.getByRole("button", { name: "Add AI API key" }));
  await waitFor(() => expect(workflow).toHaveBeenLastCalledWith(true));
  const originalReads = value.list.mock.calls.length;
  await act(async () => { await value.client.invalidateQueries(); await new Promise(resolve => setTimeout(resolve, 20)); });
  expect(value.list).toHaveBeenCalledTimes(originalReads);
  fireEvent.click(screen.getByRole("button", { name: "Close Add AI API key" }));
  await waitFor(() => expect(workflow).toHaveBeenLastCalledWith(false));
});

it("opens the exact inventory action once under Strict Mode and rerender without writes", async () => {
  const value = fixture();
  const props = { startApiWizard: { key: "provider-add-1", providerId: value.providerId, provider: value.providerOption }, providers: [] };
  const view = render(<StrictMode>{value.view(value.settings(AccountSettingsSection.Api, props))}</StrictMode>);
  expect(await screen.findByRole("heading", { name: "Add AI API key" })).toBeTruthy();
  expect(screen.getByRole("heading", { name: "Connect your entry" })).toBeTruthy();
  expect(screen.getByLabelText("Entry name")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Back to AI API Keys" }));
  view.rerender(<StrictMode>{value.view(value.settings(AccountSettingsSection.Api, props))}</StrictMode>);
  expect(screen.queryByRole("heading", { name: "Connect your entry" })).toBeNull();
  expect(value.other).not.toHaveBeenCalled();
  expect(value.save).not.toHaveBeenCalled();
  expect(value.connect).not.toHaveBeenCalled();
});

it.each(["account inventory", "picker inventory"])("keeps direct-entry fields unavailable until the %s capability gates are ready", async (unavailable) => {
  const value = fixture();
  const entry = { key: "gated-provider-entry", providerId: value.providerId, provider: value.providerOption };
  const picker = { ready: unavailable !== "picker inventory", loaded: true, fetching: false, pageToken: "", nextPageToken: "", retry: vi.fn(), next: vi.fn(), first: vi.fn() };
  const props = { startApiWizard: entry, accountTypeFilteringReady: unavailable !== "account inventory", providerPicker: picker };
  const view = render(<StrictMode>{value.view(value.settings(AccountSettingsSection.Api, props))}</StrictMode>);
  await screen.findByRole("heading", { name: "Connect your entry" });
  const name = screen.getByLabelText("Entry name") as HTMLInputElement;
  const key = screen.getByLabelText("API key") as HTMLInputElement;
  expect(name.matches(":disabled")).toBe(true);
  expect(key.matches(":disabled")).toBe(true);
  expect((screen.getByRole("button", { name: "Add and connect" }) as HTMLButtonElement).disabled).toBe(true);
  fireEvent.submit(name.closest("form")!);
  view.rerender(<StrictMode>{value.view(value.settings(AccountSettingsSection.Api, { ...props, accountTypeFilteringReady: true, providerPicker: { ...picker, ready: true } }))}</StrictMode>);
  expect(name.matches(":disabled")).toBe(false);
  expect(key.matches(":disabled")).toBe(false);
  await chooseAPIFormat("openai-responses");
  fireEvent.change(name, { target: { value: "Explicit draft" } });
  fireEvent.change(key, { target: { value: "fixture-only-key" } });
  view.rerender(<StrictMode>{value.view(value.settings(AccountSettingsSection.Api, props))}</StrictMode>);
  expect(name.matches(":disabled")).toBe(true);
  expect(key.matches(":disabled")).toBe(true);
  fireEvent.submit(name.closest("form")!);
  expect(value.save).not.toHaveBeenCalled();
  expect(value.connect).not.toHaveBeenCalled();
  expect(value.other).not.toHaveBeenCalled();
});

it.each([Authentication.Key, Authentication.Keyless])("keeps the clicked %s contract across stale independent inventory snapshots", async (authentication) => {
  const providerId = newRequestId();
  const keyless = authentication === Authentication.Keyless;
  const provider = create(ResourceSchema, { ...resource(EntityKind.PROVIDER, { name: "Clicked provider", protocol: "openai-chat", authentication, endpoint: "http://127.0.0.1:11434/v1", enabled: true }, 2n), id: providerId });
  const staleProvider = create(ResourceSchema, { ...provider, revision: 1n, documentJson: encode({ name: "Stale provider", protocol: "openai-chat", authentication: keyless ? Authentication.Key : Authentication.Keyless, endpoint: "https://stale.example.test/v1", enabled: false }) });
  const selected: AccountProviderSummary = { providerId, displayName: "Clicked provider", enabled: true, provider, keyGuidance: "", documentationUrl: "" };
  const stale: AccountProviderSummary = { ...selected, displayName: "Stale provider", enabled: false, provider: staleProvider };
  const account = resource(EntityKind.ACCOUNT, { alias: "Exact clicked provider", provider_id: providerId, type: "api", api_protocol: "openai-chat", enabled: true, health: "disconnected" }, 2n);
  const connected = create(ResourceSchema, { ...account, revision: 3n, documentJson: encode({ alias: "Exact clicked provider", provider_id: providerId, type: "api", api_protocol: "openai-chat", enabled: true, health: "unverified", connection: { id: newRequestId(), authentication } }) });
  const value = fixture({ providerId, currentProvider: provider, save: async (request) => ({ resource: account, requestId: requestId(request) }), connect: async (request) => ({ account: connected, requestId: requestId(request) }) });
  const view = render(value.view(value.settings(AccountSettingsSection.Api, { providers: [stale], eligibleProviders: [selected] })));
  fireEvent.click(screen.getByRole("button", { name: "Add AI API key" }));
  fireEvent.click(screen.getByRole("button", { name: `Clicked provider ${keyless ? "Local endpoint" : "API key"}` }));
  expect(screen.getByText("Clicked provider")).toBeTruthy();
  await chooseAPIFormat();
  fireEvent.change(screen.getByLabelText("Entry name"), { target: { value: "Exact clicked provider" } });
  if (!keyless) fireEvent.change(screen.getByLabelText("API key"), { target: { value: "fixture-only-key" } });
  view.rerender(value.view(value.settings(AccountSettingsSection.Api, { providers: [stale], eligibleProviders: [stale] })));
  expect(screen.getByText("Clicked provider")).toBeTruthy();
  expect(screen.queryByLabelText("API key") === null).toBe(keyless);
  expect(value.save).not.toHaveBeenCalled();
  expect(value.connect).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Add and connect" }));
  await waitFor(() => expect(value.connect).toHaveBeenCalledTimes(1));
  expect(value.save).toHaveBeenCalledTimes(1);
  expect(value.connect.mock.calls[0][0]).toMatchObject({ keyless });
  expect(new TextDecoder().decode((value.connect.mock.calls[0][0] as { apiKey: Uint8Array }).apiKey)).toBe(keyless ? "" : "fixture-only-key");
  expect(value.other).not.toHaveBeenCalled();
});

it("creates an API account once, clears the key at submit, connects without auto-validation, and retries the exact uncertain connection", async () => {
  const providerId = newRequestId(), connectionId = newRequestId();
  const created = resource(EntityKind.ACCOUNT, { alias: "Work key", provider_id: providerId, type: "api", api_protocol: "openai-responses", enabled: true, exclude_automatic: false, recovery_notifications: true, health: "disconnected", quota: [] }, 4n);
  const connected = create(ResourceSchema, { ...created, revision: 5n, documentJson: encode({ ...JSON.parse(new TextDecoder().decode(created.documentJson)), connection: { id: connectionId, authentication: "api-key" }, health: "unverified" }) });
  const save = vi.fn(async (request: unknown) => ({ resource: created, requestId: requestId(request) }));
  const connect = vi.fn(async (request: unknown) => ({ account: connected, requestId: requestId(request) })).mockRejectedValueOnce(new ConnectError("response lost", Code.Unavailable));
  const value = fixture({ save, connect, providerId });
  render(value.view(value.settings(AccountSettingsSection.Api)));
  fireEvent.click(screen.getByRole("button", { name: "Add AI API key" }));
  fireEvent.click(screen.getByRole("button", { name: "API provider API key" }));
  await chooseAPIFormat();
  fireEvent.change(screen.getByLabelText("Entry name"), { target: { value: "Work key" } });
  const key = screen.getByLabelText("API key");
  fireEvent.change(key, { target: { value: "fixture-only-secret" } });
  fireEvent.click(screen.getByRole("button", { name: "Add and connect" }));
  await screen.findByRole("heading", { name: "Work key" });
  expect((key as HTMLInputElement).value).toBe("");
  expect(save).toHaveBeenCalledTimes(1);
  const retry = await screen.findByRole("button", { name: "Retry the same connection" });
  expect((screen.getByLabelText("API key") as HTMLInputElement).value).toBe("");
  expect(JSON.stringify(value.client.getQueryCache().getAll().map((query) => query.queryKey))).not.toContain("fixture-only-secret");
  fireEvent.click(retry);
  await waitFor(() => expect(connect).toHaveBeenCalledTimes(2));
  expect(connect.mock.calls[0][0]).toEqual(connect.mock.calls[1][0]);
  expect(connect.mock.calls[0][0]).toMatchObject({ mutation: { id: created.id, expectedRevision: 4n }, keyless: false });
  expect(new TextDecoder().decode((connect.mock.calls[0][0] as unknown as { apiKey: Uint8Array }).apiKey)).toBe("fixture-only-secret");
  expect(screen.getByRole("status").textContent).toContain("validation required");
  expect(value.client.getMutationCache().getAll()).toHaveLength(0);
  expect(value.other).not.toHaveBeenCalled();
});

it("accepts current disconnected metadata on a replay without restoring the old connection or revision", async () => {
  const providerId = newRequestId(), connectionId = newRequestId();
  const created = resource(EntityKind.ACCOUNT, { alias: "Peer changed", provider_id: providerId, type: "api", api_protocol: "openai-responses", enabled: true, health: "disconnected" }, 4n);
  const peerDisconnected = create(ResourceSchema, { ...created, revision: 8n, documentJson: encode({ ...JSON.parse(new TextDecoder().decode(created.documentJson)), health: "disconnected" }) });
  const reconnected = create(ResourceSchema, { ...created, revision: 9n, documentJson: encode({ ...JSON.parse(new TextDecoder().decode(created.documentJson)), health: "unverified", connection: { id: connectionId, authentication: "api-key" } }) });
  const save = vi.fn(async (request: unknown) => ({ resource: created, requestId: requestId(request) }));
  const connect = vi.fn(async (request: unknown) => ({ account: reconnected, requestId: requestId(request), replayed: false }))
    .mockImplementationOnce(async () => { throw new ConnectError("response lost", Code.Unavailable); })
    .mockImplementationOnce(async (request) => ({ account: peerDisconnected, requestId: requestId(request), replayed: true }));
  const value = fixture({ save, connect, providerId });
  render(value.view(value.settings(AccountSettingsSection.Api)));
  fireEvent.click(screen.getByRole("button", { name: "Add AI API key" }));
  fireEvent.click(screen.getByRole("button", { name: "API provider API key" }));
  await chooseAPIFormat();
  fireEvent.change(screen.getByLabelText("Entry name"), { target: { value: "Peer changed" } });
  fireEvent.change(screen.getByLabelText("API key"), { target: { value: "fixture-only-secret" } });
  fireEvent.click(screen.getByRole("button", { name: "Add and connect" }));
  fireEvent.click(await screen.findByRole("button", { name: "Retry the same connection" }));
  await waitFor(() => expect(connect).toHaveBeenCalledTimes(2));
  await waitFor(() => expect(screen.queryByRole("button", { name: "Retry the same connection" })).toBeNull());
  expect(await screen.findByText("Connection: disconnected")).toBeTruthy();
  expect(connect.mock.calls[0][0]).toEqual(connect.mock.calls[1][0]);
  expect(screen.queryByText(/Connected ·/)).toBeNull();
  fireEvent.change(screen.getByLabelText("API key"), { target: { value: "new-explicit-key" } });
  fireEvent.click(screen.getByRole("button", { name: "Connect API key" }));
  await waitFor(() => expect(connect).toHaveBeenCalledTimes(3));
  expect(connect.mock.calls[2][0]).toMatchObject({ mutation: { id: created.id, expectedRevision: 8n } });
  expect(screen.getByText(/Connected · health unverified · validation required/)).toBeTruthy();
});

it.each([
  { change: "authentication", authentication: "bearer", enabled: true },
  { change: "enabled state", authentication: "api-key", enabled: false },
])("rechecks provider $change before account creation and clears the submitted key on mismatch", async ({ authentication, enabled }) => {
  const providerId = newRequestId();
  const changedProvider = create(ResourceSchema, { ...resource(EntityKind.PROVIDER, { name: "Changed provider", protocol: "openai-responses", authentication, endpoint: "https://api.example.test/v1", enabled }), id: providerId });
  const value = fixture({ providerId, currentProvider: changedProvider });
  render(value.view(value.settings(AccountSettingsSection.Api)));
  fireEvent.click(screen.getByRole("button", { name: "Add AI API key" }));
  fireEvent.click(screen.getByRole("button", { name: "API provider API key" }));
  await chooseAPIFormat();
  fireEvent.change(screen.getByLabelText("Entry name"), { target: { value: "Current provider only" } });
  const key = screen.getByLabelText("API key");
  fireEvent.change(key, { target: { value: "fixture-only-secret" } });
  fireEvent.click(screen.getByRole("button", { name: "Add and connect" }));
  expect((await screen.findAllByRole("alert")).some((alert) => /provider changed or is no longer available/.test(alert.textContent ?? ""))).toBe(true);
  expect((key as HTMLInputElement).value).toBe("");
  expect(value.save).not.toHaveBeenCalled();
  expect(value.connect).not.toHaveBeenCalled();
});

it("retries a lost account-create acknowledgment exactly and requires key re-entry before connecting", async () => {
  const providerId = newRequestId();
  const created = resource(EntityKind.ACCOUNT, { alias: "Replayed create", provider_id: providerId, type: "api", api_protocol: "openai-responses", enabled: true, health: "disconnected" }, 2n);
  const save = vi.fn(async (request: unknown) => ({ resource: created, requestId: requestId(request) }))
    .mockRejectedValueOnce(new ConnectError("response lost", Code.Unavailable));
  const value = fixture({ save, providerId });
  render(value.view(value.settings(AccountSettingsSection.Api)));
  fireEvent.click(screen.getByRole("button", { name: "Add AI API key" }));
  fireEvent.click(screen.getByRole("button", { name: "API provider API key" }));
  await chooseAPIFormat();
  fireEvent.change(screen.getByLabelText("Entry name"), { target: { value: "Replayed create" } });
  fireEvent.change(screen.getByLabelText("API key"), { target: { value: "fixture-only-secret" } });
  fireEvent.click(screen.getByRole("button", { name: "Add and connect" }));
  fireEvent.click(await screen.findByRole("button", { name: "Retry the same entry creation" }));
  await screen.findByRole("heading", { name: "Replayed create" });
  await waitFor(() => expect(save).toHaveBeenCalledTimes(2));
  expect(save.mock.calls[0][0]).toEqual(save.mock.calls[1][0]);
  expect(value.connect).not.toHaveBeenCalled();
  expect((screen.getByLabelText("API key") as HTMLInputElement).value).toBe("");
  expect(screen.getByRole("button", { name: "Connect API key" })).toBeTruthy();
});

it("uses explicit keyless connection, sends no key bytes, and keeps subscription setup unavailable", async () => {
  const providerId = newRequestId();
  const provider = create(ResourceSchema, { ...resource(EntityKind.PROVIDER, { name: "Loopback", protocol: "openai-chat", authentication: "keyless", endpoint: "http://127.0.0.1:11434/v1" }), id: providerId });
  const summary = { providerId, displayName: "Loopback", enabled: true, provider, keyGuidance: "", documentationUrl: "" } satisfies AccountProviderSummary;
  const account = resource(EntityKind.ACCOUNT, { alias: "Local endpoint", provider_id: providerId, type: "api", api_protocol: "openai-chat", enabled: true, health: "disconnected" }, 3n);
  const connected = create(ResourceSchema, { ...account, revision: 4n, documentJson: encode({ ...JSON.parse(new TextDecoder().decode(account.documentJson)), health: "unverified", connection: { id: newRequestId(), authentication: "keyless" } }) });
  const save = vi.fn(async (request: unknown) => ({ resource: account, requestId: requestId(request) }));
  const connect = vi.fn(async (request: unknown) => ({ account: connected, requestId: requestId(request) }));
  const value = fixture({ resources: [provider], save, connect, providerId });
  render(value.view(value.settings(AccountSettingsSection.Api, { providers: [summary], eligibleProviders: [summary] })));
  fireEvent.click(screen.getByRole("button", { name: "Add AI API key" }));
  fireEvent.click(screen.getByRole("button", { name: "Loopback Local endpoint" }));
  await chooseAPIFormat("openai-chat");
  expect(screen.queryByLabelText("API key")).toBeNull();
  expect(screen.getByText("Connect to this local endpoint on the selected server.")).toBeTruthy();
  await chooseAPIFormat();
  fireEvent.change(screen.getByLabelText("Entry name"), { target: { value: "Local endpoint" } });
  fireEvent.click(screen.getByRole("button", { name: "Add and connect" }));
  await screen.findByRole("heading", { name: "Local endpoint" });
  await waitFor(() => expect(connect).toHaveBeenCalledTimes(1));
  expect(connect.mock.calls[0][0]).toMatchObject({ keyless: true, apiKey: new Uint8Array() });
  expect(screen.getByRole("status").textContent).toContain("validation required");
  expect(save).toHaveBeenCalledTimes(1);
});

it("does not auto-connect after a hidden keyed create completes late", async () => {
  const account = resource(EntityKind.ACCOUNT, { alias: "Late key", provider_id: newRequestId(), type: "api", enabled: true, health: "disconnected" });
  let resolveCreate!: (value: { resource: Resource; requestId: string }) => void;
  let savedRequestId = "";
  const save = vi.fn((request: unknown) => new Promise<{ resource: Resource; requestId: string }>((resolve) => { resolveCreate = resolve; savedRequestId = requestId(request); }));
  const value = fixture({ save, providerId: JSON.parse(new TextDecoder().decode(account.documentJson)).provider_id });
  const accepted = create(ResourceSchema, { ...account, documentJson: encode({ ...JSON.parse(new TextDecoder().decode(account.documentJson)), provider_id: value.providerId }) });
  const view = render(value.view(value.settings(AccountSettingsSection.Api)));
  fireEvent.click(screen.getByRole("button", { name: "Add AI API key" }));
  fireEvent.click(screen.getByRole("button", { name: "API provider API key" }));
  await chooseAPIFormat();
  fireEvent.change(screen.getByLabelText("Entry name"), { target: { value: "Late key" } });
  fireEvent.change(screen.getByLabelText("API key"), { target: { value: "fixture-only-secret" } });
  fireEvent.click(screen.getByRole("button", { name: "Add and connect" }));
  await waitFor(() => expect(save).toHaveBeenCalledTimes(1));
  view.rerender(value.view(value.settings(AccountSettingsSection.Api, { active: false })));
  resolveCreate({ resource: accepted, requestId: savedRequestId });
  await waitFor(() => expect(value.connect).not.toHaveBeenCalled());
});

it("requires a deliberate keyless connection and exposes provider-off state separately from account health", async () => {
  const providerId = newRequestId();
  const account = resource(EntityKind.ACCOUNT, { alias: "Local", provider_id: providerId, type: "api", enabled: false, health: "disconnected" }, 3n);
  const provider = resource(EntityKind.PROVIDER, { name: "Local provider", protocol: "openai-chat", authentication: "keyless", endpoint: "http://127.0.0.1:11434/v1", enabled: false });
  const off = { providerId, displayName: "Local provider", enabled: false, provider, keyGuidance: "", documentationUrl: "" };
  const providerResource = create(ResourceSchema, { ...provider, id: providerId });
  const value = fixture({ resources: [account, providerResource], providerId: off.providerId });
  render(value.view(value.settings(AccountSettingsSection.Api, { providers: [off], eligibleProviders: [] })));
  expect(await screen.findByText("Provider status:")).toBeTruthy();
  expect(screen.getByText("Off")).toBeTruthy();
  expect(screen.getByText("Disabled")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Manage connection" }));
  expect(await screen.findByText(/Enable it in API Providers/)).toBeTruthy();
});

it("keeps legacy service-native inventory read-only without independent login support", async () => {
  const value = fixture(); render(value.view(value.settings(AccountSettingsSection.Subscription)));
  for (const name of ["ChatGPT", "Claude", "Grok"]) {
    const button = await screen.findByRole("button", { name: `${name} · Coming soon` });
    expect((button as HTMLButtonElement).disabled).toBe(true); fireEvent.click(button);
  }
  expect(value.save).not.toHaveBeenCalled(); expect(value.connect).not.toHaveBeenCalled();
  expect(screen.queryByLabelText("Account name")).toBeNull();
  expect(screen.queryByLabelText("Subscription provider")).toBeNull(); expect(screen.queryByLabelText("Search providers")).toBeNull();
  expect(value.list.mock.calls.every(([request]) => request.filter?.kind === EntityKind.ACCOUNT && request.providerId === "")).toBe(true);
});

it("renders ordered native provider actions, enters once without writes, and returns focus to the exact button", async () => {
  const value = fixture();
  const names = ["OpenAI", "Anthropic", "OpenRouter", "Vercel AI Gateway", "xAI", "DeepSeek"];
  const providers = names.map((displayName) => {
    const provider = resource(EntityKind.PROVIDER, { name: displayName, authentication: "bearer", protocol: "openai-chat", endpoint: "https://api.example.test/v1" });
    return { ...value.providerOption, providerId: provider.id, provider, displayName };
  });
  const props = { providers: [], eligibleProviders: providers };
  const view = render(<StrictMode>{value.view(value.settings(AccountSettingsSection.Api, props))}</StrictMode>);
  fireEvent.click(screen.getByRole("button", { name: "Add AI API key" }));
  expect(screen.getByRole("heading", { name: "Choose an API provider" })).toBe(window.document.activeElement);
  const choices = window.document.querySelector(".account-provider-choices")!;
  expect(Array.from(choices.querySelectorAll("button strong"), (element) => element.textContent)).toEqual(names);
  expect(screen.queryByRole("searchbox")).toBeNull();
  expect(screen.queryByRole("radio")).toBeNull();
  expect(screen.queryByRole("button", { name: /Continue|More providers|Next page/ })).toBeNull();
  expect(screen.queryByText(/Step [12] of/)).toBeNull();
  for (const button of choices.querySelectorAll("button")) {
    expect(button.getAttribute("type")).toBe("button");
    expect(button.hasAttribute("aria-pressed")).toBe(false);
    expect(button.hasAttribute("aria-selected")).toBe(false);
  }
  expect(value.save).not.toHaveBeenCalled();
  expect(value.connect).not.toHaveBeenCalled();
  expect(value.other).not.toHaveBeenCalled();
  const button = screen.getByRole("button", { name: "OpenRouter API key" });
  fireEvent.click(button);
  fireEvent.click(button);
  expect(screen.getByRole("heading", { name: "Connect your entry" })).toBe(window.document.activeElement);
  expect(screen.getByText("OpenRouter", { selector: "strong" })).toBeTruthy();
  view.rerender(<StrictMode>{value.view(value.settings(AccountSettingsSection.Api, props))}</StrictMode>);
  fireEvent.click(screen.getByRole("button", { name: "Change" }));
  expect(screen.getByRole("button", { name: "OpenRouter API key" })).toBe(window.document.activeElement);
  fireEvent.click(screen.getByRole("button", { name: "OpenRouter API key" }));
  fireEvent.click(screen.getByRole("button", { name: "Change" }));
  expect(screen.getByRole("button", { name: "OpenRouter API key" })).toBe(window.document.activeElement);
  expect(value.other).not.toHaveBeenCalled();
  expect(value.save).not.toHaveBeenCalled();
  expect(value.connect).not.toHaveBeenCalled();
});

it.each([
  { label: "loading", picker: { loaded: false, ready: false, fetching: true }, message: "Loading providers…", next: false, first: false, open: false, retry: false },
  { label: "permission", picker: { loaded: false, ready: false, error: new ConnectError("Denied", Code.PermissionDenied) }, message: "Provider inventory access is denied", next: false, first: false, open: false, retry: true },
  { label: "unavailable", picker: { loaded: false, ready: false, error: new ConnectError("Failed", Code.Unavailable) }, message: "The DeliDev request could not complete", next: false, first: false, open: false, retry: true },
  { label: "empty first", picker: {}, message: "Enable an API provider", next: false, first: false, open: true, retry: false },
  { label: "empty with continuation", picker: { nextPageToken: "page-2" }, message: "No enabled API providers on this page", next: true, first: false, open: false, retry: false },
  { label: "empty later", picker: { pageToken: "page-2" }, message: "No enabled API providers on this page", next: false, first: true, open: false, retry: false },
  { label: "missing capabilities", picker: { ready: false }, message: "Update the server", next: false, first: false, open: false, retry: false },
])("distinguishes $label provider state without writes or invented empty success", ({ picker, message, next, first, open, retry }) => {
  const value = fixture();
  const retryRead = vi.fn();
  render(value.view(value.settings(AccountSettingsSection.Api, { eligibleProviders: [], providerPicker: { ready: true, loaded: true, fetching: false, pageToken: "", nextPageToken: "", retry: retryRead, next: vi.fn(), first: vi.fn(), ...picker } })));
  fireEvent.click(screen.getByRole("button", { name: "Add AI API key" }));
  expect(screen.getByText(new RegExp(message))).toBeTruthy();
  expect(Boolean(screen.queryByRole("button", { name: "Load more Provider pages" }))).toBe(next);
  expect(screen.queryByRole("button", { name: "First page" })).toBeNull();
  expect(Boolean(screen.queryByRole("button", { name: "Open API Providers" }))).toBe(open);
  if (retry) { fireEvent.click(screen.getByRole("button", { name: "Retry providers" })); expect(retryRead).toHaveBeenCalledTimes(1); }
  expect(value.save).not.toHaveBeenCalled();
  expect(value.connect).not.toHaveBeenCalled();
});

it("labels stale provider results and excludes disabled, unsaved and subscription choices", () => {
  const value = fixture();
  const invalid = [
    { ...value.providerOption, providerId: newRequestId(), enabled: false },
    { ...value.providerOption, providerId: "" },
    { ...value.providerOption, providerId: newRequestId(), provider: resource(EntityKind.PROVIDER, { protocol: "native-subscription", authentication: "subscription" }) },
  ];
  render(value.view(value.settings(AccountSettingsSection.Api, { eligibleProviders: [value.providerOption, ...invalid], providerPicker: { ready: true, loaded: true, fetching: false, error: new ConnectError("Unavailable", Code.Unavailable), pageToken: "", nextPageToken: "", retry: vi.fn(), next: vi.fn(), first: vi.fn() } })));
  fireEvent.click(screen.getByRole("button", { name: "Add AI API key" }));
  expect(screen.getByText(/last successfully loaded providers/)).toBeTruthy();
  expect(window.document.querySelectorAll(".account-provider-action")).toHaveLength(1);
  expect(screen.queryByRole("button", { name: "Open API Providers" })).toBeNull();
});

it("labels API entry navigation and omits empty subscription pagination", async () => {
  const value = fixture();
  const view = render(value.view(value.settings(AccountSettingsSection.Api)));
  expect(await screen.findByRole("heading", { name: "No AI API key entries" })).toBeTruthy();
  expect(screen.getByRole("region", { name: "AI API Keys settings" })).toBeTruthy();
  expect(screen.queryByRole("combobox", { name: "Filter entries by provider" })).toBeNull();
  expect(screen.queryByRole("navigation", { name: "Entry pages" })).toBeNull();
  view.rerender(value.view(value.settings(AccountSettingsSection.Subscription)));
  expect(await screen.findByRole("heading", { name: "No subscriptions yet" })).toBeTruthy();
  const advanced = screen.getByText("Advanced settings").closest("details")!;
  expect(advanced.open).toBe(false); advanced.open = true;
  expect(screen.queryByRole("combobox", { name: "Filter accounts by provider" })).toBeNull();
  expect(screen.queryByRole("navigation", { name: "Account pages" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Add AI API key" })).toBeNull();
});

it("retains API entries during failed refreshes and labels initial loading without an empty success", async () => {
  const entry = resource(EntityKind.ACCOUNT, { alias: "Retained alias", type: "api", provider_id: newRequestId(), enabled: true });
  const value = fixture({ resources: [entry] });
  let release!: (value: { resources: Resource[] }) => void;
  value.list.mockImplementationOnce(() => new Promise((resolve) => { release = resolve; }));
  render(value.view(value.settings(AccountSettingsSection.Api)));
  expect(await screen.findByText("Loading entries…")).toBeTruthy();
  expect(screen.queryByText(/No AI API key entries/)).toBeNull();
  release({ resources: [entry] });
  await screen.findByRole("heading", { name: "Retained alias" });
  value.list.mockRejectedValueOnce(new ConnectError("fixture refresh failure", Code.Unavailable));
  await value.client.invalidateQueries();
  expect(await screen.findByText("Refresh failed. Showing the last successfully loaded entries.")).toBeTruthy();
  expect(screen.getByRole("heading", { name: "Retained alias" })).toBeTruthy();
  expect(screen.queryByText(/No AI API key entries/)).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "More actions for Retained alias" }));
  expect(screen.getByRole("button", { name: "Delete entry" })).toBeTruthy();
});

it("offers a local endpoint retry after a keyless connection failure without requesting a key", async () => {
  const providerId = newRequestId();
  const provider = create(ResourceSchema, { ...resource(EntityKind.PROVIDER, { name: "Loopback", protocol: "openai-chat", authentication: "keyless", endpoint: "http://127.0.0.1:11434/v1" }), id: providerId });
  const summary = { providerId, displayName: "Loopback", enabled: true, provider, keyGuidance: "", documentationUrl: "" } satisfies AccountProviderSummary;
  const entry = resource(EntityKind.ACCOUNT, { alias: "Local endpoint", provider_id: providerId, type: "api", api_protocol: "openai-chat", enabled: true, health: "disconnected" }, 3n);
  const value = fixture({ resources: [provider], providerId, save: async (request) => ({ resource: entry, requestId: requestId(request) }), connect: async () => { throw new ConnectError("fixture endpoint failure", Code.InvalidArgument); } });
  render(value.view(value.settings(AccountSettingsSection.Api, { providers: [summary], eligibleProviders: [summary] })));
  fireEvent.click(screen.getByRole("button", { name: "Add AI API key" }));
  fireEvent.click(screen.getByRole("button", { name: "Loopback Local endpoint" }));
  expect(screen.getByRole("heading", { name: "Connect your entry" })).toBeTruthy();
  await chooseAPIFormat();
  fireEvent.change(screen.getByLabelText("Entry name"), { target: { value: "Local endpoint" } });
  fireEvent.click(screen.getByRole("button", { name: "Add and connect" }));
  await screen.findByText("Entry created; connection failed. Retry the local endpoint connection when ready.");
  expect(screen.queryByLabelText("API key")).toBeNull();
  expect(screen.queryByText(/Re-enter the key/)).toBeNull();
  expect(screen.getByRole("button", { name: "Connect local endpoint" })).toBeTruthy();
  expect(value.connect.mock.calls[0][0]).toMatchObject({ mutation: { id: entry.id, expectedRevision: 3n }, keyless: true, apiKey: new Uint8Array() });
});

it("shows one compact empty list action and no API inventory controls", async () => {
  const value = fixture();
  render(value.view(value.settings(AccountSettingsSection.Api)));
  const empty = await screen.findByRole("heading", { name: "No AI API key entries" });
  expect(screen.getAllByRole("heading", { level: 1, name: "AI API Keys" })).toHaveLength(1);
  expect(screen.getAllByRole("button", { name: "Add AI API key" })).toHaveLength(1);
  expect(empty.closest("section")?.querySelector(".settings-empty-icon")?.getAttribute("aria-hidden")).toBe("true");
  expect(screen.queryByRole("searchbox")).toBeNull();
  expect(screen.queryByRole("combobox")).toBeNull();
  expect(screen.queryByRole("button", { name: "More provider filters" })).toBeNull();
  expect(screen.queryByRole("navigation", { name: "Entry pages" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Add AI API key" }));
  expect(screen.getAllByRole("heading", { level: 1, name: "AI API Keys" })).toHaveLength(1);
  expect(screen.getAllByRole("heading", { level: 2, name: "Add AI API key" })).toHaveLength(1);
  fireEvent.click(screen.getByRole("button", { name: "API provider API key" }));
  expect(screen.getByRole("heading", { name: "Connect your entry" })).toBe(document.activeElement);
  expect(screen.queryByRole("button", { name: "Back to provider" })).toBeNull();
  expect((screen.getByText("Where to get an API key").closest("details") as HTMLDetailsElement).open).toBe(false);
  expect((screen.getByText("Advanced preferences").closest("details") as HTMLDetailsElement).open).toBe(false);
  expect(value.save).not.toHaveBeenCalled(); expect(value.connect).not.toHaveBeenCalled();
});

it("preserves every independent row fact, full fallback identity, server order and action gates", async () => {
  const providerId = newRequestId(), missing = newRequestId();
  const value = fixture({ providerId, resources: [
    resource(EntityKind.ACCOUNT, { alias: "Personal", provider_id: providerId, type: "api", enabled: true, health: "unverified", connection: { id: newRequestId() }, quota: [] }),
    resource(EntityKind.ACCOUNT, { alias: "Work", provider_id: missing, type: "api", enabled: true, health: "disconnected", quota: [{}] }),
    create(ResourceSchema, { ...resource(EntityKind.ACCOUNT, { alias: "Disabled cleanup entry", provider_id: providerId, type: "api", enabled: false, health: "disconnected", removal: { request_id: newRequestId() }, confirmed_exhausted: true }), schemaVersion: 1 }),
    create(ResourceSchema, { ...resource(EntityKind.ACCOUNT, { alias: "Unknown schema", type: "api", provider_id: providerId }), schemaVersion: 2 }),
  ] });
  render(value.view(value.settings(AccountSettingsSection.Api)));
  await screen.findByRole("heading", { name: "Personal" });
  const rows = screen.getAllByRole("article");
  expect(rows.map((row) => row.querySelector("h2")?.textContent)).toEqual(["Personal", "Work", "Disabled cleanup entry", "Unnamed"]);
  expect(new Set(rows.map((row) => row.parentElement)).size).toBe(1);
  expect(within(rows[0]).getByText("Connected")).toBeTruthy();
  expect(within(rows[0]).getByText("unverified")).toBeTruthy();
  expect(within(rows[0]).getAllByText("Not reported").length).toBeGreaterThan(0);
  expect(within(rows[1]).getByText(`Provider unavailable · ${missing}`)).toBeTruthy();
  expect(within(rows[1]).getAllByText("Unavailable").length).toBeGreaterThan(0);
  expect(within(rows[1]).getAllByText("Unknown").length).toBeGreaterThan(0);
  expect(within(rows[2]).getByText("Credential cleanup pending")).toBeTruthy();
  expect(within(rows[2]).getByText("Disabled")).toBeTruthy();
  expect(within(rows[2]).getByText("Confirmed exhausted")).toBeTruthy();
  for (const row of rows.slice(0, 3)) {
    fireEvent.click(within(row).getByRole("button", { name: /More actions/ }));
    expect(within(row).getByRole("button", { name: "Edit preferences" })).toBeTruthy();
    expect(within(row).getByRole("button", { name: "Delete entry" })).toBeTruthy();
  }
  expect((within(rows[3]).getByRole("button", { name: "Manage connection" }) as HTMLButtonElement).disabled).toBe(true);
  expect((within(rows[3]).getByRole("button", { name: /More actions/ }) as HTMLButtonElement).disabled).toBe(true);
  expect(screen.queryByLabelText("API key")).toBeNull();
});

it.each([false, true])("distinguishes scoped empty results and empty continuation pages (%s)", async (continued) => {
  const providerId = newRequestId();
  const value = fixture({ providerId, listPage: (request) => ({ resources: [], nextPageToken: continued && !request.filter?.pageToken ? "api-page-2" : "" }) });
  render(value.view(value.settings(AccountSettingsSection.Api, { providerIdFilter: providerId, providerHint: value.providerOption, providers: [] })));
  await screen.findByText(continued ? "No entries on this page." : "No entries for this provider.");
  expect(screen.getByText("Provider: API provider")).toBeTruthy();
  expect(screen.queryByRole("heading", { name: "No AI API key entries" })).toBeNull();
  expect(screen.queryByRole("button", { name: "First page" })).toBeNull();
  if (continued) {
    fireEvent.click(screen.getByRole("button", { name: "Load more Entry pages" }));
    await waitFor(() => expect(screen.queryByRole("button", { name: "Load more Entry pages" })).toBeNull());
    expect(screen.queryByRole("button", { name: "Load more Entry pages" })).toBeNull();
    expect(value.list.mock.calls.at(-1)?.[0]).toMatchObject({ providerId, accountType: 1, filter: { pageSize: 50, pageToken: "api-page-2" } });
  } else expect(screen.queryByRole("navigation", { name: "Entry pages" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Clear provider filter" }));
  expect(value.callbacks.clearProviderFilter).toHaveBeenCalledTimes(1);
  expect(value.save).not.toHaveBeenCalled();
});

it.each([Code.Unavailable, Code.PermissionDenied])("retries only the failed account page and suppresses cached empty success (%s)", async (code) => {
  const value = fixture({ listPage: (request) => ({ resources: [], nextPageToken: request.filter?.pageToken ? "" : "api-page-2" }) });
  render(value.view(value.settings(AccountSettingsSection.Api)));
  fireEvent.click(await screen.findByRole("button", { name: "Load more Entry pages" }));
  await waitFor(() => expect(screen.queryByRole("button", { name: "Load more Entry pages" })).toBeNull());
  value.list.mockRejectedValueOnce(new ConnectError("fixture-only read failure", code));
  await value.client.invalidateQueries();
  await screen.findByText("Refresh failed. Showing the last successfully loaded entries.");
  const original = value.list.mock.calls.at(-1)?.[0];
  expect(screen.queryByText("No entries on this page.")).toBeNull();
  let release!: (value: { resources: Resource[] }) => void;
  value.list.mockImplementationOnce(() => new Promise((resolve) => { release = resolve; }));
  const retry = screen.getByRole("button", { name: "Retry entries" });
  fireEvent.click(retry);
  await waitFor(() => expect((retry as HTMLButtonElement).disabled).toBe(true));
  expect(value.list.mock.calls.at(-1)?.[0]).toEqual(original);
  release({ resources: [] });
  await screen.findByRole("heading", { name: "No AI API key entries" });
  expect(value.save).not.toHaveBeenCalled(); expect(value.connect).not.toHaveBeenCalled(); expect(value.other).not.toHaveBeenCalled();
});

it("keeps inventory loading, denial, missing capabilities and failed cached emptiness distinct", async () => {
  const value = fixture(), retry = vi.fn();
  const view = render(value.view(value.settings(AccountSettingsSection.Api, { accountTypeFilteringReady: false, accountTypeFilteringLoading: true })));
  expect(screen.getByText("Loading provider capabilities…")).toBeTruthy();
  expect(screen.queryByText(/Update the selected server/)).toBeNull();
  view.rerender(value.view(value.settings(AccountSettingsSection.Api, { accountTypeFilteringReady: false, accountTypeFilteringProblem: new ConnectError("fixture denial", Code.PermissionDenied), retryAccountCapabilities: retry })));
  expect(screen.getByText(/Entry access is denied/)).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Retry provider inventory" }));
  expect(retry).toHaveBeenCalledTimes(1);
  expect(value.list).not.toHaveBeenCalled();
  view.rerender(value.view(value.settings(AccountSettingsSection.Api, { accountTypeFilteringReady: false })));
  expect(screen.getByText(/Update the selected server/)).toBeTruthy();
  view.rerender(value.view(value.settings(AccountSettingsSection.Api)));
  await screen.findByRole("heading", { name: "No AI API key entries" });
  value.list.mockRejectedValueOnce(new ConnectError("fixture failure", Code.Unavailable));
  await value.client.invalidateQueries();
  await screen.findByRole("button", { name: "Retry entries" });
  expect(screen.queryByRole("heading", { name: "No AI API key entries" })).toBeNull();
  expect(screen.getByText(/last successfully loaded entries/)).toBeTruthy();
});

it("refreshes every reached account and usage without business mutations", async () => {
  const first = resource(EntityKind.ACCOUNT, { alias: "First key", type: "api", enabled: true });
  const second = resource(EntityKind.ACCOUNT, { alias: "Second key", type: "api", enabled: true });
  const f = fixture({ listPage: request => request.filter?.pageToken ? { resources: [second] } : { resources: [first], nextPageToken: "next-page" } });
  render(f.view(f.settings(AccountSettingsSection.Api)));
  await waitFor(() => expect(f.usage).toHaveBeenCalledTimes(1));
  fireEvent.click(screen.getByRole("button", { name: "Load more Entry pages" }));
  await screen.findByRole("heading", { name: "Second key" });
  await waitFor(() => expect(f.usage).toHaveBeenCalledTimes(2));
  const refresh = screen.getByRole("button", { name: "Refresh usage" });
  await waitFor(() => expect((refresh as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(refresh);
  await waitFor(() => expect(f.usage).toHaveBeenCalledTimes(4));
  expect(f.usage.mock.calls.map(([request]) => request.accountId)).toEqual([first.id, second.id, first.id, second.id]);
  expect(f.list.mock.calls.map(([request]) => request.filter?.pageToken)).toEqual(["", "next-page", "", "next-page"]);
  expect(f.save).not.toHaveBeenCalled(); expect(f.connect).not.toHaveBeenCalled(); expect(f.other).not.toHaveBeenCalled();
});

it("offers explicit verification guidance without promising maintenance on a compatible older server", async () => {
  const value = fixture();
  render(value.view(value.settings(AccountSettingsSection.Api)));
  fireEvent.click(screen.getByRole("button", { name: "Add AI API key" }));
  fireEvent.click(screen.getByRole("button", { name: "API provider API key" }));
  await chooseAPIFormat();
  expect(screen.getByText("After connecting, use Check again to verify authentication and refresh models without inference. Unsupported authentication remains unverified.")).toBeTruthy();
  expect(screen.queryByText(/connections are checked automatically/)).toBeNull();
  expect(value.save).not.toHaveBeenCalled();
  expect(value.connect).not.toHaveBeenCalled();
  expect(value.other).not.toHaveBeenCalled();
});

it("opens, refreshes and revisits API entries without inspecting account storage", async () => {
  const row = resource(EntityKind.ACCOUNT, { alias: "Storage-independent API", type: "api", enabled: true, health: "disconnected" });
  const value = fixture({ resources: [row] });
  const view = render(value.view(value.settings(AccountSettingsSection.Api)));
  await screen.findByRole("heading", { name: "Storage-independent API" });
  const absent = () => {
    expect(screen.queryByRole("heading", { name: "Account storage" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Refresh account storage" })).toBeNull();
    expect(view.container.querySelector(".account-storage-notice")).toBeNull();
    expect(value.doctor).not.toHaveBeenCalled();
  };
  absent();
  await act(async () => { await value.client.invalidateQueries({ refetchType: "active" }); });
  absent();
  view.rerender(value.view(value.settings(AccountSettingsSection.Api, { active: false })));
  view.rerender(value.view(value.settings(AccountSettingsSection.Api)));
  await screen.findByRole("heading", { name: "Storage-independent API" });
  absent();
  expect(screen.getByRole("button", { name: "Details" })).toBeTruthy();
  expect(screen.getByRole("button", { name: "View usage" })).toBeTruthy();
  expect(screen.getByRole("button", { name: "Manage connection" })).toBeTruthy();
  expect(screen.getByText(/Credentials are stored securely/)).toBeTruthy();
});
