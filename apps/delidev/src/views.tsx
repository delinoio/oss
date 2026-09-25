import { useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import {
  ActivityKind, ActivityQuery, EntityKind, InboxQuery, InboxReadState, ResourceQuery,
  SearchArchiveState, SearchQuery, SessionQuery, newRequestId, type InboxView,
} from "@delinoio/delidev-api-client";
import { document, encode, items, Mode, object, resourceName, text, Workspace } from "./documents";
import { useRetainedMutation } from "./mutation";
import { Modal, Problem } from "./ui";
import { ResourceChoice } from "./configuration-fields";
import { StartingReferences } from "./schedules";
import { useLocalWorkerProof, type ReadLocalWorkerProof } from "./local-worker";
import { Interaction } from "./interactions";

export enum Surface { Sessions = "sessions", Search = "search", Activity = "activity", Inbox = "inbox", Schedules = "schedules" }
function Pager({ page, next, setPage, busy }: { page: string; next?: string; setPage: (value: string) => void; busy: boolean }) {
  return <nav aria-label="Results pages"><button disabled={!page || busy} onClick={() => setPage("")}>First page</button><button disabled={!next || busy} onClick={() => setPage(next!)}>Next page</button></nav>;
}

export function Search({ open }: { open: (id: string) => void }) {
  const [draft, setDraft] = useState("");
  const [query, setQuery] = useState("");
  const [page, setPage] = useState("");
  const [archive, setArchive] = useState(SearchArchiveState.UNSPECIFIED);
  const result = useQuery(SearchQuery.searchConversations, { query, archive, pageSize: 30, pageToken: page }, { enabled: query.trim().length > 0 });
  return <section className="page"><h2>Search conversations</h2><form className="search-form" onSubmit={(event) => { event.preventDefault(); setQuery(draft); setPage(""); }}>
    <label>Search text<input autoFocus value={draft} onChange={(event) => setDraft(event.target.value)} /></label><label>Archive<select value={archive} onChange={(event) => { setArchive(Number(event.target.value)); setPage(""); }}><option value={SearchArchiveState.UNSPECIFIED}>Include archived</option><option value={SearchArchiveState.ACTIVE}>Active only</option><option value={SearchArchiveState.ARCHIVED}>Archived only</option></select></label><button className="primary" disabled={!draft.trim()}>Search</button></form>
    <Problem error={result.error} />{query && result.isPending ? <p>Searching…</p> : null}
    {result.data?.hits.map((hit) => <article key={hit.message?.id} className="result"><button disabled={!hit.message?.sessionId} onClick={() => open(hit.message!.sessionId)}>{hit.sessionName}</button><p>{text(document(hit.message).text)}</p><small>{hit.message?.sessionId}</small></article>)}
    {result.data?.hits.length === 0 ? <p>No retained conversation matches.</p> : null}<Pager page={page} setPage={setPage} next={result.data?.nextPageToken} busy={result.isFetching} />
  </section>;
}

export function Activity({ open }: { open: (id: string) => void }) {
  const [page, setPage] = useState("");
  const result = useQuery(ActivityQuery.listActivity, { pageSize: 50, pageToken: page });
  return <section className="page"><header><h2>Activity</h2><button onClick={() => { setPage(""); void result.refetch(); }}>Refresh</button></header><Problem error={result.error} />
    {result.data?.entries.map((entry) => <article className="result" key={entry.id}><strong>{ActivityKind[entry.kind]?.toLowerCase().replaceAll("_", " ")}</strong><p><time dateTime={new Date(Number(entry.observedAtUnixMs)).toISOString()}>{new Date(Number(entry.observedAtUnixMs)).toLocaleString()}</time></p>{entry.sessionId ? <button onClick={() => open(entry.sessionId)}>Open session</button> : <p>Waiting or skipped occurrence</p>}{entry.accountId ? <small>Account {entry.accountId}</small> : null}</article>)}
    {result.data?.entries.length === 0 ? <p>No activity yet.</p> : null}<Pager page={page} setPage={setPage} next={result.data?.nextPageToken} busy={result.isFetching} />
  </section>;
}

function InboxRow({ view, open, refresh }: { view: InboxView; open: (id: string) => void; refresh: () => void }) {
  const data = document(view.entry);
  const read = text(data.read_state) === "read";
  const mutation = useRetainedMutation(`inbox:${view.entry?.id}`, InboxQuery.setInboxReadState, refresh);
  return <article className="result"><strong>{resourceName(view.session)}</strong><p>{text(data.source) === "interaction" ? "Agent request" : `Execution ${text(object(data.terminal).outcome)}`}</p>
    <p>{read ? "Read" : "Unread"}{view.interaction ? ` · ${text(document(view.interaction).closure)}` : ""}</p><div className="actions"><button onClick={() => open(view.entry!.sessionId)}>Open session</button><button disabled={mutation.busy || mutation.uncertain} onClick={() => void mutation.send({ mutation: { requestId: newRequestId(), id: view.entry!.id, expectedRevision: view.entry!.revision }, readState: read ? InboxReadState.UNREAD : InboxReadState.READ })}>{read ? "Mark unread" : "Mark read"}</button></div><Problem error={mutation.error} />{view.interaction ? <Interaction resource={view.interaction} refresh={refresh} /> : null}{mutation.uncertain ? <button onClick={mutation.retry}>Retry the same read-state change</button> : null}
  </article>;
}
export function Inbox({ open }: { open: (id: string) => void }) {
  const [page, setPage] = useState("");
  const result = useQuery(InboxQuery.listInbox, { pageSize: 20, pageToken: page }, { refetchInterval: 5000 });
  return <section className="page"><header><h2>Inbox</h2><button onClick={() => { setPage(""); void result.refetch(); }}>Refresh</button></header><p>Reading an item never approves or answers it.</p><Problem error={result.error} />
    {result.data?.entries.filter((view) => view.entry).map((view) => <InboxRow key={view.entry!.id} view={view} open={open} refresh={() => void result.refetch()} />)}
    {result.data?.entries.length === 0 ? <p>No retained requests or completions.</p> : null}<Pager page={page} setPage={setPage} next={result.data?.nextPageToken} busy={result.isFetching} />
  </section>;
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
  const selectedProject = useQuery(ResourceQuery.getResource, { kind: EntityKind.PROJECT, id: project }, { enabled: visible && Boolean(project) });
  const mutation = useRetainedMutation("create-session", SessionQuery.createSession, (result) => { if (result.change?.session) { setPrompt(""); setName(""); open(result.change.session.id); close(); } });
  const restrictions = object(document(selectedProject.data?.resource).agents);
  const blocked = mutation.busy || mutation.uncertain || local.busy;
  const submit = async () => {
    if (blocked) return;
    const selection = { name, prompt, agent_id: agent, machine_id: machine, project_id: project || undefined, workspace: project ? workspace : Workspace.GeneralChat, starting: project && workspace === Workspace.Worktree ? starting : undefined, mode, source: "MANUAL" };
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
      <label>First message<textarea required rows={5} maxLength={262144} value={prompt} onChange={(event) => { const value = event.target.value; if (new TextEncoder().encode(value).byteLength > 256 << 10) { setPromptLimit(true); return; } setPrompt(value); setPromptLimit(false); }} /></label><button className="primary" disabled={!agent || !machine || !name.trim() || !prompt.trim()}>Create session</button>
    </fieldset><Problem error={mutation.error} />{mutation.uncertain ? <button type="button" disabled={mutation.busy} onClick={mutation.retry}>Retry the same session creation</button> : null}
    <Problem error={selectedProject.error} />{local.problem ? <p role="alert">{local.problem}</p> : null}{promptLimit ? <p role="alert">The first message exceeds 256 KiB. The previous draft is retained.</p> : null}
  </form></Modal>;
}
