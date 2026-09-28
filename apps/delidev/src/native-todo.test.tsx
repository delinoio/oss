import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { NativeTodo, NativeTodoProgress } from "./native-todo";

const todos = [{ content: " original\n<script>untrusted()</script>🙂 ", status: "in_progress", priority: "high" }, { content: "Cancelled task", status: "cancelled", priority: "low" }, { content: "Native extension", status: "waiting", priority: "urgent" }];
function fixture() {
  return {
    started: { kind: "opencode-todo", status: "pending", todo: { call_id: "original", input: {}, raw: "" } },
    states: [{ sequence: 5, snapshot: { kind: "opencode-todo", status: "running", todo: { call_id: "original", input: { todos: structuredClone(todos) }, time: { start: 100 } } } }],
    completed: { kind: "opencode-todo", status: "completed", todo: { call_id: "original", input: { todos: structuredClone(todos) }, time: { start: 100, end: 200 }, title: "3 todos", output: "Original output", metadata: { todos: structuredClone(todos), truncated: false } } },
  };
}

it("preserves ordered original priorities and unknown statuses as inert read-only content", () => {
  const progress = { kind: "opencode-todo", todo: { native_event_id: "evt_01960dcbe1faABCDEFGHIJKLMN", todos } };
  const { container } = render(<NativeTodoProgress progress={progress} state="complete" />);
  expect([...container.querySelectorAll("li pre")].map((e) => e.textContent)).toEqual(todos.map((t) => t.content));
  expect(screen.getByText("Status: In progress · Priority: high")).toBeTruthy();
  expect(screen.getByText("Status: Cancelled · Priority: low")).toBeTruthy();
  expect(screen.getByText("Status: waiting · Priority: urgent")).toBeTruthy();
  expect(container.querySelector("script, input, button, a")).toBeNull();
});

it("preserves a proposal without a list and later explicit clearing", () => {
  const tool = fixture();
  const { rerender } = render(<NativeTodo tool={{ started: tool.started }} state="streaming" />);
  expect(screen.getByText("The native proposal has no applied list yet.")).toBeTruthy();
  expect(screen.queryByText("The native list is empty.")).toBeNull();
  tool.states[0]!.snapshot.todo.input.todos = [];
  tool.completed.todo.input.todos = [];
  tool.completed.todo.metadata.todos = [];
  rerender(<NativeTodo tool={tool} state="complete" />);
  expect(screen.getByText("Todo update · completed")).toBeTruthy();
  expect(screen.getAllByText("The native list is empty.").length).toBeGreaterThan(0);
  expect(screen.getByText("Original output")).toBeTruthy();
});

it("keeps native failure separate from successful list replacement", () => {
  const tool = fixture();
  const completed = { kind: "opencode-todo", status: "failed", todo: { call_id: "original", input: {}, time: { start: 100, end: 100 }, error: "Original failure" } };
  render(<NativeTodo tool={{ started: tool.started, completed }} state="complete" />);
  expect(screen.getByLabelText("Todo error").textContent).toBe("Original failure");
  expect(screen.queryByText("Original native result")).toBeNull();
});

it.each([
  { name: "changed input", change: (t: ReturnType<typeof fixture>) => { t.completed.todo.input.todos[0]!.content = "changed"; } },
  { name: "changed result", change: (t: ReturnType<typeof fixture>) => { t.completed.todo.metadata.todos.reverse(); } },
  { name: "changed call", change: (t: ReturnType<typeof fixture>) => { t.completed.todo.call_id = "changed"; } },
  { name: "changed start", change: (t: ReturnType<typeof fixture>) => { t.completed.todo.time.start++; } },
  { name: "missing running", change: (t: ReturnType<typeof fixture>) => { t.states = []; } },
  { name: "imprecise sequence", change: (t: ReturnType<typeof fixture>) => { t.states[0]!.sequence = Number.MAX_SAFE_INTEGER + 1; } },
  { name: "invalid Unicode", change: (t: ReturnType<typeof fixture>) => { t.completed.todo.output = "\uD800"; } },
])("rejects $name before displaying any partial tool evidence", ({ change }) => {
  const tool = fixture(); change(tool);
  const { container } = render(<NativeTodo tool={tool} state="complete" />);
  expect(screen.getByText("Todo update · Unavailable")).toBeTruthy();
  expect(container.querySelector("pre")).toBeNull();
});

it.each([null, [null], [{ content: "x", priority: "high" }], [{ content: "x", status: null, priority: "high" }], [{ content: "x", status: "done", priority: "high", extra: true }], [{ content: "\uD800", status: "done", priority: "high" }]])("rejects malformed progress %j without invented defaults", (values) => {
  const { container } = render(<NativeTodoProgress progress={{ kind: "opencode-todo", todo: { native_event_id: "evt_01960dcbe1faABCDEFGHIJKLMN", todos: values } }} state="complete" />);
  expect(screen.getByText("Todo progress · Unavailable")).toBeTruthy();
  expect(container.querySelector("li")).toBeNull();
});

it("does not reinterpret an empty native status or object property name", () => {
  render(<NativeTodoProgress progress={{ kind: "opencode-todo", todo: { native_event_id: "evt_01960dcbe1faABCDEFGHIJKLMN", todos: [{ content: "Empty values", status: "", priority: "" }, { content: "Original value", status: "toString", priority: "" }] } }} state="complete" />);
  expect(screen.getByText("Status: · Priority:")).toBeTruthy();
  expect(screen.getByText("Status: toString · Priority:")).toBeTruthy();
});
