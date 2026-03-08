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

// ── Types ──────────────────────────────────────────────────

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

// ── Constants ──────────────────────────────────────────────

const defaultPagePath = "/threads";
const defaultRemoteEndpointUrl = "http://127.0.0.1:7878";
const defaultListPageSize = 50;
const maxStreamEvents = 120;

// ── Utility functions ──────────────────────────────────────

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
  return { ...previous, ...patch };
}

function unitTaskDotClass(status: number): string {
  switch (status) {
    case UnitTaskStatus.IN_PROGRESS: return "dot-running";
    case UnitTaskStatus.COMPLETED: return "dot-completed";
    case UnitTaskStatus.FAILED: return "dot-failed";
    case UnitTaskStatus.ACTION_REQUIRED: return "dot-action-required";
    case UnitTaskStatus.BLOCKED: return "dot-warning";
    case UnitTaskStatus.CANCELLED: return "dot-cancelled";
    default: return "dot-pending";
  }
}

function subTaskDotClass(status: number): string {
  switch (status) {
    case SubTaskStatus.IN_PROGRESS: return "dot-running";
    case SubTaskStatus.COMPLETED: return "dot-completed";
    case SubTaskStatus.FAILED: return "dot-failed";
    case SubTaskStatus.WAITING_FOR_PLAN_APPROVAL: return "dot-waiting";
    case SubTaskStatus.WAITING_FOR_USER_INPUT: return "dot-action-required";
    case SubTaskStatus.CANCELLED: return "dot-cancelled";
    default: return "dot-pending";
  }
}

function sessionDotClass(status: number): string {
  switch (status) {
    case AgentSessionStatus.RUNNING: return "dot-running";
    case AgentSessionStatus.COMPLETED: return "dot-completed";
    case AgentSessionStatus.FAILED: return "dot-failed";
    case AgentSessionStatus.WAITING_FOR_INPUT: return "dot-waiting";
    case AgentSessionStatus.STARTING: return "dot-pending";
    case AgentSessionStatus.CANCELLED: return "dot-cancelled";
    default: return "dot-default";
  }
}

function prDotClass(status: number): string {
  switch (status) {
    case PrStatus.OPEN: return "dot-open";
    case PrStatus.APPROVED: return "dot-approved";
    case PrStatus.MERGED: return "dot-merged";
    case PrStatus.CHANGES_REQUESTED: return "dot-changes-requested";
    case PrStatus.CLOSED: return "dot-closed";
    case PrStatus.CI_FAILED: return "dot-ci-failed";
    default: return "dot-default";
  }
}

// ── Stream cache updater ───────────────────────────────────

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

// ══════════════════════════════════════════════════════════
//  Page Components
// ══════════════════════════════════════════════════════════

// ── Threads Page ───────────────────────────────────────────

