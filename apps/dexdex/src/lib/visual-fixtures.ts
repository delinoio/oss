import {
  ActionType,
  AgentCliType,
  AgentSessionStatus,
  PrStatus,
  SessionOutputSourceEventType,
  SessionOutputKind,
  StreamEventType,
  SubTaskCompletionReason,
  SubTaskStatus,
  SubTaskType,
  UnitTaskStatus,
  type PullRequestRecord,
  type RepositoryGroup,
  type ReviewAssistItem,
  type ReviewComment,
  type SessionOutputEvent,
  type SessionSummary,
  type StreamWorkspaceEventsResponse,
  type SubTask,
  type UnitTask,
  type WorkspaceOverview,
} from "../gen/v1/dexdex_pb";

function timestamp(iso: string) {
  return {
    seconds: BigInt(Math.floor(new Date(iso).getTime() / 1000)),
    nanos: 0,
  };
}

export const visualWorkspaceOverview = {
  workspaceId: "visual-workspace",
  totalUnitTaskCount: 12,
  actionRequiredUnitTaskCount: 2,
  waitingPlanSubTaskCount: 1,
  failedSubTaskCount: 1,
  activeSessionCount: 4,
  openPullRequestCount: 3,
  notificationCount: 5,
} as unknown as WorkspaceOverview;

export const visualRepositoryGroups = [
  {
    repositoryGroupId: "core-runtime",
    repositories: [
      {
        repositoryId: "dexdex-main-server",
        repositoryUrl: "https://github.com/delinoio/oss",
        branchRef: "main",
      },
      {
        repositoryId: "dexdex-worker-server",
        repositoryUrl: "https://github.com/delinoio/oss",
        branchRef: "main",
      },
    ],
  },
  {
    repositoryGroupId: "desktop-app",
    repositories: [
      {
        repositoryId: "dexdex-desktop",
        repositoryUrl: "https://github.com/delinoio/oss",
        branchRef: "kdy1/dexdex-ui-parity",
      },
    ],
  },
] as unknown as RepositoryGroup[];

export const visualUnitTasks = [
  {
    unitTaskId: "visual-task-101",
    status: UnitTaskStatus.IN_PROGRESS,
    actionRequired: ActionType.PLAN_APPROVAL_REQUIRED,
  },
  {
    unitTaskId: "visual-task-102",
    status: UnitTaskStatus.ACTION_REQUIRED,
    actionRequired: ActionType.REVIEW_REQUESTED,
  },
  {
    unitTaskId: "visual-task-103",
    status: UnitTaskStatus.COMPLETED,
    actionRequired: ActionType.UNSPECIFIED,
  },
] as unknown as UnitTask[];

export const visualSubTasks = [
  {
    subTaskId: "visual-subtask-001",
    unitTaskId: "visual-task-101",
    type: SubTaskType.INITIAL_IMPLEMENTATION,
    status: SubTaskStatus.WAITING_FOR_PLAN_APPROVAL,
    completionReason: SubTaskCompletionReason.UNSPECIFIED,
    commitChain: [],
  },
  {
    subTaskId: "visual-subtask-002",
    unitTaskId: "visual-task-101",
    type: SubTaskType.PR_REVIEW_FIX,
    status: SubTaskStatus.IN_PROGRESS,
    completionReason: SubTaskCompletionReason.UNSPECIFIED,
    commitChain: [],
  },
  {
    subTaskId: "visual-subtask-003",
    unitTaskId: "visual-task-102",
    type: SubTaskType.REQUEST_CHANGES,
    status: SubTaskStatus.WAITING_FOR_USER_INPUT,
    completionReason: SubTaskCompletionReason.UNSPECIFIED,
    commitChain: [],
  },
] as unknown as SubTask[];

