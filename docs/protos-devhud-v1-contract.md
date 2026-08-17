# protos-devhud-v1-contract

## Scope

`protos/devhud/v1` is the implemented versioned protobuf/Connect RPC contract. It owns wire types, service names, stable enum identifiers, compatibility rules, and generated-source inputs. Committed Go messages and Connect bindings are generated under `protos/gen/go/devhud/v1`; handwritten changes to generated files are prohibited.

## Runtime and Language

Protocol definitions use English names/comments and package `devhud.v1`. The directory is not a runtime package and has no development server port.

## Users and Operators

The Go API, DevHud app, administrator SPA, generated TypeScript client, CI compatibility checks, and self-hosting operators.

## Interfaces and Contracts

Define Bootstrap, Settings, Upload, Account, Diagnostics, and Admin services with the following stable RPC identifiers: `BootstrapService.GetBootstrap`; `SettingsService.GetSettings`/`ReplaceSettings`; `UploadService.CreateUpload`/`FinalizeUpload`/`ListUploads`/`DeleteUpload`; `AccountService.GetAccount`/`DeleteAccount`/`RestoreAccount`; `DiagnosticsService.SubmitCrashReport`; and `AdminService.ListUsers`/`SetUserBlocked`/`GetUserUsage`/`ListUploads`/`QuarantineUpload`/`DeleteUpload`/`ListAuditEvents`. Stable values include project `devhud`, mini-apps `realqa`/`deck`, capture action IDs, `devhud-admin`, protocol/API version, and typed Connect error semantics. Bootstrap capabilities are static compatibility declarations, never remote feature flags. `AdminService.ListUsers`, `AdminService.ListUploads`, `AdminService.ListAuditEvents`, and user `UploadService.ListUploads` use the shared bounded page request (`page_size`, default 50, maximum 100; `page_token` opaque and scoped to the authenticated user plus query/filter parameters) and return an opaque `next_page_token`; results are ordered by `created_at` descending with UUID descending as the tie-breaker, and an empty continuation token marks the end. `AdminService.ListUsers` accepts an optional query normalized with Unicode NFC, trimmed, and case-folded for case-insensitive prefix matching against display name, email, and external Logto subject; the normalized query is part of page-token scope.

The current server consumes these generated bindings and registers Bootstrap, Settings, Upload, Account, and Diagnostics. Admin remains a wire-stable planned service and is deliberately not registered as a placeholder handler; upload administration is exposed only through internal hooks until AdminService is implemented.

Missing or invalid Logto credentials return `Unauthenticated`; transient identity-provider verification failures return `Unavailable` with the same correlation metadata as other Connect errors, so clients must not treat an upstream outage as credential invalidation.

Settings are schema-versioned full snapshots with monotonic revisions and expected-revision replacement; stale writes return `Aborted` and a conflict payload. Service identifiers use UUID v7; Logto and GitHub identifiers are external IDs. Protocols must not include secrets, PATs, R2 keys, DOM, screenshots, Deck results, agent output, or local paths. Administrator mutation reasons are required non-blank, well-formed Unicode strings capped at 4 KiB of UTF-8; credential and local-path patterns are invalid, validation occurs before persistence, and audit responses expose only previously validated reasons. Administrator upload responses use a direct metadata-only `AdminUpload` projection that cannot reach the user-facing `Upload`, `UploadReservation`, or their public/signed asset locators. `GetBootstrap` is unauthenticated. Settings, uploads, and diagnostics require an authenticated, unblocked user; user upload listing returns only actor-owned records and user upload deletion requires ownership; account methods require authenticated ownership, with `RestoreAccount` limited to the 30-day recovery window; admin methods require the `devhud-admin` role. Administrative blocking is distinct from the deletion-state block: `RestoreAccount` clears only the latter and never changes `SetUserBlocked` state. Bootstrap uses stable public client-ID keys `desktop`, `ios`, `android`, and `admin`, preserves the native `devhud://auth/callback`, and carries the exact deployment-configured admin redirect URI. The admin SPA uses Authorization Code with PKCE and validates state/nonce before role-gated RPCs; its development redirect is `http://localhost:46306/auth/callback` and its embedded redirect is the API origin's `/admin/auth/callback` path. Missing credentials return `Unauthenticated`, blocked users or missing roles return `PermissionDenied`, invalid administrator reasons return `InvalidArgument`, quota failures return `ResourceExhausted`, and invalid upload finalization returns `FailedPrecondition`. `CreateUpload` requires an explicit `CreateUploadTarget`: `new_submission` carries no IDs and creates a server-owned UUID v7 submission and first group, `new_group` carries the owned submission ID and creates a group, and `existing_group` carries both owned IDs for another upload in that group. An absent request target or unset target oneof is invalid, and `FinalizeUpload` repeats both IDs returned by the reservation. `CreateUpload` atomically reserves the signed-URL issuance quota before issuing a URL and reports the reservation needed by finalization; failed issuance does not consume the reservation. `FinalizeUpload` is the validation boundary after direct R2 upload: it revalidates authentication, block status, ownership, and the exact submission/staging-key/upload-group binding; verifies declared and observed size, the exact 32 raw-byte checksum, allowed image content type, PNG signature, and safe raster dimensions; validates the existing signed-URL reservation without charging that quota again; atomically rechecks or reserves the submission image-count, rolling-byte, and stored-byte quotas; rejects replay or already-finalized keys; and removes invalid staging objects before returning its typed result. Deletion and quarantine replace the origin object before purging or revalidating its public CDN URL, and are not effective while the original remains retrievable from a cache. Restore and final purge are mutually exclusive atomic account-state transitions.

