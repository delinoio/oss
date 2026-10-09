import { create, type MessageShape } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, InboxService, IntegrationService, NotificationPreferencesSchema, PullRequestFixProfile, PullRequestFixQuery, PullRequestFixService, ResourceSchema, ResourceService, SessionService, SystemService, newRequestId } from "@delinoio/delidev-api-client";
import { App } from "./App";
import { chooseScrollOption } from "./test-scroll-picker";
import { document, encode, object, type Document } from "./documents";
import { defaultRemediationPolicy } from "./remediation-policy";

// This fixture mounts the complete desktop shell for every case. Keep its
// product assertions under the repository's CI CPU contention budget without
// changing the global test deadline or any product deadline.
vi.setConfig({ testTimeout: 15000 });

type FixRequest = MessageShape<typeof PullRequestFixQuery.requestPullRequestFix.input>;

// These full-shell navigation fixtures can exceed Vitest's default on a
// shared CI worker under concurrent frontend/build load. Keep the larger
// deadline local to this file so product and unrelated test deadlines remain
// unchanged.
vi.setConfig({ testTimeout: 15000 });

function fixture() {
  const repositoryId = newRequestId(), projectId = newRequestId();
  const repository = create(ResourceSchema, { id: repositoryId, kind: EntityKind.REPOSITORY, revision: 1n, schemaVersion: 1, documentJson: encode({ name: "Fixture repository", integration_id: newRequestId(), github_owner: "fixture-owner", github_name: "repo" }) });
  const project = create(ResourceSchema, { id: projectId, kind: EntityKind.PROJECT, revision: 1n, schemaVersion: 1, documentJson: encode({ name: "Fixture project" }) });
  const observation = { base_sha: "a".repeat(40), head_sha: "b".repeat(40), observed_at: "2026-09-28T00:00:00Z" };
  const target = { version: 1, provider: "github.com", repository_id: repositoryId, remote_repository_id: "37", repository_node_id: "R_37", owner: "fixture-owner", name: "repo", pull_request_id: "53", pull_request_node_id: "PR_53", number: "17", title: "Original fixture PR", observed_at: observation.observed_at };
  const set = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.PROBLEM, revision: 9007199254740993n, schemaVersion: 1, documentJson: encode({ version: 1, type: "pull-request-set", target, feedback: observation }) });
  const problem = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.PROBLEM, revision: 9007199254740995n, schemaVersion: 1, documentJson: encode({ version: 1, type: "pull-request-evidence", set_id: set.id, kind: "review-feedback", target, observation, content_version: "c".repeat(64), current: true, state: "unhandled", feedback: { kind: "review", id: "71", node_id: "REVIEW_71", body: "Original feedback", content_version: "c".repeat(64), published_at: observation.observed_at, url: "https://github.com/fixture-owner/repo/pull/17#pullrequestreview-71" }, original_provider: { native_state: "APPROVED", author_present: false }, latest_provider: { native_state: "APPROVED", author_present: false } }) });
  let repositoryAvailable = true, rowAvailable = true;
  const query = vi.fn(async (request: { queryJson: Uint8Array }) => {
    const q = JSON.parse(new TextDecoder().decode(request.queryJson));
    const remote = { provider: "github.com", id: "37", node_id: "R_37", owner: "fixture-owner", name: "repo", private: true };
    const item = { provider: "github.com", kind: "pull-request", identity_source: "pull-request-api", id: "53", node_id: "PR_53", number: "17", title: target.title, state: "open", created_at: "2026-09-01T00:00:00Z", updated_at: observation.observed_at, url: "https://github.com/fixture-owner/repo/pull/17", draft: false, ...(q.operation === "detail" ? { body: "Fixture body", merged: false, mergeable: true, base_ref: "main", head_ref: "feature", base_sha: observation.base_sha, head_sha: observation.head_sha, head_repository: { state: "available", repository: remote } } : {}) };
    return { schemaVersion: 1, documentJson: encode({ repository_id: repositoryId, repository_revision: "1", profile_id: document(repository).integration_id, generation_id: newRequestId(), observed_at: observation.observed_at, identity: { id: "17", node_id: "U_17", login: "fixture-user" }, repository: remote, query: q, items: rowAvailable ? [item] : [] }) };
  });
  const receipt = (request: FixRequest) => {
    const original = JSON.parse(new TextDecoder().decode(request.documentJson));
    const id = newRequestId(), sessionId = newRequestId(), chainId = newRequestId();
    const session = create(ResourceSchema, { id: sessionId, sessionId, kind: EntityKind.SESSION, revision: 1n, schemaVersion: 1, projectId, documentJson: encode({ project_id: projectId }) });
    const problemSet = create(ResourceSchema, { ...set, revision: set.revision + 1n, documentJson: encode({ ...document(set), remediation: { id: chainId, sequence: 1, automatic_attempts: 0, resume_baseline: 0, active_attempt_id: id } }) });
    const attempt = create(ResourceSchema, { id, kind: EntityKind.PROBLEM, revision: 3n, schemaVersion: 1, documentJson: encode({ version: 1, type: "pull-request-remediation-attempt", set_id: set.id, chain_id: chainId, sequence: 1, mode: "manual", state: "bound", policy: defaultRemediationPolicy(), problems: original.problems.map((p: { id: string; content_version: string }) => ({ id: p.id, content_version: p.content_version })), reserved: { actor_type: "owner", request_id: request.requestId, at: observation.observed_at }, session_id: sessionId, input_id: newRequestId(), input_digest: "d".repeat(64), project_id: projectId, git_target: { version: 1, target, head_repository: { provider: "github.com", id: "79", node_id: "R_fork", owner: "fixture-author", name: "fork", private: true }, base_ref: "main", head_ref: "feature", base_sha: observation.base_sha, head_sha: observation.head_sha } }) });
    return { attempt, session, problemSet, requestId: request.requestId };
  };
  const fix = vi.fn(async (request: FixRequest) => receipt(request));
  const capabilities = vi.fn(async () => ({ profiles: [PullRequestFixProfile.CODEX_GIT_V1] }));
  const history = vi.fn(async () => ({ problemSet: set, problems: [problem] }));
  const transport = createRouterTransport(router => {
    router.service(SystemService, { getStatus: () => ({ version: "0.1.0", protocolVersion: 1 }) });
    router.service(SessionService, { listSessions: () => ({ sessions: [] }) });
    router.service(ResourceService, { listResources: request => ({ resources: request.filter?.kind === EntityKind.REPOSITORY ? repositoryAvailable ? [repository] : [] : request.filter?.kind === EntityKind.PROJECT ? [project] : [] }), getResource: request => ({ resource: request.kind === EntityKind.PROJECT && request.id === projectId ? project : request.kind === EntityKind.REPOSITORY && repositoryAvailable && request.id === repositoryId ? repository : undefined }) });
    router.service(InboxService, { listInbox: () => ({ entries: [] }), getNotificationPreferences: () => ({ preferences: create(NotificationPreferencesSchema, { revision: 1n }) }) });
    router.service(IntegrationService, { queryRepositoryIntegration: query, listPullRequestProblems: history });
    router.service(PullRequestFixService, { getPullRequestFixCapabilities: capabilities, requestPullRequestFix: fix });
  });
  const start = async () => {
    fireEvent.click(screen.getByRole("button", { name: "Pull requests" }));
    fireEvent.click(await screen.findByRole("button", { name: `Fixture repository. Repository ID: ${repositoryId}` }));
    await waitFor(() => expect(screen.getByRole("button", { name: "Load pull requests" }).hasAttribute("disabled")).toBe(false));
    fireEvent.click(screen.getByRole("button", { name: "Load pull requests" }));
    fireEvent.click(await screen.findByRole("button", { name: "Read #17" }));
    fireEvent.click(await screen.findByRole("button", { name: "Show retained PR problems" }));
    fireEvent.click(await screen.findByRole("button", { name: "Fix now" }));
    await chooseScrollOption(screen.getByRole("combobox", { name: "Fix project" }), projectId);
    await waitFor(() => expect((screen.getByRole("button", { name: "Start fix" }) as HTMLButtonElement).disabled).toBe(false));
    fireEvent.click(screen.getByRole("button", { name: "Start fix" }));
    await waitFor(() => expect(fix).toHaveBeenCalledOnce());
  };
  const leave = () => fireEvent.click(screen.getByRole("button", { name: "Sessions" }));
  const back = () => fireEvent.click(screen.getByRole("button", { name: "Pull requests" }));
  const pending = () => within(screen.getByRole("region", { name: "Pending PR actions" }));
  return { transport, query, fix, capabilities, history, receipt, start, leave, back, pending, set, problem, projectId, repositoryId, removeRepository: () => { repositoryAvailable = false; }, removeRow: () => { rowAvailable = false; } };
}

