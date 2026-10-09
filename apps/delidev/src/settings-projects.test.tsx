// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { StrictMode, useState } from "react";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { ConfigurationService, EntityKind, ErrorDetailSchema, ProviderService, ResourceSchema, ResourceService, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { encode, type Document } from "./documents";
import { Settings, SettingsEntryDestination } from "./settings";

function resource(kind: EntityKind, data: Document, revision = 7n) { return create(ResourceSchema, { id: newRequestId(), kind, revision, schemaVersion: 1, documentJson: encode(data) }); }
function project(name: string) { return resource(EntityKind.PROJECT, { name, repositories: [], primary_repository: "", agents: { configured: false, ids: [] }, accounts: { configured: false, ids: [] } }); }
function deferred<T>() {
  let resolve!: (value: T) => void, reject!: (error: unknown) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
type Page = { resources: Resource[]; nextPageToken?: string };
const creationRepository = resource(EntityKind.REPOSITORY, { name: "Fixture repository" });
function fixture(resources: Resource[] = [creationRepository]) {
  const list = vi.fn(async (kind: EntityKind, _page: string): Promise<Page> => ({ resources: resources.filter(row => row.kind === kind) }));
  const get = vi.fn(async (id: string) => ({ resource: resources.find(row => row.id === id) }));
  const save = vi.fn(async (request: { documentJson: Uint8Array }) => ({ resource: resource(EntityKind.PROJECT, JSON.parse(new TextDecoder().decode(request.documentJson))) }));
  const remove = vi.fn(async (_request: unknown) => ({}));
  const transport = createRouterTransport(router => {
    router.service(ResourceService, { listResources: request => list(request.filter?.kind ?? EntityKind.UNSPECIFIED, request.filter?.pageToken ?? ""), getResource: request => get(request.id) });
    router.service(ConfigurationService, { saveConfiguration: save, deleteConfiguration: remove });
    router.service(ProviderService, { listProviderInventory: () => ({ entries: [] }), listProviderPresets: () => ({ presetsJson: encode([]) }) });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity }, mutations: { retry: false, gcTime: 0 } } });
  const view = (children: React.ReactNode) => <StrictMode><TransportProvider transport={transport}><QueryClientProvider client={client}>{children}</QueryClientProvider></TransportProvider></StrictMode>;
  return { list, get, save, remove, client, view };
}
function openProjects(value: ReturnType<typeof fixture>) {
  render(value.view(<Settings />));
  fireEvent.click(screen.getByRole("button", { name: "Projects" }));
}
function decoded(request: unknown) { return JSON.parse(new TextDecoder().decode((request as { documentJson: Uint8Array }).documentJson)); }
async function configureProject(name: string) {
  fireEvent.click(await screen.findByRole("checkbox", { name: "Fixture repository" }));
  fireEvent.click(screen.getByRole("button", { name: "Next" }));
  fireEvent.change(screen.getByRole("textbox", { name: "Project name" }), { target: { value: name } });
  fireEvent.change(screen.getByRole("combobox", { name: "Primary repository" }), { target: { value: creationRepository.id } });
  fireEvent.click(screen.getByRole("button", { name: "Next" }));
}

