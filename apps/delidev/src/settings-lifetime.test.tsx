import { create } from "@bufbuild/protobuf";
import { StrictMode, useState } from "react";
import { Code, ConnectError, createRouterTransport, type Transport } from "@connectrpc/connect";
import { TransportProvider, useQuery } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { SystemService, SystemCapability, ConfigurationQuery, ConfigurationService, EntityKind, ProviderInventoryCapability, ProviderService, ResourceQuery, ResourceSchema, ResourceService, newRequestId } from "@delinoio/delidev-api-client";
import { chooseScrollOption } from "./test-scroll-picker";
import { Settings } from "./settings";
import { SettingsOpening } from "./settings-lifetime";
import { MutationIntents, useRetainedMutation } from "./mutation";
import { encode } from "./documents";
import { LocalWorkerAction, LocalWorkerState, type ControlLocalWorker, type LocalWorkerStatus } from "./local-worker-controls";

function deferred() {
  let resolve!: () => void;
  const promise = new Promise<void>((done) => { resolve = done; });
  return { promise, resolve };
}

function fixture() {
  const capabilities = [ProviderInventoryCapability.PROVIDER_ACTIVATION, ProviderInventoryCapability.ACTIVE_API_MODEL_FILTER, ProviderInventoryCapability.ACCOUNT_PROVIDER_FILTER, ProviderInventoryCapability.ACCOUNT_TYPE_FILTER];
  const resources = [create(ResourceSchema, { id: newRequestId(), kind: EntityKind.PROJECT, revision: 1n, schemaVersion: 1, documentJson: encode({ name: "Sibling project" }) })];
  const provider = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.PROVIDER, revision: 1n, schemaVersion: 1, documentJson: encode({ name: "Fixture Provider", enabled: true, protocol: "openai-responses", authentication: "keyless", endpoint: "http://127.0.0.1:1234/v1" }) });
  const model = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.MODEL, revision: 1n, schemaVersion: 1, documentJson: encode({ name: "Fixture Model", native_id: "fixture-native", provider_id: provider.id }) });
  const account = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.ACCOUNT, revision: 1n, schemaVersion: 1, documentJson: encode({ alias: "Fixture account", type: "api", provider_id: provider.id, enabled: true, health: "unverified", connection: { id: newRequestId(), authentication: "keyless" } }) });
  resources.push(provider, model, account);
  const save = vi.fn((request: { kind: EntityKind; documentJson: Uint8Array }) => {
    const resource = create(ResourceSchema, { id: newRequestId(), kind: request.kind, revision: 1n, schemaVersion: 1, documentJson: request.documentJson });
    resources.push(resource);
    return { resource };
  });
  const base = createRouterTransport((router) => {
    router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1, SystemCapability.AGENT_WORKER_WIZARD_V1] }) });
    router.service(ProviderService, { listProviderInventory: () => ({ entries: [{ provider, providerId: provider.id, displayName: "Fixture Provider", enabled: true }], capabilities }), listProviderPresets: () => ({ presetsJson: encode([]) }), searchModels: () => ({ models: [model], providers: [provider] }) });
    router.service(ResourceService, { getResource: request => ({ resource: resources.find(row => row.id === request.id) }), listResources: (request) => ({ resources: resources.filter((row) => row.kind === request.filter?.kind) }) });
    router.service(ConfigurationService, { saveConfiguration: save, saveAgentWorker: request => ({ ...save({ kind: EntityKind.AGENT, documentJson: request.documentJson }), requestId: request.mutation!.requestId }) });
  });
  const waiting: { method: string; signal: AbortSignal; gate: ReturnType<typeof deferred> }[] = [];
  let delay: string | undefined, failure: Code | undefined;
  const transport: Transport = { ...base, unary: async (method, signal, ...args) => {
    if (method.name !== delay) return base.unary(method, signal, ...args);
    // The fixture deliberately commits before delaying acknowledgment, ignores
    // abort, then delivers success/error. This is accepted-server-effect evidence.
    const outcome = failure;
    const result = await base.unary(method, undefined, ...args);
    const gate = deferred();
    waiting.push({ method: method.name, signal: signal!, gate });
    await gate.promise;
    if (outcome) throw new ConnectError("Delayed fixture response", outcome);
    return result;
  } };
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity }, mutations: { retry: false, gcTime: 0 } } });
  const view = (visible: boolean, upstream = transport, controlLocalWorker?: ControlLocalWorker) => <StrictMode><TransportProvider transport={upstream}><QueryClientProvider client={client}><MutationIntents><Sibling /><Settings visible={visible} controlLocalWorker={controlLocalWorker} /></MutationIntents></QueryClientProvider></TransportProvider></StrictMode>;
  return { provider, client, save, waiting, transport, view, delay: (method?: string, code?: Code) => { delay = method; failure = code; } };
}

