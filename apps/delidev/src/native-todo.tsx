import { object } from "./documents";

enum ToolState { Pending = "pending", Running = "running", Completed = "completed", Failed = "failed" }
enum TodoStatus { Pending = "pending", Running = "in_progress", Completed = "completed", Cancelled = "cancelled" }
type Todo = { content: string; status: string; priority: string };
type Snapshot = {
  status: ToolState; call: string; todos?: Todo[]; start?: number; providerExecuted?: boolean;
  raw?: string; title?: string; output?: string; error?: string;
  result?: Todo[]; truncated?: boolean; outputPath?: string; interrupted?: boolean;
};
const encoder = new TextEncoder();
function bounded(value: unknown): value is string {
  return typeof value === "string" && value.length <= 256 * 1024 && !value.includes("\0") && !/[\uD800-\uDFFF]/u.test(value) && encoder.encode(value).length <= 256 * 1024;
}
function count(value: unknown): value is number { return typeof value === "number" && Number.isSafeInteger(value) && value >= 0; }
function shape(value: unknown, names: string[]): boolean {
  return value !== null && typeof value === "object" && !Array.isArray(value) && Object.keys(value).every((key) => names.includes(key));
}
function list(value: unknown): value is Todo[] {
  return Array.isArray(value) && value.length <= 1024 && value.every((item) => shape(item, ["content", "status", "priority"]) && bounded(item.content) && bounded(item.status) && bounded(item.priority));
}
function same(a: Todo[] | undefined, b: Todo[] | undefined): boolean {
  return a === b || a !== undefined && b !== undefined && a.length === b.length && a.every((t, i) => t.content === b[i]!.content && t.status === b[i]!.status && t.priority === b[i]!.priority);
}
function snapshot(value: unknown): Snapshot | undefined {
  const s = object(value), todo = object(s.todo), input = object(todo.input), time = object(todo.time), metadata = object(todo.metadata);
  const status = s.status as ToolState;
  if (s.kind !== "opencode-todo" || s.read != null || s.shell != null || s.command != null || s.changes != null || !Object.values(ToolState).includes(status) || !bounded(todo.call_id) || !todo.call_id.trim() || encoder.encode(todo.call_id).length > 1024 || todo.provider_executed != null && todo.provider_executed !== false) return;
  if (!shape(todo, ["call_id", "input", "raw", "title", "output", "error", "metadata", "time", "provider_executed"]) || !shape(todo.input, ["todos"]) || todo.time != null && !shape(todo.time, ["start", "end"]) || todo.metadata != null && !shape(todo.metadata, ["todos", "truncated", "outputPath", "interrupted"])) return;
  if ("todos" in input && !list(input.todos) || "todos" in metadata && !list(metadata.todos)) return;
  for (const v of [todo.raw, todo.title, todo.output, todo.error, metadata.outputPath]) if (v != null && !bounded(v)) return;
  for (const v of [metadata.truncated, metadata.interrupted]) if (v != null && typeof v !== "boolean") return;
  if (metadata.outputPath != null && metadata.truncated !== true) return;
  if (todo.time != null && (!count(time.start) || time.end != null && (!count(time.end) || time.end < time.start))) return;
  if (status === ToolState.Pending) {
    if (!bounded(todo.raw) || todo.time != null || todo.metadata != null || todo.title != null || todo.output != null || todo.error != null) return;
  } else {
    if (todo.raw != null || !count(time.start)) return;
    if (status === ToolState.Running && (!list(input.todos) || time.end != null || todo.output != null || todo.error != null)) return;
    if (status === ToolState.Completed && (!list(input.todos) || !list(metadata.todos) || !same(input.todos, metadata.todos) || !count(time.end) || !bounded(todo.title) || !bounded(todo.output) || todo.error != null || typeof metadata.truncated !== "boolean")) return;
    if (status === ToolState.Failed && (!count(time.end) || !bounded(todo.error) || todo.title != null || todo.output != null)) return;
  }
  return { status, call: todo.call_id, todos: input.todos as Todo[] | undefined, start: time.start as number | undefined, providerExecuted: todo.provider_executed as boolean | undefined,
    raw: todo.raw as string | undefined, title: todo.title as string | undefined, output: todo.output as string | undefined, error: todo.error as string | undefined,
    result: metadata.todos as Todo[] | undefined, truncated: metadata.truncated as boolean | undefined, outputPath: metadata.outputPath as string | undefined, interrupted: metadata.interrupted as boolean | undefined };
}
function retained(tool: Record<string, unknown>, state: string): Snapshot[] | undefined {
  const first = snapshot(tool.started), states = tool.states ?? [];
  if (!first || first.status !== ToolState.Pending || tool.output != null || tool.inputs != null || tool.patches != null || !Array.isArray(states) || states.length > 1024) return;
  const snapshots = [first];
  let sequence = 0;
  for (const value of states) {
    const entry = object(value), next = snapshot(entry.snapshot);
    if (!count(entry.sequence) || entry.sequence <= sequence || entry.sequence > 100000 || !next || next.status !== ToolState.Pending && next.status !== ToolState.Running) return;
    sequence = entry.sequence;
    snapshots.push(next);
  }
  if (state === "complete") {
    const end = snapshot(tool.completed);
    if (!end || end.status !== ToolState.Completed && end.status !== ToolState.Failed) return;
    snapshots.push(end);
  } else if (state !== "streaming" || tool.completed != null) return;
  for (let i = 1; i < snapshots.length; i++) {
    const prior = snapshots[i - 1]!, next = snapshots[i]!;
    if (prior.call !== next.call || prior.providerExecuted !== next.providerExecuted) return;
    if (prior.status === ToolState.Pending) { if (next.status === ToolState.Completed) return; }
    else if (prior.status !== ToolState.Running || next.status === ToolState.Pending || prior.start !== next.start || !same(prior.todos, next.todos)) return;
  }
  return snapshots;
}
const statusLabels: Record<TodoStatus, string> = { pending: "Pending", in_progress: "In progress", completed: "Completed", cancelled: "Cancelled" };
function TodoList({ todos }: { todos: Todo[] }) {
  if (todos.length === 0) return <p>The native list is empty.</p>;
  return <ol>{todos.map((todo, index) => <li key={index}><pre>{todo.content}</pre><p>Status: {Object.hasOwn(statusLabels, todo.status) ? statusLabels[todo.status as TodoStatus] : todo.status} · Priority: {todo.priority}</p></li>)}</ol>;
}

