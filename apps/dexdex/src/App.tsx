import { type FormEvent, useEffect, useMemo, useState } from "react";
import {
  BrowserRouter,
  NavLink,
  useLocation,
  useNavigate,
} from "react-router-dom";
import {
  DexDexPageId,
  dexdexPageDefinitions,
  type DexDexPageDefinition,
} from "./contracts/dexdex-page";
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
  type DashboardInspectorState,
  RpcDashboard,
  type RpcDashboardPageId,
} from "./components/rpc-dashboard";
import { defaultLogger, type DexDexLogger } from "./lib/logger";

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
  logger?: DexDexLogger;
};

type PlaceholderItem = {
  title: string;
  description: string;
  status: "Planned" | "In progress";
};

const defaultPagePath = "/projects";

const automationsItems: ReadonlyArray<PlaceholderItem> = [
  {
    title: "Daily review digest",
    description: "Summarize unresolved review items and prepare handoff context.",
    status: "Planned",
  },
  {
    title: "Nightly stream health snapshot",
    description: "Collect stream lag and heartbeat status for the active workspace.",
    status: "In progress",
  },
  {
    title: "Session adapter replay check",
    description: "Run fixture replay checks and report normalization drift.",
    status: "Planned",
  },
];

const settingsItems: ReadonlyArray<PlaceholderItem> = [
  {
    title: "Sandbox policy profile",
    description: "Review command permissions and desktop execution boundaries.",
    status: "Planned",
  },
  {
    title: "Workspace default endpoint",
    description: "Store preferred endpoint selection for startup routing.",
    status: "In progress",
  },
  {
    title: "Notification routing",
    description: "Select channels for review, task, and stream alerts.",
    status: "Planned",
  },
];

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

function resolvePageByPath(pathname: string): DexDexPageDefinition | null {
  return dexdexPageDefinitions.find((definition) => definition.path === pathname) ?? null;
}

function isRpcDashboardPage(pageId: DexDexPageId): pageId is RpcDashboardPageId {
  return (
    pageId === DexDexPageId.Projects ||
    pageId === DexDexPageId.Threads ||
    pageId === DexDexPageId.Review ||
    pageId === DexDexPageId.Worktrees
  );
}

function PlaceholderSurface({
  title,
  description,
  items,
}: {
  title: string;
  description: string;
  items: ReadonlyArray<PlaceholderItem>;
}) {
  return (
    <section className="panel page-panel" aria-label={`${title} page`}>
      <header className="page-header">
        <h2>{title}</h2>
        <p className="note">{description}</p>
      </header>
      <div className="placeholder-grid">
        <article className="query-card">
          <h3>Queue</h3>
          <p className="note">
            Skeleton page aligned with Codex desktop role expectations. RPC wiring will be
            connected in later increments.
          </p>
          <ul className="placeholder-list">
            {items.map((item) => (
              <li key={item.title}>
                <strong>{item.title}</strong>
                <p>{item.description}</p>
                <small>{item.status}</small>
              </li>
            ))}
          </ul>
        </article>

        <article className="query-card">
          <h3>Detail</h3>
          <p className="note">
            Select an item from the queue to inspect schedules, ownership, and execution history.
          </p>
          <p className="query-status">No item selected in this scaffold state.</p>
        </article>

        <article className="query-card">
          <h3>Actions</h3>
          <p className="note">
            Action panel intentionally disabled while the service contract is under design.
          </p>
          <div className="actions">
            <button type="button" className="secondary-button" disabled>
              Run Action
            </button>
            <button type="button" className="secondary-button" disabled>
              Save Draft
            </button>
          </div>
        </article>
      </div>
    </section>
  );
}