function Sibling() {
  const query = useQuery(ResourceQuery.listResources, { filter: { kind: EntityKind.PROJECT, pageSize: 50 } });
  const mutation = useRetainedMutation("sibling", ConfigurationQuery.saveConfiguration);
  const [composer, setComposer] = useState("");
  return <section><label>Sibling composer<textarea value={composer} onChange={(event) => setComposer(event.target.value)} /></label><span>{query.data ? "Sibling ready" : "Sibling loading"}</span><button onClick={() => void mutation.send({ kind: EntityKind.PROJECT, mutation: { requestId: newRequestId() }, documentJson: encode({ name: "Sibling write" }) })}>Sibling save</button>{mutation.uncertain ? <button onClick={mutation.retry}>Sibling retry</button> : null}</section>;
}

it.each(["", "Unsaved instructions"])("discards an Instructions form on category departure: %s", async (name) => {
  const value = fixture();
  render(value.view(true));
  await screen.findByText("Sibling ready");
  fireEvent.change(screen.getByRole("textbox", { name: "Sibling composer" }), { target: { value: "Keep sibling input" } });
  fireEvent.click(screen.getByRole("button", { name: "Instructions" }));
  fireEvent.click(screen.getByRole("button", { name: "New Instructions" }));
  const input = screen.getByRole("textbox", { name: "Name" });
  fireEvent.change(input, { target: { value: name } });
  fireEvent.click(screen.getByRole("button", { name: "Instructions" }));
  expect(screen.getByRole("textbox", { name: "Name" })).toBe(input);
  fireEvent.click(screen.getByRole("button", { name: "Projects" }));
  expect(screen.queryByRole("textbox", { name: "Name" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Instructions" }));
  expect(screen.getByRole("button", { name: "New Instructions" })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "New Instructions" }));
  expect((screen.getByRole("textbox", { name: "Name" }) as HTMLInputElement).value).toBe("");
  expect((screen.getByRole("textbox", { name: "Sibling composer" }) as HTMLTextAreaElement).value).toBe("Keep sibling input");
  expect(value.save).not.toHaveBeenCalled();
});

it.each([undefined, Code.Unavailable, Code.Canceled])("disposes a category write before its late outcome without replay: %s", async (code) => {
  const value = fixture();
  render(value.view(true));
  await screen.findByText("Sibling ready");
  const sibling = value.client.getQueryCache().getAll()[0];
  fireEvent.click(screen.getByRole("button", { name: "Instructions" }));
  fireEvent.click(screen.getByRole("button", { name: "New Instructions" }));
  fireEvent.change(screen.getByRole("textbox", { name: "Name" }), { target: { value: "Committed after departure" } });
  fireEvent.change(screen.getByRole("textbox", { name: "Instructions" }), { target: { value: "Committed content" } });
  value.delay("SaveConfiguration", code);
  fireEvent.click(screen.getByRole("button", { name: "Save Instructions" }));
  await waitFor(() => expect(value.waiting).toHaveLength(1));
  const pending = value.waiting[0];
  const oldQueries = value.client.getQueryCache().getAll().filter(query => query !== sibling);
  fireEvent.click(screen.getByRole("button", { name: "Projects" }));
  expect(pending.signal.aborted).toBe(true);
  expect(value.client.getQueryCache().getAll()).toContain(sibling);
  expect(value.client.getQueryCache().getAll().some(query => oldQueries.includes(query))).toBe(false);
  expect(value.client.getMutationCache().getAll()).toHaveLength(0);
  const focus = screen.getByRole("button", { name: "Projects" });
  focus.focus();
  value.delay();
  await act(async () => pending.gate.resolve());
  expect(document.activeElement).toBe(focus);
  expect(screen.getByRole("heading", { level: 1, name: "Projects" })).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Retry the same configuration" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Instructions" }));
  expect(await screen.findByRole("heading", { name: "Committed after departure" })).toBeTruthy();
  expect(value.save).toHaveBeenCalledTimes(1);
});

