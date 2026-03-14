import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, beforeEach } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { TransportProvider } from "@connectrpc/connect-query";
import { createRouterTransport } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import App from "./App";
import {
  TaskService,
  NotificationService,
  SessionService,
  EventStreamService,
  WorkspaceService,
  UnitTaskSchema,
  SubTaskSchema,
  SessionOutputEventSchema,
  NotificationRecordSchema,
  UnitTaskStatus,
  SubTaskType,
  SubTaskStatus,
  SubTaskCompletionReason,
  SessionOutputKind,
  NotificationType,
} from "./gen/v1/dexdex_pb";

// Mock localStorage
const localStorageMock = (() => {
  let store: Record<string, string> = {};
  return {
    getItem: (key: string) => store[key] ?? null,
    setItem: (key: string, value: string) => { store[key] = value; },
    removeItem: (key: string) => { delete store[key]; },
    clear: () => { store = {}; },
  };
})();

Object.defineProperty(window, "localStorage", { value: localStorageMock });

// Mock proto data matching the old MOCK_TASKS shape
const mockUnitTasks = [
  create(UnitTaskSchema, {
    unitTaskId: "task-001",
    title: "Add user authentication flow",
    description: "Implement OAuth2 login with Google and GitHub providers, including token refresh and session management.",
    status: UnitTaskStatus.IN_PROGRESS,
    createdAt: timestampFromDate(new Date("2026-03-14T08:00:00Z")),
    updatedAt: timestampFromDate(new Date("2026-03-14T10:30:00Z")),
    subTaskCount: 2,
  }),
  create(UnitTaskSchema, {
    unitTaskId: "task-002",
    title: "Fix database migration rollback",
    description: "The migration 20260301_add_profiles fails on rollback due to a missing DOWN statement.",
    status: UnitTaskStatus.ACTION_REQUIRED,
    createdAt: timestampFromDate(new Date("2026-03-13T14:00:00Z")),
    updatedAt: timestampFromDate(new Date("2026-03-14T07:00:00Z")),
    subTaskCount: 1,
  }),
  create(UnitTaskSchema, {
    unitTaskId: "task-003",
    title: "Refactor API response serialization",
    description: "Move from manual JSON marshaling to typed response builders with consistent error envelope.",
    status: UnitTaskStatus.COMPLETED,
    createdAt: timestampFromDate(new Date("2026-03-12T10:00:00Z")),
    updatedAt: timestampFromDate(new Date("2026-03-13T16:00:00Z")),
    subTaskCount: 2,
  }),
  create(UnitTaskSchema, {
    unitTaskId: "task-004",
    title: "Add rate limiting middleware",
    description: "Implement token-bucket rate limiting for public API endpoints.",
    status: UnitTaskStatus.QUEUED,
    createdAt: timestampFromDate(new Date("2026-03-14T11:00:00Z")),
    updatedAt: timestampFromDate(new Date("2026-03-14T11:00:00Z")),
    subTaskCount: 0,
  }),
  create(UnitTaskSchema, {
    unitTaskId: "task-005",
    title: "Update CI pipeline for monorepo",
    description: "Configure path-based change detection and parallel job execution.",
    status: UnitTaskStatus.FAILED,
    createdAt: timestampFromDate(new Date("2026-03-13T09:00:00Z")),
    updatedAt: timestampFromDate(new Date("2026-03-14T06:00:00Z")),
    subTaskCount: 1,
  }),
];

const mockSubTasksFor001 = [
  create(SubTaskSchema, {
    subTaskId: "sub-001-1",
    unitTaskId: "task-001",
    type: SubTaskType.INITIAL_IMPLEMENTATION,
    status: SubTaskStatus.COMPLETED,
    completionReason: SubTaskCompletionReason.SUCCEEDED,
    sessionId: "sess-001-1",
    createdAt: timestampFromDate(new Date("2026-03-14T08:00:00Z")),
    updatedAt: timestampFromDate(new Date("2026-03-14T09:15:00Z")),
    title: "Implement OAuth2 providers, token storage, and session middleware.",
  }),
  create(SubTaskSchema, {
    subTaskId: "sub-001-2",
    unitTaskId: "task-001",
    type: SubTaskType.PR_CREATE,
    status: SubTaskStatus.IN_PROGRESS,
    completionReason: SubTaskCompletionReason.UNSPECIFIED,
    sessionId: "sess-001-2",
    createdAt: timestampFromDate(new Date("2026-03-14T09:20:00Z")),
    updatedAt: timestampFromDate(new Date("2026-03-14T10:30:00Z")),
  }),
];

