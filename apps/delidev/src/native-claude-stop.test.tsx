import { render, screen } from "@testing-library/react";
import { expect, test } from "vitest";
import { newRequestId } from "@delinoio/delidev-api-client";
import { NativeClaudeStop } from "./native-claude-stop";
import { NativeClaudeMessage } from "./native-claude-message";
import { object, type Document } from "./documents";

function fixture(retry = false): Document {
  const input = newRequestId();
  const v: Document = { request_id: newRequestId(), input_id: input, message_id: newRequestId(), native_message_id: "original_message", content_evidence: "aborted-assistant", interrupted_native_id: newRequestId(), context_native_id: newRequestId(), result_native_id: newRequestId(), command_native_id: newRequestId(), idle_native_id: newRequestId(), text: "<script>original partial</script>", context: "[Request interrupted by user]", native_input_id: null, kind: "error_during_execution", reason: "aborted_streaming", is_error: true, command: "cancelled", usage: { main_loop_turn: { input_tokens: "9007199254740993", output_tokens: null }, native_cumulative_models: {}, native_cumulative_cost_usd: "0.0000010" }, partial_usage: null, acknowledged: true, idle: true, cleanup_verified: true };
  if (retry) {
    delete v.interrupted_native_id;
    Object.assign(v, { content_evidence: "closed-stream-before-retry", block_stop_native_id: newRequestId(), message_stop_native_id: newRequestId(), retries: [{ native_event_id: newRequestId(), attempt: "1", max_retries: "9007199254740993", retry_delay_ms: "18446744073709551615", error_status: null, error: "unknown" }] });
  }
  return { execution_id: newRequestId(), input_id: input, native_thread_id: newRequestId(), native_turn_id: newRequestId(), outcome: "stopped", claude_stop: v };
}

test.each([false, true])("preserves native Stop and independent workspace report: retry=%s", (retry) => {
  const p = fixture(retry);
  const view = render(<NativeClaudeStop progress={p} />);
  expect(screen.getByRole("region", { name: "Claude original Stop" })).toBeTruthy();
  expect(screen.getByText(/without an input result identity/)).toBeTruthy();
  expect(screen.getByText("Not confirmed")).toBeTruthy();
  expect(screen.getAllByText("9007199254740993").length).toBeGreaterThan(0);
  if (retry) expect(screen.getByText(/18446744073709551615 ms/)).toBeTruthy();
  p.cleanup_verified = true;
  view.rerender(<NativeClaudeStop progress={p} />);
  expect(screen.queryByText("Not confirmed")).toBeNull();
  expect(screen.queryByRole("button")).toBeNull();
  expect(view.container.querySelector("script")).toBeNull();
});

test.each(["input", "missing-absence", "success", "cleanup", "workspace", "mixed", "reused", "missing-retry", "retry-overflow", "retry-status", "fake-assistant", "usage", "unknown"])("rejects inconsistent original Stop: %s", (change) => {
  const p = fixture(true), v = object(p.claude_stop);
  const retry = object((v.retries as Document[])[0]);
  if (change === "input") v.native_input_id = p.input_id;
  if (change === "missing-absence") delete v.native_input_id;
  if (change === "success") p.outcome = "succeeded";
  if (change === "cleanup") v.cleanup_verified = "true";
  if (change === "workspace") p.cleanup_verified = "true";
  if (change === "mixed") p.claude_terminal = {};
  if (change === "reused") retry.native_event_id = v.result_native_id;
  if (change === "missing-retry") delete v.retries;
  if (change === "retry-overflow") retry.attempt = "18446744073709551616";
  if (change === "retry-status") retry.error_status = 200;
  if (change === "fake-assistant") v.interrupted_native_id = newRequestId();
  if (change === "usage") v.partial_usage = {};
  if (change === "unknown") v.extra = "unknown";
  render(<NativeClaudeStop progress={p} />);
  expect(screen.getByText(/Stop evidence is unavailable/)).toBeTruthy();
  expect(screen.queryByText("Acknowledged")).toBeNull();
});

test.each(["aborted-assistant", "closed-stream-before-retry"])("keeps interrupted text inert and distinct from provider completion: %s", (evidence) => {
  const content: Document = { model: "fixture", blocks: [{ index: 0, block: { kind: "text", text: "<script>original partial</script>" }, state: "interrupted" }], stop_reason: null, stop_sequence: null, interruption: { request_id: newRequestId(), native_event_id: newRequestId(), evidence } };
  const view = render(<NativeClaudeMessage content={content} state="complete" />);
  expect(screen.getByText("Interrupted · partial response")).toBeTruthy();
  expect(screen.queryByText("Stream closed")).toBeNull();
  expect(view.container.querySelector("script")).toBeNull();
  delete content.interruption;
  view.rerender(<NativeClaudeMessage content={content} state="complete" />);
  expect(screen.getByLabelText("Claude message unavailable")).toBeTruthy();
});

 test("retains stopped output when native cleanup is unconfirmed", () => {
  const p = fixture(true); object(p.claude_stop).cleanup_verified = false;
  render(<NativeClaudeStop progress={p} />);
  expect(screen.getAllByText("Not confirmed")).toHaveLength(2);
  expect(screen.queryByText("Confirmed")).toBeNull();
 });
