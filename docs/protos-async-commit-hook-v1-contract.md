# async-commit-hook v1 protocol contract

## Scope
`protos/async_commit_hook/v1` owns package `async_commit_hook.v1`; committed Go and TypeScript bindings are tool-generated.

## Runtime and Language
Protobuf and Connect over loopback HTTP. CLI/MCP call the same Go application service directly.

## Users and Operators
Paired local browsers; one owning OS account.

## Interfaces and Contracts
Typed v1 services expose pairing, repositories/worktrees/branches, changes/commits, runs/checks, logs/reports/failures, comparisons, inbox, acknowledgement, cancellation and existing-run reruns. Product identifiers are UUID v7. Pagination and log cursors are bounded and scope-validated. Reject unsupported API/state versions. Browser requests never carry arbitrary shell commands or unrestricted filesystem paths.

## Storage
Browser tokens are stored hashed in SQLite, with explicit revocation. Pairing codes expire after five minutes and are consumed atomically once. All results stay on the local machine.

## Security
Bind 127.0.0.1 only. Exact allowed Origins: https://ach.delino.io, http://localhost:46308, http://127.0.0.1:46308. Validate Host against the configured loopback endpoint. Require authentication on every result/control RPC; only pairing and bounded version information are unauthenticated. CORS preflight does not bypass RPC authentication. Revocation is checked on each request. File reads are scoped by stored execution/evidence IDs.

## Logging
Stable error codes and correlation IDs; never log Authorization, pairing codes, request secrets or raw reports.

## Build and Test
Buf format/lint/breaking/freshness, Go/TypeScript serialization and transport tests, origin/auth/pairing/revocation tests. Generator filters keep DevHud and ach client outputs separate.

## Dependencies and Integrations
Connect-Go, protobuf-es, Connect Query and the shared command service.

## Change Triggers
Update project, command, app and client contracts with all wire changes.

## References
- [Project](project-async-commit-hook.md)
- [Repository defaults](repository-defaults.md)