it("retries the original Fix after App navigation before another GitHub Load", async () => {
  const f = fixture(); f.fix.mockRejectedValueOnce(new ConnectError("Lost receipt", Code.Unavailable));
  render(<App transport={f.transport} />); await f.start();
  await f.pending().findByRole("button", { name: "Retry original fix request" });
  f.leave(); f.back();
  expect(screen.queryByText("Original feedback")).toBeNull();
  expect(f.pending().getByText("Manual PR fix · remote repository 37 · PR ID 53")).toBeTruthy();
  const queries = f.query.mock.calls.length, history = f.history.mock.calls.length, capabilities = f.capabilities.mock.calls.length;
  expect(queries).toBe(2);
  expect(f.fix).toHaveBeenCalledOnce();
  fireEvent.click(f.pending().getByRole("button", { name: "Retry original fix request" }));
  await f.pending().findByText("No pending PR actions.");
  expect(f.fix.mock.calls[1][0]).toEqual(f.fix.mock.calls[0][0]);
  expect(JSON.parse(new TextDecoder().decode(f.fix.mock.calls[1][0].documentJson))).toEqual({ set_id: f.set.id, set_revision: "9007199254740993", project_id: f.projectId, repository_id: f.repositoryId, problems: [{ id: f.problem.id, revision: "9007199254740995", content_version: "c".repeat(64) }] });
  expect(f.query).toHaveBeenCalledTimes(queries); expect(f.history).toHaveBeenCalledTimes(history); expect(f.capabilities).toHaveBeenCalledTimes(capabilities);
});

