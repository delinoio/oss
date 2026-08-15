# protos-devhud-v1-contract

## Scope

`protos/devhud/v1` is the implemented versioned protobuf/Connect RPC contract. It owns wire types, service names, stable enum identifiers, compatibility rules, representative serialization fixtures, and generated Go message/Connect bindings.

## Runtime and Language

Protocol definitions use proto3, English names/comments, package `devhud.v1`, and Go import path `github.com/delinoio/oss/protos/devhud/v1`. The directory is not a standalone runtime and has no development server port.

## Users and Operators

The Go API, DevHud app, administrator SPA, generated TypeScript client, CI compatibility checks, and self-hosting operators.

## Interfaces and Contracts

Define Bootstrap, Settings, Upload, Account, Diagnostics, and Admin services with the following stable RPC identifiers: `BootstrapService.GetBootstrap`; `SettingsService.GetSettings`/`ReplaceSettings`; `UploadService.CreateUpload`/`FinalizeUpload`/`ListUploads`/`DeleteUpload`; `AccountService.GetAccount`/`DeleteAccount`/`RestoreAccount`; `DiagnosticsService.SubmitCrashReport`; and `AdminService.ListUsers`/`SetUserBlocked`/`GetUserUsage`/`ListUploads`/`QuarantineUpload`/`DeleteUpload`/`ListAuditEvents`. Stable values include project `devhud`, mini-apps `realqa`/`deck`, capture action IDs, `devhud-admin`, protocol/API version, and typed Connect error semantics. Bootstrap capabilities are static compatibility declarations, never remote feature flags. `AdminService.ListUsers`, `AdminService.ListUploads`, `AdminService.ListAuditEvents`, and user `UploadService.ListUploads` use the shared bounded page request (`page_size`, default 50, maximum 100; `page_token` opaque and scoped to the authenticated user plus query/filter parameters) and return an opaque `next_page_token`; results are ordered by `created_at` descending with UUID descending as the tie-breaker, and an empty continuation token marks the end. `AdminService.ListUsers` accepts an optional query normalized with Unicode NFC, trimmed, and case-folded for case-insensitive prefix matching against display name, email, and external Logto subject; the normalized query is part of page-token scope.

Service-owned identifiers use the `UuidV7` wrapper with canonical lowercase RFC 9562 UUID v7 values; Logto subjects remain external strings. Bootstrap uses explicit `desktop`, `ios`, `android`, and `admin` public-client fields rather than an extensible credential map. Capability enums declare support for settings synchronization, official uploads, account recovery, crash reports, and administration; they describe static protocol compatibility only.

Settings are schema-versioned full snapshots using `google.protobuf.Struct`, monotonic `uint64` revisions, and expected-revision replacement. Expected revision zero creates the initial snapshot; stale writes return `Aborted` with `SettingsRevisionConflict`, including the latest server snapshot for an explicit diff/reapply flow. Admin request and response graphs must never reference the settings snapshot or `google.protobuf.Struct`.

Protocols must not include secrets, PATs, R2 keys, DOM, screenshot bytes, Deck results, agent output, or local paths. `GetBootstrap` is unauthenticated. Settings, uploads, and diagnostics require an authenticated, unblocked user; user upload listing returns only actor-owned records and user upload deletion requires ownership; account methods require authenticated ownership, with `RestoreAccount` limited to the 30-day recovery window; admin methods require the `devhud-admin` role. Administrative blocking is distinct from the deletion-state block: `RestoreAccount` clears only the latter and never changes `SetUserBlocked` state. Bootstrap preserves the native `devhud://auth/callback` and carries the exact deployment-configured admin redirect URI. The admin SPA uses Authorization Code with PKCE and validates state/nonce before role-gated RPCs; its development redirect is `http://localhost:46306/auth/callback` and its embedded redirect is the API origin's `/admin/auth/callback` path.

