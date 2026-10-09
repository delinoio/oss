// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { EntityKind, ResourceSchema, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { act, render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { encode } from "./documents";
import { conversationProjection } from "./tool-turn-projection";
import { initialExecutionPending, progressMessages, progressResponseOwner, responseSuppressesProgress, responseEvidence, ResponseEvidenceKind, SessionProgressPhase, sessionProgress, type ProgressObservation } from "./session-progress";
import { SessionProgressStatus } from "./session-progress-status";
import { i18n } from "./localization";
const sessionId = newRequestId(), executionId = newRequestId(), inputId = newRequestId(), jobId = newRequestId();
const progress = { execution_id: executionId, input_id: inputId, job_id: jobId, last_sequence: 6, native_thread_id: "original-thread", native_turn_id: "original-turn", outcome: "running", waiting: { user_input: false, approval: false }, accepted_inputs: [{ input_id: inputId, prompt_digest: "a".repeat(64) }] };
const body = { archive: "active", recovery: "none", dispatch: "claimed", outcome: "running", pending_inputs: 0, preparation: { job_id: newRequestId(), state: "ready" }, active_execution_id: executionId, initial_execution: { id: executionId, input_id: inputId }, execution: progress };
function resource(kind: EntityKind, document: object, id = newRequestId(), revision = 1n): Resource { return create(ResourceSchema, { kind, id, sessionId, revision, schemaVersion: 1, documentJson: encode(document) }); }
function observation(extra: object = {}, options: Partial<ProgressObservation> = {}): ProgressObservation { return { session: resource(EntityKind.SESSION, { ...body, ...extra }, sessionId), sessionId, current: true, complete: true, blocked: false, messages: [], queueCurrent: true, queue: [], ...options }; }
function message(extra: object = {}, revision = 1n, id = newRequestId()) { return resource(EntityKind.MESSAGE, { execution_id: executionId, input_id: inputId, native_thread_id: "original-thread", native_turn_id: "original-turn", role: "user", state: "complete", text: "Initial original input", first_sequence: 3, last_sequence: 3, ...extra }, id, revision); }
it("resolves the complete ordered preparation/queue/claim/acceptance phases", () => {
 const queued = resource(EntityKind.QUEUE, { delivery: "queued", sequence: 1, content_revision: 1, mode: "execute", prompt: "Initial queued input" }, inputId);
 expect(sessionProgress(observation({ outcome: "not-started", dispatch: "ready", active_execution_id: undefined, execution: undefined, preparation: { job_id: newRequestId(), state: "pending" } }))).toBe(SessionProgressPhase.Preparing);
 expect(sessionProgress(observation({ outcome: "not-started", dispatch: "ready", active_execution_id: undefined, execution: undefined, pending_inputs: 1 }, { queue: [queued] }))).toBe(SessionProgressPhase.Queued);
 expect(sessionProgress(observation({ outcome: "not-started", execution: undefined }))).toBe(SessionProgressPhase.Starting);
 expect(sessionProgress(observation({ outcome: "not-started", execution: { ...progress, outcome: "not-started", native_turn_id: undefined, accepted_inputs: undefined, last_sequence: 1 } }))).toBe(SessionProgressPhase.Starting);
 expect(sessionProgress(observation())).toBe(SessionProgressPhase.Response);
});
it.each([{ dispatch: "paused" },{ dispatch: "blocked" },{ archive: "archiving" },{ archive: "archived" },{ recovery: "required" },{ recovery: "reconciling" },{ problem: {} },{ startup_rejection: {} },{ startup: { failure: {} } },{ outcome: "failed" },{ outcome: "stopped" },{ outcome: "succeeded" },{ execution: { ...progress, outcome: "failed" } },{ execution: { ...progress, waiting: { approval: true, user_input: false } } },{ execution: { ...progress, waiting: { approval: false, user_input: true } } },{ execution: { ...progress, waiting: {} } },{ execution: { ...progress, unconfirmed_responses: 1 } },{ execution: { ...progress, claude_interruption: {} } },{ execution: { ...progress, execution_id: newRequestId() } },{ execution: { ...progress, input_id: newRequestId() } },{ execution: { ...progress, accepted_inputs: [{ input_id: newRequestId(), prompt_digest: "a".repeat(64) }] } },{ execution: "malformed" }])("suppresses blocked, terminal, uncertain, waiting and foreign metadata %j", extra => { expect(sessionProgress(observation(extra))).toBeUndefined(); });
it.each([{ current: false },{ complete: false },{ blocked: true }])("does not infer live progress from unavailable observation %j", option => { expect(sessionProgress(observation({}, option))).toBeUndefined(); });
it("requires original queued input and complete current queue evidence", () => {
 const data = { outcome: "not-started", dispatch: "ready", active_execution_id: undefined, execution: undefined, pending_inputs: 1 };
 expect(sessionProgress(observation(data))).toBeUndefined();
 expect(sessionProgress(observation(data, { queue: [resource(EntityKind.QUEUE, { delivery: "uncertain" })] }))).toBeUndefined();
 expect(sessionProgress(observation(data, { queueCurrent: false, queue: [resource(EntityKind.QUEUE, { delivery: "queued", sequence: 1, content_revision: 1, mode: "execute", prompt: "Initial queued input" })] }))).toBeUndefined();
});
it("retains response waiting for the original initial user and an empty assistant start only", () => {
 const user = conversationProjection(message(), sessionId);
 expect(sessionProgress(observation({}, { messages: [user] }))).toBe(SessionProgressPhase.Response);
 const empty = conversationProjection(message({ role: "assistant", input_id: undefined, text: "", state: "streaming" }), sessionId);
 expect(sessionProgress(observation({}, { messages: [user, empty] }))).toBe(SessionProgressPhase.Response);
 for (const changes of [{ role: "assistant", text: "Actual response" },{ role: "tool", tool: { started: { kind: "observed" } } },{ role: "progress", progress: { kind: "observed" } },{ role: "assistant", claude: {} },{ role: "user", input_id: newRequestId() },{ execution_id: newRequestId() },{ native_turn_id: "old-turn" },{ last_sequence: 7 },{ first_sequence: "bad" },{ inherited: {} }]) {
  expect(sessionProgress(observation({}, { messages: [conversationProjection(message(changes), sessionId)] }))).toBeUndefined();
 }
});
it("preserves bounded page evidence through eviction and updates it with exact newer live revisions", () => {
 const first = message({ role: "assistant", text: "Private response output" });
 const projected = conversationProjection(first, sessionId);
 expect(JSON.stringify(projected, (_, v) => typeof v === "bigint" ? String(v) : v)).not.toContain("Private response output");
 expect(projected.response?.kind).toBe(ResponseEvidenceKind.Visible);
 const stale = message({ role: "user" }, 0n, first.id);
 expect(progressMessages([projected], new Map([[first.id, stale]]), new Set(), sessionId)[0]).toBe(projected);
 expect(sessionProgress(observation({}, { messages: [projected] }))).toBeUndefined();
 const updated = message({ role: "assistant", text: "Changed response" }, 2n, first.id);
 expect(progressMessages([projected], new Map([[first.id, updated]]), new Set(), sessionId)[0].revision).toBe(2n);
 expect(progressMessages([projected], new Map([[first.id, updated]]), new Set([first.id]), sessionId)).toEqual([]);
 expect(responseEvidence({ ...first, sessionId: newRequestId() }, sessionId)).toBeUndefined();
});
it("keeps one polite status instance through repeated observations and locale changes", async () => {
 const view = render(<SessionProgressStatus phase={SessionProgressPhase.Response} compact={false} />);
 const status = screen.getByRole("status"); expect(status.textContent).toBe("Waiting for response");
 view.rerender(<SessionProgressStatus phase={SessionProgressPhase.Response} compact={true} />); expect(screen.getByRole("status")).toBe(status);
 await act(() => i18n.changeLanguage("ko")); expect(screen.getByRole("status")).toBe(status); expect(status.textContent).toBe("응답 대기 중");
 await act(() => i18n.changeLanguage("en"));
});

