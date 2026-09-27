import { object } from "./documents";
import { claudeToolReference, type ClaudeToolReference } from "./native-claude-tool";

enum BlockKind { Tool = "tool_use", Text = "text", Thinking = "thinking", Redacted = "redacted_thinking" }
enum BlockState { Streaming = "streaming", Completed = "completed", Stopped = "stopped", Interrupted = "interrupted" }
enum MessageState { Streaming = "streaming", Complete = "complete" }
const stopReasons = new Set(["end_turn", "max_tokens", "stop_sequence", "tool_use", "pause_turn", "refusal", "model_context_window_exceeded"]);
const blockLabels = { [BlockState.Streaming]: "Streaming", [BlockState.Completed]: "Content complete", [BlockState.Stopped]: "Stream closed", [BlockState.Interrupted]: "Interrupted · partial response" };
type Block = { tool?: ClaudeToolReference; index: number; kind: BlockKind; text: string; state: BlockState };

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
  let bytes = 0;
  for (const [index, value] of data.blocks.entries()) {
    const entry = object(value), block = object(entry.block);
    if (!keys(entry, ["index", "block", "state"]) || !keys(block, block.kind === BlockKind.Tool ? ["kind", "text", "tool"] : ["kind", "text"]) || entry.index !== index || !Object.values(BlockKind).includes(block.kind as BlockKind) || !Object.values(BlockState).includes(entry.state as BlockState) || !validText(block.text, 256 * 1024) || ((block.kind === BlockKind.Redacted || block.kind === BlockKind.Tool) && block.text !== "") || (block.kind === BlockKind.Tool && !claudeToolReference(block.tool)) || (interrupted ? entry.state !== BlockState.Interrupted || block.kind !== BlockKind.Text : entry.state === BlockState.Interrupted || (state === MessageState.Complete || index < data.blocks.length - 1) && entry.state !== BlockState.Stopped)) return undefined;
    bytes += new TextEncoder().encode(block.text).length;
    if (bytes > 256 * 1024) return undefined;
    blocks.push({ tool: claudeToolReference(block.tool), index, kind: block.kind as BlockKind, text: block.text, state: entry.state as BlockState });
  }
  return { blocks, reason: data.stop_reason as string | null, sequence: data.stop_sequence as string | null };
}

// Native text stays inert and ordered. Redacted reasoning has no reconstructed
// content; a provider message closing cannot imply successful session outcome.
export function NativeClaudeMessage({ content, state }: { content: unknown; state: string }) {
  const retained = message(content, state);
  if (!retained) return <section aria-label="Claude message unavailable"><p>The retained Claude message is unavailable or inconsistent.</p></section>;
  return <section aria-label="Claude message content">
    <ol>{retained.blocks.map((block) => <li key={block.index}>
      {block.kind === BlockKind.Tool ? <p>Tool proposal: {block.tool!.name}. <small>{blockLabels[block.state]}</small></p> : block.kind === BlockKind.Thinking ? <details><summary>Reasoning · {blockLabels[block.state]}</summary><pre>{block.text}</pre></details> : block.kind === BlockKind.Redacted ? <p>Reasoning was redacted by the harness. <small>{blockLabels[block.state]}</small></p> : <><pre>{block.text}</pre><small>{blockLabels[block.state]}</small></>}
    </li>)}</ol>
    {retained.reason !== null ? <p>Native stop reason: {retained.reason}</p> : null}
    {retained.sequence !== null ? <details><summary>Native stop sequence</summary><pre>{retained.sequence}</pre></details> : null}
  </section>;
}
