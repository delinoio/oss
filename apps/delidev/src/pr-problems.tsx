import { useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { EntityKind, IntegrationQuery, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, object, text, type Document } from "./documents";
import { bounded, date, positive, sha, uuid } from "./github-query-model";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";

enum LocalState { Unhandled = "unhandled", Dismissed = "locally-dismissed" }
export type PRProblemSelection = { repositoryId: string; remoteRepositoryId: string; pullRequestId: string; number: string };
const options = { retry: false, gcTime: 0, staleTime: 0, refetchOnWindowFocus: false, refetchOnReconnect: false };
const envelope = (row: Resource) => row.schemaVersion === 1 && row.kind === EntityKind.PROBLEM && uuid(row.id) && row.revision > 0n && row.revision < 1n << 63n && !row.sessionId && !row.projectId && row.documentJson.byteLength <= 1 << 20;
function targetValid(target: Document, selection: PRProblemSelection) {
  return target.version === 1 && target.provider === "github.com" && uuid(target.repository_id) && target.remote_repository_id === selection.remoteRepositoryId && target.pull_request_id === selection.pullRequestId && target.number === selection.number && positive(target.remote_repository_id) && positive(target.pull_request_id) && positive(target.number) && bounded(target.repository_node_id, 256) && bounded(target.pull_request_node_id, 256) && bounded(target.title, 4096) && date(target.observed_at) && /^[A-Za-z0-9](?:[A-Za-z0-9-]{0,98}[A-Za-z0-9])?$/.test(text(target.owner)) && /^[A-Za-z0-9_.-]{1,100}$/.test(text(target.name)) && ![".", ".."].includes(text(target.name));
}
function observationValid(value: Document) { return sha(value.base_sha) && sha(value.head_sha) && date(value.observed_at); }
export function readPRProblemSet(row: Resource, selection: PRProblemSelection): Document | undefined {
  const value = document(row);
  if (envelope(row) && value.version === 1 && value.type === "pull-request-set" && targetValid(object(value.target), selection) && observationValid(object(value.feedback))) return value;
}
export function readPRProblem(row: Resource, set: Resource, selection: PRProblemSelection): Document | undefined {
  const value = document(row), target = object(value.target), feedback = object(value.feedback), code = object(feedback.code), dismissal = object(value.dismissal);
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
function ProblemRow({ row, value, disabled, refreshed }: { row: Resource; value: Document; disabled: boolean; refreshed: () => void }) {
  const feedback = object(value.feedback), latest = object(value.latest_provider), original = object(value.original_provider), observation = object(value.observation), dismissal = object(value.dismissal), code = object(feedback.code);
  const dismiss = useRetainedMutation(`pr-problem-dismiss:${row.id}`, IntegrationQuery.dismissPullRequestProblem, refreshed);
  return <article className="result" aria-label={`Retained feedback ${text(feedback.node_id)}`}>
    <h5>{text(feedback.kind)} · {text(object(feedback.author).login) || "Author unavailable"}</h5>
    <p>Local handling: {value.state === LocalState.Dismissed ? "Locally dismissed" : "Unhandled"} · {value.current ? "Present in latest complete collection" : "Retained earlier version or absent from latest collection"}</p>
    <p>Original provider state: {text(original.native_state) || "Published comment"}{original.review_state ? ` · Review ${text(original.review_state)}` : ""}. Latest observed provider state: {text(latest.native_state) || "Published comment"}{latest.review_state ? ` · Review ${text(latest.review_state)}` : ""}.</p>
    {typeof latest.thread_resolved === "boolean" ? <p>Latest thread observation: {latest.thread_resolved ? "resolved" : "unresolved"}{latest.thread_outdated ? " · outdated code position" : ""}. This does not mark this content version handled.</p> : null}
    <pre>{text(feedback.body) || "Empty published body."}</pre>
    {feedback.code != null ? <details><summary>Original code context · {text(code.path)}</summary><p>Original line: {code.original_line == null ? "Unavailable" : String(code.original_line)}</p><pre>{text(code.diff_hunk)}</pre></details> : null}
    <details><summary>Original feedback identity and provenance</summary><p>Source: <code>{text(feedback.url)}</code></p><p>Provider ID {text(feedback.id)} · Node <code>{text(feedback.node_id)}</code> · Author ID {text(object(feedback.author).id) || "Unavailable"}</p><p>Published {text(feedback.published_at)}{feedback.last_edited_at ? ` · Edited ${text(feedback.last_edited_at)}` : ""}</p><p>Content version: <code>{text(value.content_version)}</code></p><p>First observed {text(observation.observed_at)} · head <code>{text(observation.head_sha)}</code> · base <code>{text(observation.base_sha)}</code></p><p>Problem {row.id} · revision {row.revision.toString()}</p></details>
    {value.state === LocalState.Dismissed ? <p>Dismissed locally at {text(dismissal.at)} by {text(dismissal.actor_type)}{dismissal.device_id ? ` ${text(dismissal.device_id)}` : ""} · request {text(dismissal.request_id)}</p> : <button disabled={disabled || dismiss.busy || dismiss.uncertain} onClick={() => void dismiss.send({ mutation: { id: row.id, expectedRevision: row.revision, requestId: newRequestId() }, contentVersion: text(value.content_version) })}>Dismiss this content version</button>}
    <Problem error={dismiss.error} />{dismiss.uncertain ? <button disabled={dismiss.busy} onClick={dismiss.retry}>Retry original dismissal</button> : null}
  </article>;
}
export function PRProblemHistory({ selection }: { selection: PRProblemSelection }) {
  const [page, setPage] = useState("");
  const history = useQuery(IntegrationQuery.listPullRequestProblems, { remoteRepositoryId: selection.remoteRepositoryId, pullRequestId: selection.pullRequestId, pageSize: 20, pageToken: page }, options);
  const refreshed = () => { if (page) setPage(""); else void history.refetch(); };
  const collect = useRetainedMutation(`pr-problem-refresh:${selection.repositoryId}:${selection.number}`, IntegrationQuery.refreshPullRequestProblems, refreshed);
  const set = history.data?.problemSet, rows = history.data?.problems ?? [];
  const values = set ? rows.map(row => readPRProblem(row, set, selection)) : [];
  const valid = rows.length <= 20 && new Set(rows.map(row => row.id)).size === rows.length && (set ? Boolean(readPRProblemSet(set, selection)) && values.every(Boolean) : !rows.length && !history.data?.nextPageToken) && (!history.data?.nextPageToken || rows.length > 0);
  const busy = collect.busy || collect.uncertain || history.isFetching;
  return <section aria-label="Retained PR feedback"><h4>Retained PR feedback</h4><p>Published feedback and its original versions are retained on the server. Local dismissal affects only the selected content version; GitHub approval, dismissal or disappearance does not handle it. Collection and dismissal do not run an agent.</p>
    <button disabled={busy} onClick={() => void collect.send({ repositoryId: selection.repositoryId, number: selection.number, requestId: newRequestId() })}>Collect current published feedback</button><button disabled={history.isFetching} onClick={refreshed}>Refresh retained history</button>
    <Problem error={collect.error || history.error} />{collect.uncertain ? <button disabled={collect.busy} onClick={collect.retry}>Retry original feedback collection</button> : null}
    {history.isPending ? <p role="status">Reading retained feedback…</p> : !valid ? <p role="alert">The retained feedback page is inconsistent. Refresh its original PR selection.</p> : <>{history.error ? <p>Previous retained history is shown; refresh failed.</p> : null}{set ? <p>Last complete collection: {text(object(document(set).feedback).observed_at)} · inventory revision {set.revision.toString()}</p> : <p>No feedback has been collected for this PR.</p>}{rows.map((row, index) => <ProblemRow key={row.id} row={row} value={values[index]!} disabled={busy || Boolean(history.error)} refreshed={refreshed} />)}{set && !rows.length ? <p>No retained feedback on this page.</p> : null}</>}
    <nav aria-label="Retained feedback pages"><button disabled={!page || busy} onClick={() => setPage("")}>First feedback page</button><button disabled={!valid || !history.data?.nextPageToken || busy || Boolean(history.error)} onClick={() => setPage(history.data!.nextPageToken)}>Next feedback page</button></nav>
  </section>;
}
export function OpenPRProblemHistory({ selection }: { selection: PRProblemSelection }) {
  const [open, setOpen] = useState(false);
  return <div><button aria-expanded={open} onClick={() => setOpen(!open)}>{open ? "Close retained feedback" : "Show retained feedback"}</button>{open ? <PRProblemHistory key={`${selection.repositoryId}:${selection.remoteRepositoryId}:${selection.pullRequestId}`} selection={selection} /> : null}</div>;
}
