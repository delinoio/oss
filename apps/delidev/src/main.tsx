import { useState } from "react";
import { createRoot } from "react-dom/client";
import { invoke, isTauri } from "@tauri-apps/api/core";
import { createClient, type Transport } from "@connectrpc/connect";
import { createDeliDevTransport, SystemService } from "@delinoio/delidev-api-client";
import { App } from "./App";
import { Problem } from "./ui";
import "./styles.css";

interface NativeConnection { endpoint: string; token: string; server_id: string; device_id: string }
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
      if (status.serverId !== connection.server_id || status.protocolVersion !== 1 || status.version !== "0.1.0") throw new Error("Incompatible selected server.");
      setTransport(candidate);
    } catch (reason) { setError(reason); } finally { setBusy(false); }
  };
  if (transport) return <App transport={transport} />;
  return <main className="connect-page"><h1>DeliDev</h1><h2>Connect to your local server</h2><p>The server and its sessions continue when you close DeliDev.</p>{isTauri() ? <button className="primary" disabled={busy} onClick={() => void connect()}>{busy ? "Connecting…" : "Start or connect"}</button> : <p>Open the DeliDev desktop app to connect. Browser clients are not supported.</p>}<Problem error={error} /></main>;
}
createRoot(document.getElementById("root")!).render(<Desktop />);
