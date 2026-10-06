// SPDX-License-Identifier: Apache-2.0
import { createRoot } from "react-dom/client";
import { useEffect, useState } from "react";
import { createDeliDevTransport } from "@delinoio/delidev-api-client";
import { App } from "../App";
import { AppearanceProvider } from "../appearance";
import { BrowserHostProvider } from "../host-capabilities";
import { LocalServerControls, LocalServerState, LocalServerStatusText, type LocalServerStatus } from "../local-server";
import { type ControlLocalWorker } from "../local-worker-controls";
import { type LocalWorkerProof } from "../local-worker";
import { ServerPresentationKind } from "../server-presentation";
import { verifyLocalServer } from "../connection";
import { WorkerNetworkControlProvider, type ControlWorkerNetwork } from "../worker-network-native";
import { appearanceBridge, hostRequest } from "./bridge";
import "../themes.css";
import "../styles.css";
import "../settings-presentation.css";
import "./qa.css";

interface Bootstrap { version: number; index: number; endpoint: string; serverId: string; deviceId: string; token: string }
const controlWorker: ControlLocalWorker = (action, generation) => hostRequest("worker", { action, ...(generation ? { generation } : {}) });
const readWorker = () => hostRequest<LocalWorkerProof>("proof", {});
const controlNetwork: ControlWorkerNetwork = (machine, action, ciphertext, digest) => hostRequest("network", { machine, action, ciphertext: btoa(Array.from(ciphertext, byte => String.fromCharCode(byte)).join("")), digest });

function QaApp({ authority }: { authority: Bootstrap }) {
  const [transport] = useState(() => createDeliDevTransport({ origin: authority.endpoint, getToken: () => authority.token }));
  const [appearance] = useState(() => appearanceBridge(authority.serverId));
  const [status, setStatus] = useState<LocalServerStatus>(), [busy, setBusy] = useState(false), [problem, setProblem] = useState(false);
  const [verified, setVerified] = useState(false), [connectionTarget, setConnectionTarget] = useState<HTMLDivElement | null>(null);
  useEffect(() => {
    let disposed = false; let timer: ReturnType<typeof setTimeout>;
    const refresh = async () => {
      try {
        const result = await hostRequest<LocalServerStatus>("status");
        if (result.state === LocalServerState.Ready) { await verifyLocalServer(transport, authority.serverId); if (!disposed) setVerified(true); }
        if (!disposed) setStatus(result);
      }
      catch { if (!disposed) setStatus({ state: LocalServerState.Blocked, attempts: 0, retry_ms: 0 }); }
      if (!disposed) timer = setTimeout(() => void refresh(), 2000);
    };
    void refresh(); return () => { disposed = true; clearTimeout(timer); };
  }, [transport, authority.serverId]);
  const start = async () => {
    setBusy(true); setProblem(false);
    try { setStatus(await hostRequest<LocalServerStatus>("start", {})); await verifyLocalServer(transport, authority.serverId); setVerified(true); }
    catch { setProblem(true); } finally { setBusy(false); }
  };
  const localServer = <LocalServerControls status={status} restart={() => void start()} busy={busy} problem={problem ? <p role="alert">The original QA connection could not be verified.</p> : undefined} />;
  return <AppearanceProvider bridge={appearance}><BrowserHostProvider available={false}><WorkerNetworkControlProvider control={controlNetwork}>
    <aside className="qa-notice" aria-label="Browser QA environment"><strong>DeliDev browser QA · environment {authority.index}</strong><details><summary>Native validation limits</summary><p>File dialogs, tray, OS notifications, trusted-window OAuth callbacks, CEF browser profiles, saved desktop windows and desktop installation are unavailable here. Use the desktop app for their native acceptance. Repository paths are inspected by this environment’s real Runner Device.</p></details><div ref={setConnectionTarget} /></aside>
    {verified ? <App transport={transport} pairingAuthority={authority} currentDeviceId={authority.deviceId} serverPresentation={{ kind: ServerPresentationKind.Local }} readLocalWorker={readWorker} controlLocalWorker={controlWorker} localServer={localServer} connectionTarget={connectionTarget ?? undefined} connectionSettings={<p>QA server lifecycle controls remain in the environment banner.</p>} connectionReady={status?.state === LocalServerState.Ready} /> : <main><h1>DeliDev browser QA unavailable</h1><LocalServerStatusText status={status} /><p>The original paired connection must be authenticated before product screens open.</p>{status && status.state !== LocalServerState.Ready ? <button disabled={busy} onClick={() => void start()}>Start local server</button> : null}{problem ? <p role="alert">The original QA connection could not be verified.</p> : null}</main>}
  </WorkerNetworkControlProvider></BrowserHostProvider></AppearanceProvider>;
}

const root = createRoot(document.getElementById("root")!);
void (async () => {
  const authority = await hostRequest<Bootstrap>("bootstrap");
  const id = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
  const endpoint = new URL(authority.endpoint);
  if (authority.version !== 1 || !Number.isInteger(authority.index) || authority.index < 1 || authority.index > 8 || !id.test(authority.serverId) || !id.test(authority.deviceId) || endpoint.hostname !== "127.0.0.1" || endpoint.protocol !== "http:" || endpoint.origin !== authority.endpoint) throw new Error("QA authority invalid");
  root.render(<QaApp authority={authority} />);
})().catch(() => root.render(<main><h1>DeliDev browser QA unavailable</h1><p>Check the original runner’s environment list and refresh after authenticated readiness is restored.</p></main>));
