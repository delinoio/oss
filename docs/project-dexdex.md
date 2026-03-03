# Project: dexdex

## Goal
`dexdex` is a Connect RPC-first task orchestration platform with a Rust control plane, Rust worker plane, and Tauri desktop client.
It manages UnitTask/SubTask workflows, normalized AgentSession outputs, PR remediation lifecycle, and event-stream-driven updates.
The desktop client provides workspace mode selection and orchestration control while preserving a single normalized downstream UX contract.

## Path
- Main server: `crates/dexdex-main-server`
- Worker server: `crates/dexdex-worker-server`
- Desktop app: `apps/dexdex`
- Desktop frontend: `apps/dexdex/src`
- Desktop Tauri backend: `apps/dexdex/src-tauri`
- Shared proto contracts: `protos/dexdex/v1/*.proto`

## Runtime and Language
- Main server: Rust binary crate
- Worker server: Rust binary crate
- Desktop app frontend: React + TypeScript (Vite)
- Desktop app backend: Rust (Tauri)

## Users
- Developers running AI-assisted implementation workflows
- Reviewers handling PR feedback and remediation loops
- Operators monitoring task/session execution and event delivery health

## In Scope
- Connect RPC-first business contracts for workspace, repository, task, session, PR, review, notification, and stream flows.
- Main server control-plane ownership of task/subtask/session/pr/review/notification state.
- Worker server execution-plane ownership of worktree runs, agent adapters, and output normalization.
- Plan-mode decision flows (`APPROVE`, `REVISE`, `REJECT`) at SubTask scope.
- PR polling and remediation SubTask lifecycle (`PR_CREATE`, `PR_REVIEW_FIX`, `PR_CI_FIX`).
- Workspace event streaming with replay/resume semantics.
- Deployment mode contracts for `SINGLE_INSTANCE` and `SCALE`.
- Desktop workspace mode resolution for `LOCAL` and `REMOTE` with a normalized Connect RPC connection shape.
- Desktop UX parity contract where `LOCAL` and `REMOTE` share the exact same post-resolution business flow behavior.

## Out of Scope
- Tauri-specific business contracts as the primary integration model.
- Patch-only authoritative change outputs without real git commit chain metadata.
- Direct execution against arbitrary local folders without worktree isolation.
- Provider-native raw session payload contracts in main server APIs and client-facing streams.
- Monthly/yearly reporting and analytics product surfaces in this phase.
- Persistent desktop token vault behavior in the initial scaffold phase.

## Architecture
- Main server (`crates/dexdex-main-server`) is the control plane.
: It exposes Connect RPC services and owns orchestration state, PR polling, event brokering, and authorization boundaries.
: It persists normalized UnitTask/SubTask/AgentSession/PR/review/notification data and emits workspace stream envelopes.
- Worker server (`crates/dexdex-worker-server`) is the execution plane.
: It prepares repository worktrees, launches agent sessions, and normalizes provider-native outputs into shared contracts.
: It persists session artifacts and ordered real-commit metadata produced by SubTask execution.
- Desktop app (`apps/dexdex`) is the orchestration client shell.
: It resolves workspace mode into one normalized Connect RPC connection contract.
: It routes all post-resolution task/session workflows through the same shared UI and business pipeline regardless of workspace mode.
: `LOCAL` mode is a special connection mode, but user-visible behavior after endpoint resolution must remain 100% identical to connecting to a `REMOTE` endpoint running on the same machine.
- Connect RPC-first boundary:
: Business workflows traverse main server and worker server through Connect RPC contracts.
: Platform-specific bindings are limited to integration concerns and are not business-data contracts.
- Normalization boundary:
: Worker adapters parse provider-native outputs.
: Main server and downstream clients consume only normalized `SessionOutputEvent` contracts.

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

Desktop Tauri command contract:
- `resolve_local_workspace_endpoint()`
: Returns `{ endpoint_url: string, token?: string, endpoint_source: "MANAGED_LOOPBACK" | "LOCAL_OVERRIDE" }`.
: Hybrid local policy:
: default managed loopback resolution uses `MANAGED_LOOPBACK`.
: explicit local override URL (`DEXDEX_LOCAL_REMOTE_OVERRIDE_URL` or legacy `DEXDEX_LOCAL_REMOTE_URL`) resolves with `LOCAL_OVERRIDE`.
: Resolves local-mode connection target without altering downstream workflow contracts.

Proto source-of-truth contract:
- Package: `dexdex.v1`
- Proto root path: `protos/dexdex/v1/*.proto`
- Proto file layout:
: `common.proto`
: `error_details.proto`
: `workspace.proto`
: `repository.proto`
: `task.proto`
: `session.proto`
: `pr_management.proto`
: `review_assist.proto`
: `review_comment.proto`
: `badge_theme.proto`
: `notification.proto`
: `event_stream.proto`
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

