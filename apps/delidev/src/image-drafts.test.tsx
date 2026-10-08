// SPDX-License-Identifier: Apache-2.0
import { webcrypto } from "node:crypto";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createClient, createRouterTransport } from "@connectrpc/connect";
import { AttachmentService, AttachmentState, AttachmentUploadSchema, ImageAttachmentSchema, ResourceSchema, EntityKind, newRequestId, type AttachmentUpload, type BeginUploadRequest } from "@delinoio/delidev-api-client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { ImageDraft } from "./image-drafts";
import { imageDigest, imageLimits } from "./image-input";
const pngBytes = Uint8Array.from(Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+a9d8AAAAASUVORK5CYII=", "base64"));
beforeEach(() => { vi.stubGlobal("crypto", webcrypto); const BaseURL = URL; vi.stubGlobal("URL", class extends BaseURL { static createObjectURL = vi.fn(() => `blob:fixture-${newRequestId()}`); static revokeObjectURL = vi.fn(); }); });
afterEach(() => vi.unstubAllGlobals());
function file(bytes = pngBytes, type = "image/png") { const value = new File([bytes], "private-image-name.png", { type }); Object.defineProperty(value, "arrayBuffer", { value: async () => bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength) }); return value; }
function fixture() {
  const uploads = new Map<string, AttachmentUpload>(), requests = new Map<string, string>();
  const begin = vi.fn(async (request: BeginUploadRequest) => {
    const prior = requests.get(request.requestId); if (prior) return { upload: uploads.get(prior) };
    const ref = create(ImageAttachmentSchema, { id: newRequestId(), machineId: request.machineId, mediaType: request.mediaType, byteLength: request.byteLength, sha256: request.sha256 });
    const upload = create(AttachmentUploadSchema, { attachment: ref, state: AttachmentState.UPLOADING, draftId: request.draftId, operationId: request.operationId }); uploads.set(ref.id, upload); requests.set(request.requestId, ref.id); return { upload };
  });
  const write = vi.fn(async (request: { attachmentId: string; offset: bigint; data: Uint8Array; sha256: string }) => { const upload = uploads.get(request.attachmentId)!; expect(request.offset).toBe(upload.uploadedBytes); expect(await imageDigest(request.data)).toBe(request.sha256); expect(request.data.length).toBeLessThanOrEqual(imageLimits.chunk); upload.uploadedBytes += BigInt(request.data.length); return { upload }; });
  const finish = vi.fn(async (request: { attachmentId: string }) => { const upload = uploads.get(request.attachmentId)!; upload.state = AttachmentState.READY; return { upload }; });
  const remove = vi.fn(async (request: { attachmentId: string }) => { const upload = uploads.get(request.attachmentId)!; upload.state = AttachmentState.DELETED; return { upload }; });
  const client = createClient(AttachmentService, createRouterTransport(router => router.service(AttachmentService, { beginUpload: begin, writeChunk: write, finishUpload: finish, getUpload: request => ({ upload: uploads.get(request.attachmentId) }), deleteDraftAttachment: remove })));
  const machine = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.MACHINE, revision: 7n, schemaVersion: 1 });
  const decode = vi.fn(async () => {}), draft = new ImageDraft(() => client, `session:${newRequestId()}`, decode);
  vi.spyOn(console, "warn").mockImplementation(() => {});
  return { draft, machine, begin, write, finish, remove, decode, uploads };
}
it("validates every file before accepting a batch and never silently drops a bad image", async () => {
  const value = fixture(); await value.draft.add([file()]); await value.draft.add([file(), file(new Uint8Array([1]), "image/png")]);
  expect(value.draft.snapshot().images).toHaveLength(1); expect(value.draft.snapshot().error).toBe("invalid"); expect(value.begin).not.toHaveBeenCalled();
});
it("preserves ordered exact bytes, scoped identities and READY references", async () => {
  const value = fixture(); await value.draft.add([file(), file()]); const operation = newRequestId(); const refs = await value.draft.prepare(value.machine, operation);
  expect(refs).toHaveLength(2); expect(new Set(refs.map(ref => ref.id)).size).toBe(2); expect(refs.map(ref => ref.sha256)).toEqual([await imageDigest(pngBytes), await imageDigest(pngBytes)]);
  expect(value.write.mock.calls.every(([request]) => Buffer.from(request.data).equals(Buffer.from(pngBytes)))).toBe(true);
  expect(value.begin.mock.calls.every(([request]) => request.operationId === operation && request.machineRevision === 7n && Boolean(request.sessionId))).toBe(true);
  value.draft.accepted(newRequestId(), refs.map(ref => ref.id)); expect(value.draft.snapshot().images).toHaveLength(2);
  value.draft.accepted(operation, refs.map(ref => ref.id)); expect(value.draft.snapshot().images).toHaveLength(0); expect(value.remove).not.toHaveBeenCalled();
});
it("reconciles lost Finish acknowledgment without retransferring or changing the original operation", async () => {
  const value = fixture(); await value.draft.add([file()]); const operation = newRequestId(), original = value.finish.getMockImplementation()!;
  value.finish.mockImplementationOnce(async request => { await original(request); throw new ConnectError("fixture reply lost", Code.Unavailable); });
  await expect(value.draft.prepare(value.machine, operation)).rejects.toThrow(); const begin = value.begin.mock.calls[0][0];
  const refs = await value.draft.prepare(value.machine, operation); expect(refs).toHaveLength(1); expect(value.begin).toHaveBeenCalledTimes(1); expect(value.write).toHaveBeenCalledTimes(1); expect(value.finish).toHaveBeenCalledTimes(1); expect(value.draft.snapshot().images[0].begin).toMatchObject({ requestId: begin.requestId, operationId: operation });
});
it("restages for a different Runner and preserves cleanup obligations after an offline removal", async () => {
  const value = fixture(); await value.draft.add([file()]); const operation = newRequestId(); const first = await value.draft.prepare(value.machine, operation);
  value.remove.mockRejectedValueOnce(new ConnectError("fixture offline", Code.Unavailable));
  const second = await value.draft.prepare(create(ResourceSchema, { ...value.machine, id: newRequestId() }), operation);
  expect(second[0].id).not.toBe(first[0].id); expect(second[0].machineId).not.toBe(first[0].machineId);
  await value.draft.retryCleanup(); expect(value.remove).toHaveBeenCalled();
});
it("enforces count and individual/combined byte limits before reading files", async () => {
  const value = fixture(); await value.draft.add(Array.from({ length: 8 }, () => file())); expect(value.draft.snapshot().images).toHaveLength(8);
  await value.draft.add([file()]); expect(value.draft.snapshot().error).toBe("limits"); expect(value.draft.snapshot().images).toHaveLength(8);
  const over = file(); Object.defineProperty(over, "size", { value: imageLimits.individual + 1 }); const other = fixture(); await other.draft.add([over]); expect(other.decode).not.toHaveBeenCalled();
  const large = file(); Object.defineProperty(large, "size", { value: imageLimits.individual }); await other.draft.add(Array.from({ length: 5 }, () => large)); expect(other.decode).not.toHaveBeenCalled(); expect(other.draft.snapshot().error).toBe("limits");
});
it("stops new transfer effects after connection disposal", async () => {
  const value = fixture(); await value.draft.add([file()]); const original = value.begin.getMockImplementation()!;
  value.begin.mockImplementationOnce(async request => { const result = await original(request); value.draft.dispose(); return result; });
  await expect(value.draft.prepare(value.machine, newRequestId())).rejects.toThrow(); expect(value.write).not.toHaveBeenCalled(); expect(value.finish).not.toHaveBeenCalled();
});
it("replays a lost Begin with its original identity before removing remote staging", async () => {
  const value = fixture(); await value.draft.add([file()]); const original = value.begin.getMockImplementation()!;
  value.begin.mockImplementationOnce(async request => { await original(request); throw new ConnectError("fixture reply lost", Code.Unavailable); });
  await expect(value.draft.prepare(value.machine,newRequestId())).rejects.toThrow();
  const request = value.begin.mock.calls[0][0];
  await value.draft.remove(value.draft.snapshot().images[0].key);
  expect(value.begin.mock.calls[1][0]).toEqual(request); expect(value.remove).toHaveBeenCalledOnce(); expect(value.draft.snapshot().images).toHaveLength(0);
});
it("keeps the original transfer offset after a lost chunk response", async () => {
  const value = fixture(); await value.draft.add([file()]); const original = value.write.getMockImplementation()!;
  value.write.mockImplementationOnce(async request => { await original(request); throw new ConnectError("fixture reply lost", Code.Unavailable); });
  const requestId = newRequestId(); await expect(value.draft.prepare(value.machine,requestId)).rejects.toThrow();
  const refs = await value.draft.prepare(value.machine,requestId);
  expect(refs).toHaveLength(1); expect(value.write).toHaveBeenCalledOnce(); expect(value.begin).toHaveBeenCalledOnce(); expect(value.finish).toHaveBeenCalledOnce();
});
