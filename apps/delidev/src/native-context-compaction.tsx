// SPDX-License-Identifier: Apache-2.0
import { object, type Document } from "./documents";

const exact = (value: Document, keys: string[]) => Object.keys(value).length === keys.length && keys.every((key) => Object.hasOwn(value, key));

export function validNativeContextCompaction(progress: unknown): boolean {
  const p = object(progress), c = object(p.compaction);
  return exact(p, ["kind", "compaction"]) && p.kind === "native-compaction" && exact(c, ["harness", "trigger", "stage", "native_item_id"]) && (c.harness === "codex" || c.harness === "opencode") && c.trigger === "automatic" && (c.stage === "started" || c.stage === "completed") && typeof c.native_item_id === "string" && c.native_item_id.trim().length > 0 && !c.native_item_id.includes("\0") && !/[\uD800-\uDFFF]/u.test(c.native_item_id) && new TextEncoder().encode(c.native_item_id).length <= 1024;
}

export function NativeContextCompaction({ progress, state }: { progress: unknown; state: string }) {
  if (state !== "complete" || !validNativeContextCompaction(progress)) return <details><summary>Context compaction · Unavailable</summary><p>The retained context observation is unavailable or inconsistent.</p></details>;
  const c = object(object(progress).compaction);
  return <details open><summary>Context compaction · Automatic · {c.stage === "started" ? "Started" : "Completed"}</summary>
    <p>{c.harness === "opencode" ? "OpenCode" : "Codex"} is compacting its working context. The conversation remains retained.</p>
    <dl><dt>Current context tokens</dt><dd>Not reported</dd><dt>Original context reference</dt><dd>{c.native_item_id as string}</dd></dl>
  </details>;
}