it("discards an uncertain write on category departure without a replacement mutation", async () => {
  const value = fixture();
  render(value.view(true));
  fireEvent.click(screen.getByRole("button", { name: "Instructions" }));
  fireEvent.click(screen.getByRole("button", { name: "New Instructions" }));
  fireEvent.change(screen.getByRole("textbox", { name: "Name" }), { target: { value: "Original uncertain write" } });
  fireEvent.change(screen.getByRole("textbox", { name: "Instructions" }), { target: { value: "Original bytes" } });
  value.delay("SaveConfiguration", Code.Unavailable);
  fireEvent.click(screen.getByRole("button", { name: "Save Instructions" }));
  await waitFor(() => expect(value.waiting).toHaveLength(1));
  await act(async () => value.waiting[0].gate.resolve());
  await screen.findByRole("button", { name: "Retry the same configuration" });
  fireEvent.click(screen.getByRole("button", { name: "Projects" }));
  fireEvent.click(screen.getByRole("button", { name: "Instructions" }));
  await screen.findByRole("heading", { name: "Original uncertain write" });
  expect(screen.queryByRole("button", { name: "Retry the same configuration" })).toBeNull();
  expect(value.save).toHaveBeenCalledTimes(1);
});

it.each([["navigation", "Instructions"], ["Escape then navigation", "Instructions"], ["navigation", "Agent Workers"], ["Escape then navigation", "Agent Workers"]])("starts at the first category after leaving via %s an unsaved %s editor", async (route, category) => {
  const value = fixture();
  function Harness() {
    const [visible, setVisible] = useState(false);
    return <TransportProvider transport={value.transport}><QueryClientProvider client={value.client}><button onClick={() => setVisible(true)}>Open fixture settings</button><button onClick={(event) => { event.currentTarget.focus(); setVisible(false); }}>Leave Settings fixture</button><Settings visible={visible} /></QueryClientProvider></TransportProvider>;
  }
  render(<StrictMode><Harness /></StrictMode>);
  const opener = screen.getByRole("button", { name: "Open fixture settings" });
  opener.focus();
  fireEvent.click(opener);
  expect(screen.getByRole("button", { name: "AI Subscription" }).getAttribute("aria-current")).toBe("page");
  fireEvent.click(screen.getByRole("button", { name: category }));
  fireEvent.click(await screen.findByRole("button", { name: category === "Agent Workers" ? "New Agent Worker" : "New Instructions" }));
  fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Abandoned draft" } });
  if (route === "Escape then navigation") { fireEvent.keyDown(screen.getByRole("region", { name: "Settings content" }), { key: "Escape" }); expect(screen.getByRole("region", { name: "Settings content" })).toBeTruthy(); }
  fireEvent.click(screen.getByRole("button", { name: "Leave Settings fixture" }));
  expect(document.activeElement).not.toBe(opener);
  fireEvent.click(opener);
  expect(screen.getByRole("button", { name: "AI Subscription" }).getAttribute("aria-current")).toBe("page");
  expect(screen.queryByRole("textbox", { name: "Name" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: category }));
  expect(await screen.findByRole("button", { name: category === "Agent Workers" ? "New Agent Worker" : "New Instructions" })).toBeTruthy();
  expect(value.save).not.toHaveBeenCalled();
});

