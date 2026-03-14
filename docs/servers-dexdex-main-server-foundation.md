# servers-dexdex-main-server-foundation

## Scope
- Project/component: DexDex main server contract
- Canonical path: `servers/dexdex-main-server`
- Role: Connect RPC control-plane boundary for workspace, task, session, PR/review, notification, and event stream orchestration

## Runtime and Language
- Runtime: Go Connect RPC server
- Primary language: Go

## Users and Operators
- DexDex clients consuming control-plane APIs and stream updates
- Worker coordination paths routing session-adapter and task orchestration actions
- Operators maintaining service health, stream reliability, and remediation policy behavior

## Interfaces and Contracts
- Stable component identifier: `main-server`.
- Main server is the canonical business API boundary for clients.
- Client business flows (task/session/review/notification) are mediated through main-server and not direct worker-server business channels.
- Connect RPC contract surface includes workspace, repository, task, session, PR management, review assist, review comments, badge theme, notification, and event stream service boundaries.
- Control-plane responsibilities:
- workspace and repository-group lifecycle ownership
- UnitTask/SubTask orchestration state transitions
- plan-mode decision handling (`APPROVE`, `REVISE`, `REJECT`) and follow-up state transitions
- session fork orchestration (`create`, `list`, `switch`, `archive`) with parent-session immutability guarantees
- agent capability lookup/caching and fork-support validation before fork execution
- latest waiting-session index maintenance for question handoff flows
- active-workspace work-status aggregation for tray and workspace-summary surfaces
- PR polling, actionable signal detection, remediation trigger orchestration, and auto-fix guardrails
- notification trigger generation and workspace-scoped delivery metadata
- Session fork and input handoff contract:
- `SessionService.ListSessionCapabilities` exposes normalized per-agent capability view
- `SessionService.ForkSession` creates child session lineage and preserves parent state
- `SessionService.ListForkedSessions` and `SessionService.ArchiveForkedSession` manage fork lifecycle
- `SessionService.GetLatestWaitingSession` returns the newest `WAITING_FOR_INPUT` session for workspace-scoped shortcut routing
- `SessionService.SubmitSessionInput` records user response and resumes worker-session flow
- unsupported fork attempts must return `FAILED_PRECONDITION` with machine-readable capability reason
- Workspace work-status contract:
- `WorkspaceService.GetWorkspaceWorkStatus` returns active workspace status for tray/UI rendering
- status ordering semantics are `FAILED > ACTION_REQUIRED > WAITING_FOR_INPUT > RUNNING > IDLE > DISCONNECTED`
- work-status updates are emitted through workspace stream events
- Event stream contract:
- monotonic sequence per workspace
- replay and resume from sequence cursor with out-of-range detail handling
- live fan-out with heartbeat and bounded retention semantics
- session output payloads are normalized contracts (provider-native formats are not public API)
- additive event families include `SESSION_FORK_UPDATED` and `WORKSPACE_WORK_STATUS_UPDATED`
- PR management contract:
- tracked PR lifecycle and status normalization
- manual remediation trigger path and auto-fix policy enforcement
- retry budget, cooldown, blocked-state semantics, and explicit resume action expectations
- Inline comment contract:
- anchored review comments tied to diff coordinates
- status transitions with stream updates through inline-comment event family
- Deployment mode contract:
- `SINGLE_INSTANCE`: single-process event backbone, local DB option, replay bounded by local retention
- `SCALE`: Redis-backed propagation/replay and relational DB backbone for multi-instance deployment
- Configuration contract (normalized to current monorepo/runtime naming):
- runtime configuration is centrally parsed via `internal/config/config.go` using env vars
- implemented envs: `DEXDEX_MAIN_SERVER_ADDR`, `DEXDEX_MAIN_STREAM_RETENTION`, `DEXDEX_MAIN_STREAM_HEARTBEAT_INTERVAL`, `DEXDEX_WORKER_SERVER_URL`, `DEXDEX_DEPLOYMENT_MODE`, `DEXDEX_DATABASE_URL`, `DEXDEX_REDIS_URL`, `DEXDEX_PR_POLL_INTERVAL_SEC`
- Implemented-vs-planned alignment (as of 2026-03-14):
- current implementation includes full Connect RPC handlers for WorkspaceService (GetWorkspace, ListWorkspaces, GetWorkspaceWorkStatus with priority-based status computation), TaskService (CRUD + SubmitPlanDecision with FanOut event publishing), SessionService (GetSessionOutput, ListSessionCapabilities, ForkSession, ListForkedSessions, ArchiveForkedSession, GetLatestWaitingSession, SubmitSessionInput), NotificationService (ListNotifications, MarkNotificationRead with stream event publishing), EventStreamService (streaming with fan-out, replay, heartbeat, SESSION_FORK_UPDATED and WORKSPACE_WORK_STATUS_UPDATED events), RepositoryService (GetRepositoryGroup, ListRepositoryGroups), PrManagementService (GetPullRequest, ListPullRequests, UpdatePullRequest), ReviewCommentService (ListReviewComments, CreateReviewComment, UpdateReviewComment, DeleteReviewComment, ResolveReviewComment, ReopenReviewComment), and BadgeThemeService (GetBadgeTheme handler with badge theme store methods)
- EventBroadcaster interface with Redis-backed `RedisFanOut` implementation for scale-mode deployment is implemented
- worker client with agent capability caching (5-minute TTL) is implemented
- worker dispatch via `internal/worker/dispatch.go` is implemented (Dispatcher manages execution goroutines, dispatches to worker via StartExecution streaming RPC, consumes events, publishes through FanOut)
- PR polling via `internal/polling/pr_poller.go` is implemented (polls GitHub via `gh api`, detects status changes, creates notifications)
- `UpdatePullRequest` is added to Store interface
- session summary store for fork orchestration and lineage tracking is implemented in memory
- in-memory store with session output storage and rich seed data
- PostgreSQL persistence layer via sqlc with conditional store selection (`DEXDEX_DATABASE_URL`) is implemented
- worktree orchestration via `internal/worker/worktree_coordinator.go` is implemented (periodic stale cleanup, WorktreeAssignment tracking from worker-emitted WorktreeStatusEvent lifecycle events)
- dispatcher consumes WorktreeStatusEvent and upserts WorktreeAssignment state in store
- `DispatchForkExecution` dispatches fork execution to worker with parent_session_id and fork_intent fields; consumes execution stream and publishes session/fork events
- `ForkSession` handler now triggers actual execution dispatch after metadata creation
- `FindSubTaskBySessionID` added to Store interface for fork-to-subtask relationship resolution
- expanded API behavior from upstream DexDex source docs remains target contract for further additive evolution

