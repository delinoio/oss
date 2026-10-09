import { Disclosure, DisclosureSummary } from "./disclosure";
import { LocalizedText, copy, useLocale } from "./localization";
import { object, type Document } from "./documents";
import { validClaudeAPIRetry } from "./native-claude-retry";
import { NativeClaudeProviderUsage, NativeClaudeResultUsage, validClaudeProviderUsage, validClaudeResultUsage } from "./native-claude-usage";

const nativeID = (v: unknown): v is string => typeof v === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-[47][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(v);
const id = (v: unknown) => nativeID(v) && v[14] === "7";
const validText = (v: unknown, limit: number): v is string => typeof v === "string" && !v.includes("\0") && !/[\uD800-\uDFFF]/u.test(v) && new TextEncoder().encode(v).length <= limit;
const fields = ["content_evidence", "request_id", "input_id", "message_id", "native_message_id", "context_native_id", "result_native_id", "command_native_id", "idle_native_id", "text", "context", "native_input_id", "kind", "reason", "is_error", "command", "usage", "partial_usage", "acknowledged", "idle", "cleanup_verified"];

function valid(progress: Document) {
  const v = object(progress.claude_stop);
  const optional = ["block_stop_native_id", "message_stop_native_id", "interrupted_native_id", "retries"].filter((key) => Object.hasOwn(v, key));
  if (Object.keys(v).length !== fields.length + optional.length || !fields.every((key) => Object.hasOwn(v, key)) || !optional.every((key) => key === "retries" || nativeID(v[key])) || v.message_stop_native_id !== undefined && v.block_stop_native_id === undefined || ![v.request_id, v.input_id, v.message_id, progress.execution_id, progress.native_thread_id].every(id) || !nativeID(progress.native_turn_id) || v.input_id !== progress.input_id || progress.outcome !== "stopped" || progress.claude_terminal != null || progress.claude_denial != null || progress.claude_interruption != null || progress.cleanup_verified !== undefined && typeof progress.cleanup_verified !== "boolean") return false;
  const original = [ v.context_native_id, v.result_native_id, v.command_native_id, v.idle_native_id];
  const retries = v.retries === undefined ? [] : v.retries;
  if (!Array.isArray(retries) || retries.length > 128 || v.retries !== undefined && retries.length === 0 || !retries.every(validClaudeAPIRetry)) return false;
  if (v.content_evidence === "aborted-assistant" ? !nativeID(v.interrupted_native_id) : v.content_evidence !== "closed-stream-before-retry" || v.interrupted_native_id !== undefined || !nativeID(v.block_stop_native_id) || !nativeID(v.message_stop_native_id) || retries.length === 0 || v.partial_usage !== null) return false;
  const identities = [...original, ...optional.filter((key) => key !== "retries").map((key) => v[key]), ...retries.map((r) => object(r).native_event_id), v.request_id, v.input_id, v.message_id, progress.native_thread_id, progress.native_turn_id];
  return original.every(nativeID) && new Set(identities).size === identities.length && !identities.includes(v.native_message_id) && validText(v.native_message_id, 1024) && v.native_message_id.trim() !== "" && validText(v.text, 256 * 1024) && v.context === "[Request interrupted by user]" && v.native_input_id === null && v.kind === "error_during_execution" && v.reason === "aborted_streaming" && v.is_error === true && v.command === "cancelled" && v.acknowledged === true && v.idle === true && v.cleanup_verified === true && validClaudeResultUsage(v.usage) && (v.partial_usage === null || validClaudeProviderUsage(v.partial_usage)) && new TextEncoder().encode(JSON.stringify([v.usage, v.partial_usage])).length <= (1 << 20);
}

export function NativeClaudeStop({ progress }: { progress: Document }) {
  useLocale();
  if (progress.claude_stop == null) return null;
  if (!valid(progress)) return <p>{copy("native-claude-stop.theRetainedClaudeStopEvidenceIs_d1dc14")}</p>;
  const v = object(progress.claude_stop);
  return <section aria-label={copy("native-claude-stop.claudeOriginalStop_ca5b72")}>
    <h4>{copy("native-claude-stop.claudeStopObserved_dc2f94")}</h4>
    <p>{copy("native-claude-stop.theResponseWasInterruptedClaudeReported_a3292e")}</p>
    <dl><dt>{copy("native-claude-stop.nativeInterruptRequest_f33630")}</dt><dd>{copy("native-claude-stop.acknowledged_d87cdf")}</dd><dt>{copy("native-claude-stop.nativeLoop_fec242")}</dt><dd>{copy("native-claude-stop.idleObserved_34b00d")}</dd>
      <dt>{copy("native-claude-stop.ownedNativeProcessCleanup_05bea7")}</dt><dd>{copy("native-claude-stop.confirmed_fe00b6")}</dd><dt>{copy("native-claude-stop.workerWorkspaceCleanupReport_e52033")}</dt><dd>{progress.cleanup_verified === true ? copy("native-claude-stop.confirmed_fe00b6") : copy("native-claude-stop.notConfirmed_bc1c29")}</dd></dl>
    {v.content_evidence === "closed-stream-before-retry" ? <p>{copy("native-claude-stop.theStreamClosedBeforeRetryWait_d850de")}</p> : <p>{copy("native-claude-stop.theTranscriptPreservesClaudeSOriginal_0dd473")}</p>}
    {Array.isArray(v.retries) ? <Disclosure><DisclosureSummary>{copy("native-claude-stop.nativeRetryObservations_7f1b4d")}</DisclosureSummary><ol>{v.retries.map((entry, index) => { const r = object(entry); return <li key={index}><LocalizedText id="native-claude-stop.attemptOfDelayMsHttp_904c77" components={{ s0: <>{r.attempt as string}</>, s1: <>{r.max_retries as string}</>, s2: <>{r.retry_delay_ms as string}</>, s3: <>{r.error as string}</>, s4: <>{r.error_status === null ? copy("native-claude-stop.unavailable_ba691b") : String(r.error_status)}</> }} /></li>; })}</ol><p>{copy("native-claude-stop.theseAreObservationsFromTheStopped_ffb6f4")}</p></Disclosure> : null}
    <Disclosure><DisclosureSummary>{copy("native-claude-stop.originalInterruptionContext_fb2fe5")}</DisclosureSummary><pre>{v.context as string}</pre></Disclosure>
    <Disclosure><DisclosureSummary>{copy("native-claude-stop.nativeInterruptionUsage_fd5e14")}</DisclosureSummary><NativeClaudeResultUsage value={v.usage} />
      {v.partial_usage !== null ? <><h4>{copy("native-claude-stop.interruptedResponseReport_751c6f")}</h4><NativeClaudeProviderUsage value={v.partial_usage} /></> : <p>{copy("native-claude-stop.interruptedResponseUsageUnavailable_d58c68")}</p>}
      <p>{copy("native-claude-stop.theseReportsOverlapAndDoNot_c16702")}</p></Disclosure>
    <p>{copy("native-claude-stop.originalHistoryMustBeReconciledBefore_9378d6")}</p>
  </section>;
}
