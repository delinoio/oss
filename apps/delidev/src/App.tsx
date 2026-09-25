import { useEffect, useMemo, useState, type ReactNode } from "react";
import { type Transport } from "@connectrpc/connect";
import { TransportProvider, useQuery } from "@connectrpc/connect-query";
import { QueryClientProvider } from "@tanstack/react-query";
import { SessionQuery, SystemQuery, newRequestId } from "@delinoio/delidev-api-client";
import { document, resourceName, text, Workspace, workspaceNames } from "./documents";
import { SessionView } from "./session";
import { Activity, CreateSession, Inbox, Search, Settings, Surface } from "./views";
import { Schedules } from "./schedules";
import type { ControlLocalWorker } from "./local-worker-controls";
import type { ReadLocalWorkerProof } from "./local-worker";
import { Problem } from "./ui";
import { MutationIntents } from "./mutation";
import { connectionQueryClient } from "./cache";
import type { PairingAuthority } from "./pairing-grant";

function Shell({ localServer, readLocalWorker, controlLocalWorker, currentDeviceId, pairingAuthority }: { pairingAuthority?: PairingAuthority; currentDeviceId?: string; controlLocalWorker?: ControlLocalWorker; localServer?: ReactNode; readLocalWorker?: ReadLocalWorkerProof }) {
  const [surface, setSurface] = useState(Surface.Sessions);
  const [selected, setSelected] = useState("");
  const [settings, setSettings] = useState(false);
  const [creating, setCreating] = useState(false);
  const [archived, setArchived] = useState(false);
  const [page, setPage] = useState("");
  const [draftState, setDraftState] = useState<{ drafts: ReadonlyMap<string, string>; error?: string }>({ drafts: new Map() });
  const { drafts } = draftState;
  const saveDraft = (id: string, value: string) => setDraftState((current) => {
    const size = new TextEncoder().encode(value).byteLength;
    const total = [...current.drafts].reduce((bytes, [key, draft]) => bytes + (key === id ? 0 : new TextEncoder().encode(draft).byteLength), size);
    if (size > 256 << 10 || total > 4 << 20 || (value && !current.drafts.has(id) && current.drafts.size >= 1000)) return { ...current, error: "The draft limit is reached. Shorten this message or send or clear another draft." };
    const drafts = new Map(current.drafts);
    if (value) drafts.set(id, value); else drafts.delete(id);
    return { drafts };
  });
  const sessions = useQuery(SessionQuery.listSessions, { includeArchived: archived, pageSize: 50, pageToken: page });
  const status = useQuery(SystemQuery.getStatus, {}, { refetchInterval: 30000 });
  const open = (id: string) => { setSelected(id); setSurface(Surface.Sessions); };
  return <div className="app"><a className="skip" href="#main">Skip to content</a><aside className="sidebar"><header><h1>DeliDev</h1><p>Personal Agent Runner</p></header>
    <button className="primary" onClick={(event) => { event.currentTarget.focus(); setCreating(true); }}>New session</button>
    <nav aria-label="Main navigation">{Object.values(Surface).map((value) => <button key={value} aria-current={surface === value ? "page" : undefined} onClick={() => setSurface(value)}>{value[0].toUpperCase() + value.slice(1)}</button>)}</nav>
    <div className="session-list"><header><h2>Sessions</h2><button aria-label="Refresh sessions" onClick={() => { setPage(""); void sessions.refetch(); }}>↻</button></header><label className="checkbox"><input type="checkbox" checked={archived} onChange={(event) => { setArchived(event.target.checked); setPage(""); }} />Include archived</label><Problem error={sessions.error} />
      {sessions.data?.sessions.map((row) => { const data = document(row); const workspace = text(data.workspace) as Workspace; return <button className="session-link" key={row.id} aria-current={selected === row.id ? "true" : undefined} onClick={() => open(row.id)}><span role="img" aria-label={workspaceNames[workspace] || "Workspace"} title={workspaceNames[workspace]}>{workspace === Workspace.Worktree ? "⑂" : workspace === Workspace.Local ? "▣" : "◌"}</span><span>{resourceName(row)}<small>{text(data.outcome)} · {text(data.archive)}</small></span></button>; })}
      {page ? <button onClick={() => setPage("")}>First page</button> : null}{sessions.data?.nextPageToken ? <button onClick={() => setPage(sessions.data!.nextPageToken)}>More sessions</button> : null}
    </div><footer><p role="status">{status.error ? "Server unavailable" : status.data ? `Server ${status.data.version}` : status.isPending ? "Connecting to server…" : "Server unavailable"}</p>{localServer}<button onClick={(event) => { event.currentTarget.focus(); setSettings(true); }}>Settings</button></footer>
  </aside><main id="main" tabIndex={-1}>{draftState.error ? <p role="alert">{draftState.error}</p> : null}
    <div hidden={surface !== Surface.Sessions} className="session-container">{selected ? <SessionView key={selected} id={selected} draft={drafts.get(selected) ?? ""} setDraft={(value) => saveDraft(selected, value)} /> : <section className="page welcome"><h2>Your sessions, in one place</h2><p>Select a retained session or start a new conversation.</p><h3>Before your first session</h3><ol><li>Connect to your DeliDev server.</li><li>Pair an execution Worker and verify its installed harness.</li><li>Connect an AI account and configure an Agent Worker.</li><li>Configure a project, or choose General Chat.</li></ol><button onClick={(event) => { event.currentTarget.focus(); setSettings(true); }}>View prerequisites in Settings</button><Problem error={status.error} /></section>}</div>
    {surface === Surface.Search ? <Search open={open} /> : surface === Surface.Activity ? <Activity open={open} /> : surface === Surface.Inbox ? <Inbox open={open} /> : null}
    <Schedules readLocalWorker={readLocalWorker} active={surface === Surface.Schedules} open={open} />
  </main><Settings pairingAuthority={pairingAuthority} currentDeviceId={currentDeviceId} controlLocalWorker={controlLocalWorker} close={() => setSettings(false)} visible={settings} /><CreateSession readLocalWorker={readLocalWorker} visible={creating} close={() => { setCreating(false); void sessions.refetch(); }} open={open} /></div>;
}

// The native caller mounts a new App per selected server/device. Query caches
// and in-memory drafts never cross that identity boundary.
export function App({ transport, localServer, connectionReady = true, connectionEpoch = 0, readLocalWorker, controlLocalWorker, currentDeviceId, pairingAuthority }: { pairingAuthority?: PairingAuthority; currentDeviceId?: string; controlLocalWorker?: ControlLocalWorker; readLocalWorker?: ReadLocalWorkerProof; transport: Transport; localServer?: ReactNode; connectionReady?: boolean; connectionEpoch?: number }) {
  const connection = useMemo(() => ({ id: newRequestId(), ...connectionQueryClient() }), [transport]);
  const client = connection.client;
  useEffect(() => connection.activate(), [connection]);
  useEffect(() => { if (connectionReady) void client.invalidateQueries({ refetchType: "active" }); }, [client, connectionReady, connectionEpoch]);
  return <TransportProvider transport={transport}><QueryClientProvider key={connection.id} client={client}><MutationIntents><Shell pairingAuthority={pairingAuthority} currentDeviceId={currentDeviceId} controlLocalWorker={controlLocalWorker} localServer={localServer} readLocalWorker={readLocalWorker} /></MutationIntents></QueryClientProvider></TransportProvider>;
}
