import type { ServerPresentation } from "./server-presentation";
import { createPortal } from "react-dom";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { type Transport } from "@connectrpc/connect";
import { TransportProvider, useQuery } from "@connectrpc/connect-query";
import { QueryClientProvider } from "@tanstack/react-query";
import { SessionQuery, SystemQuery, newRequestId } from "@delinoio/delidev-api-client";
import { SessionView } from "./session";
import { Activity, Search, Settings, Surface } from "./views";
import { NewSession } from "./new-session";
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
import type { ChooseRepositoryFolder } from "./repository-registration";
import { SettingsEntryDestination } from "./settings";
import { SidebarOutletProvider } from "./sidebar-context";
import { PullRequests } from "./pull-requests";
import { SessionForkProvider } from "./session-fork";
import { SessionStorageProvider } from "./session-storage";
import { PRWorkflowProvider } from "./pr-workflow";

function Shell({ localServer, serverPresentation, connectionReady, connectionSettings, connectionTarget, readLocalWorker, controlLocalWorker, chooseRepositoryFolder, currentDeviceId, pairingAuthority }: { pairingAuthority?: PairingAuthority; currentDeviceId?: string; controlLocalWorker?: ControlLocalWorker; chooseRepositoryFolder?: ChooseRepositoryFolder; localServer?: ReactNode; serverPresentation?: ServerPresentation; connectionReady: boolean; connectionSettings?: ReactNode; connectionTarget?: HTMLElement; readLocalWorker?: ReadLocalWorkerProof }) {
  const [surface, setSurface] = useState(Surface.Sessions);
  const pendingFocusDestination = useRef<Surface>(undefined);
  const main = useRef<HTMLElement>(null);
  const contextOpener = useRef<HTMLButtonElement>(null);
  const [sidebarTarget, setSidebarTarget] = useState<HTMLElement | null>(null);
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [selected, setSelected] = useState("");
  const [selectedInbox, setSelectedInbox] = useState("");
  const [inboxActivation, setInboxActivation] = useState(0);
  const [newSessionActivation, setNewSessionActivation] = useState(0);
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
  const leaveSettings = (destination: Surface) => { pendingFocusDestination.current = surface === Surface.Settings ? destination : undefined; setSettingsEntry(undefined); };
  const open = (id: string) => { leaveSettings(Surface.Sessions); setSelected(id); setSurface(Surface.Sessions); setDrawerOpen(false); };
  const navigateTray = (destination: TrayDestination, inboxId?: string) => {
    if (destination === TrayDestination.Settings) { openSettings(); return; }
    const next = destination === TrayDestination.Inbox ? Surface.Inbox : destination === TrayDestination.Usage ? Surface.Usage : Surface.Sessions;
    leaveSettings(next);
    if (destination === TrayDestination.Inbox) { setSelectedInbox(inboxId ?? ""); setInboxActivation((value) => value + 1); }
    setDrawerOpen(false);
    setSurface(next);
  };
  const openSettings = (destination?: SettingsEntryDestination) => {
    // Selecting the active rail item keeps the current visit and deferred entry.
    if (surface !== Surface.Settings || destination) {
      pendingFocusDestination.current = Surface.Settings;
      setSettingsEntry(destination);
    }
    setDrawerOpen(false);
    setSurface(Surface.Settings);
  };
  const consumeSettingsEntry = useCallback(() => setSettingsEntry(undefined), []);
  const surfaceName = surface === Surface.Sessions || surface === Surface.NewSession ? "session navigation" : surface === Surface.Settings ? "settings categories" : surface === Surface.PullRequests ? "pull request filters" : surface === Surface.Usage ? "usage filters" : surface === Surface.Schedules ? "schedule navigation" : surface === Surface.Activity ? "activity filters" : surface === Surface.Inbox ? "inbox filters" : "search filters";
  const startNewSession = () => { leaveSettings(Surface.NewSession); setDrawerOpen(false); setNewSessionActivation((value) => value + 1); setSurface(Surface.NewSession); };
  const navigate = (destination: Surface) => {
    leaveSettings(destination);
    setDrawerOpen(false);
    setSurface(destination);
    if (destination === Surface.Inbox) setSelectedInbox("");
  };
  const navigateHeader = (destination: Surface.Inbox | Surface.Search) => {
    navigate(destination);
    pendingFocusDestination.current = destination;
  };
  useLayoutEffect(() => {
    const destination = pendingFocusDestination.current;
    pendingFocusDestination.current = undefined;
    if (!destination || destination !== surface || drawerOpen) return;
    // Child layout effects close the drawer first. Consuming here keeps Search's
    // existing first-entry animation frame authoritative, with no later handoff.
    const compact = typeof window.matchMedia === "function" && window.matchMedia("(max-width: 759px)").matches;
    const target = compact ? contextOpener.current : main.current;
    if (target && !target.closest("[hidden], [inert]")) target.focus({ preventScroll: true });
  }, [surface, drawerOpen, settingsEntry]);
  return <SessionForkProvider openSession={open} readLocalWorker={readLocalWorker}><SessionStorageProvider><SidebarOutletProvider target={sidebarTarget} closeDrawer={() => setDrawerOpen(false)} drawerOpen={drawerOpen}><div className="app"><a className="skip" href="#main">Skip to content</a><Sidebar connectionReady={connectionReady} serverPresentation={serverPresentation} surface={surface} selectedSessionId={selected} navigate={navigate} navigateHeader={navigateHeader} openSession={open} newSession={startNewSession} openSettings={openSettings} setContextTarget={setSidebarTarget} drawerOpen={drawerOpen} setDrawerOpen={setDrawerOpen} /><main ref={main} id="main" tabIndex={-1}><button ref={contextOpener} type="button" className="sidebar-context-trigger" aria-haspopup="dialog" aria-expanded={drawerOpen} onClick={() => setDrawerOpen(true)}>Open {surfaceName}</button><TrayPresentation navigate={navigateTray} /><NotificationPresentation />{draftState.error ? <p role="alert">{draftState.error}</p> : null}
    <div hidden={surface !== Surface.Sessions} className="session-container">{selected ? <SessionView key={selected} id={selected} draft={drafts.get(selected) ?? ""} setDraft={(value) => saveDraft(selected, value)} /> : <section className="page welcome"><h2>Your sessions, in one place</h2><p>Select a retained session or start a new conversation.</p><Prerequisites active={surface === Surface.Sessions} openSettings={openSettings} /><Problem error={status.error} /></section>}</div>
    <NewSession active={surface === Surface.NewSession} ownsActivation={surface === Surface.NewSession} activation={newSessionActivation} readLocalWorker={readLocalWorker} back={() => { navigate(Surface.Sessions); void sessions.refetch(); }} openSettings={openSettings} open={open} created={() => { void sessions.refetch(); }} />
    <Search active={surface === Surface.Search} open={open} />
    <Activity active={surface === Surface.Activity} open={open} />
    <div className="inbox-container" hidden={surface !== Surface.Inbox}><Inbox active={surface === Surface.Inbox} open={open} notificationId={selectedInbox} notificationActivation={inboxActivation} /></div>
    <Usage active={surface === Surface.Usage} open={open} />
    <Schedules readLocalWorker={readLocalWorker} active={surface === Surface.Schedules} open={open} />
    <PullRequests active={surface === Surface.PullRequests} openSettings={openSettings} />
    <Settings readLocalWorker={readLocalWorker} connectionSettings={connectionSettings} pairingAuthority={pairingAuthority} currentDeviceId={currentDeviceId} controlLocalWorker={controlLocalWorker} chooseRepositoryFolder={chooseRepositoryFolder} visible={surface === Surface.Settings} entryDestination={settingsEntry} destinationConsumed={consumeSettingsEntry} />
  </main>{connectionTarget && localServer ? createPortal(localServer, connectionTarget) : null}</div></SidebarOutletProvider></SessionStorageProvider></SessionForkProvider>;
}

