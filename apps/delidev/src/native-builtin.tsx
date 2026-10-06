import { copy, useLocale } from "./localization";
import { object } from "./documents";

enum Builtin { Write = "write", Edit = "edit", ApplyPatch = "apply_patch", Glob = "glob", Grep = "grep", Question = "question", Task = "task" }
enum ToolState { Pending = "pending", Running = "running", Completed = "completed", Failed = "failed" }
enum FileEvent { Edited = "file-edited", Added = "file-added", Changed = "file-changed", Unlinked = "file-unlinked" }
const encoder = new TextEncoder();
function bounded(value: unknown, max = 256 * 1024): value is string { return typeof value === "string" && value.length <= max && !value.includes("\0") && !/[\uD800-\uDFFF]/u.test(value) && encoder.encode(value).length <= max; }
function count(value: unknown): value is number { return typeof value === "number" && Number.isSafeInteger(value) && value >= 0; }
function shape(value: unknown, names: string[]): boolean { return value !== null && typeof value === "object" && !Array.isArray(value) && Object.keys(value).every((key) => names.includes(key)); }
function objectJSON(value: unknown): value is string {
  if (!bounded(value)) return false;
  try { const parsed: unknown = JSON.parse(value); return parsed !== null && typeof parsed === "object" && !Array.isArray(parsed); } catch { return false; }
}
type Snapshot = { name: Builtin; status: ToolState; call: string; input: string; metadata?: string; raw?: string; title?: string; output?: string; error?: string; start?: number; provider?: boolean };
function snapshot(value: unknown): Snapshot | undefined {
  const s = object(value), b = object(s.builtin), timing = object(b.time), status = s.status as ToolState;
  if (s.kind !== "opencode-builtin" || s.read != null || s.shell != null || s.todo != null || s.command != null || s.changes != null || !Object.values(ToolState).includes(status) || !Object.values(Builtin).includes(b.name as Builtin) || !bounded(b.call_id, 1024) || !b.call_id.trim() || !objectJSON(b.input_json) || b.metadata_json != null && !objectJSON(b.metadata_json) || b.provider_executed != null && b.provider_executed !== false) return;
  if (!shape(s.builtin, ["name", "call_id", "input_json", "metadata_json", "raw", "title", "output", "error", "time", "provider_executed"]) || b.time != null && !shape(b.time, ["start", "end"])) return;
  for (const field of [b.raw, b.title, b.output, b.error]) if (field != null && !bounded(field)) return;
  if (b.time != null && (!count(timing.start) || timing.end != null && (!count(timing.end) || timing.end < timing.start))) return;
  if (status === ToolState.Pending) {
    if (!bounded(b.raw) || b.time != null || b.metadata_json != null || b.title != null || b.output != null || b.error != null) return;
  } else {
    if (b.raw != null || !count(timing.start)) return;
    if (status === ToolState.Running && (timing.end != null || b.output != null || b.error != null)) return;
    if (status === ToolState.Completed && (!count(timing.end) || !bounded(b.title) || !bounded(b.output) || !objectJSON(b.metadata_json) || b.error != null)) return;
    if (status === ToolState.Failed) {
      if (!count(timing.end) || !bounded(b.error) || b.output != null) return;
      // Only the original interrupted task preserves its running title.
      if (b.title != null && (b.name !== Builtin.Task || !objectJSON(b.metadata_json) || object(JSON.parse(b.metadata_json)).interrupted !== true)) return;
    }
  }
  return { name: b.name as Builtin, status, call: b.call_id, input: b.input_json, metadata: b.metadata_json as string | undefined, raw: b.raw as string | undefined, title: b.title as string | undefined, output: b.output as string | undefined, error: b.error as string | undefined, start: timing.start as number | undefined, provider: b.provider_executed as boolean | undefined };
}
function retained(tool: Record<string, unknown>, state: string): Snapshot[] | undefined {
  const first = snapshot(tool.started), states = tool.states ?? [];
  if (!first || first.status !== ToolState.Pending || tool.output != null || tool.inputs != null || tool.patches != null || !Array.isArray(states) || states.length > 1024) return;
  const values = [first]; let sequence = 0;
  for (const value of states) {
    const entry = object(value), next = snapshot(entry.snapshot);
    if (!count(entry.sequence) || entry.sequence <= sequence || entry.sequence > 100000 || !next || next.status !== ToolState.Pending && next.status !== ToolState.Running) return;
    sequence = entry.sequence; values.push(next);
  }
  if (state === "complete") { const completed = snapshot(tool.completed); if (!completed || completed.status !== ToolState.Completed && completed.status !== ToolState.Failed) return; values.push(completed); }
  else if (state !== "streaming" || tool.completed != null) return;
  for (let i = 1; i < values.length; i++) {
    const prior = values[i - 1]!, next = values[i]!;
    if (prior.name !== next.name || prior.call !== next.call || prior.provider !== next.provider) return;
    if (next.status === ToolState.Failed && next.title !== undefined && next.title !== prior.title) return;
    if (prior.status === ToolState.Pending) { if (next.status === ToolState.Completed) return; }
    else if (prior.status !== ToolState.Running || next.status === ToolState.Pending || prior.start !== next.start || prior.input !== next.input) return;
  }
  return values;
}
const labels: Record<Builtin, string> = { get write() { return copy("native-builtin.write_3f0092"); }, get edit() { return copy("native-builtin.edit_464c4f"); }, get apply_patch() { return copy("native-builtin.applyPatch_01bcfe"); }, get glob() { return copy("native-builtin.glob_18d00f"); }, get grep() { return copy("native-builtin.grep_16970e"); }, get question() { return copy("native-builtin.questionTool_1a05b1"); }, get task() { return copy("native-builtin.foregroundTask_168e9c"); } };

