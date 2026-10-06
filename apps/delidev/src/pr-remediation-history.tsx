import { formatTimestamp } from "./localization";
import { statusLabel } from "./product-status";
import { LocalizedText, copy, useLocale } from "./localization";
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
  useLocale();
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
  return <section aria-label={copy("pr-remediation-history.prRemediationHistory_932012")}><h4>{copy("pr-remediation-history.prRemediationHistory_932012")}</h4>
    <p>{copy("pr-remediation-history.attemptsRemainRecordedAcrossSessionsAnd_bf906c")}</p>
    <button disabled={history.isFetching} onClick={refresh}>{copy("pr-remediation-history.refreshRemediationHistory_512d6b")}</button>
    <Problem error={history.error || resume.error} />
    {resume.uncertain ? <button disabled={resume.busy} onClick={resume.retry}>{copy("pr-remediation-history.retryOriginalAllowanceResumption_991985")}</button> : null}
    {history.isPending ? <p role="status">{copy("pr-remediation-history.readingRemediationAttempts_2d6438")}</p> : !valid ? <p role="alert">{copy("pr-remediation-history.thisRemediationPageDoesNotMatch_b7c6c3")}</p> : <>
      {history.error ? <p>{copy("pr-remediation-history.previousAttemptHistoryIsShownRefresh_8a7eae")}</p> : null}
      {chain ? <div><p><LocalizedText id="pr-remediation-history.recordedAttemptsLifetimeAutomaticAttemptsSince_9c58b9" components={{ s0: <>{String(chain.sequence)}</>, s1: <>{String(chain.automatic_attempts)}</>, s2: <>{Number(chain.automatic_attempts) - Number(chain.resume_baseline)}</> }} /></p>
        {chain.limit ? <p className="notice"><LocalizedText id="pr-remediation-history.automaticAttemptLimitReachedOfExplicit_56b24c" components={{ s0: <>{String(object(chain.limit).attempts)}</>, s1: <>{String(object(chain.limit).limit)}</> }} /></p> : null}
        {chain.last_resume ? <p><LocalizedText id="pr-remediation-history.lastExplicitResumption_609b7e" components={{ s0: <>{text(object(chain.last_resume).at)}</> }} /></p> : null}
        {chain.active_attempt_id ? <p>{copy("pr-remediation-history.anActiveOrUncertainAttemptStill_618805")}</p> : null}
      </div> : <p>{copy("pr-remediation-history.noRemediationChainHasBeenStarted_a87f59")}</p>}
      {canResume && !confirmation ? <button disabled={busy} onClick={() => workflow.confirmAllowance({ key: confirmationKey, selection, id: set!.id, revision: set!.revision })}>{copy("pr-remediation-history.resumeAutomaticAttemptAllowance_e69c94")}</button> : null}
      {rows.map((row, index) => { const value = values[index]!, policy = object(value.policy); return <article className="result" key={row.id} aria-label={copy("pr-remediation-history.remediationAttempt_0baa9c", { v0: String(value.sequence) })}><h5><LocalizedText id="pr-remediation-history.attempt_c31548" components={{ s0: <>{String(value.sequence)}</>, s1: <>{text(value.mode)}</> }} /></h5><p><LocalizedText id="pr-remediation-history.state_0ee4e7" components={{ s0: <>{statusLabel(text(value.state))}</>, s1: <>{value.outcome ? copy("pr-remediation-history.outcome_f04e83", { v0: statusLabel(text(value.outcome)) }) : ""}</> }} /></p><p><LocalizedText id="pr-remediation-history.originalProblemsSelectedLimitConflictStrategy_9ccc62" components={{ s0: <>{(value.problems as unknown[]).length}</>, s1: <>{String(policy.attempt_limit)}</>, s2: <>{text(policy.conflict_strategy)}</> }} /></p><p><LocalizedText id="pr-remediation-history.reserved_100f06" components={{ s0: <>{text(object(value.reserved).at)}</>, s1: <>{value.started_at ? copy("pr-remediation-history.started_331f35", { v0: formatTimestamp(text(value.started_at)) }) : ""}</>, s2: <>{value.finished_at ? copy("pr-remediation-history.finished_d049e3", { v0: formatTimestamp(text(value.finished_at)) }) : ""}</> }} /></p>{value.session_id ? <p><LocalizedText id="pr-remediation-history.originalSession_ffde31" components={{ s0: <code>{text(value.session_id)}</code> }} /></p> : null}{value.startup_rejection_job_id ? <p>{copy("pr-remediation-history.theOriginalInputWasRejectedBefore_d0cae9")}</p> : null}</article>; })}
      {history.data?.nextPageToken ? <button disabled={busy} onClick={() => setPage(history.data!.nextPageToken)}>{copy("pr-remediation-history.nextRemediationPage_73e603")}</button> : null}
      {page ? <button disabled={history.isFetching} onClick={() => setPage("")}>{copy("pr-remediation-history.restartRemediationPages_04aa5a")}</button> : null}
    </>}
    {confirmation ? <div className="notice"><p>{copy("pr-remediation-history.allowAnotherCycleOfAutomaticAttempts_66d8c1")}</p>{stale ? <p role="alert">{copy("pr-remediation-history.thePrHistoryChangedCancelAnd_728126")}</p> : null}<button disabled={busy || Boolean(stale)} onClick={() => { if (!stale && !busy) void resume.send({ mutation: { id: confirmation.id, expectedRevision: confirmation.revision, requestId: newRequestId() } }); }}>{copy("pr-remediation-history.confirmAllowanceResumption_b26ee9")}</button><button disabled={resume.busy} onClick={() => workflow.cancelAllowance(confirmationKey)}>{copy("pr-remediation-history.cancelAllowanceResumption_efb144")}</button></div> : null}
  </section>;
}

export function OpenPRRemediationHistory(props: Props) {
  useLocale();
  const [open, setOpen] = useState(false);
  return <div><button aria-expanded={open} onClick={() => setOpen(!open)}>{open ? copy("pr-remediation-history.closeRemediationAttemptHistory_1082e5") : copy("pr-remediation-history.showRemediationAttemptHistory_0b60bc")}</button>{open ? <PRRemediationHistory key={`${props.selection.remoteRepositoryId}:${props.selection.pullRequestId}`} {...props} /> : null}</div>;
}
