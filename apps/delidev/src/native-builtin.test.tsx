import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { NativeBuiltin, NativeWorkspaceEvent } from "./native-builtin";

const input = '{"filePath":"/original/file","content":"<script>untrusted()</script>🙂","exact":9007199254740993,"scale":1.000}';
const metadata = '{"diagnostics":{"extension":true},"exact":9007199254740993}';
function fixture(name = "write") {
  return {
    started: { kind: "opencode-builtin", status: "pending", builtin: { name, call_id: "original", input_json: "{}", raw: "" } },
    states: [{ sequence: 5, snapshot: { kind: "opencode-builtin", status: "running", builtin: { name, call_id: "original", input_json: input, time: { start: 100 } } } }],
    completed: { kind: "opencode-builtin", status: "completed", builtin: { name, call_id: "original", input_json: input, metadata_json: metadata, time: { start: 100, end: 200 }, title: "Original title", output: "Original output" } },
  };
}

it.each([["write", "Write"], ["edit", "Edit"], ["apply_patch", "Apply Patch"], ["glob", "Glob"], ["grep", "Grep"]])("preserves %s native JSON precision and inert metadata", (name, label) => {
  const { container } = render(<NativeBuiltin tool={fixture(name)} state="complete" />);
  expect(screen.getByText(`${label} · completed`)).toBeTruthy();
  const values = [...container.querySelectorAll("pre")].map((e) => e.textContent);
  expect(values).toContain(input);
  expect(values).toContain(metadata);
  expect(screen.getByLabelText("Native tool result").textContent).toBe("Original output");
  expect(container.querySelector("script, a, button, input")).toBeNull();
  expect(container.querySelector("details")?.open).toBe(false);
});

it("preserves native failure without synthesizing a result", () => {
  const tool = fixture();
  const completed = { kind: "opencode-builtin", status: "failed", builtin: { name: "write", call_id: "original", input_json: input, time: { start: 100, end: 200 }, error: "Original failure" } };
  render(<NativeBuiltin tool={{ ...tool, completed }} state="complete" />);
  expect(screen.getByLabelText("Native tool error").textContent).toBe("Original failure");
  expect(screen.queryByLabelText("Native tool result")).toBeNull();
});

it.each(["valid", "changed-title", "ordinary-tool", "missing-interruption"])("retains only the original interrupted task title: %s", mode => {
  const tool = fixture("task");
  const running = { ...tool.states[0]!.snapshot.builtin, title: "Original task title" };
  const completed = { kind: "opencode-builtin", status: "failed", builtin: { ...running, time: { start: 100, end: 200 }, metadata_json: '{"interrupted":true}', error: "Tool execution aborted" } };
  if (mode === "changed-title") completed.builtin.title = "Changed title";
  if (mode === "ordinary-tool") { tool.started.builtin.name = "write"; running.name = "write"; completed.builtin.name = "write"; }
  if (mode === "missing-interruption") completed.builtin.metadata_json = "{}";
  render(<NativeBuiltin tool={{ ...tool, states: [{ sequence: 5, snapshot: { ...tool.states[0]!.snapshot, builtin: running } }], completed }} state="complete" />);
  if (mode === "valid") {
    expect(screen.getByText("Foreground task · failed")).toBeTruthy();
    expect(screen.getByText("Original task title")).toBeTruthy();
    expect(screen.queryByLabelText("Native tool result")).toBeNull();
  } else expect(screen.getByText("Native tool · Unavailable")).toBeTruthy();
});

it.each([
  { name: "changed input", change: (t: ReturnType<typeof fixture>) => { t.completed.builtin.input_json = "{}"; } },
  { name: "changed tool", change: (t: ReturnType<typeof fixture>) => { t.completed.builtin.name = "edit"; } },
  { name: "changed call", change: (t: ReturnType<typeof fixture>) => { t.completed.builtin.call_id = "changed"; } },
  { name: "changed start", change: (t: ReturnType<typeof fixture>) => { t.completed.builtin.time.start++; } },
  { name: "missing running", change: (t: ReturnType<typeof fixture>) => { t.states = []; } },
  { name: "invalid object", change: (t: ReturnType<typeof fixture>) => { t.completed.builtin.metadata_json = "null"; } },
  { name: "imprecise sequence", change: (t: ReturnType<typeof fixture>) => { t.states[0]!.sequence = Number.MAX_SAFE_INTEGER + 1; } },
  { name: "invalid Unicode", change: (t: ReturnType<typeof fixture>) => { t.completed.builtin.output = "\uD800"; } },
])("rejects $name without partial tool evidence", ({ change }) => {
  const tool = fixture(); change(tool);
  const { container } = render(<NativeBuiltin tool={tool} state="complete" />);
  expect(screen.getByText("Native tool · Unavailable")).toBeTruthy();
  expect(container.querySelector("pre")).toBeNull();
});

it("preserves original workspace notifications without adding tool or file actions", () => {
  const progress = { kind: "opencode-workspace", workspace: { kind: "file-unlinked", native_event_id: "evt_01960dcbe1faABCDEFGHIJKLMN", file: "/original/<script>path</script>🙂" } };
  const { container } = render(<NativeWorkspaceEvent progress={progress} state="complete" />);
  expect(screen.getByText("Native file unlinked")).toBeTruthy();
  expect(container.querySelector("pre")?.textContent).toBe(progress.workspace.file);
  expect(container.querySelector("script, a, button")).toBeNull();
});

it.each([
  { kind: "unknown", native_event_id: "evt_01960dcbe1faABCDEFGHIJKLMN", file: "/original" },
  { kind: "file-added", native_event_id: "invented", file: "/original" },
  { kind: "file-added", native_event_id: "evt_01960dcbe1faABCDEFGHIJKLMN", file: "" },
  { kind: "file-added", native_event_id: "evt_01960dcbe1faABCDEFGHIJKLMN", file: "/original", tool_id: "invented" },
])("rejects inconsistent workspace notification %j", (workspace) => {
  const { container } = render(<NativeWorkspaceEvent progress={{ kind: "opencode-workspace", workspace }} state="complete" />);
  expect(screen.getByText("Native workspace event · Unavailable")).toBeTruthy();
  expect(container.querySelector("pre")).toBeNull();
});
