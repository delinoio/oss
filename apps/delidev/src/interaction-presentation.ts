// SPDX-License-Identifier: Apache-2.0
import type { Resource } from "@delinoio/delidev-api-client";
import { document, items, object, text, type Document } from "./documents";
import { validGenericQuestionResponse } from "./generic-question-response";
import { rfc3339Instant } from "./rfc3339";
import { validOpenCodeInteraction } from "./native-interaction";
import { validClaudeInteraction } from "./native-claude-interaction";
import { validGrokInteraction } from "./native-grok-interactions";

export enum InteractionPresentation {
  Form = "form",
  Pending = "pending",
  Retired = "retired",
}
/**
 * Bounded closure values retained by a projection. Any unrecognized original
 * string collapses to `Unknown` so no untrusted text outlives its payload page.
 */
export enum InteractionClosure {
  Open = "open",
  NativeClosed = "native-closed",
  TurnEnded = "turn-ended",
  Unknown = "unknown",
}
/**
 * Bounded question response states retained by a projection. `None` marks an
 * absent (or empty) response; unrecognized strings collapse to `Unknown`.
 */
export enum QuestionResponseState {
  Queued = "queued",
  Claimed = "claimed",
  Transmitted = "transmitted",
  Uncertain = "uncertain",
  Canceled = "canceled",
  Accepted = "accepted",
  None = "none",
  Unknown = "unknown",
}
const closureValues: ReadonlySet<string> = new Set([
  InteractionClosure.Open,
  InteractionClosure.NativeClosed,
  InteractionClosure.TurnEnded,
]);
const responseStateValues: ReadonlySet<string> = new Set([
  QuestionResponseState.Queued,
  QuestionResponseState.Claimed,
  QuestionResponseState.Transmitted,
  QuestionResponseState.Uncertain,
  QuestionResponseState.Canceled,
  QuestionResponseState.Accepted,
]);
export function normalizeClosure(value: string): InteractionClosure {
  return closureValues.has(value)
    ? (value as InteractionClosure)
    : InteractionClosure.Unknown;
}
export function normalizeResponseState(value: string): QuestionResponseState {
  if (value === "") return QuestionResponseState.None;
  return responseStateValues.has(value)
    ? (value as QuestionResponseState)
    : QuestionResponseState.Unknown;
}
/** Display text for a retained response state; an absent response is empty. */
export function responseStateText(state: QuestionResponseState): string {
  return state === QuestionResponseState.None ? "" : state;
}
export interface InteractionProjection {
  revision: bigint;
  presentation: InteractionPresentation;
  question: boolean;
  responseState: QuestionResponseState;
  closure: InteractionClosure;
  mutationKey: string;
  unavailable: boolean;
}
export function questionMutationKey(id: string, data: Document): string {
  const family = Object.hasOwn(data, "grok")
    ? "grok-answer"
    : data.claude != null
      ? "claude-answer"
      : data.opencode != null
        ? "opencode-answer"
        : "answer";
  return `${family}:${id}`;
}
const UUID_V7 =
  /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const validId = (value: unknown): boolean =>
  typeof value === "string" && UUID_V7.test(value);
const validTimestamp = (value: unknown): boolean =>
  typeof value === "string" && rfc3339Instant(value) !== undefined;
// Go encodes uint64 sequences as JSON numbers. A value outside the safe
// integer range cannot be compared exactly, so it is an invalid envelope.
const validSequence = (value: unknown): value is number =>
  typeof value === "number" && Number.isSafeInteger(value) && value >= 0;
const hasOnlyKeys = (value: Document, keys: string[]): boolean =>
  Object.keys(value).every((key) => keys.includes(key));
function genericAnswers(input: unknown): unknown {
  const wrapper = object(input);
  return hasOnlyKeys(wrapper, ["answers"]) ? wrapper.answers : undefined;
}
/**
 * Validates the complete accepted QuestionResponse envelope that Go persists:
 * response id/input/accepted_at, the full claim identity, a delivery
 * observation and an acceptance observation whose sequence strictly follows
 * delivery. An incomplete envelope must never retire a question.
 */
