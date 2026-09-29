import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, IntegrationService, ResourceSchema, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, encode } from "./documents";
import { MutationIntents } from "./mutation";
import { defaultRemediationPolicy } from "./remediation-policy";
import { readPRProblemSet } from "./pr-problems";
import { PRRemediationHistory } from "./pr-remediation-history";
import { PRWorkflowProvider } from "./pr-workflow";
import { readRemediationAttempt, readRemediationChain } from "./pr-remediation-model";

function fixture() {
  const setId = newRequestId(), chainId = newRequestId(), at = "2026-09-28T00:00:00.000000001Z";
  const selection = { repositoryId: newRequestId(), remoteRepositoryId: "9007199254740993", pullRequestId: "9007199254740995", number: "17" };
  const chain = { id: chainId, sequence: 2, automatic_attempts: 2, resume_baseline: 0, limit: { limit: 2, attempts: 2, policy_digest: "a".repeat(64), at } };
  const value = { version: 1, type: "pull-request-set", target: { version: 1, provider: "github.com", repository_id: selection.repositoryId, remote_repository_id: selection.remoteRepositoryId, repository_node_id: "R_original", owner: "fixture-owner", name: "repo", pull_request_id: selection.pullRequestId, pull_request_node_id: "PR_original", number: "17", title: "Original PR", observed_at: at }, feedback: { base_sha: "a".repeat(40), head_sha: "b".repeat(40), observed_at: at }, remediation: chain };
  const resource = (id: string, data: unknown) => create(ResourceSchema, { id, kind: EntityKind.PROBLEM, schemaVersion: 1, revision: 9007199254740993n, documentJson: encode(data) });
  let set = resource(setId, value);
  const attempts = [2, 1].map(sequence => resource(newRequestId(), { version: 1, type: "pull-request-remediation-attempt", set_id: setId, chain_id: chainId, sequence, mode: "automatic", state: "finished", outcome: "failed", session_id: newRequestId(), input_id: newRequestId(), input_digest: "d".repeat(64), execution_id: newRequestId(), started_at: "2026-09-28T00:00:00.000000002Z", policy: defaultRemediationPolicy(), problems: [{ id: newRequestId(), content_version: "c".repeat(64) }], reserved: { request_id: newRequestId(), actor_type: "owner", at }, finished_at: "2026-09-28T00:00:01Z" }));
  const history = vi.fn(async (_request: unknown) => ({ problemSet: set, attempts }));
  const resume = vi.fn(async (request: { mutation?: { requestId: string } }) => ({ problemSet: set, requestId: request.mutation?.requestId }));
  const transport = createRouterTransport(router => { router.service(IntegrationService, { listPullRequestRemediationAttempts: history, resumePullRequestRemediation: resume }); });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const view = () => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><PRWorkflowProvider><PRRemediationHistory selection={selection} validateSet={row => Boolean(readPRProblemSet(row, selection))} /></PRWorkflowProvider></MutationIntents></QueryClientProvider></TransportProvider>;
  return { view, history, resume, set, attempts, value, resource, replaceSet: (next: Resource) => { set = next; } };
}

it("retains exact PR and revision identities and retries only the original confirmed allowance mutation", async () => {
  const f = fixture();
  f.resume.mockRejectedValueOnce(new ConnectError("response lost", Code.Unavailable));
  render(f.view());
  await screen.findByRole("article", { name: "Remediation attempt 2" });
  expect(f.history.mock.calls[0][0]).toMatchObject({ remoteRepositoryId: "9007199254740993", pullRequestId: "9007199254740995" });
  fireEvent.click(screen.getByRole("button", { name: "Resume automatic attempt allowance" }));
  expect(f.resume).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Confirm allowance resumption" }));
  await screen.findByRole("button", { name: "Retry original allowance resumption" });
  f.replaceSet(create(ResourceSchema, { ...f.set, revision: f.set.revision + 1n }));
  fireEvent.click(screen.getByRole("button", { name: "Refresh remediation history" }));
  await screen.findByText(/The PR history changed/);
  fireEvent.click(screen.getByRole("button", { name: "Retry original allowance resumption" }));
  await waitFor(() => expect(f.resume).toHaveBeenCalledTimes(2));
  expect(f.resume.mock.calls[0][0]).toEqual(f.resume.mock.calls[1][0]);
  expect(f.resume.mock.calls[0][0]).toMatchObject({ mutation: { id: f.set.id, expectedRevision: 9007199254740993n } });
});

it.each(["foreign-chain", "native-before-start", "invalid-time", "bad-allowance", "unordered"])("disables actions for %s history", async scenario => {
  const f = fixture(), attempt = document(f.attempts[0]);
  if (scenario === "foreign-chain") attempt.chain_id = newRequestId();
  if (scenario === "native-before-start") attempt.state = "canceled";
  if (scenario === "invalid-time") attempt.finished_at = "2026-09-28T00:00:00Z";
  if (scenario === "bad-allowance") { f.value.remediation.resume_baseline = 3; f.replaceSet(f.resource(f.set.id, f.value)); }
  if (scenario === "unordered") f.attempts.reverse();
  else f.attempts[0] = f.resource(f.attempts[0].id, attempt);
  render(f.view());
  await screen.findByRole("alert");
  expect(screen.queryByRole("button", { name: "Resume automatic attempt allowance" })).toBeNull();
  expect(f.resume).not.toHaveBeenCalled();
});

it("preserves a charged startup rejection separately from native completion", () => {
  const f = fixture(), original = document(f.attempts[0]);
  const rejected = { ...original, state: "finished", session_id: newRequestId(), input_id: newRequestId(), input_digest: "a".repeat(64), execution_id: newRequestId(), started_at: "2026-09-28T00:00:00.000000002Z", outcome: "not-started", startup_rejection_job_id: newRequestId() };
  expect(readRemediationChain(f.value.remediation)?.automatic_attempts).toBe(2);
  expect(readRemediationAttempt(f.resource(f.attempts[0].id, rejected), f.set)?.outcome).toBe("not-started");
  expect(readRemediationAttempt(f.resource(f.attempts[0].id, { ...rejected, startup_rejection_job_id: undefined }), f.set)).toBeUndefined();
});