const mockSubTasksFor002 = [
  create(SubTaskSchema, {
    subTaskId: "sub-002-1",
    unitTaskId: "task-002",
    type: SubTaskType.INITIAL_IMPLEMENTATION,
    status: SubTaskStatus.WAITING_FOR_PLAN_APPROVAL,
    completionReason: SubTaskCompletionReason.UNSPECIFIED,
    sessionId: "sess-002-1",
    createdAt: timestampFromDate(new Date("2026-03-13T14:00:00Z")),
    updatedAt: timestampFromDate(new Date("2026-03-14T07:00:00Z")),
    title: "Add DOWN migration for 20260301_add_profiles and verify rollback cycle.",
  }),
];

const mockSessionOutput = [
  create(SessionOutputEventSchema, { sessionId: "sess-001-2", kind: SessionOutputKind.TEXT, body: "Starting PR creation for feat/auth-flow..." }),
  create(SessionOutputEventSchema, { sessionId: "sess-001-2", kind: SessionOutputKind.TOOL_CALL, body: "git.createBranch({ name: 'feat/auth-flow', base: 'main' })" }),
  create(SessionOutputEventSchema, { sessionId: "sess-001-2", kind: SessionOutputKind.TOOL_RESULT, body: "Branch created successfully" }),
];

const mockNotifications = [
  create(NotificationRecordSchema, {
    notificationId: "notif-001",
    type: NotificationType.PLAN_ACTION_REQUIRED,
    title: "Plan approval needed",
    body: "Task 'Fix database migration rollback' is waiting for your plan approval.",
    referenceId: "task-002",
    read: false,
    createdAt: timestampFromDate(new Date("2026-03-14T07:00:00Z")),
  }),
  create(NotificationRecordSchema, {
    notificationId: "notif-002",
    type: NotificationType.PR_CI_FAILURE,
    title: "CI failure on PR #42",
    body: "Lint check failed for 'Add user authentication flow'.",
    referenceId: "task-001",
    read: false,
    createdAt: timestampFromDate(new Date("2026-03-14T10:00:00Z")),
  }),
  create(NotificationRecordSchema, {
    notificationId: "notif-003",
    type: NotificationType.AGENT_SESSION_FAILED,
    title: "Agent session failed",
    body: "Session for 'Update CI pipeline for monorepo' encountered an unrecoverable error.",
    referenceId: "task-005",
    read: true,
    createdAt: timestampFromDate(new Date("2026-03-14T06:00:00Z")),
  }),
];

function createTestTransport() {
  return createRouterTransport((router) => {
    router.service(TaskService, {
      listUnitTasks: () => ({ unitTasks: mockUnitTasks }),
      listSubTasks: (req) => {
        if (req.unitTaskId === "task-001") return { subTasks: mockSubTasksFor001 };
        if (req.unitTaskId === "task-002") return { subTasks: mockSubTasksFor002 };
        return { subTasks: [] };
      },
      createUnitTask: (req) => ({
        unitTask: create(UnitTaskSchema, {
          unitTaskId: `task-${Date.now()}`,
          title: req.title,
          description: req.description,
          status: UnitTaskStatus.QUEUED,
          createdAt: timestampFromDate(new Date()),
          updatedAt: timestampFromDate(new Date()),
        }),
      }),
      submitPlanDecision: () => ({
        updatedSubTask: undefined,
        createdSubTask: undefined,
      }),
    });
    router.service(NotificationService, {
      listNotifications: () => ({ notifications: mockNotifications }),
      markNotificationRead: () => ({ notification: undefined }),
    });
    router.service(SessionService, {
      getSessionOutput: () => ({ events: mockSessionOutput }),
      listSessionCapabilities: () => ({ capabilities: [] }),
      forkSession: () => ({ forkedSession: undefined }),
      listForkedSessions: () => ({ sessions: [] }),
      archiveForkedSession: () => ({}),
      getLatestWaitingSession: () => ({ session: undefined }),
      submitSessionInput: () => ({}),
    });
    router.service(WorkspaceService, {
      getWorkspace: () => ({ workspace: undefined }),
      listWorkspaces: () => ({ workspaces: [] }),
      getWorkspaceWorkStatus: () => ({ status: 0 }),
    });
    // EventStreamService is server-streaming; provide a no-op stub
    router.service(EventStreamService, {
      streamWorkspaceEvents: async function* () {
        // No events in tests
      },
    });
  });
}

