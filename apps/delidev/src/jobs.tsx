import type { ReactNode } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { EntityKind, ResourceQuery, type Resource } from "@delinoio/delidev-api-client";
import { document, object, text, type Document } from "./documents";
import { Problem } from "./ui";

export enum JobState { Queued = "queued", Claimed = "claimed", Succeeded = "succeeded", Failed = "failed", Canceled = "canceled", Uncertain = "uncertain" }
const terminal = (row?: Resource) => [JobState.Succeeded, JobState.Failed, JobState.Canceled].includes(document(row).state as JobState);
// Acknowledgment retains the original job identity. A successful RPC alone is
// never successful Worker validation, and observing a job never resubmits it.
export function TrackedJob({ initial, active, children }: { initial: Resource; active: boolean; children?: (state: string, output: Document) => ReactNode }) {
  const result = useQuery(ResourceQuery.getResource, { kind: EntityKind.JOB, id: initial.id }, { enabled: active, refetchInterval: (query) => active && !terminal(query.state.data?.resource) ? 2000 : false });
  const latest = result.data?.resource;
  const current = latest && latest.id === initial.id && latest.kind === EntityKind.JOB && latest.revision >= initial.revision ? latest : initial;
  const value = document(current), state = text(value.state), problem = object(value.problem);
  return <section className="notice" aria-label="Worker operation"><p>Worker operation: {state || "Unknown"}</p><small>{initial.id}</small>{state === JobState.Queued || state === JobState.Claimed ? <p>Accepted by the server. Waiting for the selected Worker to finish.</p> : null}{state === JobState.Uncertain ? <p>The Worker outcome is uncertain. Inspect the original operation before starting another.</p> : null}{text(problem.message) ? <p role="alert">{text(problem.message)} {text(problem.guidance)}</p> : null}<Problem error={result.error} /><button type="button" disabled={result.isFetching} onClick={() => void result.refetch()}>Refresh operation</button>{children?.(state, object(value.output))}</section>;
}
