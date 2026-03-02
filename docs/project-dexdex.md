# Project: dexdex

## Goal
`dexdex` is a Connect RPC-first engineering workflow platform for AI-assisted code execution and PR lifecycle management.
It provides a single operational model across desktop and mobile clients while keeping server-side orchestration and worker execution boundaries explicit.

## Path
- Client: `apps/dexdex-app`
- Main server: `servers/dexdex-main`
- Worker server: `servers/dexdex-worker`

## Runtime and Language
- Client: Tauri + React (TypeScript)
- Main server: Go
- Worker server: Go

## Users
- Engineers running AI-assisted implementation and remediation tasks across one or more repositories
- Reviewers and maintainers managing PR quality, CI outcomes, and review feedback
- Operators managing workspace connectivity, policies, and runtime reliability

## In Scope
- Connect RPC-first business flows for workspace, repository, task, session, PR, review, and notification workflows
- Workspace connectivity model for local and remote endpoint workspaces
- UnitTask/SubTask/AgentSession execution model with plan-mode decision checkpoints
- Worktree-only repository execution policy and repository-group ordered execution
- Real commit-chain contract for code-changing subtasks
- PR management and remediation flows (manual and policy-driven auto-fix)
- Server-streamed event model for low-latency client updates
- Web Notification API-backed notification delivery with in-app notification center authority

## Out of Scope
- Direct execution against arbitrary local folders without worktree materialization
- Tauri-invoke-first business contracts for task and workflow state
- Patch-only authoritative output that bypasses real git commit history
- Provider-native agent message contracts exposed to main server or clients
- Native OS notification plugins as the primary notification channel

## Architecture
- `dexdex` is a three-component system: client (`apps/dexdex-app`), main server (`servers/dexdex-main`), and worker server (`servers/dexdex-worker`).
- All business communication uses Connect RPC contracts; platform-native bindings are reserved for local integration concerns (window lifecycle, keychain, file picker, deep links).
- Main server owns control-plane state: workspace and repository metadata, UnitTask/SubTask orchestration, PR tracking, review assist, inline comments, notifications, and workspace event sequencing.
- Worker server owns execution-plane responsibilities: repository cache/worktree management, agent process supervision, provider-native output normalization, commit-chain generation, and artifact export.
- Execution uses repository-group order semantics: launch from the first repository and attach remaining repositories as additional execution directories.
- Task model hierarchy is fixed: UnitTask (top-level work item) -> SubTask (execution or remediation unit) -> AgentSession (runtime session history).
- Plan mode is a first-class flow: sessions can pause at proposal checkpoints and resume/terminate after explicit user decision.
- Event streaming is workspace-scoped and sequence-based, with replay/reconnect behavior defined by deployment retention boundaries.
- Notification delivery is event-driven: in-app notification center is authoritative and Web Notification API dispatch is conditional by permission and app foreground/background state.

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
  Client = "client",
  MainServer = "main-server",
  WorkerServer = "worker-server",
}
```

Component path mapping:
- `Client` -> `apps/dexdex-app`
- `MainServer` -> `servers/dexdex-main`
- `WorkerServer` -> `servers/dexdex-worker`

Core workspace connectivity identifiers:

```ts
enum WorkspaceType {
  LocalEndpoint = "LOCAL_ENDPOINT",
  RemoteEndpoint = "REMOTE_ENDPOINT",
}
```

Core task execution identifiers:

```ts
enum UnitTaskStatus {
  Queued = "QUEUED",
  InProgress = "IN_PROGRESS",
  ActionRequired = "ACTION_REQUIRED",
  Blocked = "BLOCKED",
  Completed = "COMPLETED",
  Failed = "FAILED",
  Cancelled = "CANCELLED",
}

enum SubTaskType {
  InitialImplementation = "INITIAL_IMPLEMENTATION",
  RequestChanges = "REQUEST_CHANGES",
  PrCreate = "PR_CREATE",
  PrReviewFix = "PR_REVIEW_FIX",
  PrCiFix = "PR_CI_FIX",
  ManualRetry = "MANUAL_RETRY",
}

