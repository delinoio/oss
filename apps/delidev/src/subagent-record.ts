// SPDX-License-Identifier: Apache-2.0
import { EntityKind, type Resource } from "@delinoio/delidev-api-client";
import { object, type Document } from "./documents";
import { claudeToolReference } from "./native-claude-tool";
import { validClaudeProviderUsage } from "./native-claude-usage";

enum Source { Collaboration = "codex-collaboration", Activity = "codex-activity", CodexHistory = "codex-history", Task = "claude-task", Content = "claude-content", ClaudeHistory = "claude-history" }
enum Status { Pending = "pending", Running = "running", Paused = "paused", Completed = "completed", Failed = "failed", Interrupted = "interrupted", Shutdown = "shutdown", Unavailable = "unavailable" }
type Guard = (value: unknown) => boolean;
export type SubagentRow = { resource: Resource; record: Document; child: Document };
const uintMax = 18446744073709551615n, intMax = 9223372036854775807n;
const uuid: Guard = v => typeof v === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(v);
const record = (v: unknown): v is Document => v !== null && typeof v === "object" && !Array.isArray(v);
const nullable = (guard: Guard): Guard => v => v === null || guard(v);
function text(v: unknown, max = 1024): v is string { return typeof v === "string" && v.length <= max && !v.includes("\0") && !/[\uD800-\uDFFF]/u.test(v) && new TextEncoder().encode(v).length <= max; }
const label = (v: unknown, max = 1024): v is string => text(v, max) && v.trim().length > 0;
function count(v: unknown, max = uintMax): v is string { return typeof v === "string" && /^(0|[1-9][0-9]{0,19})$/.test(v) && BigInt(v) <= max; }
const sequence: Guard = v => count(v) && v !== "0";
function shape(v: unknown, fields: Record<string, Guard>, required: string[] = []): v is Document {
  return record(v) && required.every(key => Object.hasOwn(v, key)) && Object.entries(v).every(([key, item]) => Object.hasOwn(fields, key) && fields[key]!(item));
}

