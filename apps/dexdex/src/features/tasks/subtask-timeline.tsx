/**
 * Subtask timeline component displaying the sequence of subtasks for a unit task.
 */

import type { CSSProperties } from "react";
import type { SubTask } from "../../lib/mock-data";
import { useCancelSubTaskMutation } from "../../hooks/use-dexdex-queries";
import { SubTaskStatus, SUB_TASK_TYPE_LABELS } from "../../lib/status";

interface SubtaskTimelineProps {
  subtasks: SubTask[];
  workspaceId: string;
}

function getSubtaskStatusColor(status: SubTaskStatus): string {
  switch (status) {
    case SubTaskStatus.COMPLETED:
      return "var(--color-status-completed)";
    case SubTaskStatus.IN_PROGRESS:
      return "var(--color-status-in-progress)";
    case SubTaskStatus.WAITING_FOR_PLAN_APPROVAL:
    case SubTaskStatus.WAITING_FOR_USER_INPUT:
      return "var(--color-status-action)";
    case SubTaskStatus.FAILED:
      return "var(--color-status-failed)";
    case SubTaskStatus.CANCELLED:
      return "var(--color-status-cancelled)";
    default:
      return "var(--color-text-tertiary)";
  }
}

function getSubtaskStatusIcon(status: SubTaskStatus): string {
  switch (status) {
    case SubTaskStatus.COMPLETED:
      return "\u2713";
    case SubTaskStatus.IN_PROGRESS:
      return "\u25D4";
    case SubTaskStatus.WAITING_FOR_PLAN_APPROVAL:
    case SubTaskStatus.WAITING_FOR_USER_INPUT:
      return "!";
    case SubTaskStatus.FAILED:
      return "\u2717";
    case SubTaskStatus.CANCELLED:
      return "\u2014";
    case SubTaskStatus.QUEUED:
      return "\u25CB";
    default:
      return "?";
  }
}

export function SubtaskTimeline({ subtasks, workspaceId }: SubtaskTimelineProps) {
  const cancelSubTask = useCancelSubTaskMutation();
  if (subtasks.length === 0) {
    return (
      <div
        style={{
          padding: "var(--space-4)",
          color: "var(--color-text-tertiary)",
          fontSize: "var(--font-size-sm)",
        }}
      >
        No subtasks yet
      </div>
    );
  }

  return (
    <div data-testid="subtask-timeline" style={{ padding: "var(--space-2) 0" }}>
      {subtasks.map((subtask, index) => {
        const color = getSubtaskStatusColor(subtask.status);
        const isLast = index === subtasks.length - 1;

        const itemStyle: CSSProperties = {
          display: "flex",
          gap: "var(--space-3)",
          padding: "0 var(--space-4)",
          minHeight: "48px",
        };

        const lineContainerStyle: CSSProperties = {
          display: "flex",
          flexDirection: "column",
          alignItems: "center",
          width: "20px",
          flexShrink: 0,
        };

        const dotStyle: CSSProperties = {
          width: "20px",
          height: "20px",
          borderRadius: "50%",
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          fontSize: "11px",
          fontWeight: 700,
          color,
          backgroundColor: "var(--color-bg-primary)",
          border: `2px solid ${color}`,
          flexShrink: 0,
        };

        const lineStyle: CSSProperties = {
          flex: 1,
          width: "2px",
          backgroundColor: isLast ? "transparent" : "var(--color-border)",
          margin: "2px 0",
        };

        const contentStyle: CSSProperties = {
          flex: 1,
          paddingBottom: "var(--space-4)",
        };

        return (
          <div key={subtask.subTaskId} style={itemStyle}>
            <div style={lineContainerStyle}>
              <div style={dotStyle}>{getSubtaskStatusIcon(subtask.status)}</div>
              <div style={lineStyle} />
            </div>
            <div style={contentStyle}>
              <div
                style={{
                  fontSize: "var(--font-size-base)",
                  fontWeight: 500,
                  color: "var(--color-text-primary)",
                }}
              >
                {SUB_TASK_TYPE_LABELS[subtask.type]}
              </div>
              <div
                style={{
                  fontSize: "var(--font-size-xs)",
                  color: "var(--color-text-tertiary)",
                  marginTop: "2px",
                }}
              >
                {subtask.status.replace(/_/g, " ").toLowerCase()}
              </div>
              {subtask.planSummary && (
                <div
                  style={{
                    fontSize: "var(--font-size-sm)",
                    color: "var(--color-text-secondary)",
                    marginTop: "var(--space-1)",
                    lineHeight: 1.4,
                  }}
                >
                  {subtask.planSummary}
                </div>
              )}
            </div>
            {(subtask.status === SubTaskStatus.IN_PROGRESS || subtask.status === SubTaskStatus.QUEUED) && (
              <button
                style={{
                  width: "24px",
                  height: "24px",
                  borderRadius: "50%",
                  border: "1px solid var(--color-status-failed)",
                  backgroundColor: "transparent",
                  color: "var(--color-status-failed)",
                  cursor: "pointer",
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "center",
                  fontSize: "12px",
                  flexShrink: 0,
                  alignSelf: "flex-start",
                  marginTop: "2px",
                }}
                title={subtask.status === SubTaskStatus.IN_PROGRESS ? "Stop subtask" : "Cancel subtask"}
                onClick={() => cancelSubTask.mutate({ workspaceId: workspaceId, unitTaskId: subtask.unitTaskId, subTaskId: subtask.subTaskId })}
                disabled={cancelSubTask.isPending}
                data-testid={`cancel-subtask-${subtask.subTaskId}`}
              >
                ✗
              </button>
            )}
          </div>
        );
      })}
    </div>
  );
}