Primary Connect RPC service contracts:
- `WorkspaceService`
: `GetWorkspace`
: `GetWorkspaceHealth`
- `RepositoryService`
: `GetRepositoryGroup`
: `ListRepositoryGroups`
- `TaskService`
: `GetUnitTask`
: `ListUnitTasks`
: `GetSubTask`
: `ListSubTasks`
: `SubmitPlanDecision`
: `RetrySubTask`
- `SessionService`
: `GetAgentSession`
: `ListAgentSessions`
: `GetSessionOutput`
: `SubmitSessionInput`
- `PrManagementService`
: `GetPullRequest`
: `ListPullRequests`
- `ReviewAssistService`
: `ListReviewAssistItems`
: `UpdateReviewAssistDisposition`
- `ReviewCommentService`
: `ListReviewComments`
: `UpdateReviewCommentState`
- `BadgeThemeService`
: `GetBadgeTheme`
: `ListBadgeThemes`
- `NotificationService`
: `ListNotifications`
: `MarkNotificationRead`
: `MarkAllNotificationsRead`
- `EventStreamService` (server-streaming)
: `StreamWorkspaceEvents`
: `GetWorkspaceEventStreamState`

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

Execution and state contracts:
- RepositoryGroup ordering is authoritative for worker launch context:
: first repository is the primary execution directory.
: remaining repositories are attached as additional directories in preserved order.
- List RPC contract:
: all `List*` methods use cursor pagination (`page_size`, `page_token`, `next_page_token`).
: default `page_size` is `50`; maximum `page_size` is `200`.
- Mutation RPC contract:
: all mutating methods include `request_id` and `idempotency_key`.
- SubTask outputs that modify code must produce one or more real git commits and ordered commit-chain metadata.
- Plan mode uses `TaskService.SubmitPlanDecision` with `APPROVE | REVISE | REJECT`.
- `TaskService.SubmitPlanDecision` semantics are fixed:
: `APPROVE` resumes the same SubTask (`WAITING_FOR_PLAN_APPROVAL` -> `IN_PROGRESS`).
: `REVISE` requires non-empty `revision_note`, completes current SubTask with `completion_reason=REVISED`, and creates new `REQUEST_CHANGES` SubTask in `QUEUED`.
: `REJECT` cancels current SubTask with `completion_reason=PLAN_REJECTED` and creates no follow-up SubTask.
- Session output normalization contract:
: `SessionOutputEvent` uses `kind + oneof payload`.
: `SessionOutputEvent` must not expose provider-native payload fields.
- `SESSION_OUTPUT` stream payloads must remain normalized and provider-agnostic.
- `EventStreamService.StreamWorkspaceEvents` replay semantics are fixed:
: `from_sequence` is exclusive (`sequence > from_sequence`).
: event sequence is workspace-scoped, monotonic, and starts at `1`.
: if `from_sequence` is older than retention, return `OutOfRange` and include `earliest_available_sequence`.
- Stream envelope payload contract:
: `WorkspaceEventEnvelope.event_type` and `payload` must match 1:1.
- Error detail contract:
: `INVALID_ARGUMENT` -> `ValidationErrorDetail`
: `FAILED_PRECONDITION` -> `StateMismatchDetail`
: `OUT_OF_RANGE` -> `EventStreamCursorOutOfRangeDetail`
: `PERMISSION_DENIED` -> `AuthorizationDeniedDetail`
: `NOT_FOUND` -> `ResourceNotFoundDetail`
- Desktop downstream flows consume `ResolvedWorkspaceConnection` and must not branch behavior based on `LOCAL` vs `REMOTE` once connection is resolved.

## Storage
Main server logical ownership:
- Workspace, Repository, RepositoryGroup metadata.
- UnitTask/SubTask state and action requirements.
- AgentSession metadata and normalized session output events.
- PullRequestTracking, ReviewAssistItem, ReviewInlineComment records.
- BadgeTheme and Notification records.
- Workspace event sequence offsets and replay metadata.

Worker server logical ownership:
- Repository cache and task worktree artifacts.
- Session-local execution logs and derived artifacts.
- Ordered commit chain metadata (`sha`, parents, message, timestamps).
- Optional patch artifacts derived from real commits.

Desktop scaffold storage contract:
- Workspace mode selection and resolved connection state are in-memory only in the initial scaffold.
- No persistent desktop token storage contract is established in the scaffold phase.

Deployment mode storage contract:
- `SINGLE_INSTANCE`: SQLite + in-process event broker.
- `SCALE`: PostgreSQL + Redis streams/pub-sub.

