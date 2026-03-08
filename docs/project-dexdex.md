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
: It serves `WorkspaceService` (`GetWorkspace`, `GetWorkspaceOverview`), `RepositoryService` (`GetRepositoryGroup`, `ListRepositoryGroups`), `TaskService` (`GetUnitTask`, `GetSubTask`, `ListUnitTasks`, `ListSubTasks`, `SubmitPlanDecision`, `RunSubTaskSessionAdapter`), `SessionService` (`GetSessionOutput`, `ListSessions`), `PrManagementService` (`GetPullRequest`, `ListPullRequests`), `ReviewAssistService.ListReviewAssistItems`, `ReviewCommentService.ListReviewComments`, `BadgeThemeService.GetBadgeTheme`, `NotificationService.ListNotifications`, and `EventStreamService.StreamWorkspaceEvents` over Connect RPC.
: It keeps workspace task/subtask/event state in memory and starts with an empty workspace set.
: It provides filtered list/index read models with page-size + page-token pagination for product UI list surfaces.
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
: It starts at a workspace picker (`/`) and requires workspace selection before entering desktop role routes.
: It resolves workspace mode into one normalized Connect RPC connection contract during picker open.
: It renders a three-panel desktop information architecture (left navigation, center page container, right action center) with dark-first styling.
: It exposes route-scoped multi-page workflows aligned with Codex desktop roles (`/projects`, `/threads`, `/review`, `/automations`, `/worktrees`, `/local-environments`, `/settings`).
: It guards desktop role routes when no active workspace session is selected and redirects to startup picker.
: It provides a shared React Query + Connect Query transport scaffold for RPC data flows.
: It replaces RPC card dashboards with product-first page containers:
: `Projects` -> workspace overview + repository groups + active task summary.
: `Threads` -> inbox + detail timeline + action center.
: `Review` -> PR queue + review assist/comment context.
: `Worktrees` -> session list + stream timeline with incremental cache updates.
: It provides local-store backed usable pages for `Automations`, `Local Environments`, and `Settings` (create/update/delete/toggle + last-selected restore + diagnostics history).
: It surfaces right-panel action context for plan decision/session adapter execution based on current shared selection state.
: It applies resolved workspace token values as `Authorization: Bearer <token>` request headers when token is present.
: It binds selected `workspace_id` globally and shares selected IDs (`selected_unit_task_id`, `selected_sub_task_id`, `selected_session_id`, `selected_pr_tracking_id`) across page containers and action center.
: It stores workspace profile metadata locally (without token persistence) and keeps active session state in memory.
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
- Saved workspace profile metadata is persisted in local storage (`workspaceId`, `mode`, optional `remoteEndpointUrl`, `lastUsedAt`)
- Active workspace session (`workspaceId` + `ResolvedWorkspaceConnection`) remains in-memory only
- Remote token values are never persisted and are entered per open action

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
- `pnpm --filter dexdex run test:visual`
- `cd apps/dexdex && pnpm test`
- `cd apps/dexdex && pnpm run test:visual`
- Visual baseline references (phase 1):
: `https://developers.openai.com/codex/overview`
: `https://developers.openai.com/codex/features`
: `https://developers.openai.com/codex/review-comments`
: `https://developers.openai.com/codex/projects`
: `https://developers.openai.com/codex/local-environments`
: `https://developers.openai.com/codex/settings`
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
20. `GetWorkspaceOverview`, `ListRepositoryGroups`, `ListUnitTasks`, `ListSubTasks`, `ListSessions`, and `ListPullRequests` require `workspace_id` and return `items + next_page_token` envelopes.
21. New `List*` methods apply enum-based filters (`status`, `cli_type`) and deterministic page-size/page-token pagination.
22. `List*` methods return empty `items` with empty `next_page_token` for existing workspaces with no matching data.
23. Worker `NormalizeSessionOutputFixture` accepts fixture presets and raw JSONL, then returns normalized `SessionOutputEvent[]` with a derived `session_status`.
24. Main `RunSubTaskSessionAdapter` rejects missing input oneof and `unit_task_id`/`sub_task_id` ownership mismatches with typed Connect errors.
25. Main `RunSubTaskSessionAdapter` persists session output under `session_id` and returns the updated SubTask state.
26. Main stream emits session adapter events in ordered sequence (`SUBTASK_UPDATED` -> `SESSION_OUTPUT` -> `SESSION_STATE_CHANGED` -> final `SUBTASK_UPDATED` when status terminal).
27. Desktop startup always renders workspace picker at `/`, and desktop navigation exposes seven Codex-role pages after workspace selection.
28. Desktop route guard redirects `/projects`, `/threads`, `/review`, `/automations`, `/worktrees`, `/local-environments`, and `/settings` to `/` when no active workspace session exists.
29. Desktop `Threads` route provides inbox + detail timeline, and shared selection state drives Action Center context.
30. Desktop `Threads` flow supports selecting a task/subtask/session, then executing `SubmitPlanDecision` and `RunSubTaskSessionAdapter` from Action Center in one continuous workflow.
31. Desktop `Worktrees` route merges session list and event timeline, and stream updates incrementally refresh React Query caches.
32. Desktop `Projects` route renders workspace overview, repository groups, and active task summaries from read APIs.
33. Desktop `Review` route renders PR queue with review assist/comments and propagates selected PR context to Action Center.
34. Desktop `Automations`, `Local Environments`, and `Settings` routes provide real local read/write UX (create/update/delete/toggle, diagnostics history, last-selected restore).
35. Desktop Connect transport sets `Authorization: Bearer <token>` only when a resolved token exists.
36. Desktop workspace profile persistence excludes remote token values from local storage payloads.
37. Desktop UI exposes no `RPC Dashboard` surface or dependency in route containers.

## Roadmap
- Phase 1: Shared proto contract scaffold (`dexdex.v1`) and desktop connection normalization.
- Phase 2: Go main/worker server domain-logic scaffolds with parity to prior Rust task/commit validation behavior.
- Phase 3: Task/stream and unary read Connect handler implementation (current), with persistence still pending.
- Phase 4: Orchestration runtime integrations (worktree lifecycle, session adapters, PR polling); session adapter vertical slice is implemented in the current scaffold.
- Phase 5: Scale-mode deployment support with production storage/event-broker backends.

## Open Questions
- None in the current scaffold scope.
