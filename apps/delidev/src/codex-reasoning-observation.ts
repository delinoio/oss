// SPDX-License-Identifier: Apache-2.0
export enum CodexReasoningDeltaKind {
  Summary = "reasoning-summary", Content = "reasoning-content", SummaryAdded = "reasoning-summary-added",
}
export interface CodexReasoningPart { index: number; text: string }
export interface CodexReasoningSnapshot { summary: CodexReasoningPart[]; content: CodexReasoningPart[] }
export interface CodexReasoningDelta { sequence: number; kind: CodexReasoningDeltaKind; index: number; text: string }
export interface CodexReasoningObservation {
  summary?: string; initial: CodexReasoningSnapshot; deltas: CodexReasoningDelta[]; final?: CodexReasoningSnapshot;
}
function record(value: unknown): Record<string, unknown> | undefined {
  return value !== null && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : undefined;
}
function fields(value: Record<string, unknown>, allowed: string[]): boolean { return Object.keys(value).every(key => allowed.includes(key)); }
function originalText(value: unknown): value is string {
  return typeof value === "string" && value.length <= 256 * 1024 && !value.includes("\0") && !/[\uD800-\uDFFF]/u.test(value) && new TextEncoder().encode(value).length <= 256 * 1024;
}
function snapshot(value: unknown): CodexReasoningSnapshot | undefined {
  const data = record(value);
  if (!data || !fields(data, ["kind", "text", "summary", "content"]) || data.kind !== "reasoning" || data.text !== "") return;
  const parts = (value: unknown): CodexReasoningPart[] | undefined => Array.isArray(value) && value.length <= 1024 && value.every(originalText) ? value.map((text, index) => ({ index, text })) : undefined;
  const summary = parts(data.summary), content = parts(data.content);
  return summary && content ? { summary, content } : undefined;
}
/** Read only supplied indexed observations; never concatenate, summarize or recover absent text. */
export function readCodexReasoning(value: unknown, state: string): CodexReasoningObservation | undefined {
  const artifact = record(value);
  if (!artifact || !fields(artifact, ["started", "deltas", "completed"]) || !["streaming", "complete", "completed", "interrupted", "failed", "unavailable", "recovery-required"].includes(state)) return;
  const initial = snapshot(artifact.started), final = artifact.completed === undefined ? undefined : snapshot(artifact.completed);
  if (!initial || artifact.completed !== undefined && !final || ["complete", "completed"].includes(state) && !final || state === "streaming" && final) return;
  const source = artifact.deltas === undefined ? [] : artifact.deltas;
  if (!Array.isArray(source) || source.length > 10000) return;
  const deltas: CodexReasoningDelta[] = [];
  let previous = 0;
  for (const item of source) {
    const observation = record(item), delta = record(observation?.delta);
    if (!observation || !fields(observation, ["sequence", "delta"]) || !Number.isSafeInteger(observation.sequence) || Number(observation.sequence) <= previous || !delta || !fields(delta, ["kind", "index", "text"]) || !Object.values(CodexReasoningDeltaKind).includes(delta.kind as CodexReasoningDeltaKind) || !Number.isInteger(delta.index) || Number(delta.index) < 0 || Number(delta.index) >= 1024 || !originalText(delta.text) || delta.kind === CodexReasoningDeltaKind.SummaryAdded && delta.text !== "") return;
    previous = Number(observation.sequence);
    deltas.push({ sequence: previous, kind: delta.kind as CodexReasoningDeltaKind, index: Number(delta.index), text: delta.text });
  }
  const summary = final?.summary.find(part => part.text.trim() !== "")?.text ?? initial.summary.find(part => part.text.trim() !== "")?.text;
  return { summary, initial, deltas, final };
}