## Security
- Use TLS for non-localhost Connect RPC endpoints.
- Enforce bearer token authentication and workspace-scoped authorization on RPC calls.
- Validate repository URLs, branch refs, prompts, and review payloads before execution.
- Keep provider-native raw payloads worker-local; never expose them in main-server APIs.
- Never log secrets, tokens, or plaintext sensitive material.
- Inject secrets only at runtime scope and clear ephemeral secret material after session termination.
- Desktop `LOCAL` mode resolution must avoid logging token values and must expose only normalized Connect RPC metadata to the UI.
- Tauri commands remain runtime adapters and must not become the primary business-data contract surface.

## Logging
- Use `tracing`-compatible structured logs in both server crates.
- Desktop Tauri backend must use `tracing` structured logs for mode resolution operations.
- Required correlation fields:
: `workspace_id`
: `unit_task_id`
: `sub_task_id`
: `session_id`
: `pr_tracking_id`
: `request_id`
- Main server baseline events:
: task/subtask/session state transitions
: PR poll snapshots and remediation decisions
: stream publish/replay health
: authorization deny outcomes (`result=denied`)
- Worker server baseline events:
: worktree create/cleanup
: session start/stop/failure
: normalization warnings and parser recoveries
: commit-chain generation summaries
: plan-mode wait/resume checkpoints
: cancellation checkpoints
- Desktop baseline events:
: workspace mode selection events
: local endpoint resolution success/failure
: normalized connection resolution success/failure
: downstream flow start checkpoints using normalized connection metadata
- Prohibited log content:
: raw provider tokens
: provider-native secret payloads
: plaintext secret material

## Build and Test
Current local validation commands:
- `cd protos/dexdex && buf lint`
- `cd protos/dexdex && buf build`
- `cd protos/dexdex && buf generate` (reproducible artifact output under `protos/dexdex/gen`)
- `cargo check -p dexdex-main-server`
- `cargo check -p dexdex-worker-server`
- `cargo test`
- `pnpm --filter dexdex test`

Acceptance-focused scenarios:
1. Main server accepts and validates Connect RPC task lifecycle requests.
2. Worker server executes SubTask flow with normalized session output emission.
3. Plan mode waits at decision boundary and resumes on `APPROVE`/`REVISE`.
4. Plan mode reject path finalizes SubTask without further execution.
5. PR remediation subtasks (`PR_REVIEW_FIX`, `PR_CI_FIX`) use the same normalized event contract.
6. Workspace stream replay resumes with exclusive cursor semantics (`sequence > from_sequence`).
7. Retention-expired replay cursor returns `OutOfRange` with `earliest_available_sequence`.
8. `SESSION_OUTPUT` payloads remain provider-agnostic at main server boundary.
9. `SINGLE_INSTANCE` mode runs without Redis dependency.
10. `SCALE` mode uses PostgreSQL + Redis-backed event propagation.
11. Desktop `LOCAL` mode resolves to normalized connection metadata through Tauri command contract.
12. Desktop `LOCAL` explicit override URL resolves `endpoint_source=LOCAL_OVERRIDE` without secret leakage in logs.
13. Desktop `REMOTE` mode resolves to the same normalized connection contract shape.
14. Desktop post-resolution UI and business flow behavior remains identical between `LOCAL` and `REMOTE` for the same endpoint.
15. SubTasks with code changes persist non-empty, ordered commit-chain metadata.
16. `SubmitPlanDecision(REVISE)` creates follow-up `REQUEST_CHANGES` SubTask.
17. `SubmitPlanDecision(REJECT)` creates no follow-up SubTask.
18. PR CI runs `pnpm --filter dexdex test` when Node-app scope changes.

CI workflow contracts:
- `.github/workflows/CI.yml`
: includes `node-dexdex-test` on PR/push node-scope changes.
- `.github/workflows/dexdex-desktop-build.yml`
: runs on `workflow_dispatch` and weekly schedule.
: executes `pnpm --filter dexdex tauri:build` across `ubuntu-latest`, `macos-latest`, `windows-latest`.

## Roadmap
- Phase 1: Finalize project contracts and Rust crate scaffolding for main and worker servers.
- Phase 2: Add proto definitions and Connect RPC handler skeletons for all listed services.
- Phase 3: Implement task orchestration, plan mode, PR polling, and stream replay.
- Phase 4: Add DexDex desktop app scaffold with normalized workspace mode resolution (`LOCAL`, `REMOTE`) and Tauri integration boundary.
- Phase 5: Add desktop CI coverage and packaging/signing automation without changing Connect RPC-first business contracts.

## Open Questions
- None in the current scaffold scope.
- Resolved in this phase:
: proto source of truth fixed at `protos/dexdex/v1/*.proto` with package `dexdex.v1`.
: desktop CI fixed to staged rollout (`CI.yml` quality checks + `dexdex-desktop-build.yml` scheduled/dispatch matrix).
: local runtime policy fixed to hybrid mode (`MANAGED_LOOPBACK` default, `LOCAL_OVERRIDE` with explicit override URL).
