import {
  UnitTaskStatus,
  SubTaskStatus,
  SubTaskType,
  ActionType,
  NotificationType,
} from "./status";

export interface MockSubTask {
  subTaskId: string;
  unitTaskId: string;
  type: SubTaskType;
  status: SubTaskStatus;
  createdAt: string;
}

export interface MockUnitTask {
  unitTaskId: string;
  title: string;
  description: string;
  status: UnitTaskStatus;
  actionRequired: ActionType;
  subTasks: MockSubTask[];
  createdAt: string;
  updatedAt: string;
}

export interface MockNotification {
  notificationId: string;
  type: NotificationType;
  title: string;
  createdAt: string;
  read: boolean;
}

export const mockWorkspace = {
  workspaceId: "ws-001",
  name: "DexDex Development",
  endpoint: "http://localhost:5990",
};

export const mockTasks: MockUnitTask[] = [
  {
    unitTaskId: "task-001",
    title: "Implement user authentication flow",
    description:
      "Add OAuth2 authentication with GitHub provider. Include login, logout, and session management.",
    status: UnitTaskStatus.IN_PROGRESS,
    actionRequired: ActionType.UNSPECIFIED,
    subTasks: [
      {
        subTaskId: "sub-001-1",
        unitTaskId: "task-001",
        type: SubTaskType.INITIAL_IMPLEMENTATION,
        status: SubTaskStatus.COMPLETED,
        createdAt: "2026-03-14T08:00:00Z",
      },
      {
        subTaskId: "sub-001-2",
        unitTaskId: "task-001",
        type: SubTaskType.PR_CREATE,
        status: SubTaskStatus.IN_PROGRESS,
        createdAt: "2026-03-14T09:30:00Z",
      },
    ],
    createdAt: "2026-03-14T08:00:00Z",
    updatedAt: "2026-03-14T09:30:00Z",
  },
  {
    unitTaskId: "task-002",
    title: "Refactor database connection pooling",
    description:
      "Replace manual connection management with pgx pool. Ensure graceful shutdown and health checks.",
    status: UnitTaskStatus.ACTION_REQUIRED,
    actionRequired: ActionType.PLAN_APPROVAL_REQUIRED,
    subTasks: [
      {
        subTaskId: "sub-002-1",
        unitTaskId: "task-002",
        type: SubTaskType.INITIAL_IMPLEMENTATION,
        status: SubTaskStatus.WAITING_FOR_PLAN_APPROVAL,
        createdAt: "2026-03-14T07:00:00Z",
      },
    ],
    createdAt: "2026-03-14T07:00:00Z",
    updatedAt: "2026-03-14T07:45:00Z",
  },
  {
    unitTaskId: "task-003",
    title: "Add integration tests for task API",
    description:
      "Write comprehensive integration tests covering CRUD operations, filtering, and pagination for the task management API.",
    status: UnitTaskStatus.COMPLETED,
    actionRequired: ActionType.UNSPECIFIED,
    subTasks: [
      {
        subTaskId: "sub-003-1",
        unitTaskId: "task-003",
        type: SubTaskType.INITIAL_IMPLEMENTATION,
        status: SubTaskStatus.COMPLETED,
        createdAt: "2026-03-13T14:00:00Z",
      },
      {
        subTaskId: "sub-003-2",
        unitTaskId: "task-003",
        type: SubTaskType.PR_CREATE,
        status: SubTaskStatus.COMPLETED,
        createdAt: "2026-03-13T15:00:00Z",
      },
      {
        subTaskId: "sub-003-3",
        unitTaskId: "task-003",
        type: SubTaskType.PR_REVIEW_FIX,
        status: SubTaskStatus.COMPLETED,
        createdAt: "2026-03-13T16:00:00Z",
      },
    ],
    createdAt: "2026-03-13T14:00:00Z",
    updatedAt: "2026-03-13T17:00:00Z",
  },
  {
    unitTaskId: "task-004",
    title: "Fix CI pipeline timeout on Windows",
    description:
      "Windows CI jobs are timing out during the build step. Investigate and fix the root cause.",
    status: UnitTaskStatus.FAILED,
    actionRequired: ActionType.CI_FAILED,
    subTasks: [
      {
        subTaskId: "sub-004-1",
        unitTaskId: "task-004",
        type: SubTaskType.INITIAL_IMPLEMENTATION,
        status: SubTaskStatus.FAILED,
        createdAt: "2026-03-14T06:00:00Z",
      },
    ],
    createdAt: "2026-03-14T06:00:00Z",
    updatedAt: "2026-03-14T06:30:00Z",
  },
  {
    unitTaskId: "task-005",
    title: "Migrate config schema to typed constants",
    description:
      "Replace string-based config keys with typed enum constants across the server codebase.",
    status: UnitTaskStatus.QUEUED,
    actionRequired: ActionType.UNSPECIFIED,
    subTasks: [],
    createdAt: "2026-03-14T10:00:00Z",
    updatedAt: "2026-03-14T10:00:00Z",
  },
  {
    unitTaskId: "task-006",
    title: "Add structured logging to worker server",
    description:
      "Integrate slog-based structured logging throughout the worker server for better observability.",
    status: UnitTaskStatus.QUEUED,
    actionRequired: ActionType.UNSPECIFIED,
    subTasks: [],
    createdAt: "2026-03-14T10:15:00Z",
    updatedAt: "2026-03-14T10:15:00Z",
  },
  {
    unitTaskId: "task-007",
    title: "Implement PR review assist suggestions",
    description:
      "Generate AI-powered review suggestions for open pull requests and display them in the review assist panel.",
    status: UnitTaskStatus.IN_PROGRESS,
    actionRequired: ActionType.UNSPECIFIED,
    subTasks: [
      {
        subTaskId: "sub-007-1",
        unitTaskId: "task-007",
        type: SubTaskType.INITIAL_IMPLEMENTATION,
        status: SubTaskStatus.IN_PROGRESS,
        createdAt: "2026-03-14T09:00:00Z",
      },
    ],
    createdAt: "2026-03-14T09:00:00Z",
    updatedAt: "2026-03-14T09:00:00Z",
  },
  {
    unitTaskId: "task-008",
    title: "Set up Tauri auto-update mechanism",
    description:
      "Configure Tauri updater plugin with signed releases and delta updates for the desktop app.",
    status: UnitTaskStatus.CANCELLED,
    actionRequired: ActionType.UNSPECIFIED,
    subTasks: [
      {
        subTaskId: "sub-008-1",
        unitTaskId: "task-008",
        type: SubTaskType.INITIAL_IMPLEMENTATION,
        status: SubTaskStatus.CANCELLED,
        createdAt: "2026-03-12T11:00:00Z",
      },
    ],
    createdAt: "2026-03-12T11:00:00Z",
    updatedAt: "2026-03-12T12:00:00Z",
  },
];

export const mockNotifications: MockNotification[] = [
  {
    notificationId: "notif-001",
    type: NotificationType.PLAN_ACTION_REQUIRED,
    title: "Plan approval required for task-002: Refactor database connection pooling",
    createdAt: "2026-03-14T07:45:00Z",
    read: false,
  },
  {
    notificationId: "notif-002",
    type: NotificationType.PR_CI_FAILURE,
    title: "CI failed for task-004: Fix CI pipeline timeout on Windows",
    createdAt: "2026-03-14T06:30:00Z",
    read: false,
  },
  {
    notificationId: "notif-003",
    type: NotificationType.PR_REVIEW_ACTIVITY,
    title: "New review comment on task-003: Add integration tests for task API",
    createdAt: "2026-03-13T16:30:00Z",
    read: true,
  },
];
