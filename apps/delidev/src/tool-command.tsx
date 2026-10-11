// SPDX-License-Identifier: Apache-2.0
import type { Resource } from "@delinoio/delidev-api-client";
import type { ReactNode } from "react";
import { document as readDocument, object, text } from "./documents";
import { retainedClaudeTool } from "./native-claude-tool";
import { retainedShell } from "./native-shell";
import { conversationProjection } from "./tool-turn-projection";
import { copy, LocalizedText } from "./localization";

export interface ResidentCommand { command: string; preview: string; outputs?: readonly string[]; evidence?: ReactNode }
function bounded(value: unknown): value is string {
  return typeof value === "string" && !value.includes("\0") && !/[\uD800-\uDFFF]/u.test(value) && new TextEncoder().encode(value).length <= 256 * 1024;
}
/** Presentation is derived afresh from a resident full payload. It never joins
 * streamed fragments or writes command/output strings into display metadata. */
export function residentCommand(resource: Resource): ResidentCommand | undefined {
  if (!conversationProjection(resource, resource.sessionId).tool) return;
  const data = readDocument(resource), state = text(data.state);
  if (data.text !== "" || data.input_id != null || data.phase != null) return;
  if (Object.hasOwn(data, "claude_tool")) {
    const retained = retainedClaudeTool(data.claude_tool, state, resource.id, text(data.native_id), text(data.native_parent_id));
    if (!retained || retained.reference.name !== "Bash") return;
    const input = object(JSON.parse(retained.proposal?.applied ?? retained.initial_input));
    if (!bounded(input.command)) return;
    const result = retained.result;
    return { command: input.command, preview: input.command,
      outputs: result?.text !== null && result?.text !== undefined ? [result.text] : result?.blocks !== null && result?.blocks !== undefined ? result.blocks.map(block => block.text) : undefined,
      evidence: <>{result?.is_error === true ? <p>{copy("native-claude-tool.errorReported_284ccd")}</p> : null}{result?.non_execution ? <p>{copy(result.non_execution.non_execution_kind === "user-rejected" ? "native-claude-tool.theOriginalUserRejectionPreventedThis_97b97e" : "native-claude-tool.nativePermissionPolicyPreventedThisTool_8e5f57")}</p> : null}</> };
  }
  const tool = object(data.tool), started = object(tool.started);
  if (started.kind === "opencode-shell") {
    const snapshots = retainedShell(tool, state), latest = snapshots?.at(-1);
    if (!latest || !bounded(latest.input.command)) return;
    const output = latest.output ?? latest.error ?? latest.preview;
    return { command: latest.input.command, preview: latest.input.command, outputs: output === undefined ? undefined : [output], evidence: <>
      {latest.exit !== undefined ? <p><LocalizedText id="native-shell.nativeExitCode_991101" components={{ s0: <>{latest.exit === null ? copy("native-shell.unavailable_ca1844") : latest.exit}</> }} /></p> : null}
      {latest.status === "failed" ? <p>{copy("native-shell.failed_031a8f")}</p> : null}
      {latest.truncated === true ? <p>{copy("native-shell.theNativeShellResultIsTruncated_e8b1dd")}</p> : null}
      {latest.interrupted === true ? <p><LocalizedText id="native-shell.nativeInterruption_edde92" components={{ s0: <>{copy("native-shell.observed_64fa8a")}</> }} /></p> : null}
      {latest.outputPath !== undefined ? <p><LocalizedText id="native-shell.nativeSavedOutputPath_6ce1ca" components={{ s0: <br />, s1: <code>{latest.outputPath}</code> }} /></p> : null}
    </> };
  }
  // Only the exclusive recorded command adapter can hide the Codex wrapper.
  // The renderer does not guess a command from another tool or Grok payload.
  if (started.kind !== "command" || started.changes != null || ["shell", "read", "todo", "builtin", "image_view", "sleep"].some(key => started[key] != null)) return;
  const completed = object(tool.completed), observation = object(tool.completed ? completed.command : started.command);
  if (state === "complete" ? !tool.completed || !["completed", "failed", "declined"].includes(text(completed.status)) : state !== "streaming" || tool.completed != null) return;
  if (["shell", "read", "todo", "builtin", "image_view", "sleep"].some(key => completed[key] != null)) return;
  if (tool.completed && (completed.kind !== undefined && completed.kind !== "command" || completed.changes != null)) return;
  if (!bounded(observation.command) || !observation.command || started.command != null && object(started.command).command !== observation.command) return;
  if (observation.aggregated_output != null && !bounded(observation.aggregated_output) || tool.output != null && !bounded(tool.output)) return;
  const output = tool.completed && typeof observation.aggregated_output === "string" ? observation.aggregated_output : typeof tool.output === "string" ? tool.output : undefined;
  const command = observation.command;
  return { command, preview: command.startsWith("/bin/zsh -lc ") ? command.slice("/bin/zsh -lc ".length) : command, outputs: output === undefined ? undefined : [output], evidence: typeof observation.exit_code === "number" && Number.isSafeInteger(observation.exit_code) ? <p><LocalizedText id="native-shell.nativeExitCode_991101" components={{ s0: <>{observation.exit_code}</> }} /></p> : null };
}

export function CommandOutput({ command }: { command: ResidentCommand }) {
  return <>{command.outputs === undefined ? <p className="tool-command-unavailable">{copy("session.commandOutputUnavailable")}</p> : command.outputs.length === 0 ? <p>{copy("session.commandOutputEmpty")}</p> : command.outputs.map((output, index) => <div key={index} className="tool-command-output"><pre><code>{output}</code></pre>{output === "" ? <p>{copy("session.commandOutputEmpty")}</p> : null}</div>)}{command.evidence}</>;
}
