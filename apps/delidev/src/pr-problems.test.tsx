import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, IntegrationService, ResourceService, ResourceSchema, PullRequestProblemCollectionKind, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { ciObservation, queueCIObservation } from "./github-ci-fixture";
import { document, encode } from "./documents";
import { MutationIntents } from "./mutation";
import { PRWorkflowProvider } from "./pr-workflow";
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
  const collect = vi.fn(async (input: { requestId: string; repositoryId: string; number: string; kind?: PullRequestProblemCollectionKind }) => ({ problemSet: set, requestId: input.requestId }));
  const proofGet = vi.fn(async () => ({ resource: undefined as Resource | undefined }));
  const transport = createRouterTransport(router => { router.service(IntegrationService, { listPullRequestProblems: list, dismissPullRequestProblem: dismiss, refreshPullRequestProblems: collect }); router.service(ResourceService, { getResource: proofGet }); });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = (toggle = false) => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><PRWorkflowProvider>{toggle ? <OpenPRProblemHistory selection={selection} /> : <PRProblemHistory selection={selection} />}</PRWorkflowProvider></MutationIntents></QueryClientProvider></TransportProvider>;
  return { selection, row, set, body, list, dismiss, collect, proofGet, client, view };
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
  render(f.view(true)); fireEvent.click(screen.getByRole("button", { name: "Show retained PR problems" }));
  fireEvent.click(await screen.findByRole("button", { name: "Dismiss this content version" }));
  await screen.findByRole("button", { name: "Retry original dismissal" });
  fireEvent.click(screen.getByRole("button", { name: "Close retained PR problems" }));
  fireEvent.click(screen.getByRole("button", { name: "Show retained PR problems" }));
  fireEvent.click(await screen.findByRole("button", { name: "Retry original dismissal" }));
  await screen.findByText(/Local handling: Locally dismissed/);
  expect(f.dismiss.mock.calls[1][0]).toEqual(f.dismiss.mock.calls[0][0]);
});
it("collects only on explicit action and keeps exact remote IDs for retained reads", async () => {
  const f = fixture(); render(f.view());
  await screen.findByRole("button", { name: "Dismiss this content version" });
  expect(f.collect).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Collect selected PR problems" }));
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

function ciHistoryFixture() {
  const f = fixture(), ci = ciObservation(), proofId = newRequestId();
  const value = { ...f.body, kind: "ci-failure", feedback: undefined, original_provider: undefined, latest_provider: undefined, ci: { observation_id: proofId, context: ci.head.contexts[0], source: ci.result.source, rules_digest: ci.rules.digest } };
  const row = create(ResourceSchema, { ...f.row, documentJson: encode(value) });
  f.set.documentJson = encode({ ...document(f.set), ci: { observation: f.body.observation, state: "unknown", reason: "commit-unverified", source: "unknown", rules_digest: ci.rules.digest } });
  const proof = create(ResourceSchema, { id: proofId, kind: EntityKind.PROBLEM, revision: 1n, schemaVersion: 1, documentJson: encode({ version: 1, type: "pull-request-ci-observation", set_id: f.set.id, target: f.body.target, observation: f.body.observation, base_ref: "main", head_ref: "feature", pull_request_state: "open", merged: false, mergeable: true, ci: { ...ci, head: { ...ci.head, contexts: [Object.fromEntries(Object.entries(ci.head.contexts[0]).reverse())] } } }) });
  f.list.mockResolvedValue({ problemSet: f.set, problems: [row] });
  f.proofGet.mockResolvedValue({ resource: proof });
  return { ...f, ci, proof, ciRow: row };
}
it("keeps original required CI proof separate from current unknown evaluation and disposes closed proof reads", async () => {
  const f = ciHistoryFixture(); const view = render(f.view());
  await screen.findByRole("heading", { name: "Required CI failure · CI Result" });
  expect(screen.getByText(/Latest CI evaluation: unknown/)).toBeTruthy();
  expect(f.proofGet).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Inspect original CI rules and results" }));
  await screen.findByRole("table", { name: "Active ruleset CI requirements" });
  expect(screen.getByText(/This is the original retained evaluation/)).toBeTruthy();
  expect(view.container.querySelector("script")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Close original CI evaluation" }));
  await waitFor(() => expect(f.client.getQueryCache().getAll()).toHaveLength(1));
  expect(screen.queryByRole("table", { name: "Active ruleset CI requirements" })).toBeNull();
});
it("rejects mismatched original CI context output without displaying its rule evaluation", async () => {
  const f = ciHistoryFixture(), body = document(f.proof);
  const changed = ciObservation(); changed.head.contexts[0].evidence.summary = "Changed original output";
  f.proof.documentJson = encode({ ...body, ci: changed });
  render(f.view()); fireEvent.click(await screen.findByRole("button", { name: "Inspect original CI rules and results" }));
  await screen.findByText("The original CI proof does not match this result version.");
  expect(screen.queryByRole("table", { name: "Active ruleset CI requirements" })).toBeNull();
});

it("reads a historical ALLGREEN entry proof without promoting it to current failure authority", async () => {
  const f = ciHistoryFixture(), ci = queueCIObservation(), body = document(f.proof), value = document(f.ciRow);
  ci.merge_queue.entry.pull_request_node_id = "PR_17";
  const evidence = { observation_id: f.proof.id, context: ci.merge_queue.rollup.contexts[0], source: "merge-queue", rules_digest: ci.rules.digest, queue_node_id: "QUEUE_Q", queue_entry_node_id: "ENTRY_E" };
  const row = create(ResourceSchema, { ...f.ciRow, documentJson: encode({ ...value, ci: evidence }) });
  f.list.mockResolvedValue({ problemSet: f.set, problems: [row] });
  f.proof.documentJson = encode({ ...body, ci });
  expect(readPRProblem(row, f.set, f.selection)).toBeTruthy();
  const missing = create(ResourceSchema, { ...row, documentJson: encode({ ...value, ci: { ...evidence, queue_entry_node_id: undefined } }) });
  expect(readPRProblem(missing, f.set, f.selection)).toBeUndefined();
  render(f.view());
  fireEvent.click(await screen.findByRole("button", { name: "Inspect original CI rules and results" }));
  await screen.findByRole("table", { name: "Active ruleset CI requirements" });
  expect(screen.getByText(/This is the original retained evaluation/)).toBeTruthy();
  expect(screen.getByText(/Latest CI evaluation: unknown/)).toBeTruthy();
  expect(f.collect).not.toHaveBeenCalled();
});
it("collects CI and conflict through explicit independent enum selections", async () => {
  const f = fixture(); render(f.view());
  await screen.findByRole("button", { name: "Dismiss this content version" });
  fireEvent.change(screen.getByLabelText("Problem collection kind"), { target: { value: String(PullRequestProblemCollectionKind.CI) } });
  fireEvent.click(screen.getByRole("button", { name: "Collect selected PR problems" }));
  await waitFor(() => expect(f.collect).toHaveBeenCalledTimes(1));
  expect(f.collect.mock.calls[0][0].kind).toBe(PullRequestProblemCollectionKind.CI);
  await waitFor(() => expect((screen.getByLabelText("Problem collection kind") as HTMLSelectElement).disabled).toBe(false));
  fireEvent.change(screen.getByLabelText("Problem collection kind"), { target: { value: String(PullRequestProblemCollectionKind.CONFLICT) } });
  fireEvent.click(screen.getByRole("button", { name: "Collect selected PR problems" }));
  await waitFor(() => expect(f.collect).toHaveBeenCalledTimes(2));
  expect(f.collect.mock.calls[1][0].kind).toBe(PullRequestProblemCollectionKind.CONFLICT);
  expect(f.collect.mock.calls[0][0].requestId).not.toBe(f.collect.mock.calls[1][0].requestId);
});
it("retains an original conflict transition while showing current unknown mergeability", async () => {
  const f = fixture();
  const conflict = { transition_id: newRequestId(), base_ref: "main", head_ref: "feature", observation: f.body.observation };
  const value = { ...f.body, kind: "merge-conflict", feedback: undefined, original_provider: undefined, latest_provider: undefined, conflict };
  const row = create(ResourceSchema, { ...f.row, documentJson: encode(value) });
  f.set.documentJson = encode({ ...document(f.set), conflict: { observation: f.body.observation, base_ref: "main", head_ref: "feature", pull_request_state: "open", merged: false, mergeable: null, state: "unknown", active: conflict } });
  f.list.mockResolvedValue({ problemSet: f.set, problems: [row] });
  render(f.view()); await screen.findByRole("heading", { name: "Merge conflict · feature → main" });
  expect(screen.getByText(/Latest mergeability: unknown/)).toBeTruthy();
  expect(screen.getByText(conflict.transition_id)).toBeTruthy();
  expect(screen.getByRole("button", { name: "Dismiss this content version" })).toBeTruthy();
  expect(readPRProblem(create(ResourceSchema, { ...row, documentJson: encode({ ...value, conflict: { ...conflict, observation: { ...f.body.observation, head_sha: "d".repeat(40) } } }) }), f.set, f.selection)).toBeUndefined();
});
