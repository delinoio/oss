import { items, object, text, type Document } from "./documents";

const names = ["read_file", "write", "ask_user_question", "enter_plan_mode", "exit_plan_mode"];
const phases = ["arguments", "declared", "described", "completed", "failed"];
const decimal = (v: unknown): v is string => typeof v === "string" && /^(0|[1-9][0-9]*)$/.test(v) && v.length <= 20 && BigInt(v) <= 18446744073709551615n;
const uuid = (v: unknown, version: number) => typeof v === "string" && new RegExp(`^[0-9a-f]{8}-[0-9a-f]{4}-${version}[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).test(v);

const bounded = (v: unknown, max: number): v is string => typeof v === "string" && !v.includes("\0") && !/[\uD800-\uDFFF]/u.test(v) && new TextEncoder().encode(v).length <= max;
const eventIndex = (v: unknown, thread: string) => { const suffix = text(v).slice(thread.length + 1); return uuid(thread, 7) && text(v).startsWith(`${thread}-`) && decimal(suffix) && BigInt(suffix) > 0n ? BigInt(suffix) : undefined; };
const snapshotValid = (s: Document, thread: string) => {
  const g = object(s.grok), m = object(g.metadata), args = object(g.arguments);
  if (s.kind !== "grok-native" || !names.includes(text(g.name)) || !phases.includes(text(g.phase)) || [s.command, s.read, s.shell, s.todo, s.builtin].some((v) => v != null) || items(s.changes).length || g.path !== undefined && !bounded(g.path, 8192) || [g.content, g.old, g.output].some((v) => v !== undefined && !bounded(v, 256 * 1024))) return false;
  if (g.phase === "arguments") return s.status === "pending" && g.metadata == null && Number.isSafeInteger(args.index) && Number(args.index) >= 0 && Number(args.index) < 128 && bounded(args.text, 256 * 1024) && (args.id == null ? args.name == null : bounded(args.id, 256) && args.id.length > 0 && args.name === g.name) && [g.path, g.content, g.output, g.old, g.plan_file, g.inherited_permission, g.questions].every((v) => v == null);
  if (g.arguments != null || eventIndex(m.event_id, thread) === undefined || ![m.context_tokens, m.timestamp_ms, m.stream_start_ms, m.turn_start_ms].every(decimal) || [m.timestamp_ms, m.stream_start_ms, m.turn_start_ms].some((v) => !decimal(v) || BigInt(v) > 253402300799999n)) return false;
  if (g.phase === "completed" ? s.status !== "completed" : g.phase === "failed" ? s.status !== "failed" || g.name !== "write" : s.status !== "pending") return false;
  if (g.plan_file != null) { const p = object(g.plan_file); if (!["read_file", "write"].includes(text(g.name)) || !bounded(p.entry_tool_id, 256) || !p.entry_tool_id || eventIndex(p.entry_event_id, thread) === undefined || !Number.isSafeInteger(p.revision) || Number(p.revision) < 0 || Number(p.revision) >= 128 || g.path !== undefined || g.inherited_permission != null) return false; }
  if (g.inherited_permission != null && (g.name !== "write" || !uuid(g.inherited_permission, 7))) return false;
  return true;
};
export function NativeGrokTool({ tool, state, thread }: { tool: Document; state: string; thread: string }) {
  const started = object(tool.started), states = items(tool.states), snapshots = [started, ...states.map((v) => object(object(v).snapshot)), ...(tool.completed != null ? [object(tool.completed)] : [])];
  let valid = snapshots.length <= 100000 && started.status === "pending" && object(started.grok).phase === "arguments" && [tool.output, tool.inputs, tool.patches].every((v) => v == null || Array.isArray(v) && v.length === 0) && (state === "complete" ? tool.completed != null : state === "streaming" && tool.completed == null);
  let sequence = 0;
  for (const v of states) { const n = object(v).sequence; if (!Number.isSafeInteger(n) || Number(n) <= sequence) valid = false; sequence = Number(n); }
  for (let i = 0; i < snapshots.length; i++) {
    const s = snapshots[i], g = object(s.grok);
    if (!snapshotValid(s, thread)) valid = false;
    if (i === 0) continue;
    const a = object(snapshots[i - 1].grok), from = text(a.phase), to = text(g.phase);
    if (a.name !== g.name || !(from === "arguments" && ["arguments", "declared"].includes(to) || from === "declared" && to === "described" || from === "described" && ["completed", "failed"].includes(to))) valid = false;
    if (from !== "arguments" && ["path", "content", "questions", "plan_file", "inherited_permission"].some((k) => JSON.stringify(a[k]) !== JSON.stringify(g[k]))) valid = false;
    if (a.metadata != null && g.metadata != null) { const before = eventIndex(object(a.metadata).event_id, thread), after = eventIndex(object(g.metadata).event_id, thread); if (before === undefined || after === undefined || after <= before) valid = false; }
  }
  const g = object(snapshots.at(-1)?.grok);
  if (!valid) return <p>The retained Grok tool is unavailable or inconsistent.</p>;
  return <details><summary>Grok {text(g.name)} · {text(g.phase)}</summary>
    {g.plan_file != null ? <p>Native Plan file · prior revision {Number(object(g.plan_file).revision)}</p> : text(g.path) ? <p>Path: <code>{text(g.path)}</code></p> : null}
    {g.inherited_permission != null ? <p>Uses the original remembered edit decision: {text(g.inherited_permission)}</p> : null}
    {typeof g.content === "string" ? <pre>{g.content}</pre> : null}
    {typeof g.old === "string" ? <details><summary>Original prior content</summary><pre>{g.old}</pre></details> : null}
    {typeof g.output === "string" ? <pre>{g.output}</pre> : null}
    <details><summary>Original native lifecycle</summary><ol>{snapshots.map((s, i) => { const v = object(s.grok); return <li key={i}>{text(v.phase)}{v.arguments != null ? <pre>{text(object(v.arguments).text)}</pre> : <p>Native event: {text(object(v.metadata).event_id)} · context: {text(object(v.metadata).context_tokens)}</p>}</li>; })}</ol></details>
    <p>Tool observations do not grant access or determine the execution outcome.</p>
  </details>;
}

export function NativeGrokProgress({ progress }: { progress: Document }) {
  if (progress.kind === "grok-mode") { const v = object(progress.grok_mode); return ["default", "plan"].includes(text(v.mode)) && decimal(v.timestamp_ms) ? <p>Native Grok mode changed to {text(v.mode)}.</p> : <p>Native Grok mode is unavailable.</p>; }
  const v = object(progress.grok_interaction);
  return ["permission-pending", "permission-resolved", "question-pending", "question-resolved", "plan-approval-pending", "plan-approval-resolved"].includes(text(v.stage)) && text(v.tool_id) ? <p>Grok {text(v.tool_id)} · {text(v.stage)}</p> : <p>Native Grok interaction observation is unavailable.</p>;
}

export function NativeGrokPublicTerminal({ progress }: { progress: Document }) {
  if (progress.grok_public_terminal == null) return null;
  const v = object(progress.grok_public_terminal), content = object(progress.grok_content), counts = object(v.counts);
  const valid = uuid(progress.execution_id, 7) && uuid(progress.native_thread_id, 7) && uuid(progress.native_turn_id, 4) && uuid(v.input_request_id, 7) && eventIndex(v.native_event_id, text(progress.native_thread_id)) !== undefined && ["succeeded", "stopped"].includes(text(v.outcome)) && v.outcome === progress.outcome && v.permission_rejected === (v.outcome === "stopped") && v.idle === true && v.cleanup_joined === true && ["default", "plan"].includes(text(v.mode)) && v.model === object(progress.observed).model && v.mode === (object(progress.grok_mode).mode ?? object(progress.observed).grok_mode) && [v.responses, v.timestamp_ms, v.total_tokens, v.api_duration_ms, v.turns, ...["input_tokens", "output_tokens", "cache_read_input_tokens", "cache_creation_input_tokens", "reasoning_tokens"].map((k) => counts[k])].every(decimal) && decimal(v.responses) && BigInt(v.responses) > 0n && BigInt(v.responses) <= 100000n && Number.isSafeInteger(content.responses) && BigInt(v.responses) === BigInt(Number(content.responses)) && Number.isSafeInteger(v.text_chunks) && Number(v.text_chunks) >= 0 && Number(v.text_chunks) <= 100000 && BigInt(text(v.timestamp_ms)) <= 253402300799999n && content.message_id == null && [v.input_digest, v.output_digest, v.native_facts_digest].every((v) => /^[a-f0-9]{64}$/.test(text(v))) && [progress.grok_terminal, progress.grok_stop, progress.claude_terminal, progress.claude_stop, progress.claude_denial, progress.opencode_stop].every((v) => v == null);
  if (!valid) return <p>The retained Grok interaction completion is unavailable or inconsistent.</p>;
  return <details><summary>Original Grok interaction completion</summary><dl><dt>Native outcome</dt><dd>{v.permission_rejected ? "Permission rejected" : "End turn"}</dd><dt>Final native mode</dt><dd>{text(v.mode)}</dd><dt>Original responses</dt><dd>{text(v.responses)}</dd><dt>Reported input total tokens</dt><dd>{text(v.total_tokens)}</dd><dt>Native API duration (ms)</dt><dd>{text(v.api_duration_ms)}</dd><dt>Workspace cleanup</dt><dd>{progress.cleanup_verified === true ? "Verified" : "Awaiting original Worker report"}</dd></dl><p>Totals overlap individual response usage. The owned native process joined; continuation and closed-history restoration remain unavailable.</p></details>;
}
