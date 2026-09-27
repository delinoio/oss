import { object, type Document } from "./documents";
import { validClaudeAPIRetry } from "./native-claude-retry";
import { NativeClaudeProviderUsage, NativeClaudeResultUsage, validClaudeProviderUsage, validClaudeResultUsage } from "./native-claude-usage";

const nativeID = (v: unknown): v is string => typeof v === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-[47][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(v);
const id = (v: unknown) => nativeID(v) && v[14] === "7";
const validText = (v: unknown, limit: number): v is string => typeof v === "string" && !v.includes("\0") && !/[\uD800-\uDFFF]/u.test(v) && new TextEncoder().encode(v).length <= limit;
const fields = ["content_evidence", "request_id", "input_id", "message_id", "native_message_id", "context_native_id", "result_native_id", "command_native_id", "idle_native_id", "text", "context", "native_input_id", "kind", "reason", "is_error", "command", "usage", "partial_usage", "acknowledged", "idle", "cleanup_verified"];

function valid(progress: Document) {
  const v = object(progress.claude_stop);
  const optional = ["block_stop_native_id", "message_stop_native_id", "interrupted_native_id", "retries"].filter((key) => Object.hasOwn(v, key));
  if (Object.keys(v).length !== fields.length + optional.length || !fields.every((key) => Object.hasOwn(v, key)) || !optional.every((key) => key === "retries" || nativeID(v[key])) || v.message_stop_native_id !== undefined && v.block_stop_native_id === undefined || ![v.request_id, v.input_id, v.message_id, progress.execution_id, progress.native_thread_id].every(id) || !nativeID(progress.native_turn_id) || v.input_id !== progress.input_id || progress.outcome !== "stopped" || progress.claude_terminal != null || progress.claude_denial != null || progress.claude_interruption != null || progress.cleanup_verified !== undefined && typeof progress.cleanup_verified !== "boolean") return false;
  const original = [ v.context_native_id, v.result_native_id, v.command_native_id, v.idle_native_id];
  const retries = v.retries === undefined ? [] : v.retries;
  if (!Array.isArray(retries) || retries.length > 128 || v.retries !== undefined && retries.length === 0 || !retries.every(validClaudeAPIRetry)) return false;
  if (v.content_evidence === "aborted-assistant" ? !nativeID(v.interrupted_native_id) : v.content_evidence !== "closed-stream-before-retry" || v.interrupted_native_id !== undefined || !nativeID(v.block_stop_native_id) || !nativeID(v.message_stop_native_id) || retries.length === 0 || v.partial_usage !== null) return false;
  const identities = [...original, ...optional.filter((key) => key !== "retries").map((key) => v[key]), ...retries.map((r) => object(r).native_event_id), v.request_id, v.input_id, v.message_id, progress.native_thread_id, progress.native_turn_id];
  return original.every(nativeID) && new Set(identities).size === identities.length && !identities.includes(v.native_message_id) && validText(v.native_message_id, 1024) && v.native_message_id.trim() !== "" && validText(v.text, 256 * 1024) && v.context === "[Request interrupted by user]" && v.native_input_id === null && v.kind === "error_during_execution" && v.reason === "aborted_streaming" && v.is_error === true && v.command === "cancelled" && v.acknowledged === true && v.idle === true && v.cleanup_verified === true && validClaudeResultUsage(v.usage) && (v.partial_usage === null || validClaudeProviderUsage(v.partial_usage)) && new TextEncoder().encode(JSON.stringify([v.usage, v.partial_usage])).length <= (1 << 20);
}

export function NativeClaudeStop({ progress }: { progress: Document }) {
  if (progress.claude_stop == null) return null;
  if (!valid(progress)) return <p>The retained Claude Stop evidence is unavailable or inconsistent.</p>;
  const v = object(progress.claude_stop);
  return <section aria-label="Claude original Stop">
    <h4>Claude Stop observed</h4>
    <p>The response was interrupted. Claude reported a session result without an input result identity.</p>
    <dl><dt>Native interrupt request</dt><dd>Acknowledged</dd><dt>Native loop</dt><dd>Idle observed</dd>
      <dt>Owned native process cleanup</dt><dd>Confirmed</dd><dt>Worker workspace cleanup report</dt><dd>{progress.cleanup_verified === true ? "Confirmed" : "Not confirmed"}</dd></dl>
    {v.content_evidence === "closed-stream-before-retry" ? <p>The stream closed before retry wait was interrupted. The transcript preserves the last streamed text; Claude did not emit a final partial-response snapshot.</p> : <p>The transcript preserves Claude’s original aborted response snapshot.</p>}
    {Array.isArray(v.retries) ? <details><summary>Native retry observations</summary><ol>{v.retries.map((entry, index) => { const r = object(entry); return <li key={index}>Attempt {r.attempt as string} of {r.max_retries as string}; delay {r.retry_delay_ms as string} ms; {r.error as string}; HTTP {r.error_status === null ? "unavailable" : String(r.error_status)}.</li>; })}</ol><p>These are observations from the stopped native run. No new request is authorized.</p></details> : null}
    <details><summary>Original interruption context</summary><pre>{v.context as string}</pre></details>
    <details><summary>Native interruption usage</summary><NativeClaudeResultUsage value={v.usage} />
      {v.partial_usage !== null ? <><h4>Interrupted response report</h4><NativeClaudeProviderUsage value={v.partial_usage} /></> : <p>Interrupted response usage unavailable.</p>}
      <p>These reports overlap and do not establish billed cost or an input outcome.</p></details>
    <p>Original history must be reconciled before another input.</p>
  </section>;
}
