import { LocalizedText, copy, useLocale } from "./localization";
import type { UsageEntry } from "./usage-entry";
import type { ServerPresentation } from "./server-presentation";
import { createPortal } from "react-dom";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { type Transport } from "@connectrpc/connect";
import { TransportProvider, useQuery } from "@connectrpc/connect-query";
import { QueryClientProvider } from "@tanstack/react-query";
import { SessionQuery, SystemQuery, newRequestId } from "@delinoio/delidev-api-client";
import { SessionView } from "./session";
import { Activity, Search, Settings, Surface } from "./views";
import { NewSession, NewSessionKind } from "./new-session";
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
import { NotificationProvider } from "./toast-notifications";
import { Sidebar } from "./sidebar";
import type { ChooseRepositoryFolder } from "./repository-registration";
import { SettingsEntryDestination } from "./settings";
import { SidebarOutletProvider } from "./sidebar-context";
import { PullRequests } from "./pull-requests";
import { SessionForkProvider } from "./session-fork";
import { SessionStorageProvider } from "./session-storage";
import { PRWorkflowProvider } from "./pr-workflow";
import { ProjectCreationDialog } from "./project-creation";
import { ShortcutProvider, useShortcutHelp, useShortcutSurface, useShortcuts } from "./shortcut-provider";
import { ShortcutId, ShortcutInput, ShortcutScope, globalShortcutBindings } from "./shortcuts";

