// SPDX-License-Identifier: Apache-2.0
import { useId } from "react";
import { DisclosureButton, DisclosureContent, DisclosureDensity } from "./disclosure";
import { Timestamp, TimestampText } from "./timestamp-display";
import { ScrollContinuation } from "./scroll-continuation";
import { ScrollPayloadWindow } from "./scroll-payload-window";
import { useConnectPaginationReader, usePaginationChain, usePaginationRefresh } from "./scroll-pagination-query";
import { useStablePageRevisions, paginationError, invalidGitHubPage, resourceProjection, useGitHubScrollRoot, visiblePageIds } from "./github-scroll";
import { statusLabel } from "./product-status";
import { LocalizedText, copy, useLocale } from "./localization";
import { useCallback, useRef, useState } from "react";
import { IntegrationQuery, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, object, text } from "./documents";
import { useRetainedMutation } from "./mutation";
import type { PRProblemSelection } from "./pr-problems";
import { readRemediationAttempt, readRemediationChain } from "./pr-remediation-model";
import { Problem } from "./ui";
import { prAllowanceKey, prSelectionKey, usePRWorkflow } from "./pr-workflow";

type Props = { selection: PRProblemSelection; validateSet: (row: Resource) => boolean; routineRefresh?: boolean };

export function PRRemediationHistory({ selection, validateSet, routineRefresh = true }: Props) {
  useLocale();
  const { root, bindRoot } = useGitHubScrollRoot();
  const binding = useRef<string | undefined>(undefined);
  const validateBoundary = useStablePageRevisions(JSON.stringify([selection.remoteRepositoryId, selection.pullRequestId]));
  const validateCurrentSet = useRef(validateSet);
  validateCurrentSet.current = validateSet;
  const workflow = usePRWorkflow();
  const confirmationKey = prAllowanceKey(selection);
  const confirmation = workflow.confirmations.get(confirmationKey);
  const request = useCallback((token: string) => ({ remoteRepositoryId: selection.remoteRepositoryId, pullRequestId: selection.pullRequestId, pageSize: 20, pageToken: token }), [selection.remoteRepositoryId, selection.pullRequestId]);
  const project = useCallback((reply: { problemSet?: Resource; attempts: Resource[]; nextPageToken: string }, token: string) => {
    const set = reply.problemSet, rows = reply.attempts;
    if (rows.length > 20 || new Set(rows.map(row => row.id)).size !== rows.length || (set ? !(validateCurrentSet.current(set) && (document(set).remediation === undefined || Boolean(readRemediationChain(document(set).remediation))) && rows.every(row => Boolean(readRemediationAttempt(row, set))) && rows.every((row, i) => i === 0 || Number(readRemediationAttempt(rows[i - 1], set)?.sequence) > Number(readRemediationAttempt(row, set)?.sequence))) : rows.length > 0 || Boolean(reply.nextPageToken))) invalidGitHubPage();
    const identity = set ? set.id + ':' + set.revision.toString() : "";
    if (!token) binding.current = identity;
    else if (binding.current !== identity) invalidGitHubPage();
    validateBoundary(token, rows.map(resourceProjection));
    return { rows: rows.map(resourceProjection), nextPageToken: reply.nextPageToken, payload: [reply] };
  }, [validateBoundary]);
  const reader = useConnectPaginationReader(IntegrationQuery.listPullRequestRemediationAttempts, request, project);
  const traversal = usePaginationChain(JSON.stringify([selection.remoteRepositoryId, selection.pullRequestId]), true, reader);

  const history = { data: traversal.payloadPages.at(-1)?.payload[0], isFetching: Boolean(traversal.loading), isPending: !traversal.loaded && !traversal.error, error: paginationError(traversal.error?.failure), refetch: traversal.refresh };
  const refresh = traversal.refresh;
  const resume = useRetainedMutation(`pr-remediation-resume:${selection.remoteRepositoryId}:${selection.pullRequestId}`, IntegrationQuery.resumePullRequestRemediation, () => { workflow.cancelAllowance(confirmationKey); refresh(); });
  const set = history.data?.problemSet, rows = history.data?.attempts ?? [], rawChain = document(set).remediation;
  const chain = readRemediationChain(rawChain), values = set ? rows.map(row => readRemediationAttempt(row, set)) : [];
  const valid = rows.length <= 20 && new Set(rows.map(row => row.id)).size === rows.length && (set ? validateSet(set) && (rawChain === undefined || Boolean(chain)) && values.every(Boolean) : rows.length === 0 && !history.data?.nextPageToken) && values.every((value, i) => i === 0 || Number(values[i - 1]?.sequence) > Number(value?.sequence)) && (!history.data?.nextPageToken || rows.length > 0);
  const busy = history.isFetching || resume.busy || resume.uncertain;
  usePaginationRefresh(IntegrationQuery.listPullRequestRemediationAttempts, request(""), !resume.busy && !resume.uncertain && !confirmation, traversal.refresh);
  const canResume = valid && Boolean(set && chain?.limit && !chain.active_attempt_id) && !history.error;
  const stale = confirmation && (confirmation.id !== set?.id || confirmation.revision !== set.revision || !canResume);
  return <section ref={bindRoot} aria-label={copy("pr-remediation-history.prRemediationHistory_932012")}><h4>{copy("pr-remediation-history.prRemediationHistory_932012")}</h4>
    <p>{copy("pr-remediation-history.attemptsRemainRecordedAcrossSessionsAnd_bf906c")}</p>
    {routineRefresh ? <button disabled={history.isFetching} onClick={refresh}>{copy("pr-remediation-history.refreshRemediationHistory_512d6b")}</button> : null}
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
      <ScrollPayloadWindow query={traversal} root={root} active={!resume.busy && !resume.uncertain}>{(payload, projections) => payload.map(reply => { const pageSet = reply.problemSet, ids = visiblePageIds(traversal.pages, projections), pageRows = reply.attempts.filter(row => ids.has(row.id)); return <div key={pageSet?.id}>{pageRows.map((row) => { const value = readRemediationAttempt(row, pageSet!)!, policy = object(value.policy); return <article className="result" key={row.id} aria-label={copy("pr-remediation-history.remediationAttempt_0baa9c", { v0: String(value.sequence) })}><h5><LocalizedText id="pr-remediation-history.attempt_c31548" components={{ s0: <>{String(value.sequence)}</>, s1: <>{text(value.mode)}</> }} /></h5><p><LocalizedText id="pr-remediation-history.state_0ee4e7" components={{ s0: <>{statusLabel(text(value.state))}</>, s1: <>{value.outcome ? copy("pr-remediation-history.outcome_f04e83", { v0: statusLabel(text(value.outcome)) }) : ""}</> }} /></p><p><LocalizedText id="pr-remediation-history.originalProblemsSelectedLimitConflictStrategy_9ccc62" components={{ s0: <>{(value.problems as unknown[]).length}</>, s1: <>{String(policy.attempt_limit)}</>, s2: <>{text(policy.conflict_strategy)}</> }} /></p><p><LocalizedText id="pr-remediation-history.reserved_100f06" components={{ s0: <>{text(object(value.reserved).at)}</>, s1: <>{value.started_at ? <TimestampText id={"pr-remediation-history.started_331f35"} values={{ v0: <Timestamp value={text(value.started_at)} /> }} /> : ""}</>, s2: <>{value.finished_at ? <TimestampText id={"pr-remediation-history.finished_d049e3"} values={{ v0: <Timestamp value={text(value.finished_at)} /> }} /> : ""}</> }} /></p>{value.session_id ? <p><LocalizedText id="pr-remediation-history.originalSession_ffde31" components={{ s0: <code>{text(value.session_id)}</code> }} /></p> : null}{value.startup_rejection_job_id ? <p>{copy("pr-remediation-history.theOriginalInputWasRejectedBefore_d0cae9")}</p> : null}</article>; })}</div>; })}</ScrollPayloadWindow>
      <ScrollContinuation query={traversal} root={root} active={!resume.busy && !resume.uncertain && !confirmation} label={copy("pr-remediation-history.prRemediationHistory_932012")} />
    </>}
    {confirmation ? <div className="notice"><p>{copy("pr-remediation-history.allowAnotherCycleOfAutomaticAttempts_66d8c1")}</p>{stale ? <p role="alert">{copy("pr-remediation-history.thePrHistoryChangedCancelAnd_728126")}</p> : null}<button disabled={busy || Boolean(stale)} onClick={() => { if (!stale && !busy) void resume.send({ mutation: { id: confirmation.id, expectedRevision: confirmation.revision, requestId: newRequestId() } }); }}>{copy("pr-remediation-history.confirmAllowanceResumption_b26ee9")}</button><button disabled={resume.busy} onClick={() => workflow.cancelAllowance(confirmationKey)}>{copy("pr-remediation-history.cancelAllowanceResumption_efb144")}</button></div> : null}
  </section>;
}

export function OpenPRRemediationHistory(props: Props) {
  const disclosureContentId1 = useId();
  useLocale();
  const [open, setOpen] = useState(false);
  return <div><DisclosureButton aria-controls={disclosureContentId1} density={DisclosureDensity.Details} aria-expanded={open} onClick={() => setOpen(!open)}>{open ? copy("pr-remediation-history.closeRemediationAttemptHistory_1082e5") : copy("pr-remediation-history.showRemediationAttemptHistory_0b60bc")}</DisclosureButton><DisclosureContent id={disclosureContentId1} hidden={!open}>{open ? <PRRemediationHistory key={`${props.selection.remoteRepositoryId}:${props.selection.pullRequestId}`} {...props} /> : null}</DisclosureContent></div>;
}
