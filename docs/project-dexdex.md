# Project: dexdex

## Goal
`dexdex` is a Connect RPC-first task orchestration platform that coordinates UnitTask/SubTask execution, plan approval decisions, commit-chain outputs, and workspace event streaming.
The project exposes a shared protobuf contract (`dexdex.v1`) for multi-runtime integrations while keeping desktop behavior normalized across local and remote endpoint resolution.

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
- Connect RPC-first business contracts for workspace, repository, task, session, PR, review, notification, and stream flows
- Main server control-plane ownership of task/subtask lifecycle decision logic
- Worker server execution-plane ownership of ordered real commit-chain validation
- Worker server event-level normalization of Codex CLI, Claude Code, and OpenCode session outputs
- Plan-mode decision transitions (`APPROVE`, `REVISE`, `REJECT`) at SubTask scope
- Workspace event streaming with replay/resume semantics (`from_sequence` exclusive)
- Desktop workspace mode resolution (`LOCAL`, `REMOTE`) with normalized connection metadata
- DexDex desktop v1 support for Codex CLI, Claude Code, and OpenCode integrations

## Out of Scope
- Tauri-specific bindings as primary business-data contracts
- Patch-only authoritative change outputs without real git commit metadata
- Provider-native raw session payload contracts in main server APIs and client-facing streams
- Full production persistence and distributed orchestration in this phase
- Persistent desktop token vault behavior in this phase

## Architecture
- Main server (`servers/dexdex-main-server`) is the control-plane Go service scaffold
: It serves `WorkspaceService.GetWorkspace`, `RepositoryService.GetRepositoryGroup`, `TaskService` (`GetUnitTask`, `GetSubTask`, `SubmitPlanDecision`, `RunSubTaskSessionAdapter`), `SessionService.GetSessionOutput`, `PrManagementService.GetPullRequest`, `ReviewAssistService.ListReviewAssistItems`, `ReviewCommentService.ListReviewComments`, `BadgeThemeService.GetBadgeTheme`, `NotificationService.ListNotifications`, and `EventStreamService.StreamWorkspaceEvents` over Connect RPC.
: It keeps workspace task/subtask/event state in memory and starts with an empty workspace set.
: It provides replay + live-tail stream delivery with retention validation and keepalive heartbeat frames.
: It orchestrates worker-driven session adapter normalization and materializes session/subtask stream events in order.
: It uses structured logs via `log/slog`.
- Worker server (`servers/dexdex-worker-server`) is the execution-plane Go service scaffold
: It serves `WorkerSessionAdapterService.NormalizeSessionOutputFixture` over Connect RPC.
: It validates ordered real commit-chain metadata emitted by SubTask execution.
: It normalizes provider-native CLI output streams into one session-output contract.
: It supports fixture presets and raw JSONL input for deterministic session adapter execution.
: It uses structured logs via `log/slog`.
- Desktop app (`apps/dexdex`) is the orchestration client shell
: It resolves workspace mode into one normalized Connect RPC connection contract.
: It renders a three-panel desktop information architecture (left navigation, center RPC workspace, right inspector) with dark-first styling.
: It provides a shared React Query + Connect Query transport scaffold for RPC data flows.
: It renders section-scoped Connect RPC dashboard workflows with unary lookups, plan-decision controls, session-adapter execution controls, and live workspace stream monitoring.
: It surfaces right-panel inspector diagnostics for lookup history, last action status, and stream state.
: It applies resolved workspace token values as `Authorization: Bearer <token>` request headers when token is present.
: Post-resolution behavior stays identical between `LOCAL` and `REMOTE` modes.
- Shared proto (`protos/dexdex/v1/dexdex.proto`) is the canonical contract surface for cross-runtime integrations.

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

Desktop Connect Query scaffold contract:

```ts
type DexDexConnectQueryRuntime = {
  queryClient: "SINGLETON_REACT_QUERY_CLIENT";
  transportProvider: "@connectrpc/connect-query";
  transportFactory: "(endpointUrl: string) => ConnectTransport";
  authInterceptor: "(token?: string) => AuthorizationBearerHeader";
};
```

Desktop dashboard section identifiers:

```ts
enum DashboardSectionId {
  Workspace = "workspace",
  Repository = "repository",
  Tasks = "tasks",
  Sessions = "sessions",
  Review = "review",
  BadgeTheme = "badge-theme",
  Notifications = "notifications",
  SessionAdapter = "session-adapter",
  EventStream = "event-stream",
}
```

Desktop inspector state contract:

