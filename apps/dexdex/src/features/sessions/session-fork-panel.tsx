/**
 * Session fork panel showing forked sessions for a given parent session.
 * Supports creating new forks, archiving active forks, and navigating to forked sessions.
 */

import { type CSSProperties, useCallback, useState } from "react";
import {
  useListForkedSessions,
  useForkSessionMutation,
  useArchiveForkedSessionMutation,
  useListSessionCapabilities,
} from "../../hooks/use-dexdex-queries";
import { toViewAgentCapability } from "../../lib/adapters";
import { SessionForkStatus, SessionForkIntent, AgentSessionStatus } from "../../lib/status";
import type { AgentCapability } from "../../lib/mock-data";

interface SessionForkPanelProps {
  workspaceId: string;
  parentSessionId: string;
  onNavigateToSession: (sessionId: string) => void;
}

const FORK_INTENT_OPTIONS: { value: SessionForkIntent; label: string }[] = [
  { value: SessionForkIntent.EXPLORE_ALTERNATIVE, label: "Explore Alternative" },
  { value: SessionForkIntent.BRANCH_EXPERIMENT, label: "Branch Experiment" },
];

const SESSION_STATUS_LABELS: Record<AgentSessionStatus, string> = {
  [AgentSessionStatus.UNSPECIFIED]: "Unknown",
  [AgentSessionStatus.STARTING]: "Starting",
  [AgentSessionStatus.RUNNING]: "Running",
  [AgentSessionStatus.WAITING_FOR_INPUT]: "Waiting",
  [AgentSessionStatus.COMPLETED]: "Completed",
  [AgentSessionStatus.FAILED]: "Failed",
  [AgentSessionStatus.CANCELLED]: "Cancelled",
};

