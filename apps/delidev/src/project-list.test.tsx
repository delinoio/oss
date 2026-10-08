// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { clientFailure, EntityKind, ResourceSchema, ResourceService, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { ProjectList } from "./project-list";
import { repositoryDetails, RepositoryDetailsState, type RepositoryDetails, useProjectListMetadata } from "./project-list-metadata";

const repository = (name = "Repository", url = "https://github.com/delinoio/oss.git") => create(ResourceSchema, { id: newRequestId(), kind: EntityKind.REPOSITORY, schemaVersion: 1, revision: 1n, documentJson: encode({ name, remote_url: url }) });
const project = (rows: Resource[]) => create(ResourceSchema, { id: newRequestId(), kind: EntityKind.PROJECT, schemaVersion: 1, revision: 1n, documentJson: encode({ name: "Project", repositories: rows.map(row => row.id), primary_repository: rows.at(-1)?.id }) });
function fixture(read: (id: string) => Promise<{ resource?: Resource }> | { resource?: Resource }) {
  const get = vi.fn(({ id }: { id: string }) => read(id)), list = vi.fn(() => ({ resources: [] }));
  const transport = createRouterTransport(router => router.service(ResourceService, { getResource: get, listResources: list }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const wrap = (children: React.ReactNode, queryClient = client) => <TransportProvider transport={transport}><QueryClientProvider client={queryClient}>{children}</QueryClientProvider></TransportProvider>;
  return { get, list, client, wrap };
}
function Reader({ ids, active = true }: { ids: string[]; active?: boolean }) {
  const metadata = useProjectListMetadata(ids, active);
  return <><button onClick={metadata.refresh}>Refresh metadata</button><output data-testid="metadata">{JSON.stringify([...metadata.rows].map(([id, row]) => [id, row.state, row.name, row.url, row.stale]))}</output></>;
}
it("shares eight exact reads across mounted pages, deduplicates, and retains only projections", async () => {
  const rows = Array.from({ length: 19 }, (_, index) => repository(`Repository ${index}`));
  const pending = new Map<string, (value: { resource?: Resource }) => void>();
  let concurrent = 0, maximum = 0;
  const value = fixture(id => new Promise(resolve => { concurrent++; maximum = Math.max(maximum, concurrent); pending.set(id, response => { concurrent--; resolve(response); }); }));
  render(value.wrap(<Reader ids={[...rows.map(row => row.id), rows[0]!.id]} />));
  await waitFor(() => expect(value.get).toHaveBeenCalledTimes(8));
  for (const row of rows) { await waitFor(() => expect(pending.has(row.id)).toBe(true)); await act(async () => pending.get(row.id)!({ resource: row })); }
  await waitFor(() => expect(screen.getByTestId("metadata").textContent).toContain("Repository 18"));
  expect(maximum).toBe(8); expect(value.get).toHaveBeenCalledTimes(19); expect(value.list).not.toHaveBeenCalled();
  expect(value.client.getQueryCache().getAll()).toHaveLength(0);
});
it("retains accepted details on failed explicit refresh and never retries automatically", async () => {
  const row = repository(), value = fixture(() => ({ resource: row }));
  render(value.wrap(<Reader ids={[row.id]} />));
  await waitFor(() => expect(screen.getByTestId("metadata").textContent).toContain("Repository"));
  value.get.mockRejectedValueOnce(new ConnectError("https://user:secret@private.invalid", Code.PermissionDenied));
  fireEvent.click(screen.getByRole("button", { name: "Refresh metadata" }));
  await waitFor(() => expect(screen.getByTestId("metadata").textContent).toContain("true"));
  expect(screen.getByTestId("metadata").textContent).toContain(row.id);
  expect(screen.getByTestId("metadata").textContent).toContain("Repository");
  expect(screen.getByTestId("metadata").textContent).not.toContain("secret");
  expect(value.get).toHaveBeenCalledTimes(2);
  const replacement = { ...row, revision: 2n, documentJson: encode({ name: "Replacement", remote_url: "ssh://git@example.org/team/oss.git" }) };
  value.get.mockResolvedValueOnce({ resource: replacement });
  fireEvent.click(screen.getByRole("button", { name: "Refresh metadata" }));
  await waitFor(() => expect(screen.getByTestId("metadata").textContent).toContain("Replacement"));
  expect(screen.getByTestId("metadata").textContent).not.toContain("true");
});
it("prunes off-window IDs and fences disposed generations without starting more reads", async () => {
  const rows = Array.from({ length: 12 }, () => repository());
  const pending = new Map<string, (value: { resource?: Resource }) => void>();
  const value = fixture(id => new Promise(resolve => pending.set(id, resolve)));
  const mounted = render(value.wrap(<Reader ids={rows.map(row => row.id)} />));
  await waitFor(() => expect(value.get).toHaveBeenCalledTimes(8));
  mounted.rerender(value.wrap(<Reader ids={[]} />));
  await act(async () => { for (const row of rows.slice(0, 8)) pending.get(row.id)!({ resource: row }); });
  expect(screen.getByTestId("metadata").textContent).toBe("[]"); expect(value.get).toHaveBeenCalledTimes(8);
  mounted.unmount(); await waitFor(() => expect(value.client.getQueryCache().getAll()).toHaveLength(0));
});
it.each(["https://user:secret@example.org/oss", "https://example.org/a/../secret", "https://example.org/oss?token=secret", "https://example.org/%2e%2e/secret", "https://example.org/oss#secret", "https://example.org/oss\\secret"])("rejects unsafe configured source bytes %s", url => {
  const row = repository("Repository", url); expect(repositoryDetails(row, row.id)).toBeUndefined();
});
it("rejects mismatched identities, schema, revision and names without substituting another row", () => {
  const row = repository();
  expect(repositoryDetails(row, newRequestId())).toBeUndefined();
  for (const update of [{ kind: EntityKind.MACHINE }, { schemaVersion: 99 }, { revision: 0n }, { documentJson: encode({ name: "Repository\nsecret", remote_url: "https://example.org/oss" }) }]) expect(repositoryDetails({ ...row, ...update }, row.id)).toBeUndefined();
  expect(repositoryDetails(repository("Legacy", ""), repository().id)).toBeUndefined();
  const legacy = repository("Legacy", ""); expect(repositoryDetails(legacy, legacy.id)?.url).toBe("");
});
it("shows first three ordered repositories, inert full URLs, explicit primary and collapsed exact identities", () => {
  const rows = Array.from({ length: 5 }, (_, index) => repository(`Repository ${index}`, `https://example.org/${"long-source-".repeat(12)}${index}.git`)), saved = project(rows);
  const metadata = new Map(rows.map(row => [row.id, repositoryDetails(row, row.id)!]));
  const edit = vi.fn(), remove = vi.fn();
  const mounted = render(<ProjectList resources={[saved]} metadata={metadata} edit={edit} remove={remove} />);
  expect(screen.getByText("5 repositories")).toBeTruthy(); expect(screen.queryByText("Repository 3")).toBeNull(); expect(screen.queryByText("Primary")).toBeNull();
  const disclosure = mounted.container.querySelector("details")!; expect(disclosure.open).toBe(false); expect(within(disclosure).getByText(saved.id)).toBeTruthy();
  expect(screen.queryByRole("link")).toBeNull();
  const show = screen.getByRole("button", { name: "Show all repositories" }); show.focus(); fireEvent.click(show);
  expect(screen.getByText("Repository 4")).toBeTruthy(); expect(screen.getByText("Primary")).toBeTruthy(); expect(document.activeElement).toBe(show);
  expect([...mounted.container.querySelectorAll(".project-repository-heading strong")].map(node => node.textContent)).toEqual(rows.map((_, i) => `Repository ${i}`));
  fireEvent.click(show); expect(screen.queryByText("Repository 4")).toBeNull(); expect(document.activeElement).toBe(show);
  fireEvent.click(screen.getByRole("button", { name: "Edit Project" })); expect(edit).toHaveBeenCalledWith(saved);
});
it("distinguishes loading, missing URL, unavailable metadata and stale results without disabling project actions", () => {
  const rows = [repository(), repository("Legacy", ""), repository(), repository("Retained")], saved = project(rows);
  const metadata = new Map([[rows[0]!.id, { state: RepositoryDetailsState.Loading }], [rows[1]!.id, repositoryDetails(rows[1], rows[1]!.id)!], [rows[2]!.id, { state: RepositoryDetailsState.Unavailable }], [rows[3]!.id, { ...repositoryDetails(rows[3], rows[3]!.id)!, stale: true }]]);
  render(<ProjectList resources={[saved]} metadata={metadata} edit={() => {}} remove={() => {}} />);
  expect(screen.getByText("Loading repository details…")).toBeTruthy(); expect(screen.getByText("URL not configured")).toBeTruthy(); expect(screen.getByText("Repository details unavailable")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Show all repositories" })); expect(screen.getByRole("status").textContent).toContain("previously loaded");
  expect(screen.getByRole("button", { name: "Edit Project" }).hasAttribute("disabled")).toBe(false);
});
it("replaces connection-owned projections and never borrows metadata from another authenticated transport", async () => {
  const row = repository("Original server"), first = fixture(() => ({ resource: row })), second = fixture(() => ({ resource: { ...row, documentJson: encode({ name: "Other server", remote_url: "https://example.org/other" }) } }));
  const mounted = render(first.wrap(<Reader ids={[row.id]} />));
  await waitFor(() => expect(screen.getByTestId("metadata").textContent).toContain("Original server"));
  mounted.rerender(second.wrap(<Reader ids={[row.id]} />));
  await waitFor(() => expect(screen.getByTestId("metadata").textContent).toContain("Other server"));
  expect(screen.getByTestId("metadata").textContent).not.toContain("Original server");
  mounted.rerender(second.wrap(<Reader ids={[row.id]} active={false} />));
  expect(screen.getByTestId("metadata").textContent).toBe("[]");
});
it("marks missing or malformed exact reads unavailable and keeps original stale projections on revision regression", async () => {
  const row = repository("Accepted"), absent = newRequestId(), invalid = newRequestId(), value = fixture(id => id === row.id ? { resource: { ...row, revision: 3n } } : id === invalid ? { resource: row } : {});
  render(value.wrap(<Reader ids={[row.id, absent, invalid]} />));
  await waitFor(() => expect(screen.getByTestId("metadata").textContent).toContain("Accepted"));
  await waitFor(() => expect(JSON.parse(screen.getByTestId("metadata").textContent!)).toEqual(expect.arrayContaining([[absent, RepositoryDetailsState.Unavailable, null, null, null], [invalid, RepositoryDetailsState.Unavailable, null, null, null]])));
  value.get.mockImplementation(async ({ id }) => id === row.id ? { resource: { ...row, revision: 2n, documentJson: encode({ name: "Regressed", remote_url: "https://example.org/regressed" }) } } : {});
  fireEvent.click(screen.getByRole("button", { name: "Refresh metadata" }));
  await waitFor(() => expect(value.get).toHaveBeenCalledTimes(6));
  await waitFor(() => expect(screen.getByTestId("metadata").textContent).toContain("true"));
  expect(screen.getByTestId("metadata").textContent).toContain("Accepted"); expect(screen.getByTestId("metadata").textContent).not.toContain("Regressed");
});

it("preserves accepted projections across same-identity authentication transport replacement until explicit Refresh", async () => {
  const row = repository("Retained across reconnect"), first = fixture(() => ({ resource: row })), second = fixture(() => ({ resource: { ...row, revision: 2n, documentJson: encode({ name: "Refreshed after reconnect", remote_url: "https://example.org/refreshed" }) } }));
  const mounted = render(first.wrap(<Reader ids={[row.id]} />));
  await waitFor(() => expect(screen.getByTestId("metadata").textContent).toContain("Retained across reconnect"));
  mounted.rerender(second.wrap(<Reader ids={[row.id]} />, first.client));
  expect(screen.getByTestId("metadata").textContent).toContain("Retained across reconnect"); expect(second.get).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Refresh metadata" }));
  await waitFor(() => expect(screen.getByTestId("metadata").textContent).toContain("Refreshed after reconnect"));
});

it.each(["window", "refresh", "reconnect"])("holds eight transport permits across overlapping %s generations and prunes obsolete queued refs", async mode => {
  const rows = Array.from({ length: 20 }, (_, index) => repository(`Overlapping ${index}`));
  const pending: { id: string; release: () => void }[] = [];
  let concurrent = 0, maximum = 0;
  const read = (id: string) => new Promise<{ resource?: Resource }>(resolve => {
    concurrent++; maximum = Math.max(maximum, concurrent);
    pending.push({ id, release: () => { concurrent--; resolve({ resource: rows.find(row => row.id === id) }); } });
  });
  const first = fixture(read), second = fixture(read), original = rows.slice(0, 12).map(row => row.id);
  const mounted = render(first.wrap(<Reader ids={original} />));
  await waitFor(() => expect(first.get).toHaveBeenCalledTimes(8));
  const current = mode === "window" ? rows.slice(12).map(row => row.id) : original;
  if (mode === "window") mounted.rerender(first.wrap(<Reader ids={current} />));
  else if (mode === "refresh") fireEvent.click(screen.getByRole("button", { name: "Refresh metadata" }));
  else mounted.rerender(second.wrap(<Reader ids={current} />, first.client));
  // Await queued query publication; an abort-ignoring original transport must
  // still own every permit instead of making another eight slots available.
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 20)); });
  expect(first.get.mock.calls.length + second.get.mock.calls.length).toBe(8);
  await act(async () => { for (const entry of pending.slice(0, 8)) entry.release(); });
  await waitFor(() => expect(pending.length).toBe(16));
  if (mode === "window") expect(pending.slice(8).map(entry => entry.id)).toEqual(current);
  for (let index = 8; index < 8 + current.length; index++) {
    await waitFor(() => expect(pending[index]).toBeTruthy());
    await act(async () => pending[index]!.release());
  }
  await waitFor(() => expect(first.client.getQueryCache().getAll()).toHaveLength(0));
  expect(maximum).toBe(8);
  if (mode === "window") for (const id of original) expect(screen.getByTestId("metadata").textContent).not.toContain(id);
  mounted.unmount();
});


