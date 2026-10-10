# packages-devhud-api-client-contract

## Scope

`packages/devhud-api-client` is the implemented private `@delinoio/devhud-api-client` TypeScript package generated from `protos/devhud/v1` with small handwritten validation, pagination, and error-mapping helpers.

## Runtime and Language

TypeScript package generated from the canonical protocol schemas. Use `@connectrpc/connect` and `@connectrpc/connect-query`; preserve repository preference for React Query. No standalone server or fixed port.

## Users and Operators

`apps/devhud` and `apps/devhud-admin`, package maintainers, and CI consumers requiring deterministic generated sources.

## Interfaces and Contracts

Expose generated messages/service descriptors and per-service Connect Query namespaces for every v1 RPC, including `AccountService.RestoreAccount` and the explicitly named `AdminService` methods, without inventing alternate REST or Tauri bindings. Separate `UploadQuery.listUploads` from `AdminQuery.listUploads`. Preserve package `devhud.v1`, enum-backed IDs, UUID v7 fields, settings revision/content-digest conflict payloads, correlated administrator audit outcomes, typed Connect errors, shared bounded opaque-token pagination for administrative and user upload list RPCs, authenticated-owner filtering for user upload reads/deletes, bootstrap capability declarations, protocol-v2 exact official-upload origin, explicit (`desktop`, `ios`, `android`, `admin`) Logto client-ID fields, the native callback URI `devhud://auth/callback`, the exact deployment-configured admin redirect URI, and upload-group, upload/checksum/finalization fields. Generated output under `src/gen` is reproducible, committed, and never handwritten.

Handwritten helpers enforce canonical UUID-v7 text, 32 raw checksum bytes, the default/maximum page sizes and well-formed-Unicode 2 KiB opaque-token ceiling, deterministic encoding and validation of the 1 MiB RFC 8785 settings snapshot limit without a UTF-8 BOM, non-blank NUL-free administrator mutation reasons capped at 4 KiB of well-formed UTF-8 with credential and local-path patterns rejected, and configured public asset locators rejected using the bootstrap-provided public asset base URL. App callers additionally verify schema-v7 settings digests and exact official-upload origin authority before persistence or native upload. The helpers also enforce crash-report schema version 1, a required canonical crash client UUID v7, at most 32 unique related correlations, a 24-hour duration, safe closed enums with Android-only armv7 and browser-only unknown architecture, exact native Tauri/browser-empty and pinned desktop CEF/mobile-browser-empty revision rules, 256-byte crash build identifiers, 64-byte uppercase enum-style error codes, and the 4 KiB/32 KiB redacted crash-report limits with NUL, forbidden local-path, URL credential parameters, and standalone credential assignments such as OAuth `code`/`oauth_code` rejected across every narrative crash string. `mapDevHudError` maps Connect codes plus generated details into a discriminated client error while preserving response correlation metadata. Transport, authorization, and React Query ownership remain with callers.

The generated `QuotaKind` includes `QUOTA_KIND_CRASH_REPORTS`, and `mapDevHudError` classifies a matching `ResourceExhausted` plus `QuotaFailure` detail as `quotaExceeded` while preserving its limit and observed count.

### Package integration

- `packages/devhud-api-client`: implemented generated TypeScript DevHud API client, Connect Query bindings, and safe handwritten wire helpers.

- Keep generated bootstrap types aligned with the `desktop`/`ios`/`android`/`admin` Logto client keys, native callback, and exact admin redirect defined by the protocol and server contracts.

- Generate only from `protos/devhud/v1`; use `@connectrpc/connect-query` and preserve package `devhud.v1`, stable enums, typed errors, and revision conflicts.

- Generated clients must expose the explicit AdminService RPC names and `AccountService.RestoreAccount` defined by the protocol contract, including upload-finalization validation fields.

- Generated administrative and user-upload-list clients must preserve the shared bounded page-size, opaque-token, deterministic-order pagination contract defined by the protocol, including query/user scope.

- Keep `packages-devhud-api-client-contract.md` and `project-devhud.md` synchronized with schema, transport, or generated API changes.

- Generated upload clients must preserve submission-scoped groups, expected 32-byte raw checksum/version fields, immutable finalization semantics, and correlation-ID response metadata.

- Keep committed output under `src/gen` tool-owned and reproducible. Public exports use per-service namespaces such as `UploadQuery` and `AdminQuery` so same-named RPCs do not collide; do not handwrite generated messages or service descriptors.

