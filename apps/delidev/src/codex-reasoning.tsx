// SPDX-License-Identifier: Apache-2.0
import { useMemo } from "react";
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
// Display-only helpers. They read the retained record and never mutate it, and
// they never log or transmit reasoning text.
const hasVisibleText = (parts: readonly string[]) =>
  parts.some((part) => part.trim().length > 0);
const deltaParts = (data: CodexReasoningData, kind: DeltaKind) =>
  data.deltas
    .filter((item) => item.delta.kind === kind)
    .sort((left, right) => left.sequence - right.sequence)
    .map((item) => item.delta.text);

// Selects exactly one source family for the visible body. Families are never
// mixed, so a completed content snapshot replaces (never extends) the stream.
export function reasoningBodyParts(data: CodexReasoningData): string[] {
  const sources = [
    () => data.completed?.content ?? [],
    () => deltaParts(data, DeltaKind.Content),
    () => data.started.content,
    () => data.completed?.summary ?? [],
    () => deltaParts(data, DeltaKind.Summary),
    () => data.started.summary,
  ];
  for (const source of sources) {
    const parts = source();
    if (hasVisibleText(parts)) return parts;
  }
  return [];
}

// Straight double quotes and straight single quotes are ambiguous, so they are
// classified by joiner state instead of membership in these sets. The curly
// single closing quote stays here because it also serves as an apostrophe.
const closingPunctuation = new Set(
  Array.from(".,;:!?)]}%…»”’、。，．：；！？）］｝」』】〉》"),
);
const openingPunctuation = new Set(Array.from("([{«“‘¡¿「『【〈《（［｛"));
const isSpace = (char: string | undefined) =>
  char !== undefined && /^\s$/u.test(char);
const isWordChar = (char: string | undefined) =>
  char !== undefined && /^[\p{L}\p{N}]$/u.test(char);
// Apostrophe suffixes that can start a part when a word is split at an
// apostrophe ("It" + "'s"), and word-initial elisions ("'tis", "'em", "'90s").
// Both patterns only inspect a short prefix, so matching stays constant time.
const contractionSuffix = /^'(?:s|t|re|ve|ll|d|m)(?![\p{L}\p{N}'])/iu;
const leadingElision =
  /^'(?:(?:tis|twas|em|cause|til)(?![\p{L}\p{N}])|\d\d(?!\d*'))/iu;
const lookaheadLength = 12;
// A leading quote directly followed by a word or opening character opens a
// quoted span; one followed by whitespace, punctuation or nothing closes it.
const startsQuotedWord = (core: string) => {
  const next = Array.from(core.slice(1, 3))[0];
  return (
    next !== undefined &&
    (isWordChar(next) || openingPunctuation.has(next) || next === '"')
  );
};
const firstCodePoint = (text: string) =>
  String.fromCodePoint(text.codePointAt(0)!);
const lastCodePoint = (text: string) => Array.from(text.slice(-2)).at(-1)!;

enum QuoteKind {
  Opening = "opening",
  Closing = "closing",
  Apostrophe = "apostrophe",
}
interface JoinState {
  // Currently unclosed straight or curly single quotes (apostrophes excluded).
  singleDepth: number;
  // True while the straight double quote count is odd.
  doubleOpen: boolean;
  // True when the last emitted character is an opening quote.
  lastOpening: boolean;
}

// Classifies a straight single quote that has a known previous character.
function singleQuoteKind(
  chars: readonly string[],
  index: number,
  state: JoinState,
  previousOpening: boolean,
): QuoteKind {
  const previous = chars[index - 1] ?? "";
  const next = chars[index + 1];
  if (isWordChar(previous) && isWordChar(next)) return QuoteKind.Apostrophe;
  if (isSpace(previous) || openingPunctuation.has(previous) || previousOpening)
    return leadingElision.test(
      chars.slice(index, index + 1 + lookaheadLength).join(""),
    )
      ? QuoteKind.Apostrophe
      : QuoteKind.Opening;
  return state.singleDepth > 0 ? QuoteKind.Closing : QuoteKind.Apostrophe;
}

