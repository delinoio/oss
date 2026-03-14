import { useCallback, useMemo, useState } from "react";
import { Plus } from "lucide-react";
import { mockTasks } from "../../lib/mock-data";
import { type UnitTaskStatus } from "../../lib/status";
import { TaskFilters, type TaskFilter } from "./task-filters";
import { TaskRow } from "./task-row";
import { CreateTaskDialog } from "./create-task-dialog";

export function TaskListPage() {
  const [activeFilter, setActiveFilter] = useState<TaskFilter>("all");
  const [searchQuery, setSearchQuery] = useState("");
  const [selectedIndex, setSelectedIndex] = useState(0);
  const [showCreateDialog, setShowCreateDialog] = useState(false);

  const filteredTasks = useMemo(() => {
    let tasks = mockTasks;

    if (activeFilter !== "all") {
      tasks = tasks.filter((t) => t.status === (activeFilter as UnitTaskStatus));
    }

    if (searchQuery.trim()) {
      const q = searchQuery.toLowerCase();
      tasks = tasks.filter(
        (t) =>
          t.title.toLowerCase().includes(q) ||
          t.unitTaskId.toLowerCase().includes(q),
      );
    }

    return tasks;
  }, [activeFilter, searchQuery]);

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (e.key === "j" && !e.metaKey && !e.ctrlKey) {
        e.preventDefault();
        setSelectedIndex((prev) => Math.min(prev + 1, filteredTasks.length - 1));
      } else if (e.key === "k" && !e.metaKey && !e.ctrlKey) {
        e.preventDefault();
        setSelectedIndex((prev) => Math.max(prev - 1, 0));
      }
    },
    [filteredTasks.length],
  );

  return (
    <div
      className="flex flex-col h-full"
      onKeyDown={handleKeyDown}
      tabIndex={0}
      role="listbox"
    >
      {/* Header */}
      <div className="flex items-center justify-between px-6 py-3 border-b border-[var(--color-border-default)]">
        <h1 className="text-[15px] font-semibold text-[var(--color-text-primary)]">
          Tasks
        </h1>
        <button
          onClick={() => setShowCreateDialog(true)}
          className="flex items-center gap-1.5 px-3 py-1.5 text-[12px] font-medium bg-[var(--color-bg-accent)] text-[var(--color-text-on-accent)] rounded hover:bg-[var(--color-bg-accent-hover)] transition-colors"
          type="button"
        >
          <Plus size={14} />
          New Task
        </button>
      </div>

      {/* Filters */}
      <TaskFilters
        activeFilter={activeFilter}
        onFilterChange={setActiveFilter}
        searchQuery={searchQuery}
        onSearchChange={setSearchQuery}
      />

      {/* Task list */}
      <div className="flex-1 overflow-y-auto">
        {filteredTasks.length === 0 ? (
          <div className="flex items-center justify-center h-full text-[13px] text-[var(--color-text-tertiary)]">
            No tasks found
          </div>
        ) : (
          filteredTasks.map((task, index) => (
            <TaskRow
              key={task.unitTaskId}
              task={task}
              isSelected={index === selectedIndex}
            />
          ))
        )}
      </div>

      {/* Create task dialog */}
      {showCreateDialog && (
        <CreateTaskDialog onClose={() => setShowCreateDialog(false)} />
      )}
    </div>
  );
}
