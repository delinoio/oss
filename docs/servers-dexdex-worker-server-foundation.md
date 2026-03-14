# servers-dexdex-worker-server-foundation

## Scope
- Project/component: DexDex worker server contract
- Canonical path: `servers/dexdex-worker-server`
- Role: execution-plane and agent output normalization boundary

## Runtime and Language
- Runtime: Go Connect RPC server
- Primary language: Go

## Interface Contracts
- Worker is not a direct desktop business API target; it is orchestrated by main-server.
- Capability API:
- `GetAgentCapabilities` returns `supports_fork` and `supports_plan_mode`.
- Current plan-mode support:
- `CLAUDE_CODE`: true
- `CODEX_CLI`: true
- `OPENCODE`: false
- Execution API:
- `StartExecution` consumes `use_plan_mode`.
- execution stream response type is `StartExecutionResponse`.
- unsupported plan mode requests must return `FAILED_PRECONDITION`.
- Worktree contract:
- repository groups are consumed via ordered `members`.
- first member repository is primary execution directory.
- additional repositories are attached in deterministic member order.

## Execution and Normalization Contract
- Plan mode decisions are logged and applied at command build/execution time.
- Worker performs agent-specific execution behavior when plan mode is enabled for supported agents.
- Provider-native output remains worker-internal and is normalized before streaming.
- Session states and worktree lifecycle transitions are emitted through normalized proto events.

## Logging Contract
- Use structured `log/slog`.
- Log:
- capability resolution
- plan-mode support checks
- plan-mode execution decisions
- worktree preparation and cleanup lifecycle states

## Build and Test
- `go test ./servers/dexdex-worker-server/...`
- Contract-sensitive coverage:
- capability matrix (`supports_plan_mode`)
- unsupported plan mode rejection (`FAILED_PRECONDITION`)
- repository-group member model handling in worktree preparation

## Dependencies and Integrations
- Shared proto: `protos/dexdex/v1/dexdex.proto`
- Upstream orchestrator: `servers/dexdex-main-server`

## Change Triggers
- Update this file with `docs/project-dexdex.md` and `docs/protos-dexdex-v1-contract.md` when worker execution/capability contracts change.
- Keep worker/main-server contract changes synchronized in the same change.

## References
- `docs/project-dexdex.md`
- `docs/protos-dexdex-v1-contract.md`
- `docs/servers-dexdex-main-server-foundation.md`
- `docs/apps-dexdex-desktop-app-foundation.md`
- `docs/domain-template.md`