// Validate syntax and duplicate keys before projecting. Numeric native report
// tokens become temporary decimal strings; never round or rewrite retained JSON.
function parse(raw: string, nativeNumbers = false): unknown {
  JSON.parse(raw);
  const tokens = raw.match(/"(?:[^"\\]|\\.)*"|-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?|[{}[\]:,]|true|false|null/g) ?? [];
  const stack: (Set<string> | null)[] = [];
  for (let i = 0; i < tokens.length; i++) {
    const token = tokens[i]!;
    if (token === "{" || token === "[") { stack.push(token === "{" ? new Set() : null); if (stack.length > 64) throw new Error("depth"); }
    else if (token === "}" || token === "]") stack.pop();
    else if (token.startsWith('"') && tokens[i + 1] === ":") {
      const key = JSON.parse(token) as string, keys = stack.at(-1);
      if (!keys || keys.has(key)) throw new Error("duplicate");
      keys.add(key);
    }
  }
  return JSON.parse(nativeNumbers ? tokens.map(token => /^-?[0-9]/.test(token) ? JSON.stringify(token) : token).join("") : raw);
}
const nativeCounters = new Set(["inputTokens", "cachedInputTokens", "cacheWriteInputTokens", "outputTokens", "reasoningOutputTokens", "totalTokens", "modelContextWindow", "total_tokens", "tool_uses", "duration_ms", "input_tokens", "output_tokens", "cache_creation_input_tokens", "cache_read_input_tokens", "ephemeral_1h_input_tokens", "ephemeral_5m_input_tokens", "thinking_tokens", "web_search_requests", "web_fetch_requests"]);
function numericCounterTypes(v: unknown): boolean {
  if (Array.isArray(v)) return v.every(numericCounterTypes);
  return !record(v) ? typeof v !== "number" : Object.entries(v).every(([key, value]) => nativeCounters.has(key) ? value === null || typeof value === "number" : typeof value !== "number" && numericCounterTypes(value));
}
function usage(v: unknown, source: Source): boolean {
  if (!shape(v, { scope: s => s === "child-cumulative" || s === "child-response", total: nullable(count), input: nullable(count), output: nullable(count), native_report: s => label(s, 64 << 10) }, ["scope", "total", "input", "output", "native_report"])) return false;
  let report: Document;
  try { if (!numericCounterTypes(JSON.parse(v.native_report as string))) return false; report = object(parse(v.native_report as string, true)); } catch { return false; }
  if (source === Source.Task) {
    return v.scope === "child-cumulative" && shape(report, { total_tokens: count, tool_uses: count, duration_ms: count }, ["total_tokens", "tool_uses", "duration_ms"]) && v.total === report.total_tokens && v.input === null && v.output === null;
  }
  if (source === Source.Content || source === Source.ClaudeHistory) {
    return v.scope === "child-response" && validClaudeProviderUsage(report) && v.total === null && v.input === (report.input_tokens ?? null) && v.output === (report.output_tokens ?? null);
  }
  if (source !== Source.CodexHistory || v.scope !== "child-cumulative") return false;
  const signed: Guard = n => count(n, intMax);
  const counts: Guard = n => shape(n, { inputTokens: signed, cachedInputTokens: signed, cacheWriteInputTokens: nullable(signed), outputTokens: signed, reasoningOutputTokens: signed, totalTokens: signed }, ["inputTokens", "cachedInputTokens", "outputTokens", "reasoningOutputTokens", "totalTokens"]);
  return shape(report, { total: counts, last: counts, modelContextWindow: nullable(n => signed(n) && n !== "0") }, ["total", "last"]) && v.total === object(report.total).totalTokens && v.input === object(report.total).inputTokens && v.output === object(report.total).outputTokens;
}
function sameUsage(a: unknown, b: unknown) {
  const one = object(a), two = object(b);
  return ["scope", "total", "input", "output", "native_report"].every(key => one[key] === two[key]);
}
const output: Guard = v => shape(v, { native_message_id: label, text: t => text(t, 256 << 10), partial: p => p === true, blocks: b => Array.isArray(b) && b.length <= 128 && b.every(item => shape(item, { kind: k => label(k, 256), text: nullable(t => text(t, 256 << 10)) }, ["kind", "text"])) }, ["native_message_id", "text", "partial"]);
const model: Guard = v => label(v, 256);
const tools: Guard = v => Array.isArray(v) && v.length <= 128 && v.every(t => shape(t, { native_id: label, name: n => label(n, 256), requested_model: model }, ["native_id", "name"])) && new Set(v.map(t => object(t).native_id)).size === v.length;
const task: Guard = v => shape(v, { agent_type: nullable(t => text(t, 256 << 10)), description: nullable(t => text(t, 256 << 10)), depth: nullable(n => Number.isInteger(n) && Number(n) > 0 && Number(n) <= 128), backgrounded: nullable(n => typeof n === "boolean"), skip_transcript: nullable(n => typeof n === "boolean"), ambient: nullable(n => typeof n === "boolean") }, ["agent_type", "description", "depth", "backgrounded", "skip_transcript", "ambient"]);

