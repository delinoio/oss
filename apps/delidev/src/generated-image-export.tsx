// SPDX-License-Identifier: Apache-2.0
import { useCallback, useState, useSyncExternalStore } from "react";
import { invoke } from "@tauri-apps/api/core";
import {
  newRequestId,
  type ImageAttachment,
} from "@delinoio/delidev-api-client";
import { copy, useLocale } from "./localization";
type Outcome = "pending" | "saved" | "canceled" | "failed" | "uncertain";
// Keep lost acknowledgments across presentation remounts. Only observation of the
// retained native operation can settle an admitted fence. A positively proved
// original non-admission response permits another explicit Save.
// Every mounted exporter of one session/reference/digest key observes this store,
// so a response that settles after a remount still reaches the current instance.
type Operation = { id: string; outcome: Outcome };
const operations = new Map<string, Operation>();
const listeners = new Map<string, Set<() => void>>();
function subscribeOperation(key: string, listener: () => void) {
  const set = listeners.get(key) ?? new Set<() => void>();
  set.add(listener);
  listeners.set(key, set);
  return () => {
    set.delete(listener);
    if (set.size === 0 && listeners.get(key) === set) listeners.delete(key);
  };
}
function notifyOperation(key: string) {
  for (const listener of [...(listeners.get(key) ?? [])]) listener();
}
// Only the operation still retained for the key may publish an outcome, so an
// older response never settles a newer operation.
function settleOperation(key: string, retained: Operation, next: Outcome) {
  if (operations.get(key) !== retained) return;
  retained.outcome = next;
  notifyOperation(key);
}
const outcome = (value: unknown): Outcome =>
  ["pending", "saved", "canceled", "failed", "uncertain"].includes(
    String(value),
  )
    ? (value as Outcome)
    : "uncertain";
function notAdmitted(value: unknown, operationId: string): boolean {
  if (!value || typeof value !== "object" || Array.isArray(value)) return false;
  const result = value as Record<string, unknown>;
  return (
    Object.keys(result).length === 3 &&
    result.status === "not-admitted" &&
    result.operationId === operationId &&
    typeof result.reason === "string" &&
    ["busy", "stopped", "invalid-input"].includes(result.reason)
  );
}
function base64(bytes: Uint8Array) {
  let text = "";
  for (let offset = 0; offset < bytes.length; offset += 8192)
    text += String.fromCharCode(...bytes.subarray(offset, offset + 8192));
  return btoa(text);
}
export function GeneratedImageExport({
  bytes,
  reference,
  sessionId,
  number,
  active,
}: {
  bytes: Uint8Array;
  reference: ImageAttachment;
  sessionId: string;
  number: number;
  active: boolean;
}) {
  useLocale();
  const key = `${sessionId}:${reference.id}:${reference.sha256}`;
  const state = useSyncExternalStore(
    useCallback(
      (listener: () => void) => subscribeOperation(key, listener),
      [key],
    ),
    () => operations.get(key)?.outcome,
    () => operations.get(key)?.outcome,
  );
  const [observing, setObserving] = useState(false);
  const save = async () => {
    if (
      operations.get(key)?.outcome === "pending" ||
      operations.get(key)?.outcome === "uncertain"
    )
      return;
    const retained = { id: newRequestId(), outcome: "pending" as Outcome };
    operations.set(key, retained);
    notifyOperation(key);
    try {
      const result = await invoke("export_generated_image", {
        request: {
          operationId: retained.id,
          sessionId,
          attachmentId: reference.id,
          sha256: reference.sha256,
          byteLength: Number(reference.byteLength),
          png: base64(bytes),
        },
      });
      // A proved non-admission settles as failed, which permits a later
      // explicit Save; the failed record keeps the retained identity guard.
      settleOperation(
        key,
        retained,
        notAdmitted(result, retained.id) ? "failed" : outcome(result),
      );
    } catch {
      settleOperation(key, retained, "uncertain");
    }
  };
  const check = async () => {
    const retained = operations.get(key);
    if (!retained) return;
    setObserving(true);
    try {
      settleOperation(
        key,
        retained,
        outcome(
          await invoke("read_generated_image_export", {
            operationId: retained.id,
          }),
        ),
      );
    } catch {
      settleOperation(key, retained, "uncertain");
    }
    setObserving(false);
  };
  return (
    <>
      <button
        type="button"
        disabled={!active || state === "pending" || state === "uncertain"}
        onClick={() => void save()}
      >
        {copy("image-input.exportGenerated", { number })}
      </button>
      {state && state !== "canceled" ? (
        <p role="status">
          {copy(
            state === "saved"
              ? "image-input.exportSaved"
              : state === "failed"
                ? "image-input.exportFailed"
                : state === "pending"
                  ? "image-input.exportPending"
                  : "image-input.exportUncertain",
          )}
        </p>
      ) : null}
      {state === "pending" || state === "uncertain" ? (
        <button
          type="button"
          disabled={!active || observing}
          onClick={() => void check()}
        >
          {copy("image-input.checkExport")}
        </button>
      ) : null}
    </>
  );
}
