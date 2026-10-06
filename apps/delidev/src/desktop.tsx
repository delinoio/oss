import { OAuthNativeProvider, type OAuthNativeControl } from "./account-oauth";
import { ServerPresentationKind } from "./server-presentation";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { invoke, isTauri } from "@tauri-apps/api/core";
import { listen } from "@tauri-apps/api/event";
import { type Transport } from "@connectrpc/connect";
import { createDeliDevTransport } from "@delinoio/delidev-api-client";
import { Updates, type DesktopUpdateControls } from "./updates";
import { App } from "./App";
import { Modal, Problem } from "./ui";
import { SavedConnections, SavedConnectionProblem, type SavedConnection, type SavedConnectionActions } from "./saved-connections";
import { LocalServerControls, LocalServerState, LocalServerStatusText, type LocalServerStatus } from "./local-server";
import type { ControlLocalWorker, LocalWorkerStatus } from "./local-worker-controls";
import { WorkerNetworkControlProvider, type ControlWorkerNetwork } from "./worker-network-native";
import { verifyLocalServer } from "./connection";

import { LocalRegistrationRecovery, localPermissionProblem, type NativeConnection } from "./local-registration";
const nativeProblems: Record<string, string> = {
  "service-managed": "A native service owns this connection. Start or inspect its original registration through service management; DeliDev will not launch a competing server.",
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
  const [showConnection, setShowConnection] = useState(false);
  const [connectionTarget, setConnectionTarget] = useState<HTMLDivElement | null>(null);
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
  const [busy, setBusy] = useState(isTauri());
  const connecting = useRef(false);
  const acceptConnection = async (connection: NativeConnection, current: () => boolean = () => true) => {
    const candidate = createDeliDevTransport({ origin: connection.endpoint, getToken: () => connection.token });
    await verifyLocalServer(candidate, connection.server_id);
    if (!current()) return;
    const old = previous.current;
    if (!old || old.server_id !== connection.server_id || old.device_id !== connection.device_id || old.endpoint !== connection.endpoint || old.token !== connection.token) setTransport(candidate);
    previous.current = connection;
    setConnectionEpoch((epoch) => epoch + 1);
    setError(undefined);
  };
  const connect = async (action: "connect_local" | "retry_local" = "connect_local") => {
    if (busy || connecting.current) return;
    connecting.current = true;
    setBusy(true); setError(undefined);
    try {
      const connection = await invoke<NativeConnection>(action);
      await acceptConnection(connection);
    } catch (reason) { setError(reason); } finally { connecting.current = false; setBusy(false); }
  };
  useEffect(() => {
    if (!isTauri()) return;
    let canceled = false;
    let timer: ReturnType<typeof setTimeout>;
    const observe = async () => {
      try {
        // Native owns the operation across every renderer lifecycle. A null
        // observation is pending; reading it never starts another attempt.
        const connection = await invoke<NativeConnection | null>("launch_local");
        if (canceled) return;
        if (!connection) { timer = setTimeout(() => void observe(), 250); return; }
        await acceptConnection(connection, () => !canceled);
        if (!canceled) setBusy(false);
      } catch (reason) { if (!canceled) { setError(reason); setBusy(false); } }
    };
    void observe();
    return () => { canceled = true; clearTimeout(timer); };
  }, []);
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
  const controlWorkerNetwork: ControlWorkerNetwork = async (machine, action, ciphertext, digest) => {
    const selected = previous.current;
    const result = await invoke<unknown>("worker_network_control", { machine, action, ciphertext: Array.from(ciphertext), digest });
    if (!selected || previous.current !== selected) throw new Error("Worker network connection changed");
    return result;
  };
  const controlLocalWorker: ControlLocalWorker = async (action, generation) => {
    const selected = previous.current;
    const value = await invoke<LocalWorkerStatus>("local_worker_control", { action, generation });
    if (!selected || previous.current !== selected) throw new Error("Local Worker connection changed");
    return value;
  };
  const problem = typeof error === "string" && Object.hasOwn(nativeProblems, error) ? <p role="alert">{nativeProblems[error]}</p> : <Problem error={error} />;
  const connectionSettings = <button onClick={() => setShowConnection(true)}>Connection controls</button>;
  const updateControls = useMemo<DesktopUpdateControls>(() => ({ readContext: () => invoke("desktop_update_context"), control: async (action, id, revision) => {
    const selected = previous.current;
    if (!selected) throw "invalid-evidence";
    const result = await invoke<import("./updates").NativeUpdateResult>("desktop_update_native", { server: selected.server_id, id, revision: revision.toString(), action });
    if (previous.current !== selected) throw "invalid-evidence";
    return result;
  } }), []);
  const controls = <><LocalServerControls status={status} restart={() => void connect()} busy={busy} problem={problem} /><Updates active={showConnection} controls={updateControls} /></>;
  const oauthServer = previous.current?.server_id ?? "";
  const controlOAuth = useCallback<OAuthNativeControl>((opening, action, generation, attempt, authorization) => invoke("account_oauth_native", { server: oauthServer, opening, action, generation, attempt, authorization }), [oauthServer]);
  return <>{transport ? <OAuthNativeProvider control={controlOAuth}><WorkerNetworkControlProvider control={controlWorkerNetwork}><App serverPresentation={{ kind: ServerPresentationKind.Local }} pairingAuthority={previous.current ? { endpoint: previous.current.endpoint, serverId: previous.current.server_id } : undefined} currentDeviceId={previous.current?.device_id} controlLocalWorker={controlLocalWorker} chooseRepositoryFolder={chooseRepositoryFolder} readLocalWorker={readLocalWorker} transport={transport} connectionReady={status?.state === LocalServerState.Ready} connectionEpoch={connectionEpoch} connectionSettings={connectionSettings} connectionTarget={connectionTarget ?? undefined} localServer={controls} /></WorkerNetworkControlProvider></OAuthNativeProvider> : <main className="connect-page"><h1>DeliDev</h1>{isTauri() ? <><p role="status">{busy ? "Starting DeliDev…" : "DeliDev could not connect."}</p>{error ? <><p role="alert">{error === "stopped" ? "DeliDev is disconnected on this computer. Open Troubleshooting to start it when you are ready." : "DeliDev could not finish starting. Retry or open Troubleshooting for help."}</p><button className="primary" disabled={busy || error === "stopped"} onClick={() => void connect("retry_local")}>Retry</button></> : null}<button onClick={() => setShowConnection(true)}>Troubleshooting</button></> : <p>Open the DeliDev desktop app to connect. Browser clients are not supported.</p>}</main>}
    <Modal title="Connection & diagnostics" visible={showConnection} close={() => setShowConnection(false)}>
      <section aria-label="Connection"><h3>Connection on this computer</h3><div ref={setConnectionTarget} />{!transport ? <><LocalServerStatusText status={status} /><button disabled={busy} onClick={() => void connect()}>Start local server</button>{problem}</> : null}
        <LocalRegistrationRecovery busy={busy} setBusy={setBusy} recovered={acceptConnection} active={showConnection} />
        <button onClick={() => setShowSaved(true)}>Saved servers</button>
      </section>
    </Modal>
    <SavedConnections visible={showSaved} close={() => setShowSaved(false)} actions={savedActions} />
  </>;

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
  const [showConnection, setShowConnection] = useState(false);
  const [connectionTarget, setConnectionTarget] = useState<HTMLDivElement | null>(null);
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
  const controlWorkerNetwork: ControlWorkerNetwork = async (machine, action, ciphertext, digest) => {
    const selected = previous.current;
    const result = await invoke<unknown>("worker_network_control", { machine, action, ciphertext: Array.from(ciphertext), digest });
    if (!selected || previous.current !== selected) throw new Error("Worker network connection changed");
    return result;
  };
  const controlLocalWorker: ControlLocalWorker = async (action, generation) => {
    const selected = previous.current;
    const value = await invoke<LocalWorkerStatus>("saved_worker_control", { action, generation });
    if (!selected || previous.current !== selected) throw new Error("Saved Worker connection changed");
    return value;
  };
  const updateControls = useMemo<DesktopUpdateControls>(() => ({ readContext: () => invoke("desktop_update_context"), control: async (action, id, revision) => {
    const selected = previous.current;
    if (!selected) throw "invalid-evidence";
    const result = await invoke<import("./updates").NativeUpdateResult>("desktop_update_native", { server: selected.server_id, id, revision: revision.toString(), action });
    if (previous.current !== selected) throw "invalid-evidence";
    return result;
  } }), []);
  const controls = <><p>{profile.name}</p><small>{profile.endpoint}</small><button disabled={busy} onClick={() => void connect()}>Verify saved connection</button><button onClick={() => void invoke("show_connection_manager").catch(setError)}>Show local window</button><SavedConnectionProblem error={error} /><Updates active={showConnection} controls={updateControls} /></>;
  const oauthServer = profile.server_id;
  const controlOAuth = useCallback<OAuthNativeControl>((opening, action, generation, attempt, authorization) => invoke("account_oauth_native", { server: oauthServer, opening, action, generation, attempt, authorization }), [oauthServer]);
  return <>{transport ? <OAuthNativeProvider control={controlOAuth}><WorkerNetworkControlProvider control={controlWorkerNetwork}><App localServer={controls} connectionTarget={connectionTarget ?? undefined} serverPresentation={{ kind: ServerPresentationKind.Saved, name: profile.name }} pairingAuthority={{ endpoint: profile.endpoint, serverId: profile.server_id }} transport={transport} readLocalWorker={readLocalWorker} chooseRepositoryFolder={chooseRepositoryFolder} controlLocalWorker={controlLocalWorker} currentDeviceId={profile.device_id} connectionEpoch={epoch} connectionSettings={<button onClick={() => setShowConnection(true)}>Connection controls</button>} /></WorkerNetworkControlProvider></OAuthNativeProvider> : <main className="connect-page"><h1>DeliDev</h1><p role="status">{busy ? "Connecting…" : "DeliDev could not connect."}</p>{error ? <p role="alert">DeliDev could not verify this connection. Retry or open Troubleshooting for help.</p> : null}<button disabled={busy} onClick={() => void connect()}>Retry</button><button onClick={() => setShowConnection(true)}>Troubleshooting</button></main>}
    <Modal title="Connection & diagnostics" visible={showConnection} close={() => setShowConnection(false)}><section aria-label="Saved connection"><h3>Saved connection</h3><div ref={setConnectionTarget} />{!transport ? <SavedConnectionProblem error={error} /> : null}</section></Modal>
  </>;

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
