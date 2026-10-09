import { Disclosure, DisclosureSummary } from "./disclosure";
import { LocalizedText, copy, useLocale } from "./localization";
import { object, type Document } from "./documents";

const exact = (v: Document, fields: string[]) => Object.keys(v).length === fields.length && fields.every((key) => Object.hasOwn(v, key));
const count = (v: unknown): v is string => typeof v === "string" && /^(0|[1-9][0-9]{0,19})$/.test(v) && BigInt(v) <= 18446744073709551615n;
const uuid = (v: unknown, version: 4 | 7): v is string => typeof v === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-[47][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(v) && v[14] === String(version);
const ordinal = (v: unknown) => Number.isInteger(v) && Number(v) > 0 && Number(v) <= 128;
const eventIndex = (v: unknown, thread: string) => typeof v === "string" && v.startsWith(`${thread}-`) && count(v.slice(thread.length + 1)) ? BigInt(v.slice(thread.length + 1)) : undefined;
function metadata(v: Document, thread: string) {
  return exact(v, ["event_id", "chunk_id", "context_tokens", "timestamp_ms", "stream_start_ms", "turn_start_ms"]) && eventIndex(v.event_id, thread) !== undefined && count(v.chunk_id) && BigInt(v.chunk_id) > 0n && count(v.context_tokens) && [v.timestamp_ms, v.stream_start_ms, v.turn_start_ms].every((s) => count(s) && BigInt(s) <= 253402300799999n);
}

function validText(data: Document) {
  if (data.role !== "assistant" || !["streaming", "complete"].includes(String(data.state)) || typeof data.text !== "string" || data.text.includes("\0") || /[\uD800-\uDFFF]/u.test(data.text) || new TextEncoder().encode(data.text).length > (256 << 10) || !uuid(data.execution_id, 7) || !uuid(data.native_thread_id, 7) || !uuid(data.native_turn_id, 4) || data.phase != null || data.input_id !== undefined || data.native_parent_id !== undefined || [data.grok_tool, data.grok_user, data.claude, data.claude_tool, data.claude_progress, data.claude_interruption, data.tool, data.artifact, data.progress].some((v) => v != null)) return false;
  const v = object(data.grok_text), chunks = v.chunks;
  if (!exact(v, ["response_ordinal", "chunks", ...(v.interruption !== undefined ? ["interruption"] : [])]) || !ordinal(v.response_ordinal) || !Array.isArray(chunks) || chunks.length < 1 || chunks.length > 1024 || !Number.isSafeInteger(data.first_sequence) || Number(data.first_sequence) < 3 || !Number.isSafeInteger(data.last_sequence) || Number(data.last_sequence) > 100000 || Number(data.last_sequence) < Number(data.first_sequence) + chunks.length - (data.state === "complete" ? 0 : 1)) return false;
  let priorEvent: bigint | undefined, priorChunk: bigint | undefined;
  for (const raw of chunks) {
    const chunk = object(raw);
    if (!metadata(chunk, data.native_thread_id)) return false;
    const event = eventIndex(chunk.event_id, data.native_thread_id)!, number = BigInt(chunk.chunk_id as string);
    if (priorEvent !== undefined && (event <= priorEvent || number <= priorChunk!)) return false;
    priorEvent = event; priorChunk = number;
  }
  const interrupted = object(v.interruption);
  if (v.interruption !== undefined && (data.state !== "complete" || !exact(interrupted, ["request_id", "native_event_id"]) || !uuid(interrupted.request_id, 7) || eventIndex(interrupted.native_event_id, data.native_thread_id) === undefined || eventIndex(interrupted.native_event_id, data.native_thread_id)! <= priorEvent!)) return false;
  return data.native_id === object(chunks[0]).event_id;
}

function userHistory(v: Document, thread: string) {
  return exact(v, ["source", "native_event_id", "timestamp_ms", "prompt_index", "model", "input_digest"]) && v.source === "closed-first-text" && eventIndex(v.native_event_id, thread) !== undefined && count(v.timestamp_ms) && BigInt(v.timestamp_ms) <= 253402300799999n && v.prompt_index === "0" && typeof v.model === "string" && v.model.length > 0 && !v.model.includes("\0") && !/[\uD800-\uDFFF]/u.test(v.model) && new TextEncoder().encode(v.model).length <= 256 && typeof v.input_digest === "string" && /^[a-f0-9]{64}$/.test(v.input_digest);
}

export function NativeGrokUser({ data }: { data: Document }) {
  useLocale();
  const v = object(data.grok_user);
  const valid = data.role === "user" && data.state === "complete" && uuid(data.execution_id, 7) && uuid(data.input_id, 7) && uuid(data.native_thread_id, 7) && uuid(data.native_turn_id, 4) && data.native_id === v.native_event_id && userHistory(v, data.native_thread_id) && typeof data.text === "string" && data.text.length > 0 && !data.text.includes("\0") && !/[\uD800-\uDFFF]/u.test(data.text) && new TextEncoder().encode(data.text).length <= (256 << 10) && Number.isSafeInteger(data.first_sequence) && Number(data.first_sequence) >= 5 && Number(data.first_sequence) <= 100000 && data.last_sequence === data.first_sequence && [data.grok_tool, data.grok_text, data.claude, data.claude_tool, data.claude_progress, data.claude_interruption, data.tool, data.artifact, data.progress, data.phase, data.native_parent_id].every((value) => value === undefined);
  if (!valid) return <article className="message" aria-label={copy("native-grok.grokUserInputUnavailable_7b21a3")}><p>{copy("native-grok.theRetainedGrokUserInputIs_ff8a57")}</p></article>;
  return <article className="message message-user" aria-label={copy("native-grok.grokUserInput_b19458")}>
    <header><strong>{copy("native-grok.user_04f899")}</strong><small>{copy("native-grok.verifiedFromClosedNativeHistory_2d5065")}</small></header>
    <pre>{data.text as string}</pre>
    <Disclosure><DisclosureSummary>{copy("native-grok.grokInputDetails_e34cc2")}</DisclosureSummary><dl>
      <dt>{copy("native-grok.originalNativeEvent_3cd761")}</dt><dd>{v.native_event_id as string}</dd>
      <dt>{copy("native-grok.model_5e2c61")}</dt><dd>{v.model as string}</dd>
      <dt>{copy("native-grok.nativeTimestampMs_0d220f")}</dt><dd>{v.timestamp_ms as string}</dd>
    </dl><p>{copy("native-grok.thisInputWasCheckedAgainstThe_61b918")}</p></Disclosure>
  </article>;
}

export function NativeGrokText({ data }: { data: Document }) {
  useLocale();
  if (!validText(data)) return <article className="message" aria-label={copy("native-grok.grokTextUnavailable_e2350d")}><p>{copy("native-grok.theRetainedGrokTextIsUnavailable_0bab2c")}</p></article>;
  const v = object(data.grok_text), chunks = v.chunks as Document[], first = chunks[0]!, last = chunks[chunks.length - 1]!;
  return <article className="message message-assistant" aria-label={copy("native-grok.grokAssistantText_45c8b0")}>
    <header><strong>{copy("native-grok.assistant_a39a7f")}</strong><small>{v.interruption !== undefined ? copy("native-grok.partialResponseStopped_fb17b3") : data.state === "complete" ? copy("native-grok.responseTextComplete_9a599a") : copy("native-grok.streaming_a951c5")}</small></header>
    <pre>{data.text as string}</pre>
    <Disclosure><DisclosureSummary>{copy("native-grok.grokTextDetails_97e9e2")}</DisclosureSummary><dl>
      <dt>{copy("native-grok.responseOrderInThisInput_902357")}</dt><dd>{v.response_ordinal as number}</dd>
      <dt>{copy("native-grok.firstNativeTextEvent_f187af")}</dt><dd>{first.event_id as string}</dd>
      <dt>{copy("native-grok.latestNativeTextEvent_f4751f")}</dt><dd>{last.event_id as string}</dd>
      <dt>{copy("native-grok.latestNativeChunk_2975cf")}</dt><dd>{last.chunk_id as string}</dd>
      <dt>{copy("native-grok.latestReportedContextTokens_5d3e70")}</dt><dd>{last.context_tokens as string}</dd>
    </dl><p>{copy("native-grok.contextIsANativeEstimateSeparate_286d1f")}</p>{v.interruption !== undefined ? <p>{copy("native-grok.theOriginalResponseWasInterruptedIts_b25935")}</p> : null}</Disclosure>
  </article>;
}

const labels = () => [["input_tokens", copy("native-grok.extra.dd856eeb5046")], ["output_tokens", copy("native-grok.extra.a9b50ea0c4a7")], ["cache_read_input_tokens", copy("native-grok.extra.57e07052a840")], ["cache_creation_input_tokens", copy("native-grok.extra.cb8efc59a7e1")], ["reasoning_tokens", copy("native-grok.extra.157b0f67b442")]] as const;
export function NativeGrokUsage({ value }: { value: Document }) {
  useLocale();
  const counts = object(value.counts);
  if (!exact(value, ["ordinal", "counts"]) || !ordinal(value.ordinal) || !exact(counts, labels().map(([key]) => key)) || !labels().every(([key]) => count(counts[key]))) return <p>{copy("native-grok.theRetainedGrokUsageIsUnavailable_9ac016")}</p>;
  return <section aria-label={copy("native-grok.grokResponseUsage_e5fd60")}><p><LocalizedText id="native-grok.sourceOriginalCompletedResponseInThis_db7138" components={{ s0: <>{value.ordinal as number}</> }} /></p><dl>
    {labels().map(([key, label]) => <div key={key}><dt>{label}</dt><dd>{counts[key] as string}</dd></div>)}
    <dt>{copy("native-grok.responseTotal_3ef98a")}</dt><dd>{copy("native-grok.notReported_adadfa")}</dd><dt>{copy("native-grok.actualCost_7bd390")}</dt><dd>{copy("native-grok.notReported_adadfa")}</dd>
  </dl><p>{copy("native-grok.theseCountersDescribeThisResponseOnly_9eefdb")}</p></section>;
}

function closedResponse(value: Document, thread: string, terminal: unknown) {
  if (!exact(value, ["responses", "message_bytes", "message_chunks", "text_bytes", "last_event", "last_chunk"]) || value.responses !== 1 || value.message_bytes !== 0 || value.message_chunks !== 0 || !Number.isInteger(value.text_bytes) || Number(value.text_bytes) < 0 || Number(value.text_bytes) > (4 << 20) || !count(value.last_chunk) || BigInt(value.last_chunk) === 0n) return false;
  const last = eventIndex(value.last_event, thread), end = eventIndex(terminal, thread);
  return last !== undefined && end !== undefined && last < end;
}

export function NativeGrokTerminal({ progress }: { progress: Document }) {
  useLocale();
  if (progress.grok_terminal == null) return null;
  const v = object(progress.grok_terminal), counts = object(v.counts), observed = object(progress.observed), content = object(progress.grok_content);
  const valid = uuid(progress.execution_id, 7) && uuid(progress.native_thread_id, 7) && uuid(progress.native_turn_id, 4) && progress.outcome === "succeeded" && exact(v, ["kind", "native_event_id", "timestamp_ms", "elapsed_ms", "model", "counts", "total_tokens", "model_calls", "api_duration_ms", "turns", "closure_id", "history_digest", ...(v.user !== undefined ? ["user"] : [])]) && v.kind === "closed-first-text" && eventIndex(v.native_event_id, progress.native_thread_id) !== undefined && count(v.timestamp_ms) && BigInt(v.timestamp_ms) <= 253402300799999n && [v.elapsed_ms, v.total_tokens, v.api_duration_ms].every(count) && v.model_calls === "1" && v.turns === "1" && uuid(v.closure_id, 7) && typeof v.history_digest === "string" && /^[a-f0-9]{64}$/.test(v.history_digest) && typeof v.model === "string" && v.model.length > 0 && v.model === observed.model && observed.grok_mode === "default" && closedResponse(content, progress.native_thread_id, v.native_event_id) && exact(counts, labels().map(([key]) => key)) && labels().every(([key]) => count(counts[key])) && [progress.grok_stop, progress.claude_terminal, progress.claude_stop, progress.claude_denial, progress.opencode_stop].every((value) => value == null);
  const userValid = v.user === undefined ? progress.grok_user_message_id === undefined : uuid(progress.grok_user_message_id, 7) && typeof progress.native_thread_id === "string" && userHistory(object(v.user), progress.native_thread_id) && object(v.user).model === v.model && eventIndex(object(v.user).native_event_id, progress.native_thread_id)! < eventIndex(content.last_event, progress.native_thread_id)!;
  if (!valid || !userValid) return <p>{copy("native-grok.theRetainedGrokCompletionIsUnavailable_0c1237")}</p>;
  return <Disclosure><DisclosureSummary>{copy("native-grok.originalGrokInputCompletion_0d2093")}</DisclosureSummary><dl>
    <dt>{copy("native-grok.nativeOutcome_826145")}</dt><dd>{copy("native-grok.endTurn_dacb1c")}</dd><dt>{copy("native-grok.model_5e2c61")}</dt><dd>{v.model as string}</dd>
    <dt>{copy("native-grok.reportedInputTotalTokens_a90319")}</dt><dd>{v.total_tokens as string}</dd>
    <dt>{copy("native-grok.nativeModelCalls_521256")}</dt><dd>{v.model_calls as string}</dd>
    <dt>{copy("native-grok.nativeApiDurationMs_796aa3")}</dt><dd>{v.api_duration_ms as string}</dd>
    <dt>{copy("native-grok.nativeElapsedTimeMs_3a0a11")}</dt><dd>{v.elapsed_ms as string}</dd>
  </dl><p>{copy("native-grok.theOriginalNativeSessionWasClosed_df240a")}</p></Disclosure>;
}

function stoppedInput(progress: Document) {
  const v = object(progress.grok_stop), observed = object(progress.observed), content = object(progress.grok_content);
  if (!uuid(progress.execution_id, 7) || !uuid(progress.native_thread_id, 7) || !uuid(progress.native_turn_id, 4)) return false;
  const thread = progress.native_thread_id;
  const beforeText = v.kind === "interrupted-before-text", interrupted = beforeText || v.kind === "interrupted-text", completed = v.kind === "completed-during-stop";
  const optional = ["category", "context_tokens", "completed", "retries", "message_id"].filter((key) => Object.hasOwn(v, key));
  if (!exact(v, ["kind", "request_id", "input_id", "input_request_id", "native_event_id", "timestamp_ms", "elapsed_ms", "model", "output_digest", "text_chunks", "delivered", "idle", "cleanup_joined", ...optional]) || (!interrupted && !completed) || progress.outcome !== (interrupted ? "stopped" : "succeeded") || [progress.grok_terminal, progress.claude_terminal, progress.claude_stop, progress.claude_denial, progress.opencode_stop].some((value) => value != null)) return false;
  const ids = [v.request_id, v.input_id, v.input_request_id, ...(beforeText ? [] : [v.message_id])];
  if (!ids.every((id) => uuid(id, 7) && id !== thread) || new Set(ids).size !== ids.length || v.input_id !== progress.input_id || v.delivered !== true || v.idle !== true || v.cleanup_joined !== true || typeof v.model !== "string" || !v.model || v.model !== observed.model || observed.grok_mode !== "default" || typeof v.output_digest !== "string" || !/^[a-f0-9]{64}$/.test(v.output_digest) || !Number.isInteger(v.text_chunks) || Number(v.text_chunks) < (beforeText ? 0 : 1) || Number(v.text_chunks) > 1024 || !count(v.timestamp_ms) || BigInt(v.timestamp_ms) > 253402300799999n || !count(v.elapsed_ms)) return false;
  const end = eventIndex(v.native_event_id, thread), last = eventIndex(content.last_event, thread);
  if (end === undefined || (!beforeText && (last === undefined || end <= last))) return false;
  let prior: bigint | undefined;
  if (v.retries !== undefined) {
    if (!Array.isArray(v.retries) || v.retries.length > 3) return false;
    for (const [index, value] of v.retries.entries()) {
      const retry = object(value), event = eventIndex(retry.native_event_id, thread);
      if (!exact(retry, ["native_event_id", "timestamp_ms", "kind", "error", "attempt", "max_retries"]) || retry.kind !== "retrying" || retry.error !== "http" || retry.attempt !== String(index + 1) || retry.max_retries !== "3" || !count(retry.timestamp_ms) || BigInt(retry.timestamp_ms) > 253402300799999n || event === undefined || event >= end || (prior !== undefined && event <= prior)) return false;
      prior = event;
    }
  }
  if (interrupted) {
    if (v.category !== "MidTurnAbort" || !count(v.context_tokens) || v.completed !== undefined || progress.latest_usage_id !== undefined) return false;
    if (beforeText) return v.message_id === undefined && v.text_chunks === 0 && v.output_digest === "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" && progress.grok_content === undefined;
    return exact(content, ["responses", "message_id", "message_bytes", "message_chunks", "text_bytes", "last_event", "last_chunk"]) && content.responses === 0 && content.message_id === v.message_id && content.message_chunks === v.text_chunks && Number.isInteger(content.message_bytes) && Number(content.message_bytes) > 0 && Number(content.message_bytes) <= (256 << 10) && content.text_bytes === content.message_bytes && count(content.last_chunk) && BigInt(content.last_chunk) > 0n;
  }
  const result = object(v.completed), counts = object(result.counts);
  return v.category === undefined && v.context_tokens === undefined && closedResponse(content, thread, v.native_event_id) && exact(result, ["counts", "total_tokens", "model_calls", "api_duration_ms", "turns"]) && result.model_calls === "1" && result.turns === "1" && [result.total_tokens, result.api_duration_ms].every(count) && exact(counts, labels().map(([key]) => key)) && labels().every(([key]) => count(counts[key]));
}

export function NativeGrokStop({ progress }: { progress: Document }) {
  useLocale();
  if (progress.grok_stop == null) return null;
  if (!stoppedInput(progress)) return <p>{copy("native-grok.theRetainedGrokStopIsUnavailable_6b1957")}</p>;
  const v = object(progress.grok_stop), result = object(v.completed), beforeText = v.kind === "interrupted-before-text", interrupted = beforeText || v.kind === "interrupted-text";
  const retries = Array.isArray(v.retries) ? v.retries : [];
  return <Disclosure><DisclosureSummary>{copy("native-grok.originalGrokStopResult_a32b1a")}</DisclosureSummary><dl>
    <dt>{copy("native-grok.nativeOutcome_826145")}</dt><dd>{beforeText ? copy("native-grok.interruptedBeforeFirstText_ec77f5") : interrupted ? copy("native-grok.interrupted_132d12") : copy("native-grok.completedWhileStopWasRequested_99bc08")}</dd>
    <dt>{copy("native-grok.model_5e2c61")}</dt><dd>{v.model as string}</dd>
    <dt>{copy("native-grok.nativeElapsedTimeMs_3a0a11")}</dt><dd>{v.elapsed_ms as string}</dd>
    {interrupted ? <><dt>{copy("native-grok.reportedContextTokens_7e562c")}</dt><dd>{v.context_tokens as string}</dd><dt>{copy("native-grok.inputUsage_5a19b1")}</dt><dd>{copy("native-grok.notReported_adadfa")}</dd></> : <><dt>{copy("native-grok.reportedInputTotalTokens_a90319")}</dt><dd>{result.total_tokens as string}</dd><dt>{copy("native-grok.nativeApiDurationMs_796aa3")}</dt><dd>{result.api_duration_ms as string}</dd></>}
    <dt>{copy("native-grok.observedHttpRetriesBeforeStopSettled_e5884b")}</dt><dd>{retries.length}</dd>
    <dt>{copy("native-grok.nativeProcessCleanup_f7f01d")}</dt><dd>{copy("native-grok.joined_69318b")}</dd>
    <dt>{copy("native-grok.workspaceCleanupReport_a4940f")}</dt><dd>{progress.cleanup_verified === true ? copy("native-grok.verified_4f7838") : copy("native-grok.notYetVerified_5b2f7a")}</dd>
  </dl><p><LocalizedText id="native-grok.thisResultDoesNotAuthorizeAnother_f4977e" components={{ s0: <>{beforeText ? copy("native-grok.noAssistantTextWasObservedContext_8f05d7") : interrupted ? copy("native-grok.partialOutputRemainsVisibleContextTokens_cf473f") : copy("native-grok.theOriginalSuccessfulResultIsRetained_9490e8")}</> }} /></p></Disclosure>;
}
