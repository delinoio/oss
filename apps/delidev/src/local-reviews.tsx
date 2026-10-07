import { paginationIdentity, paginationRevision } from "./scroll-pagination";
import { useConversationDrafts } from "./conversation-drafts";
import { useConversationPages } from "./conversation-pagination";
import { ScrollContinuation } from "./scroll-continuation";
import { ScrollPayloadWindow } from "./scroll-payload-window";
import {  ownedMessage, useProductMessage, LocalizedText, copy, useLocale   } from "./localization";
import { useEffect, useRef, useState } from "react";
import { createConnectQueryKey, useQuery, useTransport } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { EntityKind, ResourceQuery, SessionQuery, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { Mode, encode } from "./documents";
import { useRetainedMutation, useRetainedMutationAccepted, useRetainedMutationIntents, type RetainedMutationIntent } from "./mutation";
import { workspaceReadOptions } from "./session-files";
import { type Diff } from "./session-diff-model";
import { AnchorKind, ReviewContextError, ReviewSide, freshness, readComment, readSubmission, readReviewContext, selectedContext, type Comment, type Selection } from "./local-review-model";
import { Failure, Problem } from "./ui";

type Selected = { resource: Resource; comment: Comment };

function useCommentDeletion(key: string, accepted?: () => void) {
  return useRetainedMutation(key, SessionQuery.deleteLocalReviewComment, accepted, (result, request) =>
    Boolean(request.mutation?.id && request.mutation.requestId && result.id === request.mutation.id && result.requestId === request.mutation.requestId), true);
}

function PendingCommentDeletion({ intent, commentId, accepted }: { intent: RetainedMutationIntent; commentId: string; accepted: () => void }) {
  const mutation = useCommentDeletion(intent.key);
  useRetainedMutationAccepted(intent.key, accepted);
  return <article aria-label={copy("local-reviews.pendingCommentDeletion", { v0: commentId })}>
    <p>{copy("local-reviews.commentDeletion", { v0: commentId })}</p><p role="status">{intent.busy ? copy("local-reviews.waitingForCommentDeletionAcknowledgement") : copy("local-reviews.commentDeletionAcknowledgementIsUncertain")}</p>
    <Problem error={mutation.error} />{intent.uncertain ? <button type="button" disabled={mutation.busy} onClick={mutation.retry}>{copy("local-reviews.retryOriginalCommentDeletion_db3527")}</button> : null}
  </article>;
}

export function LocalReviewRecovery({ sessionId, onAccepted }: { sessionId: string; onAccepted?: (commentId: string) => void }) {
  useLocale();
  const client = useQueryClient();
  const transport = useTransport();
  const deletionPrefix = `review:delete:${sessionId}:`;
  const reviewListQueryKey = createConnectQueryKey({ schema: ResourceQuery.listResources, transport, cardinality: "finite", input: { filter: { kind: EntityKind.REVIEW, sessionId } } });
  const deletions = useRetainedMutationIntents(deletionPrefix);
  if (!deletions.length) return null;
  return <section aria-label={copy("local-reviews.pendingCommentDeletions")}><h3>{copy("local-reviews.pendingCommentDeletions")}</h3>{deletions.map((intent) => {
    const commentId = intent.key.slice(deletionPrefix.length);
    return <PendingCommentDeletion key={intent.key} intent={intent} commentId={commentId} accepted={() => {
      onAccepted?.(commentId);
      void client.invalidateQueries({ queryKey: reviewListQueryKey, refetchType: "active" });
    }} />;
  })}</section>;
}

function NewComment({ sessionId, diff, saved, close }: { sessionId: string; diff: Diff; saved: (message: string) => void; close: () => void }) {
  useLocale();
  const [filePath, setFilePath] = useState<string>(), [kind, setKind] = useState(AnchorKind.File), [side, setSide] = useState(ReviewSide.New);
  const [start, setStart] = useState("1"), [end, setEnd] = useState("1"), [body, setBody] = useState("");
  const query = { operation: "git-diff", repository_id: diff.repository_id, comparison: diff.comparison, path: diff.path };
  const context = useQuery(SessionQuery.readSessionReviewContext, { sessionId, queryJson: encode(query) }, { ...workspaceReadOptions, select: (r) => readReviewContext(r.documentJson, diff) });
  const mutation = useRetainedMutation(`review:create:${sessionId}`, SessionQuery.createLocalReviewComment, (r) => { saved(r.comment ? copy("local-reviews.extra.22c97ed3217b") : copy("local-reviews.extra.29ba3bbaf8fc")); close(); });
  const blocked = mutation.busy || mutation.uncertain;
  const file = context.data?.files.find((v) => v.path === filePath) ?? context.data?.files[0];
  const pick: Selection = { path: file?.path ?? "", kind, ...(kind === AnchorKind.Lines ? { side, start: Number(start), end: Number(end) } : {}) };
  const preview = selectedContext(file, pick);
  return <form aria-label={copy("local-reviews.newLocalReviewComment_ccfb31")} onSubmit={(event) => { event.preventDefault(); if (blocked || preview === undefined || context.error || !body.trim()) return; void mutation.send({ requestId: newRequestId(), sessionId, documentJson: encode({ query, diff_revision: diff.revision, selection: pick, body }) }); }}>
    <h4>{copy("local-reviews.newComment_cf9c25")}</h4>{context.error instanceof ReviewContextError ? <p role="alert">{copy(context.error.productMessage.key)}</p> : <Problem error={context.error} />}{context.isPending ? <p role="status">{copy("local-reviews.readingReviewLocations_00d23b")}</p> : null}
    {context.error ? <button type="button" disabled={blocked || context.isFetching} onClick={() => void context.refetch()}>{copy("local-reviews.retryOriginalReviewLocations_c44ebc")}</button> : null}
    {context.data && !context.data.files.length ? <p>{copy("local-reviews.thisComparisonHasNoChangedFile_0d46bf")}</p> : null}
    <fieldset disabled={blocked || !file || Boolean(context.error)}><label>{copy("local-reviews.reviewFile_d5f013")}<select value={file?.path ?? ""} onChange={(e) => { setFilePath(e.target.value); setKind(AnchorKind.File); }}>{context.data?.files.map((f) => <option key={f.path} value={f.path}>{f.path}{f.kind === "non-line" ? copy("local-reviews.fileCommentsOnly_b3d4e3") : ""}</option>)}</select></label>
      <label>{copy("local-reviews.commentLocation_fb237d")}<select value={kind} onChange={(e) => setKind(e.target.value as AnchorKind)}><option value={AnchorKind.File}>{copy("local-reviews.wholeFile_b9c0ee")}</option>{file?.kind === "text" ? <option value={AnchorKind.Lines}>{copy("local-reviews.visibleLines_5f83af")}</option> : null}</select></label>
      {kind === AnchorKind.Lines ? <><label>{copy("local-reviews.diffSide_1f49a7")}<select value={side} onChange={(e) => setSide(e.target.value as ReviewSide)}><option value={ReviewSide.New}>{copy("local-reviews.new_18fdd5")}</option><option value={ReviewSide.Old}>{copy("local-reviews.old_bca971")}</option></select></label><label>{copy("local-reviews.firstLine_2361df")}<input type="number" min="1" step="1" value={start} onChange={(e) => setStart(e.target.value)} /></label><label>{copy("local-reviews.lastLine_d3626c")}<input type="number" min="1" step="1" value={end} onChange={(e) => setEnd(e.target.value)} /></label><p>{copy("local-reviews.chooseUpTo20ConsecutiveVisible_12e359")}</p>{preview === undefined ? <p role="alert">{copy("local-reviews.thisRangeHasNoUnambiguousText_07fe4d")}</p> : <pre aria-label={copy("local-reviews.attribute.09359102ca4b")}>{preview || "(Empty selected text)"}</pre>}</> : null}
      <label>{copy("local-reviews.reviewComment_c8866a")}<textarea value={body} maxLength={8192} onChange={(e) => setBody(e.target.value)} /></label>
    </fieldset>
    <Problem error={mutation.error} /><div className="actions"><button disabled={blocked || preview === undefined || !body.trim() || Boolean(context.error)}>{copy("local-reviews.saveComment_448d29")}</button>{mutation.uncertain ? <button type="button" disabled={mutation.busy} onClick={mutation.retry}>{copy("local-reviews.retryOriginalComment_3a8f1e")}</button> : null}<button type="button" disabled={mutation.busy} onClick={close}>{copy("local-reviews.closeCommentEditor_60629c")}</button></div>
  </form>;
}

type ReviewDraft = { editing: boolean; body: string; revision: bigint };
function CommentRow({ row, comment, sessionId, diff, selected, choose, refreshed, submitting, draft, changeDraft }: { row: Resource; comment: Comment; sessionId: string; diff: Diff; selected?: Selected; choose: (value?: Selected) => void; refreshed: () => void; submitting: boolean; draft?: ReviewDraft; changeDraft: (value: ReviewDraft) => void }) {
  useLocale();
  const { editing, body, revision } = draft ?? { editing: false, body: comment.body, revision: row.revision };
  const setEditing = (editing: boolean) => changeDraft({ editing, body, revision });
  const setBody = (body: string) => changeDraft({ editing, body, revision });
  const setRevision = (revision: bigint) => changeDraft({ editing, body, revision });
  const edit = useRetainedMutation(`review:edit:${sessionId}:${row.id}`, SessionQuery.editLocalReviewComment, () => { setEditing(false); choose(); refreshed(); });
  const remove = useCommentDeletion(`review:delete:${sessionId}:${row.id}`, () => { choose(); refreshed(); });
  const blocked = submitting || edit.busy || edit.uncertain || remove.busy || remove.uncertain;
  const anchor = comment.anchor, staleEdit = revision !== row.revision;
  return <article className="local-review-comment" aria-label={copy("local-reviews.reviewCommentOn_429062", { v0: anchor.selection.path })}>
    <label className="checkbox"><input type="checkbox" checked={Boolean(selected)} disabled={blocked} onChange={(e) => choose(e.target.checked ? { resource: row, comment } : undefined)} /><LocalizedText id="local-reviews.selectCommentOn_bfc20c" components={{ s0: <>{anchor.selection.path}</> }} /></label>
    <p>{anchor.selection.kind === AnchorKind.Lines ? copy("local-reviews.lines_0434c4", { v0: anchor.selection.side, v1: anchor.selection.start, v2: anchor.selection.end }) : copy("local-reviews.fileComment_f9fcbb")} · {freshness(anchor, diff)}</p>
    <details><summary>{copy("local-reviews.originalReviewContext_f8890f")}</summary><p className="file-path"><LocalizedText id="local-reviews.repositoryDiffRevision_7f2d2f" components={{ s0: <>{anchor.repository_id}</>, s1: <br />, s2: <>{anchor.diff_revision}</> }} /></p>{anchor.context ? <pre>{anchor.context}</pre> : <p>{copy("local-reviews.wholeFileLocation_cf901e")}</p>}</details>
    <p className="review-body">{comment.body}</p>
    {comment.last_submission_id ? <p><LocalizedText id="local-reviews.lastSubmittedContentRevision_d86b30" components={{ s0: <>{comment.last_submitted_content_revision}</>, s1: <>{comment.last_submitted_content_revision !== comment.content_revision ? copy("local-reviews.editedSinceSubmission_2161ce") : ""}</> }} /></p> : null}
    {selected && selected.resource.revision !== row.revision ? <p role="alert">{copy("local-reviews.anEarlierVersionIsSelectedDeselect_d7387d")}</p> : null}
    {editing ? <form onSubmit={(e) => { e.preventDefault(); if (blocked || staleEdit || !body.trim()) return; void edit.send({ mutation: { id: row.id, expectedRevision: revision, requestId: newRequestId() }, sessionId, body }); }}><label>{copy("local-reviews.editReviewComment_ca032a")}<textarea disabled={blocked} value={body} maxLength={8192} onChange={(e) => setBody(e.target.value)} /></label>{staleEdit ? <><p role="alert">{copy("local-reviews.theCommentChangedYourEditIs_fb9219")}</p><button type="button" disabled={blocked} onClick={() => setRevision(row.revision)}>{copy("local-reviews.useLatestCommentRevisionWithThis_b1c152")}</button></> : null}<button disabled={blocked || staleEdit || !body.trim()}>{copy("local-reviews.saveCommentEdit_6b39cb")}</button><button type="button" disabled={blocked} onClick={() => setEditing(false)}>{copy("local-reviews.cancelCommentEdit_d38b3c")}</button></form> : <div className="actions"><button disabled={blocked} onClick={() => changeDraft({ body: comment.body, revision: row.revision, editing: true })}>{copy("local-reviews.editComment_4f346f")}</button><button disabled={blocked} onClick={() => void remove.send({ mutation: { id: row.id, expectedRevision: row.revision, requestId: newRequestId() }, sessionId })}>{copy("local-reviews.deleteComment_e43811")}</button></div>}
    <Problem error={edit.error || (remove.uncertain ? undefined : remove.error)} />{edit.uncertain ? <button disabled={edit.busy} onClick={edit.retry}>{copy("local-reviews.retryOriginalCommentEdit_f362b6")}</button> : null}
  </article>;
}

export function LocalReviews({ sessionId, diff, reading, acceptedDeletionId }: { sessionId: string; diff: Diff; reading: boolean; acceptedDeletionId?: string }) {
  useLocale();
  const [authoring, setAuthoring] = useState<Diff>(), [notice, setNotice] = useProductMessage("");
  const [selected, setSelected] = useState<Map<string, Selected>>(() => new Map()), [mode, setMode] = useState(Mode.Execute), [allowStale, setAllowStale] = useState(false);
  useEffect(() => {
    if (!acceptedDeletionId) return;
    setSelected((previous) => {
      if (!previous.has(acceptedDeletionId)) return previous;
      const next = new Map(previous);
      next.delete(acceptedDeletionId);
      return next;
    });
  }, [acceptedDeletionId]);
  const edits = useConversationDrafts<ReviewDraft>();
  const reviewRoot = useRef<HTMLElement>(null);
  const list = useConversationPages(EntityKind.REVIEW, sessionId);
  const refresh = () => { void list.refetch(); };
  const submit = useRetainedMutation(`review:submit:${sessionId}`, SessionQuery.submitLocalReview, (r) => { setSelected(new Map()); setAllowStale(false); setNotice(r.change?.input ? ownedMessage("local-reviews.sentence.72171603f37d", { v0: r.change.input.id }) : ownedMessage("local-reviews.extra.68459ebcb74e")); refresh(); });
  const blocked = submit.busy || submit.uncertain;
  const choose = (id: string, value?: Selected) => { if (blocked) return; if (value && selected.size >= 25 && !selected.has(id)) { setNotice(ownedMessage("local-reviews.extra.28df21a538c2")); return; } setSelected((previous) => { const next = new Map(previous); if (value) next.set(id, value); else next.delete(id); return next; }); };
  return <section ref={reviewRoot} aria-label={copy("local-reviews.localAgentReview_4069f1")} className="local-reviews conversation-page-scroll">
    <h3>{copy("local-reviews.localAgentReview_4069f1")}</h3><p>{copy("local-reviews.saveFileOrLineCommentsThen_fb2192")}</p>
    <div className="actions"><button disabled={reading || Boolean(authoring) || blocked} onClick={() => setAuthoring(diff)}>{copy("local-reviews.addReviewComment_659e59")}</button><button disabled={list.isFetching} onClick={refresh}>{copy("local-reviews.refreshReviews_d7467d")}</button></div>
    {authoring ? <NewComment sessionId={sessionId} diff={authoring} saved={(message) => { setNotice(message); refresh(); }} close={() => setAuthoring(undefined)} /> : null}
    {notice ? <p role="status">{notice}</p> : null}<Failure failure={list.error?.failure} />{list.error && list.data ? <p role="alert">{copy("local-reviews.reviewRefreshFailedRetainedRecordsMay_3dcd6a")}</p> : null}
    {list.isPending ? <p>{copy("local-reviews.loadingLocalReviews_d8fa14")}</p> : list.rows.length ? <div><ScrollPayloadWindow identity={paginationIdentity} revision={paginationRevision} query={list} root={reviewRoot} active={!blocked}>{payload => payload.map((row) => {
      const comment = readComment(row, sessionId);
      if (comment) return <CommentRow key={row.id} row={row} comment={comment} sessionId={sessionId} diff={diff} selected={selected.get(row.id)} choose={(v) => choose(row.id, v)} refreshed={refresh} submitting={blocked || Boolean(list.error)} draft={edits.values.get(row.id)} changeDraft={value => edits.save(row.id, value)} />;
      const submission = readSubmission(row, sessionId);
      if (submission) return <details key={row.id}><summary><LocalizedText id="local-reviews.submittedReview_d16733" components={{ s0: <>{submission.mode}</>, s1: <>{row.id}</> }} /></summary><p className="file-path"><LocalizedText id="local-reviews.acceptedInput_d7512d" components={{ s0: <>{submission.input_id}</> }} /></p>{submission.comments.map((c) => <article key={c.id}><p><LocalizedText id="local-reviews.atSubmissionContentRevision_bcd985" components={{ s0: <>{c.anchor.selection.path}</>, s1: <>{c.freshness}</>, s2: <>{c.content_revision}</> }} /></p><p className="review-body">{c.body}</p>{c.anchor.context ? <pre>{c.anchor.context}</pre> : null}</article>)}</details>;
      return <p role="alert" key={row.id}>{copy("local-reviews.thisRetainedReviewRecordIsUnavailable_bfdee4")}</p>;
    })}</ScrollPayloadWindow></div> : <p>{copy("local-reviews.noLocalReviewsOnThisPage_55133f")}</p>}
    <ScrollContinuation query={list} root={reviewRoot} active={!blocked} label={copy("local-reviews.reviewPages_87ffd1")} />
    <form aria-label={copy("local-reviews.requestChanges_cb5d9f")} onSubmit={(e) => { e.preventDefault(); if (blocked || !selected.size || list.error) return; void submit.send({ requestId: newRequestId(), sessionId, documentJson: encode({ comments: [...selected.values()].map((v) => ({ id: v.resource.id, revision: v.resource.revision.toString() })), mode, allow_stale: allowStale }) }); }}>
      <h4><LocalizedText id="local-reviews.selectedComments_648207" components={{ s0: <>{selected.size}</> }} /></h4><ul>{[...selected.values()].map((v) => <li key={v.resource.id}><LocalizedText id="local-reviews.revision_24230a" components={{ s0: <>{v.comment.anchor.selection.path}</>, s1: <>{v.resource.revision.toString()}</> }} /><p className="review-body">{v.comment.body}</p></li>)}</ul>
      <fieldset disabled={blocked}><label>{copy("local-reviews.reviewRequestMode_c25b4d")}<select value={mode} onChange={(e) => setMode(e.target.value as Mode)}><option value={Mode.Execute}>{copy("local-reviews.execute_e3a67d")}</option><option value={Mode.Plan}>{copy("local-reviews.plan_fa8ed0")}</option></select></label><label className="checkbox"><input type="checkbox" checked={allowStale} onChange={(e) => setAllowStale(e.target.checked)} />{copy("local-reviews.includeStaleCommentsAfterCheckingTheir_87ab7b")}</label></fieldset>
      <p>{copy("local-reviews.theServerChecksTheSelectedComparisons_6a6cfc")}</p>
      <Problem error={submit.error} /><div className="actions"><button disabled={blocked || !selected.size || Boolean(list.error)}>{copy("local-reviews.requestChanges_cb5d9f")}</button>{submit.uncertain ? <button type="button" disabled={submit.busy} onClick={submit.retry}>{copy("local-reviews.retryOriginalReviewSubmission_b3064b")}</button> : null}<button type="button" disabled={blocked || !selected.size} onClick={() => setSelected(new Map())}>{copy("local-reviews.clearSelectedComments_8db10f")}</button></div>
    </form>
  </section>;
}
