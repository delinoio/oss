import { useSessionQuery as useQuery } from "./session-activity";
import { LocalizedText, copy, useLocale } from "./localization";

import { EntityKind, ResourceQuery, SessionContextCapability, SessionQuery, SystemCapability, SystemQuery, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, object, text, type Document } from "./documents";
import { JobState, OperationStatus } from "./jobs";
import { useRetainedMutation } from "./mutation";
import { NativeContextCompaction } from "./native-context-compaction";
import { ServiceProblem, Problem  } from "./ui";
import { timestampInstant } from "./timestamp-format";
import { Timestamp } from "./timestamp-display";

export enum NativeContextStatus { Latest = "latest", Historical = "historical" }
export interface NativeContextObservation {
 harness: "codex"; source: "last-request-total"; tokens: string;
 observation_id: string; execution_id: string; native_thread_id: string; native_turn_id: string;
 sequence: string; observed_at: string; status: NativeContextStatus;
}
const contextID = (value: unknown): value is string => typeof value === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(value);
export function nativeContextObservation(value: unknown): NativeContextObservation | undefined {
 const v = object(value);
 const fields = ["harness", "source", "tokens", "observation_id", "execution_id", "native_thread_id", "native_turn_id", "sequence", "observed_at", "status"];
 if (Object.keys(v).length !== fields.length || Object.keys(v).some(key => !fields.includes(key)) || v.harness !== "codex" || v.source !== "last-request-total" || ![v.observation_id, v.execution_id, v.native_thread_id, v.native_turn_id].every(contextID)) return;
 if (typeof v.tokens !== "string" || !/^(0|[1-9][0-9]{0,18})$/.test(v.tokens) || BigInt(v.tokens) > 9223372036854775807n || typeof v.sequence !== "string" || !/^[1-9][0-9]{0,19}$/.test(v.sequence) || BigInt(v.sequence) > 18446744073709551615n || typeof v.observed_at !== "string" || timestampInstant(v.observed_at) === undefined || !Object.values(NativeContextStatus).includes(v.status as NativeContextStatus)) return;
 return v as unknown as NativeContextObservation;
}

export function contextDocument(bytes: Uint8Array | undefined, session: string, expected?: Document): Document | undefined {
  if (!bytes || bytes.byteLength > 1 << 20) return;
  try {
    const value = object(JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(bytes)));
    if (value.session_id !== session || typeof value.session_revision !== "string" || !/^[1-9][0-9]*$/.test(value.session_revision)) return;
    if ("native_context" in value) {
      const native = nativeContextObservation(value.native_context);
      if (!native || !contextID(value.execution_id) || native.status === NativeContextStatus.Latest && native.execution_id !== value.execution_id || value.current_tokens !== null) return;
      if (expected) {
        const progress = object(expected.execution);
        if (object(object(expected.initial_execution).configuration).harness !== "codex" || native.execution_id !== progress.execution_id || native.native_thread_id !== progress.native_thread_id) return;
      }
    }
    return value;
  } catch { return; }
}

export function contextCapacity(resource: Resource | undefined, sessionId: string, native: NativeContextObservation | undefined): number | undefined {
  if (!native || !resource || resource.kind !== EntityKind.USAGE || resource.id !== native.observation_id || resource.sessionId !== sessionId || resource.schemaVersion !== 1) return;
  const value = document(resource), observation = object(value.observation), total = object(observation.last_request).total, capacity = observation.context_window;
  if (value.harness !== "codex" || value.execution_id !== native.execution_id || value.native_thread_id !== native.native_thread_id || value.native_turn_id !== native.native_turn_id || !Number.isSafeInteger(value.sequence) || BigInt(value.sequence as number) !== BigInt(native.sequence) || !Number.isSafeInteger(total) || (total as number) < 0 || BigInt(total as number) !== BigInt(native.tokens) || !Number.isSafeInteger(capacity) || (capacity as number) <= 0) return;
  return capacity as number;
}