`CreateUpload` uses an explicit ID-presence state machine: neither `submission_id` nor `upload_group_id` creates the submission and first group; `submission_id` alone creates a later group; both IDs create an upload in that existing owned group; `upload_group_id` without `submission_id` is invalid. It atomically reserves signed-URL issuance quota before returning a typed PUT URL/header response and reports the reservation needed by finalization; failed issuance does not consume the reservation. `FinalizeUpload` is the validation boundary after direct R2 upload: it revalidates authentication, block status, ownership, and the exact upload/submission/staging-key/upload-group/reservation binding; verifies declared and observed size, the exact 32 raw-byte checksum, PNG content type/signature, safe raster dimensions, ETag, and staging version; validates the signed-URL reservation without charging that quota again; atomically rechecks or reserves submission image-count, rolling-byte, and stored-byte quotas; rejects replay; and removes invalid staging objects before returning its typed result. Deletion and quarantine replace the origin object before purging or revalidating its public CDN URL, and are not effective while the original remains retrievable from a cache. Restore and final purge are mutually exclusive atomic account-state transitions.

### Stable Connect error mapping

| Connect code | Stable condition | Safe detail |
| --- | --- | --- |
| `Unauthenticated` | Missing, expired, invalid, or wrong-audience Logto credential | No credential-bearing detail |
| `PermissionDenied` | Administrative block, pending-deletion block, ownership failure, or missing `devhud-admin` role | `PermissionDeniedDetail` |
| `Aborted` | `ReplaceSettings` expected revision differs from the current revision | `SettingsRevisionConflict` |
| `ResourceExhausted` | Object, submission, rolling-byte, stored-byte, signed-URL, or public-GET quota exhaustion | `QuotaExceededDetail` |
| `FailedPrecondition` | Invalid upload lifecycle/binding/content/replay/staging state or expired account recovery | `UploadPreconditionDetail` or `AccountPreconditionDetail` |

Unknown resource IDs must not be distinguished from unauthorized resources. Every success and error carries a UUID v7 `x-devhud-correlation-id` response header; correlation IDs are transport metadata and are not duplicated into every response body.

## Storage

The protocol defines representations only. Persistence and retention belong to the API contract; generated clients must not add a second storage or secret policy.

## Security

Upload wire types carry the server-owned `submission_id`, upload-group binding, expected checksum as exactly 32 raw bytes, and staging object version/generation. A submission owns at most 10 finalized images across its groups; finalization rejects dimensions above 4096×4096 or 16,777,216 total pixels before raster decoding and conditionally promotes only the recorded staging version. The R2 checksum header is standard Base64 of the raw checksum bytes.

Keep credentials out of messages and logs. Mark authenticated/admin methods and upload-finalization invariants explicitly. Preserve compatibility without weakening authorization, quota, redaction, or deletion rules.

## Logging

Protocol diagnostics may expose typed safe error fields and UUID v7 correlation IDs only; the transport returns the correlation ID in the `x-devhud-correlation-id` response header, including errors, and never serializes credentials or sensitive content into error details.

## Build and Test

Buf v2 `STANDARD` lint and `FILE` breaking policy are canonical. `pnpm proto:generate` invokes repository-pinned local Go, Protobuf-ES, and Connect-Query generators; the Go and TypeScript output is committed. `pnpm proto:check-generated` removes only known generator-owned files, regenerates, and rejects modified, missing, orphaned, or untracked output. `pnpm proto:breaking` compares against Git `main` by default and accepts `BUF_BREAKING_AGAINST` for CI base selection.

CI runs schema lint, breaking-change checks, generated freshness, Go binding/fixture tests, TypeScript typecheck/build/tests, service descriptor conformance, enum/path consistency, prohibited-field checks, Admin/settings isolation, and absence of REST annotations. Any wire change updates this contract, the server, client, and app contracts together. Removed fields and enum values must be reserved rather than reused.

## Dependencies and Integrations

Consumed by `servers/devhud-api` and `packages/devhud-api-client`; used by the app/admin surfaces. Connect RPC is the business transport; REST remains limited to the server contract’s operational endpoints.

## Change Triggers

Update the project index, server/client/admin/app contracts, `protos/AGENTS.md`, and CI generation/compatibility rules for any service, message, enum, revision, error, or field change.

## References

- [DevHud project index](project-devhud.md)
- [Server contract](servers-devhud-api-contract.md)
- [Client contract](packages-devhud-api-client-contract.md)
- [Repository defaults](repository-defaults.md)
