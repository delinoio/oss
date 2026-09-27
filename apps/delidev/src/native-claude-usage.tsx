import { object } from "./documents";

enum Source { Start = "provider-message-start", Block = "block-complete", Metadata = "provider-message-metadata", Result = "input-result" }
type Guard = (value: unknown) => boolean;
const record = (value: unknown): value is Record<string, unknown> => value !== null && typeof value === "object" && !Array.isArray(value);
const nullable = (guard: Guard): Guard => (value) => value === null || guard(value);
const choice = (...values: string[]): Guard => (value) => typeof value === "string" && values.includes(value);
function text(value: unknown, max = 256): value is string {
  return typeof value === "string" && value.length <= max && !value.includes("\0") && !/[\uD800-\uDFFF]/u.test(value) && new TextEncoder().encode(value).length <= max;
}
const label: Guard = (v) => text(v) && v.trim().length > 0;
const count: Guard = (v) => typeof v === "string" && /^(0|[1-9][0-9]{0,18})$/.test(v) && BigInt(v) <= 9223372036854775807n;
const positive: Guard = (v) => count(v) && v !== "0";
const decimal: Guard = (v) => typeof v === "string" && v.length <= 128 && /^(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$/.test(v) && Number.isFinite(Number(v));
function shape(value: unknown, fields: Record<string, Guard>): value is Record<string, unknown> {
  return record(value) && Object.entries(value).every(([key, item]) => Object.hasOwn(fields, key) && fields[key]!(item));
}
const cache: Guard = (v) => shape(v, { ephemeral_1h_input_tokens: nullable(count), ephemeral_5m_input_tokens: nullable(count) });
const counters = { input_tokens: nullable(count), output_tokens: nullable(count), cache_creation_input_tokens: nullable(count), cache_read_input_tokens: nullable(count) };
const iteration: Guard = (v) => shape(v, { ...counters, type: choice("message", "compaction", "advisor_message", "fallback_message"), cache_creation: nullable(cache), model: nullable(label) }) &&
  typeof v.type === "string" && (v.type === "compaction" ? v.model == null : v.type === "message" ? true : label(v.model));
const creditReasons = ["body_mismatch", "continuation_excluded", "continuation_only", "expired", "invalid_target_model", "not_enabled", "reprice_unavailable", "temporarily_unavailable", "variant_fields_present", "wrong_organization", "wrong_platform", "wrong_workspace"];
const credit: Guard = (v) => {
  if (!shape(v, { status: (s) => shape(s, { type: choice("redeemed", "not_applied"), reason: choice(...creditReasons), remove_to_redeem: (a) => Array.isArray(a) && a.length > 0 && a.length <= 128 && a.every(label) && new Set(a).size === a.length }) })) return false;
  const s = object(v.status);
  return s.type === "redeemed" ? s.reason === undefined && s.remove_to_redeem === undefined : s.type === "not_applied" && creditReasons.includes(s.reason as string) && (s.reason === "variant_fields_present" ? Array.isArray(s.remove_to_redeem) : s.remove_to_redeem === undefined);
};
const provider: Guard = (v) => shape(v, { ...counters,
  output_tokens_details: nullable((v) => shape(v, { thinking_tokens: nullable(count) })), cache_creation: nullable(cache),
  server_tool_use: nullable((v) => shape(v, { web_search_requests: nullable(count), web_fetch_requests: nullable(count) })),
  fallback_credit: nullable(credit), service_tier: nullable(choice("standard", "priority", "batch")), speed: nullable(choice("standard", "fast")), inference_geo: nullable((v) => text(v, 128)),
  iterations: nullable((a) => Array.isArray(a) && a.length <= 1024 && a.every(iteration)),
});
const model: Guard = (v) => shape(v, { inputTokens: nullable(count), outputTokens: nullable(count), cacheReadInputTokens: nullable(count), cacheCreationInputTokens: nullable(count), webSearchRequests: nullable(count), costUSD: nullable(decimal), contextWindow: nullable(positive), maxOutputTokens: nullable(positive), canonicalModel: nullable(label), provider: nullable(label) });
const result: Guard = (v) => shape(v, { main_loop_turn: nullable(provider), native_cumulative_cost_usd: nullable(decimal), native_cumulative_models: nullable((m) => record(m) && Object.keys(m).length <= 256 && Object.entries(m).every(([key, value]) => label(key) && model(value))) });
const nativeID: Guard = (v) => typeof v === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-[47][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(v);
const messageID: Guard = (v) => nativeID(v) && (v as string)[14] === "7";
function valid(v: Record<string, unknown>) {
  if (!shape(v, { source: choice(...Object.values(Source)), native_event_id: nativeID, message_id: messageID, native_message_id: (v) => text(v, 1024) && v.trim().length > 0, model: label, index: (v) => Number.isInteger(v) && (v as number) >= 0 && (v as number) < 1024, provider, result }) || !nativeID(v.native_event_id)) return false;
  if (v.source === Source.Result) return result(v.result) && v.provider === undefined && v.message_id === undefined && v.native_message_id === undefined && v.model === undefined && v.index === undefined;
  return Object.values(Source).includes(v.source as Source) && provider(v.provider) && v.result === undefined && messageID(v.message_id) && text(v.native_message_id, 1024) && v.native_message_id.trim().length > 0 && label(v.model) && (v.source === Source.Block ? v.index !== undefined : v.index === undefined);
}
const labels = [["input_tokens", "Input excluding cache"], ["cache_read_input_tokens", "Cache read input"], ["cache_creation_input_tokens", "Cache creation input"], ["output_tokens", "Output including thinking"]] as const;
function ProviderCounts({ value }: { value: unknown }) {
  if (value == null) return <p>Main-loop usage unavailable.</p>;
  const p = object(value);
  return <><dl>{labels.map(([key, name]) => <div key={key}><dt>{name}</dt><dd>{p[key] == null ? "Unavailable" : p[key] as string}</dd></div>)}</dl><details><summary>Native usage details</summary><pre>{JSON.stringify(p, null, 2)}</pre></details></>;
}

export function NativeClaudeUsage({ value }: { value: Record<string, unknown> }) {
  if (!valid(value)) return <p>The retained Claude usage observation is unavailable or inconsistent.</p>;
  const retained = object(value.result), models = object(retained.native_cumulative_models);
  return <section aria-label="Claude native usage">
    <p>Source: {value.source === Source.Result ? "Original input result" : value.source === Source.Start ? "Provider message start" : value.source === Source.Block ? `Completed native block ${Number(value.index) + 1}` : "Provider message metadata"}</p>
    {value.source === Source.Result ? <>
      <h4>Main-loop input turn</h4><ProviderCounts value={retained.main_loop_turn} />
      <h4>Native cumulative model ledger</h4>
      {retained.native_cumulative_models == null ? <p>Unavailable</p> : Object.keys(models).length === 0 ? <p>No model entries reported.</p> : Object.entries(models).map(([name, usage]) => <details key={name}><summary>{name}</summary><pre>{JSON.stringify(usage, null, 2)}</pre></details>)}
      <dl><dt>Native cumulative USD estimate</dt><dd>{retained.native_cumulative_cost_usd == null ? "Unavailable" : retained.native_cumulative_cost_usd as string}</dd></dl>
    </> : <ProviderCounts value={value.provider} />}
    <p>Native reports overlap. Main-loop usage excludes auxiliary and subagent calls; cumulative model values belong to the original native runtime. Output includes thinking. These reports and native estimates are not added to billed usage, price estimates or session budgets.</p>
  </section>;
}