function validAcceptedEnvelope(
  response: Document,
  expectedEvidence: string,
  request: unknown,
): boolean {
  const claim = object(response.claim),
    delivery = object(response.delivery),
    acceptance = object(response.acceptance);
  return (
    response.state === "accepted" &&
    validId(response.id) &&
    validTimestamp(response.accepted_at) &&
    validGenericQuestionResponse(request, genericAnswers(response.input)) &&
    response.claim != null &&
    [
      claim.id,
      claim.job_id,
      claim.machine_id,
      claim.instance_id,
      claim.device_id,
    ].every(validId) &&
    validTimestamp(claim.claimed_at) &&
    response.delivery != null &&
    hasOnlyKeys(delivery, ["state", "sequence"]) &&
    ["transmitted", "uncertain"].includes(text(delivery.state)) &&
    validSequence(delivery.sequence) &&
    response.acceptance != null &&
    hasOnlyKeys(acceptance, ["evidence", "sequence"]) &&
    acceptance.evidence === expectedEvidence &&
    validSequence(acceptance.sequence) &&
    acceptance.sequence > delivery.sequence
  );
}
function validQuestion(data: Document): boolean {
  if (Object.hasOwn(data, "grok")) return validGrokInteraction(data);
  if (data.claude != null) return validClaudeInteraction(data);
  if (data.opencode != null) {
    const response = object(data.response),
      acceptance = object(response.acceptance);
    const delivery = object(response.delivery);
    return (
      validOpenCodeInteraction(data) &&
      (response.state !== "accepted" ||
        (data.closure === "native-closed" &&
          response.claim != null &&
          ["transmitted", "uncertain"].includes(text(delivery.state)) &&
          acceptance.evidence ===
            (object(object(response.input).opencode).reject === true
              ? "native-opencode-question-rejected"
              : "native-opencode-question-reply") &&
          Number.isSafeInteger(acceptance.sequence) &&
          Number(acceptance.sequence) > Number(delivery.sequence)))
    );
  }
  if (
    data.approval != null ||
    data.approval_response != null ||
    !items(object(data.questions).questions).length
  )
    return false;
  const response = object(data.response);
  if (response.state === "accepted") {
    return (
      data.closure === "native-closed" &&
      validAcceptedEnvelope(response, "native-question-output", data.questions)
    );
  }
  return response.acceptance == null;
}
/** This projection grants no submission or native-delivery authority. */
export function interactionProjection(row: Resource): InteractionProjection {
  const data = document(row),
    question = data.type === "user-question";
  const response = object(data.response),
    closure = text(data.closure);
  const responseState = text(response.state);
  let presentation = InteractionPresentation.Form;
  let unavailable = false;
  if (question) {
    const valid =
      validQuestion(data) &&
      ["open", "native-closed", "turn-ended"].includes(closure);
    unavailable = !valid;
    const closed = closure === "native-closed" || closure === "turn-ended";
    const canceled =
      responseState === "canceled" &&
      response.claim == null &&
      response.delivery == null &&
      response.acceptance == null;
    if (
      valid &&
      closed &&
      (data.response == null || responseState === "accepted" || canceled)
    )
      presentation = InteractionPresentation.Retired;
    else if (!valid || data.response != null || closed)
      presentation = InteractionPresentation.Pending;
  }
  return {
    revision: row.revision,
    question,
    presentation,
    // Validity and presentation above use the raw values; only bounded enum
    // values are retained for the session lifetime.
    responseState: normalizeResponseState(responseState),
    closure: normalizeClosure(closure),
    mutationKey: questionMutationKey(row.id, data),
    unavailable,
  };
}
/** Bound on authoritative removal identities retained per session; matches
 * the stream's own removed-set bound. */
