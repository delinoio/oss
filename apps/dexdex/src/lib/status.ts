/**
 * Status configuration for UnitTaskStatus and SubTaskStatus enums.
 * Maps proto enum values to display labels and colors.
 */

export enum UnitTaskStatus {
  UNSPECIFIED = 0,
  QUEUED = 1,
  IN_PROGRESS = 2,
  ACTION_REQUIRED = 3,
  BLOCKED = 4,
  COMPLETED = 5,
  FAILED = 6,
  CANCELLED = 7,
}

export enum SubTaskStatus {
  UNSPECIFIED = 0,
  QUEUED = 1,
  IN_PROGRESS = 2,
  WAITING_FOR_PLAN_APPROVAL = 3,
  WAITING_FOR_USER_INPUT = 4,
  COMPLETED = 5,
  FAILED = 6,
  CANCELLED = 7,
}

export enum SubTaskType {
  UNSPECIFIED = 0,
  INITIAL_IMPLEMENTATION = 1,
  REQUEST_CHANGES = 2,
  PR_CREATE = 3,
  PR_REVIEW_FIX = 4,
  PR_CI_FIX = 5,
  MANUAL_RETRY = 6,
}

export enum ActionType {
  UNSPECIFIED = 0,
  REVIEW_REQUESTED = 1,
  PR_CREATION_READY = 2,
  PLAN_APPROVAL_REQUIRED = 3,
  CI_FAILED = 4,
  MERGE_CONFLICT = 5,
  SECURITY_ALERT = 6,
  USER_INPUT_REQUIRED = 7,
}

export enum PlanDecision {
  UNSPECIFIED = 0,
  APPROVE = 1,
  REVISE = 2,
  REJECT = 3,
}

export enum NotificationType {
  UNSPECIFIED = 0,
  TASK_ACTION_REQUIRED = 1,
  PLAN_ACTION_REQUIRED = 2,
  PR_REVIEW_ACTIVITY = 3,
  PR_CI_FAILURE = 4,
  AGENT_SESSION_FAILED = 5,
}

export interface StatusConfig {
  label: string;
  color: string;
  dotClass: string;
  bgClass: string;
}

export const unitTaskStatusConfig: Record<UnitTaskStatus, StatusConfig> = {
  [UnitTaskStatus.UNSPECIFIED]: {
    label: "Unknown",
    color: "var(--color-status-queued)",
    dotClass: "bg-[var(--color-status-queued)]",
    bgClass: "bg-[var(--color-status-queued)]/10 text-[var(--color-status-queued)]",
  },
  [UnitTaskStatus.QUEUED]: {
    label: "Queued",
    color: "var(--color-status-queued)",
    dotClass: "bg-[var(--color-status-queued)]",
    bgClass: "bg-[var(--color-status-queued)]/10 text-[var(--color-status-queued)]",
  },
  [UnitTaskStatus.IN_PROGRESS]: {
    label: "In Progress",
    color: "var(--color-status-in-progress)",
    dotClass: "bg-[var(--color-status-in-progress)]",
    bgClass: "bg-[var(--color-status-in-progress)]/10 text-[var(--color-status-in-progress)]",
  },
  [UnitTaskStatus.ACTION_REQUIRED]: {
    label: "Action Required",
    color: "var(--color-status-action-required)",
    dotClass: "bg-[var(--color-status-action-required)]",
    bgClass: "bg-[var(--color-status-action-required)]/10 text-[var(--color-status-action-required)]",
  },
  [UnitTaskStatus.BLOCKED]: {
    label: "Blocked",
    color: "var(--color-status-blocked)",
    dotClass: "bg-[var(--color-status-blocked)]",
    bgClass: "bg-[var(--color-status-blocked)]/10 text-[var(--color-status-blocked)]",
  },
  [UnitTaskStatus.COMPLETED]: {
    label: "Completed",
    color: "var(--color-status-completed)",
    dotClass: "bg-[var(--color-status-completed)]",
    bgClass: "bg-[var(--color-status-completed)]/10 text-[var(--color-status-completed)]",
  },
  [UnitTaskStatus.FAILED]: {
    label: "Failed",
    color: "var(--color-status-failed)",
    dotClass: "bg-[var(--color-status-failed)]",
    bgClass: "bg-[var(--color-status-failed)]/10 text-[var(--color-status-failed)]",
  },
  [UnitTaskStatus.CANCELLED]: {
    label: "Cancelled",
    color: "var(--color-status-cancelled)",
    dotClass: "bg-[var(--color-status-cancelled)]",
    bgClass: "bg-[var(--color-status-cancelled)]/10 text-[var(--color-status-cancelled)]",
  },
};

