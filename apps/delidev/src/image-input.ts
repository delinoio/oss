// SPDX-License-Identifier: Apache-2.0
import { Code, ConnectError } from "@connectrpc/connect";
import { EntityKind, ImageMediaType, supportsResourceSchema, isEntityId, type Resource, type ImageAttachment } from "@delinoio/delidev-api-client";

export const imageLimits = Object.freeze({ count: 8, individual: 10 * 1024 * 1024, total: 40 * 1024 * 1024, pixels: 40_000_000, chunk: 256 * 1024 });
export const imageMime: Record<ImageMediaType, string> = { [ImageMediaType.UNSPECIFIED]: "", [ImageMediaType.PNG]: "image/png", [ImageMediaType.JPEG]: "image/jpeg", [ImageMediaType.WEBP]: "image/webp" };
export enum ImageProblem { Invalid = "invalid", Limits = "limits", Unsupported = "unsupported", Transfer = "transfer", Cleanup = "cleanup" }
export class ImageInputError extends Error { constructor(readonly problem: ImageProblem) { super(problem); } }
const invalid = () => { throw new ImageInputError(ImageProblem.Invalid); };
function ascii(bytes: Uint8Array, start: number, end: number) { return String.fromCharCode(...bytes.subarray(start, end)); }

/** Inspect size and animation before allocating a decoder. Decoding remains
 * required: a matching signature or supplied media type is not image evidence. */
