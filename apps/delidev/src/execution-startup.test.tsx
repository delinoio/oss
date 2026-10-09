// SPDX-License-Identifier: Apache-2.0
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { newRequestId } from "@delinoio/delidev-api-client";
import { expect, it, vi } from "vitest";
import { canRetryExecutionStartup, executionStartupFailure, ExecutionStartupDetails, startupCorrection } from "./execution-startup";
import type { Document } from "./documents";

function fixture(): Document {
  const execution = newRequestId(), job = newRequestId();
  return { archive: "active", dispatch: "paused", recovery: "none", pending_inputs: 0, pending_input_bytes: 0, initial_execution: { id: execution }, startup: { job_id: job, execution_id: execution, failure: { state: 2, phase: 1, harness: "codex", problem_code: "not_found", correlation_id: job, input_delivery: 1, cleanup: 1 } } };
}

it("offers explicit retry only for settled no-input failure", () => {
  const value = fixture();
  expect(canRetryExecutionStartup(value)).toBe(true);
  for (const change of [ { active_execution_id: newRequestId() }, { pending_inputs: 1 }, { recovery: "required" }, { archive: "archived" }, { dispatch: "claimed" } ]) expect(canRetryExecutionStartup({ ...value, ...change })).toBe(false);
  const record = value.startup as Document, failure = record.failure as Document;
  failure.state = 3; failure.input_delivery = 2;
  expect(executionStartupFailure(value)).toBe(failure);
  expect(canRetryExecutionStartup(value)).toBe(false);
  failure.state = 2;
  expect(executionStartupFailure(value)).toBeUndefined();
});

it.each([ { native_version: "secret/path" }, { native_version: "a".repeat(65) }, { raw_output: "secret" }, { phase: 99 }, { input_delivery: "1" }, { correlation_id: newRequestId() }, { executable_sha256: "A".repeat(64) }, { protocol: "grok-acp" } ])("rejects foreign or unbounded startup metadata %j", fault => {
  const value = fixture(), record = value.startup as Document;
  Object.assign(record.failure as Document, fault);
  expect(executionStartupFailure(value)).toBeUndefined();
  expect(canRetryExecutionStartup(value)).toBe(false);
});

it("copies only validated debugging metadata without another check", async () => {
  const value = fixture(), failure = executionStartupFailure(value)!;
  failure.native_version = "0.150.9";
  const writeText = vi.fn().mockResolvedValue(undefined);
  Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
  render(<ExecutionStartupDetails failure={failure} />);
  fireEvent.click(screen.getByText("Startup failure details"));
  expect(screen.getByText("0.150.9")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Copy debugging details" }));
  await waitFor(() => expect(screen.getByRole("status").textContent).toContain("copied"));
  expect(JSON.parse(writeText.mock.calls[0][0])).toEqual({ phase: 1, harness: "codex", native_version: "0.150.9", problem_code: "not_found", correlation_id: failure.correlation_id, input_delivery: 1, cleanup: 1 });
  expect(screen.queryByRole("button", { name: /inspect|check|retry/i })).toBeNull();
});

it("offers image guidance only for closed settled image provenance", () => {
 const value = fixture(), failure = (value.startup as Document).failure as Document;
 Object.assign(failure, { phase: 5, problem_code: "unsupported", failure_kind: 1 });
 expect(executionStartupFailure(value)).toBe(failure);
 expect(canRetryExecutionStartup(value)).toBe(true);
 expect(startupCorrection(failure)).toContain("image");
 for (const change of [{failure_kind: 2}, {failure_kind: "1"}, {phase: 4}, {harness: "claude"}, {problem_code: "unavailable"}, {state: 3}, {cleanup: 2}, {input_delivery: 2}]) {
  const changed = {...failure, ...change};
  expect(executionStartupFailure({...value, startup: {...value.startup as Document, failure: changed}})).toBeUndefined();
 }
 delete failure.failure_kind;
 expect(executionStartupFailure(value)).toBe(failure);
 expect(startupCorrection(failure)).not.toContain("image");
});
