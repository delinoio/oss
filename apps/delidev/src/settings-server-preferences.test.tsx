// SPDX-License-Identifier: Apache-2.0
import { StrictMode, type ReactNode } from "react";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport, type Transport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { configure, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { SystemService, SystemCapability, ConfigurationService, EntityKind, ProviderInventoryCapability, ProviderService, ResourceSchema, ResourceService, newRequestId, type ListResourcesRequest, type SaveConfigurationRequest, type Resource } from "@delinoio/delidev-api-client";
import { ConfigurationEditor, ConfigurationEditorPresentation, Settings } from "./settings";
import { newConfiguration, ServerPreferenceSection } from "./configuration-fields";
import { encode, type Document } from "./documents";
import { SettingsCategory } from "./settings-category";
import { MutationIntents } from "./mutation";

// Settings renders all mounted categories under Strict Mode. Keep this fixture
// bounded while allowing concurrent integration/build load on development hosts.
vi.setConfig({ testTimeout: 15000 });
configure({ asyncUtilTimeout: 5000 });

type Page = { resources: Resource[]; nextPageToken?: string };
function resource(kind: EntityKind, data: Document, revision = 8n) {
  return create(ResourceSchema, { kind, id: newRequestId(), schemaVersion: 1, revision, documentJson: encode(data) });
}
function fixture(rows: Resource[] = [], read?: (token: string) => Page | Promise<Page>, capabilities: SystemCapability[] = []) {
  const list = vi.fn((request: ListResourcesRequest) => request.filter?.kind === EntityKind.SETTINGS && read
    ? read(request.filter.pageToken) : { resources: rows.filter(row => row.kind === request.filter?.kind) });
  const save = vi.fn(async (request: SaveConfigurationRequest) => {
    const saved = create(ResourceSchema, { kind: EntityKind.SETTINGS, id: request.mutation?.id || newRequestId(), schemaVersion: 1, revision: (request.mutation?.expectedRevision ?? 0n) + 1n, documentJson: request.documentJson });
    const index = rows.findIndex(row => row.id === saved.id);
    if (index < 0) rows.push(saved); else rows[index] = saved;
    return { resource: saved };
  });
  const get = vi.fn((request: { id: string }): { resource?: Resource } | Promise<{ resource?: Resource }> => ({ resource: rows.find(row => row.id === request.id) }));
  const makeTransport = () => createRouterTransport(router => {
    router.service(SystemService, { getStatus: () => ({ capabilities }) });
    router.service(ResourceService, { listResources: list, getResource: get });
    router.service(ConfigurationService, { saveConfiguration: save });
    router.service(ProviderService, { listProviderInventory: () => ({ entries: [], capabilities: [ProviderInventoryCapability.PROVIDER_ACTIVATION, ProviderInventoryCapability.ACTIVE_API_MODEL_FILTER, ProviderInventoryCapability.ACCOUNT_PROVIDER_FILTER, ProviderInventoryCapability.ACCOUNT_TYPE_FILTER] }) });
  });
  const transport = makeTransport();
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false, gcTime: 0 } } });
  const view = (children: ReactNode, current: Transport = transport) => <StrictMode><TransportProvider transport={current}><QueryClientProvider client={client}><MutationIntents>{children}</MutationIntents></QueryClientProvider></TransportProvider></StrictMode>;
  return { list, save, get, client, transport, makeTransport, view };
}
function choosePreferences() { fireEvent.click(screen.getByRole("button", { name: "Project defaults" })); }
function chooseGit() { fireEvent.click(screen.getByRole("button", { name: "Project defaults" })); }
function automaticFetch() { return screen.getByLabelText("Allow automatic fetch before Worktree preparation") as HTMLInputElement; }
function details() { return screen.getByText("Remediation details").closest("details")!; }

const known = { default_routing: "priority", automatic_fetch: false, notifications: false, remediation: { ci_failure: true, review_feedback: false, merge_conflict: true, conflict_strategy: "rebase", session_strategy: "dedicated", attempt_limit: 9, agent_id: newRequestId(), machine_id: newRequestId() } };
function saveButton() { return screen.getByRole("button", { name: "Save changes" }) as HTMLButtonElement; }
function discardButton() { return screen.getByRole("button", { name: "Discard changes" }) as HTMLButtonElement; }
function routing() { return screen.getByLabelText("Default account routing") as HTMLSelectElement; }

it.each([true, false])("opens the scoped Git form directly without writes, empty: %s", async empty => {
  const row = resource(EntityKind.SETTINGS, known), value = fixture(empty ? [] : [row]);
  render(value.view(<Settings />)); chooseGit();
  const form = await screen.findByRole("form", { name: "Project defaults form" });
  expect(automaticFetch().checked).toBe(empty);
  expect(screen.getAllByRole("checkbox")).toHaveLength(4); expect(details().open).toBe(false);
  expect(screen.getByLabelText("Default account routing")).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Network settings" })).toBeNull();
  for (const label of ["New Project defaults", "Edit Project defaults", "Delete Project defaults", "Cancel edit"]) expect(screen.queryByRole("button", { name: label })).toBeNull();
  expect(screen.queryByText("Saved git workflow")).toBeNull(); expect(screen.queryByRole("dialog")).toBeNull();
  expect(saveButton().disabled).toBe(true); expect(discardButton().disabled).toBe(true);
  fireEvent.click(automaticFetch()); expect(saveButton().disabled).toBe(false);
  fireEvent.click(discardButton()); expect(automaticFetch().checked).toBe(empty);
  expect(screen.getByRole("form")).toBe(form); expect(value.save).not.toHaveBeenCalled();
});

