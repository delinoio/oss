import { Disclosure, DisclosureSummary } from "./disclosure";
import { LocalizedText, copy, useLocale } from "./localization";
import { NativeClaudeCitations, validClaudeCitationHistory, claudeCitationHistoryBytes } from "./native-claude-citations";
import { object, type Document } from "./documents";
import { claudeToolReference, type ClaudeToolReference } from "./native-claude-tool";

enum BlockKind { Tool = "tool_use", Text = "text", Thinking = "thinking", Redacted = "redacted_thinking" }
enum BlockState { Streaming = "streaming", Completed = "completed", Stopped = "stopped", Interrupted = "interrupted" }
enum MessageState { Streaming = "streaming", Complete = "complete" }
const stopReasons = new Set(["end_turn", "max_tokens", "stop_sequence", "tool_use", "pause_turn", "refusal", "model_context_window_exceeded"]);
const blockLabels = { get [BlockState.Streaming]() { return copy("native-claude-message.streaming_a951c5"); }, get [BlockState.Completed]() { return copy("native-claude-message.contentComplete_829c09"); }, get [BlockState.Stopped]() { return copy("native-claude-message.streamClosed_5eab18"); }, get [BlockState.Interrupted]() { return copy("native-claude-message.interruptedPartialResponse_637011"); } };
type Block = { citations?: Document; tool?: ClaudeToolReference; index: number; kind: BlockKind; text: string; state: BlockState };

function keys(value: Record<string, unknown>, expected: string[]) {
  return Object.keys(value).length === expected.length && expected.every((key) => Object.hasOwn(value, key));
}
function validText(value: unknown, max: number): value is string {
  return typeof value === "string" && value.length <= max && !value.includes("\0") && !/[\uD800-\uDFFF]/u.test(value) && new TextEncoder().encode(value).length <= max;
}
function message(value: unknown, state: string): { blocks: Block[]; reason: string | null; sequence: string | null } | undefined {
  const data = object(value);
  const interrupted = Object.hasOwn(data, "interruption"), proof = object(data.interruption);
  const nativeID = (v: unknown): v is string => typeof v === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-[47][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(v);
  if (interrupted && (!keys(proof, ["request_id", "native_event_id", "evidence"]) || !["aborted-assistant", "closed-stream-before-retry"].includes(String(proof.evidence)) || !nativeID(proof.request_id) || proof.request_id[14] !== "7" || !nativeID(proof.native_event_id) || proof.request_id === proof.native_event_id || state !== MessageState.Complete || data.stop_reason !== null || data.stop_sequence !== null || !Array.isArray(data.blocks) || data.blocks.length !== 1)) return undefined;
  if (!keys(data, ["model", "blocks", "stop_reason", "stop_sequence", ...(interrupted ? ["interruption"] : [])]) || !validText(data.model, 256) || !data.model || !Array.isArray(data.blocks) || data.blocks.length > 1024 || (state !== MessageState.Streaming && state !== MessageState.Complete) || (data.stop_reason !== null && (typeof data.stop_reason !== "string" || !stopReasons.has(data.stop_reason))) || (data.stop_sequence !== null && !validText(data.stop_sequence, 256 * 1024))) return undefined;
  const blocks: Block[] = [];
  let bytes = 0, citationBytes = 0;
  for (const [index, value] of data.blocks.entries()) {
    const entry = object(value), block = object(entry.block);
    if (!keys(entry, ["index", "block", "state", ...(Object.hasOwn(entry,"citations")?["citations"]:[])]) || !keys(block, block.kind === BlockKind.Tool ? ["kind", "text", "tool"] : ["kind", "text"]) || entry.index !== index || !Object.values(BlockKind).includes(block.kind as BlockKind) || !Object.values(BlockState).includes(entry.state as BlockState) || !validText(block.text, 256 * 1024) || ((block.kind === BlockKind.Redacted || block.kind === BlockKind.Tool) && block.text !== "") || (block.kind === BlockKind.Tool && !claudeToolReference(block.tool)) || (interrupted ? entry.state !== BlockState.Interrupted || block.kind !== BlockKind.Text : entry.state === BlockState.Interrupted || (state === MessageState.Complete || index < data.blocks.length - 1) && entry.state !== BlockState.Stopped)) return undefined;
    if (Object.hasOwn(entry,"citations")) {
      if (interrupted || block.kind !== BlockKind.Text || !validClaudeCitationHistory(entry.citations, String(entry.state))) return undefined;
      citationBytes += claudeCitationHistoryBytes(entry.citations);
      if (citationBytes > 256 * 1024) return undefined;
    }
    bytes += new TextEncoder().encode(block.text).length;
    if (bytes > 256 * 1024) return undefined;
    blocks.push({ citations: entry.citations as Document | undefined, tool: claudeToolReference(block.tool), index, kind: block.kind as BlockKind, text: block.text, state: entry.state as BlockState });
  }
  return { blocks, reason: data.stop_reason as string | null, sequence: data.stop_sequence as string | null };
}

// Native text stays inert and ordered. Redacted reasoning has no reconstructed
// content; a provider message closing cannot imply successful session outcome.
export function NativeClaudeMessage({ content, state }: { content: unknown; state: string }) {
  useLocale();
  const retained = message(content, state);
  if (!retained) return <section aria-label={copy("native-claude-message.claudeMessageUnavailable_9efc2c")}><p>{copy("native-claude-message.theRetainedClaudeMessageIsUnavailable_453226")}</p></section>;
  return <section className="native-claude-message-content" aria-label={copy("native-claude-message.claudeMessageContent_fa712d")}>
    <ol>{retained.blocks.map((block) => <li key={block.index}>
      {block.kind === BlockKind.Tool ? <p><LocalizedText id="native-claude-message.toolProposal_e8bdb5" components={{ s0: <>{block.tool!.name}</>, s1: <small>{blockLabels[block.state]}</small> }} /></p> : block.kind === BlockKind.Thinking ? <Disclosure><DisclosureSummary><LocalizedText id="native-claude-message.reasoning_4d3137" components={{ s0: <>{blockLabels[block.state]}</> }} /></DisclosureSummary><pre>{block.text}</pre></Disclosure> : block.kind === BlockKind.Redacted ? <p><LocalizedText id="native-claude-message.reasoningWasRedactedByTheHarness_4aeaa3" components={{ s0: <small>{blockLabels[block.state]}</small> }} /></p> : <><pre>{block.text}</pre><small>{blockLabels[block.state]}</small></>}
      {block.citations ? <NativeClaudeCitations value={block.citations} /> : null}
    </li>)}</ol>
    {retained.reason !== null ? <p><LocalizedText id="native-claude-message.nativeStopReason_e2e029" components={{ s0: <>{retained.reason}</> }} /></p> : null}
    {retained.sequence !== null ? <Disclosure><DisclosureSummary>{copy("native-claude-message.nativeStopSequence_96951d")}</DisclosureSummary><pre>{retained.sequence}</pre></Disclosure> : null}
  </section>;
}
