# Feature: interfaces

## Interfaces
Canonical project identifier:

```ts
enum ProjectId {
  DexDex = "dexdex",
}
```

Canonical component identifiers:

```ts
enum DexDexComponent {
  MainServer = "main-server",
  WorkerServer = "worker-server",
  DesktopApp = "desktop-app",
}
```

Canonical release tag prefix:

```ts
enum DexDexReleaseTagPrefix {
  Stable = "dexdex@v",
}
```

Canonical package identifiers:

```ts
enum DexDexPackageId {
  HomebrewDesktopCask = "dexdex",
  HomebrewMainServerFormula = "dexdex-main-server",
  HomebrewWorkerServerFormula = "dexdex-worker-server",
  WingetDesktop = "DelinoIO.DexDex",
  WingetMainServer = "DelinoIO.DexDexMainServer",
  WingetWorkerServer = "DelinoIO.DexDexWorkerServer",
}
```

Installer script contract:
- `scripts/install/dexdex-stack.sh`
- `scripts/install/dexdex-stack.ps1`
- Required shared flags:
: `--version <semver|latest>`
: `--method package-manager|direct`

Deployment mode identifiers:

```ts
enum DexDexDeploymentMode {
  SingleInstance = "SINGLE_INSTANCE",
  Scale = "SCALE",
}
```

Workspace mode identifiers:

```ts
enum WorkspaceMode {
  Local = "LOCAL",
  Remote = "REMOTE",
}
```

Desktop workspace endpoint source identifiers:

```ts
enum WorkspaceEndpointSource {
  ManagedLoopback = "MANAGED_LOOPBACK",
  LocalOverride = "LOCAL_OVERRIDE",
  UserRemote = "USER_REMOTE",
}
```

Desktop normalized connection contract:

```ts
type ResolvedWorkspaceConnection = {
  mode: WorkspaceMode;
  endpointUrl: string;
  endpointSource: WorkspaceEndpointSource;
  token?: string;
  transport: "CONNECT_RPC";
};
```

Desktop saved workspace profile contract:

```ts
type SavedWorkspaceProfile = {
  workspaceId: string;
  mode: WorkspaceMode;
  remoteEndpointUrl?: string;
  lastUsedAt: string;
};
```

Desktop Connect Query scaffold contract:

```ts
type DexDexConnectQueryRuntime = {
  queryClient: "SINGLETON_REACT_QUERY_CLIENT";
  transportProvider: "@connectrpc/connect-query";
  transportFactory: "(endpointUrl: string) => ConnectTransport";
  authInterceptor: "(token?: string) => AuthorizationBearerHeader";
};
```

Desktop page identifiers:

```ts
enum DexDexPageId {
  Projects = "PROJECTS",
  Threads = "THREADS",
  Review = "REVIEW",
  Automations = "AUTOMATIONS",
  Worktrees = "WORKTREES",
  LocalEnvironments = "LOCAL_ENVIRONMENTS",
  Settings = "SETTINGS",
}
```

Desktop page route contract:
- `/` (startup workspace picker)
- `/projects`
- `/threads`
- `/review`
- `/automations`
- `/worktrees`
- `/local-environments`
- `/settings`

Desktop visual-regression mode contract:
- Query parameter `?visual=1` enables fixture-backed desktop rendering for route screenshots.
- Visual mode auto-bootstraps a synthetic workspace session when route is not `/`.
- Visual mode preserves the startup picker at `/` for dedicated picker screenshot baselines.

Desktop shared selection contract:

```ts
type SharedSelectionState = {
  selectedUnitTaskId: string | null;
  selectedSubTaskId: string | null;
  selectedSessionId: string | null;
  selectedPrTrackingId: string | null;
};
```

Desktop list-read request contract:

```ts
type DexDexListRequest = {
  workspaceId: string;
  pageSize: number;
  pageToken: string;
  status?: "ENUM_FILTER";
  cliType?: "ENUM_FILTER";
};
```

