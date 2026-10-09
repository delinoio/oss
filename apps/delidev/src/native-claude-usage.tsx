import { LocalizedText, copy, useLocale } from "./localization";
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
export const validClaudeProviderUsage = provider;
const model: Guard = (v) => shape(v, { inputTokens: nullable(count), outputTokens: nullable(count), cacheReadInputTokens: nullable(count), cacheCreationInputTokens: nullable(count), webSearchRequests: nullable(count), costUSD: nullable(decimal), contextWindow: nullable(positive), maxOutputTokens: nullable(positive), canonicalModel: nullable(label), provider: nullable(label) });
export const validClaudeResultUsage: Guard = (v) => shape(v, { main_loop_turn: nullable(provider), native_cumulative_cost_usd: nullable(decimal), native_cumulative_models: nullable((m) => record(m) && Object.keys(m).length <= 256 && Object.entries(m).every(([key, value]) => label(key) && model(value))) });
const result = validClaudeResultUsage;
const nativeID: Guard = (v) => typeof v === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-[47][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(v);
const messageID: Guard = (v) => nativeID(v) && (v as string)[14] === "7";
function valid(v: Record<string, unknown>) {
  if (!shape(v, { source: choice(...Object.values(Source)), native_event_id: nativeID, message_id: messageID, native_message_id: (v) => text(v, 1024) && v.trim().length > 0, model: label, index: (v) => Number.isInteger(v) && (v as number) >= 0 && (v as number) < 1024, provider, result }) || !nativeID(v.native_event_id)) return false;
  if (v.source === Source.Result) return result(v.result) && v.provider === undefined && v.message_id === undefined && v.native_message_id === undefined && v.model === undefined && v.index === undefined;
  return Object.values(Source).includes(v.source as Source) && provider(v.provider) && v.result === undefined && messageID(v.message_id) && text(v.native_message_id, 1024) && v.native_message_id.trim().length > 0 && label(v.model) && (v.source === Source.Block ? v.index !== undefined : v.index === undefined);
}
const labels = () => [["input_tokens", copy("native-claude-usage.extra.7fa3de9600ee")], ["cache_read_input_tokens", copy("native-claude-usage.extra.085171a719a1")], ["cache_creation_input_tokens", copy("native-claude-usage.extra.7018ea47815e")], ["output_tokens", copy("native-claude-usage.extra.ca019159180d")]] as const;
function ProviderCounts({ value }: { value: unknown }) {
  useLocale();
  if (value == null) return <><p>{copy("native-claude-usage.mainLoopUsageUnavailable_8890bb")}</p><p>{copy("native-claude-usage.evidenceHelp")}</p></>;
  const p = object(value);
  return <><dl>{labels().map(([key, name]) => <div key={key}><dt>{name}</dt><dd>{p[key] == null ? copy("native-claude-usage.unavailable_ca1844") : p[key] as string}</dd></div>)}</dl><details><summary>{copy("native-claude-usage.nativeUsageDetails_c90c8b")}</summary><pre>{JSON.stringify(p, null, 2)}</pre></details></>;
}

export function NativeClaudeProviderUsage({ value }: { value: unknown }) {
  useLocale();
  return provider(value) ? <ProviderCounts value={value} /> : <><p>{copy("native-claude-usage.nativeProviderUsageUnavailable_e70578")}</p><p>{copy("native-claude-usage.evidenceHelp")}</p></>;
}

export function NativeClaudeUsage({ value }: { value: Record<string, unknown> }) {
  useLocale();
  if (!valid(value)) return <><p>{copy("native-claude-usage.theRetainedClaudeUsageObservationIs_5789fe")}</p><p>{copy("native-claude-usage.evidenceHelp")}</p></>;
  const retained = object(value.result);
  return <section aria-label={copy("native-claude-usage.claudeNativeUsage_53a311")}>
    <p><LocalizedText id="native-claude-usage.source_590a7b" components={{ s0: <>{value.source === Source.Result ? copy("native-claude-usage.originalInputResult_8746ee") : value.source === Source.Start ? copy("native-claude-usage.providerMessageStart_1f2c60") : value.source === Source.Block ? copy("native-claude-usage.completedNativeBlock_9f2f2e", { v0: Number(value.index) + 1 }) : copy("native-claude-usage.providerMessageMetadata_9b71ca")}</> }} /></p>
    {value.source === Source.Result ? <>
      <NativeClaudeResultUsage value={retained} />
    </> : <ProviderCounts value={value.provider} />}
    <p>{copy("native-claude-usage.nativeReportsOverlapMainLoopUsage_9fa040")}</p>
  </section>;
}

export function NativeClaudeResultUsage({ value }: { value: unknown }) {
  useLocale();
  if (!result(value)) return <><p>{copy("native-claude-usage.theRetainedClaudeResultUsageIs_c820c7")}</p><p>{copy("native-claude-usage.evidenceHelp")}</p></>;
  const retained = object(value), models = object(retained.native_cumulative_models);
  return <section aria-label={copy("native-claude-usage.claudeResultUsage_3e6c8f")}>
      <h4>{copy("native-claude-usage.nativeMainLoopTurn_7fae43")}</h4><ProviderCounts value={retained.main_loop_turn} />
      <h4>{copy("native-claude-usage.nativeCumulativeModelLedger_46974a")}</h4>
      {retained.native_cumulative_models == null ? <p>{copy("native-claude-usage.unavailable_ca1844")}</p> : Object.keys(models).length === 0 ? <p>{copy("native-claude-usage.noModelEntriesReported_d168a7")}</p> : Object.entries(models).map(([name, usage]) => <details key={name}><summary>{name}</summary><pre>{JSON.stringify(usage, null, 2)}</pre></details>)}
      {retained.native_cumulative_models == null || retained.native_cumulative_cost_usd == null ? <p>{copy("native-claude-usage.evidenceHelp")}</p> : null}
      <dl><dt>{copy("native-claude-usage.nativeCumulativeUsdEstimate_f82a7c")}</dt><dd>{retained.native_cumulative_cost_usd == null ? copy("native-claude-usage.unavailable_ca1844") : retained.native_cumulative_cost_usd as string}</dd></dl>
  </section>;
}
