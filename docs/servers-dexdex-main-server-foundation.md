# servers-dexdex-main-server-foundation

## Scope
- Project/component: DexDex main server control-plane contract
- Canonical path: `servers/dexdex-main-server`
- Role: Connect RPC business API boundary for workspace/task/session/PR/review/notification orchestration

## Runtime and Language
- Runtime: Go Connect RPC server
- Primary language: Go

## Users and Operators
- Client engineers consuming business RPC and stream contracts
- Backend engineers implementing orchestration, state transitions, and polling
- Operators running single-instance or scale deployments

## Interfaces and Contracts
Contract alignment note:
- This contract follows upstream DexDex server architecture and API behavior as target state.
- Local implementation may diverge temporarily and must be synchronized in follow-up changes.

Core responsibilities:
- workspace endpoint and auth profile management
- repository and repository-group lifecycle management
- UnitTask and SubTask orchestration
- worker dispatch and cancellation propagation
- PR tracking, polling, and auto-fix control
- review assist and inline comment lifecycle
- notification record lifecycle
- event-stream fan-out and replay handoff

Architecture and deployment contracts:
- Main server is the canonical client-facing business boundary.
- Single-instance mode:
  - SQLite primary store
  - in-memory event propagation
- Scale mode:
  - PostgreSQL primary store
  - Redis stream/pubsub propagation

Task orchestration contracts:
- `CreateUnitTask` persists queued top-level work and schedules initial SubTask.
- Cancellation contracts (`CancelUnitTask`, `CancelSubTask`) propagate quickly to worker runtime.
- Plan decision contracts enforce explicit state transitions and typed errors.

PR management contracts:
- Poll provider state and detect actionable signals.
- Create remediation SubTasks for manual or auto-fix flows.
- Persist attempt budgets and blocked states.

Normalized session contract:
- main server consumes only normalized worker output payloads
- provider-native raw agent payloads are not public contract surface

## Storage
Main server owns persistence for:
- workspace, repository, repository group
- UnitTask, SubTask, AgentSession metadata
- commit-chain metadata and task lifecycle state
- PR tracking and review assist data
- review inline comments and status
- notification records
- stream sequence offsets and replay anchors

## Security
- workspace-scoped authorization on every RPC
- bearer-token auth for shared/remote deployments
- strict input validation for repository refs, prompts, and review payloads
- no secret leakage in logs or stream payloads

## Logging
Use structured `log/slog` with correlation keys:
- `request_id`
- `workspace_id`
- `unit_task_id`
- `sub_task_id`
- `session_id`
- `pr_tracking_id`

Required log categories:
- task/subtask/session state transitions
- worker dispatch/cancel outcomes
- PR polling snapshots and auto-fix decisions
- event-stream publish/replay health
- notification generation reasons

## Build and Test
- `go test ./servers/dexdex-main-server/...`
- Contract-sensitive checks:
  - workspace/repository/task/session API behavior
  - plan-decision transition validation
  - PR polling and remediation workflow behavior
  - event-stream sequence/replay semantics

## Dependencies and Integrations
- Shared proto contracts: `protos/dexdex/v1/dexdex.proto`
- Worker integration contract: `docs/servers-dexdex-worker-server-foundation.md`
- Event streaming details: `docs/servers-dexdex-event-streaming-contract.md`
- PR workflow details: `docs/servers-dexdex-pr-management-contract.md`
- App integration: `docs/apps-dexdex-desktop-app-foundation.md`

## Change Triggers
- Any server API/state/storage/orchestration contract change must update this file and `docs/project-dexdex.md` in the same change.
- Stream or PR workflow changes must synchronize with dedicated server-domain contract docs.
- Proto-facing changes must synchronize with proto-domain contract docs.

## References
- `docs/project-dexdex.md`
- `docs/protos-dexdex-v1-contract.md`
- `docs/protos-dexdex-api-contract.md`
- `docs/servers-dexdex-worker-server-foundation.md`
- `docs/servers-dexdex-event-streaming-contract.md`
- `docs/servers-dexdex-pr-management-contract.md`
