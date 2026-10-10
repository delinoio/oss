import { SettingsActionButton, SettingsActionIcon } from "./settings-action";
import { copy, useLocale } from "./localization";
import { useMemo, useRef, type ReactNode } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { EntityKind, ResourceQuery, type Resource } from "@delinoio/delidev-api-client";
import { document, object, text, type Document } from "./documents";
import { ServiceProblem, Problem  } from "./ui";

export enum JobState { Queued = "queued", Claimed = "claimed", Succeeded = "succeeded", Failed = "failed", Canceled = "canceled", Uncertain = "uncertain" }
const terminal = (row?: Resource) => [JobState.Succeeded, JobState.Failed, JobState.Canceled].includes(document(row).state as JobState);
function originalJob(row: Resource | undefined, initial: Resource): row is Resource {
  return Boolean(row && row.id === initial.id && row.kind === EntityKind.JOB && row.schemaVersion === 1 && row.revision > 0n && row.revision >= initial.revision && Object.values(JobState).includes(document(row).state as JobState));
}
/** One exact-job query owner; presenters may borrow the verified observation. */
export function useTrackedJob(initial: Resource | undefined, active: boolean) {
  const retained = useRef<Resource | undefined>(undefined);
  if (retained.current?.id !== initial?.id) retained.current = undefined;
  const result = useQuery(ResourceQuery.getResource, { kind: EntityKind.JOB, id: initial?.id ?? "" }, { enabled: active && Boolean(initial), refetchInterval: query => {
    const row = query.state.data?.resource;
    const finished = initial && originalJob(row, initial) && row.revision >= (retained.current?.revision ?? initial.revision) && terminal(row);
    return active && initial && !finished ? 2000 : false;
  } });
  const latest = result.data?.resource;
  const baseline = retained.current ?? initial;
  const readable = Boolean(initial && originalJob(latest, initial) && latest.revision >= (baseline?.revision ?? 0n));
  if (readable) retained.current = latest;
  const current = readable ? latest : baseline;
  const unreadable = Boolean(initial && (!originalJob(current, initial) || result.data && !readable));
  const verified = Boolean(initial && originalJob(current, initial) && !result.error && !unreadable);
  return useMemo(() => ({ current, verified, terminal: verified && terminal(current), unreadable, error: result.error, refetch: result.refetch, loading: result.isFetching }), [current, verified, unreadable, result.error, result.refetch, result.isFetching]);
}
export type TrackedJobObservation = ReturnType<typeof useTrackedJob>;
// Acknowledgment retains the original job identity. A successful RPC alone is
// never successful Worker validation, and observing a job never resubmits it.
export function TrackedJob({ initial, active, children }: { initial: Resource; active: boolean; children?: (state: string, output: Document) => ReactNode }) {
  const observation = useTrackedJob(initial, active);
  return <TrackedJobView observation={observation}>{children}</TrackedJobView>;
}
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
