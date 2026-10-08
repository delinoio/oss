// SPDX-License-Identifier: Apache-2.0
import { createContext, useContext, useEffect, useRef, useState, useSyncExternalStore, type ReactNode } from "react";
import { Code, ConnectError } from "@connectrpc/connect";
import { EntityKind, isEntityId, supportsResourceSchema, type EnqueueInputRequest, type EnqueueInputResponse, type ImageAttachment, type Resource } from "@delinoio/delidev-api-client";
import { document, Mode, text } from "./documents";
import { acknowledgeImages, retainedImages } from "./image-input";
import { RetainedMutationPhase, useRetainedMutationNotifications, useRetainedMutationOutcomes } from "./mutation";

export enum SubmissionPhase {
  Preparing = "preparing", Sending = "sending", Queued = "queued", Claimed = "claimed",
  Accepted = "accepted", Uncertain = "uncertain", Rejected = "rejected", Removed = "removed",
}
export interface SessionSubmission {
  readonly requestId: string; readonly sessionId: string; readonly prompt: string; readonly mode: Mode;
  readonly attachments: readonly ImageAttachment[]; readonly attachmentCount: number;
  readonly phase: SubmissionPhase; readonly queueId?: string; readonly queueRevision: bigint; readonly native: boolean; readonly observationUnavailable?: boolean;
}
interface QueueEvidence { id: string; sessionId: string; revision: bigint; prompt: string; attachments: ImageAttachment[]; phase: SubmissionPhase }
const phases: Record<string, SubmissionPhase> = {
  queued: SubmissionPhase.Queued, claimed: SubmissionPhase.Claimed, accepted: SubmissionPhase.Accepted,
  uncertain: SubmissionPhase.Uncertain, "rejected-before-start": SubmissionPhase.Rejected, removed: SubmissionPhase.Removed,
};
const maximumCount = 1000, maximumBytes = 4 << 20;
const bytes = (prompt: string) => new TextEncoder().encode(prompt).byteLength;
function queueEvidence(resource: Resource): QueueEvidence | undefined {
  if (resource.kind !== EntityKind.QUEUE || !isEntityId(resource.id) || !isEntityId(resource.sessionId) || resource.revision < 1n || !supportsResourceSchema(resource)) return;
  const data = document(resource), attachments = retainedImages(data.attachments);
  if (typeof data.prompt !== "string" || bytes(data.prompt) > 256 << 10 || ![Mode.Execute, Mode.Plan].includes(data.mode as Mode) || !attachments) return;
  return { id: resource.id, sessionId: resource.sessionId, revision: resource.revision, prompt: data.prompt, attachments, phase: phases[text(data.delivery)] ?? SubmissionPhase.Uncertain };
}
export function submissionQueueReadable(resource: Resource): boolean { return Boolean(queueEvidence(resource)); }

/** Only an original user MESSAGE with its exact input identity replaces a projection. */
export function nativeSubmissionInput(resource: Resource): string | undefined {
  if (resource.kind !== EntityKind.MESSAGE || !isEntityId(resource.id) || resource.revision < 1n || !supportsResourceSchema(resource)) return;
  const data = document(resource), inputId = text(data.input_id);
  if (data.role !== "user" || data.state !== "complete" || typeof data.text !== "string" || !isEntityId(inputId) ||
    ["tool", "artifact", "progress", "claude", "claude_tool", "claude_progress", "claude_interruption", "grok_tool", "grok_text"].some(key => data[key] != null)) return;
  return inputId;
}
/** Admission is not runner acceptance. Verify the original mapping for text too. */
export function acknowledgeSessionSubmission(result: EnqueueInputResponse, request: EnqueueInputRequest): boolean {
  return acknowledgeImages(result.change, request.requestId, request.attachments, request.sessionId) && Boolean(result.change?.input && queueEvidence(result.change.input) && Object.hasOwn(phases, text(document(result.change.input).delivery)));
}

