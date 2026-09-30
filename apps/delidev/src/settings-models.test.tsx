// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { StrictMode, useState } from "react";
import { Code, ConnectError, createRouterTransport, type Transport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { ConfigurationService, EntityKind, ErrorDetailSchema, ListProviderInventoryResponseSchema, ProviderInventoryCapability, ProviderInventoryEntrySchema, ProviderPresetId, ProviderService, ResourceSchema, ResourceService, SearchModelsResponseSchema, UsageService, newRequestId, type ListProviderInventoryRequest, type SearchModelsRequest } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { Settings } from "./settings";

const capabilities = [ProviderInventoryCapability.PROVIDER_ACTIVATION, ProviderInventoryCapability.ACTIVE_API_MODEL_FILTER, ProviderInventoryCapability.ACCOUNT_PROVIDER_FILTER, ProviderInventoryCapability.ACCOUNT_TYPE_FILTER];
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}
function fixture() {
  const provider = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.PROVIDER, schemaVersion: 1, revision: 1n, documentJson: encode({ name: "OpenAI", enabled: true, protocol: "openai-responses", authentication: "bearer", endpoint: "https://api.openai.com/v1" }) });
  const models = [
    { name: "Example model A", native_id: "example-model-a", alias: "example-a", order: 0, new: true, hidden: false, harnesses: ["codex"] },
    { name: "Example model B", native_id: "example-model-b", alias: "", order: 0, new: false, hidden: true, harnesses: [] },
  ].map((data) => create(ResourceSchema, { id: newRequestId(), kind: EntityKind.MODEL, schemaVersion: 1, revision: 1n, documentJson: encode({ ...data, provider_id: provider.id }) }));
  const entry = create(ProviderInventoryEntrySchema, { provider, providerId: provider.id, presetId: ProviderPresetId.OPENAI, displayName: "OpenAI", enabled: true, accountCountsAvailable: true, connectedAccounts: 0n, totalAccounts: 0n });
  const inventory = vi.fn(async (_request: ListProviderInventoryRequest) => create(ListProviderInventoryResponseSchema, { entries: [entry], capabilities }));
  const search = vi.fn(async (_request: SearchModelsRequest) => create(SearchModelsResponseSchema, { models: [], providers: [provider] }));
  const save = vi.fn(async (_request: unknown) => ({ resource: models[0] }));
  const price = vi.fn(async () => ({}));
  const readPrice = vi.fn(async (_request: { modelId: string }) => ({ modelRevision: 1n }));
  const transport = createRouterTransport((router) => {
    router.service(ProviderService, {
      listProviderInventory: (request) => request.enabledOnly ? inventory(request) : { entries: [entry], capabilities },
      listProviderPresets: () => ({ presetsJson: encode([]) }), searchModels: search,
    });
    router.service(ResourceService, {
      listResources: (request) => ({ resources: request.filter?.kind === EntityKind.PROVIDER ? [provider] : [] }),
      getResource: (request) => ({ resource: [provider, ...models].find((row) => row.id === request.id) }),
    });
    router.service(ConfigurationService, { saveConfiguration: save });
    router.service(UsageService, { getModelPricing: readPrice, setModelPricing: price });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity }, mutations: { retry: false, gcTime: 0 } } });
  const view = (visible = true, upstream = transport) => <StrictMode><TransportProvider transport={upstream}><QueryClientProvider client={client}><Settings visible={visible} close={() => {}} /></QueryClientProvider></TransportProvider></StrictMode>;
  return { provider, models, entry, inventory, search, save, price, readPrice, transport, client, view };
}
function openModels() { fireEvent.click(screen.getByRole("button", { name: "Models" })); }
const searchInput = () => screen.getByRole("textbox", { name: "Search active provider models" }) as HTMLInputElement;