it("compacts only exact matching-name singleton metadata while retaining inert source bytes and identities", () => {
  const row = repository("Project", "git@example.org:team/Project.git"), saved = project([row]);
  const edit = vi.fn(), remove = vi.fn();
  const mounted = render(<ProjectList resources={[saved]} metadata={new Map([[row.id, repositoryDetails(row, row.id)!]])} edit={edit} remove={remove} />);
  expect(screen.getAllByText("Project")).toHaveLength(1);
  expect(screen.queryByText("1 repository")).toBeNull(); expect(screen.queryByText("Primary")).toBeNull();
  expect(screen.getByText("git@example.org:team/Project.git")).toBeTruthy(); expect(screen.queryByRole("link")).toBeNull();
  const details = mounted.container.querySelector("details")!;
  expect(details.open).toBe(false); expect(within(details).getByText(saved.id)).toBeTruthy(); expect(within(details).getByText(row.id)).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Edit Project" })); fireEvent.click(screen.getByRole("button", { name: "Delete Project" }));
  expect(edit).toHaveBeenCalledWith(saved); expect(remove).toHaveBeenCalledWith(saved);
});

it.each([
  ["different name", { state: RepositoryDetailsState.Ready, name: "Different project", url: "https://example.org/project.git" }, "Different project"],
  ["case differs", { state: RepositoryDetailsState.Ready, name: "project", url: "https://example.org/project.git" }, "project"],
  ["loading", { state: RepositoryDetailsState.Loading }, "Loading repository details…"],
  ["unavailable", { state: RepositoryDetailsState.Unavailable }, "Repository details unavailable"],
  ["missing URL", { state: RepositoryDetailsState.Ready, name: "Project", url: "" }, "URL not configured"],
  ["stale", { state: RepositoryDetailsState.Ready, name: "Project", url: "https://example.org/project.git", stale: true }, "Showing previously loaded repository details. Refresh to read the latest details."],
] satisfies [string, RepositoryDetails, string][])("retains singleton %s guidance and saved-ID cardinality", (_, details, label) => {
  const row = repository(), saved = project([row]);
  render(<ProjectList resources={[saved]} metadata={new Map([[row.id, details]])} edit={() => {}} remove={() => {}} />);
  expect(screen.getByText(label)).toBeTruthy(); expect(screen.queryByText("1 repository")).toBeNull(); expect(screen.queryByText("Primary")).toBeNull();
  expect(screen.getByRole("button", { name: "Edit Project" }).hasAttribute("disabled")).toBe(false);
});