it.each([Code.PermissionDenied, Code.Unavailable])("does not admit Git defaults after an initial %s failure", async code => {
  const value = fixture([], () => { throw new ConnectError("Fixture read failure", code); });
  render(value.view(<Settings />)); chooseGit(); await screen.findByRole("alert");
  expect(screen.queryByRole("form")).toBeNull(); expect(value.save).not.toHaveBeenCalled();
});

it.each(["empty-continuation", "singleton-continuation", "multiple", "unsupported"])("retains Git identities without admitting an editor for %s", async variant => {
  const rows = variant === "empty-continuation" ? [] : [resource(EntityKind.SETTINGS, known)];
  if (variant === "multiple") rows.push(resource(EntityKind.SETTINGS, known));
  if (variant === "unsupported") rows[0].schemaVersion = 2;
  const value = fixture(rows, () => ({ resources: rows, nextPageToken: variant.endsWith("continuation") ? "opaque-page-2" : "" }));
  render(value.view(<Settings />)); chooseGit(); await screen.findByRole("region", { name: "Project defaults unavailable" });
  for (const row of rows) expect(screen.getByText(row.id)).toBeTruthy();
  expect(screen.queryByRole("form")).toBeNull(); expect(value.save).not.toHaveBeenCalled();
  if (variant.endsWith("continuation")) {
    fireEvent.click(screen.getByRole("button", { name: "Load more Settings pages" }));
    await waitFor(() => expect(value.list.mock.calls.some(([request]) => request.filter?.pageToken === "opaque-page-2")).toBe(true));
    expect(screen.queryByRole("form")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Refresh settings" }));
  }
});

it("retains a dirty Git draft and disclosure through failed refresh and reconnect", async () => {
  let fail = false;
  const rows = [resource(EntityKind.SETTINGS, known)];
  const value = fixture(rows, () => {
    if (fail) throw new ConnectError("Refresh unavailable", Code.Unavailable);
    return { resources: rows };
  });
  const view = render(value.view(<Settings />)); chooseGit(); const form = await screen.findByRole("form");
  fireEvent.click(automaticFetch()); details().open = true;
  fail = true; fireEvent.click(screen.getByRole("button", { name: "Refresh settings" }));
  await screen.findByText("Refresh failed. Showing the last successfully loaded results.");
  expect(screen.getByRole("form")).toBe(form); expect(automaticFetch().checked).toBe(true); expect(details().open).toBe(true);
  expect(saveButton().disabled).toBe(true); expect(discardButton().disabled).toBe(false);
  fail = false; view.rerender(value.view(<Settings />, value.makeTransport()));
  fireEvent.click(screen.getByRole("button", { name: "Refresh settings" }));
  await waitFor(() => expect(saveButton().disabled).toBe(false));
  expect(screen.getByRole("form")).toBe(form); expect(details().open).toBe(true); expect(value.save).not.toHaveBeenCalled();
});

it("discards Git revision drift into the latest full document and preserves hidden values", async () => {
  const original = resource(EntityKind.SETTINGS, known), rows = [original], value = fixture(rows);
  render(value.view(<Settings />)); chooseGit(); await screen.findByRole("form");
  fireEvent.click(automaticFetch());
  const latest = { ...known, default_routing: "fixed", retained_extension: { value: "keep" }, remediation: { ...known.remediation, attempt_limit: 11 } };
  rows[0] = { ...original, revision: 9n, documentJson: encode(latest) };
  fireEvent.click(screen.getByRole("button", { name: "Refresh settings" }));
  await screen.findByText(/Project defaults changed elsewhere/);
  expect(automaticFetch().checked).toBe(true); expect(saveButton().disabled).toBe(true);
  fireEvent.click(discardButton()); expect(automaticFetch().checked).toBe(false);
  fireEvent.click(automaticFetch()); await waitFor(() => expect(saveButton().disabled).toBe(false)); fireEvent.click(saveButton());
  await waitFor(() => expect(value.save).toHaveBeenCalledOnce());
  expect(value.save.mock.calls[0][0].mutation).toMatchObject({ id: original.id, expectedRevision: 9n });
  expect(JSON.parse(new TextDecoder().decode(value.save.mock.calls[0][0].documentJson))).toEqual({ ...latest, automatic_fetch: true });
});

it("freezes the Git draft and discard until the original uncertain save is retried", async () => {
  const value = fixture([resource(EntityKind.SETTINGS, known)]);
  value.save.mockRejectedValueOnce(new ConnectError("Lost response", Code.Unavailable));
  render(value.view(<Settings />)); chooseGit(); await screen.findByRole("form");
  fireEvent.click(automaticFetch()); fireEvent.click(saveButton());
  const retry = await screen.findByRole("button", { name: "Retry the same configuration" });
  expect(automaticFetch().matches(":disabled")).toBe(true); expect(saveButton().disabled).toBe(true); expect(discardButton().disabled).toBe(true);
  fireEvent.click(retry); await waitFor(() => expect(value.save).toHaveBeenCalledTimes(2));
  expect(value.save.mock.calls[1][0]).toEqual(value.save.mock.calls[0][0]);
  await waitFor(() => expect(screen.queryByText("Save outcome unknown.")).toBeNull());
  expect(automaticFetch().checked).toBe(true);
});

it("blocks a fresh Git save after a typed revision rejection until explicit discard", async () => {
  const original = resource(EntityKind.SETTINGS, known), rows = [original], value = fixture(rows);
  value.save.mockImplementationOnce(async () => {
    rows[0] = { ...original, revision: 9n, documentJson: encode({ ...known, default_routing: "fixed" }) };
    throw new ConnectError("Revision conflict", Code.Aborted);
  });
  render(value.view(<Settings />)); chooseGit(); await screen.findByRole("form");
  fireEvent.click(automaticFetch()); fireEvent.click(saveButton());
  await screen.findByText(/Project defaults changed elsewhere/);
  expect(automaticFetch().checked).toBe(true); expect(saveButton().disabled).toBe(true);
  fireEvent.click(discardButton()); expect(automaticFetch().checked).toBe(false);
  fireEvent.click(automaticFetch()); await waitFor(() => expect(saveButton().disabled).toBe(false)); fireEvent.click(saveButton());
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(2));
  expect(value.save.mock.calls[1][0].mutation).toMatchObject({ id: original.id, expectedRevision: 9n });
  expect(JSON.parse(new TextDecoder().decode(value.save.mock.calls[1][0].documentJson)).default_routing).toBe("fixed");
});

