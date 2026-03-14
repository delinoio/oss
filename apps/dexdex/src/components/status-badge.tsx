import { cn } from "../lib/cn";
import {
  type UnitTaskStatus,
  type SubTaskStatus,
  unitTaskStatusConfig,
  subTaskStatusConfig,
} from "../lib/status";

interface StatusBadgeProps {
  status: UnitTaskStatus | SubTaskStatus;
  variant?: "unit" | "sub";
  className?: string;
}

export function StatusBadge({
  status,
  variant = "unit",
  className,
}: StatusBadgeProps) {
  const config =
    variant === "unit"
      ? unitTaskStatusConfig[status as UnitTaskStatus]
      : subTaskStatusConfig[status as SubTaskStatus];

  if (!config) {
    return null;
  }

  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 text-[11px] font-medium px-1.5 py-0.5 rounded-sm",
        config.bgClass,
        className,
      )}
    >
      <span className={cn("w-1.5 h-1.5 rounded-full shrink-0", config.dotClass)} />
      {config.label}
    </span>
  );
}

interface StatusDotProps {
  status: UnitTaskStatus | SubTaskStatus;
  variant?: "unit" | "sub";
  className?: string;
}

export function StatusDot({
  status,
  variant = "unit",
  className,
}: StatusDotProps) {
  const config =
    variant === "unit"
      ? unitTaskStatusConfig[status as UnitTaskStatus]
      : subTaskStatusConfig[status as SubTaskStatus];

  if (!config) {
    return null;
  }

  return (
    <span
      className={cn("w-2 h-2 rounded-full shrink-0", config.dotClass, className)}
      title={config.label}
    />
  );
}
