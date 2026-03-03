import { type FormEvent, useMemo, useState } from "react";
import {
  type ResolveWorkspaceConnectionInput,
  type ResolvedWorkspaceConnection,
} from "./contracts/workspace-connection";
import { WorkspaceMode } from "./contracts/workspace-mode";
import { createDexDexApiClient } from "./lib/dexdex-api";
import {
  resolveWorkspaceConnection,
  type ResolveWorkspaceConnection,
} from "./lib/resolve-workspace-connection";

type AppStatus = "idle" | "resolving" | "resolved" | "error";

type AppProps = {
  resolver?: ResolveWorkspaceConnection;
};

type ConsoleState = {
  workspaceId: string;
  unitTaskTitle: string;
  selectedUnitTaskId: string;
  selectedSubTaskId: string;
  revisionNote: string;
  prTrackingId: string;
  sessionId: string;
};

const DEFAULT_REMOTE_ENDPOINT =
  (import.meta.env as Record<string, string | undefined>).VITE_DEXDEX_MAIN_ADDR ??
  "http://127.0.0.1:7878";

function modeOptions(): Array<{ value: WorkspaceMode; label: string }> {
  return [
    { value: WorkspaceMode.Local, label: "LOCAL" },
    { value: WorkspaceMode.Remote, label: "REMOTE" },
  ];
}

function prettyJson(value: unknown): string {
  return JSON.stringify(value, null, 2);
}