it.each(["failed GitHub reload", "removed PR row", "removed repository"])("keeps original Fix recovery available with %s", async state => {
  const f = fixture(); f.fix.mockRejectedValueOnce(new ConnectError("Lost receipt", Code.Unavailable));
  render(<App transport={f.transport} />); await f.start();
  await f.pending().findByRole("button", { name: "Retry original fix request" });
  f.leave();
  if (state === "failed GitHub reload") f.query.mockRejectedValue(new ConnectError("GitHub unavailable", Code.Unavailable));
  if (state === "removed PR row") f.removeRow();
  if (state === "removed repository") f.removeRepository();
  f.back();
  if (state !== "removed repository") {
    if (state === "removed PR row") fireEvent.click(screen.getByRole("radio", { name: "All" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "Load pull requests" }).hasAttribute("disabled")).toBe(false));
    fireEvent.click(screen.getByRole("button", { name: "Load pull requests" }));
    if (state === "failed GitHub reload") await waitFor(() => expect(screen.getAllByRole("alert").some(alert => !screen.getByRole("region", { name: "Pending PR actions" }).contains(alert) && alert.textContent?.includes("server_unavailable"))).toBe(true));
    else await screen.findByText("No pull requests were returned on page 1.");
  }
  const queries = f.query.mock.calls.length;
  fireEvent.click(f.pending().getByRole("button", { name: "Retry original fix request" }));
  await f.pending().findByText("No pending PR actions.");
  expect(f.fix.mock.calls[1][0]).toEqual(f.fix.mock.calls[0][0]);
  expect(f.query).toHaveBeenCalledTimes(queries);
});

