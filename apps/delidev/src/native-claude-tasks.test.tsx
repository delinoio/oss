import { render, screen } from "@testing-library/react";
import { expect, test } from "vitest";
import { newRequestId } from "@delinoio/delidev-api-client";
import { NativeClaudeProgress, NativeClaudePermissionProgress } from "./native-claude-progress";
import { object, type Document } from "./documents";

function fixture(task: Document = { kind: "task_started", task_id: "original-task", task_type: "local_bash", description: "Original Bash task", tool: { id: newRequestId(), native_id: "original-tool", name: "Bash" } }): Document {
  const native = newRequestId();
  return {
    execution_id: newRequestId(), native_thread_id: newRequestId(), native_turn_id: newRequestId(), native_id: native,
    role: "progress", state: "complete", text: "", first_sequence: 11, last_sequence: 11,
    claude_progress: { native_event_id: native, kind: "task-lifecycle", input_accepted: true, status: null, thinking: null, task },
  };
}

test("preserves unreported background mode separately from explicit false without native actions", () => {
  const data = fixture();
  const { rerender } = render(<NativeClaudeProgress data={data} />);
  expect(screen.getByText("Task started")).toBeTruthy();
  expect(screen.getAllByText("Not reported")).toHaveLength(3);
  object(object(data.claude_progress).task).is_backgrounded = false;
  rerender(<NativeClaudeProgress data={data} />);
  expect(screen.getByText("False")).toBeTruthy();
  expect(screen.queryByRole("button")).toBeNull();
});

test("renders original task counters exactly without deriving usage or executing output paths", () => {
  const data = fixture({ kind: "task_notification", task_id: "original-task", status: "stopped", reason: "worker_restart", output_file: "file:///private/original-output", summary: "<script>run()</script>", skip_transcript: false, usage: { total_tokens: "18446744073709551615", tool_uses: "9007199254740993", duration_ms: "0" } });
  const { container } = render(<NativeClaudeProgress data={data} />);
  expect(screen.getByText("18446744073709551615")).toBeTruthy();
  expect(screen.getByText("9007199254740993")).toBeTruthy();
  expect(screen.getByText("<script>run()</script>")).toBeTruthy();
  expect(screen.getByText("file:///private/original-output")).toBeTruthy();
  expect(screen.getByText(/can overlap provider usage/)).toBeTruthy();
  expect(container.querySelector("script, a, button")).toBeNull();
});

test("preserves empty snapshots independently of lifecycle completion", () => {
  render(<NativeClaudeProgress data={fixture({ kind: "background_tasks_changed", background: [] })} />);
  expect(screen.getByText("No background tasks were listed.")).toBeTruthy();
  expect(screen.getByText(/does not complete or stop previously observed tasks/)).toBeTruthy();
});

test("shows exact task patch fields and preserves empty description and diagnostic", () => {
  render(<NativeClaudeProgress data={fixture({ kind: "task_updated", task_id: "original-task", patch: { status: "completed", end_time: "18446744073709551615", total_paused_ms: "0", description: "", error: "", is_backgrounded: false } })} />);
  expect(screen.getByText("completed")).toBeTruthy();
  expect(screen.getByText("18446744073709551615")).toBeTruthy();
  expect(screen.getByText("Task description")).toBeTruthy();
  expect(screen.getByText("Task diagnostic")).toBeTruthy();
});

test.each(["mixed", "unknown", "type", "name", "null-mode", "task-id", "missing-description", "counter", "overflow", "terminal", "null-patch", "patch-status", "patch-null", "null-snapshot", "duplicate-snapshot", "reason", "early"])("rejects malformed or mixed task observations: %s", (change) => {
  const data = fixture(), v = object(data.claude_progress), task = object(v.task);
  if (change === "mixed") v.tool_summary = { summary: "mixed", preceding_tools: [task.tool] };
  if (change === "unknown") task.prompt = "unsupported";
  if (change === "type") task.task_type = "local_agent";
  if (change === "name") object(task.tool).name = "Read";
  if (change === "null-mode") task.is_backgrounded = null;
  if (change === "task-id") task.task_id = "";
  if (change === "missing-description") delete task.description;
  if (change === "counter" || change === "overflow") v.task = { kind: "task_progress", task_id: "original-task", description: "", usage: { total_tokens: change === "counter" ? 9007199254740992 : "18446744073709551616", tool_uses: "0", duration_ms: "0" } };
  if (change === "terminal" || change === "reason") v.task = { kind: "task_notification", task_id: "original-task", output_file: "", summary: "", status: change === "terminal" ? "running" : "completed", reason: "worker_restart" };
  if (change === "null-patch" || change === "patch-status" || change === "patch-null") v.task = { kind: "task_updated", task_id: "original-task", patch: change === "null-patch" ? null : change === "patch-null" ? { error: null } : { status: "stopped" } };
  if (change === "null-snapshot" || change === "duplicate-snapshot") v.task = { kind: "background_tasks_changed", background: change === "null-snapshot" ? null : [{ task_id: "same", task_type: "local_bash", description: "" }, { task_id: "same", task_type: "local_bash", description: "" }] };
  if (change === "early") v.input_accepted = false;
  render(<NativeClaudeProgress data={data} />);
  expect(screen.getByRole("article", { name: "Claude progress unavailable" })).toBeTruthy();
});

test("task-only progress preserves unknown permission mode", () => {
  render(<NativeClaudePermissionProgress progress={{ claude_progress: { native_turn_id: newRequestId(), latest_task_id: newRequestId() } }} />);
  expect(screen.getByText("Not reported")).toBeTruthy();
});
