import { useState } from "react";
import { chooseScrollOption, waitScrollChoices } from "./test-scroll-picker";
import { create, toBinary } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, ResourceSchema, ResourceService, SessionService, SessionQuery, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { MutationIntents, useRetainedMutation } from "./mutation";
import { SessionPullRequests, readSessionPR } from "./session-pull-requests";

function PrimaryPRSection({ session }: { session: Resource }) {
 const [visible,setVisible]=useState(true), [target,setTarget]=useState<HTMLDivElement|null>(null);
 return <><div ref={setTarget}/><button type="button" aria-expanded={visible} onClick={()=>setVisible(!visible)}>PR associations</button><div hidden={!visible}><SessionPullRequests session={session} visible={visible} diagnosticsTarget={target}/></div></>;
}
function openAssociations() { const header=screen.getByRole("button",{name:"PR associations"}); if(header.getAttribute("aria-expanded")==="false")fireEvent.click(header); expect(screen.queryByRole("button",{name:"Show PR associations"})).toBeNull(); }
function closeAssociations() { fireEvent.click(screen.getByRole("button",{name:"PR associations"})); }

function OriginalLink({ sessionId, repositoryId }: { sessionId:string; repositoryId:string }) { const link=useRetainedMutation(`session-pr:link:${sessionId}`,SessionQuery.linkSessionPullRequest); return <button onClick={()=>void link.send({sessionId, repositoryId, number:"18", requestId:newRequestId()})}>Seed original link</button>; }
function fixture() {
  const sessionId = newRequestId(), projectId = newRequestId(), repositoryId = newRequestId();
  const session = create(ResourceSchema, { id: sessionId, projectId, kind: EntityKind.SESSION, revision: 1n, schemaVersion: 1, documentJson: encode({ archive: "archived", dispatch: "paused" }) });
  const value = { version: 1, provider: "github.com", repository_id: repositoryId, remote_repository_id: "37", repository_node_id: "R_37", owner: "fixture-owner", name: "repo", pull_request_id: "9007199254740993", pull_request_node_id: "PR_17", number: "17", title: "<script>Retained PR</script>", observed_at: "2026-09-28T00:00:00Z" };
  let rows: Resource[] = [create(ResourceSchema, { id: newRequestId(), projectId, sessionId, kind: EntityKind.PULL_REQUEST, revision: 9007199254740993n, schemaVersion: 1, documentJson: encode(value) })];
  const list = vi.fn(async (_r: { filter?: { kind: EntityKind; sessionId: string } }) => ({ resources: _r.filter?.kind === EntityKind.REPOSITORY ? [create(ResourceSchema, { id: repositoryId, kind: EntityKind.REPOSITORY, revision: 1n, schemaVersion: 1, documentJson: encode({ name: "Fixture repository" }) })] : rows }));
  const link = vi.fn(async (r: { requestId: string; sessionId: string; repositoryId: string; number: string }) => { const association = create(ResourceSchema, { id: r.requestId, projectId, sessionId, kind: EntityKind.PULL_REQUEST, revision: 1n, schemaVersion: 1, documentJson: encode({ ...value, number: r.number, title: "New PR" }) }); rows = [...rows, association]; return { association, requestId: r.requestId }; });
  const unlinkEffects = vi.fn();
  const receipts = new Map<string, { id?: string; requestId?: string }>();
  const unlink = vi.fn(async (r: { sessionId: string; mutation?: { id: string; requestId: string; expectedRevision: bigint } }) => {
    const retained = receipts.get(r.mutation?.requestId ?? "");
    if (retained) return retained;
    unlinkEffects(); rows = rows.filter((row) => row.id !== r.mutation?.id);
    const receipt = { id: r.mutation?.id, requestId: r.mutation?.requestId };
    receipts.set(r.mutation?.requestId ?? "", receipt);
    return receipt;
  });
  const transport = createRouterTransport((router) => {
    router.service(ResourceService, { listResources: list, getResource: (r) => ({ resource: create(ResourceSchema, { id: r.id, kind: r.kind, revision: 1n, schemaVersion: 1, documentJson: encode(r.kind === EntityKind.PROJECT ? { repositories: [repositoryId] } : { name: "Fixture repository", github_owner: "fixture-owner", github_name: "repo", integration_id: newRequestId() }) }) }) });
    router.service(SessionService, { linkSessionPullRequest: link, unlinkSessionPullRequest: unlink });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const viewFor = (selected: Resource | null = session) => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><OriginalLink sessionId={sessionId} repositoryId={repositoryId}/>{selected ? <PrimaryPRSection session={selected} /> : null}</MutationIntents></QueryClientProvider></TransportProvider>;
  return { view: viewFor(), viewFor, list, link, unlink, unlinkEffects, session, repositoryId, value, rows, client, replace: (next: Resource[]) => { rows = next; } };
}

it("retains archived associations, shows exact identities and unlinks without execution", async () => {
  const f = fixture(); const view = render(f.view); await waitFor(()=>expect(f.list).toHaveBeenCalled());
  openAssociations();
  await screen.findByText("<script>Retained PR</script>"); expect(view.container.querySelector("script")).toBeNull(); expect(screen.queryAllByRole("link")).toHaveLength(0);
  expect(screen.getByText(/9007199254740993/)).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Unlink #17" }));
  await screen.findByText("No PR associations on this page.");
  expect(f.unlink.mock.calls[0][0]).toMatchObject({ sessionId: f.session.id, mutation: { expectedRevision: 9007199254740993n } }); expect(f.link).not.toHaveBeenCalled();
  expect(screen.queryByRole("region", { name: "Pending PR unlinks" })).toBeNull();
});

it("keeps a committed unlink recoverable after refresh, reopen and session navigation", async () => {
  const f = fixture(), commit = f.unlink.getMockImplementation()!;
  f.unlink.mockImplementationOnce(async (request) => { await commit(request); throw new ConnectError("Original acknowledgment lost", Code.Unavailable); });
  const view = render(f.view);
  openAssociations();
  fireEvent.click(await screen.findByRole("button", { name: "Unlink #17" }));
  await screen.findByRole("button", { name: "Retry original PR unlink" });
  const original = f.unlink.mock.calls[0][0], method = SessionService.method.unlinkSessionPullRequest;
  const wire = toBinary(method.input, create(method.input, original));
  void f.client.invalidateQueries({ refetchType:"active" });
  await screen.findByText("No PR associations on this page.");
  expect(screen.queryByRole("button", { name: "Unlink #17" })).toBeNull();
  expect(screen.getByRole("button", { name: "Retry original PR unlink" })).toBeTruthy();
  expect(screen.getByText(`Original association: ${original.mutation!.id}`)).toBeTruthy();

  closeAssociations();
  openAssociations();
  await screen.findByText("No PR associations on this page.");
  expect(screen.getByRole("button", { name: "Retry original PR unlink" })).toBeTruthy();
  view.rerender(f.viewFor(null));
  view.rerender(f.viewFor({ ...f.session, id: newRequestId() }));
  openAssociations();
  await screen.findByText("No PR associations on this page.");
  expect(screen.queryByRole("region", { name: "Pending PR unlinks" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Retry original PR unlink" })).toBeNull();
  expect(f.unlink).toHaveBeenCalledOnce();

  view.rerender(f.viewFor());
  fireEvent.click(await screen.findByRole("button", { name: "Retry original PR unlink" }));
  await waitFor(() => expect(screen.queryByRole("region", { name: "Pending PR unlinks" })).toBeNull());
  expect(f.unlink.mock.calls[1][0]).toEqual(original);
  expect(toBinary(method.input, create(method.input, f.unlink.mock.calls[1][0]))).toEqual(wire);
  expect(original.mutation).toMatchObject({ id: f.rows[0].id, expectedRevision: 9007199254740993n });
  expect(f.unlinkEffects).toHaveBeenCalledOnce();
  expect(f.link).not.toHaveBeenCalled();
});

it.each(["association", "request"])("keeps the original unlink after a mismatched %s acknowledgment", async (field) => {
  const f = fixture(), commit = f.unlink.getMockImplementation()!;
  const mismatched = async (request: Parameters<typeof commit>[0]) => { const receipt = await commit(request); return { ...receipt, [field === "association" ? "id" : "requestId"]: newRequestId() }; };
  f.unlink.mockImplementationOnce(mismatched).mockImplementationOnce(mismatched);
  render(f.view); openAssociations();
  fireEvent.click(await screen.findByRole("button", { name: "Unlink #17" }));
  await screen.findByRole("button", { name: "Retry original PR unlink" });
  void f.client.invalidateQueries({ refetchType:"active" });
  await screen.findByText("No PR associations on this page.");
  fireEvent.click(screen.getByRole("button", { name: "Retry original PR unlink" }));
  await waitFor(() => expect(f.unlink).toHaveBeenCalledTimes(2));
  await waitFor(() => expect((screen.getByRole("button", { name: "Retry original PR unlink" }) as HTMLButtonElement).disabled).toBe(false));
  expect(screen.getByRole("region", { name: "Pending PR unlinks" })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Retry original PR unlink" }));
  await waitFor(() => expect(screen.queryByRole("region", { name: "Pending PR unlinks" })).toBeNull());
  expect(f.unlink.mock.calls[1][0]).toEqual(f.unlink.mock.calls[0][0]);
  expect(f.unlink.mock.calls[2][0]).toEqual(f.unlink.mock.calls[0][0]);
  expect(f.unlinkEffects).toHaveBeenCalledOnce();
});

it("retains a late unverifiable unlink acknowledgment after the panel closes", async () => {
  const f = fixture(), commit = f.unlink.getMockImplementation()!;
  let complete!: (receipt: Awaited<ReturnType<typeof commit>>) => void;
  f.unlink.mockImplementationOnce(() => new Promise((resolve) => { complete = resolve; }));
  render(f.view); openAssociations();
  fireEvent.click(await screen.findByRole("button", { name: "Unlink #17" }));
  await waitFor(() => expect(f.unlink).toHaveBeenCalledOnce());
  const original = f.unlink.mock.calls[0][0];
  closeAssociations();
  const receipt = await commit(original);
  await act(async () => { complete({ ...receipt, requestId: newRequestId() }); });
  openAssociations();
  await screen.findByText("No PR associations on this page.");
  fireEvent.click(await screen.findByRole("button", { name: "Retry original PR unlink" }));
  await waitFor(() => expect(screen.queryByRole("region", { name: "Pending PR unlinks" })).toBeNull());
  expect(f.unlink.mock.calls[1][0]).toEqual(original);
  expect(f.unlinkEffects).toHaveBeenCalledOnce();
});

it("exposes retained associations without a new manual PR link form", async () => {
 const f=fixture();render(f.view);await screen.findByText("<script>Retained PR</script>");
 expect(screen.queryByLabelText("PR project repository")).toBeNull();expect(screen.queryByLabelText("PR number")).toBeNull();expect(screen.queryByRole("button",{name:"Link PR"})).toBeNull();expect(f.link).not.toHaveBeenCalled();
});

it("retries an uncertain original link after closing without replacing its input", async () => {
  const f = fixture(); f.link.mockRejectedValueOnce(new ConnectError("Response lost", Code.Unavailable)); render(f.view); openAssociations();
  fireEvent.click(screen.getByRole("button", {name:"Seed original link"}));
  await screen.findByRole("button", { name: "Retry original PR link" }); const original = f.link.mock.calls[0][0];
  closeAssociations(); openAssociations();
  fireEvent.click(await screen.findByRole("button", { name: "Retry original PR link" })); await waitFor(()=>expect(screen.queryByRole("button",{name:"Retry original PR link"})).toBeNull());
  expect(f.link.mock.calls[1][0]).toEqual(original);
});

it("rejects a foreign or malformed retained page and discards it when closed", async () => {
  const f = fixture(); expect(readSessionPR(f.rows[0], f.session.id)).toBeTruthy();
  f.replace([{ ...f.rows[0], sessionId: newRequestId() }]); render(f.view); openAssociations();
  await screen.findByRole("alert"); expect(screen.queryByRole("button", { name: "Unlink #17" })).toBeNull();
  expect(readSessionPR({ ...f.rows[0], documentJson: encode({ ...f.value, remote_repository_id: 9007199254740993 }) }, f.session.id)).toBeUndefined();
  closeAssociations(); await waitFor(() => expect(f.client.getQueryCache().getAll().filter(q => JSON.stringify(q.queryKey).includes("sessionId"))).toHaveLength(0));
});

it("loads the reader directly and disposes its presentation without introducing link drafts",async()=>{
 const f=fixture();render(f.view);await screen.findByText("<script>Retained PR</script>");
 const reads=f.list.mock.calls.filter(([request])=>request.filter?.kind===EntityKind.PULL_REQUEST).length;
 closeAssociations();await waitFor(()=>expect(f.client.getQueryCache().getAll().filter(q=>JSON.stringify(q.queryKey).includes("sessionId"))).toHaveLength(0));
 openAssociations();await screen.findByText("<script>Retained PR</script>");
 expect(f.list.mock.calls.filter(([request])=>request.filter?.kind===EntityKind.PULL_REQUEST)).toHaveLength(reads+1);expect(f.link).not.toHaveBeenCalled();expect(f.unlink).not.toHaveBeenCalled();
});
