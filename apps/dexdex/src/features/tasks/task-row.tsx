import { useNavigate } from "react-router";
import { cn } from "../../lib/cn";
import { StatusDot } from "../../components/status-badge";
import {
  ActionType,
  actionTypeLabels,
  UnitTaskStatus,
} from "../../lib/status";
import { formatRelativeTime } from "../../lib/time";
import { useAppStore } from "../../stores/app-store";
import type { MockUnitTask } from "../../lib/mock-data";

interface TaskRowProps {
  task: MockUnitTask;
  isSelected: boolean;
}

export function TaskRow({ task, isSelected }: TaskRowProps) {
  const navigate = useNavigate();
  const openTab = useAppStore((s) => s.openTab);

  function handleClick() {
    openTab({
      id: task.unitTaskId,
      title: task.title,
      path: `/tasks/${task.unitTaskId}`,
    });
    navigate(`/tasks/${task.unitTaskId}`);
  }

  return (
    <button
      onClick={handleClick}
      className={cn(
        "flex items-center w-full gap-3 px-6 py-2.5 text-left border-b border-[var(--color-border-subtle)] cursor-pointer transition-colors",
        isSelected
          ? "bg-[var(--color-bg-active)]"
          : "hover:bg-[var(--color-bg-hover)]",
      )}
      type="button"
    >
      {/* Status dot */}
      <StatusDot status={task.status} variant="unit" />

      {/* Task content */}
      <div className="flex-1 min-w-0">
        <div className="flex items-center gap-2">
          <span className="text-[13px] font-medium text-[var(--color-text-primary)] truncate">
            {task.title}
          </span>
        </div>
        <span className="text-[11px] text-[var(--color-text-tertiary)]">
          {task.unitTaskId}
        </span>
      </div>

      {/* Right side: badges and time */}
      <div className="flex items-center gap-2 shrink-0">
        {task.subTasks.length > 0 && (
          <span className="text-[11px] text-[var(--color-text-tertiary)] bg-[var(--color-bg-secondary)] px-1.5 py-0.5 rounded-sm">
            {task.subTasks.length} sub
          </span>
        )}

        {task.actionRequired !== ActionType.UNSPECIFIED && (
          <span className="text-[11px] font-medium text-[var(--color-status-action-required)] bg-[var(--color-status-action-required)]/10 px-1.5 py-0.5 rounded-sm">
            {actionTypeLabels[task.actionRequired]}
          </span>
        )}

        <span className="text-[11px] text-[var(--color-text-tertiary)] w-14 text-right">
          {formatRelativeTime(task.updatedAt)}
        </span>
      </div>
    </button>
  );
}
