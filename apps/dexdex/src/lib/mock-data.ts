/**
 * Mock data for DexDex desktop app development.
 */

import {
  UnitTaskStatus,
  SubTaskType,
  SubTaskStatus,
  SubTaskCompletionReason,
  AgentSessionStatus,
  SessionOutputKind,
  NotificationType,
  SessionForkStatus,
  AgentCliType,
} from "./status";

export interface UnitTask {
  unitTaskId: string;
  title: string;
  description: string;
  status: UnitTaskStatus;
  repositoryUrl: string;
  branchRef: string;
  createdAt: string;
  updatedAt: string;
  subTasks: SubTask[];
}

export interface SubTask {
  subTaskId: string;
  unitTaskId: string;
  type: SubTaskType;
  status: SubTaskStatus;
  completionReason: SubTaskCompletionReason;
  sessionId: string;
  createdAt: string;
  updatedAt: string;
  planSummary?: string;
}

export interface SessionOutputEvent {
  sessionId: string;
  kind: SessionOutputKind;
  body: string;
  timestamp: string;
}

export interface Notification {
  notificationId: string;
  type: NotificationType;
  title: string;
  body: string;
  taskId?: string;
  read: boolean;
  createdAt: string;
}

export interface SessionSummary {
  sessionId: string;
  parentSessionId: string;
  rootSessionId: string;
  forkStatus: SessionForkStatus;
  forkedFromSequence: number;
  agentSessionStatus: AgentSessionStatus;
  createdAt: string;
}

export interface AgentCapability {
  agentCliType: AgentCliType;
  supportsFork: boolean;
  displayName: string;
}

export interface ReviewComment {
  reviewCommentId: string;
  body: string;
  filePath: string;
  side: string;
  lineNumber: number;
  status: string;
  prTrackingId: string;
  createdAt: string;
  updatedAt: string;
}

export const MOCK_TASKS: UnitTask[] = [
  {
    unitTaskId: "task-001",
    title: "Add user authentication flow",
    description: "Implement OAuth2 login with Google and GitHub providers, including token refresh and session management.",
    status: UnitTaskStatus.IN_PROGRESS,
    repositoryUrl: "https://github.com/acme/webapp",
    branchRef: "feat/auth-flow",
    createdAt: "2026-03-14T08:00:00Z",
    updatedAt: "2026-03-14T10:30:00Z",
    subTasks: [
      {
        subTaskId: "sub-001-1",
        unitTaskId: "task-001",
        type: SubTaskType.INITIAL_IMPLEMENTATION,
        status: SubTaskStatus.COMPLETED,
        completionReason: SubTaskCompletionReason.SUCCEEDED,
        sessionId: "sess-001-1",
        createdAt: "2026-03-14T08:00:00Z",
        updatedAt: "2026-03-14T09:15:00Z",
        planSummary: "Implement OAuth2 providers, token storage, and session middleware.",
      },
      {
        subTaskId: "sub-001-2",
        unitTaskId: "task-001",
        type: SubTaskType.PR_CREATE,
        status: SubTaskStatus.IN_PROGRESS,
        completionReason: SubTaskCompletionReason.UNSPECIFIED,
        sessionId: "sess-001-2",
        createdAt: "2026-03-14T09:20:00Z",
        updatedAt: "2026-03-14T10:30:00Z",
      },
    ],
  },
  {
    unitTaskId: "task-002",
    title: "Fix database migration rollback",
    description: "The migration 20260301_add_profiles fails on rollback due to a missing DOWN statement.",
    status: UnitTaskStatus.ACTION_REQUIRED,
    repositoryUrl: "https://github.com/acme/webapp",
    branchRef: "fix/migration-rollback",
    createdAt: "2026-03-13T14:00:00Z",
    updatedAt: "2026-03-14T07:00:00Z",
    subTasks: [
      {
        subTaskId: "sub-002-1",
        unitTaskId: "task-002",
        type: SubTaskType.INITIAL_IMPLEMENTATION,
        status: SubTaskStatus.WAITING_FOR_PLAN_APPROVAL,
        completionReason: SubTaskCompletionReason.UNSPECIFIED,
        sessionId: "sess-002-1",
        createdAt: "2026-03-13T14:00:00Z",
        updatedAt: "2026-03-14T07:00:00Z",
        planSummary: "Add DOWN migration for 20260301_add_profiles and verify rollback cycle.",
      },
    ],
  },
  {
    unitTaskId: "task-003",
    title: "Refactor API response serialization",
    description: "Move from manual JSON marshaling to typed response builders with consistent error envelope.",
    status: UnitTaskStatus.COMPLETED,
    repositoryUrl: "https://github.com/acme/api-server",
    branchRef: "refactor/response-builders",
    createdAt: "2026-03-12T10:00:00Z",
    updatedAt: "2026-03-13T16:00:00Z",
    subTasks: [
      {
        subTaskId: "sub-003-1",
        unitTaskId: "task-003",
        type: SubTaskType.INITIAL_IMPLEMENTATION,
        status: SubTaskStatus.COMPLETED,
        completionReason: SubTaskCompletionReason.SUCCEEDED,
        sessionId: "sess-003-1",
        createdAt: "2026-03-12T10:00:00Z",
        updatedAt: "2026-03-12T14:00:00Z",
      },
      {
        subTaskId: "sub-003-2",
        unitTaskId: "task-003",
        type: SubTaskType.PR_CREATE,
        status: SubTaskStatus.COMPLETED,
        completionReason: SubTaskCompletionReason.SUCCEEDED,
        sessionId: "sess-003-2",
        createdAt: "2026-03-12T14:05:00Z",
        updatedAt: "2026-03-13T16:00:00Z",
      },
    ],
  },
  {
    unitTaskId: "task-004",
    title: "Add rate limiting middleware",
    description: "Implement token-bucket rate limiting for public API endpoints.",
    status: UnitTaskStatus.QUEUED,
    repositoryUrl: "https://github.com/acme/api-server",
    branchRef: "feat/rate-limiting",
    createdAt: "2026-03-14T11:00:00Z",
    updatedAt: "2026-03-14T11:00:00Z",
    subTasks: [],
  },
  {
    unitTaskId: "task-005",
    title: "Update CI pipeline for monorepo",
    description: "Configure path-based change detection and parallel job execution.",
    status: UnitTaskStatus.FAILED,
    repositoryUrl: "https://github.com/acme/infra",
    branchRef: "chore/ci-monorepo",
    createdAt: "2026-03-13T09:00:00Z",
    updatedAt: "2026-03-14T06:00:00Z",
    subTasks: [
      {
        subTaskId: "sub-005-1",
        unitTaskId: "task-005",
        type: SubTaskType.INITIAL_IMPLEMENTATION,
        status: SubTaskStatus.FAILED,
        completionReason: SubTaskCompletionReason.FAILED,
        sessionId: "sess-005-1",
        createdAt: "2026-03-13T09:00:00Z",
        updatedAt: "2026-03-14T06:00:00Z",
      },
    ],
  },
];

