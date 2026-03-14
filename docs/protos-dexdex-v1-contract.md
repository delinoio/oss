# protos-dexdex-v1-contract

## Scope
- Project/component: DexDex shared v1 proto contract
- Canonical path: `protos/dexdex/v1/dexdex.proto`
- Contract role: stable cross-component Connect RPC schema for app, main-server, and worker-server integration

## Runtime and Language
- Runtime: Connect RPC schema contract shared across client and Go server runtimes
- Primary language: Protocol Buffers (`proto3`)

## Users and Operators
- API and client engineers consuming DexDex service/message/enum contracts
- Server maintainers evolving orchestration and normalization boundaries
- Operators validating compatibility and rollout safety across components

## Interfaces and Contracts
- `dexdex.v1` package identifiers are stable enum-style contract language and must evolve additively by default.
- Canonical enum vocabulary includes workspace connectivity, task/subtask/session states, action badges, PR/review/comment/notification types, stream event families, and plan decisions.
- Canonical entity vocabulary includes workspace, repository-group scope, unit/sub tasks, session output/state, PR tracking, review assist items, inline comments, badge themes, and notifications.
- Service-level contract families include:
- workspace/repository control-plane queries and lifecycle
- task/subtask/session orchestration and plan decisions
- PR/review/comment operations
- badge and notification operations
- workspace event streaming
- worker session adapter normalization
- Event stream envelope semantics:
- workspace-scoped monotonic `sequence`
- typed `event_type`
- timestamped occurrence field
- oneof payload family for task, subtask, session output/state, PR, review assist, inline comment, and notification events
- Replay semantics:
- client resumes from last observed sequence
- server returns out-of-range detail when requested cursor predates retained envelopes
- Error semantics:
- contract expects standard Connect error-code mapping (`INVALID_ARGUMENT`, `UNAUTHENTICATED`, `PERMISSION_DENIED`, `NOT_FOUND`, `FAILED_PRECONDITION`, `RESOURCE_EXHAUSTED`, `INTERNAL`, `UNAVAILABLE`) with request-correlation metadata
- API evolution rules:
- additive fields/endpoints preferred
- enum expansion is allowed with unknown-safe client behavior
- breaking changes require coordinated rollout across app and servers
- Implemented-vs-planned alignment (current repo reality):
- implemented proto currently contains subset-centric RPCs such as `Get*`/`List*`, `SubmitPlanDecision`, `RunSubTaskSessionAdapter`, `NormalizeSessionOutputFixture`, and stream APIs
- upstream DexDex source docs define expanded create/update/delete and richer flow contracts that are target scope for additive evolution
- `protos/dexdex/v1/dexdex.proto` remains the canonical source for what is implemented now; this document records both current contract and planned-compatible expansion direction

## Storage
- Canonical proto schema is versioned in-repo at `protos/dexdex/v1/dexdex.proto`.
- Generated artifacts are derived outputs and must remain reproducible from canonical schema inputs.
- Stream replay and sequence cursor compatibility requirements must remain stable across releases.

## Security
- Fields carrying potentially sensitive values require explicit redaction handling in server/client logging and diagnostics.
- Auth and workspace-scope semantics embedded in API contracts must remain explicit and backward compatible.
- Worker normalization boundaries must prevent provider-native raw payload leakage into public API contracts.

## Logging
- Schema evolution and generation workflows should emit structured logs with compatibility-check outcomes.
- Runtime logs should include request/workspace/task/session correlation IDs for RPC and stream processing paths.
- Contract-violation logs (unknown enum handling, invalid payload shape, out-of-range replay) must be actionable and sanitized.

## Build and Test
- Validate schema with repository proto generation and compile checks.
- Run repository baseline tests: `go test ./...`.
- Contract-sensitive coverage should include stream replay semantics, plan decision transitions, normalized session output shape, and backward-compatible enum handling.

## Dependencies and Integrations
- Upstream contract consumers: `apps/dexdex`, `servers/dexdex-main-server`, `servers/dexdex-worker-server`.
- Integrates with Connect RPC tooling/generation pipeline for Go and TypeScript client usage.
- Aligns with DexDex product-level contracts documented in app and server domain docs.

## Change Triggers
- Update this file with `docs/project-dexdex.md` whenever enum/message/service contracts change.
- Synchronize downstream app and server contract docs when proto-level contracts impacting behavior are modified.
- Keep implemented-vs-planned alignment notes current when runtime coverage changes materially.

## References
- `docs/project-dexdex.md`
- `docs/apps-dexdex-desktop-app-foundation.md`
- `docs/servers-dexdex-main-server-foundation.md`
- `docs/servers-dexdex-worker-server-foundation.md`
- `docs/domain-template.md`
- Implementation anchor:
- `protos/dexdex/v1/dexdex.proto`
- Upstream source docs merged into this contract:
- `https://github.com/delinoio/dexdex/blob/main/docs/api.md`
- `https://github.com/delinoio/dexdex/blob/main/docs/entities.md`
- `https://github.com/delinoio/dexdex/blob/main/docs/event-streaming.md`
- `https://github.com/delinoio/dexdex/blob/main/docs/plan-yaml.md`
- `https://github.com/delinoio/dexdex/blob/main/docs/pr-management.md`
- `https://github.com/delinoio/dexdex/blob/main/docs/notifications.md`
