import { render, screen } from "@testing-library/react";
import { expect, test } from "vitest";
import { newRequestId } from "@delinoio/delidev-api-client";
import { NativeClaudeInterruption } from "./native-claude-interruption";
import { object, type Document } from "./documents";

function fixture(result = false): Document {
  const native = newRequestId();
  return {
    execution_id: newRequestId(), native_thread_id: newRequestId(), native_turn_id: newRequestId(), native_id: native,
    role: "progress", state: "complete", text: "", first_sequence: 50, last_sequence: 50,
    claude_interruption: {
      kind: result ? "denial-session-result" : "denial-context", native_event_id: native,
      interaction_id: newRequestId(), arrival_id: newRequestId(), tool_message_id: newRequestId(), tool_result_native_id: newRequestId(),
      context: result ? null : "[Request interrupted by user for tool use]",
      result: result ? { kind: "error_during_execution", reason: "aborted_tools", is_error: true, native_input_id: null, usage: { main_loop_turn: { input_tokens: "9007199254740993", output_tokens: null }, native_cumulative_models: {}, native_cumulative_cost_usd: "0.0000010" } } : null,
    },
  };
}

test("displays independent original denial context without input controls", () => {
  render(<NativeClaudeInterruption data={fixture()} />);
  expect(screen.getByText("[Request interrupted by user for tool use]")).toBeTruthy();
  expect(screen.getByRole("article", { name: "Claude interruption observation" })).toBeTruthy();
  expect(screen.queryByRole("button")).toBeNull();
});

test("keeps uncorrelated session result and exact usage separate from input completion", () => {
  render(<NativeClaudeInterruption data={fixture(true)} />);
  expect(screen.getByText(/did not report an input result identity/)).toBeTruthy();
  expect(screen.getByText(/does not establish input completion or process cleanup/)).toBeTruthy();
  expect(screen.getByText("9007199254740993")).toBeTruthy();
  expect(screen.getByText("0.0000010")).toBeTruthy();
  expect(screen.queryByText("Original input result")).toBeNull();
});

test.each(["input", "missing-input-absence", "false-error", "success", "wrong-reason", "foreign-native", "reused-native", "tool", "partial", "unknown-field", "bad-usage"])("rejects inconsistent original interruption: %s", (change) => {
  const data = fixture(true), v = object(data.claude_interruption), r = object(v.result);
  if (change === "input") r.native_input_id = newRequestId();
  if (change === "missing-input-absence") delete r.native_input_id;
  if (change === "false-error") r.is_error = false;
  if (change === "success") r.kind = "success";
  if (change === "wrong-reason") r.reason = "completed";
  if (change === "foreign-native") data.native_id = newRequestId();
  if (change === "reused-native") v.tool_result_native_id = v.native_event_id;
  if (change === "tool") data.claude_tool = {};
  if (change === "partial") data.state = "streaming";
  if (change === "unknown-field") v.unknown = true;
  if (change === "bad-usage") r.usage = { main_loop_turn: { input_tokens: 9007199254740992 } };
  render(<NativeClaudeInterruption data={data} />);
  expect(screen.getByRole("article", { name: "Claude interruption unavailable" })).toBeTruthy();
  expect(screen.queryByText(/Claude stopped after/)).toBeNull();
});
