import { Code, ConnectError, createClient } from "@connectrpc/connect";
import { useQuery } from "@connectrpc/connect-query";
import { type FormEvent, useEffect, useMemo, useRef, useState } from "react";
import type { ResolvedWorkspaceConnection } from "../contracts/workspace-connection";
import { getBadgeTheme } from "../gen/v1/dexdex-BadgeThemeService_connectquery";
import { listNotifications } from "../gen/v1/dexdex-NotificationService_connectquery";
import { getPullRequest } from "../gen/v1/dexdex-PrManagementService_connectquery";
import { getRepositoryGroup } from "../gen/v1/dexdex-RepositoryService_connectquery";
import { listReviewAssistItems } from "../gen/v1/dexdex-ReviewAssistService_connectquery";
import { listReviewComments } from "../gen/v1/dexdex-ReviewCommentService_connectquery";
import {
  AgentCliType,
  EventStreamService,
  SessionAdapterFixturePreset,
  StreamEventType,
  TaskService,
  type StreamWorkspaceEventsResponse,
} from "../gen/v1/dexdex_pb";
import { getSessionOutput } from "../gen/v1/dexdex-SessionService_connectquery";
import {
  getSubTask,
  getUnitTask,
} from "../gen/v1/dexdex-TaskService_connectquery";
import { getWorkspace } from "../gen/v1/dexdex-WorkspaceService_connectquery";
import { createDexDexTransport } from "../lib/connect-query-provider";

const HISTORY_LIMIT = 5;
const STREAM_EVENT_HISTORY_LIMIT = 100;

type StreamStatus = "idle" | "running" | "stopped" | "error";
type SessionAdapterInputMode = "preset" | "raw";

type LookupHistory = {
  workspaceId: string[];
  repositoryGroupId: string[];
  unitTaskId: string[];
  subTaskId: string[];
  sessionId: string[];
  prTrackingId: string[];
};

type QueryResultPanelProps = {
  title: string;
  pending: boolean;
  fetching: boolean;
  error: unknown;
  data: unknown;
  idleMessage: string;
  notFoundMessage: string;
  emptyMessage?: string;
};

function pushHistory(history: string[], value: string): string[] {
  const normalized = value.trim();
  if (normalized.length === 0) {
    return history;
  }

  const next = [normalized, ...history.filter((entry) => entry !== normalized)];
  return next.slice(0, HISTORY_LIMIT);
}

function formatForDisplay(data: unknown): string {
  return JSON.stringify(
    data,
    (_key, value) => {
      if (typeof value === "bigint") {
        return value.toString();
      }
      return value;
    },
    2,
  );
}

function describeQueryError(error: unknown, notFoundMessage: string): string {
  if (error instanceof ConnectError) {
    if (error.code === Code.NotFound) {
      return notFoundMessage;
    }
    return error.rawMessage;
  }

  if (error instanceof Error) {
    return error.message;
  }

  return "Unknown query error.";
}

function cliTypeOptions(): Array<{ value: AgentCliType; label: string }> {
  return [
    { value: AgentCliType.CODEX_CLI, label: "CODEX_CLI" },
    { value: AgentCliType.CLAUDE_CODE, label: "CLAUDE_CODE" },
    { value: AgentCliType.OPENCODE, label: "OPENCODE" },
  ];
}

function fixturePresetOptions(): Array<{
  value: SessionAdapterFixturePreset;
  label: string;
}> {
  return [
    {
      value: SessionAdapterFixturePreset.CODEX_CLI_FAILURE,
      label: "CODEX_CLI_FAILURE",
    },
    {
      value: SessionAdapterFixturePreset.CLAUDE_CODE_STREAM,
      label: "CLAUDE_CODE_STREAM",
    },
    {
      value: SessionAdapterFixturePreset.OPENCODE_RUN,
      label: "OPENCODE_RUN",
    },
  ];
}

function parseFromSequence(rawValue: string): bigint | null {
  const normalized = rawValue.trim();
  if (normalized.length === 0) {
    return null;
  }
  if (!/^\d+$/.test(normalized)) {
    return null;
  }

  try {
    return BigInt(normalized);
  } catch {
    return null;
  }
}

function describeStreamEventType(eventType: StreamEventType): string {
  return StreamEventType[eventType] ?? "STREAM_EVENT_TYPE_UNSPECIFIED";
}

