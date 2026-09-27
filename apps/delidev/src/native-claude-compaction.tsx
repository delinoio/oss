import { object, type Document } from "./documents";

const exact = (v: Document, keys: string[]) => Object.keys(v).length === keys.length && keys.every((key) => Object.hasOwn(v, key));
const nativeID = (v: unknown): v is string => typeof v === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-[47][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(v);
const count = (v: unknown) => typeof v === "string" && /^(0|[1-9][0-9]{0,19})$/.test(v) && BigInt(v) <= 18446744073709551615n;
const text = (v: unknown): v is string => typeof v === "string" && !v.includes("\0") && !/[\uD800-\uDFFF]/u.test(v) && new TextEncoder().encode(v).length <= (256 << 10);
const optional = (v: Document, keys: string[]) => keys.filter((key) => Object.hasOwn(v, key));
const identities = (v: unknown, anchor: unknown) => Array.isArray(v) && v.length > 0 && v.length <= 4096 && v.every((id) => nativeID(id) && id !== anchor) && new Set(v).size === v.length;

export function validClaudeCompactionBoundary(value: unknown): boolean {
  const v = object(value), counts = ["post_tokens", "duration_ms", "cumulative_dropped_tokens"];
  if (!exact(v, ["trigger", "pre_tokens", ...optional(v, [...counts, "logical_parent_uuid", "preserved_segment", "preserved_messages"])]) || (v.trigger !== "auto" && v.trigger !== "manual") || !count(v.pre_tokens) || optional(v, counts).some((key) => !count(v[key])) || (Object.hasOwn(v, "logical_parent_uuid") && !nativeID(v.logical_parent_uuid))) return false;
  const segment = object(v.preserved_segment), messages = object(v.preserved_messages);
  if (Object.hasOwn(v, "preserved_segment") && (!exact(segment, ["head_uuid", "anchor_uuid", "tail_uuid"]) || ![segment.head_uuid, segment.anchor_uuid, segment.tail_uuid].every(nativeID) || segment.head_uuid === segment.anchor_uuid || segment.tail_uuid === segment.anchor_uuid)) return false;
  if (Object.hasOwn(v, "preserved_messages") && (!exact(messages, ["anchor_uuid", "uuids", ...optional(messages, ["all_uuids"])]) || !nativeID(messages.anchor_uuid) || !identities(messages.uuids, messages.anchor_uuid) || Object.hasOwn(messages, "all_uuids") && !identities(messages.all_uuids, messages.anchor_uuid) || Object.hasOwn(v, "preserved_segment") && segment.anchor_uuid !== messages.anchor_uuid)) return false;
  return true;
}

export function validClaudeCompactionSummary(value: unknown): boolean {
  const v = object(value);
  if (!exact(v, ["boundary_native_id", "boundary_message_id", "text", "blocks"]) || !nativeID(v.boundary_native_id) || !nativeID(v.boundary_message_id) || v.boundary_message_id[14] !== "7") return false;
  if (v.text !== null) return text(v.text) && v.blocks === null;
  if (!Array.isArray(v.blocks) || v.blocks.length === 0 || v.blocks.length > 128) return false;
  let bytes = 0;
  for (const value of v.blocks) {
    const block = object(value);
    if (!exact(block, ["kind", "text"]) || block.kind !== "text" || !text(block.text)) return false;
    bytes += new TextEncoder().encode(block.text).length;
  }
  return bytes <= (256 << 10);
}

export function NativeClaudeCompactionBoundary({ value }: { value: unknown }) {
  const v = object(value);
  return <>
    <dl><dt>Compaction trigger</dt><dd>{v.trigger === "auto" ? "Automatic" : "Manual"}</dd><dt>Tokens before compaction</dt><dd>{v.pre_tokens as string}</dd>
      <dt>Tokens after compaction</dt><dd>{v.post_tokens as string ?? "Not reported"}</dd><dt>Duration (milliseconds)</dt><dd>{v.duration_ms as string ?? "Not reported"}</dd><dt>Cumulative dropped tokens</dt><dd>{v.cumulative_dropped_tokens as string ?? "Not reported"}</dd></dl>
    <details><summary>Original context references</summary><pre>{JSON.stringify({ logical_parent_uuid: v.logical_parent_uuid, preserved_segment: v.preserved_segment, preserved_messages: v.preserved_messages }, null, 2)}</pre></details>
    <p>Context counts are separate from provider usage and billed cost. This boundary does not delete the retained conversation or confirm input completion.</p>
  </>;
}

export function NativeClaudeCompactionSummary({ value }: { value: unknown }) {
  const v = object(value);
  return <details><summary>Original native compaction summary</summary>
    <p>This is context supplied by Claude, not a new user message or a replacement for the retained transcript.</p>
    {v.text !== null ? <pre>{v.text as string}</pre> : (v.blocks as Document[]).map((block, index) => <pre key={index}>{block.text as string}</pre>)}
    <dl><dt>Original boundary</dt><dd>{v.boundary_native_id as string}</dd></dl>
  </details>;
}