// These are independent native session observations, never editable checkboxes
// or a synthesized completion percentage. Unknown string values stay visible.
export function NativeTodoProgress({ progress, state }: { progress: Record<string, unknown>; state: string }) {
  const todo = object(progress.todo);
  if (state !== "complete" || progress.kind !== "opencode-todo" || progress.plan != null || progress.diff != null || !shape(progress.todo, ["native_event_id", "todos"]) || typeof todo.native_event_id !== "string" || !/^evt_[0-9a-f]{12}[a-zA-Z0-9]{14}$/.test(todo.native_event_id) || !list(todo.todos)) return <details><summary>Todo progress · Unavailable</summary><p>The retained Todo progress is unavailable or inconsistent.</p></details>;
  return <details open><summary>Todo progress</summary><TodoList todos={todo.todos} /><p>This list is an original native observation; it does not establish session completion.</p></details>;
}

export function NativeTodo({ tool, state }: { tool: Record<string, unknown>; state: string }) {
  const snapshots = retained(tool, state), latest = snapshots?.at(-1);
  if (!snapshots || !latest) return <details><summary>Todo update · Unavailable</summary><p>The retained Todo operation is unavailable or inconsistent.</p></details>;
  return <details><summary>Todo update · {latest.status}</summary>
    {latest.todos !== undefined ? <TodoList todos={latest.todos} /> : <p>The native proposal has no applied list yet.</p>}
    {latest.title !== undefined ? <p>{latest.title}</p> : null}
    {latest.output !== undefined ? <details><summary>Original native result</summary><pre>{latest.output}</pre></details> : null}
    {latest.error !== undefined ? <section aria-label="Todo error"><pre>{latest.error}</pre></section> : null}
    {latest.truncated !== undefined ? <p>{latest.truncated ? "The native result is truncated." : "The native result is not truncated."}</p> : null}
    {latest.outputPath !== undefined ? <p>Native saved output path: <code>{latest.outputPath}</code></p> : null}
    {latest.interrupted !== undefined ? <p>Native interruption: {latest.interrupted ? "Observed" : "Not observed"}</p> : null}
    <details><summary>Original proposal and observations</summary><ol>{snapshots.map((s, i) => <li key={i}><details><summary>{s.status}</summary>{s.todos !== undefined ? <TodoList todos={s.todos} /> : null}{s.raw !== undefined ? <pre>{s.raw}</pre> : null}{s.result !== undefined ? <section aria-label="Observed native list"><TodoList todos={s.result} /></section> : null}</details></li>)}</ol></details>
  </details>;
}