/** Bounded presentation memory. No history reads, native authority or image bytes. */
export class SessionSubmissions {
  alive = true;
  private records = new Map<string, SessionSubmission>();
  private queues = new Map<string, QueueEvidence>();
  private nativeInputs = new Map<string, string>();
  private listeners = new Set<() => void>();
  private value: readonly SessionSubmission[] = [];
  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  snapshot = () => this.value;
  private unmatched(sessionId: string) {
    return [...this.records.values()].some(row => row.sessionId === sessionId && !row.queueId &&
      [SubmissionPhase.Preparing, SubmissionPhase.Sending, SubmissionPhase.Uncertain].includes(row.phase));
  }
  private pruneEvidence(sessionId: string) {
    if (this.unmatched(sessionId)) return;
    for (const [id, queue] of this.queues) if (queue.sessionId === sessionId) this.queues.delete(id);
    for (const [id, owner] of this.nativeInputs) if (owner === sessionId) this.nativeInputs.delete(id);
  }
  private publish() { if (!this.alive) return; this.value = [...this.records.values()]; for (const listener of this.listeners) listener(); }
  freeze(requestId: string, sessionId: string, prompt: string, mode: Mode, attachmentCount: number) {
    if (!this.alive) return false;
    const previous = this.records.get(requestId);
    const used = [...this.records.values()].reduce((sum, row) => sum + (row.requestId === requestId ? 0 : bytes(row.prompt)), 0);
    // Settled native projections retain no content; retire their mappings only
    // when the existing retained-input bound needs room for a fresh submission.
    if (!previous && this.records.size >= maximumCount) {
      const settled = [...this.records.values()].find(row => row.native);
      if (settled) this.records.delete(settled.requestId);
    }
    if (bytes(prompt) > 256 << 10 || used + bytes(prompt) > maximumBytes || (!previous && this.records.size >= maximumCount)) throw new ConnectError("Resolve retained inputs before sending more messages.", Code.ResourceExhausted);
    if (previous && previous.phase !== SubmissionPhase.Rejected) return false;
    this.records.set(requestId, { requestId, sessionId, prompt, mode, attachmentCount, attachments: [], phase: attachmentCount ? SubmissionPhase.Preparing : SubmissionPhase.Sending, queueRevision: 0n, native: false });
    this.publish(); return true;
  }
  preparing(sessionId: string) { return this.value.some(row => row.sessionId === sessionId && row.phase === SubmissionPhase.Preparing); }
  prepared(requestId: string, attachments: readonly ImageAttachment[]) {
    const row = this.records.get(requestId);
    if (!this.alive || !row || row.phase !== SubmissionPhase.Preparing) return;
    this.records.set(requestId, { ...row, attachments: attachments.map(image => ({ ...image })), phase: SubmissionPhase.Sending }); this.publish();
  }
  preparationFailed(requestId: string) {
    const row = this.records.get(requestId);
    if (!this.alive || !row || row.phase !== SubmissionPhase.Preparing) return;
    this.records.set(requestId, { ...row, phase: SubmissionPhase.Rejected }); this.publish();
  }
  outcome(key: string, request: object, phase: RetainedMutationPhase) {
    const input = request as EnqueueInputRequest, row = this.records.get(input.requestId);
    if (!this.alive || key !== `enqueue:${input.sessionId}` || !row || row.sessionId !== input.sessionId || row.native || row.queueRevision > 0n) return;
    const next = phase === RetainedMutationPhase.Sending ? SubmissionPhase.Sending : phase === RetainedMutationPhase.Uncertain ? SubmissionPhase.Uncertain : SubmissionPhase.Rejected;
    this.records.set(row.requestId, { ...row, phase: next }); this.pruneEvidence(row.sessionId); this.publish();
  }
  accepted(key: string, request: object, result: unknown) {
    const input = request as EnqueueInputRequest, row = this.records.get(input.requestId), response = result as EnqueueInputResponse;
    if (!this.alive || key !== `enqueue:${input.sessionId}` || !row || row.sessionId !== input.sessionId || !acknowledgeSessionSubmission(response, input)) return;
    const receipt = queueEvidence(response.change!.input!)!;
    if (row.queueId && row.queueId !== receipt.id) return;
    const observed = this.queues.get(receipt.id);
    const latest = observed?.sessionId === row.sessionId && observed.revision > receipt.revision ? observed : receipt;
    let next: SessionSubmission = { ...row, queueId: receipt.id };
    if (latest.revision > row.queueRevision) next = this.withQueue(next, latest);
    if (this.nativeInputs.get(receipt.id) === row.sessionId) next = this.withNative(next);
    this.records.set(row.requestId, next); this.pruneEvidence(row.sessionId); this.publish();
  }
  observationFailed(queueId: string) {
    if (!this.alive) return;
    let changed = false;
    for (const row of this.records.values()) if (!row.native && row.queueId === queueId && !row.observationUnavailable) {
      this.records.set(row.requestId, { ...row, observationUnavailable: true }); changed = true;
    }
    if (changed) this.publish();
  }
  private withQueue(row: SessionSubmission, queue: QueueEvidence): SessionSubmission {
    return row.native ? { ...row, queueRevision: queue.revision } : { ...row, prompt: queue.prompt, attachments: queue.attachments, attachmentCount: queue.attachments.length, phase: queue.phase, queueRevision: queue.revision, observationUnavailable: false };
  }
  private withNative(row: SessionSubmission): SessionSubmission { return { ...row, native: true, prompt: "", attachments: [], attachmentCount: 0 }; }
  observe(sessionId: string, resources: readonly Resource[], reinspection = false) {
    if (!this.alive || !this.value.some(row => row.sessionId === sessionId)) return;
    let changed = false;
    for (const resource of resources) {
      if (resource.sessionId !== sessionId) continue;
      const queue = queueEvidence(resource);
      if (queue) {
        const current = this.queues.get(queue.id);
        if ((!current || queue.revision > current.revision) && this.unmatched(sessionId) && !this.value.some(row => row.queueId === queue.id)) {
          // Bound unmatched evidence exactly like retained queue input. Mapped
          // inputs update their record even when unrelated evidence fills it.
          const used = [...this.queues.values()].reduce((sum, value) => sum + (value.id === queue.id ? 0 : bytes(value.prompt)), 0);
          if ((current || this.queues.size < maximumCount) && used + bytes(queue.prompt) <= maximumBytes) this.queues.set(queue.id, queue);
        }
        for (const row of this.records.values()) if (row.sessionId === sessionId && row.queueId === queue.id && (queue.revision > row.queueRevision || queue.revision === row.queueRevision && row.observationUnavailable && reinspection)) {
          if (queue.revision > row.queueRevision) {
            this.records.set(row.requestId, this.withQueue(row, queue)); changed = true;
          } else if (queue.prompt === row.prompt && queue.phase === row.phase && queue.attachments.length === row.attachments.length && queue.attachments.every((image, index) => {
            const original = row.attachments[index];
            return image.id === original.id && image.machineId === original.machineId && image.sha256 === original.sha256 && image.byteLength === original.byteLength && image.mediaType === original.mediaType;
          })) {
            // A current reread clears failed observation without allowing an
            // unchanged revision to rewrite its already accepted content.
            this.records.set(row.requestId, { ...row, observationUnavailable: false }); changed = true;
          }
        }
      } else if (resource.kind === EntityKind.MESSAGE && resource.revision > 0n && supportsResourceSchema(resource)) {
        const inputId = nativeSubmissionInput(resource);
        if (!inputId) continue;
        if (this.unmatched(sessionId)) {
          if (!this.nativeInputs.has(inputId) && this.nativeInputs.size >= 2000) this.nativeInputs.delete(this.nativeInputs.keys().next().value!);
          this.nativeInputs.set(inputId, sessionId);
        }
        for (const row of this.records.values()) if (!row.native && row.sessionId === sessionId && row.queueId === inputId) {
          this.records.set(row.requestId, this.withNative(row)); changed = true;
        }
      }
    }
    if (changed) this.publish();
  }
  dispose() { this.alive = false; this.records.clear(); this.queues.clear(); this.nativeInputs.clear(); this.value = []; this.listeners.clear(); }
}
const Context = createContext<SessionSubmissions | undefined>(undefined);
function useOutcomes(store: SessionSubmissions) {
  useRetainedMutationNotifications((key, request, result) => store.accepted(key, request, result));
  useRetainedMutationOutcomes((key, request, phase) => store.outcome(key, request, phase));
}
export function SessionSubmissionsProvider({ children }: { children: ReactNode }) {
  const [store] = useState(() => new SessionSubmissions());
  useOutcomes(store);
  const mounted = useRef(true);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; queueMicrotask(() => { if (!mounted.current) store.dispose(); }); }; }, [store]);
  return <Context.Provider value={store}>{children}</Context.Provider>;
}
export function useSessionSubmissions() {
  const context = useContext(Context), [fallback] = useState(() => new SessionSubmissions());
  const store = context ?? fallback;
  // Standalone session fixtures have the same notification order as the App.
  useOutcomes(fallback);
  useEffect(() => () => { if (!context) fallback.dispose(); }, [context, fallback]);
  const rows = useSyncExternalStore(store.subscribe, store.snapshot, store.snapshot);
  return { store, rows };
}
