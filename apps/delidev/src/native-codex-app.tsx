// SPDX-License-Identifier: Apache-2.0
import { object } from "./documents";
import { Disclosure, DisclosureSummary } from "./disclosure";
import { copy, useLocale } from "./localization";
import { statusLabel } from "./product-status";

const uuid = (v: unknown) => typeof v === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(v);
const closed = (v: Record<string, unknown>, keys: string[]) => Object.keys(v).every(k => keys.includes(k));
const bounded = (v: unknown, max = 256 << 10, required = false) => typeof v === "string" && (!required || Boolean(v.trim())) && new TextEncoder().encode(v).length <= max && !v.includes("\0");
const jsonBound = (v: unknown) => { if (v === undefined) return false; try { return new TextEncoder().encode(JSON.stringify(v)).length <= 256 << 10; } catch { return false; } };
function snapshot(value: unknown) {
  const s = object(value), a = object(s.codex_app), i = object(a.identity);
  if (!closed(s, ["kind", "status", "codex_app", "changes"]) || s.changes != null || s.kind !== "codex-app" || !["running", "completed", "failed"].includes(String(s.status)) || !closed(a, ["identity", "result", "error_present", "duration_ms", "progress"]) || !closed(i, ["account_id", "configuration_generation", "app_id", "link_id", "tool", "arguments", "app_name", "action_name", "read_only_hint"]) || !uuid(i.account_id) || !uuid(i.configuration_generation) || !bounded(i.app_id, 1024, true) || !bounded(i.tool, 1024, true) || !jsonBound(i.arguments) || typeof a.error_present !== "boolean") return;
  for (const v of [i.link_id, i.app_name, i.action_name]) if (v !== null && !bounded(v, 4096)) return;
  if (i.read_only_hint !== null && typeof i.read_only_hint !== "boolean" || a.duration_ms !== null && (!Number.isSafeInteger(a.duration_ms) || Number(a.duration_ms) < 0) || a.progress !== null && !bounded(a.progress)) return;
  const r = a.result === null ? undefined : object(a.result);
  if (r && (!closed(r, ["content", "structured_content"]) || !Array.isArray(r.content) || r.content.length > 1024 || !r.content.every(jsonBound) || !jsonBound(r.structured_content))) return;
  if (s.status === "running" && (r || a.error_present || a.duration_ms !== null) || s.status === "completed" && (!r || a.error_present)) return;
  return { status: String(s.status), identity: i, result: r, progress: a.progress as string | null, error: a.error_present as boolean };
}

export function codexAppTranscript(tool: Record<string, unknown>, state: string) {
  const first = snapshot(tool.started), states = tool.states ?? [];
  if (!first || first.status !== "running" || tool.output != null || tool.inputs != null || tool.patches != null || !Array.isArray(states) || states.length > 1024) return;
  const values = [first]; let previous = 0;
  for (const raw of states) {
    const s = object(raw), next = snapshot(s.snapshot);
    if (!closed(s, ["sequence", "snapshot"]) || !Number.isSafeInteger(s.sequence) || Number(s.sequence) <= previous || Number(s.sequence) > 100000 || !next || next.status !== "running" || JSON.stringify(next.identity) !== JSON.stringify(first.identity)) return;
    previous = Number(s.sequence); values.push(next);
  }
  if (tool.completed != null) {
    const last = snapshot(tool.completed);
    if (!last || last.status === "running" || JSON.stringify(last.identity) !== JSON.stringify(first.identity) || state !== "complete") return;
    values.push(last);
  } else if (state === "complete") return;
  return values;
}

export function NativeCodexApp({ tool, state }: { tool: Record<string, unknown>; state: string }) {
  useLocale();
  const values = codexAppTranscript(tool, state);
  if (!values) return <section role="status">{copy("native-builtin.theRetainedNativeOperationIsUnavailable_ed146e")}</section>;
  const first = values[0]!, latest = values[values.length - 1]!;
  return <Disclosure><DisclosureSummary>{String(first.identity.app_name || first.identity.app_id)} · {String(first.identity.tool)} · {statusLabel(latest.status)}</DisclosureSummary>
    <Disclosure><DisclosureSummary>{copy("native-builtin.originalNativeMetadata_d7e8f9")}</DisclosureSummary><pre>{JSON.stringify(first.identity, null, 2)}</pre></Disclosure>
    <Disclosure><DisclosureSummary>{copy("native-builtin.originalNativeInput_b3b718")}</DisclosureSummary><pre>{JSON.stringify(first.identity.arguments, null, 2)}</pre></Disclosure>
    {values.map((value, n) => value.progress !== null ? <pre key={n}>{value.progress}</pre> : null)}
    {latest.result ? <Disclosure><DisclosureSummary>{copy("native-builtin.nativeToolResult_1cf5a0")}</DisclosureSummary><pre>{JSON.stringify(latest.result, null, 2)}</pre></Disclosure> : null}
    {latest.error ? <p>{copy("native-builtin.nativeToolError_440d26")}</p> : null}
  </Disclosure>;
}
