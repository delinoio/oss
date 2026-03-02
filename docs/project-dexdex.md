# Project: dexdex

## Goal
`dexdex` is a Connect RPC-first task orchestration platform with a Rust control plane and Rust worker plane.
It manages UnitTask/SubTask workflows, normalized AgentSession outputs, PR remediation lifecycle, and event-stream-driven updates.

## Path
- Main server: `crates/dexdex-main-server`
- Worker server: `crates/dexdex-worker-server`

## Runtime and Language
- Main server: Rust binary crate
- Worker server: Rust binary crate

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

## Out of Scope
- Tauri-specific business contracts as the primary integration model.
- Patch-only authoritative change outputs without real git commit chain metadata.
- Direct execution against arbitrary local folders without worktree isolation.
- Provider-native raw session payload contracts in main server APIs and client-facing streams.
- Monthly/yearly reporting and analytics product surfaces in this phase.

## Architecture
- Main server (`crates/dexdex-main-server`) is the control plane.
: It exposes Connect RPC services and owns orchestration state, PR polling, event brokering, and authorization boundaries.
: It persists normalized UnitTask/SubTask/AgentSession/PR/review/notification data and emits workspace stream envelopes.
- Worker server (`crates/dexdex-worker-server`) is the execution plane.
: It prepares repository worktrees, launches agent sessions, and normalizes provider-native outputs into shared contracts.
: It persists session artifacts and ordered real-commit metadata produced by SubTask execution.
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
}
```

Deployment mode identifiers:

```ts
enum DexDexDeploymentMode {
  SingleInstance = "SINGLE_INSTANCE",
  Scale = "SCALE",
}
```

Primary Connect RPC service contracts:
- `WorkspaceService`
- `RepositoryService`
- `TaskService`
- `SessionService`
- `PrManagementService`
- `ReviewAssistService`
- `ReviewCommentService`
- `BadgeThemeService`
- `NotificationService`
- `EventStreamService` (server-streaming)

Core enum contracts:

```txt
WorkspaceType:
- LOCAL_ENDPOINT
- REMOTE_ENDPOINT

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
- SubTask outputs that modify code must produce one or more real git commits and ordered commit-chain metadata.
- Plan mode uses `TaskService.SubmitPlanDecision` with `APPROVE | REVISE | REJECT`.
- `SESSION_OUTPUT` stream payloads must remain normalized and provider-agnostic.

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

## Logging
- Use `tracing`-compatible structured logs in both server crates.
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
- Prohibited log content:
: raw provider tokens
: provider-native secret payloads
: plaintext secret material

## Build and Test
Current local validation commands:
- `cargo check -p dexdex-main-server`
- `cargo check -p dexdex-worker-server`
- `cargo test`

Acceptance-focused scenarios:
1. Main server accepts and validates Connect RPC task lifecycle requests.
2. Worker server executes SubTask flow with normalized session output emission.
3. Plan mode waits at decision boundary and resumes on `APPROVE`/`REVISE`.
4. Plan mode reject path finalizes SubTask without further execution.
5. PR remediation subtasks (`PR_REVIEW_FIX`, `PR_CI_FIX`) use the same normalized event contract.
6. Workspace stream replay resumes correctly from `from_sequence`.
7. `SESSION_OUTPUT` payloads remain provider-agnostic at main server boundary.
8. SubTasks with code changes persist real commit-chain metadata.
9. `SINGLE_INSTANCE` mode runs without Redis dependency.
10. `SCALE` mode uses PostgreSQL + Redis-backed event propagation.

## Roadmap
- Phase 1: Finalize project contracts and Rust crate scaffolding for main and worker servers.
- Phase 2: Add proto definitions and Connect RPC handler skeletons for all listed services.
- Phase 3: Implement task orchestration, plan mode, PR polling, and stream replay.
- Phase 4: Harden deployment mode behavior, operational metrics, and failure recovery.

## Open Questions
- Proto package and generated-code directory conventions for DexDex services.
- Worker artifact retention policy defaults and cleanup schedule.
- Final retry/backoff defaults for PR auto-remediation loops.
- Whether patch artifact persistence should be optional by deployment profile.