Wire details are final for v1. `UuidV7` carries canonical lowercase RFC 9562 UUID-v7 text for every service-owned identifier. Every successful response contains `ResponseMetadata.correlation_id`; every Connect error carries `ErrorMetadata`, and both mirror `x-devhud-correlation-id`. Bootstrap protocol schema version is an unsigned integer and Logto client IDs use explicit `desktop`/`ios`/`android`/`admin` fields with explicit native/admin redirects. Capabilities are closed static compatibility enums and cannot act as feature flags. Settings bodies are at most 1 MiB of RFC 8785 canonical UTF-8 JSON bytes; absence means never stored, expected revision zero creates revision one, and each successful replacement increments exactly once. Administrator message graphs cannot reach a settings snapshot.

`CreateUploadTarget` is an explicit oneof: create a new submission and first group, create a new group under an owned submission, or use an existing owned submission/group. The response supplies upload, submission, group, and quota-reservation UUIDs; an immutable nonzero staging generation; the signed PUT URL; exact PNG/checksum headers; a 15-minute `expires_at`; an independent 24-hour `staging_expires_at`; and the future stable public URL. Finalization repeats those bindings, size, raw checksum, generation, and observed ETag. There is no request or response field capable of carrying an image body. List requests may filter by state/submission, and administrator requests may additionally filter by owner/group.

Diagnostics schema version 1 carries a required client UUID v7, up to 32 unique related UUID v7 values, a duration capped at 24 hours, armv7 as an explicit architecture, and an explicit browser platform alongside the native platforms. Native reports require an exact Tauri revision while browser reports require it to be empty; desktop reports require CEF and mobile/browser reports require it to be empty.

## Storage

The protocol defines representations only. Persistence and retention belong to the API contract; generated clients must not add a second storage or secret policy.

## Security

Upload wire types carry the server-owned `submission_id`, upload-group binding, expected checksum as exactly 32 raw bytes, and staging object version/generation. A submission owns at most 10 finalized images across its groups; finalization rejects dimensions above 4096×4096 or 16,777,216 total pixels before raster decoding and conditionally promotes only the recorded staging version. The R2 checksum header is standard Base64 of the raw checksum bytes.

Keep credentials out of messages and logs. Mark authenticated/admin methods and upload-finalization invariants explicitly. Preserve compatibility without weakening authorization, quota, redaction, or deletion rules.

## Logging

Protocol diagnostics may expose typed safe error fields and UUID v7 correlation IDs only; the transport returns the correlation ID in the `x-devhud-correlation-id` response header, including errors, and never serializes credentials or sensitive content into error details.

## Build and Test

Use Buf v2 `STANDARD` lint and `FILE` breaking policy. Generation pins Buf 1.72.0, protoc-gen-go 1.36.12, Connect-Go/protoc-gen-connect-go 1.20.0, Protobuf-ES 2.14.0, and protoc-gen-connect-query 2.3.1. Run schema formatting/lint, breaking-change checks, clean generated freshness, API conformance, enum/path consistency, Go/TypeScript serialization, pagination, forbidden-field, Connect Query export and React Query integration, and typed error-mapping tests in CI. Any wire change updates this contract, the server, client, and app contracts together.

## Dependencies and Integrations

Consumed by `servers/devhud-api` and `packages/devhud-api-client`; used by the app/admin surfaces. Connect RPC is the business transport; REST remains limited to the server contract’s operational endpoints.

## Change Triggers

Update the project index, server/client/admin/app contracts, `protos/AGENTS.md`, and CI generation/compatibility rules for any service, message, enum, revision, error, or field change.

## References

- [DevHud project index](project-devhud.md)
- [Server contract](servers-devhud-api-contract.md)
- [Client contract](packages-devhud-api-client-contract.md)
- [Repository defaults](repository-defaults.md)
