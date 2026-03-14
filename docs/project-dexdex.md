# Project: dexdex

## Goal
Define DexDex as a Connect RPC-first, multi-component orchestration platform contract for desktop-first and mobile-ready workflows, while documenting both implemented and planned capabilities in one canonical project index.

## Project ID
`dexdex`

## Domain Ownership Map
- `apps/dexdex` (`desktop-app`)
- `servers/dexdex-main-server` (`main-server`)
- `servers/dexdex-worker-server` (`worker-server`)
- `protos/dexdex/v1` (`v1` shared contracts)

## Domain Contract Documents
- `docs/apps-dexdex-desktop-app-foundation.md`
- `docs/servers-dexdex-main-server-foundation.md`
- `docs/servers-dexdex-worker-server-foundation.md`
- `docs/protos-dexdex-v1-contract.md`

## Cross-Domain Invariants
- Connect RPC is the canonical business contract boundary across all DexDex components.
- Desktop/mobile clients use `main-server` APIs for business flows and must not call worker business internals directly.
- Tauri-specific bindings are adapters for platform capabilities only and do not define business contracts.
- Workspace connectivity is a first-class concept with stable enum-style types: `LOCAL_ENDPOINT`, `REMOTE_ENDPOINT`.
- Task execution is worktree-only and repository-group scoped.
- Repository order in a repository group is deterministic; the first repository is the primary execution directory.
- Multi-repository execution attaches non-primary repositories via `--add-dir` (or agent-equivalent options) while preserving order.
- Real git commit chain metadata is the authoritative output artifact for PR creation and commit-local flows.
- Patch artifacts are derived artifacts for diff rendering and are not authoritative execution output.
- Plan mode uses explicit decisions (`APPROVE`, `REVISE`, `REJECT`) and must preserve decision history linkage to subtask/session records.
- Session fork is additive-only to existing session workflows and must never mutate the parent session context.
- Session fork v1 scope is fixed to `create`, `list`, `switch`, and `archive`; merge and parent-branch auto-integration are explicitly out of scope.
- Coding-agent fork support is provider-capability-driven and must be abstracted by `main-server` rather than exposed as provider-native behavior.
- Event streaming uses monotonic workspace sequence semantics with replay/resume behavior and explicit out-of-range handling.
- Notification contracts are event-driven; in-app notification state is authoritative while Web Notification API dispatch is permission-dependent.
- Waiting-for-input handoff is a first-class workflow and must support latest-question routing for global shortcut flows.
- Menu bar tray state is display-only and is derived from active-workspace work status priority (`FAILED > ACTION_REQUIRED > WAITING_FOR_INPUT > RUNNING > IDLE > DISCONNECTED`).
- Coding-agent provider-native output is normalized in `worker-server`; only normalized events cross server/client boundaries.
- Shared enum and message identifiers in `protos/dexdex/v1` remain stable or evolve additively under explicit version policy.

## Implementation Status (as of 2026-03-14)

### Proto (`protos/dexdex/v1/dexdex.proto`)
- **Implemented**: All enums, UnitTask/SubTask/Workspace messages with full fields (title, description, timestamps), List/Create/Update RPCs, EventStreamService streaming RPC. New enums: `SessionForkIntent`, `SessionForkStatus`, `WorkspaceWorkStatus`, `AgentCliType`. New enum values: `NOTIFICATION_TYPE_AGENT_INPUT_REQUIRED`, `STREAM_EVENT_TYPE_SESSION_FORK_UPDATED`, `STREAM_EVENT_TYPE_WORKSPACE_WORK_STATUS_UPDATED`. New messages: `SessionSummary` (with lineage fields), `AgentCapability`, `SessionForkUpdatedEvent`, `WorkspaceWorkStatusUpdatedEvent`. New RPCs on SessionService: `ListSessionCapabilities`, `ForkSession`, `ListForkedSessions`, `ArchiveForkedSession`, `GetLatestWaitingSession`, `SubmitSessionInput`. New RPC on WorkspaceService: `GetWorkspaceWorkStatus`. New RPC on NotificationService: `MarkNotificationRead`. New RPC on PrManagementService: `ListPullRequests`. New service: `WorkerSessionAdapterService` (`GetAgentCapabilities`, `ForkSessionAdapter`).
- **Generated**: Go code (`protos/dexdex/gen/`), TypeScript code (`apps/dexdex/src/gen/`).

### Main Server (`servers/dexdex-main-server`)
- **Implemented**: Connect RPC server with WorkspaceService (GetWorkspace, ListWorkspaces, GetWorkspaceWorkStatus with priority-based status computation), TaskService (ListUnitTasks, ListSubTasks, CreateUnitTask, UpdateUnitTaskStatus, GetUnitTask, GetSubTask, SubmitPlanDecision), SessionService (GetSessionOutput, ListSessionCapabilities, ForkSession, ListForkedSessions, ArchiveForkedSession, GetLatestWaitingSession, SubmitSessionInput), NotificationService (ListNotifications, MarkNotificationRead with stream event publishing), EventStreamService (fan-out, replay, heartbeat, SESSION_FORK_UPDATED and WORKSPACE_WORK_STATUS_UPDATED events), RepositoryService (GetRepositoryGroup), PrManagementService (GetPullRequest, ListPullRequests), ReviewAssistService (ListReviewAssistItems), ReviewCommentService (ListReviewComments). Worker client with agent capability caching (5-minute TTL). Session summary store for fork orchestration and lineage tracking. FanOut event publishing on mutations. In-memory store with session output storage and rich seed data support (`DEXDEX_SEED_DATA=true`). PostgreSQL persistence layer via sqlc with conditional store selection (`DEXDEX_DATABASE_URL`). CORS middleware for dev. Default addr `127.0.0.1:7878`.
- **Planned**: Worker adapter routing logic (client exists but no real dispatch), PR polling, worktree orchestration.

