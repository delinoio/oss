import { StatusBadge } from "../../components/status-badge";
import { SubTaskStatus, subTaskTypeLabels } from "../../lib/status";
import { formatRelativeTime } from "../../lib/time";
import type { MockSubTask } from "../../lib/mock-data";
import { PlanDecisionControls } from "./plan-decision-controls";

interface SubTaskTimelineProps {
  subTasks: MockSubTask[];
}

export function SubTaskTimeline({ subTasks }: SubTaskTimelineProps) {
  if (subTasks.length === 0) {
    return (
      <div className="text-[13px] text-[var(--color-text-tertiary)] py-4">
        No subtasks yet
      </div>
    );
  }

  return (
    <div className="relative pl-4">
      {/* Vertical line */}
      <div className="absolute left-[7px] top-2 bottom-2 w-px bg-[var(--color-border-default)]" />

      {subTasks.map((subTask, index) => (
        <div key={subTask.subTaskId} className="relative flex gap-3 pb-4 last:pb-0">
          {/* Timeline dot */}
          <div className="relative z-10 mt-1.5">
            <div className="w-2.5 h-2.5 rounded-full border-2 border-[var(--color-border-default)] bg-[var(--color-bg-primary)]" />
          </div>

          {/* Content */}
          <div className="flex-1 min-w-0">
            <div className="flex items-center gap-2 mb-1">
              <span className="text-[13px] font-medium text-[var(--color-text-primary)]">
                {subTaskTypeLabels[subTask.type]}
              </span>
              <StatusBadge status={subTask.status} variant="sub" />
            </div>

            <div className="flex items-center gap-2 text-[11px] text-[var(--color-text-tertiary)]">
              <span>{subTask.subTaskId}</span>
              <span>{formatRelativeTime(subTask.createdAt)}</span>
            </div>

            {/* Plan decision controls for waiting approval */}
            {subTask.status === SubTaskStatus.WAITING_FOR_PLAN_APPROVAL && (
              <div className="mt-3">
                <PlanDecisionControls subTaskId={subTask.subTaskId} />
              </div>
            )}
          </div>
        </div>
      ))}
    </div>
  );
}
