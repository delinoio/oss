import { render, screen } from "@testing-library/react";
import { expect, test } from "vitest";
import { newRequestId } from "@delinoio/delidev-api-client";
import { NativeClaudeTerminal } from "./native-claude-terminal";
import { object, type Document } from "./documents";

function fixture(): Document {
  const input = newRequestId();
  return { execution_id: newRequestId(), input_id: input, native_thread_id: newRequestId(), native_turn_id: newRequestId(), outcome: "succeeded", claude_terminal: { input_id: input, result_native_id: newRequestId(), command_native_id: newRequestId(), idle_native_id: newRequestId(), kind: "success", reason: "completed", is_error: false, command: "completed" } };
}

test("shows original terminal and idle independently of later verified cleanup", () => {
  const p = fixture();
  const view = render(<NativeClaudeTerminal progress={p} />);
  expect(screen.getByText("Succeeded")).toBeTruthy();
  expect(screen.getByText("Idle observed")).toBeTruthy();
  expect(screen.getByText("Not confirmed")).toBeTruthy();
  expect(screen.getByText(/Session Stop or recovery remains separate/)).toBeTruthy();
  p.cleanup_verified = true;
  view.rerender(<NativeClaudeTerminal progress={p} />);
  expect(screen.getByText("Confirmed")).toBeTruthy();
  expect(screen.queryByRole("button")).toBeNull();
});

test("native success subtype with an API error never becomes successful input", () => {
  const p = fixture();
  p.outcome = "failed";
  Object.assign(object(p.claude_terminal), { reason: "api_error", is_error: true, command: "cancelled" });
  render(<NativeClaudeTerminal progress={p} />);
  expect(screen.getByText("Failed")).toBeTruthy();
  expect(screen.queryByText("Succeeded")).toBeNull();
});

test.each(["input", "reused", "turn", "outcome", "reason", "false-error", "command", "interruption", "cleanup", "missing"])("rejects inconsistent terminal evidence: %s", (scenario) => {
  const p = fixture(), v = object(p.claude_terminal);
  if (scenario === "input") v.input_id = newRequestId();
  if (scenario === "reused") v.idle_native_id = v.result_native_id;
  if (scenario === "turn") v.command_native_id = p.native_turn_id;
  if (scenario === "outcome") p.outcome = "running";
  if (scenario === "reason") v.reason = "unknown";
  if (scenario === "false-error") Object.assign(v, { reason: "api_error", command: "cancelled" });
  if (scenario === "command") v.command = "cancelled";
  if (scenario === "interruption") p.claude_interruption = {};
  if (scenario === "cleanup") p.cleanup_verified = "true";
  if (scenario === "missing") delete v.is_error;
  render(<NativeClaudeTerminal progress={p} />);
  expect(screen.getByText(/terminal evidence is unavailable/)).toBeTruthy();
  expect(screen.queryByText("Succeeded")).toBeNull();
});
