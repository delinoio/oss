// SPDX-License-Identifier: Apache-2.0
// Protocol 2 replaces the direct Model/accountless editor with inline source
// routes. Keep the original disclosure, draft, revision and receipt guards on
// the actual owning wizard rather than supplying retired Model resources.
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { StrictMode } from "react";
import { expect, it, vi } from "vitest";
import { ConfigurationService, SystemService, SystemCapability, EntityKind, ProviderInventoryCapability, ProviderService, ResourceSchema, ResourceService, newRequestId, type Resource, type SaveAgentWorkerRequest } from "@delinoio/delidev-api-client";
import { document as resourceDocument, encode, type Document } from "./documents";
import { MutationIntents } from "./mutation";
import { AgentWorkerWizard } from "./agent-worker-wizard";
import { chooseScrollOption, scrollChoiceValue } from "./test-scroll-picker";

function resource(kind: EntityKind, data: Document, revision = 1n) {
  return create(ResourceSchema, { id: newRequestId(), kind, revision, schemaVersion: kind === EntityKind.AGENT ? 4 : kind === EntityKind.PROVIDER || kind === EntityKind.ACCOUNT ? 3 : 1, documentJson: encode(data) });
}
function fixture() {
  const provider = resource(EntityKind.PROVIDER, { name: "Fixture provider", enabled: true, api_formats: ["openai-responses", "anthropic-messages", "openai-chat"].map(protocol => ({ protocol, endpoint: "https://fixture.test/v1", authentication: "keyless" })) });
  const accounts = ["First account", "Second account"].map(alias => resource(EntityKind.ACCOUNT, { alias, type: "api", provider_id: provider.id, api_protocol: "openai-responses", health: "ready", enabled: true, connection: { id: newRequestId(), authentication: "keyless" } }));
  const templates = [resource(EntityKind.TEMPLATE, { name: "First instructions" }), resource(EntityKind.TEMPLATE, { name: "Second instructions" })];
  const resources = [provider, ...accounts, ...templates];
  const save = vi.fn(async (request: SaveAgentWorkerRequest) => ({ requestId: request.mutation!.requestId, resource: create(ResourceSchema, { ...resource(EntityKind.AGENT, decoded(request)), id: request.mutation!.id || newRequestId() }) }));
  const list = vi.fn(async (kind: EntityKind, _page: string): Promise<{ resources: Resource[]; nextPageToken?: string }> => ({ resources: resources.filter(row => row.kind === kind) }));
  const search = vi.fn(() => ({ models: [] })); // A retired registry read must never occur.
  const get = vi.fn((id: string) => ({ resource: resources.find(row => row.id === id) }));
  const inventory = vi.fn(() => ({ entries: [{ providerId: provider.id, provider, displayName: "Fixture provider", enabled: true }], capabilities: [ProviderInventoryCapability.PROVIDER_ACTIVATION, ProviderInventoryCapability.ACCOUNT_PROVIDER_FILTER, ProviderInventoryCapability.ACCOUNT_TYPE_FILTER] }));
  const transport = createRouterTransport(router => {
    router.service(ResourceService, { listResources: request => list(request.filter?.kind ?? EntityKind.UNSPECIFIED, request.filter?.pageToken ?? ""), getResource: request => get(request.id) });
    router.service(ProviderService, { listProviderInventory: inventory, searchModels: search });
    router.service(ConfigurationService, { saveAgentWorker: save });
    router.service(SystemService, { getStatus: () => ({ protocolVersion: 2, capabilities: [SystemCapability.INLINE_WORKER_MODELS_V1, SystemCapability.CODEX_SUBAGENT_CONFIGURATION_V1] }) });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity }, mutations: { retry: false, gcTime: 0 } } });
  const saved = vi.fn(), cancel = vi.fn();
  const view = (initial?: Resource, active = true) => <StrictMode><TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><AgentWorkerWizard initial={initial} active={active} saved={saved} cancel={cancel} /></MutationIntents></QueryClientProvider></TransportProvider></StrictMode>;
  const model = { provider_id: provider.id, native_id: "fixture-native", metadata_source: "unknown", input_modalities: ["text"] };
  const data = (extra: Document = {}): Document => ({ name: "Original", harness: "codex", routes: [{ model, accounts: [{ id: accounts[0].id, weight: 1 }], routing: "priority" }], templates: [], options: { permission: "default" }, ...extra });
  return { provider, model, accounts, templates, resources, save, list, search, inventory, get, client, view, data, saved, cancel };
}
function disclosure(title: string) { return Array.from(document.querySelectorAll<HTMLDetailsElement>(".agent-disclosure")).find(section => section.querySelector(".agent-section-title")?.textContent?.startsWith(title))!; }
function toggle(section: HTMLDetailsElement, open: boolean) { section.open = open; fireEvent(section, new Event("toggle")); }
function decoded(request: { documentJson: Uint8Array }) { return JSON.parse(new TextDecoder().decode(request.documentJson)); }
function change(label: string, value: string) { fireEvent.change(screen.getByLabelText(label), { target: { value } }); }
async function choose(label: string, id: string) { await chooseScrollOption(screen.getByRole("combobox", { name: label }), id); }
async function settled(value: ReturnType<typeof fixture>) { await waitFor(() => expect(value.client.isFetching()).toBe(0), { timeout: 5000 }); }
async function proved(value: ReturnType<typeof fixture>, rows = [value.accounts[0]]) {
  await waitFor(() => { for (const row of rows) expect(value.client.getQueryCache().getAll().some(query => (query.state.data as { resource?: Resource } | undefined)?.resource?.id === row.id && query.state.status === "success" && query.state.fetchStatus === "idle")).toBe(true); });
  await act(async () => {});
}
async function accountsStep(value: ReturnType<typeof fixture>, initial?: Resource) {
  fireEvent.click(await screen.findByRole("radio", { name: ({ "claude-code": "Claude Code", "opencode": "OpenCode", "grok-build": "Grok Build" } as Record<string, string>)[String(resourceDocument(initial).harness)] || "Codex" }));
  if (!initial) await choose("Account source 1", `api:${value.provider.id}`);
  await settled(value);
}
async function configure(value: ReturnType<typeof fixture>, initial?: Resource) {
  await accountsStep(value, initial);
  if (!initial) fireEvent.click(await screen.findByRole("checkbox", { name: /First account/ }));
  await screen.findByRole("checkbox", { name: "Select First account" }); await proved(value); await settled(value);
  fireEvent.click(screen.getByRole("button", { name: "Next" }));
  const model = await screen.findByRole("combobox", { name: "Model for Fixture provider" });
  if (!initial) fireEvent.change(model, { target: { value: value.model.native_id } });
  fireEvent.click(screen.getByRole("button", { name: "Next" }));
  await screen.findByRole("heading", { name: "Configure", level: 3 }); await settled(value);
  expect(value.search).not.toHaveBeenCalled();
}
async function save(value: ReturnType<typeof fixture>) { fireEvent.click(screen.getByRole("button", { name: "Save Agent Worker" })); await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1)); }
function initial(value: ReturnType<typeof fixture>, data: Document, revision = 9n) { const row = resource(EntityKind.AGENT, data, revision); value.resources.push(row); return row; }