function DexDexShell({
  resolver,
  logger,
}: {
  resolver: ResolveWorkspaceConnection;
  logger: DexDexLogger;
}) {
  const location = useLocation();
  const navigate = useNavigate();

  const [mode, setMode] = useState<WorkspaceMode>(WorkspaceMode.Local);
  const [remoteEndpointUrl, setRemoteEndpointUrl] = useState("http://127.0.0.1:7878");
  const [remoteToken, setRemoteToken] = useState("");
  const [status, setStatus] = useState<AppStatus>(AppStatus.Idle);
  const [errorMessage, setErrorMessage] = useState<string | null>(null);
  const [resolvedConnection, setResolvedConnection] = useState<ResolvedWorkspaceConnection | null>(
    null,
  );
  const [panelMode, setPanelMode] = useState<PanelMode>(detectPanelMode());
  const [dashboardInspector, setDashboardInspector] =
    useState<DashboardInspectorState>(createEmptyDashboardInspectorState());

  const activePage = useMemo(
    () => resolvePageByPath(location.pathname),
    [location.pathname],
  );

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

  useEffect(() => {
    const handleResize = () => setPanelMode(detectPanelMode());
    window.addEventListener("resize", handleResize);
    return () => window.removeEventListener("resize", handleResize);
  }, []);

  useEffect(() => {
    if (location.pathname === "/" || activePage === null) {
      navigate(defaultPagePath, { replace: true });
    }
  }, [activePage, location.pathname, navigate]);

  useEffect(() => {
    if (!activePage) {
      return;
    }

    logger.info("desktop.page.view", {
      page_id: activePage.id,
      action: "page-view",
      result: "success",
    });
  }, [activePage, logger]);

  async function handleResolve(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();

    const input: ResolveWorkspaceConnectionInput = {
      mode,
      remoteEndpointUrl,
      remoteToken,
    };

    logger.info("desktop.workspace.resolve", {
      page_id: DexDexPageId.LocalEnvironments,
      action: "resolve-workspace",
      result: "pending",
    });

    setStatus(AppStatus.Resolving);
    setErrorMessage(null);

    try {
      const connection = await resolver(input);
      setResolvedConnection(connection);
      setDashboardInspector(createEmptyDashboardInspectorState());
      setStatus(AppStatus.Resolved);
      logger.info("desktop.workspace.resolve", {
        page_id: DexDexPageId.LocalEnvironments,
        action: "resolve-workspace",
        result: "success",
      });
    } catch (error) {
      const message =
        error instanceof Error ? error.message : "Unknown resolution error.";
      setErrorMessage(message);
      setResolvedConnection(null);
      setDashboardInspector(createEmptyDashboardInspectorState());
      setStatus(AppStatus.Error);
      logger.error("desktop.workspace.resolve", {
        page_id: DexDexPageId.LocalEnvironments,
        action: "resolve-workspace",
        result: "error",
      });
    }
  }

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

  function renderRpcPage(pageId: RpcDashboardPageId) {
    if (!resolvedConnection) {
      return (
        <section className="panel page-panel empty-state" aria-label="Resolve workspace">
          <h2>Resolve Workspace to Open {activePage?.label}</h2>
          <p className="note">
            This page is contract-ready. Complete workspace resolution in Local Environments
            before running RPC workflows.
          </p>
          <div className="actions">
            <button
              type="button"
              className="secondary-button"
              onClick={() => navigate("/local-environments")}
            >
              Open Local Environments
            </button>
          </div>
        </section>
      );
    }

    return (
      <ConnectQueryProvider
        endpointUrl={resolvedConnection.endpointUrl}
        bearerToken={resolvedConnection.token}
      >
        <RpcDashboard
          connection={resolvedConnection}
          activePage={pageId}
          onInspectorChange={setDashboardInspector}
          logger={logger}
        />
      </ConnectQueryProvider>
    );
  }

  function renderWorkspaceSurface() {
    if (!activePage) {
      return null;
    }

    if (isRpcDashboardPage(activePage.id)) {
      return renderRpcPage(activePage.id);
    }

    if (activePage.id === DexDexPageId.LocalEnvironments) {
      const isRemoteMode = mode === WorkspaceMode.Remote;

      return (
        <section className="panel page-panel" aria-label="Local environments page">
          <header className="page-header">
            <h2>Local Environments</h2>
            <p className="note">
              Resolve LOCAL or REMOTE mode into one normalized Connect RPC contract.
            </p>
          </header>

          <form onSubmit={handleResolve}>
            <div className="field">
              <label htmlFor="workspace-mode">Workspace Mode</label>
              <select
                id="workspace-mode"
                name="workspace-mode"
                value={mode}
                onChange={(event) => setMode(event.target.value as WorkspaceMode)}
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
              <button type="submit" disabled={status === AppStatus.Resolving}>
                Resolve Workspace
              </button>
            </div>
          </form>

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
                  Post-resolution task/session flows consume this normalized contract
                  regardless of workspace mode.
                </p>
              </>
            ) : (
              <p className="note">No active connection.</p>
            )}
          </section>
          {errorMessage ? (
            <p className="error" role="alert">
              {errorMessage}
            </p>
          ) : null}
        </section>
      );
    }

    if (activePage.id === DexDexPageId.Automations) {
      return (
        <PlaceholderSurface
          title="Automations"
          description="Codex-style scheduled workflow control surface (skeleton)."
          items={automationsItems}
        />
      );
    }

    return (
      <PlaceholderSurface
        title="Settings"
        description="Codex-style desktop preferences and policy controls (skeleton)."
        items={settingsItems}
      />
    );
  }

  return (
    <main className={`app-shell panel-mode-${panelMode}`}>
      <header className="app-topbar">
        <div>
          <p className="app-eyebrow">DEXDEX DESKTOP</p>
          <h1>Codex-Role Multi-Page Control Surface</h1>
          <p className="note">
            Active page: <strong>{activePage?.label ?? "Redirecting"}</strong>
          </p>
        </div>
        <div className={`status-pill status-pill-${status}`}>
          <span>Workspace</span>
          <strong>{status.toUpperCase()}</strong>
        </div>
      </header>

      <div className="desktop-layout">
        <aside className="panel side-rail" aria-label="DexDex navigation">
          <section className="side-section">
            <h2>Pages</h2>
            <nav className="section-nav" aria-label="DexDex page navigation">
              {dexdexPageDefinitions.map((page) => (
                <NavLink
                  key={page.id}
                  to={page.path}
                  className={({ isActive }) =>
                    `nav-tab nav-link ${isActive ? "nav-tab-active" : ""}`
                  }
                  onClick={() => {
                    logger.info("desktop.page.navigate", {
                      page_id: page.id,
                      action: "navigate",
                      result: "pending",
                    });
                  }}
                >
                  <span>{page.label}</span>
                  <small>{page.description}</small>
                </NavLink>
              ))}
            </nav>
          </section>

          <section className="side-section">
            <h2>Connection Snapshot</h2>
            <p className="note">{statusLabel}</p>
            <p className="note">
              Mode: <strong>{resolvedConnection?.mode ?? "UNRESOLVED"}</strong>
            </p>
            <p className="note">
              Endpoint: <strong>{resolvedConnection?.endpointUrl ?? "N/A"}</strong>
            </p>
          </section>
        </aside>

        <section className="workspace-main" aria-label="Workspace dashboard">
          {renderWorkspaceSurface()}
        </section>

        <aside className="panel inspector-rail" aria-label="Workspace inspector">
          <h2>Inspector</h2>
          <p className="status" aria-live="polite">
            {statusLabel}
          </p>
          <p className="note">
            Active page: <strong>{activePage?.label ?? "Redirecting"}</strong>
          </p>

          <section className="inspector-block" data-testid="global-connection-summary">
            <h3>Global Connection</h3>
            {resolvedConnection ? (
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
              {dashboardInspector.lastActionStatus.toUpperCase()} · {" "}
              {dashboardInspector.lastActionMessage}
            </p>
          </section>

          <section className="inspector-block">
            <h3>Live Stream</h3>
            <p className="inspector-line">
              Status: <strong>{dashboardInspector.streamStatus.toUpperCase()}</strong>
            </p>
            <p className="note">Buffered events: {dashboardInspector.streamEventCount}</p>
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

export function App({
  resolver = resolveWorkspaceConnection,
  logger = defaultLogger,
}: AppProps) {
  return (
    <BrowserRouter>
      <DexDexShell resolver={resolver} logger={logger} />
    </BrowserRouter>
  );
}