it("retains same-owner suppression for malformed response evidence without a sequence", () => {
 const owner = progressResponseOwner(observation().session, sessionId);
 const malformed = conversationProjection(message({ role: "assistant", first_sequence: "unknown" }), sessionId);
 expect(responseSuppressesProgress([malformed], owner)).toBe(true);
 expect(responseSuppressesProgress([conversationProjection(message({ execution_id: newRequestId(), first_sequence: "unknown" }), sessionId)], owner)).toBe(false);
});

it("validates READY metadata without treating it as input acceptance", () => {
 const ready = { state: 1, phase: 4, harness: "codex", protocol: "codex-app-server-v2", executable_sha256: "a".repeat(64), correlation_id: jobId, input_delivery: 1 };
 const startup = { job_id: jobId, execution_id: executionId, ready };
 expect(sessionProgress(observation({ startup, execution: undefined }))).toBe(SessionProgressPhase.Starting);
 expect(sessionProgress(observation({ startup }))).toBe(SessionProgressPhase.Response);
 expect(sessionProgress(observation({ startup: { ...startup, ready: { ...ready, state: 3 } } }))).toBeUndefined();
 expect(sessionProgress(observation({ execution: undefined, startup: { ...startup, execution_id: newRequestId() } }))).toBeUndefined();
});

