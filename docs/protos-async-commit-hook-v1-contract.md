# async-commit-hook v1 protocol contract

## Scope
`protos/async_commit_hook/v1` owns package `async_commit_hook.v1`; committed Go and TypeScript bindings are tool-generated.

## Runtime and Language
Protobuf and Connect over loopback HTTP. CLI/MCP call the same Go application service directly.

## Users and Operators
Paired local browsers; one owning OS account.

## Interfaces and Contracts
Typed v1 services expose pairing, repositories/worktrees/branches, changes/commits, runs/checks, logs/reports/failures, comparisons, inbox, acknowledgement, cancellation and existing-run reruns. Product identifiers are UUID v7. Pagination and log cursors are bounded and scope-validated. Reject unsupported API/state versions. Browser requests never carry arbitrary shell commands or unrestricted filesystem paths.

`RerunResponse.run_id` is a durable acceptance receipt. Its additive optional `startup_diagnostic` reports post-acceptance runner startup failure while preserving a successful Connect response. The diagnostic uses `startup-failed`, a safe message and recovery hint. Pre-acceptance failures remain Connect errors with no receipt. Omitted diagnostics retain the existing successful-start response.

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

Run pages apply repository, worktree and branch filters before cursor pagination. Cursor scope includes all selected filters. Report reads require an artifact ID listed on a check belonging to the selected execution; report and log pages are bounded and root-confined.

`ListRunsRequest.detached=true` selects exactly the empty stored branch and rejects a simultaneous named branch. An empty branch with `detached=false` leaves branches unfiltered, preserving existing clients and the cross-branch inbox. Cursors include this discriminator.

Evidence text pages replace invalid UTF-8 sequences with U+FFFD before serialization. Offsets count original stored bytes. The byte limit may extend by at most three bytes to complete a valid UTF-8 rune; an incomplete rune at the end of an active log is deferred until more bytes arrive or the check finishes. Concatenating pages preserves valid source text. Rendering never rewrites stored evidence or its integrity digest.

Changes diff text is normalized to valid UTF-8 after applying its 2 MiB raw-byte limit. Invalid path/content bytes and a final cut rune become U+FFFD; normalization never changes the truncation decision.

Failure summary fields are bounded to 4 KiB and summaries share a 1 MiB JSON budget per run, below the 8 MiB Connect response limit. Truncation has a stable diagnostic; complete report bodies remain paginated evidence.