- Handwritten helpers may validate canonical UUID v7 values, checksums, bounded pagination, RFC 8785 settings bytes without a UTF-8 BOM, bounded NUL-free sensitive-content-safe administrator reasons, required crash client/related correlations, duration, browser-only unknown architecture, explicit browser and native platform revision rules, NUL-free 256-byte crash identifiers, redacted crash details, and typed Connect errors, but must not add persistence, implicit authentication, or another business transport.

- The API client must retain package-local typecheck, lint, unit, and build commands for affected CI execution. Its generated output is cacheable only when derived from the committed schemas and lockfile.

- Windows console Ctrl+C/Break reaches both launcher and native child. Await native cleanup and numeric completion instead of forwarding these events with Node's forceful kill API; preserve explicit Unix signal forwarding. Keep Unix acknowledgement credit scoped to one pending grace attempt; discard unsolicited credit and disable suppression for the launch after an unacknowledged fallback or overlapping SIGINT. After fallback, retain forwarding even when terminal delivery is duplicated; native cleanup may then skip grace. Integration fixtures must prove fallback before accepting short grace and confirm owned cleanup. Cancel owned grace timers on exit/error. Cover both policies with unit fixtures and isolated Windows console integration.

- Keep the ROAM travel IR example's reusable React source, local image assets, generation prompts and provenance together. Resolve its assets relative to the task module, retain explicit fictional-metric labels and calculation consistency, and keep exported decks/previews untracked. The example must run without network access or image-generation/conversion tools and must generate successfully on all six native CI hosts with their installed fonts.

## Storage

Generated upload types preserve the server-owned `submission_id`, cross-group 10-image limit, signed expected checksum as 32 raw bytes, staging version/generation, immutable conditional-finalization fields, the 15-minute signed PUT `expires_at`, the independent 24-hour `staging_expires_at`, and the `x-devhud-correlation-id` response metadata used by integrators. Clients set the returned `Content-Type` and standard-Base64 checksum headers, upload the declared byte length directly to R2, and never send image bodies through a Connect message.

No persistence. App callers own local encrypted storage and secure credentials; the client package must not cache tokens, settings, Deck results, or upload bodies implicitly.

## Security

Keep authentication transport configuration explicit and caller-owned. Redact errors and diagnostics. Never serialize PATs, R2 secrets, DOM, screenshots, agent output, or local paths into generated models or logs. Handwritten crash-report validation accepts only parsed HTTP(S) diagnostic URLs so custom schemes cannot hide local paths from the locator checks.

## Logging

The package must not log by default. Integrators provide redacted structured logging and UUID v7 correlation handling.

## Build and Test

CI regenerates from `protos/devhud/v1`, fails on stale output, runs TypeScript lint/build/tests, verifies the exact 18-RPC and Connect Query export inventory, executes generated query and mutation descriptors through the React Query adapter, exercises binary/ProtoJSON serialization and error mapping, and proves forbidden fields, settings bodies, and asset locators are absent from administrator message graphs.

The `devhud-frontend` client step runs the package `typecheck` and `test` tasks through `scripts/ci/run-affected.mjs` with `FORCE_RUN=true`. The semantic check uses `tsc -p tsconfig.json --noEmit` and includes package test sources. Runtime tests and the source-only client build remain required; consumer builds do not type check the package tests.

## Dependencies and Integrations

Generated from `protos/devhud/v1`; consumed by the DevHud and admin apps; targets `servers/devhud-api`. The generated client covers the complete v1 wire contract, and the current server registers Bootstrap, Settings, Upload, Account, Admin, and Diagnostics. It must remain independent of Tauri, Chrome Native Messaging, GitHub, and R2 SDKs.

## Change Triggers

Update the project index, protocol/server/app/admin contracts, `packages/AGENTS.md`, and generation CI whenever schemas, package API, transport, or error behavior changes.

Instruction-file updates in this requirement apply only to changes in development procedures, directory ownership or repository/domain development rules under the [instruction-update policy](README.md#instruction-update-policy); ordinary behavior and validation changes update the owning contracts and validation records.

## References

- [DevHud project index](project-devhud.md)
- [Protocol contract](protos-devhud-v1-contract.md)
- [Server contract](servers-devhud-api-contract.md)
- [Repository defaults](repository-defaults.md)
