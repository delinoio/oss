import { SettingsActionButton, SettingsActionIcon } from "./settings-action";
import { copy, useLocale } from "./localization";
import type { ReactNode } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { EntityKind, ResourceQuery, type Resource } from "@delinoio/delidev-api-client";
import { document, object, text, type Document } from "./documents";
import { ServiceProblem, Problem  } from "./ui";

export enum JobState { Queued = "queued", Claimed = "claimed", Succeeded = "succeeded", Failed = "failed", Canceled = "canceled", Uncertain = "uncertain" }
const terminal = (row?: Resource) => [JobState.Succeeded, JobState.Failed, JobState.Canceled].includes(document(row).state as JobState);
// Acknowledgment retains the original job identity. A successful RPC alone is
// never successful Worker validation, and observing a job never resubmits it.
export function TrackedJob({ initial, active, children }: { initial: Resource; active: boolean; children?: (state: string, output: Document) => ReactNode }) {
  useLocale();
  const result = useQuery(ResourceQuery.getResource, { kind: EntityKind.JOB, id: initial.id }, { enabled: active, refetchInterval: (query) => active && !terminal(query.state.data?.resource) ? 2000 : false });
  const latest = result.data?.resource;
  const current = latest && latest.id === initial.id && latest.kind === EntityKind.JOB && latest.revision >= initial.revision ? latest : initial;
  const value = document(current), state = text(value.state), problem = object(value.problem);
  const unreadable = Boolean(result.data && (!latest || latest !== current));
  const attention = state !== JobState.Succeeded || Boolean(result.error) || unreadable || Boolean(text(problem.message));
  return <>{attention ? <section className="notice" data-job-state={state}><OperationStatus state={state} />{unreadable ? <p role="alert">{copy("jobs.unreadableStatus")}</p> : null}{text(problem.message) ? <ServiceProblem code={text(problem.code) || text(problem.problem_code)}><p role="alert">{text(problem.message)} {text(problem.guidance)}</p></ServiceProblem> : null}<Problem error={result.error} />{result.error || unreadable ? <SettingsActionButton icon={SettingsActionIcon.Retry} type="button" disabled={result.isFetching} onClick={() => void result.refetch()}>{copy("jobs.retryStatusRead")}</SettingsActionButton> : null}</section> : null}{children?.(state, object(value.output))}</>;
}

// Presentation only: callers retain original query, polling and mutation ownership.
export function OperationStatus({ state }: { state: string }) {
  useLocale();
  if (state === JobState.Succeeded) return null;
  const message = state === JobState.Queued || state === JobState.Claimed ? "jobs.acceptedByTheServerWaitingFor_2b0bf0" : state === JobState.Uncertain ? "jobs.theWorkerOutcomeIsUncertainInspect_2212e2" : state === JobState.Failed ? "jobs.failed" : state === JobState.Canceled ? "jobs.canceled" : "jobs.readingStatus";
  return <p role="status">{copy(message)}</p>;
}
