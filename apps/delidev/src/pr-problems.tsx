import { formatTimestamp } from "./localization";
import { LocalizedText, copy, useLocale } from "./localization";
import { useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { EntityKind, IntegrationQuery, ResourceQuery, PullRequestProblemCollectionKind, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, object, text, type Document } from "./documents";
import { bounded, date, positive, sha, uuid } from "./github-query-model";
import { useRetainedMutation } from "./mutation";
import { selectedCIRollup } from "./github-ci-queue";
import { CIOriginalEvidence, PRCI, validCIContext, validPRCI } from "./github-ci";
import { Problem } from "./ui";
import { prSelectionKey, usePRWorkflow } from "./pr-workflow";
import { PRFixAction } from "./pr-fix";
import { OpenPRRemediationHistory } from "./pr-remediation-history";

enum LocalState { Unhandled = "unhandled", Dismissed = "locally-dismissed", Handled = "handled" }
export type PRProblemSelection = { repositoryId: string; remoteRepositoryId: string; pullRequestId: string; number: string };
const options = { retry: false, gcTime: 0, staleTime: 0, refetchOnWindowFocus: false, refetchOnReconnect: false };
const envelope = (row: Resource) => row.schemaVersion === 1 && row.kind === EntityKind.PROBLEM && uuid(row.id) && row.revision > 0n && row.revision < 1n << 63n && !row.sessionId && !row.projectId && row.documentJson.byteLength <= 1 << 20;
function targetValid(target: Document, selection: PRProblemSelection) {
  return target.version === 1 && target.provider === "github.com" && uuid(target.repository_id) && target.remote_repository_id === selection.remoteRepositoryId && target.pull_request_id === selection.pullRequestId && target.number === selection.number && positive(target.remote_repository_id) && positive(target.pull_request_id) && positive(target.number) && bounded(target.repository_node_id, 256) && bounded(target.pull_request_node_id, 256) && bounded(target.title, 4096) && date(target.observed_at) && /^[A-Za-z0-9](?:[A-Za-z0-9-]{0,98}[A-Za-z0-9])?$/.test(text(target.owner)) && /^[A-Za-z0-9_.-]{1,100}$/.test(text(target.name)) && ![".", ".."].includes(text(target.name));
}
function observationValid(value: Document) { return sha(value.base_sha) && sha(value.head_sha) && date(value.observed_at); }
function conflictSnapshotValid(value: Document) { return uuid(value.transition_id) && bounded(value.base_ref, 1024) && bounded(value.head_ref, 1024) && observationValid(object(value.observation)); }
function localDecisionValid(value: Document) {
 const dismissal = object(value.dismissal), handling = object(value.handling);
 if (value.state === LocalState.Handled) return value.dismissal == null && uuid(handling.attempt_id) && uuid(handling.execution_id) && sha(handling.pushed_head) && date(handling.at);
 if (value.handling != null) return false;
 return value.state === LocalState.Unhandled ? value.dismissal == null : value.state === LocalState.Dismissed && uuid(dismissal.request_id) && date(dismissal.at) && (dismissal.actor_type === "client" ? uuid(dismissal.device_id) : dismissal.actor_type === "owner" && dismissal.device_id == null);
}
export function readPRProblemSet(row: Resource, selection: PRProblemSelection): Document | undefined {
 const value = document(row), ci = object(value.ci), conflict = object(value.conflict);
 if (!envelope(row) || value.version !== 1 || value.type !== "pull-request-set" || !targetValid(object(value.target), selection) || (value.feedback == null && value.ci == null && value.conflict == null)) return;
 if (value.feedback != null && !observationValid(object(value.feedback))) return;
 if (value.ci != null && (!observationValid(object(ci.observation)) || !["unknown", "missing", "pending", "non-failing", "terminal-failure", "not-required"].includes(text(ci.state)) || !["head", "test-merge", "merge-queue", "unknown"].includes(text(ci.source)) || !/^[a-f0-9]{64}$/.test(text(ci.rules_digest)) || (ci.source === "unknown" ? ci.evaluated_sha != null || ci.state !== "unknown" : !sha(ci.evaluated_sha)))) return;
 if (value.conflict != null && (!observationValid(object(conflict.observation)) || !bounded(conflict.base_ref, 1024) || !bounded(conflict.head_ref, 1024) || !["unknown", "mergeable", "conflicting", "not-applicable"].includes(text(conflict.state)) || !["open", "closed"].includes(text(conflict.pull_request_state)) || typeof conflict.merged !== "boolean" || (conflict.mergeable != null && typeof conflict.mergeable !== "boolean") || (conflict.active != null && !conflictSnapshotValid(object(conflict.active))))) return;
 return value;
}
export function readPRProblem(row: Resource, set: Resource, selection: PRProblemSelection): Document | undefined {
  const value = document(row), target = object(value.target), feedback = object(value.feedback), code = object(feedback.code), dismissal = object(value.dismissal);
  if (!envelope(row) || value.version !== 1 || value.type !== "pull-request-evidence" || value.set_id !== set.id || !targetValid(target, selection) || !observationValid(object(value.observation)) || typeof value.current !== "boolean" || !localDecisionValid(value) || !/^[a-f0-9]{64}$/.test(text(value.content_version))) return;
  if (value.kind === "ci-failure") {
    const ci = object(value.ci), context = object(ci.context);
    const terminal = context.kind === "check-run" ? context.native_status === "COMPLETED" && ["FAILURE", "CANCELLED", "TIMED_OUT", "ACTION_REQUIRED", "STALE", "STARTUP_FAILURE"].includes(text(context.native_conclusion)) : context.kind === "commit-status" && ["FAILURE", "ERROR"].includes(text(context.native_status));
    if (value.feedback != null || value.original_provider != null || value.latest_provider != null || value.conflict != null || !uuid(ci.observation_id) || !validCIContext(context) || context.required !== true || !terminal || !["head", "test-merge", "merge-queue"].includes(text(ci.source)) || !/^[a-f0-9]{64}$/.test(text(ci.rules_digest))) return;
    if (ci.source === "merge-queue" ? !bounded(ci.queue_node_id, 256) || !bounded(ci.queue_entry_node_id, 256) : ci.queue_node_id != null || ci.queue_entry_node_id != null) return;
    return value;
  }
  if (value.kind === "merge-conflict") {
    const conflict = object(value.conflict), original = object(conflict.observation), observed = object(value.observation);
    if (value.feedback != null || value.original_provider != null || value.latest_provider != null || value.ci != null || !conflictSnapshotValid(conflict) || original.base_sha !== observed.base_sha || original.head_sha !== observed.head_sha || original.observed_at !== observed.observed_at) return;
    return value;
  }
  if (value.ci != null || value.conflict != null) return;
  if (!envelope(row) || value.version !== 1 || value.type !== "pull-request-evidence" || value.kind !== "review-feedback" || value.set_id !== set.id || !targetValid(target, selection) || !observationValid(object(value.observation)) || typeof value.current !== "boolean" || !Object.values(LocalState).includes(value.state as LocalState) || !/^[a-f0-9]{64}$/.test(text(value.content_version)) || value.content_version !== feedback.content_version || !bounded(feedback.body, 64 << 10, false) || !bounded(feedback.node_id, 256) || !positive(feedback.id) || !date(feedback.published_at) || (feedback.last_edited_at != null && !date(feedback.last_edited_at))) return;
  const fragments = new Map([["review", "pullrequestreview-"], ["review-comment", "discussion_r"], ["conversation-comment", "issuecomment-"]]);
  const fragment = fragments.get(text(feedback.kind));
  if (!fragment || feedback.url !== `https://github.com/${text(target.owner)}/${text(target.name)}/pull/${selection.number}#${fragment}${text(feedback.id)}`) return;
  if (feedback.kind === "review-comment" && (!bounded(code.path, 4096) || !bounded(code.diff_hunk, 64 << 10, false) || !bounded(feedback.thread_node_id, 256) || !bounded(feedback.review_node_id, 256))) return;
  if (feedback.kind !== "review-comment" && feedback.code != null) return;
  const published = (raw: unknown) => ["APPROVED", "COMMENTED", "CHANGES_REQUESTED", "DISMISSED"].includes(text(raw));
  for (const raw of [value.original_provider, value.latest_provider]) {
    const state = object(raw);
    if (typeof state.author_present !== "boolean") return;
    if (feedback.kind === "review" && (!published(state.native_state) || state.review_state != null || state.thread_resolved != null || state.thread_outdated != null)) return;
    if (feedback.kind === "conversation-comment" && [state.native_state, state.review_state, state.thread_resolved, state.thread_outdated].some(field => field != null)) return;
    if (feedback.kind === "review-comment" && (state.native_state !== "SUBMITTED" || !published(state.review_state) || typeof state.thread_resolved !== "boolean" || typeof state.thread_outdated !== "boolean")) return;
  }
  if (value.state === LocalState.Dismissed) {
    if (!uuid(dismissal.request_id) || !date(dismissal.at) || (dismissal.actor_type === "client" ? !uuid(dismissal.device_id) : dismissal.actor_type !== "owner" || dismissal.device_id != null)) return;
  } else if (value.dismissal != null) return;
  return value;
}
function sameJSON(left: unknown, right: unknown) {
  const pending: [unknown, unknown][] = [[left, right]];
  let visited = 0;
  while (pending.length) {
    if (++visited > 2048) return false;
    const [a, b] = pending.pop()!;
    if (a === b) continue;
    if (a == null || b == null || typeof a !== "object" || typeof b !== "object" || Array.isArray(a) !== Array.isArray(b)) return false;
    const aKeys = Object.keys(a), bKeys = Object.keys(b);
    if (aKeys.length !== bKeys.length) return false;
    for (const key of aKeys) {
      if (!Object.hasOwn(b, key)) return false;
      pending.push([(a as Document)[key], (b as Document)[key]]);
    }
  }
  return true;
}
function OriginalCIProofRead({ value, selection }: { value: Document; selection: PRProblemSelection }) {
  useLocale();
  const ci = object(value.ci), context = object(ci.context);
  const query = useQuery(ResourceQuery.getResource, { kind: EntityKind.PROBLEM, id: text(ci.observation_id) }, options);
  const row = query.data?.resource, proof = document(row), observation = object(proof.observation), evaluation = object(proof.ci), result = object(evaluation.result);
  const item = { node_id: object(proof.target).pull_request_node_id, number: object(proof.target).number, base_ref: proof.base_ref, head_ref: proof.head_ref, base_sha: observation.base_sha, head_sha: observation.head_sha, state: proof.pull_request_state, merged: proof.merged, mergeable: proof.mergeable };
  const selected = selectedCIRollup(evaluation);
  const contexts = Array.isArray(selected.contexts) ? selected.contexts as Document[] : [];
  const matches = contexts.find(candidate => candidate.node_id === context.node_id);
  const valid = Boolean(row && envelope(row) && row.id === ci.observation_id && proof.version === 1 && proof.type === "pull-request-ci-observation" && proof.set_id === value.set_id && targetValid(object(proof.target), selection) && observationValid(observation) && sameJSON(proof.observation, value.observation) && validPRCI(evaluation, item) && result.state === "terminal-failure" && result.source === ci.source && object(evaluation.rules).digest === ci.rules_digest && (ci.source !== "merge-queue" || (object(evaluation.merge_queue).node_id === ci.queue_node_id && object(object(evaluation.merge_queue).entry).node_id === ci.queue_entry_node_id && object(evaluation.merge_queue).repository_node_id === object(proof.target).repository_node_id)) && matches && sameJSON(matches, context));
  return <><Problem error={query.error} />{query.isPending ? <p>{copy("pr-problems.readingOriginalCiEvaluation_d6ccc5")}</p> : !valid ? <p role="alert">{copy("pr-problems.theOriginalCiProofDoesNot_2583b5")}</p> : <PRCI value={evaluation} historical />}</>;
}
function OriginalCIProof({ value, selection }: { value: Document; selection: PRProblemSelection }) {
  useLocale();
  const [open, setOpen] = useState(false), ci = object(value.ci), context = object(ci.context);
  return <section><p><LocalizedText id="pr-problems.originalEvaluatedCommitRulesDigest_ebddce" components={{ s0: <>{text(ci.source)}</>, s1: <code>{text(context.commit_sha)}</code>, s2: <code>{text(ci.rules_digest)}</code> }} /></p><CIOriginalEvidence row={context} /><button aria-expanded={open} onClick={() => setOpen(!open)}>{open ? copy("pr-problems.closeOriginalCiEvaluation_b284c7") : copy("pr-problems.inspectOriginalCiRulesAndResults_f03f99")}</button>{open ? <OriginalCIProofRead value={value} selection={selection} /> : null}</section>;
}

function ProblemRow({ row, set, value, selection, disabled, refreshed }: { row: Resource; set: Resource; value: Document; selection: PRProblemSelection; disabled: boolean; refreshed: () => void }) {
  useLocale();
  const feedback = object(value.feedback), latest = object(value.latest_provider), original = object(value.original_provider), observation = object(value.observation), dismissal = object(value.dismissal), handling = object(value.handling), code = object(feedback.code), ci = object(value.ci), conflict = object(value.conflict);
  const title = value.kind === "ci-failure" ? copy("pr-problems.sentence.6c6da9a2aa0e", { v0: text(object(ci.context).name) }) : value.kind === "merge-conflict" ? copy("pr-problems.sentence.9c334bded783", { v0: text(conflict.head_ref), v1: text(conflict.base_ref) }) : `${text(feedback.kind)} · ${text(object(feedback.author).login) || copy("pr-problems.extra.a326f4758492")}`;
  const dismiss = useRetainedMutation(`pr-problem-dismiss:${row.id}`, IntegrationQuery.dismissPullRequestProblem, refreshed);
  return <article className="result" aria-label={title}><h5>{title}</h5>
    <p><LocalizedText id="pr-problems.localHandling_dd5bec" components={{ s0: <>{value.state === LocalState.Dismissed ? copy("pr-problems.locallyDismissed_a1413e") : value.state === LocalState.Handled ? copy("pr-problems.handledAfterVerifiedPush_130023") : copy("pr-problems.unhandled_3c7095")}</>, s1: <>{value.current ? copy("pr-problems.presentInLatestCompleteCollection_b2c2fa") : copy("pr-problems.retainedEarlierVersionOrAbsentFrom_6ca81d")}</> }} /></p>
    {value.kind === "review-feedback" ? <><p><LocalizedText id="pr-problems.originalProviderStateLatestObservedProvider_402415" components={{ s0: <>{text(original.native_state) || copy("pr-problems.extra.81e3ec48dfc5")}</>, s1: <>{original.review_state ? copy("pr-problems.review_741980", { v0: text(original.review_state) }) : ""}</>, s2: <>{text(latest.native_state) || copy("pr-problems.extra.81e3ec48dfc5")}</>, s3: <>{latest.review_state ? copy("pr-problems.review_741980", { v0: text(latest.review_state) }) : ""}</> }} /></p>
      {typeof latest.thread_resolved === "boolean" ? <p><LocalizedText id="pr-problems.latestThreadObservationThisDoesNot_ddf132" components={{ s0: <>{latest.thread_resolved ? copy("pr-problems.resolved_dc676b") : copy("pr-problems.unresolved_27a888")}</>, s1: <>{latest.thread_outdated ? copy("pr-problems.outdatedCodePosition_560908") : ""}</> }} /></p> : null}
      <pre>{text(feedback.body) || copy("pr-problems.extra.9ead3dff0264")}</pre>
      {feedback.code != null ? <details><summary><LocalizedText id="pr-problems.originalCodeContext_951001" components={{ s0: <>{text(code.path)}</> }} /></summary><p><LocalizedText id="pr-problems.originalLine_d04246" components={{ s0: <>{code.original_line == null ? copy("pr-problems.unavailable_ca1844") : String(code.original_line)}</> }} /></p><pre>{text(code.diff_hunk)}</pre></details> : null}
      <details><summary>{copy("pr-problems.originalFeedbackIdentity_bdf0a2")}</summary><p><LocalizedText id="pr-problems.source_590a7b" components={{ s0: <code>{text(feedback.url)}</code> }} /></p><p><LocalizedText id="pr-problems.providerIdNodeAuthorId_ec9614" components={{ s0: <>{text(feedback.id)}</>, s1: <code>{text(feedback.node_id)}</code>, s2: <>{text(object(feedback.author).id) || copy("pr-problems.extra.ca1844969742")}</> }} /></p><p><LocalizedText id="pr-problems.published_09e232" components={{ s0: <>{formatTimestamp(text(feedback.published_at))}</>, s1: <>{feedback.last_edited_at ? copy("pr-problems.edited_01b606", { v0: formatTimestamp(text(feedback.last_edited_at)) }) : ""}</> }} /></p></details></> : value.kind === "ci-failure" ? <OriginalCIProof value={value} selection={selection} /> : <p><LocalizedText id="pr-problems.verifiedConflictTransitionANewConfirmed_e5bb28" components={{ s0: <code>{text(conflict.transition_id)}</code> }} /></p>}
    <details><summary>{copy("pr-problems.originalProblemProvenance_c4f1fe")}</summary><p><LocalizedText id="pr-problems.contentVersion_0bc2d3" components={{ s0: <code>{text(value.content_version)}</code> }} /></p><p><LocalizedText id="pr-problems.firstObservedHeadBase_aae0e2" components={{ s0: <>{formatTimestamp(text(observation.observed_at))}</>, s1: <code>{text(observation.head_sha)}</code>, s2: <code>{text(observation.base_sha)}</code> }} /></p><p><LocalizedText id="pr-problems.problemRevision_f77cf7" components={{ s0: <>{row.id}</>, s1: <>{row.revision.toString()}</> }} /></p></details>
    {value.state === LocalState.Handled ? <p><LocalizedText id="pr-problems.verifiedPushAttemptExecutionHandledAt_358d0e" components={{ s0: <code>{text(handling.pushed_head)}</code>, s1: <code>{text(handling.attempt_id)}</code>, s2: <code>{text(handling.execution_id)}</code>, s3: <time dateTime={text(handling.at)}>{text(handling.at)}</time> }} /></p> : value.state === LocalState.Dismissed ? <p><LocalizedText id="pr-problems.dismissedLocallyAtByRequest_7a1f5b" components={{ s0: <>{text(dismissal.at)}</>, s1: <>{text(dismissal.actor_type)}</>, s2: <>{dismissal.device_id ? copy("pr-problems.message_a4e9a4", { v0: text(dismissal.device_id) }) : ""}</>, s3: <>{text(dismissal.request_id)}</> }} /></p> : <button disabled={disabled || dismiss.busy || dismiss.uncertain} onClick={() => void dismiss.send({ mutation: { id: row.id, expectedRevision: row.revision, requestId: newRequestId() }, contentVersion: text(value.content_version) })}>{copy("pr-problems.dismissThisContentVersion_2603b7")}</button>}
    {value.state === LocalState.Unhandled ? <PRFixAction row={row} set={set} value={value} selection={selection} disabled={disabled || dismiss.busy || dismiss.uncertain} refreshed={refreshed} /> : null}
    <Problem error={dismiss.error} />{dismiss.uncertain ? <button disabled={dismiss.busy} onClick={dismiss.retry}>{copy("pr-problems.retryOriginalDismissal_bb2de0")}</button> : null}
  </article>;
}
export function PRProblemHistory({ selection }: { selection: PRProblemSelection }) {
  useLocale();
  const [page, setPage] = useState("");
  const workflow = usePRWorkflow();
  const collectionKey = prSelectionKey(selection);
  const kind = workflow.collectionKinds.get(collectionKey) ?? PullRequestProblemCollectionKind.FEEDBACK;
  const history = useQuery(IntegrationQuery.listPullRequestProblems, { remoteRepositoryId: selection.remoteRepositoryId, pullRequestId: selection.pullRequestId, pageSize: 20, pageToken: page }, options);
  const refreshed = () => { if (page) setPage(""); else void history.refetch(); };
  const collect = useRetainedMutation(`pr-problem-refresh:${selection.repositoryId}:${selection.number}`, IntegrationQuery.refreshPullRequestProblems, refreshed);
  const set = history.data?.problemSet, rows = history.data?.problems ?? [];
  const summary = document(set), latestCI = object(summary.ci), latestConflict = object(summary.conflict);
  const values = set ? rows.map(row => readPRProblem(row, set, selection)) : [];
  const valid = rows.length <= 20 && new Set(rows.map(row => row.id)).size === rows.length && (set ? Boolean(readPRProblemSet(set, selection)) && values.every(Boolean) : !rows.length && !history.data?.nextPageToken) && (!history.data?.nextPageToken || rows.length > 0);
  const busy = collect.busy || collect.uncertain || history.isFetching;
  return <section aria-label={copy("pr-problems.retainedPrProblems_cf443b")}><h4>{copy("pr-problems.retainedPrProblems_cf443b")}</h4><p>{copy("pr-problems.originalFeedbackRequiredCiFailuresAnd_cfc64c")}</p>
    <label>{copy("pr-problems.problemCollectionKind_8e764b")}<select disabled={busy} value={kind} onChange={event => workflow.setCollectionKind(collectionKey, Number(event.target.value) as PullRequestProblemCollectionKind)}><option value={PullRequestProblemCollectionKind.FEEDBACK}>{copy("pr-problems.publishedFeedback_12d23d")}</option><option value={PullRequestProblemCollectionKind.CI}>{copy("pr-problems.requiredCi_5cd645")}</option><option value={PullRequestProblemCollectionKind.CONFLICT}>{copy("pr-problems.mergeConflict_d6b6f5")}</option></select></label><button disabled={busy} onClick={() => void collect.send({ repositoryId: selection.repositoryId, number: selection.number, requestId: newRequestId(), kind })}>{copy("pr-problems.collectSelectedPrProblems_224d57")}</button><button disabled={history.isFetching} onClick={refreshed}>{copy("pr-problems.refreshRetainedHistory_f50db9")}</button>
    <Problem error={collect.error || history.error} />{collect.uncertain ? <button disabled={collect.busy} onClick={collect.retry}>{copy("pr-problems.retryOriginalProblemCollection_31fab2")}</button> : null}
    {history.isPending ? <p role="status">{copy("pr-problems.readingRetainedPrProblems_b5a9b7")}</p> : !valid ? <p role="alert">{copy("pr-problems.theRetainedProblemPageIsInconsistent_6cec39")}</p> : <>{history.error ? <p>{copy("pr-problems.previousRetainedHistoryIsShownRefresh_37aacd")}</p> : null}{set ? <div><p><LocalizedText id="pr-problems.inventoryRevision_c7429c" components={{ s0: <>{set.revision.toString()}</> }} /></p>{summary.feedback != null ? <p><LocalizedText id="pr-problems.latestFeedbackCollection_43b0c1" components={{ s0: <>{formatTimestamp(text(object(summary.feedback).observed_at))}</> }} /></p> : null}{summary.ci != null ? <p><LocalizedText id="pr-problems.latestCiEvaluation_7dc33a" components={{ s0: <>{text(latestCI.state)}</>, s1: <>{text(latestCI.reason)}</>, s2: <>{formatTimestamp(text(object(latestCI.observation).observed_at))}</> }} /></p> : null}{summary.conflict != null ? <p><LocalizedText id="pr-problems.latestMergeability_2446bf" components={{ s0: <>{text(latestConflict.state)}</>, s1: <>{formatTimestamp(text(object(latestConflict.observation).observed_at))}</> }} /></p> : null}</div> : <p>{copy("pr-problems.noProblemsHaveBeenCollectedFor_2e8578")}</p>}{rows.map((row, index) => <ProblemRow key={row.id} row={row} set={set!} value={values[index]!} selection={selection} disabled={busy || Boolean(history.error)} refreshed={refreshed} />)}{set && !rows.length ? <p>{copy("pr-problems.noRetainedProblemsOnThisPage_c6abc5")}</p> : null}</>}
    <nav aria-label={copy("pr-problems.retainedProblemPages_3c24f7")}><button disabled={!page || busy} onClick={() => setPage("")}>{copy("pr-problems.firstProblemPage_cdd66d")}</button><button disabled={!valid || !history.data?.nextPageToken || busy || Boolean(history.error)} onClick={() => setPage(history.data!.nextPageToken)}>{copy("pr-problems.nextProblemPage_de4c68")}</button></nav>
  </section>;
}
export function OpenPRProblemHistory({ selection }: { selection: PRProblemSelection }) {
  useLocale();
  const [open, setOpen] = useState(false);
  return <div><OpenPRRemediationHistory selection={selection} validateSet={row => Boolean(readPRProblemSet(row, selection))} /><button aria-expanded={open} onClick={() => setOpen(!open)}>{open ? copy("pr-problems.closeRetainedPrProblems_3f8091") : copy("pr-problems.showRetainedPrProblems_0758db")}</button>{open ? <PRProblemHistory key={`${selection.repositoryId}:${selection.remoteRepositoryId}:${selection.pullRequestId}`} selection={selection} /> : null}</div>;
}
