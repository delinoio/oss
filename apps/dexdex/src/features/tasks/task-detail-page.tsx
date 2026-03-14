import { useParams, useNavigate } from "react-router";
import { ArrowLeft } from "lucide-react";
import { StatusBadge } from "../../components/status-badge";
import { mockTasks } from "../../lib/mock-data";
import { SubTaskTimeline } from "./subtask-timeline";

export function TaskDetailPage() {
  const { taskId } = useParams<{ taskId: string }>();
  const navigate = useNavigate();

  const task = mockTasks.find((t) => t.unitTaskId === taskId);

  if (!task) {
    return (
      <div className="flex items-center justify-center h-full text-[13px] text-[var(--color-text-tertiary)]">
        Task not found
      </div>
    );
  }

  return (
    <div className="flex flex-col h-full overflow-y-auto">
      {/* Header */}
      <div className="px-6 py-4 border-b border-[var(--color-border-default)]">
        <button
          onClick={() => navigate("/tasks")}
          className="flex items-center gap-1.5 text-[12px] text-[var(--color-text-secondary)] hover:text-[var(--color-text-primary)] transition-colors mb-3"
          type="button"
        >
          <ArrowLeft size={14} />
          Back to Tasks
        </button>

        <div className="flex items-center gap-3 mb-2">
          <h1 className="text-[18px] font-semibold text-[var(--color-text-primary)]">
            {task.title}
          </h1>
          <StatusBadge status={task.status} variant="unit" />
        </div>

        <span className="text-[12px] text-[var(--color-text-tertiary)]">
          {task.unitTaskId}
        </span>
      </div>

      {/* Description */}
      <div className="px-6 py-4 border-b border-[var(--color-border-default)]">
        <h2 className="text-[13px] font-semibold text-[var(--color-text-primary)] mb-2">
          Description
        </h2>
        <p className="text-[13px] text-[var(--color-text-secondary)] leading-relaxed">
          {task.description}
        </p>
      </div>

      {/* SubTask timeline */}
      <div className="px-6 py-4">
        <h2 className="text-[13px] font-semibold text-[var(--color-text-primary)] mb-4">
          SubTasks
        </h2>
        <SubTaskTimeline subTasks={task.subTasks} />
      </div>
    </div>
  );
}
