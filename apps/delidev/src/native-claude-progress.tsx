import { object, type Document } from "./documents";
import { NativeClaudeAPIRetry, validClaudeAPIRetry } from "./native-claude-retry";

enum Kind { Status = "session-status", Thinking = "thinking-tokens-estimated", Retry = "api-retry" }
enum Permission { Default = "default", Plan = "plan", AcceptEdits = "acceptEdits", DontAsk = "dontAsk", Bypass = "bypassPermissions" }
const nativeID = (v: unknown): v is string => typeof v === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-[47][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(v);
const id = (v: unknown) => nativeID(v) && v[14] === "7";
const exact = (v: Document, fields: string[]) => Object.keys(v).length === fields.length && fields.every((k) => Object.hasOwn(v, k));
const count = (v: unknown) => typeof v === "string" && /^(0|[1-9][0-9]{0,19})$/.test(v) && BigInt(v) <= 18446744073709551615n;
const permission = (v: unknown) => Object.values(Permission).includes(v as Permission);
const errorText = (v: unknown) => typeof v === "string" && !v.includes("\0") && !/[\uD800-\uDFFF]/u.test(v) && new TextEncoder().encode(v).length <= (256 << 10);

function valid(data: Document) {
  const v = object(data.claude_progress);
  if (data.role !== "progress" || data.state !== "complete" || data.text !== "" || data.input_id !== undefined || data.native_parent_id !== undefined || data.phase != null || [data.claude, data.claude_tool, data.claude_interruption, data.tool, data.artifact, data.progress].some((v) => v != null) || !id(data.execution_id) || !id(data.native_thread_id) || !nativeID(data.native_turn_id) || !Number.isSafeInteger(data.first_sequence) || Number(data.first_sequence) <= 0 || data.first_sequence !== data.last_sequence) return false;
  if (!exact(v, ["native_event_id", "kind", "input_accepted", "status", "thinking", ...(Object.hasOwn(v, "api_retry") ? ["api_retry"] : [])]) || !nativeID(v.native_event_id) || data.native_id !== v.native_event_id || v.native_event_id === data.native_turn_id || typeof v.input_accepted !== "boolean") return false;
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
  if (!valid(data)) return <article className="message" aria-label="Claude progress unavailable"><p>The retained Claude progress is unavailable or inconsistent.</p></article>;
  const v = object(data.claude_progress), s = object(v.status), t = object(v.thinking);
  return <article className="message" aria-label="Claude progress observation">
    <header><strong>Claude progress</strong><small>{v.input_accepted ? "After input acceptance" : "Before input acceptance"}</small></header>
    {v.kind === Kind.Retry ? <NativeClaudeAPIRetry value={v.api_retry} /> : v.kind === Kind.Status ? <>
      <dl><dt>Reported status</dt><dd>{s.status === null ? "Status cleared" : s.status === "requesting" ? "Requesting" : "Compacting"}</dd>
        <dt>Reported permission mode</dt><dd>{s.permission === null ? "Not reported" : s.permission as string}</dd>
        {s.compact_result !== null ? <><dt>Compaction result</dt><dd>{s.compact_result === "success" ? "Succeeded" : "Failed"}</dd></> : null}
      </dl>
      {s.compact_error !== null ? <details><summary>Compaction diagnostic</summary><pre>{s.compact_error as string}</pre></details> : null}
      <p>This status does not confirm input acceptance, idle state or execution completion.</p>
    </> : <>
      <dl><dt>Estimated thinking tokens</dt><dd>{t.estimated_tokens as string}</dd><dt>Estimated thinking token delta</dt><dd>{t.estimated_tokens_delta as string}</dd></dl>
      <p>These native progress estimates are separate from provider usage and billed cost.</p>
    </>}
  </article>;
}

export function NativeClaudePermissionProgress({ progress }: { progress: Document }) {
  if (progress.claude_progress == null) return null;
  const state = object(progress.claude_progress);
  const valid = Object.keys(state).every((key) => ["native_turn_id", "latest_status_id", "latest_thinking_id", "latest_retry_id", "permission", "permission_changed"].includes(key)) && nativeID(state.native_turn_id) && (progress.native_turn_id == null || progress.native_turn_id === "" || state.native_turn_id === progress.native_turn_id) && (state.latest_status_id === undefined || id(state.latest_status_id)) && (state.latest_thinking_id === undefined || id(state.latest_thinking_id)) && (state.latest_retry_id === undefined || id(state.latest_retry_id)) && (state.latest_status_id !== undefined || state.latest_thinking_id !== undefined || state.latest_retry_id !== undefined) && (state.permission === undefined || permission(state.permission) && state.latest_status_id !== undefined) && (state.permission_changed === undefined || typeof state.permission_changed === "boolean" && state.permission !== undefined);
  if (!valid) return <p>Retained Claude permission progress is unavailable or inconsistent.</p>;
  return <section aria-label="Claude permission progress">
    <dl><dt>Latest reported Claude permission</dt><dd>{state.permission === undefined ? "Not reported" : state.permission as string}</dd></dl>
    {state.permission_changed === true ? <p className="notice">Claude changed permission mode during this recorded execution. Its initial settings remain unchanged in this record. Further input requires configuration reconciliation.</p> : null}
  </section>;
}