export function SessionContext({ session }: { session: Resource }) {
  useLocale();
  const status = useQuery(SystemQuery.getStatus, {});
  const supported = Boolean(status.data?.capabilities.includes(SystemCapability.NATIVE_SESSION_COMPACTION_V1));
  const context = useQuery(SessionQuery.getSessionContext, { sessionId: session.id }, { enabled: supported, refetchInterval: 5000 });
  const view = contextDocument(context.data?.documentJson, session.id, document(session));
  const manual = object(object(view?.manual_action).document);
  const state = text(manual.state);
  const pending = Boolean(state && ![JobState.Succeeded, JobState.Failed, JobState.Canceled].includes(state as JobState));
  const mutation = useRetainedMutation(`compact:${session.id}`, SessionQuery.compactSession, () => { void context.refetch(); }, (reply, request) => {
    const input = object(document(reply.job).input);
    return reply.requestId === request.mutation?.requestId && reply.job?.kind === EntityKind.JOB && reply.job.sessionId === session.id && input.action_id === request.mutation.requestId && object(input.assignment).session_id === session.id;
  });
  const harness = text(object(object(document(session).initial_execution).configuration).harness);
  const native = harness === "codex" ? nativeContextObservation(view?.native_context) : undefined;
  const usage = useQuery(ResourceQuery.getResource, { kind: EntityKind.USAGE, id: native?.observation_id ?? "" }, { enabled: Boolean(native), retry: false });
  const capacity = usage.error ? undefined : contextCapacity(usage.data?.resource, session.id, native);
  const percent = capacity !== undefined && native ? Number(native.tokens) / capacity * 100 : undefined;
  const historical = native?.native_turn_id !== object(document(session).execution).native_turn_id || native?.status === NativeContextStatus.Historical || Boolean(context.error) || context.isFetching || view?.session_revision !== session.revision.toString();
  const capability = harness === "codex" ? SessionContextCapability.CODEX_MANUAL_COMPACTION_V1 : harness === "claude-code" ? SessionContextCapability.CLAUDE_MANUAL_COMPACTION_V1 : harness === "opencode" ? SessionContextCapability.OPENCODE_MANUAL_COMPACTION_V1 : undefined;
  const eligible = supported && (harness !== "codex" || status.data?.capabilities.includes(SystemCapability.CODEX_SESSION_COMPACTION_V1)) && (harness !== "opencode" || status.data?.capabilities.includes(SystemCapability.OPENCODE_SESSION_COMPACTION_V1)) && capability !== undefined && context.data?.capabilities.includes(capability) && view?.session_revision === session.revision.toString() && !context.error && !context.isFetching;
  const result = object(manual.result), codex = object(result.codex), opencode = object(result.opencode), problem = object(manual.problem);
  if (!supported) return status.isLoading ? <p role="status">{copy("session-context.loadingCapability")}</p> : <section aria-label={copy("session-context.sessionContext_93a2ab")}>{!status.error ? <p>{copy("session-context.updateTheServerAndOriginalWorker_00f78a")}</p> : <p>{copy("session-context.capabilityReadFailed")}</p>}<Problem error={status.error} />{status.error ? <button disabled={status.isFetching} onClick={() => void status.refetch()}>{copy("session-context.retryCapability")}</button> : null}</section>;
  return <section aria-label={copy("session-context.sessionContext_93a2ab")} className="session-context">{context.error ? <button disabled={context.isFetching} onClick={() => void context.refetch()}>{copy("session-name.retryRead")}</button> : null}
    {harness === "codex" ? <>
      <p><LocalizedText id="session-context.latestNativeTokens" components={{ s0: <>{native?.tokens ?? copy("session-context.notReported_adadfa")}</> }} /></p>
      <div className="context-capacity" role="progressbar" aria-label={copy("session-name.contextSnapshot")} aria-valuemin={0} aria-valuemax={percent === undefined ? 100 : Math.max(100, percent)} aria-valuenow={percent} aria-valuetext={percent === undefined ? copy("session-name.capacityUnavailable") : `${native?.tokens} / ${capacity} (${percent.toFixed(1)}%)`}><div hidden={percent === undefined} style={{ width: `${percent === undefined ? 0 : Math.min(100, Math.max(0, percent))}%` }} /></div>
      <p>{percent === undefined ? copy("session-name.capacityUnavailable") : `${native?.tokens} / ${capacity} (${percent.toFixed(1)}%)`}</p>
      {native ? <><p>{copy(historical ? "session-context.historicalSnapshot" : "session-context.latestSnapshot")}</p><p><LocalizedText id="session-context.observedAt" components={{ s0: <Timestamp value={native.observed_at} /> }} /></p></> : null}
      <p>{copy("session-context.nativeSnapshotExplanation")}</p>
      {context.error ? <p role="status">{copy("session-context.contextReadFailed")}</p> : null}
      {context.data && !view && !context.error ? <p role="status">{copy("session-context.invalidContext")}</p> : null}
    </> : <p><LocalizedText id="session-context.currentContextTokens_2fb474" components={{ s0: <>{view?.current_tokens === null || view?.current_tokens === undefined ? copy("session-context.notReported_adadfa") : text(view.current_tokens)}</> }} /></p>}
    {(harness === "codex" || harness === "opencode") && view?.automatic_boundary ? <NativeContextCompaction progress={object(object(object(view.automatic_boundary).document).progress)} state="complete" /> : null}
    {state && (state !== JobState.Succeeded || result.compact_result === "success" || typeof codex.actions === "number" || typeof opencode.actions === "number" || text(problem.message)) ? <div><OperationStatus state={state} />
      {state === JobState.Succeeded && result.version !== 4 && result.compact_result === "success" ? <p>{copy("session-context.theNativeContextWasCompactedAnd_c4307f")}</p> : null}
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
