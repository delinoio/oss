import { Code, ConnectError, createClient, type Transport } from "@connectrpc/connect";
import { useQuery, useTransport } from "@connectrpc/connect-query";
import { createQueryOptions } from "@connectrpc/connect-query-core";
import { useQueryClient } from "@tanstack/react-query";
import { type FormEvent, useEffect, useMemo, useRef, useState } from "react";
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
  createEmptySharedSelectionState,
  type SharedSelectionState,
} from "./contracts/selection-state";
import type { SavedWorkspaceProfile } from "./contracts/workspace-profile";
import {
  type ResolveWorkspaceConnectionInput,
  type ResolvedWorkspaceConnection,
} from "./contracts/workspace-connection";
import { WorkspaceMode } from "./contracts/workspace-mode";
import {
  getWorkspaceOverview,
} from "./gen/v1/dexdex-WorkspaceService_connectquery";
import {
  getRepositoryGroup,
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
  type SessionSummary,
  type StreamWorkspaceEventsResponse,
  type SubTask,
} from "./gen/v1/dexdex_pb";
import { ConnectQueryProvider, createDexDexTransport } from "./lib/connect-query-provider";
import {
  defaultLogger,
  type DexDexLogger,
} from "./lib/logger";
import {
  LocalEnvironmentHealth,
  type DesktopLocalStoreState,
  loadDesktopLocalStoreState,
  saveDesktopLocalStoreState,
  updateDesktopLocalStoreState,
} from "./lib/desktop-local-store";
import {
  deleteWorkspaceProfile,
  listSavedWorkspaceProfiles,
  upsertWorkspaceProfile,
} from "./lib/workspace-profiles-store";
import {
  resolveWorkspaceConnection,
  type ResolveWorkspaceConnection,
} from "./lib/resolve-workspace-connection";
import { stringifyForUi } from "./lib/safe-json";

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

const defaultPagePath = "/projects";
const defaultRemoteEndpointUrl = "http://127.0.0.1:7878";
const defaultListPageSize = 50;
const maxStreamEvents = 120;

function detectPanelMode(): PanelMode {
  if (typeof window === "undefined") {
    return PanelMode.Desktop;
  }
  return window.innerWidth <= 1080 ? PanelMode.Mobile : PanelMode.Desktop;
}

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

function modeOptions(): Array<{ value: WorkspaceMode; label: string }> {
  return [
    { value: WorkspaceMode.Local, label: "LOCAL" },
    { value: WorkspaceMode.Remote, label: "REMOTE" },
  ];
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
  return {
    ...previous,
    ...patch,
  };
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
      if (!previous) {
        return previous;
      }

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
            } satisfies SessionSummary);

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

      return {
        ...previous,
        items: nextItems,
      };
    });
  }

  if (event.payload.case === "sessionStateChanged") {
    const changed = event.payload.value;
    queryClient.setQueryData<ListSessionsResponse>(listSessionKey, (previous) => {
      if (!previous) {
        return previous;
      }

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
            } satisfies SessionSummary);

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

      return {
        ...previous,
        items: nextItems,
      };
    });
  }

  if (event.payload.case === "subTask") {
    const updatedSubTask = event.payload.value;
    queryClient.setQueryData<ListSubTasksResponse>(listSubTaskKey, (previous) => {
      if (!previous) {
        return previous;
      }

      const existingIndex = previous.items.findIndex(
        (item) => item.subTaskId === updatedSubTask.subTaskId,
      );
      const nextItems = [...previous.items];
      if (existingIndex >= 0) {
        nextItems[existingIndex] = updatedSubTask;
      } else {
        nextItems.unshift(updatedSubTask);
      }

      return {
        ...previous,
        items: nextItems,
      };
    });
  }
}

function ProjectsPage({
  workspaceId,
  selection,
  onSelectionChange,
}: {
  workspaceId: string;
  selection: SharedSelectionState;
  onSelectionChange: (patch: Partial<SharedSelectionState>) => void;
}) {
  const overviewQuery = useQuery(getWorkspaceOverview, { workspaceId });
  const repositoryGroupsQuery = useQuery(listRepositoryGroups, {
    workspaceId,
    pageSize: defaultListPageSize,
    pageToken: "",
  });
  const unitTasksQuery = useQuery(listUnitTasks, {
    workspaceId,
    status: UnitTaskStatus.UNSPECIFIED,
    pageSize: defaultListPageSize,
    pageToken: "",
  });

  return (
    <section className="panel page-panel" aria-label="Projects page">
      <header className="page-header">
        <h2>Projects Overview</h2>
        <p className="note">
          Workspace-level summary and repository groups for planning and risk review.
        </p>
      </header>

      <div className="product-grid product-grid-two">
        <article className="query-card">
          <h3>Workspace Overview</h3>
          {overviewQuery.isPending ? (
            <p className="query-status">Loading workspace overview...</p>
          ) : overviewQuery.error ? (
            <p className="error" role="alert">
              {describeConnectError(overviewQuery.error, "Failed to load workspace overview.")}
            </p>
          ) : overviewQuery.data?.overview ? (
            <dl className="summary-grid">
              <dt>Total unit tasks</dt>
              <dd>{overviewQuery.data.overview.totalUnitTaskCount}</dd>
              <dt>Action required</dt>
              <dd>{overviewQuery.data.overview.actionRequiredUnitTaskCount}</dd>
              <dt>Waiting plan review</dt>
              <dd>{overviewQuery.data.overview.waitingPlanSubTaskCount}</dd>
              <dt>Failed subtasks</dt>
              <dd>{overviewQuery.data.overview.failedSubTaskCount}</dd>
              <dt>Active sessions</dt>
              <dd>{overviewQuery.data.overview.activeSessionCount}</dd>
              <dt>Open pull requests</dt>
              <dd>{overviewQuery.data.overview.openPullRequestCount}</dd>
            </dl>
          ) : (
            <p className="query-status">No overview data available.</p>
          )}
        </article>

        <article className="query-card">
          <h3>Repository Groups</h3>
          {repositoryGroupsQuery.isPending ? (
            <p className="query-status">Loading repository groups...</p>
          ) : repositoryGroupsQuery.error ? (
            <p className="error" role="alert">
              {describeConnectError(repositoryGroupsQuery.error, "Failed to load repository groups.")}
            </p>
          ) : repositoryGroupsQuery.data?.items.length ? (
            <ul className="entity-list">
              {repositoryGroupsQuery.data.items.map((group) => (
                <li key={group.repositoryGroupId}>
                  <strong>{group.repositoryGroupId}</strong>
                  <small>{group.repositories.length} repositories</small>
                </li>
              ))}
            </ul>
          ) : (
            <p className="query-status">No repository groups found.</p>
          )}
        </article>

        <article className="query-card">
          <h3>Active Unit Tasks</h3>
          {unitTasksQuery.isPending ? (
            <p className="query-status">Loading unit tasks...</p>
          ) : unitTasksQuery.error ? (
            <p className="error" role="alert">
              {describeConnectError(unitTasksQuery.error, "Failed to load unit tasks.")}
            </p>
          ) : unitTasksQuery.data?.items.length ? (
            <ul className="entity-list clickable-list">
              {unitTasksQuery.data.items.slice(0, 12).map((unitTask) => (
                <li key={unitTask.unitTaskId}>
                  <button
                    type="button"
                    className={
                      selection.selectedUnitTaskId === unitTask.unitTaskId
                        ? "entity-link entity-link-active"
                        : "entity-link"
                    }
                    onClick={() =>
                      onSelectionChange({
                        selectedUnitTaskId: unitTask.unitTaskId,
                        selectedSubTaskId: null,
                        selectedSessionId: null,
                        selectedPrTrackingId: null,
                      })
                    }
                  >
                    <strong>{unitTask.unitTaskId}</strong>
                    <small>{enumLabel(UnitTaskStatus, unitTask.status)}</small>
                  </button>
                </li>
              ))}
            </ul>
          ) : (
            <p className="query-status">No unit tasks found.</p>
          )}
        </article>
      </div>
    </section>
  );
}

