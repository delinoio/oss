# async-commit-hook v1 protocol contract

## Scope
`protos/async_commit_hook/v1` owns package `async_commit_hook.v1`; committed Go and TypeScript bindings are tool-generated.

## Runtime and Language
Protobuf and Connect over loopback HTTP. CLI/MCP call the same Go application service directly.

## Users and Operators
Browsers visiting the local ach UI; trusted local processes.

## Interfaces and Contracts
Typed v1 services expose repositories/worktrees/branches, changes/commits, runs/checks, logs/reports/failures, comparisons, inbox, acknowledgement, cancellation and existing-run reruns. Product identifiers are UUID v7. Pagination and log cursors are bounded and scope-validated. Reject unsupported API/state versions. Browser requests never carry arbitrary shell commands or unrestricted filesystem paths.

`RerunResponse.run_id` is a durable acceptance receipt. Its additive optional `startup_diagnostic` reports post-acceptance runner startup failure while preserving a successful Connect response. The diagnostic uses `startup-failed`, a safe message and recovery hint. Pre-acceptance failures remain Connect errors with no receipt. Omitted diagnostics retain the existing successful-start response.

### Protocol integration

- `protos/async_commit_hook/v1` owns package `async_commit_hook.v1`; follow `protos-async-commit-hook-v1-contract.md`. Generate Go bindings and the isolated ach TypeScript client reproducibly. No arbitrary command or filesystem endpoint.

- RerunResponse carries the accepted run_id even when startup fails, with an optional startup_diagnostic; pre-acceptance errors remain Connect errors.

- Its run-list detached filter must distinguish an empty stored branch from omitted filtering and participate in cursor scope.

- async-commit-hook run lists carry optional check_count totals and omit check arrays/diagnostics; GetRun retains complete detail. Preserve older-response count fallback in clients.

- async-commit-hook repository lists use cursor/limit requests and next_cursor responses, page across worktrees (maximum 50), and bound display fields to 4 KiB; one repository can span pages.

- async-commit-hook branch lists accept cursor/limit and return next_cursor; default/max 50 refs and 128 KiB raw records per page, with 64 KiB per-record rejection. Cursors bind the worktree and raw lexical ref.

- async-commit-hook branch labels are display-only when Branch.id or Worktree.branch_id is present. Preserve additive branch_id fields, bounded worktree scope, legacy omission and exact-byte run filtering.

- async-commit-hook Pair is a deprecated v1 compatibility tombstone returning Unimplemented. All active RPCs require the same local UI origin and API version header; no pairing/authentication messages are newly introduced.

- Additive GetUsageSummary daily/model analytics use `UsageTimeGranularity`, an explicit IANA timezone, and optional response analytics; omitted/UNSPECIFIED fields preserve the existing summary behavior. DAY analytics are present even when empty and contain exact same-snapshot UsageTotals with chronological clipped day bounds, complete sorted model identities, and server-owned Other aggregation. Regenerate Go and TypeScript/Connect Query output from the canonical schema and preserve encoded response-size limits.

- OpenCode `turn-finished` requires original completed user and finalized-assistant usage evidence in addition to current execution/sequence checks. Preserve terminal inbox receipts, independent cleanup/job reporting and authoritative late Stop/recovery state. Terminal publication alone cannot enable continuation or claim process closure.

- OpenCode original question/permission request JSON uses a disjoint `opencode` payload, original `que_`/`per_` identities and observed tool-part/assistant/call ownership. Preserve ordered matrices without synthetic question IDs, optional native flags and exact permission scope/metadata. Commit original request uniqueness and unread inbox atomically, preserve independent read state and block tool/root completion with pending requests. Proposal publication cannot authorize Codex response encoding, response delivery or public execution. The separate direct-response union uses original ordered answer matrices and one-request `once` permissions through existing response/claim RPCs; bounded native reply evidence binds proposal/event/request/body digest to the current response claim and confirmed exact-body HTTP delivery before independent native closure. Keep reply content out of metadata, and reject missing/mixed proofs or unsupported cascade policies.

