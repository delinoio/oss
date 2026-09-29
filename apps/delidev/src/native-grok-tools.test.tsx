import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { newRequestId } from "@delinoio/delidev-api-client";
import { NativeGrokPublicTerminal, NativeGrokTool } from "./native-grok-tools";
import { type Document } from "./documents";

const thread = newRequestId();
function toolFixture(): Document {
  const g = { name: "write", path: " inert path\n", content: " Original contents\n" };
  const metadata = (index: number) => ({ event_id: `${thread}-${index}`, context_tokens: "9007199254740993", timestamp_ms: "1", stream_start_ms: "1", turn_start_ms: "1" });
  return { started: { kind: "grok-native", status: "pending", changes: null, grok: { name: "write", phase: "arguments", arguments: { index: 0, id: "tool", name: "write", text: '{"original":' } } }, states: [{ sequence: 4, snapshot: { kind: "grok-native", status: "pending", grok: { ...g, phase: "declared", metadata: metadata(1) } } }, { sequence: 5, snapshot: { kind: "grok-native", status: "pending", grok: { ...g, phase: "described", metadata: metadata(2) } } }], completed: { kind: "grok-native", status: "completed", grok: { ...g, phase: "completed", metadata: metadata(3), output: " Exact output\n", old: "" } }, output: null };
}
it("keeps original tool arguments, lifecycle, prior content and precise context inspectable", () => {
  render(<NativeGrokTool tool={toolFixture()} state="complete" thread={thread} />);
  expect(screen.getByText("Grok write · completed")).toBeTruthy();
  expect(screen.getByText('{"original":')).toBeTruthy();
  expect(screen.getAllByText(/context: 9007199254740993/)).toHaveLength(3);
  expect(screen.getByText("Original prior content")).toBeTruthy();
  expect(screen.queryByRole("link")).toBeNull();
});
it.each(["order", "input", "foreign", "status", "rounded", "closure"])("rejects contradictory retained tool evidence: %s", (change) => {
  const tool = toolFixture();
  const states = tool.states as { sequence: number; snapshot: Document }[];
  if (change === "order") states[1].sequence = 4;
  if (change === "input") (states[1].snapshot.grok as Document).path = "Replacement";
  if (change === "foreign") states[1].snapshot.read = {};
  if (change === "status") (tool.completed as Document).status = "pending";
  if (change === "rounded") ((tool.completed as Document).grok as Document).metadata = { event_id: `${thread}-3`, context_tokens: 9007199254740993, timestamp_ms: "1", stream_start_ms: "1", turn_start_ms: "1" };
  render(<NativeGrokTool tool={tool} state={change === "closure" ? "streaming" : "complete"} thread={thread} />);
  expect(screen.getByText(/unavailable or inconsistent/)).toBeTruthy();
  expect(screen.queryByText(/Exact output/)).toBeNull();
});
function terminalFixture(): Document {
  return { execution_id: newRequestId(), native_thread_id: thread, native_turn_id: "00000000-0000-4000-8000-000000000001", outcome: "succeeded", observed: { model: "Original model", grok_mode: "default" }, grok_mode: { mode: "plan" }, grok_content: { responses: 2 }, grok_public_terminal: { input_request_id: newRequestId(), native_event_id: `${thread}-20`, outcome: "succeeded", permission_rejected: false, idle: true, cleanup_joined: true, mode: "plan", model: "Original model", responses: "2", text_chunks: 0, timestamp_ms: "1", total_tokens: "9007199254740993", api_duration_ms: "1", turns: "1", input_digest: "ab".repeat(32), output_digest: "cd".repeat(32), native_facts_digest: "ef".repeat(32), counts: { input_tokens: "1", output_tokens: "1", cache_read_input_tokens: "0", cache_creation_input_tokens: "0", reasoning_tokens: "0" } } };
}
it("keeps native rich completion distinct from workspace cleanup and continuation", () => {
  const progress = terminalFixture(); const view = render(<NativeGrokPublicTerminal progress={progress} />);
  expect(screen.getByText("9007199254740993")).toBeTruthy();
  expect(screen.getByText("Awaiting original Worker report")).toBeTruthy();
  view.rerender(<NativeGrokPublicTerminal progress={{ ...progress, cleanup_verified: true }} />);
  expect(screen.getByText("Verified")).toBeTruthy();
  expect(screen.queryByRole("button")).toBeNull();
});
it.each(["mixed", "mode", "event", "responses", "cleanup"])("keeps malformed rich terminal unavailable: %s", (change) => {
  const progress = terminalFixture(), v = progress.grok_public_terminal as Document;
  if (change === "mixed") progress.grok_terminal = {};
  if (change === "mode") v.mode = "default";
  if (change === "event") v.native_event_id = `${thread}-01`;
  if (change === "responses") v.responses = "3";
  if (change === "cleanup") v.cleanup_joined = false;
  render(<NativeGrokPublicTerminal progress={progress} />);
  expect(screen.getByText(/unavailable or inconsistent/)).toBeTruthy();
});