function ThreadsPage({
  workspaceId,
  selection,
  onSelectionChange,
}: {
  workspaceId: string;
  selection: SharedSelectionState;
  onSelectionChange: (patch: Partial<SharedSelectionState>) => void;
}) {
  const unitTasksQuery = useQuery(listUnitTasks, {
    workspaceId,
    status: UnitTaskStatus.UNSPECIFIED,
    pageSize: defaultListPageSize,
    pageToken: "",
  });

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
      enabled: selection.selectedUnitTaskId !== null,
    },
  );

  const sessionListQuery = useQuery(listSessions, {
    workspaceId,
    status: AgentSessionStatus.UNSPECIFIED,
    cliType: AgentCliType.UNSPECIFIED,
    pageSize: defaultListPageSize,
    pageToken: "",
  });

  const selectedSubTaskQuery = useQuery(
    getSubTask,
    selection.selectedSubTaskId
      ? {
          workspaceId,
          subTaskId: selection.selectedSubTaskId,
        }
      : undefined,
    {
      enabled: selection.selectedSubTaskId !== null,
    },
  );

  const selectedSessionOutputQuery = useQuery(
    getSessionOutput,
    selection.selectedSessionId
      ? {
          workspaceId,
          sessionId: selection.selectedSessionId,
        }
      : undefined,
    {
      enabled: selection.selectedSessionId !== null,
    },
  );

  useEffect(() => {
    if (selection.selectedUnitTaskId || !unitTasksQuery.data?.items.length) {
      return;
    }

    onSelectionChange({
      selectedUnitTaskId: unitTasksQuery.data.items[0].unitTaskId,
    });
  }, [onSelectionChange, selection.selectedUnitTaskId, unitTasksQuery.data?.items]);

  useEffect(() => {
    if (selection.selectedSubTaskId || !subTasksQuery.data?.items.length) {
      return;
    }

    onSelectionChange({
      selectedSubTaskId: subTasksQuery.data.items[0].subTaskId,
    });
  }, [onSelectionChange, selection.selectedSubTaskId, subTasksQuery.data?.items]);

  useEffect(() => {
    if (selection.selectedSessionId || !sessionListQuery.data?.items.length) {
      return;
    }

    onSelectionChange({
      selectedSessionId: sessionListQuery.data.items[0].sessionId,
    });
  }, [onSelectionChange, selection.selectedSessionId, sessionListQuery.data?.items]);

  return (
    <section className="panel page-panel" aria-label="Threads page">
      <header className="page-header">
        <h2>Thread Inbox</h2>
        <p className="note">
          Inbox, execution detail, and action-ready context for unit task workflows.
        </p>
      </header>

      <div className="threads-layout">
        <article className="query-card">
          <h3>Inbox</h3>
          <h4>Unit Tasks</h4>
          {unitTasksQuery.isPending ? (
            <p className="query-status">Loading unit tasks...</p>
          ) : unitTasksQuery.error ? (
            <p className="error" role="alert">
              {describeConnectError(unitTasksQuery.error, "Failed to load unit tasks.")}
            </p>
          ) : (
            <ul className="entity-list clickable-list">
              {unitTasksQuery.data?.items.map((unitTask) => (
                <li key={unitTask.unitTaskId}>
                  <button
                    type="button"
                    className={
                      selection.selectedUnitTaskId === unitTask.unitTaskId
                        ? "entity-link entity-link-active"
                        : "entity-link"
                    }
                    onClick={() =>
                      onSelectionChange({
                        selectedUnitTaskId: unitTask.unitTaskId,
                        selectedSubTaskId: null,
                      })
                    }
                  >
                    <strong>{unitTask.unitTaskId}</strong>
                    <small>{enumLabel(UnitTaskStatus, unitTask.status)}</small>
                  </button>
                </li>
              ))}
            </ul>
          )}

          <h4>Sub Tasks</h4>
          {subTasksQuery.isPending ? (
            <p className="query-status">Loading sub tasks...</p>
          ) : subTasksQuery.error ? (
            <p className="error" role="alert">
              {describeConnectError(subTasksQuery.error, "Failed to load sub tasks.")}
            </p>
          ) : (
            <ul className="entity-list clickable-list">
              {subTasksQuery.data?.items.map((subTask) => (
                <li key={subTask.subTaskId}>
                  <button
                    type="button"
                    className={
                      selection.selectedSubTaskId === subTask.subTaskId
                        ? "entity-link entity-link-active"
                        : "entity-link"
                    }
                    onClick={() =>
                      onSelectionChange({
                        selectedSubTaskId: subTask.subTaskId,
                        selectedUnitTaskId: subTask.unitTaskId,
                      })
                    }
                  >
                    <strong>{subTask.subTaskId}</strong>
                    <small>{enumLabel(SubTaskStatus, subTask.status)}</small>
                  </button>
                </li>
              ))}
            </ul>
          )}

          <h4>Sessions</h4>
          {sessionListQuery.isPending ? (
            <p className="query-status">Loading sessions...</p>
          ) : sessionListQuery.error ? (
            <p className="error" role="alert">
              {describeConnectError(sessionListQuery.error, "Failed to load sessions.")}
            </p>
          ) : (
            <ul className="entity-list clickable-list">
              {sessionListQuery.data?.items.map((session) => (
                <li key={session.sessionId}>
                  <button
                    type="button"
                    className={
                      selection.selectedSessionId === session.sessionId
                        ? "entity-link entity-link-active"
                        : "entity-link"
                    }
                    onClick={() =>
                      onSelectionChange({
                        selectedSessionId: session.sessionId,
                      })
                    }
                  >
                    <strong>{session.sessionId}</strong>
                    <small>{enumLabel(AgentSessionStatus, session.status)}</small>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </article>

        <article className="query-card">
          <h3>Detail Timeline</h3>
          {selectedSubTaskQuery.data?.subTask ? (
            <div className="detail-block">
              <h4>Selected Sub Task</h4>
              <dl className="summary-grid">
                <dt>Sub task</dt>
                <dd>{selectedSubTaskQuery.data.subTask.subTaskId}</dd>
                <dt>Unit task</dt>
                <dd>{selectedSubTaskQuery.data.subTask.unitTaskId}</dd>
                <dt>Status</dt>
                <dd>{enumLabel(SubTaskStatus, selectedSubTaskQuery.data.subTask.status)}</dd>
              </dl>
            </div>
          ) : (
            <p className="query-status">Select a sub task to inspect detail.</p>
          )}

          <div className="detail-block">
            <h4>Session Output</h4>
            {selectedSessionOutputQuery.isPending ? (
              <p className="query-status">Loading session output...</p>
            ) : selectedSessionOutputQuery.error ? (
              <p className="error" role="alert">
                {describeConnectError(
                  selectedSessionOutputQuery.error,
                  "Failed to load session output.",
                )}
              </p>
            ) : selectedSessionOutputQuery.data?.events.length ? (
              <div className="stream-events">
                {selectedSessionOutputQuery.data.events.map((event, index) => (
                  <article key={`${event.sessionId}-${index}`} className="stream-event-item">
                    <header className="stream-event-header">
                      <span>{enumLabel(SessionOutputKind, event.kind)}</span>
                      <span>{event.isTerminal ? "terminal" : "active"}</span>
                    </header>
                    <pre className="query-result stream-event-body">{event.body}</pre>
                  </article>
                ))}
              </div>
            ) : (
              <p className="query-status">No session output selected yet.</p>
            )}
          </div>
        </article>
      </div>
    </section>
  );
}

function ReviewPage({
  workspaceId,
  selection,
  onSelectionChange,
}: {
  workspaceId: string;
  selection: SharedSelectionState;
  onSelectionChange: (patch: Partial<SharedSelectionState>) => void;
}) {
  const pullRequestListQuery = useQuery(listPullRequests, {
    workspaceId,
    status: PrStatus.UNSPECIFIED,
    pageSize: defaultListPageSize,
    pageToken: "",
  });

  const selectedPullRequestQuery = useQuery(
    getPullRequest,
    selection.selectedPrTrackingId
      ? {
          workspaceId,
          prTrackingId: selection.selectedPrTrackingId,
        }
      : undefined,
    {
      enabled: selection.selectedPrTrackingId !== null,
    },
  );

  const reviewCommentQuery = useQuery(
    listReviewComments,
    selection.selectedPrTrackingId
      ? {
          workspaceId,
          prTrackingId: selection.selectedPrTrackingId,
        }
      : undefined,
    {
      enabled: selection.selectedPrTrackingId !== null,
    },
  );

  const reviewAssistQuery = useQuery(
    listReviewAssistItems,
    selection.selectedUnitTaskId
      ? {
          workspaceId,
          unitTaskId: selection.selectedUnitTaskId,
        }
      : undefined,
    {
      enabled: selection.selectedUnitTaskId !== null,
    },
  );

  useEffect(() => {
    if (selection.selectedPrTrackingId || !pullRequestListQuery.data?.items.length) {
      return;
    }

    onSelectionChange({
      selectedPrTrackingId: pullRequestListQuery.data.items[0].prTrackingId,
    });
  }, [onSelectionChange, pullRequestListQuery.data?.items, selection.selectedPrTrackingId]);

  return (
    <section className="panel page-panel" aria-label="Review page">
      <header className="page-header">
        <h2>Review Hub</h2>
        <p className="note">
          Pull request queue with unified review assist and comment context.
        </p>
      </header>

      <div className="threads-layout">
        <article className="query-card">
          <h3>Pull Request Queue</h3>
          {pullRequestListQuery.isPending ? (
            <p className="query-status">Loading pull requests...</p>
          ) : pullRequestListQuery.error ? (
            <p className="error" role="alert">
              {describeConnectError(pullRequestListQuery.error, "Failed to load pull requests.")}
            </p>
          ) : (
            <ul className="entity-list clickable-list">
              {pullRequestListQuery.data?.items.map((pullRequest) => (
                <li key={pullRequest.prTrackingId}>
                  <button
                    type="button"
                    className={
                      selection.selectedPrTrackingId === pullRequest.prTrackingId
                        ? "entity-link entity-link-active"
                        : "entity-link"
                    }
                    onClick={() =>
                      onSelectionChange({
                        selectedPrTrackingId: pullRequest.prTrackingId,
                      })
                    }
                  >
                    <strong>{pullRequest.prTrackingId}</strong>
                    <small>{enumLabel(PrStatus, pullRequest.status)}</small>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </article>

        <article className="query-card">
          <h3>Review Detail</h3>
          {selectedPullRequestQuery.data?.pullRequest ? (
            <dl className="summary-grid">
              <dt>PR tracking id</dt>
              <dd>{selectedPullRequestQuery.data.pullRequest.prTrackingId}</dd>
              <dt>Status</dt>
              <dd>{enumLabel(PrStatus, selectedPullRequestQuery.data.pullRequest.status)}</dd>
            </dl>
          ) : (
            <p className="query-status">Select a pull request to inspect review detail.</p>
          )}

          <div className="detail-block">
            <h4>Review Assist Items</h4>
            {reviewAssistQuery.isPending ? (
              <p className="query-status">Loading review assist items...</p>
            ) : reviewAssistQuery.error ? (
              <p className="error" role="alert">
                {describeConnectError(reviewAssistQuery.error, "Failed to load review assist items.")}
              </p>
            ) : reviewAssistQuery.data?.items.length ? (
              <ul className="entity-list">
                {reviewAssistQuery.data.items.map((item) => (
                  <li key={item.reviewAssistId}>
                    <strong>{item.reviewAssistId}</strong>
                    <small>{item.body}</small>
                  </li>
                ))}
              </ul>
            ) : (
              <p className="query-status">No review assist items for selected unit task.</p>
            )}
          </div>

          <div className="detail-block">
            <h4>Review Comments</h4>
            {reviewCommentQuery.isPending ? (
              <p className="query-status">Loading review comments...</p>
            ) : reviewCommentQuery.error ? (
              <p className="error" role="alert">
                {describeConnectError(reviewCommentQuery.error, "Failed to load review comments.")}
              </p>
            ) : reviewCommentQuery.data?.comments.length ? (
              <ul className="entity-list">
                {reviewCommentQuery.data.comments.map((comment) => (
                  <li key={comment.reviewCommentId}>
                    <strong>{comment.reviewCommentId}</strong>
                    <small>{comment.body}</small>
                  </li>
                ))}
              </ul>
            ) : (
              <p className="query-status">No review comments for selected pull request.</p>
            )}
          </div>
        </article>
      </div>
    </section>
  );
}

function WorktreesPage({
  workspaceId,
  selection,
  onSelectionChange,
  logger,
}: {
  workspaceId: string;
  selection: SharedSelectionState;
  onSelectionChange: (patch: Partial<SharedSelectionState>) => void;
  logger: DexDexLogger;
}) {
  const queryClient = useQueryClient();
  const transport = useTransport();
  const eventStreamClient = useMemo(
    () => createClient(EventStreamService, transport),
    [transport],
  );

  const [streamStatus, setStreamStatus] = useState<"idle" | "running" | "stopped" | "error">(
    "idle",
  );
  const [streamError, setStreamError] = useState<string | null>(null);
  const [streamFromSequenceInput, setStreamFromSequenceInput] = useState("0");
  const [streamEvents, setStreamEvents] = useState<StreamWorkspaceEventsResponse[]>([]);
  const streamAbortControllerRef = useRef<AbortController | null>(null);

  const sessionListQuery = useQuery(listSessions, {
    workspaceId,
    status: AgentSessionStatus.UNSPECIFIED,
    cliType: AgentCliType.UNSPECIFIED,
    pageSize: defaultListPageSize,
    pageToken: "",
  });

  const selectedSessionOutputQuery = useQuery(
    getSessionOutput,
    selection.selectedSessionId
      ? {
          workspaceId,
          sessionId: selection.selectedSessionId,
        }
      : undefined,
    {
      enabled: selection.selectedSessionId !== null,
    },
  );

  useEffect(() => {
    if (selection.selectedSessionId || !sessionListQuery.data?.items.length) {
      return;
    }

    onSelectionChange({
      selectedSessionId: sessionListQuery.data.items[0].sessionId,
    });
  }, [onSelectionChange, selection.selectedSessionId, sessionListQuery.data?.items]);

  useEffect(() => {
    return () => {
      streamAbortControllerRef.current?.abort();
      streamAbortControllerRef.current = null;
    };
  }, []);

  async function startStream() {
    const normalized = streamFromSequenceInput.trim();
    if (!/^\d+$/.test(normalized)) {
      setStreamError("From sequence must be a non-negative integer.");
      setStreamStatus("error");
      return;
    }

    streamAbortControllerRef.current?.abort();
    const abortController = new AbortController();
    streamAbortControllerRef.current = abortController;

    setStreamStatus("running");
    setStreamError(null);

    logger.info("worktrees.stream.start", {
      workspace_id: workspaceId,
      from_sequence: normalized,
      result: "pending",
    });

    try {
      for await (const event of eventStreamClient.streamWorkspaceEvents(
        {
          workspaceId,
          fromSequence: BigInt(normalized),
        },
        {
          signal: abortController.signal,
        },
      )) {
        if (event.sequence === 0n) {
          continue;
        }

        setStreamEvents((previous) => [event, ...previous].slice(0, maxStreamEvents));
        applyStreamEventToCaches(event, workspaceId, transport, queryClient);
      }

      if (!abortController.signal.aborted) {
        setStreamStatus("stopped");
        logger.info("worktrees.stream.stop", {
          workspace_id: workspaceId,
          result: "success",
        });
      }
    } catch (error) {
      if (abortController.signal.aborted) {
        return;
      }

      const message = describeConnectError(error, "Live stream failed.");
      setStreamStatus("error");
      setStreamError(message);
      logger.error("worktrees.stream.error", {
        workspace_id: workspaceId,
        result: "error",
      });
    } finally {
      if (streamAbortControllerRef.current === abortController) {
        streamAbortControllerRef.current = null;
      }
    }
  }

  function stopStream() {
    streamAbortControllerRef.current?.abort();
    streamAbortControllerRef.current = null;
    setStreamStatus("stopped");
  }

  return (
    <section className="panel page-panel" aria-label="Worktrees page">
      <header className="page-header">
        <h2>Worktrees & Live Stream</h2>
        <p className="note">
          Session execution list with incremental stream-driven cache updates.
        </p>
      </header>

      <div className="threads-layout">
        <article className="query-card">
          <h3>Session Runs</h3>
          {sessionListQuery.isPending ? (
            <p className="query-status">Loading sessions...</p>
          ) : sessionListQuery.error ? (
            <p className="error" role="alert">
              {describeConnectError(sessionListQuery.error, "Failed to load sessions.")}
            </p>
          ) : (
            <ul className="entity-list clickable-list">
              {sessionListQuery.data?.items.map((session) => (
                <li key={session.sessionId}>
                  <button
                    type="button"
                    className={
                      selection.selectedSessionId === session.sessionId
                        ? "entity-link entity-link-active"
                        : "entity-link"
                    }
                    onClick={() =>
                      onSelectionChange({
                        selectedSessionId: session.sessionId,
                      })
                    }
                  >
                    <strong>{session.sessionId}</strong>
                    <small>{enumLabel(AgentSessionStatus, session.status)}</small>
                  </button>
                </li>
              ))}
            </ul>
          )}

          <div className="field">
            <label htmlFor="stream-from-sequence">From Sequence</label>
            <input
              id="stream-from-sequence"
              name="stream-from-sequence"
              value={streamFromSequenceInput}
              onChange={(event) => setStreamFromSequenceInput(event.target.value)}
              placeholder="0"
            />
          </div>
          <div className="actions">
            <button type="button" onClick={() => void startStream()} disabled={streamStatus === "running"}>
              Start Live Stream
            </button>
            <button
              type="button"
              className="secondary-button"
              onClick={stopStream}
              disabled={streamStatus !== "running"}
            >
              Stop Stream
            </button>
          </div>
          <p className="note">Stream status: {streamStatus.toUpperCase()}</p>
          {streamError ? (
            <p className="error" role="alert">
              {streamError}
            </p>
          ) : null}
        </article>

        <article className="query-card">
          <h3>Event Timeline</h3>
          {streamEvents.length > 0 ? (
            <div className="stream-events">
              {streamEvents.map((event) => (
                <article
                  key={`${event.sequence.toString()}-${event.eventType}`}
                  className="stream-event-item"
                >
                  <header className="stream-event-header">
                    <span>#{event.sequence.toString()}</span>
                    <span>{enumLabel(StreamEventType, event.eventType)}</span>
                  </header>
                  <p className="note">{formatOccurredAt(event.occurredAt)}</p>
                  <pre className="query-result stream-event-body">
                    {stringifyForUi(event)}
                  </pre>
                </article>
              ))}
            </div>
          ) : (
            <p className="query-status">No stream events yet.</p>
          )}

          <h4>Selected Session Output</h4>
          {selectedSessionOutputQuery.data?.events.length ? (
            <pre className="query-result">
              {JSON.stringify(selectedSessionOutputQuery.data.events, null, 2)}
            </pre>
          ) : (
            <p className="query-status">Select a session to inspect output.</p>
          )}
        </article>
      </div>
    </section>
  );
}

function AutomationsPage({
  localStoreState,
  updateLocalStore,
}: {
  localStoreState: DesktopLocalStoreState;
  updateLocalStore: UpdateLocalStore;
}) {
  const [newAutomationName, setNewAutomationName] = useState("");
  const [newAutomationSchedule, setNewAutomationSchedule] = useState("Every weekday 09:00");

  function handleCreateAutomation(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const name = newAutomationName.trim();
    if (name.length === 0) {
      return;
    }

    updateLocalStore((current) => {
      const id = `automation-${Date.now().toString()}`;
      return {
        ...current,
        automations: [
          ...current.automations,
          {
            id,
            name,
            schedule: newAutomationSchedule.trim() || "Manual",
            enabled: true,
            lastRunAt: null,
          },
        ],
        lastSelectedAutomationId: id,
      };
    });

    setNewAutomationName("");
  }

  return (
    <section className="panel page-panel" aria-label="Automations page">
      <header className="page-header">
        <h2>Automations</h2>
        <p className="note">Manage recurring automation records with local persistence.</p>
      </header>

      <div className="product-grid product-grid-two">
        <article className="query-card">
          <h3>Automation Queue</h3>
          <ul className="entity-list clickable-list">
            {localStoreState.automations.map((automation) => (
              <li key={automation.id}>
                <button
                  type="button"
                  className={
                    localStoreState.lastSelectedAutomationId === automation.id
                      ? "entity-link entity-link-active"
                      : "entity-link"
                  }
                  onClick={() =>
                    updateLocalStore((current) => ({
                      ...current,
                      lastSelectedAutomationId: automation.id,
                    }))
                  }
                >
                  <strong>{automation.name}</strong>
                  <small>{automation.schedule}</small>
                </button>
                <div className="actions">
                  <button
                    type="button"
                    className="secondary-button"
                    onClick={() =>
                      updateLocalStore((current) => ({
                        ...current,
                        automations: current.automations.map((item) =>
                          item.id === automation.id
                            ? {
                                ...item,
                                enabled: !item.enabled,
                                lastRunAt: item.lastRunAt,
                              }
                            : item,
                        ),
                      }))
                    }
                  >
                    {automation.enabled ? "Disable" : "Enable"}
                  </button>
                  <button
                    type="button"
                    className="secondary-button"
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
              </li>
            ))}
          </ul>
        </article>

        <article className="query-card">
          <h3>Create Automation</h3>
          <form onSubmit={handleCreateAutomation}>
            <div className="field">
              <label htmlFor="automation-name">Name</label>
              <input
                id="automation-name"
                name="automation-name"
                value={newAutomationName}
                onChange={(event) => setNewAutomationName(event.target.value)}
                placeholder="Nightly Stream Health"
              />
            </div>
            <div className="field">
              <label htmlFor="automation-schedule">Schedule</label>
              <input
                id="automation-schedule"
                name="automation-schedule"
                value={newAutomationSchedule}
                onChange={(event) => setNewAutomationSchedule(event.target.value)}
                placeholder="Every weekday 09:00"
              />
            </div>
            <button type="submit">Create Automation</button>
          </form>
        </article>
      </div>
    </section>
  );
}

function LocalEnvironmentsPage({
  localStoreState,
  updateLocalStore,
}: {
  localStoreState: DesktopLocalStoreState;
  updateLocalStore: UpdateLocalStore;
}) {
  const [environmentName, setEnvironmentName] = useState("");
  const [environmentEndpoint, setEnvironmentEndpoint] = useState("http://127.0.0.1:7878");

  function runDiagnostics(environmentId: string) {
    updateLocalStore((current) => ({
      ...current,
      localEnvironments: current.localEnvironments.map((environment) => {
        if (environment.id !== environmentId) {
          return environment;
        }

        const reachable = environment.endpointUrl.startsWith("http://") ||
          environment.endpointUrl.startsWith("https://");

        return {
          ...environment,
          health: reachable ? LocalEnvironmentHealth.Healthy : LocalEnvironmentHealth.Unreachable,
          lastCheckedAt: new Date().toISOString(),
          lastErrorMessage: reachable ? null : "endpoint must use http/https",
        };
      }),
      lastSelectedEnvironmentId: environmentId,
    }));
  }

  function handleCreateEnvironment(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();

    const name = environmentName.trim();
    const endpointUrl = environmentEndpoint.trim();
    if (name.length === 0 || endpointUrl.length === 0) {
      return;
    }

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

    setEnvironmentName("");
  }

  return (
    <section className="panel page-panel" aria-label="Local environments page">
      <header className="page-header">
        <h2>Local Environments</h2>
        <p className="note">Persist and diagnose endpoint profiles for workspace access.</p>
      </header>

      <div className="product-grid product-grid-two">
        <article className="query-card">
          <h3>Environment Records</h3>
          <ul className="entity-list">
            {localStoreState.localEnvironments.map((environment) => (
              <li key={environment.id}>
                <strong>{environment.name}</strong>
                <small>{environment.endpointUrl}</small>
                <small>{environment.health}</small>
                <small>
                  Last checked: {environment.lastCheckedAt ? new Date(environment.lastCheckedAt).toLocaleString() : "never"}
                </small>
                <div className="actions">
                  <button type="button" onClick={() => runDiagnostics(environment.id)}>
                    Run Diagnostics
                  </button>
                  <button
                    type="button"
                    className="secondary-button"
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
                    Delete
                  </button>
                </div>
              </li>
            ))}
          </ul>
        </article>

        <article className="query-card">
          <h3>Add Environment</h3>
          <form onSubmit={handleCreateEnvironment}>
            <div className="field">
              <label htmlFor="environment-name">Name</label>
              <input
                id="environment-name"
                name="environment-name"
                value={environmentName}
                onChange={(event) => setEnvironmentName(event.target.value)}
                placeholder="Staging"
              />
            </div>
            <div className="field">
              <label htmlFor="environment-endpoint">Endpoint URL</label>
              <input
                id="environment-endpoint"
                name="environment-endpoint"
                value={environmentEndpoint}
                onChange={(event) => setEnvironmentEndpoint(event.target.value)}
                placeholder="https://dexdex.example/rpc"
              />
            </div>
            <button type="submit">Save Environment</button>
          </form>
        </article>
      </div>
    </section>
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
    <section className="panel page-panel" aria-label="Settings page">
      <header className="page-header">
        <h2>Settings</h2>
        <p className="note">Desktop preferences with persistent state.</p>
      </header>

      <article className="query-card">
        <div className="field">
          <label htmlFor="default-page">Default Page</label>
          <select
            id="default-page"
            name="default-page"
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
              <option key={page.id} value={page.id}>
                {page.label}
              </option>
            ))}
          </select>
        </div>

        <div className="field field-inline">
          <label htmlFor="compact-mode">Compact Mode</label>
          <input
            id="compact-mode"
            name="compact-mode"
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
          />
        </div>

        <div className="field field-inline">
          <label htmlFor="auto-start-stream">Auto Start Stream on Worktrees</label>
          <input
            id="auto-start-stream"
            name="auto-start-stream"
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
          />
        </div>
      </article>
    </section>
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
  const [runRawJsonlInput, setRunRawJsonlInput] = useState(
    '{"type":"text","part":{"text":"hello"}}',
  );
  const [runInputMode, setRunInputMode] = useState<"preset" | "raw">("preset");

  async function handleSubmitPlanDecision(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();

    if (!selection.selectedSubTaskId) {
      onActionStateChange({
        label: "Submit Plan Decision",
        status: ActionResultStatus.Error,
        message: "Select a sub task first.",
      });
      return;
    }

    if (planDecision === PlanDecision.REVISE && planRevisionNote.trim().length === 0) {
      onActionStateChange({
        label: "Submit Plan Decision",
        status: ActionResultStatus.Error,
        message: "Revision note is required for REVISE.",
      });
      return;
    }

    onActionStateChange({
      label: "Submit Plan Decision",
      status: ActionResultStatus.Pending,
      message: "Submitting decision...",
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
          response.createdSubTask?.subTaskId ?? response.updatedSubTask?.subTaskId ??
          selection.selectedSubTaskId,
        selectedUnitTaskId:
          response.updatedSubTask?.unitTaskId ?? selection.selectedUnitTaskId,
      });

      await queryClient.invalidateQueries();
      onActionStateChange({
        label: "Submit Plan Decision",
        status: ActionResultStatus.Success,
        message: "Plan decision submitted.",
      });
    } catch (error) {
      onActionStateChange({
        label: "Submit Plan Decision",
        status: ActionResultStatus.Error,
        message: describeConnectError(error, "Failed to submit plan decision."),
      });
    }
  }

  async function handleRunSessionAdapter(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();

    if (!selection.selectedUnitTaskId || !selection.selectedSubTaskId || !selection.selectedSessionId) {
      onActionStateChange({
        label: "Run Session Adapter",
        status: ActionResultStatus.Error,
        message: "Select unit task, sub task, and session before running adapter.",
      });
      return;
    }

    if (runInputMode === "raw" && runRawJsonlInput.trim().length === 0) {
      onActionStateChange({
        label: "Run Session Adapter",
        status: ActionResultStatus.Error,
        message: "Raw JSONL is required in raw mode.",
      });
      return;
    }

    onActionStateChange({
      label: "Run Session Adapter",
      status: ActionResultStatus.Pending,
      message: "Running session adapter...",
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
            ? {
                case: "fixturePreset",
                value: runFixturePreset,
              }
            : {
                case: "rawJsonl",
                value: runRawJsonlInput,
              },
      });

      await queryClient.invalidateQueries();
      onActionStateChange({
        label: "Run Session Adapter",
        status: ActionResultStatus.Success,
        message: "Session adapter run completed.",
      });
    } catch (error) {
      onActionStateChange({
        label: "Run Session Adapter",
        status: ActionResultStatus.Error,
        message: describeConnectError(error, "Failed to run session adapter."),
      });
    }
  }

  return (
    <aside className="panel inspector-rail" aria-label="Action center">
      <h2>Action Center</h2>
      <p className="status" aria-live="polite">
        {actionState.status.toUpperCase()} · {actionState.label}
      </p>
      <p className="note">{actionState.message}</p>

      <section className="inspector-block">
        <h3>Selection Context</h3>
        <dl className="summary-grid">
          <dt>Page</dt>
          <dd>{activePage?.label ?? "n/a"}</dd>
          <dt>Unit task</dt>
          <dd>{selection.selectedUnitTaskId ?? "n/a"}</dd>
          <dt>Sub task</dt>
          <dd>{selection.selectedSubTaskId ?? "n/a"}</dd>
          <dt>Session</dt>
          <dd>{selection.selectedSessionId ?? "n/a"}</dd>
          <dt>PR tracking</dt>
          <dd>{selection.selectedPrTrackingId ?? "n/a"}</dd>
        </dl>
      </section>

      {activePage?.id === DexDexPageId.Threads ? (
        <section className="inspector-block">
          <h3>Thread Actions</h3>
          <form onSubmit={handleSubmitPlanDecision}>
            <div className="field">
              <label htmlFor="plan-decision-select">Plan Decision</label>
              <select
                id="plan-decision-select"
                name="plan-decision-select"
                value={planDecision}
                onChange={(event) => setPlanDecision(Number(event.target.value) as PlanDecision)}
              >
                <option value={PlanDecision.APPROVE}>APPROVE</option>
                <option value={PlanDecision.REVISE}>REVISE</option>
                <option value={PlanDecision.REJECT}>REJECT</option>
              </select>
            </div>
            {planDecision === PlanDecision.REVISE ? (
              <div className="field">
                <label htmlFor="plan-revision-note">Revision Note</label>
                <textarea
                  id="plan-revision-note"
                  name="plan-revision-note"
                  value={planRevisionNote}
                  onChange={(event) => setPlanRevisionNote(event.target.value)}
                  rows={4}
                />
              </div>
            ) : null}
            <button type="submit">Submit Plan Decision</button>
          </form>

          <form onSubmit={handleRunSessionAdapter}>
            <div className="field">
              <label htmlFor="run-cli-type">CLI Type</label>
              <select
                id="run-cli-type"
                name="run-cli-type"
                value={runCliType}
                onChange={(event) => setRunCliType(Number(event.target.value) as AgentCliType)}
              >
                <option value={AgentCliType.CODEX_CLI}>CODEX_CLI</option>
                <option value={AgentCliType.CLAUDE_CODE}>CLAUDE_CODE</option>
                <option value={AgentCliType.OPENCODE}>OPENCODE</option>
              </select>
            </div>
            <div className="field">
              <label htmlFor="run-input-mode">Input Mode</label>
              <select
                id="run-input-mode"
                name="run-input-mode"
                value={runInputMode}
                onChange={(event) => setRunInputMode(event.target.value as "preset" | "raw")}
              >
                <option value="preset">Preset Fixture</option>
                <option value="raw">Raw JSONL</option>
              </select>
            </div>
            {runInputMode === "preset" ? (
              <div className="field">
                <label htmlFor="run-fixture-preset">Fixture Preset</label>
                <select
                  id="run-fixture-preset"
                  name="run-fixture-preset"
                  value={runFixturePreset}
                  onChange={(event) =>
                    setRunFixturePreset(Number(event.target.value) as SessionAdapterFixturePreset)
                  }
                >
                  <option value={SessionAdapterFixturePreset.CODEX_CLI_FAILURE}>
                    CODEX_CLI_FAILURE
                  </option>
                  <option value={SessionAdapterFixturePreset.CLAUDE_CODE_STREAM}>
                    CLAUDE_CODE_STREAM
                  </option>
                  <option value={SessionAdapterFixturePreset.OPENCODE_RUN}>OPENCODE_RUN</option>
                </select>
              </div>
            ) : (
              <div className="field">
                <label htmlFor="run-raw-jsonl">Raw JSONL</label>
                <textarea
                  id="run-raw-jsonl"
                  name="run-raw-jsonl"
                  value={runRawJsonlInput}
                  onChange={(event) => setRunRawJsonlInput(event.target.value)}
                  rows={6}
                />
              </div>
            )}
            <button type="submit">Run Session Adapter</button>
          </form>
        </section>
      ) : null}

      <section className="inspector-block">
        <h3>Connection</h3>
        <dl className="summary-grid">
          <dt>Workspace</dt>
          <dd>{workspaceId}</dd>
          <dt>Mode</dt>
          <dd>{connection.mode}</dd>
          <dt>Endpoint</dt>
          <dd>{connection.endpointUrl}</dd>
          <dt>Source</dt>
          <dd>{connection.endpointSource}</dd>
        </dl>
      </section>
    </aside>
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
  const [selectionState, setSelectionState] = useState<SharedSelectionState>(
    createEmptySharedSelectionState(),
  );
  const [actionState, setActionState] = useState<ActionCenterState>({
    label: "Awaiting action",
    status: ActionResultStatus.Idle,
    message: "Select an item and run an action.",
  });
  const [localStoreState, setLocalStoreState] = useState<DesktopLocalStoreState>(() =>
    loadDesktopLocalStoreState(),
  );

  const activePage = useMemo(
    () => resolvePageByPath(location.pathname),
    [location.pathname],
  );

  const statusLabel = useMemo(() => {
    if (status === AppStatus.Resolving) {
      return "Resolving workspace endpoint...";
    }
    if (status === AppStatus.Resolved) {
      return "Workspace connected with Connect RPC transport.";
    }
    if (status === AppStatus.Error) {
      return "Resolution failed. Review the error and retry.";
    }
    return "Select a workspace to enter DexDex.";
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
      navigate(pagePathFromPageId(localStoreState.settings.defaultPage), {
        replace: true,
      });
    }
  }, [
    activePage,
    activeWorkspaceSession,
    localStoreState.settings.defaultPage,
    location.pathname,
    navigate,
  ]);

  useEffect(() => {
    if (!activeWorkspaceSession || !activePage) {
      return;
    }

    logger.info("desktop.page.view", {
      page_id: activePage.id,
      result: "success",
      workspace_id: activeWorkspaceSession.workspaceId,
    });
  }, [activePage, activeWorkspaceSession, logger]);

  function updateLocalStore(updater: (current: DesktopLocalStoreState) => DesktopLocalStoreState) {
    const updated = updateDesktopLocalStoreState(updater);
    setLocalStoreState(updated);
  }

  function resolveProfileInputFromForm(actionLabel: string): {
    workspaceId: string;
    mode: WorkspaceMode;
    remoteEndpointUrl?: string;
  } | null {
    const workspaceId = workspaceIdInput.trim();
    if (workspaceId.length === 0) {
      setErrorMessage(`${actionLabel}: workspace id is required.`);
      setPickerMessage(null);
      setStatus(AppStatus.Error);
      return null;
    }

    try {
      const normalizedRemoteEndpointUrl =
        mode === WorkspaceMode.Remote
          ? normalizeRemoteEndpointUrl(remoteEndpointUrl)
          : undefined;

      setErrorMessage(null);
      setPickerMessage(null);
      setStatus(AppStatus.Idle);

      return {
        workspaceId,
        mode,
        remoteEndpointUrl: normalizedRemoteEndpointUrl,
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
      remoteToken: profileInput.mode === WorkspaceMode.Remote ? remoteToken : undefined,
    };

    setStatus(AppStatus.Resolving);
    setErrorMessage(null);
    setPickerMessage(null);

    try {
      const connection = await resolver(resolveInput);
      const savedProfilesNext = upsertWorkspaceProfile(profileInput);
      const defaultPath = pagePathFromPageId(localStoreState.settings.defaultPage);

      setSavedProfiles(savedProfilesNext);
      setActiveWorkspaceSession({
        workspaceId: profileInput.workspaceId,
        connection,
      });
      setSelectionState(createEmptySharedSelectionState());
      setActionState({
        label: "Workspace Opened",
        status: ActionResultStatus.Success,
        message: "Select an item from inbox and run actions from Action Center.",
      });
      setRemoteToken("");
      setStatus(AppStatus.Resolved);
      navigate(defaultPath, { replace: true });
    } catch (error) {
      const message = error instanceof Error ? error.message : "Unknown resolution error.";
      setErrorMessage(message);
      setStatus(AppStatus.Error);
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
    setSelectionState(createEmptySharedSelectionState());
    setActionState({
      label: "Awaiting action",
      status: ActionResultStatus.Idle,
      message: "Select an item and run an action.",
    });
    setStatus(AppStatus.Idle);
    setErrorMessage(null);
    setPickerMessage("Choose a workspace profile or open one manually.");
    navigate("/", { replace: true });
  }

  function renderWorkspacePicker() {
    const isRemoteMode = mode === WorkspaceMode.Remote;

    return (
      <main className="app-shell workspace-picker-shell">
        <header className="app-topbar">
          <div>
            <p className="app-eyebrow">DEXDEX DESKTOP</p>
            <h1>Workspace Picker</h1>
            <p className="note">Choose a workspace to enter the product UI.</p>
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
                  <button type="button" className="secondary-button" onClick={handleSaveProfile}>
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

    switch (activePage.id) {
      case DexDexPageId.Projects:
        return (
          <ProjectsPage
            workspaceId={activeWorkspaceSession.workspaceId}
            selection={selectionState}
            onSelectionChange={(patch) =>
              setSelectionState((previous) => updateSelectionState(previous, patch))
            }
          />
        );
      case DexDexPageId.Threads:
        return (
          <ThreadsPage
            workspaceId={activeWorkspaceSession.workspaceId}
            selection={selectionState}
            onSelectionChange={(patch) =>
              setSelectionState((previous) => updateSelectionState(previous, patch))
            }
          />
        );
      case DexDexPageId.Review:
        return (
          <ReviewPage
            workspaceId={activeWorkspaceSession.workspaceId}
            selection={selectionState}
            onSelectionChange={(patch) =>
              setSelectionState((previous) => updateSelectionState(previous, patch))
            }
          />
        );
      case DexDexPageId.Worktrees:
        return (
          <WorktreesPage
            workspaceId={activeWorkspaceSession.workspaceId}
            selection={selectionState}
            onSelectionChange={(patch) =>
              setSelectionState((previous) => updateSelectionState(previous, patch))
            }
            logger={logger}
          />
        );
      case DexDexPageId.Automations:
        return (
          <AutomationsPage
            localStoreState={localStoreState}
            updateLocalStore={updateLocalStore}
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
        return null;
    }
  }

  if (!activeWorkspaceSession) {
    return renderWorkspacePicker();
  }

  return (
    <ConnectQueryProvider
      endpointUrl={activeWorkspaceSession.connection.endpointUrl}
      bearerToken={activeWorkspaceSession.connection.token}
    >
      <main className={`app-shell panel-mode-${panelMode}`}>
        <header className="app-topbar">
          <div>
            <p className="app-eyebrow">DEXDEX DESKTOP</p>
            <h1>DexDex Product Desktop</h1>
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

          <section className="workspace-main" aria-label="Workspace main panel">
            {renderWorkspaceSurface()}
          </section>

          <ActionCenter
            activePage={activePage}
            workspaceId={activeWorkspaceSession.workspaceId}
            connection={activeWorkspaceSession.connection}
            selection={selectionState}
            actionState={actionState}
            onActionStateChange={setActionState}
            onSelectionChange={(patch) =>
              setSelectionState((previous) => updateSelectionState(previous, patch))
            }
          />
        </div>
      </main>
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
