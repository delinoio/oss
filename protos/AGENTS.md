### Instructions for `protos/`

- Follow root `AGENTS.md` and the owning project/domain contracts.
- Keep protobuf package names, enum identifiers, compatibility, and generated-client rules stable and documented before implementation.
- Write schemas and comments in English.

### Scope in This Domain

- `protos/delidev/v1` and `protos/gen/go/delidev/v1`: DeliDev authenticated Connect schemas and tool-owned Go bindings; follow `docs/protos-delidev-v1-contract.md`. Account credentials are bounded write-only inputs; an accepted disconnect with failed cleanup returns the current account and a sanitized typed cleanup problem, preserving its original retry identity. Account validation returns the accepted sanitized non-inference observation with current account metadata; replay cannot repeat provider requests or grant readiness from an old connection.
- DeliDev `ProviderService` is owner/client-only: catalog discovery returns an accepted observation and current account metadata; search cursors bind filters and the catalog event epoch; resolution returns canonical identities without treating display preferences as execution permissions. Model provenance and account catalog observations remain server-owned.
- DeliDev `SessionService` is owner/client-only and follows `docs/cmds-delidev-sessions-contract.md`: creation/input acceptance is durable but does not establish native execution; reference-only receipt retries return current records. Preserve queue mode/order, expected revisions, removal tombstones and signed scope-bound pages. Clients cannot write lifecycle/delivery state through configuration, and Restore never resumes dispatch.
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

- DeliDev workspace preparation uses the current `SessionChange.workspace_job` and revision-checked `PrepareSessionWorkspace`. Worker cancellation is an immutable-job-scoped control, including pre-cancellation on reconnect; it must never mutate claimed assignment revisions or cancel unrelated work. Preserve session/job atomic publication, unknown native ownership and pending Archive visibility until completion is proven.

- DeliDev `RecoverSessionWorkspace` is an owner/client revision-checked operation with explicit partial-cleanup selection and current `SessionChange.recovery_job`. Recovery jobs bind the original claimed identity/revision/digest and cannot create execution authority. Keep malformed/mismatched proof uncertain and publish confirmed recovery plus the original preparation outcome atomically.

- DeliDev `RegisterExecution` is restricted to the owning current Worker and exact claimed job revision. Accept a SHA-256 digest only; never send raw execution tokens or upstream keys in RPC replies, jobs or receipts. Exact retries must recheck live ownership and server epoch before returning the fixed relative proxy path.

- DeliDev `PublishExecution` accepts closed typed core events only from the owning Worker and exact claimed job revision. Preserve contiguous sequences, immutable native identity bindings, reference-only receipts and atomic acceptance/transcript publication. Replaying an event cannot send native input, repeat queue accounting, clear recovery or claim owned cleanup. Never carry raw native extensions or diagnostics in this envelope.

- DeliDev execution usage/notice publications carry only typed counters with an immutable observation ID or a closed generic notice kind. Derive attribution from the accepted execution assignment, preserve nullable native fields, and never turn cumulative samples into charges or native diagnostics into product payloads.

- DeliDev `WatchWork.question_response` is a metadata-only control for its active immutable assignment, independent of job order and mutually exclusive with other stream forms. `ClaimQuestionResponse` is owning-Worker-only and binds the original interaction revision, response, job, instance and device. Return the non-secret original response only after current authority validation, including on reference-only receipt replay. A claim is not native delivery or semantic acceptance; closure/lost ownership preserves uncertainty. Never carry answer content in stream controls or claim receipts.

- DeliDev `InteractionService.RespondQuestion` is owner/paired-client-only. Bind the response UUID to the mutation request ID and validate the original question revision/content plus current execution authority before queued acceptance. Reference-only receipt identity includes the actor and canonical answers; exact retries return current interaction state after closure without requeuing or native resend. Workers cannot invoke owner acceptance, secret questions cannot use ordinary answer fields, and native encoded-size bounds apply before persistence.

- DeliDev `InboxService` is owner/paired-client-only. Get/list join current original interaction and session state in one authorized transaction. List cursors bind all selection filters and the inbox event epoch, with complete joined-document byte bounds. Read-state mutations use the inbox entry revision, explicit closed read/unread enum and actor-bound reference-only receipts; replay returns current state without reapplying old marks. Reading or marking cannot change question revisions, response authority or execution, and Worker credentials cannot invoke these operations.