enum AgentSessionStatus {
  Starting = "STARTING",
  Running = "RUNNING",
  WaitingForInput = "WAITING_FOR_INPUT",
  Completed = "COMPLETED",
  Failed = "FAILED",
  Cancelled = "CANCELLED",
}
```

Plan-mode decision identifiers:

```ts
enum PlanDecision {
  Approve = "APPROVE",
  Revise = "REVISE",
  Reject = "REJECT",
}
```

Event stream identifiers:

```ts
enum StreamEventType {
  TaskUpdated = "TASK_UPDATED",
  SubTaskUpdated = "SUBTASK_UPDATED",
  SessionOutput = "SESSION_OUTPUT",
  SessionStateChanged = "SESSION_STATE_CHANGED",
  PrUpdated = "PR_UPDATED",
  ReviewAssistUpdated = "REVIEW_ASSIST_UPDATED",
  InlineCommentUpdated = "INLINE_COMMENT_UPDATED",
  NotificationCreated = "NOTIFICATION_CREATED",
}
```

Notification category identifiers:

```ts
enum NotificationType {
  TaskActionRequired = "TASK_ACTION_REQUIRED",
  PlanActionRequired = "PLAN_ACTION_REQUIRED",
  PrReviewActivity = "PR_REVIEW_ACTIVITY",
  PrCiFailure = "PR_CI_FAILURE",
  AgentSessionFailed = "AGENT_SESSION_FAILED",
}
```

Connect RPC service boundary (business contracts):

```ts
enum DexDexConnectService {
  Workspace = "WorkspaceService",
  Repository = "RepositoryService",
  Task = "TaskService",
  Session = "SessionService",
  PrManagement = "PrManagementService",
  ReviewAssist = "ReviewAssistService",
  ReviewComment = "ReviewCommentService",
  BadgeTheme = "BadgeThemeService",
  Notification = "NotificationService",
  EventStream = "EventStreamService",
}
```

Cross-component contract rules:
- Worker is the only boundary allowed to parse provider-native agent outputs.
- Main server and client consume normalized session output contracts only.
- PR creation and local commit application consume real generated commit chains as source of truth.

## Storage
- Main server persistent state (deployment-mode dependent) includes workspace, repository, repository-group, UnitTask, SubTask, and AgentSession metadata.
- Main server also stores ordered generated commit-chain metadata, PR tracking state, review assist state, inline comments, badge themes, notifications, and workspace stream sequence offsets.
- Worker local state includes repository cache directories, task-specific worktree directories, and session-local temporary artifacts.
- Client local state includes active workspace pointer, workspace-scoped tab/draft state, and notification read/permission cache metadata.

## Security
- Enforce Connect RPC over TLS for non-local endpoint workspaces.
- Apply workspace-scoped authentication and authorization checks on every business RPC and stream open request.
- Validate repository URLs, branch names, prompts, and review payloads with strict input rules.
- Limit secret lifetime to runtime execution scope; do not persist raw secret material in logs or user-facing payloads.
- Terminate or degrade stream/session behavior safely on token expiry, permission changes, or trust-boundary violations.

## Logging
Required structured logging baseline:
- Main server: workspace routing, task/subtask transitions, PR polling decisions, stream health, notification trigger reasons
- Worker server: worktree lifecycle, session lifecycle, plan wait/resume transitions, commit-chain generation, cancellation checkpoints
- Client: stream reconnect behavior, notification permission outcomes, user-triggered remediation actions

Required correlation fields:
- `workspace_id`
- `unit_task_id`
- `sub_task_id`
- `session_id`
- `pr_tracking_id`
- `request_id`

Prohibited log content:
- Plaintext secret values
- Provider-native raw payloads outside worker-local debug scope
- Authentication tokens and key material

## Build and Test
Planned baseline commands:
- Main server build/test: `go build ./servers/dexdex-main/...` and `go test ./servers/dexdex-main/...`
- Worker server build/test: `go build ./servers/dexdex-worker/...` and `go test ./servers/dexdex-worker/...`
- Client test: `cd apps/dexdex-app && pnpm test`

Acceptance-focused scenarios:
1. Create and switch local/remote workspaces using shared Connect RPC contracts.
2. Create UnitTask and observe SubTask/AgentSession lifecycle progression through stream updates.
3. Execute repository-group work with first-repository launch and additional repository attachments.
4. Verify code-changing subtasks produce ordered real commit-chain metadata.
5. Verify plan-mode checkpoint pauses and decision loop (`APPROVE`, `REVISE`, `REJECT`) behavior.
6. Track PR state changes, surface actionable signals, and run remediation subtasks.
7. Verify stream reconnect with sequence resume and idempotent reducer behavior.
8. Verify notification deduplication and permission-aware dispatch behavior.

## Roadmap
1. Phase 1: Establish canonical contracts for component boundaries, identifiers, and execution invariants.
2. Phase 2: Implement main server and worker server with normalized session contracts and stream backbone.
3. Phase 3: Implement client shell, task and PR workflows, plan mode, and inline review comment UX.
4. Phase 4: Harden operational reliability, scale deployment profile support, and policy-driven automation.

## Open Questions
- Final persistence backend defaults for early DexDex deployments (SQLite-only vs immediate dual-mode parity).
- PR provider support rollout order and normalization strategy beyond the initial provider.
- Artifact retention windows for session output, patches, and worker-local debug payloads.
- Exact mobile-first UX parity targets for all remediation and plan-mode interactions.
- Cross-workspace policy model for shared teams and delegated permissions.
