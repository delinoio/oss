import { object } from "./documents";

enum ShellStatus { Pending = "pending", Running = "running", Completed = "completed", Failed = "failed" }
type ShellInput = { command?: string; workdir?: string; timeout?: number };
type ShellSnapshot = {
  status: ShellStatus; call: string; input: ShellInput; start?: number;
  raw?: string; title?: string; output?: string; error?: string;
  preview?: string; exit?: number | null; truncated?: boolean; outputPath?: string; interrupted?: boolean; providerExecuted?: boolean;
};
const encoder = new TextEncoder();
function bounded(value: unknown): value is string {
  return typeof value === "string" && value.length <= 256 * 1024 && !value.includes("\0") && !/[\uD800-\uDFFF]/u.test(value) && encoder.encode(value).length <= 256 * 1024;
}
function count(value: unknown): value is number { return typeof value === "number" && Number.isSafeInteger(value) && value >= 0; }
function allowed(value: Record<string, unknown>, names: string[]): boolean { return Object.keys(value).every((key) => names.includes(key)); }

function snapshot(value: unknown): ShellSnapshot | undefined {
  const s = object(value), shell = object(s.shell), input = object(shell.input), timing = object(shell.time), metadata = object(shell.metadata);
  const status = s.status as ShellStatus;
  if (s.kind !== "opencode-shell" || s.read != null || s.command != null || s.changes != null || !Object.values(ShellStatus).includes(status) || !bounded(shell.call_id) || !shell.call_id || encoder.encode(shell.call_id).length > 1024 || shell.provider_executed != null && shell.provider_executed !== false) return;
  if (!allowed(shell, ["call_id", "input", "raw", "title", "output", "error", "metadata", "time", "provider_executed"]) || !allowed(input, ["command", "workdir", "timeout"]) || !allowed(timing, ["start", "end"]) || !allowed(metadata, ["output", "exit", "exit_observed", "truncated", "outputPath", "interrupted"])) return;
  for (const field of [input.command, input.workdir, shell.raw, shell.title, shell.output, shell.error, metadata.output, metadata.outputPath]) if (field != null && !bounded(field)) return;
  if (input.timeout != null && (!count(input.timeout) || input.timeout === 0)) return;
  for (const field of [timing.start, timing.end]) if (field != null && !count(field)) return;
  if (timing.end != null && (!count(timing.start) || !count(timing.end) || timing.end < timing.start)) return;
  if (shell.metadata != null && (typeof metadata.exit_observed !== "boolean" || metadata.exit_observed && metadata.exit !== null && (typeof metadata.exit !== "number" || !Number.isSafeInteger(metadata.exit)) || !metadata.exit_observed && metadata.exit != null)) return;
  for (const field of [metadata.truncated, metadata.interrupted]) if (field != null && typeof field !== "boolean") return;
  if (metadata.outputPath != null && metadata.truncated !== true) return;
  if (status === ShellStatus.Pending) {
    if (!bounded(shell.raw) || shell.time != null || shell.metadata != null || shell.title != null || shell.output != null || shell.error != null) return;
  } else {
    if (shell.raw != null || !count(timing.start)) return;
    if (status === ShellStatus.Running && (!bounded(input.command) || timing.end != null || shell.output != null || shell.error != null)) return;
    if (status === ShellStatus.Completed && (!bounded(input.command) || !count(timing.end) || !bounded(shell.title) || !bounded(shell.output) || shell.error != null || !bounded(metadata.output) || typeof metadata.truncated !== "boolean" || metadata.exit_observed !== true)) return;
    if (status === ShellStatus.Failed && (!count(timing.end) || !bounded(shell.error) || shell.title != null || shell.output != null)) return;
  }
  return {
    status, call: shell.call_id, input: input as ShellInput, start: timing.start as number | undefined,
    raw: shell.raw as string | undefined, title: shell.title as string | undefined, output: shell.output as string | undefined, error: shell.error as string | undefined,
    preview: metadata.output as string | undefined, exit: metadata.exit_observed ? metadata.exit as number | null : undefined,
    truncated: metadata.truncated as boolean | undefined, outputPath: metadata.outputPath as string | undefined, interrupted: metadata.interrupted as boolean | undefined,
    providerExecuted: shell.provider_executed as boolean | undefined,
  };
}