it.each([undefined, Code.Unavailable, Code.Canceled])("aborts a Settings write and ignores its late outcome %s while retaining committed resources", async (code) => {
  const value = fixture();
  const view = render(value.view(true));
  await screen.findByText("Sibling ready");
  fireEvent.click(screen.getByRole("button", { name: "Instructions" }));
  fireEvent.click(await screen.findByRole("button", { name: "New Instructions" }));
  fireEvent.change(screen.getByRole("textbox", { name: "Name" }), { target: { value: "Committed instructions" } });
  fireEvent.change(screen.getByRole("textbox", { name: "Instructions" }), { target: { value: "Committed contents" } });
  value.delay("SaveConfiguration", code);
  fireEvent.click(screen.getByRole("button", { name: "Save Instructions" }));
  await waitFor(() => expect(value.waiting).toHaveLength(1));
  const pending = value.waiting[0];
  view.rerender(value.view(false));
  expect(pending.signal.aborted).toBe(true);
  expect(value.client.getQueryCache().getAll()).toHaveLength(1);
  expect(value.client.getMutationCache().getAll()).toHaveLength(0);
  value.delay();
  view.rerender(value.view(true));
  fireEvent.click(screen.getByRole("button", { name: "Projects" }));
  const focus = screen.getByRole("button", { name: "Projects" });
  focus.focus();
  await act(async () => pending.gate.resolve());
  expect(document.activeElement).toBe(focus);
  expect(screen.getByRole("heading", { level: 1, name: "Projects" })).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Retry the same configuration" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Instructions" }));
  expect(await screen.findByRole("heading", { name: "Committed instructions" })).toBeTruthy();
  expect(value.save).toHaveBeenCalledTimes(1);
});

it("aborts Settings reads without evicting sibling queries or accumulating opening caches under Strict Mode", async () => {
  const value = fixture();
  const view = render(value.view(false));
  await screen.findByText("Sibling ready");
  const sibling = value.client.getQueryCache().getAll()[0];
  for (let cycle = 0; cycle < 4; cycle++) {
    const before = value.waiting.length;
    value.delay("GetStatus");
    view.rerender(value.view(true));
    await waitFor(() => expect(value.waiting.length).toBeGreaterThan(before));
    const reads = value.waiting.filter((entry) => !entry.signal.aborted);
    view.rerender(value.view(false));
    expect(reads.every((entry) => entry.signal.aborted)).toBe(true);
    await act(async () => { for (const entry of value.waiting) entry.gate.resolve(); });
    expect(value.client.getQueryCache().getAll()).toEqual([sibling]);
    expect(value.client.getMutationCache().getAll()).toHaveLength(0);
  }
});

