/**
 * DexDex status enums and configurations.
 * These mirror the proto definitions but are self-contained for frontend use.
 */

export enum UnitTaskStatus {
  UNSPECIFIED = "UNSPECIFIED",
  QUEUED = "QUEUED",
  IN_PROGRESS = "IN_PROGRESS",
  ACTION_REQUIRED = "ACTION_REQUIRED",
  BLOCKED = "BLOCKED",
  COMPLETED = "COMPLETED",
  FAILED = "FAILED",
  CANCELLED = "CANCELLED",
}

export enum SubTaskType {
  UNSPECIFIED = "UNSPECIFIED",
  INITIAL_IMPLEMENTATION = "INITIAL_IMPLEMENTATION",
  REQUEST_CHANGES = "REQUEST_CHANGES",
  PR_CREATE = "PR_CREATE",
  PR_REVIEW_FIX = "PR_REVIEW_FIX",
  PR_CI_FIX = "PR_CI_FIX",
  MANUAL_RETRY = "MANUAL_RETRY",
}

export enum SubTaskStatus {
  UNSPECIFIED = "UNSPECIFIED",
  QUEUED = "QUEUED",
  IN_PROGRESS = "IN_PROGRESS",
  WAITING_FOR_PLAN_APPROVAL = "WAITING_FOR_PLAN_APPROVAL",
  WAITING_FOR_USER_INPUT = "WAITING_FOR_USER_INPUT",
  COMPLETED = "COMPLETED",
  FAILED = "FAILED",
  CANCELLED = "CANCELLED",
}

export enum SubTaskCompletionReason {
  UNSPECIFIED = "UNSPECIFIED",
  SUCCEEDED = "SUCCEEDED",
  REVISED = "REVISED",
  PLAN_REJECTED = "PLAN_REJECTED",
  FAILED = "FAILED",
  CANCELLED_BY_USER = "CANCELLED_BY_USER",
}

export enum AgentSessionStatus {
  UNSPECIFIED = "UNSPECIFIED",
  STARTING = "STARTING",
  RUNNING = "RUNNING",
  WAITING_FOR_INPUT = "WAITING_FOR_INPUT",
  COMPLETED = "COMPLETED",
  FAILED = "FAILED",
  CANCELLED = "CANCELLED",
}

export enum SessionOutputKind {
  UNSPECIFIED = "UNSPECIFIED",
  TEXT = "TEXT",
  PLAN_UPDATE = "PLAN_UPDATE",
  TOOL_CALL = "TOOL_CALL",
  TOOL_RESULT = "TOOL_RESULT",
  PROGRESS = "PROGRESS",
  WARNING = "WARNING",
  ERROR = "ERROR",
}

export enum StreamEventType {
  UNSPECIFIED = "UNSPECIFIED",
  TASK_UPDATED = "TASK_UPDATED",
  SUBTASK_UPDATED = "SUBTASK_UPDATED",
  SESSION_OUTPUT = "SESSION_OUTPUT",
  SESSION_STATE_CHANGED = "SESSION_STATE_CHANGED",
  PR_UPDATED = "PR_UPDATED",
  REVIEW_ASSIST_UPDATED = "REVIEW_ASSIST_UPDATED",
  INLINE_COMMENT_UPDATED = "INLINE_COMMENT_UPDATED",
  NOTIFICATION_CREATED = "NOTIFICATION_CREATED",
  SESSION_FORK_UPDATED = "SESSION_FORK_UPDATED",
  WORKSPACE_WORK_STATUS_UPDATED = "WORKSPACE_WORK_STATUS_UPDATED",
}

export enum PlanDecision {
  UNSPECIFIED = "UNSPECIFIED",
  APPROVE = "APPROVE",
  REVISE = "REVISE",
  REJECT = "REJECT",
}

