# servers-dexdex-main-server-foundation

## Scope
- Project/component: DexDex main server contract
- Canonical path: `servers/dexdex-main-server`
- Role: Connect RPC control-plane for workspace/task/repository/session orchestration

## Runtime and Language
- Runtime: Go Connect RPC server
- Primary language: Go

## Interface Contracts
- Main server is the canonical business API boundary for DexDex clients.
- Workspace settings API:
- `GetWorkspaceSettings`
- `UpdateWorkspaceSettings`
- `default_agent_cli_type` is persisted in `workspace_settings`.
- Repository API:
- full CRUD for `Repository`.
- full CRUD for `RepositoryGroup`.
- Repository groups persist ordered members in normalized storage.
- Task create API is prompt-first and requires repository group + agent:
- `prompt`
- `repository_group_id`
- `agent_cli_type`
- `use_plan_mode`
- Task create prevalidation:
- repository group existence is required.
- unsupported plan mode must return `FAILED_PRECONDITION`.
- Dispatch contract:
- task dispatch uses requested/default agent selection.
- dispatch forwards `use_plan_mode` to worker `StartExecutionRequest`.

## Storage Contract
- DexDex schema is intentionally breaking and non-backward-compatible for this rollout.
- Normalized repository model:
- `repositories`
- `repository_groups`
- `repository_group_members`
- Workspace settings model:
- `workspace_settings`
- Unit task persistence stores prompt/agent/plan mode directly.
- No legacy JSON repository-group blob compatibility layer.

## Logging Contract
- Use structured `log/slog`.
- Log capability checks and plan-mode validation decisions.
- Log dispatch start/failure with workspace/task/session identifiers.
- Log repository/group CRUD validation failures with typed outcomes.

## Build and Test
- `go test ./servers/dexdex-main-server/...`
- Contract-sensitive coverage:
- repository CRUD + repository-group CRUD handler/store behavior
- workspace default-agent settings persistence
- prompt-only task creation validation
- unsupported plan-mode rejection (`FAILED_PRECONDITION`)

## Dependencies and Integrations
- Shared proto: `protos/dexdex/v1/dexdex.proto`
- Worker integration: `servers/dexdex-worker-server` via worker adapter/service RPCs
- Desktop integration: `apps/dexdex`

## Change Triggers
- Update this file with `docs/project-dexdex.md` and `docs/protos-dexdex-v1-contract.md` whenever main-server contract behavior changes.
- Keep storage/model contract changes synchronized with migrations/sqlc/query docs in the same change.

## References
- `docs/project-dexdex.md`
- `docs/protos-dexdex-v1-contract.md`
- `docs/servers-dexdex-worker-server-foundation.md`
- `docs/apps-dexdex-desktop-app-foundation.md`
- `docs/domain-template.md`
