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
- currently implemented envs: `DEXDEX_MAIN_SERVER_ADDR`, `DEXDEX_MAIN_STREAM_RETENTION`, `DEXDEX_MAIN_STREAM_HEARTBEAT_INTERVAL`, `DEXDEX_WORKER_SERVER_URL`
- planned profile/env extensions from upstream contract (for additive rollout): `DEXDEX_DEPLOYMENT_MODE`, `DEXDEX_DATABASE_URL`, `DEXDEX_REDIS_URL`, `DEXDEX_PR_POLL_INTERVAL_SEC`
- Implemented-vs-planned alignment:
- current implementation exposes a subset-focused proto surface with in-process store and stream replay retention controls
- expanded API behavior from upstream DexDex source docs and this session-fork/work-status extension is treated as target contract and must be rolled out additively

## Storage
- Owns control-plane records for workspace scope, task/subtask/session state snapshots, PR/review/notification metadata, and stream sequence progression.
- Owns session-lineage metadata (`parent_session_id`, `root_session_id`, `forked_from_sequence`, `fork_status`) and fork archival state.
- Owns active-workspace waiting-session index and workspace-work-status snapshots for shortcut/tray read models.
- Caches worker agent capability snapshots with bounded TTL and invalidation on worker capability refresh failures.
- Current implementation uses in-process retention-backed workspace store for stream and orchestration state.
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
- Integrates upstream with `apps/dexdex` client behavior through Connect RPC and event streaming.
- Integrates downstream with `servers/dexdex-worker-server` via worker session adapter RPC boundary.
- Consumes worker capability and fork-adapter contracts from `WorkerSessionAdapterService`.
- Integrates with PR providers and notification consumers through normalized control-plane entities.

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
- `servers/dexdex-main-server/internal/service/connect_server.go`
- Upstream source docs merged into this contract:
- `https://github.com/delinoio/dexdex/blob/main/docs/main-server.md`
- `https://github.com/delinoio/dexdex/blob/main/docs/event-streaming.md`
- `https://github.com/delinoio/dexdex/blob/main/docs/notifications.md`
- `https://github.com/delinoio/dexdex/blob/main/docs/pr-management.md`
- `https://github.com/delinoio/dexdex/blob/main/docs/developer-setup.md`