## Storage
- Target runtime owns control-plane records for workspace scope, task/subtask/session state snapshots, PR/review/notification metadata, and stream sequence progression.
- Target runtime owns session-lineage metadata (`parent_session_id`, `root_session_id`, `forked_from_sequence`, `fork_status`) and fork archival state.
- Target runtime owns active-workspace waiting-session index and workspace-work-status snapshots for shortcut/tray read models.
- Target runtime caches worker agent capability snapshots with bounded TTL and invalidation on worker capability refresh failures.
- Current scaffold implementation is stateless and does not persist orchestration or stream state.
- Target deployment contract supports durable relational storage and Redis-backed stream propagation in scale mode.
- Retention and replay boundaries must be explicit and observable in operational behavior.

## Security
- Every RPC call is workspace-scoped and must enforce authorization semantics appropriate to workspace type/deployment profile.
- Non-localhost remote endpoints require token-authenticated Connect RPC transport and secure configuration handling.
- Sensitive payload fields and credentials must be redacted from logs and stream bodies.
- Worker coordination endpoints must validate request context and reject cross-workspace task/session misuse.

## Logging
- Use structured `log/slog` logging for request handling, orchestration transitions, stream replay behavior, worker routing outcomes, and remediation decisions.
- Required correlation fields include workspace/task/subtask/session/request identifiers and PR tracking identifiers where relevant.
- Emit diagnostics for replay out-of-range, heartbeat send failures, and worker adapter call failures.
- Notification and PR polling decision reasons must be auditable in server logs.
- Fork-capability checks, unsupported-fork rejections (`FAILED_PRECONDITION`), and session-lineage writes must be auditable in structured logs.
- Waiting-session index updates and workspace-work-status recomputation outcomes must be logged with active workspace context.

## Build and Test
- Component-local validation: `go test ./servers/dexdex-main-server/...`
- Repository baseline: `go test ./...`
- Contract-sensitive tests should cover plan decisions, stream replay ordering/idempotency, worker adapter integration, and error-code semantics.
- Contract-sensitive tests should cover fork parent immutability, unsupported-fork `FAILED_PRECONDITION`, waiting-session lookup ordering, and workspace-work-status stream emission.

## Dependencies and Integrations
- Depends on shared schema contracts in `protos/dexdex/v1`.
- Current implementation exposes live Connect RPC handlers to app, has a worker client for capability caching, worker dispatch via Dispatcher for StartExecution streaming, and PR polling via GitHub CLI integration.
- Target runtime integrates upstream with `apps/dexdex` client behavior through Connect RPC and event streaming.
- Target runtime integrates downstream with `servers/dexdex-worker-server` via worker session adapter RPC boundary.
- Target runtime consumes worker capability and fork-adapter contracts from `WorkerSessionAdapterService`.
- Target runtime integrates with PR providers and notification consumers through normalized control-plane entities.

## Change Triggers
- Update this file with `docs/project-dexdex.md` when control-plane responsibilities or deployment-mode guarantees change.
- Synchronize updates with `docs/protos-dexdex-v1-contract.md` for any service/message/enum or error mapping changes.
- Update `docs/apps-dexdex-desktop-app-foundation.md` when client-facing behavior for stream, notifications, plan decisions, or PR remediation changes.
- Update `docs/servers-dexdex-worker-server-foundation.md` when worker-routing boundaries or normalized session contracts change.
- Synchronize with `servers/AGENTS.md` when session-fork capability policy or workspace-work-status aggregation policy changes.

## References
- `docs/project-dexdex.md`
- `docs/protos-dexdex-v1-contract.md`
- `docs/servers-dexdex-worker-server-foundation.md`
- `docs/apps-dexdex-desktop-app-foundation.md`
- `docs/domain-template.md`
- Implementation anchors:
- `servers/dexdex-main-server/main.go`
- `servers/dexdex-main-server/internal/service/plan_decision.go`
- `servers/dexdex-main-server/internal/service/stream_replay.go`
- Upstream source docs merged into this contract:
- `https://github.com/delinoio/dexdex/blob/main/docs/main-server.md`
- `https://github.com/delinoio/dexdex/blob/main/docs/event-streaming.md`
- `https://github.com/delinoio/dexdex/blob/main/docs/notifications.md`
- `https://github.com/delinoio/dexdex/blob/main/docs/pr-management.md`
- `https://github.com/delinoio/dexdex/blob/main/docs/developer-setup.md`
