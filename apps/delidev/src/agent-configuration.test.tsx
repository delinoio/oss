// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport, type Transport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { StrictMode, useState } from "react";
import { expect, it, vi } from "vitest";
import { ConfigurationService, SystemService, SystemCapability, EntityKind, ProviderInventoryCapability, ProviderService, ResourceSchema, ResourceService, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { encode, type Document } from "./documents";
import { MutationIntents } from "./mutation";
import { ConfigurationEditor, Settings } from "./settings";
import { chooseScrollOption, waitScrollChoices, scrollChoiceValue } from "./test-scroll-picker";

function resource(kind: EntityKind, data: Document, revision = 1n) {
  return create(ResourceSchema, { id: newRequestId(), kind, revision, schemaVersion: 1, documentJson: encode(data) });
}
function fixture() {
  const provider = resource(EntityKind.PROVIDER, { name: "Fixture provider", enabled: true });
  const model = resource(EntityKind.MODEL, { name: "Fixture model", provider_id: provider.id });
  const accounts = [resource(EntityKind.ACCOUNT, { alias: "First account", health: "disconnected" }), resource(EntityKind.ACCOUNT, { alias: "Second account", health: "disconnected" })];
  const templates = [resource(EntityKind.TEMPLATE, { name: "First instructions" }), resource(EntityKind.TEMPLATE, { name: "Second instructions" })];
  const resources = [provider, model, ...accounts, ...templates];
  const save = vi.fn(async (request: { kind: EntityKind; documentJson: Uint8Array }) => ({ resource: resource(request.kind, JSON.parse(new TextDecoder().decode(request.documentJson))) }));
  const list = vi.fn(async (kind: EntityKind, _page: string): Promise<{ resources: Resource[]; nextPageToken?: string }> => ({ resources: resources.filter(row => row.kind === kind) }));
  const search = vi.fn(async (_page: string): Promise<{ models: Resource[]; nextPageToken?: string }> => ({ models: [model] }));
  const get = vi.fn((id: string) => ({ resource: resources.find(row => row.id === id) }));
  const inventory = vi.fn(() => ({ entries: [], capabilities: [ProviderInventoryCapability.PROVIDER_ACTIVATION, ProviderInventoryCapability.ACTIVE_API_MODEL_FILTER, ProviderInventoryCapability.ACCOUNT_PROVIDER_FILTER, ProviderInventoryCapability.ACCOUNT_TYPE_FILTER] }));
  const transport = createRouterTransport(router => {
    router.service(ResourceService, { listResources: request => list(request.filter?.kind ?? EntityKind.UNSPECIFIED, request.filter?.pageToken ?? ""), getResource: request => get(request.id) });
    router.service(ProviderService, { listProviderInventory: inventory, searchModels: request => search(request.pageToken), listProviderPresets: () => ({ presetsJson: encode([]) }) });
    router.service(ConfigurationService, { saveConfiguration: save });
    router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.CODEX_SUBAGENT_CONFIGURATION_V1] }) });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity }, mutations: { retry: false, gcTime: 0 } } });
  const view = (children: React.ReactNode, upstream: Transport = transport) => <StrictMode><TransportProvider transport={upstream}><QueryClientProvider client={client}><MutationIntents>{children}</MutationIntents></QueryClientProvider></TransportProvider></StrictMode>;
  return { provider, model, accounts, templates, resources, save, list, search, inventory, get, transport, client, view };
}
function disclosure(title: string) {
  return Array.from(document.querySelectorAll<HTMLDetailsElement>(".agent-disclosure")).find(section => section.querySelector(".agent-section-title")?.textContent?.startsWith(title))!;
}
function toggle(section: HTMLDetailsElement, open: boolean) { section.open = open; fireEvent(section, new Event("toggle")); }
function decoded(request: unknown) { return JSON.parse(new TextDecoder().decode((request as { documentJson: Uint8Array }).documentJson)); }
function change(label: string, value: string) { fireEvent.change(screen.getByLabelText(label), { target: { value } }); }
// A fresh Settings opening performs capability discovery before model search.
// This fixture budget allows both ordinary asynchronous replies; it is not a product SLA.
async function ready(value: ReturnType<typeof fixture>) { await waitScrollChoices(screen.getByRole("combobox", { name: "Model" })); await waitFor(() => expect(value.client.isFetching()).toBe(0), { timeout: 5000 }); }
async function choose(label: string, id: string) { await chooseScrollOption(screen.getByRole("combobox", { name: label }), id); }

