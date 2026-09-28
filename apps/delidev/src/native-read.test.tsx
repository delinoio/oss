import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { NativeRead } from "./native-read";

function fixture() {
  return {
    output: null,
    started: { kind: "opencode-read", status: "pending", read: { call_id: "call-original", input: {}, raw: "" } },
    states: [{ sequence: 5, snapshot: { kind: "opencode-read", status: "running", read: { call_id: "call-original", input: { filePath: "/original/한글", offset: 0, limit: 1 }, time: { start: 100 } } } }],
    completed: { kind: "opencode-read", status: "completed", read: { call_id: "call-original", input: { filePath: "/original/한글", offset: 0, limit: 1 }, time: { start: 100, end: 200 }, title: "한글", output: " original\n<script>untrusted()</script>🙂 ", metadata: { preview: "preview", truncated: true, loaded: [] } } },
  };
}

it("shows original Read input and result as inert content with explicit native clipping", () => {
  const tool = fixture();
  const { container } = render(<NativeRead tool={tool} state="complete" />);
  expect(screen.getByText("Read · Completed")).toBeTruthy();
  expect(container.querySelector("details")?.open).toBe(false);
  expect(screen.getByLabelText("Read result").textContent).toBe(tool.completed.read.output);
  expect(screen.getByText("Requested offset").nextElementSibling?.textContent).toBe("0");
  expect(screen.getByText("The native Read result is truncated.")).toBeTruthy();
  expect(screen.getByText("Loaded instruction files: 0")).toBeTruthy();
  expect(container.querySelector("script")).toBeNull();
  expect(container.querySelector("a")).toBeNull();
});

it("keeps streaming proposals and applied input separate without an invented result", () => {
  const fixtureValue = fixture();
  const { container, rerender } = render(<NativeRead tool={{ started: fixtureValue.started }} state="streaming" />);
  expect(screen.getByText("Read · Pending")).toBeTruthy();
  expect(screen.queryByText("Requested path")).toBeNull();
  expect(screen.queryByLabelText("Read result")).toBeNull();
  rerender(<NativeRead tool={{ started: fixtureValue.started, states: fixtureValue.states }} state="streaming" />);
  expect(screen.getByText("Read · Running")).toBeTruthy();
  expect(screen.getByText("/original/한글")).toBeTruthy();
  expect(screen.queryByLabelText("Read result")).toBeNull();
  expect(container.querySelectorAll("li")).toHaveLength(2);
});

it("preserves a pending native failure without fabricating running or successful output", () => {
  const tool = fixture();
  const failed = { kind: "opencode-read", status: "failed", read: { call_id: "call-original", input: {}, time: { start: 100, end: 100 }, error: " Native error\n한글 " } };
  render(<NativeRead tool={{ started: tool.started, completed: failed }} state="complete" />);
  expect(screen.getByText("Read · Failed")).toBeTruthy();
  expect(screen.getByLabelText("Read error").textContent).toBe(failed.read.error);
  expect(screen.queryByLabelText("Read result")).toBeNull();
  expect(screen.queryByText("Running")).toBeNull();
});

it("preserves an explicitly empty successful output", () => {
  const tool = fixture();
  tool.completed.read.output = "";
  render(<NativeRead tool={tool} state="complete" />);
  expect(screen.getByLabelText("Read result").textContent).toBe("");
});

it.each([
  { name: "changed call", change: (tool: ReturnType<typeof fixture>) => { tool.completed.read.call_id = "replacement"; } },
  { name: "changed applied input", change: (tool: ReturnType<typeof fixture>) => { tool.completed.read.input.filePath = "replacement"; } },
  { name: "changed start time", change: (tool: ReturnType<typeof fixture>) => { tool.completed.read.time.start = 101; } },
  { name: "imprecise sequence", change: (tool: ReturnType<typeof fixture>) => { tool.states[0]!.sequence = Number.MAX_SAFE_INTEGER + 1; } },
  { name: "duplicate sequence", change: (tool: ReturnType<typeof fixture>) => { tool.states.push(tool.states[0]!); } },
  { name: "missing running", change: (tool: ReturnType<typeof fixture>) => { tool.states = []; } },
  { name: "foreign tool", change: (tool: ReturnType<typeof fixture>) => { tool.completed.kind = "command"; } },
  { name: "oversized result", change: (tool: ReturnType<typeof fixture>) => { tool.completed.read.output = "한".repeat(90000); } },
  { name: "invalid Unicode", change: (tool: ReturnType<typeof fixture>) => { tool.completed.read.output = "\uD800"; } },
])("keeps $name unavailable without partial output", ({ change }) => {
  const tool = fixture();
  change(tool);
  const { container } = render(<NativeRead tool={tool} state="complete" />);
  expect(screen.getByText("Read · Unavailable")).toBeTruthy();
  expect(container.querySelector("pre")).toBeNull();
});
