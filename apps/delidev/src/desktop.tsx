import { useEffect, useRef, useState } from "react";
import { invoke, isTauri } from "@tauri-apps/api/core";
import { listen } from "@tauri-apps/api/event";
import { type Transport } from "@connectrpc/connect";
import { createDeliDevTransport } from "@delinoio/delidev-api-client";
import { App } from "./App";
import { Problem } from "./ui";
import { SavedConnections, SavedConnectionProblem, type SavedConnection, type SavedConnectionActions } from "./saved-connections";
import { LocalServerControls, LocalServerState, LocalServerStatusText, type LocalServerStatus } from "./local-server";
import type { ControlLocalWorker, LocalWorkerStatus } from "./local-worker-controls";
import { verifyLocalServer } from "./connection";

import { LocalRegistrationRecovery, localPermissionProblem, type NativeConnection } from "./local-registration";
const nativeProblems: Record<string, string> = {
  stopped: "The server was explicitly stopped. Start it only when you intend to resume its local lifecycle.",
  busy: "A local connection attempt is already running. Wait for it to finish.",
  "sidecar-missing": "The bundled DeliDev executable is missing. Repair the desktop installation.",
  "sidecar-failed": "The local server could not connect. Inspect DeliDev server status and its private log, then retry.",
  "timed-out": "Local startup has not completed. Check server status before retrying; accepted work may continue.",
  incompatible: "The running server uses a different version or listener. Preserve its sessions and use a compatible client or explicitly stop it before changing the server.",
  "credential-unavailable": "This desktop credential is unavailable or revoked. Use Check desktop registration to inspect it and explicitly recover a revoked local registration.",
  "permission-denied": localPermissionProblem,
  "invalid-evidence": "The retained local connection requires inspection. Preserve its original pairing and server data.",
  "storage-unavailable": "The private DeliDev configuration directory is unavailable. Check this computer's user configuration.",
};
function LocalDesktop() {
  const [showSaved, setShowSaved] = useState(false);
  const previous = useRef<NativeConnection>(undefined);
  const [status, setStatus] = useState<LocalServerStatus>();
  const [connectionEpoch, setConnectionEpoch] = useState(0);
  useEffect(() => {
    if (!isTauri()) return;
    let canceled = false;
    let timer: ReturnType<typeof setTimeout>;
    const read = async () => {
      try { const value = await invoke<LocalServerStatus>("local_server_status"); if (!canceled) setStatus(value); }
      catch { if (!canceled) setStatus(undefined); }
      finally { if (!canceled) timer = setTimeout(() => void read(), 2000); }
    };
    void read();
    return () => { canceled = true; clearTimeout(timer); };
  }, []);
  const [transport, setTransport] = useState<Transport>();
  const [error, setError] = useState<unknown>();
  const [busy, setBusy] = useState(false);
  const connecting = useRef(false);
  const acceptConnection = async (connection: NativeConnection) => {
    const candidate = createDeliDevTransport({ origin: connection.endpoint, getToken: () => connection.token });
    await verifyLocalServer(candidate, connection.server_id);
    const old = previous.current;
    if (!old || old.server_id !== connection.server_id || old.device_id !== connection.device_id || old.endpoint !== connection.endpoint || old.token !== connection.token) setTransport(candidate);
    previous.current = connection;
    setConnectionEpoch((epoch) => epoch + 1);
    setError(undefined);
  };
  const connect = async () => {
    if (busy || connecting.current) return;
    connecting.current = true;
    setBusy(true); setError(undefined);
    try {
      const connection = await invoke<NativeConnection>("connect_local");
      await acceptConnection(connection);
    } catch (reason) { setError(reason); } finally { connecting.current = false; setBusy(false); }
  };
  const chooseRepositoryFolder = async () => {
    const selected = previous.current;
    const path = await invoke<string | null>("choose_repository_folder");
    if (!selected || previous.current !== selected) throw new Error("Folder selection connection changed");
    return path;
  };
  const readLocalWorker = async () => {
    const selected = previous.current;
    const proof = await invoke<{ endpoint: string; server_id: string; machine_id: string; token: string }>("local_worker_proof");
    if (!selected || previous.current !== selected || proof.endpoint !== selected.endpoint || proof.server_id !== selected.server_id) throw new Error("Local Worker authority changed");
    return { machineId: proof.machine_id, token: proof.token };
  };
  const controlLocalWorker: ControlLocalWorker = async (action, generation) => {
    const selected = previous.current;
    const value = await invoke<LocalWorkerStatus>("local_worker_control", { action, generation });
    if (!selected || previous.current !== selected) throw new Error("Local Worker connection changed");
    return value;
  };
  const registration = isTauri() ? <LocalRegistrationRecovery busy={busy} setBusy={setBusy} recovered={acceptConnection} /> : null;
  const problem = typeof error === "string" && Object.hasOwn(nativeProblems, error) ? <p role="alert">{nativeProblems[error]}</p> : <Problem error={error} />;
  return <>{transport ? <App chooseRepositoryFolder={chooseRepositoryFolder} pairingAuthority={previous.current ? { endpoint: previous.current.endpoint, serverId: previous.current.server_id } : undefined} currentDeviceId={previous.current?.device_id} controlLocalWorker={controlLocalWorker} readLocalWorker={readLocalWorker} transport={transport} connectionReady={status?.state === LocalServerState.Ready} connectionEpoch={connectionEpoch} localServer={<><LocalServerControls status={status} restart={() => void connect()} busy={busy} problem={problem} />{registration}<button onClick={() => setShowSaved(true)}>Saved servers</button></>} /> : <main className="connect-page"><h1>DeliDev</h1><h2>Connect to your local server</h2><p>The server and its sessions continue when you close DeliDev.</p>{isTauri() ? <><button className="primary" disabled={busy} onClick={() => void connect()}>{busy ? "Connecting…" : "Start or connect"}</button><button onClick={() => setShowSaved(true)}>Saved servers</button></> : <p>Open the DeliDev desktop app to connect. Browser clients are not supported.</p>}{isTauri() ? <LocalServerStatusText status={status} /> : null}{problem}{registration}</main>}<SavedConnections visible={showSaved} close={() => setShowSaved(false)} actions={savedActions} /></>;
}
const savedActions: SavedConnectionActions = {
  list: () => invoke<SavedConnection[]>("saved_connections"),
  removed: (after) => invoke("removed_connections", { after }),
  remove: (id, requestId, revision) => invoke("remove_connection", { id, requestId, revision }),
  retainedWorker: (id, action, generation) => invoke("retained_worker_control", { id, action, generation }),
  pair: (id, name, grant) => invoke<SavedConnection>("pair_connection", { id, name, grant }),
  retry: (id) => invoke<SavedConnection>("retry_connection", { id }),
  rename: (id, requestId, revision, name) => invoke<SavedConnection>("rename_connection", { id, requestId, revision, name }),
  open: (id) => invoke<void>("open_connection", { id }),
};
function SavedDesktop({ profile }: { profile: SavedConnection }) {
  const [transport, setTransport] = useState<Transport>();
  const [error, setError] = useState<unknown>();
  const [busy, setBusy] = useState(false);
  const [epoch, setEpoch] = useState(0);
  const previous = useRef<NativeConnection>(undefined);
  const connecting = useRef(false);
  const connect = async () => {
    if (connecting.current) return;
    connecting.current = true; setBusy(true); setError(undefined);
    try {
      const connection = await invoke<NativeConnection>("connect_saved");
      if (connection.server_id !== profile.server_id || connection.device_id !== profile.device_id || connection.endpoint !== profile.endpoint) throw "invalid-evidence";
      const candidate = createDeliDevTransport({ origin: connection.endpoint, getToken: () => connection.token });
      await verifyLocalServer(candidate, connection.server_id);
      if (!previous.current || previous.current.token !== connection.token) setTransport(candidate);
      previous.current = connection; setEpoch((value) => value + 1);
    } catch (error) { setError(error); } finally { connecting.current = false; setBusy(false); }
  };
  // Opening this native window is the explicit selection. Reconnecting verifies
  // its same saved identity; it never retries pairing or starts the server.
  useEffect(() => { void connect(); }, []);
  const chooseRepositoryFolder = async () => {
    const selected = previous.current;
    const path = await invoke<string | null>("choose_repository_folder");
    if (!selected || previous.current !== selected) throw new Error("Folder selection connection changed");
    return path;
  };
  const readLocalWorker = async () => {
    const selected = previous.current;
    const proof = await invoke<{ endpoint: string; server_id: string; machine_id: string; token: string }>("saved_worker_proof");
    if (!selected || previous.current !== selected || proof.endpoint !== selected.endpoint || proof.server_id !== selected.server_id) throw new Error("Saved Worker authority changed");
    return { machineId: proof.machine_id, token: proof.token };
  };
  const controlLocalWorker: ControlLocalWorker = async (action, generation) => {
    const selected = previous.current;
    const value = await invoke<LocalWorkerStatus>("saved_worker_control", { action, generation });
    if (!selected || previous.current !== selected) throw new Error("Saved Worker connection changed");
    return value;
  };
  const controls = <><p>{profile.name}</p><small>{profile.endpoint}</small><button disabled={busy} onClick={() => void connect()}>Verify saved connection</button><button onClick={() => void invoke("show_connection_manager").catch(setError)}>Show local window</button><SavedConnectionProblem error={error} /></>;
  return transport ? <App chooseRepositoryFolder={chooseRepositoryFolder} pairingAuthority={{ endpoint: profile.endpoint, serverId: profile.server_id }} transport={transport} readLocalWorker={readLocalWorker} controlLocalWorker={controlLocalWorker} currentDeviceId={profile.device_id} connectionEpoch={epoch} localServer={controls} /> : <main className="connect-page"><h1>DeliDev</h1><h2>{busy ? "Connecting to saved server…" : "Saved server connection"}</h2><p>This window is pinned to server {profile.server_id}. Remote sessions continue when it closes.</p>{controls}</main>;
}
export function Desktop() {
  const [context, setContext] = useState<{ ready: boolean; profile?: SavedConnection; error?: unknown }>({ ready: !isTauri() });
  const read = async () => {
    try { const profile = await invoke<SavedConnection | null>("connection_context"); setContext({ ready: true, profile: profile ?? undefined }); }
    catch (error) { setContext({ ready: false, error }); }
  };
  useEffect(() => { if (isTauri()) void read(); }, []);
  useEffect(() => {
    if (!isTauri()) return;
    let canceled = false;
    const subscription = listen("saved-connection-label", () => {
      if (canceled) return;
      void invoke<SavedConnection | null>("connection_context").then((profile) => {
        if (!canceled && profile) setContext((previous) => previous.profile?.id === profile.id && profile.revision >= previous.profile.revision ? { ...previous, profile } : previous);
      }).catch(() => { /* The original window, authorization and drafts remain active if label refresh fails. */ });
    }).catch(() => () => {});
    return () => { canceled = true; void subscription.then((unlisten) => unlisten()).catch(() => {}); };
  }, []);
  if (!context.ready) return <main className="connect-page"><h1>DeliDev</h1><p>Reading this window's server identity…</p><SavedConnectionProblem error={context.error} />{context.error ? <button onClick={() => void read()}>Retry window context</button> : null}</main>;
  return context.profile ? <SavedDesktop profile={context.profile} /> : <LocalDesktop />;
}
