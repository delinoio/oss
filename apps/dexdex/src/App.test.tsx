import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, beforeEach } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { TransportProvider } from "@connectrpc/connect-query";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { MemoryRouter } from "react-router";
import { create } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import App from "./App";
import {
  TaskService,
  NotificationService,
  SessionService,
  EventStreamService,
  WorkspaceService,
  PrManagementService,
  ReviewAssistService,
  ReviewCommentService,
  RepositoryService,
  WorkspaceSchema,
  UnitTaskSchema,
  SubTaskSchema,
  RepositorySchema,
  RepositoryGroupMemberSchema,
  RepositoryGroupSchema,
  WorkspaceSettingsSchema,
  SessionOutputEventSchema,
  NotificationRecordSchema,
  UnitTaskStatus,
  SubTaskType,
  SubTaskStatus,
  SubTaskCompletionReason,
  SessionOutputKind,
  NotificationType,
  PrStatus,
  AgentCliType,
  WorkspaceType,
  PullRequestRecordSchema,
} from "./gen/v1/dexdex_pb";
import { AUTO_REPOSITORY_GROUP_PREFIX } from "./lib/repository-target";

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

const DEFAULT_WORKSPACE_ID = "ws-default";
const LEGACY_DEFAULT_WORKSPACE_ID = "workspace-default";

