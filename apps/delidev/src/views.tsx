import { LocalizedText, copy, displayLocale, useLocale } from "./localization";
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
import { ActivityPRDetails } from "./activity-pr-source";
import "./activity-sidebar.css";

const activityNames: Partial<Record<ActivityKind, import("./localization").MessageKey>> = {
  [ActivityKind.UNSPECIFIED]: "views.activity.UNSPECIFIED",
  [ActivityKind.EXECUTION_ACCEPTED]: "views.activity.EXECUTION_ACCEPTED",
  [ActivityKind.EXECUTION_SUCCEEDED]: "views.activity.EXECUTION_SUCCEEDED",
  [ActivityKind.EXECUTION_FAILED]: "views.activity.EXECUTION_FAILED",
  [ActivityKind.EXECUTION_STOPPED]: "views.activity.EXECUTION_STOPPED",
  [ActivityKind.SCHEDULE_CRON]: "views.activity.SCHEDULE_CRON",
  [ActivityKind.SCHEDULE_RUN_NOW]: "views.activity.SCHEDULE_RUN_NOW",
  [ActivityKind.PR_PROBLEM_OBSERVED]: "views.activity.PR_PROBLEM_OBSERVED",
  [ActivityKind.PR_PROBLEM_DISMISSED]: "views.activity.PR_PROBLEM_DISMISSED",
  [ActivityKind.PR_REMEDIATION_ATTEMPT]: "views.activity.PR_REMEDIATION_ATTEMPT",
  [ActivityKind.PR_VERIFIED_HANDLED]: "views.activity.PR_VERIFIED_HANDLED"
};

export enum Surface { Sessions = "sessions", NewSession = "new-session", PullRequests = "pull-requests", Usage = "usage", Schedules = "schedules", Activity = "activity", Inbox = "inbox", Search = "search", Settings = "settings" }
function Pager({ page, next, setPage, busy }: { page: string; next?: string; setPage: (value: string) => void; busy: boolean }) {
  useLocale();
  return <nav aria-label={copy("views.resultsPages_9c69dd")}><button disabled={!page || busy} onClick={() => setPage("")}>{copy("views.firstPage_0bdbb7")}</button><button disabled={!next || busy} onClick={() => setPage(next!)}>{copy("views.nextPage_c08ac7")}</button></nav>;
}

interface SearchFilters { query: string; archive: SearchArchiveState; projectId: string; sessionId: string; agentId: string; accountId: string; outcome: SearchExecutionOutcome }
const emptySearch: SearchFilters = { query: "", archive: SearchArchiveState.UNSPECIFIED, projectId: "", sessionId: "", agentId: "", accountId: "", outcome: SearchExecutionOutcome.UNSPECIFIED };

