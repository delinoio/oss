import { SettingsActionButton, SettingsActionIcon } from "./settings-action";
import { copy, useLocale } from "./localization";
import { useMemo, useRef, type ReactNode } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { EntityKind, ResourceQuery, supportsResourceSchema, isEntityId, type Resource } from "@delinoio/delidev-api-client";
import { document, object, text, type Document } from "./documents";
import { ServiceProblem, Problem  } from "./ui";

export enum JobState { Queued = "queued", Claimed = "claimed", Succeeded = "succeeded", Failed = "failed", Canceled = "canceled", Uncertain = "uncertain" }
export const terminalJobState = (state: string) => [JobState.Succeeded, JobState.Failed, JobState.Canceled].includes(state as JobState);
/** One exact-job read owner, shared with retained controllers and their views. */
export function useJobObservation(initial: Resource | undefined, active: boolean) {
  const retained = useRef<Resource | undefined>(initial);
  if (retained.current?.id !== initial?.id) retained.current = initial;
  const valid = (row?: Resource): row is Resource => Boolean(initial && initial.kind === EntityKind.JOB && initial.revision > 0n && supportsResourceSchema(initial) && row && isEntityId(row.id) && row.id === initial.id && row.kind === EntityKind.JOB && row.revision >= initial.revision && row.revision >= (retained.current?.revision ?? initial.revision) && supportsResourceSchema(row) && Object.values(JobState).includes(document(row).state as JobState));
  const result = useQuery(ResourceQuery.getResource, { kind: EntityKind.JOB, id: initial?.id ?? "" }, { enabled: active && Boolean(initial), refetchInterval: (query) => active && initial && !(valid(query.state.data?.resource) && terminalJobState(text(document(query.state.data?.resource).state))) ? 2000 : false });
  const latest = result.data?.resource;
  const readable = valid(latest);
  if (readable) retained.current = latest;
  const current = retained.current ?? initial;
  const value = document(current), state = text(value.state), problem = object(value.problem);
  const unreadable = Boolean(result.data && !readable);
  const verified = Boolean(readable && !result.error && !result.isFetching);
  return useMemo(() => ({ current, value, state, problem, unreadable, verified, error: result.error, loading: result.isFetching, refetch: result.refetch }), [current, state, unreadable, verified, result.error, result.isFetching, result.refetch]);
}
export type JobObservation = ReturnType<typeof useJobObservation>;
// Acknowledgment retains identity; observing a job never resubmits it.
export function TrackedJob({ initial, active, children }: { initial: Resource; active: boolean; children?: (state: string, output: Document, observation: { verified: boolean }) => ReactNode }) {
  const observation = useJobObservation(initial, active);
  return <ObservedJob observation={observation}>{children}</ObservedJob>;
}
/** Presentation and retry reuse the original owner's cache and read lifetime. */
export function ObservedJob({ observation, children }: { observation: JobObservation; children?: (state: string, output: Document, observation: { verified: boolean }) => ReactNode }) {
  useLocale();
  const { state, value, problem, unreadable, verified } = observation;
  const result = { error: observation.error, isFetching: observation.loading, refetch: observation.refetch };
  const attention = state !== JobState.Succeeded || Boolean(result.error) || unreadable || Boolean(text(problem.message));
  return <>{attention ? <section className="notice" data-job-state={state}><OperationStatus state={state} />{unreadable ? <p role="alert">{copy("jobs.unreadableStatus")}</p> : null}{text(problem.message) ? <ServiceProblem code={text(problem.code) || text(problem.problem_code)}><p role="alert">{text(problem.message)} {text(problem.guidance)}</p></ServiceProblem> : null}<Problem error={result.error} />{result.error || unreadable ? <SettingsActionButton icon={SettingsActionIcon.Retry} type="button" disabled={result.isFetching} onClick={() => void result.refetch()}>{copy("jobs.retryStatusRead")}</SettingsActionButton> : null}</section> : null}{children?.(state, object(value.output), { verified })}</>;
}

// Presentation only: callers retain original query, polling and mutation ownership.
export function OperationStatus({ state }: { state: string }) {
  useLocale();
  if (state === JobState.Succeeded) return null;
  const message = state === JobState.Queued || state === JobState.Claimed ? "jobs.acceptedByTheServerWaitingFor_2b0bf0" : state === JobState.Uncertain ? "jobs.theWorkerOutcomeIsUncertainInspect_2212e2" : state === JobState.Failed ? "jobs.failed" : state === JobState.Canceled ? "jobs.canceled" : "jobs.readingStatus";
  return <p role="status">{copy(message)}</p>;
}
