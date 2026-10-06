import { statusLabel } from "./product-status";
import { LocalizedText, copy, useLocale } from "./localization";
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
  return <section className="notice" aria-label={copy("jobs.workerOperation_b3e39e")}><p><LocalizedText id="jobs.workerOperation_8d8a5d" components={{ s0: <>{statusLabel(state) || copy("jobs.extra.b764cdc0eab7")}</> }} /></p><small>{initial.id}</small>{state === JobState.Queued || state === JobState.Claimed ? <p>{copy("jobs.acceptedByTheServerWaitingFor_2b0bf0")}</p> : null}{state === JobState.Uncertain ? <p>{copy("jobs.theWorkerOutcomeIsUncertainInspect_2212e2")}</p> : null}{text(problem.message) ? <ServiceProblem code={text(problem.code) || text(problem.problem_code)}><p role="alert">{text(problem.message)} {text(problem.guidance)}</p></ServiceProblem> : null}<Problem error={result.error} /><button type="button" disabled={result.isFetching} onClick={() => void result.refetch()}>{copy("jobs.refreshOperation_8b5c83")}</button>{children?.(state, object(value.output))}</section>;
}
