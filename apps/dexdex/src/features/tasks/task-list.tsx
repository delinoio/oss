/**
 * Task list view displaying all unit tasks with status and filtering.
 */

import { type CSSProperties, useState } from "react";
import { StatusBadge } from "../../components/status-badge";
import { TaskListSkeleton } from "../../components/skeleton-loader";
import type { UnitTask } from "../../lib/mock-data";
import { UnitTaskStatus } from "../../lib/status";

interface TaskListProps {
  tasks: UnitTask[];
  isLoading?: boolean;
  onTaskSelect: (taskId: string) => void;
  onCreateTask: () => void;
}

const FILTER_OPTIONS = [
  { value: "all", label: "All" },
  { value: UnitTaskStatus.IN_PROGRESS, label: "In Progress" },
  { value: UnitTaskStatus.ACTION_REQUIRED, label: "Action Required" },
  { value: UnitTaskStatus.QUEUED, label: "Queued" },
  { value: UnitTaskStatus.COMPLETED, label: "Completed" },
  { value: UnitTaskStatus.FAILED, label: "Failed" },
];

export function TaskList({ tasks, isLoading, onTaskSelect, onCreateTask }: TaskListProps) {
  const [filter, setFilter] = useState<string>("all");

  const filteredTasks = filter === "all"
    ? tasks
    : tasks.filter((t) => t.status === filter);

  const containerStyle: CSSProperties = {
    height: "100%",
    display: "flex",
    flexDirection: "column",
    overflow: "hidden",
  };

  const headerStyle: CSSProperties = {
    padding: "var(--space-4) var(--space-6)",
    borderBottom: "1px solid var(--color-border)",
    display: "flex",
    alignItems: "center",
    justifyContent: "space-between",
    flexShrink: 0,
  };

  const filterBarStyle: CSSProperties = {
    display: "flex",
    gap: "var(--space-1)",
    padding: "var(--space-3) var(--space-6)",
    borderBottom: "1px solid var(--color-border)",
    flexShrink: 0,
  };

  const listStyle: CSSProperties = {
    flex: 1,
    overflowY: "auto",
  };

  const createButtonStyle: CSSProperties = {
    padding: "var(--space-2) var(--space-3)",
    borderRadius: "var(--radius-md)",
    backgroundColor: "var(--color-accent)",
    color: "var(--color-text-inverse)",
    fontSize: "var(--font-size-sm)",
    fontWeight: 500,
    cursor: "pointer",
  };

  return (
    <div style={containerStyle} data-testid="task-list">
      <div style={headerStyle}>
        <h1
          style={{
            fontSize: "var(--font-size-xl)",
            fontWeight: 600,
          }}
        >
          Tasks
        </h1>
        <button
          style={createButtonStyle}
          onClick={onCreateTask}
          data-testid="create-task-button"
        >
          + New Task
        </button>
      </div>

      <div style={filterBarStyle}>
        {FILTER_OPTIONS.map((opt) => {
          const isActive = filter === opt.value;
          const chipStyle: CSSProperties = {
            padding: "var(--space-1) var(--space-3)",
            borderRadius: "var(--radius-full)",
            fontSize: "var(--font-size-sm)",
            fontWeight: isActive ? 500 : 400,
            color: isActive ? "var(--color-accent)" : "var(--color-text-secondary)",
            backgroundColor: isActive ? "var(--color-accent-subtle)" : "transparent",
            cursor: "pointer",
          };
          return (
            <button
              key={opt.value}
              style={chipStyle}
              onClick={() => setFilter(opt.value)}
              data-testid={`filter-${opt.value}`}
            >
              {opt.label}
            </button>
          );
        })}
      </div>

      <div style={listStyle}>
        {isLoading ? (
          <TaskListSkeleton />
        ) : filteredTasks.length === 0 ? (
          <div
            style={{
              padding: "var(--space-8)",
              textAlign: "center",
              color: "var(--color-text-tertiary)",
              fontSize: "var(--font-size-sm)",
            }}
          >
            No tasks match the current filter
          </div>
        ) : (
          filteredTasks.map((task) => (
            <TaskRow key={task.unitTaskId} task={task} onClick={() => onTaskSelect(task.unitTaskId)} />
          ))
        )}
      </div>
    </div>
  );
}

function TaskRow({ task, onClick }: { task: UnitTask; onClick: () => void }) {
  const rowStyle: CSSProperties = {
    display: "flex",
    alignItems: "center",
    gap: "var(--space-3)",
    padding: "var(--space-3) var(--space-6)",
    borderBottom: "1px solid var(--color-border-subtle)",
    cursor: "pointer",
    transition: "background-color 0.1s",
  };

  const titleStyle: CSSProperties = {
    flex: 1,
    fontSize: "var(--font-size-base)",
    fontWeight: 500,
    color: "var(--color-text-primary)",
    overflow: "hidden",
    textOverflow: "ellipsis",
    whiteSpace: "nowrap",
  };

  const metaStyle: CSSProperties = {
    fontSize: "var(--font-size-xs)",
    color: "var(--color-text-tertiary)",
    whiteSpace: "nowrap",
  };

  return (
    <div
      style={rowStyle}
      onClick={onClick}
      onMouseEnter={(e) => {
        (e.currentTarget as HTMLElement).style.backgroundColor = "var(--color-bg-hover)";
      }}
      onMouseLeave={(e) => {
        (e.currentTarget as HTMLElement).style.backgroundColor = "transparent";
      }}
      data-testid={`task-row-${task.unitTaskId}`}
    >
      <StatusBadge status={task.status} size="sm" />
      <span style={titleStyle}>{task.prompt.split("\n")[0].slice(0, 80) || "Untitled"}</span>
      <span style={metaStyle}>{task.branchRef}</span>
    </div>
  );
}