export function Search({ active, open }: { active: boolean; open: (id: string) => void }) {
  useLocale();
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
    <SidebarSurface active={active} title={copy("views.search_49c266")}>
      <form className="sidebar-form" onSubmit={submit}>
        <label>{copy("views.searchConversations_8abdf3")}<input ref={searchInput} value={draft.query} onChange={(event) => change("query", event.target.value)} /></label>
        <label>{copy("views.archive_66f480")}<select value={draft.archive} onChange={(event) => change("archive", Number(event.target.value) as SearchArchiveState)}><option value={SearchArchiveState.UNSPECIFIED}>{copy("views.includeArchived_b6c334")}</option><option value={SearchArchiveState.ACTIVE}>{copy("views.activeOnly_a9b5ed")}</option><option value={SearchArchiveState.ARCHIVED}>{copy("views.archivedOnly_da7ebd")}</option></select></label>
        <ResourceChoice label={copy("views.project_985959")} kind={EntityKind.PROJECT} value={draft.projectId} change={(id) => change("projectId", id)} active={active} />
        <ResourceChoice label={copy("views.session_6959b4")} kind={EntityKind.SESSION} value={draft.sessionId} change={(id) => change("sessionId", id)} active={active} />
        <ResourceChoice label={copy("views.agentWorker_a4caa7")} kind={EntityKind.AGENT} value={draft.agentId} change={(id) => change("agentId", id)} active={active} />
        <ResourceChoice label={copy("views.account_7e1b0d")} kind={EntityKind.ACCOUNT} value={draft.accountId} change={(id) => change("accountId", id)} active={active} />
        <label>{copy("views.outcome_4e80ab")}<select value={draft.outcome} onChange={(event) => change("outcome", Number(event.target.value) as SearchExecutionOutcome)}>{[
          [SearchExecutionOutcome.UNSPECIFIED, copy("views.extra.a52ace420f21")], [SearchExecutionOutcome.NOT_STARTED, copy("views.extra.ba35f0c47d86")], [SearchExecutionOutcome.RUNNING, copy("views.extra.f4ccae29e1bb")], [SearchExecutionOutcome.SUCCEEDED, copy("views.extra.6d9a6f97a5fd")], [SearchExecutionOutcome.FAILED, copy("views.extra.031a8f0f659d")], [SearchExecutionOutcome.STOPPED, copy("views.extra.1a4f630ac1b6")],
        ].map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select></label>
        <button className="primary" disabled={!draft.query.trim()}>{copy("views.search_49c266")}</button>
      </form>
    </SidebarSurface>
    <section hidden={!active} className="page"><h2>{copy("views.searchConversations_8abdf3")}</h2>
    {!query ? <p>{copy("views.chooseASearchTermAndFilters_99f204")}</p> : null}
    <Problem error={result.error} />{query && result.isPending ? <p role="status">{copy("views.searching_c31723")}</p> : null}{query && result.error && result.data ? <p className="notice">{copy("views.theRefreshFailedTheseAreThe_22c320")}</p> : null}
    {result.data?.hits.map((hit) => <article key={hit.message?.id} className="result"><button disabled={!hit.message?.sessionId} onClick={() => open(hit.message!.sessionId)}>{hit.sessionName}</button><p>{text(document(hit.message).text)}</p><small>{hit.message?.sessionId}</small></article>)}
    {result.data?.hits.length === 0 ? <p>{page ? copy("views.noFurtherConversationsOnThisPage_5b8014") : copy("views.noRetainedConversationMatches_b59f78")}</p> : null}<Pager page={page} setPage={setPage} next={result.data?.nextPageToken} busy={result.isFetching} />
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
  useLocale();
  return id ? <p className="activity-selected-label"><LocalizedText id="views.selectedId_e226b5" components={{ s0: <>{name ? <><LocalizedText id="views.lastSelectedLabel_2f7d02" components={{ s0: <>{label.toLowerCase()}</>, s1: <span>{name}</span>, s2: <br /> }} /></> : null}</>, s1: <>{label.toLowerCase()}</>, s2: <span>{id}</span> }} /></p> : null;
}
export function Activity({ active, open }: { active: boolean; open: (id: string) => void }) {
  useLocale();
  const [page, setPage] = useState("");
  const [draft, setDraft] = useState<ActivityDraft>(emptyActivityDraft);
  const [selection, setSelection] = useState<ActivityFilters>(emptyActivity);
  const closeDrawer = useCloseSidebarDrawer();
  const result = useQuery(ActivityQuery.listActivity, { projectId: selection.projectId, sessionId: selection.sessionId, pageSize: 50, pageToken: page }, { enabled: active });
  const apply = (next: ActivityFilters) => { setSelection(next); setPage(""); closeDrawer(); };
  return <>
  <SidebarSurface active={active} title={copy("views.activity_38da15")} className="activity-sidebar">
    <button type="button" className="activity-all" aria-pressed={!selection.projectId && !selection.sessionId} onClick={() => { setDraft(emptyActivityDraft); apply(emptyActivity); }}>
      <svg className="activity-filter-icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" aria-hidden="true"><path d="M5 4h8M5 8h8M5 12h8M2 4h.01M2 8h.01M2 12h.01" /></svg>
      <span>{copy("views.allActivity_29ebb2")}</span>
      {!selection.projectId && !selection.sessionId ? <svg className="activity-filter-icon activity-selected-check" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="m3 8 3 3 7-7" /></svg> : null}
    </button>
    <div className="activity-filter-group" role="group" aria-label={copy("views.activityFilters_b58a53")}>
      <h3>{copy("views.filters_29ded9")}</h3>
      <div>
        <ResourceChoice label={copy("views.project_985959")} kind={EntityKind.PROJECT} value={draft.projectId} change={(projectId, _data, row) => setDraft((current) => ({ ...current, projectId, projectLabel: row ? resourceName(row) : "" }))} active={active} showStatus />
        <ActivitySelectedLabel label={copy("views.project_985959")} id={draft.projectId} name={draft.projectLabel} />
      </div>
      <div>
        <ResourceChoice label={copy("views.session_6959b4")} kind={EntityKind.SESSION} value={draft.sessionId} change={(sessionId, _data, row) => setDraft((current) => ({ ...current, sessionId, sessionLabel: row ? resourceName(row) : "" }))} active={active} showStatus />
        <ActivitySelectedLabel label={copy("views.session_6959b4")} id={draft.sessionId} name={draft.sessionLabel} />
      </div>
      <div className="activity-filter-actions"><button type="button" className="activity-apply" onClick={() => apply({ projectId: draft.projectId, sessionId: draft.sessionId })}>{copy("views.applyFilters_d80ab1")}</button><button type="button" className="activity-reset" onClick={() => { setDraft(emptyActivityDraft); apply(emptyActivity); }}>{copy("views.reset_daee76")}</button></div>
    </div>
  </SidebarSurface>
  <section hidden={!active} className="page"><header><h2>{copy("views.activity_38da15")}</h2><button disabled={result.isFetching} onClick={() => { setPage(""); void result.refetch(); }}>{copy("views.refresh_0e9161")}</button></header><Problem error={result.error} />{result.isFetching ? <p role="status">{copy("views.loadingActivity_a389c3")}</p> : null}{result.error && result.data ? <p className="notice">{copy("views.theRefreshFailedTheseAreThe_c8711b")}</p> : null}
    {result.data?.entries.map((entry) => <article className="result" key={entry.id}><strong>{activityNames[entry.kind] ? copy(activityNames[entry.kind]!) : copy("views.activity.UNSPECIFIED")}</strong><p><time dateTime={new Date(Number(entry.observedAtUnixMs)).toISOString()}>{new Date(Number(entry.observedAtUnixMs)).toLocaleString(displayLocale())}</time></p>{entry.pullRequest ? <ActivityPRDetails target={entry.pullRequest} revision={entry.sourceRevision} active={active} /> : null}{entry.sessionId ? <button onClick={() => open(entry.sessionId)}>{copy("views.openSession_b205bb")}</button> : !entry.pullRequest ? <p>{copy("views.waitingOrSkippedOccurrence_94fac5")}</p> : null}{entry.accountId ? <small><LocalizedText id="views.account_cc4945" components={{ s0: <>{entry.accountId}</> }} /></small> : null}</article>)}
    {result.data?.entries.length === 0 ? <p>{page ? copy("views.noFurtherActivityOnThisPage_1ca656") : copy("views.noActivityYet_a288d2")}</p> : null}<Pager page={page} setPage={setPage} next={result.data?.nextPageToken} busy={result.isFetching} />
  </section></>;
}

export { Settings } from "./settings";
