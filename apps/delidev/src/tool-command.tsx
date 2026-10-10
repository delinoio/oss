// SPDX-License-Identifier: Apache-2.0
import type { Resource } from "@delinoio/delidev-api-client";
import { document as readDocument, object, text } from "./documents";
import { validatedClaudeTool, claudeCommand } from "./native-claude-tool";
import { retainedShell } from "./native-shell";
import { Disclosure, DisclosureSummary } from "./disclosure";
import { LocalizedText, copy } from "./localization";

export function codexCommand(tool: Record<string, unknown>): Record<string, unknown> | undefined {
  const started = object(tool.started), completed = object(tool.completed);
  if (started.kind !== "command" || [started, ...(tool.completed != null ? [completed] : [])].some(value => value.kind !== "command" && value.kind !== undefined || ["shell", "read", "todo", "builtin", "image_view", "sleep"].some(key => value[key] != null))) return;
  const command = object((tool.completed != null ? completed : started).command);
  return typeof command.command === "string" ? command : undefined;
}

/** Resident payload only. Never project, persist or cache native command text. */
export function toolCommandPreview(resource: Resource): string | undefined {
  const d = readDocument(resource);
  if (d.role !== "tool" || ["artifact", "progress", "claude", "claude_progress", "claude_interruption", "grok_text", "grok_user"].some(key => Object.hasOwn(d,key))) return;
  if (["tool", "claude_tool", "grok_tool"].filter(key=>Object.hasOwn(d,key)).length !== 1) return;
  if (d.claude_tool != null) {
    if (d.text !== "" || d.input_id != null || d.phase != null) return;
    const retained = validatedClaudeTool(d.claude_tool,text(d.state),resource.id,text(d.native_id),text(d.native_parent_id));
    return retained && claudeCommand(retained);
  }
  const tool = object(d.tool);
  if (object(tool.started).kind === "opencode-shell") return retainedShell(tool,text(d.state))?.at(-1)?.input.command;
  const command = codexCommand(tool)?.command;
  // Exact literal preview exception only; preserve every suffix byte/spelling.
  return typeof command === "string" ? command.startsWith("/bin/zsh -lc ") ? command.slice("/bin/zsh -lc ".length) : command : undefined;
}

export function GroupedCodexCommand({tool}: {tool:Record<string,unknown>}) {
  const command=codexCommand(tool)!;
  const aggregate=object(object(tool.completed).command).aggregated_output;
  const output=typeof aggregate === "string" ? aggregate : typeof tool.output === "string" ? tool.output : undefined;
  return <section data-command-presentation>
    {output !== undefined ? <pre className="tool-command-output"><code>{output}</code></pre> : <p>{copy("session.commandOutputUnavailable")}</p>}
    {typeof command.exit_code === "number" ? <p><LocalizedText id="native-shell.nativeExitCode_991101" components={{s0:<>{command.exit_code}</>}}/></p> : null}
    <Disclosure data-tool-detail="command-details"><DisclosureSummary>{copy("session.commandDetails")}</DisclosureSummary>
      <pre><code>{command.command as string}</code></pre>
      {typeof command.cwd === "string" ? <p><LocalizedText id="session.directory_369f13" components={{s0:<>{command.cwd}</>}}/></p> : null}
      {typeof aggregate === "string" && typeof tool.output === "string" ? <Disclosure data-tool-detail="codex-stream"><DisclosureSummary>{copy("session.streamedObservations_589dae")}</DisclosureSummary><pre>{tool.output}</pre></Disclosure> : null}
      <Disclosure data-tool-detail="codex-observations"><DisclosureSummary>{copy("native-shell.originalProposalAndObservations_8d63b6")}</DisclosureSummary><pre>{JSON.stringify(tool,null,2)}</pre></Disclosure>
    </Disclosure>
  </section>;
}
