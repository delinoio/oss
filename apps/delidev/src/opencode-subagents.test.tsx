// SPDX-License-Identifier: Apache-2.0
import { render, screen } from "@testing-library/react";
import { newRequestId } from "@delinoio/delidev-api-client";
import { expect, test } from "vitest";
import { document, encode, items, object } from "./documents";
import { openCodeSubagentFixture } from "./subagent-test-fixture";
import { validateSubagentPage } from "./subagent-record";
import { SubagentRows } from "./subagents";

test("renders independently owned foreground child facts without child controls", () => {
  const session = newRequestId(), row = openCodeSubagentFixture(session);
  expect(validateSubagentPage([row], session)).toHaveLength(1);
  render(<SubagentRows rows={[row]} sessionId={session} />);
  expect(screen.getByText("Original foreground child output")).toBeTruthy();
  expect(screen.getByText("Input: 30")).toBeTruthy();
  expect(screen.queryAllByRole("button")).toHaveLength(0);
});

test.each(["valid", "missing-cleanup", "malformed-cleanup", "missing-interruption", "completed", "foreign-source"])("distinguishes joined scope cleanup from native child completion: %s", mode => {
  const session = newRequestId(), row = openCodeSubagentFixture(session), value = document(row), child = object(value.observation);
  const proof = { request_id: newRequestId(), input_request_id: newRequestId(), input_part_id: "prt_01960dcbe1fa1234567890ABCD", assistant_id: "msg_01960dcbe1faABCDEFGHIJKLMN", history_digest: "a".repeat(64), http_accepted: true, interrupted_observed: true, terminal_observed: true, idle_observed: true, pending_cleared: true, cleanup_verified: true };
  child.source = "opencode-child-scope-cleanup"; child.source_id = "original-cleanup"; child.status = "interrupted"; child.opencode_cleanup = proof;
  object(items(value.sources)[1]).source = child.source; object(items(value.sources)[1]).source_id = child.source_id;
  if (mode === "missing-cleanup") proof.cleanup_verified = false;
  if (mode === "malformed-cleanup") Object.assign(proof, { cleanup_verified: "true" });
  if (mode === "missing-interruption") proof.interrupted_observed = false;
  if (mode === "completed") child.status = "completed";
  if (mode === "foreign-source") { child.source = "opencode-child-history"; object(items(value.sources)[1]).source = child.source; }
  row.documentJson = encode(value);
  expect(validateSubagentPage([row], session)?.length).toBe(mode === "valid" || mode === "missing-cleanup" ? 1 : undefined);
});

test.each(["parent", "tool", "model", "missing-tool", "foreign-family", "missing-initial-task", "native-string-counter", "rounded-counter", "nested", "nonpartial"])("rejects the complete foreground child page: %s", mode => {
  const session = newRequestId(), row = openCodeSubagentFixture(session), value = document(row), child = object(value.observation);
  if (mode === "parent" || mode === "nested") child.parent_id = "ses_01960dcbe1fcABCDEFGHIJKLMN";
  if (mode === "tool") object(child.opencode_tool).part_id = "prt_01960dcbe1fb1234567890ABCD";
  if (mode === "model") child.observed_model = "foreign-model";
  if (mode === "missing-tool") delete child.opencode_tool;
  if (mode === "foreign-family") child.source = "claude-history";
  if (mode === "missing-initial-task") object(items(value.sources)[0]).source = "opencode-child-history";
  if (mode === "nonpartial") object(child.output).partial = false;
  if (mode === "native-string-counter" || mode === "rounded-counter") {
    const usage = object(child.usage);
    usage.native_report = String(usage.native_report).replace('"input":30', mode === "native-string-counter" ? '"input":"30"' : '"input":9007199254740992');
    object(items(value.sources)[1]).usage = usage;
  }
  row.documentJson = encode(value);
  expect(validateSubagentPage([openCodeSubagentFixture(session), row], session)).toBeUndefined();
});

test.each(["pending", "running", "completed", "failed"])("task-only foreground child requires pending status: %s", status => {
 const session = newRequestId(), row = openCodeSubagentFixture(session), value = document(row), child = object(value.observation);
 value.sources = [items(value.sources)[0]]; value.last_sequence = value.first_sequence;
 child.source = "opencode-task"; child.source_id = "original-task"; child.status = status;
 child.observed_model = null; child.output = null; child.usage = null;
 row.documentJson = encode(value);
 expect(validateSubagentPage([row], session)?.length).toBe(status === "pending" ? 1 : undefined);
});