function QueryResultPanel({
  title,
  pending,
  fetching,
  error,
  data,
  idleMessage,
  notFoundMessage,
  emptyMessage,
}: QueryResultPanelProps) {
  if (pending || fetching) {
    return <p className="query-status">Loading {title}...</p>;
  }

  if (error) {
    return (
      <p className="error" role="alert">
        {describeQueryError(error, notFoundMessage)}
      </p>
    );
  }

  if (data == null) {
    return <p className="query-status">{idleMessage}</p>;
  }

  if (Array.isArray(data) && data.length === 0 && emptyMessage) {
    return <p className="query-status">{emptyMessage}</p>;
  }

  return (
    <pre className="query-result" data-testid={`${title}-result`}>
      {formatForDisplay(data)}
    </pre>
  );
}

type HistoryRowProps = {
  title: string;
  values: string[];
  onSelect: (value: string) => void;
};

function HistoryRow({ title, values, onSelect }: HistoryRowProps) {
  if (values.length === 0) {
    return null;
  }

  return (
    <div className="history-row">
      <span>{title}</span>
      <div className="history-chips">
        {values.map((value) => (
          <button
            key={value}
            type="button"
            className="history-chip"
            onClick={() => onSelect(value)}
          >
            {value}
          </button>
        ))}
      </div>
    </div>
  );
}

