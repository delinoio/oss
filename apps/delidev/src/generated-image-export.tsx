// SPDX-License-Identifier: Apache-2.0
import { useState } from "react";
import { invoke } from "@tauri-apps/api/core";
import { newRequestId, type ImageAttachment } from "@delinoio/delidev-api-client";
import { copy, useLocale } from "./localization";
type Outcome = "pending" | "saved" | "canceled" | "failed" | "uncertain";
// Keep lost acknowledgments across presentation remounts. Only observation of the
// retained native operation can release this fence; never retry its Save dialog.
const operations = new Map<string, { id: string; outcome: Outcome }>();
const outcome = (value: unknown): Outcome => ["pending", "saved", "canceled", "failed", "uncertain"].includes(String(value)) ? value as Outcome : "uncertain";
function base64(bytes: Uint8Array) { let text = ""; for (let offset = 0; offset < bytes.length; offset += 8192) text += String.fromCharCode(...bytes.subarray(offset, offset + 8192)); return btoa(text); }
export function GeneratedImageExport({ bytes, reference, sessionId, number, active }: { bytes: Uint8Array; reference: ImageAttachment; sessionId: string; number: number; active: boolean }) {
 useLocale(); const key = `${sessionId}:${reference.id}:${reference.sha256}`;
 const [state, setState] = useState<Outcome | undefined>(() => operations.get(key)?.outcome);
 const [observing, setObserving] = useState(false);
 const save = async () => {
  if (operations.get(key)?.outcome === "pending" || operations.get(key)?.outcome === "uncertain") return;
  const retained = { id: newRequestId(), outcome: "pending" as Outcome }; operations.set(key, retained); setState("pending");
  try { retained.outcome = outcome(await invoke("export_generated_image", { request: { operationId: retained.id, sessionId, attachmentId: reference.id, sha256: reference.sha256, byteLength: Number(reference.byteLength), png: base64(bytes) } })); }
  catch { retained.outcome = "uncertain"; }
  setState(retained.outcome);
 };
 const check = async () => { const retained = operations.get(key); if (!retained) return; setObserving(true); try { retained.outcome = outcome(await invoke("read_generated_image_export", { operationId: retained.id })); } catch { retained.outcome = "uncertain"; } setState(retained.outcome); setObserving(false); };
 return <><button type="button" disabled={!active || state === "pending" || state === "uncertain"} onClick={() => void save()}>{copy("image-input.exportGenerated", { number })}</button>{state && state !== "canceled" ? <p role="status">{copy(state === "saved" ? "image-input.exportSaved" : state === "failed" ? "image-input.exportFailed" : state === "pending" ? "image-input.exportPending" : "image-input.exportUncertain")}</p> : null}{state === "pending" || state === "uncertain" ? <button type="button" disabled={!active || observing} onClick={() => void check()}>{copy("image-input.checkExport")}</button> : null}</>;
}
