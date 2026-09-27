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
  if (data.role !== "assistant" || !["streaming", "complete"].includes(String(data.state)) || typeof data.text !== "string" || data.text.includes("\0") || /[\uD800-\uDFFF]/u.test(data.text) || new TextEncoder().encode(data.text).length > (256 << 10) || !uuid(data.execution_id, 7) || !uuid(data.native_thread_id, 7) || !uuid(data.native_turn_id, 4) || data.phase != null || data.input_id !== undefined || data.native_parent_id !== undefined || [data.claude, data.claude_tool, data.claude_progress, data.claude_interruption, data.tool, data.artifact, data.progress].some((v) => v != null)) return false;
  const v = object(data.grok_text), chunks = v.chunks;
  if (!exact(v, ["response_ordinal", "chunks"]) || !ordinal(v.response_ordinal) || !Array.isArray(chunks) || chunks.length < 1 || chunks.length > 1024 || !Number.isSafeInteger(data.first_sequence) || Number(data.first_sequence) < 3 || !Number.isSafeInteger(data.last_sequence) || Number(data.last_sequence) > 100000 || Number(data.last_sequence) < Number(data.first_sequence) + chunks.length - (data.state === "complete" ? 0 : 1)) return false;
  let priorEvent: bigint | undefined, priorChunk: bigint | undefined;
  for (const raw of chunks) {
    const chunk = object(raw);
    if (!metadata(chunk, data.native_thread_id)) return false;
    const event = eventIndex(chunk.event_id, data.native_thread_id)!, number = BigInt(chunk.chunk_id as string);
    if (priorEvent !== undefined && (event <= priorEvent || number <= priorChunk!)) return false;
    priorEvent = event; priorChunk = number;
  }
  return data.native_id === object(chunks[0]).event_id;
}

export function NativeGrokText({ data }: { data: Document }) {
  if (!validText(data)) return <article className="message" aria-label="Grok text unavailable"><p>The retained Grok text is unavailable or inconsistent.</p></article>;
  const v = object(data.grok_text), chunks = v.chunks as Document[], first = chunks[0]!, last = chunks[chunks.length - 1]!;
  return <article className="message" aria-label="Grok assistant text">
    <header><strong>assistant</strong><small>{data.state === "complete" ? "Response text complete" : "Streaming"}</small></header>
    <pre>{data.text as string}</pre>
    <details><summary>Grok text details</summary><dl>
      <dt>Response order in this input</dt><dd>{v.response_ordinal as number}</dd>
      <dt>First native text event</dt><dd>{first.event_id as string}</dd>
      <dt>Latest native text event</dt><dd>{last.event_id as string}</dd>
      <dt>Latest native chunk</dt><dd>{last.chunk_id as string}</dd>
      <dt>Latest reported context tokens</dt><dd>{last.context_tokens as string}</dd>
    </dl><p>Context is a native estimate, separate from response usage. Completed response text does not mean the execution has finished.</p></details>
  </article>;
}

const labels = [["input_tokens", "Input tokens"], ["output_tokens", "Output tokens"], ["cache_read_input_tokens", "Cache read input tokens"], ["cache_creation_input_tokens", "Cache creation input tokens"], ["reasoning_tokens", "Reasoning tokens"]] as const;
export function NativeGrokUsage({ value }: { value: Document }) {
  const counts = object(value.counts);
  if (!exact(value, ["ordinal", "counts"]) || !ordinal(value.ordinal) || !exact(counts, labels.map(([key]) => key)) || !labels.every(([key]) => count(counts[key]))) return <p>The retained Grok usage is unavailable or inconsistent.</p>;
  return <section aria-label="Grok response usage"><p>Source: original completed response {value.ordinal as number} in this input</p><dl>
    {labels.map(([key, label]) => <div key={key}><dt>{label}</dt><dd>{counts[key] as string}</dd></div>)}
    <dt>Response total</dt><dd>Not reported</dd><dt>Actual cost</dt><dd>Not reported</dd>
  </dl><p>These counters describe this response only. They are separate from context estimates, input totals and auxiliary requests, and are not added to billed usage or session budgets.</p></section>;
}

function closedResponse(value: Document, thread: string, terminal: unknown) {
  if (!exact(value, ["responses", "message_bytes", "message_chunks", "text_bytes", "last_event", "last_chunk"]) || value.responses !== 1 || value.message_bytes !== 0 || value.message_chunks !== 0 || !Number.isInteger(value.text_bytes) || Number(value.text_bytes) < 0 || Number(value.text_bytes) > (4 << 20) || !count(value.last_chunk) || BigInt(value.last_chunk) === 0n) return false;
  const last = eventIndex(value.last_event, thread), end = eventIndex(terminal, thread);
  return last !== undefined && end !== undefined && last < end;
}

export function NativeGrokTerminal({ progress }: { progress: Document }) {
  if (progress.grok_terminal == null) return null;
  const v = object(progress.grok_terminal), counts = object(v.counts), observed = object(progress.observed), content = object(progress.grok_content);
  const valid = uuid(progress.execution_id, 7) && uuid(progress.native_thread_id, 7) && uuid(progress.native_turn_id, 4) && progress.outcome === "succeeded" && exact(v, ["kind", "native_event_id", "timestamp_ms", "elapsed_ms", "model", "counts", "total_tokens", "model_calls", "api_duration_ms", "turns", "closure_id", "history_digest"]) && v.kind === "closed-first-text" && eventIndex(v.native_event_id, progress.native_thread_id) !== undefined && count(v.timestamp_ms) && BigInt(v.timestamp_ms) <= 253402300799999n && [v.elapsed_ms, v.total_tokens, v.api_duration_ms].every(count) && v.model_calls === "1" && v.turns === "1" && uuid(v.closure_id, 7) && typeof v.history_digest === "string" && /^[a-f0-9]{64}$/.test(v.history_digest) && typeof v.model === "string" && v.model.length > 0 && v.model === observed.model && observed.grok_mode === "default" && closedResponse(content, progress.native_thread_id, v.native_event_id) && exact(counts, labels.map(([key]) => key)) && labels.every(([key]) => count(counts[key])) && [progress.claude_terminal, progress.claude_stop, progress.claude_denial, progress.opencode_stop].every((value) => value == null);
  if (!valid) return <p>The retained Grok completion is unavailable or inconsistent.</p>;
  return <details><summary>Original Grok input completion</summary><dl>
    <dt>Native outcome</dt><dd>End turn</dd><dt>Model</dt><dd>{v.model as string}</dd>
    <dt>Reported input total tokens</dt><dd>{v.total_tokens as string}</dd>
    <dt>Native model calls</dt><dd>{v.model_calls as string}</dd>
    <dt>Native API duration (ms)</dt><dd>{v.api_duration_ms as string}</dd>
    <dt>Native elapsed time (ms)</dt><dd>{v.elapsed_ms as string}</dd>
  </dl><p>The original native session was closed and its text history checked. Input totals overlap the response observation and do not report actual cost or auxiliary usage. Continuing this history requires separate support.</p></details>;
}
