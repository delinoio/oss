import { useEffect, useRef, useState, type FormEvent } from "react";
import { useQuery } from "@connectrpc/connect-query";
import {
  ActivityKind, ActivityQuery, ActivityPRAttemptState, ActivityPRActorType, ActivityPRMode, EntityKind, ResourceQuery,
  SearchArchiveState, SearchExecutionOutcome, SearchQuery,
} from "@delinoio/delidev-api-client";
import { document, text } from "./documents";
import { Problem } from "./ui";
import { ResourceChoice } from "./configuration-fields";
import { SidebarSurface, useCloseSidebarDrawer, useSidebarDrawerOpen } from "./sidebar-context";
import { ActivityPRSource } from "./activity-pr-source";

export enum Surface { Sessions = "sessions", NewSession = "new-session", PullRequests = "pull-requests", Usage = "usage", Schedules = "schedules", Activity = "activity", Inbox = "inbox", Search = "search" }
function Pager({ page, next, setPage, busy }: { page: string; next?: string; setPage: (value: string) => void; busy: boolean }) {
  return <nav aria-label="Results pages"><button disabled={!page || busy} onClick={() => setPage("")}>First page</button><button disabled={!next || busy} onClick={() => setPage(next!)}>Next page</button></nav>;
}

interface SearchFilters { query: string; archive: SearchArchiveState; projectId: string; sessionId: string; agentId: string; accountId: string; outcome: SearchExecutionOutcome }
const emptySearch: SearchFilters = { query: "", archive: SearchArchiveState.UNSPECIFIED, projectId: "", sessionId: "", agentId: "", accountId: "", outcome: SearchExecutionOutcome.UNSPECIFIED };

