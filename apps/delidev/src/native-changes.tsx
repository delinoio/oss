import { LocalizedText, copy, useLocale } from "./localization";
import { object } from "./documents";

enum RevisionSource { Snapshot = "snapshot", Patch = "patch", Start = "step-start", Finish = "step-finish" }
enum ChangeSource { Session = "session-diff", Input = "input-summary" }
enum FileStatus { Added = "added", Deleted = "deleted", Modified = "modified" }
const encoder = new TextEncoder();
function bounded(value: unknown, max = 256 * 1024): value is string {
  return typeof value === "string" && value.length <= max && !value.includes("\0") && !/[\uD800-\uDFFF]/u.test(value) && encoder.encode(value).length <= max;
}
function count(value: unknown): value is number { return typeof value === "number" && Number.isSafeInteger(value) && value >= 0; }
function shape(value: unknown, names: string[]): boolean {
  return value !== null && typeof value === "object" && !Array.isArray(value) && Object.keys(value).every((key) => names.includes(key));
}
type Revision = { source: RevisionSource; hash: string; files: string[] | null };
function revisionSnapshot(value: unknown): Revision | undefined {
  const s = object(value), r = object(s.revision);
  if (!shape(s, ["kind", "text", "summary", "content", "revision"]) || s.kind !== "opencode-revision" || s.text !== "" || s.summary != null || s.content != null || !shape(s.revision, ["source", "hash", "files"]) || !Object.values(RevisionSource).includes(r.source as RevisionSource) || !bounded(r.hash, 4096) || !r.hash.trim()) return;
  if (r.source === RevisionSource.Patch) {
    if (!Array.isArray(r.files) || r.files.length > 4096 || !r.files.every((f) => bounded(f, 32768) && f.trim())) return;
  } else if (r.files != null) return;
  return { source: r.source as RevisionSource, hash: r.hash, files: r.files as string[] | null };
}
const revisionLabels: Record<RevisionSource, string> = { get snapshot() { return copy("native-changes.snapshotReference_7c953a"); }, get patch() { return copy("native-changes.changedFileReferences_9d7464"); }, get "step-start"() { return copy("native-changes.stepStartSnapshot_d9105a"); }, get "step-finish"() { return copy("native-changes.stepFinishSnapshot_835810"); } };

export function NativeRevision({ artifact, state }: { artifact: Record<string, unknown>; state: string }) {
  useLocale();
  const first = revisionSnapshot(artifact.started), completed = revisionSnapshot(artifact.completed);
  if (!first || artifact.deltas != null || state !== "complete" || !completed || first.source !== completed.source || first.hash !== completed.hash || (first.files == null) !== (completed.files == null) || first.files && (!completed.files || first.files.length !== completed.files.length || first.files.some((f, i) => f !== completed.files![i]))) return <details><summary>{copy("native-changes.nativeRevisionUnavailable_5ba088")}</summary><p>{copy("native-changes.theRetainedRevisionObservationIsIncomplete_cf4770")}</p></details>;
  return <details><summary>{revisionLabels[first.source]}</summary><p><LocalizedText id="native-changes.originalNativeReference_bbb471" components={{ s0: <code>{first.hash}</code> }} /></p>
    {first.files ? first.files.length ? <ul>{first.files.map((path, i) => <li key={i}><code>{path}</code></li>)}</ul> : <p>{copy("native-changes.theOriginalFileListIsEmpty_e072b3")}</p> : null}
  </details>;
}

type FileDiff = { file?: string; patch?: string; additions: number; deletions: number; status?: FileStatus };
function fileDiff(value: unknown): value is FileDiff {
  const d = object(value);
  return shape(value, ["file", "patch", "additions", "deletions", "status"]) && (d.file === undefined || bounded(d.file)) && (d.patch === undefined || bounded(d.patch)) && count(d.additions) && count(d.deletions) && (d.status === undefined || Object.values(FileStatus).includes(d.status as FileStatus));
}

// Reported patches are original inert evidence. This disclosure never reads
// files, applies a patch, navigates to a path, or treats snapshots as backups.
export function NativeChanges({ progress, state, turn }: { progress: Record<string, unknown>; state: string; turn: string }) {
  useLocale();
  const c = object(progress.changes);
  const valid = state === "complete" && progress.kind === "opencode-changes" && progress.workspace == null && progress.plan == null && progress.diff == null && progress.todo == null
    && shape(progress.changes, ["source", "native_event_id", "native_message_id", "title", "body", "diffs"])
    && typeof c.native_event_id === "string" && /^evt_[0-9a-f]{12}[a-zA-Z0-9]{14}$/.test(c.native_event_id)
    && (c.source === ChangeSource.Session && c.native_message_id === undefined && c.title == null && c.body == null || c.source === ChangeSource.Input && typeof c.native_message_id === "string" && /^msg_[0-9a-f]{12}[a-zA-Z0-9]{14}$/.test(c.native_message_id) && c.native_message_id === turn)
    && (c.title == null || bounded(c.title)) && (c.body == null || bounded(c.body)) && Array.isArray(c.diffs) && c.diffs.length <= 4096 && c.diffs.every(fileDiff);
  if (!valid) return <details><summary>{copy("native-changes.nativeChangesUnavailable_06d3fa")}</summary><p>{copy("native-changes.theRetainedChangeObservationIsUnavailable_2cf048")}</p></details>;
  const diffs = c.diffs as FileDiff[];
  return <details><summary>{c.source === ChangeSource.Session ? copy("native-changes.nativeSessionDiff_dd228a") : copy("native-changes.nativeInputChangeSummary_a7b5ad")}</summary>
    {typeof c.title === "string" ? <pre>{c.title}</pre> : null}{typeof c.body === "string" ? <pre>{c.body}</pre> : null}
    {diffs.length ? <ol>{diffs.map((d, i) => <li key={i}><details>
      <summary>{d.file === undefined ? copy("native-changes.fileUnavailable_b3b9ef") : d.file} · {d.status ?? copy("native-changes.statusUnavailable_7eb5af")} · +{d.additions} / −{d.deletions}</summary>
      {d.patch === undefined ? <p>{copy("native-changes.patchUnavailable_87d458")}</p> : <pre>{d.patch}</pre>}
    </details></li>)}</ol> : <p>{copy("native-changes.theOriginalDiffListIsEmpty_078d07")}</p>}
  </details>;
}
