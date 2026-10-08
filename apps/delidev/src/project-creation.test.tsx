// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { StrictMode, useState, type ReactNode } from "react";
import { expect, it, vi } from "vitest";
import { ConfigurationService, EntityKind, ResourceSchema, ResourceService, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { ConfigurationEditor } from "./settings";
import { ProjectCreation } from "./project-creation";
import { MutationIntents } from "./mutation";
import { encode, type Document } from "./documents";
import { i18n, SupportedLanguage } from "./localization";

function reorder(position: number) { const grip = screen.getAllByRole("button", { name: /^Move repository/ })[position - 1]!; fireEvent.keyDown(grip, { key: " " }); fireEvent.keyDown(grip, { key: "ArrowUp" }); fireEvent.keyDown(grip, { key: "Enter" }); }
function repository(name: string) { return create(ResourceSchema, { id: newRequestId(), kind: EntityKind.REPOSITORY, revision: 7n, schemaVersion: 1, documentJson: encode({ name, private_metadata: "not retained in the search cache" }) }); }
type Page = { resources: Resource[]; nextPageToken?: string };
function deferred<T>() { let resolve!: (value: T) => void; const promise = new Promise<T>(yes => { resolve = yes; }); return { resolve, promise }; }
function fixture(rows = [repository("oss"), repository("delidev")], initialData?: Document) {
  const list = vi.fn(async (_token: string): Promise<Page> => ({ resources: rows }));
  const get = vi.fn(async (id: string) => ({ resource: rows.find(row => row.id === id) }));
  const save = vi.fn(async (request: { documentJson: Uint8Array }) => ({ resource: create(ResourceSchema, { id: newRequestId(), kind: EntityKind.PROJECT, revision: 1n, schemaVersion: 1, documentJson: request.documentJson }) }));
  const transport = createRouterTransport(router => {
    router.service(ResourceService, { listResources: request => request.filter?.kind === EntityKind.REPOSITORY ? list(request.filter.pageToken) : { resources: [] }, getResource: request => get(request.id) });
    router.service(ConfigurationService, { saveConfiguration: save });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false, gcTime: 0 } } });
  const view = (content?: ReactNode) => <StrictMode><TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents>{content ?? <ConfigurationEditor kind={EntityKind.PROJECT} initialData={initialData} active saved={() => {}} cancel={() => {}} />}</MutationIntents></QueryClientProvider></TransportProvider></StrictMode>;
  return { rows, list, get, save, client, view };
}
const next = () => fireEvent.click(screen.getByRole("button", { name: "Next" }));
const previous = () => fireEvent.click(screen.getByRole("button", { name: "Previous" }));
const name = () => screen.getByRole("textbox", { name: "Project name" }) as HTMLInputElement;
const primary = () => screen.getByRole("combobox", { name: "Primary repository" }) as HTMLSelectElement;
const search = () => screen.getByRole("searchbox", { name: "Search repository names" }) as HTMLInputElement;
const choose = async (label: string) => fireEvent.click(await screen.findByRole("checkbox", { name: label }));
function configure(id: string) { next(); fireEvent.change(primary(), { target: { value: id } }); next(); }

it("defaults the first primary, permits an explicit override, preserves configured-empty restrictions and saves UUIDs once", async () => {
  const value = fixture(); render(value.view());
  await waitFor(() => expect(document.activeElement).toBe(search()));
  expect(screen.queryByRole("button", { name: "Save Project" })).toBeNull();
  expect((screen.getByRole("button", { name: "Next" }) as HTMLButtonElement).disabled).toBe(true);
  await choose("oss"); await choose("delidev"); next();
  expect(name().value).toBe("oss"); expect(document.activeElement).toBe(name());
  expect(primary().value).toBe(value.rows[0].id); expect(within(primary()).getByRole("option", { name: "oss" }).getAttribute("value")).toBe(value.rows[0].id);
  expect((screen.getByRole("button", { name: "Next" }) as HTMLButtonElement).disabled).toBe(false);
  fireEvent.submit(name().form!); expect(value.save).not.toHaveBeenCalled();
  fireEvent.change(primary(), { target: { value: value.rows[1].id } }); next();
  expect(value.save).not.toHaveBeenCalled();
  expect(document.activeElement).toBe(screen.getByRole("heading", { name: "Usage restrictions" }));
  fireEvent.click(screen.getByRole("checkbox", { name: "Restrict agent workers" }));
  fireEvent.click(screen.getByRole("checkbox", { name: "Restrict ai accounts" }));
  fireEvent.click(screen.getByRole("checkbox", { name: "Restrict ai accounts" }));
  const save = screen.getByRole("button", { name: "Save Project" }); fireEvent.click(save); fireEvent.click(save);
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  expect(JSON.parse(new TextDecoder().decode(value.save.mock.calls[0][0].documentJson))).toEqual({ name: "oss", repositories: value.rows.map(row => row.id), primary_repository: value.rows[1].id, agents: { configured: true, ids: [] }, accounts: { configured: false, ids: [] } });
});

