/**
 * Status badge component for displaying task and subtask status.
 */

import type { CSSProperties } from "react";
import { UnitTaskStatus, UNIT_TASK_STATUS_CONFIG } from "../lib/status";

interface StatusBadgeProps {
  status: UnitTaskStatus;
  size?: "sm" | "md";
}

export function StatusBadge({ status, size = "md" }: StatusBadgeProps) {
  const config = UNIT_TASK_STATUS_CONFIG[status];
  const fontSize = size === "sm" ? "var(--font-size-xs)" : "var(--font-size-sm)";
  const padding = size === "sm" ? "1px 6px" : "2px 8px";

  const style: CSSProperties = {
    display: "inline-flex",
    alignItems: "center",
    gap: "4px",
    fontSize,
    fontWeight: 500,
    padding,
    borderRadius: "var(--radius-full)",
    color: config.color,
    backgroundColor: config.bgColor,
    whiteSpace: "nowrap",
  };

  return (
    <span style={style} data-testid="status-badge">
      <span style={{ fontSize: "10px" }}>{config.icon}</span>
      {config.label}
    </span>
  );
}
