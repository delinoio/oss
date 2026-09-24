# DeliDev implementation and evidence ledger

Issue #964 is preserved in full in [requirements](cmds-delidev-requirements.md). This ledger distinguishes code, deterministic tests, and actual external/native evidence. It is not a reduction of the requirements.

## Current implementation work
The CLI/server/Worker implementation is in progress. No release or real-harness integration claim is made.

| Boundary | Implementation | Verification |
| --- | --- | --- |
| CLI, typed JSON, explicit startup, server/sidecar lifecycle | Versioned JSON/errors, explicit detached/foreground startup and compatible reuse, status/stop, configuration CRUD, read-only doctor, backup implemented; native services/supervision pending | Native macOS subprocess smoke: no implicit startup, readiness/reuse, CRUD, backup, explicit stop, restart persistence |
| Connect, authentication, origins, pairing, TLS, streams | Owner authentication on every product RPC, exact origins, TLS-required remote listeners, signed scoped cursors, bounded Connect event streaming implemented; device pairing/Worker authorization pending | Real loopback Connect: missing authentication, hostile origin/Host, snapshot/replay, deduplication across restart, backup deduplication; remote TLS validation guards tested, remote native lifecycle not validated |
| SQLite, IDs/revisions, atomic events/receipts, backup/restore | Exclusive private scope, WAL transactions, global entity identity, optimistic revisions, durable receipts, metadata-only events, coherent bounded snapshots, consistent backup implemented; restore/deletion coordination pending | Real temporary SQLite: concurrent retries/restart, rollback, kind collisions, deletion receipt redaction, cursor bounds, WAL backup, corrupt/newer DB preservation; Go race tests pass |
| Projects, repositories, settings, restrictions, templates | Strict versioned configuration schemas, relationship/revision validation and server-owned health fields implemented; repository save remains blocked until Worker inspection is implemented | CLI/Connect configuration and stale revision tests; complete project workflow pending |
| Accounts, provider/models, protected secrets, proxy | Pending | Pending |
| Six routing policies, quota evidence, immutable snapshots | Pure six-policy selection and read-only preview implemented; first-dispatch atomic snapshot/routing persistence pending | Weighted rotation, sequential traversal/recovery, project/account restrictions, Fixed exclusions, minimum blocking windows, stale evidence and quota ties tested |
| Workers, executables, outbound jobs, processes, updates, services | Pending | Pending |
| Codex, Claude Code, OpenCode, Grok Build native adapters | Pending | Real accounts/versions not validated |
| Workspace preparation, Local protection, forks, snapshots | Worker-local inspection, exact reference resolution, detached multi-repository preparation, primary cwd, Local protection, General Chat isolation, durable preparation/cleanup manifests implemented; RPC integration, native forks and snapshots pending | Real temporary Git: subdirectory/linked inspection, detached commits, retry reuse, partial rollback, dirty Local preservation, remote fetch advancement/failure, disabled fetch, cancellation, explicit missing-default failure |
| Sessions, queue, Steer, interactions, Plan, archive/recovery | Pending | Pending |
| Schedules, overlap/skip/wait, durable occurrences | Pending | Pending |
| Terminal/files/diff/reviews/Sidechat/forwarding | Pending | Pending |
| GitHub PAT/query/PR evidence/remediation | Pending | Real GitHub integration not validated |
| Usage/costs/budgets, diagnostics/doctor | Pending | Pending |
| Search/activity/inbox, config import/export | Pending | Pending |
| Deletion/managed backups/offline cleanup/storage | Pending | Pending |
| CLI build/distribution and OS lifecycle | Pending | Native Windows/Linux evidence pending |

## Presentation scope
The current user request is the CLI. Desktop windows/tray/widgets/native browser presentation and desktop artifact installation belong to the full product issue and remain visible in the complete requirements; they are not claimed as CLI implementation evidence. Their server-owned data and product operations remain within the CLI/RPC scope.

## Local verification log
- 2026-09-24, macOS arm64, Go 1.26.1: `go test -race ./cmds/delidev-cli/internal/...` and `go vet ./cmds/delidev-cli/internal/...` for the domain, private-files, and SQLite foundation. No harness or provider account was invoked.
- 2026-09-24: `go test -race ./cmds/delidev-cli/...`, package vet, Buf formatting/lint, and generated Go bindings validated. The native macOS executable was exercised against an isolated temporary data directory. Linux arm64 cross-compilation succeeded; this does not establish native Linux behavior.
- 2026-09-24: Windows amd64 cross-compilation succeeded. Native Windows runtime remains unverified. Real temporary Git preparation tests and package race tests/vet pass on macOS arm64.

## Native interface discovery (not execution validation)
- Installed read-only version/help checks: Codex CLI `0.151.0`, Claude Code `2.1.236`, OpenCode `1.18.20`. No `grok` executable was found on this machine. No user credential files were read and no inference was invoked by these checks.
- Official adapter references retrieved: [Codex app-server](https://learn.chatgpt.com/docs/app-server) for native expected-turn steering; [Claude Code programmatic execution](https://code.claude.com/docs/en/headless) for structured streams; [OpenCode server](https://opencode.ai/docs/server/) for authenticated native HTTP; [Grok Build headless/ACP](https://docs.x.ai/build/cli/headless-scripting) for native JSON-RPC integration. Installed-version capability validation and real-account evidence remain required before claiming an adapter supported end to end.
