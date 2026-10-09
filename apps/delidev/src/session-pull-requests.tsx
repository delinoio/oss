import { createPortal } from "react-dom";
import { SessionActivityProvider, useSessionActive, useSessionQuery as useQuery } from "./session-activity";
// SPDX-License-Identifier: Apache-2.0
import { Disclosure, DisclosureSummary } from "./disclosure";
import { Timestamp } from "./timestamp-display";
import { ScrollContinuation } from "./scroll-continuation";
import { ScrollPayloadWindow } from "./scroll-payload-window";
import { useConnectPaginationReader, usePaginationChain, usePaginationRefresh } from "./scroll-pagination-query";
import { useStablePageRevisions, paginationError, invalidGitHubPage, resourceProjection, useGitHubScrollRoot, visiblePageIds } from "./github-scroll";
import { LocalizedText, copy, useLocale  } from "./localization";
import { useCallback, useEffect, useLayoutEffect, useRef } from "react";

import { EntityKind, FailureCode, ResourceQuery, SessionQuery, newRequestId, type Resource, type UnlinkSessionPullRequestRequest } from "@delinoio/delidev-api-client";
import { document, text, type Document } from "./documents";
import { bounded, date, positive, uuid } from "./github-query-model";
import { useRetainedMutation, useRetainedMutationIntents } from "./mutation";
import { OpenPRProblemHistory } from "./pr-problems";
import { Problem } from "./ui";

const readOptions = { retry: false, gcTime: 0, staleTime: 0, refetchOnWindowFocus: false, refetchOnReconnect: false };
export function readSessionPR(row: Resource, sessionId: string): Document | undefined {
  const value = document(row);
  if (row.kind !== EntityKind.PULL_REQUEST || row.sessionId !== sessionId || !uuid(row.id) || !uuid(row.projectId) || row.revision <= 0n || row.revision >= 1n << 63n || value.version !== 1 || value.provider !== "github.com" || !uuid(value.repository_id) || !positive(value.remote_repository_id) || !positive(value.pull_request_id) || !positive(value.number) || !bounded(value.repository_node_id, 256) || !bounded(value.pull_request_node_id, 256) || !bounded(value.title, 4096) || !date(value.observed_at)) return;
  if (!/^[A-Za-z0-9](?:[A-Za-z0-9-]{0,98}[A-Za-z0-9])?$/.test(text(value.owner)) || !/^[A-Za-z0-9_.-]{1,100}$/.test(text(value.name)) || [".", ".."].includes(text(value.name))) return;
  return value;
}

function usePRUnlink(sessionId: string, associationId: string, refreshed: () => void) {
  return useRetainedMutation(`session-pr:unlink:${sessionId}:${associationId}`, SessionQuery.unlinkSessionPullRequest, refreshed, (result, request) =>
    request.sessionId === sessionId && request.mutation?.id === associationId && result.id === request.mutation.id && result.requestId === request.mutation.requestId);
}

function PendingUnlink({ sessionId, associationId, refreshed }: { sessionId: string; associationId: string; refreshed: () => void }) {
  const remove = usePRUnlink(sessionId, associationId, refreshed);
  const original = remove.input as UnlinkSessionPullRequestRequest | undefined;
  const valid = original?.sessionId === sessionId && original.mutation?.id === associationId && uuid(original.mutation.requestId) && original.mutation.expectedRevision > 0n && original.mutation.expectedRevision < 1n << 63n;
  return <article aria-label={`Pending PR unlink ${associationId}`}>
    <p>{copy("session-pull-requests.inline.60c1498ca3")} {associationId}</p>
    <p role="status">{remove.busy ? "Submitting original PR unlink…" : "PR unlink acknowledgment is uncertain."}</p>
    <Problem error={remove.error} />
    {!valid ? <p role="alert">{copy("session-pull-requests.inline.a4c646bacf")}</p> : remove.uncertain ? <button disabled={remove.busy} onClick={remove.retry}>{copy("session-pull-requests.retryOriginalPrUnlink_29e215")}</button> : null}
  </article>;
}