it.each(["missing", "request", "remote repository", "PR", "number", "local repository", "project", "session", "set", "problem", "content version"])("retains late %s receipts and validates recovery against the original authority", async mismatch => {
  const f = fixture(); let complete!: (value: ReturnType<typeof f.receipt>) => void;
  f.fix.mockImplementationOnce(() => new Promise(resolve => { complete = resolve; }));
  render(<App transport={f.transport} />); await f.start(); f.leave();
  const invalid = (request: FixRequest) => {
    const result = f.receipt(request);
    const attempt = document(result.attempt);
    const set = document(result.problemSet);
    const git = object(attempt.git_target), problems = attempt.problems as Document[];
    if (mismatch === "missing") return { requestId: request.requestId } as ReturnType<typeof f.receipt>;
    if (mismatch === "request") result.requestId = newRequestId();
    if (mismatch === "remote repository" || mismatch === "PR" || mismatch === "number") {
      const key = mismatch === "remote repository" ? "remote_repository_id" : mismatch === "PR" ? "pull_request_id" : "number";
      const target = { ...object(set.target), [key]: "99" };
      set.target = target; git.target = target;
    }
    if (mismatch === "local repository") object(git.target).repository_id = newRequestId();
    if (mismatch === "project") { result.session.projectId = newRequestId(); result.session.documentJson = encode({ project_id: result.session.projectId }); attempt.project_id = result.session.projectId; }
    if (mismatch === "session") result.session.id = newRequestId();
    if (mismatch === "set") { result.problemSet.id = newRequestId(); attempt.set_id = result.problemSet.id; }
    if (mismatch === "problem") problems[0].id = newRequestId();
    if (mismatch === "content version") problems[0].content_version = "e".repeat(64);
    result.attempt.documentJson = encode(attempt); result.problemSet.documentJson = encode(set);
    return result;
  };
  complete(invalid(f.fix.mock.calls[0][0])); f.back();
  await f.pending().findByRole("button", { name: "Retry original fix request" });
  f.fix.mockImplementationOnce(async request => invalid(request));
  fireEvent.click(f.pending().getByRole("button", { name: "Retry original fix request" }));
  await waitFor(() => expect(f.fix).toHaveBeenCalledTimes(2));
  await waitFor(() => expect((f.pending().getByRole("button", { name: "Retry original fix request" }) as HTMLButtonElement).disabled).toBe(false));
  expect(f.pending().queryByText("No pending PR actions.")).toBeNull();
  fireEvent.click(f.pending().getByRole("button", { name: "Retry original fix request" }));
  await f.pending().findByText("No pending PR actions.");
  expect(f.fix.mock.calls[1][0]).toEqual(f.fix.mock.calls[0][0]); expect(f.fix.mock.calls[2][0]).toEqual(f.fix.mock.calls[0][0]);
  expect(f.query).toHaveBeenCalledTimes(2);
});

it("clears a valid late receipt after leaving the row without replaying it", async () => {
  const f = fixture(); let complete!: (value: ReturnType<typeof f.receipt>) => void;
  f.fix.mockImplementationOnce(() => new Promise(resolve => { complete = resolve; }));
  render(<App transport={f.transport} />); await f.start(); f.leave();
  complete(f.receipt(f.fix.mock.calls[0][0])); f.back();
  await f.pending().findByText("No pending PR actions.");
  expect(f.fix).toHaveBeenCalledOnce(); expect(f.query).toHaveBeenCalledTimes(2); expect(f.history).toHaveBeenCalledOnce();
});

it.each([Code.PermissionDenied, Code.Unauthenticated, Code.FailedPrecondition, Code.NotFound])("keeps an already uncertain Fix after rejected replay %s", async code => {
  const f = fixture();
  f.fix.mockRejectedValueOnce(new ConnectError("Lost receipt", Code.Unavailable)).mockRejectedValueOnce(new ConnectError("Replay unavailable to the current client", code));
  render(<App transport={f.transport} />); await f.start();
  await f.pending().findByRole("button", { name: "Retry original fix request" });
  f.leave(); f.back();
  fireEvent.click(f.pending().getByRole("button", { name: "Retry original fix request" }));
  await waitFor(() => expect(f.fix).toHaveBeenCalledTimes(2));
  await waitFor(() => expect((f.pending().getByRole("button", { name: "Retry original fix request" }) as HTMLButtonElement).disabled).toBe(false));
  expect(f.pending().queryByText("No pending PR actions.")).toBeNull();
  fireEvent.click(f.pending().getByRole("button", { name: "Retry original fix request" }));
  await f.pending().findByText("No pending PR actions.");
  expect(f.fix.mock.calls[1][0]).toEqual(f.fix.mock.calls[0][0]); expect(f.fix.mock.calls[2][0]).toEqual(f.fix.mock.calls[0][0]);
  expect(f.query).toHaveBeenCalledTimes(2); expect(f.capabilities).toHaveBeenCalledOnce();
});
