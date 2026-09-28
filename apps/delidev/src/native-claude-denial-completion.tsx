import { object, type Document } from "./documents";

const nativeID = (v: unknown): v is string => typeof v === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-[47][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(v);
const id = (v: unknown) => nativeID(v) && v[14] === "7";
const exact = (v: Document, fields: string[]) => Object.keys(v).length === fields.length && fields.every((key) => Object.hasOwn(v, key));

function valid(progress: Document) {
  const v = object(progress.claude_denial), prior = object(progress.claude_interruption);
  const references = [v.input_id, v.interaction_id, v.arrival_id, v.context_id, v.result_id, progress.execution_id, progress.native_thread_id];
  const envelopes = [v.command_native_id, v.idle_native_id, progress.native_turn_id];
  return exact(v, ["input_id", "interaction_id", "arrival_id", "context_id", "result_id", "command_native_id", "idle_native_id", "native_input_id", "cleanup_verified"]) && exact(prior, ["interaction_id", "context_id", "result_id"]) && references.every(id) && envelopes.every(nativeID) && new Set([...references, ...envelopes]).size === references.length + envelopes.length && v.input_id === progress.input_id && v.interaction_id === prior.interaction_id && v.context_id === prior.context_id && v.result_id === prior.result_id && v.native_input_id === null && v.cleanup_verified === true && progress.outcome === "stopped" && progress.claude_terminal == null && progress.claude_stop == null && (progress.cleanup_verified === undefined || typeof progress.cleanup_verified === "boolean");
}

export function NativeClaudeDenialCompletion({ progress }: { progress: Document }) {
  if (progress.claude_denial == null) return null;
  if (!valid(progress)) return <p>The retained Claude denial cleanup is unavailable or inconsistent.</p>;
  return <section aria-label="Claude original denial cleanup">
    <h4>Claude stopped after denial</h4>
    <p>The original denial was processed and its command was cancelled. Claude reported a session result without an input result identity.</p>
    <dl><dt>Native loop</dt><dd>Idle observed</dd><dt>Owned native process cleanup</dt><dd>Confirmed</dd>
      <dt>Worker workspace cleanup report</dt><dd>{progress.cleanup_verified === true ? "Confirmed" : "Not confirmed"}</dd></dl>
    <p>History reconciliation is still required before further input. This cleanup does not grant Resume.</p>
  </section>;
}