```ts
type DashboardInspectorState = {
  history: {
    workspaceId: string[];
    repositoryGroupId: string[];
    unitTaskId: string[];
    subTaskId: string[];
    sessionId: string[];
    prTrackingId: string[];
  };
  lastActionLabel: string;
  lastActionStatus: "idle" | "pending" | "success" | "error";
  lastActionMessage: string;
  streamStatus: "idle" | "running" | "stopped" | "error";
  streamEventCount: number;
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
- `RepositoryService.GetRepositoryGroup`
- `TaskService.GetUnitTask`
- `TaskService.GetSubTask`
- `TaskService.SubmitPlanDecision`
- `TaskService.RunSubTaskSessionAdapter`
- `SessionService.GetSessionOutput`
- `PrManagementService.GetPullRequest`
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

## Storage
Main server scaffold ownership:
- In-memory task/subtask maps per workspace with empty-on-boot default state
- In-memory workspace/repository/session/pr/review/badge/notification read-model maps per workspace
- In-memory workspace event ring buffer with configurable retention
- In-memory live subscriber registry per workspace
- Non-blocking subscriber fan-out with explicit drop policy when subscriber buffers are full
- Empty workspace entries created for stream-only sessions are garbage-collected when the last subscriber disconnects

Worker server scaffold ownership:
- In-memory commit-chain validation logic (`sha`, parent links, message, timestamp ordering)
- In-memory session-output normalization logic and fixture-backed parser validation

Desktop scaffold storage contract:
- Workspace mode selection and resolved connection state are in-memory only in this phase

Future deployment mode storage contract (reserved):
- `SINGLE_INSTANCE`: SQLite + in-process event broker
- `SCALE`: PostgreSQL + Redis streams/pub-sub

## Security
- Use TLS for non-localhost Connect RPC endpoints.
- Enforce bearer token authentication and workspace-scoped authorization in full server implementations.
- Validate repository URLs, branch refs, prompts, and review payloads before execution.
- Keep provider-native raw payloads worker-local; never expose them in main-server APIs.
- Never log secrets, tokens, or plaintext sensitive material.
- Desktop `LOCAL` mode resolution must avoid token value logging and expose normalized Connect metadata only.

## Logging
- Main and worker Go server scaffolds use `log/slog` structured logging.
- Required correlation fields for full runtime implementations:
: `workspace_id`
: `unit_task_id`
: `sub_task_id`
: `session_id`
: `pr_tracking_id`
: `request_id`
- Baseline scaffold events:
: server scaffold start (`component`, `result`)
: plan decision/replay validation failures with typed error codes
: stream open/close transitions and heartbeat send failures
: subscriber backpressure drops with fixed `policy=drop`
: commit-chain validation failures with typed error codes
- Prohibited log content:
: raw provider tokens
: provider-native secret payloads
: plaintext secret material

## Build and Test
Current local validation commands:
- `cd protos/dexdex && buf lint`
- `cd protos/dexdex && buf build`
- `./scripts/generate-go-proto.sh`
- `pnpm --filter dexdex run gen:proto`
- `go test ./servers/dexdex-main-server/...`
- `go test ./servers/dexdex-worker-server/...`
- `go test ./...`
- `cargo test`
- `pnpm --filter dexdex test`
- `cd apps/dexdex && pnpm test`
- Distribution pipeline:
: `.github/workflows/release-dexdex.yml`
: tag trigger: `dexdex@v*`
: `workflow_dispatch` supports `version` and `dry_run`
- Release artifact contract:
: Desktop: `dexdex-desktop-linux-amd64.AppImage`, `dexdex-desktop-darwin-universal.dmg`, `dexdex-desktop-windows-amd64.msi`
: Main server: `dexdex-main-server-{linux|darwin|windows}-{amd64|arm64}.(tar.gz|zip)`
: Worker server: `dexdex-worker-server-{linux|darwin|windows}-{amd64|arm64}.(tar.gz|zip)`
: Integrity/signature set: `SHA256SUMS` + per-artifact cosign signatures (`*.sig`, `*.pem`)
- Package-manager publication integration:
: Homebrew updates via `scripts/release/update-homebrew.sh` (`dexdex`, `dexdex-main-server`, `dexdex-worker-server`)
: winget updates via `scripts/release/update-winget.sh` (`DelinoIO.DexDex`, `DelinoIO.DexDexMainServer`, `DelinoIO.DexDexWorkerServer`)
- Desktop signing/notarization contract:
: macOS signing/notarization uses GitHub Actions secrets (`DEXDEX_APPLE_CERTIFICATE_BASE64`, `DEXDEX_APPLE_CERTIFICATE_PASSWORD`, `DEXDEX_APPLE_SIGNING_IDENTITY`, `DEXDEX_APPLE_ID`, `DEXDEX_APPLE_PASSWORD`, `DEXDEX_APPLE_TEAM_ID`)
: Windows signing uses GitHub Actions secrets (`DEXDEX_WINDOWS_CERTIFICATE_BASE64`, `DEXDEX_WINDOWS_CERTIFICATE_PASSWORD`)

Main server runtime configuration:
- `DEXDEX_MAIN_SERVER_ADDR` (default: `127.0.0.1:7878`)
- `DEXDEX_MAIN_STREAM_RETENTION` (default: `256`)
- `DEXDEX_MAIN_STREAM_HEARTBEAT_INTERVAL` (default: `15s`, Go duration format)
- `DEXDEX_WORKER_SERVER_URL` (default: `http://127.0.0.1:7879`)

