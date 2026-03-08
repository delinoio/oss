import { Code, ConnectError, createClient, type Transport } from "@connectrpc/connect";
import { useQuery, useTransport } from "@connectrpc/connect-query";
import { createQueryOptions } from "@connectrpc/connect-query-core";
import { useQueryClient } from "@tanstack/react-query";
import { type FormEvent, useEffect, useMemo, useRef, useState } from "react";
import { BrowserRouter, useLocation, useNavigate } from "react-router-dom";
import { WorkspacePicker } from "./components/workspace-picker";
import { DesktopShellFrame } from "./components/desktop-shell-frame";
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
import {
  listRepositoryGroups,
} from "./gen/v1/dexdex-RepositoryService_connectquery";
import {
  getSessionOutput,
  listSessions,
} from "./gen/v1/dexdex-SessionService_connectquery";
import {
  getSubTask,
  listSubTasks,
  listUnitTasks,
} from "./gen/v1/dexdex-TaskService_connectquery";
import {
  getPullRequest,
  listPullRequests,
} from "./gen/v1/dexdex-PrManagementService_connectquery";
import { listReviewAssistItems } from "./gen/v1/dexdex-ReviewAssistService_connectquery";
import { listReviewComments } from "./gen/v1/dexdex-ReviewCommentService_connectquery";
import { getWorkspaceOverview } from "./gen/v1/dexdex-WorkspaceService_connectquery";
import {
  AgentCliType,
  AgentSessionStatus,
  EventStreamService,
  PlanDecision,
  PrStatus,
  SessionAdapterFixturePreset,
  SessionOutputKind,
  StreamEventType,
  SubTaskStatus,
  TaskService,
  UnitTaskStatus,
  type ListSessionsResponse,
  type ListSubTasksResponse,
  type PullRequestRecord,
  type RepositoryGroup,
  type SessionSummary,
  type StreamWorkspaceEventsResponse,
  type SubTask,
  type WorkspaceOverview,
} from "./gen/v1/dexdex_pb";
import { ConnectQueryProvider, createDexDexTransport } from "./lib/connect-query-provider";
import {
  LocalEnvironmentHealth,
  type DesktopLocalStoreState,
  loadDesktopLocalStoreState,
  updateDesktopLocalStoreState,
} from "./lib/desktop-local-store";
import { defaultLogger, type DexDexLogger } from "./lib/logger";
import {
  resolveWorkspaceConnection,
  type ResolveWorkspaceConnection,
} from "./lib/resolve-workspace-connection";
import { stringifyForUi } from "./lib/safe-json";
import {
  deleteWorkspaceProfile,
  listSavedWorkspaceProfiles,
  upsertWorkspaceProfile,
} from "./lib/workspace-profiles-store";
import {
  visualPullRequests,
  visualRepositoryGroups,
  visualReviewAssistItems,
  visualReviewComments,
  visualSessionOutputEvents,
  visualSessions,
  visualStreamEvents,
  visualSubTasks,
  visualUnitTasks,
  visualWorkspaceOverview,
} from "./lib/visual-fixtures";

enum AppStatus {
  Idle = "idle",
  Resolving = "resolving",
  Resolved = "resolved",
  Error = "error",
}

enum ActionResultStatus {
  Idle = "idle",
  Pending = "pending",
  Success = "success",
  Error = "error",
}

type AppProps = {
  resolver?: ResolveWorkspaceConnection;
  logger?: DexDexLogger;
};

type ActiveWorkspaceSession = {
  workspaceId: string;
  connection: ResolvedWorkspaceConnection;
};

type ActionCenterState = {
  label: string;
  status: ActionResultStatus;
  message: string;
};

type UpdateLocalStore = (
  updater: (current: DesktopLocalStoreState) => DesktopLocalStoreState,
) => void;

const defaultPagePath = "/threads";
const defaultRemoteEndpointUrl = "http://127.0.0.1:7878";
const defaultListPageSize = 50;
const maxStreamEvents = 120;
const visualWorkspaceId = "visual-workspace";

function describeConnectError(error: unknown, fallbackMessage: string): string {
  if (error instanceof ConnectError) {
    if (error.code === Code.NotFound) {
      return fallbackMessage;
    }
    return error.rawMessage;
  }
  if (error instanceof Error) {
    return error.message;
  }
  return fallbackMessage;
}

function resolvePageByPath(pathname: string): DexDexPageDefinition | null {
  return dexdexPageDefinitions.find((definition) => definition.path === pathname) ?? null;
}

function pagePathFromPageId(pageId: DexDexPageId): string {
  return (
    dexdexPageDefinitions.find((definition) => definition.id === pageId)?.path ??
    defaultPagePath
  );
}