function Shell({ localServer, serverPresentation, connectionReady, connectionSettings, connectionTarget, readLocalWorker, controlLocalWorker, chooseRepositoryFolder, currentDeviceId, pairingAuthority }: { pairingAuthority?: PairingAuthority; currentDeviceId?: string; controlLocalWorker?: ControlLocalWorker; chooseRepositoryFolder?: ChooseRepositoryFolder; localServer?: ReactNode; serverPresentation?: ServerPresentation; connectionReady: boolean; connectionSettings?: ReactNode; connectionTarget?: HTMLElement; readLocalWorker?: ReadLocalWorkerProof }) {
  useLocale();
  const [surface, setSurface] = useState(Surface.Sessions);
  useShortcutSurface(surface);
  const openHelp = useShortcutHelp();
  const [searchFocusActivation, setSearchFocusActivation] = useState(0);
  const pendingFocusDestination = useRef<Surface>(undefined);
  const main = useRef<HTMLElement>(null);
  const contextOpener = useRef<HTMLButtonElement>(null);
  const [sidebarTarget, setSidebarTarget] = useState<HTMLElement | null>(null);
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [selected, setSelected] = useState("");
  const [selectedInbox, setSelectedInbox] = useState("");
  const [inboxActivation, setInboxActivation] = useState(0);
  const [newSessionEntry, setNewSessionEntry] = useState<{ activation: number; projectId?: string }>({ activation: 0 });
  const [newSessionProjectBlocked, setNewSessionProjectBlocked] = useState(false);
  const [newGeneralChatActivation, setNewGeneralChatActivation] = useState(0);
  const [usageEntry, setUsageEntry] = useState<UsageEntry>();
  const [settingsEntry, setSettingsEntry] = useState<SettingsEntryDestination>();
  const [projectCreation, setProjectCreation] = useState<{ id: string; activation: number }>();
  const [draftState, setDraftState] = useState<{ drafts: ReadonlyMap<string, string>; error?: string }>({ drafts: new Map() });
  const { drafts } = draftState;
  const saveDraft = (id: string, value: string) => setDraftState((current) => {
    const size = new TextEncoder().encode(value).byteLength;
    const total = [...current.drafts].reduce((bytes, [key, draft]) => bytes + (key === id ? 0 : new TextEncoder().encode(draft).byteLength), size);
    if (size > 256 << 10 || total > 4 << 20 || (value && !current.drafts.has(id) && current.drafts.size >= 1000)) return { ...current, error: copy("App.extra.979e130130a9") };
    const drafts = new Map(current.drafts);
    if (value) drafts.set(id, value); else drafts.delete(id);
    return { drafts };
  });
  const sessions = useQuery(SessionQuery.listSessions, { projectId: "", includeArchived: false, pageSize: 50, pageToken: "" });
  const status = useQuery(SystemQuery.getStatus, {}, { refetchInterval: 30000 });
  const leaveSurface = (destination: Surface) => {
    pendingFocusDestination.current = surface === Surface.Settings ? destination : undefined;
    setSettingsEntry(undefined);
    if (destination !== surface) setProjectCreation(undefined);
  };
  const open = (id: string) => { leaveSurface(Surface.Sessions); if (id !== selected) setProjectCreation(undefined); setSelected(id); setSurface(Surface.Sessions); setDrawerOpen(false); };
  const navigateTray = (destination: TrayDestination, inboxId?: string) => {
    if (destination === TrayDestination.Settings) { openSettings(); return; }
    const next = destination === TrayDestination.Inbox ? Surface.Inbox : destination === TrayDestination.Usage ? Surface.Usage : Surface.Sessions;
    leaveSurface(next);
    if (destination === TrayDestination.Inbox) { setSelectedInbox(inboxId ?? ""); setInboxActivation((value) => value + 1); }
    setDrawerOpen(false);
    setSurface(next);
  };
  const openSettings = (destination?: SettingsEntryDestination) => {
    setProjectCreation(undefined);
    // Selecting the active rail item keeps the current visit and deferred entry.
    if (surface !== Surface.Settings || destination) {
      pendingFocusDestination.current = Surface.Settings;
      setSettingsEntry(destination);
    }
    setDrawerOpen(false);
    setSurface(Surface.Settings);
  };
  const consumeSettingsEntry = useCallback(() => setSettingsEntry(undefined), []);
  const openNewProject = () => {
    setDrawerOpen(false);
    setProjectCreation(current => current ? { ...current, activation: current.activation + 1 } : { id: newRequestId(), activation: 0 });
  };
  const closeProjectCreation = useCallback(() => setProjectCreation(undefined), []);
  const projectFallbackFocus = useCallback(() => typeof window.matchMedia === "function" && window.matchMedia("(max-width: 759px)").matches ? contextOpener.current : main.current, []);
  const surfaceName = surface === Surface.Sessions || surface === Surface.NewSession || surface === Surface.NewGeneralChat ? copy("App.extra.2998edd080d1") : surface === Surface.Settings ? copy("App.extra.a1de4eceaa3b") : surface === Surface.PullRequests ? copy("App.extra.23533b15bc29") : surface === Surface.Usage ? copy("App.extra.34d76f3f7da4") : surface === Surface.Schedules ? copy("App.extra.a6a986427e87") : surface === Surface.Activity ? copy("App.extra.3fa855f8f6de") : surface === Surface.Inbox ? copy("App.extra.a1de2be5c09b") : copy("App.extra.1f73d5f3eac5");
  const startNewSession = (projectId?: string) => {
    if (projectId && newSessionProjectBlocked) return;
    leaveSurface(Surface.NewSession);
    setDrawerOpen(false);
    setNewSessionEntry((current) => ({ activation: current.activation + 1, projectId }));
    setSurface(Surface.NewSession);
  };
  const startNewGeneralChat = () => { leaveSurface(Surface.NewGeneralChat); setDrawerOpen(false); setNewGeneralChatActivation((value) => value + 1); setSurface(Surface.NewGeneralChat); };
  const navigate = (destination: Surface) => {
    leaveSurface(destination);
    setDrawerOpen(false);
    setSurface(destination);
    if (destination === Surface.Inbox) setSelectedInbox("");
  };
  const openAccountUsage = (entry: UsageEntry) => { setUsageEntry(entry); navigate(Surface.Usage); };
  const navigateHeader = (destination: Surface.Inbox | Surface.Search) => {
    navigate(destination);
    pendingFocusDestination.current = destination;
  };
  useShortcuts([
    { id: ShortcutId.Help, scope: ShortcutScope.Global, label: "shortcuts.help", bindings: globalShortcutBindings[ShortcutId.Help], run: openHelp },
    { id: ShortcutId.Search, scope: ShortcutScope.Global, label: "shortcuts.openSearch", bindings: globalShortcutBindings[ShortcutId.Search], input: ShortcutInput.Allow, run: () => {
      navigateHeader(Surface.Search);
      setSearchFocusActivation(value => value + 1);
      if (typeof window.matchMedia === "function" && window.matchMedia("(max-width: 759px)").matches) setDrawerOpen(true);
    } },
    { id: ShortcutId.NewSession, scope: ShortcutScope.Global, label: "shortcuts.newSession", bindings: globalShortcutBindings[ShortcutId.NewSession], input: ShortcutInput.Allow, run: startNewSession },
  ]);
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
  return <SessionForkProvider openSession={open} readLocalWorker={readLocalWorker}><SessionStorageProvider><SidebarOutletProvider target={sidebarTarget} closeDrawer={() => setDrawerOpen(false)} drawerOpen={drawerOpen} openDrawer={() => setDrawerOpen(true)}><div className="app"><a className="skip" href="#main">{copy("App.skipToContent_ac576a")}</a><Sidebar connectionReady={connectionReady} serverPresentation={serverPresentation} surface={surface} selectedSessionId={selected} navigate={navigate} navigateHeader={navigateHeader} openSession={open} newSession={startNewSession} newGeneralChat={startNewGeneralChat} newProject={openNewProject} projectSelectionBlocked={newSessionProjectBlocked} openSettings={openSettings} setContextTarget={setSidebarTarget} drawerOpen={drawerOpen} setDrawerOpen={setDrawerOpen} /><main ref={main} id="main" tabIndex={-1}><button ref={contextOpener} type="button" className="sidebar-context-trigger" aria-haspopup="dialog" aria-expanded={drawerOpen} onClick={() => setDrawerOpen(true)}><LocalizedText id="App.open_a007d6" components={{ s0: <>{surfaceName}</> }} /></button><TrayPresentation navigate={navigateTray} /><NotificationPresentation />{draftState.error ? <p role="alert">{draftState.error}</p> : null}
    {projectCreation ? <ProjectCreationDialog key={projectCreation.id} activation={projectCreation.activation} close={closeProjectCreation} fallbackFocus={projectFallbackFocus} /> : null}
    <div hidden={surface !== Surface.Sessions} className="session-container">{selected ? <SessionView key={selected} id={selected} draft={drafts.get(selected) ?? ""} setDraft={(value) => saveDraft(selected, value)} /> : <section className="page welcome"><h2>{copy("App.yourSessionsInOnePlace_5dad94")}</h2><p>{copy("App.selectARetainedSessionOrStart_a9de9e")}</p><Prerequisites active={surface === Surface.Sessions} openSettings={openSettings} /><Problem error={status.error} /></section>}</div>
    <NewSession active={surface === Surface.NewSession} ownsActivation={surface === Surface.NewSession} activation={newSessionEntry.activation} entryProjectId={newSessionEntry.projectId} projectSelectionBlockedChanged={setNewSessionProjectBlocked} readLocalWorker={readLocalWorker} back={() => { navigate(Surface.Sessions); void sessions.refetch(); }} openSettings={openSettings} open={open} created={() => { void sessions.refetch(); }} />
    {newGeneralChatActivation > 0 ? <NewSession kind={NewSessionKind.GeneralChat} active={surface === Surface.NewGeneralChat} ownsActivation={surface === Surface.NewGeneralChat} activation={newGeneralChatActivation} back={() => { navigate(Surface.Sessions); void sessions.refetch(); }} openSettings={openSettings} open={open} created={() => { void sessions.refetch(); }} /> : null}
    <Search active={surface === Surface.Search} open={open} focusActivation={searchFocusActivation} />
    <Activity active={surface === Surface.Activity} open={open} />
    <div className="inbox-container" hidden={surface !== Surface.Inbox}><Inbox active={surface === Surface.Inbox} open={open} notificationId={selectedInbox} notificationActivation={inboxActivation} /></div>
    <Usage active={surface === Surface.Usage} open={open} entry={usageEntry} />
    <Schedules readLocalWorker={readLocalWorker} active={surface === Surface.Schedules} open={open} />
    <PullRequests active={surface === Surface.PullRequests} openSettings={openSettings} />
    <Settings openUsage={openAccountUsage} readLocalWorker={readLocalWorker} connectionSettings={connectionSettings} pairingAuthority={pairingAuthority} currentDeviceId={currentDeviceId} controlLocalWorker={controlLocalWorker} chooseRepositoryFolder={chooseRepositoryFolder} visible={surface === Surface.Settings} entryDestination={settingsEntry} destinationConsumed={consumeSettingsEntry} />
  </main>{connectionTarget && localServer ? createPortal(localServer, connectionTarget) : null}</div></SidebarOutletProvider></SessionStorageProvider></SessionForkProvider>;
}