function ThreadsPage({
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

  const [threadTab, setThreadTab] = useState<"detail" | "sessions" | "stream">("detail");
  const [streamStatus, setStreamStatus] = useState<"idle" | "running" | "stopped" | "error">("idle");
  const [streamError, setStreamError] = useState<string | null>(null);
  const [streamEvents, setStreamEvents] = useState<StreamWorkspaceEventsResponse[]>([]);
  const streamAbortControllerRef = useRef<AbortController | null>(null);

  const subTasksQuery = useQuery(
    listSubTasks,
    {
      workspaceId,
      unitTaskId: selection.selectedUnitTaskId ?? "",
      status: SubTaskStatus.UNSPECIFIED,
      pageSize: defaultListPageSize,
      pageToken: "",
    },
    { enabled: selection.selectedUnitTaskId !== null },
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
      ? { workspaceId, subTaskId: selection.selectedSubTaskId }
      : undefined,
    { enabled: selection.selectedSubTaskId !== null },
  );

  const selectedSessionOutputQuery = useQuery(
    getSessionOutput,
    selection.selectedSessionId
      ? { workspaceId, sessionId: selection.selectedSessionId }
      : undefined,
    { enabled: selection.selectedSessionId !== null },
  );

  useEffect(() => {
    if (selection.selectedSubTaskId || !subTasksQuery.data?.items.length) return;
    onSelectionChange({ selectedSubTaskId: subTasksQuery.data.items[0].subTaskId });
  }, [onSelectionChange, selection.selectedSubTaskId, subTasksQuery.data?.items]);

  useEffect(() => {
    if (selection.selectedSessionId || !sessionListQuery.data?.items.length) return;
    onSelectionChange({ selectedSessionId: sessionListQuery.data.items[0].sessionId });
  }, [onSelectionChange, selection.selectedSessionId, sessionListQuery.data?.items]);

  useEffect(() => {
    return () => {
      streamAbortControllerRef.current?.abort();
      streamAbortControllerRef.current = null;
    };
  }, []);

  async function startStream() {
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
        setStreamEvents((prev) => [event, ...prev].slice(0, maxStreamEvents));
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
    streamAbortControllerRef.current?.abort();
    streamAbortControllerRef.current = null;
    setStreamStatus("stopped");
  }

  return (
    <>
      <div className="tab-bar">
        <button
          type="button"
          className={`tab-item ${threadTab === "detail" ? "tab-item-active" : ""}`}
          onClick={() => setThreadTab("detail")}
        >
          Detail
        </button>
        <button
          type="button"
          className={`tab-item ${threadTab === "sessions" ? "tab-item-active" : ""}`}
          onClick={() => setThreadTab("sessions")}
        >
          Sessions
        </button>
        <button
          type="button"
          className={`tab-item ${threadTab === "stream" ? "tab-item-active" : ""}`}
          onClick={() => setThreadTab("stream")}
        >
          Live Stream
          {streamStatus === "running" ? (
            <span style={{ marginLeft: 6, width: 6, height: 6, borderRadius: "50%", background: "var(--green)", display: "inline-block" }} />
          ) : null}
        </button>
      </div>

      <div className="content-body">
        {threadTab === "detail" ? (
          <>
            <div className="detail-card">
              <div className="detail-card-header">
                <h3>Sub Tasks</h3>
                {selection.selectedUnitTaskId ? (
                  <span className="badge badge-muted">{selection.selectedUnitTaskId}</span>
                ) : null}
              </div>
              <div className="detail-card-body">
                {!selection.selectedUnitTaskId ? (
                  <p className="empty-state">Select a thread from the sidebar.</p>
                ) : subTasksQuery.isPending ? (
                  <p className="text-muted text-sm">Loading sub tasks...</p>
                ) : subTasksQuery.error ? (
                  <p style={{ color: "var(--red)", fontSize: 13 }}>
                    {describeConnectError(subTasksQuery.error, "Failed to load sub tasks.")}
                  </p>
                ) : subTasksQuery.data?.items.length ? (
                  <ul className="item-list">
                    {subTasksQuery.data.items.map((subTask) => (
                      <li key={subTask.subTaskId}>
                        <button
                          type="button"
                          className={`item-row ${selection.selectedSubTaskId === subTask.subTaskId ? "item-row-active" : ""}`}
                          onClick={() => onSelectionChange({
                            selectedSubTaskId: subTask.subTaskId,
                            selectedUnitTaskId: subTask.unitTaskId,
                          })}
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
                ) : (
                  <p className="empty-state">No sub tasks found.</p>
                )}
              </div>
            </div>

            {selectedSubTaskQuery.data?.subTask ? (
              <div className="detail-card">
                <div className="detail-card-header">
                  <h3>Sub Task Detail</h3>
                  <span className={`badge ${subTaskDotClass(selectedSubTaskQuery.data.subTask.status) === "dot-running" ? "badge-green" : subTaskDotClass(selectedSubTaskQuery.data.subTask.status) === "dot-failed" ? "badge-red" : "badge-muted"}`}>
                    {enumLabel(SubTaskStatus, selectedSubTaskQuery.data.subTask.status)}
                  </span>
                </div>
                <div className="detail-card-body">
                  <div className="kv-grid">
                    <span className="kv-key">Sub task</span>
                    <span className="kv-value">{selectedSubTaskQuery.data.subTask.subTaskId}</span>
                    <span className="kv-key">Unit task</span>
                    <span className="kv-value">{selectedSubTaskQuery.data.subTask.unitTaskId}</span>
                    <span className="kv-key">Status</span>
                    <span className="kv-value">{enumLabel(SubTaskStatus, selectedSubTaskQuery.data.subTask.status)}</span>
                  </div>
                </div>
              </div>
            ) : null}

            {selectedSessionOutputQuery.data?.events.length ? (
              <div className="detail-card">
                <div className="detail-card-header">
                  <h3>Session Output</h3>
                </div>
                <div className="detail-card-body">
                  <div className="stream-list">
                    {selectedSessionOutputQuery.data.events.map((event, index) => (
                      <div key={`${event.sessionId}-${index}`} className="stream-item">
                        <div className="stream-item-header">
                          <span>{enumLabel(SessionOutputKind, event.kind)}</span>
                          <span>{event.isTerminal ? "terminal" : "active"}</span>
                        </div>
                        <pre className="stream-item-body">{event.body}</pre>
                      </div>
                    ))}
                  </div>
                </div>
              </div>
            ) : null}
          </>
        ) : null}

        {threadTab === "sessions" ? (
          <div className="detail-card">
            <div className="detail-card-header">
              <h3>Session Runs</h3>
            </div>
            <div className="detail-card-body">
              {sessionListQuery.isPending ? (
                <p className="text-muted text-sm">Loading sessions...</p>
              ) : sessionListQuery.error ? (
                <p style={{ color: "var(--red)", fontSize: 13 }}>
                  {describeConnectError(sessionListQuery.error, "Failed to load sessions.")}
                </p>
              ) : sessionListQuery.data?.items.length ? (
                <ul className="item-list">
                  {sessionListQuery.data.items.map((session) => (
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
              ) : (
                <p className="empty-state">No sessions found.</p>
              )}
            </div>
          </div>
        ) : null}

        {threadTab === "stream" ? (
          <>
            <div style={{ display: "flex", gap: 8, marginBottom: 12 }}>
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
              <span className="text-muted text-sm" style={{ display: "flex", alignItems: "center" }}>
                {streamStatus.toUpperCase()}
              </span>
            </div>

            {streamError ? (
              <p style={{ color: "var(--red)", fontSize: 13, marginBottom: 12 }}>{streamError}</p>
            ) : null}

            {streamEvents.length > 0 ? (
              <div className="stream-list">
                {streamEvents.map((event) => (
                  <div
                    key={`${event.sequence.toString()}-${event.eventType}`}
                    className="stream-item"
                  >
                    <div className="stream-item-header">
                      <span>#{event.sequence.toString()}</span>
                      <span>{enumLabel(StreamEventType, event.eventType)}</span>
                    </div>
                    <pre className="stream-item-body">{stringifyForUi(event)}</pre>
                  </div>
                ))}
              </div>
            ) : (
              <p className="empty-state">No stream events yet. Press Start Stream to begin.</p>
            )}
          </>
        ) : null}
      </div>
    </>
  );
}

// ── Review Page ────────────────────────────────────────────

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
      ? { workspaceId, prTrackingId: selection.selectedPrTrackingId }
      : undefined,
    { enabled: selection.selectedPrTrackingId !== null },
  );

  const reviewCommentQuery = useQuery(
    listReviewComments,
    selection.selectedPrTrackingId
      ? { workspaceId, prTrackingId: selection.selectedPrTrackingId }
      : undefined,
    { enabled: selection.selectedPrTrackingId !== null },
  );

  const reviewAssistQuery = useQuery(
    listReviewAssistItems,
    selection.selectedUnitTaskId
      ? { workspaceId, unitTaskId: selection.selectedUnitTaskId }
      : undefined,
    { enabled: selection.selectedUnitTaskId !== null },
  );

  useEffect(() => {
    if (selection.selectedPrTrackingId || !pullRequestListQuery.data?.items.length) return;
    onSelectionChange({ selectedPrTrackingId: pullRequestListQuery.data.items[0].prTrackingId });
  }, [onSelectionChange, pullRequestListQuery.data?.items, selection.selectedPrTrackingId]);

  return (
    <div className="content-split">
      <div className="content-list-pane">
        <div style={{ padding: "8px 8px 4px", fontSize: 11, fontWeight: 700, textTransform: "uppercase" as const, letterSpacing: "0.05em", color: "var(--text-muted)" }}>
          Pull Requests
        </div>
        {pullRequestListQuery.isPending ? (
          <p className="empty-state">Loading...</p>
        ) : pullRequestListQuery.error ? (
          <p style={{ color: "var(--red)", fontSize: 13, padding: 12 }}>
            {describeConnectError(pullRequestListQuery.error, "Failed to load pull requests.")}
          </p>
        ) : pullRequestListQuery.data?.items.length ? (
          <ul className="item-list">
            {pullRequestListQuery.data.items.map((pr) => (
              <li key={pr.prTrackingId}>
                <button
                  type="button"
                  className={`item-row ${selection.selectedPrTrackingId === pr.prTrackingId ? "item-row-active" : ""}`}
                  onClick={() => onSelectionChange({ selectedPrTrackingId: pr.prTrackingId })}
                >
                  <span className={`item-row-dot ${prDotClass(pr.status)}`} />
                  <span className="item-row-body">
                    <span className="item-row-title">{pr.prTrackingId}</span>
                    <span className="item-row-sub">{enumLabel(PrStatus, pr.status)}</span>
                  </span>
                </button>
              </li>
            ))}
          </ul>
        ) : (
          <p className="empty-state">No pull requests.</p>
        )}
      </div>

      <div className="content-detail-pane">
        {selectedPullRequestQuery.data?.pullRequest ? (
          <div className="detail-card">
            <div className="detail-card-header">
              <h3>Pull Request</h3>
              <span className={`badge ${prDotClass(selectedPullRequestQuery.data.pullRequest.status) === "dot-open" ? "badge-green" : prDotClass(selectedPullRequestQuery.data.pullRequest.status) === "dot-merged" ? "badge-blue" : "badge-muted"}`}>
                {enumLabel(PrStatus, selectedPullRequestQuery.data.pullRequest.status)}
              </span>
            </div>
            <div className="detail-card-body">
              <div className="kv-grid">
                <span className="kv-key">PR tracking ID</span>
                <span className="kv-value">{selectedPullRequestQuery.data.pullRequest.prTrackingId}</span>
                <span className="kv-key">Status</span>
                <span className="kv-value">{enumLabel(PrStatus, selectedPullRequestQuery.data.pullRequest.status)}</span>
              </div>
            </div>
          </div>
        ) : (
          <p className="empty-state">Select a pull request to view details.</p>
        )}

        {reviewAssistQuery.data?.items.length ? (
          <div className="detail-card">
            <div className="detail-card-header">
              <h3>Review Assist</h3>
            </div>
            <div className="detail-card-body">
              <ul className="item-list">
                {reviewAssistQuery.data.items.map((item) => (
                  <li key={item.reviewAssistId}>
                    <div className="item-row">
                      <span className="item-row-body">
                        <span className="item-row-title">{item.reviewAssistId}</span>
                        <span className="item-row-sub">{item.body}</span>
                      </span>
                    </div>
                  </li>
                ))}
              </ul>
            </div>
          </div>
        ) : null}

        {reviewCommentQuery.data?.comments.length ? (
          <div className="detail-card">
            <div className="detail-card-header">
              <h3>Review Comments</h3>
            </div>
            <div className="detail-card-body">
              <ul className="item-list">
                {reviewCommentQuery.data.comments.map((comment) => (
                  <li key={comment.reviewCommentId}>
                    <div className="item-row">
                      <span className="item-row-body">
                        <span className="item-row-title">{comment.reviewCommentId}</span>
                        <span className="item-row-sub">{comment.body}</span>
                      </span>
                    </div>
                  </li>
                ))}
              </ul>
            </div>
          </div>
        ) : null}
      </div>
    </div>
  );
}

// ── Automations Page ───────────────────────────────────────

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
      <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 16 }}>
        <div>
          <div style={{ fontSize: 11, fontWeight: 700, textTransform: "uppercase" as const, letterSpacing: "0.05em", color: "var(--text-muted)", marginBottom: 10 }}>
            Automation Queue
          </div>
          {localStoreState.automations.length === 0 ? (
            <p className="empty-state">No automations configured.</p>
          ) : (
            localStoreState.automations.map((automation) => (
              <div key={automation.id} className="automation-item">
                <div className="automation-item-header">
                  <div>
                    <div className="automation-item-name">
                      {automation.name}
                      {!automation.enabled ? (
                        <span className="badge badge-muted" style={{ marginLeft: 8 }}>Disabled</span>
                      ) : null}
                    </div>
                    <div className="automation-item-schedule">{automation.schedule}</div>
                  </div>
                </div>
                <div className="automation-item-actions">
                  <button
                    type="button"
                    className="btn btn-secondary btn-sm"
                    onClick={() =>
                      updateLocalStore((c) => ({
                        ...c,
                        automations: c.automations.map((a) =>
                          a.id === automation.id ? { ...a, enabled: !a.enabled, lastRunAt: a.lastRunAt } : a,
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
                      updateLocalStore((c) => ({
                        ...c,
                        automations: c.automations.filter((a) => a.id !== automation.id),
                        lastSelectedAutomationId:
                          c.lastSelectedAutomationId === automation.id ? null : c.lastSelectedAutomationId,
                      }))
                    }
                  >
                    Delete
                  </button>
                </div>
              </div>
            ))
          )}
        </div>

        <div>
          <div className="detail-card">
            <div className="detail-card-header">
              <h3>Create Automation</h3>
            </div>
            <div className="detail-card-body">
              <form onSubmit={handleCreate}>
                <div className="form-group">
                  <label className="form-label" htmlFor="auto-name">Name</label>
                  <input
                    id="auto-name"
                    className="form-input"
                    value={newName}
                    onChange={(e) => setNewName(e.target.value)}
                    placeholder="Nightly Stream Health"
                  />
                </div>
                <div className="form-group">
                  <label className="form-label" htmlFor="auto-schedule">Schedule</label>
                  <input
                    id="auto-schedule"
                    className="form-input"
                    value={newSchedule}
                    onChange={(e) => setNewSchedule(e.target.value)}
                    placeholder="Every weekday 09:00"
                  />
                </div>
                <div className="form-actions">
                  <button type="submit" className="btn btn-primary btn-sm">Create</button>
                </div>
              </form>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}

// ── Settings Page ──────────────────────────────────────────

function SettingsPage({
  localStoreState,
  updateLocalStore,
}: {
  localStoreState: DesktopLocalStoreState;
  updateLocalStore: UpdateLocalStore;
}) {
  const [envName, setEnvName] = useState("");
  const [envEndpoint, setEnvEndpoint] = useState("http://127.0.0.1:7878");

  function handleCreateEnv(event: FormEvent<HTMLFormElement>) {
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
          { id, name, endpointUrl, health: LocalEnvironmentHealth.Unknown, lastCheckedAt: null, lastErrorMessage: null },
        ],
        lastSelectedEnvironmentId: id,
      };
    });
    setEnvName("");
  }

  function runDiagnostics(environmentId: string) {
    updateLocalStore((current) => ({
      ...current,
      localEnvironments: current.localEnvironments.map((env) => {
        if (env.id !== environmentId) return env;
        const reachable = env.endpointUrl.startsWith("http://") || env.endpointUrl.startsWith("https://");
        return {
          ...env,
          health: reachable ? LocalEnvironmentHealth.Healthy : LocalEnvironmentHealth.Unreachable,
          lastCheckedAt: new Date().toISOString(),
          lastErrorMessage: reachable ? null : "endpoint must use http/https",
        };
      }),
      lastSelectedEnvironmentId: environmentId,
    }));
  }

  return (
    <div className="content-body">
      {/* Preferences */}
      <div className="settings-section">
        <div className="settings-section-header">Preferences</div>
        <div className="settings-section-body">
          <div className="settings-row">
            <div>
              <div className="settings-row-label">Default Page</div>
              <div className="settings-row-description">Which page to open when entering a workspace.</div>
            </div>
            <select
              className="form-select"
              style={{ width: 160 }}
              value={localStoreState.settings.defaultPage}
              onChange={(e) =>
                updateLocalStore((c) => ({
                  ...c,
                  settings: { ...c.settings, defaultPage: e.target.value as DexDexPageId },
                }))
              }
            >
              {dexdexPageDefinitions.map((p) => (
                <option key={p.id} value={p.id}>{p.label}</option>
              ))}
            </select>
          </div>

          <div className="settings-row">
            <div>
              <div className="settings-row-label">Compact Mode</div>
              <div className="settings-row-description">Reduce spacing and font sizes.</div>
            </div>
            <input
              type="checkbox"
              checked={localStoreState.settings.compactMode}
              onChange={(e) =>
                updateLocalStore((c) => ({
                  ...c,
                  settings: { ...c.settings, compactMode: e.target.checked },
                }))
              }
              style={{ width: 18, height: 18, accentColor: "var(--green)" }}
            />
          </div>

          <div className="settings-row">
            <div>
              <div className="settings-row-label">Auto Start Stream</div>
              <div className="settings-row-description">Automatically start live stream on Threads page.</div>
            </div>
            <input
              type="checkbox"
              checked={localStoreState.settings.autoStartStream}
              onChange={(e) =>
                updateLocalStore((c) => ({
                  ...c,
                  settings: { ...c.settings, autoStartStream: e.target.checked },
                }))
              }
              style={{ width: 18, height: 18, accentColor: "var(--green)" }}
            />
          </div>
        </div>
      </div>

      {/* Local Environments */}
      <div className="settings-section">
        <div className="settings-section-header">Local Environments</div>
        <div className="settings-section-body">
          {localStoreState.localEnvironments.map((env) => (
            <div key={env.id} className="env-item">
              <div className="env-item-name">{env.name}</div>
              <div className="env-item-meta">{env.endpointUrl}</div>
              <div className="env-item-meta">
                Health: {env.health} · Last checked: {env.lastCheckedAt ? new Date(env.lastCheckedAt).toLocaleString() : "never"}
              </div>
              <div className="env-item-actions">
                <button type="button" className="btn btn-secondary btn-sm" onClick={() => runDiagnostics(env.id)}>
                  Diagnostics
                </button>
                <button
                  type="button"
                  className="btn btn-danger btn-sm"
                  onClick={() =>
                    updateLocalStore((c) => ({
                      ...c,
                      localEnvironments: c.localEnvironments.filter((e) => e.id !== env.id),
                      lastSelectedEnvironmentId: c.lastSelectedEnvironmentId === env.id ? null : c.lastSelectedEnvironmentId,
                    }))
                  }
                >
                  Remove
                </button>
              </div>
            </div>
          ))}

          <form onSubmit={handleCreateEnv} style={{ marginTop: 16 }}>
            <div style={{ display: "flex", gap: 8 }}>
              <input className="form-input" value={envName} onChange={(e) => setEnvName(e.target.value)} placeholder="Name" />
              <input className="form-input" value={envEndpoint} onChange={(e) => setEnvEndpoint(e.target.value)} placeholder="Endpoint URL" />
              <button type="submit" className="btn btn-primary btn-sm" style={{ flexShrink: 0 }}>Add</button>
            </div>
          </form>
        </div>
      </div>
    </div>
  );
}

// ── Action Center (Right Panel) ────────────────────────────

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

  async function handleSubmitPlanDecision(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!selection.selectedSubTaskId) {
      onActionStateChange({ label: "Plan Decision", status: ActionResultStatus.Error, message: "Select a sub task first." });
      return;
    }
    if (planDecision === PlanDecision.REVISE && planRevisionNote.trim().length === 0) {
      onActionStateChange({ label: "Plan Decision", status: ActionResultStatus.Error, message: "Revision note required." });
      return;
    }

    onActionStateChange({ label: "Plan Decision", status: ActionResultStatus.Pending, message: "Submitting..." });

    try {
      const response = await taskClient.submitPlanDecision({
        workspaceId,
        subTaskId: selection.selectedSubTaskId,
        decision: planDecision,
        revisionNote: planDecision === PlanDecision.REVISE ? planRevisionNote : "",
      });
      onSelectionChange({
        selectedSubTaskId: response.createdSubTask?.subTaskId ?? response.updatedSubTask?.subTaskId ?? selection.selectedSubTaskId,
        selectedUnitTaskId: response.updatedSubTask?.unitTaskId ?? selection.selectedUnitTaskId,
      });
      await queryClient.invalidateQueries();
      onActionStateChange({ label: "Plan Decision", status: ActionResultStatus.Success, message: "Decision submitted." });
    } catch (error) {
      onActionStateChange({ label: "Plan Decision", status: ActionResultStatus.Error, message: describeConnectError(error, "Failed.") });
    }
  }

  async function handleRunSessionAdapter(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!selection.selectedUnitTaskId || !selection.selectedSubTaskId || !selection.selectedSessionId) {
      onActionStateChange({ label: "Session Adapter", status: ActionResultStatus.Error, message: "Select unit task, sub task, and session." });
      return;
    }
    if (runInputMode === "raw" && runRawJsonlInput.trim().length === 0) {
      onActionStateChange({ label: "Session Adapter", status: ActionResultStatus.Error, message: "Raw JSONL required." });
      return;
    }

    onActionStateChange({ label: "Session Adapter", status: ActionResultStatus.Pending, message: "Running..." });

    try {
      await taskClient.runSubTaskSessionAdapter({
        workspaceId,
        unitTaskId: selection.selectedUnitTaskId,
        subTaskId: selection.selectedSubTaskId,
        sessionId: selection.selectedSessionId,
        cliType: runCliType,
        input: runInputMode === "preset"
          ? { case: "fixturePreset", value: runFixturePreset }
          : { case: "rawJsonl", value: runRawJsonlInput },
      });
      await queryClient.invalidateQueries();
      onActionStateChange({ label: "Session Adapter", status: ActionResultStatus.Success, message: "Completed." });
    } catch (error) {
      onActionStateChange({ label: "Session Adapter", status: ActionResultStatus.Error, message: describeConnectError(error, "Failed.") });
    }
  }

  return (
    <aside className="right-panel">
      {/* Status */}
      <div className="right-panel-section">
        <div className="right-panel-title">Status</div>
        <div style={{ display: "flex", alignItems: "center", gap: 8, fontSize: 13 }}>
          <span className={`topbar-status topbar-status-${actionState.status === ActionResultStatus.Success ? "resolved" : actionState.status === ActionResultStatus.Error ? "error" : actionState.status === ActionResultStatus.Pending ? "resolving" : "idle"}`} />
          <span>{actionState.label}</span>
        </div>
        <p className="text-muted text-sm mt-2">{actionState.message}</p>
      </div>

      {/* Selection */}
      <div className="right-panel-section">
        <div className="right-panel-title">Selection</div>
        <div className="kv-grid" style={{ fontSize: 12 }}>
          <span className="kv-key">Unit task</span>
          <span className="kv-value">{selection.selectedUnitTaskId ?? "—"}</span>
          <span className="kv-key">Sub task</span>
          <span className="kv-value">{selection.selectedSubTaskId ?? "—"}</span>
          <span className="kv-key">Session</span>
          <span className="kv-value">{selection.selectedSessionId ?? "—"}</span>
          <span className="kv-key">PR</span>
          <span className="kv-value">{selection.selectedPrTrackingId ?? "—"}</span>
        </div>
      </div>

      {/* Plan Decision */}
      {activePage?.id === DexDexPageId.Threads ? (
        <div className="right-panel-section">
          <div className="right-panel-title">Plan Decision</div>
          <form onSubmit={handleSubmitPlanDecision}>
            <div className="form-group">
              <label className="form-label" htmlFor="rp-plan-decision">Decision</label>
              <select
                id="rp-plan-decision"
                className="form-select"
                value={planDecision}
                onChange={(e) => setPlanDecision(Number(e.target.value) as PlanDecision)}
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
                  onChange={(e) => setPlanRevisionNote(e.target.value)}
                  rows={3}
                />
              </div>
            ) : null}
            <div className="form-actions">
              <button type="submit" className="btn btn-primary btn-sm">Submit</button>
            </div>
          </form>
        </div>
      ) : null}

      {/* Session Adapter */}
      {activePage?.id === DexDexPageId.Threads ? (
        <div className="right-panel-section">
          <div className="right-panel-title">Session Adapter</div>
          <form onSubmit={handleRunSessionAdapter}>
            <div className="form-group">
              <label className="form-label" htmlFor="rp-cli-type">CLI Type</label>
              <select
                id="rp-cli-type"
                className="form-select"
                value={runCliType}
                onChange={(e) => setRunCliType(Number(e.target.value) as AgentCliType)}
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
                onChange={(e) => setRunInputMode(e.target.value as "preset" | "raw")}
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
                  onChange={(e) => setRunFixturePreset(Number(e.target.value) as SessionAdapterFixturePreset)}
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
                  onChange={(e) => setRunRawJsonlInput(e.target.value)}
                  rows={4}
                />
              </div>
            )}
            <div className="form-actions">
              <button type="submit" className="btn btn-primary btn-sm">Run</button>
            </div>
          </form>
        </div>
      ) : null}

      {/* Connection */}
      <div className="right-panel-section">
        <div className="right-panel-title">Connection</div>
        <div className="kv-grid" style={{ fontSize: 12 }}>
          <span className="kv-key">Workspace</span>
          <span className="kv-value">{workspaceId}</span>
          <span className="kv-key">Mode</span>
          <span className="kv-value">{connection.mode}</span>
          <span className="kv-key">Endpoint</span>
          <span className="kv-value">{connection.endpointUrl}</span>
          <span className="kv-key">Source</span>
          <span className="kv-value">{connection.endpointSource}</span>
        </div>
      </div>
    </aside>
  );
}

