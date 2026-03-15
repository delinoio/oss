/**
 * Task detail view showing task information, subtask timeline, plan decisions, and session output.
 */

import { type CSSProperties, useState } from "react";
import { StatusBadge } from "../../components/status-badge";
import type { UnitTask } from "../../lib/mock-data";
import { PlanDecision, SubTaskStatus, UnitTaskStatus } from "../../lib/status";
import { useListSubTasks, useListSubTasksRaw, useGetSessionOutput, useCancelUnitTaskMutation, useTrackPullRequestMutation } from "../../hooks/use-dexdex-queries";
import { SubtaskTimeline } from "./subtask-timeline";
import { PlanDecisions } from "./plan-decisions";
import { SessionOutputPanel } from "./session-output-panel";
import { SessionInputForm } from "../sessions/session-input-form";
import { useAppStore } from "../../stores/app-store";

interface TaskDetailProps {
  task: UnitTask;
  onBack: () => void;
  onPlanDecision: (subTaskId: string, decision: PlanDecision, revisionNote?: string) => void;
}

export function TaskDetail({ task, onBack, onPlanDecision }: TaskDetailProps) {
  const { activeWorkspaceId } = useAppStore();

  // Fetch subtasks from server
  const { data: subTasks = [] } = useListSubTasks(activeWorkspaceId, task.unitTaskId);

  // Find active subtask for session output display
  const activeSubTask =
    subTasks.find(
      (st) =>
        st.status === SubTaskStatus.IN_PROGRESS ||
        st.status === SubTaskStatus.WAITING_FOR_PLAN_APPROVAL ||
        st.status === SubTaskStatus.WAITING_FOR_USER_INPUT,
    ) ?? subTasks[subTasks.length - 1];

  // Fetch session output for the active subtask
  const { data: sessionOutput = [] } = useGetSessionOutput(
    activeWorkspaceId,
    activeSubTask?.sessionId,
  );

  const waitingSubtask = subTasks.find(
    (st) => st.status === SubTaskStatus.WAITING_FOR_PLAN_APPROVAL,
  );

  // Fetch raw subtasks to access commitChain
  const { data: rawSubTasks = [] } = useListSubTasksRaw(activeWorkspaceId, task.unitTaskId);

  // Cancel/stop mutation for the unit task
  const cancelUnitTask = useCancelUnitTaskMutation();

  // Track PR mutation
  const trackPullRequest = useTrackPullRequestMutation();
  const [prUrl, setPrUrl] = useState("");
  const [prCreated, setPrCreated] = useState(false);

  const handleCancelTask = () => {
    cancelUnitTask.mutate({ workspaceId: activeWorkspaceId, unitTaskId: task.unitTaskId });
  };

  // Check if any subtask has commits in its commit chain
  const hasCommits = rawSubTasks.some((st) => st.commitChain.length > 0);
  const showCreatePr = task.status === UnitTaskStatus.ACTION_REQUIRED && hasCommits;

  const handleCreatePr = () => {
    const url = prUrl.trim();
    if (!url) return;
    trackPullRequest.mutate(
      { workspaceId: activeWorkspaceId, prUrl: url, unitTaskId: task.unitTaskId },
      {
        onSuccess: () => {
          setPrCreated(true);
          setPrUrl("");
        },
      },
    );
  };

  const isStoppable = task.status === UnitTaskStatus.IN_PROGRESS;
  const isCancellable = task.status === UnitTaskStatus.QUEUED;

  const containerStyle: CSSProperties = {
    height: "100%",
    display: "flex",
    flexDirection: "column",
    overflow: "hidden",
  };

  const headerStyle: CSSProperties = {
    padding: "var(--space-4) var(--space-6)",
    borderBottom: "1px solid var(--color-border)",
    flexShrink: 0,
  };

  const contentStyle: CSSProperties = {
    flex: 1,
    overflowY: "auto",
    padding: "var(--space-4) var(--space-6)",
  };

  const backButtonStyle: CSSProperties = {
    fontSize: "var(--font-size-sm)",
    color: "var(--color-text-secondary)",
    cursor: "pointer",
    display: "inline-flex",
    alignItems: "center",
    gap: "var(--space-1)",
    marginBottom: "var(--space-3)",
  };

  return (
    <div style={containerStyle} data-testid="task-detail">
      <div style={headerStyle}>
        <button style={backButtonStyle} onClick={onBack} data-testid="back-button">
          {"\u2190"} Back to Tasks
        </button>
        <div style={{ display: "flex", alignItems: "center", gap: "var(--space-3)" }}>
          <StatusBadge status={task.status} />
          <h1
            style={{
              fontSize: "var(--font-size-xl)",
              fontWeight: 600,
              flex: 1,
            }}
          >
            {task.title}
          </h1>
          {isStoppable && (
            <button
              style={{
                fontSize: "var(--font-size-sm)",
                padding: "var(--space-1) var(--space-3)",
                borderRadius: "6px",
                border: "1px solid var(--color-status-failed)",
                backgroundColor: "transparent",
                color: "var(--color-status-failed)",
                cursor: "pointer",
                fontWeight: 500,
              }}
              onClick={handleCancelTask}
              disabled={cancelUnitTask.isPending}
              data-testid="stop-task-button"
            >
              {cancelUnitTask.isPending ? "Stopping..." : "Stop Task"}
            </button>
          )}
          {isCancellable && (
            <button
              style={{
                fontSize: "var(--font-size-sm)",
                padding: "var(--space-1) var(--space-3)",
                borderRadius: "6px",
                border: "1px solid var(--color-status-cancelled)",
                backgroundColor: "transparent",
                color: "var(--color-status-cancelled)",
                cursor: "pointer",
                fontWeight: 500,
              }}
              onClick={handleCancelTask}
              disabled={cancelUnitTask.isPending}
              data-testid="cancel-task-button"
            >
              {cancelUnitTask.isPending ? "Cancelling..." : "Cancel Task"}
            </button>
          )}
        </div>
      </div>
      <div style={contentStyle}>
        {/* Description */}
        <div style={{ marginBottom: "var(--space-6)" }}>
          <div
            style={{
              fontSize: "var(--font-size-sm)",
              color: "var(--color-text-secondary)",
              lineHeight: 1.6,
              whiteSpace: "pre-wrap",
            }}
          >
            {task.prompt || task.description}
          </div>
          <div
            style={{
              display: "flex",
              flexWrap: "wrap",
              gap: "var(--space-4)",
              marginTop: "var(--space-3)",
              fontSize: "var(--font-size-xs)",
              color: "var(--color-text-tertiary)",
            }}
          >
            <span>Repository Group: {task.repositoryGroupId || "-"}</span>
            <span>Agent: {task.agentCliType || "UNSPECIFIED"}</span>
            <span>Plan Mode: {task.usePlanMode ? "ON" : "OFF"}</span>
          </div>
        </div>

        {/* Create PR action */}
        {showCreatePr && (
          <div
            style={{
              marginBottom: "var(--space-6)",
              padding: "var(--space-4)",
              border: "1px solid var(--color-border)",
              borderRadius: "var(--radius-md)",
              backgroundColor: "var(--color-bg-secondary)",
            }}
            data-testid="create-pr-section"
          >
            <h2
              style={{
                fontSize: "var(--font-size-md)",
                fontWeight: 600,
                marginBottom: "var(--space-3)",
              }}
            >
              Create Pull Request
            </h2>
            {prCreated ? (
              <div
                style={{
                  color: "var(--color-status-completed)",
                  fontSize: "var(--font-size-sm)",
                  fontWeight: 500,
                }}
                data-testid="pr-created-success"
              >
                Pull request tracked successfully.
              </div>
            ) : (
              <div style={{ display: "grid", gridTemplateColumns: "1fr auto", gap: "var(--space-2)", maxWidth: 560 }}>
                <input
                  style={{
                    width: "100%",
                    padding: "var(--space-2) var(--space-3)",
                    borderRadius: "var(--radius-md)",
                    border: "1px solid var(--color-border)",
                    fontSize: "var(--font-size-sm)",
                    backgroundColor: "var(--color-bg-primary)",
                    color: "var(--color-text-primary)",
                    outline: "none",
                  }}
                  type="text"
                  value={prUrl}
                  onChange={(e) => setPrUrl(e.target.value)}
                  placeholder="https://github.com/org/repo/pull/123"
                  data-testid="pr-url-input"
                />
                <button
                  style={{
                    padding: "var(--space-2) var(--space-3)",
                    borderRadius: "var(--radius-md)",
                    border: "1px solid var(--color-accent)",
                    backgroundColor: "var(--color-accent)",
                    color: "var(--color-text-inverse)",
                    fontSize: "var(--font-size-sm)",
                    fontWeight: 500,
                    cursor: "pointer",
                  }}
                  onClick={handleCreatePr}
                  disabled={trackPullRequest.isPending || !prUrl.trim()}
                  data-testid="create-pr-button"
                >
                  {trackPullRequest.isPending ? "Creating..." : "Create PR"}
                </button>
              </div>
            )}
          </div>
        )}

        {/* Plan decisions for waiting subtask */}
        {waitingSubtask && (
          <PlanDecisions subtask={waitingSubtask} onDecision={onPlanDecision} />
        )}

        {/* Subtask timeline */}
        <div style={{ marginBottom: "var(--space-4)" }}>
          <h2
            style={{
              fontSize: "var(--font-size-md)",
              fontWeight: 600,
              marginBottom: "var(--space-3)",
            }}
          >
            Subtasks
          </h2>
          <SubtaskTimeline subtasks={subTasks} workspaceId={activeWorkspaceId} />
        </div>

        {/* Session output panel */}
        {activeSubTask && activeSubTask.sessionId && (
          <SessionOutputPanel
            events={sessionOutput}
            sessionId={activeSubTask.sessionId}
          />
        )}

        {/* Session input form for waiting-for-input subtasks */}
        {activeSubTask &&
          activeSubTask.status === SubTaskStatus.WAITING_FOR_USER_INPUT &&
          activeSubTask.sessionId && (
            <SessionInputForm
              workspaceId={activeWorkspaceId}
              sessionId={activeSubTask.sessionId}
            />
          )}
      </div>
    </div>
  );
}