it("composes one Models heading/action and a neutral successful empty page with optional accounts", async () => {
  const value = fixture();
  render(value.view()); openModels();
  await screen.findByRole("heading", { name: "No models yet" });
  expect(screen.getAllByRole("heading", { name: "Models", level: 1 })).toHaveLength(1);
  expect(screen.getAllByRole("button", { name: "New Model" })).toHaveLength(1);
  expect(screen.getByText("Saved on the selected server.")).toBeTruthy();
  expect(screen.getByText("Add models manually using New Model.")).toBeTruthy();
  expect(screen.getByText("You can add models without an API account.")).toBeTruthy();
  expect(screen.getByText("Connect an account only for automatic model discovery.")).toBeTruthy();
  expect(screen.queryByRole("alert")).toBeNull();
  expect(screen.queryByRole("navigation", { name: "Model pages" })).toBeNull();
  expect(searchInput().maxLength).toBe(256);
  expect(searchInput().placeholder).toBe("Search models...");
  expect(value.inventory.mock.calls[0][0]).toMatchObject({ enabledOnly: true, pageToken: "", pageSize: 200 });
  expect(value.search.mock.calls[0][0]).toMatchObject({ query: "", pageToken: "", pageSize: 50, enabledProvidersOnly: true, includeHidden: true });
  fireEvent.click(screen.getByRole("button", { name: "New Model" }));
  expect(await screen.findByRole("heading", { name: "New Model" })).toBeTruthy();
  expect(screen.getAllByRole("heading", { name: "Models", level: 1 })).toHaveLength(1);
  expect(value.save).not.toHaveBeenCalled(); expect(value.price).not.toHaveBeenCalled();
});

it.each(["incomplete", "unavailable", "nonzero"])("does not infer zero accounts from %s inventory", async (variant) => {
  const value = fixture();
  value.inventory.mockResolvedValue(create(ListProviderInventoryResponseSchema, { entries: [create(ProviderInventoryEntrySchema, { ...value.entry, accountCountsAvailable: variant !== "unavailable", connectedAccounts: variant === "nonzero" ? 1n : 0n })], capabilities, nextPageToken: variant === "incomplete" ? "providers-page-2" : "" }));
  render(value.view()); openModels();
  await screen.findByRole("heading", { name: "No models yet" });
  expect(screen.queryByText("You can add models without an API account.")).toBeNull();
  expect((screen.getByRole("button", { name: "New Model" }) as HTMLButtonElement).disabled).toBe(false);
});

it.each(capabilities.slice(0, 3))("gates creation and model reads when required capability %s is missing", async (missing) => {
  const value = fixture();
  value.inventory.mockResolvedValue(create(ListProviderInventoryResponseSchema, { entries: [value.entry], capabilities: capabilities.filter((capability) => capability !== missing) }));
  render(value.view()); openModels();
  await screen.findByRole("alert");
  expect(screen.getByText(/Update the server before using model settings/)).toBeTruthy();
  expect((screen.getByRole("button", { name: "New Model" }) as HTMLButtonElement).disabled).toBe(true);
  expect(value.search).not.toHaveBeenCalled();
  expect(screen.queryByRole("button", { name: "Retry" })).toBeNull();
  expect(screen.queryByRole("heading", { name: "No models yet" })).toBeNull();
});

it("distinguishes no enabled providers and searched emptiness from the unfiltered empty panel", async () => {
  const value = fixture();
  value.inventory.mockResolvedValue(create(ListProviderInventoryResponseSchema, { entries: [], capabilities }));
  const view = render(value.view()); openModels();
  await screen.findByText("No API providers are enabled. Turn on a provider in API Providers.");
  expect(screen.queryByRole("heading", { name: "No models yet" })).toBeNull();
  view.unmount();
  const enabled = fixture(); render(enabled.view()); openModels();
  await screen.findByRole("heading", { name: "No models yet" });
  fireEvent.change(searchInput(), { target: { value: "missing-fixture" } });
  await screen.findByText("No models match this search.");
  expect(screen.queryByRole("heading", { name: "No models yet" })).toBeNull();
  expect(screen.queryByText("You can add models without an API account.")).toBeNull();
  expect(screen.queryByRole("navigation", { name: "Model pages" })).toBeNull();
});

