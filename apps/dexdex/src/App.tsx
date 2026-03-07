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
import type { SavedWorkspaceProfile } from "./contracts/workspace-profile";
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
import {
  deleteWorkspaceProfile,
  listSavedWorkspaceProfiles,
  upsertWorkspaceProfile,
} from "./lib/workspace-profiles-store";

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

type ActiveWorkspaceSession = {
  workspaceId: string;
  connection: ResolvedWorkspaceConnection;
};

type PlaceholderItem = {
  title: string;
  description: string;
  status: "Planned" | "In progress";
};

const defaultPagePath = "/projects";
const defaultRemoteEndpointUrl = "http://127.0.0.1:7878";

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

function normalizeRemoteEndpointUrl(remoteEndpointUrl: string): string {
  const trimmedUrl = remoteEndpointUrl.trim();
  if (trimmedUrl.length === 0) {
    throw new Error("remoteEndpointUrl must not be empty.");
  }

  const parsedUrl = new URL(trimmedUrl);
  if (parsedUrl.protocol !== "http:" && parsedUrl.protocol !== "https:") {
    throw new Error("remoteEndpointUrl must use http or https scheme.");
  }

  return parsedUrl.toString();
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
  const [workspaceIdInput, setWorkspaceIdInput] = useState("");
  const [remoteEndpointUrl, setRemoteEndpointUrl] = useState(defaultRemoteEndpointUrl);
  const [remoteToken, setRemoteToken] = useState("");
  const [status, setStatus] = useState<AppStatus>(AppStatus.Idle);
  const [errorMessage, setErrorMessage] = useState<string | null>(null);
  const [pickerMessage, setPickerMessage] = useState<string | null>(null);
  const [activeWorkspaceSession, setActiveWorkspaceSession] =
    useState<ActiveWorkspaceSession | null>(null);
  const [savedProfiles, setSavedProfiles] = useState<SavedWorkspaceProfile[]>(() =>
    listSavedWorkspaceProfiles(),
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

    return "Select a workspace to enter the desktop surface.";
  }, [status]);

  useEffect(() => {
    const handleResize = () => setPanelMode(detectPanelMode());
    window.addEventListener("resize", handleResize);
    return () => window.removeEventListener("resize", handleResize);
  }, []);

  useEffect(() => {
    if (!activeWorkspaceSession) {
      if (location.pathname !== "/") {
        navigate("/", { replace: true });
      }
      return;
    }

    if (location.pathname === "/" || activePage === null) {
      navigate(defaultPagePath, { replace: true });
    }
  }, [activePage, activeWorkspaceSession, location.pathname, navigate]);

  useEffect(() => {
    if (!activeWorkspaceSession || !activePage) {
      return;
    }

    logger.info("desktop.page.view", {
      page_id: activePage.id,
      action: "page-view",
      result: "success",
      workspace_id: activeWorkspaceSession.workspaceId,
    });
  }, [activePage, activeWorkspaceSession, logger]);

  function resolveProfileInputFromForm(actionLabel: string): {
    workspaceId: string;
    mode: WorkspaceMode;
    remoteEndpointUrl?: string;
  } | null {
    const workspaceId = workspaceIdInput.trim();
    if (workspaceId.length === 0) {
      const message = `${actionLabel}: workspace id is required.`;
      setErrorMessage(message);
      setPickerMessage(null);
      setStatus(AppStatus.Error);
      return null;
    }

    try {
      const nextRemoteEndpointUrl =
        mode === WorkspaceMode.Remote
          ? normalizeRemoteEndpointUrl(remoteEndpointUrl)
          : undefined;

      setErrorMessage(null);
      setPickerMessage(null);
      setStatus(AppStatus.Idle);

      return {
        workspaceId,
        mode,
        remoteEndpointUrl: nextRemoteEndpointUrl,
      };
    } catch (error) {
      const message =
        error instanceof Error ? error.message : `${actionLabel}: invalid remote endpoint.`;
      setErrorMessage(`${actionLabel}: ${message}`);
      setPickerMessage(null);
      setStatus(AppStatus.Error);
      return null;
    }
  }

  function handleSaveProfile() {
    const profileInput = resolveProfileInputFromForm("Save Profile");
    if (!profileInput) {
      return;
    }

    const profiles = upsertWorkspaceProfile(profileInput);
    setSavedProfiles(profiles);
    setPickerMessage("Workspace profile saved.");
    setStatus(AppStatus.Idle);
    logger.info("desktop.workspace.profile.save", {
      action: "save-profile",
      result: "success",
      workspace_id: profileInput.workspaceId,
      mode: profileInput.mode,
    });
  }

  function handleEditProfile(profile: SavedWorkspaceProfile) {
    setWorkspaceIdInput(profile.workspaceId);
    setMode(profile.mode);
    setRemoteEndpointUrl(profile.remoteEndpointUrl ?? defaultRemoteEndpointUrl);
    setRemoteToken("");
    setErrorMessage(null);
    setPickerMessage(`Loaded profile ${profile.workspaceId}.`);
    setStatus(AppStatus.Idle);
  }

  function handleDeleteProfile(profile: SavedWorkspaceProfile) {
    const profiles = deleteWorkspaceProfile(profile.workspaceId);
    setSavedProfiles(profiles);
    setPickerMessage(`Deleted profile ${profile.workspaceId}.`);
    setErrorMessage(null);
    setStatus(AppStatus.Idle);

    logger.info("desktop.workspace.profile.delete", {
      action: "delete-profile",
      result: "success",
      workspace_id: profile.workspaceId,
      mode: profile.mode,
    });
  }

  async function handleOpenWorkspace(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();

    const profileInput = resolveProfileInputFromForm("Open Workspace");
    if (!profileInput) {
      return;
    }

    const resolveInput: ResolveWorkspaceConnectionInput = {
      mode: profileInput.mode,
      remoteEndpointUrl: profileInput.remoteEndpointUrl,
      remoteToken:
        profileInput.mode === WorkspaceMode.Remote ? remoteToken : undefined,
    };

    logger.info("desktop.workspace.open", {
      action: "open-workspace",
      result: "pending",
      workspace_id: profileInput.workspaceId,
      mode: profileInput.mode,
    });

    setStatus(AppStatus.Resolving);
    setErrorMessage(null);
    setPickerMessage(null);

    try {
      const connection = await resolver(resolveInput);
      const saved = upsertWorkspaceProfile(profileInput);

      setSavedProfiles(saved);
      setActiveWorkspaceSession({
        workspaceId: profileInput.workspaceId,
        connection,
      });
      setRemoteToken("");
      setDashboardInspector(createEmptyDashboardInspectorState());
      setStatus(AppStatus.Resolved);
      setPickerMessage(null);
      navigate(defaultPagePath, { replace: true });

      logger.info("desktop.workspace.open", {
        action: "open-workspace",
        result: "success",
        workspace_id: profileInput.workspaceId,
        mode: profileInput.mode,
      });
    } catch (error) {
      const message =
        error instanceof Error ? error.message : "Unknown resolution error.";
      setErrorMessage(message);
      setStatus(AppStatus.Error);
      logger.error("desktop.workspace.open", {
        action: "open-workspace",
        result: "error",
        workspace_id: profileInput.workspaceId,
        mode: profileInput.mode,
      });
    }
  }

  function handleSwitchWorkspace() {
    if (!activeWorkspaceSession) {
      return;
    }

    setWorkspaceIdInput(activeWorkspaceSession.workspaceId);
    setMode(activeWorkspaceSession.connection.mode);
    setRemoteEndpointUrl(activeWorkspaceSession.connection.endpointUrl);
    setRemoteToken("");
    setActiveWorkspaceSession(null);
    setDashboardInspector(createEmptyDashboardInspectorState());
    setStatus(AppStatus.Idle);
    setErrorMessage(null);
    setPickerMessage("Choose a workspace profile or open one manually.");
    navigate("/", { replace: true });

    logger.info("desktop.workspace.switch", {
      action: "switch-workspace",
      result: "success",
      workspace_id: activeWorkspaceSession.workspaceId,
    });
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

  function renderWorkspacePicker() {
    const isRemoteMode = mode === WorkspaceMode.Remote;

    return (
      <main className="app-shell workspace-picker-shell">
        <header className="app-topbar">
          <div>
            <p className="app-eyebrow">DEXDEX DESKTOP</p>
            <h1>Workspace Picker</h1>
            <p className="note">
              Select a workspace first, then continue with the Codex-style desktop UI.
            </p>
          </div>
          <div className={`status-pill status-pill-${status}`}>
            <span>Workspace</span>
            <strong>{status.toUpperCase()}</strong>
          </div>
        </header>

        <section className="panel page-panel" aria-label="Workspace picker">
          <header className="page-header">
            <h2>Open Workspace</h2>
            <p className="note">Recent profiles are stored locally without token persistence.</p>
          </header>

          <div className="workspace-picker-grid">
            <article className="query-card">
              <h3>Recent Profiles</h3>
              {savedProfiles.length > 0 ? (
                <ul className="workspace-profile-list">
                  {savedProfiles.map((profile) => (
                    <li key={profile.workspaceId} className="workspace-profile-item">
                      <div>
                        <p className="workspace-profile-title">{profile.workspaceId}</p>
                        <p className="note">
                          Mode: <strong>{profile.mode}</strong>
                        </p>
                        <p className="note">
                          Endpoint: <strong>{profile.remoteEndpointUrl ?? "managed-local"}</strong>
                        </p>
                        <p className="note">Last used: {profile.lastUsedAt}</p>
                      </div>
                      <div className="actions workspace-profile-actions">
                        <button
                          type="button"
                          className="secondary-button"
                          onClick={() => handleEditProfile(profile)}
                        >
                          Edit
                        </button>
                        <button
                          type="button"
                          className="secondary-button"
                          onClick={() => handleDeleteProfile(profile)}
                        >
                          Delete
                        </button>
                      </div>
                    </li>
                  ))}
                </ul>
              ) : (
                <p className="query-status">No saved workspace profiles yet.</p>
              )}
            </article>

            <article className="query-card">
              <h3>Workspace Form</h3>
              <form onSubmit={handleOpenWorkspace}>
                <div className="field">
                  <label htmlFor="workspace-id">Workspace ID</label>
                  <input
                    id="workspace-id"
                    name="workspace-id"
                    value={workspaceIdInput}
                    onChange={(event) => setWorkspaceIdInput(event.target.value)}
                    placeholder="workspace-1"
                  />
                </div>

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
                  <label htmlFor="remote-token">Remote Token (optional, not persisted)</label>
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
                    {status === AppStatus.Resolving ? "Opening..." : "Open Workspace"}
                  </button>
                  <button
                    type="button"
                    className="secondary-button"
                    onClick={handleSaveProfile}
                  >
                    Save Profile
                  </button>
                </div>
              </form>
            </article>
          </div>

          {pickerMessage ? <p className="note picker-feedback">{pickerMessage}</p> : null}
          {errorMessage ? (
            <p className="error" role="alert">
              {errorMessage}
            </p>
          ) : null}
        </section>
      </main>
    );
  }

  function renderWorkspaceSurface() {
    if (!activePage || !activeWorkspaceSession) {
      return null;
    }

    if (isRpcDashboardPage(activePage.id)) {
      return (
        <ConnectQueryProvider
          endpointUrl={activeWorkspaceSession.connection.endpointUrl}
          bearerToken={activeWorkspaceSession.connection.token}
        >
          <RpcDashboard
            connection={activeWorkspaceSession.connection}
            workspaceId={activeWorkspaceSession.workspaceId}
            activePage={activePage.id}
            onInspectorChange={setDashboardInspector}
            logger={logger}
          />
        </ConnectQueryProvider>
      );
    }

    if (activePage.id === DexDexPageId.LocalEnvironments) {
      return (
        <section className="panel page-panel" aria-label="Local environments page">
          <header className="page-header">
            <h2>Local Environments</h2>
            <p className="note">
              Active workspace session is resolved. Use switch to return to the startup picker.
            </p>
          </header>

          <section className="inspector-block" data-testid="connection-summary">
            <h3>Resolved Connection</h3>
            <dl className="summary-grid">
              <dt>Workspace ID</dt>
              <dd>{activeWorkspaceSession.workspaceId}</dd>
              <dt>Mode</dt>
              <dd>{activeWorkspaceSession.connection.mode}</dd>
              <dt>Endpoint URL</dt>
              <dd>{activeWorkspaceSession.connection.endpointUrl}</dd>
              <dt>Endpoint Source</dt>
              <dd>{activeWorkspaceSession.connection.endpointSource}</dd>
              <dt>Transport</dt>
              <dd>{activeWorkspaceSession.connection.transport}</dd>
              <dt>Token</dt>
              <dd>{activeWorkspaceSession.connection.token ? "present" : "absent"}</dd>
            </dl>
          </section>

          <div className="actions">
            <button type="button" className="secondary-button" onClick={handleSwitchWorkspace}>
              Switch Workspace
            </button>
          </div>
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

  if (!activeWorkspaceSession) {
    return renderWorkspacePicker();
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
          <p className="note">
            Workspace ID: <strong>{activeWorkspaceSession.workspaceId}</strong>
          </p>
        </div>
        <div className="app-topbar-actions">
          <button type="button" className="secondary-button" onClick={handleSwitchWorkspace}>
            Switch Workspace
          </button>
          <div className={`status-pill status-pill-${status}`}>
            <span>Workspace</span>
            <strong>{status.toUpperCase()}</strong>
          </div>
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
                      workspace_id: activeWorkspaceSession.workspaceId,
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
              Mode: <strong>{activeWorkspaceSession.connection.mode}</strong>
            </p>
            <p className="note">
              Endpoint: <strong>{activeWorkspaceSession.connection.endpointUrl}</strong>
            </p>
            <p className="note">
              Workspace: <strong>{activeWorkspaceSession.workspaceId}</strong>
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
            <dl className="summary-grid">
              <dt>Workspace ID</dt>
              <dd>{activeWorkspaceSession.workspaceId}</dd>
              <dt>Mode</dt>
              <dd>{activeWorkspaceSession.connection.mode}</dd>
              <dt>Endpoint URL</dt>
              <dd>{activeWorkspaceSession.connection.endpointUrl}</dd>
              <dt>Endpoint Source</dt>
              <dd>{activeWorkspaceSession.connection.endpointSource}</dd>
              <dt>Transport</dt>
              <dd>{activeWorkspaceSession.connection.transport}</dd>
              <dt>Token</dt>
              <dd>{activeWorkspaceSession.connection.token ? "present" : "absent"}</dd>
            </dl>
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
