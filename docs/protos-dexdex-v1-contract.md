# protos-dexdex-v1-contract

## Scope
- Project/component: DexDex shared v1 proto contract
- Canonical path: `protos/dexdex/v1/dexdex.proto`
- Contract role: stable cross-runtime schema for app/main-server/worker-server

## Runtime and Language
- Runtime: Connect RPC cross-component contracts
- Primary language: Protocol Buffers (`proto3`)

## Users and Operators
- API client implementers in `apps/dexdex`
- Main-server and worker-server implementers
- Operators validating contract rollout and compatibility windows

## Interfaces and Contracts
Contract alignment note:
- The source-of-truth contract for this document is the DexDex upstream docs set.
- Local proto/code may temporarily diverge while synchronization work is in progress.

Core package and identifiers:
- Package: `dexdex.v1`
- Agent enum contract includes `CODEX_CLI`, `CLAUDE_CODE`, `OPENCODE` variants.
- Plan decision contract uses explicit enum states (`APPROVE`, `REVISE`, `REJECT`).

Service surface summary:
- `WorkspaceService`
- `RepositoryService`
- `TaskService`
- `SessionService`
- `PrManagementService`
- `ReviewAssistService`
- `ReviewCommentService`
- `BadgeThemeService`
- `NotificationService`
- `WorkerSessionAdapterService`
- `EventStreamService`

V1 behavior priorities adopted from upstream contracts:
- Workspace connectivity supports local and remote endpoint modes.
- RepositoryGroup is ordered and execution-significant.
- Task orchestration uses UnitTask -> SubTask -> AgentSession hierarchy.
- Session output is normalized and provider-agnostic.
- Event stream is sequence-based and workspace-scoped.

Known alignment hotspots requiring explicit sync tracking:
- `CreateUnitTask` request shape and optional title/branch fields in upstream API docs versus local prompt-first schema.
- Stream envelope naming and payload grouping.
- Workspace connectivity and entity field expansion.

## Storage
- Proto definitions are versioned in `protos/dexdex/v1/dexdex.proto`.
- Generated code artifacts are consumers of this schema, not the source of truth.

## Security
- Non-localhost endpoints require TLS.
- Authenticated workspaces use bearer-token style auth.
- Secrets must not be embedded in proto payload fields intended for logs or streams.

## Logging
- Contract validation logs must include request identifiers and typed error mapping.
- Schema mismatch handling should log contract version context and rejected field details.

## Build and Test
- `cd protos/dexdex && buf lint && buf build`
- `cd protos/dexdex && buf generate && buf generate --template buf.gen.web.yaml`
- `go test ./servers/dexdex-main-server/...`
- `go test ./servers/dexdex-worker-server/...`
- `cd apps/dexdex && pnpm test`

## Dependencies and Integrations
- Producers/consumers:
  - `apps/dexdex`
  - `servers/dexdex-main-server`
  - `servers/dexdex-worker-server`
- Detailed service contract: `docs/protos-dexdex-api-contract.md`
- Detailed entity contract: `docs/protos-dexdex-entities-contract.md`
- Detailed plan-mode contract: `docs/protos-dexdex-plan-mode-contract.md`

## Change Triggers
- Update this file with `docs/project-dexdex.md` whenever proto services/messages/enums change.
- Keep API, entity, and plan-mode contract docs synchronized in the same change.
- Keep desktop/main-server/worker-server docs synchronized with changed proto behavior.

## References
- `docs/project-dexdex.md`
- `docs/protos-dexdex-api-contract.md`
- `docs/protos-dexdex-entities-contract.md`
- `docs/protos-dexdex-plan-mode-contract.md`
- `protos/dexdex/v1/dexdex.proto`
