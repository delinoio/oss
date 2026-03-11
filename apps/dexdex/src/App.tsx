import { type FormEvent, useEffect, useMemo, useState } from "react";
import { BrowserRouter, NavLink, useLocation, useNavigate } from "react-router-dom";
import { useQuery } from "@connectrpc/connect-query";
import { WorkspacePicker } from "./components/workspace-picker";
import {
  DexDexPageId,
  dexdexPageDefinitions,
  type DexDexPageDefinition,
} from "./contracts/dexdex-page";
import {
  createEmptySharedSelectionState,
  type SharedSelectionState,
} from "./contracts/selection-state";
import type { SavedWorkspaceProfile } from "./contracts/workspace-profile";
import {
  type ResolveWorkspaceConnectionInput,
  type ResolvedWorkspaceConnection,
  WorkspaceEndpointSource,
} from "./contracts/workspace-connection";
import { WorkspaceMode } from "./contracts/workspace-mode";
import { listUnitTasks } from "./gen/v1/dexdex-TaskService_connectquery";
import { getWorkspaceOverview } from "./gen/v1/dexdex-WorkspaceService_connectquery";
import { UnitTaskStatus } from "./gen/v1/dexdex_pb";
import { ConnectQueryProvider } from "./lib/connect-query-provider";
import {
  type DesktopLocalStoreState,
  loadDesktopLocalStoreState,
  updateDesktopLocalStoreState,
} from "./lib/desktop-local-store";
import { defaultLogger, type DexDexLogger } from "./lib/logger";
import {
  resolveWorkspaceConnection,
  type ResolveWorkspaceConnection,
} from "./lib/resolve-workspace-connection";
import {
  deleteWorkspaceProfile,
  listSavedWorkspaceProfiles,
  upsertWorkspaceProfile,
} from "./lib/workspace-profiles-store";
import {
  visualSessions,
  visualPullRequests,
  visualSubTasks,
  visualUnitTasks,
} from "./lib/visual-fixtures";
import { unitTaskDotClass } from "./components/ui/StatusDot";

import { ProjectsPage } from "./pages/projects/ProjectsPage";
import { ThreadsPage } from "./pages/threads/ThreadsPage";
import { ReviewPage } from "./pages/review/ReviewPage";
import { WorktreesPage } from "./pages/worktrees/WorktreesPage";
import { AutomationsPage } from "./pages/automations/AutomationsPage";
import { LocalEnvironmentsPage } from "./pages/local-environments/LocalEnvironmentsPage";
import { SettingsPage } from "./pages/settings/SettingsPage";
import { ActionCenter } from "./features/action-center/ActionCenter";

// ─── Enums & shared types (exported for page use) ────────────────────────────

export enum AppStatus {
  Idle = "idle",
  Resolving = "resolving",
  Resolved = "resolved",
  Error = "error",
}

export enum ActionResultStatus {
  Idle = "idle",
  Pending = "pending",
  Success = "success",
  Error = "error",
}

export type ActionCenterState = {
  label: string;
  status: ActionResultStatus;
  message: string;
};

export type UpdateLocalStore = (
  updater: (current: DesktopLocalStoreState) => DesktopLocalStoreState,
) => void;

// ─── Internal types ───────────────────────────────────────────────────────────

type AppProps = {
  resolver?: ResolveWorkspaceConnection;
  logger?: DexDexLogger;
};

type ActiveWorkspaceSession = {
  workspaceId: string;
  connection: ResolvedWorkspaceConnection;
};

// ─── Constants ────────────────────────────────────────────────────────────────

const defaultPagePath = "/threads";
const defaultRemoteEndpointUrl = "http://127.0.0.1:7878";
const defaultListPageSize = 50;
const visualWorkspaceId = "visual-workspace";

// ─── Utilities ────────────────────────────────────────────────────────────────

