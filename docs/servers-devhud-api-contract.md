# servers-devhud-api-contract

## Scope

`servers/devhud-api` is the planned stateless Go API backed by PostgreSQL, Cloudflare R2, and external Logto. It is not implemented. Its fixed development port is `46307`. A separate planned `devhud-api-sweeper` Go worker/OCI job performs timed cleanup and is not part of request-serving API replicas.

## Runtime and Language

Go service using `log/slog`, Connect RPC, migrations, health/readiness endpoints, OpenTelemetry-compatible traces/metrics, Prometheus metrics, and a non-root OCI image. REST is limited to liveness/readiness, embedded admin assets, public CDN behavior, and the signed updater manifest route at `/updates/{channel}/{platform}/{architecture}.json`; the API deployment serves that route from the fixed `https://devhud.api.delino.io` origin.

## Users and Operators

DevHud guest/authenticated clients, self-hosting operators, `devhud-admin` operators, and deployment/release operators.

## Interfaces and Contracts

Services in package `devhud.v1`: `BootstrapService.GetBootstrap`; `SettingsService.GetSettings`/`ReplaceSettings`; `UploadService.CreateUpload`/`FinalizeUpload`/`ListUploads`/`DeleteUpload`; `AccountService.GetAccount`/`DeleteAccount`/`RestoreAccount`; `DiagnosticsService.SubmitCrashReport`; and `AdminService.ListUsers`/`SetUserBlocked`/`GetUserUsage`/`ListUploads`/`QuarantineUpload`/`DeleteUpload`/`ListAuditEvents`. `GetBootstrap` is unauthenticated. Settings, uploads, and diagnostics require an authenticated, unblocked user; user `ListUploads` returns only actor-owned records and user `DeleteUpload` requires ownership; account methods require authenticated ownership, with `RestoreAccount` limited to the 30-day recovery window; admin methods require the `devhud-admin` role. Bootstrap supplies protocol/API version, Logto issuer/audience, the native callback URI `devhud://auth/callback`, public client IDs keyed by `desktop`, `ios`, `android`, and `admin`, the deployment-configured exact admin redirect URI, public asset base URL, static capabilities, and upload limits; it is not a remote feature-flag channel. The admin SPA uses Authorization Code with PKCE and state/nonce validation: development uses `http://localhost:46306/auth/callback`, while the embedded deployment uses the API origin's `/admin/auth/callback` path.

The server implements the generated Connect-Go handlers directly; no duplicate REST business API is permitted. Service-owned identifiers are canonical lowercase RFC 9562 UUID v7 values and schema versions are unsigned. Every successful response contains `ResponseMetadata` and every Connect error carries `ErrorMetadata`; their UUID-v7 correlation ID matches the exposed `x-devhud-correlation-id` header. Bootstrap uses explicit platform client fields and explicit native/admin redirects, and its capability enum values are static compatibility declarations.

Use `Unauthenticated`, `PermissionDenied`, `Aborted`, `ResourceExhausted`, and `FailedPrecondition` for the contracted failure classes. `RestoreAccount` requires authenticated ownership, is idempotent during the 30-day recovery window, cancels only the pending deletion-state block, and never clears an independent `AdminService.SetUserBlocked` administrative block; calls after the window return `FailedPrecondition`. An administrative block continues to gate all operations that require an unblocked user. Attach a UUID v7 correlation ID to every response/log context and return it in the `x-devhud-correlation-id` response header. The API never proxies GitHub or R2 upload bodies, stores Deck results, polls Decks, receives webhooks, or acts as a GitHub credential broker.

Settings snapshots are at most 1 MiB of RFC 8785 canonical UTF-8 JSON bytes. No stored snapshot is represented by absence; expected revision zero creates revision one, every matching replacement increments the revision exactly once, and stale writes return `Aborted` with `SettingsRevisionConflict`. List RPCs default to 50 records, reject page sizes above 100, reject opaque tokens above 2 KiB, scope tokens to the authenticated actor plus normalized filters, and order newest-first with UUID descending as the tie-breaker. Invalid page input returns `InvalidArgument`.

The API CORS allowlist is exact: `http://localhost:46305`, `http://127.0.0.1:46305`, `http://localhost:46306`, `http://127.0.0.1:46306`, and the pinned Tauri shell origin `http://tauri.localhost`. Connect requests permit only `POST`, `GET`, and `OPTIONS` as applicable; preflight permits `Content-Type`, `Connect-Protocol-Version`, `Connect-Timeout-Ms`, and `Authorization`, exposes `x-devhud-correlation-id`, and otherwise exposes only required Connect response metadata. Origins, methods, and headers are not wildcarded. The private R2 staging bucket uses a separate exact CORS policy with the same five origins, `PUT` and `OPTIONS` methods, `Content-Type` and `x-amz-checksum-sha256` request headers, and `ETag` response exposure; it has no wildcard origin, method, or header.

## Storage

