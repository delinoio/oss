import { SettingsActionButton, SettingsActionIcon } from "./settings-action";
import { copy, useLocale } from "./localization";
import { useRef, useMemo, type ReactNode } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { EntityKind, ResourceQuery, supportsResourceSchema, isEntityId, type Resource } from "@delinoio/delidev-api-client";
import { document, object, text, type Document } from "./documents";
import { ServiceProblem, Problem  } from "./ui";

export enum JobState { Queued = "queued", Claimed = "claimed", Succeeded = "succeeded", Failed = "failed", Canceled = "canceled", Uncertain = "uncertain" }
const terminal = (row?: Resource) => [JobState.Succeeded, JobState.Failed, JobState.Canceled].includes(document(row).state as JobState);
// Acknowledgment retains the original job identity. A successful RPC alone is
// never successful Worker validation, and observing a job never resubmits it.
export function validJobObservation(row: Resource | undefined, initial: Resource | undefined, minimumRevision = initial?.revision ?? 1n): row is Resource {
  return Boolean(row && initial && isEntityId(initial.id) && row.id === initial.id && row.kind === EntityKind.JOB && initial.kind === EntityKind.JOB && initial.schemaVersion === 1 && supportsResourceSchema(initial) && row.schemaVersion === 1 && supportsResourceSchema(row) && initial.revision >= 1n && row.revision >= 1n && row.revision >= minimumRevision && Object.values(JobState).includes(document(row).state as JobState));
}
/** One exact-job cache observer shared by the retained owner and its view. */
export function useTrackedJobObservation(initial: Resource | undefined, active: boolean) {
  const highest = useRef<Resource | undefined>(initial);
  if (highest.current?.id !== initial?.id) highest.current = initial;
  const result = useQuery(ResourceQuery.getResource, { kind: EntityKind.JOB, id: initial?.id ?? "" }, { enabled: active && Boolean(initial), refetchInterval: query => active && Boolean(initial) && (!validJobObservation(query.state.data?.resource, initial, highest.current?.revision) || !terminal(query.state.data?.resource)) ? 2000 : false });
  const latest = result.data?.resource;
  const valid = validJobObservation(latest, initial, highest.current?.revision);
  if (valid) highest.current = latest;
  const current = highest.current ?? initial;
  const unreadable = Boolean(result.data && !valid);
  const settled = valid && !result.error && terminal(latest);
  return useMemo(() => ({ current, unreadable, error: result.error, loading: result.isFetching, refetch: result.refetch, settled }), [current, unreadable, result.error, result.isFetching, result.refetch, settled]);
}
export type TrackedJobObservation = ReturnType<typeof useTrackedJobObservation>;
export function TrackedJob({ initial, active, children }: { initial: Resource; active: boolean; children?: (state: string, output: Document) => ReactNode }) {
  const observation = useTrackedJobObservation(initial, active);
  return <TrackedJobView observation={observation}>{children}</TrackedJobView>;
}
/** The retained controller supplies its observer; presentation starts no poller. */
export function TrackedJobView({ observation, children }: { observation: TrackedJobObservation; children?: (state: string, output: Document) => ReactNode }) {
  useLocale();
  const { current, unreadable } = observation;
  const value = document(current), state = text(value.state), problem = object(value.problem);
  const attention = state !== JobState.Succeeded || Boolean(observation.error) || unreadable || Boolean(text(problem.message));
  return <>{attention ? <section className="notice" data-job-state={state}><OperationStatus state={state} />{unreadable ? <p role="alert">{copy("jobs.unreadableStatus")}</p> : null}{text(problem.message) ? <ServiceProblem code={text(problem.code) || text(problem.problem_code)}><p role="alert">{text(problem.message)} {text(problem.guidance)}</p></ServiceProblem> : null}<Problem error={observation.error} />{observation.error || unreadable ? <SettingsActionButton icon={SettingsActionIcon.Retry} type="button" disabled={observation.loading} onClick={() => void observation.refetch()}>{copy("jobs.retryStatusRead")}</SettingsActionButton> : null}</section> : null}{children?.(state, object(value.output))}</>;
}

// Presentation only: callers retain original query, polling and mutation ownership.
export function OperationStatus({ state }: { state: string }) {
  useLocale();
  if (state === JobState.Succeeded) return null;
  const message = state === JobState.Queued || state === JobState.Claimed ? "jobs.acceptedByTheServerWaitingFor_2b0bf0" : state === JobState.Uncertain ? "jobs.theWorkerOutcomeIsUncertainInspect_2212e2" : state === JobState.Failed ? "jobs.failed" : state === JobState.Canceled ? "jobs.canceled" : "jobs.readingStatus";
  return <p role="status">{copy(message)}</p>;
}
