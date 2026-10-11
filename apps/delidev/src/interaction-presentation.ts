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
    const next = interactionProjection(row);
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