it("shows final first-page emptiness with exact help, one create action and no pagination", async () => {
  const value = fixture(); openProjects(value);
  const empty = await screen.findByRole("region", { name: "No projects yet" });
  expect(within(empty).getByText("Group repositories and choose which Agent Workers and AI accounts a project can use.")).toBeTruthy();
  expect(within(empty).getByText("Choose New Project to get started.")).toBeTruthy();
  expect(screen.getAllByRole("button", { name: "New Project" })).toHaveLength(1);
  expect(screen.queryByRole("navigation", { name: "Settings pages" })).toBeNull();
});
it("reads an empty continuation and renders later projects without claiming final emptiness", async () => {
  const later = project("Later project");
  const value = fixture(); value.list.mockImplementation(async (_kind, page) => page ? { resources: [later] } : { resources: [], nextPageToken: "projects-2" }); openProjects(value);
  await screen.findByText("No projects on this page."); expect(screen.queryByText("No projects yet")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Load more Settings pages" }));
  await screen.findByRole("heading", { name: "Later project" });
  expect(value.list).toHaveBeenCalledWith(EntityKind.PROJECT, "projects-2");
  expect(screen.queryByText("No projects yet")).toBeNull();
  expect(screen.queryByRole("button", { name: "First page" })).toBeNull();
});

it("groups rows in server order with complete identities, schema guards and no extra reads", async () => {
  const last = project(`Zulu ${"long project name ".repeat(30)}`), first = project("Alpha"), future = create(ResourceSchema, { ...project("Future project"), schemaVersion: 2 });
  const value = fixture([last, first, future]); openProjects(value);
  const list = await screen.findByRole("region", { name: "Saved projects" });
  expect(within(list).getAllByRole("heading").map(node => node.textContent)).toEqual(["Zulu " + "long project name ".repeat(30), "Alpha", "Unnamed"]);
  for (const row of [last, first, future]) expect(within(list).getByText(row.id)).toBeTruthy();
  for (const action of ["Edit", "Delete"]) expect((screen.getByRole("button", { name: `${action} Unnamed` }) as HTMLButtonElement).disabled).toBe(true);
  expect(value.get).not.toHaveBeenCalled();
  expect(value.list.mock.calls.filter(([kind]) => kind === EntityKind.PROJECT)).toEqual([[EntityKind.PROJECT, ""], [EntityKind.PROJECT, ""]]);
});
it.each([Code.PermissionDenied, Code.Unauthenticated, Code.Unavailable])("separates loading/failure %s from empty success and preserves creation availability", async code => {
  const value = fixture(), pending = deferred<Page>(), correlationId = newRequestId(); value.list.mockReturnValue(pending.promise); openProjects(value);
  expect(screen.getByRole("status").textContent).toBe("Loading projects…"); expect(screen.queryByText("No projects yet")).toBeNull();
  expect((screen.getByRole("button", { name: "New Project" }) as HTMLButtonElement).disabled).toBe(false);
  await act(async () => pending.reject(new ConnectError("The server denied this read.", code, undefined, [{ desc: ErrorDetailSchema, value: create(ErrorDetailSchema, { code: "unavailable", guidance: "Retry the selected server read.", correlationId }) }])));
  const alert = await screen.findByRole("alert"); expect(alert.textContent).not.toContain("private fixture detail"); expect(alert.textContent).toContain(correlationId);
  expect(screen.queryByText("No projects yet")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "New Project" })); expect(screen.getByRole("button", { name: "Next" })).toBeTruthy();
});
it.each([false, true])("retains cached content during refresh and reports failed refresh truthfully (empty=%s)", async empty => {
  const value = fixture(empty ? [] : [project("Retained project")]); openProjects(value);
  await screen.findByText(empty ? "No projects yet" : "Retained project");
  const pending = deferred<Page>(), correlationId = newRequestId(); value.list.mockReturnValue(pending.promise); fireEvent.click(screen.getByRole("button", { name: "Refresh settings" }));
  await waitFor(() => expect(value.list.mock.calls.filter(([kind]) => kind === EntityKind.PROJECT)).toHaveLength(2));
  expect(screen.getByText(empty ? "No projects yet" : "Retained project")).toBeTruthy();
  await act(async () => pending.reject(new ConnectError("The server is temporarily unavailable.", Code.Unavailable, undefined, [{ desc: ErrorDetailSchema, value: create(ErrorDetailSchema, { code: "unavailable", guidance: "Retry the selected server read.", correlationId }) }])));
  await screen.findByText("Refresh failed. Showing the last successfully loaded results."); expect(screen.getByRole("alert").textContent).toContain(correlationId);
  if (empty) expect(screen.queryByText("No projects yet")).toBeNull(); else expect(screen.getByText("Retained project")).toBeTruthy();
});
it("preserves four field groups, repository order, primary clearing, restrictions and full documents", async () => {
  const first = resource(EntityKind.REPOSITORY, { name: "First" }), second = resource(EntityKind.REPOSITORY, { name: "Second" }), row = project("Editable project");
  row.documentJson = encode({ name: "Editable project", repositories: [first.id, second.id], primary_repository: first.id, agents: { configured: true, ids: [newRequestId()] }, accounts: { configured: false, ids: [] }, extension: { retained: true } });
  const value = fixture([row, first, second]); openProjects(value); fireEvent.click(await screen.findByRole("button", { name: "Edit Editable project" }));
  expect(screen.getByText("The harness starts in this repository. Select it explicitly after adding repositories.")).toBeTruthy();
  expect(screen.queryByText(/The first repository you select becomes the primary repository/)).toBeNull();
  for (const label of ["Name", "Repositories", "Agent Workers", "AI accounts"]) expect(screen.getByRole("group", { name: label })).toBeTruthy();
  await within(screen.getByRole("combobox", { name: "Primary repository" })).findByRole("option", { name: "First" });
  expect(within(screen.getByRole("combobox", { name: "Primary repository" })).getByRole("option", { name: "Second" }).getAttribute("value")).toBe(second.id);
  const repositories = screen.getByRole("group", { name: "Repositories" }); const grip = within(repositories).getByRole("button", { name: "Move repository 2: Second" }); fireEvent.keyDown(grip, { key: " " }); fireEvent.keyDown(grip, { key: "ArrowUp" }); expect(value.save).not.toHaveBeenCalled(); fireEvent.keyDown(grip, { key: "Enter" });
  expect(value.save).not.toHaveBeenCalled();
  expect(Array.from(repositories.querySelectorAll("li[data-repository-id]"), node => node.getAttribute("data-repository-id"))).toEqual([second.id, first.id]);
  fireEvent.click(within(repositories).getByRole("button", { name: "Remove entry 2" }));
  const primary = screen.getByRole("combobox", { name: "Primary repository" }) as HTMLSelectElement; expect(primary.value).toBe(""); expect(primary.required).toBe(true); expect(primary.checkValidity()).toBe(false);
  fireEvent.change(primary, { target: { value: second.id } });
  fireEvent.click(screen.getByRole("checkbox", { name: "Restrict agent workers" })); fireEvent.click(screen.getByRole("checkbox", { name: "Restrict agent workers" }));
  fireEvent.click(screen.getByRole("checkbox", { name: "Restrict ai accounts" })); expect(screen.getAllByText("An empty selection permits none. Turning this restriction off permits every otherwise eligible entry.")).toHaveLength(2);
  fireEvent.click(screen.getByRole("checkbox", { name: "Restrict ai accounts" })); fireEvent.click(screen.getByRole("button", { name: "Save Project" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  expect(decoded(value.save.mock.calls[0][0])).toEqual({ name: "Editable project", repositories: [second.id], primary_repository: second.id, agents: { configured: true, ids: [] }, accounts: { configured: false, ids: [] }, extension: { retained: true } });
});

it("retries exact edit repository IDs without replacing unreadable names with selector labels", async () => {
  const first = resource(EntityKind.REPOSITORY, { name: "Exact repository" }), other = resource(EntityKind.REPOSITORY, { name: "Other repository" }), row = project("Name lookup project");
  row.documentJson = encode({ name: "Name lookup project", repositories: [first.id], primary_repository: first.id, agents: { configured: false, ids: [] }, accounts: { configured: false, ids: [] } });
  const value = fixture([row, first, other]); let readable = false;
  value.get.mockImplementation(async id => {
    if (id === first.id && !readable) throw new ConnectError("Name read unavailable", Code.Unavailable);
    return { resource: [row, first, other].find(candidate => candidate.id === id) };
  });
  openProjects(value); fireEvent.click(await screen.findByRole("button", { name: "Edit Name lookup project" }));
  const primary = screen.getByRole("combobox", { name: "Primary repository" }) as HTMLSelectElement;
  const retry = await screen.findByRole("button", { name: "Retry repository loading" });
  expect(within(primary).getByRole("option", { name: "Repository name unavailable (entry 1)" })).toBeTruthy();
  const reads = value.get.mock.calls.filter(([id]) => id === first.id).length;
  readable = true; fireEvent.click(retry);
  await waitFor(() => expect(within(primary).getByRole("option", { name: "Exact repository" })).toBeTruthy());
  expect(primary.value).toBe(first.id); expect(value.get.mock.calls.filter(([id]) => id === first.id)).toHaveLength(reads + 1);
  expect(within(primary).queryByRole("option", { name: "Other repository" })).toBeNull();
});
it.each(["revision", "failure"])("retains an edit draft and blocks Save after current-resource %s", async reason => {
  const row = project("Original"), value = fixture([row]);
  if (reason === "failure") value.get.mockRejectedValue(new ConnectError("current unavailable", Code.Unavailable)); else value.get.mockResolvedValue({ resource: create(ResourceSchema, { ...row, revision: 8n }) });
  openProjects(value); fireEvent.click(await screen.findByRole("button", { name: "Edit Original" })); fireEvent.change(screen.getByRole("textbox", { name: "Name" }), { target: { value: "Retained draft" } }); await screen.findByRole("alert");
  expect((screen.getByRole("textbox", { name: "Name" }) as HTMLInputElement).value).toBe("Retained draft"); expect((screen.getByRole("button", { name: "Save Project" }) as HTMLButtonElement).disabled).toBe(true); expect(value.save).not.toHaveBeenCalled();
});
it.each(["save", "delete"])("explicitly retries the exact project %s request within its opening", async action => {
  const row = project("Retry project"), value = fixture([row]), operation = action === "save" ? value.save : value.remove; operation.mockRejectedValueOnce(new ConnectError("ack lost", Code.Unavailable));
  openProjects(value); fireEvent.click(await screen.findByRole("button", { name: `${action === "save" ? "Edit" : "Delete"} Retry project` }));
  if (action === "delete") { expect(screen.getByText(/Retained sessions and history remain/)).toBeTruthy(); expect(screen.getByText(/Schedules using this configuration will be disabled/)).toBeTruthy(); }
  const submit = action === "save" ? "Save Project" : "Confirm configuration deletion";
  // Submit directly to isolate retry identity from HTML required-field validation.
  if (action === "save") fireEvent.submit((screen.getByRole("button", { name: submit }) as HTMLButtonElement).form!); else fireEvent.click(screen.getByRole("button", { name: submit }));
  const retry = await screen.findByRole("button", { name: action === "save" ? "Retry the same configuration" : "Retry the same deletion" });
  expect((screen.getByRole("button", { name: "Repositories" }) as HTMLButtonElement).disabled).toBe(false); expect((screen.getByRole("button", { name: submit }) as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(retry); await waitFor(() => expect(operation).toHaveBeenCalledTimes(2)); expect(operation.mock.calls[1][0]).toEqual(operation.mock.calls[0][0]); expect(operation.mock.calls[0][0]).toMatchObject({ mutation: { id: row.id, expectedRevision: 7n } });
});
it.each(["navigation", "Escape then navigation"])("discards a project draft and uncertain retry after %s without replay", async route => {
  const value = fixture(); value.save.mockRejectedValue(new ConnectError("ack lost", Code.Unavailable));
  function Harness() { const [visible, show] = useState(false); return <><button onClick={() => show(true)}>Open settings fixture</button><button onClick={(event) => { event.currentTarget.focus(); show(false); }}>Leave Settings fixture</button><Settings visible={visible} /></>; }
  render(value.view(<Harness />)); const opener = screen.getByRole("button", { name: "Open settings fixture" }); opener.focus(); fireEvent.click(opener);
  fireEvent.click(screen.getByRole("button", { name: "Projects" })); fireEvent.click(screen.getByRole("button", { name: "New Project" })); await configureProject("Abandoned project"); fireEvent.submit((screen.getByRole("button", { name: "Save Project" }) as HTMLButtonElement).form!); await screen.findByRole("button", { name: "Retry the same configuration" });
  if (route === "Escape then navigation") { fireEvent.keyDown(screen.getByRole("region", { name: "Settings content" }), { key: "Escape" }); expect(screen.getByRole("button", { name: "Retry the same configuration" })).toBeTruthy(); }
  fireEvent.click(screen.getByRole("button", { name: "Leave Settings fixture" }));
  expect(document.activeElement).not.toBe(opener); fireEvent.click(opener); expect(screen.getByRole("heading", { level: 1, name: "AI Subscription" })).toBeTruthy(); expect(screen.queryByRole("button", { name: "Retry the same configuration" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Projects" })); fireEvent.click(screen.getByRole("button", { name: "New Project" })); expect((screen.getByRole("searchbox", { name: "Search repository names" }) as HTMLInputElement).value).toBe(""); expect(value.save).toHaveBeenCalledTimes(1);
});
it("focuses targeted creation and confines the presentation to Projects", async () => {
  const value = fixture(); render(value.view(<Settings entryDestination={SettingsEntryDestination.NewProject} />)); await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("searchbox", { name: "Search repository names" })));
  expect(screen.getByRole("region", { name: "Settings content" }).classList.contains("settings-projects")).toBe(true); fireEvent.click(screen.getByRole("button", { name: "Close New Project" }));
  for (const category of ["Repositories", "Instructions", "API Providers"]) { fireEvent.click(screen.getByRole("button", { name: category })); expect(screen.getByRole("region", { name: "Settings content" }).classList.contains("settings-projects")).toBe(false); expect(screen.getByRole("heading", { level: 1, name: category })).toBeTruthy(); }
});
it("consumes explicit New Project and Repositories entry while another category has an unsaved form", async () => {
  const value = fixture(), consumed = vi.fn(), view = render(value.view(<Settings />));
  fireEvent.click(screen.getByRole("button", { name: "Instructions" })); fireEvent.click(screen.getByRole("button", { name: "New Instructions" }));
  fireEvent.change(screen.getByRole("textbox", { name: "Name" }), { target: { value: "Discard this draft" } });
  view.rerender(value.view(<Settings entryDestination={SettingsEntryDestination.NewProject} destinationConsumed={consumed} />));
  await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("searchbox", { name: "Search repository names" })));
  expect((screen.getByRole("searchbox", { name: "Search repository names" }) as HTMLInputElement).value).toBe(""); expect(consumed).toHaveBeenCalledTimes(1);
  fireEvent.change(screen.getByRole("searchbox", { name: "Search repository names" }), { target: { value: "Keep current project draft" } });
  view.rerender(value.view(<Settings entryDestination={SettingsEntryDestination.NewProject} destinationConsumed={consumed} />));
  expect((screen.getByRole("searchbox", { name: "Search repository names" }) as HTMLInputElement).value).toBe("Keep current project draft"); expect(consumed).toHaveBeenCalledTimes(1);
  view.rerender(value.view(<Settings entryDestination={SettingsEntryDestination.Repositories} destinationConsumed={consumed} />));
  await screen.findByRole("heading", { level: 1, name: "Repositories" }); expect(screen.queryByRole("textbox", { name: "Name" })).toBeNull(); expect(consumed).toHaveBeenCalledTimes(2);
  expect(value.save).not.toHaveBeenCalled();
});


it("refreshes only the active category metadata through the shared Settings header", async () => {
  const repository = resource(EntityKind.REPOSITORY, { name: "Initial repository", remote_url: "https://example.org/repository" });
  const model = resource(EntityKind.MODEL, { name: "Initial model", native_id: "initial-model" });
  const worker = resource(EntityKind.AGENT, { name: "Refresh Worker", harness: "codex", model_id: model.id, accounts: [{ id: newRequestId(), weight: 1 }] });
  const row = project("Refresh project");
  row.documentJson = encode({ name: "Refresh project", repositories: [repository.id], primary_repository: repository.id, agents: { configured: false, ids: [] }, accounts: { configured: false, ids: [] } });
  const value = fixture([row, repository, worker, model]); openProjects(value);
  await screen.findByText("Initial repository");
  const repositoryReads = value.get.mock.calls.filter(([id]) => id === repository.id).length;
  repository.revision += 1n; repository.documentJson = encode({ name: "Updated repository", remote_url: "https://example.org/updated" });
  fireEvent.click(screen.getByRole("button", { name: "Refresh settings" }));
  await screen.findByText("Updated repository");
  expect(value.get.mock.calls.filter(([id]) => id === repository.id)).toHaveLength(repositoryReads + 1);
  expect(value.get.mock.calls.filter(([id]) => id === model.id)).toHaveLength(0);
  fireEvent.click(screen.getByRole("button", { name: "Agent Workers" }));
  await screen.findByText("initial-model");
  const modelReads = value.get.mock.calls.filter(([id]) => id === model.id).length;
  model.revision += 1n; model.documentJson = encode({ name: "Updated model", native_id: "updated-model" });
  fireEvent.click(screen.getByRole("button", { name: "Refresh settings" }));
  await screen.findByText("updated-model");
  expect(value.get.mock.calls.filter(([id]) => id === model.id)).toHaveLength(modelReads + 1);
  expect(value.get.mock.calls.filter(([id]) => id === repository.id)).toHaveLength(repositoryReads + 1);
});
