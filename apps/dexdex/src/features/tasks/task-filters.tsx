import { Search } from "lucide-react";
import { cn } from "../../lib/cn";
import { UnitTaskStatus } from "../../lib/status";

export type TaskFilter = "all" | UnitTaskStatus;

interface FilterOption {
  value: TaskFilter;
  label: string;
}

const filterOptions: FilterOption[] = [
  { value: "all", label: "All" },
  { value: UnitTaskStatus.QUEUED, label: "Queued" },
  { value: UnitTaskStatus.IN_PROGRESS, label: "In Progress" },
  { value: UnitTaskStatus.ACTION_REQUIRED, label: "Action Required" },
  { value: UnitTaskStatus.COMPLETED, label: "Completed" },
  { value: UnitTaskStatus.FAILED, label: "Failed" },
];

interface TaskFiltersProps {
  activeFilter: TaskFilter;
  onFilterChange: (filter: TaskFilter) => void;
  searchQuery: string;
  onSearchChange: (query: string) => void;
}

export function TaskFilters({
  activeFilter,
  onFilterChange,
  searchQuery,
  onSearchChange,
}: TaskFiltersProps) {
  return (
    <div className="flex items-center justify-between gap-3 px-6 py-2 border-b border-[var(--color-border-default)]">
      <div className="flex items-center gap-1">
        {filterOptions.map((option) => (
          <button
            key={String(option.value)}
            onClick={() => onFilterChange(option.value)}
            className={cn(
              "px-2.5 py-1 text-[12px] font-medium rounded-sm transition-colors",
              activeFilter === option.value
                ? "bg-[var(--color-bg-accent)] text-[var(--color-text-on-accent)]"
                : "text-[var(--color-text-secondary)] hover:text-[var(--color-text-primary)] hover:bg-[var(--color-bg-hover)]",
            )}
            type="button"
          >
            {option.label}
          </button>
        ))}
      </div>

      <div className="relative">
        <Search
          size={14}
          className="absolute left-2.5 top-1/2 -translate-y-1/2 text-[var(--color-text-tertiary)]"
        />
        <input
          type="text"
          placeholder="Search tasks..."
          value={searchQuery}
          onChange={(e) => onSearchChange(e.target.value)}
          className="w-56 pl-8 pr-3 py-1.5 text-[12px] bg-[var(--color-bg-secondary)] border border-[var(--color-border-default)] rounded text-[var(--color-text-primary)] placeholder:text-[var(--color-text-tertiary)] focus:outline-none focus:border-[var(--color-border-accent)] transition-colors"
        />
      </div>
    </div>
  );
}
