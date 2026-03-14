/**
 * Task detail view showing task information, subtask timeline, plan decisions, and session output.
 */

import type { CSSProperties } from "react";
import { StatusBadge } from "../../components/status-badge";
import type { UnitTask, SessionOutputEvent } from "../../lib/mock-data";
import { PlanDecision, SubTaskStatus } from "../../lib/status";
import { SubtaskTimeline } from "./subtask-timeline";
import { PlanDecisions } from "./plan-decisions";
import { SessionOutputPanel } from "./session-output-panel";

interface TaskDetailProps {
  task: UnitTask;
  sessionOutput: SessionOutputEvent[];
  onBack: () => void;
  onPlanDecision: (subTaskId: string, decision: PlanDecision, revisionNote?: string) => void;
}

export function TaskDetail({ task, sessionOutput, onBack, onPlanDecision }: TaskDetailProps) {
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

  // Find the latest subtask with a session for session output
  const latestSessionSubtask = [...task.subTasks].reverse().find((st) => st.sessionId);
  const waitingSubtask = task.subTasks.find(
    (st) => st.status === SubTaskStatus.WAITING_FOR_PLAN_APPROVAL,
  );

  return (
    <div style={containerStyle} data-testid="task-detail">
      <div style={headerStyle}>
        <button style={backButtonStyle} onClick={onBack} data-testid="back-button">
          \u2190 Back to Tasks
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
            }}
          >
            {task.description}
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
          <SubtaskTimeline subtasks={task.subTasks} />
        </div>

        {/* Session output panel */}
        {latestSessionSubtask && (
          <SessionOutputPanel
            events={sessionOutput}
            sessionId={latestSessionSubtask.sessionId}
          />
        )}
      </div>
    </div>
  );
}