function resolvePageByPath(pathname: string): DexDexPageDefinition | null {
  return dexdexPageDefinitions.find((definition) => definition.path === pathname) ?? null;
}

function pagePathFromPageId(pageId: DexDexPageId): string {
  return (
    dexdexPageDefinitions.find((definition) => definition.id === pageId)?.path ?? defaultPagePath
  );
}

function enumLabel<T extends Record<string, string | number>>(enumType: T, value: number): string {
  const maybeLabel = enumType[value as unknown as keyof T];
  return typeof maybeLabel === "string" ? maybeLabel : "UNSPECIFIED";
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

function updateSelectionState(
  previous: SharedSelectionState,
  patch: Partial<SharedSelectionState>,
): SharedSelectionState {
  return { ...previous, ...patch };
}

function isVisualModeActive(search: string): boolean {
  if (typeof window === "undefined") return false;
  return new URLSearchParams(search).get("visual") === "1";
}

// ─── DesktopLayout ────────────────────────────────────────────────────────────

function DesktopLayout({
  workspaceSession,
  status,
  activePage,
  selectionState,
  actionState,
  localStoreState,
  logger,
  visualMode,
  onSwitchWorkspace,
  onSelectionChange,
  onActionStateChange,
  updateLocalStore,
}: {
  workspaceSession: ActiveWorkspaceSession;
  status: AppStatus;
  activePage: DexDexPageDefinition | null;
  selectionState: SharedSelectionState;
  actionState: ActionCenterState;
  localStoreState: DesktopLocalStoreState;
  logger: DexDexLogger;
  visualMode: boolean;
  onSwitchWorkspace: () => void;
  onSelectionChange: (patch: Partial<SharedSelectionState>) => void;
  onActionStateChange: (next: ActionCenterState) => void;
  updateLocalStore: UpdateLocalStore;
}) {
  const overviewQuery = useQuery(
    getWorkspaceOverview,
    { workspaceId: workspaceSession.workspaceId },
    { enabled: !visualMode },
  );

  const unitTasksQuery = useQuery(
    listUnitTasks,
    {
      workspaceId: workspaceSession.workspaceId,
      status: UnitTaskStatus.UNSPECIFIED,
      pageSize: defaultListPageSize,
      pageToken: "",
    },
    { enabled: !visualMode },
  );

  const sidebarUnitTasks = visualMode ? visualUnitTasks : (unitTasksQuery.data?.items ?? []);
  const overview = visualMode ? null : overviewQuery.data?.overview;

  useEffect(() => {
    if (selectionState.selectedUnitTaskId || sidebarUnitTasks.length === 0) return;
    onSelectionChange({ selectedUnitTaskId: sidebarUnitTasks[0].unitTaskId });
  }, [onSelectionChange, selectionState.selectedUnitTaskId, sidebarUnitTasks]);

  function renderPageContent() {
    if (!activePage) return <p className="empty-state">Unknown page.</p>;

    switch (activePage.id) {
      case DexDexPageId.Projects:
        return (
          <ProjectsPage
            workspaceId={workspaceSession.workspaceId}
            visualMode={visualMode}
          />
        );
      case DexDexPageId.Threads:
        return (
          <ThreadsPage
            workspaceId={workspaceSession.workspaceId}
            selection={selectionState}
            onSelectionChange={onSelectionChange}
            visualMode={visualMode}
          />
        );
      case DexDexPageId.Review:
        return (
          <ReviewPage
            workspaceId={workspaceSession.workspaceId}
            selection={selectionState}
            onSelectionChange={onSelectionChange}
            visualMode={visualMode}
          />
        );
      case DexDexPageId.Automations:
        return (
          <AutomationsPage
            localStoreState={localStoreState}
            updateLocalStore={updateLocalStore}
          />
        );
      case DexDexPageId.Worktrees:
        return (
          <WorktreesPage
            workspaceId={workspaceSession.workspaceId}
            selection={selectionState}
            onSelectionChange={onSelectionChange}
            logger={logger}
            visualMode={visualMode}
          />
        );
      case DexDexPageId.LocalEnvironments:
        return (
          <LocalEnvironmentsPage
            localStoreState={localStoreState}
            updateLocalStore={updateLocalStore}
          />
        );
      case DexDexPageId.Settings:
        return (
          <SettingsPage
            localStoreState={localStoreState}
            updateLocalStore={updateLocalStore}
          />
        );
      default:
        return <p className="empty-state">Page not found.</p>;
    }
  }

  return (
    <div className="desktop-shell">
      <header className="topbar">
        <div className="topbar-left">
          <span className="topbar-logo">DexDex</span>
          <span className="topbar-separator" />
          <span className="topbar-workspace">{workspaceSession.workspaceId}</span>
          <span className={`topbar-status topbar-status-${status}`} />
        </div>
        <div className="topbar-right">
          <button type="button" className="btn btn-ghost btn-sm" onClick={onSwitchWorkspace}>
            Switch
          </button>
        </div>
      </header>

      <div className="desktop-body">
        <aside className="sidebar" aria-label="Desktop navigation">
          <nav className="sidebar-nav">
            {dexdexPageDefinitions.map((page) => (
              <NavLink
                key={page.id}
                to={page.path}
                className={({ isActive }) =>
                  `sidebar-nav-item ${isActive ? "sidebar-nav-item-active" : ""}`
                }
              >
                {page.label}
              </NavLink>
            ))}
          </nav>

          <div className="sidebar-stats">
            {[
              { label: "Tasks",    value: overview?.totalUnitTaskCount ?? "-" },
              { label: "Sessions", value: overview?.activeSessionCount ?? "-" },
              { label: "PRs",      value: overview?.openPullRequestCount ?? "-" },
              { label: "Alerts",   value: overview?.notificationCount ?? "-" },
            ].map((stat) => (
              <div key={stat.label} className="sidebar-stat">
                <span className="sidebar-stat-label">{stat.label}</span>
                <span className="sidebar-stat-value">{stat.value}</span>
              </div>
            ))}
          </div>

          <div className="sidebar-body">
            <div className="sidebar-section">
              <div className="sidebar-section-title">Unit Tasks</div>
            </div>
            <div className="sidebar-list">
              {sidebarUnitTasks.length > 0 ? (
                <ul className="item-list">
                  {sidebarUnitTasks.map((task) => (
                    <li key={task.unitTaskId}>
                      <button
                        type="button"
                        className={`sidebar-item ${selectionState.selectedUnitTaskId === task.unitTaskId ? "sidebar-item-active" : ""}`}
                        onClick={() => onSelectionChange({ selectedUnitTaskId: task.unitTaskId })}
                      >
                        <span className={`sidebar-item-dot ${unitTaskDotClass(task.status)}`} />
                        <span className="sidebar-item-content">
                          <span className="sidebar-item-title">{task.unitTaskId}</span>
                          <span className="sidebar-item-meta">
                            {enumLabel(UnitTaskStatus, task.status)}
                          </span>
                        </span>
                      </button>
                    </li>
                  ))}
                </ul>
              ) : unitTasksQuery.isPending ? (
                <p className="text-muted text-sm">Loading tasks...</p>
              ) : (
                <p className="empty-state">No unit tasks.</p>
              )}
            </div>
          </div>

          <footer className="sidebar-footer">
            <div className="sidebar-connection">
              <span>Connection</span>
              <strong>{workspaceSession.connection.mode}</strong>
            </div>
          </footer>
        </aside>

        <main className="main-content">
          {activePage ? (
            <header className="content-header">
              <h2>{activePage.label}</h2>
            </header>
          ) : null}
          {renderPageContent()}
        </main>

        <ActionCenter
          activePage={activePage}
          workspaceId={workspaceSession.workspaceId}
          connection={workspaceSession.connection}
          selection={selectionState}
          actionState={actionState}
          onActionStateChange={onActionStateChange}
          onSelectionChange={onSelectionChange}
        />
      </div>
    </div>
  );
}

// ─── DexDexShell – connection state machine ───────────────────────────────────

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
  const [selectionState, setSelectionState] = useState<SharedSelectionState>(
    createEmptySharedSelectionState(),
  );
  const [actionState, setActionState] = useState<ActionCenterState>({
    label: "Ready",
    status: ActionResultStatus.Idle,
    message: "Select an item and run an action.",
  });
  const [localStoreState, setLocalStoreState] = useState<DesktopLocalStoreState>(() =>
    loadDesktopLocalStoreState(),
  );

  const activePage = useMemo(() => resolvePageByPath(location.pathname), [location.pathname]);
  const visualMode = isVisualModeActive(location.search);

  // Visual mode auto-connect
  useEffect(() => {
    if (!visualMode || location.pathname === "/" || activeWorkspaceSession) return;

    setActiveWorkspaceSession({
      workspaceId: visualWorkspaceId,
      connection: {
        mode: WorkspaceMode.Remote,
        endpointUrl: "http://127.0.0.1:7878/",
        endpointSource: WorkspaceEndpointSource.UserRemote,
        transport: "CONNECT_RPC",
      },
    });
    setSelectionState({
      selectedUnitTaskId: visualUnitTasks[0]?.unitTaskId ?? null,
      selectedSubTaskId: visualSubTasks[0]?.subTaskId ?? null,
      selectedSessionId: visualSessions[0]?.sessionId ?? null,
      selectedPrTrackingId: visualPullRequests[0]?.prTrackingId ?? null,
    });
    setStatus(AppStatus.Resolved);
    setActionState({
      label: "Visual Mode",
      status: ActionResultStatus.Success,
      message: "Using fixture-backed visual references.",
    });
  }, [activeWorkspaceSession, location.pathname, visualMode]);

  // Route guard
  useEffect(() => {
    if (!activeWorkspaceSession) {
      if (visualMode && location.pathname !== "/") return;
      if (location.pathname !== "/") navigate("/", { replace: true });
      return;
    }
    if (location.pathname === "/" || activePage === null) {
      navigate(pagePathFromPageId(localStoreState.settings.defaultPage), { replace: true });
    }
  }, [
    activePage,
    activeWorkspaceSession,
    localStoreState.settings.defaultPage,
    location.pathname,
    navigate,
    visualMode,
  ]);

  // Page view logging
  useEffect(() => {
    if (!activeWorkspaceSession || !activePage) return;
    logger.info("desktop.page.view", {
      page_id: activePage.id,
      result: "success",
      workspace_id: activeWorkspaceSession.workspaceId,
    });
  }, [activePage, activeWorkspaceSession, logger]);

  function updateLocalStore(updater: (current: DesktopLocalStoreState) => DesktopLocalStoreState) {
    setLocalStoreState(updateDesktopLocalStoreState(updater));
  }

  function resolveProfileInputFromForm(actionLabel: string) {
    const workspaceId = workspaceIdInput.trim();
    if (workspaceId.length === 0) {
      setErrorMessage(`${actionLabel}: workspace id is required.`);
      setPickerMessage(null);
      setStatus(AppStatus.Error);
      return null;
    }
    try {
      const normalizedRemoteEndpointUrl =
        mode === WorkspaceMode.Remote ? normalizeRemoteEndpointUrl(remoteEndpointUrl) : undefined;
      setErrorMessage(null);
      setPickerMessage(null);
      setStatus(AppStatus.Idle);
      return { workspaceId, mode, remoteEndpointUrl: normalizedRemoteEndpointUrl };
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
    const input = resolveProfileInputFromForm("Save Profile");
    if (!input) return;
    setSavedProfiles(upsertWorkspaceProfile(input));
    setPickerMessage("Profile saved.");
    setStatus(AppStatus.Idle);
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
    setSavedProfiles(deleteWorkspaceProfile(profile.workspaceId));
    setPickerMessage(`Removed profile ${profile.workspaceId}.`);
    setErrorMessage(null);
    setStatus(AppStatus.Idle);
  }

  async function handleOpenWorkspace(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const input = resolveProfileInputFromForm("Connect");
    if (!input) return;

    const resolveInput: ResolveWorkspaceConnectionInput = {
      mode: input.mode,
      remoteEndpointUrl: input.remoteEndpointUrl,
      remoteToken: input.mode === WorkspaceMode.Remote ? remoteToken : undefined,
    };

    setStatus(AppStatus.Resolving);
    setErrorMessage(null);
    setPickerMessage(null);

    try {
      const connection = await resolver(resolveInput);
      setSavedProfiles(upsertWorkspaceProfile(input));
      setActiveWorkspaceSession({ workspaceId: input.workspaceId, connection });
      setSelectionState(createEmptySharedSelectionState());
      setActionState({
        label: "Connected",
        status: ActionResultStatus.Success,
        message: "Workspace connected.",
      });
      setRemoteToken("");
      setStatus(AppStatus.Resolved);
      navigate(pagePathFromPageId(localStoreState.settings.defaultPage), { replace: true });
    } catch (error) {
      setErrorMessage(error instanceof Error ? error.message : "Unknown error.");
      setStatus(AppStatus.Error);
    }
  }

  function handleSwitchWorkspace() {
    if (!activeWorkspaceSession) return;
    setWorkspaceIdInput(activeWorkspaceSession.workspaceId);
    setMode(activeWorkspaceSession.connection.mode);
    setRemoteEndpointUrl(activeWorkspaceSession.connection.endpointUrl);
    setRemoteToken("");
    setActiveWorkspaceSession(null);
    setSelectionState(createEmptySharedSelectionState());
    setActionState({
      label: "Ready",
      status: ActionResultStatus.Idle,
      message: "Select an item and run an action.",
    });
    setStatus(AppStatus.Idle);
    setErrorMessage(null);
    setPickerMessage("Choose a workspace or connect manually.");
    navigate("/", { replace: true });
  }

  if (!activeWorkspaceSession) {
    return (
      <WorkspacePicker
        status={status}
        errorMessage={errorMessage}
        pickerMessage={pickerMessage}
        mode={mode}
        workspaceIdInput={workspaceIdInput}
        remoteEndpointUrl={remoteEndpointUrl}
        remoteToken={remoteToken}
        savedProfiles={savedProfiles}
        onModeChange={setMode}
        onWorkspaceIdChange={setWorkspaceIdInput}
        onRemoteEndpointChange={setRemoteEndpointUrl}
        onRemoteTokenChange={setRemoteToken}
        onOpenWorkspace={handleOpenWorkspace}
        onSaveProfile={handleSaveProfile}
        onEditProfile={handleEditProfile}
        onDeleteProfile={handleDeleteProfile}
      />
    );
  }

  return (
    <ConnectQueryProvider
      endpointUrl={activeWorkspaceSession.connection.endpointUrl}
      bearerToken={activeWorkspaceSession.connection.token}
    >
      <DesktopLayout
        workspaceSession={activeWorkspaceSession}
        status={status}
        activePage={activePage}
        selectionState={selectionState}
        actionState={actionState}
        localStoreState={localStoreState}
        logger={logger}
        visualMode={visualMode && activeWorkspaceSession.workspaceId === visualWorkspaceId}
        onSwitchWorkspace={handleSwitchWorkspace}
        onSelectionChange={(patch) => setSelectionState((prev) => updateSelectionState(prev, patch))}
        onActionStateChange={setActionState}
        updateLocalStore={updateLocalStore}
      />
    </ConnectQueryProvider>
  );
}

// ─── Root ─────────────────────────────────────────────────────────────────────

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