// Reconnects for one server/device retain this memory and its mutation receipts
// even when authentication creates a replacement transport. Selecting another
// identity creates a fresh query, draft and mutation scope.
export function App({ transport, localServer, serverPresentation, connectionSettings, connectionTarget, connectionReady = true, connectionEpoch = 0, readLocalWorker, controlLocalWorker, chooseRepositoryFolder, currentDeviceId, pairingAuthority }: { pairingAuthority?: PairingAuthority; currentDeviceId?: string; controlLocalWorker?: ControlLocalWorker; chooseRepositoryFolder?: ChooseRepositoryFolder; readLocalWorker?: ReadLocalWorkerProof; transport: Transport; localServer?: ReactNode; serverPresentation?: ServerPresentation; connectionSettings?: ReactNode; connectionTarget?: HTMLElement; connectionReady?: boolean; connectionEpoch?: number }) {
  useLocale();
  const connectionIdentity = pairingAuthority && currentDeviceId ? JSON.stringify([pairingAuthority.endpoint, pairingAuthority.serverId, currentDeviceId]) : transport;
  const connection = useMemo(() => ({ id: newRequestId(), ...connectionQueryClient() }), [connectionIdentity]);
  const client = connection.client;
  useEffect(() => connection.activate(), [connection]);
  useEffect(() => { if (connectionReady) void client.invalidateQueries({ refetchType: "active" }); }, [client, connectionReady, connectionEpoch]);
  return <TransportProvider transport={transport}><QueryClientProvider key={connection.id} client={client}><NotificationProvider><MutationIntents><PRWorkflowProvider><ShortcutProvider><Shell connectionReady={connectionReady} pairingAuthority={pairingAuthority} currentDeviceId={currentDeviceId} controlLocalWorker={controlLocalWorker} chooseRepositoryFolder={chooseRepositoryFolder} connectionSettings={connectionSettings} connectionTarget={connectionTarget} localServer={localServer} serverPresentation={serverPresentation} readLocalWorker={readLocalWorker} /></ShortcutProvider></PRWorkflowProvider></MutationIntents></NotificationProvider></QueryClientProvider></TransportProvider>;
}
