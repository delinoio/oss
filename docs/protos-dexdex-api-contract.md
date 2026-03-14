# protos-dexdex-api-contract

## Scope
- Project/component: DexDex API service contract
- Canonical source path: `protos/dexdex/v1/dexdex.proto`
- Contract role: Connect RPC business API definitions and service-level behavior

## Runtime and Language
- Runtime: Connect RPC over HTTP/2 (with HTTP/1.1 compatibility where needed)
- Primary language: Protocol Buffers (`proto3`)

## Users and Operators
- Desktop/mobile client engineers consuming business APIs
- Main-server engineers implementing orchestration handlers
- Worker/server engineers integrating execution adapter APIs
- Operators and SREs validating auth, reliability, and rollout compatibility

## Interfaces and Contracts
Contract alignment note:
- This file defines the DexDex API behavior target for this repository.
- Local proto/code synchronization may lag this document during staged migration windows.

Protocol and design rules:
- Connect RPC is the primary business interface.
- Requests/responses use enums for known variants.
- Server-streaming contracts emit typed events with monotonic sequence semantics.
- Coding-agent output contracts are normalized and provider-agnostic.
- Main server is the canonical client boundary.

Service overview:
- `WorkspaceService`
  - `CreateWorkspace`, `ListWorkspaces`, `UpdateWorkspace`, `DeleteWorkspace`, `SetActiveWorkspace`
- `RepositoryService`
  - `AddRepository`, `ListRepositories`, `CreateRepositoryGroup`, `UpdateRepositoryGroup`, `DeleteRepositoryGroup`
- `TaskService`
  - `CreateUnitTask`, `ListUnitTasks`, `GetUnitTask`, `UpdateUnitTaskStatus`, `CancelUnitTask`, `CreateSubTask`, `ListSubTasks`, `ListSubTaskCommits`, `RetrySubTask`, `CancelSubTask`, `SubmitPlanDecision`
- `SessionService`
  - `ListAgentSessions`, `GetAgentSessionLog`, `StopAgentSession`, `SubmitSessionInput`
- `PrManagementService`
  - `TrackPullRequest`, `ListTrackedPullRequests`, `RunAutoFixNow`, `SetAutoFixPolicy`
- `ReviewAssistService`
  - `ListReviewAssistItems`, `ResolveReviewAssistItem`
- `ReviewCommentService`
  - `CreateInlineComment`, `ListInlineComments`, `UpdateInlineComment`, `SetInlineCommentStatus`
- `BadgeThemeService`
  - `ListBadgeThemes`, `UpsertBadgeTheme`
- `NotificationService`
  - `ListNotifications`, `MarkNotificationRead`
- `EventStreamService`
  - `StreamWorkspaceEvents`

Key API behavior contracts:
- Repository group rules:
  - group must contain at least one repository
  - order is preserved and execution-significant
  - first repository is the primary execution repository
- Task cancellation rules:
  - `CancelUnitTask` and `CancelSubTask` are fast user-stop endpoints
  - status transition target is `CANCELLED`
- Plan mode decision rules:
  - decisions use explicit enum actions (`APPROVE`, `REVISE`, `REJECT`)
  - invalid state transitions map to `FAILED_PRECONDITION`
- Inline comment rules:
  - anchors use `file_path`, `side`, and `line_number`
  - status transitions are constrained and stream-emitting
- Event stream envelope:
  - includes sequence, event type, timestamp, and typed payload variants

Error contract:
- `INVALID_ARGUMENT`
- `UNAUTHENTICATED`
- `PERMISSION_DENIED`
- `NOT_FOUND`
- `FAILED_PRECONDITION`
- `RESOURCE_EXHAUSTED`
- `INTERNAL`
- `UNAVAILABLE`

## Storage
- API contracts map to canonical entity definitions in `docs/protos-dexdex-entities-contract.md`.
- Stream sequence offsets and replay cursors are persistence-backed in server storage layers.

## Security
- Authenticated workspaces use bearer-token based authorization.
- Workspace-scoped authorization is required for every business RPC.
- Stream authorization is enforced at stream open and permission-change boundaries.

## Logging
- API logs must include `request_id` and relevant correlation IDs (`workspace_id`, `unit_task_id`, `sub_task_id`, `session_id`, `pr_tracking_id`).
- Contract violations must be logged with typed error code and request context.

## Build and Test
- `cd protos/dexdex && buf lint && buf build`
- `go test ./servers/dexdex-main-server/...`
- `go test ./servers/dexdex-worker-server/...`
- `cd apps/dexdex && pnpm test`

## Dependencies and Integrations
- Entity model: `docs/protos-dexdex-entities-contract.md`
- Plan mode behavior: `docs/protos-dexdex-plan-mode-contract.md`
- Main-server implementation contract: `docs/servers-dexdex-main-server-foundation.md`
- Worker behavior contract: `docs/servers-dexdex-worker-server-foundation.md`
- App integration contract: `docs/apps-dexdex-desktop-app-foundation.md`

## Change Triggers
- Any service/method/request/response/error mapping change must update this file and `docs/protos-dexdex-v1-contract.md` in the same change.
- Changes to Task, Session, PR, Review, Notification, or stream APIs must update corresponding app/server domain docs in the same change.

## References
- `docs/project-dexdex.md`
- `docs/protos-dexdex-v1-contract.md`
- `docs/protos-dexdex-entities-contract.md`
- `docs/protos-dexdex-plan-mode-contract.md`
- `protos/dexdex/v1/dexdex.proto`