export const visualSessions = [
  {
    sessionId: "visual-session-001",
    status: AgentSessionStatus.WAITING_FOR_INPUT,
    cliType: AgentCliType.CODEX_CLI,
    lastOutputKind: SessionOutputKind.PLAN_UPDATE,
    updatedAt: timestamp("2026-03-08T08:15:00Z"),
  },
  {
    sessionId: "visual-session-002",
    status: AgentSessionStatus.RUNNING,
    cliType: AgentCliType.CLAUDE_CODE,
    lastOutputKind: SessionOutputKind.PROGRESS,
    updatedAt: timestamp("2026-03-08T08:18:00Z"),
  },
  {
    sessionId: "visual-session-003",
    status: AgentSessionStatus.COMPLETED,
    cliType: AgentCliType.OPENCODE,
    lastOutputKind: SessionOutputKind.TEXT,
    updatedAt: timestamp("2026-03-08T08:05:00Z"),
  },
] as unknown as SessionSummary[];

export const visualSessionOutputEvents = [
  {
    sessionId: "visual-session-001",
    kind: SessionOutputKind.TEXT,
    body: "I inspected the repository and identified the missing route guards.",
    source: {
      cliType: AgentCliType.CODEX_CLI,
      sourceEventType: SessionOutputSourceEventType.TEXT_DELTA,
      sourceSequence: 1n,
      rawEventType: "text.delta",
    },
    isTerminal: false,
  },
  {
    sessionId: "visual-session-001",
    kind: SessionOutputKind.PLAN_UPDATE,
    body: "Plan updated: add Projects/Worktrees/Local Environments routes first.",
    source: {
      cliType: AgentCliType.CODEX_CLI,
      sourceEventType: SessionOutputSourceEventType.TEXT_FINAL,
      sourceSequence: 2n,
      rawEventType: "plan.update",
    },
    isTerminal: false,
  },
  {
    sessionId: "visual-session-001",
    kind: SessionOutputKind.WARNING,
    body: "Waiting for plan approval before continuing implementation.",
    source: {
      cliType: AgentCliType.CODEX_CLI,
      sourceEventType: SessionOutputSourceEventType.RESULT,
      sourceSequence: 3n,
      rawEventType: "turn.waiting",
    },
    isTerminal: true,
  },
] as unknown as SessionOutputEvent[];

export const visualStreamEvents = [
  {
    sequence: 101n,
    workspaceId: "visual-workspace",
    eventType: StreamEventType.SUBTASK_UPDATED,
    occurredAt: timestamp("2026-03-08T08:12:00Z"),
    payload: {
      case: "subTask",
      value: visualSubTasks[1],
    },
  },
  {
    sequence: 102n,
    workspaceId: "visual-workspace",
    eventType: StreamEventType.SESSION_OUTPUT,
    occurredAt: timestamp("2026-03-08T08:13:00Z"),
    payload: {
      case: "sessionOutput",
      value: visualSessionOutputEvents[0],
    },
  },
  {
    sequence: 103n,
    workspaceId: "visual-workspace",
    eventType: StreamEventType.SESSION_STATE_CHANGED,
    occurredAt: timestamp("2026-03-08T08:14:00Z"),
    payload: {
      case: "sessionStateChanged",
      value: {
        sessionId: "visual-session-002",
        status: AgentSessionStatus.RUNNING,
      },
    },
  },
] as unknown as StreamWorkspaceEventsResponse[];

export const visualPullRequests = [
  {
    prTrackingId: "visual-pr-001",
    status: PrStatus.OPEN,
  },
  {
    prTrackingId: "visual-pr-002",
    status: PrStatus.CHANGES_REQUESTED,
  },
  {
    prTrackingId: "visual-pr-003",
    status: PrStatus.APPROVED,
  },
] as unknown as PullRequestRecord[];

export const visualReviewAssistItems = [
  {
    reviewAssistId: "assist-001",
    body: "Split top-level shell and keep route guards consistent across pages.",
  },
  {
    reviewAssistId: "assist-002",
    body: "Prefer enum-backed page identifiers over string literals.",
  },
] as unknown as ReviewAssistItem[];

export const visualReviewComments = [
  {
    reviewCommentId: "comment-001",
    body: "Please align the local environments page with the route contract.",
  },
  {
    reviewCommentId: "comment-002",
    body: "Inline styles should be migrated to token-based CSS classes.",
  },
] as unknown as ReviewComment[];
