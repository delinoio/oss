# Project: dexdex

## Goal
Define DexDex as a Connect RPC-first orchestration platform for CLI coding agents across desktop client, control-plane server, execution-plane server, and shared proto contracts.

This project index prioritizes the architecture and behavior documented in `github.com/delinoio/dexdex` `docs/`.
When implementation details in this repository differ, this document remains the contract target and follow-up sync work is required.

## Project ID
`dexdex`

## Domain Ownership Map
- `apps/dexdex` (`desktop-app`)
- `servers/dexdex-main-server` (`main-server`)
- `servers/dexdex-worker-server` (`worker-server`)
- `protos/dexdex/v1` (`shared-v1-contract`)

## Domain Contract Documents
- `docs/apps-dexdex-desktop-app-foundation.md`
- `docs/apps-dexdex-ui-contract.md`
- `docs/apps-dexdex-user-guide-contract.md`
- `docs/apps-dexdex-notification-contract.md`
- `docs/apps-dexdex-workspace-connectivity-contract.md`
- `docs/servers-dexdex-main-server-foundation.md`
- `docs/servers-dexdex-worker-server-foundation.md`
- `docs/servers-dexdex-event-streaming-contract.md`
- `docs/servers-dexdex-pr-management-contract.md`
- `docs/protos-dexdex-v1-contract.md`
- `docs/protos-dexdex-api-contract.md`
- `docs/protos-dexdex-entities-contract.md`
- `docs/protos-dexdex-plan-mode-contract.md`

## Cross-Domain Invariants
- Connect RPC is the canonical business contract for DexDex; Tauri-native APIs are integration-only.
- Main server is the canonical business API boundary for clients; direct client-to-worker business calls are out of scope.
- Workspace is the top-level scope boundary and supports two connectivity types:
  - `LOCAL_ENDPOINT`
  - `REMOTE_ENDPOINT`
- RepositoryGroup is the execution unit, and repository ordering is deterministic.
- Execution is worktree-only for task runs; direct local-folder editing is out of scope.
- Worker output that changes code must produce a real git commit chain.
- PR creation and commit-to-local flows must use commit-chain metadata as source of truth.
- Plan mode is explicit and decision-driven (`APPROVE`, `REVISE`, `REJECT`) at SubTask execution boundaries.
- Event streaming is workspace-scoped, sequence-based, and reconnect-safe within retention policy.
- Notifications are event-stream driven; the in-app center is authoritative.
- UI behavior is keyboard-first and includes multiline submit (`Cmd+Enter`) and tab lifecycle shortcuts.

## Developer Setup and Validation
Repository layout for DexDex in this monorepo:
- `apps/dexdex`
- `servers/dexdex-main-server`
- `servers/dexdex-worker-server`
- `protos/dexdex`

Prerequisites:
- Go (as pinned by repository toolchain)
- Node.js + pnpm
- Rust toolchain for Tauri host runtime
- SQLite for single-instance mode
- PostgreSQL + Redis for scale mode

Bootstrap and validation checklist:
- `pnpm install`
- `go test ./servers/dexdex-main-server/...`
- `go test ./servers/dexdex-worker-server/...`
- `cd apps/dexdex && pnpm test`
- `cd protos/dexdex && buf lint && buf build`

Recommended runtime environment keys:
- `DEXDEX_DEPLOYMENT_MODE`
- `DEXDEX_HTTP_ADDR`
- `DEXDEX_DATABASE_URL`
- `DEXDEX_REDIS_URL` (`SCALE` mode)
- `DEXDEX_PR_POLL_INTERVAL_SEC`
- `DEXDEX_WORKTREE_ROOT`

## Change Policy
- Any DexDex API, entity, plan-mode, event-streaming, or connectivity contract change must update `docs/project-dexdex.md` and the related domain contract docs in the same change.
- If remote-source contract behavior is adopted before local proto/code sync, keep an explicit alignment note in the changed docs.
- Any path ownership or component-boundary change must update this index and `AGENTS.md` files in the same change.

## References
- `docs/README.md`
- `docs/project-template.md`
- `docs/domain-template.md`
- `docs/protos-dexdex-v1-contract.md`
- `docs/protos-dexdex-api-contract.md`
- `docs/protos-dexdex-entities-contract.md`
- `docs/protos-dexdex-plan-mode-contract.md`
- `docs/apps-dexdex-desktop-app-foundation.md`
- `docs/servers-dexdex-main-server-foundation.md`
- `docs/servers-dexdex-worker-server-foundation.md`