it("keeps the Git save result authoritative over an older pending read", async () => {
  const original = resource(EntityKind.SETTINGS, known), value = fixture([original]);
  let resolve!: (response: { resource: Resource }) => void;
  value.get.mockImplementation(request => request.id === original.id ? new Promise(done => { resolve = done; }) : { resource: undefined });
  render(value.view(<ConfigurationEditor kind={EntityKind.SETTINGS} initial={original} serverPreferenceSection={ServerPreferenceSection.GitWorkflow} presentation={ConfigurationEditorPresentation.InlineServerPreferences} preferencesObservation={{ complete: true, resource: original, fetching: false }} active saved={() => {}} cancel={() => {}} />));
  const form = screen.getByRole("form", { name: "Project defaults form" });
  fireEvent.click(automaticFetch()); fireEvent.click(saveButton());
  await waitFor(() => expect(screen.queryByText("Unsaved project defaults")).toBeNull());
  resolve({ resource: original }); await waitFor(() => expect(value.client.isFetching()).toBe(0));
  expect(automaticFetch().checked).toBe(true); expect(screen.getByRole("form")).toBe(form);
  expect(screen.queryByText(/changed elsewhere/)).toBeNull();
  fireEvent.click(automaticFetch()); fireEvent.click(saveButton());
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(2));
  expect(value.save.mock.calls[1][0].mutation).toMatchObject({ id: original.id, expectedRevision: 9n });
});

it("disposes a Git draft and disclosure on category departure while preserving saved settings", async () => {
  const value = fixture([resource(EntityKind.SETTINGS, known)]);
  render(value.view(<Settings />)); chooseGit(); await screen.findByRole("form");
  fireEvent.click(automaticFetch()); details().open = true;
  fireEvent.click(screen.getByRole("button", { name: "Server preferences" }));
  expect(screen.queryByText("Remediation details")).toBeNull();
  chooseGit(); await screen.findByRole("form", { name: "Project defaults form" });
  expect(automaticFetch().checked).toBe(false); expect(details().open).toBe(false);
  expect(saveButton().disabled).toBe(true); expect(value.save).not.toHaveBeenCalled();
});

