// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { expect, test } from "vitest";
import { EntityKind, ResourceSchema, newRequestId } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { nativeShellEligible, ownedNativeShellJob, verifiedNativeShellRun } from "./native-shell-action";
const sessionId = newRequestId(), requestId = newRequestId();
const input = { action_id: newRequestId(), assignment: { session_id: sessionId }, shell: { command: "  printf hello  ", full_access_confirmed: true } };
const job = () => create(ResourceSchema, { id: requestId, kind: EntityKind.JOB, schemaVersion: 1, revision: 1n, sessionId, documentJson: encode({ type: "native-shell", state: "queued", input }) });

test("read recovery verifies the original request, session and exact unchanged command", () => {
  expect(verifiedNativeShellRun(job(), sessionId, requestId, input.shell.command)).toBe(true);
  expect(verifiedNativeShellRun(job(), newRequestId(), requestId, input.shell.command)).toBe(false);
  expect(verifiedNativeShellRun(job(), sessionId, newRequestId(), input.shell.command)).toBe(false);
  expect(verifiedNativeShellRun(job(), sessionId, requestId, input.shell.command.trim())).toBe(false);
  expect(verifiedNativeShellRun(job(), sessionId, requestId, input.shell.command, 30000)).toBe(false);
  expect(ownedNativeShellJob(create(ResourceSchema, { ...job(), schemaVersion: 2 }), sessionId)).toBe(false);
});

test("Shell eligibility rejects Sidechat, active work and unverified cleanup", () => {
  const base = { initial_execution: { configuration: { harness: "codex" } }, execution: { native_thread_id: "original-thread", cleanup_verified: true }, archive: "active", recovery: "none" };
  const resource = (data: unknown) => create(ResourceSchema, { id: sessionId, kind: EntityKind.SESSION, schemaVersion: 1, revision: 1n, documentJson: encode(data) });
  expect(nativeShellEligible(resource(base))).toBe(true);
  for (const replacement of [{ ...base, fork: { sidechat_parent_snapshot: true } }, { ...base, active_execution_id: newRequestId() }, { ...base, execution: { cleanup_verified: false } }, { ...base, execution: { cleanup_verified: true, subagents: { original: true } } }, { ...base, initial_execution: { configuration: { harness: "claude" } } }, { ...base, archive: "archived" }, { ...base, recovery: "required" }]) expect(nativeShellEligible(resource(replacement))).toBe(false);
});
