import { SettingsActionButton, SettingsActionIcon } from "./settings-action";
import { copy, useLocale } from "./localization";
import { useCallback, useMemo, useRef, type ReactNode } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { EntityKind, ResourceQuery, supportsResourceSchema, type Resource } from "@delinoio/delidev-api-client";
import { document, object, text, type Document } from "./documents";
import { ServiceProblem, Problem  } from "./ui";

export enum JobState { Queued = "queued", Claimed = "claimed", Succeeded = "succeeded", Failed = "failed", Canceled = "canceled", Uncertain = "uncertain" }
export const terminalJobState = (state: string) => [JobState.Succeeded, JobState.Failed, JobState.Canceled].includes(state as JobState);
export interface JobObservation {
  state: string;
  output: Document;
  problem: Document;
  readable: boolean;
  verified: boolean;
  pending: boolean;
  unreadable: boolean;
  error: unknown;
  retry: () => void;
}
// The original controller owns this reader. Presenters can borrow its result
// without subscribing to another query or extending the active-read lifetime.
export function useJobObservation(initial: Resource | undefined, active: boolean): JobObservation {
  const retained = useRef(initial);
  if (retained.current?.id !== initial?.id) retained.current = initial;
  const acceptable = (row?: Resource): row is Resource => Boolean(initial && row && row.id === initial.id && row.kind === EntityKind.JOB && row.revision > 0n && row.revision >= initial.revision && row.revision >= (retained.current?.revision ?? 0n) && supportsResourceSchema(row) && Object.values(JobState).includes(text(document(row).state) as JobState));
  const result = useQuery(ResourceQuery.getResource, { kind: EntityKind.JOB, id: initial?.id ?? "" }, { enabled: active && Boolean(initial), refetchInterval: (query) => active && initial && !(acceptable(query.state.data?.resource) && terminalJobState(text(document(query.state.data?.resource).state))) ? 2000 : false });
  const latest = result.data?.resource;
  const accepted = acceptable(latest);
  if (accepted && !result.error && !result.isFetching) retained.current = latest;
  const value = document(retained.current ?? initial), state = text(value.state), problem = object(value.problem);
  const unreadable = Boolean(result.data && !accepted);
  const pending = result.isFetching || Boolean(initial && !result.data && !result.error);
  const readable = Boolean(accepted && !result.error && !pending);
  const verified = readable && !text(problem.message);
  const retry = useCallback(() => { if (active && initial) void result.refetch(); }, [active, initial?.id, result.refetch]);
  return useMemo(() => ({ state, output: object(value.output), problem, readable, verified, pending, unreadable, error: result.error, retry }), [retained.current, state, readable, verified, pending, unreadable, result.error, retry]);
}
type JobChildren = (state: string, output: Document, observation: JobObservation) => ReactNode;
// Acknowledgment retains the original job identity. A successful RPC alone is
// never successful Worker validation, and observing a job never resubmits it.
export function TrackedJob({ initial, active, children }: { initial: Resource; active: boolean; children?: JobChildren }) {
  const observation = useJobObservation(initial, active);
  return <JobPresentation observation={observation}>{children}</JobPresentation>;
}
export function JobPresentation({ observation, children }: { observation: JobObservation; children?: JobChildren }) {
  useLocale();
  const { state, output, problem, unreadable, error, pending, retry } = observation;
  const attention = state !== JobState.Succeeded || Boolean(error) || unreadable || Boolean(text(problem.message));
  return <>{attention ? <section className="notice" data-job-state={state}><OperationStatus state={state} />{unreadable ? <p role="alert">{copy("jobs.unreadableStatus")}</p> : null}{text(problem.message) ? <ServiceProblem code={text(problem.code) || text(problem.problem_code)}><p role="alert">{text(problem.message)} {text(problem.guidance)}</p></ServiceProblem> : null}<Problem error={error} />{error || unreadable ? <SettingsActionButton icon={SettingsActionIcon.Retry} type="button" disabled={pending} onClick={retry}>{copy("jobs.retryStatusRead")}</SettingsActionButton> : null}</section> : null}{children?.(state, output, observation)}</>;
}

// Presentation only: callers retain original query, polling and mutation ownership.
export function OperationStatus({ state }: { state: string }) {
  useLocale();
  if (state === JobState.Succeeded) return null;
  const message = state === JobState.Queued || state === JobState.Claimed ? "jobs.acceptedByTheServerWaitingFor_2b0bf0" : state === JobState.Uncertain ? "jobs.theWorkerOutcomeIsUncertainInspect_2212e2" : state === JobState.Failed ? "jobs.failed" : state === JobState.Canceled ? "jobs.canceled" : "jobs.readingStatus";
  return <p role="status">{copy(message)}</p>;
}