it("rechecks every required value at final submission and focuses the invalid step", async () => {
  const value = fixture(), submit = vi.fn();
  function Harness() {
    const [data, change] = useState<Document>({ name: "", repositories: [], primary_repository: "", agents: { configured: false, ids: [] }, accounts: { configured: false, ids: [] } });
    return <ProjectCreation data={data} change={change} active visible blocked={false} busy={false} saveDisabled={false} submit={submit} cancel={() => {}} cancelDisabled={false} uncertain={false} retry={() => {}}>
      <button type="button" onClick={() => change({ ...data, primary_repository: "" })}>Invalidate primary fixture</button>
      <button type="button" onClick={() => change({ ...data, name: "" })}>Invalidate name fixture</button>
      <button type="button" onClick={() => change({ ...data, repositories: [] })}>Invalidate repositories fixture</button>
    </ProjectCreation>;
  }
  render(value.view(<Harness />)); await waitFor(() => expect(document.activeElement).toBe(search()));
  await choose("oss"); next(); fireEvent.change(name(), { target: { value: "Manual name" } }); fireEvent.change(primary(), { target: { value: value.rows[0].id } }); next();
  fireEvent.click(screen.getByRole("button", { name: "Invalidate primary fixture" })); fireEvent.click(screen.getByRole("button", { name: "Save Project" }));
  expect(document.activeElement).toBe(primary()); expect(submit).not.toHaveBeenCalled();
  fireEvent.change(primary(), { target: { value: value.rows[0].id } }); next();
  fireEvent.click(screen.getByRole("button", { name: "Invalidate name fixture" })); fireEvent.click(screen.getByRole("button", { name: "Save Project" }));
  expect(document.activeElement).toBe(name()); expect(submit).not.toHaveBeenCalled();
  fireEvent.change(name(), { target: { value: "Manual name" } }); next();
  fireEvent.click(screen.getByRole("button", { name: "Invalidate repositories fixture" })); fireEvent.click(screen.getByRole("button", { name: "Save Project" }));
  expect(document.activeElement).toBe(search()); expect(submit).not.toHaveBeenCalled();
});

it("updates untouched names after removal/reordering, clears all selections, and protects manual names", async () => {
  const value = fixture(); render(value.view()); await choose("oss"); await choose("delidev");
  reorder(2); next(); expect(name().value).toBe("delidev"); expect(primary().value).toBe(value.rows[0].id);
  fireEvent.change(primary(), { target: { value: value.rows[1].id } }); previous();
  fireEvent.click(screen.getByRole("button", { name: "Remove entry 1" })); next(); expect(name().value).toBe("oss"); expect(primary().value).toBe("");
  fireEvent.change(name(), { target: { value: "My project" } }); previous();
  fireEvent.click(screen.getByRole("button", { name: "Remove entry 1" })); await choose("delidev"); next(); expect(name().value).toBe("My project"); expect(primary().value).toBe(value.rows[1].id);
});

it("preserves a manual primary through additions and reorder, and requires correction after removal", async () => {
  const value = fixture([repository("A"), repository("B"), repository("C")]); render(value.view());
  await choose("A"); next(); expect(primary().value).toBe(value.rows[0].id); previous();
  await choose("B"); next(); fireEvent.change(primary(), { target: { value: value.rows[1].id } }); previous();
  await choose("C"); reorder(3);
  fireEvent.click(screen.getByRole("button", { name: "Remove entry 1" })); next(); expect(primary().value).toBe(value.rows[1].id);
  previous(); await choose("B"); next(); expect(primary().value).toBe(""); expect((screen.getByRole("button", { name: "Next" }) as HTMLButtonElement).disabled).toBe(true);
  previous(); await choose("A"); next(); expect(primary().value).toBe(""); expect((screen.getByRole("button", { name: "Next" }) as HTMLButtonElement).disabled).toBe(true);
  previous(); await choose("C"); await choose("A"); await choose("C"); next(); expect(primary().value).toBe(value.rows[2].id);
});