it("requires an original account source and saves inline defaults without a Model registry", async () => {
  const value = fixture(); render(value.view()); await accountsStep(value);
  fireEvent.click(screen.getByRole("button", { name: "Next" })); expect(screen.getByRole("heading", { name: "Accounts", level: 3 })).toBeTruthy(); expect(value.save).not.toHaveBeenCalled();
  fireEvent.click(await screen.findByRole("checkbox", { name: /First account/ })); await proved(value); await settled(value);
  fireEvent.click(screen.getByRole("button", { name: "Next" })); change("Model for Fixture provider", "fixture-native"); fireEvent.click(screen.getByRole("button", { name: "Next" }));
  expect(Array.from(document.querySelectorAll(".agent-section-title"), title => title.textContent)).toEqual(["Reasoning", "Instructions", "Native harness options"]);
  expect(Array.from(document.querySelectorAll<HTMLDetailsElement>(".agent-disclosure")).every(section => !section.open)).toBe(true);
  expect(Array.from(document.querySelectorAll(".agent-core .agent-required"), marker => marker.parentElement?.textContent)).toEqual(["Name *"]);
  change("Name", "Minimal agent"); await save(value);
  expect(decoded(value.save.mock.calls[0][0])).toMatchObject({ name: "Minimal agent", harness: "codex", routes: [{ model: value.model, accounts: [{ id: value.accounts[0].id, weight: 1 }], routing: "priority" }], templates: [], options: { permission: "default" } });
  expect(value.save.mock.calls[0][0]).toMatchObject({ schemaVersion: 4, mutation: { expectedRevision: 0n }, routeModels: [{ selection: { case: "nativeId", value: "fixture-native" } }] });
  expect(decoded(value.save.mock.calls[0][0]).model_id).toBeUndefined(); expect(value.search).not.toHaveBeenCalled();
});
it("changes only Name while preserving the complete document, exact revision and mounted selectors", async () => {
  const value = fixture(); const data = value.data({ effort: "high", templates: value.templates.map(row => row.id), future_field: { retained: true }, options: { permission: "workspace-write", subagent_model: "child", subagent_effort: "medium", max_concurrency: 4, approval_policy: "on-request", approval_review_model: "reviewer", service_tier: "priority", future_option: "retained" } });
  const row = initial(value, data); render(value.view(row)); await configure(value, row);
  expect(screen.getByText("Customized · unknown options retained")).toBeTruthy(); const selector = screen.getByLabelText("Add Instructions");
  const before = [value.list.mock.calls.length, value.inventory.mock.calls.length, value.search.mock.calls.length, value.get.mock.calls.length];
  for (const section of document.querySelectorAll<HTMLDetailsElement>(".agent-disclosure")) { toggle(section, true); toggle(section, false); }
  fireEvent(window, new Event("resize")); expect(screen.getByLabelText("Add Instructions")).toBe(selector);
  expect([value.list.mock.calls.length, value.inventory.mock.calls.length, value.search.mock.calls.length, value.get.mock.calls.length]).toEqual(before);
  change("Name", "Changed name"); await save(value); expect(decoded(value.save.mock.calls[0][0])).toEqual({ ...data, name: "Changed name" }); expect(value.save.mock.calls[0][0]).toMatchObject({ mutation: { id: row.id, expectedRevision: 9n } });
});
it("preserves both effort drafts and unrelated fields across Configure, Back and an inactive visit", async () => {
  const value = fixture(), data = value.data({ future_field: { retained: true }, options: { permission: "default", future_option: "retained" } }); const row = initial(value, data), rendered = render(value.view(row)); await configure(value, row);
  toggle(disclosure("Reasoning"), true); toggle(disclosure("Native harness options"), true);
  change("Reasoning effort", " Future-Effort "); change("Subagent effort", " Future-Child ");
  rendered.rerender(value.view(row, false)); rendered.rerender(value.view(row)); fireEvent.click(screen.getByRole("button", { name: "Back" })); fireEvent.click(screen.getByRole("button", { name: "Next" }));
  expect(screen.getByRole("combobox", { name: "Reasoning effort" })).toHaveProperty("value", " Future-Effort "); expect(screen.getByRole("combobox", { name: "Subagent effort" })).toHaveProperty("value", " Future-Child ");
  await save(value); expect(decoded(value.save.mock.calls[0][0])).toEqual({ ...data, effort: " Future-Effort ", options: { ...data.options as Document, subagent_effort: " Future-Child " } });
});
it("keeps ordered account weights and duplicate prevention, and ordered Instructions through collapse", async () => {
  const value = fixture(); render(value.view()); await accountsStep(value);
  for (const account of value.accounts) fireEvent.click(await screen.findByRole("checkbox", { name: new RegExp(String(resourceDocument(account).alias)) })); await proved(value, value.accounts); await settled(value);
  expect(screen.getAllByRole("checkbox", { name: /^Select / })).toHaveLength(2);
  const weights = screen.getAllByLabelText(/Weight for account/); fireEvent.change(weights[1], { target: { value: "7" } }); fireEvent.click(screen.getByRole("button", { name: "Move account 2 up" })); fireEvent.click(screen.getByRole("checkbox", { name: "Select First account" }));
  fireEvent.click(screen.getByRole("button", { name: "Next" })); change("Model for Fixture provider", "fixture-native"); fireEvent.click(screen.getByRole("button", { name: "Next" })); await settled(value);
  const instructions = disclosure("Instructions"); toggle(instructions, true);
  for (const template of value.templates) { await choose("Add Instructions", template.id); fireEvent.click(within(instructions).getByRole("button", { name: "Add selected" })); }
  await choose("Add Instructions", value.templates[0].id); expect(within(instructions).getByRole("button", { name: "Add selected" })).toHaveProperty("disabled", true);
  fireEvent.click(within(instructions).getByRole("button", { name: "Move entry 2 up" })); fireEvent.click(within(instructions).getByRole("button", { name: "Remove entry 2" })); toggle(instructions, false); toggle(instructions, true);
  change("Name", "Ordered"); await save(value); expect(decoded(value.save.mock.calls[0][0])).toMatchObject({ routes: [{ accounts: [{ id: value.accounts[1].id, weight: 7 }] }], templates: [value.templates[1].id] });
});
it("retains cumulative source-account choices and exact selection across step and inactive changes", async () => {
  const value = fixture(); value.list.mockImplementation(async (kind, page) => ({ resources: kind === EntityKind.ACCOUNT ? page ? [value.accounts[1]] : [value.accounts[0]] : [], nextPageToken: kind === EntityKind.ACCOUNT && !page ? "next-accounts" : "" }));
  const rendered = render(value.view()); await accountsStep(value); fireEvent.click(await screen.findByRole("checkbox", { name: /First account/ }));
  fireEvent.click(await screen.findByRole("button", { name: /Load more Source 1 account pages/ })); await screen.findByRole("checkbox", { name: /Second account/ });
  const before = value.list.mock.calls.length; rendered.rerender(value.view(undefined, false)); rendered.rerender(value.view());
  expect(screen.getByRole("checkbox", { name: "Select First account" })).toHaveProperty("checked", true); expect(await screen.findByRole("checkbox", { name: /Second account/ })).toBeTruthy(); expect(value.list.mock.calls.slice(before).map(([kind, page]) => [kind, page])).toEqual([[EntityKind.ACCOUNT, ""], [EntityKind.ACCOUNT, "next-accounts"]]); expect(value.save).not.toHaveBeenCalled();
});
it.each([Code.PermissionDenied, Code.Unauthenticated, Code.Unavailable])("exposes source-account read problems without claiming an empty inventory (%s)", async code => {
  const value = fixture(); value.list.mockImplementation(async kind => { if (kind === EntityKind.ACCOUNT) throw new ConnectError("Private fixture failure", code); return { resources: [] }; }); render(value.view()); await accountsStep(value);
  await screen.findByRole("button", { name: "Retry accounts" }); expect(document.body.textContent).not.toContain("Private fixture failure"); expect(screen.queryByText(/No accounts are available/)).toBeNull(); fireEvent.click(screen.getByRole("button", { name: "Next" })); expect(screen.getByRole("heading", { name: "Accounts", level: 3 })).toBeTruthy(); expect(value.save).not.toHaveBeenCalled(); expect(value.search).not.toHaveBeenCalled();
});
it("keeps source inventory failures scoped to source selection and never reads the retired model registry", async () => {
  const value = fixture(); value.inventory.mockImplementation(() => { throw new ConnectError("Private inventory failure", Code.Unavailable); }); render(value.view()); fireEvent.click(await screen.findByRole("radio", { name: "Codex" })); await settled(value);
  expect(document.querySelector('.worker-source-details [role="alert"]')).toBeTruthy(); expect(document.body.textContent).not.toContain("Private inventory failure"); expect(value.search).not.toHaveBeenCalled(); expect(value.save).not.toHaveBeenCalled(); expect(value.list.mock.calls.every(([kind]) => kind !== EntityKind.MODEL)).toBe(true);
});
it("distinguishes delayed and empty continuation pages from a failed cached account refresh", async () => {
  const value = fixture(); let release!: () => void; const gate = new Promise<void>(resolve => { release = resolve; }); value.list.mockImplementation(async kind => { if (kind === EntityKind.ACCOUNT) { await gate; return { resources: [], nextPageToken: "later" }; } return { resources: [] }; }); render(value.view()); fireEvent.click(await screen.findByRole("radio", { name: "Codex" })); await choose("Account source 1", `api:${value.provider.id}`);
  expect(await screen.findByText(/Loading accounts/)).toBeTruthy(); await act(async () => release()); await settled(value); expect(screen.queryByText(/No accounts are available/)).toBeNull();
  value.list.mockImplementation(async kind => ({ resources: kind === EntityKind.ACCOUNT ? value.accounts : [], nextPageToken: "" })); fireEvent.click(await screen.findByRole("button", { name: /Load more Source 1 account pages/ })); await screen.findByRole("checkbox", { name: /First account/ }); fireEvent.click(screen.getByRole("checkbox", { name: /First account/ })); await settled(value);
  value.list.mockImplementation(async kind => { if (kind === EntityKind.ACCOUNT) throw new ConnectError("Private refresh failure", Code.Unavailable); return { resources: [] }; }); await act(async () => { await value.client.invalidateQueries(); });
  expect(await screen.findByText(/last successfully loaded accounts/)).toBeTruthy(); expect(screen.getByRole("checkbox", { name: "Select First account" })).toHaveProperty("checked", true); expect(value.save).not.toHaveBeenCalled(); expect(document.body.textContent).not.toContain("Private refresh failure");
});
it("reveals a hidden invalid control and keeps unrelated draft values", async () => {
  const value = fixture(); render(value.view()); await configure(value); change("Name", "Retained draft"); const section = disclosure("Native harness options"); toggle(section, true); change("Maximum concurrency (0 uses native default)", "4294967296"); toggle(section, false);
  fireEvent.click(screen.getByRole("button", { name: "Save Agent Worker" })); const input = screen.getByLabelText("Maximum concurrency (0 uses native default)"); await waitFor(() => expect(document.activeElement).toBe(input)); expect(section.open).toBe(true); expect(screen.getByLabelText("Name")).toHaveProperty("value", "Retained draft"); expect(value.save).not.toHaveBeenCalled();
});
it("preserves incompatible permission values across harness switches and clears only explicitly", async () => {
  const value = fixture(), data = value.data({ options: { permission: "full-access", claude_permission: "future-mode", future_option: "keep" } }); const row = initial(value, data); render(value.view(row)); await configure(value, row);
  expect(scrollChoiceValue(screen.getByRole("combobox", { name: "Permission mode" }))).toBe("full-access"); fireEvent.click(screen.getByRole("button", { name: "Back" })); fireEvent.click(screen.getByRole("button", { name: "Back" })); fireEvent.click(screen.getByRole("button", { name: "Change harness" })); fireEvent.click(screen.getByRole("radio", { name: "Claude Code" }));
  // A harness change requires fresh source selection; it cannot reuse the old
  // account route. The retained permission draft must survive that reset.
  expect(screen.queryByRole("checkbox", { name: "Select First account" })).toBeNull(); expect(value.save).not.toHaveBeenCalled();
  const claudeAccount = resource(EntityKind.ACCOUNT, { ...resourceDocument(value.accounts[0]), alias: "Fresh Claude account", api_protocol: "anthropic-messages" }); value.resources.push(claudeAccount); await choose("Account source 1", `api:${value.provider.id}`); fireEvent.click(await screen.findByRole("checkbox", { name: /Fresh Claude account/ })); await proved(value, [claudeAccount]); await settled(value); fireEvent.click(screen.getByRole("button", { name: "Next" })); change("Model for Fixture provider", "fixture-native"); fireEvent.click(screen.getByRole("button", { name: "Next" }));
  expect(screen.getByRole("combobox", { name: "Claude permission mode" }).textContent).toContain("future-mode"); await choose("Claude permission mode", "default"); await save(value); expect(decoded(value.save.mock.calls[0][0]).options).toEqual({ permission: "full-access", claude_permission: "default", future_option: "keep" });
});
it("reveals invalid account weight in its owning step without losing account order", async () => {
  const value = fixture(); render(value.view()); await accountsStep(value); for (const account of value.accounts) fireEvent.click(await screen.findByRole("checkbox", { name: new RegExp(String(resourceDocument(account).alias)) })); await settled(value);
  const weight = screen.getAllByLabelText(/Weight for account/)[0]; fireEvent.change(weight, { target: { value: "0" } }); fireEvent.click(screen.getByRole("button", { name: "Next" })); expect(screen.getByRole("heading", { name: "Accounts", level: 3 })).toBeTruthy(); await waitFor(() => expect(document.activeElement).toBe(weight)); expect(screen.getAllByRole("checkbox", { name: /^Select / }).map(row => row.getAttribute("aria-label"))).toEqual(["Select First account", "Select Second account"]); expect(value.save).not.toHaveBeenCalled();
});
it("shows unsupported stored native options without changing them", async () => {
  const value = fixture(); value.accounts.forEach(account => { account.documentJson = encode({ ...resourceDocument(account), api_protocol: "anthropic-messages" }); }); const data = value.data({ harness: "claude-code", effort: "future-effort", options: { permission: "default", claude_permission: "future-mode", future_option: "retained" } }), row = initial(value, data); render(value.view(row)); await configure(value, row);
  expect(screen.getByText("Customized · unknown options retained")).toBeTruthy(); expect(screen.getByRole("combobox", { name: "Claude permission mode" }).textContent).toContain("future-mode"); change("Name", "Future renamed"); await save(value); expect(decoded(value.save.mock.calls[0][0])).toEqual({ ...data, name: "Future renamed" });
});
it("keeps the byte-identical uncertain request through disclosure, resize and an inactive visit", async () => {
  const value = fixture(); value.save.mockRejectedValueOnce(new ConnectError("Lost response", Code.Unavailable)); const rendered = render(value.view()); await configure(value); change("Name", "Exact retry"); await choose("Permission mode", "full-access"); fireEvent.click(screen.getByRole("button", { name: "Save Agent Worker" })); const retry = await screen.findByRole("button", { name: "Retry the same Worker save" });
  for (const section of document.querySelectorAll<HTMLDetailsElement>(".agent-disclosure")) { toggle(section, true); toggle(section, false); } fireEvent(window, new Event("resize")); rendered.rerender(value.view(undefined, false)); rendered.rerender(value.view()); expect(screen.getByRole("button", { name: "Save Agent Worker" })).toHaveProperty("disabled", true); fireEvent.click(retry); await waitFor(() => expect(value.save).toHaveBeenCalledTimes(2)); expect(value.save.mock.calls[1][0]).toEqual(value.save.mock.calls[0][0]);
});
it("blocks a stale revision without losing the Agent draft", async () => {
  const value = fixture(), row = initial(value, value.data()); render(value.view(row)); await configure(value, row); change("Name", "Unsaved"); value.get.mockImplementation(id => ({ resource: id === row.id ? create(ResourceSchema, { ...row, revision: 10n }) : value.resources.find(candidate => candidate.id === id) })); await act(async () => { await value.client.invalidateQueries(); }); expect(await screen.findByText(/Worker changed elsewhere/)).toBeTruthy(); expect(screen.getByRole("button", { name: "Save Agent Worker" })).toHaveProperty("disabled", true); expect(screen.getByLabelText("Name")).toHaveProperty("value", "Unsaved"); expect(value.save).not.toHaveBeenCalled();
});
it("keeps all Codex permissions selectable and saves concurrency above the former product cap", async () => {
  const value = fixture(); render(value.view()); await configure(value); const permission = screen.getByLabelText("Permission mode"); fireEvent.click(permission); expect(within(document.getElementById(permission.getAttribute("aria-controls")!)!).getAllByRole("option").map(option => (option as HTMLElement).dataset.pickerId)).toEqual(["default", "read-only", "workspace-write", "full-access"]); fireEvent.keyDown(permission, { key: "Escape" }); for (const mode of ["default", "read-only", "workspace-write", "full-access"]) { await choose("Permission mode", mode); expect(scrollChoiceValue(permission)).toBe(mode); } expect(screen.getByText(/managed authentication file/)).toBeTruthy(); toggle(disclosure("Native harness options"), true); change("Maximum concurrency (0 uses native default)", "1024"); change("Name", "Native concurrency"); await save(value); expect(decoded(value.save.mock.calls[0][0]).options).toEqual({ permission: "full-access", max_concurrency: 1024 });
});
it("disables unavailable Claude settings and clears only the explicitly selected retained value", async () => {
  const value = fixture(); value.accounts.forEach(account => { account.documentJson = encode({ ...resourceDocument(account), api_protocol: "anthropic-messages" }); }); const data = value.data({ harness: "claude-code", effort: "Future-Effort", options: { permission: "default", service_tier: "future-tier", subagent_effort: "future-child", max_concurrency: 1024, future_option: "keep" } }), row = initial(value, data, 5n); render(value.view(row)); await configure(value, row); toggle(disclosure("Native harness options"), true); expect(screen.getByLabelText("Service tier")).toHaveProperty("disabled", true); expect(screen.getByLabelText("Service tier")).toHaveProperty("value", "future-tier"); expect(screen.getByLabelText("Subagent effort")).toHaveProperty("disabled", true); fireEvent.click(screen.getByRole("button", { name: "Clear retained option: Service tier" })); await save(value); expect(decoded(value.save.mock.calls[0][0])).toEqual({ ...data, options: { ...data.options as Document, service_tier: "" } });
});
it("permission menus perform no additional resource or provider reads", async () => {
  const value = fixture(); render(value.view()); await configure(value); const reads = () => [value.list.mock.calls.length, value.get.mock.calls.length, value.inventory.mock.calls.length, value.search.mock.calls.length], before = reads(); const trigger = screen.getByRole("combobox", { name: "Permission mode" }); fireEvent.click(trigger); fireEvent.keyDown(trigger, { key: "End" }); fireEvent.keyDown(trigger, { key: "Escape" }); await choose("Permission mode", "workspace-write"); await act(async () => {}); expect(reads()).toEqual(before); expect(value.save).not.toHaveBeenCalled();
});
it("keeps cancellation and inactive configuration visits free of saves", async () => { const value = fixture(), rendered = render(value.view()); await configure(value); change("Name", "Retained canceled draft"); rendered.rerender(value.view(undefined, false)); expect(screen.getByRole("button", { name: "Save Agent Worker" })).toHaveProperty("disabled", true); rendered.rerender(value.view()); expect(screen.getByLabelText("Name")).toHaveProperty("value", "Retained canceled draft"); fireEvent.click(screen.getByRole("button", { name: "Cancel" })); expect(value.cancel).toHaveBeenCalledTimes(1); expect(value.save).not.toHaveBeenCalled(); });
it("leaves unsupported saved Agent schemas inert without rewriting them", async () => { const value = fixture(), row = create(ResourceSchema, { ...resource(EntityKind.AGENT, { name: "Retired", model_id: newRequestId(), accounts: [] }), schemaVersion: 1 }); value.resources.push(row); render(value.view(row)); await screen.findByRole("radio", { name: "Codex" }); fireEvent.click(screen.getByRole("radio", { name: "Codex" })); fireEvent.click(screen.getByRole("button", { name: "Next" })); expect(screen.getByRole("heading", { name: "Accounts", level: 3 })).toBeTruthy(); expect(screen.queryByRole("button", { name: "Save Agent Worker" })).toBeNull(); expect(value.save).not.toHaveBeenCalled(); expect(value.search).not.toHaveBeenCalled(); });

it.each([["codex", "openai-responses", 9], ["claude-code", "anthropic-messages", 5], ["opencode", "openai-chat", 0], ["grok-build", "openai-chat", 0]])("keeps %s effort hints and unsupported child settings scoped to the selected harness", async (harness, protocol, count) => {
  const value = fixture(); value.accounts.forEach(account => { account.documentJson = encode({ ...resourceDocument(account), api_protocol: protocol }); }); const row = initial(value, value.data({ harness })); render(value.view(row)); await configure(value, row); toggle(disclosure("Reasoning"), true); toggle(disclosure("Native harness options"), true);
  const effort = screen.getByRole("combobox", { name: "Reasoning effort" }); expect(effort).toHaveProperty("disabled", harness === "grok-build");
  if (harness !== "grok-build") { fireEvent.focus(effort); expect(within(screen.getByRole("listbox", { name: "Reasoning effort suggestions" })).getAllByRole("option")).toHaveLength(Number(count) + 1); fireEvent.keyDown(effort, { key: "Escape" }); }
  expect(screen.getByRole("combobox", { name: "Subagent effort" })).toHaveProperty("disabled", harness !== "codex"); expect(value.save).not.toHaveBeenCalled(); expect(value.search).not.toHaveBeenCalled();
});
