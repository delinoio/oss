import { copy, useLocale } from "./localization";
import { object, type Document } from "./documents";

enum ResultKind { Success = "success", Error = "error_during_execution", Turns = "error_max_turns", Budget = "error_max_budget_usd", Structured = "error_max_structured_output_retries" }
const failures = new Set(["blocking_limit", "rapid_refill_breaker", "prompt_too_long", "image_error", "model_error", "api_error", "malformed_tool_use_exhausted", "budget_exhausted", "structured_output_retry_exhausted", "tool_deferred_unavailable", "turn_setup_failed"]);
const ordinary = new Set(["completed", "stop_hook_prevented", "hook_stopped", "tool_deferred", "max_turns", "background_requested"]);
const nativeID = (v: unknown): v is string => typeof v === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-[47][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(v);
const id = (v: unknown) => nativeID(v) && v[14] === "7";

function outcome(progress: Document): string | undefined {
	if (progress.claude_stop != null || progress.claude_denial != null) return;
  const v = object(progress.claude_terminal);
  const fields = ["input_id", "result_native_id", "command_native_id", "idle_native_id", "kind", "reason", "is_error", "command"];
  if (Object.keys(v).length !== fields.length || !fields.every((key) => Object.hasOwn(v, key)) || !id(v.input_id) || v.input_id !== progress.input_id || !id(progress.execution_id) || !id(progress.native_thread_id) || !nativeID(progress.native_turn_id) || progress.claude_interruption != null || progress.cleanup_verified !== undefined && typeof progress.cleanup_verified !== "boolean") return;
  const ids = [v.result_native_id, v.command_native_id, v.idle_native_id];
  if (!ids.every(nativeID) || new Set([...ids, v.input_id, progress.native_thread_id, progress.native_turn_id]).size !== 6 || !Object.values(ResultKind).includes(v.kind as ResultKind) || typeof v.reason !== "string" || typeof v.is_error !== "boolean") return;
  const failed = failures.has(v.reason), aborted = v.reason === "aborted_streaming" || v.reason === "aborted_tools";
  if (!failed && !aborted && !ordinary.has(v.reason) || (failed || v.kind !== ResultKind.Success) && !v.is_error || v.command !== (failed || aborted ? "cancelled" : "completed")) return;
  if (v.kind === ResultKind.Turns && v.reason !== "max_turns" || v.kind === ResultKind.Budget && v.reason !== "budget_exhausted" || v.kind === ResultKind.Structured && v.reason !== "structured_output_retry_exhausted" && !aborted) return;
  const result = v.kind === ResultKind.Success && !v.is_error ? v.reason === "completed" ? "succeeded" : aborted ? "stopped" : "failed" : "failed";
  return progress.outcome === result ? result : undefined;
}

export function NativeClaudeTerminal({ progress }: { progress: Document }) {
  useLocale();
  if (progress.claude_terminal == null) return null;
  const result = outcome(progress), v = object(progress.claude_terminal);
  if (!result) return <p>{copy("native-claude-terminal.theRetainedClaudeTerminalEvidenceIs_351997")}</p>;
  return <section aria-label={copy("native-claude-terminal.claudeOriginalInputOutcome_d261e9")}>
    <h4>{copy("native-claude-terminal.originalClaudeInputResult_4d612d")}</h4>
    <dl><dt>{copy("native-claude-terminal.nativeInputOutcome_15e4d4")}</dt><dd>{result === "succeeded" ? copy("native-claude-terminal.succeeded_6d9a6f") : result === "stopped" ? copy("native-claude-terminal.stopped_1a4f63") : copy("native-claude-terminal.failed_031a8f")}</dd>
      <dt>{copy("native-claude-terminal.nativeResultReason_8974b9")}</dt><dd>{v.reason as string}</dd><dt>{copy("native-claude-terminal.nativeCommand_29f746")}</dt><dd>{v.command === "completed" ? copy("native-claude-terminal.completed_22a970") : copy("native-claude-terminal.cancelled_d353a9")}</dd>
      <dt>{copy("native-claude-terminal.nativeLoop_fec242")}</dt><dd>{copy("native-claude-terminal.idleObserved_34b00d")}</dd><dt>{copy("native-claude-terminal.ownedProcessAndWorkspaceCleanup_7844eb")}</dt><dd>{progress.cleanup_verified === true ? copy("native-claude-terminal.confirmed_fe00b6") : copy("native-claude-terminal.notConfirmed_bc1c29")}</dd>
    </dl>
    <p>{copy("native-claude-terminal.thisRecordsTheOriginalInputOutcome_3a83a3")}</p>
  </section>;
}