const initialProblem = { code: "unavailable", message: "The first execution is waiting for its workspace, Runner Device or account.", guidance: "Prepare the workspace, connect the selected Runner Device and validate the selected account. Inspect the retained session for the current blocking reason." };
const originalInitial = { outcome: "not-started", dispatch: "blocked", problem: initialProblem, active_execution_id: undefined, initial_execution: undefined, execution: undefined, pending_inputs: 1, last_input_sequence: 1, preparation: { job_id: jobId, state: "pending" } };
const firstQueue = resource(EntityKind.QUEUE, { delivery: "queued", sequence: 1, content_revision: 1, mode: "execute", prompt: "Initial queued input" }, inputId);
it("projects the exact server sentinel through original preparation and ready queue", () => {
 expect(initialExecutionPending(initialProblem)).toBe(true);
 expect(sessionProgress(observation(originalInitial, { queue: [firstQueue] }))).toBe(SessionProgressPhase.Preparing);
 expect(sessionProgress(observation({ ...originalInitial, preparation: { job_id: jobId, state: "ready" } }, { queue: [firstQueue] }))).toBe(SessionProgressPhase.Queued);
});
it.each([{ problem: { ...initialProblem, message: "Runner Device is disconnected." } }, { problem: { ...initialProblem, code: "internal" } }, { problem: { ...initialProblem, guidance: "Changed guidance" } }, { problem: { ...initialProblem, cause: "Unknown" } }, { dispatch: "paused" }, { outcome: "stopped" }, { archive: "archived" }, { recovery: "required" }, { initial_execution: { id: executionId, input_id: inputId } }, { current_execution: { id: executionId, input_id: inputId } }, { startup: {} }, { startup_rejection: {} }, { last_input_sequence: 2 }, { pending_inputs: 2 }, { preparation: { job_id: jobId, state: "failed" } }, { execution_recovery_job_id: jobId }])("retains initial problem precedence for incompatible proof %j", extra => {
 expect(sessionProgress(observation({ ...originalInitial, ...extra }, { queue: [firstQueue] }))).toBeUndefined();
});
it.each([{ current: false }, { complete: false }, { blocked: true }, { queueCurrent: false }, { queue: [] }, { messages: [{ id: inputId, revision: 1n }] }])("suppresses initial readiness without complete current evidence", extra => {
 expect(sessionProgress(observation(originalInitial, { queue: [firstQueue], ...extra }))).toBeUndefined();
});
it.each([{ sequence: 2 }, { delivery: "uncertain" }, { execution_id: executionId }, { native_request_id: inputId }, { mode: "unknown" }])("requires untouched original initial queue proof %j", extra => {
 const queue = resource(EntityKind.QUEUE, { delivery: "queued", sequence: 1, content_revision: 1, mode: "execute", prompt: "Initial queued input", ...extra }, inputId);
 expect(sessionProgress(observation(originalInitial, { queue: [queue] }))).toBeUndefined();
});