// Reconnects for one server/device retain this memory and its mutation receipts
// even when authentication creates a replacement transport. Selecting another
// identity creates a fresh query, draft and mutation scope.
export function App({ transport, localServer, serverPresentation, connectionSettings, connectionTarget, connectionReady = true, connectionEpoch = 0, readLocalWorker, controlLocalWorker, chooseRepositoryFolder, currentDeviceId, pairingAuthority }: { pairingAuthority?: PairingAuthority; currentDeviceId?: string; controlLocalWorker?: ControlLocalWorker; chooseRepositoryFolder?: ChooseRepositoryFolder; readLocalWorker?: ReadLocalWorkerProof; transport: Transport; localServer?: ReactNode; serverPresentation?: ServerPresentation; connectionSettings?: ReactNode; connectionTarget?: HTMLElement; connectionReady?: boolean; connectionEpoch?: number }) {
  const connectionIdentity = pairingAuthority && currentDeviceId ? JSON.stringify([pairingAuthority.endpoint, pairingAuthority.serverId, currentDeviceId]) : transport;
  const connection = useMemo(() => ({ id: newRequestId(), ...connectionQueryClient() }), [connectionIdentity]);
  const client = connection.client;
  useEffect(() => connection.activate(), [connection]);
  useEffect(() => { if (connectionReady) void client.invalidateQueries({ refetchType: "active" }); }, [client, connectionReady, connectionEpoch]);
  return <TransportProvider transport={transport}><QueryClientProvider key={connection.id} client={client}><MutationIntents><PRWorkflowProvider><Shell connectionReady={connectionReady} pairingAuthority={pairingAuthority} currentDeviceId={currentDeviceId} controlLocalWorker={controlLocalWorker} chooseRepositoryFolder={chooseRepositoryFolder} connectionSettings={connectionSettings} connectionTarget={connectionTarget} localServer={localServer} serverPresentation={serverPresentation} readLocalWorker={readLocalWorker} /></PRWorkflowProvider></MutationIntents></QueryClientProvider></TransportProvider>;
}