export const subTaskStatusConfig: Record<SubTaskStatus, StatusConfig> = {
  [SubTaskStatus.UNSPECIFIED]: {
    label: "Unknown",
    color: "var(--color-status-queued)",
    dotClass: "bg-[var(--color-status-queued)]",
    bgClass: "bg-[var(--color-status-queued)]/10 text-[var(--color-status-queued)]",
  },
  [SubTaskStatus.QUEUED]: {
    label: "Queued",
    color: "var(--color-status-queued)",
    dotClass: "bg-[var(--color-status-queued)]",
    bgClass: "bg-[var(--color-status-queued)]/10 text-[var(--color-status-queued)]",
  },
  [SubTaskStatus.IN_PROGRESS]: {
    label: "In Progress",
    color: "var(--color-status-in-progress)",
    dotClass: "bg-[var(--color-status-in-progress)]",
    bgClass: "bg-[var(--color-status-in-progress)]/10 text-[var(--color-status-in-progress)]",
  },
  [SubTaskStatus.WAITING_FOR_PLAN_APPROVAL]: {
    label: "Waiting for Approval",
    color: "var(--color-status-action-required)",
    dotClass: "bg-[var(--color-status-action-required)]",
    bgClass: "bg-[var(--color-status-action-required)]/10 text-[var(--color-status-action-required)]",
  },
  [SubTaskStatus.WAITING_FOR_USER_INPUT]: {
    label: "Waiting for Input",
    color: "var(--color-status-action-required)",
    dotClass: "bg-[var(--color-status-action-required)]",
    bgClass: "bg-[var(--color-status-action-required)]/10 text-[var(--color-status-action-required)]",
  },
  [SubTaskStatus.COMPLETED]: {
    label: "Completed",
    color: "var(--color-status-completed)",
    dotClass: "bg-[var(--color-status-completed)]",
    bgClass: "bg-[var(--color-status-completed)]/10 text-[var(--color-status-completed)]",
  },
  [SubTaskStatus.FAILED]: {
    label: "Failed",
    color: "var(--color-status-failed)",
    dotClass: "bg-[var(--color-status-failed)]",
    bgClass: "bg-[var(--color-status-failed)]/10 text-[var(--color-status-failed)]",
  },
  [SubTaskStatus.CANCELLED]: {
    label: "Cancelled",
    color: "var(--color-status-cancelled)",
    dotClass: "bg-[var(--color-status-cancelled)]",
    bgClass: "bg-[var(--color-status-cancelled)]/10 text-[var(--color-status-cancelled)]",
  },
};

export const subTaskTypeLabels: Record<SubTaskType, string> = {
  [SubTaskType.UNSPECIFIED]: "Unknown",
  [SubTaskType.INITIAL_IMPLEMENTATION]: "Initial Implementation",
  [SubTaskType.REQUEST_CHANGES]: "Request Changes",
  [SubTaskType.PR_CREATE]: "PR Creation",
  [SubTaskType.PR_REVIEW_FIX]: "PR Review Fix",
  [SubTaskType.PR_CI_FIX]: "CI Fix",
  [SubTaskType.MANUAL_RETRY]: "Manual Retry",
};

export const actionTypeLabels: Record<ActionType, string> = {
  [ActionType.UNSPECIFIED]: "",
  [ActionType.REVIEW_REQUESTED]: "Review Requested",
  [ActionType.PR_CREATION_READY]: "PR Ready",
  [ActionType.PLAN_APPROVAL_REQUIRED]: "Plan Approval",
  [ActionType.CI_FAILED]: "CI Failed",
  [ActionType.MERGE_CONFLICT]: "Merge Conflict",
  [ActionType.SECURITY_ALERT]: "Security Alert",
  [ActionType.USER_INPUT_REQUIRED]: "Input Required",
};

export const notificationTypeLabels: Record<NotificationType, string> = {
  [NotificationType.UNSPECIFIED]: "Notification",
  [NotificationType.TASK_ACTION_REQUIRED]: "Task Action Required",
  [NotificationType.PLAN_ACTION_REQUIRED]: "Plan Action Required",
  [NotificationType.PR_REVIEW_ACTIVITY]: "PR Review Activity",
  [NotificationType.PR_CI_FAILURE]: "PR CI Failure",
  [NotificationType.AGENT_SESSION_FAILED]: "Agent Session Failed",
};