it("retains pagination on an empty first page with continuation and a later empty page", async () => {
  const value = fixture();
  value.search.mockImplementation(async (request) => create(SearchModelsResponseSchema, { models: [], providers: [value.provider], nextPageToken: request.pageToken ? "" : "model-page-2" }));
  render(value.view()); openModels();
  fireEvent.click(await screen.findByRole("button", { name: "Load more" }));
  await screen.findByText("No models on this page.");
  await waitFor(() => expect((screen.getByRole("button", { name: "First page" }) as HTMLButtonElement).disabled).toBe(false));
  expect(screen.queryByRole("heading", { name: "No models yet" })).toBeNull();
  fireEvent.change(searchInput(), { target: { value: "new scope" } });
  await waitFor(() => expect(value.search.mock.calls.at(-1)?.[0]).toMatchObject({ query: "new scope", pageToken: "" }));
  expect(value.search.mock.calls.some(([request]) => request.query === "new scope" && request.pageToken !== "")).toBe(false);
  expect(value.inventory.mock.calls.every(([request]) => request.pageToken === "")).toBe(true);
});

it("shows initial inventory and model loading without successful-empty content", async () => {
  const value = fixture(), inventory = deferred<ReturnType<typeof create<typeof ListProviderInventoryResponseSchema>>>(), search = deferred<ReturnType<typeof create<typeof SearchModelsResponseSchema>>>();
  value.inventory.mockImplementation(() => inventory.promise); value.search.mockImplementation(() => search.promise);
  render(value.view()); openModels();
  await screen.findByText("Loading provider inventory…");
  expect(value.search).not.toHaveBeenCalled();
  expect(screen.queryByRole("heading", { name: "No models yet" })).toBeNull();
  await act(async () => inventory.resolve(create(ListProviderInventoryResponseSchema, { entries: [value.entry], capabilities })));
  await screen.findByText("Loading active provider models…");
  expect(screen.queryByRole("heading", { name: "No models yet" })).toBeNull();
  await act(async () => search.resolve(create(SearchModelsResponseSchema, {})));
  await screen.findByRole("heading", { name: "No models yet" });
});

it.each(["inventory", "models"] as const)("retries only the failed %s read with its exact current query/cursor", async (target) => {
  const value = fixture();
  const read = target === "inventory" ? value.inventory : value.search;
  read.mockRejectedValue(new ConnectError("Do not disclose raw fixture request", Code.Unavailable));
  render(value.view()); openModels();
  const region = await screen.findByLabelText(target === "inventory" ? "Provider inventory read failure" : "Model search read failure");
  expect(screen.queryByRole("heading", { name: "No models yet" })).toBeNull();
  expect(screen.queryByText("Do not disclose raw fixture request")).toBeNull();
  const original = read.mock.calls[0][0], otherCount = target === "inventory" ? value.search.mock.calls.length : value.inventory.mock.calls.length;
  value.inventory.mockResolvedValue(create(ListProviderInventoryResponseSchema, { entries: [value.entry], capabilities }));
  value.search.mockResolvedValue(create(SearchModelsResponseSchema, { models: [], providers: [value.provider] }));
  const before = read.mock.calls.length;
  fireEvent.click(within(region).getByRole("button", { name: "Retry" }));
  await screen.findByRole("heading", { name: "No models yet" });
  expect(read.mock.calls[before][0]).toEqual(original);
  if (target === "models") expect(value.inventory.mock.calls.length).toBe(otherCount);
  expect(value.save).not.toHaveBeenCalled(); expect(value.price).not.toHaveBeenCalled();
});

it.each([Code.PermissionDenied, Code.Unauthenticated])("does not offer transient Retry or empty success for authority failure %s", async (code) => {
  const value = fixture();
  const reference = newRequestId();
  value.search.mockRejectedValue(new ConnectError("Sanitized fixture failure", code, undefined, [{ desc: ErrorDetailSchema, value: { code: code === Code.PermissionDenied ? "permission_denied" : "unauthenticated", guidance: "Fixture authorization guidance", correlationId: reference } }]));
  render(value.view()); openModels();
  await screen.findByText("Sanitized fixture failure");
  expect(screen.getByText(`Reference: ${reference}`)).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Retry" })).toBeNull();
  expect(screen.queryByRole("heading", { name: "No models yet" })).toBeNull();
});