// Mock proto data matching the old MOCK_TASKS shape
const mockUnitTasks = [
  create(UnitTaskSchema, {
    unitTaskId: "task-001",
    prompt: "Add user authentication flow",
    status: UnitTaskStatus.IN_PROGRESS,
    repositoryGroupId: "repo-group-main",
    agentCliType: AgentCliType.CLAUDE_CODE,
    usePlanMode: false,
    createdAt: timestampFromDate(new Date("2026-03-14T08:00:00Z")),
    updatedAt: timestampFromDate(new Date("2026-03-14T10:30:00Z")),
    subTaskCount: 2,
  }),
  create(UnitTaskSchema, {
    unitTaskId: "task-002",
    prompt: "Fix database migration rollback",
    status: UnitTaskStatus.ACTION_REQUIRED,
    repositoryGroupId: "repo-group-main",
    agentCliType: AgentCliType.CLAUDE_CODE,
    usePlanMode: true,
    createdAt: timestampFromDate(new Date("2026-03-13T14:00:00Z")),
    updatedAt: timestampFromDate(new Date("2026-03-14T07:00:00Z")),
    subTaskCount: 1,
  }),
  create(UnitTaskSchema, {
    unitTaskId: "task-003",
    prompt: "Refactor API response serialization",
    status: UnitTaskStatus.COMPLETED,
    repositoryGroupId: "repo-group-main",
    agentCliType: AgentCliType.CLAUDE_CODE,
    usePlanMode: false,
    createdAt: timestampFromDate(new Date("2026-03-12T10:00:00Z")),
    updatedAt: timestampFromDate(new Date("2026-03-13T16:00:00Z")),
    subTaskCount: 2,
  }),
  create(UnitTaskSchema, {
    unitTaskId: "task-004",
    prompt: "Add rate limiting middleware",
    status: UnitTaskStatus.ACTION_REQUIRED,
    repositoryGroupId: "repo-group-main",
    agentCliType: AgentCliType.CODEX_CLI,
    usePlanMode: false,
    createdAt: timestampFromDate(new Date("2026-03-14T11:00:00Z")),
    updatedAt: timestampFromDate(new Date("2026-03-14T11:00:00Z")),
    subTaskCount: 1,
  }),
  create(UnitTaskSchema, {
    unitTaskId: "task-005",
    prompt: "Update CI pipeline for monorepo",
    status: UnitTaskStatus.FAILED,
    repositoryGroupId: `${AUTO_REPOSITORY_GROUP_PREFIX}repo-oss`,
    agentCliType: AgentCliType.OPENCODE,
    usePlanMode: false,
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

const mockSubTasksFor004 = [
  create(SubTaskSchema, {
    subTaskId: "sub-004-1",
    unitTaskId: "task-004",
    type: SubTaskType.INITIAL_IMPLEMENTATION,
    status: SubTaskStatus.WAITING_FOR_USER_INPUT,
    completionReason: SubTaskCompletionReason.UNSPECIFIED,
    sessionId: "sess-004-1",
    createdAt: timestampFromDate(new Date("2026-03-14T11:00:00Z")),
    updatedAt: timestampFromDate(new Date("2026-03-14T11:30:00Z")),
    title: "Implement rate limiting logic.",
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

const mockPullRequests = [
  create(PullRequestRecordSchema, { prTrackingId: "pr-157", status: PrStatus.CI_FAILED }),
  create(PullRequestRecordSchema, { prTrackingId: "pr-142", status: PrStatus.APPROVED }),
  create(PullRequestRecordSchema, { prTrackingId: "pr-138", status: PrStatus.MERGED }),
];

const mockRepositories = [
  create(RepositorySchema, {
    repositoryId: "repo-oss",
    workspaceId: DEFAULT_WORKSPACE_ID,
    repositoryUrl: "https://github.com/example/oss",
    createdAt: timestampFromDate(new Date("2026-03-10T00:00:00Z")),
    updatedAt: timestampFromDate(new Date("2026-03-10T00:00:00Z")),
  }),
  create(RepositorySchema, {
    repositoryId: "repo-infra",
    workspaceId: DEFAULT_WORKSPACE_ID,
    repositoryUrl: "https://github.com/example/infra",
    createdAt: timestampFromDate(new Date("2026-03-10T00:00:00Z")),
    updatedAt: timestampFromDate(new Date("2026-03-10T00:00:00Z")),
  }),
];

const mockRepositoryGroups = [
  create(RepositoryGroupSchema, {
    repositoryGroupId: "repo-group-main",
    workspaceId: DEFAULT_WORKSPACE_ID,
    members: [
      create(RepositoryGroupMemberSchema, {
        repositoryId: "repo-oss",
        branchRef: "main",
        displayOrder: 0,
        repository: mockRepositories[0],
      }),
    ],
    createdAt: timestampFromDate(new Date("2026-03-10T00:00:00Z")),
    updatedAt: timestampFromDate(new Date("2026-03-10T00:00:00Z")),
  }),
  create(RepositoryGroupSchema, {
    repositoryGroupId: `${AUTO_REPOSITORY_GROUP_PREFIX}repo-oss`,
    workspaceId: DEFAULT_WORKSPACE_ID,
    members: [
      create(RepositoryGroupMemberSchema, {
        repositoryId: "repo-oss",
        branchRef: "HEAD",
        displayOrder: 0,
        repository: mockRepositories[0],
      }),
    ],
    createdAt: timestampFromDate(new Date("2026-03-10T00:00:00Z")),
    updatedAt: timestampFromDate(new Date("2026-03-10T00:00:00Z")),
  }),
];

const mockWorkspaceSettings = create(WorkspaceSettingsSchema, {
  workspaceId: DEFAULT_WORKSPACE_ID,
  defaultAgentCliType: AgentCliType.CLAUDE_CODE,
});

let lastCreateUnitTaskRequest:
  | {
      repositoryGroupId?: string;
      repositoryId?: string;
    }
  | null = null;

interface TestTransportOptions {
  workspaces?: Array<{
    workspaceId: string;
    name: string;
    type?: WorkspaceType;
  }>;
  createRepositoryErrorMessage?: string;
}

function createTestTransport(options: TestTransportOptions = {}) {
  const workspaces = (options.workspaces ?? [
    {
      workspaceId: DEFAULT_WORKSPACE_ID,
      name: "Default Workspace",
      type: WorkspaceType.LOCAL_ENDPOINT,
    },
  ]).map((workspace) =>
    create(WorkspaceSchema, {
      workspaceId: workspace.workspaceId,
      name: workspace.name,
      type: workspace.type ?? WorkspaceType.LOCAL_ENDPOINT,
      createdAt: timestampFromDate(new Date("2026-03-10T00:00:00Z")),
    }),
  );
  return createRouterTransport((router) => {
    router.service(TaskService, {
      listUnitTasks: () => ({ unitTasks: mockUnitTasks }),
      listSubTasks: (req) => {
        if (req.unitTaskId === "task-001") return { subTasks: mockSubTasksFor001 };
        if (req.unitTaskId === "task-002") return { subTasks: mockSubTasksFor002 };
        if (req.unitTaskId === "task-004") return { subTasks: mockSubTasksFor004 };
        return { subTasks: [] };
      },
      createUnitTask: (req) => {
        lastCreateUnitTaskRequest = {
          repositoryGroupId: req.repositoryGroupId,
          repositoryId: req.repositoryId,
        };
        return {
          unitTask: create(UnitTaskSchema, {
            unitTaskId: `task-${Date.now()}`,
            prompt: req.prompt,
            repositoryGroupId:
              req.repositoryGroupId || (req.repositoryId ? `${AUTO_REPOSITORY_GROUP_PREFIX}${req.repositoryId}` : ""),
            agentCliType: req.agentCliType,
            usePlanMode: req.usePlanMode,
            status: UnitTaskStatus.QUEUED,
            createdAt: timestampFromDate(new Date()),
            updatedAt: timestampFromDate(new Date()),
          }),
        };
      },
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
      listSessionCapabilities: () => ({
        capabilities: [
          { agentCliType: AgentCliType.CLAUDE_CODE, supportsFork: true, displayName: "Claude Code", supportsPlanMode: true },
          { agentCliType: AgentCliType.CODEX_CLI, supportsFork: false, displayName: "Codex CLI", supportsPlanMode: true },
          { agentCliType: AgentCliType.OPENCODE, supportsFork: false, displayName: "OpenCode", supportsPlanMode: false },
        ],
      }),
      forkSession: () => ({ forkedSession: undefined }),
      listForkedSessions: () => ({ sessions: [] }),
      archiveForkedSession: () => ({}),
      getLatestWaitingSession: () => ({ session: undefined }),
      submitSessionInput: () => ({}),
      stopAgentSession: () => ({}),
    });
    router.service(WorkspaceService, {
      getWorkspace: (req) => ({
        workspace: workspaces.find((workspace) => workspace.workspaceId === req.workspaceId),
      }),
      listWorkspaces: () => ({ workspaces }),
      createWorkspace: (req) => {
        const createdWorkspace = create(WorkspaceSchema, {
          workspaceId: `ws-${Date.now()}`,
          name: req.name,
          type: req.type,
          createdAt: timestampFromDate(new Date()),
        });
        workspaces.push(createdWorkspace);
        return { workspace: createdWorkspace };
      },
      setActiveWorkspace: (req) => {
        const workspace = workspaces.find((item) => item.workspaceId === req.workspaceId);
        if (!workspace) {
          throw new Error(`workspace not found: ${req.workspaceId}`);
        }
        return { workspace };
      },
      getWorkspaceWorkStatus: () => ({ status: 0 }),
      getWorkspaceSettings: () => ({ settings: mockWorkspaceSettings }),
      updateWorkspaceSettings: (req) => ({
        settings: create(WorkspaceSettingsSchema, {
          workspaceId: req.workspaceId,
          defaultAgentCliType: req.defaultAgentCliType,
        }),
      }),
    });
    router.service(PrManagementService, {
      getPullRequest: () => ({ pullRequest: undefined }),
      listPullRequests: () => ({ pullRequests: mockPullRequests }),
      trackPullRequest: () => ({ pullRequest: undefined }),
    });
    router.service(ReviewAssistService, {
      listReviewAssistItems: () => ({ items: [] }),
    });
    router.service(ReviewCommentService, {
      listReviewComments: () => ({ comments: [] }),
    });
    router.service(RepositoryService, {
      getRepository: () => ({ repository: mockRepositories[0] }),
      listRepositories: () => ({ repositories: mockRepositories }),
      createRepository: async (req) => {
        if (options.createRepositoryErrorMessage) {
          throw new ConnectError(options.createRepositoryErrorMessage, Code.Internal);
        }
        return {
          repository: create(RepositorySchema, {
            repositoryId: `repo-${Date.now()}`,
            workspaceId: req.workspaceId,
            repositoryUrl: req.repositoryUrl,
            createdAt: timestampFromDate(new Date()),
            updatedAt: timestampFromDate(new Date()),
          }),
        };
      },
      updateRepository: (req) => ({
        repository: create(RepositorySchema, {
          repositoryId: req.repositoryId,
          workspaceId: req.workspaceId,
          repositoryUrl: req.repositoryUrl,
          createdAt: timestampFromDate(new Date()),
          updatedAt: timestampFromDate(new Date()),
        }),
      }),
      deleteRepository: () => ({}),
      getRepositoryGroup: () => ({ repositoryGroup: undefined }),
      listRepositoryGroups: () => ({ repositoryGroups: mockRepositoryGroups }),
      createRepositoryGroup: (req) => ({
        repositoryGroup: create(RepositoryGroupSchema, {
          repositoryGroupId: req.repositoryGroupId,
          workspaceId: req.workspaceId,
          members: req.members.map((member, index) =>
            create(RepositoryGroupMemberSchema, {
              repositoryId: member.repositoryId,
              branchRef: member.branchRef,
              displayOrder: index,
              repository: mockRepositories.find((repository) => repository.repositoryId === member.repositoryId),
            }),
          ),
          createdAt: timestampFromDate(new Date()),
          updatedAt: timestampFromDate(new Date()),
        }),
      }),
      updateRepositoryGroup: (req) => ({
        repositoryGroup: create(RepositoryGroupSchema, {
          repositoryGroupId: req.repositoryGroupId,
          workspaceId: req.workspaceId,
          members: req.members.map((member, index) =>
            create(RepositoryGroupMemberSchema, {
              repositoryId: member.repositoryId,
              branchRef: member.branchRef,
              displayOrder: index,
              repository: mockRepositories.find((repository) => repository.repositoryId === member.repositoryId),
            }),
          ),
          createdAt: timestampFromDate(new Date()),
          updatedAt: timestampFromDate(new Date()),
        }),
      }),
      deleteRepositoryGroup: () => ({}),
    });
    // EventStreamService is server-streaming; provide a no-op stub
    router.service(EventStreamService, {
      streamWorkspaceEvents: async function* () {
        // No events in tests
      },
    });
  });
}

function renderWithProviders(
  ui: React.ReactElement,
  {
    initialEntries = ["/tasks"],
    transportOptions,
  }: { initialEntries?: string[]; transportOptions?: TestTransportOptions } = {},
) {
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
      <TransportProvider transport={createTestTransport(transportOptions)}>
        <MemoryRouter initialEntries={initialEntries}>
          {ui}
        </MemoryRouter>
      </TransportProvider>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  localStorageMock.clear();
  lastCreateUnitTaskRequest = null;
  document.documentElement.classList.remove("dark");
  localStorageMock.setItem("dexdex-active-workspace-id", DEFAULT_WORKSPACE_ID);
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

  it("navigates to repository groups via sidebar", async () => {
    const user = userEvent.setup();
    renderWithProviders(<App />);

    await screen.findByTestId("task-list");

    await user.click(screen.getByTestId("nav-repository-groups"));
    expect(await screen.findByTestId("repository-groups-page")).toBeTruthy();
    expect(screen.getByRole("heading", { name: "Repository Groups", level: 1 })).toBeTruthy();
    expect(screen.queryByText(`${AUTO_REPOSITORY_GROUP_PREFIX}repo-oss`)).toBeNull();
  });

  it("navigates to repositories via sidebar", async () => {
    const user = userEvent.setup();
    renderWithProviders(<App />);

    await screen.findByTestId("task-list");

    await user.click(screen.getByTestId("nav-repositories"));
    expect(await screen.findByTestId("repositories-page")).toBeTruthy();
    expect(screen.getByRole("heading", { name: "Repositories", level: 1 })).toBeTruthy();
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
    const promptInput = screen.getByTestId("task-prompt-input");
    await waitFor(() => {
      expect(document.activeElement).toBe(promptInput);
    });
  });

  it("closes create task dialog with Escape", async () => {
    const user = userEvent.setup();
    renderWithProviders(<App />);

    await screen.findByTestId("task-list");
    await user.click(screen.getByTestId("create-task-button"));
    expect(screen.getByTestId("create-dialog")).toBeTruthy();

    await user.keyboard("{Escape}");
    expect(screen.queryByTestId("create-dialog")).toBeNull();
  });

  it("creates a new task via dialog", async () => {
    const user = userEvent.setup();
    renderWithProviders(<App />);

    await screen.findByTestId("task-list");
    await user.click(screen.getByTestId("create-task-button"));

    await user.type(screen.getByTestId("task-prompt-input"), "My new task prompt");
    await user.selectOptions(screen.getByTestId("task-repo-group-select"), "group:repo-group-main");
    await user.selectOptions(screen.getByTestId("task-agent-select"), `${AgentCliType.CLAUDE_CODE}`);
    if (screen.queryByTestId("task-plan-mode-toggle")) {
      await user.click(screen.getByTestId("task-plan-mode-toggle"));
    }
    await user.click(screen.getByTestId("submit-create-task"));

    // Dialog should close
    expect(screen.queryByTestId("create-dialog")).toBeNull();
    expect(lastCreateUnitTaskRequest?.repositoryGroupId).toBe("repo-group-main");
    expect(lastCreateUnitTaskRequest?.repositoryId).toBe("");
  });

  it("creates a new task via repository selector", async () => {
    const user = userEvent.setup();
    renderWithProviders(<App />);

    await screen.findByTestId("task-list");
    await user.click(screen.getByTestId("create-task-button"));

    await user.type(screen.getByTestId("task-prompt-input"), "Fix CI for repo target");
    await user.selectOptions(screen.getByTestId("task-repo-group-select"), "repository:repo-oss");
    await user.selectOptions(screen.getByTestId("task-agent-select"), `${AgentCliType.CLAUDE_CODE}`);
    await user.click(screen.getByTestId("submit-create-task"));

    expect(screen.queryByTestId("create-dialog")).toBeNull();
    expect(lastCreateUnitTaskRequest?.repositoryId).toBe("repo-oss");
    expect(lastCreateUnitTaskRequest?.repositoryGroupId).toBe("");
  });

  it("renders repository-based metadata instead of internal auto group id", async () => {
    renderWithProviders(<App />);
    expect(await screen.findByTestId("task-row-task-005")).toBeTruthy();
    expect(screen.getByText("Repository: repo-oss")).toBeTruthy();
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

  it("shows repository navigation actions in command palette", async () => {
    const user = userEvent.setup();
    renderWithProviders(<App />);

    await screen.findByTestId("task-list");
    await user.keyboard("{Meta>}k{/Meta}");

    expect(screen.getByText("Go to Repository Groups")).toBeTruthy();
    expect(screen.getByText("Go to Repositories")).toBeTruthy();
  });

  it("navigates to repository groups via command palette", async () => {
    const user = userEvent.setup();
    renderWithProviders(<App />);

    await screen.findByTestId("task-list");
    await user.keyboard("{Meta>}k{/Meta}");

    await user.click(screen.getByText("Go to Repository Groups"));
    expect(await screen.findByTestId("repository-groups-page")).toBeTruthy();
    expect(screen.getByRole("heading", { name: "Repository Groups", level: 1 })).toBeTruthy();
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

  it("auto-focuses the revise input when revise mode opens", async () => {
    const user = userEvent.setup();
    renderWithProviders(<App />);

    await screen.findByTestId("task-row-task-002");
    await user.click(screen.getByTestId("task-row-task-002"));
    await screen.findByTestId("plan-decisions");
    await user.click(screen.getByTestId("revise-button"));

    const reviseInput = screen.getByTestId("revision-note-input");
    await waitFor(() => {
      expect(document.activeElement).toBe(reviseInput);
    });
  });

  it("auto-focuses session input for waiting-for-input subtasks", async () => {
    const user = userEvent.setup();
    renderWithProviders(<App />);

    await screen.findByTestId("task-row-task-004");
    await user.click(screen.getByTestId("task-row-task-004"));
    const sessionInput = await screen.findByTestId("session-input-textarea");

    await waitFor(() => {
      expect(document.activeElement).toBe(sessionInput);
    });
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

  it("navigates to pull requests page via sidebar", async () => {
    const user = userEvent.setup();
    renderWithProviders(<App />);

    await screen.findByTestId("task-list");
    await user.click(screen.getByTestId("nav-prs"));
    expect(await screen.findByTestId("pr-management-page")).toBeTruthy();
    expect(screen.getByRole("heading", { name: "Pull Requests" })).toBeTruthy();
  });

  it("displays pull requests with status badges", async () => {
    const user = userEvent.setup();
    renderWithProviders(<App />);

    await screen.findByTestId("task-list");
    await user.click(screen.getByTestId("nav-prs"));
    expect(await screen.findByTestId("pr-row-pr-157")).toBeTruthy();
    expect(screen.getByTestId("pr-row-pr-142")).toBeTruthy();
    expect(screen.getByTestId("pr-row-pr-138")).toBeTruthy();
  });

  it("shows session input form for waiting-for-input subtask", async () => {
    const user = userEvent.setup();
    renderWithProviders(<App />);

    await screen.findByTestId("task-row-task-004");
    await user.click(screen.getByTestId("task-row-task-004"));
    expect(await screen.findByTestId("session-input-form")).toBeTruthy();
    expect(screen.getByTestId("session-input-textarea")).toBeTruthy();
    expect(screen.getByTestId("session-input-submit")).toBeTruthy();
  });

  it("shows notifications in inbox with unread indicator", async () => {
    const user = userEvent.setup();
    renderWithProviders(<App />);

    await screen.findByTestId("task-list");
    await user.click(screen.getByTestId("nav-inbox"));

    expect(await screen.findByText("Plan approval needed")).toBeTruthy();
    expect(screen.getByText("CI failure on PR #42")).toBeTruthy();
  });

  it("migrates legacy persisted workspace id to canonical workspace id", async () => {
    localStorageMock.setItem("dexdex-active-workspace-id", LEGACY_DEFAULT_WORKSPACE_ID);
    renderWithProviders(<App />);

    await screen.findByTestId("task-list");
    await waitFor(() => {
      expect(localStorageMock.getItem("dexdex-active-workspace-id")).toBe(DEFAULT_WORKSPACE_ID);
    });
  });

  it("blocks repository creation when no workspace exists", async () => {
    localStorageMock.removeItem("dexdex-active-workspace-id");
    renderWithProviders(<App />, {
      initialEntries: ["/repositories"],
      transportOptions: { workspaces: [] },
    });

    expect(await screen.findByTestId("repositories-page")).toBeTruthy();
    expect(screen.getByTestId("repository-workspace-hint")).toBeTruthy();

    const createInput = screen.getByTestId("create-repository-url") as HTMLInputElement;
    const createButton = screen.getByRole("button", { name: "Add Repository" }) as HTMLButtonElement;
    expect(createInput.disabled).toBe(true);
    expect(createButton.disabled).toBe(true);
  });

  it("shows repository create validation errors", async () => {
    const user = userEvent.setup();
    renderWithProviders(<App />, { initialEntries: ["/repositories"] });

    expect(await screen.findByTestId("repositories-page")).toBeTruthy();
    const createInput = screen.getByTestId("create-repository-url") as HTMLInputElement;
    const createButton = screen.getByRole("button", { name: "Add Repository" }) as HTMLButtonElement;

    await waitFor(() => {
      expect(createInput.disabled).toBe(false);
      expect(createButton.disabled).toBe(false);
    });

    await user.clear(createInput);
    await user.type(createInput, "github.com/example/new-repo");
    await user.click(createButton);

    const mutationError = await screen.findByTestId("repository-mutation-error");
    expect(mutationError.textContent).toBe("Repository URL must start with http:// or https://.");
  });

  it("shows repository create mutation errors", async () => {
    const user = userEvent.setup();
    renderWithProviders(<App />, {
      initialEntries: ["/repositories"],
      transportOptions: { createRepositoryErrorMessage: "rpc create failed" },
    });

    expect(await screen.findByTestId("repositories-page")).toBeTruthy();
    const createInput = screen.getByTestId("create-repository-url") as HTMLInputElement;
    const createButton = screen.getByRole("button", { name: "Add Repository" }) as HTMLButtonElement;

    await waitFor(() => {
      expect(createInput.disabled).toBe(false);
      expect(createButton.disabled).toBe(false);
    });

    await user.type(createInput, "https://github.com/example/new-repo");
    await user.click(createButton);

    const mutationError = await screen.findByTestId("repository-mutation-error");
    expect(mutationError.textContent?.includes("Failed to add repository:")).toBe(true);
    expect(mutationError.textContent?.includes("rpc create failed")).toBe(true);
  });
});
