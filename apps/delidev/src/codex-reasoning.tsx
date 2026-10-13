// SPDX-License-Identifier: Apache-2.0
import { Disclosure, DisclosureSummary } from "./disclosure";
import { copy, useLocale } from "./localization";
import "./codex-reasoning.css";

enum DeltaKind {
  Summary = "reasoning-summary",
  Content = "reasoning-content",
  SummaryAdded = "reasoning-summary-added",
}
interface Snapshot {
  kind: "reasoning";
  text: "";
  summary: string[];
  content: string[];
}
interface Observation {
  sequence: number;
  delta: { kind: DeltaKind; index: number; text: string };
}
export interface CodexReasoningData {
  started: Snapshot;
  completed?: Snapshot;
  deltas: Observation[];
}
const record = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === "object" && !Array.isArray(value);
const only = (value: Record<string, unknown>, keys: string[]) =>
  Object.keys(value).every((key) => keys.includes(key));
const suppliedText = (value: unknown): value is string =>
  typeof value === "string" &&
  value.length <= 256 * 1024 &&
  !value.includes("\0") &&
  !/[\uD800-\uDFFF]/u.test(value) &&
  new TextEncoder().encode(value).length <= 256 * 1024;
function snapshot(value: unknown): value is Snapshot {
  if (
    !record(value) ||
    !only(value, [
      "kind",
      "text",
      "summary",
      "content",
      "revision",
      "image_generation",
    ]) ||
    value.kind !== "reasoning" ||
    value.text !== "" ||
    value.revision != null ||
    value.image_generation != null
  )
    return false;
  return [value.summary, value.content].every(
    (parts) =>
      Array.isArray(parts) && parts.length <= 1024 && parts.every(suppliedText),
  );
}

