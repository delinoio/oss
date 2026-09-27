import { object, type Document } from "./documents";

const exact = (v: Document, fields: string[]) => Object.keys(v).length === fields.length && fields.every((key) => Object.hasOwn(v, key));
const uuid = (v: unknown) => typeof v === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(v);
const bounded = (v: unknown, max: number, required = true) => typeof v === "string" && (!required || v.trim().length > 0) && !v.includes("\0") && !/[\uD800-\uDFFF]/u.test(v) && new TextEncoder().encode(v).length <= max;
const tool = (v: Document) => exact(v, ["id", "native_id", "name"]) && uuid(v.id) && bounded(v.native_id, 1024) && bounded(v.name, 256);
const seconds = (v: unknown) => typeof v === "string" && v.length <= 64 && /^(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?$/.test(v) && Number.isFinite(Number(v));

export function validClaudeToolProgress(value: unknown): boolean {
  const v = object(value);
  return exact(v, ["tool", "parent_tool_use_id", "elapsed_time_seconds", "heartbeat"]) && tool(object(v.tool)) && v.parent_tool_use_id === null && seconds(v.elapsed_time_seconds) && (v.heartbeat === null || typeof v.heartbeat === "boolean");
}

export function validClaudeToolSummary(value: unknown): boolean {
  const v = object(value);
  if (!exact(v, ["summary", "preceding_tools"]) || !bounded(v.summary, 256 << 10, false) || !Array.isArray(v.preceding_tools) || v.preceding_tools.length === 0 || v.preceding_tools.length > 128) return false;
  const ids = new Set<unknown>(), native = new Set<unknown>();
  for (const value of v.preceding_tools) {
    const ref = object(value);
    if (!tool(ref) || ids.has(ref.id) || native.has(ref.native_id)) return false;
    ids.add(ref.id); native.add(ref.native_id);
  }
  return true;
}

export function NativeClaudeToolProgress({ value }: { value: unknown }) {
  const v = object(value), ref = object(v.tool);
  return <>
    <dl><dt>Tool</dt><dd>{ref.name as string}</dd><dt>Reported elapsed seconds</dt><dd>{v.elapsed_time_seconds as string}</dd><dt>Heartbeat</dt><dd>{v.heartbeat === null ? "Not reported" : v.heartbeat ? "Reported" : "Explicitly false"}</dd></dl>
    <p>Tool progress does not confirm completion, approval or execution success.</p>
  </>;
}

export function NativeClaudeToolSummary({ value }: { value: unknown }) {
  const v = object(value);
  return <details><summary>Original tool summary</summary><pre>{v.summary as string}</pre><ul>{(v.preceding_tools as Document[]).map((ref) => <li key={ref.id as string}>{ref.name as string}</li>)}</ul><p>This summary does not confirm tool completion or approval.</p></details>;
}
