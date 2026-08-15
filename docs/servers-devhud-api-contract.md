# servers-devhud-api-contract

## Scope

`servers/devhud-api` is the planned stateless Go API backed by PostgreSQL, Cloudflare R2, and external Logto. It is not implemented. Its fixed development port is `46307`. A separate planned `devhud-api-sweeper` Go worker/OCI job performs timed cleanup and is not part of request-serving API replicas.

## Runtime and Language

Go service using `log/slog`, Connect RPC, migrations, health/readiness endpoints, OpenTelemetry-compatible traces/metrics, Prometheus metrics, and a non-root OCI image. REST is limited to liveness/readiness, embedded admin assets, and public CDN behavior.

## Users and Operators

DevHud guest/authenticated clients, self-hosting operators, `devhud-admin` operators, and deployment/release operators.

## Interfaces and Contracts

Services in package `devhud.v1`: `BootstrapService.GetBootstrap`; `SettingsService.GetSettings`/`ReplaceSettings`; `UploadService.CreateUpload`/`FinalizeUpload`/`ListUploads`/`DeleteUpload`; `AccountService.GetAccount`/`DeleteAccount`/`RestoreAccount`; `DiagnosticsService.SubmitCrashReport`; and `AdminService.ListUsers`/`SetUserBlocked`/`GetUserUsage`/`ListUploads`/`QuarantineUpload`/`DeleteUpload`/`ListAuditEvents`. `GetBootstrap` is unauthenticated. Settings, uploads, and diagnostics require an authenticated, unblocked user; account methods require authenticated ownership, with `RestoreAccount` limited to the 30-day recovery window; admin methods require the `devhud-admin` role. Bootstrap supplies protocol/API version, Logto issuer/audience, the exact callback URI `devhud://auth/callback`, public client IDs keyed by `desktop`, `ios`, and `android`, public asset base URL, static capabilities, and upload limits; it is not a remote feature-flag channel.

Use `Unauthenticated`, `PermissionDenied`, `Aborted`, `ResourceExhausted`, and `FailedPrecondition` for the contracted failure classes. `RestoreAccount` requires authenticated ownership, is idempotent during the 30-day recovery window, cancels pending purge, and removes the account block; calls after the window return `FailedPrecondition`. Attach a UUID v7 correlation ID to every response/log context. The API never proxies GitHub or R2 upload bodies, stores Deck results, polls Decks, receives webhooks, or acts as a GitHub credential broker.

The API CORS allowlist is exact: `http://localhost:46305`, `http://127.0.0.1:46305`, `http://localhost:46306`, `http://127.0.0.1:46306`, and the pinned Tauri shell origin `http://tauri.localhost`. Connect requests permit only `POST`, `GET`, and `OPTIONS` as applicable; preflight permits `Content-Type`, `Connect-Protocol-Version`, `Connect-Timeout-Ms`, and `Authorization`, and exposes only required Connect response metadata. Origins, methods, and headers are not wildcarded.

## Storage

Persist users, schema-versioned settings snapshots/revisions, official uploads/tombstones, upload groups, blocks/admin actions, audit records, and opt-in crash reports. Use PostgreSQL for metadata and private R2 staging/public assets. Signed upload URLs are one-time; staging expires after 24 hours. The first `CreateUpload` without an `upload_group_id` creates and returns a server-owned UUID v7 upload group bound to the authenticated user; subsequent `CreateUpload` and `FinalizeUpload` calls must provide that group. Enforce 50 MiB/object, 10 finalized images per upload group, 1 GiB rolling-24-hour, 20 GiB stored, 120 signed URLs/rolling-hour, and 300 public GETs/IP/minute. `CreateUpload` atomically reserves the signed-URL issuance quota before issuing a URL and rolls back the reservation if issuance fails. `FinalizeUpload` validates that reservation without charging the signed-URL quota again; it revalidates authentication, block status, ownership, exact staging-key and upload-group binding, size, checksum, allowed image content, PNG signature, safe raster dimensions, and every other applicable quota. It must atomically recheck or reserve the group image-count, rolling-byte, and stored-byte quota state so concurrent finalizations cannot bypass limits; quota failures return `ResourceExhausted`. It rejects replay and deletes invalid staging objects. Deleted/quarantined objects must first be replaced at the origin with a non-sensitive localized removal PNG, then have the public CDN purged or revalidated for the stable URL; the operation is not considered effective deletion while a cached original remains retrievable.

Retain request logs and crash reports 30 days; retain pseudonymized security/admin audit events at most 180 days. Account deletion blocks immediately, permits 30-day recovery, then purges synchronized settings, users, reports, official image bytes, and official-upload/tombstone metadata. Restore and final purge serialize through an atomic account-state transition or row lock: purge claims the account before destructive work, and restore cannot succeed after that claim. Owner IDs, object keys, checksums, and timestamps are deleted or irreversibly pseudonymized; any retained security/admin audit subset follows the explicit 180-day limit. The dedicated sweeper processes expired staging and recovery-complete accounts idempotently in bounded batches, retries safe failures, and uses a PostgreSQL lease or advisory lock for multi-instance coordination.

## Security

Validate Logto credentials and admin role. Never persist or log PATs, R2 secrets, Logto tokens, DOM, screenshots outside upload ownership, issue bodies, agent output, or local paths. Official uploads require authenticated, unblocked users. BYO R2 credentials never reach this service.

## Logging

Use `log/slog` structured logs with correlation IDs and redaction. Provide safe diagnostics, health/readiness, traces, and metrics without product analytics. Crash reporting is opt-in and user-previewed.

## Build and Test

Validate Go format/vet/unit/integration/migration/API conformance tests, Connect compatibility, exact CORS allowlist and preflight behavior, upload-group binding and quota validation, concurrent CreateUpload reservation and issuance rollback, finalization non-double-charging, auth/blocking, revision conflicts, restore-versus-purge races, idempotent multi-instance sweeper execution, deletion/retention, admin audit behavior, origin replacement before CDN purge/revalidation for user deletion, admin quarantine, and account purge, non-root API and sweeper OCI execution, SBOM/signature/provenance, and fixed port `46307` conflict failure.

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