it("shows an ordinary form only after a complete empty read without creating settings", async () => {
  let resolve!: (value: Page) => void;
  const pending = new Promise<Page>(done => { resolve = done; });
  const value = fixture([], () => pending);
  render(value.view(<Settings />)); choosePreferences();
  expect(screen.getByRole("status").textContent).toBe("Loading project defaults…");
  expect(screen.queryByRole("form")).toBeNull();
  expect(screen.queryByRole("button", { name: "New Server preferences" })).toBeNull();
  resolve({ resources: [] });
  const form = await screen.findByRole("form", { name: "Project defaults form" });
  expect(routing().value).toBe("sequential-exhaustion");
  expect(screen.getAllByRole("checkbox")).toHaveLength(4);
  expect(details().open).toBe(false);
  expect(saveButton().disabled).toBe(true); expect(discardButton().disabled).toBe(true);
  expect(screen.queryByText("No saved server preferences")).toBeNull();
  expect(screen.queryByRole("heading", { name: /New Server|Edit Server/ })).toBeNull();
  expect(screen.queryByRole("navigation", { name: "Settings pages" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Network settings" })).toBeNull();
  fireEvent.change(routing(), { target: { value: "priority" } });
  expect(saveButton().disabled).toBe(false); expect(discardButton().disabled).toBe(false);
  expect(screen.getByText("Unsaved project defaults")).toBeTruthy();
  fireEvent.click(discardButton());
  expect(routing().value).toBe("sequential-exhaustion"); expect(saveButton().disabled).toBe(true);
  expect(screen.getByRole("form")).toBe(form); expect(value.save).not.toHaveBeenCalled();
});

it.each([Code.PermissionDenied, Code.Unavailable])("does not fabricate defaults on an initial %s failure", async code => {
  const value = fixture([], () => { throw new ConnectError("Fixture read failure", code); });
  render(value.view(<Settings />)); choosePreferences(); await screen.findByRole("alert");
  expect(screen.queryByRole("form")).toBeNull(); expect(screen.queryByLabelText("Default account routing")).toBeNull();
  expect(screen.queryByRole("button", { name: "New Server preferences" })).toBeNull();
  expect(value.save).not.toHaveBeenCalled();
});

it.each(["empty-continuation", "singleton-continuation", "multiple"])("does not admit an editor for %s", async variant => {
  const rows = variant === "empty-continuation" ? [] : [resource(EntityKind.SETTINGS, known)];
  if (variant === "multiple") rows.push(resource(EntityKind.SETTINGS, known));
  const value = fixture(rows, () => ({ resources: rows, nextPageToken: variant === "multiple" ? "" : "opaque-page-2" }));
  render(value.view(<Settings />)); choosePreferences();
  await screen.findByRole("region", { name: "Project defaults unavailable" });
  for (const row of rows) expect(screen.getByText(row.id)).toBeTruthy();
  expect(screen.queryByRole("form")).toBeNull(); expect(screen.queryByRole("navigation", { name: "Settings pages" })).toBeNull();
  expect(value.save).not.toHaveBeenCalled();
});

it("opens saved values directly with no New, Edit, summary or deletion workflow", async () => {
  const row = resource(EntityKind.SETTINGS, known), value = fixture([row]);
  render(value.view(<Settings />)); choosePreferences(); await screen.findByRole("form");
  expect(routing().value).toBe("priority");
  expect(automaticFetch()).toBeTruthy();
  for (const label of ["New Server preferences", "Edit Server preferences", "Delete Server preferences"]) expect(screen.queryByRole("button", { name: label })).toBeNull();
  expect(screen.queryByText("Saved server preferences")).toBeNull(); expect(saveButton().disabled).toBe(true);
  expect(value.save).not.toHaveBeenCalled();
});

it.each(["future", "invalid-json", "missing-policy", "unknown-routing", "invalid-fetch"])("keeps identity without invented values for %s", async variant => {
  const row = resource(EntityKind.SETTINGS, variant === "missing-policy" ? {} : variant === "unknown-routing" ? { ...known, default_routing: "future-routing" } : variant === "invalid-fetch" ? { ...known, automatic_fetch: null } : known);
  if (variant === "future") row.schemaVersion = 2;
  if (variant === "invalid-json") row.documentJson = new Uint8Array([255]);
  const value = fixture([row]); render(value.view(<Settings />)); choosePreferences();
  const unavailable = within(await screen.findByRole("region", { name: "Project defaults unavailable" }));
  expect(unavailable.getByText(row.id)).toBeTruthy(); expect(unavailable.getByRole("status").textContent).toContain("Policy values are unavailable.");
  expect(screen.queryByRole("form")).toBeNull(); expect(screen.queryByLabelText("Default account routing")).toBeNull();
  expect(value.save).not.toHaveBeenCalled();
});

it.each([true, false])("preserves the mounted dirty form through refresh failure, empty: %s", async empty => {
  const rows = empty ? [] : [resource(EntityKind.SETTINGS, known)];
  let fail = false, reject!: (error: ConnectError) => void;
  const value = fixture(rows, () => fail ? new Promise<Page>((_resolve, rejection) => { reject = rejection; }) : { resources: rows });
  render(value.view(<Settings />)); choosePreferences(); const form = await screen.findByRole("form");
  fireEvent.change(routing(), { target: { value: "fixed" } });
  fail = true; fireEvent.click(screen.getByRole("button", { name: "Refresh settings" }));
  await screen.findByText("Refreshing project defaults…"); expect(saveButton().disabled).toBe(true);
  reject(new ConnectError("Refresh unavailable", Code.Unavailable));
  await screen.findByText("Refresh failed. Showing the last successfully loaded results.");
  expect(screen.getByRole("form")).toBe(form); expect(routing().value).toBe("fixed");
  expect(saveButton().disabled).toBe(true); expect(discardButton().disabled).toBe(false);
  fail = false; fireEvent.click(screen.getByRole("button", { name: "Refresh settings" }));
  await waitFor(() => expect(saveButton().disabled).toBe(false)); expect(routing().value).toBe("fixed");
  expect(value.save).not.toHaveBeenCalled();
});

it("adopts the first save and keeps the same form, disclosure and singleton for another save", async () => {
  const value = fixture(); render(value.view(<Settings />)); chooseGit(); const form = await screen.findByRole("form");
  details().open = true;
  fireEvent.click(automaticFetch()); fireEvent.click(saveButton());
  await waitFor(() => expect(value.save).toHaveBeenCalledOnce());
  await waitFor(() => expect(screen.queryByText("Unsaved project defaults")).toBeNull());
  expect(screen.getByRole("form")).toBe(form); expect(details().open).toBe(true); expect(saveButton().disabled).toBe(true);
  const first = (await value.save.mock.results[0].value).resource;
  fireEvent.click(screen.getByLabelText("Automatically fix required CI failures"));
  await waitFor(() => expect(saveButton().disabled).toBe(false)); fireEvent.click(saveButton());
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(2));
  expect(value.save.mock.calls[1][0].mutation).toMatchObject({ id: first.id, expectedRevision: first.revision });
  expect(screen.getByRole("form")).toBe(form);
  const { automatic_plan_approval: _newCapability, ...legacyDefaults } = newConfiguration(EntityKind.SETTINGS);
  expect(JSON.parse(new TextDecoder().decode(value.save.mock.calls[1][0].documentJson))).toEqual({ ...legacyDefaults, automatic_fetch: false, remediation: { ...legacyDefaults.remediation as Document, ci_failure: true } });
});

it("retains a changed draft on external revision drift and discards into the latest full document", async () => {
  const original = resource(EntityKind.SETTINGS, { ...known, retained_extension: { value: "keep" } });
  const rows = [original], value = fixture(rows);
  render(value.view(<Settings />)); choosePreferences(); await screen.findByRole("form");
  fireEvent.change(routing(), { target: { value: "fixed" } });
  rows[0] = { ...original, revision: 9n, documentJson: encode({ ...known, default_routing: "round-robin", retained_extension: { value: "latest" } }) };
  fireEvent.click(screen.getByRole("button", { name: "Refresh settings" }));
  await screen.findByText(/Project defaults changed elsewhere/);
  expect(routing().value).toBe("fixed"); expect(saveButton().disabled).toBe(true);
  fireEvent.click(discardButton()); expect(routing().value).toBe("round-robin");
  fireEvent.change(routing(), { target: { value: "reset-window" } });
  await waitFor(() => expect(saveButton().disabled).toBe(false)); fireEvent.click(saveButton());
  await waitFor(() => expect(value.save).toHaveBeenCalledOnce());
  expect(value.save.mock.calls[0][0].mutation).toMatchObject({ id: original.id, expectedRevision: 9n });
  expect(JSON.parse(new TextDecoder().decode(value.save.mock.calls[0][0].documentJson))).toEqual({ ...known, default_routing: "reset-window", retained_extension: { value: "latest" } });
});

it("adopts an external revision automatically only while the form is clean", async () => {
  const original = resource(EntityKind.SETTINGS, known), rows = [original], value = fixture(rows);
  render(value.view(<Settings />)); choosePreferences(); const form = await screen.findByRole("form");
  rows[0] = { ...original, revision: 9n, documentJson: encode({ ...known, default_routing: "round-robin" }) };
  fireEvent.click(screen.getByRole("button", { name: "Refresh settings" }));
  await waitFor(() => expect(routing().value).toBe("round-robin"));
  expect(saveButton().disabled).toBe(true); expect(screen.getByRole("form")).toBe(form); expect(value.save).not.toHaveBeenCalled();
});

it("retains the original request and freezes discard after an uncertain save", async () => {
  const value = fixture(); value.save.mockRejectedValueOnce(new ConnectError("Lost response", Code.Unavailable));
  render(value.view(<Settings />)); choosePreferences(); await screen.findByRole("form");
  fireEvent.change(routing(), { target: { value: "priority" } }); fireEvent.click(saveButton());
  const retry = await screen.findByRole("button", { name: "Retry the same configuration" });
  expect(discardButton().disabled).toBe(true); expect(saveButton().disabled).toBe(true); expect(routing().matches(":disabled")).toBe(true);
  fireEvent.click(retry); await waitFor(() => expect(value.save).toHaveBeenCalledTimes(2));
  expect(value.save.mock.calls[1][0]).toEqual(value.save.mock.calls[0][0]);
  await waitFor(() => expect(screen.queryByRole("button", { name: "Retry the same configuration" })).toBeNull());
  expect(routing().value).toBe("priority"); expect(value.save).toHaveBeenCalledTimes(2);
});

it("keeps a save response authoritative when a previously started read returns an older revision", async () => {
  const original = resource(EntityKind.SETTINGS, known), value = fixture([original]);
  let resolve!: (response: { resource: Resource }) => void;
  value.get.mockImplementation(request => request.id === original.id ? new Promise(done => { resolve = done; }) : { resource: undefined });
  render(value.view(<ConfigurationEditor kind={EntityKind.SETTINGS} initial={original} presentation={ConfigurationEditorPresentation.InlineServerPreferences} preferencesObservation={{ complete: true, resource: original, fetching: false }} active saved={() => {}} cancel={() => {}} />));
  fireEvent.change(routing(), { target: { value: "fixed" } }); fireEvent.click(saveButton());
  await waitFor(() => expect(value.save).toHaveBeenCalledOnce());
  await waitFor(() => expect(screen.queryByText("Unsaved project defaults")).toBeNull());
  resolve({ resource: original }); await waitFor(() => expect(value.client.isFetching()).toBe(0));
  expect(routing().value).toBe("fixed"); expect(screen.queryByText(/changed elsewhere/)).toBeNull();
  fireEvent.change(routing(), { target: { value: "reset-window" } }); expect(saveButton().disabled).toBe(false);
  fireEvent.click(saveButton()); await waitFor(() => expect(value.save).toHaveBeenCalledTimes(2));
  expect(value.save.mock.calls[1][0].mutation).toMatchObject({ id: original.id, expectedRevision: 9n });
});

it("keeps a dirty initial draft when another client creates the singleton", async () => {
  const rows: Resource[] = [], value = fixture(rows);
  render(value.view(<Settings />)); choosePreferences(); await screen.findByRole("form");
  fireEvent.change(routing(), { target: { value: "fixed" } });
  const external = resource(EntityKind.SETTINGS, known); rows.push(external);
  fireEvent.click(screen.getByRole("button", { name: "Refresh settings" }));
  await screen.findByText(/Project defaults changed elsewhere/);
  expect(routing().value).toBe("fixed"); expect(saveButton().disabled).toBe(true);
  fireEvent.click(discardButton()); expect(routing().value).toBe("priority");
  fireEvent.change(routing(), { target: { value: "remaining-quota" } });
  await waitFor(() => expect(saveButton().disabled).toBe(false)); fireEvent.click(saveButton());
  await waitFor(() => expect(value.save).toHaveBeenCalledOnce());
  expect(value.save.mock.calls[0][0].mutation).toMatchObject({ id: external.id, expectedRevision: external.revision });
});

it("blocks another save after a server revision rejection and reads the concurrent singleton", async () => {
  const rows: Resource[] = [], value = fixture(rows), external = resource(EntityKind.SETTINGS, known);
  value.save.mockImplementationOnce(async () => { rows.push(external); throw new ConnectError("Revision conflict", Code.Aborted); });
  render(value.view(<Settings />)); choosePreferences(); await screen.findByRole("form");
  fireEvent.change(routing(), { target: { value: "fixed" } }); fireEvent.click(saveButton());
  await screen.findByText(/Project defaults changed elsewhere/);
  expect(saveButton().disabled).toBe(true); expect(routing().value).toBe("fixed");
  fireEvent.click(discardButton()); expect(routing().value).toBe("priority");
  fireEvent.change(routing(), { target: { value: "reset-window" } });
  await waitFor(() => expect(saveButton().disabled).toBe(false)); fireEvent.click(saveButton());
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(2));
  expect(value.save.mock.calls[1][0].mutation).toMatchObject({ id: external.id, expectedRevision: external.revision });
});

it.each(["foreign-id", "foreign-kind", "old-revision", "unreadable"])("retains uncertainty instead of adopting a %s save response", async variant => {
  const original = resource(EntityKind.SETTINGS, known), value = fixture([original]);
  const returned = { ...original, revision: 9n };
  if (variant === "foreign-id") returned.id = newRequestId();
  if (variant === "foreign-kind") returned.kind = EntityKind.PROJECT;
  if (variant === "old-revision") returned.revision = original.revision;
  if (variant === "unreadable") returned.documentJson = new Uint8Array([255]);
  value.save.mockResolvedValueOnce({ resource: returned });
  render(value.view(<Settings />)); choosePreferences(); await screen.findByRole("form");
  fireEvent.change(routing(), { target: { value: "fixed" } }); fireEvent.click(saveButton());
  await screen.findByRole("button", { name: "Retry the same configuration" });
  expect(routing().value).toBe("fixed"); expect(discardButton().disabled).toBe(true); expect(saveButton().disabled).toBe(true);
  expect(value.save).toHaveBeenCalledOnce();
});

it("keeps a retained draft read-only when refresh finds unsupported settings", async () => {
  const original = resource(EntityKind.SETTINGS, known), rows = [original], value = fixture(rows);
  render(value.view(<Settings />)); choosePreferences(); const form = await screen.findByRole("form");
  fireEvent.change(routing(), { target: { value: "fixed" } }); rows[0] = { ...original, schemaVersion: 2, revision: 9n };
  fireEvent.click(screen.getByRole("button", { name: "Refresh settings" }));
  await screen.findByRole("region", { name: "Project defaults unavailable" });
  expect(screen.getByText(original.id)).toBeTruthy(); expect(screen.getByRole("form")).toBe(form);
  expect(routing().value).toBe("fixed"); expect(routing().matches(":disabled")).toBe(true); expect(saveButton().disabled).toBe(true);
  expect(value.save).not.toHaveBeenCalled();
});

it("retains the inline draft across same-identity transport replacement", async () => {
  const value = fixture([resource(EntityKind.SETTINGS, known)]);
  const view = render(value.view(<Settings />)); choosePreferences(); const form = await screen.findByRole("form");
  fireEvent.change(routing(), { target: { value: "fixed" } });
  view.rerender(value.view(<Settings />, value.makeTransport()));
  await waitFor(() => expect(value.client.isFetching()).toBe(0));
  expect(screen.getByRole("form")).toBe(form); expect(routing().value).toBe("fixed");
  expect(value.save).not.toHaveBeenCalled();
});

it("opens inline remediation details and focuses their first invalid control before saving", async () => {
  const value = fixture(); render(value.view(<Settings />)); chooseGit(); await screen.findByRole("form");
  details().open = true;
  const limit = screen.getByLabelText("Consecutive automatic attempt limit");
  fireEvent.change(limit, { target: { value: "0" } }); details().open = false;
  fireEvent.click(saveButton()); expect(details().open).toBe(true);
  await waitFor(() => expect(document.activeElement).toBe(limit)); expect(value.save).not.toHaveBeenCalled();
});

it("preserves mounted disclosure values and resource cursors through collapse and reconnect", async () => {
  const agent = resource(EntityKind.AGENT, { name: "Fix agent" });
  const machine = resource(EntityKind.MACHINE, { name: "Fix machine" });
  const secondAgent = resource(EntityKind.AGENT, { name: "Second page agent" });
  const value = fixture([agent, secondAgent, machine]);
  value.list.mockImplementation(request => request.filter?.kind === EntityKind.AGENT
    ? { resources: request.filter.pageToken ? [secondAgent] : [agent], nextPageToken: request.filter.pageToken ? "" : "agent-page-2" }
    : { resources: request.filter?.kind === EntityKind.MACHINE ? [machine] : [] });
  const element = <ConfigurationEditor kind={EntityKind.SETTINGS} active saved={() => {}} cancel={() => {}} />;
  const view = render(value.view(element));
  expect(details().open).toBe(false);
  expect(screen.getAllByRole("checkbox")).toHaveLength(4);
  fireEvent.click(screen.getByText("Remediation details")); details().open = true;
  fireEvent.click(screen.getByRole("combobox", { name: "Remediation Agent Worker" }));
  await screen.findByRole("option", { name: "Fix agent" });
  fireEvent.click(screen.getByRole("button", { name: "Load more Remediation Agent Worker" }));
  await screen.findByRole("option", { name: "Second page agent" });
  const limit = screen.getByLabelText("Consecutive automatic attempt limit");
  fireEvent.change(limit, { target: { value: "9" } });
  fireEvent.click(screen.getByRole("option", { name: "Second page agent" }));
  await waitFor(() => expect(screen.getByRole("combobox", { name: "Remediation Agent Worker" }).dataset.value).toBe(secondAgent.id));
  fireEvent.click(screen.getByRole("combobox", { name: "Remediation Runner Device" }));
  fireEvent.click(await screen.findByRole("option", { name: "Fix machine" }));
  await waitFor(() => expect(screen.getByRole("combobox", { name: "Remediation Runner Device" }).dataset.value).toBe(machine.id));
  fireEvent.change(screen.getByLabelText("Remediation session strategy"), { target: { value: "dedicated" } });
  fireEvent.click(screen.getByRole("button", { name: "Add reviewer selector" }));
  fireEvent.change(screen.getByLabelText("Selector 1 GitHub numeric ID"), { target: { value: "9007199254740993" } });
  fireEvent.change(screen.getByLabelText("Selector 1 GitHub node ID"), { target: { value: "BOT_exact" } });
  const reads = value.list.mock.calls.length;
  details().open = false; view.rerender(value.view(element, value.makeTransport()));
  expect(details().open).toBe(false); expect(screen.getByLabelText("Consecutive automatic attempt limit")).toBe(limit);
  details().open = true;
  expect((limit as HTMLInputElement).value).toBe("9");
  expect(screen.getByRole("combobox", { name: "Remediation Agent Worker" }).dataset.value).toBe(secondAgent.id);
  expect(screen.getByRole("combobox", { name: "Remediation Runner Device" }).dataset.value).toBe(machine.id);
  expect((screen.getByLabelText("Selector 1 GitHub numeric ID") as HTMLInputElement).value).toBe("9007199254740993");
  // Transport replacement may legitimately refetch. Toggling alone must not.
  await waitFor(() => expect(value.list.mock.calls.length).toBeGreaterThanOrEqual(reads));
  expect(value.list.mock.calls.filter(([request]) => request.filter?.kind === EntityKind.AGENT).at(-1)?.[0].filter?.pageToken).toBe("agent-page-2");
  const afterReconnect = value.list.mock.calls.length; details().open = false; details().open = true;
  expect(value.list.mock.calls.length).toBe(afterReconnect); expect(value.save).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Save Project defaults" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledOnce());
  expect(JSON.parse(new TextDecoder().decode((value.save.mock.calls[0][0] as { documentJson: Uint8Array }).documentJson)).remediation.reviewer_selectors[0].id).toBe("9007199254740993");
});

it.each(["", "0", "reviewer"])("reveals and focuses a hidden invalid %s input before allowing save", async invalid => {
  const value = fixture(); render(value.view(<ConfigurationEditor kind={EntityKind.SETTINGS} active saved={() => {}} cancel={() => {}} />));
  details().open = true;
  let control: HTMLElement;
  if (invalid === "reviewer") { fireEvent.click(screen.getByRole("button", { name: "Add reviewer selector" })); control = screen.getByLabelText("Selector 1 GitHub numeric ID"); }
  else { control = screen.getByLabelText("Consecutive automatic attempt limit"); fireEvent.change(control, { target: { value: invalid } }); }
  details().open = false;
  fireEvent.click(screen.getByRole("button", { name: "Save Project defaults" }));
  expect(details().open).toBe(true);
  await waitFor(() => expect(document.activeElement).toBe(control));
  expect(value.save).not.toHaveBeenCalled();
});

it("retains a draft after revision drift and blocks a fresh save", async () => {
  const row = resource(EntityKind.SETTINGS, known);
  const value = fixture([row]); render(value.view(<ConfigurationEditor kind={EntityKind.SETTINGS} initial={row} active saved={() => {}} cancel={() => {}} />));
  fireEvent.change(screen.getByLabelText("Default account routing"), { target: { value: "fixed" } });
  value.get.mockReturnValue({ resource: { ...row, revision: 9n } });
  await value.client.invalidateQueries();
  await screen.findByText(/This entry changed elsewhere/);
  expect((screen.getByLabelText("Default account routing") as HTMLSelectElement).value).toBe("fixed");
  expect((screen.getByRole("button", { name: "Save Project defaults" }) as HTMLButtonElement).disabled).toBe(true);
  expect(value.save).not.toHaveBeenCalled();
});

it("disposes a changed disclosure and ignores a late save in a replacement visit", async () => {
  const value = fixture(); let resolve!: (response: { resource: Resource }) => void;
  value.save.mockImplementation(() => new Promise(done => { resolve = done; }));
  const view = render(value.view(<Settings visible />)); chooseGit(); await screen.findByRole("form");
  details().open = true; fireEvent.change(screen.getByLabelText("Consecutive automatic attempt limit"), { target: { value: "11" } });
  fireEvent.click(saveButton()); await waitFor(() => expect(value.save).toHaveBeenCalledOnce());
  expect(discardButton().disabled).toBe(true);
  view.rerender(value.view(<Settings visible={false} />)); view.rerender(value.view(<Settings visible />));
  expect(screen.getByRole("heading", { level: 1, name: "AI Subscription" })).toBeTruthy();
  chooseGit(); await screen.findByRole("form");
  resolve({ resource: resource(EntityKind.SETTINGS, known) }); await waitFor(() => expect(value.client.isMutating()).toBe(0));
  expect(screen.queryByRole("button", { name: "Retry the same configuration" })).toBeNull();
  expect(details().open).toBe(false); expect((screen.getByLabelText("Consecutive automatic attempt limit") as HTMLInputElement).value).toBe("3");
  expect(automaticFetch().checked).toBe(true); expect(value.save).toHaveBeenCalledOnce();
});

it("ignores a late prior read after its Settings visit was replaced", async () => {
  let resolve!: (response: Page) => void, waiting = true;
  const pending = new Promise<Page>(done => { resolve = done; });
  const value = fixture([], () => waiting ? pending : { resources: [] });
  const view = render(value.view(<Settings visible />)); choosePreferences();
  await waitFor(() => expect(value.list.mock.calls.some(([request]) => request.filter?.kind === EntityKind.SETTINGS)).toBe(true));
  view.rerender(value.view(<Settings visible={false} />)); waiting = false;
  view.rerender(value.view(<Settings visible />)); choosePreferences(); await screen.findByRole("form");
  resolve({ resources: [resource(EntityKind.SETTINGS, known)] }); await waitFor(() => expect(value.client.isFetching()).toBe(0));
  expect(routing().value).toBe("sequential-exhaustion"); expect(value.save).not.toHaveBeenCalled();
});

it("assigns all policy groups to Project defaults and only Network to Server preferences", async () => {
  const value = fixture([resource(EntityKind.SETTINGS, known)]);
  render(value.view(<Settings />)); choosePreferences(); await screen.findByRole("form", { name: "Project defaults form" });
  expect(screen.queryByRole("button", { name: "Git", exact: true })).toBeNull();
  expect(routing()).toBeTruthy(); expect(automaticFetch()).toBeTruthy(); expect(details().open).toBe(false);
  expect(screen.queryByRole("button", { name: "Network settings" })).toBeNull();
  const policyReads = value.list.mock.calls.filter(([request]) => request.filter?.kind === EntityKind.SETTINGS).length;
  fireEvent.click(screen.getByRole("button", { name: "Server preferences" }));
  expect(screen.getByRole("button", { name: "Network settings" }).closest(".settings-content")?.classList.contains("settings-server-preferences")).toBe(true);
  expect(screen.queryByRole("form")).toBeNull(); expect(screen.queryByLabelText("Default account routing")).toBeNull();
  expect(screen.queryByRole("button", { name: "Save changes" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Discard changes" })).toBeNull();
  expect(value.list.mock.calls.filter(([request]) => request.filter?.kind === EntityKind.SETTINGS)).toHaveLength(policyReads);
  expect(value.save).not.toHaveBeenCalled();
});

it("redirects the retained Git destination to the sole policy editor", async () => {
 const value = fixture([resource(EntityKind.SETTINGS, known)]);
 render(value.view(<Settings entryDestination={{ category: SettingsCategory.GitWorkflow, generation: "legacy-git" }} />));
 await screen.findByRole("form", { name: "Project defaults form" });
 expect(screen.getByRole("heading", { level: 1, name: "Project defaults" })).toBeTruthy();
 expect(screen.queryByRole("button", { name: "Git", exact: true })).toBeNull();
 expect(routing()).toBeTruthy(); expect(automaticFetch()).toBeTruthy();
 expect(value.save).not.toHaveBeenCalled();
});

it("shows all four schema-2 policy groups only in Project defaults", async () => {
 const row = resource(EntityKind.SETTINGS, { ...known, automatic_plan_approval: false }); row.schemaVersion = 2;
 const value = fixture([row], undefined, [SystemCapability.PROJECT_BEHAVIOR_SETTINGS_V1]);
 render(value.view(<Settings />)); choosePreferences(); await screen.findByRole("form");
 expect(await screen.findByRole("checkbox", { name: "Automatically approve native plans" })).toBeTruthy();
 expect(screen.getByRole("heading", { name: "Plan approval" })).toBeTruthy();
 expect(routing()).toBeTruthy(); expect(automaticFetch()).toBeTruthy(); expect(details().open).toBe(false);
 fireEvent.click(screen.getByRole("button", { name: "Server preferences" }));
 expect(screen.queryByRole("checkbox", { name: "Automatically approve native plans" })).toBeNull();
 expect(screen.queryByLabelText("Default account routing")).toBeNull();
 expect(screen.queryByText("Remediation details")).toBeNull();
 expect(value.save).not.toHaveBeenCalled();
});