## Storage
All results stay on the local machine. New stores do not create browser/pairing tables. Existing tables and results are preserved but legacy credentials are never consulted. The deprecated Pair RPC remains a wire-compatible tombstone returning Unimplemented (pairing-removed), without side effects.

## Security
Bind 127.0.0.1 only and validate Host exactly against the configured port. Every RPC requires POST, Origin exactly `http://127.0.0.1:<api_port>`, and exactly one `X-Ach-Api-Version: 1` header. Missing, null, duplicate and other origins/versions fail before dispatch. Version rejection retains the `incompatible-version` diagnostic in a Connect error envelope so stale clients can render recovery guidance. There are no remote/development CORS grants or authentication exceptions. Static navigation is GET/HEAD and requires the same Host. Local processes are trusted; this is browser-origin isolation rather than per-browser or per-OS-user authentication. File reads remain scoped by stored execution/evidence IDs.

## Logging
Stable error codes and correlation IDs; never log Authorization, pairing codes, request secrets or raw reports.

## Build and Test
Buf format/lint/breaking/freshness, Go/TypeScript serialization and transport tests, same-origin, Host, method, version-header and retired-pairing tests. Generator filters keep DevHud and ach client outputs separate.

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

`Run.check_count` is an additive optional uint32 field set on list and detail responses. `ListRuns` returns execution metadata and that total without check arrays or diagnostics; check commands, states, reports and diagnostics belong to `GetRun`. Thus a page does not grow with the number of checks. Older responses without the field remain readable by clients using the legacy checks-array length.

`ListRepositories` pages across repository/worktree ID pairs, with a default and maximum of 50 worktrees per page. Requests accept an opaque bounded v1 cursor and limit; responses include `next_cursor`. A repository may span pages. Display names, paths and branch labels are valid UTF-8 capped at 4 KiB (with an ellipsis when shortened); stable IDs and raw registry paths are unchanged. Only the selected page is probed for current availability/branch. Clients merge pages by repository and worktree ID; an older response without a cursor remains one complete page.

`ListBranches` accepts additive `cursor` and `limit` fields and returns `next_cursor`. Default/maximum page size is 50 refs with a 128 KiB raw record budget (below 1 MiB after JSON escaping). Git records are streamed, capped at 64 KiB each; oversized records fail with a recoverable Git diagnostic. Opaque lexical cursors bind the canonical worktree and last raw ref, including non-UTF-8 bytes, and are capped at 128 KiB. This live listing is not a snapshot: refs inserted before the cursor appear on refresh. The reader is cancelled and reaped after a partial page. Older responses without a cursor remain complete pages.

ListCommits retains its offset-based 100-commit pages. Display subjects are valid UTF-8 capped at 4 KiB and end with an ellipsis when shortened; commit and parent IDs remain exact. A parent header above 16 KiB is rejected rather than partially represented. Streaming happens before transport projection, keeping each escaped page below 5 MiB.

Branch.id and Worktree.branch_id are additive opaque worktree-scoped identities preserving raw branch bytes. ListCommits, GetChanges and ListRuns accept branch_id instead of their legacy ref/branch string (mutually exclusive). IDs are bounded, versioned and scope-validated before lookup; run filters decode exact historical bytes without requiring an available checkout. Empty identities preserve existing named/detached behavior. Display names never serve as identity when an ID is present.

Source discovery for ListBranches, ListCommits and GetChanges uses the incoming request context, including registered-worktree lookup and Git metadata discovery. Abandoned discovery processes are terminated and reaped; cancellation and deadline expiry return Connect Canceled and DeadlineExceeded respectively without being rewritten as worktree-unavailable.

The same context and status preservation extend through post-discovery commit/config/default-base/merge-base resolution and streamed branch/history/diff reads. After reader cleanup and child reaping, caller cancellation wins over translated Git/parse/unavailable errors. Stopping a bounded branch page internally does not produce a caller cancellation.

Source-RPC cancellation regression fixtures use an isolated loopback connection for the stalled test Git process readiness barrier. This avoids Windows rename/read sharing violations without weakening canceled/deadline status or process-reaping assertions. Production source operations retain their existing local Git behavior.
