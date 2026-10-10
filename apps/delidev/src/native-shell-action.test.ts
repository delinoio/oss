// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { expect, test } from "vitest";
import { EntityKind, ResourceSchema, newRequestId } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { nativeShellEligible, nativeShellSettled, ownedNativeShellJob, verifiedNativeShellRun } from "./native-shell-action";
const sessionId = newRequestId(), requestId = newRequestId();
const input = { action_id: newRequestId(), assignment: { session_id: sessionId }, shell: { command: "  printf hello  ", full_access_confirmed: true } };
const job = () => create(ResourceSchema, { id: requestId, kind: EntityKind.JOB, schemaVersion: 1, revision: 1n, sessionId, documentJson: encode({ type: "native-shell", state: "queued", input }) });

test("read recovery verifies the original request, session and exact unchanged command", () => {
  expect(verifiedNativeShellRun(job(), sessionId, requestId, input.shell.command)).toBe(true);
  expect(verifiedNativeShellRun(job(), newRequestId(), requestId, input.shell.command)).toBe(false);
  expect(verifiedNativeShellRun(job(), sessionId, newRequestId(), input.shell.command)).toBe(false);
  expect(verifiedNativeShellRun(job(), sessionId, requestId, input.shell.command.trim())).toBe(false);
  expect(verifiedNativeShellRun(job(), sessionId, requestId, input.shell.command, 30000n)).toBe(false);
  expect(ownedNativeShellJob(create(ResourceSchema, { ...job(), schemaVersion: 2 }), sessionId)).toBe(false);
});

test("Shell eligibility rejects Sidechat, active work and unverified cleanup", () => {
  const base = { initial_execution: { configuration: { harness: "codex" } }, execution: { native_thread_id: "original-thread", cleanup_verified: true }, archive: "active", recovery: "none" };
  const resource = (data: unknown) => create(ResourceSchema, { id: sessionId, kind: EntityKind.SESSION, schemaVersion: 1, revision: 1n, documentJson: encode(data) });
  expect(nativeShellEligible(resource(base))).toBe(true);
  for (const replacement of [{ ...base, fork: { sidechat_parent_snapshot: true } }, { ...base, active_execution_id: newRequestId() }, { ...base, execution: { cleanup_verified: false } }, { ...base, execution: { cleanup_verified: true, subagents: { original: true } } }, { ...base, initial_execution: { configuration: { harness: "claude" } } }, { ...base, archive: "archived" }, { ...base, recovery: "required" }]) expect(nativeShellEligible(resource(replacement))).toBe(false);
});

 test("explicit zero timeout stays separate from an absent timeout in original receipt", () => {
 const zero = create(ResourceSchema, { ...job(), documentJson: encode({ type: "native-shell", state: "queued", input: { ...input, shell: { ...input.shell, timeout_ms: 0 } } }) });
 expect(ownedNativeShellJob(zero, sessionId)).toBe(true);
 expect(verifiedNativeShellRun(zero, sessionId, requestId, input.shell.command, 0n)).toBe(true);
 expect(verifiedNativeShellRun(zero, sessionId, requestId, input.shell.command)).toBe(false);
 expect(verifiedNativeShellRun(job(), sessionId, requestId, input.shell.command, 0n)).toBe(false);
 });

test("original queued cancellation settles no-send lifetime without inventing native cleanup", () => {
 const canceled = create(ResourceSchema, { ...job(), documentJson: encode({ type: "native-shell", state: "canceled", input, finished_at: "2026-10-10T00:00:00Z", problem: { code: "canceled" } }) });
 expect(nativeShellSettled(canceled, sessionId)).toBe(true);
 expect(nativeShellSettled(canceled, newRequestId())).toBe(false);
 expect(nativeShellSettled(job(), sessionId)).toBe(false);
});
