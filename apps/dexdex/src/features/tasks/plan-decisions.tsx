/**
 * Plan decision controls for subtasks waiting for plan approval.
 */

import { type CSSProperties, useState } from "react";
import { PlanDecision, SubTaskStatus } from "../../lib/status";
import type { SubTask } from "../../lib/mock-data";

interface PlanDecisionsProps {
  subtask: SubTask;
  onDecision: (subTaskId: string, decision: PlanDecision, revisionNote?: string) => void;
}

export function PlanDecisions({ subtask, onDecision }: PlanDecisionsProps) {
  const [revisionNote, setRevisionNote] = useState("");
  const [showReviseInput, setShowReviseInput] = useState(false);

  if (subtask.status !== SubTaskStatus.WAITING_FOR_PLAN_APPROVAL) {
    return null;
  }

  const containerStyle: CSSProperties = {
    padding: "var(--space-4)",
    backgroundColor: "var(--color-status-action-bg)",
    borderRadius: "var(--radius-md)",
    border: "1px solid var(--color-status-action)",
    margin: "var(--space-3) 0",
  };

  const buttonGroupStyle: CSSProperties = {
    display: "flex",
    gap: "var(--space-2)",
    marginTop: "var(--space-3)",
  };

  const buttonBase: CSSProperties = {
    padding: "var(--space-2) var(--space-4)",
    borderRadius: "var(--radius-md)",
    fontSize: "var(--font-size-sm)",
    fontWeight: 500,
    cursor: "pointer",
    border: "1px solid var(--color-border)",
  };

  return (
    <div style={containerStyle} data-testid="plan-decisions">
      <div
        style={{
          fontSize: "var(--font-size-sm)",
          fontWeight: 600,
          color: "var(--color-text-primary)",
          marginBottom: "var(--space-1)",
        }}
      >
        Plan Approval Required
      </div>
      {subtask.planSummary && (
        <div
          style={{
            fontSize: "var(--font-size-sm)",
            color: "var(--color-text-secondary)",
            marginBottom: "var(--space-2)",
          }}
        >
          {subtask.planSummary}
        </div>
      )}

      {showReviseInput && (
        <div style={{ marginTop: "var(--space-2)" }}>
          <textarea
            style={{
              width: "100%",
              padding: "var(--space-2)",
              borderRadius: "var(--radius-md)",
              border: "1px solid var(--color-border)",
              fontSize: "var(--font-size-sm)",
              backgroundColor: "var(--color-bg-primary)",
              color: "var(--color-text-primary)",
              minHeight: "60px",
              resize: "vertical",
              outline: "none",
            }}
            placeholder="Describe the revisions needed..."
            value={revisionNote}
            onChange={(e) => setRevisionNote(e.target.value)}
            data-testid="revision-note-input"
          />
        </div>
      )}

      <div style={buttonGroupStyle}>
        <button
          style={{
            ...buttonBase,
            backgroundColor: "var(--color-status-completed)",
            color: "#fff",
            borderColor: "var(--color-status-completed)",
          }}
          onClick={() => onDecision(subtask.subTaskId, PlanDecision.APPROVE)}
          data-testid="approve-button"
        >
          Approve
        </button>
        {!showReviseInput ? (
          <button
            style={{
              ...buttonBase,
              backgroundColor: "var(--color-bg-primary)",
            }}
            onClick={() => setShowReviseInput(true)}
            data-testid="revise-button"
          >
            Revise
          </button>
        ) : (
          <button
            style={{
              ...buttonBase,
              backgroundColor: "var(--color-status-action)",
              color: "#fff",
              borderColor: "var(--color-status-action)",
            }}
            onClick={() => {
              if (revisionNote.trim()) {
                onDecision(subtask.subTaskId, PlanDecision.REVISE, revisionNote.trim());
              }
            }}
            disabled={!revisionNote.trim()}
          >
            Submit Revision
          </button>
        )}
        <button
          style={{
            ...buttonBase,
            backgroundColor: "var(--color-bg-primary)",
            color: "var(--color-status-failed)",
          }}
          onClick={() => onDecision(subtask.subTaskId, PlanDecision.REJECT)}
          data-testid="reject-button"
        >
          Reject
        </button>
      </div>
    </div>
  );
}
