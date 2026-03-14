/**
 * Task detail view showing task information, subtask timeline, plan decisions, and session output.
 */

import type { CSSProperties } from "react";
import { StatusBadge } from "../../components/status-badge";
import type { UnitTask } from "../../lib/mock-data";
import { PlanDecision, SubTaskStatus } from "../../lib/status";
import { useListSubTasks, useGetSessionOutput } from "../../hooks/use-dexdex-queries";
import { SubtaskTimeline } from "./subtask-timeline";
import { PlanDecisions } from "./plan-decisions";
import { SessionOutputPanel } from "./session-output-panel";
import { SessionInputForm } from "../sessions/session-input-form";

const WORKSPACE_ID = "workspace-default";

interface TaskDetailProps {
  task: UnitTask;
  onBack: () => void;
  onPlanDecision: (subTaskId: string, decision: PlanDecision, revisionNote?: string) => void;
}

export function TaskDetail({ task, onBack, onPlanDecision }: TaskDetailProps) {
  // Fetch subtasks from server
  const { data: subTasks = [] } = useListSubTasks(WORKSPACE_ID, task.unitTaskId);

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
    WORKSPACE_ID,
    activeSubTask?.sessionId,
  );

  const waitingSubtask = subTasks.find(
    (st) => st.status === SubTaskStatus.WAITING_FOR_PLAN_APPROVAL,
  );

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
            {task.prompt.split("\n")[0].slice(0, 80) || "Untitled"}
          </h1>
        </div>
      </div>
      <div style={contentStyle}>
        {/* Prompt */}
        <div style={{ marginBottom: "var(--space-6)" }}>
          <div
            style={{
              fontSize: "var(--font-size-sm)",
              color: "var(--color-text-secondary)",
              lineHeight: 1.6,
              whiteSpace: "pre-wrap",
            }}
          >
            {task.prompt}
          </div>
          <div
            style={{
              display: "flex",
              gap: "var(--space-4)",
              marginTop: "var(--space-3)",
              fontSize: "var(--font-size-xs)",
              color: "var(--color-text-tertiary)",
            }}
          >
            <span>Repository: {task.repositoryUrl}</span>
            <span>Branch: {task.branchRef}</span>
          </div>
        </div>

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
          <SubtaskTimeline subtasks={subTasks} />
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
              workspaceId={WORKSPACE_ID}
              sessionId={activeSubTask.sessionId}
            />
          )}
      </div>
    </div>
  );
}
