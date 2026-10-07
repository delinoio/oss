- DeliDev remote-first repositories reserve System 37 `REMOTE_REPOSITORIES_V1` and Worker 19 `REMOTE_WORKSPACE_CLONE_V1` on main before activation. Preserve System 31/32 and Worker 18 as the separate immediate-clone/listing boundary; reservations alone grant no support or Git authority.

- DeliDev Agent Worker source routes reserve System capability 36 and SaveAgentWorkerRequest.route_models field 5 before dependent implementation. Preserve capability 35 and every existing allocation; reservations alone grant no support.

### Instructions for `protos/`

- Follow root `AGENTS.md` and the owning project/domain contracts.

- Keep protobuf package names, enum identifiers, compatibility, and generated-client rules stable and documented before implementation.

- Write schemas and comments in English.

### Scope in This Domain

- `protos/devhud/v1`: implemented versioned DevHud Connect RPC schemas.

- `protos/gen/go/devhud/v1`: committed, tool-owned Go messages and Connect server bindings generated from `protos/devhud/v1`.

### DevHud Rules

- Keep package `devhud.v1`, UUID v7 service-owned identifiers, typed Connect errors, revision conflicts, and the service/RPC list aligned with `docs/protos-devhud-v1-contract.md`.

- Administrative and user upload-list RPCs use the shared bounded page-size, opaque-token, deterministic-order pagination contract documented in `docs/protos-devhud-v1-contract.md`; user results are owner-scoped and user-search tokens include normalized query scope.

- Keep the explicit AdminService RPC names and FinalizeUpload validation boundary aligned with the protocol contract; schemas must not permit direct-upload callers to bypass ownership, quota, content, replay, or staging cleanup checks. Keep `GetBootstrap` unauthenticated, publish platform-keyed Logto client IDs including `admin` and its exact redirect URI, and preserve the per-RPC auth/role matrix.

- Upload messages also carry the server-owned submission ID, expected checksum as 32 raw bytes, and staging version/generation; finalization enforces the cross-group 10-image cap and 4096×4096/16,777,216-pixel pre-decode limits. The R2 header uses standard Base64 of those bytes, and API correlation IDs use the exposed `x-devhud-correlation-id` response header.

- Settings snapshots are at most 1 MiB of RFC 8785 canonical JSON bytes and use exact monotonic revisions: expected revision zero creates revision one, each successful replacement increments once, and stale writes return the typed conflict detail. Successful responses and errors carry correlation metadata mirrored to `x-devhud-correlation-id`.

- `CreateUploadTarget` remains an explicit oneof for new submission, new group, or existing group ownership. Reservation IDs, immutable nonzero staging generations, expected checksum/size, and observed ETag are required finalization bindings.

- Crash diagnostics are typed, user-previewed, and redacted, with an explicit browser platform, browser-only unknown architecture, exact native Tauri/browser-empty and desktop CEF/mobile-browser-empty revisions, 256-byte build/code identifier ceilings, a 4 KiB summary, and a 32 KiB stack ceiling. Administrator mutation reasons are required, capped at 4 KiB of well-formed UTF-8, and reject credential and local-path patterns before persistence; audit responses expose only previously validated reasons. Administrator message graphs use a metadata-only upload projection and must not reach settings bodies, secrets, DOM, screenshots, public or signed asset locators, Deck results, agent output, or local paths.

- CI must validate schema formatting, lint, compatibility, and generated-client freshness through package/root commands and the committed Turbo binary. Generated Go and TypeScript outputs are deterministic cacheable products and must never be edited by hand.

### async-commit-hook

- `protos/async_commit_hook/v1` owns package `async_commit_hook.v1`; follow `docs/protos-async-commit-hook-v1-contract.md`. Generate Go bindings and the isolated ach TypeScript client reproducibly. No arbitrary command or filesystem endpoint.

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

## Scoped DeliDev ownership

Read the relevant owner before changing its behavior, including cross-domain consumers:

- `protos/delidev/AGENTS.md`

Record implementation status and validation results in pull requests, issues and CI logs/artifacts under the root DeliDev validation policy. Do not add repository evidence documents. Update instructions only when their rules or ownership change, not merely to record another validation run.

- DeliDev BrowserService follows `docs/cmds-delidev-browser-contract.md`: use its own typed profile/state/capability declarations, exact original mutation identities and metadata-only payloads. Preserve all existing numeric allocations and migration reservations; generated compatibility exports must reproduce.

- Remote repositories activate the main-established System 37 / Worker 19 allocations; retain System 31/32 and Worker 18 immediate Local Clone/metadata meanings. Source-kind enums and pinned URL JSON remain additive and historical omitted fields are never rewritten. Regenerate bindings from reconciled schemas.

- Native Claude subscriptions activate only the main-established PR #1612 allocation closure (System 38 / Worker 20). Keep existing Codex fields and independent capabilities unchanged, generated parity, write-only once-only code and original Worker ownership under the subscription/protocol contracts. No database migration.
