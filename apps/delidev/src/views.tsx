import { BudgetFields, budgetInput, emptyBudget } from "./session-budget";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { useQuery } from "@connectrpc/connect-query";
import {
  ActivityKind, ActivityQuery, EntityKind, ResourceQuery,
  SearchArchiveState, SearchExecutionOutcome, SearchQuery, SessionQuery, newRequestId,
} from "@delinoio/delidev-api-client";
import { document, encode, items, Mode, object, resourceName, text, Workspace } from "./documents";
import { useRetainedMutation } from "./mutation";
import { Modal, Problem } from "./ui";
import { ResourceChoice } from "./configuration-fields";
import { StartingReferences } from "./schedules";
import { useLocalWorkerProof, type ReadLocalWorkerProof } from "./local-worker";
import { SidebarSurface, useCloseSidebarDrawer, useSidebarDrawerOpen } from "./sidebar-context";

export enum Surface { Sessions = "sessions", PullRequests = "pull-requests", Usage = "usage", Schedules = "schedules", Activity = "activity", Inbox = "inbox", Search = "search" }
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
    {result.data?.entries.map((entry) => <article className="result" key={entry.id}><strong>{ActivityKind[entry.kind]?.toLowerCase().replaceAll("_", " ")}</strong><p><time dateTime={new Date(Number(entry.observedAtUnixMs)).toISOString()}>{new Date(Number(entry.observedAtUnixMs)).toLocaleString()}</time></p>{entry.sessionId ? <button onClick={() => open(entry.sessionId)}>Open session</button> : <p>Waiting or skipped occurrence</p>}{entry.accountId ? <small>Account {entry.accountId}</small> : null}</article>)}
    {result.data?.entries.length === 0 ? <p>{page ? "No further activity on this page." : "No activity yet."}</p> : null}<Pager page={page} setPage={setPage} next={result.data?.nextPageToken} busy={result.isFetching} />
  </section></>;
}

export { Settings } from "./settings";

export function CreateSession({ close, open, visible, readLocalWorker }: { close: () => void; open: (id: string) => void; visible: boolean; readLocalWorker?: ReadLocalWorkerProof }) {
  const local = useLocalWorkerProof(readLocalWorker);
  const [workspace, setWorkspace] = useState(Workspace.Worktree);
  const [project, setProject] = useState("");
  const [agent, setAgent] = useState("");
  const [machine, setMachine] = useState("");
  const [name, setName] = useState("");
  const [prompt, setPrompt] = useState("");
  const [mode, setMode] = useState(Mode.Execute);
  const [starting, setStarting] = useState<unknown[]>([]);
  const [promptLimit, setPromptLimit] = useState(false);
 const [budget,setBudget]=useState(emptyBudget);
 const [budgetProblem,setBudgetProblem]=useState("");
  const selectedProject = useQuery(ResourceQuery.getResource, { kind: EntityKind.PROJECT, id: project }, { enabled: visible && Boolean(project) });
  const mutation = useRetainedMutation("create-session", SessionQuery.createSession, (result) => { if (result.change?.session) { setPrompt(""); setName(""); open(result.change.session.id); close(); } });
  const restrictions = object(document(selectedProject.data?.resource).agents);
  const blocked = mutation.busy || mutation.uncertain || local.busy;
  const submit = async () => {
    if (blocked) return;
    let estimatedBudget;
    try {estimatedBudget=budgetInput(budget);setBudgetProblem("");} catch(error){setBudgetProblem(error instanceof Error ? error.message : "Review the budget.");return;}
    const selection = { estimated_cost_budget: estimatedBudget, name, prompt, agent_id: agent, machine_id: machine, project_id: project || undefined, workspace: project ? workspace : Workspace.GeneralChat, starting: project && workspace === Workspace.Worktree ? starting : undefined, mode, source: "MANUAL" };
    const proof = selection.workspace === Workspace.Local ? await local.load(machine) : undefined;
    if (selection.workspace === Workspace.Local && !proof) return;
    void mutation.send({ requestId: newRequestId(), documentJson: encode(selection), localWorkerToken: proof?.token });
  };
  return <Modal title="New session" close={close} visible={visible}><form onSubmit={(event) => { event.preventDefault(); void submit(); }}>
    <fieldset disabled={blocked}><label>Name<input autoFocus required maxLength={256} value={name} onChange={(event) => setName(event.target.value)} /></label>
      <ResourceChoice label="Project" kind={EntityKind.PROJECT} value={project} active={visible} change={(id) => { setProject(id); setAgent(""); setStarting([]); setWorkspace(Workspace.Worktree); }} />
      {project ? <><p>A separate detached worktree is prepared for every project repository. Select Local explicitly to use this computer's original checkouts as-is.</p><div className="actions"><button type="button" aria-pressed={workspace === Workspace.Worktree} onClick={() => setWorkspace(Workspace.Worktree)}>Use separate Worktrees</button><button type="button" disabled={!local.available} aria-pressed={workspace === Workspace.Local} onClick={() => { void local.load().then((proof) => { if (proof) { setWorkspace(Workspace.Local); setMachine(proof.machineId); setStarting([]); } }); }}>Use this computer's Local checkouts</button></div>{workspace === Workspace.Local ? <p>Existing local checkouts are shared explicitly, including their current branches and uncommitted changes. No fetch or starting-reference selection occurs.</p> : <StartingReferences key={project} project={project} starting={starting} change={setStarting} active={visible} />}</> : <p>General Chat uses an isolated projectless directory on the selected Worker.</p>}
      <ResourceChoice label="Agent Worker" kind={EntityKind.AGENT} value={agent} active={visible} required allowed={restrictions.configured === true ? items(restrictions.ids) : undefined} change={setAgent} />
      {restrictions.configured === true && items(restrictions.ids).length === 0 ? <p>This project explicitly allows no Agent Workers. Update its restrictions before creating a session.</p> : null}
      <ResourceChoice label="Execution Worker" kind={EntityKind.MACHINE} value={machine} active={visible} disabled={Boolean(project) && workspace === Workspace.Local} required change={setMachine} />
      <label>Mode<select value={mode} onChange={(event) => setMode(event.target.value as Mode)}><option value={Mode.Execute}>Execute</option><option value={Mode.Plan}>Plan</option></select></label>
      <details><summary>Optional estimated-cost budget</summary><BudgetFields draft={budget} change={setBudget} /></details>
      <label>First message<textarea required rows={5} maxLength={262144} value={prompt} onChange={(event) => { const value = event.target.value; if (new TextEncoder().encode(value).byteLength > 256 << 10) { setPromptLimit(true); return; } setPrompt(value); setPromptLimit(false); }} /></label><button className="primary" disabled={!agent || !machine || !name.trim() || !prompt.trim()}>Create session</button>
    </fieldset>{budgetProblem ? <p role="alert">{budgetProblem}</p> : null}<Problem error={mutation.error} />{mutation.uncertain ? <button type="button" disabled={mutation.busy} onClick={mutation.retry}>Retry the same session creation</button> : null}
    <Problem error={selectedProject.error} />{local.problem ? <p role="alert">{local.problem}</p> : null}{promptLimit ? <p role="alert">The first message exceeds 256 KiB. The previous draft is retained.</p> : null}
  </form></Modal>;
}
