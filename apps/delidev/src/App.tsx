import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";
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
import { MutationIntents } from "./mutation";
import { connectionQueryClient } from "./cache";
import type { PairingAuthority } from "./pairing-grant";
import { TrayPresentation } from "./tray-presentation";
import { TrayDestination } from "./tray";
import { NotificationPresentation } from "./notification-presentation";
import { Sidebar } from "./sidebar";
import { SettingsEntryDestination } from "./settings";

function Shell({ localServer, readLocalWorker, controlLocalWorker, currentDeviceId, pairingAuthority }: { pairingAuthority?: PairingAuthority; currentDeviceId?: string; controlLocalWorker?: ControlLocalWorker; localServer?: ReactNode; readLocalWorker?: ReadLocalWorkerProof }) {
  const [surface, setSurface] = useState(Surface.Sessions);
  const [selected, setSelected] = useState("");
  const [selectedInbox, setSelectedInbox] = useState("");
  const [inboxActivation, setInboxActivation] = useState(0);
  const [settings, setSettings] = useState(false);
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
  const open = (id: string) => { setSelected(id); setSurface(Surface.Sessions); };
  const navigateTray = (destination: TrayDestination, inboxId?: string) => {
    if (destination === TrayDestination.Inbox) { setSelectedInbox(inboxId ?? ""); setInboxActivation((value) => value + 1); }
    if (destination === TrayDestination.Settings) { setSettings(true); return; }
    setSettings(false);
    setSurface(destination === TrayDestination.Inbox ? Surface.Inbox : destination === TrayDestination.Usage ? Surface.Usage : Surface.Sessions);
  };
  const openSettings = (destination?: SettingsEntryDestination) => { if (destination) setSettingsEntry(destination); setSettings(true); };
  const consumeSettingsEntry = useCallback(() => setSettingsEntry(undefined), []);
  return <div className="app"><a className="skip" href="#main">Skip to content</a><Sidebar surface={surface} selectedSessionId={selected} localServer={localServer} navigate={(destination) => { setSurface(destination); if (destination === Surface.Inbox) setSelectedInbox(""); }} openSession={open} newSession={() => { setSettings(false); setNewSessionActivation((value) => value + 1); setSurface(Surface.NewSession); }} openSettings={openSettings} /><main id="main" tabIndex={-1}><TrayPresentation navigate={navigateTray} /><NotificationPresentation />{draftState.error ? <p role="alert">{draftState.error}</p> : null}
    <div hidden={surface !== Surface.Sessions} className="session-container">{selected ? <SessionView key={selected} id={selected} draft={drafts.get(selected) ?? ""} setDraft={(value) => saveDraft(selected, value)} /> : <section className="page welcome"><h2>Your sessions, in one place</h2><p>Select a retained session or start a new conversation.</p><h3>Before your first session</h3><ol><li>Connect to your DeliDev server.</li><li>Pair an execution Worker and verify its installed harness.</li><li>Connect an AI account and configure an Agent Worker.</li><li>Configure a project, or choose General Chat.</li></ol><button onClick={(event) => { event.currentTarget.focus(); setSettings(true); }}>View prerequisites in Settings</button><Problem error={status.error} /></section>}</div>
    <NewSession active={surface === Surface.NewSession} ownsActivation={surface === Surface.NewSession && !settings} activation={newSessionActivation} readLocalWorker={readLocalWorker} back={() => { setSurface(Surface.Sessions); void sessions.refetch(); }} openSettings={() => openSettings()} open={open} created={() => { void sessions.refetch(); }} />
    {surface === Surface.Search ? <Search open={open} /> : surface === Surface.Activity ? <Activity open={open} /> : null}
    <div className="inbox-container" hidden={surface !== Surface.Inbox}><Inbox active={surface === Surface.Inbox} open={open} notificationId={selectedInbox} notificationActivation={inboxActivation} /></div>
    <Usage active={surface === Surface.Usage} open={open} />
    <Schedules readLocalWorker={readLocalWorker} active={surface === Surface.Schedules} open={open} />
  </main><Settings pairingAuthority={pairingAuthority} currentDeviceId={currentDeviceId} controlLocalWorker={controlLocalWorker} close={() => setSettings(false)} visible={settings} entryDestination={settingsEntry} destinationConsumed={consumeSettingsEntry} /></div>;
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
  return <TransportProvider transport={transport}><QueryClientProvider key={connection.id} client={client}><MutationIntents><Shell pairingAuthority={pairingAuthority} currentDeviceId={currentDeviceId} controlLocalWorker={controlLocalWorker} localServer={localServer} readLocalWorker={readLocalWorker} /></MutationIntents></QueryClientProvider></TransportProvider>;
}
