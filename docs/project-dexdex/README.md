# Project: dexdex

## Documentation Layout
- Canonical entrypoint for this project: docs/project-dexdex/README.md
- Keep this directory flat (no nested directories).
- Add feature contracts as feature-<kebab-id>.md files.

## Goal
`dexdex` is a Connect RPC-first task orchestration platform that coordinates UnitTask/SubTask execution, plan approval decisions, commit-chain outputs, and workspace event streaming.
The project exposes a shared protobuf contract (`dexdex.v1`) for multi-runtime integrations while keeping desktop behavior normalized across local and remote endpoint resolution.


## Path
- Main server: `servers/dexdex-main-server`
- Worker server: `servers/dexdex-worker-server`
- Desktop app: `apps/dexdex`
- Desktop frontend: `apps/dexdex/src`
- Desktop Tauri backend: `apps/dexdex/src-tauri`
- Shared proto contracts: `protos/dexdex/v1/dexdex.proto`


## Runtime and Language
- Main server: Go
- Worker server: Go
- Desktop app frontend: React + TypeScript (Vite)
- Desktop app backend: Rust (Tauri)
- Shared RPC contract: Protocol Buffers (`dexdex.v1`) + Connect RPC


## Users
- Developers running AI-assisted implementation workflows
- Reviewers handling PR feedback and remediation loops
- Operators monitoring task/session execution and event delivery health


## In Scope
- Connect RPC-first business contracts for workspace, repository, task, session, PR, review, notification, and stream flows
- Main server control-plane ownership of task/subtask lifecycle decision logic
- Worker server execution-plane ownership of ordered real commit-chain validation
- Worker server event-level normalization of Codex CLI, Claude Code, and OpenCode session outputs
- Plan-mode decision transitions (`APPROVE`, `REVISE`, `REJECT`) at SubTask scope
- Workspace event streaming with replay/resume semantics (`from_sequence` exclusive)
- Desktop workspace mode resolution (`LOCAL`, `REMOTE`) with normalized connection metadata
- DexDex desktop v1 support for Codex CLI, Claude Code, and OpenCode integrations


## Out of Scope
- Tauri-specific bindings as primary business-data contracts
- Patch-only authoritative change outputs without real git commit metadata
- Provider-native raw session payload contracts in main server APIs and client-facing streams
- Full production persistence and distributed orchestration in this phase
- Persistent desktop token vault behavior in this phase


## Document Index
- [feature-architecture.md](./feature-architecture.md)
- [feature-interfaces.md](./feature-interfaces.md)
- [feature-operations.md](./feature-operations.md)
- [feature-roadmap.md](./feature-roadmap.md)

## Documentation Update Rules
- Keep all project contract files in this directory (flat layout).
- Use feature-<kebab-id>.md naming for new capability contracts.
- Update this index whenever feature files are added or removed.
