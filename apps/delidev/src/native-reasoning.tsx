import { LocalizedText, copy, useLocale } from "./localization";
import { object } from "./documents";

enum ReasoningState {
  Streaming = "streaming",
  Complete = "complete",
}

function snapshot(value: unknown): string | undefined {
  const data = object(value);
  if (data.kind !== "reasoning-text" || typeof data.text !== "string" || data.summary != null || data.content != null || data.revision != null) return undefined;
  return data.text;
}

function reasoningText(artifact: Record<string, unknown>, state: string): string | undefined {
  const started = snapshot(artifact.started);
  if (started === undefined || (state !== ReasoningState.Streaming && state !== ReasoningState.Complete)) return undefined;
  const deltas = artifact.deltas ?? [];
  if (!Array.isArray(deltas) || deltas.length > 10000) return undefined;
  const parts = [started];
  let length = started.length;
  if (length > 256 * 1024) return undefined;
  let sequence = 0;
  for (const value of deltas) {
    const observation = object(value);
    const delta = object(observation.delta);
    if (typeof observation.sequence !== "number" || !Number.isSafeInteger(observation.sequence) || observation.sequence <= sequence || observation.sequence > 100000 || delta.kind !== "reasoning-text" || delta.index != null || typeof delta.text !== "string") return undefined;
    sequence = observation.sequence;
    length += delta.text.length;
    if (length > 256 * 1024) return undefined;
    parts.push(delta.text);
  }
  const streamed = parts.join("");
  if (new TextEncoder().encode(streamed).length > 256 * 1024 || streamed.includes("\0") || /[\uD800-\uDFFF]/u.test(streamed)) return undefined;
  if (state === ReasoningState.Streaming) return artifact.completed == null ? streamed : undefined;
  const completed = snapshot(artifact.completed);
  return completed === streamed ? completed : undefined;
}

// The server's original reasoning part stays separate from assistant answers
// and indexed summaries. Render plain text once, without executing native HTML.
export function NativeReasoning({ artifact, state }: { artifact: Record<string, unknown>; state: string }) {
  useLocale();
  const content = reasoningText(artifact, state);
  return <details>
    <summary><LocalizedText id="native-reasoning.reasoning_4d3137" components={{ s0: <>{content === undefined ? copy("native-reasoning.unavailable_ca1844") : state === ReasoningState.Complete ? copy("native-reasoning.complete_143b27") : copy("native-reasoning.streaming_a951c5")}</> }} /></summary>
    {content === undefined ? <p>{copy("native-reasoning.theRetainedReasoningContentIsUnavailable_d2c416")}</p> : <pre>{content}</pre>}
  </details>;
}