it.each([undefined, Code.Unavailable, Code.Canceled])("disposes an Agent opening without replaying its committed late save: %s", async (code) => {
  const value = fixture();
  const view = render(value.view(true));
  await screen.findByText("Sibling ready");
  fireEvent.change(screen.getByRole("textbox", { name: "Sibling composer" }), { target: { value: "Unsent sibling draft" } });
  const siblingQuery = value.client.getQueryCache().getAll()[0];
  fireEvent.click(screen.getByRole("button", { name: "Agent Workers" }));
  fireEvent.click(await screen.findByRole("button", { name: "New Agent Worker" }));
  await waitFor(() => expect((screen.getByRole("radio", { name: "Codex" }) as HTMLButtonElement).disabled).toBe(false));
  const next = () => fireEvent.click(screen.getByRole("button", { name: "Next" }));
  fireEvent.click(screen.getByRole("radio", { name: "Codex" }));
  await chooseScrollOption(screen.getByRole("combobox", { name: "Account source" }), `api:${value.provider.id}`);
  fireEvent.click(await screen.findByRole("checkbox", { name: /Fixture account/ })); next();
  fireEvent.change(screen.getByRole("combobox", { name: "Model" }), { target: { value: "fixture-native" } }); next();
  fireEvent.change(screen.getByRole("textbox", { name: "Name" }), { target: { value: "Committed Agent" } });
  value.delay("SaveAgentWorker", code);
  fireEvent.click(screen.getByRole("button", { name: "Save Agent Worker" }));
  await waitFor(() => expect(value.waiting).toHaveLength(1));
  const pending = value.waiting[0];
  view.rerender(value.view(false));
  expect(pending.signal.aborted).toBe(true);
  expect(value.client.getQueryCache().getAll()).toEqual([siblingQuery]);
  expect(value.client.getMutationCache().getAll()).toHaveLength(0);
  value.delay();
  view.rerender(value.view(true));
  const focus = screen.getByRole("button", { name: "AI Subscription" });
  focus.focus();
  await act(async () => pending.gate.resolve());
  expect(document.activeElement).toBe(focus);
  expect(screen.getByRole("heading", { level: 1, name: "AI Subscription" })).toBeTruthy();
  expect(screen.queryByRole("textbox", { name: "Name" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Retry the same configuration" })).toBeNull();
  expect((screen.getByRole("textbox", { name: "Sibling composer" }) as HTMLTextAreaElement).value).toBe("Unsent sibling draft");
  fireEvent.click(screen.getByRole("button", { name: "Agent Workers" }));
  expect(await screen.findByRole("heading", { name: "Committed Agent" })).toBeTruthy();
  expect(value.save).toHaveBeenCalledTimes(1);
});

it("keeps a sibling uncertain mutation when the Settings registry is discarded", async () => {
  const value = fixture();
  value.save.mockImplementationOnce(() => { throw new ConnectError("Sibling acknowledgment lost", Code.Unavailable); });
  const view = render(value.view(false));
  fireEvent.click(screen.getByRole("button", { name: "Sibling save" }));
  await screen.findByRole("button", { name: "Sibling retry" });
  view.rerender(value.view(true));
  view.rerender(value.view(false));
  fireEvent.click(screen.getByRole("button", { name: "Sibling retry" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(2));
  expect(value.save.mock.calls[1][0]).toEqual(value.save.mock.calls[0][0]);
});

it("preserves an open editor and its transport identity through a same-identity transport replacement", async () => {
  const value = fixture();
  const view = render(value.view(true));
  fireEvent.click(screen.getByRole("button", { name: "Instructions" }));
  fireEvent.click(await screen.findByRole("button", { name: "New Instructions" }));
  const name = screen.getByRole("textbox", { name: "Name" });
  fireEvent.change(name, { target: { value: "Open draft" } });
  fireEvent.change(screen.getByRole("textbox", { name: "Instructions" }), { target: { value: "Open contents" } });
  const replacementCalls = vi.fn();
  const replacement: Transport = { ...value.transport, unary: (...args) => { replacementCalls(); return value.transport.unary(...args); } };
  view.rerender(value.view(true, replacement));
  expect(screen.getByRole("textbox", { name: "Name" })).toBe(name);
  expect((name as HTMLInputElement).value).toBe("Open draft");
  fireEvent.click(screen.getByRole("button", { name: "Save Instructions" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  expect(replacementCalls).toHaveBeenCalled();
});

it("ignores a late native Worker completion without refreshing or replacing the new opening's status", async () => {
  const value = fixture(), gate = deferred();
  const initial: LocalWorkerStatus = { state: LocalWorkerState.NotStarted, machine_id: newRequestId(), controller_active: false };
  let current = initial;
  const control = vi.fn(async (action: LocalWorkerAction) => {
    if (action === LocalWorkerAction.Start) { await gate.promise; return { ...initial, state: LocalWorkerState.Running, generation: newRequestId(), controller_active: true }; }
    return current;
  });
  const view = render(value.view(true, value.transport, control));
  fireEvent.click(screen.getByRole("button", { name: "Runner Devices" }));
  const start = await screen.findByRole("button", { name: "Start local Worker" });
  await waitFor(() => expect((start as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(start);
  await waitFor(() => expect(control.mock.calls.some(([action]) => action === LocalWorkerAction.Start)).toBe(true));
  view.rerender(value.view(false, value.transport, control));
  current = { ...initial, state: LocalWorkerState.Exited };
  view.rerender(value.view(true, value.transport, control));
  fireEvent.click(screen.getByRole("button", { name: "Runner Devices" }));
  await screen.findByText("Worker controller exited. Existing session cleanup and recovery remain separate.");
  const reads = control.mock.calls.length;
  await act(async () => gate.resolve());
  expect(screen.queryByText("Worker controller running. Server connectivity and harness readiness are shown separately below.")).toBeNull();
  expect(control).toHaveBeenCalledTimes(reads);
});

it("does not start follow-up RPC or native work from a disposed opening", async () => {
  const value = fixture();
  const opening = new SettingsOpening(() => value.transport);
  const operation = vi.fn(async () => "native result");
  opening.dispose(value.client);
  await expect(opening.native(operation)).rejects.toMatchObject({ code: Code.Canceled });
  await expect(opening.transport.unary(ResourceQuery.listResources, undefined, undefined, undefined, {})).rejects.toMatchObject({ code: Code.Canceled });
  expect(operation).not.toHaveBeenCalled();
});
