import { useState } from "react";
import { createRoot } from "react-dom/client";
import { invoke, isTauri } from "@tauri-apps/api/core";
import { createClient, type Transport } from "@connectrpc/connect";
import { createDeliDevTransport, SystemService } from "@delinoio/delidev-api-client";
import { App } from "./App";
import { Problem } from "./ui";
import "./styles.css";

interface NativeConnection { endpoint: string; token: string; server_id: string; device_id: string }
const nativeProblems: Record<string, string> = {
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
function Desktop() {
  const [transport, setTransport] = useState<Transport>();
  const [error, setError] = useState<unknown>();
  const [busy, setBusy] = useState(false);
  const connect = async () => {
    if (busy) return;
    setBusy(true); setError(undefined);
    try {
      const connection = await invoke<NativeConnection>("connect_local");
      const candidate = createDeliDevTransport({ origin: connection.endpoint, getToken: () => connection.token });
      const status = await createClient(SystemService, candidate).getStatus({}, { timeoutMs: 10000 });
      if (status.serverId !== connection.server_id || status.protocolVersion !== 1 || status.version !== "0.1.0") throw "incompatible";
      setTransport(candidate);
    } catch (reason) { setError(reason); } finally { setBusy(false); }
  };
  if (transport) return <App transport={transport} />;
  return <main className="connect-page"><h1>DeliDev</h1><h2>Connect to your local server</h2><p>The server and its sessions continue when you close DeliDev.</p>{isTauri() ? <button className="primary" disabled={busy} onClick={() => void connect()}>{busy ? "Connecting…" : "Start or connect"}</button> : <p>Open the DeliDev desktop app to connect. Browser clients are not supported.</p>}{typeof error === "string" && Object.hasOwn(nativeProblems, error) ? <p role="alert">{nativeProblems[error]}</p> : <Problem error={error} />}</main>;
}
createRoot(document.getElementById("root")!).render(<Desktop />);
