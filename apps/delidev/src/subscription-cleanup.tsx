// SPDX-License-Identifier: Apache-2.0
import { useCallback, useEffect, useRef, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import {
  FailedSubscriptionCleanupState as State, FailedSubscriptionCleanupOutcome as Outcome,
  FailedSubscriptionCleanupReason as Reason, SubscriptionQuery, isEntityId, newRequestId,
  type FailedSubscriptionCleanupJob, type GetFailedSubscriptionCleanupResponse,
} from "@delinoio/delidev-api-client";
import { copy, useLocale, type MessageKey } from "./localization";
import { useRetainedMutation } from "./mutation";
import { useSettingsOpening } from "./settings-lifetime";
import { InlineRemediation, Problem } from "./ui";
import { useConnectPaginationReader, usePaginationChain } from "./scroll-pagination-query";
import { ScrollContinuation, useScrollRoot } from "./scroll-continuation";

export function validCleanupJob(job?: FailedSubscriptionCleanupJob): job is FailedSubscriptionCleanupJob {
  return Boolean(job && isEntityId(job.id) && job.revision > 0n && [State.PENDING, State.COMPLETED, State.FAILED].includes(job.state) &&
    job.total <= 10000 && job.processed === job.deleted + job.retained && job.processed <= job.total &&
    (job.state !== State.COMPLETED || job.processed === job.total) && job.problemCode.length <= 128);
}
function validPage(value: GetFailedSubscriptionCleanupResponse, original: FailedSubscriptionCleanupJob) {
  const job = value.job;
  return validCleanupJob(job) && job.id === original.id && job.revision >= original.revision && job.total === original.total &&
    job.deleted >= original.deleted && job.retained >= original.retained &&
    (original.state === State.PENDING || job.state === original.state) && value.results.length <= Math.min(50, job.total) &&
    new Set(value.results.map(result => result.accountId)).size === value.results.length && value.results.every(result =>
      isEntityId(result.accountId) && new TextEncoder().encode(result.alias).length <= 256 &&
      [Outcome.PENDING, Outcome.DELETED, Outcome.RETAINED].includes(result.outcome) &&
      result.reason >= Reason.UNSPECIFIED && result.reason <= Reason.UNAVAILABLE && result.problemCode.length <= 128 &&
      (result.outcome === Outcome.RETAINED ? result.reason !== Reason.UNSPECIFIED : result.reason === Reason.UNSPECIFIED && result.problemCode === ""));
}
const reasons: Record<Reason, MessageKey> = {
  [Reason.UNSPECIFIED]: "subscription-settings.cleanupReasonUnavailable",
  [Reason.CHANGED]: "subscription-settings.cleanupReasonChanged",
  [Reason.REFERENCED]: "subscription-settings.cleanupReasonReferenced",
  [Reason.CLEANUP_UNCONFIRMED]: "subscription-settings.cleanupReasonUnconfirmed",
  [Reason.INVALID_OWNERSHIP]: "subscription-settings.cleanupReasonOwnership",
  [Reason.AUTHORIZATION]: "subscription-settings.cleanupReasonAuthorization",
  [Reason.UNAVAILABLE]: "subscription-settings.cleanupReasonUnavailable",
};

export function useFailedSubscriptionCleanup(active: boolean, available: boolean, completed: () => void) {
  useLocale();
  const opening = useSettingsOpening();
  const [accepted, setAccepted] = useState<FailedSubscriptionCleanupJob>();
  const [expanded, setExpanded] = useState(false);
  const original = useRef<FailedSubscriptionCleanupJob | undefined>(undefined);
  const submitting = useRef(false);
  const completedJob = useRef("");
  const complete = useRef(completed);
  complete.current = completed;
  const mutation = useRetainedMutation("subscription:failed-cleanup", SubscriptionQuery.cleanupFailedSubscriptions, result => {
    original.current = result.job;
    submitting.current = false;
    setAccepted(result.job);
  }, (result, request) => result.requestId === request.requestId && validCleanupJob(result.job));
  const status = useQuery(SubscriptionQuery.getFailedSubscriptionCleanup, { jobId: accepted?.id ?? "", pageToken: "" }, {
    enabled: active && Boolean(accepted), retry: false, refetchOnWindowFocus: false, refetchOnReconnect: false,
    refetchInterval: query => active && accepted?.state === State.PENDING && !query.state.error &&
      (!query.state.data || validPage(query.state.data, original.current ?? accepted) && query.state.data.job?.state === State.PENDING) ? 2000 : false,
  });
  const readable = Boolean(status.data && accepted && validPage(status.data, original.current ?? accepted));
  const invalid = Boolean(status.data && !readable);
  const job = !status.error && readable ? status.data!.job! : accepted;
  useEffect(() => {
    if (opening?.disposed || !readable || status.error || !status.data?.job) return;
    original.current = status.data.job;
    setAccepted(previous => previous?.id === status.data!.job!.id && previous.revision < status.data!.job!.revision ? status.data!.job : previous);
  }, [opening, readable, status.data, status.error]);
  useEffect(() => {
    if (!mutation.busy && !mutation.uncertain && mutation.error) submitting.current = false;
  }, [mutation.busy, mutation.uncertain, mutation.error]);
  useEffect(() => {
    if (!active || opening?.disposed || !job || job.state === State.PENDING || completedJob.current === job.id) return;
    completedJob.current = job.id;
    complete.current();
  }, [active, opening, job]);
  const blocked = mutation.busy || mutation.uncertain || accepted?.state === State.PENDING;
  const canMutate = () => !submitting.current && original.current?.state !== State.PENDING && !mutation.busy && !mutation.uncertain;
  const begin = () => {
    if (!active || !available || !canMutate() || opening?.disposed) return;
    submitting.current = true;
    original.current = undefined;
    setAccepted(undefined); setExpanded(false);
    void mutation.send({ requestId: newRequestId() });
  };
  const resultsRoot = useRef<HTMLDetailsElement>(null);
  const scrollRoot = useScrollRoot(resultsRoot);
  const requestResults = useCallback((token: string) => ({ jobId: accepted?.id ?? "", pageToken: token }), [accepted?.id]);
  const projectResults = useCallback((value: GetFailedSubscriptionCleanupResponse) => {
    const owner = original.current;
    if (!owner || !validPage(value, owner)) throw new Error("Invalid original cleanup result page");
    return { rows: value.results.map(result => ({ id: result.accountId, revision: value.job!.revision, alias: result.alias, outcome: result.outcome, reason: result.reason })), nextPageToken: value.nextPageToken };
  }, []);
  const resultsReader = useConnectPaginationReader(SubscriptionQuery.getFailedSubscriptionCleanup, requestResults, projectResults);
  const resultsQuery = usePaginationChain(`subscription-cleanup-results:${accepted?.id ?? ""}`, active && expanded && Boolean(job && job.state !== State.PENDING), resultsReader);
  const results = resultsQuery.rows.filter(result => result.outcome === Outcome.RETAINED);
  const body = <>
    {mutation.busy || mutation.uncertain || job ? <div className="subscription-cleanup-status" role="status" aria-live="polite" aria-atomic="true">
      <p>{mutation.uncertain ? copy("subscription-settings.cleanupAdmissionUncertain") : mutation.busy ? copy("subscription-settings.cleanupProgress", { processed: 0, total: 0 }) :
        job?.state === State.PENDING ? copy("subscription-settings.cleanupProgress", { processed: job.processed, total: job.total }) :
        job?.state === State.FAILED ? copy("subscription-settings.cleanupFailed", { deleted: job.deleted, retained: job.retained }) : job?.total === 0 ? copy("subscription-settings.cleanupEmpty") : copy("subscription-settings.cleanupComplete", { deleted: job?.deleted, retained: job?.retained })}</p>
    </div> : null}
    <Problem error={mutation.error || status.error} summary={<p>{copy("account-connection.inline.deletion")}</p>} />
    {job && job.state !== State.PENDING && job.retained > 0 ? <InlineRemediation summary={<p>{copy("account-connection.inline.cleanupTerminal")}</p>} /> : null}
    {mutation.uncertain ? <button type="button" disabled={mutation.busy || !active} onClick={mutation.retry}>{copy("subscription-settings.cleanupRetryOriginal")}</button> : null}
    {accepted && (status.error || invalid) ? <div><p>{copy("subscription-settings.cleanupStatusUnavailable")}</p><button type="button" disabled={!active || status.isFetching} onClick={() => void status.refetch()}>{copy("subscription-settings.cleanupRetryStatus")}</button></div> : null}
    {job && job.state !== State.PENDING && job.retained > 0 ? <details ref={resultsRoot} className="subscription-cleanup-results" open={expanded} onToggle={event => setExpanded(event.currentTarget.open)}>
      <summary>{copy("subscription-settings.cleanupRetainedDetails", { count: job.retained })}</summary>
      {results.length ? <ul>{results.map(result => <li key={result.id}><strong>{result.alias}</strong> · {copy(reasons[result.reason])}</li>)}</ul> : resultsQuery.loaded && !resultsQuery.loading && !resultsQuery.error && !status.error && !invalid ? <p>{copy("subscription-settings.cleanupNoRetainedOnPage")}</p> : null}
      <ScrollContinuation query={resultsQuery} label={copy("subscription-settings.cleanupResultPages")} root={scrollRoot} active={active && expanded} />
    </details> : null}
  </>;
  return { begin, blocked, canMutate, body, busy: mutation.busy || accepted?.state === State.PENDING };
}
