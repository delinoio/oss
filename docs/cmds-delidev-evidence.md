# DeliDev implementation and evidence ledger

Issue #964 is preserved in full in [requirements](cmds-delidev-requirements.md). This ledger distinguishes code, deterministic tests, and actual external/native evidence. It is not a reduction of the requirements.

## Current implementation work
The CLI/server/Worker implementation is in progress. No release or real-harness integration claim is made.

| Boundary | Implementation | Verification |
| --- | --- | --- |
| CLI, typed JSON, explicit startup, server/sidecar lifecycle | Pending | Pending |
| Connect, authentication, origins, pairing, TLS, streams | Pending | Pending |
| SQLite, IDs/revisions, atomic events/receipts, backup/restore | Exclusive private scope, WAL transactions, global entity identity, optimistic revisions, durable receipts, metadata-only events, coherent bounded snapshots, consistent backup implemented; restore/deletion coordination pending | Real temporary SQLite: concurrent retries/restart, rollback, kind collisions, deletion receipt redaction, cursor bounds, WAL backup, corrupt/newer DB preservation; Go race tests pass |
| Projects, repositories, settings, restrictions, templates | Pending | Pending |
| Accounts, provider/models, protected secrets, proxy | Pending | Pending |
| Six routing policies, quota evidence, immutable snapshots | Pending | Pending |
| Workers, executables, outbound jobs, processes, updates, services | Pending | Pending |
| Codex, Claude Code, OpenCode, Grok Build native adapters | Pending | Real accounts/versions not validated |
| Workspace preparation, Local protection, forks, snapshots | Pending | Pending |
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
