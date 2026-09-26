import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { NativeShell } from "./native-shell";

function fixture() {
  return {
    output: null,
    started: { kind: "opencode-shell", status: "pending", shell: { call_id: "original", input: {}, raw: "" } },
    states: [
      { sequence: 5, snapshot: { kind: "opencode-shell", status: "running", shell: { call_id: "original", input: { command: "original command", timeout: 100 }, time: { start: 100 }, metadata: { output: "first preview", exit: null, exit_observed: false } } } },
      { sequence: 6, snapshot: { kind: "opencode-shell", status: "running", shell: { call_id: "original", input: { command: "original command", timeout: 100 }, time: { start: 100 }, metadata: { output: "different tail", exit: null, exit_observed: false } } } },
    ],
    completed: { kind: "opencode-shell", status: "completed", shell: { call_id: "original", input: { command: "original command", timeout: 100 }, time: { start: 100, end: 200 }, title: "original command", output: " original\n<script>untrusted()</script>🙂 ", metadata: { output: "final preview", exit: 7 as number | null, exit_observed: true, truncated: true, outputPath: "https://untrusted.invalid/output" } } },
  };
}

it("shows original command and combined output without fabricating successful exit or appending previews", () => {
  const tool = fixture();
  const { container } = render(<NativeShell tool={tool} state="complete" />);
  expect(screen.getByText("Shell · Completed")).toBeTruthy();
  expect(screen.getByText("Native exit code: 7")).toBeTruthy();
  expect(container.querySelector("details")?.open).toBe(false);
  expect(screen.getByLabelText("Shell result").textContent).toBe(tool.completed.shell.output);
  expect(screen.getByText("first preview")).toBeTruthy();
  expect(screen.getByText("different tail")).toBeTruthy();
  expect(screen.getByText("The native Shell result is truncated.")).toBeTruthy();
  expect(screen.getByText("https://untrusted.invalid/output")).toBeTruthy();
  expect(screen.queryByText("Requested directory")).toBeNull();
  expect(container.querySelector("script")).toBeNull();
  expect(container.querySelector("a")).toBeNull();
});

it("keeps an original null exit unavailable while preserving empty result", () => {
  const tool = fixture();
  tool.completed.shell.metadata.exit = null;
  tool.completed.shell.output = "";
  render(<NativeShell tool={tool} state="complete" />);
  expect(screen.getByText("Native exit code: Unavailable")).toBeTruthy();
  expect(screen.getByLabelText("Shell result").textContent).toBe("");
});

it("keeps pending and running state distinct from a terminal result", () => {
  const tool = fixture();
  const { rerender } = render(<NativeShell tool={{ started: tool.started }} state="streaming" />);
  expect(screen.getByText("Shell · Pending")).toBeTruthy();
  expect(screen.queryByText("Command")).toBeNull();
  rerender(<NativeShell tool={{ started: tool.started, states: tool.states }} state="streaming" />);
  expect(screen.getByText("Shell · Running")).toBeTruthy();
  expect(screen.queryByText(/Native exit code/)).toBeNull();
  expect(screen.queryByLabelText("Shell result")).toBeNull();
});

it("retains original pending failure without invented running or output", () => {
  const tool = fixture();
  const failed = { kind: "opencode-shell", status: "failed", shell: { call_id: "original", input: {}, time: { start: 100, end: 100 }, error: "Original error" } };
  render(<NativeShell tool={{ started: tool.started, completed: failed }} state="complete" />);
  expect(screen.getByText("Shell · Failed")).toBeTruthy();
  expect(screen.getByLabelText("Shell error").textContent).toBe("Original error");
  expect(screen.queryByLabelText("Shell result")).toBeNull();
  expect(screen.queryByText("Running")).toBeNull();
});

it.each([
  { name: "changed command", change: (tool: ReturnType<typeof fixture>) => { tool.completed.shell.input.command = "changed"; } },
  { name: "changed timeout", change: (tool: ReturnType<typeof fixture>) => { tool.completed.shell.input.timeout = 101; } },
  { name: "changed owner", change: (tool: ReturnType<typeof fixture>) => { tool.completed.shell.call_id = "changed"; } },
  { name: "changed start", change: (tool: ReturnType<typeof fixture>) => { tool.completed.shell.time.start++; } },
  { name: "duplicate sequence", change: (tool: ReturnType<typeof fixture>) => { tool.states[1]!.sequence = 5; } },
  { name: "unsafe exit", change: (tool: ReturnType<typeof fixture>) => { tool.completed.shell.metadata.exit = Number.MAX_SAFE_INTEGER + 1; } },
  { name: "missing exit observation", change: (tool: ReturnType<typeof fixture>) => { tool.completed.shell.metadata.exit_observed = false; } },
  { name: "missing running", change: (tool: ReturnType<typeof fixture>) => { tool.states = []; } },
  { name: "invalid Unicode", change: (tool: ReturnType<typeof fixture>) => { tool.completed.shell.output = "\uD800"; } },
  { name: "oversized output", change: (tool: ReturnType<typeof fixture>) => { tool.completed.shell.output = "한".repeat(90000); } },
  { name: "unproved output path", change: (tool: ReturnType<typeof fixture>) => { tool.completed.shell.metadata.truncated = false; } },
])("rejects $name without showing partial evidence", ({ change }) => {
  const tool = fixture();
  change(tool);
  const { container } = render(<NativeShell tool={tool} state="complete" />);
  expect(screen.getByText("Shell · Unavailable")).toBeTruthy();
  expect(container.querySelector("pre")).toBeNull();
});