Desktop action center state contract:

```ts
type ActionCenterState = {
  label: string;
  status: "idle" | "pending" | "success" | "error";
  message: string;
};
```

Desktop local store contract:

```ts
type DesktopLocalStoreState = {
  automations: {
    id: string;
    name: string;
    schedule: string;
    enabled: boolean;
    lastRunAt: string | null;
  }[];
  localEnvironments: {
    id: string;
    name: string;
    endpointUrl: string;
    health: "UNKNOWN" | "HEALTHY" | "UNREACHABLE";
    lastCheckedAt: string | null;
    lastErrorMessage: string | null;
  }[];
  settings: {
    defaultPage: DexDexPageId;
    compactMode: boolean;
    autoStartStream: boolean;
  };
  lastSelectedAutomationId: string | null;
  lastSelectedEnvironmentId: string | null;
};
```

Desktop generated TypeScript contract outputs:
- Generated message/service descriptors: `apps/dexdex/src/gen/v1/dexdex_pb.ts`
- Generated Connect Query method descriptors: `apps/dexdex/src/gen/v1/*_connectquery.ts`
- Regeneration command: `pnpm --filter dexdex run gen:proto`

Proto source-of-truth contract:
- Package: `dexdex.v1`
- Proto root path: `protos/dexdex/v1/*.proto`
- Shared proto is the canonical contract surface for:
: `WorkspaceService`
: `RepositoryService`
: `TaskService`
: `SessionService`
: `PrManagementService`
: `ReviewAssistService`
: `ReviewCommentService`
: `BadgeThemeService`
: `NotificationService`
: `EventStreamService`
: `WorkerSessionAdapterService`

Primary Connect RPC service contracts:
- `WorkspaceService.GetWorkspace`
- `WorkspaceService.GetWorkspaceOverview`
- `RepositoryService.GetRepositoryGroup`
- `RepositoryService.ListRepositoryGroups`
- `TaskService.GetUnitTask`
- `TaskService.GetSubTask`
- `TaskService.ListUnitTasks`
- `TaskService.ListSubTasks`
- `TaskService.SubmitPlanDecision`
- `TaskService.RunSubTaskSessionAdapter`
- `SessionService.GetSessionOutput`
- `SessionService.ListSessions`
- `PrManagementService.GetPullRequest`
- `PrManagementService.ListPullRequests`
- `ReviewAssistService.ListReviewAssistItems`
- `ReviewCommentService.ListReviewComments`
- `BadgeThemeService.GetBadgeTheme`
- `NotificationService.ListNotifications`
- `EventStreamService.StreamWorkspaceEvents` (server-streaming)
- `WorkerSessionAdapterService.NormalizeSessionOutputFixture`

Core enum contracts:

