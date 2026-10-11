import { diagnosticPresentation } from "./product-presentation";
import { SettingsActionButton, SettingsActionIcon } from "./settings-action";
import { copy, useLocale } from "./localization";
import { useRef, useMemo, type ReactNode } from "react";
import { useQuery } from "@connectrpc/connect-query";
import {
  EntityKind,
  isEntityId,
  ResourceQuery,
  supportsResourceSchema,
  type Resource,
} from "@delinoio/delidev-api-client";
import { document, object, text, type Document } from "./documents";
import { ServiceProblem, Problem } from "./ui";

export enum JobState {
  Queued = "queued",
  Claimed = "claimed",
  Succeeded = "succeeded",
  Failed = "failed",
  Canceled = "canceled",
  Uncertain = "uncertain",
}
const terminalStates = [JobState.Succeeded, JobState.Failed, JobState.Canceled];
function exactJob(
  row: Resource | undefined,
  initial: Resource,
  revision: bigint,
  expectedType?: string,
  machineId?: string,
): row is Resource {
  if (
    !row ||
    !isEntityId(initial.id) ||
    initial.revision < 1n ||
    initial.kind !== EntityKind.JOB ||
    !supportsResourceSchema(initial) ||
    row.id !== initial.id ||
    row.kind !== EntityKind.JOB ||
    row.revision < revision ||
    !supportsResourceSchema(row)
  )
    return false;
  const original = document(initial),
    value = document(row);
  return (
    Object.values(JobState).includes(value.state as JobState) &&
    (!text(original.type) || value.type === original.type) &&
    (!expectedType ||
      (original.type === expectedType && value.type === expectedType)) &&
    (!machineId ||
      (original.machine_id === machineId && value.machine_id === machineId))
  );
}

// One retained controller owns the exact-job read and presentation shares this
// observation. A status read never resubmits work or grants native readiness.
export function useJobObservation(
  initial: Resource | undefined,
  active: boolean,
  expectedType?: string,
  machineId?: string,
) {
  const retained = useRef<{ id: string; current: Resource } | undefined>(
    undefined,
  );
  if (initial && retained.current?.id !== initial.id)
    retained.current = { id: initial.id, current: initial };
  if (!initial) retained.current = undefined;
  const floor = initial
    ? retained.current && retained.current.current.revision > initial.revision
      ? retained.current.current.revision
      : initial.revision
    : 0n;
  const result = useQuery(
    ResourceQuery.getResource,
    { kind: EntityKind.JOB, id: initial?.id ?? "" },
    {
      enabled: active && Boolean(initial),
      refetchInterval: (query) => {
        if (!active || !initial || query.state.error) return false;
        const response = query.state.data;
        const row = response?.resource;
        if (response && !exactJob(row, initial, floor, expectedType, machineId))
          return false;
        return !row || !terminalStates.includes(document(row).state as JobState)
          ? 2000
          : false;
      },
    },
  );
  const latest = result.data?.resource;
  const exact = Boolean(
    initial && exactJob(latest, initial, floor, expectedType, machineId),
  );
  if (initial && latest && exact && !result.error && !result.isFetching)
    retained.current = { id: initial.id, current: latest };
  const current = exact ? latest : (retained.current?.current ?? initial);
  const value = useMemo(() => document(current), [current]);
  const state = text(value.state);
  const problem = useMemo(() => object(value.problem), [value]);
  const output = useMemo(() => object(value.output), [value]);
  const unreadable = Boolean(
    result.data &&
    (!exact || (state === JobState.Succeeded && value.problem != null)),
  );
  // Failed/canceled outcomes can carry their original problem and still prove
  // settlement. A succeeded result carrying a problem cannot prove success.
  const verified = Boolean(
    initial &&
    exact &&
    !result.error &&
    !result.isFetching &&
    (state !== JobState.Succeeded || value.problem == null),
  );
  return useMemo(
    () => ({
      current,
      state,
      output,
      problem,
      unreadable,
      verified,
      terminal: verified && terminalStates.includes(state as JobState),
      active: active && Boolean(initial),
      pending: result.isFetching,
      error: result.error,
      retry: result.refetch,
    }),
    [
      current,
      state,
      output,
      problem,
      unreadable,
      verified,
      result.isFetching,
      result.error,
      result.refetch,
      active,
      initial,
    ],
  );
}
export type JobObservation = ReturnType<typeof useJobObservation>;
type JobChildren = (
  state: string,
  output: Document,
  observation: JobObservation,
) => ReactNode;
export function TrackedJob({
  initial,
  active,
  expectedType,
  children,
}: {
  initial: Resource;
  active: boolean;
  expectedType?: string;
  children?: JobChildren;
}) {
  const observation = useJobObservation(initial, active, expectedType);
  return <ObservedJob observation={observation}>{children}</ObservedJob>;
}
// Presentation only: no second query or polling owner is created here.
export function ObservedJob({
  observation,
  children,
}: {
  observation: JobObservation;
  children?: JobChildren;
}) {
  useLocale();
  const { state, output, problem, unreadable } = observation;
  const attention =
    state !== JobState.Succeeded ||
    Boolean(observation.error) ||
    unreadable ||
    Boolean(text(problem.message));
  return (
    <>
      {attention ? (
        <section className="notice" data-job-state={state}>
          <OperationStatus state={state} />
          {unreadable ? (
            <p role="alert">{copy("jobs.unreadableStatus")}</p>
          ) : null}
          {text(problem.message) ? (
            <ServiceProblem
              code={text(problem.code) || text(problem.problem_code)}
            >
              <p role="alert">
                {diagnosticPresentation(problem.message)}{" "}
                {diagnosticPresentation(problem.guidance)}
              </p>
            </ServiceProblem>
          ) : null}
          <Problem error={observation.error} />
          {observation.error || unreadable ? (
            <SettingsActionButton
              icon={SettingsActionIcon.Retry}
              type="button"
              disabled={!observation.active}
              onClick={() => void observation.retry()}
            >
              {copy("jobs.retryStatusRead")}
            </SettingsActionButton>
          ) : null}
        </section>
      ) : null}
      {children?.(state, output, observation)}
    </>
  );
}

// Presentation only: callers retain original query, polling and mutation ownership.
export function OperationStatus({ state }: { state: string }) {
  useLocale();
  if (state === JobState.Succeeded) return null;
  const message =
    state === JobState.Queued || state === JobState.Claimed
      ? "jobs.acceptedByTheServerWaitingFor_2b0bf0"
      : state === JobState.Uncertain
        ? "jobs.theWorkerOutcomeIsUncertainInspect_2212e2"
        : state === JobState.Failed
          ? "jobs.failed"
          : state === JobState.Canceled
            ? "jobs.canceled"
            : "jobs.readingStatus";
  return <p role="status">{copy(message)}</p>;
}
