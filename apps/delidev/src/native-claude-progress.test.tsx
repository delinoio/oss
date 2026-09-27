import { render, screen } from "@testing-library/react";
import { expect, test } from "vitest";
import { newRequestId } from "@delinoio/delidev-api-client";
import { NativeClaudeProgress } from "./native-claude-progress";
import { object, type Document } from "./documents";

function fixture(thinking = false): Document {
  const native = newRequestId();
  return {
    execution_id: newRequestId(), native_thread_id: newRequestId(), native_turn_id: newRequestId(), native_id: native,
    role: "progress", state: "complete", text: "", first_sequence: 2, last_sequence: 2,
    claude_progress: {
      native_event_id: native, kind: thinking ? "thinking-tokens-estimated" : "session-status", input_accepted: thinking,
      status: thinking ? null : { status: "requesting", permission: null, compact_result: null, compact_error: null },
      thinking: thinking ? { estimated_tokens: "18446744073709551615", estimated_tokens_delta: "9007199254740993" } : null,
    },
  };
}

test("keeps original pre-acceptance status separate from input and root outcome", () => {
  render(<NativeClaudeProgress data={fixture()} />);
  expect(screen.getByText("Before input acceptance")).toBeTruthy();
  expect(screen.getByText("Requesting")).toBeTruthy();
  expect(screen.getByText(/does not confirm input acceptance, idle state or execution completion/)).toBeTruthy();
  expect(screen.queryByRole("button")).toBeNull();
});

test("retains cleared status, native mode and compaction failure independently as inert text", () => {
  const data = fixture();
  object(data.claude_progress).status = { status: null, permission: "plan", compact_result: "failed", compact_error: "<script>private()</script>" };
  const { container } = render(<NativeClaudeProgress data={data} />);
  expect(screen.getByText("Status cleared")).toBeTruthy();
  expect(screen.getByText("plan")).toBeTruthy();
  expect(screen.getByText("Failed")).toBeTruthy();
  expect(screen.getByText("<script>private()</script>")).toBeTruthy();
  expect(container.querySelector("script")).toBeNull();
});

test("preserves exact uint64 thinking estimates independently of usage", () => {
  render(<NativeClaudeProgress data={fixture(true)} />);
  expect(screen.getByText("18446744073709551615")).toBeTruthy();
  expect(screen.getByText("9007199254740993")).toBeTruthy();
  expect(screen.getByText(/separate from provider usage and billed cost/)).toBeTruthy();
});

test.each(["overflow", "negative", "rounded", "missing", "early-thinking", "input", "mixed", "foreign", "init-identity", "unknown", "mode", "partial", "missing-null", "oversized-error"])("rejects inconsistent progress: %s", (change) => {
  const data = fixture(["overflow", "negative", "rounded", "missing", "early-thinking"].includes(change));
  const v = object(data.claude_progress), t = object(v.thinking), s = object(v.status);
  if (change === "overflow") t.estimated_tokens = "18446744073709551616";
  if (change === "negative") t.estimated_tokens_delta = "-1";
  if (change === "rounded") t.estimated_tokens = 9007199254740992;
  if (change === "missing") delete t.estimated_tokens_delta;
  if (change === "early-thinking") v.input_accepted = false;
  if (change === "input") data.input_id = newRequestId();
  if (change === "mixed") data.claude_interruption = {};
  if (change === "foreign") data.native_id = newRequestId();
  if (change === "init-identity") data.native_turn_id = data.native_id;
  if (change === "unknown") s.future = true;
  if (change === "mode") s.permission = "unknown";
  if (change === "partial") data.state = "streaming";
  if (change === "missing-null") delete s.compact_result;
  if (change === "oversized-error") s.compact_error = "x".repeat((256 << 10) + 1);
  render(<NativeClaudeProgress data={data} />);
  expect(screen.getByRole("article", { name: "Claude progress unavailable" })).toBeTruthy();
});
