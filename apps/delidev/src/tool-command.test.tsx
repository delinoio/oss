// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { EntityKind, ResourceSchema, newRequestId } from "@delinoio/delidev-api-client";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { encode } from "./documents";
import { CommandOutput, residentCommand } from "./tool-command";
afterEach(cleanup);
const sessionId = newRequestId(), execution = newRequestId();
function row(data: object, id = newRequestId()) { return create(ResourceSchema, { id, sessionId, kind: EntityKind.MESSAGE, documentJson: encode({ role: "tool", text: "", state: "complete", execution_id: execution, native_thread_id: "thread", native_turn_id: "turn", ...data }) }); }
const codex = (command: string, aggregate?: string, output?: string) => row({ tool: { started: { kind: "command", command: { command } }, completed: { kind: "command", status: "completed", command: { command, ...(aggregate === undefined ? {} : { aggregated_output: aggregate }) } }, ...(output === undefined ? {} : { output }) } });
it.each([
 ["/bin/zsh -lc pwd", "pwd"], ["/bin/bash -lc pwd", "/bin/bash -lc pwd"],
 ["/bin/zsh -l -c pwd", "/bin/zsh -l -c pwd"], ["printf '/bin/zsh -lc pwd'", "printf '/bin/zsh -lc pwd'"],
 ["/bin/zsh -lc  '유니코드'\n  exact", " '유니코드'\n  exact"],
])("hides only the exact Codex leading wrapper: %s", (command, preview) => {
 const result = residentCommand(codex(command, "original aggregate", "different stream"))!;
 expect(result.command).toBe(command); expect(result.preview).toBe(preview); expect(result.outputs).toEqual(["original aggregate"]);
});
it("keeps empty, absent, stream and inert markup output distinct", () => {
 const empty = residentCommand(codex("pwd", "", "stream"))!;
 expect(empty.outputs).toEqual([""]); const view = render(<CommandOutput command={empty} />);
 expect(screen.getByText("Empty output.")).toBeTruthy(); expect(view.container.querySelector("code")?.textContent).toBe("");
 view.rerender(<CommandOutput command={residentCommand(codex("pwd"))!} />);
 expect(screen.getByText("No command output has been observed.")).toBeTruthy();
 const exact = "<script>fetch('https://invalid')</script>\n[link](https://invalid)\x1b[31m";
 view.rerender(<CommandOutput command={residentCommand(codex("pwd", undefined, exact))!} />);
 expect(view.container.querySelector("code")?.textContent).toBe(exact); expect(view.container.querySelectorAll("script,a,button")).toHaveLength(0);
});
function claude(applied = '{"command":"/bin/zsh -lc pwd"}') {
 const id = newRequestId(), initial = '{"command":"initial exact"}';
 return row({ native_id: "tool-original", native_parent_id: "msg-original", claude_tool: { reference: { id, native_id: "tool-original", name: "Bash" }, message_id: newRequestId(), native_message_id: "msg-original", index: 0, caller: null, initial_input: initial, input_delta: null, proposal: { proposed: initial, applied }, result: { native_event_id: "123e4567-e89b-42d3-a456-426614174000", is_error: true, text: null, blocks: [{ kind: "text", text: "first" }, { kind: "text", text: "" }, { kind: "text", text: "last" }], structured: '{"original":true}', non_execution: { id: "tool-original", non_execution_kind: "user-rejected" } } } }, id);
}
it("uses complete Claude applied Bash input, ordered blocks and visible rejection", () => {
 const result = residentCommand(claude())!; expect(result.preview).toBe("/bin/zsh -lc pwd"); expect(result.outputs).toEqual(["first", "", "last"]);
 const view = render(<CommandOutput command={result} />); expect(view.container.querySelectorAll("pre code")).toHaveLength(3); expect(screen.getByText("The original user rejection prevented this tool from executing.")).toBeTruthy();
});
it("rejects malformed Claude and never reconstructs command fragments", () => {
 expect(residentCommand(claude('{"command":17}'))).toBeUndefined();
 const original = claude(); const data = JSON.parse(new TextDecoder().decode(original.documentJson));
 data.claude_tool.input_delta = '{"command":"partial'; data.claude_tool.proposal.proposed = data.claude_tool.initial_input;
 expect(residentCommand({ ...original, documentJson: encode(data) })).toBeUndefined();
 data.claude_tool.proposal = null; data.claude_tool.result = null; data.state = "streaming";
 data.claude_tool.initial_input = "{}";
 expect(residentCommand({ ...original, documentJson: encode(data) })).toBeUndefined();
});
function shell() {
 const input = { command: "/bin/zsh -lc pwd" };
 return { started: { kind: "opencode-shell", status: "pending", shell: { call_id: "original", input: {}, raw: "" } }, states: [{ sequence: 1, snapshot: { kind: "opencode-shell", status: "running", shell: { call_id: "original", input, time: { start: 1 }, metadata: { output: "preview", exit: null, exit_observed: false } } } }], completed: { kind: "opencode-shell", status: "completed", shell: { call_id: "original", input, time: { start: 1, end: 2 }, title: "native title", output: "complete original", metadata: { output: "independent preview", exit: 7, exit_observed: true, truncated: true, outputPath: "https://inert.invalid/retained", interrupted: true } } } };
}
it("uses immutable OpenCode snapshots and preserves visible exit/truncation/interruption", () => {
 const result = residentCommand(row({ tool: shell() }))!; expect(result.preview).toBe("/bin/zsh -lc pwd"); expect(result.outputs).toEqual(["complete original"]);
 const view = render(<CommandOutput command={result} />); expect(screen.getByText("Native exit code: 7")).toBeTruthy(); expect(screen.getByText("The native Shell result is truncated.")).toBeTruthy(); expect(view.container.textContent).toContain("https://inert.invalid/retained"); expect(view.container.querySelector("a")).toBeNull();
 const tool = shell(); expect(residentCommand(row({ state: "streaming", tool: { started: tool.started } }))).toBeUndefined();
 expect(residentCommand(row({ state: "streaming", tool: { started: tool.started, states: tool.states } }))!.outputs).toEqual(["preview"]);
 tool.completed.shell.input.command = "foreign";
 expect(residentCommand(row({ tool }))).toBeUndefined();
});
it("does not infer commands from mixed, unknown, foreign and non-command resources", () => {
 expect(residentCommand(row({ tool: { started: { kind: "read", command: { command: "pwd" } } } }))).toBeUndefined();
 expect(residentCommand(row({ tool: { started: { kind: "command", command: { command: "pwd" }, shell: {} } } }))).toBeUndefined();
 const original = codex("pwd"); expect(residentCommand({ ...original, kind: EntityKind.UNSPECIFIED })).toBeUndefined();
 const data = JSON.parse(new TextDecoder().decode(original.documentJson)); data.artifact = {}; expect(residentCommand({ ...original, documentJson: encode(data) })).toBeUndefined();
 expect(residentCommand(row({ grok_tool: { command: "pwd", title: "pwd" } }))).toBeUndefined();
});