export function App({ resolver = resolveWorkspaceConnection }: AppProps) {
  const [mode, setMode] = useState<WorkspaceMode>(WorkspaceMode.Local);
  const [remoteEndpointUrl, setRemoteEndpointUrl] = useState(DEFAULT_REMOTE_ENDPOINT);
  const [remoteToken, setRemoteToken] = useState("");
  const [status, setStatus] = useState<AppStatus>("idle");
  const [errorMessage, setErrorMessage] = useState<string | null>(null);
  const [resolvedConnection, setResolvedConnection] =
    useState<ResolvedWorkspaceConnection | null>(null);

  const [consoleState, setConsoleState] = useState<ConsoleState>({
    workspaceId: "workspace-default",
    unitTaskTitle: "Implement workflow hardening",
    selectedUnitTaskId: "",
    selectedSubTaskId: "",
    revisionNote: "Please improve tests and logging before re-run.",
    prTrackingId: "owner/repo#1",
    sessionId: "",
  });

  const [lastResponse, setLastResponse] = useState<string>("{}");
  const [consoleError, setConsoleError] = useState<string | null>(null);
  const [busyAction, setBusyAction] = useState<string | null>(null);

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

  async function executeAction(
    action: string,
    fn: (client: ReturnType<typeof createDexDexApiClient>) => Promise<unknown>,
  ) {
    if (!resolvedConnection) {
      setConsoleError("Resolve workspace connection first.");
      return;
    }

    const client = createDexDexApiClient(resolvedConnection);
    setBusyAction(action);
    setConsoleError(null);

    try {
      const result = await fn(client);
      setLastResponse(prettyJson(result));
    } catch (error) {
      const message = error instanceof Error ? error.message : "Unknown API error";
      setConsoleError(message);
    } finally {
      setBusyAction(null);
    }
  }

  const isRemoteMode = mode === WorkspaceMode.Remote;

  return (
    <main className="app-shell">
      <header className="app-header">
        <h1>DexDex Operator Console</h1>
        <p>
          LOCAL and REMOTE modes converge to one Connect RPC contract. After
          resolution, orchestration actions use the same control-plane flows.
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
        </section>
      ) : null}

      <section className="panel" aria-label="Operator actions">
        <h2>Control Plane Actions</h2>

        <div className="field-grid">
          <div className="field">
            <label htmlFor="workspace-id">Workspace ID</label>
            <input
              id="workspace-id"
              value={consoleState.workspaceId}
              onChange={(event) =>
                setConsoleState((prev) => ({ ...prev, workspaceId: event.target.value }))
              }
            />
          </div>
          <div className="field">
            <label htmlFor="unit-task-title">Unit Task Title</label>
            <input
              id="unit-task-title"
              value={consoleState.unitTaskTitle}
              onChange={(event) =>
                setConsoleState((prev) => ({ ...prev, unitTaskTitle: event.target.value }))
              }
            />
          </div>
          <div className="field">
            <label htmlFor="unit-task-id">Unit Task ID</label>
            <input
              id="unit-task-id"
              value={consoleState.selectedUnitTaskId}
              onChange={(event) =>
                setConsoleState((prev) => ({ ...prev, selectedUnitTaskId: event.target.value }))
              }
            />
          </div>
          <div className="field">
            <label htmlFor="sub-task-id">Sub Task ID</label>
            <input
              id="sub-task-id"
              value={consoleState.selectedSubTaskId}
              onChange={(event) =>
                setConsoleState((prev) => ({ ...prev, selectedSubTaskId: event.target.value }))
              }
            />
          </div>
          <div className="field">
            <label htmlFor="revision-note">Revision Note</label>
            <input
              id="revision-note"
              value={consoleState.revisionNote}
              onChange={(event) =>
                setConsoleState((prev) => ({ ...prev, revisionNote: event.target.value }))
              }
            />
          </div>
          <div className="field">
            <label htmlFor="pr-tracking-id">PR Tracking ID</label>
            <input
              id="pr-tracking-id"
              value={consoleState.prTrackingId}
              onChange={(event) =>
                setConsoleState((prev) => ({ ...prev, prTrackingId: event.target.value }))
              }
            />
          </div>
          <div className="field">
            <label htmlFor="session-id">Session ID</label>
            <input
              id="session-id"
              value={consoleState.sessionId}
              onChange={(event) =>
                setConsoleState((prev) => ({ ...prev, sessionId: event.target.value }))
              }
            />
          </div>
        </div>

        <div className="action-grid">
          <button
            type="button"
            disabled={busyAction !== null}
            onClick={() =>
              executeAction("create-unit-task", (client) =>
                client.createUnitTask(consoleState.workspaceId, consoleState.unitTaskTitle),
              )
            }
          >
            Create Unit Task
          </button>
          <button
            type="button"
            disabled={busyAction !== null}
            onClick={() =>
              executeAction("list-unit-tasks", (client) =>
                client.listUnitTasks(consoleState.workspaceId),
              )
            }
          >
            List Unit Tasks
          </button>
          <button
            type="button"
            disabled={busyAction !== null}
            onClick={() =>
              executeAction("start-sub-task", (client) =>
                client.startSubTask(
                  consoleState.workspaceId,
                  consoleState.selectedUnitTaskId,
                  "Implement and validate sub task execution.",
                ),
              )
            }
          >
            Start Sub Task
          </button>
          <button
            type="button"
            disabled={busyAction !== null}
            onClick={() =>
              executeAction("list-sub-tasks", (client) =>
                client.listSubTasks(
                  consoleState.workspaceId,
                  consoleState.selectedUnitTaskId,
                ),
              )
            }
          >
            List Sub Tasks
          </button>
          <button
            type="button"
            disabled={busyAction !== null}
            onClick={() =>
              executeAction("approve-plan", (client) =>
                client.submitPlanDecision(
                  consoleState.workspaceId,
                  consoleState.selectedSubTaskId,
                  "PLAN_DECISION_APPROVE",
                ),
              )
            }
          >
            Approve Plan
          </button>
          <button
            type="button"
            disabled={busyAction !== null}
            onClick={() =>
              executeAction("revise-plan", (client) =>
                client.submitPlanDecision(
                  consoleState.workspaceId,
                  consoleState.selectedSubTaskId,
                  "PLAN_DECISION_REVISE",
                  consoleState.revisionNote,
                ),
              )
            }
          >
            Revise Plan
          </button>
          <button
            type="button"
            disabled={busyAction !== null}
            onClick={() =>
              executeAction("reject-plan", (client) =>
                client.submitPlanDecision(
                  consoleState.workspaceId,
                  consoleState.selectedSubTaskId,
                  "PLAN_DECISION_REJECT",
                ),
              )
            }
          >
            Reject Plan
          </button>
          <button
            type="button"
            disabled={busyAction !== null}
            onClick={() =>
              executeAction("get-pr", (client) =>
                client.getPullRequest(consoleState.workspaceId, consoleState.prTrackingId),
              )
            }
          >
            Get PR
          </button>
          <button
            type="button"
            disabled={busyAction !== null}
            onClick={() =>
              executeAction("list-review-comments", (client) =>
                client.listReviewComments(consoleState.workspaceId, consoleState.prTrackingId),
              )
            }
          >
            List Review Comments
          </button>
          <button
            type="button"
            disabled={busyAction !== null}
            onClick={() =>
              executeAction("get-session-output", (client) =>
                client.getSessionOutput(consoleState.workspaceId, consoleState.sessionId),
              )
            }
          >
            Get Session Output
          </button>
          <button
            type="button"
            disabled={busyAction !== null}
            onClick={() =>
              executeAction("list-notifications", (client) =>
                client.listNotifications(consoleState.workspaceId),
              )
            }
          >
            List Notifications
          </button>
        </div>

        {busyAction ? <p className="status">Running: {busyAction}</p> : null}
        {consoleError ? (
          <p className="error" role="alert">
            {consoleError}
          </p>
        ) : null}

        <pre className="result" data-testid="console-last-response">
          {lastResponse}
        </pre>
      </section>
    </main>
  );
}
