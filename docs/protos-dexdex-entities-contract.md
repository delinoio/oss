# protos-dexdex-entities-contract

## Scope
- Project/component: DexDex canonical entity and enum model
- Canonical source path: `protos/dexdex/v1/dexdex.proto`
- Contract role: shared entity vocabulary across app/main-server/worker-server

## Runtime and Language
- Runtime: cross-runtime data model for Connect RPC services and event streams
- Primary language: Protocol Buffers (`proto3`) with typed enum contracts

## Users and Operators
- API and UI engineers implementing state transitions and presentation mappings
- Main-server engineers persisting entity state
- Worker engineers emitting normalized session artifacts
- Operators validating lifecycle and incident reconstruction

## Interfaces and Contracts
Contract alignment note:
- This file defines DexDex entity definitions as target behavior for this repository.
- Local field names or payload shapes may temporarily differ until sync work completes.

Conventions:
- IDs are UUID-like string identifiers.
- Timestamps use RFC3339 UTC semantics at API boundaries.
- Known variants use enums, not free-form strings.

Core enums:
- `WorkspaceType`: `LOCAL_ENDPOINT`, `REMOTE_ENDPOINT`
- `UnitTaskStatus`: `QUEUED`, `IN_PROGRESS`, `ACTION_REQUIRED`, `BLOCKED`, `COMPLETED`, `FAILED`, `CANCELLED`
- `SubTaskType`: `INITIAL_IMPLEMENTATION`, `REQUEST_CHANGES`, `PR_CREATE`, `PR_REVIEW_FIX`, `PR_CI_FIX`, `MANUAL_RETRY`
- `SubTaskStatus`: `QUEUED`, `IN_PROGRESS`, `WAITING_FOR_PLAN_APPROVAL`, `WAITING_FOR_USER_INPUT`, `COMPLETED`, `FAILED`, `CANCELLED`
- `AgentSessionStatus`: `STARTING`, `RUNNING`, `WAITING_FOR_INPUT`, `COMPLETED`, `FAILED`, `CANCELLED`
- `SessionOutputKind`: `TEXT`, `PLAN_UPDATE`, `TOOL_CALL`, `TOOL_RESULT`, `PROGRESS`, `WARNING`, `ERROR`
- `ActionType`: `REVIEW_REQUESTED`, `PR_CREATION_READY`, `PLAN_APPROVAL_REQUIRED`, `CI_FAILED`, `MERGE_CONFLICT`, `SECURITY_ALERT`, `USER_INPUT_REQUIRED`
- `BadgeColorKey`: `BLUE`, `GREEN`, `YELLOW`, `ORANGE`, `RED`, `GRAY`
- `PrStatus`: `OPEN`, `APPROVED`, `CHANGES_REQUESTED`, `MERGED`, `CLOSED`, `CI_FAILED`
- `ReviewAssistStatus`: `OPEN`, `ACKNOWLEDGED`, `RESOLVED`, `DISMISSED`
- `ReviewInlineCommentStatus`: `OPEN`, `RESOLVED`, `DELETED`
- `DiffSide`: `OLD`, `NEW`
- `NotificationType`: `TASK_ACTION_REQUIRED`, `PLAN_ACTION_REQUIRED`, `PR_REVIEW_ACTIVITY`, `PR_CI_FAILURE`, `AGENT_SESSION_FAILED`
- `StreamEventType`: `TASK_UPDATED`, `SUBTASK_UPDATED`, `SESSION_OUTPUT`, `SESSION_STATE_CHANGED`, `PR_UPDATED`, `REVIEW_ASSIST_UPDATED`, `INLINE_COMMENT_UPDATED`, `NOTIFICATION_CREATED`

Core entities:
- `Workspace`
- `Repository`
- `RepositoryGroup`
- `UnitTask`
- `SubTask`
- `GeneratedCommit`
- `AgentSession`
- `SessionOutputEvent`
- `TokenUsageMetrics`
- `PullRequestTracking`
- `ReviewAssistItem`
- `ReviewInlineComment`
- `BadgeTheme`
- `Notification`

Entity relationship invariants:
- `Workspace 1:N Repository`
- `Workspace 1:N UnitTask`
- `UnitTask 1:N SubTask`
- `SubTask 1:N AgentSession`
- `AgentSession 1:N SessionOutputEvent`
- `UnitTask 1:N PullRequestTracking`
- `PullRequestTracking 1:N ReviewAssistItem`
- `UnitTask 1:N ReviewInlineComment`

Execution and commit invariants:
- RepositoryGroup ordering is deterministic and execution-significant.
- First repository is the primary execution directory.
- `UnitTask.repository_group_id` may reference either an explicit `RepositoryGroup` ID or a `Repository` ID resolved as an implicit single-member repository group at execution time.
- SubTask code changes produce ordered real git commit metadata.
- Patch references are derived artifacts; commit chain is authoritative.

Plan mode attachment contract:
- Plan-mode decision metadata is attached to SubTask and AgentSession lifecycle records.

## Storage
- Main server persists canonical task/session/PR/review/notification state.
- Worker persists provider-native diagnostics locally but emits only normalized contracts.

## Security
- Entity payloads must not include secret material in event stream or persisted user-visible logs.
- Authorization is workspace-scoped for all entity reads/writes.

## Logging
- Lifecycle logs should include state transition tuples (`previous_state`, `next_state`) and entity IDs.
- Failure logs should include typed reason codes for retry/backoff policy decisions.

## Build and Test
- `cd protos/dexdex && buf lint && buf build`
- `go test ./servers/dexdex-main-server/...`
- `go test ./servers/dexdex-worker-server/...`
- `cd apps/dexdex && pnpm test`

## Dependencies and Integrations
- API contract: `docs/protos-dexdex-api-contract.md`
- Plan mode contract: `docs/protos-dexdex-plan-mode-contract.md`
- UI mapping: `docs/apps-dexdex-ui-contract.md`
- PR/stream/notification workflows:
  - `docs/servers-dexdex-pr-management-contract.md`
  - `docs/servers-dexdex-event-streaming-contract.md`
  - `docs/apps-dexdex-notification-contract.md`

## Change Triggers
- Any enum/entity/relationship update must update this file and `docs/protos-dexdex-v1-contract.md` in the same change.
- Any change to status/action mapping must update API, app UI, and server workflow docs in the same change.

## References
- `docs/project-dexdex.md`
- `docs/protos-dexdex-v1-contract.md`
- `docs/protos-dexdex-api-contract.md`
- `docs/protos-dexdex-plan-mode-contract.md`
- `protos/dexdex/v1/dexdex.proto`