// ══════════════════════════════════════════════════════════
//  Workspace Picker
// ══════════════════════════════════════════════════════════

function WorkspacePicker({
  status,
  errorMessage,
  pickerMessage,
  mode,
  workspaceIdInput,
  remoteEndpointUrl,
  remoteToken,
  savedProfiles,
  onModeChange,
  onWorkspaceIdChange,
  onRemoteEndpointChange,
  onRemoteTokenChange,
  onOpenWorkspace,
  onSaveProfile,
  onEditProfile,
  onDeleteProfile,
}: {
  status: AppStatus;
  errorMessage: string | null;
  pickerMessage: string | null;
  mode: WorkspaceMode;
  workspaceIdInput: string;
  remoteEndpointUrl: string;
  remoteToken: string;
  savedProfiles: SavedWorkspaceProfile[];
  onModeChange: (mode: WorkspaceMode) => void;
  onWorkspaceIdChange: (value: string) => void;
  onRemoteEndpointChange: (value: string) => void;
  onRemoteTokenChange: (value: string) => void;
  onOpenWorkspace: (event: FormEvent<HTMLFormElement>) => void;
  onSaveProfile: () => void;
  onEditProfile: (profile: SavedWorkspaceProfile) => void;
  onDeleteProfile: (profile: SavedWorkspaceProfile) => void;
}) {
  const isRemoteMode = mode === WorkspaceMode.Remote;

  return (
    <main className="picker-shell">
      <div className="picker-container">
        <div className="picker-header">
          <div className="picker-logo">DexDex</div>
          <div className="picker-subtitle">Select a workspace to get started.</div>
        </div>

        {/* Recent Profiles */}
        {savedProfiles.length > 0 ? (
          <div className="picker-card">
            <div className="picker-card-header">Recent Workspaces</div>
            <div className="picker-card-body">
              {savedProfiles.map((profile) => (
                <div key={profile.workspaceId} className="picker-profile" onClick={() => onEditProfile(profile)}>
                  <div className="picker-profile-info">
                    <div className="picker-profile-name">{profile.workspaceId}</div>
                    <div className="picker-profile-meta">
                      {profile.mode} · {profile.remoteEndpointUrl ?? "managed-local"}
                    </div>
                  </div>
                  <div className="picker-profile-actions" onClick={(e) => e.stopPropagation()}>
                    <button type="button" className="btn btn-ghost btn-sm" onClick={() => onDeleteProfile(profile)}>
                      Remove
                    </button>
                  </div>
                </div>
              ))}
            </div>
          </div>
        ) : null}

        {/* Open Workspace Form */}
        <div className="picker-card">
          <div className="picker-card-header">Open Workspace</div>
          <div className="picker-card-body">
            <form onSubmit={onOpenWorkspace}>
              <div className="form-group">
                <label className="form-label" htmlFor="ws-id">Workspace ID</label>
                <input
                  id="ws-id"
                  className="form-input"
                  value={workspaceIdInput}
                  onChange={(e) => onWorkspaceIdChange(e.target.value)}
                  placeholder="workspace-1"
                />
              </div>

              <div className="form-group">
                <label className="form-label" htmlFor="ws-mode">Mode</label>
                <select
                  id="ws-mode"
                  className="form-select"
                  value={mode}
                  onChange={(e) => onModeChange(e.target.value as WorkspaceMode)}
                >
                  {modeOptions().map((opt) => (
                    <option key={opt.value} value={opt.value}>{opt.label}</option>
                  ))}
                </select>
              </div>

              <div className="form-group">
                <label className="form-label" htmlFor="ws-endpoint">Remote Endpoint</label>
                <input
                  id="ws-endpoint"
                  className="form-input"
                  type="url"
                  value={remoteEndpointUrl}
                  disabled={!isRemoteMode}
                  onChange={(e) => onRemoteEndpointChange(e.target.value)}
                  placeholder="https://dexdex.example/rpc"
                />
              </div>

              <div className="form-group">
                <label className="form-label" htmlFor="ws-token">Token (not persisted)</label>
                <input
                  id="ws-token"
                  className="form-input"
                  type="password"
                  value={remoteToken}
                  disabled={!isRemoteMode}
                  onChange={(e) => onRemoteTokenChange(e.target.value)}
                />
              </div>

              <div className="form-actions">
                <button type="submit" className="btn btn-primary" disabled={status === AppStatus.Resolving}>
                  {status === AppStatus.Resolving ? "Connecting..." : "Connect"}
                </button>
                <button type="button" className="btn btn-secondary" onClick={onSaveProfile}>
                  Save Profile
                </button>
              </div>
            </form>

            {pickerMessage ? <p className="picker-message">{pickerMessage}</p> : null}
            {errorMessage ? <p className="picker-error">{errorMessage}</p> : null}
          </div>
        </div>
      </div>
    </main>
  );
}