it("keeps cached same-page rows through refresh/failure/retry, but never displays them under a new search", async () => {
  const value = fixture();
  value.search.mockResolvedValue(create(SearchModelsResponseSchema, { models: value.models, providers: [value.provider], nextPageToken: "model-page-2" }));
  render(value.view()); openModels();
  await screen.findByRole("heading", { name: "Example model A" });
  fireEvent.click(screen.getByRole("button", { name: "Load more" }));
  await waitFor(() => expect(value.search.mock.calls).toHaveLength(2));
  const original = value.search.mock.calls[1][0];
  const refresh = deferred<ReturnType<typeof create<typeof SearchModelsResponseSchema>>>();
  value.search.mockImplementationOnce(() => refresh.promise);
  void value.client.refetchQueries({ predicate: (query) => JSON.stringify(query.queryKey).includes('"pageToken":"model-page-2"') });
  await screen.findByText("Refreshing active provider models.");
  expect(screen.getByRole("heading", { name: "Example model A" })).toBeTruthy();
  await act(async () => refresh.resolve(create(SearchModelsResponseSchema, { models: value.models, providers: [value.provider] })));
  value.search.mockRejectedValueOnce(new ConnectError("Refresh unavailable", Code.Unavailable));
  await act(async () => { await value.client.refetchQueries({ predicate: (query) => JSON.stringify(query.queryKey).includes('"pageToken":"model-page-2"') }); });
  await screen.findByText("Refresh failed. Showing the last successfully loaded results.");
  expect(screen.getByRole("heading", { name: "Example model A" })).toBeTruthy();
  const retryGate = deferred<ReturnType<typeof create<typeof SearchModelsResponseSchema>>>();
  value.search.mockImplementationOnce(() => retryGate.promise);
  const retry = screen.getByRole("button", { name: "Retry" }); fireEvent.click(retry);
  await waitFor(() => expect((retry as HTMLButtonElement).disabled).toBe(true));
  expect(value.search.mock.calls.at(-1)?.[0]).toEqual(original);
  await act(async () => retryGate.resolve(create(SearchModelsResponseSchema, { models: value.models, providers: [value.provider] })));
  const next = deferred<ReturnType<typeof create<typeof SearchModelsResponseSchema>>>(); value.search.mockImplementationOnce(() => next.promise);
  fireEvent.change(searchInput(), { target: { value: "missing-fixture" } });
  await screen.findByText("Loading active provider models…");
  expect(screen.queryByRole("heading", { name: "Example model A" })).toBeNull();
  await act(async () => next.resolve(create(SearchModelsResponseSchema, {})));
  await screen.findByText("No models match this search.");
  expect(value.save).not.toHaveBeenCalled(); expect(value.price).not.toHaveBeenCalled();
});

it("preserves every model identity/status/metadata and both original resource actions", async () => {
  const value = fixture();
  value.search.mockResolvedValue(create(SearchModelsResponseSchema, { models: [...value.models, create(ResourceSchema, { ...value.models[0], id: newRequestId(), schemaVersion: 2 })], providers: [value.provider] }));
  render(value.view()); openModels();
  const group = await screen.findByRole("region", { name: "Models from OpenAI" });
  const rows = within(group).getAllByRole("article");
  expect(rows.map((row) => row.querySelector("h4")?.textContent)).toEqual(["Example model A", "Example model B"]);
  for (const text of ["Native ID: example-model-a", "CLI alias: example-a", "Configured harnesses: codex", "NEW", "Visible"]) expect(within(rows[0]).getByText(text)).toBeTruthy();
  for (const text of ["Native ID: example-model-b", "CLI alias: None", "Configured harnesses: None", "Reviewed", "Hidden"]) expect(within(rows[1]).getByText(text)).toBeTruthy();
  expect(within(screen.getAllByRole("article")[2]).getAllByRole("button").every((button) => (button as HTMLButtonElement).disabled)).toBe(true);
  fireEvent.click(within(rows[0]).getByRole("button", { name: "Edit model" }));
  expect((await screen.findByRole("textbox", { name: "Display name" }) as HTMLInputElement).value).toBe("Example model A");
  fireEvent.click(screen.getByRole("button", { name: "Cancel edit" }));
  fireEvent.click(within((await screen.findByRole("region", { name: "Models from OpenAI" })).querySelectorAll("article")[1]).getByRole("button", { name: "Token pricing" }));
  await screen.findByRole("heading", { name: "Token pricing · Example model B" });
  await waitFor(() => expect(value.readPrice).toHaveBeenCalledWith(expect.objectContaining({ modelId: value.models[1].id }), expect.anything()));
  expect(value.save).not.toHaveBeenCalled(); expect(value.price).not.toHaveBeenCalled();
});