export const MOCK_SESSION_OUTPUT: SessionOutputEvent[] = [
  { sessionId: "sess-001-2", kind: SessionOutputKind.TEXT, body: "Starting PR creation for feat/auth-flow...", timestamp: "2026-03-14T09:20:00Z" },
  { sessionId: "sess-001-2", kind: SessionOutputKind.TOOL_CALL, body: "git.createBranch({ name: 'feat/auth-flow', base: 'main' })", timestamp: "2026-03-14T09:20:05Z" },
  { sessionId: "sess-001-2", kind: SessionOutputKind.TOOL_RESULT, body: "Branch created successfully", timestamp: "2026-03-14T09:20:08Z" },
  { sessionId: "sess-001-2", kind: SessionOutputKind.PROGRESS, body: "Pushing commits to remote...", timestamp: "2026-03-14T09:20:15Z" },
  { sessionId: "sess-001-2", kind: SessionOutputKind.TEXT, body: "Creating pull request with title: 'feat: add user authentication flow'", timestamp: "2026-03-14T09:20:30Z" },
  { sessionId: "sess-001-2", kind: SessionOutputKind.WARNING, body: "PR template not found, using default description", timestamp: "2026-03-14T09:20:32Z" },
  { sessionId: "sess-001-2", kind: SessionOutputKind.TOOL_CALL, body: "github.createPullRequest({ title: 'feat: add user authentication flow', base: 'main' })", timestamp: "2026-03-14T09:20:35Z" },
  { sessionId: "sess-001-2", kind: SessionOutputKind.TOOL_RESULT, body: "Pull request #42 created successfully\nhttps://github.com/acme/webapp/pull/42", timestamp: "2026-03-14T09:20:40Z" },
  { sessionId: "sess-001-2", kind: SessionOutputKind.ERROR, body: "CI check 'lint' failed with exit code 1", timestamp: "2026-03-14T10:00:00Z" },
];

export const MOCK_NOTIFICATIONS: Notification[] = [
  {
    notificationId: "notif-001",
    type: NotificationType.PLAN_ACTION_REQUIRED,
    title: "Plan approval needed",
    body: "Task 'Fix database migration rollback' is waiting for your plan approval.",
    taskId: "task-002",
    read: false,
    createdAt: "2026-03-14T07:00:00Z",
  },
  {
    notificationId: "notif-002",
    type: NotificationType.PR_CI_FAILURE,
    title: "CI failure on PR #42",
    body: "Lint check failed for 'Add user authentication flow'.",
    taskId: "task-001",
    read: false,
    createdAt: "2026-03-14T10:00:00Z",
  },
  {
    notificationId: "notif-003",
    type: NotificationType.AGENT_SESSION_FAILED,
    title: "Agent session failed",
    body: "Session for 'Update CI pipeline for monorepo' encountered an unrecoverable error.",
    taskId: "task-005",
    read: true,
    createdAt: "2026-03-14T06:00:00Z",
  },
];