export function RpcDashboard({
  connection,
}: {
  connection: ResolvedWorkspaceConnection;
}) {
  const [workspaceInput, setWorkspaceInput] = useState("workspace-1");
  const [repositoryGroupInput, setRepositoryGroupInput] = useState("");
  const [unitTaskInput, setUnitTaskInput] = useState("");
  const [subTaskInput, setSubTaskInput] = useState("");
  const [sessionInput, setSessionInput] = useState("");
  const [pullRequestInput, setPullRequestInput] = useState("");
  const [reviewAssistInput, setReviewAssistInput] = useState("");
  const [reviewCommentInput, setReviewCommentInput] = useState("");
  const [runUnitTaskInput, setRunUnitTaskInput] = useState("");
  const [runSubTaskInput, setRunSubTaskInput] = useState("");
  const [runSessionInput, setRunSessionInput] = useState("");
  const [runCliTypeInput, setRunCliTypeInput] = useState<AgentCliType>(
    AgentCliType.CODEX_CLI,
  );
  const [runInputMode, setRunInputMode] =
    useState<SessionAdapterInputMode>("preset");
  const [runPresetInput, setRunPresetInput] =
    useState<SessionAdapterFixturePreset>(
      SessionAdapterFixturePreset.CODEX_CLI_FAILURE,
    );
  const [runRawJsonlInput, setRunRawJsonlInput] = useState(
    `{"type":"step_start","part":{"type":"step-start"}}
{"type":"text","part":{"text":"HELLO"}}
{"type":"step_finish","part":{"reason":"stop"}}`,
  );
  const [streamFromSequenceInput, setStreamFromSequenceInput] = useState("0");
  const [localError, setLocalError] = useState<string | null>(null);
  const [sessionAdapterPending, setSessionAdapterPending] = useState(false);
  const [sessionAdapterError, setSessionAdapterError] = useState<string | null>(
    null,
  );
  const [sessionAdapterResult, setSessionAdapterResult] = useState<unknown>(null);
  const [streamStatus, setStreamStatus] = useState<StreamStatus>("idle");
  const [streamError, setStreamError] = useState<string | null>(null);
  const [streamEvents, setStreamEvents] = useState<StreamWorkspaceEventsResponse[]>(
    [],
  );
  const streamAbortControllerRef = useRef<AbortController | null>(null);
  const [history, setHistory] = useState<LookupHistory>({
    workspaceId: [],
    repositoryGroupId: [],
    unitTaskId: [],
    subTaskId: [],
    sessionId: [],
    prTrackingId: [],
  });

  const [workspaceLookup, setWorkspaceLookup] = useState<{ workspaceId: string } | null>(null);
  const [repositoryLookup, setRepositoryLookup] = useState<{
    workspaceId: string;
    repositoryGroupId: string;
  } | null>(null);
  const [unitTaskLookup, setUnitTaskLookup] = useState<{
    workspaceId: string;
    unitTaskId: string;
  } | null>(null);
  const [subTaskLookup, setSubTaskLookup] = useState<{
    workspaceId: string;
    subTaskId: string;
  } | null>(null);
  const [sessionLookup, setSessionLookup] = useState<{
    workspaceId: string;
    sessionId: string;
  } | null>(null);
  const [pullRequestLookup, setPullRequestLookup] = useState<{
    workspaceId: string;
    prTrackingId: string;
  } | null>(null);
  const [reviewAssistLookup, setReviewAssistLookup] = useState<{
    workspaceId: string;
    unitTaskId: string;
  } | null>(null);
  const [reviewCommentLookup, setReviewCommentLookup] = useState<{
    workspaceId: string;
    prTrackingId: string;
  } | null>(null);
  const [badgeThemeLookup, setBadgeThemeLookup] = useState<{ workspaceId: string } | null>(null);
  const [notificationLookup, setNotificationLookup] = useState<{ workspaceId: string } | null>(null);

  const transport = useMemo(
    () => createDexDexTransport(connection.endpointUrl, connection.token),
    [connection.endpointUrl, connection.token],
  );
  const taskClient = useMemo(() => createClient(TaskService, transport), [transport]);
  const eventStreamClient = useMemo(
    () => createClient(EventStreamService, transport),
    [transport],
  );

  const workspaceQuery = useQuery(getWorkspace, workspaceLookup ?? undefined, {
    enabled: workspaceLookup !== null,
  });
  const repositoryQuery = useQuery(
    getRepositoryGroup,
    repositoryLookup ?? undefined,
    {
      enabled: repositoryLookup !== null,
    },
  );
  const unitTaskQuery = useQuery(getUnitTask, unitTaskLookup ?? undefined, {
    enabled: unitTaskLookup !== null,
  });
  const subTaskQuery = useQuery(getSubTask, subTaskLookup ?? undefined, {
    enabled: subTaskLookup !== null,
  });
  const sessionQuery = useQuery(getSessionOutput, sessionLookup ?? undefined, {
    enabled: sessionLookup !== null,
  });
  const pullRequestQuery = useQuery(
    getPullRequest,
    pullRequestLookup ?? undefined,
    {
      enabled: pullRequestLookup !== null,
    },
  );
  const reviewAssistQuery = useQuery(
    listReviewAssistItems,
    reviewAssistLookup ?? undefined,
    {
      enabled: reviewAssistLookup !== null,
    },
  );
  const reviewCommentQuery = useQuery(
    listReviewComments,
    reviewCommentLookup ?? undefined,
    {
      enabled: reviewCommentLookup !== null,
    },
  );
  const badgeThemeQuery = useQuery(getBadgeTheme, badgeThemeLookup ?? undefined, {
    enabled: badgeThemeLookup !== null,
  });
  const notificationQuery = useQuery(
    listNotifications,
    notificationLookup ?? undefined,
    {
      enabled: notificationLookup !== null,
    },
  );

  useEffect(() => {
    return () => {
      if (streamAbortControllerRef.current) {
        streamAbortControllerRef.current.abort();
        streamAbortControllerRef.current = null;
      }
    };
  }, []);

  function remember(key: keyof LookupHistory, value: string) {
    setHistory((previous) => ({
      ...previous,
      [key]: pushHistory(previous[key], value),
    }));
  }

  function requireWorkspaceInput(actionLabel: string): string | null {
    const workspaceId = workspaceInput.trim();
    if (workspaceId.length === 0) {
      setLocalError(`${actionLabel}: workspace id is required.`);
      return null;
    }

    setLocalError(null);
    remember("workspaceId", workspaceId);
    return workspaceId;
  }

  function requireLookupInput(
    rawValue: string,
    fieldName: string,
    actionLabel: string,
  ): string | null {
    const normalized = rawValue.trim();
    if (normalized.length === 0) {
      setLocalError(`${actionLabel}: ${fieldName} is required.`);
      return null;
    }

    setLocalError(null);
    return normalized;
  }

  function handleWorkspaceLookup(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const workspaceId = requireWorkspaceInput("Fetch Workspace");
    if (!workspaceId) {
      return;
    }

    setWorkspaceLookup({ workspaceId });
  }

  function handleRepositoryLookup(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const workspaceId = requireWorkspaceInput("Fetch Repository Group");
    if (!workspaceId) {
      return;
    }
    const repositoryGroupId = requireLookupInput(
      repositoryGroupInput,
      "repository group id",
      "Fetch Repository Group",
    );
    if (!repositoryGroupId) {
      return;
    }

    remember("repositoryGroupId", repositoryGroupId);
    setRepositoryLookup({
      workspaceId,
      repositoryGroupId,
    });
  }

  function handleUnitTaskLookup(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const workspaceId = requireWorkspaceInput("Fetch Unit Task");
    if (!workspaceId) {
      return;
    }
    const unitTaskId = requireLookupInput(
      unitTaskInput,
      "unit task id",
      "Fetch Unit Task",
    );
    if (!unitTaskId) {
      return;
    }

    remember("unitTaskId", unitTaskId);
    setUnitTaskLookup({
      workspaceId,
      unitTaskId,
    });
  }

  function handleSubTaskLookup(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const workspaceId = requireWorkspaceInput("Fetch Sub Task");
    if (!workspaceId) {
      return;
    }
    const subTaskId = requireLookupInput(
      subTaskInput,
      "sub task id",
      "Fetch Sub Task",
    );
    if (!subTaskId) {
      return;
    }

    remember("subTaskId", subTaskId);
    setSubTaskLookup({
      workspaceId,
      subTaskId,
    });
  }

  function handleSessionLookup(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const workspaceId = requireWorkspaceInput("Fetch Session Output");
    if (!workspaceId) {
      return;
    }
    const sessionId = requireLookupInput(
      sessionInput,
      "session id",
      "Fetch Session Output",
    );
    if (!sessionId) {
      return;
    }

    remember("sessionId", sessionId);
    setSessionLookup({
      workspaceId,
      sessionId,
    });
  }

  function handlePullRequestLookup(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const workspaceId = requireWorkspaceInput("Fetch Pull Request");
    if (!workspaceId) {
      return;
    }
    const prTrackingId = requireLookupInput(
      pullRequestInput,
      "pr tracking id",
      "Fetch Pull Request",
    );
    if (!prTrackingId) {
      return;
    }

    remember("prTrackingId", prTrackingId);
    setPullRequestLookup({
      workspaceId,
      prTrackingId,
    });
  }

  function handleReviewAssistLookup(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const workspaceId = requireWorkspaceInput("Fetch Review Assist");
    if (!workspaceId) {
      return;
    }
    const unitTaskId = requireLookupInput(
      reviewAssistInput,
      "unit task id",
      "Fetch Review Assist",
    );
    if (!unitTaskId) {
      return;
    }

    remember("unitTaskId", unitTaskId);
    setReviewAssistLookup({
      workspaceId,
      unitTaskId,
    });
  }

  function handleReviewCommentLookup(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const workspaceId = requireWorkspaceInput("Fetch Review Comments");
    if (!workspaceId) {
      return;
    }
    const prTrackingId = requireLookupInput(
      reviewCommentInput,
      "pr tracking id",
      "Fetch Review Comments",
    );
    if (!prTrackingId) {
      return;
    }

    remember("prTrackingId", prTrackingId);
    setReviewCommentLookup({
      workspaceId,
      prTrackingId,
    });
  }

  function handleBadgeThemeLookup(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const workspaceId = requireWorkspaceInput("Fetch Badge Theme");
    if (!workspaceId) {
      return;
    }
    setBadgeThemeLookup({ workspaceId });
  }

  function handleNotificationLookup(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const workspaceId = requireWorkspaceInput("Fetch Notifications");
    if (!workspaceId) {
      return;
    }
    setNotificationLookup({ workspaceId });
  }

  async function handleRunSessionAdapter(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const workspaceId = requireWorkspaceInput("Run Session Adapter");
    if (!workspaceId) {
      return;
    }
    const unitTaskId = requireLookupInput(
      runUnitTaskInput,
      "unit task id",
      "Run Session Adapter",
    );
    if (!unitTaskId) {
      return;
    }
    const subTaskId = requireLookupInput(
      runSubTaskInput,
      "sub task id",
      "Run Session Adapter",
    );
    if (!subTaskId) {
      return;
    }
    const sessionId = requireLookupInput(
      runSessionInput,
      "session id",
      "Run Session Adapter",
    );
    if (!sessionId) {
      return;
    }

    let input: { case: "fixturePreset"; value: SessionAdapterFixturePreset } | {
      case: "rawJsonl";
      value: string;
    };
    if (runInputMode === "preset") {
      input = { case: "fixturePreset", value: runPresetInput };
    } else {
      const rawJsonl = requireLookupInput(
        runRawJsonlInput,
        "raw jsonl",
        "Run Session Adapter",
      );
      if (!rawJsonl) {
        return;
      }
      input = { case: "rawJsonl", value: rawJsonl };
    }

    remember("unitTaskId", unitTaskId);
    remember("subTaskId", subTaskId);
    remember("sessionId", sessionId);

    setSessionAdapterPending(true);
    setSessionAdapterError(null);
    try {
      const response = await taskClient.runSubTaskSessionAdapter({
        workspaceId,
        unitTaskId,
        subTaskId,
        sessionId,
        cliType: runCliTypeInput,
        input,
      });
      setSessionAdapterResult(response);
    } catch (error) {
      setSessionAdapterError(
        describeQueryError(error, "Session adapter target was not found."),
      );
      setSessionAdapterResult(null);
    } finally {
      setSessionAdapterPending(false);
    }
  }

  function stopLiveWorkspaceStream() {
    if (!streamAbortControllerRef.current) {
      return;
    }

    streamAbortControllerRef.current.abort();
    streamAbortControllerRef.current = null;
    setStreamStatus("stopped");
  }

  async function startLiveWorkspaceStream() {
    const workspaceId = requireWorkspaceInput("Start Live Stream");
    if (!workspaceId) {
      return;
    }

    const fromSequence = parseFromSequence(streamFromSequenceInput);
    if (fromSequence === null) {
      setLocalError("Start Live Stream: from sequence must be a non-negative integer.");
      return;
    }

    setLocalError(null);
    stopLiveWorkspaceStream();

    const abortController = new AbortController();
    streamAbortControllerRef.current = abortController;
    setStreamStatus("running");
    setStreamError(null);
    setStreamEvents([]);

    try {
      for await (const event of eventStreamClient.streamWorkspaceEvents(
        {
          workspaceId,
          fromSequence,
        },
        {
          signal: abortController.signal,
        },
      )) {
        if (event.sequence === 0n) {
          continue;
        }

        setStreamEvents((previous) =>
          [event, ...previous].slice(0, STREAM_EVENT_HISTORY_LIMIT),
        );
      }

      if (!abortController.signal.aborted) {
        setStreamStatus("stopped");
      }
    } catch (error) {
      if (abortController.signal.aborted) {
        return;
      }

      setStreamStatus("error");
      setStreamError(
        describeQueryError(
          error,
          "No workspace found for this stream subscription.",
        ),
      );
    } finally {
      if (streamAbortControllerRef.current === abortController) {
        streamAbortControllerRef.current = null;
      }
    }
  }

  return (
    <section className="panel" data-testid="rpc-dashboard">
      <h2>RPC Dashboard</h2>
      <p className="note">
        Endpoint: <code>{connection.endpointUrl}</code> · Source:{" "}
        <code>{connection.endpointSource}</code> · Token:{" "}
        <code>{connection.token ? "present" : "absent"}</code>
      </p>

      <div className="field">
        <label htmlFor="lookup-workspace-id">Workspace ID</label>
        <input
          id="lookup-workspace-id"
          name="lookup-workspace-id"
          value={workspaceInput}
          onChange={(event) => setWorkspaceInput(event.target.value)}
          placeholder="workspace-1"
        />
      </div>
      <HistoryRow
        title="Recent workspace IDs"
        values={history.workspaceId}
        onSelect={setWorkspaceInput}
      />

      {localError ? (
        <p className="error" role="alert">
          {localError}
        </p>
      ) : null}

      <div className="dashboard-grid">
        <article className="query-card">
          <h3>WorkspaceService.GetWorkspace</h3>
          <form onSubmit={handleWorkspaceLookup}>
            <button type="submit">Fetch Workspace</button>
          </form>
          <QueryResultPanel
            title="workspace"
            pending={workspaceQuery.isPending}
            fetching={workspaceQuery.isFetching}
            error={workspaceQuery.error}
            data={workspaceQuery.data?.workspace}
            idleMessage="Run lookup to load workspace data."
            notFoundMessage="No workspace found for this workspace id."
          />
        </article>

        <article className="query-card">
          <h3>RepositoryService.GetRepositoryGroup</h3>
          <form onSubmit={handleRepositoryLookup}>
            <div className="field">
              <label htmlFor="lookup-repository-group-id">Repository Group ID</label>
              <input
                id="lookup-repository-group-id"
                name="lookup-repository-group-id"
                value={repositoryGroupInput}
                onChange={(event) => setRepositoryGroupInput(event.target.value)}
                placeholder="repo-group-1"
              />
            </div>
            <button type="submit">Fetch Repository Group</button>
          </form>
          <HistoryRow
            title="Recent repository groups"
            values={history.repositoryGroupId}
            onSelect={setRepositoryGroupInput}
          />
          <QueryResultPanel
            title="repository-group"
            pending={repositoryQuery.isPending}
            fetching={repositoryQuery.isFetching}
            error={repositoryQuery.error}
            data={repositoryQuery.data?.repositoryGroup}
            idleMessage="Run lookup to load repository group data."
            notFoundMessage="No repository group found for this workspace and id."
          />
        </article>

        <article className="query-card">
          <h3>TaskService.GetUnitTask</h3>
          <form onSubmit={handleUnitTaskLookup}>
            <div className="field">
              <label htmlFor="lookup-unit-task-id">Unit Task ID</label>
              <input
                id="lookup-unit-task-id"
                name="lookup-unit-task-id"
                value={unitTaskInput}
                onChange={(event) => setUnitTaskInput(event.target.value)}
                placeholder="unit-1"
              />
            </div>
            <button type="submit">Fetch Unit Task</button>
          </form>
          <HistoryRow
            title="Recent unit tasks"
            values={history.unitTaskId}
            onSelect={setUnitTaskInput}
          />
          <QueryResultPanel
            title="unit-task"
            pending={unitTaskQuery.isPending}
            fetching={unitTaskQuery.isFetching}
            error={unitTaskQuery.error}
            data={unitTaskQuery.data?.unitTask}
            idleMessage="Run lookup to load unit task data."
            notFoundMessage="No unit task found for this workspace and id."
          />
        </article>

        <article className="query-card">
          <h3>TaskService.GetSubTask</h3>
          <form onSubmit={handleSubTaskLookup}>
            <div className="field">
              <label htmlFor="lookup-sub-task-id">Sub Task ID</label>
              <input
                id="lookup-sub-task-id"
                name="lookup-sub-task-id"
                value={subTaskInput}
                onChange={(event) => setSubTaskInput(event.target.value)}
                placeholder="sub-1"
              />
            </div>
            <button type="submit">Fetch Sub Task</button>
          </form>
          <HistoryRow
            title="Recent sub tasks"
            values={history.subTaskId}
            onSelect={setSubTaskInput}
          />
          <QueryResultPanel
            title="sub-task"
            pending={subTaskQuery.isPending}
            fetching={subTaskQuery.isFetching}
            error={subTaskQuery.error}
            data={subTaskQuery.data?.subTask}
            idleMessage="Run lookup to load sub task data."
            notFoundMessage="No sub task found for this workspace and id."
          />
        </article>

        <article className="query-card">
          <h3>SessionService.GetSessionOutput</h3>
          <form onSubmit={handleSessionLookup}>
            <div className="field">
              <label htmlFor="lookup-session-id">Session ID</label>
              <input
                id="lookup-session-id"
                name="lookup-session-id"
                value={sessionInput}
                onChange={(event) => setSessionInput(event.target.value)}
                placeholder="session-1"
              />
            </div>
            <button type="submit">Fetch Session Output</button>
          </form>
          <HistoryRow
            title="Recent sessions"
            values={history.sessionId}
            onSelect={setSessionInput}
          />
          <QueryResultPanel
            title="session-output"
            pending={sessionQuery.isPending}
            fetching={sessionQuery.isFetching}
            error={sessionQuery.error}
            data={sessionQuery.data?.events}
            idleMessage="Run lookup to load session events."
            notFoundMessage="No workspace found for this session lookup."
            emptyMessage="No session output events available for this session id."
          />
        </article>

        <article className="query-card">
          <h3>PrManagementService.GetPullRequest</h3>
          <form onSubmit={handlePullRequestLookup}>
            <div className="field">
              <label htmlFor="lookup-pr-tracking-id">PR Tracking ID</label>
              <input
                id="lookup-pr-tracking-id"
                name="lookup-pr-tracking-id"
                value={pullRequestInput}
                onChange={(event) => setPullRequestInput(event.target.value)}
                placeholder="pr-1"
              />
            </div>
            <button type="submit">Fetch Pull Request</button>
          </form>
          <HistoryRow
            title="Recent PR tracking IDs"
            values={history.prTrackingId}
            onSelect={setPullRequestInput}
          />
          <QueryResultPanel
            title="pull-request"
            pending={pullRequestQuery.isPending}
            fetching={pullRequestQuery.isFetching}
            error={pullRequestQuery.error}
            data={pullRequestQuery.data?.pullRequest}
            idleMessage="Run lookup to load pull request data."
            notFoundMessage="No pull request found for this workspace and tracking id."
          />
        </article>

        <article className="query-card">
          <h3>ReviewAssistService.ListReviewAssistItems</h3>
          <form onSubmit={handleReviewAssistLookup}>
            <div className="field">
              <label htmlFor="lookup-review-assist-unit-task-id">
                Unit Task ID
              </label>
              <input
                id="lookup-review-assist-unit-task-id"
                name="lookup-review-assist-unit-task-id"
                value={reviewAssistInput}
                onChange={(event) => setReviewAssistInput(event.target.value)}
                placeholder="unit-1"
              />
            </div>
            <button type="submit">Fetch Review Assist Items</button>
          </form>
          <HistoryRow
            title="Recent unit tasks"
            values={history.unitTaskId}
            onSelect={setReviewAssistInput}
          />
          <QueryResultPanel
            title="review-assist"
            pending={reviewAssistQuery.isPending}
            fetching={reviewAssistQuery.isFetching}
            error={reviewAssistQuery.error}
            data={reviewAssistQuery.data?.items}
            idleMessage="Run lookup to load review assist items."
            notFoundMessage="No workspace found for this review assist lookup."
            emptyMessage="No review assist items available for this unit task id."
          />
        </article>

        <article className="query-card">
          <h3>ReviewCommentService.ListReviewComments</h3>
          <form onSubmit={handleReviewCommentLookup}>
            <div className="field">
              <label htmlFor="lookup-review-comment-pr-id">PR Tracking ID</label>
              <input
                id="lookup-review-comment-pr-id"
                name="lookup-review-comment-pr-id"
                value={reviewCommentInput}
                onChange={(event) => setReviewCommentInput(event.target.value)}
                placeholder="pr-1"
              />
            </div>
            <button type="submit">Fetch Review Comments</button>
          </form>
          <HistoryRow
            title="Recent PR tracking IDs"
            values={history.prTrackingId}
            onSelect={setReviewCommentInput}
          />
          <QueryResultPanel
            title="review-comments"
            pending={reviewCommentQuery.isPending}
            fetching={reviewCommentQuery.isFetching}
            error={reviewCommentQuery.error}
            data={reviewCommentQuery.data?.comments}
            idleMessage="Run lookup to load review comments."
            notFoundMessage="No workspace found for this review comment lookup."
            emptyMessage="No review comments available for this pr tracking id."
          />
        </article>

        <article className="query-card">
          <h3>BadgeThemeService.GetBadgeTheme</h3>
          <form onSubmit={handleBadgeThemeLookup}>
            <button type="submit">Fetch Badge Theme</button>
          </form>
          <QueryResultPanel
            title="badge-theme"
            pending={badgeThemeQuery.isPending}
            fetching={badgeThemeQuery.isFetching}
            error={badgeThemeQuery.error}
            data={badgeThemeQuery.data?.theme}
            idleMessage="Run lookup to load badge theme data."
            notFoundMessage="No badge theme found for this workspace id."
          />
        </article>

        <article className="query-card">
          <h3>NotificationService.ListNotifications</h3>
          <form onSubmit={handleNotificationLookup}>
            <button type="submit">Fetch Notifications</button>
          </form>
          <QueryResultPanel
            title="notifications"
            pending={notificationQuery.isPending}
            fetching={notificationQuery.isFetching}
            error={notificationQuery.error}
            data={notificationQuery.data?.notifications}
            idleMessage="Run lookup to load notifications."
            notFoundMessage="No workspace found for this notification lookup."
            emptyMessage="No notifications available for this workspace id."
          />
        </article>

        <article className="query-card">
          <h3>TaskService.RunSubTaskSessionAdapter</h3>
          <form onSubmit={handleRunSessionAdapter}>
            <div className="field">
              <label htmlFor="run-unit-task-id">Run Unit Task ID</label>
              <input
                id="run-unit-task-id"
                name="run-unit-task-id"
                value={runUnitTaskInput}
                onChange={(event) => setRunUnitTaskInput(event.target.value)}
                placeholder="unit-1"
              />
            </div>
            <div className="field">
              <label htmlFor="run-sub-task-id">Run Sub Task ID</label>
              <input
                id="run-sub-task-id"
                name="run-sub-task-id"
                value={runSubTaskInput}
                onChange={(event) => setRunSubTaskInput(event.target.value)}
                placeholder="sub-1"
              />
            </div>
            <div className="field">
              <label htmlFor="run-session-id">Run Session ID</label>
              <input
                id="run-session-id"
                name="run-session-id"
                value={runSessionInput}
                onChange={(event) => setRunSessionInput(event.target.value)}
                placeholder="session-1"
              />
            </div>
            <div className="field">
              <label htmlFor="run-cli-type">CLI Type</label>
              <select
                id="run-cli-type"
                name="run-cli-type"
                value={runCliTypeInput}
                onChange={(event) =>
                  setRunCliTypeInput(Number(event.target.value) as AgentCliType)
                }
              >
                {cliTypeOptions().map((option) => (
                  <option key={option.label} value={option.value}>
                    {option.label}
                  </option>
                ))}
              </select>
            </div>
            <div className="field">
              <label htmlFor="run-input-mode">Input Mode</label>
              <select
                id="run-input-mode"
                name="run-input-mode"
                value={runInputMode}
                onChange={(event) =>
                  setRunInputMode(event.target.value as SessionAdapterInputMode)
                }
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
                  value={runPresetInput}
                  onChange={(event) =>
                    setRunPresetInput(
                      Number(event.target.value) as SessionAdapterFixturePreset,
                    )
                  }
                >
                  {fixturePresetOptions().map((option) => (
                    <option key={option.label} value={option.value}>
                      {option.label}
                    </option>
                  ))}
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
            <button type="submit" disabled={sessionAdapterPending}>
              {sessionAdapterPending ? "Running..." : "Run Session Adapter"}
            </button>
          </form>
          <HistoryRow
            title="Recent unit tasks"
            values={history.unitTaskId}
            onSelect={setRunUnitTaskInput}
          />
          <HistoryRow
            title="Recent sub tasks"
            values={history.subTaskId}
            onSelect={setRunSubTaskInput}
          />
          <HistoryRow
            title="Recent sessions"
            values={history.sessionId}
            onSelect={setRunSessionInput}
          />
          {sessionAdapterError ? (
            <p className="error" role="alert">
              {sessionAdapterError}
            </p>
          ) : null}
          {sessionAdapterResult ? (
            <pre className="query-result" data-testid="session-adapter-result">
              {formatForDisplay(sessionAdapterResult)}
            </pre>
          ) : (
            <p className="query-status">
              Run session adapter to execute fixture normalization.
            </p>
          )}
        </article>

        <article className="query-card">
          <h3>EventStreamService.StreamWorkspaceEvents</h3>
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
            <button
              type="button"
              onClick={() => {
                void startLiveWorkspaceStream();
              }}
              disabled={streamStatus === "running"}
            >
              Start Live Stream
            </button>
            <button
              type="button"
              className="secondary-button"
              onClick={stopLiveWorkspaceStream}
              disabled={streamStatus !== "running"}
            >
              Stop Live Stream
            </button>
          </div>
          <p className="query-status">
            Stream status: <code>{streamStatus.toUpperCase()}</code>
          </p>
          {streamError ? (
            <p className="error" role="alert">
              {streamError}
            </p>
          ) : null}
          {streamEvents.length > 0 ? (
            <div className="stream-events">
              {streamEvents.map((event) => (
                <article
                  key={`${event.sequence.toString()}-${event.eventType}`}
                  className="stream-event-item"
                >
                  <header className="stream-event-header">
                    <span>#{event.sequence.toString()}</span>
                    <span>{describeStreamEventType(event.eventType)}</span>
                  </header>
                  <pre className="query-result stream-event-body">
                    {formatForDisplay(event)}
                  </pre>
                </article>
              ))}
            </div>
          ) : (
            <p className="query-status">
              No live stream events yet (heartbeats are filtered out).
            </p>
          )}
        </article>
      </div>
    </section>
  );
}
