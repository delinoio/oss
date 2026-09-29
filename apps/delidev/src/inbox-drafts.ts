import { useState, type Dispatch, type SetStateAction } from "react";
import type { Resource } from "@delinoio/delidev-api-client";
import { document, object, text, type Document } from "./documents";

import type { GrokDecision, GrokQuestionOutcome } from "./native-grok-interaction";

export enum InteractionDraftKind {
  Grok = "grok",
  CodexQuestion = "codex-question",
  CodexApproval = "codex-approval",
  OpenCodeQuestion = "opencode-question",
  OpenCodePermission = "opencode-permission",
  Claude = "claude",
}

export enum DraftGrantScope { Turn = "turn", Session = "session" }
export enum DraftAccess { Omit = "omit", Read = "read", Write = "write", Deny = "deny" }

export type InteractionDraftState =
  | { kind: InteractionDraftKind.Grok; answers: Record<string, string>; outcome: GrokQuestionOutcome; decision: GrokDecision | "" }
  | { kind: InteractionDraftKind.CodexQuestion; selected: Record<string, string[]>; free: Record<string, string>; unanswered: Record<string, boolean> }
  | { kind: InteractionDraftKind.CodexApproval; choice: number; access: Record<number, DraftAccess>; network: boolean; scope: DraftGrantScope; strict: boolean }
  | { kind: InteractionDraftKind.OpenCodeQuestion; selected: string[][]; custom: Record<number, string>; customEnabled: Record<number, boolean>; unanswered: Record<number, boolean> }
  | { kind: InteractionDraftKind.OpenCodePermission; feedbackEnabled: boolean; feedback: string }
  | { kind: InteractionDraftKind.Claude; selected: string[][]; custom: Record<number, string>; customEnabled: Record<number, boolean>; skipped: Record<number, boolean>; denial: boolean; reason: string; interrupt: boolean };

export interface InboxInteractionDraft {
  version: 1;
  interactionId: string;
  interactionRevision: string;
  requestIdentity: string;
  editable: InteractionDraftState;
}

export function interactionRequestIdentity(resource: Resource): string {
  const data = document(resource);
  const nativeRequest = object(data.native_request_id);
  const claude = object(data.claude), opencode = object(data.opencode), grok = object(data.grok);
  return [resource.id, text(nativeRequest.text), text(data.native_item_id), text(data.native_turn_id), text(claude.arrival_id), text(opencode.native_event_id), text(grok.request_digest), text(grok.proposal_digest), text(object(grok.plan).content_digest)].filter(Boolean).join("|");
}

export function initialInteractionDraft(resource: Resource): InboxInteractionDraft | undefined {
  const data = document(resource);
  let editable: InteractionDraftState | undefined;
  if (data.grok != null) {
    editable = { kind: InteractionDraftKind.Grok, answers: {}, outcome: "accepted" as GrokQuestionOutcome, decision: "" };
  } else if (data.claude != null) {
    editable = { kind: InteractionDraftKind.Claude, selected: [], custom: {}, customEnabled: {}, skipped: {}, denial: false, reason: "", interrupt: false };
  } else if (data.opencode != null) {
    editable = data.type === "user-question"
      ? { kind: InteractionDraftKind.OpenCodeQuestion, selected: [], custom: {}, customEnabled: {}, unanswered: {} }
      : { kind: InteractionDraftKind.OpenCodePermission, feedbackEnabled: false, feedback: "" };
  } else if (data.type === "user-question") {
    editable = { kind: InteractionDraftKind.CodexQuestion, selected: {}, free: {}, unanswered: {} };
  } else if (data.type === "native-approval") {
    editable = { kind: InteractionDraftKind.CodexApproval, choice: 0, access: {}, network: false, scope: DraftGrantScope.Turn, strict: false };
  }
  return editable ? { version: 1, interactionId: resource.id, interactionRevision: resource.revision.toString(), requestIdentity: interactionRequestIdentity(resource), editable } : undefined;
}

export function isEmptyInteractionDraft(state: InteractionDraftState): boolean {
  switch (state.kind) {
    case InteractionDraftKind.Grok:
      return !Object.values(state.answers).some(Boolean) && state.decision === "" && state.outcome === "accepted";
    case InteractionDraftKind.CodexQuestion:
      return Object.values(state.selected).every((row) => row.length === 0) && Object.values(state.free).every((value) => value === "") && !Object.values(state.unanswered).some(Boolean);
    case InteractionDraftKind.CodexApproval:
      return state.choice === 0 && !Object.values(state.access).some((value) => value !== DraftAccess.Omit) && !state.network && state.scope === DraftGrantScope.Turn && !state.strict;
    case InteractionDraftKind.OpenCodeQuestion:
      return state.selected.every((row) => row.length === 0) && Object.values(state.custom).every((value) => value === "") && !Object.values(state.customEnabled).some(Boolean) && !Object.values(state.unanswered).some(Boolean);
    case InteractionDraftKind.OpenCodePermission:
      return !state.feedbackEnabled && state.feedback === "";
    case InteractionDraftKind.Claude:
      return state.selected.every((row) => row.length === 0) && Object.values(state.custom).every((value) => value === "") && !Object.values(state.customEnabled).some(Boolean) && !Object.values(state.skipped).some(Boolean) && !state.denial && state.reason === "" && !state.interrupt;
  }
}

export function useEditableInteractionDraft<T extends InteractionDraftState>(kind: T["kind"], initial: () => T, stored?: InteractionDraftState, save?: (value: T) => void): [T, Dispatch<SetStateAction<T>>] {
  const [local, setLocal] = useState<T>(initial);
  const value = stored?.kind === kind ? stored as T : local;
  const setValue: Dispatch<SetStateAction<T>> = (next) => {
    const updated = typeof next === "function" ? (next as (previous: T) => T)(value) : next;
    if (save) save(updated); else setLocal(updated);
  };
  return [value, setValue];
}

export function draftByteLength(draft: InboxInteractionDraft): number {
  return new TextEncoder().encode(JSON.stringify(draft)).byteLength;
}

export type { Document };
