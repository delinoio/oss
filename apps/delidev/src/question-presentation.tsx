// SPDX-License-Identifier: Apache-2.0
import { createContext, useContext, useLayoutEffect, useState, useSyncExternalStore, type ReactNode } from "react";
import { create } from "@bufbuild/protobuf";
import { EntityKind, ResourceSchema, type Resource } from "@delinoio/delidev-api-client";
import { document, encode, object, text, type Document } from "./documents";
import { useRetainedMutationNotifications } from "./mutation";

export enum QuestionPresentation { Form = "form", Recovery = "recovery", Hidden = "hidden" }
export function questionMutationKey(resource: Resource): string {
  const data = document(resource);
  return `${data.grok != null ? "grok-answer" : data.claude != null ? "claude-answer" : data.opencode != null ? "opencode-answer" : "answer"}:${resource.id}`;
}
export function isQuestion(resource: Resource): boolean { return text(document(resource).type) === "user-question"; }
export function questionPresentation(resource: Resource, retained = false): QuestionPresentation {
  const data = document(resource), closure = text(data.closure), response = object(data.response);
  const knownClosure = ["open", "native-closed", "turn-ended"].includes(closure);
  if (!knownClosure || data.approval_response != null || data.presentation_conflict === true || data.presentation_recovery === true) return QuestionPresentation.Recovery;
  if (!Object.hasOwn(data, "response")) return closure === "open" ? QuestionPresentation.Form : retained ? QuestionPresentation.Recovery : QuestionPresentation.Hidden;
  if (retained) return QuestionPresentation.Recovery;
  if (response.state === "accepted" || response.state === "canceled" && closure !== "open") return QuestionPresentation.Hidden;
  return QuestionPresentation.Recovery;
}
function conflicted(resource: Resource): Resource {
  return create(ResourceSchema, { ...resource, documentJson: encode({ ...document(resource), presentation_conflict: true }) });
}
interface QuestionObservation {
  id: string; sessionId: string; revision: bigint; closure: string; hasResponse: boolean;
  responseState: string; hasApproval: boolean; source: string; conflict: boolean;
}
function observation(resource: Resource): QuestionObservation {
  const data = document(resource);
  return { id: resource.id, sessionId: resource.sessionId, revision: resource.revision, closure: text(data.closure), hasResponse: Object.hasOwn(data, "response"), responseState: text(object(data.response).state), hasApproval: data.approval_response != null, source: questionMutationKey(resource).split(":")[0]!, conflict: data.presentation_conflict === true };
}
function applyObservation(resource: Resource, value: QuestionObservation): Resource {
  const data: Document = { ...document(resource), closure: value.closure, presentation_conflict: value.conflict };
  if (value.hasResponse) Object.assign(data, { response: { ...object(data.response), state: value.responseState } });
  else delete data.response;
  if (value.hasApproval) Object.assign(data, { approval_response: {} });
  return create(ResourceSchema, { ...resource, revision: value.revision, documentJson: encode(data) });
}
/** Connection-local bounded lifecycle metadata contains no original question or
 * answer bodies. It never changes source history, pagination or arrival order. */
export class QuestionPresentationStore {
  private rows = new Map<string, QuestionObservation>();
  private overflow = false;
  private revision = 0;
  private listeners = new Set<() => void>();
  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  snapshot = () => this.revision;
  latest(resource: Resource): Resource {
    if (!isQuestion(resource)) return resource;
    if (this.overflow) return conflicted(resource);
    const previous = this.rows.get(resource.id);
    if (!previous || previous.sessionId !== resource.sessionId) return resource;
    const current = observation(resource);
    if (previous.revision < resource.revision) return regresses(previous, current) ? conflicted(resource) : resource;
    if (previous.revision === resource.revision && !same(previous, current)) return conflicted(resource);
    return previous.revision > resource.revision || previous.conflict ? applyObservation(resource, previous) : resource;
  }
  retained(sessionId: string, ids: ReadonlySet<string>): Resource[] {
    return [...this.rows.values()].filter(row => row.sessionId === sessionId && ids.has(row.id)).map(row => {
      const source = row.source === "claude-answer" ? { claude: {} } : row.source === "grok-answer" ? { grok: {} } : row.source === "opencode-answer" ? { opencode: {} } : {};
      return applyObservation(create(ResourceSchema, { id: row.id, sessionId, kind: EntityKind.INTERACTION, schemaVersion: 1, documentJson: encode({ type: "user-question", presentation_recovery: true, ...source }) }), row);
    });
  }
  observe(resource: Resource) {
    if (resource.kind !== EntityKind.INTERACTION || !resource.sessionId || !isQuestion(resource) || this.overflow) return;
    const previous = this.rows.get(resource.id), current = observation(resource);
    if (previous && (previous.sessionId !== resource.sessionId || previous.revision > resource.revision)) return;
    if (previous && previous.revision === resource.revision && (previous.conflict || same(previous, current))) return;
    const regression = previous && regresses(previous, current);
    const next = previous?.revision === resource.revision || regression ? { ...current, conflict: true } : current;
    if (!previous && this.rows.size >= 4096) { this.overflow = true; }
    else this.rows.set(resource.id, next);
    this.revision++; for (const listener of this.listeners) listener();
  }
}
function regresses(previous: QuestionObservation, current: QuestionObservation): boolean {
  return previous.hasResponse && !current.hasResponse || previous.closure !== "open" && current.closure === "open" || previous.source !== current.source;
}
function same(a: QuestionObservation, b: QuestionObservation): boolean {
  return a.closure === b.closure && a.hasResponse === b.hasResponse && a.responseState === b.responseState && a.hasApproval === b.hasApproval && a.source === b.source && a.conflict === b.conflict;
}
const Context = createContext<QuestionPresentationStore | undefined>(undefined);
export function QuestionPresentationProvider({ children }: { children: ReactNode }) {
  const [store] = useState(() => new QuestionPresentationStore());
  useRetainedMutationNotifications((key, request, result) => {
    const resource = (result as { interaction?: Resource }).interaction;
    const mutation = (request as { mutation?: { id?: string; expectedRevision?: bigint } }).mutation;
    if (resource && key === questionMutationKey(resource) && mutation?.id === resource.id && resource.revision > (mutation.expectedRevision ?? 0n)) store.observe(resource);
  });
  return <Context.Provider value={store}>{children}</Context.Provider>;
}
export function useQuestionPresentationStore() {
  const context = useContext(Context), [fallback] = useState(() => new QuestionPresentationStore());
  const store = context ?? fallback;
  useSyncExternalStore(store.subscribe, store.snapshot, store.snapshot);
  return store;
}
export function useObservedQuestions(store: QuestionPresentationStore, rows: readonly Resource[]) {
  useLayoutEffect(() => { for (const row of rows) store.observe(row); }, [store, rows]);
}
export interface QuestionTrayCoverage { loaded: boolean; loading?: unknown; error?: unknown; nextPageToken: string; pages: readonly { token: string }[]; payloadPages: readonly { token: string }[] }
export function questionTrayEmpty(rows: readonly Resource[], pending: boolean, query: QuestionTrayCoverage): boolean {
  return query.loaded && !query.loading && !query.error && !query.nextPageToken && !pending && rows.length === 0 && query.pages.every(page => query.payloadPages.some(payload => payload.token === page.token));
}
