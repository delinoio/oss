import { object } from "./documents";

enum ReadStatus {
  Pending = "pending",
  Running = "running",
  Completed = "completed",
  Failed = "failed",
}

type ReadSnapshot = {
  status: ReadStatus;
  call: string;
  input: { path?: string; offset?: number; limit?: number };
  raw?: string;
  title?: string;
  output?: string;
  error?: string;
  start?: number;
  metadata: Record<string, unknown>;
};

function boundedText(value: unknown): value is string {
  return typeof value === "string" && value.length <= 256 * 1024 && !value.includes("\0") && !/[\uD800-\uDFFF]/u.test(value) && new TextEncoder().encode(value).length <= 256 * 1024;
}

function count(value: unknown): value is number {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= 0;
}

function readSnapshot(value: unknown): ReadSnapshot | undefined {
  const snapshot = object(value);
  const read = object(snapshot.read);
  const input = object(read.input);
  const timing = object(read.time);
  const status = snapshot.status as ReadStatus;
  if (snapshot.kind !== "opencode-read" || snapshot.command != null || snapshot.shell != null || snapshot.changes != null || !Object.values(ReadStatus).includes(status) || !boundedText(read.call_id) || !read.call_id || read.provider_executed === true) return;
  for (const field of [input.filePath, read.raw, read.title, read.output, read.error]) if (field != null && !boundedText(field)) return;
  for (const field of [input.offset, input.limit, timing.start, timing.end]) if (field != null && !count(field)) return;
  if (timing.end != null && (!count(timing.start) || !count(timing.end) || timing.end < timing.start)) return;
  if (status === ReadStatus.Pending) {
    if (!boundedText(read.raw) || read.time != null || read.title != null || read.output != null || read.error != null || read.metadata != null) return;
  } else {
    if (read.raw != null || !count(timing.start)) return;
    if (status === ReadStatus.Running && (!boundedText(input.filePath) || timing.end != null || read.output != null || read.error != null)) return;
    if (status === ReadStatus.Completed && (!boundedText(input.filePath) || !count(timing.end) || !boundedText(read.title) || !boundedText(read.output) || read.error != null)) return;
    if (status === ReadStatus.Failed && (!count(timing.end) || !boundedText(read.error) || read.title != null || read.output != null)) return;
  }
  const metadata = object(read.metadata);
  if (status === ReadStatus.Completed && (!boundedText(metadata.preview) || typeof metadata.truncated !== "boolean" || !Array.isArray(metadata.loaded))) return;
  if (metadata.preview != null && !boundedText(metadata.preview) || metadata.truncated != null && typeof metadata.truncated !== "boolean" || metadata.interrupted != null && typeof metadata.interrupted !== "boolean") return;
  if (metadata.loaded != null && (!Array.isArray(metadata.loaded) || metadata.loaded.length > 1024 || !metadata.loaded.every(boundedText))) return;
  return {
    status, call: read.call_id,
    input: { path: input.filePath as string | undefined, offset: input.offset as number | undefined, limit: input.limit as number | undefined },
    raw: read.raw as string | undefined, title: read.title as string | undefined, output: read.output as string | undefined, error: read.error as string | undefined,
    start: timing.start as number | undefined, metadata,
  };
}

function retainedRead(tool: Record<string, unknown>, state: string): ReadSnapshot[] | undefined {
  const first = readSnapshot(tool.started);
  if (!first || first.status !== ReadStatus.Pending || tool.output != null || tool.inputs != null || tool.patches != null) return;
  const states = tool.states ?? [];
  if (!Array.isArray(states) || states.length > 1024) return;
  const snapshots = [first];
  let sequence = 0;
  for (const value of states) {
    const entry = object(value);
    const next = readSnapshot(entry.snapshot);
    if (!count(entry.sequence) || entry.sequence <= sequence || entry.sequence > 100000 || !next || next.status !== ReadStatus.Pending && next.status !== ReadStatus.Running) return;
    sequence = entry.sequence;
    snapshots.push(next);
  }
  if (state === "complete") {
    const completed = readSnapshot(tool.completed);
    if (!completed || completed.status !== ReadStatus.Completed && completed.status !== ReadStatus.Failed) return;
    snapshots.push(completed);
  } else if (state !== "streaming" || tool.completed != null) return;
  for (let i = 1; i < snapshots.length; i++) {
    const prior = snapshots[i - 1]!;
    const next = snapshots[i]!;
    if (prior.call !== next.call) return;
    if (prior.status === ReadStatus.Pending) {
      if (next.status === ReadStatus.Completed) return;
    } else if (prior.status !== ReadStatus.Running || next.status === ReadStatus.Pending || prior.start !== next.start || prior.input.path !== next.input.path || prior.input.offset !== next.input.offset || prior.input.limit !== next.input.limit) return;
  }
  return snapshots;
}

const labels: Record<ReadStatus, string> = { pending: "Pending", running: "Running", completed: "Completed", failed: "Failed" };

function Arguments({ input }: { input: ReadSnapshot["input"] }) {
  return <dl>
    {input.path !== undefined ? <><dt>Requested path</dt><dd><pre>{input.path}</pre></dd></> : null}
    {input.offset !== undefined ? <><dt>Requested offset</dt><dd>{input.offset}</dd></> : null}
    {input.limit !== undefined ? <><dt>Requested limit</dt><dd>{input.limit}</dd></> : null}
  </dl>;
}

// Render only retained content. A path is not a link or filesystem capability,
// and a tool error does not become an assistant answer or execution outcome.
export function NativeRead({ tool, state }: { tool: Record<string, unknown>; state: string }) {
  const snapshots = retainedRead(tool, state);
  const latest = snapshots?.at(-1);
  if (!snapshots || !latest) return <details><summary>Read · Unavailable</summary><p>The retained Read operation is unavailable or inconsistent.</p></details>;
  const first = snapshots[0]!;
  return <details>
    <summary>Read · {labels[latest.status]}</summary>
    <Arguments input={latest.input} />
    {latest.title !== undefined ? <p>{latest.title}</p> : null}
    {latest.output !== undefined ? <section aria-label="Read result"><pre>{latest.output}</pre></section> : null}
    {latest.error !== undefined ? <section aria-label="Read error"><pre>{latest.error}</pre></section> : null}
    {typeof latest.metadata.truncated === "boolean" ? <p>{latest.metadata.truncated ? "The native Read result is truncated." : "The native Read result is not truncated."}</p> : null}
    {typeof latest.metadata.interrupted === "boolean" ? <p>Native interruption: {latest.metadata.interrupted ? "Observed" : "Not observed"}</p> : null}
    <details><summary>Original proposal and observations</summary>
      <Arguments input={first.input} />
      <pre>{first.raw}</pre>
      <ol>{snapshots.map((snapshot, index) => <li key={index}>{labels[snapshot.status]}{snapshot.title !== undefined ? ` · ${snapshot.title}` : ""}</li>)}</ol>
      {typeof latest.metadata.preview === "string" ? <details><summary>Native preview</summary><pre>{latest.metadata.preview}</pre></details> : null}
      {Array.isArray(latest.metadata.loaded) ? <><p>Loaded instruction files: {latest.metadata.loaded.length}</p><ul>{latest.metadata.loaded.map((path, index) => <li key={index}><pre>{path as string}</pre></li>)}</ul></> : null}
    </details>
  </details>;
}
