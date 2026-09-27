import { object, type Document } from "./documents";
import { validClaudeToolReference } from "./native-claude-tool-progress";

enum Kind { Started = "task_started", Progress = "task_progress", Updated = "task_updated", Notification = "task_notification", Background = "background_tasks_changed" }
enum Status { Pending = "pending", Running = "running", Paused = "paused", Completed = "completed", Failed = "failed", Killed = "killed", Stopped = "stopped" }
const text = (v: unknown, limit = 256 << 10, required = false) => typeof v === "string" && (!required || v.trim().length > 0) && !v.includes("\0") && !/[\uD800-\uDFFF]/u.test(v) && new TextEncoder().encode(v).length <= limit;
const count = (v: unknown) => typeof v === "string" && /^(0|[1-9][0-9]{0,19})$/.test(v) && BigInt(v) <= 18446744073709551615n;
const fields = (v: Document, required: string[], optional: string[] = []) => required.every((key) => Object.hasOwn(v, key)) && Object.keys(v).every((key) => required.includes(key) || optional.includes(key));
const optional = (v: Document, key: string, valid: (value: unknown) => boolean) => !Object.hasOwn(v, key) || valid(v[key]);
const bool = (v: unknown) => typeof v === "boolean";
const status = (v: unknown) => Object.values(Status).includes(v as Status);
const usage = (v: unknown) => { const u = object(v); return fields(u, ["total_tokens", "tool_uses", "duration_ms"]) && Object.values(u).every(count); };
const flags = ["is_backgrounded", "skip_transcript", "ambient"];

export function validClaudeTask(value: unknown): boolean {
  const v = object(value);
  if (!Object.values(Kind).includes(v.kind as Kind) || !flags.every((key) => optional(v, key, bool)) || !["description", "summary", "output_file", "last_tool_name"].every((key) => optional(v, key, text)) || !optional(v, "tool", validClaudeToolReference) || !optional(v, "usage", usage)) return false;
  if (v.kind === Kind.Background) {
    if (!fields(v, ["kind", "background"]) || !Array.isArray(v.background) || v.background.length > 128) return false;
    const ids = new Set<unknown>();
    for (const item of v.background) {
      const task = object(item);
      if (!fields(task, ["task_id", "task_type", "description"], ["ambient"]) || !text(task.task_id, 1024, true) || !text(task.task_type, 256, true) || !text(task.description) || !optional(task, "ambient", bool) || ids.has(task.task_id)) return false;
      ids.add(task.task_id);
    }
    return true;
  }
  if (!text(v.task_id, 1024, true)) return false;
  if (v.kind === Kind.Started) return fields(v, ["kind", "task_id", "task_type", "description", "tool"], flags) && v.task_type === "local_bash" && object(v.tool).name === "Bash";
  if (v.kind === Kind.Progress) return fields(v, ["kind", "task_id", "description", "usage"], ["tool", "summary", "last_tool_name"]);
  if (v.kind === Kind.Notification) return fields(v, ["kind", "task_id", "status", "output_file", "summary"], ["tool", "reason", "usage", "skip_transcript", "ambient"]) && [Status.Completed, Status.Failed, Status.Stopped].includes(v.status as Status) && optional(v, "reason", (reason) => reason === "worker_restart" && v.status === Status.Stopped);
  const patch = object(v.patch);
  return fields(v, ["kind", "task_id", "patch"]) && v.patch !== null && typeof v.patch === "object" && !Array.isArray(v.patch) && fields(patch, [], ["status", "description", "end_time", "total_paused_ms", "error", "is_backgrounded"]) && optional(patch, "status", (value) => status(value) && value !== Status.Stopped) && optional(patch, "description", text) && optional(patch, "error", text) && optional(patch, "end_time", count) && optional(patch, "total_paused_ms", count) && optional(patch, "is_backgrounded", bool);
}

function TaskUsage({ value }: { value: unknown }) {
  const v = object(value);
  return <><dl><dt>Task tokens</dt><dd>{v.total_tokens as string}</dd><dt>Task tool uses</dt><dd>{v.tool_uses as string}</dd><dt>Task duration (ms)</dt><dd>{v.duration_ms as string}</dd></dl><p>These task counters can overlap provider usage and do not establish billed cost.</p></>;
}
function TaskText({ label, value }: { label: string; value: unknown }) {
  return value === undefined ? null : <details><summary>{label}</summary><pre>{value as string}</pre></details>;
}
function TaskFlags({ value }: { value: Document }) {
  const labels = { is_backgrounded: "Backgrounded", skip_transcript: "Skip transcript", ambient: "Ambient" };
  return <dl>{flags.map((key) => <div key={key}><dt>{labels[key as keyof typeof labels]}</dt><dd>{value[key] === undefined ? "Not reported" : value[key] ? "True" : "False"}</dd></div>)}</dl>;
}
export function NativeClaudeTask({ value }: { value: unknown }) {
  const v = object(value), patch = object(v.patch), ref = object(v.tool);
  const labels = { [Kind.Started]: "Task started", [Kind.Progress]: "Task progress", [Kind.Updated]: "Task updated", [Kind.Notification]: "Task notification", [Kind.Background]: "Background task snapshot" };
  return <section aria-label="Original Claude task observation">
    <h4>{labels[v.kind as Kind]}</h4>
    {v.kind === Kind.Background ? <>
      {(v.background as Document[]).length === 0 ? <p>No background tasks were listed.</p> : <ul>{(v.background as Document[]).map((task) => <li key={task.task_id as string}><dl><dt>Task</dt><dd>{task.task_id as string}</dd><dt>Type</dt><dd>{task.task_type as string}</dd><dt>Ambient</dt><dd>{task.ambient === undefined ? "Not reported" : task.ambient ? "True" : "False"}</dd></dl><TaskText label="Task description" value={task.description} /></li>)}</ul>}
      <p>This snapshot does not complete or stop previously observed tasks.</p>
    </> : <>
      <dl><dt>Task</dt><dd>{v.task_id as string}</dd>{v.tool !== undefined ? <><dt>Original tool</dt><dd>{ref.name as string}</dd></> : null}{v.task_type !== undefined ? <><dt>Task type</dt><dd>{v.task_type as string}</dd></> : null}
        {v.status !== undefined || patch.status !== undefined ? <><dt>Reported task status</dt><dd>{(v.status ?? patch.status) as string}</dd></> : null}
        {v.reason !== undefined ? <><dt>Reported reason</dt><dd>{v.reason as string}</dd></> : null}
        {v.last_tool_name !== undefined ? <><dt>Last tool</dt><dd>{v.last_tool_name as string}</dd></> : null}
        {patch.end_time !== undefined ? <><dt>Reported end time</dt><dd>{patch.end_time as string}</dd></> : null}
        {patch.total_paused_ms !== undefined ? <><dt>Total paused (ms)</dt><dd>{patch.total_paused_ms as string}</dd></> : null}
      </dl>
      <TaskText label="Task description" value={v.description ?? patch.description} />
      <TaskText label="Task summary" value={v.summary} />
      <TaskText label="Task diagnostic" value={patch.error} />
      <TaskText label="Reported output file" value={v.output_file} />
      {v.kind === Kind.Started || v.kind === Kind.Notification ? <TaskFlags value={v} /> : null}
      {patch.is_backgrounded !== undefined ? <dl><dt>Backgrounded</dt><dd>{patch.is_backgrounded ? "True" : "False"}</dd></dl> : null}
      {v.usage !== undefined ? <TaskUsage value={v.usage} /> : null}
      <p>A task observation does not establish input completion, tool approval or process cleanup.</p>
    </>}
  </section>;
}
