import { useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { EntityKind, ResourceQuery, SessionQuery, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { Mode, encode } from "./documents";
import { useRetainedMutation } from "./mutation";
import { workspaceReadOptions } from "./session-files";
import { type Diff } from "./session-diff-model";
import { AnchorKind, ReviewContextError, ReviewSide, freshness, readComment, readSubmission, readReviewContext, selectedContext, type Comment, type Selection } from "./local-review-model";
import { Problem } from "./ui";

type Selected = { resource: Resource; comment: Comment };

function NewComment({ sessionId, diff, saved, close }: { sessionId: string; diff: Diff; saved: (message: string) => void; close: () => void }) {
  const [filePath, setFilePath] = useState<string>(), [kind, setKind] = useState(AnchorKind.File), [side, setSide] = useState(ReviewSide.New);
  const [start, setStart] = useState("1"), [end, setEnd] = useState("1"), [body, setBody] = useState("");
  const query = { operation: "git-diff", repository_id: diff.repository_id, comparison: diff.comparison, path: diff.path };
  const context = useQuery(SessionQuery.readSessionReviewContext, { sessionId, queryJson: encode(query) }, { ...workspaceReadOptions, select: (r) => readReviewContext(r.documentJson, diff) });
  const mutation = useRetainedMutation(`review:create:${sessionId}`, SessionQuery.createLocalReviewComment, (r) => { saved(r.comment ? "Comment saved." : "Comment acknowledged. Refresh reviews to inspect the result."); close(); });
  const blocked = mutation.busy || mutation.uncertain;
  const file = context.data?.files.find((v) => v.path === filePath) ?? context.data?.files[0];
  const pick: Selection = { path: file?.path ?? "", kind, ...(kind === AnchorKind.Lines ? { side, start: Number(start), end: Number(end) } : {}) };
  const preview = selectedContext(file, pick);
  return <form aria-label="New local review comment" onSubmit={(event) => { event.preventDefault(); if (blocked || preview === undefined || context.error || !body.trim()) return; void mutation.send({ requestId: newRequestId(), sessionId, documentJson: encode({ query, diff_revision: diff.revision, selection: pick, body }) }); }}>
    <h4>New comment</h4>{context.error instanceof ReviewContextError ? <p role="alert">{context.error.message}</p> : <Problem error={context.error} />}{context.isPending ? <p role="status">Reading review locations…</p> : null}
    {context.error ? <button type="button" disabled={blocked || context.isFetching} onClick={() => void context.refetch()}>Retry original review locations</button> : null}
    {context.data && !context.data.files.length ? <p>This comparison has no changed file locations.</p> : null}
    <fieldset disabled={blocked || !file || Boolean(context.error)}><label>Review file<select value={file?.path ?? ""} onChange={(e) => { setFilePath(e.target.value); setKind(AnchorKind.File); }}>{context.data?.files.map((f) => <option key={f.path} value={f.path}>{f.path}{f.kind === "non-line" ? " · File comments only" : ""}</option>)}</select></label>
      <label>Comment location<select value={kind} onChange={(e) => setKind(e.target.value as AnchorKind)}><option value={AnchorKind.File}>Whole file</option>{file?.kind === "text" ? <option value={AnchorKind.Lines}>Visible lines</option> : null}</select></label>
      {kind === AnchorKind.Lines ? <><label>Diff side<select value={side} onChange={(e) => setSide(e.target.value as ReviewSide)}><option value={ReviewSide.New}>New</option><option value={ReviewSide.Old}>Old</option></select></label><label>First line<input type="number" min="1" step="1" value={start} onChange={(e) => setStart(e.target.value)} /></label><label>Last line<input type="number" min="1" step="1" value={end} onChange={(e) => setEnd(e.target.value)} /></label><p>Choose up to 20 consecutive visible lines within one diff hunk.</p>{preview === undefined ? <p role="alert">This range has no unambiguous text location. Check the side and line numbers in the diff.</p> : <pre aria-label="Selected review context">{preview || "(Empty selected text)"}</pre>}</> : null}
      <label>Review comment<textarea value={body} maxLength={8192} onChange={(e) => setBody(e.target.value)} /></label>
    </fieldset>
    <Problem error={mutation.error} /><div className="actions"><button disabled={blocked || preview === undefined || !body.trim() || Boolean(context.error)}>Save comment</button>{mutation.uncertain ? <button type="button" disabled={mutation.busy} onClick={mutation.retry}>Retry original comment</button> : null}<button type="button" disabled={mutation.busy} onClick={close}>Close comment editor</button></div>
  </form>;
}

function CommentRow({ row, comment, sessionId, diff, selected, choose, refreshed, submitting }: { row: Resource; comment: Comment; sessionId: string; diff: Diff; selected?: Selected; choose: (value?: Selected) => void; refreshed: () => void; submitting: boolean }) {
  const [editing, setEditing] = useState(false), [body, setBody] = useState(comment.body), [revision, setRevision] = useState(row.revision);
  const edit = useRetainedMutation(`review:edit:${sessionId}:${row.id}`, SessionQuery.editLocalReviewComment, () => { setEditing(false); choose(); refreshed(); });
  const remove = useRetainedMutation(`review:delete:${sessionId}:${row.id}`, SessionQuery.deleteLocalReviewComment, () => { choose(); refreshed(); });
  const blocked = submitting || edit.busy || edit.uncertain || remove.busy || remove.uncertain;
  const anchor = comment.anchor, staleEdit = revision !== row.revision;
  return <article className="local-review-comment" aria-label={`Review comment on ${anchor.selection.path}`}>
    <label className="checkbox"><input type="checkbox" checked={Boolean(selected)} disabled={blocked} onChange={(e) => choose(e.target.checked ? { resource: row, comment } : undefined)} />Select comment on {anchor.selection.path}</label>
    <p>{anchor.selection.kind === AnchorKind.Lines ? `${anchor.selection.side} lines ${anchor.selection.start}–${anchor.selection.end}` : "File comment"} · {freshness(anchor, diff)}</p>
    <details><summary>Original review context</summary><p className="file-path">Repository: {anchor.repository_id}<br />Diff revision: {anchor.diff_revision}</p>{anchor.context ? <pre>{anchor.context}</pre> : <p>Whole-file location</p>}</details>
    <p className="review-body">{comment.body}</p>
    {comment.last_submission_id ? <p>Last submitted content revision {comment.last_submitted_content_revision}{comment.last_submitted_content_revision !== comment.content_revision ? " · Edited since submission" : ""}</p> : null}
    {selected && selected.resource.revision !== row.revision ? <p role="alert">An earlier version is selected. Deselect and select this comment again after reviewing the change.</p> : null}
    {editing ? <form onSubmit={(e) => { e.preventDefault(); if (blocked || staleEdit || !body.trim()) return; void edit.send({ mutation: { id: row.id, expectedRevision: revision, requestId: newRequestId() }, sessionId, body }); }}><label>Edit review comment<textarea disabled={blocked} value={body} maxLength={8192} onChange={(e) => setBody(e.target.value)} /></label>{staleEdit ? <><p role="alert">The comment changed. Your edit is retained.</p><button type="button" disabled={blocked} onClick={() => setRevision(row.revision)}>Use latest comment revision with this edit</button></> : null}<button disabled={blocked || staleEdit || !body.trim()}>Save comment edit</button><button type="button" disabled={blocked} onClick={() => setEditing(false)}>Cancel comment edit</button></form> : <div className="actions"><button disabled={blocked} onClick={() => { setBody(comment.body); setRevision(row.revision); setEditing(true); }}>Edit comment</button><button disabled={blocked} onClick={() => void remove.send({ mutation: { id: row.id, expectedRevision: row.revision, requestId: newRequestId() }, sessionId })}>Delete comment</button></div>}
    <Problem error={edit.error || remove.error} />{edit.uncertain ? <button disabled={edit.busy} onClick={edit.retry}>Retry original comment edit</button> : null}{remove.uncertain ? <button disabled={remove.busy} onClick={remove.retry}>Retry original comment deletion</button> : null}
  </article>;
}

export function LocalReviews({ sessionId, diff, reading }: { sessionId: string; diff: Diff; reading: boolean }) {
  const [page, setPage] = useState(""), [authoring, setAuthoring] = useState<Diff>(), [notice, setNotice] = useState("");
  const [selected, setSelected] = useState<Map<string, Selected>>(() => new Map()), [mode, setMode] = useState(Mode.Execute), [allowStale, setAllowStale] = useState(false);
  const list = useQuery(ResourceQuery.listResources, { filter: { kind: EntityKind.REVIEW, sessionId, pageToken: page, pageSize: 50 } }, { staleTime: 0, gcTime: 0, refetchOnWindowFocus: false, retry: false });
  const refresh = () => { void list.refetch(); };
  const submit = useRetainedMutation(`review:submit:${sessionId}`, SessionQuery.submitLocalReview, (r) => { setSelected(new Map()); setAllowStale(false); setNotice(r.change?.input ? `Request changes queued as input ${r.change.input.id}.` : "Submission acknowledged. Inspect the session queue and review history."); refresh(); });
  const blocked = submit.busy || submit.uncertain;
  const rows = list.data?.resources ?? [];
  const choose = (id: string, value?: Selected) => { if (blocked) return; if (value && selected.size >= 25 && !selected.has(id)) { setNotice("Select at most 25 comments for one request."); return; } setSelected((previous) => { const next = new Map(previous); if (value) next.set(id, value); else next.delete(id); return next; }); };
  return <section aria-label="Local agent review" className="local-reviews">
    <h3>Local agent review</h3><p>Save file or line comments, then send selected comments to this session's agent. Submission follows the session's input queue and does not resolve comments.</p>
    <div className="actions"><button disabled={reading || Boolean(authoring) || blocked} onClick={() => setAuthoring(diff)}>Add review comment</button><button disabled={list.isFetching} onClick={refresh}>Refresh reviews</button></div>
    {authoring ? <NewComment sessionId={sessionId} diff={authoring} saved={(message) => { setNotice(message); refresh(); }} close={() => setAuthoring(undefined)} /> : null}
    {notice ? <p role="status">{notice}</p> : null}<Problem error={list.error} />{list.error && list.data ? <p role="alert">Review refresh failed. Retained records may be outdated.</p> : null}
    {list.isPending ? <p>Loading local reviews…</p> : rows.length ? rows.map((row) => {
      const comment = readComment(row, sessionId);
      if (comment) return <CommentRow key={row.id} row={row} comment={comment} sessionId={sessionId} diff={diff} selected={selected.get(row.id)} choose={(v) => choose(row.id, v)} refreshed={refresh} submitting={blocked} />;
      const submission = readSubmission(row, sessionId);
      if (submission) return <details key={row.id}><summary>Submitted review · {submission.mode} · {row.id}</summary><p className="file-path">Accepted input: {submission.input_id}</p>{submission.comments.map((c) => <article key={c.id}><p>{c.anchor.selection.path} · {c.freshness} at submission · Content revision {c.content_revision}</p><p className="review-body">{c.body}</p>{c.anchor.context ? <pre>{c.anchor.context}</pre> : null}</article>)}</details>;
      return <p role="alert" key={row.id}>This retained review record is unavailable.</p>;
    }) : <p>No local reviews on this page.</p>}
    <nav className="actions" aria-label="Review pages"><button disabled={!page || list.isFetching} onClick={() => setPage("")}>First review page</button><button disabled={!list.data?.nextPageToken || list.isFetching} onClick={() => setPage(list.data!.nextPageToken)}>Next review page</button></nav>
    <form aria-label="Request changes" onSubmit={(e) => { e.preventDefault(); if (blocked || !selected.size || list.error) return; void submit.send({ requestId: newRequestId(), sessionId, documentJson: encode({ comments: [...selected.values()].map((v) => ({ id: v.resource.id, revision: v.resource.revision.toString() })), mode, allow_stale: allowStale }) }); }}>
      <h4>Selected comments: {selected.size}</h4><ul>{[...selected.values()].map((v) => <li key={v.resource.id}>{v.comment.anchor.selection.path} · Revision {v.resource.revision.toString()}<p className="review-body">{v.comment.body}</p></li>)}</ul>
      <fieldset disabled={blocked}><label>Review request mode<select value={mode} onChange={(e) => setMode(e.target.value as Mode)}><option value={Mode.Execute}>Execute</option><option value={Mode.Plan}>Plan</option></select></label><label className="checkbox"><input type="checkbox" checked={allowStale} onChange={(e) => setAllowStale(e.target.checked)} />Include stale comments after checking their original locations</label></fieldset>
      <p>The server checks the selected comparisons again before accepting this request. While the agent is running, feedback is queued. Use the session queue's explicit Steer action only when supported.</p>
      <Problem error={submit.error} /><div className="actions"><button disabled={blocked || !selected.size || Boolean(list.error)}>Request changes</button>{submit.uncertain ? <button type="button" disabled={submit.busy} onClick={submit.retry}>Retry original review submission</button> : null}<button type="button" disabled={blocked || !selected.size} onClick={() => setSelected(new Map())}>Clear selected comments</button></div>
    </form>
  </section>;
}
