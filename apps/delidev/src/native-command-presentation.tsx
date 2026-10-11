// SPDX-License-Identifier: Apache-2.0
import { type ReactNode } from "react";
import { EntityKind, type Resource } from "@delinoio/delidev-api-client";
import { Disclosure, DisclosureSummary } from "./disclosure";
import { document, object, text, type Document } from "./documents";
import { retainedClaudeTool } from "./native-claude-tool";
import { retainedShell } from "./native-shell";
import { statusLabel } from "./product-status";
import { copy, LocalizedText } from "./localization";

export enum CommandAdapter {
  Codex = "codex",
  Claude = "claude",
  OpenCode = "opencode",
}
type CodexCommand = {
  command: string;
  cwd: string;
  source: string;
  actions: Document[];
  process_id?: string | null;
  aggregated_output?: string | null;
  exit_code?: number | null;
  duration_ms?: number | null;
  plugin_id?: string | null;
  script_path?: string | null;
};
type Claude = NonNullable<ReturnType<typeof retainedClaudeTool>>;
type Shell = NonNullable<ReturnType<typeof retainedShell>>;
export type NativeCommandPresentation =
  | {
      adapter: CommandAdapter.Codex;
      command: string;
      started: CodexCommand;
      completed?: CodexCommand;
      output?: string | null;
      inputs: Document[];
    }
  | { adapter: CommandAdapter.Claude; command: string; retained: Claude }
  | { adapter: CommandAdapter.OpenCode; command: string; snapshots: Shell };
const bounded = (v: unknown, max = 256 * 1024): v is string =>
  typeof v === "string" &&
  !v.includes("\0") &&
  !/[\uD800-\uDFFF]/u.test(v) &&
  new TextEncoder().encode(v).length <= max;
const allowed = (v: Document, keys: string[]) =>
  Object.keys(v).every((k) => keys.includes(k));
