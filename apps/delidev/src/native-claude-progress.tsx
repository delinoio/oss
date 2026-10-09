import { Disclosure, DisclosureSummary } from "./disclosure";
import { copy, useLocale } from "./localization";
import { NativeClaudeCompactionBoundary, NativeClaudeCompactionSummary, validClaudeCompactionBoundary, validClaudeCompactionSummary } from "./native-claude-compaction";
import { NativeClaudeTask, validClaudeTask } from "./native-claude-tasks";
import { NativeClaudeToolProgress, NativeClaudeToolSummary, validClaudeToolProgress, validClaudeToolSummary } from "./native-claude-tool-progress";
import { object, type Document } from "./documents";
import { NativeClaudeAPIRetry, validClaudeAPIRetry } from "./native-claude-retry";

enum Kind { Compaction = "compaction-boundary", CompactionSummary = "compaction-summary", Task = "task-lifecycle", Status = "session-status", Thinking = "thinking-tokens-estimated", Retry = "api-retry", Tool = "tool-progress", ToolSummary = "tool-summary" }
enum Permission { Default = "default", Plan = "plan", AcceptEdits = "acceptEdits", DontAsk = "dontAsk", Bypass = "bypassPermissions", Auto = "auto" }
const nativeID = (v: unknown): v is string => typeof v === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-[47][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(v);
const id = (v: unknown) => nativeID(v) && v[14] === "7";
const exact = (v: Document, fields: string[]) => Object.keys(v).length === fields.length && fields.every((k) => Object.hasOwn(v, k));
const count = (v: unknown) => typeof v === "string" && /^(0|[1-9][0-9]{0,19})$/.test(v) && BigInt(v) <= 18446744073709551615n;
const permission = (v: unknown) => Object.values(Permission).includes(v as Permission);
const errorText = (v: unknown) => typeof v === "string" && !v.includes("\0") && !/[\uD800-\uDFFF]/u.test(v) && new TextEncoder().encode(v).length <= (256 << 10);

function valid(data: Document) {
  const v = object(data.claude_progress);
  if (data.role !== "progress" || data.state !== "complete" || data.text !== "" || data.input_id !== undefined || data.native_parent_id !== undefined || data.phase != null || [data.claude, data.claude_tool, data.claude_interruption, data.tool, data.artifact, data.progress].some((v) => v != null) || !id(data.execution_id) || !id(data.native_thread_id) || !nativeID(data.native_turn_id) || !Number.isSafeInteger(data.first_sequence) || Number(data.first_sequence) <= 0 || data.first_sequence !== data.last_sequence) return false;
  if (!exact(v, ["native_event_id", "kind", "input_accepted", "status", "thinking", ...["api_retry", "tool", "tool_summary", "task", "compaction", "compaction_summary"].filter((key) => Object.hasOwn(v, key))]) || !nativeID(v.native_event_id) || data.native_id !== v.native_event_id || v.native_event_id === data.native_turn_id || typeof v.input_accepted !== "boolean") return false;
  if (v.kind === Kind.Compaction || v.kind === Kind.CompactionSummary) {
    if (v.status !== null || v.thinking !== null || ["api_retry", "tool", "tool_summary", "task"].some((key) => Object.hasOwn(v, key))) return false;
    return v.kind === Kind.Compaction ? v.compaction_summary === undefined && validClaudeCompactionBoundary(v.compaction) : v.compaction === undefined && validClaudeCompactionSummary(v.compaction_summary) && object(v.compaction_summary).boundary_native_id !== v.native_event_id;
  }
  if (v.compaction !== undefined || v.compaction_summary !== undefined) return false;
  if (v.kind === Kind.Task) return v.input_accepted === true && v.status === null && v.thinking === null && v.api_retry === undefined && v.tool === undefined && v.tool_summary === undefined && validClaudeTask(v.task);
  if (v.task !== undefined) return false;
  if (v.kind === Kind.Tool) return v.input_accepted === true && v.status === null && v.thinking === null && v.api_retry === undefined && v.tool_summary === undefined && validClaudeToolProgress(v.tool);
  if (v.kind === Kind.ToolSummary) return v.input_accepted === true && v.status === null && v.thinking === null && v.api_retry === undefined && v.tool === undefined && validClaudeToolSummary(v.tool_summary);
  if (v.tool !== undefined || v.tool_summary !== undefined) return false;
  if (v.kind === Kind.Retry) return v.status === null && v.thinking === null && validClaudeAPIRetry(v.api_retry) && object(v.api_retry).native_event_id === v.native_event_id;
  if (v.api_retry !== undefined) return false;
  if (v.kind === Kind.Status) {
    const s = object(v.status);
    return v.thinking === null && exact(s, ["status", "permission", "compact_result", "compact_error"]) && (s.status === null || s.status === "requesting" || s.status === "compacting") && (s.permission === null || permission(s.permission)) && (s.compact_result === null || s.compact_result === "success" || s.compact_result === "failed") && (s.compact_error === null || errorText(s.compact_error));
  }
  const t = object(v.thinking);
  return v.kind === Kind.Thinking && v.input_accepted === true && v.status === null && exact(t, ["estimated_tokens", "estimated_tokens_delta"]) && count(t.estimated_tokens) && count(t.estimated_tokens_delta);
}

export function NativeClaudeProgress({ data }: { data: Document }) {
  useLocale();
  if (!valid(data)) return <article className="message" aria-label={copy("native-claude-progress.claudeProgressUnavailable_b578af")}><p>{copy("native-claude-progress.theRetainedClaudeProgressIsUnavailable_f7ac03")}</p></article>;
  const v = object(data.claude_progress), s = object(v.status), t = object(v.thinking);
  return <article className="message" aria-label={copy("native-claude-progress.claudeProgressObservation_c7a8db")}>
    <header><strong>{copy("native-claude-progress.claudeProgress_6fd5f4")}</strong><small>{v.input_accepted ? copy("native-claude-progress.afterInputAcceptance_048042") : copy("native-claude-progress.beforeInputAcceptance_d6a50c")}</small></header>
    {v.kind === Kind.Compaction ? <NativeClaudeCompactionBoundary value={v.compaction} /> : v.kind === Kind.CompactionSummary ? <NativeClaudeCompactionSummary value={v.compaction_summary} /> : v.kind === Kind.Task ? <NativeClaudeTask value={v.task} /> : v.kind === Kind.Tool ? <NativeClaudeToolProgress value={v.tool} /> : v.kind === Kind.ToolSummary ? <NativeClaudeToolSummary value={v.tool_summary} /> : v.kind === Kind.Retry ? <NativeClaudeAPIRetry value={v.api_retry} /> : v.kind === Kind.Status ? <>
      <dl><dt>{copy("native-claude-progress.reportedStatus_e8dbb8")}</dt><dd>{s.status === null ? copy("native-claude-progress.statusCleared_0836e6") : s.status === "requesting" ? copy("native-claude-progress.requesting_4ba9d8") : copy("native-claude-progress.compacting_df7779")}</dd>
        <dt>{copy("native-claude-progress.reportedPermissionMode_eb94a5")}</dt><dd>{s.permission === null ? copy("native-claude-progress.notReported_adadfa") : s.permission as string}</dd>
        {s.compact_result !== null ? <><dt>{copy("native-claude-progress.compactionResult_5a6b3c")}</dt><dd>{s.compact_result === "success" ? copy("native-claude-progress.succeeded_6d9a6f") : copy("native-claude-progress.failed_031a8f")}</dd></> : null}
      </dl>
      {s.compact_error !== null ? <Disclosure><DisclosureSummary>{copy("native-claude-progress.compactionDiagnostic_f316f6")}</DisclosureSummary><pre>{s.compact_error as string}</pre></Disclosure> : null}
      <p>{copy("native-claude-progress.thisStatusDoesNotConfirmInput_8c3d56")}</p>
    </> : <>
      <dl><dt>{copy("native-claude-progress.estimatedThinkingTokens_8bcb46")}</dt><dd>{t.estimated_tokens as string}</dd><dt>{copy("native-claude-progress.estimatedThinkingTokenDelta_4b1b48")}</dt><dd>{t.estimated_tokens_delta as string}</dd></dl>
      <p>{copy("native-claude-progress.theseNativeProgressEstimatesAreSeparate_a3ca3d")}</p>
    </>}
  </article>;
}

export function NativeClaudePermissionProgress({ progress }: { progress: Document }) {
  useLocale();
  if (progress.claude_progress == null) return null;
  const state = object(progress.claude_progress);
  const valid = Object.keys(state).every((key) => ["native_turn_id", "latest_status_id", "latest_thinking_id", "latest_retry_id", "latest_tool_id", "latest_tool_summary_id", "latest_task_id", "latest_compaction_id", "latest_compaction_summary_id", "permission", "permission_changed"].includes(key)) && nativeID(state.native_turn_id) && (progress.native_turn_id == null || progress.native_turn_id === "" || state.native_turn_id === progress.native_turn_id) && (state.latest_compaction_id === undefined || id(state.latest_compaction_id)) && (state.latest_compaction_summary_id === undefined || id(state.latest_compaction_summary_id)) && (state.latest_status_id === undefined || id(state.latest_status_id)) && (state.latest_thinking_id === undefined || id(state.latest_thinking_id)) && (state.latest_retry_id === undefined || id(state.latest_retry_id)) && (state.latest_task_id === undefined || id(state.latest_task_id)) && (state.latest_tool_id === undefined || id(state.latest_tool_id)) && (state.latest_tool_summary_id === undefined || id(state.latest_tool_summary_id)) && (state.latest_compaction_id !== undefined || state.latest_compaction_summary_id !== undefined || state.latest_task_id !== undefined || state.latest_status_id !== undefined || state.latest_thinking_id !== undefined || state.latest_retry_id !== undefined || state.latest_tool_id !== undefined || state.latest_tool_summary_id !== undefined) && (state.permission === undefined || permission(state.permission) && state.latest_status_id !== undefined) && (state.permission_changed === undefined || typeof state.permission_changed === "boolean" && state.permission !== undefined);
  if (!valid) return <p>{copy("native-claude-progress.retainedClaudePermissionProgressIsUnavailable_647e88")}</p>;
  return <section aria-label={copy("native-claude-progress.claudePermissionProgress_cad9ea")}>
    <dl><dt>{copy("native-claude-progress.latestReportedClaudePermission_506a56")}</dt><dd>{state.permission === undefined ? copy("native-claude-progress.notReported_adadfa") : state.permission as string}</dd></dl>
    {state.permission_changed === true ? <p className="notice">{copy("native-claude-progress.claudeChangedPermissionModeDuringThis_7b8108")}</p> : null}
  </section>;
}
