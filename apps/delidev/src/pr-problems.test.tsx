import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, IntegrationService, ResourceSchema, newRequestId } from "@delinoio/delidev-api-client";
import { document, encode } from "./documents";
import { MutationIntents } from "./mutation";
import { OpenPRProblemHistory, PRProblemHistory, readPRProblem } from "./pr-problems";

function fixture() {
  const selection = { repositoryId: newRequestId(), remoteRepositoryId: "37", pullRequestId: "9007199254740993", number: "17" };
  const observation = { base_sha: "a".repeat(40), head_sha: "b".repeat(40), observed_at: "2026-09-28T00:00:00Z" };
  const target = { version: 1, provider: "github.com", repository_id: selection.repositoryId, remote_repository_id: selection.remoteRepositoryId, repository_node_id: "R_37", owner: "fixture-owner", name: "repo", pull_request_id: selection.pullRequestId, pull_request_node_id: "PR_17", number: "17", title: "Original PR", observed_at: observation.observed_at };
  let set = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.PROBLEM, revision: 1n, schemaVersion: 1, documentJson: encode({ version: 1, type: "pull-request-set", target, feedback: observation }) });
  const body = { version: 1, type: "pull-request-evidence", set_id: set.id, kind: "review-feedback", target, observation, content_version: "c".repeat(64), current: false, state: "unhandled", feedback: { kind: "review", id: "71", node_id: "REVIEW_71", body: "<script>original approved feedback</script>", native_state: "APPROVED", published_at: observation.observed_at, review_submitted_at: observation.observed_at, content_version: "c".repeat(64), url: "https://github.com/fixture-owner/repo/pull/17#pullrequestreview-71" }, original_provider: { native_state: "APPROVED", author_present: false }, latest_provider: { native_state: "DISMISSED", author_present: false } };
  let row = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.PROBLEM, revision: 9007199254740993n, schemaVersion: 1, documentJson: encode(body) });
  const list = vi.fn(async () => ({ problemSet: set, problems: [row] }));
  const dismiss = vi.fn(async (input: { mutation?: { id: string; expectedRevision: bigint; requestId: string }; contentVersion: string }) => {
    expect(input.mutation?.id).toBe(row.id); expect(input.mutation?.expectedRevision).toBe(9007199254740993n); expect(input.contentVersion).toBe(body.content_version);
    row = create(ResourceSchema, { ...row, revision: row.revision + 1n, documentJson: encode({ ...body, state: "locally-dismissed", dismissal: { actor_type: "owner", request_id: input.mutation!.requestId, at: observation.observed_at } }) });
    set = create(ResourceSchema, { ...set, revision: set.revision + 1n });
    return { problem: row, requestId: input.mutation!.requestId };
  });
  const collect = vi.fn(async (input: { requestId: string; repositoryId: string; number: string }) => ({ problemSet: set, requestId: input.requestId }));
  const transport = createRouterTransport(router => router.service(IntegrationService, { listPullRequestProblems: list, dismissPullRequestProblem: dismiss, refreshPullRequestProblems: collect }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = (toggle = false) => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents>{toggle ? <OpenPRProblemHistory selection={selection} /> : <PRProblemHistory selection={selection} />}</MutationIntents></QueryClientProvider></TransportProvider>;
  return { selection, row, set, body, list, dismiss, collect, view };
}
it("retains original approved feedback and dismisses only its exact local version", async () => {
  const f = fixture(); const view = render(f.view());
  fireEvent.click(await screen.findByRole("button", { name: "Dismiss this content version" }));
  await screen.findByText(/Local handling: Locally dismissed/);
  expect(f.collect).not.toHaveBeenCalled(); expect(f.dismiss).toHaveBeenCalledTimes(1);
  expect(screen.getByText(/Original provider state: APPROVED.*Latest observed provider state: DISMISSED/)).toBeTruthy();
  expect(screen.getByText(/Retained earlier version/)).toBeTruthy();
  expect(screen.getByText(/<script>original approved feedback/)).toBeTruthy(); expect(view.container.querySelector("script")).toBeNull();
});
it("retries an uncertain dismissal with its original identity across closing history", async () => {
  const f = fixture(); f.dismiss.mockRejectedValueOnce(new ConnectError("Acknowledgment unavailable", Code.Unavailable));
  render(f.view(true)); fireEvent.click(screen.getByRole("button", { name: "Show retained feedback" }));
  fireEvent.click(await screen.findByRole("button", { name: "Dismiss this content version" }));
  await screen.findByRole("button", { name: "Retry original dismissal" });
  fireEvent.click(screen.getByRole("button", { name: "Close retained feedback" }));
  fireEvent.click(screen.getByRole("button", { name: "Show retained feedback" }));
  fireEvent.click(await screen.findByRole("button", { name: "Retry original dismissal" }));
  await screen.findByText(/Local handling: Locally dismissed/);
  expect(f.dismiss.mock.calls[1][0]).toEqual(f.dismiss.mock.calls[0][0]);
});
it("collects only on explicit action and keeps exact remote IDs for retained reads", async () => {
  const f = fixture(); render(f.view());
  await screen.findByRole("button", { name: "Dismiss this content version" });
  expect(f.collect).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Collect current published feedback" }));
  await waitFor(() => expect(f.collect).toHaveBeenCalledTimes(1));
  expect(f.collect.mock.calls[0][0]).toMatchObject({ repositoryId: f.selection.repositoryId, number: "17" });
});
it("rejects foreign set identity, malformed local decisions and unknown schema", () => {
  const f = fixture(); expect(readPRProblem(f.row, f.set, f.selection)).toBeTruthy();
  for (const patch of [{ set_id: newRequestId() }, { content_version: "d".repeat(64) }, { state: "locally-dismissed" }, { target: { ...document(f.row).target as object, pull_request_id: "99" } }]) {
    expect(readPRProblem(create(ResourceSchema, { ...f.row, documentJson: encode({ ...f.body, ...patch }) }), f.set, f.selection)).toBeUndefined();
  }
  expect(readPRProblem(create(ResourceSchema, { ...f.row, schemaVersion: 2 }), f.set, f.selection)).toBeUndefined();
});