it("does not carry an automatic name through clearing and reselecting every repository", async () => {
  const value = fixture(); render(value.view()); await choose("oss"); next(); expect(name().value).toBe("oss"); previous();
  await choose("oss"); await choose("delidev"); next(); expect(name().value).toBe("delidev");
});

it("searches later pages, keeps off-filter selections, limits rendered choices and caches no original documents", async () => {
  const first = Array.from({ length: 50 }, (_, index) => repository(`Repository ${index}`)), last = repository("Later needle");
  const value = fixture([...first, last]); value.list.mockImplementation(async token => token ? { resources: [last] } : { resources: first, nextPageToken: "second" });
  render(value.view()); await choose("Repository 0");
  await screen.findByRole("button", { name: "Load more Repository choices" });
  expect(within(screen.getByRole("group", { name: "Repository choices" })).getAllByRole("checkbox")).toHaveLength(50);
  fireEvent.click(screen.getByRole("button", { name: "Load more Repository choices" }));
  await waitFor(() => expect(within(screen.getByRole("group", { name: "Repository choices" })).getAllByRole("checkbox")).toHaveLength(51));
  fireEvent.change(search(), { target: { value: "needle" } }); await choose("Later needle");
  expect(screen.getByText("2 selected repositories")).toBeTruthy();
  next(); expect(name().value).toBe("Repository 0");
  expect(within(primary()).getAllByRole("option").map(row => row.textContent)).toEqual(["Select the primary repository", "Repository 0", "Later needle"]);
  expect(value.list.mock.calls.some(([token]) => token === "second")).toBe(true);
  expect(value.client.getQueryCache().getAll().some(query => (JSON.stringify(query.state.data) ?? "").includes("private_metadata"))).toBe(false);
});

it("keeps partial selections after a later-page failure and retries that exact page", async () => {
  const first = repository("oss"), second = repository("delidev"), value = fixture([first, second]); let attempt = 0;
  value.list.mockImplementation(async token => {
    if (!token) return { resources: [first], nextPageToken: "retained-token" };
    if (++attempt === 1) throw new ConnectError("Unavailable", Code.Unavailable);
    return { resources: [second] };
  });
  render(value.view()); await choose("oss");
  fireEvent.click(await screen.findByRole("button", { name: "Retry repository loading" }));
  await screen.findByRole("checkbox", { name: "delidev" });
  expect((screen.getByRole("checkbox", { name: "oss" }) as HTMLInputElement).checked).toBe(true);
  expect(value.list.mock.calls.filter(([token]) => token === "retained-token")).toHaveLength(2);
});

it("stops repeated cursors without falsely reporting empty inventory", async () => {
  const value = fixture(); value.list.mockImplementation(async token => ({ resources: token ? [] : value.rows, nextPageToken: "repeat" })); render(value.view());
  await screen.findByText("The repository list is invalid or its page cursor repeated. Reload repositories.");
  expect(screen.queryByText(/No registered repositories/)).toBeNull();
  expect(value.list.mock.calls.filter(([token]) => token === "repeat")).toHaveLength(1);
});

it("rejects repository IDs repeated across catalog pages", async () => {
  const value = fixture(); value.list.mockImplementation(async token => token ? { resources: [value.rows[0]!] } : { resources: [value.rows[0]!], nextPageToken: "second" }); render(value.view());
  await screen.findByText("The repository list is invalid or its page cursor repeated. Reload repositories.");
  expect(value.list.mock.calls.filter(([token]) => token === "second")).toHaveLength(1);
});

it("distinguishes loading, final emptiness and no matches", async () => {
  const pending = deferred<Page>(), value = fixture(); value.list.mockReturnValue(pending.promise); render(value.view());
  expect(screen.getByText("Loading repositories…")).toBeTruthy(); expect(screen.queryByText(/No registered repositories/)).toBeNull();
  await act(async () => pending.resolve({ resources: value.rows }));
  fireEvent.change(search(), { target: { value: "absent" } }); await screen.findByText("No repository names match your search.");
  expect(screen.queryByText(/No registered repositories/)).toBeNull();
});

