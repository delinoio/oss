import { useRef, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { EntityKind, ResourceQuery, SessionQuery, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, items, text, type Document } from "./documents";
import { bounded, date, positive, uuid } from "./github-query-model";
import { useRetainedMutation } from "./mutation";
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
  const [repository, setRepository] = useState(""), [number, setNumber] = useState(""), [notice, setNotice] = useState("");
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
    if (!value || !expected.current || value.repository_id !== expected.current.repositoryId || value.number !== expected.current.number || r.requestId !== expected.current.requestId || r.association?.id !== r.requestId) { setNotice("The link was acknowledged, but its response could not be verified. Refresh retained associations before another operation."); refreshed(); return; }
    setNumber(""); setNotice("PR association saved."); refreshed();
  });
  const blocked = link.busy || link.uncertain;
  const ready = repositories.includes(repository) && uuid(configured.integration_id) && Boolean(text(configured.github_owner) && text(configured.github_name)) && !project.error && !selected.error && positive(number);
  return <form aria-label="Link a PR to this session" onSubmit={(event) => { event.preventDefault(); if (blocked || !ready) return; setNotice(""); const input = { requestId: newRequestId(), sessionId, repositoryId: repository, number }; expected.current = input; void link.send(input); }}>
    <fieldset disabled={blocked}><ResourceChoice label="PR project repository" kind={EntityKind.REPOSITORY} value={repository} change={setRepository} active={projectValid} disabled={blocked} allowed={repositories} />
      {repository ? <p>{text(configured.name)} · {text(configured.github_owner)}/{text(configured.github_name)}{!uuid(configured.integration_id) && !selected.isPending ? " · Select a GitHub profile in repository settings first." : ""}</p> : null}
      <label>PR number<input inputMode="numeric" maxLength={20} value={number} onChange={(e) => setNumber(e.target.value)} required /></label>
    </fieldset><p>The server verifies the PR through this repository's selected GitHub profile. Linking preserves an association and does not start an agent or authorize a fix.</p>
    <Problem error={project.error || selected.error || link.error} />{notice ? <p role="status">{notice}</p> : null}
    <button disabled={blocked || !ready}>Link PR</button>{link.uncertain ? <button type="button" disabled={link.busy} onClick={() => { expected.current = link.input as { requestId: string; repositoryId: string; number: string }; void link.retry(); }}>Retry original PR link</button> : null}
  </form>;
}

function LinkRow({ row, value, sessionId, refreshed }: { row: Resource; value: Document; sessionId: string; refreshed: () => void }) {
  const [notice, setNotice] = useState("");
  const remove = useRetainedMutation(`session-pr:unlink:${sessionId}:${row.id}`, SessionQuery.unlinkSessionPullRequest, (r) => { if (r.id !== row.id) setNotice("The unlink acknowledgment could not be verified. Refresh current associations."); refreshed(); });
  return <article aria-label={`Linked PR ${text(value.owner)}/${text(value.name)}#${text(value.number)}`}>
    <h4>{text(value.owner)}/{text(value.name)}#{text(value.number)}</h4><p>{text(value.title)}</p>
    <p>Linked observation: {text(value.observed_at)}. Current PR state and access may have changed.</p>
    <details><summary>Original PR identity</summary><p>Repository ID {text(value.remote_repository_id)} · PR ID {text(value.pull_request_id)} · Node {text(value.pull_request_node_id)}</p><p>Configured repository: {text(value.repository_id)}</p><p>{`https://github.com/${text(value.owner)}/${text(value.name)}/pull/${text(value.number)}`}</p></details>
    <OpenPRProblemHistory selection={{ repositoryId: text(value.repository_id), remoteRepositoryId: text(value.remote_repository_id), pullRequestId: text(value.pull_request_id), number: text(value.number) }} />
    <button disabled={remove.busy || remove.uncertain} onClick={() => void remove.send({ sessionId, mutation: { id: row.id, expectedRevision: row.revision, requestId: newRequestId() } })}>Unlink #{text(value.number)}</button>
    <Problem error={remove.error} />{notice ? <p role="alert">{notice}</p> : null}{remove.uncertain ? <button disabled={remove.busy} onClick={remove.retry}>Retry original PR unlink</button> : null}
  </article>;
}

function RetainedLinks({ session }: { session: Resource }) {
  const [page, setPage] = useState("");
  const list = useQuery(ResourceQuery.listResources, { filter: { kind: EntityKind.PULL_REQUEST, sessionId: session.id, pageSize: 50, pageToken: page } }, readOptions);
  const refresh = () => { void list.refetch(); };
  const rows = list.data?.resources ?? [], decoded = rows.map((row) => readSessionPR(row, session.id));
  const valid = rows.length <= 50 && new Set(rows.map((row) => row.id)).size === rows.length && decoded.every(Boolean);
  return <section aria-label="Session PR associations"><p>Associations remain after Archive or problem resolution. Unlinking removes only this session's association.</p>
    <button disabled={list.isFetching} onClick={refresh}>Refresh PR associations</button><Problem error={list.error} />{list.error && list.data ? <p>Previous associations are shown; refresh failed.</p> : null}
    {list.isPending ? <p>Loading PR associations…</p> : !valid ? <p role="alert">The association page is inconsistent and cannot be displayed.</p> : rows.length ? rows.map((row, index) => <LinkRow key={row.id} row={row} value={decoded[index]!} sessionId={session.id} refreshed={refresh} />) : <p>No PR associations on this page.</p>}
    <nav aria-label="PR association pages"><button disabled={!page || list.isFetching} onClick={() => setPage("")}>First association page</button><button disabled={!valid || !list.data?.nextPageToken || list.isFetching} onClick={() => setPage(list.data!.nextPageToken)}>Next association page</button></nav>
    {uuid(session.projectId) ? <LinkForm sessionId={session.id} projectId={session.projectId} refreshed={refresh} /> : <p>Linking a PR requires a project session. Existing associations remain inspectable.</p>}
  </section>;
}

export function SessionPullRequests({ session }: { session: Resource }) {
  const [open, setOpen] = useState(false);
  return <section><button aria-expanded={open} onClick={() => setOpen(!open)}>{open ? "Close PR associations" : "Show PR associations"}</button>{open ? <RetainedLinks key={session.id} session={session} /> : null}</section>;
}
