import { object, text, type Document } from "./documents";
import { claudeToolReference } from "./native-claude-tool";

enum Kind { Tool = "tool-permission", Question = "user-question", Plan = "plan-approval" }
const uuid = (v: unknown) => typeof v === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(v);
const exact = (v: Document, fields: string[]) => Object.keys(v).length === fields.length && fields.every((k) => Object.hasOwn(v, k));
const bounded = (v: unknown, max: number): v is string => typeof v === "string" && v.length <= max && !v.includes("\0") && !/[\uD800-\uDFFF]/u.test(v) && new TextEncoder().encode(v).length <= max;
const label = (v: unknown, max: number): v is string => bounded(v, max) && v.trim().length > 0;
const optionalText = (v: unknown) => v === null || bounded(v, 4096);
const modes = ["default", "plan", "acceptEdits", "dontAsk", "bypassPermissions"];
function suggestion(value: unknown) {
  const p = object(value);
  if (!Object.keys(p).every((k) => ["type", "rules", "behavior", "mode", "directories", "destination"].includes(k)) || (p.destination !== undefined && !["userSettings", "projectSettings", "localSettings", "session"].includes(text(p.destination)))) return false;
  if (["addRules", "replaceRules", "removeRules"].includes(text(p.type))) return Array.isArray(p.rules) && p.rules.length > 0 && p.rules.length <= 128 && ["allow", "deny", "ask"].includes(text(p.behavior)) && p.mode === undefined && p.directories === undefined && p.rules.every((v) => { const r = object(v); return exact(r, r.ruleContent === undefined ? ["toolName"] : ["toolName", "ruleContent"]) && label(r.toolName, 256) && (r.ruleContent === undefined || bounded(r.ruleContent, 4096)); });
  if (p.type === "setMode") return modes.includes(text(p.mode)) && p.rules === undefined && p.behavior === undefined && p.directories === undefined;
  if (p.type === "addDirectories" || p.type === "removeDirectories") return Array.isArray(p.directories) && p.directories.length > 0 && p.directories.length <= 128 && p.directories.every((v) => label(v, 4096)) && new Set(p.directories).size === p.directories.length && p.rules === undefined && p.behavior === undefined && p.mode === undefined;
  return false;
}
function request(data: Document) {
  const r = object(data.claude), m = object(r.metadata), ref = claudeToolReference(r.tool), native = object(data.native_request_id);
  if (!exact(r, ["version", "kind", "arrival_id", "tool", "message_id", "native_message_id", "index", "caller", "input_json", "metadata"]) || r.version !== "2.1.236" || !Object.values(Kind).includes(r.kind as Kind) || !uuid(r.arrival_id) || !ref || !uuid(r.message_id) || r.message_id === ref.id || !label(r.native_message_id, 1024) || r.native_message_id === ref.native_id || data.native_item_id !== ref.native_id || !Number.isInteger(r.index) || Number(r.index) < 0 || Number(r.index) >= 1024 || (r.caller !== null && r.caller !== "direct") || !bounded(r.input_json, 256 * 1024) || !exact(native, ["kind", "text"]) || native.kind !== "text" || !label(native.text, 128) || data.opencode != null || data.questions != null || data.approval != null || data.response != null || data.approval_response != null || data.opencode_stop != null || data.opencode_closure != null) return undefined;
  if (new TextEncoder().encode(JSON.stringify(r)).length > 512 * 1024 || !exact(m, ["permission_suggestions", "blocked_path", "decision_reason", "decision_reason_type", "requires_user_interaction", "agent_id", "title", "display_name", "description"]) || ![m.blocked_path, m.decision_reason, m.decision_reason_type, m.agent_id, m.title, m.display_name, m.description].every(optionalText) || (m.requires_user_interaction !== null && typeof m.requires_user_interaction !== "boolean") || (m.permission_suggestions !== null && (!Array.isArray(m.permission_suggestions) || m.permission_suggestions.length > 128 || !m.permission_suggestions.every(suggestion)))) return undefined;
  const cancellation = object(data.claude_cancellation);
  if (data.closure === "open" ? data.claude_cancellation != null : data.closure !== "native-closed" || !exact(cancellation, ["arrival_id"]) || cancellation.arrival_id !== r.arrival_id) return undefined;
  let input: Document;
  try { const parsed: unknown = JSON.parse(r.input_json); if (parsed === null || typeof parsed !== "object" || Array.isArray(parsed)) return undefined; input = object(parsed); } catch { return undefined; }
  if (r.kind === Kind.Question) {
    if (data.type !== "user-question" || ref.name !== "AskUserQuestion" || !Object.keys(input).every((k) => k === "questions" || k === "metadata") || !Array.isArray(input.questions) || input.questions.length === 0 || input.questions.length > 4) return undefined;
    const seen = new Set<string>();
    for (const value of input.questions) {
      const q = object(value);
      if (!exact(q, ["question", "header", "options", "multiSelect"]) || !label(q.question, 4096) || seen.has(q.question) || !label(q.header, 256) || typeof q.multiSelect !== "boolean" || !Array.isArray(q.options) || q.options.length < 2 || q.options.length > 4) return undefined;
      seen.add(q.question);
      const options = new Set<string>();
      for (const value of q.options) { const o = object(value); if (!exact(o, ["label", "description"]) || !label(o.label, 1024) || !bounded(o.description, 4096) || options.has(o.label)) return undefined; options.add(o.label); }
    }
  } else if (data.type !== "native-approval" || (r.kind === Kind.Plan ? ref.name !== "ExitPlanMode" || !label(input.plan, 256 * 1024) || (input.planFilePath !== undefined && !label(input.planFilePath, 4096)) : ref.name === "ExitPlanMode" || ref.name === "AskUserQuestion")) return undefined;
  return { original: r, metadata: m, input, name: ref.name };
}

export function NativeClaudeInteraction({ data }: { data: Document }) {
  const r = request(data);
  if (!r) return <section aria-label="Claude request unavailable"><p>The retained Claude request is unavailable or inconsistent.</p></section>;
  return <section aria-label="Original Claude request">
    <p>{r.name} · {r.original.kind === Kind.Question ? "User question" : r.original.kind === Kind.Plan ? "Plan approval" : "Tool permission"}</p>
    {typeof r.metadata.title === "string" ? <p>{r.metadata.title}</p> : null}
    {typeof r.metadata.description === "string" ? <p>{r.metadata.description}</p> : null}
    {r.original.kind === Kind.Question ? <ol>{(r.input.questions as Document[]).map((q, index) => <li key={index}><strong>{text(q.header)}</strong><p>{text(q.question)}</p><p>{q.multiSelect ? "Multiple selections offered" : "Single selection offered"}</p><ul>{(q.options as Document[]).map((o, index) => <li key={index}>{text(o.label)} — {text(o.description)}</li>)}</ul></li>)}</ol> : null}
    {r.original.kind === Kind.Plan ? <><pre>{text(r.input.plan)}</pre>{typeof r.input.planFilePath === "string" ? <p>Native plan path: {r.input.planFilePath}</p> : null}</> : null}
    <details><summary>Original callback input</summary><pre>{text(r.original.input_json)}</pre></details>
    <details><summary>Original callback metadata</summary><pre>{JSON.stringify(r.metadata, null, 2)}</pre></details>
    <p>{data.closure === "native-closed" ? "The original native request was canceled. Cancellation does not prove that a response was accepted." : "The original request is waiting. Claude reply delivery is not available yet; an ordinary message cannot answer this request."}</p>
    <p>Native suggestions do not grant access or change saved permissions.</p>
  </section>;
}
