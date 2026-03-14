# servers-dexdex-event-streaming-contract

## Scope
- Project/component: DexDex workspace event streaming contract
- Canonical path: `servers/dexdex-main-server`
- Contract role: typed stream envelope, replay semantics, ordering, and operational behavior

## Runtime and Language
- Runtime: Connect RPC server-streaming (`EventStreamService.StreamWorkspaceEvents`)
- Primary language: Go implementation + proto-defined stream payload contracts

## Users and Operators
- Client engineers building stream subscribers and reducers
- Main-server engineers implementing broker/replay semantics
- Operators monitoring stream health and reconnect behavior

## Interfaces and Contracts
Core endpoint contract:
- Request: `workspace_id`, optional `from_sequence`
- Response: ordered stream of workspace event envelopes

Envelope contract:
- `sequence` (workspace-monotonic)
- `event_type` (typed enum)
- `emitted_at` timestamp
- event-specific payload (`task`, `subtask`, `session`, `PR`, `review`, `inline comment`, `notification`)

Event type contract:
- `TASK_UPDATED`
- `SUBTASK_UPDATED`
- `SESSION_OUTPUT`
- `SESSION_STATE_CHANGED`
- `PR_UPDATED`
- `REVIEW_ASSIST_UPDATED`
- `INLINE_COMMENT_UPDATED`
- `NOTIFICATION_CREATED`

Backbone modes:
- `SINGLE_INSTANCE`: in-memory propagation, process-lifetime replay window
- `SCALE`: Redis streams/pubsub propagation with retention-driven replay

Replay and resume contract:
- client resumes with `from_sequence = last_applied + 1`
- server replays retained events from requested sequence
- if sequence is unavailable, return resync-required failure

Ordering and idempotency contract:
- ordering is guaranteed per workspace sequence
- client reducers must be idempotent by sequence
- duplicate envelopes on reconnect are ignored by sequence checks

Backpressure and health contract:
- server may batch high-frequency session output events
- keepalive/heartbeat behavior is allowed
- clients use bounded exponential reconnect backoff

## Storage
- stream sequence offsets are workspace-scoped
- replay buffers are in-memory (`SINGLE_INSTANCE`) or Redis-backed (`SCALE`)

## Security
- workspace authorization required at stream open
- stream closes on auth expiry/permission changes
- no secret payloads in stream bodies

## Logging
Required structured logs:
- stream open/close and auth decisions
- publish/replay counts and lag
- dropped connection and reconnect telemetry
- backlog/retention misses and resync-required outcomes

## Build and Test
- `go test ./servers/dexdex-main-server/...`
- required scenarios:
  - monotonic sequence progression
  - replay from sequence cursor
  - reconnect idempotency
  - retention miss behavior

## Dependencies and Integrations
- API contract: `docs/protos-dexdex-api-contract.md`
- Entity and event enums: `docs/protos-dexdex-entities-contract.md`
- Notification integration: `docs/apps-dexdex-notification-contract.md`
- App stream consumer: `docs/apps-dexdex-desktop-app-foundation.md`

## Change Triggers
- Any envelope/event/backbone/replay contract change must update this file and related proto/app docs in the same change.

## References
- `docs/project-dexdex.md`
- `docs/protos-dexdex-api-contract.md`
- `docs/protos-dexdex-entities-contract.md`
- `docs/apps-dexdex-desktop-app-foundation.md`
