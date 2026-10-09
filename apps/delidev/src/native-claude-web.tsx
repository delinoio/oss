import { Disclosure, DisclosureSummary } from "./disclosure";
import { object, type Document } from "./documents";
import { timestampInstant } from "./timestamp-format";
import { copy } from "./localization";

export enum ClaudeWebKind { Call = "server_tool_use", Search = "web_search_tool_result", Fetch = "web_fetch_tool_result" }
const keys = (v: Document, expected: string[]) => Object.keys(v).length === expected.length && expected.every((key) => Object.hasOwn(v, key));
const text = (v: unknown, max = 256 * 1024): v is string => typeof v === "string" && !v.includes("\0") && !/[\uD800-\uDFFF]/u.test(v) && new TextEncoder().encode(v).length <= max;
const optionalText = (v: unknown, max: number) => v === null || text(v, max);
const errors = { web_search: ["invalid_tool_input", "unavailable", "max_uses_exceeded", "too_many_requests", "query_too_long", "request_too_large"], web_fetch: ["invalid_tool_input", "url_too_long", "url_not_allowed", "url_not_in_prior_context", "url_not_accessible", "unsupported_content_type", "too_many_requests", "max_uses_exceeded", "unavailable", "content_too_large"] };

export function validClaudeWeb(value: unknown, kind: string): value is Document {
  const w = object(value);
  if (w.caller!==null&&w.caller!=="direct")return false;
  if (!text(w.native_id, 1024) || !w.native_id || !["web_search", "web_fetch"].includes(String(w.name))) return false;
  if (kind === ClaudeWebKind.Call) {
    const c = object(w.call);
    if (!keys(w, ["native_id", "name", "caller", "call"]) || !keys(c, ["initial_input", "input_delta"]) || !text(c.initial_input) || !optionalText(c.input_delta, 256 * 1024)) return false;
    try { const input: unknown = JSON.parse(c.initial_input); return input !== null && typeof input === "object" && !Array.isArray(input); } catch { return false; }
  }
  const r = object(w.result);
  if (!keys(w, ["native_id", "name", "caller", "result"]) || !keys(r, ["problem", "search", "fetch"]) || (kind === ClaudeWebKind.Search) !== (w.name === "web_search") || ![ClaudeWebKind.Search, ClaudeWebKind.Fetch].includes(kind as ClaudeWebKind)) return false;
  if (r.problem !== "") return typeof r.problem === "string" && errors[w.name as keyof typeof errors].includes(r.problem) && r.search === null && r.fetch === null;
  if (w.name === "web_search") return r.fetch === null && Array.isArray(r.search) && r.search.length <= 1024 && r.search.every((v) => { const s = object(v); return keys(s, ["url", "title", "page_age"]) && text(s.url, 16 * 1024) && !!s.url && text(s.title, 16 * 1024) && optionalText(s.page_age, 1024); });
  const f = object(r.fetch), d = object(f.document);
  return r.search === null && keys(f, ["url", "retrieved_at", "document"]) && text(f.url, 16 * 1024) && !!f.url && (f.retrieved_at === null || typeof f.retrieved_at === "string" && timestampInstant(f.retrieved_at) !== undefined) && keys(d, ["source", "media", "title", "context", "citations", "text"]) && ["text", "base64", "url", "file", "content"].includes(String(d.source)) && (d.source === "text" ? d.media === "text/plain" : d.source === "base64" ? d.media === "application/pdf" : d.media === "") && optionalText(d.title, 16 * 1024) && optionalText(d.context, 256 * 1024) && (d.citations === null || typeof d.citations === "boolean") && optionalText(d.text, 256 * 1024) && (d.text === null || d.source === "text" || d.source === "content");
}

// All provider data stays inert. A displayed URL never becomes a fetch action.
export function NativeClaudeWeb({ value, kind }: { value: Document; kind: string }) {
  const r = object(value.result), f = object(r.fetch), d = object(f.document), c = object(value.call);
  return <Disclosure><DisclosureSummary>{String(value.name)}</DisclosureSummary>
    {kind === ClaudeWebKind.Call ? <><pre>{String(c.initial_input)}</pre>{c.input_delta !== null ? <pre>{String(c.input_delta)}</pre> : null}</> : r.problem ? <p>{copy("native-claude-message.nativeWebToolError")} <code>{String(r.problem)}</code></p> : kind === ClaudeWebKind.Search ? <ol>{(r.search as Document[]).map((s, i) => <li key={i}><p>{String(s.title)}</p><p>{String(s.url)}</p>{s.page_age !== null ? <p>{String(s.page_age)}</p> : null}</li>)}</ol> : <><p>{String(f.url)}</p>{f.retrieved_at !== null ? <p>{String(f.retrieved_at)}</p> : null}{d.title !== null ? <p>{String(d.title)}</p> : null}{d.context !== null ? <pre>{String(d.context)}</pre> : null}{d.text !== null ? <pre>{String(d.text)}</pre> : <p>{copy("native-claude-message.nativeDocumentContentPrivate")}</p>}</>}
  </Disclosure>;
}