export function Search({ active, open }: { active: boolean; open: (id: string) => void }) {
  const [draft, setDraft] = useState<SearchFilters>(emptySearch);
  const [query, setQuery] = useState<SearchFilters>();
  const [page, setPage] = useState("");
  const searchInput = useRef<HTMLInputElement>(null);
  const focusedOnce = useRef(false);
  const closeDrawer = useCloseSidebarDrawer();
  const drawerOpen = useSidebarDrawerOpen();
  const result = useQuery(SearchQuery.searchConversations, { ...(query ?? emptySearch), pageSize: 30, pageToken: page }, { enabled: active && Boolean(query?.query.trim()) });
  useEffect(() => {
    if (!active || focusedOnce.current) return;
    if (typeof window.matchMedia === "function" && window.matchMedia("(max-width: 759px)").matches && !drawerOpen) return;
    const frame = window.requestAnimationFrame(() => { searchInput.current?.focus(); focusedOnce.current = true; });
    return () => window.cancelAnimationFrame(frame);
  }, [active, drawerOpen]);
  const change = <K extends keyof SearchFilters>(key: K, value: SearchFilters[K]) => setDraft((current) => ({ ...current, [key]: value }));
  const submit = (event: FormEvent) => { event.preventDefault(); if (!draft.query.trim()) return; setQuery({ ...draft, query: draft.query.trim() }); setPage(""); closeDrawer(); };
  return <>
    <SidebarSurface active={active} title="Search">
      <form className="sidebar-form" onSubmit={submit}>
        <label>Search conversations<input ref={searchInput} value={draft.query} onChange={(event) => change("query", event.target.value)} /></label>
        <label>Archive<select value={draft.archive} onChange={(event) => change("archive", Number(event.target.value) as SearchArchiveState)}><option value={SearchArchiveState.UNSPECIFIED}>Include archived</option><option value={SearchArchiveState.ACTIVE}>Active only</option><option value={SearchArchiveState.ARCHIVED}>Archived only</option></select></label>
        <ResourceChoice label="Project" kind={EntityKind.PROJECT} value={draft.projectId} change={(id) => change("projectId", id)} active={active} />
        <ResourceChoice label="Session" kind={EntityKind.SESSION} value={draft.sessionId} change={(id) => change("sessionId", id)} active={active} />
        <ResourceChoice label="Agent Worker" kind={EntityKind.AGENT} value={draft.agentId} change={(id) => change("agentId", id)} active={active} />
        <ResourceChoice label="Account" kind={EntityKind.ACCOUNT} value={draft.accountId} change={(id) => change("accountId", id)} active={active} />
        <label>Outcome<select value={draft.outcome} onChange={(event) => change("outcome", Number(event.target.value) as SearchExecutionOutcome)}>{[
          [SearchExecutionOutcome.UNSPECIFIED, "All"], [SearchExecutionOutcome.NOT_STARTED, "Not started"], [SearchExecutionOutcome.RUNNING, "Running"], [SearchExecutionOutcome.SUCCEEDED, "Succeeded"], [SearchExecutionOutcome.FAILED, "Failed"], [SearchExecutionOutcome.STOPPED, "Stopped"],
        ].map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select></label>
        <button className="primary" disabled={!draft.query.trim()}>Search</button>
      </form>
    </SidebarSurface>
    <section hidden={!active} className="page"><h2>Search conversations</h2>
    {!query ? <p>Choose a search term and filters, then search retained conversations.</p> : null}
    <Problem error={result.error} />{query && result.isPending ? <p role="status">Searching…</p> : null}{query && result.error && result.data ? <p className="notice">The refresh failed. These are the last results for the applied search.</p> : null}
    {result.data?.hits.map((hit) => <article key={hit.message?.id} className="result"><button disabled={!hit.message?.sessionId} onClick={() => open(hit.message!.sessionId)}>{hit.sessionName}</button><p>{text(document(hit.message).text)}</p><small>{hit.message?.sessionId}</small></article>)}
    {result.data?.hits.length === 0 ? <p>{page ? "No further conversations on this page." : "No retained conversation matches."}</p> : null}<Pager page={page} setPage={setPage} next={result.data?.nextPageToken} busy={result.isFetching} />
  </section></>;
}

interface ActivityFilters { projectId: string; sessionId: string }
const emptyActivity: ActivityFilters = { projectId: "", sessionId: "" };
export function Activity({ active, open }: { active: boolean; open: (id: string) => void }) {
  const [page, setPage] = useState("");
  const [draft, setDraft] = useState<ActivityFilters>(emptyActivity);
  const [selection, setSelection] = useState<ActivityFilters>(emptyActivity);
  const closeDrawer = useCloseSidebarDrawer();
  const result = useQuery(ActivityQuery.listActivity, { projectId: selection.projectId, sessionId: selection.sessionId, pageSize: 50, pageToken: page }, { enabled: active });
  const apply = (next: ActivityFilters) => { setSelection(next); setPage(""); closeDrawer(); };
  return <>
  <SidebarSurface active={active} title="Activity">
    <div className="sidebar-filter-options"><button type="button" aria-pressed={!selection.projectId && !selection.sessionId} onClick={() => { setDraft(emptyActivity); apply(emptyActivity); }}>All activity</button></div>
    <ResourceChoice label="Project" kind={EntityKind.PROJECT} value={draft.projectId} change={(projectId) => setDraft((current) => ({ ...current, projectId }))} active={active} />
    <ResourceChoice label="Session" kind={EntityKind.SESSION} value={draft.sessionId} change={(sessionId) => setDraft((current) => ({ ...current, sessionId }))} active={active} />
    <div className="actions"><button className="primary" onClick={() => apply(draft)}>Apply filters</button><button onClick={() => { setDraft(emptyActivity); apply(emptyActivity); }}>Reset</button></div>
  </SidebarSurface>
  <section hidden={!active} className="page"><header><h2>Activity</h2><button disabled={result.isFetching} onClick={() => { setPage(""); void result.refetch(); }}>Refresh</button></header><Problem error={result.error} />{result.isFetching ? <p role="status">Loading activity…</p> : null}{result.error && result.data ? <p className="notice">The refresh failed. These are the last activity rows for this scope.</p> : null}
    {result.data?.entries.map((entry) => <article className="result" key={entry.id}><strong>{ActivityKind[entry.kind]?.toLowerCase().replaceAll("_", " ")}</strong><p><time dateTime={new Date(Number(entry.observedAtUnixMs)).toISOString()}>{new Date(Number(entry.observedAtUnixMs)).toLocaleString()}</time></p>{entry.pullRequest ? <>
      <p>{entry.pullRequest.owner}/{entry.pullRequest.name} #{entry.pullRequest.number}</p>
      {entry.pullRequest.attemptState !== ActivityPRAttemptState.ACTIVITY_PR_ATTEMPT_STATE_UNSPECIFIED ? <p>Attempt: {ActivityPRAttemptState[entry.pullRequest.attemptState]?.replace("ACTIVITY_PR_ATTEMPT_STATE_", "").toLowerCase().replaceAll("_", " ")} · {ActivityPRMode[entry.pullRequest.mode]?.replace("ACTIVITY_PR_MODE_", "").toLowerCase()}. Success does not establish verified handling.</p> : null}
      <p>Actor: {ActivityPRActorType[entry.pullRequest.actorType]?.replace("ACTIVITY_PR_ACTOR_TYPE_", "").toLowerCase()}{entry.pullRequest.deviceId ? ` ${entry.pullRequest.deviceId}` : ""}</p>
      <details><summary>Original activity references</summary><p>Source {entry.pullRequest.sourceId} · revision {entry.sourceRevision.toString()} · request {entry.pullRequest.requestId}</p>{entry.pullRequest.problems.map(ref => <p key={ref.id}>Problem {ref.id} · version <code>{ref.contentVersion}</code></p>)}</details>
      <ActivityPRSource target={entry.pullRequest} revision={entry.sourceRevision} />
    </> : null}{entry.sessionId ? <button onClick={() => open(entry.sessionId)}>Open session</button> : !entry.pullRequest ? <p>Waiting or skipped occurrence</p> : null}{entry.accountId ? <small>Account {entry.accountId}</small> : null}</article>)}
    {result.data?.entries.length === 0 ? <p>{page ? "No further activity on this page." : "No activity yet."}</p> : null}<Pager page={page} setPage={setPage} next={result.data?.nextPageToken} busy={result.isFetching} />
  </section></>;
}

export { Settings } from "./settings";