function codexSnapshot(value: unknown): CodexCommand | undefined {
  const v = object(value),
    c = object(v.command);
  if (
    v.kind !== "command" ||
    !["running", "completed", "failed", "declined"].includes(text(v.status)) ||
    !allowed(v, ["kind", "status", "command", "changes"]) ||
    v.changes != null ||
    !allowed(c, [
      "command",
      "cwd",
      "source",
      "actions",
      "process_id",
      "aggregated_output",
      "exit_code",
      "duration_ms",
      "plugin_id",
      "script_path",
    ]) ||
    !bounded(c.command) ||
    c.command.length === 0 ||
    !bounded(c.cwd, 4096) ||
    c.cwd.length === 0 ||
    !["agent", "user-shell", "exec-startup", "exec-input"].includes(
      text(c.source),
    ) ||
    !Array.isArray(c.actions) ||
    c.actions.length > 1024
  )
    return;
  for (const [key, limit] of [
    ["process_id", 1024],
    ["aggregated_output", 256 * 1024],
    ["plugin_id", 1024],
    ["script_path", 4096],
  ] as const)
    if (c[key] != null && !bounded(c[key], limit)) return;
  if (
    (c.exit_code != null &&
      (typeof c.exit_code !== "number" ||
        !Number.isInteger(c.exit_code) ||
        c.exit_code < -2147483648 ||
        c.exit_code > 2147483647)) ||
    (c.duration_ms != null &&
      (typeof c.duration_ms !== "number" ||
        !Number.isSafeInteger(c.duration_ms) ||
        c.duration_ms < 0))
  )
    return;
  for (const raw of c.actions) {
    const a = object(raw);
    if (
      !allowed(a, ["kind", "command", "name", "path", "query"]) ||
      !["read", "list", "search", "unknown"].includes(text(a.kind)) ||
      !bounded(a.command) ||
      a.command.length === 0 ||
      [a.name, a.path, a.query].some((x) => x != null && !bounded(x))
    )
      return;
    if (
      (a.kind === "read" &&
        (a.name == null || a.path == null || a.query != null)) ||
      (a.kind === "list" && (a.name != null || a.query != null)) ||
      (a.kind === "search" && a.name != null) ||
      (a.kind === "unknown" &&
        (a.name != null || a.path != null || a.query != null))
    )
      return;
  }
  return c as CodexCommand;
}
/** A transient view of resident validated source data. Never add it to projections. */
export function nativeCommandPresentation(
  resource: Resource,
): NativeCommandPresentation | undefined {
  const d = document(resource);
  if (
    resource.kind !== EntityKind.MESSAGE ||
    d.role !== "tool" ||
    d.text !== "" ||
    d.input_id != null ||
    d.phase != null ||
    [
      "artifact",
      "progress",
      "claude",
      "claude_progress",
      "claude_interruption",
      "grok_text",
      "grok_user",
      "grok_tool",
    ].some((k) => Object.hasOwn(d, k))
  )
    return;
  if (Object.hasOwn(d, "claude_tool")) {
    if (Object.hasOwn(d, "tool")) return;
    const retained = retainedClaudeTool(
      d.claude_tool,
      text(d.state),
      resource.id,
      text(d.native_id),
      text(d.native_parent_id),
    );
    if (!retained || retained.reference.name !== "Bash") return;
    // Only complete, validated input JSON is parsed. Stream fragments remain data.
    const input = object(
      JSON.parse(retained.proposal?.applied ?? retained.initial_input),
    );
    return bounded(input.command)
      ? { adapter: CommandAdapter.Claude, command: input.command, retained }
      : undefined;
  }
  const tool = object(d.tool);
  if (object(tool.started).kind === "opencode-shell") {
    const snapshots = retainedShell(tool, text(d.state)),
      latest = snapshots?.at(-1);
    return snapshots && latest && typeof latest.input.command === "string"
      ? {
          adapter: CommandAdapter.OpenCode,
          command: latest.input.command,
          snapshots,
        }
      : undefined;
  }
  const started = codexSnapshot(tool.started),
    completed =
      tool.completed == null ? undefined : codexSnapshot(tool.completed);
  if (
    !started ||
    object(tool.started).status !== "running" ||
    !allowed(tool, ["started", "completed", "output", "inputs", "states"]) ||
    (tool.states != null &&
      (!Array.isArray(tool.states) || tool.states.length !== 0)) ||
    (tool.output != null && !bounded(tool.output)) ||
    (d.state === "complete" &&
      (!completed || object(tool.completed).status === "running")) ||
    (d.state === "streaming" && tool.completed != null) ||
    !["streaming", "complete"].includes(text(d.state))
  )
    return;
  const inputs = tool.inputs ?? [];
  if (!Array.isArray(inputs) || inputs.length > 1024) return;
  let sequence = 0;
  for (const raw of inputs) {
    const v = object(raw),
      input = object(v.input);
    if (
      !allowed(v, ["sequence", "input"]) ||
      !Number.isSafeInteger(v.sequence) ||
      Number(v.sequence) <= sequence ||
      !allowed(input, ["process_id", "text"]) ||
      !bounded(input.process_id, 1024) ||
      !input.process_id ||
      !bounded(input.text)
    )
      return;
    sequence = Number(v.sequence);
  }
  return {
    adapter: CommandAdapter.Codex,
    command: (completed ?? started).command,
    started,
    completed,
    output: tool.output as string | null | undefined,
    inputs: inputs.map(object),
  };
}
export function commandPreview(p: NativeCommandPresentation): string {
  const prefix = "/bin/zsh -lc ";
  return p.adapter === CommandAdapter.Codex && p.command.startsWith(prefix)
    ? p.command.slice(prefix.length)
    : p.command;
}
function Literal({ value, label }: { value: string; label?: string }) {
  return (
    <pre className="tool-command-output" aria-label={label}>
      <code>{value}</code>
    </pre>
  );
}
function MissingOutput() {
  return (
    <p className="tool-command-notice">{copy("tool-command.outputAbsent")}</p>
  );
}
function Details({ children }: { children: ReactNode }) {
  return (
    <Disclosure className="tool-command-details" open={false}>
      <DisclosureSummary>{copy("tool-command.details")}</DisclosureSummary>
      {children}
    </Disclosure>
  );
}
function CodexBody({
  value,
}: {
  value: Extract<NativeCommandPresentation, { adapter: CommandAdapter.Codex }>;
}) {
  const current = value.completed ?? value.started;
  const aggregate = value.completed?.aggregated_output;
  const output = typeof aggregate === "string" ? aggregate : value.output;
  return (
    <>
      {typeof output === "string" ? (
        <Literal
          value={output}
          label={copy(
            typeof aggregate === "string"
              ? "session.nativeAggregateOutput_2d06e4"
              : "tool-command.streamOutput",
          )}
        />
      ) : (
        <MissingOutput />
      )}
      {current.exit_code != null ? (
        <p>{copy("tool-command.exit", { exit: current.exit_code })}</p>
      ) : null}
      <Details>
        <Literal
          value={value.command}
          label={copy("native-shell.command_713166")}
        />
        <p>
          <LocalizedText
            id="session.directory_369f13"
            components={{ s0: <>{current.cwd}</> }}
          />
        </p>
        {typeof aggregate === "string" && typeof value.output === "string" ? (
          <section aria-label={copy("tool-command.streamOutput")}>
            <Literal value={value.output} />
          </section>
        ) : null}
        <section aria-label={copy("tool-command.originalObservations")}>
          <p>{copy("tool-command.originalStart")}</p>
          <Literal value={JSON.stringify(value.started, null, 2)} />
          {value.completed ? (
            <section aria-label={copy("tool-command.originalCompletion")}>
              <p>{copy("tool-command.originalCompletion")}</p>
              <Literal value={JSON.stringify(value.completed, null, 2)} />
            </section>
          ) : null}
          {value.inputs.map((v, i) => (
            <div key={i}>
              <Literal value={text(object(v.input).process_id)} />
              <Literal value={text(object(v.input).text)} />
            </div>
          ))}
        </section>
      </Details>
    </>
  );
}
function ClaudeBody({
  value,
}: {
  value: Extract<NativeCommandPresentation, { adapter: CommandAdapter.Claude }>;
}) {
  const r = value.retained,
    result = r.result;
  return (
    <>
      {result ? (
        <>
          {result.text !== null ? (
            <Literal
              value={result.text}
              label={copy("native-claude-tool.originalToolResult_2b1077")}
            />
          ) : result.blocks !== null ? (
            <ol aria-label={copy("native-claude-tool.toolResultBlocks_7800be")}>
              {result.blocks.map((b, i) => (
                <li key={i}>
                  <Literal value={b.text} />
                </li>
              ))}
            </ol>
          ) : (
            <MissingOutput />
          )}
          <p>
            <LocalizedText
              id="native-claude-tool.nativeErrorFlag_e3d9ff"
              components={{
                s0: (
                  <>
                    {copy(
                      result.is_error === null
                        ? "native-claude-tool.notReported_adadfa"
                        : result.is_error
                          ? "native-claude-tool.errorReported_284ccd"
                          : "native-claude-tool.noErrorReported_71376f",
                    )}
                  </>
                ),
              }}
            />
          </p>
          {result.non_execution ? (
            <p>
              {copy(
                result.non_execution.non_execution_kind === "user-rejected"
                  ? "native-claude-tool.theOriginalUserRejectionPreventedThis_97b97e"
                  : "native-claude-tool.nativePermissionPolicyPreventedThisTool_8e5f57",
              )}
            </p>
          ) : null}
        </>
      ) : (
        <p>{copy("native-claude-tool.noToolResultHasBeenObserved_39f5c0")}</p>
      )}
      <Details>
        <Literal
          value={value.command}
          label={copy("native-shell.command_713166")}
        />
        <p>
          <LocalizedText
            id="native-claude-tool.providerBlockCaller_c07946"
            components={{
              s0: <>{r.index + 1}</>,
              s1: (
                <>
                  {copy(
                    r.caller === null
                      ? "native-claude-tool.notReported_adadfa"
                      : "native-claude-tool.direct_002c7c",
                  )}
                </>
              ),
            }}
          />
        </p>
        <section aria-label={copy("native-claude-tool.initialInput_f6c601")}>
          <Literal value={r.initial_input} />
        </section>
        {r.input_delta !== null ? (
          <section aria-label={copy("native-claude-tool.streamedInput_bafbd1")}>
            <Literal value={r.input_delta} />
          </section>
        ) : null}
        {r.proposal ? (
          <>
            <section
              aria-label={copy(
                "native-claude-tool.originalProposedInput_0ecc84",
              )}
            >
              <Literal value={r.proposal.proposed} />
            </section>
            <section
              aria-label={copy("native-claude-tool.nativeAppliedInput_6397df")}
            >
              <Literal value={r.proposal.applied} />
            </section>
          </>
        ) : null}
        {result?.structured != null ? (
          <section
            aria-label={copy(
              "native-claude-tool.structuredNativeResult_762fc2",
            )}
          >
            <Literal value={result.structured} />
          </section>
        ) : null}
        <p>
          {copy(
            "native-claude-tool.proposalCompletionDoesNotEstablishApproval_0d9759",
          )}
        </p>
      </Details>
    </>
  );
}
function ShellBody({
  value,
}: {
  value: Extract<
    NativeCommandPresentation,
    { adapter: CommandAdapter.OpenCode }
  >;
}) {
  const latest = value.snapshots.at(-1)!;
  const output =
    latest.status === "completed"
      ? latest.output
      : latest.status === "failed"
        ? latest.error
        : latest.preview;
  return (
    <>
      {output !== undefined ? (
        <Literal
          value={output}
          label={copy(
            latest.status === "failed"
              ? "native-shell.shellError_662943"
              : "native-shell.shellResult_8e738a",
          )}
        />
      ) : (
        <MissingOutput />
      )}
      {latest.exit !== undefined ? (
        <p>
          <LocalizedText
            id="native-shell.nativeExitCode_991101"
            components={{
              s0: (
                <>
                  {latest.exit === null
                    ? copy("native-shell.unavailable_ca1844")
                    : latest.exit}
                </>
              ),
            }}
          />
        </p>
      ) : null}
      {latest.truncated === true ? (
        <p>{copy("native-shell.theNativeShellResultIsTruncated_e8b1dd")}</p>
      ) : null}
      {latest.interrupted === true ? (
        <p>
          <LocalizedText
            id="native-shell.nativeInterruption_edde92"
            components={{ s0: <>{copy("native-shell.observed_64fa8a")}</> }}
          />
        </p>
      ) : null}
      {latest.outputPath !== undefined ? (
        <p>
          <LocalizedText
            id="native-shell.nativeSavedOutputPath_6ce1ca"
            components={{ s0: <br />, s1: <code>{latest.outputPath}</code> }}
          />
        </p>
      ) : null}
      <Details>
        <Literal
          value={value.command}
          label={copy("native-shell.command_713166")}
        />
        {latest.input.workdir !== undefined ? (
          <section aria-label={copy("native-shell.requestedDirectory_3fa8da")}>
            <Literal value={latest.input.workdir} />
          </section>
        ) : null}
        {latest.input.timeout !== undefined ? (
          <p>
            {copy("native-shell.requestedTimeoutMs_50df7a")}:{" "}
            {latest.input.timeout}
          </p>
        ) : null}
        {latest.title !== undefined ? <p>{latest.title}</p> : null}
        {latest.truncated === false ? (
          <p>{copy("native-shell.theNativeShellResultIsNot_9537c3")}</p>
        ) : null}
        {latest.interrupted === false ? (
          <p>
            <LocalizedText
              id="native-shell.nativeInterruption_edde92"
              components={{
                s0: <>{copy("native-shell.notObserved_1d3efc")}</>,
              }}
            />
          </p>
        ) : null}
        <section
          aria-label={copy(
            "native-shell.originalProposalAndObservations_8d63b6",
          )}
        >
          <ol>
            {value.snapshots.map((s, i) => (
              <li key={i}>
                <small>{statusLabel(s.status)}</small>
                <Literal value={JSON.stringify(s.input, null, 2)} />
                {s.raw !== undefined ? <Literal value={s.raw} /> : null}
                {s.preview !== undefined ? <Literal value={s.preview} /> : null}
              </li>
            ))}
          </ol>
        </section>
      </Details>
    </>
  );
}
export function NativeCommandBody({
  value,
}: {
  value: NativeCommandPresentation;
}) {
  return value.adapter === CommandAdapter.Codex ? (
    <CodexBody value={value} />
  ) : value.adapter === CommandAdapter.Claude ? (
    <ClaudeBody value={value} />
  ) : (
    <ShellBody value={value} />
  );
}
