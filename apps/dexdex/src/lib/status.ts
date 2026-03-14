import { UnitTaskStatus, SubTaskStatus } from "../gen/v1/dexdex_pb";

export const unitTaskStatusConfig: Record<
  number,
  { label: string; color: string; dotClass: string }
> = {
  [UnitTaskStatus.QUEUED]: {
    label: "Queued",
    color: "var(--color-status-queued)",
    dotClass: "bg-status-queued",
  },
  [UnitTaskStatus.IN_PROGRESS]: {
    label: "In Progress",
    color: "var(--color-status-in-progress)",
    dotClass: "bg-status-in-progress",
  },
  [UnitTaskStatus.ACTION_REQUIRED]: {
    label: "Action Required",
    color: "var(--color-status-action-required)",
    dotClass: "bg-status-action-required",
  },
  [UnitTaskStatus.BLOCKED]: {
    label: "Blocked",
    color: "var(--color-status-blocked)",
    dotClass: "bg-status-blocked",
  },
  [UnitTaskStatus.COMPLETED]: {
    label: "Completed",
    color: "var(--color-status-completed)",
    dotClass: "bg-status-completed",
  },
  [UnitTaskStatus.FAILED]: {
    label: "Failed",
    color: "var(--color-status-failed)",
    dotClass: "bg-status-failed",
  },
  [UnitTaskStatus.CANCELLED]: {
    label: "Cancelled",
    color: "var(--color-status-cancelled)",
    dotClass: "bg-status-cancelled",
  },
};

export const subTaskStatusConfig: Record<
  number,
  { label: string; color: string }
> = {
  [SubTaskStatus.QUEUED]: {
    label: "Queued",
    color: "var(--color-status-queued)",
  },
  [SubTaskStatus.IN_PROGRESS]: {
    label: "In Progress",
    color: "var(--color-status-in-progress)",
  },
  [SubTaskStatus.WAITING_FOR_PLAN_APPROVAL]: {
    label: "Waiting for Approval",
    color: "var(--color-status-action-required)",
  },
  [SubTaskStatus.WAITING_FOR_USER_INPUT]: {
    label: "Waiting for Input",
    color: "var(--color-status-action-required)",
  },
  [SubTaskStatus.COMPLETED]: {
    label: "Completed",
    color: "var(--color-status-completed)",
  },
  [SubTaskStatus.FAILED]: {
    label: "Failed",
    color: "var(--color-status-failed)",
  },
  [SubTaskStatus.CANCELLED]: {
    label: "Cancelled",
    color: "var(--color-status-cancelled)",
  },
};
