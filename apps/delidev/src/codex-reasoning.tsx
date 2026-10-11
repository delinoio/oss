// SPDX-License-Identifier: Apache-2.0
import { Disclosure, DisclosureSummary } from "./disclosure";
import { copy, useLocale } from "./localization";
import "./codex-reasoning.css";

enum DeltaKind { Summary = "reasoning-summary", Content = "reasoning-content", SummaryAdded = "reasoning-summary-added" }
interface Snapshot { kind: "reasoning"; text: string; summary: string[]; content: string[] }
interface Observation { sequence: number; delta: { kind: DeltaKind; index: number; text: string } }
export interface CodexReasoningObservation { started: Snapshot; completed?: Snapshot; deltas: Observation[] }
const record = (value: unknown): value is Record<string, unknown> => value !== null && typeof value === "object" && !Array.isArray(value);
const suppliedText = (value: unknown): value is string => typeof value === "string" && value.length <= 256 * 1024 && !value.includes("\0") && !/[\uD800-\uDFFF]/u.test(value) && new TextEncoder().encode(value).length <= 256 * 1024;
function snapshot(value: unknown): value is Snapshot {
  return record(value) && Object.keys(value).every(key => ["kind", "text", "summary", "content", "revision", "image_generation"].includes(key)) && value.kind === "reasoning" && value.text === "" && value.revision == null && value.image_generation == null && [value.summary, value.content].every(parts => Array.isArray(parts) && parts.length <= 1024 && parts.every(suppliedText));
}

// A narrow presentation admission only. Malformed/foreign observations keep the
// existing generic artifact fallback; OpenCode's reasoning-text is independent.
export function codexReasoningObservation(value: unknown): CodexReasoningObservation | undefined {
  if (!record(value) || !Object.keys(value).every(key => ["started", "completed", "deltas"].includes(key)) || !snapshot(value.started) || value.completed != null && !snapshot(value.completed)) return undefined;
  const deltas = value.deltas ?? [];
  if (!Array.isArray(deltas) || deltas.length > 100000) return undefined;
  let sequence = 0;
  for (const observation of deltas) {
    if (!record(observation) || Object.keys(observation).some(key => !["sequence", "delta"].includes(key)) || !Number.isSafeInteger(observation.sequence) || Number(observation.sequence) <= sequence || Number(observation.sequence) > 100000 || !record(observation.delta)) return undefined;
    const delta = observation.delta;
    if (Object.keys(delta).some(key => !["kind", "index", "text"].includes(key)) || !Object.values(DeltaKind).includes(delta.kind as DeltaKind) || !Number.isSafeInteger(delta.index) || Number(delta.index) < 0 || Number(delta.index) >= 1024 || !suppliedText(delta.text) || delta.kind === DeltaKind.SummaryAdded && delta.text !== "") return undefined;
    sequence = Number(observation.sequence);
  }
  return { started: value.started, completed: value.completed == null ? undefined : value.completed as Snapshot, deltas: deltas as Observation[] };
}
function hasText(snapshot: Snapshot | undefined) { return snapshot !== undefined && [snapshot.text, ...snapshot.summary, ...snapshot.content].some(part => part.length > 0); }
function OriginalSnapshot({ value, final = false }: { value: Snapshot; final?: boolean }) {
  return <section aria-label={copy(final ? "codex-reasoning.final" : "codex-reasoning.initial")}>
    <h4>{copy(final ? "codex-reasoning.final" : "codex-reasoning.initial")}</h4>
    {value.text.length > 0 ? <pre>{value.text}</pre> : null}
    {(["summary", "content"] as const).map(family => value[family].length > 0 ? <div key={family}>
      <h5>{copy(family === "summary" ? "codex-reasoning.summary" : "codex-reasoning.content")}</h5>
      {value[family].map((part, index) => <div key={index}><small>{copy("codex-reasoning.part", { v0: index })}</small><pre>{part}</pre></div>)}
    </div> : null)}
  </section>;
}
export function CodexReasoning({ observation }: { observation: CodexReasoningObservation }) {
  useLocale();
  const summary = observation.completed?.summary.find(part => part.trim().length > 0) ?? observation.started.summary.find(part => part.trim().length > 0) ?? copy("codex-reasoning.thinking");
  const streamed = observation.deltas.some(part => part.delta.text.length > 0);
  const initial = hasText(observation.started), final = hasText(observation.completed);
  return <Disclosure className="codex-reasoning" appearanceKind="reasoning_disclosure" open={false}>
    <DisclosureSummary><span className="codex-reasoning-label">{summary}</span></DisclosureSummary>
    {initial ? <OriginalSnapshot value={observation.started} /> : null}
    {streamed ? <section aria-label={copy("codex-reasoning.streamed")}><h4>{copy("codex-reasoning.streamed")}</h4>{observation.deltas.map(({ sequence, delta }) => <div key={sequence}><small>{delta.kind} [{delta.index}] · {copy("codex-reasoning.sequence", { v0: sequence })}</small><pre>{delta.text}</pre></div>)}</section> : null}
    {final && observation.completed ? <OriginalSnapshot value={observation.completed} final /> : null}
    {!initial && !streamed && !final ? <p>{copy("codex-reasoning.empty")}</p> : null}
  </Disclosure>;
}
