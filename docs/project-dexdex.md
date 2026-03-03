# Project: dexdex

## Goal
`dexdex` is a Connect RPC-first task orchestration platform that coordinates UnitTask/SubTask lifecycle decisions, worker execution, PR/review synchronization, and workspace event streaming for desktop operations.

## Path
- Main server: `servers/dexdex-main-server`
- Worker server: `servers/dexdex-worker-server`
- Desktop app: `apps/dexdex`
- Desktop frontend: `apps/dexdex/src`
- Desktop Tauri backend: `apps/dexdex/src-tauri`
- Shared proto contracts: `protos/dexdex/v1/dexdex.proto`

## Runtime and Language
- Main server: Go
- Worker server: Go
- Desktop app frontend: React + TypeScript (Vite)
- Desktop app backend: Rust (Tauri)
- Shared RPC contract: Protocol Buffers (`dexdex.v1`) + Connect RPC

## Users
- Developers running AI-assisted implementation workflows
- Reviewers handling PR feedback and remediation loops
- Operators monitoring task/session execution and event delivery health

## In Scope
- Connect RPC contract ownership for workspace/repository/task/session/pr/review/notification/stream/worker execution
- Main server control-plane task/subtask orchestration and plan-decision transitions
- Worker server execution-plane codex session execution and commit-chain validation
- Deployment-mode-specific runtime behavior:
  - `SINGLE_INSTANCE`: SQLite + in-process broker
  - `SCALE`: PostgreSQL + Redis Streams broker
- Desktop operator console actions for orchestration APIs after workspace connection resolution

## Out of Scope
- Full production-grade distributed scheduler (current implementation is service-local with retries)
- Persistent desktop credential vault behavior
- Multi-provider PR integrations beyond GitHub CLI adapter in current implementation

## Architecture
- Main server (`servers/dexdex-main-server`)
  - Connect handlers for Workspace/Repository/Task/Session/PR/Review/Notification/EventStream services
  - SQL-backed task/session/event persistence
  - Broker abstraction with in-process and Redis implementations
  - `log/slog` structured logging and bearer auth middleware
- Worker server (`servers/dexdex-worker-server`)
  - Connect `ExecutionService` handlers
  - `codex exec --json` runner with retry/backoff session execution
  - Commit-chain validation using typed validation errors
  - `log/slog` structured logging and bearer auth middleware
- Desktop app (`apps/dexdex`)
  - Local/remote workspace connection normalization
  - Operator console for task/subtask/decision/pr/session/notification API operations
- Shared proto (`protos/dexdex/v1/dexdex.proto`)
  - Canonical contract surface for all server/client integrations

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

Desktop normalized connection contract:

```ts
type ResolvedWorkspaceConnection = {
  mode: WorkspaceMode;
  endpointUrl: string;
  endpointSource: "MANAGED_LOOPBACK" | "LOCAL_OVERRIDE" | "USER_REMOTE";
  token?: string;
  transport: "CONNECT_RPC";
};
```

### Proto Contract Surface (`dexdex.v1`)
Primary services:
- `WorkspaceService.GetWorkspace`
- `RepositoryService.GetRepositoryGroup`
- `TaskService.CreateUnitTask`
- `TaskService.StartSubTask`
- `TaskService.RetrySubTask`
- `TaskService.GetUnitTask`
- `TaskService.ListUnitTasks`
- `TaskService.GetSubTask`
- `TaskService.ListSubTasks`
- `TaskService.SubmitPlanDecision`
- `SessionService.GetSessionOutput`
- `SessionService.StreamSessionOutput`
- `PrManagementService.GetPullRequest`
- `ReviewAssistService.ListReviewAssistItems`
- `ReviewCommentService.ListReviewComments`
- `BadgeThemeService.GetBadgeTheme`
- `NotificationService.ListNotifications`
- `EventStreamService.StreamWorkspaceEvents`
- `ExecutionService.ExecuteSubTask`
- `ExecutionService.ValidateCommitChain`

Pagination contract:
- `ListUnitTasksRequest.page_size/page_token` and `ListUnitTasksResponse.next_page_token`
- `ListSubTasksRequest.page_size/page_token` and `ListSubTasksResponse.next_page_token`

Typed detail contracts:
- `PlanDecisionValidationDetail` for plan-decision validation failures
- `EventStreamCursorOutOfRangeDetail` for replay cursor out-of-range responses

Plan decision semantics:
- Decision target is identified by `sub_task_id`.
- `APPROVE`: resumes same SubTask (`WAITING_FOR_PLAN_APPROVAL` -> `IN_PROGRESS`).
- `REVISE`: requires non-empty `revision_note`, marks current SubTask `REVISED`, creates queued `REQUEST_CHANGES` SubTask.
- `REJECT`: marks current SubTask `PLAN_REJECTED`, creates no follow-up SubTask.