### Worker Server (`servers/dexdex-worker-server`)
- **Implemented**: Connect RPC server with SessionService (GetSessionOutput) and WorkerSessionAdapterService (GetAgentCapabilities for CLAUDE_CODE, CODEX_CLI, OPENCODE; ForkSessionAdapter). Session output normalization (raw kind → proto enum). In-memory session store with lineage tracking. Commit chain validation. Default addr `127.0.0.1:7879`.
- **Planned**: Worktree orchestration, actual coding-agent integration/adapters, real fork execution.

### Desktop App (`apps/dexdex`)
- **Implemented**: Linear-style task management UI with light mode default. Connect RPC integration via React Query + @connectrpc/connect-query (replaces mock data). Proto-to-view adapter layer. react-router for page navigation (`/tasks`, `/tasks/:taskId`, `/inbox`, `/prs`, `/settings`). Sidebar navigation with Pull Requests (collapsible, Cmd+B). Task list with status filters and keyboard navigation. Task detail with subtask timeline (fetched via RPC), plan decision controls (Approve/Revise/Reject, wired to server), session output panel (fetched via RPC), session input form for waiting-for-input subtasks. PR management page with status badges (OPEN, APPROVED, CHANGES_REQUESTED, MERGED, CI_FAILED). Review assist panel with suggestions and inline comments. Tab system. Command palette (Cmd+K). Global keyboard shortcuts (G+T, G+I, C). Tauri menu bar tray with workspace work status icon/tooltip. Global shortcut Cmd/Ctrl+Shift+I for question handoff (opens latest waiting session or inbox). Event stream consumer with real Connect streaming RPC and query cache invalidation on events, including SESSION_FORK_UPDATED, WORKSPACE_WORK_STATUS_UPDATED, PR_UPDATED, REVIEW_ASSIST_UPDATED, INLINE_COMMENT_UPDATED. Dark mode toggle (opt-in). Notification inbox with server-side read state (MarkNotificationRead replaces local state). Create task dialog (wired to server). `SessionForkPanel` component. `SessionInputForm` component. 24 UI tests passing with mock transport.
- **Planned**: Repository group selector in create dialog, credential bridge/import.

## Change Policy
- Contract changes across app/server/proto boundaries must update this index and all affected DexDex domain contract docs in the same change set.
- Schema and enum changes in `protos/dexdex/v1` must synchronize desktop, main-server, and worker-server contracts in the same change set.
- Repository path or component ownership changes must keep canonical paths aligned with this file and DexDex domain contract docs.
- Any update that changes execution invariants (worktree policy, repository ordering, commit-chain authority, plan decisions, stream sequencing) must be reflected consistently across all DexDex contract docs.
- Any update that changes session-fork behavior, waiting-input routing, or workspace work-status priority must be reflected consistently across app/server/proto DexDex contract docs.
- Contract sections may include implemented-vs-planned annotations when runtime/proto coverage lags product contract scope.
- External DexDex source-doc merges must update traceability references in this file to keep source coverage auditable.

## References
- `docs/project-template.md`
- `docs/domain-template.md`
- `docs/README.md`
- DexDex upstream source coverage (`delinoio/dexdex` `main` docs, read on 2026-03-13):
- `https://github.com/delinoio/dexdex/blob/main/docs/api.md` -> `docs/protos-dexdex-v1-contract.md`
- `https://github.com/delinoio/dexdex/blob/main/docs/design.md` -> this file + all DexDex domain docs
- `https://github.com/delinoio/dexdex/blob/main/docs/developer-setup.md` -> `docs/apps-dexdex-desktop-app-foundation.md`, `docs/servers-dexdex-main-server-foundation.md`, `docs/servers-dexdex-worker-server-foundation.md`
- `https://github.com/delinoio/dexdex/blob/main/docs/entities.md` -> `docs/protos-dexdex-v1-contract.md`
- `https://github.com/delinoio/dexdex/blob/main/docs/event-streaming.md` -> `docs/servers-dexdex-main-server-foundation.md`, `docs/protos-dexdex-v1-contract.md`
- `https://github.com/delinoio/dexdex/blob/main/docs/main-server.md` -> `docs/servers-dexdex-main-server-foundation.md`
- `https://github.com/delinoio/dexdex/blob/main/docs/notifications.md` -> `docs/apps-dexdex-desktop-app-foundation.md`, `docs/servers-dexdex-main-server-foundation.md`, `docs/protos-dexdex-v1-contract.md`
- `https://github.com/delinoio/dexdex/blob/main/docs/plan-yaml.md` -> `docs/protos-dexdex-v1-contract.md`
- `https://github.com/delinoio/dexdex/blob/main/docs/pr-management.md` -> `docs/servers-dexdex-main-server-foundation.md`, `docs/protos-dexdex-v1-contract.md`
- `https://github.com/delinoio/dexdex/blob/main/docs/tauri-app.md` -> `docs/apps-dexdex-desktop-app-foundation.md`
- `https://github.com/delinoio/dexdex/blob/main/docs/ui.md` -> `docs/apps-dexdex-desktop-app-foundation.md`
- `https://github.com/delinoio/dexdex/blob/main/docs/user-guide.md` -> `docs/apps-dexdex-desktop-app-foundation.md`
- `https://github.com/delinoio/dexdex/blob/main/docs/worker-server.md` -> `docs/servers-dexdex-worker-server-foundation.md`
- `https://github.com/delinoio/dexdex/blob/main/docs/workspace-connectivity.md` -> `docs/apps-dexdex-desktop-app-foundation.md`
