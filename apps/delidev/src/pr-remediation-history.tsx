import { useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { IntegrationQuery, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, object, text } from "./documents";
import { useRetainedMutation } from "./mutation";
import type { PRProblemSelection } from "./pr-problems";
import { readRemediationAttempt, readRemediationChain } from "./pr-remediation-model";
import { Problem } from "./ui";
import { prAllowanceKey, prSelectionKey, usePRWorkflow } from "./pr-workflow";

type Props = { selection: PRProblemSelection; validateSet: (row: Resource) => boolean };
const options = { retry: false, gcTime: 0, staleTime: 0, refetchOnWindowFocus: false, refetchOnReconnect: false };

export function PRRemediationHistory({ selection, validateSet }: Props) {
  const [page, setPage] = useState("");
  const workflow = usePRWorkflow();
  const confirmationKey = prAllowanceKey(selection);
  const confirmation = workflow.confirmations.get(confirmationKey);
  const history = useQuery(IntegrationQuery.listPullRequestRemediationAttempts, { remoteRepositoryId: selection.remoteRepositoryId, pullRequestId: selection.pullRequestId, pageSize: 20, pageToken: page }, options);
  const refresh = () => { if (page) setPage(""); else void history.refetch(); };
  const resume = useRetainedMutation(`pr-remediation-resume:${selection.remoteRepositoryId}:${selection.pullRequestId}`, IntegrationQuery.resumePullRequestRemediation, () => { workflow.cancelAllowance(confirmationKey); refresh(); });
  const set = history.data?.problemSet, rows = history.data?.attempts ?? [], rawChain = document(set).remediation;
  const chain = readRemediationChain(rawChain), values = set ? rows.map(row => readRemediationAttempt(row, set)) : [];
  const valid = rows.length <= 20 && new Set(rows.map(row => row.id)).size === rows.length && (set ? validateSet(set) && (rawChain === undefined || Boolean(chain)) && values.every(Boolean) : rows.length === 0 && !history.data?.nextPageToken) && values.every((value, i) => i === 0 || Number(values[i - 1]?.sequence) > Number(value?.sequence)) && (!history.data?.nextPageToken || rows.length > 0);
  const busy = history.isFetching || resume.busy || resume.uncertain;
  const canResume = valid && Boolean(set && chain?.limit && !chain.active_attempt_id) && !history.error;
  const stale = confirmation && (confirmation.id !== set?.id || confirmation.revision !== set.revision || !canResume);
  return <section aria-label="PR remediation history"><h4>PR remediation history</h4>
    <p>Attempts remain recorded across sessions and PR updates. A completed attempt alone does not prove a push or mark feedback handled.</p>
    <button disabled={history.isFetching} onClick={refresh}>Refresh remediation history</button>
    <Problem error={history.error || resume.error} />
    {resume.uncertain ? <button disabled={resume.busy} onClick={resume.retry}>Retry original allowance resumption</button> : null}
    {history.isPending ? <p role="status">Reading remediation attempts…</p> : !valid ? <p role="alert">This remediation page does not match its original PR history. Refresh before taking an action.</p> : <>
      {history.error ? <p>Previous attempt history is shown; refresh failed.</p> : null}
      {chain ? <div><p>Recorded attempts: {String(chain.sequence)} · Lifetime automatic attempts: {String(chain.automatic_attempts)} · Since last resumption: {Number(chain.automatic_attempts) - Number(chain.resume_baseline)}</p>
        {chain.limit ? <p className="notice">Automatic attempt limit reached: {String(object(chain.limit).attempts)} of {String(object(chain.limit).limit)}. Explicit resumption or a higher policy limit is required.</p> : null}
        {chain.last_resume ? <p>Last explicit resumption: {text(object(chain.last_resume).at)}</p> : null}
        {chain.active_attempt_id ? <p>An active or uncertain attempt still owns this PR.</p> : null}
      </div> : <p>No remediation chain has been started for this PR.</p>}
      {canResume && !confirmation ? <button disabled={busy} onClick={() => workflow.confirmAllowance({ key: confirmationKey, selection, id: set!.id, revision: set!.revision })}>Resume automatic attempt allowance</button> : null}
      {rows.map((row, index) => { const value = values[index]!, policy = object(value.policy); return <article className="result" key={row.id} aria-label={`Remediation attempt ${String(value.sequence)}`}><h5>Attempt {String(value.sequence)} · {text(value.mode)}</h5><p>State: {text(value.state)}{value.outcome ? ` · Outcome: ${text(value.outcome)}` : ""}</p><p>Original problems: {(value.problems as unknown[]).length} · Selected limit: {String(policy.attempt_limit)} · Conflict strategy: {text(policy.conflict_strategy)}</p><p>Reserved: {text(object(value.reserved).at)}{value.started_at ? ` · Started: ${text(value.started_at)}` : ""}{value.finished_at ? ` · Finished: ${text(value.finished_at)}` : ""}</p>{value.session_id ? <p>Original session: <code>{text(value.session_id)}</code></p> : null}{value.startup_rejection_job_id ? <p>The original input was rejected before the agent started; its charged attempt remains in history.</p> : null}</article>; })}
      {history.data?.nextPageToken ? <button disabled={busy} onClick={() => setPage(history.data!.nextPageToken)}>Next remediation page</button> : null}
      {page ? <button disabled={history.isFetching} onClick={() => setPage("")}>Restart remediation pages</button> : null}
    </>}
    {confirmation ? <div className="notice"><p>Allow another cycle of automatic attempts while keeping all past attempts. This does not resume paused or archived sessions, and does not start an agent by itself.</p>{stale ? <p role="alert">The PR history changed. Cancel and review its current state first.</p> : null}<button disabled={busy || Boolean(stale)} onClick={() => { if (!stale && !busy) void resume.send({ mutation: { id: confirmation.id, expectedRevision: confirmation.revision, requestId: newRequestId() } }); }}>Confirm allowance resumption</button><button disabled={resume.busy} onClick={() => workflow.cancelAllowance(confirmationKey)}>Cancel allowance resumption</button></div> : null}
  </section>;
}

export function OpenPRRemediationHistory(props: Props) {
  const [open, setOpen] = useState(false);
  return <div><button aria-expanded={open} onClick={() => setOpen(!open)}>{open ? "Close remediation attempt history" : "Show remediation attempt history"}</button>{open ? <PRRemediationHistory key={`${props.selection.remoteRepositoryId}:${props.selection.pullRequestId}`} {...props} /> : null}</div>;
}