function PendingUnlinks({ sessionId, refreshed }: { sessionId: string; refreshed: () => void }) {
  useLocale();
  const prefix = `session-pr:unlink:${sessionId}:`;
  const intents = useRetainedMutationIntents(prefix).filter((intent) => uuid(intent.key.slice(prefix.length)));
  // Unlink can remove its own row before the original receipt reaches this
  // connection. Recovery must remain independent of the current resource page.
  return intents.length ? <section aria-label={copy("session-pull-requests.inline.e3faf29ec8")}><h4>{copy("session-pull-requests.inline.e3faf29ec8")}</h4>
    {intents.map((intent) => <PendingUnlink key={intent.key} sessionId={sessionId} associationId={intent.key.slice(prefix.length)} refreshed={refreshed} />)}
  </section> : null;
}

function LinkRow({ row, value, sessionId, refreshed, diagnosticsTarget }: { row: Resource; value: Document; sessionId: string; refreshed: () => void; diagnosticsTarget?: HTMLElement | null }) {
  const remove = usePRUnlink(sessionId, row.id, refreshed);
  return <article aria-label={copy("session-pull-requests.linkedPr_299ef1", { v0: text(value.owner), v1: text(value.name), v2: text(value.number) })}>
    <h4>{text(value.owner)}/{text(value.name)}#{text(value.number)}</h4><p>{text(value.title)}</p>
    <p><LocalizedText id="session-pull-requests.linkedObservationCurrentPrStateAnd_e8d519" components={{ s0: <><Timestamp value={text(value.observed_at)} /></> }} /></p>
    {diagnosticsTarget ? createPortal(<section><h3>{copy("session-pull-requests.originalPrIdentity_92b511")}</h3><p><LocalizedText id="session-pull-requests.repositoryIdPrIdNode_78f0da" components={{ s0: <>{text(value.remote_repository_id)}</>, s1: <>{text(value.pull_request_id)}</>, s2: <>{text(value.pull_request_node_id)}</> }} /></p><p><LocalizedText id="session-pull-requests.configuredRepository_6aa131" components={{ s0: <>{text(value.repository_id)}</> }} /></p><p>{`https://github.com/${text(value.owner)}/${text(value.name)}/pull/${text(value.number)}`}</p></section>, diagnosticsTarget) : null}
    <OpenPRProblemHistory routineRefresh={false} selection={{ repositoryId: text(value.repository_id), remoteRepositoryId: text(value.remote_repository_id), pullRequestId: text(value.pull_request_id), number: text(value.number) }} />
    <button disabled={remove.busy || remove.uncertain} onClick={() => void remove.send({ sessionId, mutation: { id: row.id, expectedRevision: row.revision, requestId: newRequestId() } })}><LocalizedText id="session-pull-requests.unlink_1c427a" components={{ s0: <>{text(value.number)}</> }} /></button>
    {!remove.busy && !remove.uncertain ? <Problem error={remove.error} /> : null}
  </article>;
}