Worker server runtime configuration:
- `DEXDEX_WORKER_SERVER_ADDR` (default: `127.0.0.1:7879`)

Acceptance-focused scenarios:
1. Approve decision resumes current SubTask from waiting-plan state.
2. Revise decision requires non-empty revision note and creates queued request-changes SubTask.
3. Revise decision server-generates a new SubTask ID with deterministic prefix `<workspace_id>-subtask-`.
4. Reject decision cancels current SubTask and creates no follow-up SubTask.
5. Replay uses exclusive cursor semantics (`sequence > from_sequence`).
6. Replay rejects non-monotonic sequence streams.
7. Replay reports cursor-out-of-range with earliest available sequence details.
8. Live tail receives newly published SubTask update events after replay completion.
9. Stream subscriber lifecycle is cleaned up on client-side cancellation.
10. Backpressure policy drops events for saturated subscriber buffers without blocking publishers.
11. Worker accepts ordered real commit chains with valid parent linkage.
12. Worker rejects empty chains, missing parent links, and non-monotonic commit time.
13. Desktop workspace resolution continues to return normalized `CONNECT_RPC` connection metadata.
14. Worker normalizes Codex CLI `turn.failed` events as terminal session output errors.
15. Worker normalizes Claude Code stream deltas and final assistant text into distinct event types.
16. Worker preserves OpenCode `step_start` -> `text` -> `step_finish` event ordering.
17. Worker converts malformed JSON source lines into non-terminal parse-error output events.
18. Main server unary handlers return `NotFound` for unknown workspace/resource IDs and `InvalidArgument` for missing required fields.
19. `GetSessionOutput`, `ListReviewAssistItems`, `ListReviewComments`, and `ListNotifications` return empty arrays when workspace exists but no records are present.
20. Desktop dashboard can query all unary RPC methods after connection resolution without changing workspace mode-specific UX flow.
21. Desktop lookup histories are in-memory, deduped, recency-ordered, and capped at five entries per lookup key.
22. Desktop Connect transport sets `Authorization: Bearer <token>` only when a resolved token exists.
23. Worker `NormalizeSessionOutputFixture` accepts fixture presets and raw JSONL, then returns normalized `SessionOutputEvent[]` with a derived `session_status`.
24. Main `RunSubTaskSessionAdapter` rejects missing input oneof and `unit_task_id`/`sub_task_id` ownership mismatches with typed Connect errors.
25. Main `RunSubTaskSessionAdapter` persists session output under `session_id` and returns the updated SubTask state.
26. Main stream emits session adapter events in ordered sequence (`SUBTASK_UPDATED` -> `SESSION_OUTPUT` -> `SESSION_STATE_CHANGED` -> final `SUBTASK_UPDATED` when status terminal).
27. Desktop dashboard can run session adapter requests with preset/raw input and render live stream events while ignoring heartbeat frames.
28. Desktop dashboard can submit `APPROVE`, `REVISE`, and `REJECT` plan decisions, requiring `revision_note` only for `REVISE`, while preserving the same post-resolution UX flow across workspace modes.

## Roadmap
- Phase 1: Shared proto contract scaffold (`dexdex.v1`) and desktop connection normalization.
- Phase 2: Go main/worker server domain-logic scaffolds with parity to prior Rust task/commit validation behavior.
- Phase 3: Task/stream and unary read Connect handler implementation (current), with persistence still pending.
- Phase 4: Orchestration runtime integrations (worktree lifecycle, session adapters, PR polling); session adapter vertical slice is implemented in the current scaffold.
- Phase 5: Scale-mode deployment support with production storage/event-broker backends.

## Open Questions
- None in the current scaffold scope.
