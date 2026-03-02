import { type FormEvent, useMemo, useState } from "react";
import {
  type ResolveWorkspaceConnectionInput,
  type ResolvedWorkspaceConnection,
} from "./contracts/workspace-connection";
import { WorkspaceMode } from "./contracts/workspace-mode";
import {
  resolveWorkspaceConnection,
  type ResolveWorkspaceConnection,
} from "./lib/resolve-workspace-connection";

type AppStatus = "idle" | "resolving" | "resolved" | "error";

type AppProps = {
  resolver?: ResolveWorkspaceConnection;
};

function modeOptions(): Array<{ value: WorkspaceMode; label: string }> {
  return [
    { value: WorkspaceMode.Local, label: "LOCAL" },
    { value: WorkspaceMode.Remote, label: "REMOTE" },
  ];
}

export function App({ resolver = resolveWorkspaceConnection }: AppProps) {
  const [mode, setMode] = useState<WorkspaceMode>(WorkspaceMode.Local);
  const [remoteEndpointUrl, setRemoteEndpointUrl] = useState(
    "http://127.0.0.1:7878",
  );
  const [remoteToken, setRemoteToken] = useState("");
  const [status, setStatus] = useState<AppStatus>("idle");
  const [errorMessage, setErrorMessage] = useState<string | null>(null);
  const [resolvedConnection, setResolvedConnection] =
    useState<ResolvedWorkspaceConnection | null>(null);

  const statusLabel = useMemo(() => {
    if (status === "resolving") {
      return "Resolving workspace endpoint...";
    }

    if (status === "resolved") {
      return "Connection resolved with normalized Connect RPC contract.";
    }

    if (status === "error") {
      return "Resolution failed. Review the error and retry.";
    }

    return "Awaiting workspace mode resolution.";
  }, [status]);

  async function handleResolve(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();

    const input: ResolveWorkspaceConnectionInput = {
      mode,
      remoteEndpointUrl,
      remoteToken,
    };

    setStatus("resolving");
    setErrorMessage(null);

    try {
      const connection = await resolver(input);
      setResolvedConnection(connection);
      setStatus("resolved");
    } catch (error) {
      const message =
        error instanceof Error ? error.message : "Unknown resolution error.";
      setErrorMessage(message);
      setResolvedConnection(null);
      setStatus("error");
    }
  }

  const isRemoteMode = mode === WorkspaceMode.Remote;

  return (
    <main className="app-shell">
      <header className="app-header">
        <h1>DexDex Workspace Connector</h1>
        <p>
          LOCAL and REMOTE modes both resolve to one shared Connect RPC
          connection contract and then follow the same downstream workflow.
        </p>
      </header>

      <section className="panel" aria-label="Workspace mode selection">
        <form onSubmit={handleResolve}>
          <div className="field">
            <label htmlFor="workspace-mode">Workspace Mode</label>
            <select
              id="workspace-mode"
              name="workspace-mode"
              value={mode}
              onChange={(event) =>
                setMode(event.target.value as WorkspaceMode)
              }
            >
              {modeOptions().map((option) => (
                <option key={option.value} value={option.value}>
                  {option.label}
                </option>
              ))}
            </select>
          </div>

          <div className="field">
            <label htmlFor="remote-endpoint-url">Remote Endpoint URL</label>
            <input
              id="remote-endpoint-url"
              name="remote-endpoint-url"
              type="url"
              value={remoteEndpointUrl}
              disabled={!isRemoteMode}
              onChange={(event) => setRemoteEndpointUrl(event.target.value)}
              placeholder="https://dexdex.example/rpc"
            />
          </div>

          <div className="field">
            <label htmlFor="remote-token">Remote Token (optional)</label>
            <input
              id="remote-token"
              name="remote-token"
              type="password"
              value={remoteToken}
              disabled={!isRemoteMode}
              onChange={(event) => setRemoteToken(event.target.value)}
            />
          </div>

          <div className="actions">
            <button type="submit" disabled={status === "resolving"}>
              Resolve Workspace
            </button>
            <span className="status" aria-live="polite">
              {statusLabel}
            </span>
          </div>

          {errorMessage ? (
            <p className="error" role="alert">
              {errorMessage}
            </p>
          ) : null}
        </form>
      </section>

      {resolvedConnection ? (
        <section className="panel" data-testid="connection-summary">
          <h2>Resolved Connection</h2>
          <dl className="summary-grid">
            <dt>Mode</dt>
            <dd>{resolvedConnection.mode}</dd>
            <dt>Endpoint URL</dt>
            <dd>{resolvedConnection.endpointUrl}</dd>
            <dt>Endpoint Source</dt>
            <dd>{resolvedConnection.endpointSource}</dd>
            <dt>Transport</dt>
            <dd>{resolvedConnection.transport}</dd>
            <dt>Token</dt>
            <dd>{resolvedConnection.token ? "present" : "absent"}</dd>
          </dl>
          <p className="note">
            Post-resolution task/session flows consume this normalized contract
            regardless of workspace mode.
          </p>
        </section>
      ) : null}
    </main>
  );
}
