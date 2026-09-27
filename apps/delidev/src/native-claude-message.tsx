import { object } from "./documents";

enum BlockKind { Text = "text", Thinking = "thinking", Redacted = "redacted_thinking" }
enum BlockState { Streaming = "streaming", Completed = "completed", Stopped = "stopped" }
enum MessageState { Streaming = "streaming", Complete = "complete" }
const stopReasons = new Set(["end_turn", "max_tokens", "stop_sequence", "tool_use", "pause_turn", "refusal", "model_context_window_exceeded"]);
const blockLabels = { [BlockState.Streaming]: "Streaming", [BlockState.Completed]: "Content complete", [BlockState.Stopped]: "Stream closed" };
type Block = { index: number; kind: BlockKind; text: string; state: BlockState };

function keys(value: Record<string, unknown>, expected: string[]) {
  return Object.keys(value).length === expected.length && expected.every((key) => Object.hasOwn(value, key));
}
function validText(value: unknown, max: number): value is string {
  return typeof value === "string" && value.length <= max && !value.includes("\0") && !/[\uD800-\uDFFF]/u.test(value) && new TextEncoder().encode(value).length <= max;
}
function message(value: unknown, state: string): { blocks: Block[]; reason: string | null; sequence: string | null } | undefined {
  const data = object(value);
  if (!keys(data, ["model", "blocks", "stop_reason", "stop_sequence"]) || !validText(data.model, 256) || !data.model || !Array.isArray(data.blocks) || data.blocks.length > 1024 || (state !== MessageState.Streaming && state !== MessageState.Complete) || (data.stop_reason !== null && (typeof data.stop_reason !== "string" || !stopReasons.has(data.stop_reason))) || (data.stop_sequence !== null && !validText(data.stop_sequence, 256 * 1024))) return undefined;
  const blocks: Block[] = [];
  let bytes = 0;
  for (const [index, value] of data.blocks.entries()) {
    const entry = object(value), block = object(entry.block);
    if (!keys(entry, ["index", "block", "state"]) || !keys(block, ["kind", "text"]) || entry.index !== index || !Object.values(BlockKind).includes(block.kind as BlockKind) || !Object.values(BlockState).includes(entry.state as BlockState) || !validText(block.text, 256 * 1024) || (block.kind === BlockKind.Redacted && block.text !== "") || ((state === MessageState.Complete || index < data.blocks.length - 1) && entry.state !== BlockState.Stopped)) return undefined;
    bytes += new TextEncoder().encode(block.text).length;
    if (bytes > 256 * 1024) return undefined;
    blocks.push({ index, kind: block.kind as BlockKind, text: block.text, state: entry.state as BlockState });
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
      {block.kind === BlockKind.Thinking ? <details><summary>Reasoning · {blockLabels[block.state]}</summary><pre>{block.text}</pre></details> : block.kind === BlockKind.Redacted ? <p>Reasoning was redacted by the harness. <small>{blockLabels[block.state]}</small></p> : <><pre>{block.text}</pre><small>{blockLabels[block.state]}</small></>}
    </li>)}</ol>
    {retained.reason !== null ? <p>Native stop reason: {retained.reason}</p> : null}
    {retained.sequence !== null ? <details><summary>Native stop sequence</summary><pre>{retained.sequence}</pre></details> : null}
  </section>;
}
