import { useEffect, useRef, useState } from "react";
import { invoke, isTauri } from "@tauri-apps/api/core";
import { type Transport } from "@connectrpc/connect";
import { createDeliDevTransport } from "@delinoio/delidev-api-client";
import { App } from "./App";
import { Problem } from "./ui";
import { LocalServerControls, LocalServerState, LocalServerStatusText, type LocalServerStatus } from "./local-server";
import type { ControlLocalWorker, LocalWorkerStatus } from "./local-worker-controls";
import { verifyLocalServer } from "./connection";

interface NativeConnection { endpoint: string; token: string; server_id: string; device_id: string }
const nativeProblems: Record<string, string> = {
  stopped: "The server was explicitly stopped. Start it only when you intend to resume its local lifecycle.",
  busy: "A local connection attempt is already running. Wait for it to finish.",
  "sidecar-missing": "The bundled DeliDev executable is missing. Repair the desktop installation.",
  "sidecar-failed": "The local server could not connect. Inspect DeliDev server status and its private log, then retry.",
  "timed-out": "Local startup has not completed. Check server status before retrying; accepted work may continue.",
  incompatible: "The running server uses a different version or listener. Preserve its sessions and use a compatible client or explicitly stop it before changing the server.",
  "credential-unavailable": "This desktop credential is unavailable or revoked. Inspect the original device registration; it will not be replaced automatically.",
  "permission-denied": "The local connection is not authorized. Check the selected device and private-directory permissions.",
  "invalid-evidence": "The retained local connection requires inspection. Preserve its original pairing and server data.",
  "storage-unavailable": "The private DeliDev configuration directory is unavailable. Check this computer's user configuration.",
};
export function Desktop() {
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
  const connect = async () => {
    if (busy) return;
    setBusy(true); setError(undefined);
    try {
      const connection = await invoke<NativeConnection>("connect_local");
      const candidate = createDeliDevTransport({ origin: connection.endpoint, getToken: () => connection.token });
      await verifyLocalServer(candidate, connection.server_id);
      const old = previous.current;
      if (!old || old.server_id !== connection.server_id || old.device_id !== connection.device_id || old.endpoint !== connection.endpoint || old.token !== connection.token) setTransport(candidate);
      previous.current = connection;
      setConnectionEpoch((epoch) => epoch + 1);
    } catch (reason) { setError(reason); } finally { setBusy(false); }
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
  const problem = typeof error === "string" && Object.hasOwn(nativeProblems, error) ? <p role="alert">{nativeProblems[error]}</p> : <Problem error={error} />;
  if (transport) return <App controlLocalWorker={controlLocalWorker} readLocalWorker={readLocalWorker} transport={transport} connectionReady={status?.state === LocalServerState.Ready} connectionEpoch={connectionEpoch} localServer={<LocalServerControls status={status} restart={() => void connect()} busy={busy} problem={problem} />} />;
  return <main className="connect-page"><h1>DeliDev</h1><h2>Connect to your local server</h2><p>The server and its sessions continue when you close DeliDev.</p>{isTauri() ? <button className="primary" disabled={busy} onClick={() => void connect()}>{busy ? "Connecting…" : "Start or connect"}</button> : <p>Open the DeliDev desktop app to connect. Browser clients are not supported.</p>}{isTauri() ? <LocalServerStatusText status={status} /> : null}{problem}</main>;
}