it("does not confuse duplicate names or offer unsupported schemas", async () => {
  const first = repository("Duplicate"), second = repository("Duplicate"), unsupported = create(ResourceSchema, { ...repository("Future"), schemaVersion: 2 });
  const value = fixture([first, second, unsupported]); render(value.view());
  const duplicates = await screen.findAllByRole("checkbox", { name: "Duplicate" }); fireEvent.click(duplicates[0]); fireEvent.click(duplicates[1]);
  const identity = duplicates[0]!.getAttribute("aria-describedby"); expect(identity).toBeTruthy(); expect(document.getElementById(identity!)?.textContent).toContain(first.id);
  expect((screen.getByRole("checkbox", { name: "Repository name unavailable" }) as HTMLInputElement).disabled).toBe(true);
  next(); expect(within(primary()).getByRole("option", { name: "Duplicate (entry 1)" }).getAttribute("value")).toBe(first.id);
  expect(within(primary()).getByRole("option", { name: "Duplicate (entry 2)" }).getAttribute("value")).toBe(second.id);
});

it("uses locale-independent repository search matching", async () => {
  const value = fixture([repository("IMAGE")]);
  const localeLowerCase = vi.spyOn(String.prototype, "toLocaleLowerCase").mockImplementation(function (this: string) { return this === "IMAGE" ? "ımage" : this.toLowerCase(); });
  try {
    render(value.view()); fireEvent.change(search(), { target: { value: "image" } });
    expect(await screen.findByRole("checkbox", { name: "IMAGE" })).toBeTruthy();
  } finally {
    localeLowerCase.mockRestore();
  }
});

it("shows final registered emptiness without enabling repository advancement", async () => {
  const value = fixture([]); render(value.view());
  await screen.findByText("No registered repositories. Add a repository in Settings → Repositories first.");
  expect(screen.queryByText("Loading repositories…")).toBeNull();
  expect((screen.getByRole("button", { name: "Next" }) as HTMLButtonElement).disabled).toBe(true);
});

it("preserves name edits, focus, query identity and uncertain request bytes during language changes", async () => {
  const value = fixture(); value.save.mockRejectedValueOnce(new ConnectError("Lost acknowledgment", Code.Unavailable)); render(value.view()); await choose("oss"); next();
  expect(screen.getByText(/The first repository you select becomes the primary repository/)).toBeTruthy();
  expect(screen.queryByText("The harness starts in this repository. Select it explicitly after adding repositories.")).toBeNull();
  fireEvent.change(name(), { target: { value: "User name" } }); const input = name(); input.focus(); const reads = value.list.mock.calls.length;
  await act(async () => { await i18n.changeLanguage(SupportedLanguage.Korean); });
  expect(screen.getByText(/처음 선택한 저장소가 기본 저장소가 됩니다/)).toBeTruthy();
  expect(screen.getByRole("textbox", { name: "프로젝트 이름" })).toBe(input); expect(input.value).toBe("User name"); expect(document.activeElement).toBe(input); expect(value.list).toHaveBeenCalledTimes(reads);
  fireEvent.change(screen.getByRole("combobox", { name: "기본 저장소" }), { target: { value: value.rows[0].id } }); fireEvent.click(screen.getByRole("button", { name: "다음" }));
  fireEvent.click(screen.getByRole("button", { name: "프로젝트 저장" })); await screen.findByRole("button", { name: "같은 설정 다시 시도" });
  const request = value.save.mock.calls[0][0];
  await act(async () => { await i18n.changeLanguage(SupportedLanguage.English); });
  fireEvent.click(screen.getByRole("button", { name: "Retry the same configuration" })); await waitFor(() => expect(value.save).toHaveBeenCalledTimes(2)); expect(value.save.mock.calls[1][0]).toEqual(request);
});

it("blocks overlong UTF-8 names before leaving settings", async () => {
  const value = fixture(); render(value.view()); await choose("oss"); next();
  fireEvent.change(name(), { target: { value: "한".repeat(86) } }); fireEvent.change(primary(), { target: { value: value.rows[0].id } });
  expect(screen.getByRole("alert").textContent).toContain("256 UTF-8 bytes"); expect((screen.getByRole("button", { name: "Next" }) as HTMLButtonElement).disabled).toBe(true);
  fireEvent.submit(name().form!); expect(value.save).not.toHaveBeenCalled();
});

it("drops late catalog pages and scoped caches on departure", async () => {
  const pending = deferred<Page>(), value = fixture(); value.list.mockReturnValue(pending.promise); const view = render(value.view());
  const outside = document.createElement("button"); document.body.append(outside); outside.focus(); view.unmount();
  await act(async () => pending.resolve({ resources: value.rows, nextPageToken: "never-read" }));
  expect(value.list.mock.calls.some(([token]) => token === "never-read")).toBe(false);
  expect(value.client.getQueryCache().getAll().filter(query => query.queryKey.some(part => typeof part === "object" && part && "projectRepositoryBatch" in part))).toHaveLength(0);
  expect(document.activeElement).toBe(outside); outside.remove();
});
