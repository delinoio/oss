# protos-dexdex-v1-contract

## Scope
- Project/component: DexDex shared v1 proto contract
- Canonical path: `protos/dexdex/v1/dexdex.proto`
- Contract role: stable Connect RPC schema shared by app/main-server/worker-server

## Runtime and Language
- Runtime: Connect RPC cross-component schema
- Primary language: Protocol Buffers (`proto3`)

## Core Contract
- Package: `dexdex.v1`
- Agent enum contract: `AGENT_CLI_TYPE_CODEX_CLI`, `AGENT_CLI_TYPE_CLAUDE_CODE`, `AGENT_CLI_TYPE_OPENCODE`
- Plan mode capability contract: `AgentCapability.supports_plan_mode`
- Repository model contract:
- `Repository` is first-class.
- `RepositoryGroup` contains ordered `members` (`RepositoryGroupMember`).
- Workspace settings contract:
- `WorkspaceSettings.default_agent_cli_type`
- `WorkspaceService.GetWorkspaceSettings`
- `WorkspaceService.UpdateWorkspaceSettings`
- Task creation contract (breaking):
- `CreateUnitTaskRequest.workspace_id`
- `CreateUnitTaskRequest.prompt`
- `CreateUnitTaskRequest.repository_group_id`
- `CreateUnitTaskRequest.agent_cli_type`
- `CreateUnitTaskRequest.use_plan_mode`
- Task creation does not include manual title/description fields.
- Worker execution contract:
- `StartExecutionRequest.use_plan_mode`
- `StartExecution` streams `StartExecutionResponse`
- Unsupported plan mode must map to `FAILED_PRECONDITION`.

## Service Contract Summary
- `WorkspaceService`: workspace lookup/list/work-status + workspace settings get/update
- `RepositoryService`: full CRUD for repository and repository group
- `TaskService`: task/subtask query, create, status update, plan decision
- `SessionService`: session output, capability listing, session fork/input handoff
- `WorkerSessionAdapterService`: capabilities/fork adapter/start execution/input/cancel
- `EventStreamService`: workspace-scoped monotonic event stream

## Evolution Policy
- DexDex v1 currently allows explicit breaking change rollout (no backward compatibility requirement for old clients/data).
- Future additive evolution should preserve enum/message identifier stability where possible.
- Error mapping remains typed and explicit (`INVALID_ARGUMENT`, `NOT_FOUND`, `FAILED_PRECONDITION`, etc.).

## Build and Test
- `cd protos/dexdex && buf lint && buf build`
- `cd protos/dexdex && buf generate && buf generate --template buf.gen.web.yaml`
- Downstream validation:
- `go test ./servers/dexdex-main-server/...`
- `go test ./servers/dexdex-worker-server/...`
- `cd apps/dexdex && pnpm test`

## Dependencies and Integrations
- Consumers:
- `apps/dexdex`
- `servers/dexdex-main-server`
- `servers/dexdex-worker-server`
- Generated artifacts:
- Go: `protos/dexdex/gen/...`
- TypeScript: `apps/dexdex/src/gen/...`

## Change Triggers
- Update this file with `docs/project-dexdex.md` whenever proto enums/messages/services change.
- Keep app/server domain contract docs synchronized with changed proto behavior in the same change.

## References
- `docs/project-dexdex.md`
- `docs/apps-dexdex-desktop-app-foundation.md`
- `docs/servers-dexdex-main-server-foundation.md`
- `docs/servers-dexdex-worker-server-foundation.md`
- `docs/domain-template.md`
- `protos/dexdex/v1/dexdex.proto`
