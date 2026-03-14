# servers-dexdex-worker-server-foundation

## Scope
- Project/component: DexDex worker server execution-plane contract
- Canonical path: `servers/dexdex-worker-server`
- Role: SubTask execution runtime, agent adapter normalization boundary, and worktree lifecycle manager

## Runtime and Language
- Runtime: Go Connect RPC server
- Primary language: Go

## Users and Operators
- Main-server engineers dispatching execution work
- Worker/runtime engineers implementing agent adapters
- Operators troubleshooting execution, commit-chain, and cancellation behavior

## Interfaces and Contracts
Contract alignment note:
- This contract defines the DexDex worker behavior target execution semantics for this repository.
- Local implementation and command builders may lag while synchronization work is ongoing.

Execution principles:
- worktree-only execution policy
- one SubTask execution context per RepositoryGroup
- deterministic repository ordering for primary and attached directories
- cancellation-safe and retry-safe runner lifecycle

Runtime architecture contracts:
- job receiver and SubTask runner orchestration
- worktree manager
- agent adapter layer
- session event emitter
- artifact collector

Worktree lifecycle contracts:
- resolve ordered repositories from repository group context
- bootstrap cache and create task-specific worktrees
- launch in primary repository (first member)
- attach remaining repositories via `--add-dir` (or equivalent)
- persist real git commit chain metadata
- export derived artifacts and apply cleanup policy

Plan mode contracts:
- honor SubTask `plan_mode_enabled`
- pause on plan checkpoints and await decisions
- resume/terminate by explicit user decision outcomes

Normalization contracts:
- provider-native outputs are worker-internal
- public outputs are normalized `SessionOutputEvent` contracts
- main-server/client never parse provider-native payloads

Usage and cost contracts:
- normalize token and cost metrics per AgentSession
- emit checkpoint and final usage summaries

## Storage
Worker-managed runtime artifacts:
- repository cache (`~/.dexdex/repo-cache/...`)
- worktree paths (`~/.dexdex/worktrees/...`)
- temporary session diagnostics
- normalized commit-chain and usage payloads sent upstream

## Security
- validate repository URLs, refs, and runtime inputs
- scope secret injection to execution runtime only
- remove ephemeral secret material after session completion
- prevent provider-native sensitive payload leakage to public APIs

## Logging
Use structured `log/slog` and include:
- worktree create/cleanup lifecycle
- session start/stop/cancel checkpoints
- plan-mode wait/resume decisions
- primary/additional repository directory mapping
- commit-chain generation counts and artifact export outcomes
- usage/cost capture checkpoints

## Build and Test
- `go test ./servers/dexdex-worker-server/...`
- Contract-sensitive checks:
  - repository-group ordering and worktree mapping
  - plan-mode gating and unsupported-request handling
  - normalized output mapping correctness
  - cancellation and cleanup behavior

## Dependencies and Integrations
- Shared proto contracts: `protos/dexdex/v1/dexdex.proto`
- Upstream orchestrator: `servers/dexdex-main-server`
- PR and stream contract docs:
  - `docs/servers-dexdex-pr-management-contract.md`
  - `docs/servers-dexdex-event-streaming-contract.md`

## Change Triggers
- Any execution/runtime/adapter contract change must update this file and `docs/project-dexdex.md` in the same change.
- Any output normalization change must synchronize with proto/API and app rendering contracts.

## References
- `docs/project-dexdex.md`
- `docs/protos-dexdex-v1-contract.md`
- `docs/protos-dexdex-api-contract.md`
- `docs/servers-dexdex-main-server-foundation.md`