// Advances the quote state over one trimmed part. The first character is
// classified by the caller because its context crosses the part boundary.
function scanQuotes(
  core: string,
  leading: QuoteKind | undefined,
  state: JoinState,
) {
  const chars = Array.from(core);
  let opening = false;
  chars.forEach((char, index) => {
    opening = false;
    if (char === '"') {
      state.doubleOpen = !state.doubleOpen;
      opening = state.doubleOpen;
    } else if (char === "\u2018") {
      state.singleDepth += 1;
      opening = true;
    } else if (char === "\u2019" || char === "'") {
      let kind: QuoteKind;
      if (index === 0 && leading !== undefined) kind = leading;
      else if (char === "\u2019")
        kind =
          state.singleDepth > 0 &&
          !(isWordChar(chars[index - 1]) && isWordChar(chars[index + 1]))
            ? QuoteKind.Closing
            : QuoteKind.Apostrophe;
      else
        kind = singleQuoteKind(
          chars,
          index,
          state,
          index > 0 && chars[index - 1] === '"' && state.doubleOpen,
        );
      if (kind === QuoteKind.Opening) {
        state.singleDepth += 1;
        opening = true;
      } else if (kind === QuoteKind.Closing && state.singleDepth > 0) {
        state.singleDepth -= 1;
      }
    }
    if (index === chars.length - 1) state.lastOpening = opening;
  });
}

// Joins ordered parts into one display string. Whitespace at a part boundary
// collapses to a single space; a boundary without whitespace gets one space
// unless it would precede closing punctuation or follow opening punctuation or
// an opening quote. Straight quotes are opening or closing by tracked state: a
// part that starts with a single quote closes an unclosed quote or continues an
// apostrophe contraction ("It" + "'s"), and otherwise opens a new quote. Text
// inside a part, and every non-whitespace character, is unchanged. Leading and
// trailing whitespace of the whole body is dropped.
export function joinReasoningParts(parts: readonly string[]): string {
  let body = "";
  let last = "";
  let boundarySpace = false;
  const state: JoinState = {
    singleDepth: 0,
    doubleOpen: false,
    lastOpening: false,
  };
  for (const part of parts) {
    const core = part.trim();
    if (core.length === 0) {
      boundarySpace = boundarySpace || part.length > 0;
      continue;
    }
    let leading: QuoteKind | undefined;
    const first = firstCodePoint(core);
    if (first === "'") leading = QuoteKind.Opening;
    if (body.length > 0) {
      const spaced = boundarySpace || isSpace(part[0]);
      const prevOpening = openingPunctuation.has(last) || state.lastOpening;
      let separator = " ";
      if (first === "'") {
        if (spaced || prevOpening) {
          leading = leadingElision.test(core)
            ? QuoteKind.Apostrophe
            : QuoteKind.Opening;
        } else if (contractionSuffix.test(core)) {
          separator = "";
          leading =
            state.singleDepth > 0 ? QuoteKind.Closing : QuoteKind.Apostrophe;
        } else if (state.singleDepth > 0 && !startsQuotedWord(core)) {
          // A quote left unclosed earlier must not swallow a later opening quote.
          separator = "";
          leading = QuoteKind.Closing;
        }
        if (prevOpening && !spaced) separator = "";
      } else if (!spaced) {
        const nextClosing =
          first === '"' ? state.doubleOpen : closingPunctuation.has(first);
        if (prevOpening || nextClosing) separator = "";
      }
      body += separator;
    } else if (first === "'" && leadingElision.test(core)) {
      leading = QuoteKind.Apostrophe;
    }
    body += core;
    scanQuotes(core, leading, state);
    last = lastCodePoint(core);
    boundarySpace = isSpace(part.at(-1));
  }
  return body;
}

export function CodexReasoning({ data }: { data: CodexReasoningData }) {
  useLocale();
  const summary =
    data.completed?.summary.find((part) => part.trim().length > 0) ??
    data.started.summary.find((part) => part.trim().length > 0) ??
    copy("native-reasoning.thinking");
  const body = useMemo(
    () => joinReasoningParts(reasoningBodyParts(data)),
    [data],
  );
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
        {body.length > 0 ? (
          <p className="codex-reasoning-text">{body}</p>
        ) : (
          <p>{copy("native-reasoning.noSuppliedContent")}</p>
        )}
      </div>
    </Disclosure>
  );
}
