import { useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { EntityKind, IntegrationQuery, ResourceQuery, PullRequestProblemCollectionKind, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, object, text, type Document } from "./documents";
import { bounded, date, positive, sha, uuid } from "./github-query-model";
import { useRetainedMutation } from "./mutation";
import { CIOriginalEvidence, PRCI, validCIContext, validPRCI } from "./github-ci";
import { Problem } from "./ui";

enum LocalState { Unhandled = "unhandled", Dismissed = "locally-dismissed" }
export type PRProblemSelection = { repositoryId: string; remoteRepositoryId: string; pullRequestId: string; number: string };
const options = { retry: false, gcTime: 0, staleTime: 0, refetchOnWindowFocus: false, refetchOnReconnect: false };
const envelope = (row: Resource) => row.schemaVersion === 1 && row.kind === EntityKind.PROBLEM && uuid(row.id) && row.revision > 0n && row.revision < 1n << 63n && !row.sessionId && !row.projectId && row.documentJson.byteLength <= 1 << 20;
function targetValid(target: Document, selection: PRProblemSelection) {
  return target.version === 1 && target.provider === "github.com" && uuid(target.repository_id) && target.remote_repository_id === selection.remoteRepositoryId && target.pull_request_id === selection.pullRequestId && target.number === selection.number && positive(target.remote_repository_id) && positive(target.pull_request_id) && positive(target.number) && bounded(target.repository_node_id, 256) && bounded(target.pull_request_node_id, 256) && bounded(target.title, 4096) && date(target.observed_at) && /^[A-Za-z0-9](?:[A-Za-z0-9-]{0,98}[A-Za-z0-9])?$/.test(text(target.owner)) && /^[A-Za-z0-9_.-]{1,100}$/.test(text(target.name)) && ![".", ".."].includes(text(target.name));
}
function observationValid(value: Document) { return sha(value.base_sha) && sha(value.head_sha) && date(value.observed_at); }
function conflictSnapshotValid(value: Document) { return uuid(value.transition_id) && bounded(value.base_ref, 1024) && bounded(value.head_ref, 1024) && observationValid(object(value.observation)); }
function localDecisionValid(value: Document) {
 const dismissal = object(value.dismissal);
 return value.state === LocalState.Unhandled ? value.dismissal == null : value.state === LocalState.Dismissed && uuid(dismissal.request_id) && date(dismissal.at) && (dismissal.actor_type === "client" ? uuid(dismissal.device_id) : dismissal.actor_type === "owner" && dismissal.device_id == null);
}
export function readPRProblemSet(row: Resource, selection: PRProblemSelection): Document | undefined {
 const value = document(row), ci = object(value.ci), conflict = object(value.conflict);
 if (!envelope(row) || value.version !== 1 || value.type !== "pull-request-set" || !targetValid(object(value.target), selection) || (value.feedback == null && value.ci == null && value.conflict == null)) return;
 if (value.feedback != null && !observationValid(object(value.feedback))) return;
 if (value.ci != null && (!observationValid(object(ci.observation)) || !["unknown", "missing", "pending", "non-failing", "terminal-failure", "not-required"].includes(text(ci.state)) || !["head", "test-merge", "unknown"].includes(text(ci.source)) || !/^[a-f0-9]{64}$/.test(text(ci.rules_digest)) || (ci.source === "unknown" ? ci.evaluated_sha != null || ci.state !== "unknown" : !sha(ci.evaluated_sha)))) return;
 if (value.conflict != null && (!observationValid(object(conflict.observation)) || !bounded(conflict.base_ref, 1024) || !bounded(conflict.head_ref, 1024) || !["unknown", "mergeable", "conflicting", "not-applicable"].includes(text(conflict.state)) || !["open", "closed"].includes(text(conflict.pull_request_state)) || typeof conflict.merged !== "boolean" || (conflict.mergeable != null && typeof conflict.mergeable !== "boolean") || (conflict.active != null && !conflictSnapshotValid(object(conflict.active))))) return;
 return value;
}
export function readPRProblem(row: Resource, set: Resource, selection: PRProblemSelection): Document | undefined {
  const value = document(row), target = object(value.target), feedback = object(value.feedback), code = object(feedback.code), dismissal = object(value.dismissal);
  if (!envelope(row) || value.version !== 1 || value.type !== "pull-request-evidence" || value.set_id !== set.id || !targetValid(target, selection) || !observationValid(object(value.observation)) || typeof value.current !== "boolean" || !localDecisionValid(value) || !/^[a-f0-9]{64}$/.test(text(value.content_version))) return;
  if (value.kind === "ci-failure") {
    const ci = object(value.ci), context = object(ci.context);
    const terminal = context.kind === "check-run" ? context.native_status === "COMPLETED" && ["FAILURE", "CANCELLED", "TIMED_OUT", "ACTION_REQUIRED", "STALE", "STARTUP_FAILURE"].includes(text(context.native_conclusion)) : context.kind === "commit-status" && ["FAILURE", "ERROR"].includes(text(context.native_status));
    if (value.feedback != null || value.original_provider != null || value.latest_provider != null || value.conflict != null || !uuid(ci.observation_id) || !validCIContext(context) || context.required !== true || !terminal || !["head", "test-merge"].includes(text(ci.source)) || !/^[a-f0-9]{64}$/.test(text(ci.rules_digest))) return;
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
  const ci = object(value.ci), context = object(ci.context);
  const query = useQuery(ResourceQuery.getResource, { kind: EntityKind.PROBLEM, id: text(ci.observation_id) }, options);
  const row = query.data?.resource, proof = document(row), observation = object(proof.observation), evaluation = object(proof.ci), result = object(evaluation.result);
  const item = { base_ref: proof.base_ref, head_ref: proof.head_ref, base_sha: observation.base_sha, head_sha: observation.head_sha, state: proof.pull_request_state, merged: proof.merged, mergeable: proof.mergeable };
  const selected = result.source === "test-merge" ? object(evaluation.test_merge) : object(evaluation.head);
  const contexts = Array.isArray(selected.contexts) ? selected.contexts as Document[] : [];
  const matches = contexts.find(candidate => candidate.node_id === context.node_id);
  const valid = Boolean(row && envelope(row) && row.id === ci.observation_id && proof.version === 1 && proof.type === "pull-request-ci-observation" && proof.set_id === value.set_id && targetValid(object(proof.target), selection) && observationValid(observation) && sameJSON(proof.observation, value.observation) && validPRCI(evaluation, item) && result.state === "terminal-failure" && result.source === ci.source && object(evaluation.rules).digest === ci.rules_digest && matches && sameJSON(matches, context));
  return <><Problem error={query.error} />{query.isPending ? <p>Reading original CI evaluation…</p> : !valid ? <p role="alert">The original CI proof does not match this result version.</p> : <PRCI value={evaluation} historical />}</>;
}
function OriginalCIProof({ value, selection }: { value: Document; selection: PRProblemSelection }) {
  const [open, setOpen] = useState(false), ci = object(value.ci), context = object(ci.context);
  return <section><p>Original evaluated {text(ci.source)} commit: <code>{text(context.commit_sha)}</code> · rules digest <code>{text(ci.rules_digest)}</code></p><CIOriginalEvidence row={context} /><button aria-expanded={open} onClick={() => setOpen(!open)}>{open ? "Close original CI evaluation" : "Inspect original CI rules and results"}</button>{open ? <OriginalCIProofRead value={value} selection={selection} /> : null}</section>;
}

function ProblemRow({ row, value, selection, disabled, refreshed }: { row: Resource; value: Document; selection: PRProblemSelection; disabled: boolean; refreshed: () => void }) {
  const feedback = object(value.feedback), latest = object(value.latest_provider), original = object(value.original_provider), observation = object(value.observation), dismissal = object(value.dismissal), code = object(feedback.code), ci = object(value.ci), conflict = object(value.conflict);
  const title = value.kind === "ci-failure" ? `Required CI failure · ${text(object(ci.context).name)}` : value.kind === "merge-conflict" ? `Merge conflict · ${text(conflict.head_ref)} → ${text(conflict.base_ref)}` : `${text(feedback.kind)} · ${text(object(feedback.author).login) || "Author unavailable"}`;
  const dismiss = useRetainedMutation(`pr-problem-dismiss:${row.id}`, IntegrationQuery.dismissPullRequestProblem, refreshed);
  return <article className="result" aria-label={title}><h5>{title}</h5>
    <p>Local handling: {value.state === LocalState.Dismissed ? "Locally dismissed" : "Unhandled"} · {value.current ? "Present in latest complete collection" : "Retained earlier version or absent from latest collection"}</p>
    {value.kind === "review-feedback" ? <><p>Original provider state: {text(original.native_state) || "Published comment"}{original.review_state ? ` · Review ${text(original.review_state)}` : ""}. Latest observed provider state: {text(latest.native_state) || "Published comment"}{latest.review_state ? ` · Review ${text(latest.review_state)}` : ""}.</p>
      {typeof latest.thread_resolved === "boolean" ? <p>Latest thread observation: {latest.thread_resolved ? "resolved" : "unresolved"}{latest.thread_outdated ? " · outdated code position" : ""}. This does not mark this content version handled.</p> : null}
      <pre>{text(feedback.body) || "Empty published body."}</pre>
      {feedback.code != null ? <details><summary>Original code context · {text(code.path)}</summary><p>Original line: {code.original_line == null ? "Unavailable" : String(code.original_line)}</p><pre>{text(code.diff_hunk)}</pre></details> : null}
      <details><summary>Original feedback identity</summary><p>Source: <code>{text(feedback.url)}</code></p><p>Provider ID {text(feedback.id)} · Node <code>{text(feedback.node_id)}</code> · Author ID {text(object(feedback.author).id) || "Unavailable"}</p><p>Published {text(feedback.published_at)}{feedback.last_edited_at ? ` · Edited ${text(feedback.last_edited_at)}` : ""}</p></details></> : value.kind === "ci-failure" ? <OriginalCIProof value={value} selection={selection} /> : <p>Verified conflict transition: <code>{text(conflict.transition_id)}</code>. A new confirmed transition or changed refs/commits creates a separate version; an unknown reading does not.</p>}
    <details><summary>Original problem provenance</summary><p>Content version: <code>{text(value.content_version)}</code></p><p>First observed {text(observation.observed_at)} · head <code>{text(observation.head_sha)}</code> · base <code>{text(observation.base_sha)}</code></p><p>Problem {row.id} · revision {row.revision.toString()}</p></details>
    {value.state === LocalState.Dismissed ? <p>Dismissed locally at {text(dismissal.at)} by {text(dismissal.actor_type)}{dismissal.device_id ? ` ${text(dismissal.device_id)}` : ""} · request {text(dismissal.request_id)}</p> : <button disabled={disabled || dismiss.busy || dismiss.uncertain} onClick={() => void dismiss.send({ mutation: { id: row.id, expectedRevision: row.revision, requestId: newRequestId() }, contentVersion: text(value.content_version) })}>Dismiss this content version</button>}
    <Problem error={dismiss.error} />{dismiss.uncertain ? <button disabled={dismiss.busy} onClick={dismiss.retry}>Retry original dismissal</button> : null}
  </article>;
}
export function PRProblemHistory({ selection }: { selection: PRProblemSelection }) {
  const [page, setPage] = useState("");
  const [kind, setKind] = useState(PullRequestProblemCollectionKind.FEEDBACK);
  const history = useQuery(IntegrationQuery.listPullRequestProblems, { remoteRepositoryId: selection.remoteRepositoryId, pullRequestId: selection.pullRequestId, pageSize: 20, pageToken: page }, options);
  const refreshed = () => { if (page) setPage(""); else void history.refetch(); };
  const collect = useRetainedMutation(`pr-problem-refresh:${selection.repositoryId}:${selection.number}`, IntegrationQuery.refreshPullRequestProblems, refreshed);
  const set = history.data?.problemSet, rows = history.data?.problems ?? [];
  const summary = document(set), latestCI = object(summary.ci), latestConflict = object(summary.conflict);
  const values = set ? rows.map(row => readPRProblem(row, set, selection)) : [];
  const valid = rows.length <= 20 && new Set(rows.map(row => row.id)).size === rows.length && (set ? Boolean(readPRProblemSet(set, selection)) && values.every(Boolean) : !rows.length && !history.data?.nextPageToken) && (!history.data?.nextPageToken || rows.length > 0);
  const busy = collect.busy || collect.uncertain || history.isFetching;
  return <section aria-label="Retained PR problems"><h4>Retained PR problems</h4><p>Original feedback, required CI failures and verified merge conflicts are retained on the server. Local dismissal affects only the selected version. Provider changes do not handle retained evidence, and collection or dismissal does not run an agent.</p>
    <label>Problem collection kind<select disabled={busy} value={kind} onChange={event => setKind(Number(event.target.value) as PullRequestProblemCollectionKind)}><option value={PullRequestProblemCollectionKind.FEEDBACK}>Published feedback</option><option value={PullRequestProblemCollectionKind.CI}>Required CI</option><option value={PullRequestProblemCollectionKind.CONFLICT}>Merge conflict</option></select></label><button disabled={busy} onClick={() => void collect.send({ repositoryId: selection.repositoryId, number: selection.number, requestId: newRequestId(), kind })}>Collect selected PR problems</button><button disabled={history.isFetching} onClick={refreshed}>Refresh retained history</button>
    <Problem error={collect.error || history.error} />{collect.uncertain ? <button disabled={collect.busy} onClick={collect.retry}>Retry original problem collection</button> : null}
    {history.isPending ? <p role="status">Reading retained PR problems…</p> : !valid ? <p role="alert">The retained problem page is inconsistent. Refresh its original PR selection.</p> : <>{history.error ? <p>Previous retained history is shown; refresh failed.</p> : null}{set ? <div><p>Inventory revision {set.revision.toString()}</p>{summary.feedback != null ? <p>Latest feedback collection: {text(object(summary.feedback).observed_at)}</p> : null}{summary.ci != null ? <p>Latest CI evaluation: {text(latestCI.state)} · {text(latestCI.reason)} · {text(object(latestCI.observation).observed_at)}</p> : null}{summary.conflict != null ? <p>Latest mergeability: {text(latestConflict.state)} · {text(object(latestConflict.observation).observed_at)}</p> : null}</div> : <p>No problems have been collected for this PR.</p>}{rows.map((row, index) => <ProblemRow key={row.id} row={row} value={values[index]!} selection={selection} disabled={busy || Boolean(history.error)} refreshed={refreshed} />)}{set && !rows.length ? <p>No retained problems on this page.</p> : null}</>}
    <nav aria-label="Retained problem pages"><button disabled={!page || busy} onClick={() => setPage("")}>First problem page</button><button disabled={!valid || !history.data?.nextPageToken || busy || Boolean(history.error)} onClick={() => setPage(history.data!.nextPageToken)}>Next problem page</button></nav>
  </section>;
}
export function OpenPRProblemHistory({ selection }: { selection: PRProblemSelection }) {
  const [open, setOpen] = useState(false);
  return <div><button aria-expanded={open} onClick={() => setOpen(!open)}>{open ? "Close retained PR problems" : "Show retained PR problems"}</button>{open ? <PRProblemHistory key={`${selection.repositoryId}:${selection.remoteRepositoryId}:${selection.pullRequestId}`} selection={selection} /> : null}</div>;
}