function validate(resource: Resource, sessionId: string): SubagentRow | undefined {
  if (resource.kind !== EntityKind.SUBAGENT || resource.sessionId !== sessionId || !uuid(resource.id) || resource.schemaVersion !== 1 || resource.revision <= 0n || resource.revision > uintMax || resource.documentJson.byteLength > 1 << 20) return undefined;
  let value: unknown;
  try { value = parse(new TextDecoder("utf-8", { fatal: true }).decode(resource.documentJson)); } catch { return undefined; }
  if (!shape(value, { execution_id: uuid, harness: h => h === "codex" || h === "claude-code", native_version: model, root_id: uuid, first_sequence: sequence, last_sequence: sequence, observation: record, sources: s => Array.isArray(s) && s.length > 0 && s.length <= 4096 }, ["execution_id", "harness", "native_version", "root_id", "first_sequence", "last_sequence", "observation", "sources"])) return undefined;
  const codex = value.harness === "codex", child = object(value.observation), sources = value.sources as Document[];
  if (value.native_version !== (codex ? "0.151.0" : "2.1.236")) return undefined;
  const native = codex ? uuid : label;
  const source: Guard = s => Object.values(Source).includes(s as Source) && String(s).startsWith(codex ? "codex-" : "claude-");
  if (!shape(child, { id: uuid, native_id: native, parent_id: native, parent_tool_id: t => text(t), tool: t => claudeToolReference(t) !== undefined, tools, task, source, source_id: label, status: s => Object.values(Status).includes(s as Status), requested_model: nullable(model), observed_model: nullable(model), output: nullable(output), usage: nullable(record) }, ["id", "native_id", "parent_id", "source", "source_id", "status", "requested_model", "observed_model", "output", "usage"]) || child.id !== resource.id || child.native_id === value.root_id || child.native_id === child.parent_id) return undefined;
  if (codex ? Boolean(child.parent_tool_id) || child.tool !== undefined || child.task !== undefined || Array.isArray(child.tools) && child.tools.length !== 0 : !label(child.parent_tool_id)) return undefined;
  if (!codex && (child.parent_id === value.root_id ? child.tool === undefined || object(child.tool).native_id !== child.parent_tool_id || !["Agent", "Task"].includes(String(object(child.tool).name)) : child.tool !== undefined)) return undefined;
  let previous = 0n, retainedUsage: unknown = null, retainedUsageSource = child.source as Source;
  for (const evidence of sources) {
    if (!shape(evidence, { source, source_id: label, sequence, usage: nullable(record) }, ["source", "source_id", "sequence"]) || BigInt(evidence.sequence as string) <= previous) return undefined;
    previous = BigInt(evidence.sequence as string);
    if (evidence.usage != null) { if (!usage(evidence.usage, evidence.source as Source)) return undefined; retainedUsage = evidence.usage; retainedUsageSource = evidence.source as Source; }
  }
  const first = sources[0]!, last = sources.at(-1)!;
  if (value.first_sequence !== first.sequence || value.last_sequence !== last.sequence || child.source !== last.source || child.source_id !== last.source_id || (child.usage === null ? retainedUsage !== null : retainedUsage === null || !usage(child.usage, retainedUsageSource) || !sameUsage(child.usage, retainedUsage))) return undefined;
  // Retained fields may originate in an earlier report. Source coverage must
  // permit them; applying the latest incoming-source matrix would erase facts.
  const history = sources.some(s => s.source === Source.CodexHistory || s.source === Source.Content || s.source === Source.ClaudeHistory);
  if (child.observed_model !== null && !history || child.output !== null && !history && !sources.some(s => s.source === Source.Collaboration) || child.output !== null && Array.isArray(object(child.output).blocks) && (object(child.output).blocks as unknown[]).length > 0 && !history || child.requested_model !== null && codex && !history && !sources.some(s => s.source === Source.Collaboration)) return undefined;
  return { resource, record: value, child };
}

export function validateSubagentPage(resources: readonly Resource[], sessionId: string): SubagentRow[] | undefined {
  if (!uuid(sessionId) || resources.length > 50) return undefined;
  const rows: SubagentRow[] = [], identities = new Set<string>(), children = new Map<string, SubagentRow>(), parentTools = new Set<string>();
  for (const resource of resources) {
    const row = validate(resource, sessionId);
    if (!row || identities.has(resource.id) || children.has(row.child.native_id as string)) return undefined;
    identities.add(resource.id); children.set(row.child.native_id as string, row); rows.push(row);
    if (row.child.parent_tool_id) { if (parentTools.has(row.child.parent_tool_id as string)) return undefined; parentTools.add(row.child.parent_tool_id as string); }
  }
  for (const row of rows) {
    const visited = new Set([row.child.native_id]);
    let parent = children.get(row.child.parent_id as string);
    while (parent) {
      if (visited.has(parent.child.native_id) || parent.record.execution_id !== row.record.execution_id || parent.record.root_id !== row.record.root_id || parent.record.harness !== row.record.harness) return undefined;
      visited.add(parent.child.native_id); parent = children.get(parent.child.parent_id as string);
    }
    // An absent parent can be on another bounded page; do not invent it or
    // require the current page to contain the complete retained hierarchy.
  }
  return rows;
}