function enumLabel<T extends Record<string, string | number>>(
  enumType: T,
  value: number,
): string {
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

function formatOccurredAt(rawValue: { seconds?: bigint } | undefined): string {
  if (!rawValue?.seconds) {
    return "n/a";
  }
  const milliseconds = Number(rawValue.seconds) * 1000;
  if (Number.isNaN(milliseconds) || !Number.isFinite(milliseconds)) {
    return "n/a";
  }
  return new Date(milliseconds).toLocaleString();
}

function updateSelectionState(
  previous: SharedSelectionState,
  patch: Partial<SharedSelectionState>,
): SharedSelectionState {
  return { ...previous, ...patch };
}

function unitTaskDotClass(status: number): string {
  switch (status) {
    case UnitTaskStatus.IN_PROGRESS:
      return "dot-running";
    case UnitTaskStatus.COMPLETED:
      return "dot-completed";
    case UnitTaskStatus.FAILED:
      return "dot-failed";
    case UnitTaskStatus.ACTION_REQUIRED:
      return "dot-action-required";
    case UnitTaskStatus.BLOCKED:
      return "dot-warning";
    case UnitTaskStatus.CANCELLED:
      return "dot-cancelled";
    default:
      return "dot-pending";
  }
}

function subTaskDotClass(status: number): string {
  switch (status) {
    case SubTaskStatus.IN_PROGRESS:
      return "dot-running";
    case SubTaskStatus.COMPLETED:
      return "dot-completed";
    case SubTaskStatus.FAILED:
      return "dot-failed";
    case SubTaskStatus.WAITING_FOR_PLAN_APPROVAL:
      return "dot-waiting";
    case SubTaskStatus.WAITING_FOR_USER_INPUT:
      return "dot-action-required";
    case SubTaskStatus.CANCELLED:
      return "dot-cancelled";
    default:
      return "dot-pending";
  }
}

function sessionDotClass(status: number): string {
  switch (status) {
    case AgentSessionStatus.RUNNING:
      return "dot-running";
    case AgentSessionStatus.COMPLETED:
      return "dot-completed";
    case AgentSessionStatus.FAILED:
      return "dot-failed";
    case AgentSessionStatus.WAITING_FOR_INPUT:
      return "dot-waiting";
    case AgentSessionStatus.STARTING:
      return "dot-pending";
    case AgentSessionStatus.CANCELLED:
      return "dot-cancelled";
    default:
      return "dot-default";
  }
}

function prDotClass(status: number): string {
  switch (status) {
    case PrStatus.OPEN:
      return "dot-open";
    case PrStatus.APPROVED:
      return "dot-approved";
    case PrStatus.MERGED:
      return "dot-merged";
    case PrStatus.CHANGES_REQUESTED:
      return "dot-changes-requested";
    case PrStatus.CLOSED:
      return "dot-closed";
    case PrStatus.CI_FAILED:
      return "dot-ci-failed";
    default:
      return "dot-default";
  }
}

function isVisualModeActive(search: string): boolean {
  if (typeof window === "undefined") {
    return false;
  }
  return new URLSearchParams(search).get("visual") === "1";
}

function applyStreamEventToCaches(
  event: StreamWorkspaceEventsResponse,
  workspaceId: string,
  transport: Transport,
  queryClient: ReturnType<typeof useQueryClient>,
): void {
  const listSessionsInput = {
    workspaceId,
    status: AgentSessionStatus.UNSPECIFIED,
    cliType: AgentCliType.UNSPECIFIED,
    pageSize: defaultListPageSize,
    pageToken: "",
  };
  const listSubTasksInput = {
    workspaceId,
    unitTaskId: "",
    status: SubTaskStatus.UNSPECIFIED,
    pageSize: defaultListPageSize,
    pageToken: "",
  };

  const listSessionKey = createQueryOptions(listSessions, listSessionsInput, {
    transport,
  }).queryKey;
  const listSubTaskKey = createQueryOptions(listSubTasks, listSubTasksInput, {
    transport,
  }).queryKey;

  if (event.payload.case === "sessionOutput") {
    const sessionOutput = event.payload.value;
    queryClient.setQueryData<ListSessionsResponse>(listSessionKey, (previous) => {
      if (!previous) return previous;
      const existingIndex = previous.items.findIndex(
        (item) => item.sessionId === sessionOutput.sessionId,
      );
      const existing =
        existingIndex >= 0
          ? previous.items[existingIndex]
          : ({
              sessionId: sessionOutput.sessionId,
              status: AgentSessionStatus.UNSPECIFIED,
              cliType: AgentCliType.UNSPECIFIED,
              lastOutputKind: SessionOutputKind.UNSPECIFIED,
              updatedAt: undefined,
            } as unknown as SessionSummary);
      const nextStatus = sessionOutput.isTerminal
        ? sessionOutput.kind === SessionOutputKind.ERROR
          ? AgentSessionStatus.FAILED
          : AgentSessionStatus.COMPLETED
        : existing.status;
      const updated: SessionSummary = {
        ...existing,
        sessionId: sessionOutput.sessionId,
        status: nextStatus,
        cliType: sessionOutput.source?.cliType ?? existing.cliType,
        lastOutputKind: sessionOutput.kind,
        updatedAt: event.occurredAt,
      };
      const nextItems = [...previous.items];
      if (existingIndex >= 0) {
        nextItems[existingIndex] = updated;
      } else {
        nextItems.unshift(updated);
      }
      return { ...previous, items: nextItems };
    });
  }

  if (event.payload.case === "sessionStateChanged") {
    const changed = event.payload.value;
    queryClient.setQueryData<ListSessionsResponse>(listSessionKey, (previous) => {
      if (!previous) return previous;
      const existingIndex = previous.items.findIndex(
        (item) => item.sessionId === changed.sessionId,
      );
      const existing =
        existingIndex >= 0
          ? previous.items[existingIndex]
          : ({
              sessionId: changed.sessionId,
              status: AgentSessionStatus.UNSPECIFIED,
              cliType: AgentCliType.UNSPECIFIED,
              lastOutputKind: SessionOutputKind.UNSPECIFIED,
              updatedAt: undefined,
            } as unknown as SessionSummary);
      const updated: SessionSummary = {
        ...existing,
        status: changed.status,
        updatedAt: event.occurredAt,
      };
      const nextItems = [...previous.items];
      if (existingIndex >= 0) {
        nextItems[existingIndex] = updated;
      } else {
        nextItems.unshift(updated);
      }
      return { ...previous, items: nextItems };
    });
  }

  if (event.payload.case === "subTask") {
    const updatedSubTask = event.payload.value;
    queryClient.setQueryData<ListSubTasksResponse>(listSubTaskKey, (previous) => {
      if (!previous) return previous;
      const existingIndex = previous.items.findIndex(
        (item) => item.subTaskId === updatedSubTask.subTaskId,
      );
      const nextItems = [...previous.items];
      if (existingIndex >= 0) {
        nextItems[existingIndex] = updatedSubTask;
      } else {
        nextItems.unshift(updatedSubTask);
      }
      return { ...previous, items: nextItems };
    });
  }
}

function ProjectsPage({
  workspaceId,
  visualMode,
}: {
  workspaceId: string;
  visualMode: boolean;
}) {
  const overviewQuery = useQuery(
    getWorkspaceOverview,
    { workspaceId },
    { enabled: !visualMode },
  );
  const repositoryGroupsQuery = useQuery(
    listRepositoryGroups,
    {
      workspaceId,
      pageSize: defaultListPageSize,
      pageToken: "",
    },
    { enabled: !visualMode },
  );
  const unitTasksQuery = useQuery(
    listUnitTasks,
    {
      workspaceId,
      status: UnitTaskStatus.UNSPECIFIED,
      pageSize: defaultListPageSize,
      pageToken: "",
    },
    { enabled: !visualMode },
  );

  const overview: WorkspaceOverview | undefined = visualMode
    ? visualWorkspaceOverview
    : overviewQuery.data?.overview;
  const repositoryGroups: RepositoryGroup[] = visualMode
    ? visualRepositoryGroups
    : (repositoryGroupsQuery.data?.items ?? []);
  const activeTasks = (visualMode ? visualUnitTasks : unitTasksQuery.data?.items ?? []).filter(
    (task) =>
      task.status === UnitTaskStatus.IN_PROGRESS ||
      task.status === UnitTaskStatus.ACTION_REQUIRED ||
      task.status === UnitTaskStatus.BLOCKED,
  );

  return (
    <div className="content-body">
      <div className="dashboard-grid">
        <section className="panel">
          <header className="panel-header">Workspace Overview</header>
          <div className="panel-body">
            {overview ? (
              <div className="metric-grid">
                <div className="metric-card">
                  <span className="metric-label">Total Unit Tasks</span>
                  <span className="metric-value">{overview.totalUnitTaskCount}</span>
                </div>
                <div className="metric-card">
                  <span className="metric-label">Action Required</span>
                  <span className="metric-value">{overview.actionRequiredUnitTaskCount}</span>
                </div>
                <div className="metric-card">
                  <span className="metric-label">Active Sessions</span>
                  <span className="metric-value">{overview.activeSessionCount}</span>
                </div>
                <div className="metric-card">
                  <span className="metric-label">Open PRs</span>
                  <span className="metric-value">{overview.openPullRequestCount}</span>
                </div>
              </div>
            ) : overviewQuery.isPending ? (
              <p className="text-muted text-sm">Loading workspace overview...</p>
            ) : (
              <p className="empty-state">No overview data available.</p>
            )}
          </div>
        </section>

        <section className="panel">
          <header className="panel-header">Repository Groups</header>
          <div className="panel-body">
            {repositoryGroups.length > 0 ? (
              <ul className="item-list">
                {repositoryGroups.map((group) => (
                  <li key={group.repositoryGroupId} className="panel-list-item">
                    <p className="item-row-title">{group.repositoryGroupId}</p>
                    <p className="item-row-sub">{group.repositories.length} repositories</p>
                  </li>
                ))}
              </ul>
            ) : repositoryGroupsQuery.isPending ? (
              <p className="text-muted text-sm">Loading repository groups...</p>
            ) : (
              <p className="empty-state">No repository groups found.</p>
            )}
          </div>
        </section>

        <section className="panel">
          <header className="panel-header">Active Task Summary</header>
          <div className="panel-body">
            {activeTasks.length > 0 ? (
              <ul className="item-list">
                {activeTasks.map((task) => (
                  <li key={task.unitTaskId} className="panel-list-item">
                    <div className="inline-gap">
                      <span className={`item-row-dot ${unitTaskDotClass(task.status)}`} />
                      <span className="item-row-title">{task.unitTaskId}</span>
                    </div>
                    <p className="item-row-sub">{enumLabel(UnitTaskStatus, task.status)}</p>
                  </li>
                ))}
              </ul>
            ) : (
              <p className="empty-state">No active tasks.</p>
            )}
          </div>
        </section>
      </div>
    </div>
  );
}

function ThreadsPage({
  workspaceId,
  selection,
  onSelectionChange,
  visualMode,
}: {
  workspaceId: string;
  selection: SharedSelectionState;
  onSelectionChange: (patch: Partial<SharedSelectionState>) => void;
  visualMode: boolean;
}) {
  const subTasksQuery = useQuery(
    listSubTasks,
    {
      workspaceId,
      unitTaskId: selection.selectedUnitTaskId ?? "",
      status: SubTaskStatus.UNSPECIFIED,
      pageSize: defaultListPageSize,
      pageToken: "",
    },
    {
      enabled: !visualMode && selection.selectedUnitTaskId !== null,
    },
  );

  const sessionListQuery = useQuery(
    listSessions,
    {
      workspaceId,
      status: AgentSessionStatus.UNSPECIFIED,
      cliType: AgentCliType.UNSPECIFIED,
      pageSize: defaultListPageSize,
      pageToken: "",
    },
    { enabled: !visualMode },
  );

  const selectedSubTaskQuery = useQuery(
    getSubTask,
    selection.selectedSubTaskId
      ? { workspaceId, subTaskId: selection.selectedSubTaskId }
      : undefined,
    { enabled: !visualMode && selection.selectedSubTaskId !== null },
  );

  const selectedSessionOutputQuery = useQuery(
    getSessionOutput,
    selection.selectedSessionId
      ? { workspaceId, sessionId: selection.selectedSessionId }
      : undefined,
    { enabled: !visualMode && selection.selectedSessionId !== null },
  );

  const subTasks = visualMode
    ? visualSubTasks.filter((subTask) =>
        selection.selectedUnitTaskId
          ? subTask.unitTaskId === selection.selectedUnitTaskId
          : true,
      )
    : (subTasksQuery.data?.items ?? []);
  const sessions = visualMode ? visualSessions : (sessionListQuery.data?.items ?? []);
  const selectedSubTask = visualMode
    ? visualSubTasks.find((item) => item.subTaskId === selection.selectedSubTaskId)
    : selectedSubTaskQuery.data?.subTask;
  const selectedSessionEvents = visualMode
    ? visualSessionOutputEvents.filter((event) =>
        selection.selectedSessionId ? event.sessionId === selection.selectedSessionId : true,
      )
    : selectedSessionOutputQuery.data?.events ?? [];

  useEffect(() => {
    if (selection.selectedSubTaskId || subTasks.length === 0) return;
    onSelectionChange({ selectedSubTaskId: subTasks[0].subTaskId });
  }, [onSelectionChange, selection.selectedSubTaskId, subTasks]);

  useEffect(() => {
    if (selection.selectedSessionId || sessions.length === 0) return;
    onSelectionChange({ selectedSessionId: sessions[0].sessionId });
  }, [onSelectionChange, selection.selectedSessionId, sessions]);

  return (
    <div className="content-split">
      <section className="content-list-pane">
        <div className="section-label">Inbox</div>
        {selection.selectedUnitTaskId ? null : (
          <p className="empty-state">Select a unit task in the left sidebar.</p>
        )}
        {selection.selectedUnitTaskId && subTasks.length === 0 ? (
          subTasksQuery.isPending ? (
            <p className="text-muted text-sm">Loading sub tasks...</p>
          ) : (
            <p className="empty-state">No sub tasks for this task.</p>
          )
        ) : null}
        {subTasks.length > 0 ? (
          <ul className="item-list">
            {subTasks.map((subTask) => (
              <li key={subTask.subTaskId}>
                <button
                  type="button"
                  className={`item-row ${selection.selectedSubTaskId === subTask.subTaskId ? "item-row-active" : ""}`}
                  onClick={() =>
                    onSelectionChange({
                      selectedSubTaskId: subTask.subTaskId,
                      selectedUnitTaskId: subTask.unitTaskId,
                    })
                  }
                >
                  <span className={`item-row-dot ${subTaskDotClass(subTask.status)}`} />
                  <span className="item-row-body">
                    <span className="item-row-title">{subTask.subTaskId}</span>
                    <span className="item-row-sub">{enumLabel(SubTaskStatus, subTask.status)}</span>
                  </span>
                </button>
              </li>
            ))}
          </ul>
        ) : null}
      </section>

      <section className="content-detail-pane">
        <div className="panel">
          <header className="panel-header">Thread Detail</header>
          <div className="panel-body">
            {selectedSubTask ? (
              <div className="kv-grid">
                <span className="kv-key">Sub task</span>
                <span className="kv-value">{selectedSubTask.subTaskId}</span>
                <span className="kv-key">Unit task</span>
                <span className="kv-value">{selectedSubTask.unitTaskId}</span>
                <span className="kv-key">Type</span>
                <span className="kv-value">{enumLabel(SubTaskStatus, selectedSubTask.status)}</span>
              </div>
            ) : (
              <p className="empty-state">Select a sub task to inspect details.</p>
            )}
          </div>
        </div>

        <div className="panel">
          <header className="panel-header">Timeline</header>
          <div className="panel-body">
            {sessions.length > 0 ? (
              <ul className="item-list">
                {sessions.map((session) => (
                  <li key={session.sessionId}>
                    <button
                      type="button"
                      className={`item-row ${selection.selectedSessionId === session.sessionId ? "item-row-active" : ""}`}
                      onClick={() => onSelectionChange({ selectedSessionId: session.sessionId })}
                    >
                      <span className={`item-row-dot ${sessionDotClass(session.status)}`} />
                      <span className="item-row-body">
                        <span className="item-row-title">{session.sessionId}</span>
                        <span className="item-row-sub">{enumLabel(AgentSessionStatus, session.status)}</span>
                      </span>
                    </button>
                  </li>
                ))}
              </ul>
            ) : sessionListQuery.isPending ? (
              <p className="text-muted text-sm">Loading sessions...</p>
            ) : (
              <p className="empty-state">No sessions found.</p>
            )}

            {selectedSessionEvents.length > 0 ? (
              <div className="stream-list mt-4">
                {selectedSessionEvents.map((event, index) => (
                  <article key={`${event.sessionId}-${index}`} className="stream-item">
                    <header className="stream-item-header">
                      <span>{enumLabel(SessionOutputKind, event.kind)}</span>
                      <span>{event.isTerminal ? "terminal" : "active"}</span>
                    </header>
                    <pre className="stream-item-body">{event.body}</pre>
                  </article>
                ))}
              </div>
            ) : null}
          </div>
        </div>
      </section>
    </div>
  );
}

function ReviewPage({
  workspaceId,
  selection,
  onSelectionChange,
  visualMode,
}: {
  workspaceId: string;
  selection: SharedSelectionState;
  onSelectionChange: (patch: Partial<SharedSelectionState>) => void;
  visualMode: boolean;
}) {
  const pullRequestListQuery = useQuery(
    listPullRequests,
    {
      workspaceId,
      status: PrStatus.UNSPECIFIED,
      pageSize: defaultListPageSize,
      pageToken: "",
    },
    { enabled: !visualMode },
  );

  const selectedPullRequestQuery = useQuery(
    getPullRequest,
    selection.selectedPrTrackingId
      ? { workspaceId, prTrackingId: selection.selectedPrTrackingId }
      : undefined,
    { enabled: !visualMode && selection.selectedPrTrackingId !== null },
  );

  const reviewCommentQuery = useQuery(
    listReviewComments,
    selection.selectedPrTrackingId
      ? { workspaceId, prTrackingId: selection.selectedPrTrackingId }
      : undefined,
    { enabled: !visualMode && selection.selectedPrTrackingId !== null },
  );

  const reviewAssistQuery = useQuery(
    listReviewAssistItems,
    selection.selectedUnitTaskId
      ? { workspaceId, unitTaskId: selection.selectedUnitTaskId }
      : undefined,
    { enabled: !visualMode && selection.selectedUnitTaskId !== null },
  );

  const pullRequests: PullRequestRecord[] = visualMode
    ? visualPullRequests
    : (pullRequestListQuery.data?.items ?? []);
  const selectedPullRequest = visualMode
    ? visualPullRequests.find((item) => item.prTrackingId === selection.selectedPrTrackingId)
    : selectedPullRequestQuery.data?.pullRequest;
  const reviewComments = visualMode
    ? visualReviewComments
    : reviewCommentQuery.data?.comments ?? [];
  const reviewAssistItems = visualMode
    ? visualReviewAssistItems
    : reviewAssistQuery.data?.items ?? [];

  useEffect(() => {
    if (selection.selectedPrTrackingId || pullRequests.length === 0) return;
    onSelectionChange({ selectedPrTrackingId: pullRequests[0].prTrackingId });
  }, [onSelectionChange, pullRequests, selection.selectedPrTrackingId]);

  return (
    <div className="content-split">
      <section className="content-list-pane">
        <div className="section-label">Pull Requests</div>
        {pullRequests.length > 0 ? (
          <ul className="item-list">
            {pullRequests.map((pullRequest) => (
              <li key={pullRequest.prTrackingId}>
                <button
                  type="button"
                  className={`item-row ${selection.selectedPrTrackingId === pullRequest.prTrackingId ? "item-row-active" : ""}`}
                  onClick={() => onSelectionChange({ selectedPrTrackingId: pullRequest.prTrackingId })}
                >
                  <span className={`item-row-dot ${prDotClass(pullRequest.status)}`} />
                  <span className="item-row-body">
                    <span className="item-row-title">{pullRequest.prTrackingId}</span>
                    <span className="item-row-sub">{enumLabel(PrStatus, pullRequest.status)}</span>
                  </span>
                </button>
              </li>
            ))}
          </ul>
        ) : pullRequestListQuery.isPending ? (
          <p className="text-muted text-sm">Loading pull requests...</p>
        ) : (
          <p className="empty-state">No pull requests.</p>
        )}
      </section>

      <section className="content-detail-pane">
        <div className="panel">
          <header className="panel-header">Review Context</header>
          <div className="panel-body">
            {selectedPullRequest ? (
              <div className="kv-grid">
                <span className="kv-key">PR tracking ID</span>
                <span className="kv-value">{selectedPullRequest.prTrackingId}</span>
                <span className="kv-key">Status</span>
                <span className="kv-value">{enumLabel(PrStatus, selectedPullRequest.status)}</span>
              </div>
            ) : (
              <p className="empty-state">Select a pull request to view details.</p>
            )}
          </div>
        </div>

        <div className="panel">
          <header className="panel-header">Review Assist</header>
          <div className="panel-body">
            {reviewAssistItems.length > 0 ? (
              <ul className="item-list">
                {reviewAssistItems.map((item) => (
                  <li key={item.reviewAssistId} className="panel-list-item">
                    <p className="item-row-title">{item.reviewAssistId}</p>
                    <p className="item-row-sub">{item.body}</p>
                  </li>
                ))}
              </ul>
            ) : (
              <p className="empty-state">No review assist records.</p>
            )}
          </div>
        </div>

        <div className="panel">
          <header className="panel-header">Inline Comments</header>
          <div className="panel-body">
            {reviewComments.length > 0 ? (
              <ul className="item-list">
                {reviewComments.map((comment) => (
                  <li key={comment.reviewCommentId} className="panel-list-item">
                    <p className="item-row-title">{comment.reviewCommentId}</p>
                    <p className="item-row-sub">{comment.body}</p>
                  </li>
                ))}
              </ul>
            ) : (
              <p className="empty-state">No review comments.</p>
            )}
          </div>
        </div>
      </section>
    </div>
  );
}

function WorktreesPage({
  workspaceId,
  selection,
  onSelectionChange,
  logger,
  visualMode,
}: {
  workspaceId: string;
  selection: SharedSelectionState;
  onSelectionChange: (patch: Partial<SharedSelectionState>) => void;
  logger: DexDexLogger;
  visualMode: boolean;
}) {
  const queryClient = useQueryClient();
  const transport = useTransport();
  const eventStreamClient = useMemo(
    () => createClient(EventStreamService, transport),
    [transport],
  );

  const [streamStatus, setStreamStatus] = useState<"idle" | "running" | "stopped" | "error">(
    visualMode ? "running" : "idle",
  );
  const [streamError, setStreamError] = useState<string | null>(null);
  const [streamEvents, setStreamEvents] = useState<StreamWorkspaceEventsResponse[]>(
    visualMode ? visualStreamEvents : [],
  );
  const streamAbortControllerRef = useRef<AbortController | null>(null);

  const sessionListQuery = useQuery(
    listSessions,
    {
      workspaceId,
      status: AgentSessionStatus.UNSPECIFIED,
      cliType: AgentCliType.UNSPECIFIED,
      pageSize: defaultListPageSize,
      pageToken: "",
    },
    { enabled: !visualMode },
  );

  const sessions = visualMode ? visualSessions : (sessionListQuery.data?.items ?? []);

  useEffect(() => {
    if (selection.selectedSessionId || sessions.length === 0) return;
    onSelectionChange({ selectedSessionId: sessions[0].sessionId });
  }, [onSelectionChange, selection.selectedSessionId, sessions]);

  useEffect(() => {
    return () => {
      streamAbortControllerRef.current?.abort();
      streamAbortControllerRef.current = null;
    };
  }, []);

  async function startStream() {
    if (visualMode) {
      setStreamStatus("running");
      setStreamError(null);
      setStreamEvents(visualStreamEvents);
      return;
    }

    streamAbortControllerRef.current?.abort();
    const abortController = new AbortController();
    streamAbortControllerRef.current = abortController;
    setStreamStatus("running");
    setStreamError(null);

    logger.info("stream.start", { workspace_id: workspaceId, result: "pending" });

    try {
      for await (const event of eventStreamClient.streamWorkspaceEvents(
        { workspaceId, fromSequence: 0n },
        { signal: abortController.signal },
      )) {
        if (event.sequence === 0n) continue;
        setStreamEvents((previous) => [event, ...previous].slice(0, maxStreamEvents));
        applyStreamEventToCaches(event, workspaceId, transport, queryClient);
      }
      if (!abortController.signal.aborted) {
        setStreamStatus("stopped");
      }
    } catch (error) {
      if (abortController.signal.aborted) return;
      setStreamStatus("error");
      setStreamError(describeConnectError(error, "Live stream failed."));
    } finally {
      if (streamAbortControllerRef.current === abortController) {
        streamAbortControllerRef.current = null;
      }
    }
  }

  function stopStream() {
    if (visualMode) {
      setStreamStatus("stopped");
      return;
    }
    streamAbortControllerRef.current?.abort();
    streamAbortControllerRef.current = null;
    setStreamStatus("stopped");
  }

  return (
    <div className="content-split">
      <section className="content-list-pane">
        <div className="section-label">Sessions</div>
        {sessions.length > 0 ? (
          <ul className="item-list">
            {sessions.map((session) => (
              <li key={session.sessionId}>
                <button
                  type="button"
                  className={`item-row ${selection.selectedSessionId === session.sessionId ? "item-row-active" : ""}`}
                  onClick={() => onSelectionChange({ selectedSessionId: session.sessionId })}
                >
                  <span className={`item-row-dot ${sessionDotClass(session.status)}`} />
                  <span className="item-row-body">
                    <span className="item-row-title">{session.sessionId}</span>
                    <span className="item-row-sub">{enumLabel(AgentSessionStatus, session.status)}</span>
                  </span>
                </button>
              </li>
            ))}
          </ul>
        ) : sessionListQuery.isPending ? (
          <p className="text-muted text-sm">Loading sessions...</p>
        ) : (
          <p className="empty-state">No sessions available.</p>
        )}
      </section>

      <section className="content-detail-pane">
        <div className="panel">
          <header className="panel-header">Event Timeline</header>
          <div className="panel-body">
            <div className="toolbar-row">
              <button
                type="button"
                className="btn btn-primary btn-sm"
                onClick={() => void startStream()}
                disabled={streamStatus === "running"}
              >
                {streamStatus === "running" ? "Streaming..." : "Start Stream"}
              </button>
              <button
                type="button"
                className="btn btn-secondary btn-sm"
                onClick={stopStream}
                disabled={streamStatus !== "running"}
              >
                Stop
              </button>
              <span className="status-pill">{streamStatus.toUpperCase()}</span>
            </div>

            {streamError ? <p className="error-inline">{streamError}</p> : null}

            {streamEvents.length > 0 ? (
              <div className="stream-list mt-3">
                {streamEvents.map((event) => (
                  <article key={`${event.sequence.toString()}-${event.eventType}`} className="stream-item">
                    <header className="stream-item-header">
                      <span>#{event.sequence.toString()}</span>
                      <span>{enumLabel(StreamEventType, event.eventType)}</span>
                    </header>
                    <pre className="stream-item-body">{stringifyForUi(event)}</pre>
                  </article>
                ))}
              </div>
            ) : (
              <p className="empty-state">No stream events yet.</p>
            )}
          </div>
        </div>
      </section>
    </div>
  );
}

function AutomationsPage({
  localStoreState,
  updateLocalStore,
}: {
  localStoreState: DesktopLocalStoreState;
  updateLocalStore: UpdateLocalStore;
}) {
  const [newName, setNewName] = useState("");
  const [newSchedule, setNewSchedule] = useState("Every weekday 09:00");

  function handleCreate(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const name = newName.trim();
    if (name.length === 0) return;

    updateLocalStore((current) => {
      const id = `automation-${Date.now().toString()}`;
      return {
        ...current,
        automations: [
          ...current.automations,
          { id, name, schedule: newSchedule.trim() || "Manual", enabled: true, lastRunAt: null },
        ],
        lastSelectedAutomationId: id,
      };
    });
    setNewName("");
  }

  return (
    <div className="content-body">
      <div className="dashboard-grid two-columns">
        <section className="panel">
          <header className="panel-header">Automation Queue</header>
          <div className="panel-body">
            {localStoreState.automations.length === 0 ? (
              <p className="empty-state">No automations configured.</p>
            ) : (
              <div className="stack-gap">
                {localStoreState.automations.map((automation) => (
                  <article key={automation.id} className="automation-item">
                    <div className="automation-item-header">
                      <div>
                        <p className="automation-item-name">{automation.name}</p>
                        <p className="automation-item-schedule">{automation.schedule}</p>
                      </div>
                      {!automation.enabled ? <span className="badge badge-muted">Disabled</span> : null}
                    </div>
                    <div className="automation-item-actions">
                      <button
                        type="button"
                        className="btn btn-secondary btn-sm"
                        onClick={() =>
                          updateLocalStore((current) => ({
                            ...current,
                            automations: current.automations.map((item) =>
                              item.id === automation.id
                                ? { ...item, enabled: !item.enabled }
                                : item,
                            ),
                          }))
                        }
                      >
                        {automation.enabled ? "Disable" : "Enable"}
                      </button>
                      <button
                        type="button"
                        className="btn btn-danger btn-sm"
                        onClick={() =>
                          updateLocalStore((current) => ({
                            ...current,
                            automations: current.automations.filter((item) => item.id !== automation.id),
                            lastSelectedAutomationId:
                              current.lastSelectedAutomationId === automation.id
                                ? null
                                : current.lastSelectedAutomationId,
                          }))
                        }
                      >
                        Delete
                      </button>
                    </div>
                  </article>
                ))}
              </div>
            )}
          </div>
        </section>

        <section className="panel">
          <header className="panel-header">Create Automation</header>
          <div className="panel-body">
            <form onSubmit={handleCreate} className="form-stack">
              <div className="form-group">
                <label className="form-label" htmlFor="auto-name">Name</label>
                <input
                  id="auto-name"
                  className="form-input"
                  value={newName}
                  onChange={(event) => setNewName(event.target.value)}
                  placeholder="Nightly Stream Health"
                />
              </div>
              <div className="form-group">
                <label className="form-label" htmlFor="auto-schedule">Schedule</label>
                <input
                  id="auto-schedule"
                  className="form-input"
                  value={newSchedule}
                  onChange={(event) => setNewSchedule(event.target.value)}
                  placeholder="Every weekday 09:00"
                />
              </div>
              <div className="form-actions">
                <button type="submit" className="btn btn-primary btn-sm">Create</button>
              </div>
            </form>
          </div>
        </section>
      </div>
    </div>
  );
}

function LocalEnvironmentsPage({
  localStoreState,
  updateLocalStore,
}: {
  localStoreState: DesktopLocalStoreState;
  updateLocalStore: UpdateLocalStore;
}) {
  const [envName, setEnvName] = useState("");
  const [envEndpoint, setEnvEndpoint] = useState("http://127.0.0.1:7878");

  function handleCreateEnvironment(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const name = envName.trim();
    const endpointUrl = envEndpoint.trim();
    if (name.length === 0 || endpointUrl.length === 0) return;

    updateLocalStore((current) => {
      const id = `env-${Date.now().toString()}`;
      return {
        ...current,
        localEnvironments: [
          ...current.localEnvironments,
          {
            id,
            name,
            endpointUrl,
            health: LocalEnvironmentHealth.Unknown,
            lastCheckedAt: null,
            lastErrorMessage: null,
          },
        ],
        lastSelectedEnvironmentId: id,
      };
    });
    setEnvName("");
  }

  function runDiagnostics(environmentId: string) {
    updateLocalStore((current) => ({
      ...current,
      localEnvironments: current.localEnvironments.map((environment) => {
        if (environment.id !== environmentId) {
          return environment;
        }
        const reachable =
          environment.endpointUrl.startsWith("http://") ||
          environment.endpointUrl.startsWith("https://");
        return {
          ...environment,
          health: reachable
            ? LocalEnvironmentHealth.Healthy
            : LocalEnvironmentHealth.Unreachable,
          lastCheckedAt: new Date().toISOString(),
          lastErrorMessage: reachable ? null : "endpoint must use http/https",
        };
      }),
      lastSelectedEnvironmentId: environmentId,
    }));
  }

  return (
    <div className="content-body">
      <div className="dashboard-grid two-columns">
        <section className="panel">
          <header className="panel-header">Environment List</header>
          <div className="panel-body">
            {localStoreState.localEnvironments.length === 0 ? (
              <p className="empty-state">No local environments configured.</p>
            ) : (
              <div className="stack-gap">
                {localStoreState.localEnvironments.map((environment) => (
                  <article key={environment.id} className="env-item">
                    <p className="env-item-name">{environment.name}</p>
                    <p className="env-item-meta">{environment.endpointUrl}</p>
                    <p className="env-item-meta">
                      Health: {environment.health} · Last checked: {environment.lastCheckedAt ? new Date(environment.lastCheckedAt).toLocaleString() : "never"}
                    </p>
                    <div className="env-item-actions">
                      <button
                        type="button"
                        className="btn btn-secondary btn-sm"
                        onClick={() => runDiagnostics(environment.id)}
                      >
                        Diagnostics
                      </button>
                      <button
                        type="button"
                        className="btn btn-danger btn-sm"
                        onClick={() =>
                          updateLocalStore((current) => ({
                            ...current,
                            localEnvironments: current.localEnvironments.filter(
                              (item) => item.id !== environment.id,
                            ),
                            lastSelectedEnvironmentId:
                              current.lastSelectedEnvironmentId === environment.id
                                ? null
                                : current.lastSelectedEnvironmentId,
                          }))
                        }
                      >
                        Remove
                      </button>
                    </div>
                  </article>
                ))}
              </div>
            )}
          </div>
        </section>

        <section className="panel">
          <header className="panel-header">Add Environment</header>
          <div className="panel-body">
            <form onSubmit={handleCreateEnvironment} className="form-stack">
              <div className="form-group">
                <label className="form-label" htmlFor="env-name">Name</label>
                <input
                  id="env-name"
                  className="form-input"
                  value={envName}
                  onChange={(event) => setEnvName(event.target.value)}
                  placeholder="Staging Cluster"
                />
              </div>
              <div className="form-group">
                <label className="form-label" htmlFor="env-endpoint">Endpoint URL</label>
                <input
                  id="env-endpoint"
                  className="form-input"
                  value={envEndpoint}
                  onChange={(event) => setEnvEndpoint(event.target.value)}
                  placeholder="https://dexdex.example/rpc"
                />
              </div>
              <div className="form-actions">
                <button type="submit" className="btn btn-primary btn-sm">Add</button>
              </div>
            </form>
          </div>
        </section>
      </div>
    </div>
  );
}

function SettingsPage({
  localStoreState,
  updateLocalStore,
}: {
  localStoreState: DesktopLocalStoreState;
  updateLocalStore: UpdateLocalStore;
}) {
  return (
    <div className="content-body">
      <section className="panel">
        <header className="panel-header">Preferences</header>
        <div className="panel-body">
          <div className="settings-row">
            <div>
              <p className="settings-row-label">Default Page</p>
              <p className="settings-row-description">Which page opens after connecting to a workspace.</p>
            </div>
            <select
              className="form-select settings-select"
              value={localStoreState.settings.defaultPage}
              onChange={(event) =>
                updateLocalStore((current) => ({
                  ...current,
                  settings: {
                    ...current.settings,
                    defaultPage: event.target.value as DexDexPageId,
                  },
                }))
              }
            >
              {dexdexPageDefinitions.map((page) => (
                <option key={page.id} value={page.id}>{page.label}</option>
              ))}
            </select>
          </div>

          <div className="settings-row">
            <div>
              <p className="settings-row-label">Compact Mode</p>
              <p className="settings-row-description">Reduce spacing and typography scale.</p>
            </div>
            <input
              type="checkbox"
              checked={localStoreState.settings.compactMode}
              onChange={(event) =>
                updateLocalStore((current) => ({
                  ...current,
                  settings: {
                    ...current.settings,
                    compactMode: event.target.checked,
                  },
                }))
              }
              className="settings-checkbox"
            />
          </div>

          <div className="settings-row">
            <div>
              <p className="settings-row-label">Auto Start Stream</p>
              <p className="settings-row-description">Start live stream automatically on Worktrees page.</p>
            </div>
            <input
              type="checkbox"
              checked={localStoreState.settings.autoStartStream}
              onChange={(event) =>
                updateLocalStore((current) => ({
                  ...current,
                  settings: {
                    ...current.settings,
                    autoStartStream: event.target.checked,
                  },
                }))
              }
              className="settings-checkbox"
            />
          </div>
        </div>
      </section>
    </div>
  );
}

function ActionCenter({
  activePage,
  workspaceId,
  connection,
  selection,
  actionState,
  onActionStateChange,
  onSelectionChange,
}: {
  activePage: DexDexPageDefinition | null;
  workspaceId: string;
  connection: ResolvedWorkspaceConnection;
  selection: SharedSelectionState;
  actionState: ActionCenterState;
  onActionStateChange: (next: ActionCenterState) => void;
  onSelectionChange: (patch: Partial<SharedSelectionState>) => void;
}) {
  const queryClient = useQueryClient();
  const transport = useMemo(
    () => createDexDexTransport(connection.endpointUrl, connection.token),
    [connection.endpointUrl, connection.token],
  );
  const taskClient = useMemo(() => createClient(TaskService, transport), [transport]);

  const [planDecision, setPlanDecision] = useState<PlanDecision>(PlanDecision.APPROVE);
  const [planRevisionNote, setPlanRevisionNote] = useState("");
  const [runCliType, setRunCliType] = useState<AgentCliType>(AgentCliType.CODEX_CLI);
  const [runFixturePreset, setRunFixturePreset] = useState<SessionAdapterFixturePreset>(
    SessionAdapterFixturePreset.CODEX_CLI_FAILURE,
  );
  const [runRawJsonlInput, setRunRawJsonlInput] = useState('{"type":"text","part":{"text":"hello"}}');
  const [runInputMode, setRunInputMode] = useState<"preset" | "raw">("preset");

  const isThreadActionPage = activePage?.id === DexDexPageId.Threads;

  async function handleSubmitPlanDecision(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!selection.selectedSubTaskId) {
      onActionStateChange({
        label: "Plan Decision",
        status: ActionResultStatus.Error,
        message: "Select a sub task first.",
      });
      return;
    }
    if (planDecision === PlanDecision.REVISE && planRevisionNote.trim().length === 0) {
      onActionStateChange({
        label: "Plan Decision",
        status: ActionResultStatus.Error,
        message: "Revision note required.",
      });
      return;
    }

    onActionStateChange({
      label: "Plan Decision",
      status: ActionResultStatus.Pending,
      message: "Submitting...",
    });

    try {
      const response = await taskClient.submitPlanDecision({
        workspaceId,
        subTaskId: selection.selectedSubTaskId,
        decision: planDecision,
        revisionNote: planDecision === PlanDecision.REVISE ? planRevisionNote : "",
      });
      onSelectionChange({
        selectedSubTaskId:
          response.createdSubTask?.subTaskId ??
          response.updatedSubTask?.subTaskId ??
          selection.selectedSubTaskId,
        selectedUnitTaskId: response.updatedSubTask?.unitTaskId ?? selection.selectedUnitTaskId,
      });
      await queryClient.invalidateQueries();
      onActionStateChange({
        label: "Plan Decision",
        status: ActionResultStatus.Success,
        message: "Decision submitted.",
      });
    } catch (error) {
      onActionStateChange({
        label: "Plan Decision",
        status: ActionResultStatus.Error,
        message: describeConnectError(error, "Failed."),
      });
    }
  }

  async function handleRunSessionAdapter(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!selection.selectedUnitTaskId || !selection.selectedSubTaskId || !selection.selectedSessionId) {
      onActionStateChange({
        label: "Session Adapter",
        status: ActionResultStatus.Error,
        message: "Select unit task, sub task, and session.",
      });
      return;
    }
    if (runInputMode === "raw" && runRawJsonlInput.trim().length === 0) {
      onActionStateChange({
        label: "Session Adapter",
        status: ActionResultStatus.Error,
        message: "Raw JSONL required.",
      });
      return;
    }

    onActionStateChange({
      label: "Session Adapter",
      status: ActionResultStatus.Pending,
      message: "Running...",
    });

    try {
      await taskClient.runSubTaskSessionAdapter({
        workspaceId,
        unitTaskId: selection.selectedUnitTaskId,
        subTaskId: selection.selectedSubTaskId,
        sessionId: selection.selectedSessionId,
        cliType: runCliType,
        input:
          runInputMode === "preset"
            ? { case: "fixturePreset", value: runFixturePreset }
            : { case: "rawJsonl", value: runRawJsonlInput },
      });
      await queryClient.invalidateQueries();
      onActionStateChange({
        label: "Session Adapter",
        status: ActionResultStatus.Success,
        message: "Completed.",
      });
    } catch (error) {
      onActionStateChange({
        label: "Session Adapter",
        status: ActionResultStatus.Error,
        message: describeConnectError(error, "Failed."),
      });
    }
  }

  return (
    <aside className="right-panel" aria-label="Action center">
      <section className="right-panel-section">
        <h3 className="right-panel-title">Status</h3>
        <div className="inline-gap">
          <span
            className={`topbar-status topbar-status-${
              actionState.status === ActionResultStatus.Success
                ? "resolved"
                : actionState.status === ActionResultStatus.Error
                  ? "error"
                  : actionState.status === ActionResultStatus.Pending
                    ? "resolving"
                    : "idle"
            }`}
          />
          <span>{actionState.label}</span>
        </div>
        <p className="text-muted text-sm mt-2">{actionState.message}</p>
      </section>

      <section className="right-panel-section">
        <h3 className="right-panel-title">Selection</h3>
        <div className="kv-grid">
          <span className="kv-key">Unit task</span>
          <span className="kv-value">{selection.selectedUnitTaskId ?? "—"}</span>
          <span className="kv-key">Sub task</span>
          <span className="kv-value">{selection.selectedSubTaskId ?? "—"}</span>
          <span className="kv-key">Session</span>
          <span className="kv-value">{selection.selectedSessionId ?? "—"}</span>
          <span className="kv-key">PR</span>
          <span className="kv-value">{selection.selectedPrTrackingId ?? "—"}</span>
        </div>
      </section>

      {isThreadActionPage ? (
        <section className="right-panel-section">
          <h3 className="right-panel-title">Plan Decision</h3>
          <form onSubmit={handleSubmitPlanDecision} className="form-stack">
            <div className="form-group">
              <label className="form-label" htmlFor="rp-plan-decision">Decision</label>
              <select
                id="rp-plan-decision"
                className="form-select"
                value={planDecision}
                onChange={(event) => setPlanDecision(Number(event.target.value) as PlanDecision)}
              >
                <option value={PlanDecision.APPROVE}>APPROVE</option>
                <option value={PlanDecision.REVISE}>REVISE</option>
                <option value={PlanDecision.REJECT}>REJECT</option>
              </select>
            </div>
            {planDecision === PlanDecision.REVISE ? (
              <div className="form-group">
                <label className="form-label" htmlFor="rp-revision-note">Revision Note</label>
                <textarea
                  id="rp-revision-note"
                  className="form-textarea"
                  value={planRevisionNote}
                  onChange={(event) => setPlanRevisionNote(event.target.value)}
                  rows={3}
                />
              </div>
            ) : null}
            <div className="form-actions">
              <button type="submit" className="btn btn-primary btn-sm">Submit</button>
            </div>
          </form>
        </section>
      ) : null}

      {isThreadActionPage ? (
        <section className="right-panel-section">
          <h3 className="right-panel-title">Session Adapter</h3>
          <form onSubmit={handleRunSessionAdapter} className="form-stack">
            <div className="form-group">
              <label className="form-label" htmlFor="rp-cli-type">CLI Type</label>
              <select
                id="rp-cli-type"
                className="form-select"
                value={runCliType}
                onChange={(event) => setRunCliType(Number(event.target.value) as AgentCliType)}
              >
                <option value={AgentCliType.CODEX_CLI}>CODEX_CLI</option>
                <option value={AgentCliType.CLAUDE_CODE}>CLAUDE_CODE</option>
                <option value={AgentCliType.OPENCODE}>OPENCODE</option>
              </select>
            </div>
            <div className="form-group">
              <label className="form-label" htmlFor="rp-input-mode">Input Mode</label>
              <select
                id="rp-input-mode"
                className="form-select"
                value={runInputMode}
                onChange={(event) => setRunInputMode(event.target.value as "preset" | "raw")}
              >
                <option value="preset">Preset Fixture</option>
                <option value="raw">Raw JSONL</option>
              </select>
            </div>
            {runInputMode === "preset" ? (
              <div className="form-group">
                <label className="form-label" htmlFor="rp-fixture">Fixture</label>
                <select
                  id="rp-fixture"
                  className="form-select"
                  value={runFixturePreset}
                  onChange={(event) =>
                    setRunFixturePreset(Number(event.target.value) as SessionAdapterFixturePreset)
                  }
                >
                  <option value={SessionAdapterFixturePreset.CODEX_CLI_FAILURE}>CODEX_CLI_FAILURE</option>
                  <option value={SessionAdapterFixturePreset.CLAUDE_CODE_STREAM}>CLAUDE_CODE_STREAM</option>
                  <option value={SessionAdapterFixturePreset.OPENCODE_RUN}>OPENCODE_RUN</option>
                </select>
              </div>
            ) : (
              <div className="form-group">
                <label className="form-label" htmlFor="rp-raw-jsonl">Raw JSONL</label>
                <textarea
                  id="rp-raw-jsonl"
                  className="form-textarea"
                  value={runRawJsonlInput}
                  onChange={(event) => setRunRawJsonlInput(event.target.value)}
                  rows={4}
                />
              </div>
            )}
            <div className="form-actions">
              <button type="submit" className="btn btn-primary btn-sm">Run</button>
            </div>
          </form>
        </section>
      ) : null}

      <section className="right-panel-section">
        <h3 className="right-panel-title">Connection</h3>
        <div className="kv-grid">
          <span className="kv-key">Workspace</span>
          <span className="kv-value">{workspaceId}</span>
          <span className="kv-key">Mode</span>
          <span className="kv-value">{connection.mode}</span>
          <span className="kv-key">Endpoint</span>
          <span className="kv-value">{connection.endpointUrl}</span>
          <span className="kv-key">Source</span>
          <span className="kv-value">{connection.endpointSource}</span>
        </div>
      </section>
    </aside>
  );
}

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
    {
      workspaceId: workspaceSession.workspaceId,
    },
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
  const overview = visualMode ? visualWorkspaceOverview : overviewQuery.data?.overview;

  useEffect(() => {
    if (selectionState.selectedUnitTaskId || sidebarUnitTasks.length === 0) return;
    onSelectionChange({ selectedUnitTaskId: sidebarUnitTasks[0].unitTaskId });
  }, [onSelectionChange, selectionState.selectedUnitTaskId, sidebarUnitTasks]);

  function renderPageContent() {
    if (!activePage) {
      return <p className="empty-state">Unknown page.</p>;
    }

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
    <DesktopShellFrame
      workspaceId={workspaceSession.workspaceId}
      status={status}
      pages={dexdexPageDefinitions}
      activePage={activePage}
      onSwitchWorkspace={onSwitchWorkspace}
      sidebarStats={[
        {
          label: "Tasks",
          value: overview?.totalUnitTaskCount ?? "-",
        },
        {
          label: "Sessions",
          value: overview?.activeSessionCount ?? "-",
        },
        {
          label: "PRs",
          value: overview?.openPullRequestCount ?? "-",
        },
        {
          label: "Alerts",
          value: overview?.notificationCount ?? "-",
        },
      ]}
      sidebarBody={
        <>
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
                        <span className="sidebar-item-meta">{enumLabel(UnitTaskStatus, task.status)}</span>
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
        </>
      }
      connectionMode={workspaceSession.connection.mode}
      mainContent={renderPageContent()}
      rightPanel={
        <ActionCenter
          activePage={activePage}
          workspaceId={workspaceSession.workspaceId}
          connection={workspaceSession.connection}
          selection={selectionState}
          actionState={actionState}
          onActionStateChange={onActionStateChange}
          onSelectionChange={onSelectionChange}
        />
      }
    />
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
  const [activeWorkspaceSession, setActiveWorkspaceSession] = useState<ActiveWorkspaceSession | null>(
    null,
  );
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

  useEffect(() => {
    if (!visualMode || location.pathname === "/" || activeWorkspaceSession) {
      return;
    }

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

  useEffect(() => {
    if (!activeWorkspaceSession) {
      if (visualMode && location.pathname !== "/") {
        return;
      }
      if (location.pathname !== "/") {
        navigate("/", { replace: true });
      }
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