```txt
UnitTaskStatus:
- QUEUED
- IN_PROGRESS
- ACTION_REQUIRED
- BLOCKED
- COMPLETED
- FAILED
- CANCELLED

SubTaskType:
- INITIAL_IMPLEMENTATION
- REQUEST_CHANGES
- PR_CREATE
- PR_REVIEW_FIX
- PR_CI_FIX
- MANUAL_RETRY

SubTaskStatus:
- QUEUED
- IN_PROGRESS
- WAITING_FOR_PLAN_APPROVAL
- WAITING_FOR_USER_INPUT
- COMPLETED
- FAILED
- CANCELLED

SubTaskCompletionReason:
- SUCCEEDED
- REVISED
- PLAN_REJECTED
- FAILED
- CANCELLED_BY_USER

AgentSessionStatus:
- STARTING
- RUNNING
- WAITING_FOR_INPUT
- COMPLETED
- FAILED
- CANCELLED

SessionOutputKind:
- TEXT
- PLAN_UPDATE
- TOOL_CALL
- TOOL_RESULT
- PROGRESS
- WARNING
- ERROR

AgentCliType:
- CODEX_CLI
- CLAUDE_CODE
- OPENCODE

SessionAdapterFixturePreset:
- CODEX_CLI_FAILURE
- CLAUDE_CODE_STREAM
- OPENCODE_RUN

SessionOutputSourceEventType:
- RUN_STARTED
- TURN_STARTED
- TEXT_DELTA
- TEXT_FINAL
- STEP_STARTED
- STEP_FINISHED
- RESULT
- ERROR
- SYSTEM

ActionType:
- REVIEW_REQUESTED
- PR_CREATION_READY
- PLAN_APPROVAL_REQUIRED
- CI_FAILED
- MERGE_CONFLICT
- SECURITY_ALERT
- USER_INPUT_REQUIRED

PrStatus:
- OPEN
- APPROVED
- CHANGES_REQUESTED
- MERGED
- CLOSED
- CI_FAILED

NotificationType:
- TASK_ACTION_REQUIRED
- PLAN_ACTION_REQUIRED
- PR_REVIEW_ACTIVITY
- PR_CI_FAILURE
- AGENT_SESSION_FAILED

StreamEventType:
- TASK_UPDATED
- SUBTASK_UPDATED
- SESSION_OUTPUT
- SESSION_STATE_CHANGED
- PR_UPDATED
- REVIEW_ASSIST_UPDATED
- INLINE_COMMENT_UPDATED
- NOTIFICATION_CREATED
```

Task decision contract:
- `SubmitPlanDecisionRequest` identifies target by `sub_task_id` (no `unit_task_id` field).
- `APPROVE`: resumes same SubTask (`WAITING_FOR_PLAN_APPROVAL` -> `IN_PROGRESS`).
- `REVISE`: requires non-empty `revision_note`, completes current SubTask with `completion_reason=REVISED`, and creates queued `REQUEST_CHANGES` SubTask.
- `REJECT`: cancels current SubTask with `completion_reason=PLAN_REJECTED` and creates no follow-up SubTask.

Session adapter orchestration contract:
- `RunSubTaskSessionAdapterRequest` requires `workspace_id`, `unit_task_id`, `sub_task_id`, `session_id`, `cli_type`, and exactly one input (`fixture_preset` or `raw_jsonl`).
- Main server validates SubTask ownership (`sub_task_id` belongs to `unit_task_id`) before invoking worker normalization.
- Main server publishes stream events in this order per successful call:
: `SUBTASK_UPDATED` (`IN_PROGRESS`) -> `SESSION_OUTPUT` (`0..N`) -> `SESSION_STATE_CHANGED` -> optional final `SUBTASK_UPDATED` (`COMPLETED` or `FAILED`).
- Worker terminal-error output maps to `session_status=FAILED`; terminal non-error output maps to `COMPLETED`; no terminal output maps to `RUNNING`.

Workspace stream contract:
- `from_sequence` is exclusive (`sequence > from_sequence`).
- Event sequence is workspace-scoped, monotonic, and starts at `1`.
- If `from_sequence` is older than retention, return `OutOfRange` and include `EventStreamCursorOutOfRangeDetail.earliest_available_sequence`.
- `StreamWorkspaceEventsResponse.oneof payload` has explicit event payload variants for all `StreamEventType` values.
- Live-tail mode remains open after replay and delivers new events as they are published.
- Keepalive frames use `sequence=0` and `event_type=STREAM_EVENT_TYPE_UNSPECIFIED` (clients should ignore them for replay cursors/state materialization).

Session output normalization contract:
- `SessionOutputEvent.source` is worker-normalized metadata for provider-native CLI events.
- `SessionOutputEvent.is_terminal` indicates whether the normalized source event closed a turn/run boundary.
- `SessionOutputSourceMetadata.source_sequence` is strictly monotonic per source stream (`1..N`).
- Raw provider event identifiers are preserved in `SessionOutputSourceMetadata.raw_event_type`.

