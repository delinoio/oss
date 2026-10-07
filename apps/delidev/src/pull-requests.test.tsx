import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, InboxService, IntegrationService, NotificationPreferencesSchema, ResourceSchema, ResourceService, SessionService, SystemService, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { App } from "./App";
import { encode, resourceName } from "./documents";
import { i18n, SupportedLanguage } from "./localization";

const repositoryId = "0195c9c0-7b13-7000-8000-000000000001";
function repository(name = "Example repository") {
  return create(ResourceSchema, { id: repositoryId, kind: EntityKind.REPOSITORY, revision: 1n, schemaVersion: 1, documentJson: encode({ name, integration_id: newRequestId(), github_owner: "owner", github_name: "repo" }) });
}
function fixture(rows = [repository()]) {
  let failure: Code | undefined;
  let gate: Promise<void> | undefined;
  const list = vi.fn(async (request: { filter?: { kind: EntityKind; pageToken: string } }) => {
    if (request.filter?.kind !== EntityKind.REPOSITORY) return { resources: [] };
    if (gate) await gate;
    if (failure) throw new ConnectError("Catalog request failed", failure);
    return request.filter.pageToken ? { resources: [] } : { resources: rows, ...(rows.length ? { nextPageToken: "repository-next" } : {}) };
  });
  const get = vi.fn(async (request: { id: string }) => ({ resource: rows.find((row) => row.id === request.id) }));
  const query = vi.fn(async (request: { repositoryId: string; queryJson: Uint8Array }) => {
    const row = rows.find((row) => row.id === request.repositoryId)!;
    const config = JSON.parse(new TextDecoder().decode(row.documentJson));
    const q = JSON.parse(new TextDecoder().decode(request.queryJson));
    return { schemaVersion: 1, documentJson: encode({ repository_id: row.id, repository_revision: row.revision.toString(), profile_id: config.integration_id, generation_id: newRequestId(), observed_at: "2026-09-28T00:00:00Z", identity: { id: "17", node_id: "U_17", login: "fixture-user" }, repository: { provider: "github.com", id: "37", node_id: "R_37", owner: "owner", name: "repo", private: true }, query: q, items: [{ provider: "github.com", kind: "pull-request", identity_source: q.operation === "search" ? "issue-api" : "pull-request-api", id: "9007199254740993", node_id: "ITEM_17", number: "17", title: "Original fixture title", state: "open", created_at: "2026-09-01T00:00:00Z", updated_at: "2026-09-28T00:00:00Z", author: { id: "19", node_id: "U_19", login: "fixture-author", kind: "user", provider_type: "User" }, url: "https://github.com/owner/repo/pull/17", draft: false }], ...(q.operation === "search" ? { total_count: "1", incomplete: false } : {}) }) };
  });
  const preferences = create(NotificationPreferencesSchema, { revision: 1n });
  const transport = createRouterTransport((router) => {
    router.service(SystemService, { getStatus: () => ({ version: "0.1.0", protocolVersion: 2 }) });
    router.service(SessionService, { listSessions: () => ({ sessions: [] }) });
    router.service(ResourceService, { listResources: list, getResource: get });
    router.service(InboxService, { listInbox: () => ({ entries: [] }), getNotificationPreferences: () => ({ preferences }) });
    router.service(IntegrationService, { queryRepositoryIntegration: query });
  });
  return { transport, rows, list, get, query, fail: (code?: Code) => { failure = code; }, defer: (promise?: Promise<void>) => { gate = promise; } };
}
async function open() {
  fireEvent.click(await screen.findByRole("button", { name: "Pull requests" }));
  return within(screen.getByRole("region", { name: "Pull requests navigation and filters" }));
}
async function choose(row: Resource) {
  fireEvent.click(await screen.findByRole("button", { name: `${resourceName(row)}. Repository ID: ${row.id}` }));
}
const submitted = (value: ReturnType<typeof fixture>, index: number) => JSON.parse(new TextDecoder().decode(value.query.mock.calls[index][0].queryJson));

