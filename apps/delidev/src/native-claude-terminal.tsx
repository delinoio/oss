import { object, type Document } from "./documents";

enum ResultKind { Success = "success", Error = "error_during_execution", Turns = "error_max_turns", Budget = "error_max_budget_usd", Structured = "error_max_structured_output_retries" }
const failures = new Set(["blocking_limit", "rapid_refill_breaker", "prompt_too_long", "image_error", "model_error", "api_error", "malformed_tool_use_exhausted", "budget_exhausted", "structured_output_retry_exhausted", "tool_deferred_unavailable", "turn_setup_failed"]);
const ordinary = new Set(["completed", "stop_hook_prevented", "hook_stopped", "tool_deferred", "max_turns", "background_requested"]);
const nativeID = (v: unknown): v is string => typeof v === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-[47][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(v);
const id = (v: unknown) => nativeID(v) && v[14] === "7";

function outcome(progress: Document): string | undefined {
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
  if (progress.claude_terminal == null) return null;
  const result = outcome(progress), v = object(progress.claude_terminal);
  if (!result) return <p>The retained Claude terminal evidence is unavailable or inconsistent.</p>;
  return <section aria-label="Claude original input outcome">
    <h4>Original Claude input result</h4>
    <dl><dt>Native input outcome</dt><dd>{result === "succeeded" ? "Succeeded" : result === "stopped" ? "Stopped" : "Failed"}</dd>
      <dt>Native result reason</dt><dd>{v.reason as string}</dd><dt>Native command</dt><dd>{v.command === "completed" ? "Completed" : "Cancelled"}</dd>
      <dt>Native loop</dt><dd>Idle observed</dd><dt>Owned process and workspace cleanup</dt><dd>{progress.cleanup_verified === true ? "Confirmed" : "Not confirmed"}</dd>
    </dl>
    <p>This records the original input outcome. Session Stop or recovery remains separate. Completion does not authorize another input.</p>
  </section>;
}
