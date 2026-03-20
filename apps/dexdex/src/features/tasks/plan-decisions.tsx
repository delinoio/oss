/**
 * Plan decision controls for subtasks waiting for plan approval.
 */

import { type CSSProperties, useCallback, useEffect, useRef, useState } from "react";
import { PlanDecision, SubTaskStatus } from "../../lib/status";
import type { SubTask } from "../../lib/mock-data";
import { useFocusOnShow } from "../../hooks/use-dialog-accessibility";

interface PlanDecisionsProps {
  subtask: SubTask;
  onDecision: (subTaskId: string, decision: PlanDecision, revisionNote?: string) => void;
}

export function PlanDecisions({ subtask, onDecision }: PlanDecisionsProps) {
  const [revisionNote, setRevisionNote] = useState("");
  const [showReviseInput, setShowReviseInput] = useState(false);
  const revisionInputRef = useRef<HTMLTextAreaElement>(null);

  useFocusOnShow(showReviseInput, revisionInputRef);

  const submitRevision = useCallback(() => {
    const trimmedRevisionNote = revisionNote.trim();
    if (!trimmedRevisionNote) {
      return;
    }
    onDecision(subtask.subTaskId, PlanDecision.REVISE, trimmedRevisionNote);
  }, [onDecision, revisionNote, subtask.subTaskId]);

  useEffect(() => {
    if (subtask.status !== SubTaskStatus.WAITING_FOR_PLAN_APPROVAL) {
      return;
    }

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.isComposing || event.keyCode === 229) {
        return;
      }

      const activeElement = document.activeElement as HTMLElement | null;
      const isTextInputFocused =
        activeElement?.tagName.toLowerCase() === "input" ||
        activeElement?.tagName.toLowerCase() === "textarea" ||
        activeElement?.isContentEditable;
      if (isTextInputFocused) {
        if (
          activeElement === revisionInputRef.current &&
          (event.metaKey || event.ctrlKey) &&
          event.key === "Enter"
        ) {
          event.preventDefault();
          submitRevision();
        }
        return;
      }

      const isMetaPressed = event.metaKey || event.ctrlKey;
      const isAltPressed = event.altKey;

      if (!isMetaPressed && !isAltPressed && !event.shiftKey && event.key.toLowerCase() === "a") {
        event.preventDefault();
        onDecision(subtask.subTaskId, PlanDecision.APPROVE);
        return;
      }

      if (!isMetaPressed && !isAltPressed && !event.shiftKey && event.key.toLowerCase() === "v") {
        event.preventDefault();
        if (!showReviseInput) {
          setShowReviseInput(true);
        } else {
          revisionInputRef.current?.focus();
        }
        return;
      }

      if (!isMetaPressed && !isAltPressed && event.shiftKey && event.key === "X") {
        event.preventDefault();
        onDecision(subtask.subTaskId, PlanDecision.REJECT);
        return;
      }

    };

    document.addEventListener("keydown", handleKeyDown);
    return () => {
      document.removeEventListener("keydown", handleKeyDown);
    };
  }, [onDecision, showReviseInput, submitRevision, subtask.status, subtask.subTaskId]);

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
            ref={revisionInputRef}
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
            onKeyDown={(event) => {
              if (event.isComposing || event.keyCode === 229) {
                return;
              }
              if ((event.metaKey || event.ctrlKey) && event.key === "Enter") {
                event.preventDefault();
                submitRevision();
              }
            }}
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
            onClick={submitRevision}
            disabled={!revisionNote.trim()}
            data-testid="submit-revision-button"
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