function retained(tool: Record<string, unknown>, state: string): ShellSnapshot[] | undefined {
  const first = snapshot(tool.started);
  if (!first || first.status !== ShellStatus.Pending || tool.output != null || tool.inputs != null || tool.patches != null) return;
  const states = tool.states ?? [];
  if (!Array.isArray(states) || states.length > 1024) return;
  const snapshots = [first];
  let sequence = 0;
  for (const value of states) {
    const entry = object(value), next = snapshot(entry.snapshot);
    if (!count(entry.sequence) || entry.sequence <= sequence || entry.sequence > 100000 || !next || next.status !== ShellStatus.Pending && next.status !== ShellStatus.Running) return;
    sequence = entry.sequence;
    snapshots.push(next);
  }
  if (state === "complete") {
    const completed = snapshot(tool.completed);
    if (!completed || completed.status !== ShellStatus.Completed && completed.status !== ShellStatus.Failed) return;
    snapshots.push(completed);
  } else if (state !== "streaming" || tool.completed != null) return;
  for (let i = 1; i < snapshots.length; i++) {
    const prior = snapshots[i - 1]!, next = snapshots[i]!;
    if (prior.call !== next.call || prior.providerExecuted !== next.providerExecuted) return;
    if (prior.status === ShellStatus.Pending) {
      if (next.status === ShellStatus.Completed) return;
    } else if (prior.status !== ShellStatus.Running || next.status === ShellStatus.Pending || prior.start !== next.start || prior.input.command !== next.input.command || prior.input.workdir !== next.input.workdir || prior.input.timeout !== next.input.timeout) return;
  }
  return snapshots;
}

const labels: Record<ShellStatus, string> = { pending: "Pending", running: "Running", completed: "Completed", failed: "Failed" };
function Arguments({ input }: { input: ShellInput }) {
  return <dl>
    {input.command !== undefined ? <><dt>Command</dt><dd><pre>{input.command}</pre></dd></> : null}
    {input.workdir !== undefined ? <><dt>Requested directory</dt><dd><pre>{input.workdir}</pre></dd></> : null}
    {input.timeout !== undefined ? <><dt>Requested timeout (ms)</dt><dd>{input.timeout}</dd></> : null}
  </dl>;
}

// Native previews may be replaced or clipped. Render them as observations,
// never concatenate them into invented output or infer success from completion.
export function NativeShell({ tool, state }: { tool: Record<string, unknown>; state: string }) {
  const snapshots = retained(tool, state), latest = snapshots?.at(-1);
  if (!snapshots || !latest) return <details><summary>Shell · Unavailable</summary><p>The retained Shell operation is unavailable or inconsistent.</p></details>;
  return <details>
    <summary>Shell · {labels[latest.status]}</summary>
    <Arguments input={latest.input} />
    {latest.title !== undefined ? <p>{latest.title}</p> : null}
    {latest.exit !== undefined ? <p>Native exit code: {latest.exit === null ? "Unavailable" : latest.exit}</p> : null}
    {latest.output !== undefined ? <section aria-label="Shell result"><pre>{latest.output}</pre></section> : null}
    {latest.error !== undefined ? <section aria-label="Shell error"><pre>{latest.error}</pre></section> : null}
    {latest.preview !== undefined ? <details><summary>Latest native output preview</summary><pre>{latest.preview}</pre></details> : null}
    {latest.truncated !== undefined ? <p>{latest.truncated ? "The native Shell result is truncated." : "The native Shell result is not truncated."}</p> : null}
    {latest.outputPath !== undefined ? <p>Native saved output path:<br /><code>{latest.outputPath}</code></p> : null}
    {latest.interrupted !== undefined ? <p>Native interruption: {latest.interrupted ? "Observed" : "Not observed"}</p> : null}
    <details><summary>Original proposal and observations</summary><ol>{snapshots.map((item, index) => <li key={index}><details>
      <summary>{labels[item.status]}</summary><Arguments input={item.input} />
      {item.raw !== undefined ? <pre>{item.raw}</pre> : null}
      {item.preview !== undefined ? <pre>{item.preview}</pre> : null}
    </details></li>)}</ol></details>
  </details>;
}
