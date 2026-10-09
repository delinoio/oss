import { SidebarPreference, SidebarPreferenceBoundary, SidebarPreferenceNotice, useSidebarPreference, useWideSidebar } from "./sidebar-preference";
import { CommandMenu, applicationCommands } from "./command-menu";
import { resourceName } from "./documents";
import { SessionTabsProvider, useSessionTabsStore, useSessionTabs, SessionTabKind } from "./session-tabs";
import { SessionSubmissionsProvider } from "./session-submissions";
import { ImageDraftProvider } from "./image-drafts";
import { RunnerRemediationProvider } from "./runner-remediation";
import { RunnerPreferenceProvider } from "./runner-device-preferences";
import { SessionControlProvider } from "./session-control";
import { LocalizedText, copy, useLocale } from "./localization";
import type { UsageEntry } from "./usage-entry";
import type { ServerPresentation } from "./server-presentation";
import { LocalConnectionPresentationProvider } from "./local-connection-presentation";
import { useCallback, useEffect, useLayoutEffect, useId, useMemo, useRef, useState, type ReactNode } from "react";
import { type Transport } from "@connectrpc/connect";
import { TransportProvider, useQuery } from "@connectrpc/connect-query";
import { QueryClientProvider } from "@tanstack/react-query";
import { SessionQuery, SystemQuery, newRequestId, EntityKind, type Resource } from "@delinoio/delidev-api-client";
import { document, text } from "./documents";
import { SettingsCategory } from "./settings-category";
import { SessionView } from "./session";
import { Search, Settings, Surface } from "./views";
import { NewSession, NewSessionKind } from "./new-session";
import { Inbox } from "./inbox";
import { Usage } from "./usage";
import { Schedules } from "./schedules";
import type { ControlLocalWorker } from "./local-worker-controls";
import type { ReadLocalWorkerProof } from "./local-worker";
import { Problem } from "./ui";
import type { SkillTokenBinding } from "./skill-completion";
import { MutationIntents, useRetainedMutationAccepted } from "./mutation";
import { connectionQueryClient } from "./cache";
import type { PairingAuthority } from "./pairing-grant";
import { QuitConfirmation } from "./quit-confirmation";
import { TrayPresentation } from "./tray-presentation";
import { TrayDestination } from "./tray";
import { NotificationPresentation } from "./notification-presentation";
import { NotificationProvider } from "./toast-notifications";
import { Sidebar, Icon } from "./sidebar";
import type { ChooseRepositoryFolder } from "./repository-registration";
import { SettingsEntryDestination, type SettingsNavigationEntry } from "./settings";
import { SidebarOutletProvider } from "./sidebar-context";
import { PullRequests } from "./pull-requests";
import { SessionForkProvider } from "./session-fork";
import { SessionStorageProvider } from "./session-storage";
import { PRWorkflowProvider } from "./pr-workflow";
import { ProjectCreationDialog } from "./project-creation";
import { ShortcutProvider, useShortcutHelp, useHeldShortcutHelp, useShortcutSurface, useShortcuts } from "./shortcut-provider";
import { ShortcutId, ShortcutInput, ShortcutScope, globalShortcutBindings } from "./shortcuts";