it("retains list state across edit/pricing/category/responsive/reconnect transitions in one opening", async () => {
  const value = fixture();
  value.search.mockResolvedValue(create(SearchModelsResponseSchema, { models: value.models, providers: [value.provider], nextPageToken: "model-page-2" }));
  const view = render(value.view()); openModels();
  await screen.findByRole("heading", { name: "Example model A" });
  fireEvent.change(searchInput(), { target: { value: "Example" } });
  fireEvent.click(await screen.findByRole("button", { name: "Load more" }));
  await waitFor(() => expect(value.search.mock.calls.at(-1)?.[0]).toMatchObject({ query: "Example", pageToken: "model-page-2" }));
  fireEvent.click(screen.getAllByRole("button", { name: "Edit model" })[0]);
  fireEvent.click(screen.getByRole("button", { name: "Cancel edit" }));
  expect(searchInput().value).toBe("Example");
  await waitFor(() => expect((screen.getByRole("button", { name: "First page" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getAllByRole("button", { name: "Token pricing" })[0]);
  await screen.findByText("No pricing basis has been configured. Earlier responses stay unpriced.");
  fireEvent.click(screen.getByRole("button", { name: "Back to Models" }));
  await screen.findByRole("heading", { name: "Example model A" });
  await waitFor(() => expect(value.client.isFetching()).toBe(0));
  await waitFor(() => expect(screen.queryByText("Refreshing active provider models.")).toBeNull());
  fireEvent.click(screen.getByRole("button", { name: "Instructions" }));
  await act(async () => { await Promise.resolve(); });
  const calls = value.search.mock.calls.length;
  await act(async () => { await value.client.invalidateQueries({ refetchType: "active" }); });
  expect(value.search.mock.calls.length).toBe(calls);
  const replacement = vi.fn();
  const upstream: Transport = { ...value.transport, unary: (...args) => { replacement(); return value.transport.unary(...args); } };
  view.rerender(value.view(true, upstream));
  fireEvent.change(screen.getByRole("combobox", { name: "Settings category" }), { target: { value: "models" } });
  expect(searchInput().value).toBe("Example");
  await waitFor(() => expect(value.search.mock.calls.at(-1)?.[0]).toMatchObject({ query: "Example", pageToken: "model-page-2" }));
  expect(replacement).toHaveBeenCalled();
});

it.each(["button", "cancel"])("discards Models search/page and read Retry on close via %s, restoring the opener", async (route) => {
  const value = fixture();
  value.search.mockImplementation(async (request) => {
    if (request.pageToken) throw new ConnectError("Later page temporarily unavailable", Code.Unavailable);
    return create(SearchModelsResponseSchema, { models: value.models, providers: [value.provider], nextPageToken: "model-page-2" });
  });
  function Harness() {
    const [visible, setVisible] = useState(false);
    return <TransportProvider transport={value.transport}><QueryClientProvider client={value.client}><button onClick={() => setVisible(true)}>Open Settings fixture</button><Settings visible={visible} close={() => setVisible(false)} /></QueryClientProvider></TransportProvider>;
  }
  render(<StrictMode><Harness /></StrictMode>);
  const opener = screen.getByRole("button", { name: "Open Settings fixture" }); opener.focus(); fireEvent.click(opener); openModels();
  await screen.findByRole("heading", { name: "Example model A" });
  fireEvent.change(searchInput(), { target: { value: "Example" } });
  fireEvent.click(await screen.findByRole("button", { name: "Load more" }));
  await screen.findByRole("button", { name: "Retry" });
  if (route === "cancel") fireEvent(screen.getByRole("dialog"), new Event("cancel", { bubbles: true, cancelable: true }));
  else fireEvent.click(screen.getByRole("button", { name: "Close Settings" }));
  expect(document.activeElement).toBe(opener);
  fireEvent.click(opener);
  expect(screen.getByRole("heading", { name: "AI Subscription", level: 1 })).toBeTruthy(); openModels();
  await screen.findByRole("heading", { name: "Example model A" });
  expect(searchInput().value).toBe("");
  expect((screen.getByRole("button", { name: "First page" }) as HTMLButtonElement).disabled).toBe(true);
  expect(screen.queryByRole("button", { name: "Retry" })).toBeNull();
  expect(value.search.mock.calls.at(-1)?.[0]).toMatchObject({ query: "", pageToken: "" });
  expect(value.save).not.toHaveBeenCalled(); expect(value.price).not.toHaveBeenCalled();
});

it("retains the original uncertain model write and list scope through reconnect without granting navigation", async () => {
  const value = fixture();
  value.search.mockResolvedValue(create(SearchModelsResponseSchema, { models: value.models, providers: [value.provider], nextPageToken: "model-page-2" }));
  value.save.mockRejectedValue(new ConnectError("Lost model acknowledgment", Code.Unavailable));
  const view = render(value.view()); openModels();
  await screen.findByRole("heading", { name: "Example model A" });
  fireEvent.change(searchInput(), { target: { value: "Example" } });
  fireEvent.click(await screen.findByRole("button", { name: "Load more" }));
  await waitFor(() => expect(value.search.mock.calls.at(-1)?.[0].pageToken).toBe("model-page-2"));
  fireEvent.click(screen.getAllByRole("button", { name: "Edit model" })[0]);
  fireEvent.change(screen.getByRole("textbox", { name: "Display name" }), { target: { value: "Retained edit" } });
  fireEvent.click(screen.getByRole("button", { name: "Save Model" }));
  await screen.findByRole("button", { name: "Retry the same configuration" });
  expect((screen.getByRole("button", { name: "Instructions" }) as HTMLButtonElement).disabled).toBe(true);
  expect((screen.getByRole("combobox", { name: "Settings category" }) as HTMLSelectElement).disabled).toBe(true);
  const original = value.save.mock.calls[0][0];
  const upstream: Transport = { ...value.transport, unary: (...args) => value.transport.unary(...args) };
  view.rerender(value.view(true, upstream));
  expect((screen.getByRole("textbox", { name: "Display name" }) as HTMLInputElement).value).toBe("Retained edit");
  value.save.mockResolvedValue({ resource: value.models[0] });
  fireEvent.click(screen.getByRole("button", { name: "Retry the same configuration" }));
  await screen.findByRole("heading", { name: "Example model A" });
  expect(value.save.mock.calls[1][0]).toEqual(original);
  expect(searchInput().value).toBe("Example");
  await waitFor(() => expect((screen.getByRole("button", { name: "First page" }) as HTMLButtonElement).disabled).toBe(false));
  expect(value.price).not.toHaveBeenCalled();
});

it("ignores a disposed Models read's late result without changing replacement focus or rows", async () => {
  const value = fixture();
  const gate = deferred<void>();
  const waits: AbortSignal[] = [];
  let delayed = true;
  value.search.mockResolvedValue(create(SearchModelsResponseSchema, { models: value.models, providers: [value.provider] }));
  const upstream: Transport = { ...value.transport, unary: async (method, signal, ...args) => {
    if (method.name !== "SearchModels" || !delayed) return value.transport.unary(method, signal, ...args);
    const response = await value.transport.unary(method, undefined, ...args);
    waits.push(signal!);
    await gate.promise;
    return response;
  } };
  const view = render(value.view(true, upstream)); openModels();
  await waitFor(() => expect(waits.length).toBeGreaterThan(0));
  view.rerender(value.view(false, upstream));
  expect(waits.every((signal) => signal.aborted)).toBe(true);
  delayed = false;
  value.search.mockResolvedValue(create(SearchModelsResponseSchema, {}));
  view.rerender(value.view(true, upstream)); openModels();
  await screen.findByRole("heading", { name: "No models yet" });
  const focus = searchInput(); focus.focus();
  await act(async () => gate.resolve());
  expect(document.activeElement).toBe(focus);
  expect(screen.queryByRole("heading", { name: "Example model A" })).toBeNull();
  expect(value.save).not.toHaveBeenCalled(); expect(value.price).not.toHaveBeenCalled();
});

it("labels native-name fallback and keeps model rows when only inventory refresh fails", async () => {
  const value = fixture();
  const fallback = create(ResourceSchema, { ...value.models[0], documentJson: encode({ name: "", native_id: "example-native-fallback", provider_id: value.provider.id }) });
  value.search.mockResolvedValue(create(SearchModelsResponseSchema, { models: [fallback], providers: [value.provider] }));
  render(value.view()); openModels();
  await screen.findByRole("heading", { name: "example-native-fallback" });
  value.inventory.mockRejectedValue(new ConnectError("Inventory refresh lost", Code.Unavailable));
  await act(async () => { await value.client.refetchQueries({ predicate: (query) => JSON.stringify(query.queryKey).includes('"enabledOnly":true') }); });
  await screen.findByText("Provider refresh failed. Showing the last successfully loaded provider state.");
  expect(screen.getByRole("heading", { name: "example-native-fallback" })).toBeTruthy();
  expect(screen.getByText("Refresh failed. Showing the last successfully loaded results.")).toBeTruthy();
  const count = value.search.mock.calls.length;
  value.inventory.mockResolvedValue(create(ListProviderInventoryResponseSchema, { entries: [value.entry], capabilities }));
  fireEvent.click(within(screen.getByLabelText("Provider inventory read failure")).getByRole("button", { name: "Retry" }));
  await waitFor(() => expect(screen.queryByLabelText("Provider inventory read failure")).toBeNull());
  expect(value.search.mock.calls.length).toBe(count);
  expect(value.save).not.toHaveBeenCalled(); expect(value.price).not.toHaveBeenCalled();
});

it.each(["unfiltered", "search", "later", "providers"].flatMap((scope) => ["inventory", "models"].map((read) => ({ scope, read }))))("retains cached $scope emptiness when the $read refresh fails", async ({ scope, read }) => {
  const value = fixture();
  const entries = scope === "providers" ? [] : [value.entry];
  value.inventory.mockResolvedValue(create(ListProviderInventoryResponseSchema, { entries, capabilities }));
  value.search.mockImplementation(async (request) => create(SearchModelsResponseSchema, { models: [], providers: [value.provider], nextPageToken: scope === "later" && !request.pageToken ? "model-page-2" : "" }));
  render(value.view()); openModels();
  const message = scope === "providers" ? "No API providers are enabled. Turn on a provider in API Providers." : scope === "search" ? "No models match this search." : scope === "later" ? "No models on this page." : "No models yet";
  if (scope !== "providers") await screen.findByRole("heading", { name: "No models yet" });
  if (scope === "search") fireEvent.change(searchInput(), { target: { value: "missing-fixture" } });
  if (scope === "later") fireEvent.click(await screen.findByRole("button", { name: "Load more" }));
  await screen.findByText(message);
  if (read === "inventory") value.inventory.mockRejectedValue(new ConnectError("Cached inventory refresh failed", Code.Unavailable));
  else value.search.mockRejectedValue(new ConnectError("Cached model refresh failed", Code.Unavailable));
  const otherReads = read === "inventory" ? value.search.mock.calls.length : value.inventory.mock.calls.length;
  await act(async () => { await value.client.refetchQueries({ predicate: (query) => JSON.stringify(query.queryKey).includes(read === "inventory" ? '\"enabledOnly\":true' : '\"enabledProvidersOnly\":true') }); });
  await screen.findByText("Refresh failed. Showing the last successfully loaded results.");
  expect(screen.getByText(message)).toBeTruthy();
  expect(screen.getByLabelText(read === "inventory" ? "Provider inventory read failure" : "Model search read failure")).toBeTruthy();
  if (scope === "later") expect((screen.getByRole("button", { name: "First page" }) as HTMLButtonElement).disabled).toBe(false);
  else expect(screen.queryByRole("navigation", { name: "Model pages" })).toBeNull();
  expect(read === "inventory" ? value.search.mock.calls.length : value.inventory.mock.calls.length).toBe(otherReads);
  expect(value.save).not.toHaveBeenCalled(); expect(value.price).not.toHaveBeenCalled();
});
