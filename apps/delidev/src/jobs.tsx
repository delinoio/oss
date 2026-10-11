import { SettingsActionButton, SettingsActionIcon } from "./settings-action";
import { copy, useLocale } from "./localization";
import { useMemo, useRef, type ReactNode } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { EntityKind, ResourceQuery, isEntityId, supportsResourceSchema, type Resource } from "@delinoio/delidev-api-client";
import { document, object, text, type Document } from "./documents";
import { ServiceProblem, Problem  } from "./ui";

export enum JobState { Queued = "queued", Claimed = "claimed", Succeeded = "succeeded", Failed = "failed", Canceled = "canceled", Uncertain = "uncertain" }
const terminal = (row?: Resource) => [JobState.Succeeded, JobState.Failed, JobState.Canceled].includes(document(row).state as JobState);
// Acknowledgment retains the original job identity. A successful RPC alone is
// never successful Worker validation, and observing a job never resubmits it.
export function useTrackedJobObservation(initial: Resource | undefined, active: boolean) {
  const floor = useRef<{ id: string; revision: bigint }>();
  if (initial && floor.current?.id !== initial.id) floor.current = { id: initial.id, revision: initial.revision };
  const validRow = (latest?: Resource) => Boolean(initial && isEntityId(initial.id) && initial.revision > 0n && initial.kind === EntityKind.JOB && supportsResourceSchema(initial) && latest && latest.id === initial.id && latest.kind === EntityKind.JOB && supportsResourceSchema(latest) && latest.revision >= initial.revision && latest.revision >= (floor.current?.revision ?? initial.revision) && Object.values(JobState).includes(document(latest).state as JobState));
  const result = useQuery(ResourceQuery.getResource, { kind: EntityKind.JOB, id: initial?.id ?? "" }, { enabled: active && Boolean(initial), refetchInterval: (query) => active && initial && !(validRow(query.state.data?.resource) && terminal(query.state.data?.resource)) ? 2000 : false });
  const latest = result.data?.resource;
  const valid = validRow(latest);
  if (valid && latest) floor.current = { id: latest.id, revision: latest.revision };
  const current = valid ? latest : initial;
  const unreadable = Boolean(result.data && !valid);
  const verified = Boolean(valid && !result.error && !result.isFetching);
  return useMemo(() => ({ current, unreadable, verified, error: result.error, loading: result.isFetching, refetch: result.refetch }), [current, unreadable, verified, result.error, result.isFetching, result.refetch]);
}
export type TrackedJobObservation = ReturnType<typeof useTrackedJobObservation>;
type TrackedJobProps = { initial: Resource; active: boolean; children?: (state: string, output: Document, observation: { verified: boolean }) => ReactNode; observation?: TrackedJobObservation };
export function TrackedJob(props: TrackedJobProps) {
  return props.observation ? <JobPresentation {...props} observation={props.observation} /> : <OwnedTrackedJob {...props} />;
}
function OwnedTrackedJob(props: TrackedJobProps) {
  const observation = useTrackedJobObservation(props.initial, props.active);
  return <JobPresentation {...props} observation={observation} />;
}
function JobPresentation({ children, observation }: TrackedJobProps & { observation: TrackedJobObservation }) {
  useLocale();
  const { current, unreadable, verified } = observation;
  const result = { error: observation.error, isFetching: observation.loading, refetch: observation.refetch };
  const value = document(current), state = text(value.state), problem = object(value.problem);
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
