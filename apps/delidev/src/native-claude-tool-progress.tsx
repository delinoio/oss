import { copy, useLocale } from "./localization";
import { object, type Document } from "./documents";

const exact = (v: Document, fields: string[]) => Object.keys(v).length === fields.length && fields.every((key) => Object.hasOwn(v, key));
const uuid = (v: unknown) => typeof v === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(v);
const bounded = (v: unknown, max: number, required = true) => typeof v === "string" && (!required || v.trim().length > 0) && !v.includes("\0") && !/[\uD800-\uDFFF]/u.test(v) && new TextEncoder().encode(v).length <= max;
const tool = (v: Document) => exact(v, ["id", "native_id", "name"]) && uuid(v.id) && bounded(v.native_id, 1024) && bounded(v.name, 256);
const seconds = (v: unknown) => typeof v === "string" && v.length <= 64 && /^(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?$/.test(v) && Number.isFinite(Number(v));

export function validClaudeToolReference(value: unknown): boolean { return tool(object(value)); }

export function validClaudeToolProgress(value: unknown): boolean {
  const v = object(value), ref = object(v.tool);
  if (!exact(v, ["tool", "parent_tool_use_id", "elapsed_time_seconds", "heartbeat", ...(Object.hasOwn(v, "task_id") ? ["task_id"] : []), ...(Object.hasOwn(v, "tool_use_id") ? ["tool_use_id"] : [])]) || (v.task_id !== undefined && !bounded(v.task_id, 1024)) || !tool(ref) || !seconds(v.elapsed_time_seconds) || !(v.heartbeat === null || typeof v.heartbeat === "boolean")) return false;
  if (v.parent_tool_use_id === null) return !Object.hasOwn(v, "tool_use_id");
  if (v.parent_tool_use_id !== ref.native_id || v.heartbeat !== true || Object.hasOwn(v, "task_id") || !bounded(v.tool_use_id, 1024)) return false;
  const prefix = `${ref.native_id}-heartbeat-`, id = v.tool_use_id as string;
  if (!id.startsWith(prefix)) return false;
  const index = id.slice(prefix.length);
  return /^(?:0|[1-9][0-9]*)$/.test(index) && Number(index) <= 4294967295;
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
  useLocale();
  const v = object(value), ref = object(v.tool);
  return <>
    <dl><dt>{copy("native-claude-tool-progress.tool_2e53bd")}</dt><dd>{ref.name as string}</dd>{v.task_id !== undefined ? <><dt>{copy("native-claude-tool-progress.task_4bc74b")}</dt><dd>{v.task_id as string}</dd></> : null}<dt>{copy("native-claude-tool-progress.reportedElapsedSeconds_b10c17")}</dt><dd>{v.elapsed_time_seconds as string}</dd><dt>{copy("native-claude-tool-progress.heartbeat_9df894")}</dt><dd>{v.heartbeat === null ? copy("native-claude-tool-progress.notReported_adadfa") : v.heartbeat ? copy("native-claude-tool-progress.reported_34540b") : copy("native-claude-tool-progress.explicitlyFalse_ae527b")}</dd></dl>
    {v.tool_use_id !== undefined ? <details><summary>{copy("native-claude-tool-progress.originalHeartbeatReferences_7ce3e3")}</summary><dl><dt>{copy("native-claude-tool-progress.progressIdentity_280f08")}</dt><dd>{v.tool_use_id as string}</dd><dt>{copy("native-claude-tool-progress.owningToolIdentity_ed9c5e")}</dt><dd>{v.parent_tool_use_id as string}</dd></dl></details> : null}
    <p>{copy("native-claude-tool-progress.toolProgressDoesNotConfirmCompletion_43ed36")}</p>
  </>;
}

export function NativeClaudeToolSummary({ value }: { value: unknown }) {
  useLocale();
  const v = object(value);
  return <details><summary>{copy("native-claude-tool-progress.originalToolSummary_064588")}</summary><pre>{v.summary as string}</pre><ul>{(v.preceding_tools as Document[]).map((ref) => <li key={ref.id as string}>{ref.name as string}</li>)}</ul><p>{copy("native-claude-tool-progress.thisSummaryDoesNotConfirmTool_e61647")}</p></details>;
}