export function inspectImage(bytes: Uint8Array, declared = ""): ImageMediaType {
  if (!bytes.length || bytes.length > imageLimits.individual) throw new ImageInputError(ImageProblem.Limits);
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  let media: ImageMediaType, width = 0, height = 0;
  if (bytes.length >= 33 && bytes[0] === 137 && ascii(bytes, 1, 4) === "PNG" && bytes[4] === 13 && bytes[5] === 10 && bytes[6] === 26 && bytes[7] === 10) {
    media = ImageMediaType.PNG;
    if (ascii(bytes, 12, 16) !== "IHDR" || view.getUint32(8) !== 13) invalid();
    width = view.getUint32(16); height = view.getUint32(20);
    let offset = 8, ended = false;
    while (offset + 12 <= bytes.length) {
      const length = view.getUint32(offset), kind = ascii(bytes, offset + 4, offset + 8);
      if (length > bytes.length - offset - 12 || ["acTL", "fcTL", "fdAT"].includes(kind)) invalid();
      offset += length + 12;
      if (kind === "IEND") { if (length !== 0 || offset !== bytes.length) invalid(); ended = true; break; }
    }
    if (!ended) invalid();
  } else if (bytes.length >= 4 && bytes[0] === 255 && bytes[1] === 216 && bytes.at(-2) === 255 && bytes.at(-1) === 217) {
    media = ImageMediaType.JPEG;
    let offset = 2;
    while (offset + 4 <= bytes.length) {
      if (bytes[offset++] !== 255) invalid();
      while (bytes[offset] === 255) offset++;
      const marker = bytes[offset++];
      if (marker === 218 || marker === 217) break;
      if (marker === 1 || marker >= 208 && marker <= 215) continue;
      if (offset + 2 > bytes.length) invalid();
      const length = view.getUint16(offset);
      if (length < 2 || offset + length > bytes.length) invalid();
      if (marker === 226 && ascii(bytes, offset + 2, offset + 5) === "MPF") invalid();
      if ([192, 193, 194, 195, 197, 198, 199, 201, 202, 203, 205, 206, 207].includes(marker)) {
        if (length < 8) invalid(); height = view.getUint16(offset + 3); width = view.getUint16(offset + 5);
      }
      offset += length;
    }
  } else if (bytes.length >= 20 && ascii(bytes, 0, 4) === "RIFF" && ascii(bytes, 8, 12) === "WEBP") {
    media = ImageMediaType.WEBP;
    if (view.getUint32(4, true) !== bytes.length - 8) invalid();
    let offset = 12;
    while (offset + 8 <= bytes.length) {
      const kind = ascii(bytes, offset, offset + 4), length = view.getUint32(offset + 4, true), start = offset + 8;
      if (length > bytes.length - start || ["ANIM", "ANMF"].includes(kind)) invalid();
      if (kind === "VP8X") {
        if (length !== 10 || bytes[start] & 2) invalid();
        width = 1 + bytes[start + 4] + (bytes[start + 5] << 8) + (bytes[start + 6] << 16);
        height = 1 + bytes[start + 7] + (bytes[start + 8] << 8) + (bytes[start + 9] << 16);
      } else if (kind === "VP8 " && length >= 10) {
        if (bytes[start + 3] !== 157 || bytes[start + 4] !== 1 || bytes[start + 5] !== 42) invalid();
        width = view.getUint16(start + 6, true) & 16383; height = view.getUint16(start + 8, true) & 16383;
      } else if (kind === "VP8L" && length >= 5) {
        if (bytes[start] !== 47) invalid();
        const dimensions = view.getUint32(start + 1, true); width = (dimensions & 16383) + 1; height = ((dimensions >>> 14) & 16383) + 1;
      }
      offset = start + length + (length % 2);
    }
    if (offset !== bytes.length) invalid();
  } else return invalid();
  if (!width || !height || width * height > imageLimits.pixels || declared && declared !== imageMime[media]) invalid();
  return media;
}
export async function imageDigest(bytes: Uint8Array): Promise<string> {
  const digest = await crypto.subtle.digest("SHA-256", new Uint8Array(bytes));
  return [...new Uint8Array(digest)].map(value => value.toString(16).padStart(2, "0")).join("");
}
export async function decodeImage(bytes: Uint8Array, media: ImageMediaType): Promise<void> {
  const blob = new Blob([new Uint8Array(bytes)], { type: imageMime[media] });
  try {
    if (typeof createImageBitmap === "function") {
      const image = await createImageBitmap(blob);
      try { if (!image.width || !image.height || image.width * image.height > imageLimits.pixels) invalid(); }
      finally { image.close(); }
    } else {
      const url = URL.createObjectURL(blob);
      try { const image = new Image(); image.src = url; await image.decode(); if (!image.naturalWidth || !image.naturalHeight || image.naturalWidth * image.naturalHeight > imageLimits.pixels) invalid(); }
      finally { URL.revokeObjectURL(url); }
    }
  } catch { invalid(); }
}
export function retainedImages(value: unknown): ImageAttachment[] | undefined {
  if (value === undefined) return [];
  if (!Array.isArray(value) || value.length > imageLimits.count) return;
  const refs: ImageAttachment[] = [];
  for (const raw of value) {
    if (!raw || typeof raw !== "object") return;
    const row = raw as Record<string, unknown>;
    const media = Object.entries(imageMime).find(([key, mime]) => Number(key) !== ImageMediaType.UNSPECIFIED && mime === row.media_type)?.[0];
    const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
    if (typeof row.id !== "string" || !uuid.test(row.id) || typeof row.machine_id !== "string" || !uuid.test(row.machine_id) || !media || typeof row.byte_length !== "number" || !Number.isSafeInteger(row.byte_length) || row.byte_length < 1 || row.byte_length > imageLimits.individual || typeof row.sha256 !== "string" || !/^[0-9a-f]{64}$/.test(row.sha256)) return;
    refs.push({ $typeName: "delidev.v1.ImageAttachment", id: row.id, machineId: row.machine_id, mediaType: Number(media), byteLength: BigInt(row.byte_length), sha256: row.sha256 });
  }
  if (new Set(refs.map(ref => ref.id)).size !== refs.length || refs.reduce((total, ref) => total + Number(ref.byteLength), 0) > imageLimits.total) return;
  return refs;
}
export function imageTransferError(error: unknown) { return error instanceof ImageInputError ? error.problem : ImageProblem.Transfer; }
export function verifyImageReference(actual: ImageAttachment | undefined, expected: Omit<ImageAttachment, "$typeName" | "id">, id?: string): actual is ImageAttachment {
  if (!actual || !isEntityId(actual.id) || id && actual.id !== id || actual.machineId !== expected.machineId || actual.mediaType !== expected.mediaType || actual.byteLength !== expected.byteLength || actual.sha256 !== expected.sha256) throw new ConnectError("Image transfer identity could not be verified.", Code.Internal);
  return true;
}

/** Image admission requires the original receipt and full ordered references.
 * Missing acknowledgment evidence keeps the original mutation uncertain. */
export function acknowledgeImages(change: { requestId: string; input?: Resource; session?: Resource } | undefined, requestId: string, expected: readonly ImageAttachment[], sessionId?: string, inputId?: string) {
  if (!change || change.requestId !== requestId || !change.input || !isEntityId(change.input.id) || (inputId && change.input.id !== inputId) || change.input.kind !== EntityKind.QUEUE || change.input.revision < 1n || !supportsResourceSchema(change.input) || !change.session || !isEntityId(change.session.id) || change.session.kind !== EntityKind.SESSION || change.session.revision < 1n || !supportsResourceSchema(change.session) || (sessionId && change.session.id !== sessionId) || change.input.sessionId !== change.session.id) return false;
  try {
    const value = JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(change.input.documentJson));
    const actual = retainedImages(value.attachments);
    return Boolean(actual && actual.length === expected.length && actual.every((image, index) => image.id === expected[index].id && image.machineId === expected[index].machineId && image.mediaType === expected[index].mediaType && image.byteLength === expected[index].byteLength && image.sha256 === expected[index].sha256));
  } catch { return false; }
}