export const REMOVED_INTERACTION_LIMIT = 2000;
/** Retain metadata only: evicted original documents remain disposable. */
export class InteractionPresentationStore {
  private projections = new Map<string, InteractionProjection>();
  // Authoritative stream removals survive Snapshot, which clears the stream's
  // own snapshot-scoped removed set. Identity only: no document or metadata.
  private removedIds = new Set<string>();
  entries() {
    return this.projections.entries();
  }
  /**
   * Records an authoritative removal. The retained projection is dropped and
   * the identity is fenced so snapshots, restored older pages and late
   * observations cannot revive its compact receipt. Insertion order bounds the
   * fence: the oldest identity is forgotten beyond REMOVED_INTERACTION_LIMIT.
   * This sends nothing, cancels no native work and leaves retained mutation
   * intents untouched.
   */
  remove(id: string): void {
    this.projections.delete(id);
    this.removedIds.delete(id);
    this.removedIds.add(id);
    while (this.removedIds.size > REMOVED_INTERACTION_LIMIT) {
      const oldest = this.removedIds.values().next();
      if (oldest.done) break;
      this.removedIds.delete(oldest.value);
    }
  }
  isRemoved(id: string): boolean {
    return this.removedIds.has(id);
  }
  /** Snapshot of the fenced identities for request filtering. */
  removed(): ReadonlySet<string> {
    return new Set(this.removedIds);
  }
  observe(row: Resource): InteractionProjection {
    // A removed identity is never retained again; callers still receive the
    // projection of the observed row for their immediate presentation.
    if (this.removedIds.has(row.id)) return interactionProjection(row);
    const prior = this.projections.get(row.id);
    if (prior && prior.revision >= row.revision) return prior;
    const observed = interactionProjection(row);
    // A newer document that drops or changes the established question identity
    // is a contradictory observation: keep the original question flag and
    // answer mutation key so retained receipt recovery stays reachable, and
    // surface it as unavailable without any response authority.
    const next =
      prior?.question &&
      (!observed.question || observed.mutationKey !== prior.mutationKey)
        ? {
            ...observed,
            question: true,
            mutationKey: prior.mutationKey,
            presentation: InteractionPresentation.Pending,
            unavailable: true,
          }
        : observed;
    this.projections.set(row.id, next);
    return next;
  }
}
export function displayedInteraction(
  projection: InteractionProjection,
  pending: ReadonlySet<string>,
): boolean {
  return (
    projection.presentation !== InteractionPresentation.Retired ||
    pending.has(projection.mutationKey)
  );
}
/**
 * Compact receipts for questions that are known by metadata but absent from the
 * resident payload rows. Retention follows the projection's unresolved state,
 * not RPC completion: only retired projections without a retained response
 * intent, and rows tombstoned by the authority, are dropped.
 */
export function missingQuestionReceipts(
  entries: Iterable<[string, InteractionProjection]>,
  presentIds: ReadonlySet<string>,
  removed: ReadonlySet<string>,
  pending: ReadonlySet<string>,
): [string, InteractionProjection][] {
  return [...entries].filter(
    ([requestId, projection]) =>
      projection.question &&
      !presentIds.has(requestId) &&
      !removed.has(requestId) &&
      displayedInteraction(projection, pending),
  );
}
export enum PageEntryKind {
  Resident = "resident",
  Receipt = "receipt",
}
export type PageEntry<Resident, Receipt> =
  | { kind: PageEntryKind.Resident; id: string; value: Resident }
  | { kind: PageEntryKind.Receipt; id: string; value: Receipt };
/**
 * Orders one reached page's retained row identities, resolving each either to
 * its resident payload row or to a compact receipt for an evicted/missing
 * question. The authoritative page row order wins over receipt observation
 * order, each identity renders at most once, and identities without a resident
 * row or receipt render nothing (removed or retired rows).
 */
export function orderedPageEntries<Resident extends { id: string }, Receipt>(
  rowIds: readonly string[],
  resident: readonly Resident[],
  receipts: ReadonlyMap<string, Receipt>,
): PageEntry<Resident, Receipt>[] {
  const rows = new Map<string, Resident>();
  for (const row of resident) if (!rows.has(row.id)) rows.set(row.id, row);
  const seen = new Set<string>();
  const entries: PageEntry<Resident, Receipt>[] = [];
  for (const id of rowIds) {
    if (seen.has(id)) continue;
    seen.add(id);
    const row = rows.get(id);
    if (row) {
      entries.push({ kind: PageEntryKind.Resident, id, value: row });
      continue;
    }
    if (receipts.has(id))
      entries.push({
        kind: PageEntryKind.Receipt,
        id,
        value: receipts.get(id) as Receipt,
      });
  }
  return entries;
}
/** Receipts for questions that no reached page owns (for example new live
 * arrivals); they trail the final page in observation order. */
export function unpagedQuestionReceipts<Receipt extends [string, unknown]>(
  receipts: readonly Receipt[],
  pagedIds: ReadonlySet<string>,
): Receipt[] {
  return receipts.filter(([requestId]) => !pagedIds.has(requestId));
}
export function completeEmptyInteractionTray({
  loaded,
  loading,
  failed,
  continuation,
  resident,
  displayed,
  pending,
}: {
  loaded: boolean;
  loading: boolean;
  failed: boolean;
  continuation: boolean;
  resident: boolean;
  displayed: number;
  pending: number;
}): boolean {
  return (
    loaded &&
    !loading &&
    !failed &&
    !continuation &&
    resident &&
    displayed === 0 &&
    pending === 0
  );
}
