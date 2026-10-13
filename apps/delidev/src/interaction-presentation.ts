// SPDX-License-Identifier: Apache-2.0
import type { Resource } from "@delinoio/delidev-api-client";
import { document, items, object, text, type Document } from "./documents";
import { validOpenCodeInteraction } from "./native-interaction";
import { validClaudeInteraction } from "./native-claude-interaction";
import { validGrokInteraction } from "./native-grok-interactions";

export enum InteractionPresentation {
  Form = "form",
  Pending = "pending",
  Retired = "retired",
}
export interface InteractionProjection {
  revision: bigint;
  presentation: InteractionPresentation;
  question: boolean;
  responseState: string;
  closure: string;
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
    const acceptance = object(response.acceptance),
      delivery = object(response.delivery);
    return (
      data.closure === "native-closed" &&
      response.claim != null &&
      ["transmitted", "uncertain"].includes(text(delivery.state)) &&
      acceptance.evidence === "native-question-output" &&
      Number.isSafeInteger(acceptance.sequence) &&
      Number(acceptance.sequence) > Number(delivery.sequence)
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
    responseState,
    closure,
    mutationKey: questionMutationKey(row.id, data),
    unavailable,
  };
}
/** Retain metadata only: evicted original documents remain disposable. */
export class InteractionPresentationStore {
  private projections = new Map<string, InteractionProjection>();
  entries() {
    return this.projections.entries();
  }
  observe(row: Resource): InteractionProjection {
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