// ══════════════════════════════════════════════════════════
//  Desktop Layout (rendered inside ConnectQueryProvider)
// ══════════════════════════════════════════════════════════

function DesktopLayout({
  workspaceSession,
  status,
  activePage,
  selectionState,
  actionState,
  localStoreState,
  logger,
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
  onSwitchWorkspace: () => void;
  onSelectionChange: (patch: Partial<SharedSelectionState>) => void;
  onActionStateChange: (next: ActionCenterState) => void;
  updateLocalStore: UpdateLocalStore;
}) {
  const overviewQuery = useQuery(getWorkspaceOverview, {
    workspaceId: workspaceSession.workspaceId,
  });

  const unitTasksQuery = useQuery(listUnitTasks, {
    workspaceId: workspaceSession.workspaceId,
    status: UnitTaskStatus.UNSPECIFIED,
    pageSize: defaultListPageSize,
    pageToken: "",
  });

  useEffect(() => {
    if (selectionState.selectedUnitTaskId || !unitTasksQuery.data?.items.length) return;
    onSelectionChange({ selectedUnitTaskId: unitTasksQuery.data.items[0].unitTaskId });
  }, [onSelectionChange, selectionState.selectedUnitTaskId, unitTasksQuery.data?.items]);

  function renderPageContent() {
    if (!activePage) return <p className="empty-state">Unknown page.</p>;
    switch (activePage.id) {
      case DexDexPageId.Threads:
        return (
          <ThreadsPage
            workspaceId={workspaceSession.workspaceId}
            selection={selectionState}
            onSelectionChange={onSelectionChange}
            logger={logger}
          />
        );
      case DexDexPageId.Review:
        return (
          <ReviewPage
            workspaceId={workspaceSession.workspaceId}
            selection={selectionState}
            onSelectionChange={onSelectionChange}
          />
        );
      case DexDexPageId.Automations:
        return (
          <AutomationsPage
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
      {/* ── Top Bar ── */}
      <header className="topbar">
        <div className="topbar-left">
          <span className="topbar-logo">DexDex</span>
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
        {/* ── Sidebar ── */}
        <aside className="sidebar">
          <nav className="sidebar-nav">
            {dexdexPageDefinitions.map((page) => (
              <NavLink
                key={page.id}
                to={page.path}
                className={({ isActive }) =>
                  `sidebar-item ${isActive ? "sidebar-item-active" : ""}`
                }
              >
                {page.label}
              </NavLink>
            ))}
          </nav>

          {/* Workspace stats */}
          <div className="sidebar-stats">
            {overviewQuery.data?.overview ? (
              <>
                <div className="sidebar-stat">
                  <span className="sidebar-stat-value">{overviewQuery.data.overview.totalUnitTaskCount}</span>
                  <span className="sidebar-stat-label">Tasks</span>
                </div>
                <div className="sidebar-stat">
                  <span className="sidebar-stat-value">{overviewQuery.data.overview.activeSessionCount}</span>
                  <span className="sidebar-stat-label">Sessions</span>
                </div>
              </>
            ) : null}
          </div>

          {/* Thread list */}
          <div className="sidebar-threads">
            <div className="sidebar-section-label">Unit Tasks</div>
            {unitTasksQuery.isPending ? (
              <div className="sidebar-empty">Loading...</div>
            ) : unitTasksQuery.error ? (
              <div className="sidebar-empty" style={{ color: "var(--red)" }}>
                {describeConnectError(unitTasksQuery.error, "Failed to load.")}
              </div>
            ) : unitTasksQuery.data?.items.length ? (
              <ul className="sidebar-thread-list">
                {unitTasksQuery.data.items.map((task) => (
                  <li key={task.unitTaskId}>
                    <button
                      type="button"
                      className={`sidebar-thread-item ${selectionState.selectedUnitTaskId === task.unitTaskId ? "sidebar-thread-item-active" : ""}`}
                      onClick={() => onSelectionChange({ selectedUnitTaskId: task.unitTaskId })}
                    >
                      <span className={`sidebar-thread-dot ${unitTaskDotClass(task.status)}`} />
                      <span className="sidebar-thread-body">
                        <span className="sidebar-thread-title">{task.unitTaskId}</span>
                        <span className="sidebar-thread-sub">{enumLabel(UnitTaskStatus, task.status)}</span>
                      </span>
                    </button>
                  </li>
                ))}
              </ul>
            ) : (
              <div className="sidebar-empty">No unit tasks.</div>
            )}
          </div>

          {/* Footer */}
          <div className="sidebar-footer">
            <span className="text-muted text-xs">{workspaceSession.connection.mode}</span>
          </div>
        </aside>

        {/* ── Main Content ── */}
        <main className="main-content">
          {activePage ? (
            <div className="main-content-header">
              <h2 className="main-content-title">{activePage.label}</h2>
            </div>
          ) : null}
          {renderPageContent()}
        </main>

        {/* ── Right Panel ── */}
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

// ══════════════════════════════════════════════════════════
//  Main Shell
// ══════════════════════════════════════════════════════════

function DexDexShell({
  resolver,
  logger,
}: {
  resolver: ResolveWorkspaceConnection;
  logger: DexDexLogger;
}) {
  const location = useLocation();
  const navigate = useNavigate();

  // ── Workspace state ──
  const [mode, setMode] = useState<WorkspaceMode>(WorkspaceMode.Local);
  const [workspaceIdInput, setWorkspaceIdInput] = useState("");
  const [remoteEndpointUrl, setRemoteEndpointUrl] = useState(defaultRemoteEndpointUrl);
  const [remoteToken, setRemoteToken] = useState("");
  const [status, setStatus] = useState<AppStatus>(AppStatus.Idle);
  const [errorMessage, setErrorMessage] = useState<string | null>(null);
  const [pickerMessage, setPickerMessage] = useState<string | null>(null);
  const [activeWorkspaceSession, setActiveWorkspaceSession] = useState<ActiveWorkspaceSession | null>(null);
  const [savedProfiles, setSavedProfiles] = useState<SavedWorkspaceProfile[]>(() => listSavedWorkspaceProfiles());

  // ── UI state ──
  const [selectionState, setSelectionState] = useState<SharedSelectionState>(createEmptySharedSelectionState());
  const [actionState, setActionState] = useState<ActionCenterState>({
    label: "Ready",
    status: ActionResultStatus.Idle,
    message: "Select an item and run an action.",
  });
  const [localStoreState, setLocalStoreState] = useState<DesktopLocalStoreState>(() => loadDesktopLocalStoreState());

  const activePage = useMemo(() => resolvePageByPath(location.pathname), [location.pathname]);

  // ── Routing ──
  useEffect(() => {
    if (!activeWorkspaceSession) {
      if (location.pathname !== "/") navigate("/", { replace: true });
      return;
    }
    if (location.pathname === "/" || activePage === null) {
      navigate(pagePathFromPageId(localStoreState.settings.defaultPage), { replace: true });
    }
  }, [activePage, activeWorkspaceSession, localStoreState.settings.defaultPage, location.pathname, navigate]);

  useEffect(() => {
    if (!activeWorkspaceSession || !activePage) return;
    logger.info("desktop.page.view", {
      page_id: activePage.id,
      result: "success",
      workspace_id: activeWorkspaceSession.workspaceId,
    });
  }, [activePage, activeWorkspaceSession, logger]);

  // ── Local store helpers ──
  function updateLocalStore(updater: (current: DesktopLocalStoreState) => DesktopLocalStoreState) {
    setLocalStoreState(updateDesktopLocalStoreState(updater));
  }

  // ── Workspace operations ──
  function resolveProfileInputFromForm(actionLabel: string) {
    const workspaceId = workspaceIdInput.trim();
    if (workspaceId.length === 0) {
      setErrorMessage(`${actionLabel}: workspace id is required.`);
      setPickerMessage(null);
      setStatus(AppStatus.Error);
      return null;
    }
    try {
      const normalizedRemoteEndpointUrl = mode === WorkspaceMode.Remote ? normalizeRemoteEndpointUrl(remoteEndpointUrl) : undefined;
      setErrorMessage(null);
      setPickerMessage(null);
      setStatus(AppStatus.Idle);
      return { workspaceId, mode, remoteEndpointUrl: normalizedRemoteEndpointUrl };
    } catch (error) {
      const message = error instanceof Error ? error.message : `${actionLabel}: invalid remote endpoint.`;
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
      setActionState({ label: "Connected", status: ActionResultStatus.Success, message: "Workspace connected." });
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
    setActionState({ label: "Ready", status: ActionResultStatus.Idle, message: "Select an item and run an action." });
    setStatus(AppStatus.Idle);
    setErrorMessage(null);
    setPickerMessage("Choose a workspace or connect manually.");
    navigate("/", { replace: true });
  }

  // ── Render: Workspace Picker ──
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

  // ── Render: Desktop Layout ──
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
        onSwitchWorkspace={handleSwitchWorkspace}
        onSelectionChange={(patch) => setSelectionState((prev) => updateSelectionState(prev, patch))}
        onActionStateChange={setActionState}
        updateLocalStore={updateLocalStore}
      />
    </ConnectQueryProvider>
  );
}

// ══════════════════════════════════════════════════════════
//  App Entry Point
// ══════════════════════════════════════════════════════════

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
