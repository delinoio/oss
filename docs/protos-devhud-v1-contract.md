# protos-devhud-v1-contract

## Scope

`protos/devhud/v1` is the planned versioned protobuf/Connect RPC contract. It owns wire types, service names, stable enum identifiers, compatibility rules, and generated-source inputs. No schema or generated code exists yet.

## Runtime and Language

Protocol definitions use English names/comments and package `devhud.v1`. The directory is not a runtime package and has no development server port.

## Users and Operators

The Go API, DevHud app, administrator SPA, generated TypeScript client, CI compatibility checks, and self-hosting operators.

## Interfaces and Contracts

Define Bootstrap, Settings, Upload, Account, Diagnostics, and Admin services with the following stable RPC identifiers: `BootstrapService.GetBootstrap`; `SettingsService.GetSettings`/`ReplaceSettings`; `UploadService.CreateUpload`/`FinalizeUpload`/`ListUploads`/`DeleteUpload`; `AccountService.GetAccount`/`DeleteAccount`/`RestoreAccount`; `DiagnosticsService.SubmitCrashReport`; and `AdminService.ListUsers`/`SetUserBlocked`/`GetUserUsage`/`ListUploads`/`QuarantineUpload`/`DeleteUpload`/`ListAuditEvents`. Stable values include project `devhud`, mini-apps `realqa`/`deck`, capture action IDs, `devhud-admin`, protocol/API version, and typed Connect error semantics. Bootstrap capabilities are static compatibility declarations, never remote feature flags.

Settings are schema-versioned full snapshots with monotonic revisions and expected-revision replacement; stale writes return `Aborted` and a conflict payload. Service identifiers use UUID v7; Logto and GitHub identifiers are external IDs. Protocols must not include secrets, PATs, R2 keys, DOM, screenshots, Deck results, agent output, or local paths. `GetBootstrap` is unauthenticated. Settings, uploads, and diagnostics require an authenticated, unblocked user; account methods require authenticated ownership, with `RestoreAccount` limited to the 30-day recovery window; admin methods require the `devhud-admin` role. Missing credentials return `Unauthenticated`, blocked users or missing roles return `PermissionDenied`, quota failures return `ResourceExhausted`, and invalid upload finalization returns `FailedPrecondition`. The first `CreateUpload` without an `upload_group_id` creates and returns a server-owned UUID v7 group bound to the authenticated user; subsequent `CreateUpload` and `FinalizeUpload` requests carry that group. `CreateUpload` atomically reserves the signed-URL issuance quota before issuing a URL and reports the reservation needed by finalization; failed issuance does not consume the reservation. `FinalizeUpload` is the validation boundary after direct R2 upload: it revalidates authentication, block status, ownership, and the exact staging-key/upload-group binding; verifies declared and observed size, checksum, allowed image content type, PNG signature, and safe raster dimensions; validates the existing signed-URL reservation without charging that quota again; atomically rechecks or reserves the upload-group image-count, rolling-byte, and stored-byte quotas; rejects replay or already-finalized keys; and removes invalid staging objects before returning its typed result. Deletion and quarantine replace the origin object before purging or revalidating its public CDN URL, and are not effective while the original remains retrievable from a cache. Restore and final purge are mutually exclusive atomic account-state transitions.

## Storage

The protocol defines representations only. Persistence and retention belong to the API contract; generated clients must not add a second storage or secret policy.

## Security

Keep credentials out of messages and logs. Mark authenticated/admin methods and upload-finalization invariants explicitly. Preserve compatibility without weakening authorization, quota, redaction, or deletion rules.

## Logging

Protocol diagnostics may expose typed safe error fields and UUID v7 correlation IDs only; never serialize credentials or sensitive content into error details.

## Build and Test

Run schema lint, breaking-change checks, generated-client freshness, API conformance, enum/path consistency, and golden error/compatibility tests in CI. Any wire change updates this contract, the server, client, and app contracts together.

## Dependencies and Integrations

Consumed by `servers/devhud-api` and `packages/devhud-api-client`; used by the app/admin surfaces. Connect RPC is the business transport; REST remains limited to the server contract’s operational endpoints.

## Change Triggers

Update the project index, server/client/admin/app contracts, `protos/AGENTS.md`, and CI generation/compatibility rules for any service, message, enum, revision, error, or field change.

## References

- [DevHud project index](project-devhud.md)
- [Server contract](servers-devhud-api-contract.md)
- [Client contract](packages-devhud-api-client-contract.md)
- [Repository defaults](repository-defaults.md)