Persist users, schema-versioned settings snapshots/revisions, official uploads/tombstones, upload groups, submission counters, blocks/admin actions, audit records, and opt-in crash reports. Use PostgreSQL for metadata and private R2 staging/public assets. Signed upload URLs are one-time; staging expires after 24 hours. The first `CreateUpload` without an `upload_group_id` creates and returns a server-owned UUID v7 submission and its first upload group; subsequent groups, `CreateUpload`, and `FinalizeUpload` calls must provide both IDs. Enforce 50 MiB/object, 10 finalized images per submission across all groups, 1 GiB rolling-24-hour, 20 GiB stored, 120 signed URLs/rolling-hour, and 300 public GETs/IP/minute. `ListUploads` is bounded with the shared page size/token/order contract and is filtered to the authenticated owner. `CreateUpload` atomically reserves the signed-URL issuance quota before issuing a URL and rolls back the reservation if issuance fails. `FinalizeUpload` validates that reservation without charging the signed-URL quota again; it revalidates authentication, block status, ownership, exact staging-key, submission, and upload-group binding, size, checksum, allowed image content, PNG signature, safe raster dimensions, and every other applicable quota. It must atomically recheck or reserve the submission image-count, rolling-byte, and stored-byte quota state so concurrent finalizations and multiple groups cannot bypass limits; quota failures return `ResourceExhausted`. It rejects replay and deletes invalid staging objects. Deleted/quarantined objects must first be replaced at the origin with a non-sensitive localized removal PNG, then have the public CDN purged or revalidated for the stable URL; the operation is not considered effective deletion while a cached original remains retrievable. SHA-256 values are stored and carried by protobuf as exactly 32 raw bytes; the R2 `x-amz-checksum-sha256` value is standard Base64 of those same bytes.

`CreateUploadTarget` explicitly distinguishes a new submission and first group, a new group under an owned submission, and an existing owned submission/group. The server issues and persists the upload, submission, group, quota-reservation, staging key, nonzero staging generation, expected size/checksum, expiry, and future public URL bindings. `FinalizeUpload` repeats the reservation, generation, checksum, size, and observed ETag and may promote only the exact staged generation. These bindings are immutable after issuance.

Retain request logs and crash reports 30 days; retain pseudonymized security/admin audit events at most 180 days. Account deletion blocks immediately, permits 30-day recovery, then purges synchronized settings, users, reports, official image bytes, and official-upload/tombstone metadata. Restore and final purge serialize through an atomic account-state transition or row lock: purge claims the account before destructive work, and restore cannot succeed after that claim. Owner IDs, object keys, checksums, and timestamps are deleted or irreversibly pseudonymized; any retained security/admin audit subset follows the explicit 180-day limit. The dedicated sweeper processes expired staging and recovery-complete accounts idempotently in bounded batches, retries safe failures, and uses a PostgreSQL lease or advisory lock for multi-instance coordination. Release the sweeper as a separate signed/provenanced non-root OCI image alongside the API image.

## Security

Upload hardening: the first upload request creates a server-owned UUID v7 submission with its first group; later groups and finalizations carry both IDs and the submission owns the maximum of 10 finalized images across all groups. Signed URLs bind the expected 32-byte SHA-256 checksum and versioned staging object. Finalization checks PNG IHDR dimensions before decoding, rejecting width or height above 4096 or total pixels above 16,777,216, and promotes only the recorded version with conditional ETag/checksum validation.

Validate Logto credentials and admin role. Never persist or log PATs, R2 secrets, Logto tokens, DOM, screenshots outside upload ownership, issue bodies, agent output, or local paths. Official uploads require authenticated, unblocked users. BYO R2 credentials never reach this service.

Crash reports accept a schema version and typed build, platform, component, severity, code, and correlation fields. Build/code identifier strings are capped at 256 UTF-8 bytes; user-previewed redacted diagnostic summary is capped at 4 KiB and stack trace at 32 KiB. Credential patterns and local-path content are invalid in every crash string. Administrator responses and error details use only the schema's admin-safe user, usage, metadata-only upload, and audit projections; their message graph cannot reach the user-facing upload object, settings bodies, secrets, DOM, screenshots, public or signed asset locators, Deck results, agent output, or local paths.

## Logging

Use `log/slog` structured logs with correlation IDs and redaction. Provide safe diagnostics, health/readiness, traces, and metrics without product analytics. Crash reporting is opt-in and user-previewed.

## Build and Test

Validate Go format/vet/unit/integration/migration/API conformance tests, Connect compatibility, exact CORS allowlist and preflight behavior including correlation-header exposure, owner-filtered user upload listing/deletion, bounded user-upload pagination, submission-scoped quota enforcement across multiple groups and concurrent finalization, raw-byte checksum/Base64 R2 conversion, concurrent CreateUpload reservation and issuance rollback, finalization non-double-charging, auth/blocking, revision conflicts, restore-versus-purge races, idempotent multi-instance sweeper execution, deletion/retention, admin audit behavior, origin replacement before CDN purge/revalidation for user deletion, admin quarantine, and account purge, non-root API and sweeper OCI execution, SBOM/signature/provenance, and fixed port `46307` conflict failure.

## Dependencies and Integrations

Consumes `protos/devhud/v1`; serves `apps/devhud-admin`; is consumed by `apps/devhud` through `packages/devhud-api-client`. Requires PostgreSQL, Logto, R2/public asset hosting, and deployment secrets. Official hosting platform remains unspecified.

## Change Triggers

Update the project index, protocol/client/admin contracts, root and server `AGENTS.md`, migrations, CI, and release contracts when RPCs, storage, retention, security, quotas, or deployment requirements change.

## References

- [DevHud project index](project-devhud.md)
- [Protocol contract](protos-devhud-v1-contract.md)
- [Client contract](packages-devhud-api-client-contract.md)
- [Admin contract](apps-devhud-admin-contract.md)
- [Repository defaults](repository-defaults.md)
