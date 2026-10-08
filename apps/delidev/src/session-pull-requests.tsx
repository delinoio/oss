// SPDX-License-Identifier: Apache-2.0
import { Timestamp } from "./timestamp-display";
import { ScrollContinuation } from "./scroll-continuation";
import { ScrollPayloadWindow } from "./scroll-payload-window";
import { useConnectPaginationReader, usePaginationChain, usePaginationRefresh } from "./scroll-pagination-query";
import { useStablePageRevisions, paginationError, invalidGitHubPage, resourceProjection, useGitHubScrollRoot, visiblePageIds } from "./github-scroll";
import { ownedMessage, useProductMessage, LocalizedText, copy, useLocale  } from "./localization";
import { useCallback, useRef, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { EntityKind, FailureCode, ResourceQuery, SessionQuery, newRequestId, type Resource, type UnlinkSessionPullRequestRequest } from "@delinoio/delidev-api-client";
import { document, items, text, type Document } from "./documents";
import { bounded, date, positive, uuid } from "./github-query-model";
import { useRetainedMutation, useRetainedMutationIntents } from "./mutation";
import { OpenPRProblemHistory } from "./pr-problems";
import { Problem } from "./ui";
import { ResourceChoice } from "./configuration-fields";

const readOptions = { retry: false, gcTime: 0, staleTime: 0, refetchOnWindowFocus: false, refetchOnReconnect: false };
export function readSessionPR(row: Resource, sessionId: string): Document | undefined {
  const value = document(row);
  if (row.kind !== EntityKind.PULL_REQUEST || row.sessionId !== sessionId || !uuid(row.id) || !uuid(row.projectId) || row.revision <= 0n || row.revision >= 1n << 63n || value.version !== 1 || value.provider !== "github.com" || !uuid(value.repository_id) || !positive(value.remote_repository_id) || !positive(value.pull_request_id) || !positive(value.number) || !bounded(value.repository_node_id, 256) || !bounded(value.pull_request_node_id, 256) || !bounded(value.title, 4096) || !date(value.observed_at)) return;
  if (!/^[A-Za-z0-9](?:[A-Za-z0-9-]{0,98}[A-Za-z0-9])?$/.test(text(value.owner)) || !/^[A-Za-z0-9_.-]{1,100}$/.test(text(value.name)) || [".", ".."].includes(text(value.name))) return;
  return value;
}

function LinkForm({ sessionId, projectId, refreshed }: { sessionId: string; projectId: string; refreshed: () => void }) {
  useLocale();
  const [repository, setRepository] = useState(""), [number, setNumber] = useState(""), [notice, setNotice] = useProductMessage("");
  const expected = useRef<{ requestId: string; repositoryId: string; number: string } | undefined>(undefined);
  const project = useQuery(ResourceQuery.getResource, { kind: EntityKind.PROJECT, id: projectId }, readOptions);
  const row = project.data?.resource;
  const candidates = items(document(row).repositories);
  const projectValid = row?.id === projectId && row.kind === EntityKind.PROJECT && candidates.length <= 1000 && candidates.every(uuid) && new Set(candidates).size === candidates.length;
  const repositories = projectValid ? candidates as string[] : [];
  const selected = useQuery(ResourceQuery.getResource, { kind: EntityKind.REPOSITORY, id: repository }, { ...readOptions, enabled: repositories.includes(repository) });
  const configured = selected.data?.resource?.id === repository && selected.data.resource.kind === EntityKind.REPOSITORY ? document(selected.data.resource) : {};
  const link = useRetainedMutation(`session-pr:link:${sessionId}`, SessionQuery.linkSessionPullRequest, (r) => {
    const value = r.association && readSessionPR(r.association, sessionId);
    if (!value || !expected.current || value.repository_id !== expected.current.repositoryId || value.number !== expected.current.number || r.requestId !== expected.current.requestId || r.association?.id !== r.requestId) { setNotice(ownedMessage("session-pull-requests.extra.26513f241135")); refreshed(); return; }
    setNumber(""); setNotice(ownedMessage("session-pull-requests.extra.d68ee597ba2c")); refreshed();
  });
  const blocked = link.busy || link.uncertain;
  const ready = repositories.includes(repository) && uuid(configured.integration_id) && Boolean(text(configured.github_owner) && text(configured.github_name)) && !project.error && !selected.error && positive(number);
  return <form aria-label={copy("session-pull-requests.linkAPrToThisSession_8fefe2")} onSubmit={(event) => { event.preventDefault(); if (blocked || !ready) return; setNotice(""); const input = { requestId: newRequestId(), sessionId, repositoryId: repository, number }; expected.current = input; void link.send(input); }}>
    <fieldset disabled={blocked}><ResourceChoice label={copy("session-pull-requests.prProjectRepository_8efefa")} kind={EntityKind.REPOSITORY} value={repository} change={setRepository} active={projectValid} disabled={blocked} allowed={repositories} />
      {repository ? <p>{text(configured.name)} · {text(configured.github_owner)}/{text(configured.github_name)}{!uuid(configured.integration_id) && !selected.isPending ? copy("session-pull-requests.selectAGithubProfileInRepository_e5f944") : ""}</p> : null}
      <label>{copy("session-pull-requests.prNumber_6f80da")}<input inputMode="numeric" maxLength={20} value={number} onChange={(e) => setNumber(e.target.value)} required /></label>
    </fieldset><p>{copy("session-pull-requests.theServerVerifiesThePrThrough_7d9700")}</p>
    <Problem error={project.error || selected.error || link.error} />{notice ? <p role="status">{notice}</p> : null}
    <button disabled={blocked || !ready}>{copy("session-pull-requests.linkPr_adf073")}</button>{link.uncertain ? <button type="button" disabled={link.busy} onClick={() => { expected.current = link.input as { requestId: string; repositoryId: string; number: string }; void link.retry(); }}>{copy("session-pull-requests.retryOriginalPrLink_9f5bd0")}</button> : null}
  </form>;
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

function LinkRow({ row, value, sessionId, refreshed }: { row: Resource; value: Document; sessionId: string; refreshed: () => void }) {
  const remove = usePRUnlink(sessionId, row.id, refreshed);
  return <article aria-label={copy("session-pull-requests.linkedPr_299ef1", { v0: text(value.owner), v1: text(value.name), v2: text(value.number) })}>
    <h4>{text(value.owner)}/{text(value.name)}#{text(value.number)}</h4><p>{text(value.title)}</p>
    <p><LocalizedText id="session-pull-requests.linkedObservationCurrentPrStateAnd_e8d519" components={{ s0: <><Timestamp value={text(value.observed_at)} /></> }} /></p>
    <details><summary>{copy("session-pull-requests.originalPrIdentity_92b511")}</summary><p><LocalizedText id="session-pull-requests.repositoryIdPrIdNode_78f0da" components={{ s0: <>{text(value.remote_repository_id)}</>, s1: <>{text(value.pull_request_id)}</>, s2: <>{text(value.pull_request_node_id)}</> }} /></p><p><LocalizedText id="session-pull-requests.configuredRepository_6aa131" components={{ s0: <>{text(value.repository_id)}</> }} /></p><p>{`https://github.com/${text(value.owner)}/${text(value.name)}/pull/${text(value.number)}`}</p></details>
    <OpenPRProblemHistory selection={{ repositoryId: text(value.repository_id), remoteRepositoryId: text(value.remote_repository_id), pullRequestId: text(value.pull_request_id), number: text(value.number) }} />
    <button disabled={remove.busy || remove.uncertain} onClick={() => void remove.send({ sessionId, mutation: { id: row.id, expectedRevision: row.revision, requestId: newRequestId() } })}><LocalizedText id="session-pull-requests.unlink_1c427a" components={{ s0: <>{text(value.number)}</> }} /></button>
    {!remove.busy && !remove.uncertain ? <Problem error={remove.error} /> : null}
  </article>;
}

function RetainedLinks({ session }: { session: Resource }) {
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
  const list = usePaginationChain(session.id, true, reader);
  usePaginationRefresh(ResourceQuery.listResources, request(""), !blocked, list.refresh);
  const refresh = list.error ? list.error.stalled || list.error.failure.code === FailureCode.CursorExpired ? list.reload : list.retry : list.refresh;
  return <section ref={bindRoot} aria-label={copy("session-pull-requests.sessionPrAssociations_1143d2")}><p>{copy("session-pull-requests.associationsRemainAfterArchiveOrProblem_909961")}</p>
    <button disabled={Boolean(list.loading)} onClick={refresh}>{copy("session-pull-requests.refreshPrAssociations_2e9a89")}</button><Problem error={paginationError(list.error?.failure)} />{list.error && list.loaded ? <p>{copy("session-pull-requests.previousAssociationsAreShownRefreshFailed_8dbbd3")}</p> : null}
    <PendingUnlinks sessionId={session.id} refreshed={refresh} />
    <ScrollPayloadWindow query={list} root={root} active={!blocked}>{(rows, projections) => { const ids = visiblePageIds(list.pages, projections); return rows.filter(row => ids.has(row.id)).map(row => <LinkRow key={row.id} row={row} value={readSessionPR(row, session.id)!} sessionId={session.id} refreshed={refresh} />); }}</ScrollPayloadWindow>
    {list.loaded && !list.rows.length ? <p>{copy("session-pull-requests.noPrAssociationsOnThisPage_83305f")}</p> : null}
    <ScrollContinuation query={list} root={root} active={!blocked} label={copy("session-pull-requests.sessionPrAssociations_1143d2")} />
    {uuid(session.projectId) ? <LinkForm sessionId={session.id} projectId={session.projectId} refreshed={refresh} /> : <p>{copy("session-pull-requests.linkingAPrRequiresAProject_301f3d")}</p>}
  </section>;
}

export function SessionPullRequests({ session }: { session: Resource }) {
  useLocale();
  const [open, setOpen] = useState(false);
  return <section><button aria-expanded={open} onClick={() => setOpen(!open)}>{open ? copy("session-pull-requests.closePrAssociations_622b6a") : copy("session-pull-requests.showPrAssociations_8dbf45")}</button>{open ? <RetainedLinks key={session.id} session={session} /> : null}</section>;
}
