import { useState } from "react";
import { Check, RotateCcw, X } from "lucide-react";
import { cn } from "../../lib/cn";
import { PlanDecision } from "../../lib/status";

interface PlanDecisionControlsProps {
  subTaskId: string;
}

export function PlanDecisionControls({ subTaskId }: PlanDecisionControlsProps) {
  const [showReviseInput, setShowReviseInput] = useState(false);
  const [revisionNote, setRevisionNote] = useState("");
  const [submitting, setSubmitting] = useState(false);

  async function handleDecision(decision: PlanDecision) {
    if (decision === PlanDecision.REVISE && !showReviseInput) {
      setShowReviseInput(true);
      return;
    }

    if (decision === PlanDecision.REVISE && !revisionNote.trim()) {
      return;
    }

    setSubmitting(true);
    // TODO: Call SubmitPlanDecision RPC via connect-query mutation
    console.log("Submit plan decision:", {
      subTaskId,
      decision,
      revisionNote: decision === PlanDecision.REVISE ? revisionNote : undefined,
    });

    // Simulate API call
    await new Promise((resolve) => setTimeout(resolve, 500));
    setSubmitting(false);
    setShowReviseInput(false);
    setRevisionNote("");
  }

  return (
    <div className="space-y-2">
      <div className="flex items-center gap-2">
        <button
          onClick={() => handleDecision(PlanDecision.APPROVE)}
          disabled={submitting}
          className={cn(
            "flex items-center gap-1.5 px-3 py-1.5 text-[12px] font-medium rounded transition-colors",
            "bg-[var(--color-approve)]/10 text-[var(--color-approve)] hover:bg-[var(--color-approve)]/20",
            submitting && "opacity-50 cursor-not-allowed",
          )}
          type="button"
        >
          <Check size={14} />
          Approve
        </button>

        <button
          onClick={() => handleDecision(PlanDecision.REVISE)}
          disabled={submitting}
          className={cn(
            "flex items-center gap-1.5 px-3 py-1.5 text-[12px] font-medium rounded transition-colors",
            "bg-[var(--color-revise)]/10 text-[var(--color-revise)] hover:bg-[var(--color-revise)]/20",
            submitting && "opacity-50 cursor-not-allowed",
          )}
          type="button"
        >
          <RotateCcw size={14} />
          Revise
        </button>

        <button
          onClick={() => handleDecision(PlanDecision.REJECT)}
          disabled={submitting}
          className={cn(
            "flex items-center gap-1.5 px-3 py-1.5 text-[12px] font-medium rounded transition-colors",
            "bg-[var(--color-reject)]/10 text-[var(--color-reject)] hover:bg-[var(--color-reject)]/20",
            submitting && "opacity-50 cursor-not-allowed",
          )}
          type="button"
        >
          <X size={14} />
          Reject
        </button>
      </div>

      {showReviseInput && (
        <div className="flex gap-2">
          <textarea
            value={revisionNote}
            onChange={(e) => setRevisionNote(e.target.value)}
            placeholder="Describe the changes needed..."
            className="flex-1 px-3 py-2 text-[12px] bg-[var(--color-bg-secondary)] border border-[var(--color-border-default)] rounded text-[var(--color-text-primary)] placeholder:text-[var(--color-text-tertiary)] focus:outline-none focus:border-[var(--color-border-accent)] resize-none"
            rows={3}
          />
          <button
            onClick={() => handleDecision(PlanDecision.REVISE)}
            disabled={submitting || !revisionNote.trim()}
            className={cn(
              "self-end px-3 py-1.5 text-[12px] font-medium rounded transition-colors",
              "bg-[var(--color-revise)] text-white hover:bg-[var(--color-revise)]/90",
              (submitting || !revisionNote.trim()) && "opacity-50 cursor-not-allowed",
            )}
            type="button"
          >
            Submit
          </button>
        </div>
      )}
    </div>
  );
}
