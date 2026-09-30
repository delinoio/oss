import { useEffect, useRef, useState, type FormEvent } from "react";
import { useQuery } from "@connectrpc/connect-query";
import {
  ActivityKind, ActivityQuery, EntityKind, ResourceQuery,
  SearchArchiveState, SearchExecutionOutcome, SearchQuery,
} from "@delinoio/delidev-api-client";
import { document, resourceName, text } from "./documents";
import { Problem } from "./ui";
import { ResourceChoice } from "./configuration-fields";
import { SidebarSurface, useCloseSidebarDrawer, useSidebarDrawerOpen } from "./sidebar-context";

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
interface ActivityDraft extends ActivityFilters { projectLabel: string; sessionLabel: string }
const emptyActivityDraft: ActivityDraft = { ...emptyActivity, projectLabel: "", sessionLabel: "" };

// Native selects can clip names. Retain only the two selected labels from the
// existing change callbacks, with exact IDs and explicit last-selected wording;
// this text never claims a fresh/off-page read or adds a resource lookup.
function ActivitySelectedLabel({ label, id, name }: { label: string; id: string; name: string }) {
  return id ? <p className="activity-selected-label">{name ? <>Last selected {label.toLowerCase()} label: <span>{name}</span><br /></> : null}Selected {label.toLowerCase()} ID: <span>{id}</span></p> : null;
}
export function Activity({ active, open }: { active: boolean; open: (id: string) => void }) {
  const [page, setPage] = useState("");
  const [draft, setDraft] = useState<ActivityDraft>(emptyActivityDraft);
  const [selection, setSelection] = useState<ActivityFilters>(emptyActivity);
  const closeDrawer = useCloseSidebarDrawer();
  const result = useQuery(ActivityQuery.listActivity, { projectId: selection.projectId, sessionId: selection.sessionId, pageSize: 50, pageToken: page }, { enabled: active });
  const apply = (next: ActivityFilters) => { setSelection(next); setPage(""); closeDrawer(); };
  return <>
  <SidebarSurface active={active} title="Activity" className="activity-sidebar">
    <button type="button" className="activity-all" aria-pressed={!selection.projectId && !selection.sessionId} onClick={() => { setDraft(emptyActivityDraft); apply(emptyActivity); }}>
      <svg className="activity-filter-icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" aria-hidden="true"><path d="M5 4h8M5 8h8M5 12h8M2 4h.01M2 8h.01M2 12h.01" /></svg>
      <span>All activity</span>
      {!selection.projectId && !selection.sessionId ? <svg className="activity-filter-icon activity-selected-check" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="m3 8 3 3 7-7" /></svg> : null}
    </button>
    <div className="activity-filter-group" role="group" aria-label="Activity filters">
      <h3>FILTERS</h3>
      <div>
        <ResourceChoice label="Project" kind={EntityKind.PROJECT} value={draft.projectId} change={(projectId, _data, row) => setDraft((current) => ({ ...current, projectId, projectLabel: row ? resourceName(row) : "" }))} active={active} showStatus />
        <ActivitySelectedLabel label="Project" id={draft.projectId} name={draft.projectLabel} />
      </div>
      <div>
        <ResourceChoice label="Session" kind={EntityKind.SESSION} value={draft.sessionId} change={(sessionId, _data, row) => setDraft((current) => ({ ...current, sessionId, sessionLabel: row ? resourceName(row) : "" }))} active={active} showStatus />
        <ActivitySelectedLabel label="Session" id={draft.sessionId} name={draft.sessionLabel} />
      </div>
      <div className="activity-filter-actions"><button type="button" className="activity-apply" onClick={() => apply({ projectId: draft.projectId, sessionId: draft.sessionId })}>Apply filters</button><button type="button" className="activity-reset" onClick={() => { setDraft(emptyActivityDraft); apply(emptyActivity); }}>Reset</button></div>
    </div>
  </SidebarSurface>
  <section hidden={!active} className="page"><header><h2>Activity</h2><button disabled={result.isFetching} onClick={() => { setPage(""); void result.refetch(); }}>Refresh</button></header><Problem error={result.error} />{result.isFetching ? <p role="status">Loading activity…</p> : null}{result.error && result.data ? <p className="notice">The refresh failed. These are the last activity rows for this scope.</p> : null}
    {result.data?.entries.map((entry) => <article className="result" key={entry.id}><strong>{ActivityKind[entry.kind]?.toLowerCase().replaceAll("_", " ")}</strong><p><time dateTime={new Date(Number(entry.observedAtUnixMs)).toISOString()}>{new Date(Number(entry.observedAtUnixMs)).toLocaleString()}</time></p>{entry.sessionId ? <button onClick={() => open(entry.sessionId)}>Open session</button> : <p>Waiting or skipped occurrence</p>}{entry.accountId ? <small>Account {entry.accountId}</small> : null}</article>)}
    {result.data?.entries.length === 0 ? <p>{page ? "No further activity on this page." : "No activity yet."}</p> : null}<Pager page={page} setPage={setPage} next={result.data?.nextPageToken} busy={result.isFetching} />
  </section></>;
}

export { Settings } from "./settings";
