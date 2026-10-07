import { useState, type Dispatch, type SetStateAction } from "react";
import type { Resource } from "@delinoio/delidev-api-client";
import { document, object, text, type Document } from "./documents";
import type { OwnedMessage } from "./localization";

export enum InteractionDraftKind {
  CodexQuestion = "codex-question",
  CodexApproval = "codex-approval",
  OpenCodeQuestion = "opencode-question",
  OpenCodePermission = "opencode-permission",
  Claude = "claude",
  Grok = "grok",
}

export enum DraftGrantScope { Turn = "turn", Session = "session" }
export enum DraftAccess { Omit = "omit", Read = "read", Write = "write", Deny = "deny" }
export enum GrokDraftOutcome { Accepted = "accepted", Cancelled = "cancelled", Skip = "skip_interview" }
export enum GrokDraftFileDecision { Once = "allow-once", Session = "allow-edits-session", Reject = "reject-once" }
export enum GrokDraftPlanDecision { Approve = "approved", Cancel = "cancelled", Abandon = "abandoned" }
export type GrokDraftDecision = "" | GrokDraftFileDecision | GrokDraftPlanDecision;

export type InteractionDraftState =
  | { kind: InteractionDraftKind.Grok; outcome: GrokDraftOutcome; decision: GrokDraftDecision; answers: Record<number, string>; notes: Record<number, string>; partial: Record<number, boolean> }
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
  const claude = object(data.claude), opencode = object(data.opencode);
  return [resource.id, text(nativeRequest.text), text(data.native_item_id), text(data.native_turn_id), text(claude.arrival_id), text(opencode.native_event_id),text(object(object(data.grok).event).arrival_id),text(object(data.grok).proposal_digest)].filter(Boolean).join("|");
}

export function initialInteractionDraft(resource: Resource): InboxInteractionDraft | undefined {
  const data = document(resource);
  let editable: InteractionDraftState | undefined;
  if (data.grok != null) {
    editable = { kind: InteractionDraftKind.Grok, outcome: GrokDraftOutcome.Accepted, decision: "", answers: {}, notes: {}, partial: {} };
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
    case InteractionDraftKind.CodexQuestion:
      return Object.values(state.selected).every((row) => row.length === 0) && Object.values(state.free).every((value) => value === "") && !Object.values(state.unanswered).some(Boolean);
    case InteractionDraftKind.CodexApproval:
      return state.choice === 0 && !Object.values(state.access).some((value) => value !== DraftAccess.Omit) && !state.network && state.scope === DraftGrantScope.Turn && !state.strict;
    case InteractionDraftKind.OpenCodeQuestion:
      return state.selected.every((row) => row.length === 0) && Object.values(state.custom).every((value) => value === "") && !Object.values(state.customEnabled).some(Boolean) && !Object.values(state.unanswered).some(Boolean);
    case InteractionDraftKind.OpenCodePermission:
      return !state.feedbackEnabled && state.feedback === "";
    case InteractionDraftKind.Grok:
      return state.outcome === GrokDraftOutcome.Accepted && state.decision === "" && Object.values(state.answers).every((value) => value === "") && Object.values(state.notes).every((value) => value === "") && !Object.values(state.partial).some(Boolean);
    case InteractionDraftKind.Claude:
      return state.selected.every((row) => row.length === 0) && Object.values(state.custom).every((value) => value === "") && !Object.values(state.customEnabled).some(Boolean) && !Object.values(state.skipped).some(Boolean) && !state.denial && state.reason === "" && !state.interrupt;
  }
}

export function useEditableInteractionDraft<T extends InteractionDraftState>(kind: T["kind"], initial: () => T, stored?: InteractionDraftState, save?: (value: T) => void, overflow?: (value: T) => OwnedMessage | undefined): [T, Dispatch<SetStateAction<T>>, OwnedMessage | undefined] {
  const [local, setLocal] = useState<T>(initial);
  const [problem, setProblem] = useState<OwnedMessage>();
  const value = stored?.kind === kind ? stored as T : local;
  const setValue: Dispatch<SetStateAction<T>> = (next) => {
    const updated = typeof next === "function" ? (next as (previous: T) => T)(value) : next;
    const error = overflow?.(updated);
    setProblem(error);
    if (error) return;
    if (save) save(updated); else setLocal(updated);
  };
  return [value, setValue, problem];
}

export function draftByteLength(draft: InboxInteractionDraft): number {
  return new TextEncoder().encode(JSON.stringify(draft)).byteLength;
}

export type { Document };
