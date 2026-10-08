// SPDX-License-Identifier: Apache-2.0
import { createContext, useContext, useEffect, useMemo, useRef, useState, useSyncExternalStore, type ReactNode } from "react";
import { createClient, type Client } from "@connectrpc/connect";
import { useQuery, useTransport } from "@connectrpc/connect-query";
import { AttachmentService, AttachmentState, EntityKind, ResourceQuery, SystemCapability, SystemQuery, WorkerCapability, clientFailure, newRequestId, supportsResourceSchema, type BeginUploadRequest, type ImageAttachment, type Resource } from "@delinoio/delidev-api-client";
import { document, items, text } from "./documents";
import { decodeImage, imageDigest, imageLimits, imageMime, imageTransferError, ImageInputError, ImageProblem, inspectImage, verifyImageReference } from "./image-input";
import { useRetainedMutationNotifications } from "./mutation";

export interface DraftImage { readonly key: string; readonly bytes: Uint8Array; readonly media: ImageAttachment["mediaType"]; readonly digest: string; readonly preview: string; begin?: Omit<BeginUploadRequest, "$typeName">; reference?: ImageAttachment; ready?: boolean }
interface Snapshot { readonly images: readonly DraftImage[]; readonly busy: boolean; readonly error?: ImageProblem; readonly cleanupPending: number }
const empty: Snapshot = Object.freeze({ images: [], busy: false, cleanupPending: 0 });
export class ImageDraft {
  draftId = newRequestId();
  operationId?: string;
  private state: Snapshot = empty;
  private alive = true;
  private removals = new Map<string, { requestId: string; reference: ImageAttachment }>();
  private listeners = new Set<() => void>();
  constructor(private client: () => Client<typeof AttachmentService>, readonly scope: string, private decode = decodeImage) {}
  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  snapshot = () => this.state;
  private publish(value: Partial<Snapshot>) { if (!this.alive) return; this.state = { ...this.state, ...value, cleanupPending: this.removals.size }; for (const listener of this.listeners) listener(); }
  private failure(error: unknown, phase: string) { console.warn("delidev.image_input.operation_failed", { phase, classification: clientFailure(error).code }); this.publish({ error: imageTransferError(error) }); }
  async add(files: readonly File[]) {
    if (!this.alive || this.state.busy || !files.length) return;
    const initial = this.state.images;
    if (initial.length + files.length > imageLimits.count || files.some(file => !file.size || file.size > imageLimits.individual) || [...initial.map(image => image.bytes.length), ...files.map(file => file.size)].reduce((a, b) => a + b, 0) > imageLimits.total) { this.publish({ error: ImageProblem.Limits }); return; }
    this.publish({ busy: true, error: undefined });
    const added: DraftImage[] = [];
    try {
      for (const file of files) {
        const raw = typeof file.arrayBuffer === "function" ? await file.arrayBuffer() : await new Promise<ArrayBuffer>((resolve, reject) => { const reader = new FileReader(); reader.onload = () => resolve(reader.result as ArrayBuffer); reader.onerror = reject; reader.readAsArrayBuffer(file); });
        const bytes = new Uint8Array(raw), media = inspectImage(bytes, file.type);
        await this.decode(bytes, media);
        const digest = await imageDigest(bytes);
        if (!this.alive) return;
        added.push({ key: newRequestId(), bytes, media, digest, preview: URL.createObjectURL(new Blob([new Uint8Array(bytes)], { type: imageMime[media] })) });
      }
      this.publish({ images: [...initial, ...added] });
    } catch (error) { for (const image of added) URL.revokeObjectURL(image.preview); this.failure(error, "validate"); }
    finally { if (!this.alive) for (const image of added) URL.revokeObjectURL(image.preview); this.publish({ busy: false }); }
  }
  private retire(image: DraftImage) {
    URL.revokeObjectURL(image.preview);
    if (image.reference) this.removals.set(image.reference.id, { requestId: newRequestId(), reference: image.reference });
  }
  async remove(key: string) {
    if (this.state.busy) return;
    const image = this.state.images.find(value => value.key === key);
    if (!image) return;
    // A lost Begin reply must be reconciled with its original request before
    // deletion. Removing a thumbnail cannot abandon owned remote staging.
    if (image.begin && !image.reference) {
      this.publish({ busy: true });
      try { const reply = await this.client().beginUpload(image.begin); verifyImageReference(reply.upload?.attachment, { machineId: image.begin.machineId, mediaType: image.media, byteLength: BigInt(image.bytes.length), sha256: image.digest }); image.reference = reply.upload!.attachment; }
      catch (error) { this.failure(error, "remove-reconcile"); this.publish({ busy: false }); return; }
      this.publish({ busy: false });
    }
    this.retire(image); this.publish({ images: this.state.images.filter(value => value !== image), error: undefined });
    await this.retryCleanup();
  }
  async retryCleanup() {
    for (const [id, entry] of this.removals) {
      if (!this.alive) return;
      try { const reply = await this.client().deleteDraftAttachment({ requestId: entry.requestId, attachmentId: id }); if (reply.upload?.attachment?.id !== id || ![AttachmentState.DELETING, AttachmentState.DELETED].includes(reply.upload.state)) throw new ImageInputError(ImageProblem.Cleanup); const observed = reply.upload.state === AttachmentState.DELETED ? reply.upload : (await this.client().getUpload({ attachmentId: id })).upload; verifyImageReference(observed?.attachment, entry.reference, id); if (observed?.state === AttachmentState.DELETED) this.removals.delete(id); }
      catch (error) { this.failure(error, "cleanup"); }
    }
    this.publish({});
  }
  async prepare(machine: Resource, operationId: string): Promise<ImageAttachment[]> {
    if (!this.alive || this.state.busy) throw new ImageInputError(ImageProblem.Transfer);
    this.publish({ busy: true, error: undefined });
    try {
      this.operationId = operationId;
      const result: ImageAttachment[] = [];
      for (const image of this.state.images) {
        if (!this.alive) throw new ImageInputError(ImageProblem.Transfer);
        if (image.begin && (image.begin.machineId !== machine.id || image.begin.machineRevision !== machine.revision || image.begin.operationId !== operationId)) {
          // Fresh selection never adopts a reference from another Runner or
          // input operation. Reconcile a lost admission before retiring it.
          if (!image.reference) { const old = await this.client().beginUpload(image.begin); verifyImageReference(old.upload?.attachment, { machineId: image.begin.machineId, mediaType: image.media, byteLength: BigInt(image.bytes.length), sha256: image.digest }); image.reference = old.upload!.attachment; }
          this.removals.set(image.reference!.id, { requestId: newRequestId(), reference: image.reference! });
          image.begin = undefined; image.reference = undefined; image.ready = false;
        }
        image.begin ??= { requestId: newRequestId(), draftId: this.draftId, operationId, machineId: machine.id, machineRevision: machine.revision, sessionId: this.scope.startsWith("session:") ? this.scope.slice(8) : "", mediaType: image.media, byteLength: BigInt(image.bytes.length), sha256: image.digest };
        let upload = (image.reference ? await this.client().getUpload({ attachmentId: image.reference.id }) : await this.client().beginUpload(image.begin)).upload;
        const expected = { machineId: machine.id, mediaType: image.media, byteLength: BigInt(image.bytes.length), sha256: image.digest };
        verifyImageReference(upload?.attachment, expected, image.reference?.id);
        if (!upload || upload.draftId !== this.draftId || upload.operationId !== operationId || upload.uploadedBytes > expected.byteLength) throw new ImageInputError(ImageProblem.Transfer);
        image.reference = upload.attachment;
        let offset = Number(upload.uploadedBytes);
        while (upload.state === AttachmentState.UPLOADING && offset < image.bytes.length) {
          if (!this.alive) throw new ImageInputError(ImageProblem.Transfer);
          const bytes = image.bytes.slice(offset, offset + imageLimits.chunk);
          upload = (await this.client().writeChunk({ attachmentId: image.reference!.id, offset: BigInt(offset), data: bytes, sha256: await imageDigest(bytes) })).upload;
          verifyImageReference(upload?.attachment, expected, image.reference!.id);
          offset += bytes.length;
          if (upload?.uploadedBytes !== BigInt(offset)) throw new ImageInputError(ImageProblem.Transfer);
        }
        if (!this.alive) throw new ImageInputError(ImageProblem.Transfer);
        if (upload?.state === AttachmentState.UPLOADING) upload = (await this.client().finishUpload({ attachmentId: image.reference!.id })).upload;
        verifyImageReference(upload?.attachment, expected, image.reference!.id);
        if (upload?.state !== AttachmentState.READY || upload.uploadedBytes !== expected.byteLength) throw new ImageInputError(ImageProblem.Transfer);
        image.ready = true; result.push(upload.attachment!);
      }
      if (!this.alive) throw new ImageInputError(ImageProblem.Transfer);
      return result;
    } catch (error) { this.failure(error, "transfer"); throw error; }
    finally { this.publish({ busy: false }); void this.retryCleanup(); }
  }
  accepted(requestId: string, ids: readonly string[]) {
    if (this.operationId !== requestId || ids.length !== this.state.images.length || this.state.images.some(image => !image.reference || !ids.includes(image.reference.id))) return;
    for (const image of this.state.images) URL.revokeObjectURL(image.preview);
    this.operationId = undefined; this.draftId = newRequestId(); this.publish({ images: [], error: undefined });
  }
  dispose() { this.alive = false; for (const image of this.state.images) URL.revokeObjectURL(image.preview); this.state = empty; this.listeners.clear(); }
}
class DraftStore {
  private drafts = new Map<string, ImageDraft>();
  constructor(readonly client: { current: Client<typeof AttachmentService> }) {}
  get(scope: string) { let draft = this.drafts.get(scope); if (!draft) { draft = new ImageDraft(() => this.client.current, scope); this.drafts.set(scope, draft); } return draft; }
  accepted(request: object) { const value = request as { requestId?: string; attachments?: ImageAttachment[] }; if (value.requestId && value.attachments?.length) for (const draft of this.drafts.values()) draft.accepted(value.requestId, value.attachments.map(image => image.id)); }
  dispose() { for (const draft of this.drafts.values()) draft.dispose(); this.drafts.clear(); }
}
const Context = createContext<DraftStore | undefined>(undefined);
export function ImageDraftProvider({ children }: { children: ReactNode }) {
  const transport = useTransport();
  const client = useRef(createClient(AttachmentService, transport)); client.current = createClient(AttachmentService, transport);
  const [store] = useState(() => new DraftStore(client));
  useRetainedMutationNotifications((_key, request) => store.accepted(request));
  const mounted = useRef(true);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; queueMicrotask(() => { if (!mounted.current) store.dispose(); }); }; }, [store]);
  return <Context.Provider value={store}>{children}</Context.Provider>;
}
export function useImageDraft(scope: string) {
  const context = useContext(Context), transport = useTransport();
  const client = useRef(createClient(AttachmentService, transport)); client.current = createClient(AttachmentService, transport);
  const fallback = useMemo(() => new DraftStore(client), []);
  const draft = (context ?? fallback).get(scope);
  const mounted = useRef(true);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; queueMicrotask(() => { if (!context && !mounted.current) fallback.dispose(); }); }; }, [context, fallback]);
  const state = useSyncExternalStore(draft.subscribe, draft.snapshot, draft.snapshot);
  return { ...state, controller: draft };
}
export function useImageRoute(machineId: string, agentId: string, active: boolean, needed: boolean, originalHarness?: string) {
  const status = useQuery(SystemQuery.getStatus, {}, { enabled: active, retry: false });
  const machine = useQuery(ResourceQuery.getResource, { kind: EntityKind.MACHINE, id: machineId }, { enabled: active && needed && Boolean(machineId), retry: false });
  const agent = useQuery(ResourceQuery.getResource, { kind: EntityKind.AGENT, id: agentId }, { enabled: active && needed && !originalHarness && Boolean(agentId), retry: false });
  const systemSupported = Boolean(status.data?.capabilities.includes(SystemCapability.IMAGE_INPUTS_V1));
  const row = machine.data?.resource, agentRow = agent.data?.resource;
  const machineSupported = row?.id === machineId && row.kind === EntityKind.MACHINE && row.revision > 0n && supportsResourceSchema(row) && items(document(row).worker_capabilities).some(value => value === WorkerCapability.IMAGE_INPUTS_V1 || value === "image-inputs-v1");
  const harnessSupported = originalHarness ? originalHarness === "codex" : agentRow?.id === agentId && agentRow.kind === EntityKind.AGENT && agentRow.revision > 0n && supportsResourceSchema(agentRow) && text(document(agentRow).harness) === "codex";
  return { systemSupported, ready: systemSupported && machineSupported && harnessSupported, machine: row, loading: needed && (machine.isFetching || agent.isFetching), error: machine.error ?? agent.error };
}