function renderWithProviders(ui: React.ReactElement) {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
        staleTime: Infinity,
      },
    },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <TransportProvider transport={createTestTransport()}>
        {ui}
      </TransportProvider>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  localStorageMock.clear();
  document.documentElement.classList.remove("dark");
});

describe("App", () => {
  it("renders the main layout with sidebar and task list", async () => {
    renderWithProviders(<App />);

    expect(screen.getByTestId("app-layout")).toBeTruthy();
    expect(screen.getByTestId("sidebar")).toBeTruthy();
    expect(screen.getByTestId("tab-bar")).toBeTruthy();
    // Task list should appear after data loads
    expect(await screen.findByTestId("task-list")).toBeTruthy();
  });

  it("shows task list heading", async () => {
    renderWithProviders(<App />);

    expect(await screen.findByRole("heading", { name: "Tasks" })).toBeTruthy();
  });

  it("displays tasks from server in the task list", async () => {
    renderWithProviders(<App />);

    expect(await screen.findByText("Add user authentication flow")).toBeTruthy();
    expect(screen.getByText("Fix database migration rollback")).toBeTruthy();
    expect(screen.getByText("Refactor API response serialization")).toBeTruthy();
  });

  it("navigates to inbox via sidebar", async () => {
    const user = userEvent.setup();
    renderWithProviders(<App />);

    // Wait for initial render
    await screen.findByTestId("task-list");

    await user.click(screen.getByTestId("nav-inbox"));
    expect(await screen.findByTestId("inbox-page")).toBeTruthy();
    expect(screen.getByRole("heading", { name: "Inbox" })).toBeTruthy();
  });

  it("navigates to settings via sidebar", async () => {
    const user = userEvent.setup();
    renderWithProviders(<App />);

    await screen.findByTestId("task-list");

    await user.click(screen.getByTestId("nav-settings"));
    expect(await screen.findByTestId("settings-page")).toBeTruthy();
    expect(screen.getByRole("heading", { name: "Settings" })).toBeTruthy();
  });

  it("navigates to task detail when clicking a task row", async () => {
    const user = userEvent.setup();
    renderWithProviders(<App />);

    await screen.findByTestId("task-row-task-001");
    await user.click(screen.getByTestId("task-row-task-001"));
    expect(await screen.findByTestId("task-detail")).toBeTruthy();
    expect(screen.getAllByText("Add user authentication flow").length).toBeGreaterThanOrEqual(1);
  });

  it("shows back button in task detail and returns to list", async () => {
    const user = userEvent.setup();
    renderWithProviders(<App />);

    await screen.findByTestId("task-row-task-001");
    await user.click(screen.getByTestId("task-row-task-001"));
    expect(await screen.findByTestId("task-detail")).toBeTruthy();

    await user.click(screen.getByTestId("back-button"));
    expect(await screen.findByTestId("task-list")).toBeTruthy();
  });

  it("shows connection status dot in sidebar", async () => {
    renderWithProviders(<App />);

    expect(await screen.findByTestId("connection-dot")).toBeTruthy();
  });

  it("shows create task button and opens dialog", async () => {
    const user = userEvent.setup();
    renderWithProviders(<App />);

    await screen.findByTestId("task-list");
    await user.click(screen.getByTestId("create-task-button"));
    expect(screen.getByTestId("create-dialog")).toBeTruthy();
  });

  it("creates a new task via dialog", async () => {
    const user = userEvent.setup();
    renderWithProviders(<App />);

    await screen.findByTestId("task-list");
    await user.click(screen.getByTestId("create-task-button"));

    await user.type(screen.getByTestId("task-title-input"), "My new task");
    await user.type(screen.getByTestId("task-description-input"), "Some description");
    await user.click(screen.getByTestId("submit-create-task"));

    // Dialog should close
    expect(screen.queryByTestId("create-dialog")).toBeNull();
  });

  it("opens command palette with keyboard shortcut", async () => {
    const user = userEvent.setup();
    renderWithProviders(<App />);

    await screen.findByTestId("task-list");
    await user.keyboard("{Meta>}k{/Meta}");
    expect(screen.getByTestId("command-palette")).toBeTruthy();
  });

  it("closes command palette with Escape", async () => {
    const user = userEvent.setup();
    renderWithProviders(<App />);

    await screen.findByTestId("task-list");
    await user.keyboard("{Meta>}k{/Meta}");
    expect(screen.getByTestId("command-palette")).toBeTruthy();

    await user.keyboard("{Escape}");
    expect(screen.queryByTestId("command-palette")).toBeNull();
  });

  it("searches in command palette", async () => {
    const user = userEvent.setup();
    renderWithProviders(<App />);

    await screen.findByTestId("task-list");
    await user.keyboard("{Meta>}k{/Meta}");
    const input = screen.getByTestId("command-palette-input");
    await user.type(input, "auth");

    const palette = screen.getByTestId("command-palette");
    expect(palette.textContent).toContain("Add user authentication flow");
  });

  it("toggles dark mode in settings", async () => {
    const user = userEvent.setup();
    renderWithProviders(<App />);

    await screen.findByTestId("task-list");
    await user.click(screen.getByTestId("nav-settings"));
    await screen.findByTestId("settings-page");
    await user.click(screen.getByTestId("theme-dark"));

    expect(document.documentElement.classList.contains("dark")).toBe(true);
    expect(localStorageMock.getItem("dexdex-theme")).toBe("dark");
  });

  it("toggles back to light mode", async () => {
    const user = userEvent.setup();
    renderWithProviders(<App />);

    await screen.findByTestId("task-list");
    await user.click(screen.getByTestId("nav-settings"));
    await screen.findByTestId("settings-page");
    await user.click(screen.getByTestId("theme-dark"));
    expect(document.documentElement.classList.contains("dark")).toBe(true);

    await user.click(screen.getByTestId("theme-light"));
    expect(document.documentElement.classList.contains("dark")).toBe(false);
    expect(localStorageMock.getItem("dexdex-theme")).toBe("light");
  });

  it("persists theme preference in localStorage", async () => {
    const user = userEvent.setup();
    renderWithProviders(<App />);

    await screen.findByTestId("task-list");
    await user.click(screen.getByTestId("nav-settings"));
    await screen.findByTestId("settings-page");
    await user.click(screen.getByTestId("theme-dark"));

    expect(localStorageMock.getItem("dexdex-theme")).toBe("dark");
  });

  it("shows session output panel in task detail", async () => {
    const user = userEvent.setup();
    renderWithProviders(<App />);

    await screen.findByTestId("task-row-task-001");
    await user.click(screen.getByTestId("task-row-task-001"));
    expect(await screen.findByTestId("session-output-panel")).toBeTruthy();
  });

  it("shows plan decision controls for waiting tasks", async () => {
    const user = userEvent.setup();
    renderWithProviders(<App />);

    await screen.findByTestId("task-row-task-002");
    await user.click(screen.getByTestId("task-row-task-002"));
    expect(await screen.findByTestId("plan-decisions")).toBeTruthy();
    expect(screen.getByTestId("approve-button")).toBeTruthy();
    expect(screen.getByTestId("reject-button")).toBeTruthy();
  });

  it("shows subtask timeline in task detail", async () => {
    const user = userEvent.setup();
    renderWithProviders(<App />);

    await screen.findByTestId("task-row-task-001");
    await user.click(screen.getByTestId("task-row-task-001"));
    expect(await screen.findByTestId("subtask-timeline")).toBeTruthy();
  });

  it("filters tasks by status", async () => {
    const user = userEvent.setup();
    renderWithProviders(<App />);

    await screen.findByTestId("task-row-task-003");
    await user.click(screen.getByTestId("filter-COMPLETED"));

    // Only the completed task should be visible in the list
    expect(screen.getByTestId("task-row-task-003")).toBeTruthy();
    expect(screen.queryByTestId("task-row-task-001")).toBeNull();
    expect(screen.queryByTestId("task-row-task-002")).toBeNull();
  });

  it("shows notifications in inbox with unread indicator", async () => {
    const user = userEvent.setup();
    renderWithProviders(<App />);

    await screen.findByTestId("task-list");
    await user.click(screen.getByTestId("nav-inbox"));

    expect(await screen.findByText("Plan approval needed")).toBeTruthy();
    expect(screen.getByText("CI failure on PR #42")).toBeTruthy();
  });
});