it("shows the approved empty-page hierarchy and scopes styles only while PR is active", async () => {
  const value = fixture([]);
  render(<App transport={value.transport} />);
  const pane = await open();
  const empty = await pane.findByText("No repositories on this page.");
  expect(empty.closest(".sidebar-repository-empty")?.querySelector('svg[aria-hidden="true"]')).toBeTruthy();
  expect(pane.getByText("Select a repository. No GitHub request is made until you load pull requests.")).toBeTruthy();
  expect((pane.getByRole("button", { name: "First" }) as HTMLButtonElement).disabled).toBe(true);
  expect((pane.getByRole("button", { name: "Next" }) as HTMLButtonElement).disabled).toBe(true);
  expect(pane.queryByRole("heading", { name: "Query options" })).toBeNull();
  expect(pane.getByRole("button", { name: "Repository settings" })).toBeTruthy();
  expect(window.document.querySelector(".sidebar-pull-requests")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Sessions" }));
  expect(window.document.querySelector(".sidebar-pull-requests")).toBeNull();
  expect(value.query).not.toHaveBeenCalled();
});

it("shows only the selected name and sends only the explicit default Load", async () => {
  const value = fixture(); render(<App transport={value.transport} />);
  const pane = await open(); await choose(value.rows[0]);
  expect(pane.getByRole("heading", { name: "Query options" })).toBeTruthy();
  const row = pane.getByRole("button", { name: `Example repository. Repository ID: ${repositoryId}` });
  expect(row.textContent).toBe("Example repository");
  expect(row.querySelector("button")).toBeNull();
  expect(pane.queryByRole("region", { name: /Details for/ })).toBeNull();
  expect(row.getAttribute("aria-pressed")).toBe("true");
  expect(row.querySelector('svg[aria-hidden="true"]')).toBeTruthy();
  expect(pane.getByText("No GitHub request is made until you load pull requests.")).toBeTruthy();
  expect(value.query).not.toHaveBeenCalled();
  fireEvent.click(pane.getByRole("button", { name: "Load pull requests" }));
  await screen.findByText(/Original fixture title/);
  expect(value.query).toHaveBeenCalledTimes(1);
  expect(submitted(value, 0)).toEqual({ kind: "pull-request", operation: "list", state: "open", page: 1, page_size: 20 });
});

it("discloses one exact duplicate-name identity without selecting or reading it", async () => {
  const first = repository(), second = repository(); second.id = newRequestId();
  const value = fixture([first, second]); render(<App transport={value.transport} />);
  const pane = await open();
  const firstButton = await pane.findByRole("button", { name: `Details for Example repository. Repository ID: ${first.id}` });
  const secondButton = pane.getByRole("button", { name: `Details for Example repository. Repository ID: ${second.id}` });
  const catalogReads = value.list.mock.calls.filter(([request]) => request.filter?.kind === EntityKind.REPOSITORY).length;
  expect(firstButton.getAttribute("aria-expanded")).toBe("false");
  fireEvent.click(firstButton);
  let details = pane.getByRole("region", { name: `Details for Example repository. Repository ID: ${first.id}` });
  expect(details.id).toBe(firstButton.getAttribute("aria-controls"));
  expect(within(details).getByText(first.id).textContent).toBe(first.id);
  expect(within(details).getByText("owner/repo")).toBeTruthy();
  fireEvent.click(secondButton);
  expect(firstButton.getAttribute("aria-expanded")).toBe("false");
  expect(secondButton.getAttribute("aria-expanded")).toBe("true");
  details = pane.getByRole("region", { name: `Details for Example repository. Repository ID: ${second.id}` });
  expect(within(details).getByText(second.id)).toBeTruthy();
  expect(pane.queryAllByRole("region", { name: /Details for/ })).toHaveLength(1);
  expect(pane.queryByRole("heading", { name: "Query options" })).toBeNull();
  expect(pane.getByRole("button", { name: `Example repository. Repository ID: ${first.id}` }).getAttribute("aria-pressed")).toBe("false");
  fireEvent.click(secondButton);
  expect(pane.queryByRole("region", { name: /Details for/ })).toBeNull();
  expect(value.list.mock.calls.filter(([request]) => request.filter?.kind === EntityKind.REPOSITORY)).toHaveLength(catalogReads);
  expect(value.get).not.toHaveBeenCalled();
  expect(value.query).not.toHaveBeenCalled();
});

it("retains expanded details and enum drafts through paging, navigation, reconnect and language changes", async () => {
  const row = repository("Long repository name ".repeat(20));
  const value = fixture([row]); const view = render(<App transport={value.transport} />);
  let pane = await open(); await choose(row);
  const detailsName = `Details for ${resourceName(row)}. Repository ID: ${row.id}`;
  fireEvent.click(pane.getByRole("button", { name: detailsName }));
  fireEvent.click(pane.getByRole("radio", { name: "Closed" }));
  fireEvent.change(pane.getByLabelText("Search title and body"), { target: { value: "fix" } });
  fireEvent.click(pane.getByRole("button", { name: "Next" }));
  await pane.findByText("No repositories on this page.");
  expect(pane.queryByRole("region", { name: detailsName })).toBeNull();
  fireEvent.click(pane.getByRole("button", { name: "First" }));
  expect((await pane.findByRole("button", { name: detailsName })).getAttribute("aria-expanded")).toBe("true");
  fireEvent.click(screen.getByRole("button", { name: "Sessions" }));
  pane = await open();
  view.rerender(<App transport={value.transport} connectionEpoch={1} />);
  expect(pane.getByRole("region", { name: detailsName })).toBeTruthy();
  await act(() => i18n.changeLanguage(SupportedLanguage.Korean));
  pane = within(screen.getByRole("region", { name: "풀 리퀘스트 탐색 및 필터" }));
  expect(pane.getByRole("button", { name: `${resourceName(row).trim()} 상세. 저장소 ID: ${row.id}` }).getAttribute("aria-expanded")).toBe("true");
  expect((pane.getByRole("radio", { name: "닫힘" }) as HTMLInputElement).checked).toBe(true);
  expect((pane.getByLabelText("제목과 본문 검색") as HTMLInputElement).value).toBe("fix");
  expect(value.query).not.toHaveBeenCalled();
});

it("keeps missing repository mapping and original content inert in Details", async () => {
  const row = repository(); row.documentJson = encode({ name: "<b>Original name</b>", remote_url: "https://private.example/repo", local_path: "/original/path" });
  const value = fixture([row]); render(<App transport={value.transport} />);
  const pane = await open();
  fireEvent.click(await pane.findByRole("button", { name: `Details for <b>Original name</b>. Repository ID: ${row.id}` }));
  const details = pane.getByRole("region", { name: `Details for <b>Original name</b>. Repository ID: ${row.id}` });
  expect(within(details).getByText("Not configured")).toBeTruthy();
  expect(details.textContent).not.toContain("https://private.example/repo");
  expect(details.textContent).not.toContain("/original/path");
  expect(pane.getByRole("button", { name: `<b>Original name</b>. Repository ID: ${row.id}` }).querySelector("b")).toBeNull();
  expect(value.get).not.toHaveBeenCalled();
  expect(value.query).not.toHaveBeenCalled();
});

it("preserves off-page identity and filter drafts through empty later pages and First", async () => {
  const value = fixture(); render(<App transport={value.transport} />);
  const pane = await open(); await choose(value.rows[0]);
  fireEvent.click(pane.getByRole("radio", { name: "Closed" }));
  fireEvent.change(pane.getByLabelText("Search title and body"), { target: { value: "fix" } });
  fireEvent.change(pane.getByLabelText("PR page size"), { target: { value: "5" } });
  fireEvent.click(pane.getByRole("button", { name: "Next" }));
  await pane.findByText("No repositories on this page.");
  await waitFor(() => expect(value.get).toHaveBeenCalledTimes(1));
  expect(value.get.mock.calls[0][0].id).toBe(repositoryId);
  expect((pane.getByRole("button", { name: "First" }) as HTMLButtonElement).disabled).toBe(false);
  expect((pane.getByRole("button", { name: "Next" }) as HTMLButtonElement).disabled).toBe(true);
  expect((pane.getByRole("radio", { name: "Closed" }) as HTMLInputElement).checked).toBe(true);
  expect((pane.getByLabelText("Search title and body") as HTMLInputElement).value).toBe("fix");
  fireEvent.click(pane.getByRole("button", { name: "First" }));
  await pane.findByRole("button", { name: `Example repository. Repository ID: ${repositoryId}` });
  expect((pane.getByLabelText("PR page size") as HTMLSelectElement).value).toBe("5");
  const tokens = value.list.mock.calls.filter(([request]) => request.filter?.kind === EntityKind.REPOSITORY).map(([request]) => request.filter?.pageToken);
  expect(tokens.slice(0, 2)).toEqual(["", "repository-next"]);
  // Returning to First can refresh its cache; later tokens must remain explicit.
  expect(tokens.slice(2).every((token) => token === "")).toBe(true);
  expect(value.query).not.toHaveBeenCalled();
  fireEvent.click(pane.getByRole("button", { name: "Load pull requests" }));
  await waitFor(() => expect(value.query).toHaveBeenCalledTimes(1));
  expect(submitted(value, 0)).toEqual({ kind: "pull-request", operation: "search", state: "closed", search: "fix", page: 1, page_size: 5 });
});

it("keeps applied results during edits and drops them on navigation without losing controls", async () => {
  const second = repository("Other repository"); second.id = newRequestId();
  const value = fixture([repository(), second]);
  const view = render(<App transport={value.transport} />);
  let pane = await open(); await choose(value.rows[0]);
  fireEvent.click(pane.getByRole("button", { name: "Load pull requests" }));
  await screen.findByText(/Original fixture title/);
  fireEvent.click(pane.getByRole("radio", { name: "Closed" }));
  fireEvent.change(pane.getByLabelText("Search title and body"), { target: { value: "fix" } });
  fireEvent.change(pane.getByLabelText("PR page size"), { target: { value: "5" } });
  expect(pane.getByText(/displayed results belong to the last loaded/)).toBeTruthy();
  expect(screen.getByText(/Original fixture title/)).toBeTruthy();
  expect(value.query).toHaveBeenCalledTimes(1);
  fireEvent.click(pane.getByRole("button", { name: "Load pull requests" }));
  await waitFor(() => expect(value.query).toHaveBeenCalledTimes(2));
  expect(submitted(value, 1)).toMatchObject({ state: "closed", search: "fix", page_size: 5 });
  fireEvent.click(screen.getByRole("button", { name: "Sessions" }));
  pane = await open();
  expect(screen.queryByText(/Original fixture title/)).toBeNull();
  expect((pane.getByRole("radio", { name: "Closed" }) as HTMLInputElement).checked).toBe(true);
  view.rerender(<App transport={value.transport} connectionEpoch={1} />);
  await waitFor(() => expect(value.list.mock.calls.length).toBeGreaterThan(2));
  expect(value.query).toHaveBeenCalledTimes(2);
  expect((pane.getByLabelText("Search title and body") as HTMLInputElement).value).toBe("fix");
  await choose(second);
  expect((pane.getByRole("radio", { name: "Open" }) as HTMLInputElement).checked).toBe(true);
  expect((pane.getByLabelText("Search title and body") as HTMLInputElement).value).toBe("");
  expect((pane.getByLabelText("PR page size") as HTMLSelectElement).value).toBe("20");
});

it("distinguishes initial loading from a successful empty page", async () => {
  const value = fixture([]); let release!: () => void;
  value.defer(new Promise<void>((resolve) => { release = resolve; }));
  render(<App transport={value.transport} />); const pane = await open();
  expect(await pane.findByRole("status")).toHaveProperty("textContent", "Loading repositories…");
  expect(pane.queryByText("No repositories on this page.")).toBeNull();
  release(); await pane.findByText("No repositories on this page.");
  expect(pane.queryByText("Loading repositories…")).toBeNull();
});

it.each([Code.PermissionDenied, Code.Unavailable])("distinguishes catalog failure %s from an empty page and preserves cached rows on failed Refresh", async (code) => {
  const value = fixture(); value.fail(code);
  render(<App transport={value.transport} />); const pane = await open();
  await pane.findByRole("alert");
  expect(pane.queryByText("No repositories on this page.")).toBeNull();
  value.fail(); fireEvent.click(pane.getByRole("button", { name: "Refresh" }));
  await choose(value.rows[0]);
  value.fail(code); fireEvent.click(pane.getByRole("button", { name: "Refresh" }));
  await pane.findByText("Refresh failed. Showing the previous repository page.");
  expect(pane.getByRole("button", { name: `Example repository. Repository ID: ${repositoryId}` })).toBeTruthy();
  expect(pane.queryByText("No repositories on this page.")).toBeNull();
  expect(value.query).not.toHaveBeenCalled();
});

it.each(["schema", "integration_id", "github_owner", "github_name"])("keeps unconfigured %s guidance and existing plain-search validation", async (missing) => {
  const row = repository();
  if (missing === "schema") row.schemaVersion = 2;
  else { const config = JSON.parse(new TextDecoder().decode(row.documentJson)); delete config[missing]; row.documentJson = encode(config); }
  const value = fixture([row]); render(<App transport={value.transport} />);
  const pane = await open(); await choose(row);
  expect(pane.getByText(/Set a supported GitHub profile/)).toBeTruthy();
  expect((pane.getByRole("button", { name: "Load pull requests" }) as HTMLButtonElement).disabled).toBe(true);
  const search = pane.getByLabelText("Search title and body") as HTMLInputElement;
  expect(search.maxLength).toBe(120);
  fireEvent.change(search, { target: { value: "is:pr" } });
  expect(pane.getByRole("alert").textContent).toMatch(/Use plain words/);
  expect(value.query).not.toHaveBeenCalled();
});