// JSON stays original text: do not round native numbers, discard metadata, or
// convert parameters into a new executable command or file-reading action.
export function NativeBuiltin({ tool, state }: { tool: Record<string, unknown>; state: string }) {
  useLocale();
  const values = retained(tool, state), latest = values?.at(-1);
  if (!values || !latest) return <details><summary>{copy("native-builtin.nativeToolUnavailable_efe84c")}</summary><p>{copy("native-builtin.theRetainedNativeOperationIsUnavailable_ed146e")}</p></details>;
  return <details><summary>{labels[latest.name]} · {latest.status}</summary>
    {latest.title !== undefined ? <p>{latest.title}</p> : null}
    <details><summary>{copy("native-builtin.originalNativeInput_b3b718")}</summary><pre>{latest.input}</pre></details>
    {latest.output !== undefined ? <section aria-label={copy("native-builtin.nativeToolResult_1cf5a0")}><pre>{latest.output}</pre></section> : null}
    {latest.error !== undefined ? <section aria-label={copy("native-builtin.nativeToolError_440d26")}><pre>{latest.error}</pre></section> : null}
    {latest.metadata !== undefined ? <details><summary>{copy("native-builtin.originalNativeMetadata_d7e8f9")}</summary><pre>{latest.metadata}</pre></details> : null}
    <details><summary>{copy("native-builtin.originalProposalAndObservations_8d63b6")}</summary><ol>{values.map((v, i) => <li key={i}><details><summary>{v.status}</summary><pre>{v.input}</pre>{v.raw !== undefined ? <pre>{v.raw}</pre> : null}{v.metadata !== undefined ? <pre>{v.metadata}</pre> : null}</details></li>)}</ol></details>
  </details>;
}

const fileLabels: Record<FileEvent, string> = { get "file-edited"() { return copy("native-builtin.nativeFileEdited_132303"); }, get "file-added"() { return copy("native-builtin.nativeFileAdded_82ed0c"); }, get "file-changed"() { return copy("native-builtin.nativeFileChanged_05aac0"); }, get "file-unlinked"() { return copy("native-builtin.nativeFileUnlinked_b796f3"); } };
export function NativeWorkspaceEvent({ progress, state }: { progress: Record<string, unknown>; state: string }) {
  useLocale();
  const w = object(progress.workspace);
  if (state !== "complete" || progress.kind !== "opencode-workspace" || progress.plan != null || progress.diff != null || progress.todo != null || progress.changes != null || !shape(progress.workspace, ["kind", "native_event_id", "file"]) || !Object.values(FileEvent).includes(w.kind as FileEvent) || typeof w.native_event_id !== "string" || !/^evt_[0-9a-f]{12}[a-zA-Z0-9]{14}$/.test(w.native_event_id) || !bounded(w.file, 32768) || !w.file.trim()) return <details><summary>{copy("native-builtin.nativeWorkspaceEventUnavailable_904936")}</summary><p>{copy("native-builtin.theRetainedFileNotificationIsUnavailable_ebdf4c")}</p></details>;
  return <details><summary>{fileLabels[w.kind as FileEvent]}</summary><pre>{w.file}</pre><p>{copy("native-builtin.thisNativeWorkspaceNotificationDoesNot_5280e0")}</p></details>;
}