function RetainedLinks({ session, refreshOwner, emptyChanged, diagnosticsTarget }: { session: Resource; refreshOwner: { current: () => void }; emptyChanged?: (value: boolean) => void; diagnosticsTarget?: HTMLElement | null }) {
  const active=useSessionActive();
  useLocale();
  const { root, bindRoot } = useGitHubScrollRoot();
  const validateBoundary = useStablePageRevisions(session.id);
  const request = useCallback((token: string) => ({ filter: { kind: EntityKind.PULL_REQUEST, sessionId: session.id, pageSize: 50, pageToken: token } }), [session.id]);
  const project = useCallback((reply: { resources: Resource[]; nextPageToken: string }, token: string) => {
    if (reply.resources.length > 50 || new Set(reply.resources.map(row => row.id)).size !== reply.resources.length || reply.resources.some(row => !readSessionPR(row, session.id))) invalidGitHubPage();
    validateBoundary(token, reply.resources.map(resourceProjection));
    return { rows: reply.resources.map(resourceProjection), nextPageToken: reply.nextPageToken, payload: reply.resources };
  }, [session.id, validateBoundary]);
  const reader = useConnectPaginationReader(ResourceQuery.listResources, request, project);
  const pending = useRetainedMutationIntents("session-pr:");
  const blocked = pending.some(intent => (intent.busy || intent.uncertain) && (intent.key === `session-pr:link:${session.id}` || intent.key.startsWith(`session-pr:unlink:${session.id}:`)));
  const list = usePaginationChain(session.id, active, reader);
  usePaginationRefresh(ResourceQuery.listResources, request(""), active, list.refresh);
  const refresh = list.error ? list.error.stalled || list.error.failure.code === FailureCode.CursorExpired ? list.reload : list.retry : list.refresh;
  useLayoutEffect(() => { refreshOwner.current = refresh; return () => { refreshOwner.current = () => undefined; }; }, [refresh, refreshOwner]);
  useEffect(() => { emptyChanged?.(list.loaded && !list.error && !list.loading && !list.nextPageToken && list.rows.length === 0 && !blocked); }, [list.loaded, list.error, list.loading, list.nextPageToken, list.rows.length, blocked, emptyChanged]);
  return <section ref={bindRoot} aria-label={copy("session-pull-requests.sessionPrAssociations_1143d2")}><p>{copy("session-pull-requests.associationsRemainAfterArchiveOrProblem_909961")}</p>
    {list.error ? <button disabled={Boolean(list.loading)} onClick={refresh}>{copy("session-name.retryRead")}</button> : null}<Problem error={paginationError(list.error?.failure)} />{list.error && list.loaded ? <p>{copy("session-pull-requests.previousAssociationsAreShownRefreshFailed_8dbbd3")}</p> : null}
    <ScrollPayloadWindow query={list} root={root} active={active && !blocked}>{(rows, projections) => { const ids = visiblePageIds(list.pages, projections); return rows.filter(row => ids.has(row.id)).map(row => <LinkRow key={row.id} row={row} value={readSessionPR(row, session.id)!} sessionId={session.id} refreshed={refresh} diagnosticsTarget={diagnosticsTarget} />); }}</ScrollPayloadWindow>
    {list.loaded && !list.rows.length ? <p>{copy("session-pull-requests.noPrAssociationsOnThisPage_83305f")}</p> : null}
    <ScrollContinuation query={list} root={root} active={active && !blocked} label={copy("session-pull-requests.sessionPrAssociations_1143d2")} />
  </section>;
}

/** The primary Info section owns visibility; only its reader is disposable.
 * Editors and uncertain requests remain mounted when that reader is closed. */
export function SessionPullRequests({ session, visible = true, emptyChanged, diagnosticsTarget }: { session: Resource; visible?: boolean; emptyChanged?: (value: boolean) => void; diagnosticsTarget?: HTMLElement | null }) {
  useLocale();
  const active = useSessionActive();
  const refreshOwner = useRef<() => void>(() => undefined);
  const refreshed = useCallback(() => refreshOwner.current(), []);
  return <section>
    {visible && active ? <RetainedLinks key={session.id} session={session} refreshOwner={refreshOwner} emptyChanged={emptyChanged} diagnosticsTarget={diagnosticsTarget} /> : null}
    <PendingUnlinks sessionId={session.id} refreshed={refreshed} />
    <OriginalLinkRecovery sessionId={session.id} refreshed={refreshed} />

  </section>;
}

function OriginalLinkRecovery({ sessionId, refreshed }: { sessionId: string; refreshed: () => void }) {
  const link = useRetainedMutation(`session-pr:link:${sessionId}`, SessionQuery.linkSessionPullRequest, refreshed);
  return link.busy || link.uncertain ? <section><Problem error={link.error}/>{link.uncertain ? <button disabled={link.busy} onClick={link.retry}>{copy("session-pull-requests.retryOriginalPrLink_9f5bd0")}</button> : null}</section> : null;
}
