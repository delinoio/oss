import { type FormEvent, useEffect, useMemo, useState } from "react";
import {
  type ResolveWorkspaceConnectionInput,
  type ResolvedWorkspaceConnection,
} from "./contracts/workspace-connection";
import { WorkspaceMode } from "./contracts/workspace-mode";
import {
  resolveWorkspaceConnection,
  type ResolveWorkspaceConnection,
} from "./lib/resolve-workspace-connection";
import { ConnectQueryProvider } from "./lib/connect-query-provider";
import {
  createEmptyDashboardInspectorState,
  dashboardSectionDefinitions,
  DashboardSectionId,
  type DashboardInspectorState,
  RpcDashboard,
} from "./components/rpc-dashboard";

enum AppStatus {
  Idle = "idle",
  Resolving = "resolving",
  Resolved = "resolved",
  Error = "error",
}

enum PanelMode {
  Desktop = "desktop",
  Mobile = "mobile",
}

type AppProps = {
  resolver?: ResolveWorkspaceConnection;
};

function detectPanelMode(): PanelMode {
  if (typeof window === "undefined") {
    return PanelMode.Desktop;
  }

  return window.innerWidth <= 1080 ? PanelMode.Mobile : PanelMode.Desktop;
}

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
  const [status, setStatus] = useState<AppStatus>(AppStatus.Idle);
  const [errorMessage, setErrorMessage] = useState<string | null>(null);
  const [resolvedConnection, setResolvedConnection] =
    useState<ResolvedWorkspaceConnection | null>(null);
  const [activeSection, setActiveSection] = useState<DashboardSectionId>(
    DashboardSectionId.Workspace,
  );
  const [panelMode, setPanelMode] = useState<PanelMode>(detectPanelMode());
  const [dashboardInspector, setDashboardInspector] =
    useState<DashboardInspectorState>(createEmptyDashboardInspectorState());

  const statusLabel = useMemo(() => {
    if (status === AppStatus.Resolving) {
      return "Resolving workspace endpoint...";
    }

    if (status === AppStatus.Resolved) {
      return "Connection resolved with normalized Connect RPC contract.";
    }

    if (status === AppStatus.Error) {
      return "Resolution failed. Review the error and retry.";
    }

    return "Awaiting workspace mode resolution.";
  }, [status]);

  const activeSectionDefinition = useMemo(
    () => dashboardSectionDefinitions.find((section) => section.id === activeSection),
    [activeSection],
  );

  useEffect(() => {
    const handleResize = () => setPanelMode(detectPanelMode());
    window.addEventListener("resize", handleResize);
    return () => window.removeEventListener("resize", handleResize);
  }, []);

  async function handleResolve(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();

    const input: ResolveWorkspaceConnectionInput = {
      mode,
      remoteEndpointUrl,
      remoteToken,
    };

    setStatus(AppStatus.Resolving);
    setErrorMessage(null);

    try {
      const connection = await resolver(input);
      setResolvedConnection(connection);
      setDashboardInspector(createEmptyDashboardInspectorState());
      setActiveSection(DashboardSectionId.Workspace);
      setStatus(AppStatus.Resolved);
    } catch (error) {
      const message =
        error instanceof Error ? error.message : "Unknown resolution error.";
      setErrorMessage(message);
      setResolvedConnection(null);
      setDashboardInspector(createEmptyDashboardInspectorState());
      setStatus(AppStatus.Error);
    }
  }

  const isRemoteMode = mode === WorkspaceMode.Remote;
  const isResolved = resolvedConnection !== null;

  function renderRecentIds(title: string, values: string[]) {
    return (
      <article className="inspector-block">
        <h4>{title}</h4>
        {values.length > 0 ? (
          <p className="inspector-inline-list">{values.join(", ")}</p>
        ) : (
          <p className="note">No entries yet.</p>
        )}
      </article>
    );
  }

  return (
    <main className={`app-shell panel-mode-${panelMode}`}>
      <header className="app-topbar">
        <div>
          <p className="app-eyebrow">DEXDEX DESKTOP</p>
          <h1>Codex-Style Control Surface</h1>
          <p className="note">
            LOCAL and REMOTE modes resolve to one normalized Connect RPC
            contract, then follow the same orchestration flow.
          </p>
        </div>
        <div className={`status-pill status-pill-${status}`}>
          <span>Workspace</span>
          <strong>{status.toUpperCase()}</strong>
        </div>
      </header>

      <div className="desktop-layout">
        <aside className="panel side-rail" aria-label="Workspace controls">
          <section className="side-section">
            <h2>Workspace Connection</h2>
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
                <button
                  type="submit"
                  disabled={status === AppStatus.Resolving}
                >
                  Resolve Workspace
                </button>
              </div>
            </form>
          </section>

          <section className="side-section">
            <h2>RPC Sections</h2>
            <div className="section-nav" role="tablist" aria-label="RPC Sections">
              {dashboardSectionDefinitions.map((section) => (
                <button
                  key={section.id}
                  type="button"
                  role="tab"
                  aria-selected={activeSection === section.id}
                  className={`nav-tab ${
                    activeSection === section.id ? "nav-tab-active" : ""
                  }`}
                  disabled={!isResolved}
                  onClick={() => setActiveSection(section.id)}
                >
                  <span>{section.label}</span>
                  <small>{section.description}</small>
                </button>
              ))}
            </div>
          </section>
        </aside>

        <section className="workspace-main" aria-label="Workspace dashboard">
          {resolvedConnection ? (
            <ConnectQueryProvider
              endpointUrl={resolvedConnection.endpointUrl}
              bearerToken={resolvedConnection.token}
            >
              <RpcDashboard
                connection={resolvedConnection}
                activeSection={activeSection}
                onInspectorChange={setDashboardInspector}
              />
            </ConnectQueryProvider>
          ) : (
            <section className="panel empty-state">
              <h2>Resolve Workspace to Start</h2>
              <p className="note">
                After connection resolution, RPC sections become interactive and
                section navigation is enabled.
              </p>
            </section>
          )}
        </section>

        <aside className="panel inspector-rail" aria-label="Workspace inspector">
          <h2>Inspector</h2>
          <p className="status" aria-live="polite">
            {statusLabel}
          </p>
          <p className="note">
            Active section:{" "}
            <strong>{activeSectionDefinition?.label ?? "Not selected"}</strong>
          </p>
          {errorMessage ? (
            <p className="error" role="alert">
              {errorMessage}
            </p>
          ) : null}

          <section className="inspector-block" data-testid="connection-summary">
            <h3>Resolved Connection</h3>
            {resolvedConnection ? (
              <>
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
                  Post-resolution task/session flows consume this normalized
                  contract regardless of workspace mode.
                </p>
              </>
            ) : (
              <p className="note">No active connection.</p>
            )}
          </section>

          <section className="inspector-block">
            <h3>Last Action</h3>
            <p className="inspector-line">
              <strong>{dashboardInspector.lastActionLabel}</strong>
            </p>
            <p className="note">
              {dashboardInspector.lastActionStatus.toUpperCase()} ·{" "}
              {dashboardInspector.lastActionMessage}
            </p>
          </section>

          <section className="inspector-block">
            <h3>Live Stream</h3>
            <p className="inspector-line">
              Status: <strong>{dashboardInspector.streamStatus.toUpperCase()}</strong>
            </p>
            <p className="note">
              Buffered events: {dashboardInspector.streamEventCount}
            </p>
          </section>

          {renderRecentIds(
            "Recent workspace IDs",
            dashboardInspector.history.workspaceId,
          )}
          {renderRecentIds(
            "Recent repository groups",
            dashboardInspector.history.repositoryGroupId,
          )}
          {renderRecentIds(
            "Recent task IDs",
            [
              ...dashboardInspector.history.unitTaskId,
              ...dashboardInspector.history.subTaskId,
            ].slice(0, 5),
          )}
        </aside>
      </div>
    </main>
  );
}
