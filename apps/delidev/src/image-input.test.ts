// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { webcrypto } from "node:crypto";
import { expect, it, vi, beforeEach, afterEach } from "vitest";
import { EntityKind, ResourceSchema, ImageMediaType, newRequestId } from "@delinoio/delidev-api-client";
import { acknowledgeImages, inspectImage, imageDigest, imageLimits, retainedImages } from "./image-input";
export const pngBytes = Uint8Array.from(Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+a9d8AAAAASUVORK5CYII=", "base64"));
beforeEach(() => vi.stubGlobal("crypto", webcrypto));
afterEach(() => vi.unstubAllGlobals());
it("identifies original still bytes and rejects mismatched media declarations", async () => {
  expect(inspectImage(pngBytes, "image/png")).toBe(ImageMediaType.PNG);
  expect(() => inspectImage(pngBytes, "image/jpeg")).toThrow();
  expect(() => inspectImage(new Uint8Array([1, 2, 3]), "image/png")).toThrow();
  expect(await imageDigest(pngBytes)).toMatch(/^[0-9a-f]{64}$/);
});
it("rejects truncated and animated PNG chunks and unsafe dimensions before decoding", () => {
  expect(() => inspectImage(pngBytes.slice(0, -1))).toThrow();
  const animation = new Uint8Array(pngBytes.length + 12); animation.set(pngBytes.slice(0, -12)); animation.set(new TextEncoder().encode("acTL"), pngBytes.length - 8); animation.set(pngBytes.slice(-12), pngBytes.length);
  expect(() => inspectImage(animation)).toThrow();
  const huge = pngBytes.slice(); new DataView(huge.buffer).setUint32(16, imageLimits.pixels + 1);
  expect(() => inspectImage(huge)).toThrow();
});
it("rejects WebP animation and inconsistent container lengths", () => {
  const bytes = new Uint8Array(30), view = new DataView(bytes.buffer); bytes.set(new TextEncoder().encode("RIFF")); view.setUint32(4, 22, true); bytes.set(new TextEncoder().encode("WEBPANIM"), 8); view.setUint32(16, 10, true);
  expect(() => inspectImage(bytes)).toThrow();
  view.setUint32(4, 0, true); expect(() => inspectImage(bytes)).toThrow();
});
it("validates bounded complete metadata without promoting missing or malformed evidence", () => {
  const value = { id: newRequestId(), machine_id: newRequestId(), media_type: "image/png", byte_length: pngBytes.length, sha256: "a".repeat(64) };
  expect(retainedImages([value])?.[0].byteLength).toBe(BigInt(pngBytes.length));
  expect(retainedImages(undefined)).toEqual([]);
  for (const invalid of [{ ...value, id: "foreign" }, { ...value, byte_length: imageLimits.individual + 1 }, { ...value, sha256: "unknown" }, { ...value, media_type: "image/gif" }]) expect(retainedImages([invalid])).toBeUndefined();
  expect(retainedImages([value, value])).toBeUndefined();
});

it("requires the original receipt, Session, queued identity and exact ordered references",()=>{
 const session=create(ResourceSchema,{id:newRequestId(),kind:EntityKind.SESSION,revision:1n,schemaVersion:1,documentJson:new TextEncoder().encode("{}")});
 const metadata={id:newRequestId(),machine_id:newRequestId(),media_type:"image/png",byte_length:1,sha256:"a".repeat(64)};
 const input=create(ResourceSchema,{id:newRequestId(),sessionId:session.id,kind:EntityKind.QUEUE,revision:1n,schemaVersion:1,documentJson:new TextEncoder().encode(JSON.stringify({attachments:[metadata]}))});
 const requestId=newRequestId(),change={requestId,session,input},refs=retainedImages([metadata])!;
 expect(acknowledgeImages(change,requestId,refs,session.id,input.id)).toBe(true);
 expect(acknowledgeImages(change,newRequestId(),refs)).toBe(false);
 expect(acknowledgeImages(change,requestId,refs,newRequestId())).toBe(false);
 expect(acknowledgeImages(change,requestId,refs,session.id,newRequestId())).toBe(false);
 expect(acknowledgeImages({...change,session:create(ResourceSchema,{...session,revision:0n})},requestId,refs)).toBe(false);
 expect(acknowledgeImages({...change,input:create(ResourceSchema,{...input,documentJson:new TextEncoder().encode("{}")})},requestId,refs)).toBe(false);
});