function Shell({ localServer, serverPresentation, connectionReady, connectionSettings, connectionTarget, onConnectionHelp, readLocalWorker, controlLocalWorker, chooseRepositoryFolder, currentDeviceId, pairingAuthority }: { pairingAuthority?: PairingAuthority; currentDeviceId?: string; controlLocalWorker?: ControlLocalWorker; chooseRepositoryFolder?: ChooseRepositoryFolder; localServer?: ReactNode; serverPresentation?: ServerPresentation; connectionReady: boolean; connectionSettings?: ReactNode; connectionTarget?: HTMLElement; onConnectionHelp?: (target: HTMLElement | undefined) => void; readLocalWorker?: ReadLocalWorkerProof }) {
  useLocale();
  const [surface, setSurface] = useState(Surface.Sessions);
  useShortcutSurface(surface);
  const openHelp = useShortcutHelp();
  const holdHelp = useHeldShortcutHelp();
  const [commandMenuOpen, setCommandMenuOpen] = useState(false);
  const toggleCommandMenu = () => setCommandMenuOpen(value => !value);
  const pendingFocusDestination = useRef<Surface>(undefined);
  const main = useRef<HTMLElement>(null);
  const contextOpener = useRef<HTMLButtonElement>(null);
  const [sidebarTarget, setSidebarTarget] = useState<HTMLElement | null>(null);
  const [drawerOpen, setDrawerOpen] = useState(false);
  const sidebarPreference = useSidebarPreference();
  const wideSidebar = useWideSidebar();
  const sidebarCollapsed = sidebarPreference.snapshot.sidebar_preference === SidebarPreference.Collapsed;
  const sidebarPaneVisible = wideSidebar ? !sidebarCollapsed : drawerOpen;
  const sidebarToggle = useRef<HTMLButtonElement>(null);
  const sidebarPaneId = useId();
  const toggleSidebar = () => sidebarPreference.select(sidebarCollapsed ? SidebarPreference.Expanded : SidebarPreference.Collapsed);
  const openSidebar = () => wideSidebar ? sidebarPreference.select(SidebarPreference.Expanded) : setDrawerOpen(true);

  const [selected, setSelected] = useState("");
  const [sessionNavigationActivation, setSessionNavigationActivation] = useState(0);
  const [visited, setVisited] = useState<readonly string[]>([]);
  const tabs = useSessionTabsStore();
  const selectedTabs = useSessionTabs(selected);
  const sidebarSession = selectedTabs.tab.kind===SessionTabKind.Sidechat ? selectedTabs.tab.id : selected;
  const [selectedInbox, setSelectedInbox] = useState("");
  const [inboxActivation, setInboxActivation] = useState(0);
  const [newSessionEntry, setNewSessionEntry] = useState<{ activation: number; projectId?: string }>({ activation: 0 });
  const [newSessionProjectBlocked, setNewSessionProjectBlocked] = useState(false);
  const [newGeneralChatActivation, setNewGeneralChatActivation] = useState(0);
  const [notificationOccurrence,setNotificationOccurrence]=useState<{scheduleId:string;id:string;generation:string}>();
  const [usageEntry, setUsageEntry] = useState<UsageEntry>();
  const [settingsEntry, setSettingsEntry] = useState<SettingsNavigationEntry>();
  const [projectCreation, setProjectCreation] = useState<{ id: string; activation: number }>();
  type SessionDraft = { prompt: string; bindings: SkillTokenBinding[] };
  const [draftState, setDraftState] = useState<{ drafts: ReadonlyMap<string, SessionDraft>; error?: string }>({ drafts: new Map() });
  const draftOwner = useRef(draftState);
  const { drafts } = draftState;
  // Keep text and typed invocation ownership atomic, including before unmount.
  const saveDraft = (id: string, prompt?: string, bindings?: SkillTokenBinding[]) => {
    const current = draftOwner.current, previous = current.drafts.get(id);
    const next = { prompt: prompt ?? previous?.prompt ?? "", bindings: bindings ?? previous?.bindings ?? [] };
    if (previous?.prompt === next.prompt && previous.bindings === next.bindings || !previous && !next.prompt && !next.bindings.length) return true;
    const size = new TextEncoder().encode(next.prompt).byteLength;
    const total = [...current.drafts].reduce((bytes, [key, draft]) => bytes + (key === id ? 0 : new TextEncoder().encode(JSON.stringify(draft)).byteLength), new TextEncoder().encode(JSON.stringify(next)).byteLength);
    if (size > 256 << 10 || total > 4 << 20 || ((next.prompt || next.bindings.length) && !previous && current.drafts.size >= 1000)) {
      draftOwner.current = { ...current, error: copy("App.extra.979e130130a9") }; setDraftState(draftOwner.current); return false;
    }
    const drafts = new Map(current.drafts);
    if (next.prompt || next.bindings.length) drafts.set(id, next); else drafts.delete(id);
    draftOwner.current = { drafts }; setDraftState(draftOwner.current); return true;
  };
  const sessions = useQuery(SessionQuery.listSessions, { projectId: "", includeArchived: false, pageSize: 50, pageToken: "" });
  const status = useQuery(SystemQuery.getStatus, {}, { refetchInterval: 30000 });
  const leaveSurface = (destination: Surface) => {
    pendingFocusDestination.current = surface === Surface.Settings ? destination : undefined;
    setSettingsEntry(undefined);
    if (destination !== surface) setProjectCreation(undefined);
  };
  const open = (id: string, originalParent?:string, name?:string) => {
    setSessionNavigationActivation(value => value + 1);
    const parent = tabs.parent(id) ?? originalParent; if (parent) { tabs.open(parent, tabs.sidechats(parent).find(tab=>tab.id===id) ?? { kind: SessionTabKind.Sidechat, id, name: name ?? id }); id = parent; }
    setVisited(previous => previous.includes(id) ? previous : [...previous, id]); leaveSurface(Surface.Sessions); if (id !== selected) setProjectCreation(undefined); setSelected(id); setSurface(Surface.Sessions); setDrawerOpen(false); };
  const navigateTray = (destination: TrayDestination, inboxId?: string) => {
    if (destination === TrayDestination.Settings) { openSettings(); return; }
    if(destination===TrayDestination.ConnectionDiagnostics){openSettings(SettingsEntryDestination.ConnectionDiagnostics);return;}
    const next = destination === TrayDestination.Inbox ? Surface.Inbox : destination === TrayDestination.Usage ? Surface.Usage : Surface.Sessions;
    leaveSurface(next);
    if (destination === TrayDestination.Inbox) { setSelectedInbox(inboxId ?? ""); setInboxActivation((value) => value + 1); }
    setDrawerOpen(false);
    setSurface(next);
  };
  const openSettings = (destination?: SettingsNavigationEntry) => {
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
  const openOperationalNotification=(resource:Resource)=>{
    if(resource.kind===EntityKind.MACHINE||resource.kind===EntityKind.ACCOUNT){openSettings({category:resource.kind===EntityKind.MACHINE?SettingsCategory.ExecutionWorkers:SettingsCategory.SubscriptionAccounts,generation:newRequestId(),resourceId:resource.id,resourceKind:resource.kind});return;}
    if(resource.kind===EntityKind.OCCURRENCE){setNotificationOccurrence({id:resource.id,scheduleId:text(document(resource).schedule_id),generation:newRequestId()});leaveSurface(Surface.Schedules);setSurface(Surface.Schedules);setDrawerOpen(false);}
  };
  const surfaceName = surface === Surface.Sessions || surface === Surface.NewSession || surface === Surface.NewGeneralChat ? copy("App.extra.2998edd080d1") : surface === Surface.Settings ? copy("App.extra.a1de4eceaa3b") : surface === Surface.PullRequests ? copy("App.extra.23533b15bc29") : surface === Surface.Usage ? copy("App.extra.34d76f3f7da4") : surface === Surface.Schedules ? copy("App.extra.a6a986427e87") : surface === Surface.Inbox ? copy("App.extra.a1de2be5c09b") : copy("App.extra.1f73d5f3eac5");
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
  const sidebarShortcuts = useShortcuts([
    { id: ShortcutId.ToggleSidebar, scope: ShortcutScope.Global, label: "sidebar-preference.toggle", bindings: globalShortcutBindings[ShortcutId.ToggleSidebar], input: ShortcutInput.Allow, active: wideSidebar, enabled: !sidebarPreference.operation && !sidebarPreference.snapshot.problem, run: toggleSidebar },
    { id: ShortcutId.CommandMenu, scope: ShortcutScope.Global, label: "command-menu.title", bindings: globalShortcutBindings[ShortcutId.CommandMenu], input: ShortcutInput.Allow, run: toggleCommandMenu },
    { id: ShortcutId.Help, scope: ShortcutScope.Global, label: "shortcuts.help", bindings: globalShortcutBindings[ShortcutId.Help], helpKeydown: holdHelp },
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
  return <LocalConnectionPresentationProvider target={connectionTarget} inline={!connectionReady} onRequest={onConnectionHelp}><RunnerRemediationProvider active authority={pairingAuthority}><RunnerPreferenceProvider readLocalWorker={readLocalWorker} scope={pairingAuthority && currentDeviceId ? { server_id: pairingAuthority.serverId, device_id: currentDeviceId } : undefined}><SessionControlProvider><SessionForkProvider openSession={open} openSidechat={(parent, child) => { tabs.open(parent, {kind:SessionTabKind.Sidechat,id:child.id,name:resourceName(child)}); open(parent); }} readLocalWorker={readLocalWorker}><SessionStorageProvider><SidebarOutletProvider target={sidebarTarget} closeDrawer={() => setDrawerOpen(false)} drawerOpen={drawerOpen} paneVisible={sidebarPaneVisible} openDrawer={openSidebar}><div className={`app${wideSidebar && sidebarCollapsed ? " sidebar-collapsed" : ""}`}><a className="skip" href="#main">{copy("App.skipToContent_ac576a")}</a><Sidebar collapsed={sidebarCollapsed} paneId={sidebarPaneId} toggleRef={sidebarToggle} compactFocusRef={contextOpener} openCommandMenu={toggleCommandMenu} connectionReady={connectionReady} serverPresentation={serverPresentation} surface={surface} selectedSessionId={sidebarSession} selectedSessionActivation={sessionNavigationActivation} navigate={navigate} navigateHeader={navigateHeader} openSession={open} newSession={startNewSession} newGeneralChat={startNewGeneralChat} newProject={openNewProject} projectSelectionBlocked={newSessionProjectBlocked} openSettings={openSettings} setContextTarget={setSidebarTarget} drawerOpen={drawerOpen} setDrawerOpen={setDrawerOpen} />{commandMenuOpen ? <CommandMenu close={() => setCommandMenuOpen(false)} commands={applicationCommands({ navigate, navigateHeader, openSettings, newSession: startNewSession, newGeneralChat: startNewGeneralChat, newProject: openNewProject, help: openHelp })} /> : null}<main ref={main} id="main" tabIndex={-1}><button ref={sidebarToggle} type="button" className="sidebar-wide-toggle" aria-label={copy(sidebarCollapsed ? "sidebar-preference.expand" : "sidebar-preference.collapse")} title={copy(sidebarCollapsed ? "sidebar-preference.expand" : "sidebar-preference.collapse")} aria-expanded={!sidebarCollapsed} aria-controls={sidebarPaneId} aria-keyshortcuts={sidebarShortcuts.aria(ShortcutId.ToggleSidebar)} aria-disabled={Boolean(sidebarPreference.operation || sidebarPreference.snapshot.problem)} onClick={toggleSidebar}><Icon name="sidebar-toggle" /></button><SidebarPreferenceNotice /><button ref={contextOpener} type="button" className="sidebar-context-trigger" aria-haspopup="dialog" aria-expanded={drawerOpen} onClick={() => setDrawerOpen(true)}><LocalizedText id="App.open_a007d6" components={{ s0: <>{surfaceName}</> }} /></button><TrayPresentation navigate={navigateTray} /><NotificationPresentation /><QuitConfirmation ready={connectionReady}/>{draftState.error ? <p role="alert">{draftState.error}</p> : null}
    {[...drafts.keys()].map(id => <SessionDraftSettlement key={id} id={id} clear={() => { saveDraft(id, "", []); }} />)}
    {projectCreation ? <ProjectCreationDialog registrationAdapters={{ readLocalWorker, controlLocalWorker, chooseFolder: chooseRepositoryFolder }} key={projectCreation.id} activation={projectCreation.activation} close={closeProjectCreation} fallbackFocus={projectFallbackFocus} /> : null}
    <div hidden={surface !== Surface.Sessions} className="session-container">{visited.map(id => <div key={id} hidden={id !== selected} inert={id !== selected}><SessionView active={surface === Surface.Sessions && id === selected} id={id} draft={drafts.get(id)?.prompt ?? ""} initialSkills={drafts.get(id)?.bindings} setDraft={(value, bindings) => saveDraft(id, value, bindings)} changeSkills={bindings => { saveDraft(id, undefined, bindings); }} openRunnerSettings={() => openSettings(SettingsEntryDestination.RunnerDevices)} /></div>)}{!selected ? <section className="page welcome"><h2>{copy("App.yourSessionsInOnePlace_5dad94")}</h2><p>{copy("App.selectARetainedSessionOrStart_a9de9e")}</p><Problem error={status.error} actions={<button type="button" disabled={status.isFetching || !connectionReady} onClick={() => void status.refetch()}>{copy("ui.retryCurrentRead")}</button>} /></section> : null}</div>
    <NewSession preferenceScope={pairingAuthority && currentDeviceId ? { server_id: pairingAuthority.serverId, device_id: currentDeviceId } : undefined} active={surface === Surface.NewSession} ownsActivation={surface === Surface.NewSession} activation={newSessionEntry.activation} entryProjectId={newSessionEntry.projectId} projectSelectionBlockedChanged={setNewSessionProjectBlocked} readLocalWorker={readLocalWorker} back={() => { navigate(Surface.Sessions); void sessions.refetch(); }} openSettings={openSettings} open={open} created={() => { void sessions.refetch(); }} />
    {newGeneralChatActivation > 0 ? <NewSession preferenceScope={pairingAuthority && currentDeviceId ? { server_id: pairingAuthority.serverId, device_id: currentDeviceId } : undefined} kind={NewSessionKind.GeneralChat} active={surface === Surface.NewGeneralChat} ownsActivation={surface === Surface.NewGeneralChat} activation={newGeneralChatActivation} back={() => { navigate(Surface.Sessions); void sessions.refetch(); }} openSettings={openSettings} open={open} created={() => { void sessions.refetch(); }} /> : null}
    <Search active={surface === Surface.Search} open={open} />
    <div className="inbox-container" hidden={surface !== Surface.Inbox}><Inbox active={surface === Surface.Inbox} open={open} notificationId={selectedInbox} notificationActivation={inboxActivation} openOperational={openOperationalNotification} /></div>
    <Usage active={surface === Surface.Usage} open={open} entry={usageEntry} />
    <Schedules readLocalWorker={readLocalWorker} active={surface === Surface.Schedules} open={open} notificationOccurrence={notificationOccurrence} clearNotificationOccurrence={()=>setNotificationOccurrence(undefined)} />
    <PullRequests active={surface === Surface.PullRequests} openSettings={openSettings} />
    <Settings openUsage={openAccountUsage} readLocalWorker={readLocalWorker} connectionSettings={connectionSettings} pairingAuthority={pairingAuthority} currentDeviceId={currentDeviceId} controlLocalWorker={controlLocalWorker} chooseRepositoryFolder={chooseRepositoryFolder} visible={surface === Surface.Settings} entryDestination={settingsEntry} destinationConsumed={consumeSettingsEntry} />
    {localServer}
  </main></div></SidebarOutletProvider></SessionStorageProvider></SessionForkProvider></SessionControlProvider></RunnerPreferenceProvider></RunnerRemediationProvider></LocalConnectionPresentationProvider>;
}

// Reconnects for one server/device retain this memory and its mutation receipts
// even when authentication creates a replacement transport. Selecting another
// identity creates a fresh query, draft and mutation scope.
export function App({ transport, localServer, serverPresentation, connectionSettings, connectionTarget, onConnectionHelp, connectionReady = true, connectionEpoch = 0, readLocalWorker, controlLocalWorker, chooseRepositoryFolder, currentDeviceId, pairingAuthority }: { pairingAuthority?: PairingAuthority; currentDeviceId?: string; controlLocalWorker?: ControlLocalWorker; chooseRepositoryFolder?: ChooseRepositoryFolder; readLocalWorker?: ReadLocalWorkerProof; transport: Transport; localServer?: ReactNode; serverPresentation?: ServerPresentation; connectionSettings?: ReactNode; connectionTarget?: HTMLElement; onConnectionHelp?: (target: HTMLElement | undefined) => void; connectionReady?: boolean; connectionEpoch?: number }) {
  useLocale();
  const connectionIdentity = pairingAuthority && currentDeviceId ? JSON.stringify([pairingAuthority.endpoint, pairingAuthority.serverId, currentDeviceId]) : transport;
  const connection = useMemo(() => ({ id: newRequestId(), ...connectionQueryClient() }), [connectionIdentity]);
  const client = connection.client;
  useEffect(() => connection.activate(), [connection]);
  useEffect(() => { if (connectionReady) void client.invalidateQueries({ refetchType: "active" }); }, [client, connectionReady, connectionEpoch]);
  return <SidebarPreferenceBoundary><TransportProvider transport={transport}><QueryClientProvider key={connection.id} client={client}><NotificationProvider><MutationIntents><SessionTabsProvider><SessionSubmissionsProvider><ImageDraftProvider><PRWorkflowProvider><ShortcutProvider><Shell connectionReady={connectionReady} pairingAuthority={pairingAuthority} currentDeviceId={currentDeviceId} controlLocalWorker={controlLocalWorker} chooseRepositoryFolder={chooseRepositoryFolder} connectionSettings={connectionSettings} connectionTarget={connectionTarget} onConnectionHelp={onConnectionHelp} localServer={localServer} serverPresentation={serverPresentation} readLocalWorker={readLocalWorker} /></ShortcutProvider></PRWorkflowProvider></ImageDraftProvider></SessionSubmissionsProvider></SessionTabsProvider></MutationIntents></NotificationProvider></QueryClientProvider></TransportProvider></SidebarPreferenceBoundary>;
}

// The connection owns submitted drafts even while another Session is mounted.
function SessionDraftSettlement({ id, clear }: { id: string; clear: () => void }) {
  useRetainedMutationAccepted(`enqueue:${id}`, clear);
  return null;
}