it.each([0, 2, 3])("keeps the saved count for %i references even when metadata is unavailable", count => {
  const rows = Array.from({ length: count }, () => repository()), saved = project(rows);
  const mounted = render(<ProjectList resources={[saved]} metadata={new Map()} edit={() => {}} remove={() => {}} />);
  expect(screen.getByText(`${count} repositories`)).toBeTruthy();
  expect(mounted.container.querySelectorAll(".project-repository-rows > li")).toHaveLength(count);
  for (const row of rows) expect(within(mounted.container.querySelector("details")!).getByText(row.id)).toBeTruthy();
});

it("preserves disabled schema actions and unreadable original singleton references", () => {
  const saved = { ...project([]), schemaVersion: 99, documentJson: encode({ name: "Unsupported", repositories: ["unreadable-original-reference"] }) };
  const mounted = render(<ProjectList resources={[saved]} metadata={new Map()} edit={() => {}} remove={() => {}} />);
  expect(screen.getAllByRole("button")[0]!.hasAttribute("disabled")).toBe(true);
  expect(screen.getAllByRole("button")[1]!.hasAttribute("disabled")).toBe(true);
  expect(within(mounted.container.querySelector("details")!).getByText(saved.id)).toBeTruthy();
  expect(screen.getByText("0 repositories")).toBeTruthy();
});


