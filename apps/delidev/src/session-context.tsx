import { LocalizedText, copy, useLocale } from "./localization";
import { useQuery } from "@connectrpc/connect-query";
import { EntityKind, SessionContextCapability, SessionQuery, SystemCapability, SystemQuery, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, object, text, type Document } from "./documents";
import { JobState, OperationStatus } from "./jobs";
import { useRetainedMutation } from "./mutation";
import { NativeContextCompaction } from "./native-context-compaction";
import { ServiceProblem, Problem  } from "./ui";

export function contextDocument(bytes: Uint8Array | undefined, session: string): Document | undefined {
  if (!bytes || bytes.byteLength > 1 << 20) return;
  try {
    const value = object(JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(bytes)));
    if (value.session_id !== session || typeof value.session_revision !== "string" || !/^[1-9][0-9]*$/.test(value.session_revision)) return;
    return value;
  } catch { return; }
}

export function SessionContext({ session }: { session: Resource }) {
  useLocale();
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
  if (!supported) return status.isLoading ? null : <section aria-label={copy("session-context.sessionContext_93a2ab")}><h3>{copy("session-context.context_a6e600")}</h3>{!status.error ? <p>{copy("session-context.updateTheServerAndOriginalWorker_00f78a")}</p> : <p>{copy("session-context.capabilityReadFailed")}</p>}<Problem error={status.error} />{status.error ? <button disabled={status.isFetching} onClick={() => void status.refetch()}>{copy("session-context.retryCapability")}</button> : null}</section>;
  return <section aria-label={copy("session-context.sessionContext_93a2ab")} className="session-context"><header><h3>{copy("session-context.context_a6e600")}</h3><button disabled={context.isFetching} onClick={() => void context.refetch()}>{copy("session-context.refreshContext_f79efc")}</button></header>
    <p><LocalizedText id="session-context.currentContextTokens_2fb474" components={{ s0: <>{view?.current_tokens === null || view?.current_tokens === undefined ? copy("session-context.notReported_adadfa") : text(view.current_tokens)}</> }} /></p>
    {(harness === "codex" || harness === "opencode") && view?.automatic_boundary ? <NativeContextCompaction progress={object(object(object(view.automatic_boundary).document).progress)} state="complete" /> : null}
    {state && (state !== JobState.Succeeded || result.compact_result === "success" || typeof codex.actions === "number" || typeof opencode.actions === "number" || text(problem.message)) ? <div><OperationStatus state={state} />
      {state === JobState.Succeeded && result.compact_result === "success" ? <p>{copy("session-context.theNativeContextWasCompactedAnd_c4307f")}</p> : null}
      {result.harness === "codex" && typeof codex.actions === "number" ? <p><LocalizedText id="session-context.retainedCompactionsResponseCounters_8201cb" components={{ s0: <>{codex.actions}</>, s1: <>{Array.isArray(codex.response_usages) && codex.response_usages.length > 0 ? copy("session-context.reported_34540b") : copy("session-context.notReportedByTheNativeProcess_e76117")}</> }} /></p> : null}
      {result.harness === "opencode" && typeof opencode.actions === "number" ? <p><LocalizedText id="session-context.retainedCompactionsNativeStepCounters_5af773" components={{ s0: <>{opencode.actions}</>, s1: <>{Array.isArray(opencode.usages) && opencode.usages.length > 0 ? copy("session-context.reported_34540b") : copy("session-context.notReportedByTheNativeProcess_e76117")}</> }} /></p> : null}
      {text(problem.message) ? <ServiceProblem code={text(problem.code) || text(problem.problem_code)}><p role="alert">{text(problem.message)} {text(problem.guidance)}</p></ServiceProblem> : null}
      {state === JobState.Uncertain ? <p role="alert">{copy("session-context.thisActionRequiresReconciliationPreserveThe_8ba4a5")}</p> : null}
    </div> : null}
    <p>{copy("session-context.compactTheNativeWorkingContextAt_69b0dd")}</p>
    <button disabled={!eligible || pending || mutation.busy || mutation.uncertain} onClick={() => void mutation.send({ mutation: { id: session.id, expectedRevision: session.revision, requestId: newRequestId() } })}>{copy("session-context.compactContext_afbbb8")}</button>
    {!eligible && !pending ? <p>{copy("session-context.compactionRequiresASupportedHarnessAnd_ae76ff")}</p> : null}
    <Problem error={context.error || mutation.error} />
    {mutation.uncertain ? <button disabled={mutation.busy} onClick={mutation.retry}>{copy("session-context.retryTheSameCompactionRequest_12667c")}</button> : null}
  </section>;
}
