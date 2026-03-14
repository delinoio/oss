# Project: dexdex

## Goal
Define DexDex as a Connect RPC-first orchestration platform with a single breaking v1 contract across desktop app, main server, worker server, and shared proto.

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
- Connect RPC is the canonical business boundary across all DexDex components.
- DexDex v1 now intentionally uses a breaking schema with no backward compatibility.
- Unit task creation contract is prompt-first and requires:
- `workspace_id`
- `prompt`
- `repository_group_id`
- `agent_cli_type`
- optional `use_plan_mode` (default `false`)
- Unit tasks do not accept user-entered title/description fields.
- Task labels in UI (lists/tabs/detail header) are prompt-derived summaries.
- Repository modeling is normalized:
- `Repository` is first-class.
- `RepositoryGroup` stores ordered `members` referencing repositories.
- `WorkspaceSettings.default_agent_cli_type` is server-backed and used as the default agent selection.
- Plan mode capability is explicit per agent via `AgentCapability.supports_plan_mode`.
- `CLAUDE_CODE` and `CODEX_CLI` support plan mode; `OPENCODE` does not.
- Unsupported plan mode requests must return `FAILED_PRECONDITION` in both main-server and worker-server flows.
- Worker execution remains worktree-only and repository-group scoped with deterministic member order.
- Event streaming stays monotonic and workspace-scoped.

## Implementation Status (as of 2026-03-14)

### Proto (`protos/dexdex/v1/dexdex.proto`)
- `CreateUnitTaskRequest` is prompt-first and no longer includes title/description.
- `UnitTask` stores prompt, repository group, agent type, and plan-mode flag.
- `WorkspaceService` includes `GetWorkspaceSettings` and `UpdateWorkspaceSettings`.
- `RepositoryService` includes full CRUD for repositories and repository groups.
- `AgentCapability` includes `supports_plan_mode`.
- `StartExecutionRequest` includes `use_plan_mode`.
- Worker streaming response type is `StartExecutionResponse`.

### Main Server (`servers/dexdex-main-server`)
- Repository persistence is normalized (`repositories`, `repository_groups`, `repository_group_members`).
- Workspace settings persistence is implemented (`workspace_settings`).
- Repository and repository-group handlers provide full CRUD.
- Workspace settings handlers provide read/update for default agent.
- Task creation validates prompt/repository-group/agent plan-mode compatibility and enforces `FAILED_PRECONDITION` for unsupported plan mode.

### Worker Server (`servers/dexdex-worker-server`)
- Agent capabilities expose per-agent plan-mode support.
- Start execution consumes `use_plan_mode`.
- Unsupported plan mode is rejected with `FAILED_PRECONDITION`.
- Worktree preparation consumes repository-group members with hydrated repositories.
- Structured logs include capability checks and plan-mode execution decisions.

### Desktop App (`apps/dexdex`)
- Settings is tabbed: `General`, `Agents`, `Repository Groups`, `Repositories`.
- `Agents` tab manages workspace default coding agent via server settings RPCs.
- `Repositories` tab supports full CRUD.
- `Repository Groups` tab supports full CRUD with ordered members.
- Create Task dialog is prompt-only plus required repository group and agent selection.
- Plan mode toggle is shown only when the selected agent supports plan mode.
- Task display labels are prompt-derived summaries.

## Change Policy
- Any proto/main-server/worker-server/app contract change must update this file and all DexDex domain docs in the same change.
- Any repository/data-model structural change must update domain contracts in the same change.
- Breaking contract updates must be documented explicitly as non-backward-compatible.

## References
- `docs/project-template.md`
- `docs/domain-template.md`
- `docs/README.md`
- `docs/apps-dexdex-desktop-app-foundation.md`
- `docs/servers-dexdex-main-server-foundation.md`
- `docs/servers-dexdex-worker-server-foundation.md`
- `docs/protos-dexdex-v1-contract.md`