// Match the original indexed Codex snapshot/delta profile. This validator never
// joins deltas or reads encrypted/private fields. Invalid records retain the
// original generic artifact fallback, separate from OpenCode reasoning-text.
export function codexReasoning(
  value: unknown,
  state: string,
): CodexReasoningData | undefined {
  if (
    !record(value) ||
    !only(value, ["started", "completed", "deltas"]) ||
    !snapshot(value.started)
  )
    return undefined;
  if (value.completed != null && !snapshot(value.completed)) return undefined;
  if (
    (state === "complete" && value.completed == null) ||
    (state === "streaming" && value.completed != null)
  )
    return undefined;
  const deltas = value.deltas ?? [];
  if (!Array.isArray(deltas) || deltas.length > 10000) return undefined;
  let sequence = 0;
  for (const item of deltas) {
    if (
      !record(item) ||
      !only(item, ["sequence", "delta"]) ||
      typeof item.sequence !== "number" ||
      !Number.isSafeInteger(item.sequence) ||
      item.sequence <= sequence ||
      item.sequence > 100000 ||
      !record(item.delta)
    )
      return undefined;
    const delta = item.delta;
    if (
      !only(delta, ["kind", "index", "text"]) ||
      !Object.values(DeltaKind).includes(delta.kind as DeltaKind) ||
      typeof delta.index !== "number" ||
      !Number.isSafeInteger(delta.index) ||
      delta.index < 0 ||
      delta.index >= 1024 ||
      !suppliedText(delta.text) ||
      (delta.kind === DeltaKind.SummaryAdded && delta.text !== "")
    )
      return undefined;
    sequence = item.sequence;
  }
  return {
    started: value.started,
    completed:
      value.completed == null ? undefined : (value.completed as Snapshot),
    deltas: deltas as Observation[],
  };
}
// Common envelope metadata a Codex reasoning artifact message may carry. Role
// specific fields (phase, input_id, inherited, turn_timing, tool, progress and
// provider content) are foreign to this record and select the generic fallback.
const messageKeys = [
  "role",
  "state",
  "text",
  "artifact",
  "execution_id",
  "native_thread_id",
  "native_turn_id",
  "native_id",
  "native_parent_id",
  "first_sequence",
  "last_sequence",
  "context_revision",
  "attachments",
];
const stringKeys = [
  "execution_id",
  "native_thread_id",
  "native_turn_id",
  "native_id",
  "native_parent_id",
];
const counterKeys = ["first_sequence", "last_sequence", "context_revision"];
// Validates the complete retained message envelope, then its nested artifact.
// Text is never coerced: only absent or an empty string selects the disclosure.
// Rejected records are never stripped, repaired or partially rendered.
export function codexReasoningMessage(
  message: Record<string, unknown>,
  state: string,
): CodexReasoningData | undefined {
  if (
    message.role !== "artifact" ||
    !only(message, messageKeys) ||
    (message.text !== undefined && message.text !== "") ||
    typeof message.state !== "string" ||
    message.state !== state ||
    stringKeys.some(
      (key) => message[key] !== undefined && typeof message[key] !== "string",
    ) ||
    counterKeys.some(
      (key) =>
        message[key] !== undefined &&
        (typeof message[key] !== "number" ||
          !Number.isSafeInteger(message[key]) ||
          (message[key] as number) < 0),
    ) ||
    (message.attachments !== undefined && !Array.isArray(message.attachments))
  )
    return undefined;
  return codexReasoning(message.artifact, state);
}
function hasText(snapshot: Snapshot | undefined) {
  return (
    snapshot &&
    [...snapshot.summary, ...snapshot.content].some((part) => part.length > 0)
  );
}
function SnapshotText({ snapshot }: { snapshot: Snapshot }) {
  return (
    <>
      {(["summary", "content"] as const).map((family) =>
        snapshot[family].map((part, index) =>
          part.length ? (
            <div key={`${family}-${index}`}>
              <small>
                {copy(
                  family === "summary"
                    ? "native-reasoning.summaryPart"
                    : "native-reasoning.contentPart",
                  { index },
                )}
              </small>
              <pre>{part}</pre>
            </div>
          ) : null,
        ),
      )}
    </>
  );
}
export function CodexReasoning({ data }: { data: CodexReasoningData }) {
  useLocale();
  const summary =
    data.completed?.summary.find((part) => part.trim().length > 0) ??
    data.started.summary.find((part) => part.trim().length > 0) ??
    copy("native-reasoning.thinking");
  const initial = hasText(data.started),
    final = hasText(data.completed),
    streamed = data.deltas.some((item) => item.delta.text.length > 0);
  return (
    <Disclosure
      className="codex-reasoning"
      open={false}
      appearanceKind="reasoning_disclosure"
    >
      <DisclosureSummary>
        <span className="codex-reasoning-summary">{summary}</span>
      </DisclosureSummary>
      <div className="codex-reasoning-body">
        {initial ? (
          <section aria-label={copy("native-reasoning.initialObservation")}>
            <h4>{copy("native-reasoning.initialObservation")}</h4>
            <SnapshotText snapshot={data.started} />
          </section>
        ) : null}
        {streamed ? (
          <section aria-label={copy("session.streamedObservations_589dae")}>
            <h4>{copy("session.streamedObservations_589dae")}</h4>
            {data.deltas.map((item) =>
              item.delta.text.length ? (
                <div key={item.sequence}>
                  <small>
                    {item.delta.kind} {item.delta.index}
                  </small>
                  <pre>{item.delta.text}</pre>
                </div>
              ) : null,
            )}
          </section>
        ) : null}
        {final && data.completed ? (
          <section aria-label={copy("native-reasoning.finalObservation")}>
            <h4>{copy("native-reasoning.finalObservation")}</h4>
            <SnapshotText snapshot={data.completed} />
          </section>
        ) : null}
        {!initial && !streamed && !final ? (
          <p>{copy("native-reasoning.noSuppliedContent")}</p>
        ) : null}
      </div>
    </Disclosure>
  );
}