Workspace stream semantics:
- `from_sequence` is exclusive (`sequence > from_sequence`).
- Sequence is workspace-scoped, monotonic, and starts at `1`.
- Out-of-retention cursor returns `OutOfRange` with `earliest_available_sequence`.

## Storage
Main server:
- `SINGLE_INSTANCE`: SQLite (`DEXDEX_SQLITE_PATH`)
- `SCALE`: PostgreSQL (`DEXDEX_POSTGRES_DSN`)
- Persisted tables:
  - `unit_tasks`
  - `sub_tasks`
  - `workspace_events`
  - `session_outputs`

Broker:
- `SINGLE_INSTANCE`: in-process subscriber fan-out + persisted replay from `workspace_events`
- `SCALE`: Redis Streams publish/subscription (`DEXDEX_REDIS_ADDR`, `DEXDEX_REDIS_STREAM_PREFIX`) + persisted replay from `workspace_events`

Worker:
- Session execution state is in-memory for current runtime process.

Desktop:
- Workspace mode and resolved connection state are in-memory.

## Runtime Configuration
Main server:
- `DEXDEX_DEPLOYMENT_MODE` (`SINGLE_INSTANCE` | `SCALE`)
- `DEXDEX_MAIN_ADDR` (default `127.0.0.1:7878`)
- `DEXDEX_WORKER_ADDR` (default `http://127.0.0.1:7879`)
- `DEXDEX_SQLITE_PATH` (single mode)
- `DEXDEX_POSTGRES_DSN` (required in scale mode)
- `DEXDEX_REDIS_ADDR` (required in scale mode)
- `DEXDEX_REDIS_STREAM_PREFIX` (default `dexdex:events`)
- `DEXDEX_AUTH_TOKEN` (optional bearer token)

Worker server:
- `DEXDEX_WORKER_ADDR` (default `127.0.0.1:7879`)
- `DEXDEX_CODEX_BIN` (default `codex`)
- `DEXDEX_CODEX_PROFILE` (optional)
- `DEXDEX_WORKER_MAX_RETRY` (default `3`)
- `DEXDEX_WORKER_RETRY_BACKOFF_MS` (default `600`)
- `DEXDEX_AUTH_TOKEN` (optional bearer token)

Desktop frontend:
- `VITE_DEXDEX_MAIN_ADDR` (optional default remote endpoint)

## Security
- Main and worker servers support bearer authentication via `Authorization: Bearer <token>` when `DEXDEX_AUTH_TOKEN` is configured.
- Never log raw tokens or secret payloads.
- Desktop endpoint/token resolution keeps token visibility minimized in UI summaries and logs.

## Logging
Main and worker server logs use `log/slog` structured events.
Required correlation fields in operation logs:
- `workspace_id`
- `unit_task_id`
- `sub_task_id`
- `session_id`
- `pr_tracking_id`
- `request_id`

Current baseline events:
- server start/stop
- auth denied reasons
- execution retry attempts
- commit-chain validation failures
- broker stream errors

## Build and Test
Validation commands:
- `cd protos/dexdex && buf lint`
- `cd protos/dexdex && buf build`
- `./scripts/generate-go-proto.sh`
- `go test ./servers/dexdex-main-server/...`
- `go test ./servers/dexdex-worker-server/...`
- `go test ./...`
- `pnpm install --frozen-lockfile`
- `pnpm --filter dexdex test`

## Acceptance Scenarios
1. `CreateUnitTask` stores and lists tasks with pagination token handling.
2. `StartSubTask` produces `WAITING_FOR_PLAN_APPROVAL` subtasks.
3. `SubmitPlanDecision` enforces APPROVE/REVISE/REJECT transition contracts and typed validation details.
4. `StreamWorkspaceEvents` enforces exclusive replay and out-of-range typed detail behavior.
5. `ExecutionService.ExecuteSubTask` runs `codex exec --json` with retry/backoff and returns final session status.
6. `ExecutionService.ValidateCommitChain` accepts ordered chains and rejects malformed chains.
7. Desktop operator console can create/list tasks, start/list subtasks, submit plan decisions, and call PR/review/session endpoints after connection resolution.

## Roadmap
- Phase 3: Connect handlers + dual-mode persistence + broker abstraction (implemented baseline)
- Phase 4: orchestration runtime integration hardening (worktree lifecycle/session adapters/reporting enrichment)
- Phase 5: scale-mode production hardening (multi-node coordination, stronger durability and observability)

## Open Questions
- Worker-to-main session output synchronization is currently API-level and can be extended to broker-driven push replication for higher throughput.
- Redis stream retention and dead-letter policy are not yet configurable at per-workspace granularity.