it("creates an accountless Agent with only the original defaults and visible core controls", async () => {
  const value = fixture();
  render(value.view(<ConfigurationEditor kind={EntityKind.AGENT} active saved={() => {}} cancel={() => {}} />));
  await ready(value);
  expect(Array.from(document.querySelectorAll(".agent-section-title"), title => title.textContent)).toEqual(["Reasoning", "Accounts & routing", "Instructions", "Native harness options"]);
  expect(Array.from(document.querySelectorAll<HTMLDetailsElement>(".agent-disclosure")).every(section => !section.open)).toBe(true);
  const core = document.querySelector(".agent-core")!;
  expect(within(core as HTMLElement).getByLabelText("Permission mode")).toBeTruthy();
  expect(Array.from(core.querySelectorAll<HTMLElement>(".agent-required"), marker => marker.parentElement?.textContent)).toEqual(["Name *", "Model *"]);
  expect(screen.getByText("Can be saved without accounts; execution requires an eligible account.").closest("details")).toBeNull();
  expect(Array.from(document.querySelectorAll(".agent-footer button"), button => button.textContent)).toEqual(["Cancel edit", "Save Agent Worker"]);
  change("Name", "Minimal agent"); await choose("Model", value.model.id);
  fireEvent.click(screen.getByRole("button", { name: "Save Agent Worker" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  expect(decoded(value.save.mock.calls[0][0])).toEqual({ name: "Minimal agent", harness: "codex", model_id: value.model.id, accounts: [], templates: [], options: { permission: "default" } });
  expect(value.save.mock.calls[0][0]).toMatchObject({ kind: EntityKind.AGENT, schemaVersion: 1, mutation: { expectedRevision: 0n } });
});

it("changes only Name while preserving the complete document, exact revision and mounted selectors", async () => {
  const value = fixture();
  const data = { name: "Original", harness: "codex", model_id: value.model.id, effort: "high", routing: "priority", accounts: value.accounts.map((account, index) => ({ id: account.id, weight: index + 2, future_link: "retained" })), templates: value.templates.map(template => template.id), future_field: { retained: true }, options: { permission: "workspace-write", subagent_model: "child", subagent_effort: "medium", max_concurrency: 4, approval_policy: "on-request", approval_review_model: "reviewer", service_tier: "priority", future_option: "retained" } };
  const agent = resource(EntityKind.AGENT, data, 9n); value.resources.push(agent);
  render(value.view(<ConfigurationEditor kind={EntityKind.AGENT} initial={agent} active saved={() => {}} cancel={() => {}} />));
  await ready(value);
  expect(screen.getByText("Customized · unknown options retained")).toBeTruthy();
  const accountSelector = screen.getByLabelText("Add AI account");
  const before = [value.list.mock.calls.length, value.inventory.mock.calls.length, value.search.mock.calls.length, value.get.mock.calls.length];
  for (const section of document.querySelectorAll<HTMLDetailsElement>(".agent-disclosure")) { toggle(section, true); toggle(section, false); }
  for (const [title, label] of [["Reasoning", "Reasoning effort"], ["Native harness options", "Subagent effort"]]) {
    const section = disclosure(title!); toggle(section, true);
    const input = screen.getByRole("combobox", { name: label! }); fireEvent.focus(input); fireEvent.keyDown(input, { key: "Escape" });
    toggle(section, false);
  }
  fireEvent(window, new Event("resize"));
  expect(screen.getByLabelText("Add AI account")).toBe(accountSelector);
  expect([value.list.mock.calls.length, value.inventory.mock.calls.length, value.search.mock.calls.length, value.get.mock.calls.length]).toEqual(before);
  change("Name", "Changed name"); fireEvent.click(screen.getByRole("button", { name: "Save Agent Worker" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  expect(decoded(value.save.mock.calls[0][0])).toEqual({ ...data, name: "Changed name" });
  expect(value.save.mock.calls[0][0]).toMatchObject({ mutation: { id: agent.id, expectedRevision: 9n } });
});

it("changes harness hints while preserving both effort drafts and unrelated fields", async () => {
  const value = fixture();
  const original = { name: "Hints", harness: "codex", model_id: value.model.id, accounts: [], templates: [], options: { permission: "default", future_option: "retained" }, future_field: { retained: true } };
  const agent = resource(EntityKind.AGENT, original); value.resources.push(agent);
  render(value.view(<ConfigurationEditor kind={EntityKind.AGENT} initial={agent} active saved={() => {}} cancel={() => {}} />)); await ready(value);
  toggle(disclosure("Reasoning"), true); toggle(disclosure("Native harness options"), true);
  for (const [harness, expected] of [["codex", 9], ["claude-code", 5], ["opencode", 0], ["grok-build", 0]] as const) {
    change("Harness", harness); change("Reasoning effort", "");
    const input = screen.getByRole("combobox", { name: "Reasoning effort" }) as HTMLInputElement;
    expect(input.disabled).toBe(harness === "grok-build");
    if (!input.disabled) {
      fireEvent.focus(input);
      expect(within(screen.getByRole("listbox", { name: "Reasoning effort suggestions" })).getAllByRole("option")).toHaveLength(expected + 1);
      fireEvent.keyDown(input, { key: "Escape" });
    }
    const child = screen.getByRole("combobox", { name: "Subagent effort" }) as HTMLInputElement;
    expect(child.disabled).toBe(harness !== "codex");
    if (harness === "codex") {
      fireEvent.focus(child);
      expect(within(screen.getByRole("listbox", { name: "Subagent effort suggestions" })).getAllByRole("option")).toHaveLength(10);
      fireEvent.keyDown(child, { key: "Escape" });
    }
  }
  change("Harness", "codex");
  change("Reasoning effort", " Future-Effort "); change("Subagent effort", " Future-Child ");
  change("Harness", "claude-code");
  expect((screen.getByRole("combobox", { name: "Reasoning effort" }) as HTMLInputElement).value).toBe(" Future-Effort ");
  expect((screen.getByRole("combobox", { name: "Subagent effort" }) as HTMLInputElement).value).toBe(" Future-Child ");
  fireEvent.click(screen.getByRole("button", { name: "Save Agent Worker" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  expect(decoded(value.save.mock.calls[0][0])).toEqual({ ...original, harness: "claude-code", effort: " Future-Effort ", options: { ...original.options, subagent_effort: " Future-Child " } });
});

it("keeps ordered reference operations, relative weights and duplicate prevention through collapse", async () => {
  const value = fixture(); render(value.view(<ConfigurationEditor kind={EntityKind.AGENT} active saved={() => {}} cancel={() => {}} />)); await ready(value);
  change("Name", "Ordered"); await choose("Model", value.model.id);
  const accounts = disclosure("Accounts & routing"); toggle(accounts, true);
  const accountFields = within(accounts);
  for (const account of value.accounts) { await choose("Add AI account", account.id); fireEvent.click(accountFields.getByRole("button", { name: "Add selected" })); }
  await choose("Add AI account", value.accounts[0].id);
  expect((accountFields.getByRole("button", { name: "Add selected" }) as HTMLButtonElement).disabled).toBe(true);
  fireEvent.change(accountFields.getAllByLabelText("Relative weight")[1], { target: { value: "7" } });
  fireEvent.click(accountFields.getByRole("button", { name: "Move entry 2 up" }));
  fireEvent.click(accountFields.getByRole("button", { name: "Remove entry 2" }));
  toggle(accounts, false); toggle(accounts, true);
  expect((accountFields.getByLabelText("Relative weight") as HTMLInputElement).value).toBe("7");
  const instructions = disclosure("Instructions"); toggle(instructions, true);
  for (const template of value.templates) { await choose("Add Instructions", template.id); fireEvent.click(within(instructions).getByRole("button", { name: "Add selected" })); }
  fireEvent.click(within(instructions).getByRole("button", { name: "Move entry 2 up" }));
  fireEvent.click(within(instructions).getByRole("button", { name: "Remove entry 2" })); toggle(instructions, false);
  fireEvent.click(screen.getByRole("button", { name: "Save Agent Worker" })); await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  expect(decoded(value.save.mock.calls[0][0])).toMatchObject({ accounts: [{ id: value.accounts[1].id, weight: 7 }], templates: [value.templates[1].id] });
});

it("retains cumulative choices and exact reference selection when disclosures toggle", async () => {
  const value = fixture();
  value.list.mockImplementation(async (kind, page) => kind === EntityKind.ACCOUNT ? page ? { resources: [value.accounts[1]] } : { resources: [value.accounts[0]], nextPageToken: "next-accounts" } : { resources: value.resources.filter(row => row.kind === kind) });
  render(value.view(<ConfigurationEditor kind={EntityKind.AGENT} active saved={() => {}} cancel={() => {}} />)); await ready(value);
  const section = disclosure("Accounts & routing"); toggle(section, true); await choose("Add AI account", value.accounts[0].id);
  fireEvent.click(screen.getByRole("combobox", { name: "Add AI account" }));
  fireEvent.click(within(section).getByRole("button", { name: "Load more Add AI account" }));
  await screen.findByRole("option", { name: /Second account/ });
  expect(value.list.mock.calls.filter(([kind]) => kind === EntityKind.ACCOUNT).map(([, token]) => token)).toEqual(["", "", "next-accounts"]);
  fireEvent.keyDown(screen.getByRole("combobox", { name: "Add AI account" }), { key: "Escape" });
  const before = value.list.mock.calls.length; toggle(section, false); toggle(section, true); fireEvent(window, new Event("resize"));
  expect(scrollChoiceValue(screen.getByRole("combobox", { name: "Add AI account" }))).toBe(value.accounts[0].id);
  fireEvent.click(screen.getByRole("combobox", { name: "Add AI account" }));
  expect(screen.getByRole("option", { name: /First account/ })).toBeTruthy();
  expect(screen.getByRole("option", { name: /Second account/ })).toBeTruthy();
  expect(value.list.mock.calls.length).toBe(before);
  expect(within(section).queryByRole("button", { name: "First choices" })).toBeNull();
});

it.each([Code.PermissionDenied, Code.Unauthenticated, Code.Unavailable])("exposes collapsed read problems without claiming an empty inventory (%s)", async code => {
  const value = fixture(); value.list.mockImplementation(async kind => { if (kind === EntityKind.ACCOUNT) throw new ConnectError("Private fixture failure", code); return { resources: [] }; });
  render(value.view(<ConfigurationEditor kind={EntityKind.AGENT} active saved={() => {}} cancel={() => {}} />)); await ready(value);
  const section = disclosure("Accounts & routing");
  expect(section.open).toBe(false); expect(section.querySelector("summary")?.textContent).toContain("Needs attention");
  expect(within(section).getByText(code === Code.Unavailable ? /server connection failed/ : /server denied access/)).toBeTruthy();
  expect(within(section).queryByText(/No selectable/)).toBeNull();
  expect(within(section).getByRole("alert")).toBeTruthy();
});

it("keeps model capability failures scoped to the model selector", async () => {
  const value = fixture(); value.inventory.mockImplementation(() => { throw new ConnectError("Inventory unavailable", Code.Unavailable); });
  render(value.view(<ConfigurationEditor kind={EntityKind.AGENT} active saved={() => {}} cancel={() => {}} />));
  expect(await within(screen.getByRole("combobox", { name: "Model" }).closest(".resource-choice") as HTMLElement).findByText(/server connection failed while loading these choices/)).toBeTruthy();
  await waitScrollChoices(screen.getByRole("combobox", { name: "Add AI account" }));
  await waitFor(() => expect(value.client.isFetching()).toBe(0));
  for (const title of ["Accounts & routing", "Instructions"]) {
    expect(disclosure(title).querySelector("summary")?.textContent).not.toContain("Needs attention");
    expect(within(disclosure(title)).queryByText(/server connection failed/)).toBeNull();
  }
  expect(value.search).not.toHaveBeenCalled();
});

it("distinguishes delayed, empty first/later pages and failed cached refresh", async () => {
  const value = fixture(); let release!: () => void;
  const gate = new Promise<void>(resolve => { release = resolve; });
  value.list.mockImplementation(async kind => { if (kind === EntityKind.ACCOUNT) { await gate; return { resources: [], nextPageToken: "later" }; } return { resources: [] }; });
  render(value.view(<ConfigurationEditor kind={EntityKind.AGENT} active saved={() => {}} cancel={() => {}} />));
  const section = disclosure("Accounts & routing"); expect(within(section).getByText("Loading Add AI account choices…")).toBeTruthy();
  await act(async () => release()); await ready(value);
  expect(within(section).getByText("No selectable Add AI account choices are on this page. More choices are available.")).toBeTruthy();
  toggle(section, true); value.list.mockImplementation(async () => ({ resources: [], nextPageToken: "another" }));
  fireEvent.click(screen.getByRole("combobox", { name: "Add AI account" })); fireEvent.click(within(section).getByRole("button", { name: "Load more Add AI account" })); await waitFor(() => expect(value.client.isFetching()).toBe(0));
  expect(within(section).getByText(/No selectable Add AI account choices are on this page. More choices are available/)).toBeTruthy();
  value.list.mockImplementation(async kind => ({ resources: kind === EntityKind.ACCOUNT ? value.accounts : [] }));
  await act(async () => { await value.client.invalidateQueries(); }); await screen.findByRole("button", { name: "Reload list" }); fireEvent.click(screen.getByRole("button", { name: "Reload list" })); await screen.findByRole("option", { name: /First account/ }); fireEvent.keyDown(screen.getByRole("combobox", { name: "Add AI account" }), { key: "Escape" }); await choose("Add AI account", value.accounts[0].id);
  value.list.mockImplementation(async kind => { if (kind === EntityKind.ACCOUNT) throw new ConnectError("Unavailable", Code.Unavailable); return { resources: [] }; });
  toggle(section, false); await act(async () => { await value.client.invalidateQueries(); });
  await waitFor(() => expect(section.querySelector("summary")?.textContent).toContain("Needs attention"));
  expect(within(section).getByText(/Showing cached Add AI account choices/)).toBeTruthy();
  expect(scrollChoiceValue(screen.getByRole("combobox", { name: "Add AI account" }))).toBe(value.accounts[0].id);
});

it("reveals a hidden invalid control and keeps unrelated draft values", async () => {
  const value = fixture(); render(value.view(<ConfigurationEditor kind={EntityKind.AGENT} active saved={() => {}} cancel={() => {}} />)); await ready(value);
  change("Name", "Retained draft"); await choose("Model", value.model.id);
  const section = disclosure("Native harness options"); toggle(section, true); change("Maximum concurrency (0 uses native default)", "4294967296"); toggle(section, false);
  expect(section.querySelector("summary")?.textContent).toContain("Needs attention");
  const input = screen.getByLabelText("Maximum concurrency (0 uses native default)") as HTMLInputElement;
  expect(document.querySelector<HTMLFormElement>(".agent-configuration")!.checkValidity()).toBe(false);
  await waitFor(() => expect(document.activeElement).toBe(input)); expect(section.open).toBe(true);
  expect((screen.getByLabelText("Name") as HTMLInputElement).value).toBe("Retained draft"); expect(value.save).not.toHaveBeenCalled();
  change("Maximum concurrency (0 uses native default)", "4"); expect(section.querySelector("summary")?.textContent).not.toContain("Needs attention");
});

it("preserves incompatible permission values across harness switches and clears only explicitly", async () => {
  const value = fixture(); const agent = resource(EntityKind.AGENT, { name: "Permissions", harness: "codex", model_id: value.model.id, accounts: [], templates: [], options: { permission: "workspace-write", approval_policy: "on-request", future_option: "keep" } }); value.resources.push(agent);
  render(value.view(<ConfigurationEditor kind={EntityKind.AGENT} initial={agent} active saved={() => {}} cancel={() => {}} />)); await ready(value);
  change("Harness", "claude-code"); expect(screen.getByText(/Retained sandbox or approval-policy settings/)).toBeTruthy();
  expect(screen.getByText(/do not establish filesystem sandbox isolation/)).toBeTruthy();
  change("Harness", "codex"); expect((screen.getByLabelText("Permission mode") as HTMLSelectElement).value).toBe("workspace-write");
  toggle(disclosure("Native harness options"), true); expect((screen.getByLabelText("Approval policy") as HTMLInputElement).value).toBe("on-request");
  change("Permission mode", "full-access"); expect(screen.getByText(/Full access permits/)).toBeTruthy();
  change("Harness", "claude-code"); fireEvent.click(screen.getByRole("button", { name: "Clear incompatible permission settings" }));
  for (const [mode, explanation] of [["acceptEdits", /Claude can accept file edits/], ["dontAsk", /Claude denies operations/], ["bypassPermissions", /Bypass skips native/]] as const) { change("Claude permission mode", mode); expect(screen.getByText(explanation)).toBeTruthy(); }
  fireEvent.click(screen.getByRole("button", { name: "Save Agent Worker" })); await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  expect(decoded(value.save.mock.calls[0][0]).options).toEqual({ permission: "default", claude_permission: "bypassPermissions", future_option: "keep" });
});

it("reveals a closed account section for an invalid weight without losing its order", async () => {
  const value = fixture(); const agent = resource(EntityKind.AGENT, { name: "Weights", harness: "codex", model_id: value.model.id, accounts: value.accounts.map(account => ({ id: account.id, weight: 2 })), templates: [], options: { permission: "default" } }); value.resources.push(agent);
  render(value.view(<ConfigurationEditor kind={EntityKind.AGENT} initial={agent} active saved={() => {}} cancel={() => {}} />)); await ready(value);
  const section = disclosure("Accounts & routing"); toggle(section, true);
  const weight = within(section).getAllByLabelText("Relative weight")[0] as HTMLInputElement;
  fireEvent.change(weight, { target: { value: "0" } }); toggle(section, false);
  expect(document.querySelector<HTMLFormElement>(".agent-configuration")!.checkValidity()).toBe(false);
  await waitFor(() => expect(document.activeElement).toBe(weight)); expect(section.open).toBe(true); expect(value.save).not.toHaveBeenCalled();
  expect(Array.from(section.querySelectorAll("li code"), code => code.textContent)).toEqual(value.accounts.map(account => account.id));
});

it("shows unsupported stored enums and native options without changing them", async () => {
  const value = fixture(); const agent = resource(EntityKind.AGENT, { name: "Future", harness: "claude-code", model_id: value.model.id, effort: "future-effort", routing: "future-routing", accounts: [], templates: [], options: { permission: "default", claude_permission: "future-mode", future_option: "retained" } }); value.resources.push(agent);
  render(value.view(<ConfigurationEditor kind={EntityKind.AGENT} initial={agent} active saved={() => {}} cancel={() => {}} />)); await ready(value);
  expect(screen.getByText("Customized · unknown options retained")).toBeTruthy(); expect(screen.getByText("future-effort")).toBeTruthy(); expect(screen.getByText("0 accounts · future-routing")).toBeTruthy();
  expect(screen.getByRole("option", { name: "Unsupported selection · future-mode" })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Save Agent Worker" })); await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1)); expect(decoded(value.save.mock.calls[0][0])).toEqual(JSON.parse(new TextDecoder().decode(agent.documentJson)));
});

it("keeps the byte-identical uncertain request through disclosure and resize", async () => {
  const value = fixture(); value.save.mockRejectedValueOnce(new ConnectError("Lost response", Code.Unavailable));
  render(value.view(<ConfigurationEditor kind={EntityKind.AGENT} active saved={() => {}} cancel={() => {}} />)); await ready(value);
  change("Name", "Exact retry"); await choose("Model", value.model.id); fireEvent.click(screen.getByRole("button", { name: "Save Agent Worker" }));
  const retry = await screen.findByRole("button", { name: "Retry the same configuration" });
  for (const section of document.querySelectorAll<HTMLDetailsElement>(".agent-disclosure")) { toggle(section, true); toggle(section, false); } fireEvent(window, new Event("resize")); fireEvent.click(retry);
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(2)); expect(value.save.mock.calls[1][0]).toEqual(value.save.mock.calls[0][0]);
});

it("blocks a stale revision without losing the Agent draft", async () => {
  const value = fixture(); const agent = resource(EntityKind.AGENT, { name: "Original", harness: "codex", model_id: value.model.id, options: { permission: "default" } }); value.resources.push(agent);
  render(value.view(<ConfigurationEditor kind={EntityKind.AGENT} initial={agent} active saved={() => {}} cancel={() => {}} />)); await ready(value); change("Name", "Unsaved");
  value.get.mockImplementation(id => ({ resource: id === agent.id ? create(ResourceSchema, { ...agent, revision: 2n }) : value.resources.find(row => row.id === id) }));
  await act(async () => { await value.client.invalidateQueries(); }); expect(await screen.findByText(/This entry changed elsewhere/)).toBeTruthy(); expect((screen.getByRole("button", { name: "Save Agent Worker" }) as HTMLButtonElement).disabled).toBe(true); expect((screen.getByLabelText("Name") as HTMLInputElement).value).toBe("Unsaved");
});

it("keeps all Codex permissions selectable and saves concurrency above the former product cap", async () => {
  const value = fixture();
  render(value.view(<ConfigurationEditor kind={EntityKind.AGENT} active saved={() => {}} cancel={() => {}} />));
  await ready(value);
  const permission = screen.getByLabelText("Permission mode");
  expect(within(permission).getAllByRole("option").map(option => (option as HTMLOptionElement).value)).toEqual(["default", "read-only", "workspace-write", "full-access"]);
  for (const mode of ["default", "read-only", "workspace-write", "full-access"]) {
    change("Permission mode", mode);
    expect((permission as HTMLSelectElement).value).toBe(mode);
  }
  expect(screen.getByText(/managed authentication file/)).toBeTruthy();
  toggle(disclosure("Native harness options"), true);
  change("Maximum concurrency (0 uses native default)", "1024");
  change("Name", "Native concurrency"); await choose("Model", value.model.id);
  fireEvent.click(screen.getByRole("button", { name: "Save Agent Worker" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  expect(decoded(value.save.mock.calls[0][0]).options).toEqual({ permission: "full-access", max_concurrency: 1024 });
});

it("disables unavailable Claude settings and clears only the explicitly selected retained value", async () => {
  const value = fixture();
  const data = { name: "Retained Claude", harness: "claude-code", model_id: value.model.id, effort: "Future-Effort", options: { permission: "default", service_tier: "future-tier", subagent_effort: "future-child", max_concurrency: 1024, future_option: "keep" } };
  const agent = resource(EntityKind.AGENT, data, 5n); value.resources.push(agent);
  render(value.view(<ConfigurationEditor kind={EntityKind.AGENT} initial={agent} active saved={() => {}} cancel={() => {}} />));
  await ready(value);
  toggle(disclosure("Native harness options"), true);
  expect((screen.getByLabelText("Service tier") as HTMLInputElement).disabled).toBe(true);
  expect((screen.getByLabelText("Service tier") as HTMLInputElement).value).toBe("future-tier");
  expect((screen.getByLabelText("Subagent effort") as HTMLInputElement).disabled).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "Clear retained option: Service tier" }));
  fireEvent.click(screen.getByRole("button", { name: "Save Agent Worker" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  const saved = decoded(value.save.mock.calls[0][0]);
  expect(saved).toEqual({ ...data, options: { ...data.options, service_tier: "" } });
});
