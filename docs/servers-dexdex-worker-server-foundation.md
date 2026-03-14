# servers-dexdex-worker-server-foundation

## Scope
- Project/component: DexDex worker server contract
- Canonical path: `servers/dexdex-worker-server`
- Role: execution-plane and normalization boundary for coding-agent session output and commit-chain artifacts

## Runtime and Language
- Runtime: Go Connect RPC server
- Primary language: Go

## Users and Operators
- Main-server orchestration paths dispatching worker execution and normalization operations
- Operators maintaining execution reliability, adapter correctness, and artifact integrity
- Maintainers extending agent adapters and execution isolation policies

## Interfaces and Contracts
- Stable component identifier: `worker-server`.
- Worker is not a direct client business API target; client-visible business flows are mediated by main-server.
- Execution-plane contract:
- worktree-only execution model for SubTask runs
- repository group ordering preserved and first repository used as primary launch directory
- non-primary repositories attached via `--add-dir` (or adapter-equivalent flags) in deterministic order
- real git commit chain required for code-changing subtasks
- commit metadata ordering and ancestry are authoritative for downstream PR and commit-local actions
- Normalization contract:
- provider-native agent output is parsed only in worker adapter/runtime boundaries
- emitted output to main-server follows normalized `SessionOutputEvent` schema
- provider-native payloads remain worker-local debug material and are not public business contract
- Agent capability contract:
- capability discovery is exposed through `WorkerSessionAdapterService.GetAgentCapabilities`
- capability response is normalized across `CODEX_CLI`, `CLAUDE_CODE`, and `OPENCODE`
- fork support is expressed as capability flags and reason codes (not provider-native text)
- Session fork adapter contract:
- `WorkerSessionAdapterService.ForkSessionAdapter` performs provider-native fork execution behind worker abstraction
- fork output is normalized into shared lineage/session references for main-server persistence
- unsupported fork paths must map to consistent, typed errors consumed by main-server as `FAILED_PRECONDITION`
- provider-native fork payloads remain worker-local diagnostics and are never exposed to app/main-server APIs
- Plan-mode execution contract:
- subtasks can pause for decisions and resume/finalize based on `APPROVE`, `REVISE`, `REJECT`
- cancellation must terminate active agent processes promptly and emit final cancellation state
- Usage/cost contract:
- normalize provider usage counters into shared token/cost schema
- support partial/null metrics where provider counters are unavailable
- Configuration contract (normalized to current monorepo/runtime naming):
- current scaffold implementation does not parse runtime env configuration
- planned envs for execution runtime rollout: `DEXDEX_WORKER_SERVER_ADDR`, `DEXDEX_WORKER_ID`, `DEXDEX_MAIN_SERVER_URL`, `DEXDEX_WORKTREE_ROOT`, `DEXDEX_REPO_CACHE_ROOT`, `DEXDEX_MAX_PARALLEL_SUBTASKS`, `DEXDEX_AGENT_EXEC_TIMEOUT_SEC`
- Implemented-vs-planned alignment:
- current implementation includes Connect RPC server with SessionService (GetSessionOutput) and WorkerSessionAdapterService (GetAgentCapabilities, ForkSessionAdapter)
- `GetAgentCapabilities` returns normalized capability records for `CLAUDE_CODE`, `CODEX_CLI`, and `OPENCODE`
- `ForkSessionAdapter` implements the fork adapter RPC boundary with lineage-tracked session metadata store
- session output normalization (raw kind → proto enum) and in-memory session store with lineage tracking are implemented
- commit chain validation primitives are implemented
- actual coding-agent integration/adapters (real process execution) and worktree orchestration remain planned rollout scope

## Storage
- Target runtime owns worker-local temporary execution data, adapter parsing buffers, and normalized event artifacts prior to main-server persistence.
- Target path conventions include repository cache and task-scoped worktree roots under user-local DexDex directories.
- Commit-chain artifact metadata must remain reproducible, ordered, and attributable per subtask/session context.
- Worker-local capability and fork debug artifacts must be bounded-retention and must not cross public contract boundaries.
- Current implementation maintains an in-memory session metadata store with lineage tracking; no durable adapter output persistence yet.

## Security
- Validate repository URLs, branch refs, and runtime inputs before agent execution.
- Secrets must be injected with minimal scope and lifetime and never logged in plaintext.
- Worker-local debug payload retention must avoid exposing provider-native secret material.
- Execution isolation and least-privilege controls are required for multi-repository operations.
- Session-fork provider calls must redact user prompts/session content from error payloads returned outside worker internals.

## Logging
- Use structured `log/slog` logging for adapter normalization lifecycle, validation failures, cancellation checkpoints, and artifact generation.
- Required diagnostics include primary repository selection, add-dir mapping order, commit-chain validation outcomes, and session status transitions.
- Include workspace/task/subtask/session correlation fields when available.
- Structured logs must include agent capability-refresh outcomes, fork-attempt decisions, and provider-error normalization mappings.

## Build and Test
- Component-local validation: `go test ./servers/dexdex-worker-server/...`
- Repository baseline: `go test ./...`
- Contract-sensitive tests should cover adapter normalization parity, commit-chain validation rules, and input-validation failures.
- Contract-sensitive tests should cover per-agent capability mapping, supported-fork success normalization, and unsupported-fork typed error mapping.

## Dependencies and Integrations
- Depends on shared `protos/dexdex/v1` schemas.
- Current implementation exposes `WorkerSessionAdapterService` RPC handlers (`GetAgentCapabilities`, `ForkSessionAdapter`); real agent process execution and worktree management remain planned.
- Target runtime integrates upstream with `servers/dexdex-main-server` through worker session adapter RPC contracts.
- Target runtime provides normalized session output and artifact contracts consumed by main-server for client-facing flows.
- Target runtime adapter fixtures and parser pipelines support multiple coding-agent CLIs.
- Target runtime fork-capability and fork-adapter contracts are consumed by main-server as provider-agnostic orchestration dependencies.

## Change Triggers
- Update this file with `docs/project-dexdex.md` when execution policy, worktree rules, or adapter boundaries change.
- Synchronize with `docs/protos-dexdex-v1-contract.md` when normalized event, commit metadata, or plan-related schema contracts change.
- Synchronize with `docs/servers-dexdex-main-server-foundation.md` for worker-routing and cancellation contract changes.
- Update app-facing contract docs when worker normalization or artifact guarantees affect user-visible behavior.
- Synchronize with `servers/AGENTS.md` when worker capability/fork normalization policy changes.

## References
- `docs/project-dexdex.md`
- `docs/protos-dexdex-v1-contract.md`
- `docs/servers-dexdex-main-server-foundation.md`
- `docs/apps-dexdex-desktop-app-foundation.md`
- `docs/domain-template.md`
- Implementation anchors:
- `servers/dexdex-worker-server/main.go`
- `servers/dexdex-worker-server/internal/service/commit_chain.go`
- Upstream source docs merged into this contract:
- `https://github.com/delinoio/dexdex/blob/main/docs/worker-server.md`
- `https://github.com/delinoio/dexdex/blob/main/docs/developer-setup.md`
