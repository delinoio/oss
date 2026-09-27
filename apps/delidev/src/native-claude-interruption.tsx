import { object, type Document } from "./documents";
import { NativeClaudeResultUsage, validClaudeResultUsage } from "./native-claude-usage";

enum Kind { Context = "denial-context", Result = "denial-session-result" }
const nativeID = (v: unknown): v is string => typeof v === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-[47][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(v);
const id = (v: unknown) => nativeID(v) && v[14] === "7";
const exact = (v: Document, fields: string[]) => Object.keys(v).length === fields.length && fields.every((k) => Object.hasOwn(v, k));

function valid(data: Document) {
  const v = object(data.claude_interruption);
  if (data.role !== "progress" || data.state !== "complete" || data.text !== "" || data.input_id !== undefined || data.native_parent_id !== undefined || data.phase != null || [data.claude, data.claude_tool, data.tool, data.artifact, data.progress].some((v) => v != null) || !id(data.execution_id) || !id(data.native_thread_id) || !nativeID(data.native_turn_id) || !Number.isSafeInteger(data.first_sequence) || Number(data.first_sequence) <= 0 || data.first_sequence !== data.last_sequence) return false;
  if (!exact(v, ["kind", "native_event_id", "interaction_id", "arrival_id", "tool_message_id", "tool_result_native_id", "context", "result"]) || !nativeID(v.native_event_id) || data.native_id !== v.native_event_id || !nativeID(v.tool_result_native_id) || v.native_event_id === v.tool_result_native_id || ![v.interaction_id, v.arrival_id, v.tool_message_id].every(id)) return false;
  if (v.kind === Kind.Context) return v.context === "[Request interrupted by user for tool use]" && v.result === null;
  const r = object(v.result);
  return v.kind === Kind.Result && v.context === null && exact(r, ["kind", "reason", "is_error", "native_input_id", "usage"]) && r.kind === "error_during_execution" && r.reason === "aborted_tools" && r.is_error === true && r.native_input_id === null && validClaudeResultUsage(r.usage) && new TextEncoder().encode(JSON.stringify(r.usage)).length <= (1 << 20);
}

export function NativeClaudeInterruption({ data }: { data: Document }) {
  if (!valid(data)) return <article className="message" aria-label="Claude interruption unavailable"><p>The retained Claude interruption is unavailable or inconsistent.</p></article>;
  const value = object(data.claude_interruption);
  return <article className="message" aria-label="Claude interruption observation">
    <header><strong>Claude interruption</strong><small>{value.kind === Kind.Context ? "Original context" : "Session result observed"}</small></header>
    {value.kind === Kind.Context ? <><pre>{value.context as string}</pre><p>Claude added this context after processing the original denial.</p></> : <>
      <p>Claude stopped after the denied request. It did not report an input result identity.</p>
      <p>Input completion and process cleanup remain unconfirmed. Further input is paused for reconciliation.</p>
      <details><summary>Native session-result usage</summary><NativeClaudeResultUsage value={object(value.result).usage} /><p>These overlapping native reports do not establish billed cost or an input outcome. Cumulative values belong to the original runtime.</p></details>
    </>}
  </article>;
}
