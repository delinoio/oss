// SPDX-License-Identifier: Apache-2.0
import { Disclosure, DisclosureSummary } from "./disclosure";
import { copy, LocalizedText, useLocale } from "./localization";
import { object, type Document } from "./documents";
import { statusLabel } from "./product-status";

interface Snapshot { kind: "reasoning"; text: ""; summary: string[]; content: string[] }
interface Delta { sequence: number; delta: { kind: "reasoning-summary" | "reasoning-content" | "reasoning-summary-added"; index: number; text: string } }
export interface CodexReasoning { started: Snapshot; completed?: Snapshot; deltas: Delta[] }
const closed = (value: Document, keys: readonly string[]) => Object.keys(value).every(key => keys.includes(key));
const validText = (value: unknown): value is string => typeof value === "string" && !value.includes("\0") && !/[\uD800-\uDFFF]/u.test(value) && new TextEncoder().encode(value).length <= 256 * 1024;
function snapshot(value: unknown): Snapshot | undefined {
 const data = object(value);
 if (!closed(data, ["kind", "text", "summary", "content"]) || data.kind !== "reasoning" || data.text !== "" ||
  ![data.summary, data.content].every(parts => Array.isArray(parts) && parts.length <= 1024 && parts.every(validText))) return;
 return data as unknown as Snapshot;
}
/** Recognize only Codex indexed source shapes. Other/malformed artifacts retain their original fallback. */
export function codexReasoning(data: Document): CodexReasoning | undefined {
 if (["grok_text", "grok_user", "grok_tool", "claude", "claude_tool", "claude_progress", "claude_interruption"].some(key => Object.hasOwn(data, key)) || data.attachments != null && (!Array.isArray(data.attachments) || data.attachments.length > 0) || data.role !== "artifact" || data.text !== "" || data.tool != null || data.progress != null || data.phase != null || data.input_id != null) return;
 const artifact = object(data.artifact), started = snapshot(artifact.started);
 if (!started || !closed(artifact, ["started", "completed", "deltas"])) return;
 const completed = artifact.completed == null ? undefined : snapshot(artifact.completed);
 if (artifact.completed != null && !completed) return;
 const deltas = artifact.deltas ?? [];
 if (!Array.isArray(deltas) || deltas.length > 100000) return;
 let sequence = 0;
 for (const entry of deltas) {
  const observation = object(entry), delta = object(observation.delta);
  if (!closed(observation, ["sequence", "delta"]) || !Number.isSafeInteger(observation.sequence) || Number(observation.sequence) <= sequence || Number(observation.sequence) > 100000 ||
   !closed(delta, ["kind", "index", "text"]) || typeof delta.kind !== "string" || !["reasoning-summary", "reasoning-content", "reasoning-summary-added"].includes(delta.kind) ||
   !Number.isInteger(delta.index) || Number(delta.index) < 0 || Number(delta.index) >= 1024 || !validText(delta.text) || delta.kind === "reasoning-summary-added" && delta.text !== "") return;
  sequence = Number(observation.sequence);
 }
 return { started, completed, deltas: deltas as Delta[] };
}
function hasText(value?: Snapshot) { return value && [...value.summary, ...value.content].some(part => part.length > 0); }
function Observation({ value, label }: { value: Snapshot; label: string }) {
 return <section aria-label={label}><h3>{label}</h3>{(["summary", "content"] as const).map(family => value[family].map((part, index) =>
  <div key={`${family}:${index}`} data-reasoning-family={family} data-reasoning-index={index}><small><LocalizedText id={family === "summary" ? "codex-reasoning.summaryPart" : "codex-reasoning.contentPart"} components={{ s0: <>{index}</> }}/></small><pre>{part}</pre></div>))}</section>;
}
export function CodexReasoningDisclosure({ value, state, previousContext = false }: { value: CodexReasoning; state: string; previousContext?: boolean }) {
 useLocale();
 const summary = value.completed?.summary.find(part => part.trim().length > 0) ?? value.started.summary.find(part => part.trim().length > 0) ?? copy("codex-reasoning.thinking");
 const streamed = value.deltas.some(entry => entry.delta.text.length > 0);
 const supplied = hasText(value.started) || streamed || hasText(value.completed);
 return <article className="message message-codex-reasoning" aria-label={copy("codex-reasoning.thinking")}>
  {state && state !== "complete" && state !== "completed" ? <header><small>{statusLabel(state)}</small></header> : null}
  {previousContext ? <small>{copy("session.previousContext")}</small> : null}
  <Disclosure className="codex-reasoning" appearanceKind="reasoning_disclosure" open={false}>
   <DisclosureSummary aria-label={summary}><span className="codex-reasoning-summary" title={summary}>{summary}</span></DisclosureSummary>
   <div className="codex-reasoning-body">
    {hasText(value.started) ? <Observation value={value.started} label={copy("codex-reasoning.initial")}/> : null}
    {streamed ? <section aria-label={copy("session.streamedObservations_589dae")}><h3>{copy("session.streamedObservations_589dae")}</h3>{value.deltas.map(entry => <div key={entry.sequence} data-reasoning-kind={entry.delta.kind} data-reasoning-index={entry.delta.index} data-reasoning-sequence={entry.sequence}><small>{entry.delta.kind} · {entry.delta.index}</small><pre>{entry.delta.text}</pre></div>)}</section> : null}
    {hasText(value.completed) ? <Observation value={value.completed!} label={copy("codex-reasoning.final")}/> : null}
    {!supplied ? <p>{copy("codex-reasoning.empty")}</p> : null}
   </div>
  </Disclosure>
 </article>;
}
