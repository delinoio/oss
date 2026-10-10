// SPDX-License-Identifier: Apache-2.0
import { Disclosure, DisclosureSummary } from "./disclosure";
import { copy, useLocale } from "./localization";
import { CodexReasoningDeltaKind, type CodexReasoningObservation, type CodexReasoningSnapshot } from "./codex-reasoning-observation";
import "./codex-reasoning.css";
function hasText(snapshot?: CodexReasoningSnapshot) { return Boolean(snapshot && [...snapshot.summary, ...snapshot.content].some(part => part.text.length > 0)); }
function Snapshot({ value, label }: { value: CodexReasoningSnapshot; label: string }) {
  return <section aria-label={label}><h4>{label}</h4>{([['summary', value.summary], ['content', value.content]] as const).map(([kind, parts]) => parts.filter(part => part.text.length > 0).map(part => <div key={`${kind}:${part.index}`}>
    <p className="codex-reasoning-part-label">{copy(kind === "summary" ? "native-reasoning.codexSummaryPart" : "native-reasoning.codexContentPart", { index: part.index })}</p><pre>{part.text}</pre>
  </div>))}</section>;
}
/** The existing native disclosure owns preference, keyboard and manual expansion lifetime. */
export function CodexReasoning({ observation }: { observation: CodexReasoningObservation }) {
  useLocale();
  const initial = hasText(observation.initial), final = hasText(observation.final), streamed = observation.deltas.some(delta => delta.text.length > 0);
  const summary = observation.summary ?? copy("native-reasoning.codexThinking");
  return <Disclosure className="codex-reasoning" appearanceKind="reasoning_disclosure" open={false}>
    <DisclosureSummary aria-label={summary}><span className="codex-reasoning-summary">{summary}</span></DisclosureSummary>
    <div className="codex-reasoning-body">
      {initial ? <Snapshot value={observation.initial} label={copy("native-reasoning.codexInitial")} /> : null}
      {streamed ? <section aria-label={copy("native-reasoning.codexStreamed")}><h4>{copy("native-reasoning.codexStreamed")}</h4>{observation.deltas.map(delta => <div key={delta.sequence}>
        <p className="codex-reasoning-part-label">{copy("native-reasoning.codexDelta", { kind: delta.kind, index: delta.index, sequence: delta.sequence })}</p>{delta.kind !== CodexReasoningDeltaKind.SummaryAdded ? <pre>{delta.text}</pre> : null}
      </div>)}</section> : null}
      {final && observation.final ? <Snapshot value={observation.final} label={copy("native-reasoning.codexFinal")} /> : null}
      {!initial && !streamed && !final ? <p>{copy("native-reasoning.codexEmpty")}</p> : null}
    </div>
  </Disclosure>;
}
