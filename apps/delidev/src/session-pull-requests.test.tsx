import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, ResourceSchema, ResourceService, SessionService, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { MutationIntents } from "./mutation";
import { SessionPullRequests, readSessionPR } from "./session-pull-requests";

function fixture() {
  const sessionId = newRequestId(), projectId = newRequestId(), repositoryId = newRequestId();
  const session = create(ResourceSchema, { id: sessionId, projectId, kind: EntityKind.SESSION, revision: 1n, schemaVersion: 1, documentJson: encode({ archive: "archived", dispatch: "paused" }) });
  const value = { version: 1, provider: "github.com", repository_id: repositoryId, remote_repository_id: "37", repository_node_id: "R_37", owner: "fixture-owner", name: "repo", pull_request_id: "9007199254740993", pull_request_node_id: "PR_17", number: "17", title: "<script>Retained PR</script>", observed_at: "2026-09-28T00:00:00Z" };
  let rows: Resource[] = [create(ResourceSchema, { id: newRequestId(), projectId, sessionId, kind: EntityKind.PULL_REQUEST, revision: 9007199254740993n, schemaVersion: 1, documentJson: encode(value) })];
  const list = vi.fn(async (_r: { filter?: { kind: EntityKind; sessionId: string } }) => ({ resources: _r.filter?.kind === EntityKind.REPOSITORY ? [create(ResourceSchema, { id: repositoryId, kind: EntityKind.REPOSITORY, revision: 1n, schemaVersion: 1, documentJson: encode({ name: "Fixture repository" }) })] : rows }));
  const link = vi.fn(async (r: { requestId: string; sessionId: string; repositoryId: string; number: string }) => { const association = create(ResourceSchema, { id: r.requestId, projectId, sessionId, kind: EntityKind.PULL_REQUEST, revision: 1n, schemaVersion: 1, documentJson: encode({ ...value, number: r.number, title: "New PR" }) }); rows = [...rows, association]; return { association, requestId: r.requestId }; });
  const unlink = vi.fn(async (r: { sessionId: string; mutation?: { id: string; requestId: string; expectedRevision: bigint } }) => { rows = rows.filter((row) => row.id !== r.mutation?.id); return { id: r.mutation?.id, requestId: r.mutation?.requestId }; });
  const transport = createRouterTransport((router) => {
    router.service(ResourceService, { listResources: list, getResource: (r) => ({ resource: create(ResourceSchema, { id: r.id, kind: r.kind, revision: 1n, schemaVersion: 1, documentJson: encode(r.kind === EntityKind.PROJECT ? { repositories: [repositoryId] } : { name: "Fixture repository", github_owner: "fixture-owner", github_name: "repo", integration_id: newRequestId() }) }) }) });
    router.service(SessionService, { linkSessionPullRequest: link, unlinkSessionPullRequest: unlink });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><SessionPullRequests session={session} /></MutationIntents></QueryClientProvider></TransportProvider>;
  return { view, list, link, unlink, session, repositoryId, value, rows, client, replace: (next: Resource[]) => { rows = next; } };
}

it("retains archived associations, shows exact identities and unlinks without execution", async () => {
  const f = fixture(); const view = render(f.view); expect(f.list).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Show PR associations" }));
  await screen.findByText("<script>Retained PR</script>"); expect(view.container.querySelector("script")).toBeNull(); expect(screen.queryAllByRole("link")).toHaveLength(0);
  expect(screen.getByText(/9007199254740993/)).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Unlink #17" }));
  await screen.findByText("No PR associations on this page.");
  expect(f.unlink.mock.calls[0][0]).toMatchObject({ sessionId: f.session.id, mutation: { expectedRevision: 9007199254740993n } }); expect(f.link).not.toHaveBeenCalled();
});

it("links an explicitly selected project repository and preserves the exact PR number", async () => {
  const f = fixture(); render(f.view); fireEvent.click(screen.getByRole("button", { name: "Show PR associations" }));
  await screen.findByRole("option", { name: "Fixture repository" }); fireEvent.change(screen.getByLabelText("PR project repository"), { target: { value: f.repositoryId } });
  await screen.findByText(/Fixture repository · fixture-owner/);
  fireEvent.change(screen.getByLabelText("PR number"), { target: { value: "9007199254740993" } });
  fireEvent.click(screen.getByRole("button", { name: "Link PR" }));
  await screen.findByText("PR association saved.");
  expect(f.link.mock.calls[0][0]).toMatchObject({ sessionId: f.session.id, repositoryId: f.repositoryId, number: "9007199254740993" });
});

it("retries an uncertain original link after closing without replacing its input", async () => {
  const f = fixture(); f.link.mockRejectedValueOnce(new ConnectError("Response lost", Code.Unavailable)); render(f.view); fireEvent.click(screen.getByRole("button", { name: "Show PR associations" }));
  await screen.findByRole("option", { name: "Fixture repository" }); fireEvent.change(screen.getByLabelText("PR project repository"), { target: { value: f.repositoryId } }); await screen.findByText(/Fixture repository · fixture-owner/);
  fireEvent.change(screen.getByLabelText("PR number"), { target: { value: "18" } }); fireEvent.click(screen.getByRole("button", { name: "Link PR" }));
  await screen.findByRole("button", { name: "Retry original PR link" }); const original = f.link.mock.calls[0][0];
  fireEvent.click(screen.getByRole("button", { name: "Close PR associations" })); fireEvent.click(screen.getByRole("button", { name: "Show PR associations" }));
  fireEvent.click(await screen.findByRole("button", { name: "Retry original PR link" })); await screen.findByText("PR association saved.");
  expect(f.link.mock.calls[1][0]).toEqual(original);
});

it("rejects a foreign or malformed retained page and discards it when closed", async () => {
  const f = fixture(); expect(readSessionPR(f.rows[0], f.session.id)).toBeTruthy();
  f.replace([{ ...f.rows[0], sessionId: newRequestId() }]); render(f.view); fireEvent.click(screen.getByRole("button", { name: "Show PR associations" }));
  await screen.findByText("The association page is inconsistent and cannot be displayed."); expect(screen.queryByRole("button", { name: "Unlink #17" })).toBeNull();
  expect(readSessionPR({ ...f.rows[0], documentJson: encode({ ...f.value, remote_repository_id: 9007199254740993 }) }, f.session.id)).toBeUndefined();
  fireEvent.click(screen.getByRole("button", { name: "Close PR associations" })); await waitFor(() => expect(f.client.getQueryCache().getAll().filter(q => JSON.stringify(q.queryKey).includes("sessionId"))).toHaveLength(0));
});