export enum NotificationType {
  UNSPECIFIED = "UNSPECIFIED",
  TASK_ACTION_REQUIRED = "TASK_ACTION_REQUIRED",
  PLAN_ACTION_REQUIRED = "PLAN_ACTION_REQUIRED",
  PR_REVIEW_ACTIVITY = "PR_REVIEW_ACTIVITY",
  PR_CI_FAILURE = "PR_CI_FAILURE",
  AGENT_SESSION_FAILED = "AGENT_SESSION_FAILED",
  AGENT_INPUT_REQUIRED = "AGENT_INPUT_REQUIRED",
}

export enum SessionForkStatus {
  UNSPECIFIED = "UNSPECIFIED",
  ACTIVE = "ACTIVE",
  ARCHIVED = "ARCHIVED",
}

export enum SessionForkIntent {
  UNSPECIFIED = "UNSPECIFIED",
  EXPLORE_ALTERNATIVE = "EXPLORE_ALTERNATIVE",
  BRANCH_EXPERIMENT = "BRANCH_EXPERIMENT",
}

export enum WorkspaceWorkStatus {
  UNSPECIFIED = "UNSPECIFIED",
  FAILED = "FAILED",
  ACTION_REQUIRED = "ACTION_REQUIRED",
  WAITING_FOR_INPUT = "WAITING_FOR_INPUT",
  RUNNING = "RUNNING",
  IDLE = "IDLE",
  DISCONNECTED = "DISCONNECTED",
}

export enum AgentCliType {
  UNSPECIFIED = "UNSPECIFIED",
  CODEX_CLI = "CODEX_CLI",
  CLAUDE_CODE = "CLAUDE_CODE",
  OPENCODE = "OPENCODE",
}

export interface StatusConfig {
  label: string;
  color: string;
  bgColor: string;
  icon: string;
}

export const UNIT_TASK_STATUS_CONFIG: Record<UnitTaskStatus, StatusConfig> = {
  [UnitTaskStatus.UNSPECIFIED]: { label: "Unknown", color: "var(--color-text-tertiary)", bgColor: "var(--color-bg-tertiary)", icon: "?" },
  [UnitTaskStatus.QUEUED]: { label: "Queued", color: "var(--color-status-queued)", bgColor: "var(--color-status-queued-bg)", icon: "\u25CB" },
  [UnitTaskStatus.IN_PROGRESS]: { label: "In Progress", color: "var(--color-status-in-progress)", bgColor: "var(--color-status-in-progress-bg)", icon: "\u25D4" },
  [UnitTaskStatus.ACTION_REQUIRED]: { label: "Action Required", color: "var(--color-status-action)", bgColor: "var(--color-status-action-bg)", icon: "!" },
  [UnitTaskStatus.BLOCKED]: { label: "Blocked", color: "var(--color-status-blocked)", bgColor: "var(--color-status-blocked-bg)", icon: "\u25A0" },
  [UnitTaskStatus.COMPLETED]: { label: "Completed", color: "var(--color-status-completed)", bgColor: "var(--color-status-completed-bg)", icon: "\u2713" },
  [UnitTaskStatus.FAILED]: { label: "Failed", color: "var(--color-status-failed)", bgColor: "var(--color-status-failed-bg)", icon: "\u2717" },
  [UnitTaskStatus.CANCELLED]: { label: "Cancelled", color: "var(--color-status-cancelled)", bgColor: "var(--color-status-cancelled-bg)", icon: "\u2014" },
};

export const SUB_TASK_TYPE_LABELS: Record<SubTaskType, string> = {
  [SubTaskType.UNSPECIFIED]: "Unknown",
  [SubTaskType.INITIAL_IMPLEMENTATION]: "Initial Implementation",
  [SubTaskType.REQUEST_CHANGES]: "Request Changes",
  [SubTaskType.PR_CREATE]: "Create PR",
  [SubTaskType.PR_REVIEW_FIX]: "PR Review Fix",
  [SubTaskType.PR_CI_FIX]: "PR CI Fix",
  [SubTaskType.MANUAL_RETRY]: "Manual Retry",
};
