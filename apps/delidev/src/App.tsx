import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";
import { type Transport } from "@connectrpc/connect";
import { TransportProvider, useQuery } from "@connectrpc/connect-query";
import { QueryClientProvider } from "@tanstack/react-query";
import { SessionQuery, SystemQuery, newRequestId } from "@delinoio/delidev-api-client";
import { SessionView } from "./session";
import { Activity, CreateSession, Search, Settings, Surface } from "./views";
import { Inbox } from "./inbox";
import { Usage } from "./usage";
import { Schedules } from "./schedules";
import type { ControlLocalWorker } from "./local-worker-controls";
import type { ReadLocalWorkerProof } from "./local-worker";
import { Problem } from "./ui";
import { Prerequisites } from "./prerequisites";
import { MutationIntents } from "./mutation";
import { connectionQueryClient } from "./cache";
import type { PairingAuthority } from "./pairing-grant";
import { TrayPresentation } from "./tray-presentation";
import { TrayDestination } from "./tray";
import { NotificationPresentation } from "./notification-presentation";
import { Sidebar } from "./sidebar";
import { SettingsEntryDestination } from "./settings";
import { SidebarOutletProvider } from "./sidebar-context";
import { PullRequests } from "./pull-requests";
import { PRWorkflowProvider } from "./pr-workflow";

function Shell({ localServer, readLocalWorker, controlLocalWorker, currentDeviceId, pairingAuthority }: { pairingAuthority?: PairingAuthority; currentDeviceId?: string; controlLocalWorker?: ControlLocalWorker; localServer?: ReactNode; readLocalWorker?: ReadLocalWorkerProof }) {
  const [surface, setSurface] = useState(Surface.Sessions);
  const [sidebarTarget, setSidebarTarget] = useState<HTMLElement | null>(null);
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [selected, setSelected] = useState("");
  const [selectedInbox, setSelectedInbox] = useState("");
  const [inboxActivation, setInboxActivation] = useState(0);
  const [settings, setSettings] = useState(false);
  const [creating, setCreating] = useState(false);
  const [settingsEntry, setSettingsEntry] = useState<SettingsEntryDestination>();
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
  const sessions = useQuery(SessionQuery.listSessions, { projectId: "", includeArchived: false, pageSize: 50, pageToken: "" });
  const status = useQuery(SystemQuery.getStatus, {}, { refetchInterval: 30000 });
  const open = (id: string) => { setSelected(id); setSurface(Surface.Sessions); };
  const navigateTray = (destination: TrayDestination, inboxId?: string) => {
    if (destination === TrayDestination.Inbox) { setSelectedInbox(inboxId ?? ""); setInboxActivation((value) => value + 1); }
    if (destination === TrayDestination.Settings) { setSettings(true); return; }
    setSettings(false); setCreating(false);
    setDrawerOpen(false);
    setSurface(destination === TrayDestination.Inbox ? Surface.Inbox : destination === TrayDestination.Usage ? Surface.Usage : Surface.Sessions);
  };
  const openSettings = (destination?: SettingsEntryDestination) => { if (destination) setSettingsEntry(destination); setSettings(true); };
  const consumeSettingsEntry = useCallback(() => setSettingsEntry(undefined), []);
  const surfaceName = surface === Surface.Sessions ? "session navigation" : surface === Surface.PullRequests ? "pull request filters" : surface === Surface.Usage ? "usage filters" : surface === Surface.Schedules ? "schedule navigation" : surface === Surface.Activity ? "activity filters" : surface === Surface.Inbox ? "inbox filters" : "search filters";
  return <SidebarOutletProvider target={sidebarTarget} closeDrawer={() => setDrawerOpen(false)} drawerOpen={drawerOpen}><div className="app"><a className="skip" href="#main">Skip to content</a><Sidebar surface={surface} selectedSessionId={selected} localServer={localServer} navigate={(destination) => { setDrawerOpen(false); setSurface(destination); if (destination === Surface.Inbox) setSelectedInbox(""); }} openSession={open} newSession={() => setCreating(true)} openSettings={openSettings} setContextTarget={setSidebarTarget} drawerOpen={drawerOpen} setDrawerOpen={setDrawerOpen} /><main id="main" tabIndex={-1}><button type="button" className="sidebar-context-trigger" aria-haspopup="dialog" aria-expanded={drawerOpen} onClick={() => setDrawerOpen(true)}>Open {surfaceName}</button><TrayPresentation navigate={navigateTray} /><NotificationPresentation />{draftState.error ? <p role="alert">{draftState.error}</p> : null}
    <div hidden={surface !== Surface.Sessions} className="session-container">{selected ? <SessionView key={selected} id={selected} draft={drafts.get(selected) ?? ""} setDraft={(value) => saveDraft(selected, value)} /> : <section className="page welcome"><h2>Your sessions, in one place</h2><p>Select a retained session or start a new conversation.</p><Prerequisites active={surface === Surface.Sessions && !settings && !creating} openSettings={() => setSettings(true)} /><Problem error={status.error} /></section>}</div>
    <Search active={surface === Surface.Search} open={open} />
    <Activity active={surface === Surface.Activity} open={open} />
    <div className="inbox-container" hidden={surface !== Surface.Inbox}><Inbox active={surface === Surface.Inbox} open={open} notificationId={selectedInbox} notificationActivation={inboxActivation} /></div>
    <Usage active={surface === Surface.Usage} open={open} />
    <Schedules readLocalWorker={readLocalWorker} active={surface === Surface.Schedules} open={open} />
    <PullRequests active={surface === Surface.PullRequests} openSettings={openSettings} />
  </main><Settings pairingAuthority={pairingAuthority} currentDeviceId={currentDeviceId} controlLocalWorker={controlLocalWorker} close={() => setSettings(false)} visible={settings} entryDestination={settingsEntry} destinationConsumed={consumeSettingsEntry} /><CreateSession readLocalWorker={readLocalWorker} visible={creating} close={() => { setCreating(false); void sessions.refetch(); }} open={open} /></div></SidebarOutletProvider>;
}

// Reconnects for one server/device retain this memory and its mutation receipts
// even when authentication creates a replacement transport. Selecting another
// identity creates a fresh query, draft and mutation scope.
export function App({ transport, localServer, connectionReady = true, connectionEpoch = 0, readLocalWorker, controlLocalWorker, currentDeviceId, pairingAuthority }: { pairingAuthority?: PairingAuthority; currentDeviceId?: string; controlLocalWorker?: ControlLocalWorker; readLocalWorker?: ReadLocalWorkerProof; transport: Transport; localServer?: ReactNode; connectionReady?: boolean; connectionEpoch?: number }) {
  const connectionIdentity = pairingAuthority && currentDeviceId ? JSON.stringify([pairingAuthority.endpoint, pairingAuthority.serverId, currentDeviceId]) : transport;
  const connection = useMemo(() => ({ id: newRequestId(), ...connectionQueryClient() }), [connectionIdentity]);
  const client = connection.client;
  useEffect(() => connection.activate(), [connection]);
  useEffect(() => { if (connectionReady) void client.invalidateQueries({ refetchType: "active" }); }, [client, connectionReady, connectionEpoch]);
  return <TransportProvider transport={transport}><QueryClientProvider key={connection.id} client={client}><MutationIntents><PRWorkflowProvider><Shell pairingAuthority={pairingAuthority} currentDeviceId={currentDeviceId} controlLocalWorker={controlLocalWorker} localServer={localServer} readLocalWorker={readLocalWorker} /></PRWorkflowProvider></MutationIntents></QueryClientProvider></TransportProvider>;
}