export function SessionForkPanel({ workspaceId, parentSessionId, onNavigateToSession }: SessionForkPanelProps) {
  const { data: sessions } = useListForkedSessions(workspaceId, parentSessionId);
  const forkMutation = useForkSessionMutation();
  const archiveMutation = useArchiveForkedSessionMutation();
  const capabilitiesQuery = useListSessionCapabilities(workspaceId);

  const [showCreateDialog, setShowCreateDialog] = useState(false);
  const [forkIntent, setForkIntent] = useState<SessionForkIntent>(SessionForkIntent.EXPLORE_ALTERNATIVE);
  const [forkPrompt, setForkPrompt] = useState("");

  const capabilities: AgentCapability[] = (capabilitiesQuery.data?.capabilities ?? []).map(toViewAgentCapability);
  const supportsFork = capabilities.some((c) => c.supportsFork);

  const handleCreateFork = useCallback(() => {
    if (!forkPrompt.trim()) return;
    forkMutation.mutate(
      {
        workspaceId,
        parentSessionId,
        forkIntent: forkIntent === SessionForkIntent.EXPLORE_ALTERNATIVE ? 1 : 2,
        prompt: forkPrompt.trim(),
      },
      {
        onSuccess: () => {
          setShowCreateDialog(false);
          setForkPrompt("");
          setForkIntent(SessionForkIntent.EXPLORE_ALTERNATIVE);
        },
      },
    );
  }, [workspaceId, parentSessionId, forkIntent, forkPrompt, forkMutation]);

  const handleArchive = useCallback(
    (sessionId: string) => {
      archiveMutation.mutate({ workspaceId, sessionId });
    },
    [workspaceId, archiveMutation],
  );

  const containerStyle: CSSProperties = {
    padding: "var(--space-4)",
    borderTop: "1px solid var(--color-border)",
  };

  const headerStyle: CSSProperties = {
    display: "flex",
    alignItems: "center",
    justifyContent: "space-between",
    marginBottom: "var(--space-3)",
  };

  const titleStyle: CSSProperties = {
    fontSize: "var(--font-size-sm)",
    fontWeight: 600,
    color: "var(--color-text-primary)",
  };

  const buttonStyle: CSSProperties = {
    padding: "4px 12px",
    fontSize: "var(--font-size-xs)",
    fontWeight: 500,
    border: "1px solid var(--color-border)",
    borderRadius: "var(--radius-sm)",
    backgroundColor: "var(--color-bg-secondary)",
    color: "var(--color-text-primary)",
    cursor: "pointer",
  };

  const disabledButtonStyle: CSSProperties = {
    ...buttonStyle,
    opacity: 0.5,
    cursor: "not-allowed",
  };

  const sessionRowStyle: CSSProperties = {
    display: "flex",
    alignItems: "center",
    gap: "var(--space-2)",
    padding: "var(--space-2) var(--space-3)",
    borderRadius: "var(--radius-sm)",
    cursor: "pointer",
    fontSize: "var(--font-size-sm)",
    color: "var(--color-text-primary)",
    transition: "background-color 0.1s",
  };

  const badgeStyle = (isActive: boolean): CSSProperties => ({
    padding: "1px 6px",
    borderRadius: "var(--radius-full)",
    fontSize: "var(--font-size-xs)",
    fontWeight: 500,
    backgroundColor: isActive ? "var(--color-status-in-progress-bg)" : "var(--color-bg-tertiary)",
    color: isActive ? "var(--color-status-in-progress)" : "var(--color-text-tertiary)",
  });

  const dialogStyle: CSSProperties = {
    marginTop: "var(--space-3)",
    padding: "var(--space-3)",
    border: "1px solid var(--color-border)",
    borderRadius: "var(--radius-md)",
    backgroundColor: "var(--color-bg-secondary)",
  };

  const inputStyle: CSSProperties = {
    width: "100%",
    padding: "var(--space-2)",
    fontSize: "var(--font-size-sm)",
    border: "1px solid var(--color-border)",
    borderRadius: "var(--radius-sm)",
    backgroundColor: "var(--color-bg-primary)",
    color: "var(--color-text-primary)",
    boxSizing: "border-box",
    resize: "vertical",
  };

  const selectStyle: CSSProperties = {
    ...inputStyle,
    marginBottom: "var(--space-2)",
  };

  return (
    <div style={containerStyle} data-testid="session-fork-panel">
      <div style={headerStyle}>
        <span style={titleStyle}>Forked Sessions</span>
        <button
          style={supportsFork ? buttonStyle : disabledButtonStyle}
          disabled={!supportsFork}
          onClick={() => setShowCreateDialog(true)}
          data-testid="fork-session-button"
          title={supportsFork ? "Fork this session" : "Agent does not support forking"}
        >
          Fork Session
        </button>
      </div>

      {sessions.length === 0 && !showCreateDialog && (
        <div
          style={{
            fontSize: "var(--font-size-sm)",
            color: "var(--color-text-tertiary)",
            padding: "var(--space-2) 0",
          }}
        >
          No forked sessions
        </div>
      )}

      {sessions.map((session) => (
        <div
          key={session.sessionId}
          style={sessionRowStyle}
          onClick={() => onNavigateToSession(session.sessionId)}
          onMouseEnter={(e) => {
            (e.currentTarget as HTMLElement).style.backgroundColor = "var(--color-bg-hover)";
          }}
          onMouseLeave={(e) => {
            (e.currentTarget as HTMLElement).style.backgroundColor = "transparent";
          }}
          data-testid={`fork-row-${session.sessionId}`}
        >
          <span style={{ flex: 1, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
            {session.sessionId.slice(0, 12)}...
          </span>
          <span style={badgeStyle(session.forkStatus === SessionForkStatus.ACTIVE)}>
            {session.forkStatus === SessionForkStatus.ACTIVE ? "Active" : "Archived"}
          </span>
          <span
            style={{
              fontSize: "var(--font-size-xs)",
              color: "var(--color-text-tertiary)",
            }}
          >
            {SESSION_STATUS_LABELS[session.agentSessionStatus] ?? "Unknown"}
          </span>
          {session.forkStatus === SessionForkStatus.ACTIVE && (
            <button
              style={{
                ...buttonStyle,
                padding: "2px 8px",
                fontSize: "var(--font-size-xs)",
              }}
              onClick={(e) => {
                e.stopPropagation();
                handleArchive(session.sessionId);
              }}
              data-testid={`archive-fork-${session.sessionId}`}
            >
              Archive
            </button>
          )}
        </div>
      ))}

      {showCreateDialog && (
        <div style={dialogStyle} data-testid="fork-create-dialog">
          <select
            value={forkIntent}
            onChange={(e) => setForkIntent(e.target.value as SessionForkIntent)}
            style={selectStyle}
            data-testid="fork-intent-select"
          >
            {FORK_INTENT_OPTIONS.map((opt) => (
              <option key={opt.value} value={opt.value}>
                {opt.label}
              </option>
            ))}
          </select>
          <textarea
            value={forkPrompt}
            onChange={(e) => setForkPrompt(e.target.value)}
            placeholder="Describe what this fork should explore..."
            rows={3}
            style={inputStyle}
            data-testid="fork-prompt-input"
          />
          <div style={{ display: "flex", gap: "var(--space-2)", marginTop: "var(--space-2)", justifyContent: "flex-end" }}>
            <button
              style={buttonStyle}
              onClick={() => {
                setShowCreateDialog(false);
                setForkPrompt("");
              }}
              data-testid="fork-cancel-button"
            >
              Cancel
            </button>
            <button
              style={{
                ...buttonStyle,
                backgroundColor: "var(--color-accent)",
                color: "var(--color-text-inverse)",
                borderColor: "var(--color-accent)",
              }}
              onClick={handleCreateFork}
              disabled={!forkPrompt.trim() || forkMutation.isPending}
              data-testid="fork-submit-button"
            >
              {forkMutation.isPending ? "Creating..." : "Create Fork"}
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
