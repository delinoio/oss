import { useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import {
  ActivityKind, ActivityQuery, EntityKind, InboxQuery, InboxReadState, ResourceQuery,
  SearchArchiveState, SearchQuery, SessionQuery, newRequestId, type InboxView,
} from "@delinoio/delidev-api-client";
import { document, encode, items, Mode, object, resourceName, text, Workspace } from "./documents";
import { useRetainedMutation } from "./mutation";
import { Modal, Problem } from "./ui";

export enum Surface { Sessions = "sessions", Search = "search", Activity = "activity", Inbox = "inbox" }
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
    <p>{read ? "Read" : "Unread"}{view.interaction ? ` · ${text(document(view.interaction).closure)}` : ""}</p><div className="actions"><button onClick={() => open(view.entry!.sessionId)}>Open session</button><button disabled={mutation.busy || mutation.uncertain} onClick={() => void mutation.send({ mutation: { requestId: newRequestId(), id: view.entry!.id, expectedRevision: view.entry!.revision }, readState: read ? InboxReadState.UNREAD : InboxReadState.READ })}>{read ? "Mark unread" : "Mark read"}</button></div><Problem error={mutation.error} />{mutation.uncertain ? <button onClick={mutation.retry}>Retry the same read-state change</button> : null}
  </article>;
}
export function Inbox({ open }: { open: (id: string) => void }) {
  const [page, setPage] = useState("");
  const result = useQuery(InboxQuery.listInbox, { pageSize: 50, pageToken: page });
  return <section className="page"><header><h2>Inbox</h2><button onClick={() => { setPage(""); void result.refetch(); }}>Refresh</button></header><p>Reading an item never approves or answers it.</p><Problem error={result.error} />
    {result.data?.entries.filter((view) => view.entry).map((view) => <InboxRow key={view.entry!.id} view={view} open={open} refresh={() => void result.refetch()} />)}
    {result.data?.entries.length === 0 ? <p>No retained requests or completions.</p> : null}<Pager page={page} setPage={setPage} next={result.data?.nextPageToken} busy={result.isFetching} />
  </section>;
}

export function Settings({ close }: { close: () => void }) {
  const [kind, setKind] = useState(EntityKind.PROJECT);
  const [page, setPage] = useState("");
  const result = useQuery(ResourceQuery.listResources, { filter: { kind, pageSize: 50, pageToken: page } });
  const tabs = [[EntityKind.PROJECT, "Projects"], [EntityKind.AGENT, "Agent Workers"], [EntityKind.ACCOUNT, "AI accounts"], [EntityKind.MACHINE, "Execution Workers"], [EntityKind.TEMPLATE, "Instructions"]] as const;
  return <Modal title="Settings" close={close}><nav aria-label="Settings categories">{tabs.map(([value, label]) => <button key={value} aria-pressed={kind === value} onClick={() => { setKind(value); setPage(""); }}>{label}</button>)}</nav><Problem error={result.error} />
    <p>These are the selected server's saved settings. Configuration editing remains available through the DeliDev CLI.</p>
    {result.data?.resources.map((row) => { const data = document(row); return <article className="result" key={row.id}><h3>{resourceName(row)}</h3>{text(data.health) ? <p>Status: {text(data.health)}</p> : null}{text(data.harness) ? <p>Harness: {text(data.harness)}</p> : null}{kind === EntityKind.TEMPLATE ? <pre>{text(data.contents)}</pre> : null}<small>{row.id}</small></article>; })}
    {result.data?.resources.length === 0 ? <p>No saved entries. Configure this prerequisite with the CLI.</p> : null}<Pager page={page} setPage={setPage} next={result.data?.nextPageToken} busy={result.isFetching} />
  </Modal>;
}

export function CreateSession({ close, open, visible }: { close: () => void; open: (id: string) => void; visible: boolean }) {
  const projects = useQuery(ResourceQuery.listResources, { filter: { kind: EntityKind.PROJECT, pageSize: 200 } }, { enabled: visible });
  const agents = useQuery(ResourceQuery.listResources, { filter: { kind: EntityKind.AGENT, pageSize: 200 } }, { enabled: visible });
  const machines = useQuery(ResourceQuery.listResources, { filter: { kind: EntityKind.MACHINE, pageSize: 200 } }, { enabled: visible });
  const [project, setProject] = useState("");
  const [agent, setAgent] = useState("");
  const [machine, setMachine] = useState("");
  const [name, setName] = useState("");
  const [prompt, setPrompt] = useState("");
  const [mode, setMode] = useState(Mode.Execute);
  const mutation = useRetainedMutation("create-session", SessionQuery.createSession, (result) => { if (result.change?.session) { setPrompt(""); setName(""); open(result.change.session.id); close(); } });
  const restrictions = object(document(projects.data?.resources.find((r) => r.id === project)).agents);
  const available = agents.data?.resources.filter((r) => !restrictions.configured || items(restrictions.ids).includes(r.id)) ?? [];
  const blocked = mutation.busy || mutation.uncertain;
  return <Modal title="New session" close={close} visible={visible}><form onSubmit={(event) => { event.preventDefault(); void mutation.send({ requestId: newRequestId(), documentJson: encode({ name, prompt, agent_id: agent, machine_id: machine, project_id: project || undefined, workspace: project ? Workspace.Worktree : Workspace.GeneralChat, mode, source: "MANUAL" }) }); }}>
    <fieldset disabled={blocked}><label>Name<input autoFocus required maxLength={256} value={name} onChange={(event) => setName(event.target.value)} /></label>
      <label>Project<select value={project} onChange={(event) => { setProject(event.target.value); setAgent(""); }}><option value="">General Chat · isolated directory</option>{projects.data?.resources.map((r) => <option key={r.id} value={r.id}>{resourceName(r)}</option>)}</select></label>
      {project ? <p>A detached worktree is prepared for each repository using its configured starting reference.</p> : null}
      <label>Agent Worker<select required value={agent} onChange={(event) => setAgent(event.target.value)}><option value="">Select an Agent Worker</option>{available.map((r) => <option key={r.id} value={r.id}>{resourceName(r)}</option>)}</select></label>
      <label>Execution Worker<select required value={machine} onChange={(event) => setMachine(event.target.value)}><option value="">Select a computer</option>{machines.data?.resources.map((r) => <option key={r.id} value={r.id}>{resourceName(r)}</option>)}</select></label>
      <label>Mode<select value={mode} onChange={(event) => setMode(event.target.value as Mode)}><option value={Mode.Execute}>Execute</option><option value={Mode.Plan}>Plan</option></select></label>
      <label>First message<textarea required rows={5} value={prompt} onChange={(event) => setPrompt(event.target.value)} /></label><button className="primary" disabled={!agent || !machine || !name.trim() || !prompt.trim()}>Create session</button>
    </fieldset><Problem error={mutation.error} />{mutation.uncertain ? <button type="button" disabled={mutation.busy} onClick={mutation.retry}>Retry the same session creation</button> : null}
    <Problem error={projects.error || agents.error || machines.error} />
    {projects.data?.nextPageToken || agents.data?.nextPageToken || machines.data?.nextPageToken ? <p>The selector is bounded to 200 entries; use the CLI to select other entries.</p> : null}
  </form></Modal>;
}
