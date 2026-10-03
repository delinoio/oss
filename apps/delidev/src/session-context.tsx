// SPDX-License-Identifier: Apache-2.0
import { useQuery } from "@connectrpc/connect-query";
import { EntityKind, SessionContextCapability, SessionQuery, SystemCapability, SystemQuery, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, object, text, type Document } from "./documents";
import { JobState } from "./jobs";
import { useRetainedMutation } from "./mutation";
import { NativeContextCompaction } from "./native-context-compaction";
import { Problem } from "./ui";

export function contextDocument(bytes: Uint8Array | undefined, session: string): Document | undefined {
  if (!bytes || bytes.byteLength > 1 << 20) return;
  try {
    const value = object(JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(bytes)));
    if (value.session_id !== session || typeof value.session_revision !== "string" || !/^[1-9][0-9]*$/.test(value.session_revision)) return;
    return value;
  } catch { return; }
}

export function SessionContext({ session }: { session: Resource }) {
  const status = useQuery(SystemQuery.getStatus, {});
  const supported = Boolean(status.data?.capabilities.includes(SystemCapability.NATIVE_SESSION_COMPACTION_V1));
  const context = useQuery(SessionQuery.getSessionContext, { sessionId: session.id }, { enabled: supported, refetchInterval: 5000 });
  const view = contextDocument(context.data?.documentJson, session.id);
  const manual = object(object(view?.manual_action).document);
  const state = text(manual.state);
  const pending = Boolean(state && ![JobState.Succeeded, JobState.Failed, JobState.Canceled].includes(state as JobState));
  const mutation = useRetainedMutation(`compact:${session.id}`, SessionQuery.compactSession, () => { void context.refetch(); }, (reply, request) => {
    const input = object(document(reply.job).input);
    return reply.requestId === request.mutation?.requestId && reply.job?.kind === EntityKind.JOB && reply.job.sessionId === session.id && input.action_id === request.mutation.requestId && object(input.assignment).session_id === session.id;
  });
  const harness = text(object(object(document(session).initial_execution).configuration).harness);
  const capability = harness === "codex" ? SessionContextCapability.CODEX_MANUAL_COMPACTION_V1 : harness === "claude-code" ? SessionContextCapability.CLAUDE_MANUAL_COMPACTION_V1 : harness === "opencode" ? SessionContextCapability.OPENCODE_MANUAL_COMPACTION_V1 : undefined;
  const eligible = supported && (harness !== "codex" || status.data?.capabilities.includes(SystemCapability.CODEX_SESSION_COMPACTION_V1)) && (harness !== "opencode" || status.data?.capabilities.includes(SystemCapability.OPENCODE_SESSION_COMPACTION_V1)) && capability !== undefined && context.data?.capabilities.includes(capability) && view?.session_revision === session.revision.toString() && !context.error && !context.isFetching;
  const result = object(manual.result), codex = object(result.codex), opencode = object(result.opencode), problem = object(manual.problem);
  if (!supported) return status.isLoading ? null : <section aria-label="Session context"><h3>Context</h3><p>Update the server and original Worker to use native context compaction.</p><Problem error={status.error} /></section>;
  return <section aria-label="Session context" className="session-context"><header><h3>Context</h3><button disabled={context.isFetching} onClick={() => void context.refetch()}>Refresh context</button></header>
    <p>Current context tokens: {view?.current_tokens === null || view?.current_tokens === undefined ? "Not reported" : text(view.current_tokens)}</p>
    {(harness === "codex" || harness === "opencode") && view?.automatic_boundary ? <NativeContextCompaction progress={object(object(object(view.automatic_boundary).document).progress)} state="complete" /> : null}
    {state ? <section aria-label="Compaction operation"><p role="status">Compaction operation: {state}</p><small>{text(manual.action_id)}</small>
      {state === JobState.Succeeded && result.compact_result === "success" ? <p>The native context was compacted and its history and cleanup were verified.</p> : null}
      {result.harness === "codex" && typeof codex.actions === "number" ? <p>Retained compactions: {codex.actions}. Response counters: {Array.isArray(codex.response_usages) && codex.response_usages.length > 0 ? "Reported" : "Not reported by the native process"}.</p> : null}
      {result.harness === "opencode" && typeof opencode.actions === "number" ? <p>Retained compactions: {opencode.actions}. Native step counters: {Array.isArray(opencode.usages) && opencode.usages.length > 0 ? "Reported" : "Not reported by the native process"}.</p> : null}
      {text(problem.message) ? <p role="alert">{text(problem.message)} {text(problem.guidance)}</p> : null}
      {state === JobState.Uncertain ? <p role="alert">This action requires reconciliation. Preserve the original operation; a new request cannot resolve it.</p> : null}
    </section> : null}
    <p>Compact the native working context at a completed boundary. The retained conversation and pending input remain available.</p>
    <button disabled={!eligible || pending || mutation.busy || mutation.uncertain} onClick={() => void mutation.send({ mutation: { id: session.id, expectedRevision: session.revision, requestId: newRequestId() } })}>Compact context</button>
    {!eligible && !pending ? <p>Compaction requires a supported harness and Worker at an eligible settled boundary.</p> : null}
    <Problem error={context.error || mutation.error} />
    {mutation.uncertain ? <button disabled={mutation.busy} onClick={mutation.retry}>Retry the same compaction request</button> : null}
  </section>;
}
