import { render, screen } from "@testing-library/react";
import { expect, test } from "vitest";
import { newRequestId } from "@delinoio/delidev-api-client";
import { NativeClaudeProgress, NativeClaudePermissionProgress } from "./native-claude-progress";
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

function retryFixture(accepted = false): Document {
  const data = fixture();
  const v = object(data.claude_progress);
  v.kind = "api-retry"; v.status = null; v.input_accepted = accepted;
  v.api_retry = { native_event_id: v.native_event_id, attempt: "9007199254740993", max_retries: "18446744073709551615", retry_delay_ms: "0", error_status: null, error: "unknown" };
  return data;
}

test.each([false, true])("retains exact retry values with independent original input acceptance: %s", (accepted) => {
  render(<NativeClaudeProgress data={retryFixture(accepted)} />);
  expect(screen.getByText("9007199254740993")).toBeTruthy();
  expect(screen.getByText("18446744073709551615")).toBeTruthy();
  expect(screen.getByText("0")).toBeTruthy();
  expect(screen.getByText("Not reported")).toBeTruthy();
  expect(screen.getByText(accepted ? "After input acceptance" : "Before input acceptance")).toBeTruthy();
  expect(screen.getByText(/does not confirm another request/)).toBeTruthy();
  expect(screen.queryByRole("button")).toBeNull();
});

test.each(["missing", "foreign", "status", "thinking", "overflow", "number", "missing-null", "http", "error", "extra", "wrong-kind"])("rejects malformed or mixed retry: %s", (change) => {
  const data = retryFixture(), v = object(data.claude_progress), r = object(v.api_retry);
  if (change === "missing") delete v.api_retry;
  if (change === "foreign") r.native_event_id = newRequestId();
  if (change === "status") v.status = { status: "requesting", permission: null, compact_result: null, compact_error: null };
  if (change === "thinking") v.thinking = { estimated_tokens: "0", estimated_tokens_delta: "0" };
  if (change === "overflow") r.attempt = "18446744073709551616";
  if (change === "number") r.max_retries = 9007199254740992;
  if (change === "missing-null") delete r.error_status;
  if (change === "http") r.error_status = 200;
  if (change === "error") r.error = "future";
  if (change === "extra") r.detail = "private";
  if (change === "wrong-kind") v.kind = "session-status";
  render(<NativeClaudeProgress data={data} />);
  expect(screen.getByRole("article", { name: "Claude progress unavailable" })).toBeTruthy();
});

test("retry-only progress does not infer permission or erase an earlier permission transition", () => {
  const state = { native_turn_id: newRequestId(), latest_retry_id: newRequestId() };
  const { rerender } = render(<NativeClaudePermissionProgress progress={{ claude_progress: state }} />);
  expect(screen.getByText("Not reported")).toBeTruthy();
  rerender(<NativeClaudePermissionProgress progress={{ claude_progress: { ...state, latest_status_id: newRequestId(), permission: "plan", permission_changed: true } }} />);
  expect(screen.getByText("plan")).toBeTruthy();
  expect(screen.getByText(/Further input requires configuration reconciliation/)).toBeTruthy();
});

function toolFixture(summary = false): Document {
  const data = fixture(true), v = object(data.claude_progress);
  v.kind = summary ? "tool-summary" : "tool-progress"; v.thinking = null;
  const ref = { id: newRequestId(), native_id: "tool_original", name: "Bash" };
  if (summary) v.tool_summary = { summary: "<script>original()</script>", preceding_tools: [ref] };
  else v.tool = { tool: ref, parent_tool_use_id: null, elapsed_time_seconds: "9.007199254740993e+15", heartbeat: false };
  return data;
}

test("retains original elapsed spelling and false heartbeat without tool actions", () => {
  render(<NativeClaudeProgress data={toolFixture()} />);
  expect(screen.getByText("9.007199254740993e+15")).toBeTruthy();
  expect(screen.getByText("Explicitly false")).toBeTruthy();
  expect(screen.getByText("Bash")).toBeTruthy();
  expect(screen.getByText(/does not confirm completion, approval/)).toBeTruthy();
  expect(screen.queryByRole("button")).toBeNull();
});

test("renders the original tool summary as inert advisory text", () => {
  const { container } = render(<NativeClaudeProgress data={toolFixture(true)} />);
  expect(screen.getByText("<script>original()</script>")).toBeTruthy();
  expect(container.querySelector("script")).toBeNull();
  expect(screen.getByText(/summary does not confirm tool completion/)).toBeTruthy();
  expect(screen.queryByRole("button")).toBeNull();
});

test.each(["number", "negative", "infinity", "space", "hex", "missing-null", "missing-heartbeat", "parent", "foreign-id", "name", "early", "mixed", "summary-mixed", "summary-duplicate", "summary-native-duplicate", "summary-empty", "summary-overflow"])("rejects malformed or mixed tool progress: %s", (change) => {
  const data = toolFixture(change.startsWith("summary")), v = object(data.claude_progress), p = object(v.tool), s = object(v.tool_summary);
  if (change === "number") p.elapsed_time_seconds = 9007199254740992;
  if (change === "negative") p.elapsed_time_seconds = "-1";
  if (change === "infinity") p.elapsed_time_seconds = "1e999";
  if (change === "space") p.elapsed_time_seconds = " 1";
  if (change === "hex") p.elapsed_time_seconds = "0xff";
  if (change === "missing-null") delete p.parent_tool_use_id;
  if (change === "missing-heartbeat") delete p.heartbeat;
  if (change === "parent") p.parent_tool_use_id = "unproved-parent";
  if (change === "foreign-id") object(p.tool).id = "foreign";
  if (change === "name") object(p.tool).name = "";
  if (change === "early") v.input_accepted = false;
  if (change === "mixed") v.api_retry = {};
  if (change === "summary-mixed") v.tool = {};
  if (change === "summary-duplicate") s.preceding_tools = [(s.preceding_tools as Document[])[0], (s.preceding_tools as Document[])[0]];
  if (change === "summary-native-duplicate") s.preceding_tools = [(s.preceding_tools as Document[])[0], { ...(s.preceding_tools as Document[])[0], id: newRequestId() }];
  if (change === "summary-empty") s.preceding_tools = [];
  if (change === "summary-overflow") s.summary = "x".repeat((256 << 10) + 1);
  render(<NativeClaudeProgress data={data} />);
  expect(screen.getByRole("article", { name: "Claude progress unavailable" })).toBeTruthy();
});

test.each(["latest_tool_id", "latest_tool_summary_id"])("tool-only progress does not invent permission: %s", (key) => {
  render(<NativeClaudePermissionProgress progress={{ claude_progress: { native_turn_id: newRequestId(), [key]: newRequestId() } }} />);
  expect(screen.getByText("Not reported")).toBeTruthy();
});
