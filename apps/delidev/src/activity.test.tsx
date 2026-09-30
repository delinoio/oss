import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { ActivityEntrySchema, ActivityKind, ActivityPRMetadataSchema, ActivityPRActorType, ActivityPRMode, ActivityPRAttemptState, ActivityService, EntityKind, ResourceService, ResourceSchema, newRequestId } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { Activity } from "./views";

function fixture() {
  const problemId = newRequestId(), setId = newRequestId(), requestId = newRequestId(), at = "2026-09-29T00:00:00Z", version = "a".repeat(64);
  const target = { version: 1, provider: "github.com", repository_id: newRequestId(), remote_repository_id: "37", repository_node_id: "R_37", owner: "fixture-owner", name: "repo", pull_request_id: "53", pull_request_node_id: "PR_53", number: "17", title: "Original title", observed_at: at };
  const observation = { base_sha: "a".repeat(40), head_sha: "b".repeat(40), observed_at: at };
  const problem = { version: 1, type: "pull-request-evidence", set_id: setId, kind: "review-feedback", target, observation, content_version: version, current: true, state: "locally-dismissed", dismissal: { request_id: requestId, actor_type: "owner", at }, feedback: { kind: "conversation-comment", id: "71", node_id: "COMMENT_71", body: "Private original feedback", published_at: at, url: "https://github.com/fixture-owner/repo/pull/17#issuecomment-71", content_version: version }, original_provider: { author_present: false }, latest_provider: { author_present: false } };
  const set = create(ResourceSchema, { id: setId, kind: EntityKind.PROBLEM, schemaVersion: 1, revision: 2n, documentJson: encode({ version: 1, type: "pull-request-set", target, feedback: observation }) });
  const source = create(ResourceSchema, { id: problemId, kind: EntityKind.PROBLEM, schemaVersion: 1, revision: 2n, documentJson: encode(problem) });
  const metadata = create(ActivityPRMetadataSchema, { problemSetId: setId, sourceId: problemId, remoteRepositoryId: "37", pullRequestId: "53", number: "17", owner: "fixture-owner", name: "repo", requestId, actorType: ActivityPRActorType.ACTIVITY_PR_ACTOR_TYPE_OWNER, problems: [{ id: problemId, contentVersion: version }] });
  const entry = (kind: ActivityKind, state = ActivityPRAttemptState.ACTIVITY_PR_ATTEMPT_STATE_UNSPECIFIED) => create(ActivityEntrySchema, { id: newRequestId(), sourceKind: EntityKind.PROBLEM, sourceRevision: 1n, observedAtUnixMs: 1790640000000n, kind, pullRequest: { ...metadata, attemptState: state, mode: state ? ActivityPRMode.ACTIVITY_PR_MODE_AUTOMATIC : ActivityPRMode.ACTIVITY_PR_MODE_UNSPECIFIED } });
  const entries = [entry(ActivityKind.PR_PROBLEM_OBSERVED), entry(ActivityKind.PR_PROBLEM_DISMISSED), entry(ActivityKind.PR_REMEDIATION_ATTEMPT, ActivityPRAttemptState.ACTIVITY_PR_ATTEMPT_STATE_FAILED), entry(ActivityKind.PR_REMEDIATION_ATTEMPT, ActivityPRAttemptState.ACTIVITY_PR_ATTEMPT_STATE_UNCERTAIN), entry(ActivityKind.PR_REMEDIATION_ATTEMPT, ActivityPRAttemptState.ACTIVITY_PR_ATTEMPT_STATE_SUCCEEDED), entry(ActivityKind.PR_VERIFIED_HANDLED)];
  const verificationId = newRequestId();
  entries[5].pullRequest!.sourceId = verificationId;
  entries[5].pullRequest!.verificationId = verificationId;
  const verification = create(ResourceSchema, { id: verificationId, kind: EntityKind.PROBLEM, schemaVersion: 1, revision: 1n, documentJson: encode({ version: 1, type: "pull-request-handling-verification", set_id: setId, problems: [{ id: problemId, content_version: version }], proof_digest: "f".repeat(64), actor: { request_id: newRequestId(), actor_type: "owner", at } }) });
  const list = vi.fn(async () => ({ entries }));
  const get = vi.fn(async (request: { id: string }) => ({ resource: request.id === setId ? set : request.id === verificationId ? verification : source }));
  const transport = createRouterTransport(router => {
    router.service(ActivityService, { listActivity: list });
    router.service(ResourceService, { listResources: async () => ({ resources: [] }), getResource: get });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return { client, transport, list, get, entries, source, problemId, verificationId };
}

it("shows independent PR decisions and outcomes and reads original sources only on demand", async () => {
  const f = fixture(), open = vi.fn();
  render(<QueryClientProvider client={f.client}><TransportProvider transport={f.transport}><Activity active open={open} /></TransportProvider></QueryClientProvider>);
  await screen.findByText("pr problem observed");
  expect(screen.getByText("pr problem dismissed")).toBeTruthy();
  expect(screen.getByText("pr verified handled")).toBeTruthy();
  for (const state of ["failed", "uncertain", "succeeded"]) expect(screen.getByText(new RegExp(`Attempt: ${state}`))).toBeTruthy();
  expect(screen.queryByText("Waiting or skipped occurrence")).toBeNull();
  expect(screen.queryByText("Private original feedback")).toBeNull();
  expect(f.get).not.toHaveBeenCalled();
  const first = screen.getByText("pr problem observed").closest("article")!;
  fireEvent.click(within(first).getByText("Inspect original PR record"));
  await screen.findByText("Private original feedback");
  expect(screen.getByText("Recorded source revision: 1 · Current source revision: 2")).toBeTruthy();
  expect(f.get.mock.calls.map(([request]) => request.id).sort()).toEqual([f.problemId, f.entries[0].pullRequest!.problemSetId].sort());
  expect(open).not.toHaveBeenCalled();
  fireEvent.click(within(first).getByText("Close original PR record"));
  await waitFor(() => expect(screen.queryByText("Private original feedback")).toBeNull());
});

it("inspects only a dedicated verified-handling source without exposing its proof commitment", async () => {
  const f = fixture();
  render(<QueryClientProvider client={f.client}><TransportProvider transport={f.transport}><Activity active open={() => undefined} /></TransportProvider></QueryClientProvider>);
  const row = (await screen.findByText("pr verified handled")).closest("article")!;
  fireEvent.click(within(row).getByText("Inspect original PR record"));
  await screen.findByText(new RegExp(`Dedicated verification record: ${f.verificationId}`));
  expect(screen.queryByText("f".repeat(64))).toBeNull();
  expect(screen.queryByText("Private original feedback")).toBeNull();
});

it("rejects a source read that substitutes another original version", async () => {
  const f = fixture();
  f.source.id = newRequestId();
  render(<QueryClientProvider client={f.client}><TransportProvider transport={f.transport}><Activity active open={() => undefined} /></TransportProvider></QueryClientProvider>);
  const first = (await screen.findByText("pr problem observed")).closest("article")!;
  fireEvent.click(within(first).getByText("Inspect original PR record"));
  await screen.findByRole("alert");
  expect(screen.queryByText("Private original feedback")).toBeNull();
});

it("disposes original source inspection on inactivity and requires a new explicit read on return", async () => {
  const f = fixture();
  const view = (active: boolean) => <QueryClientProvider client={f.client}><TransportProvider transport={f.transport}><Activity active={active} open={() => undefined} /></TransportProvider></QueryClientProvider>;
  const { rerender } = render(view(true));
  const row = (await screen.findByText("pr problem observed")).closest("article")!;
  fireEvent.click(within(row).getByText("Inspect original PR record"));
  await screen.findByText("Private original feedback");
  expect(f.get).toHaveBeenCalledTimes(2);
  rerender(view(false));
  await waitFor(() => expect(screen.queryByText("Private original feedback")).toBeNull());
  rerender(view(true));
  await screen.findByText("pr problem observed");
  expect(screen.queryByText("Private original feedback")).toBeNull();
  expect(f.get).toHaveBeenCalledTimes(2);
  fireEvent.click(within(screen.getByText("pr problem observed").closest("article")!).getByText("Inspect original PR record"));
  await screen.findByText("Private original feedback");
  expect(f.get).toHaveBeenCalledTimes(4);
});