it("keeps unreadable saved references in supported project details", () => {
  const saved = { ...project([]), documentJson: encode({ name: "Project", repositories: ["unreadable-original-reference"] }) };
  const mounted = render(<ProjectList resources={[saved]} metadata={new Map([["unreadable-original-reference", { state: RepositoryDetailsState.Unavailable }]])} edit={() => {}} remove={() => {}} />);
  expect(within(mounted.container.querySelector("details")!).getByText("unreadable-original-reference")).toBeTruthy();
  expect(screen.getByText("Repository details unavailable")).toBeTruthy(); expect(screen.queryByText("1 repository")).toBeNull();
});


it("keeps sanitized failure guidance and retained singleton URLs outside identity details", () => {
  const row = repository("Project"), saved = project([row]);
  const metadata = new Map([[row.id, { ...repositoryDetails(row, row.id)!, stale: true, failure: clientFailure(new ConnectError("https://user:secret@private.invalid", Code.PermissionDenied)) }]]);
  const mounted = render(<ProjectList resources={[saved]} metadata={metadata} edit={() => {}} remove={() => {}} />);
  expect(screen.getByRole("alert")).toBeTruthy(); expect(screen.getByRole("status").textContent).toContain("previously loaded");
  expect(screen.getByText("https://github.com/delinoio/oss.git")).toBeTruthy();
  expect(mounted.container.textContent).not.toContain("secret"); expect(screen.queryByText("1 repository")).toBeNull();
});
