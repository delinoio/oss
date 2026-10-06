import { LocalizedText, copy, useLocale } from "./localization";
import { object, type Document } from "./documents";

const exact = (value: Document, keys: string[]) => Object.keys(value).length === keys.length && keys.every((key) => Object.hasOwn(value, key));

export function validNativeContextCompaction(progress: unknown): boolean {
  const p = object(progress), c = object(p.compaction);
  return exact(p, ["kind", "compaction"]) && p.kind === "native-compaction" && exact(c, ["harness", "trigger", "stage", "native_item_id"]) && (c.harness === "codex" || c.harness === "opencode") && (c.harness !== "opencode" || typeof c.native_item_id === "string" && /^prt_[0-9a-f]{12}[a-zA-Z0-9]{14}$/.test(c.native_item_id)) && c.trigger === "automatic" && (c.stage === "started" || c.stage === "completed") && typeof c.native_item_id === "string" && c.native_item_id.trim().length > 0 && !c.native_item_id.includes("\0") && !/[\uD800-\uDFFF]/u.test(c.native_item_id) && new TextEncoder().encode(c.native_item_id).length <= 1024;
}

export function NativeContextCompaction({ progress, state }: { progress: unknown; state: string }) {
  useLocale();
  if (state !== "complete" || !validNativeContextCompaction(progress)) return <details><summary>{copy("native-context-compaction.contextCompactionUnavailable_368e82")}</summary><p>{copy("native-context-compaction.theRetainedContextObservationIsUnavailable_e3e304")}</p></details>;
  const c = object(object(progress).compaction);
  return <details open><summary><LocalizedText id="native-context-compaction.contextCompactionAutomatic_e2d9be" components={{ s0: <>{c.stage === "started" ? copy("native-context-compaction.started_ecbc89") : copy("native-context-compaction.completed_22a970")}</> }} /></summary>
    <p><LocalizedText id="native-context-compaction.isCompactingItsWorkingContextThe_3d6bfb" components={{ s0: <>{c.harness === "opencode" ? copy("native-context-compaction.opencode_3af0e5") : copy("native-context-compaction.codex_616efb")}</> }} /></p>
    <dl><dt>{copy("native-context-compaction.currentContextTokens_77a977")}</dt><dd>{copy("native-context-compaction.notReported_adadfa")}</dd><dt>{copy("native-context-compaction.originalContextReference_ad006e")}</dt><dd>{c.native_item_id as string}</dd></dl>
  </details>;
}
