# packages-devhud-api-client-contract

## Scope

`packages/devhud-api-client` is the implemented private `@delinoio/devhud-api-client` workspace generated from `protos/devhud/v1`. It provides TypeScript messages/service descriptors, Connect Query method exports, and a minimal correlation-header helper for both DevHud SPAs.

## Runtime and Language

Strict TypeScript ESM package generated with Protobuf-ES and `@connectrpc/connect-query`; it uses `@connectrpc/connect` descriptors and preserves repository preference for React Query. It emits JavaScript and declaration files to untracked `dist/` output and has no standalone server or fixed port.

## Users and Operators

`apps/devhud` and `apps/devhud-admin`, package maintainers, and CI consumers requiring deterministic generated sources.

## Interfaces and Contracts

Expose generated clients for every v1 service/RPC, including `AccountService.RestoreAccount` and the explicitly named `AdminService` methods, without inventing alternate REST or Tauri bindings. Preserve package `devhud.v1`, enum-backed IDs, UUID v7 fields, revision-conflict payloads, typed Connect errors, shared bounded opaque-token pagination for administrative and user upload list RPCs, authenticated-owner filtering for user upload reads/deletes, bootstrap capability declarations, platform-keyed (`desktop`, `ios`, `android`, `admin`) Logto client IDs, the native callback URI `devhud://auth/callback`, the exact deployment-configured admin redirect URI, and upload-group, upload/checksum/finalization fields. Generated output must be reproducible and must not contain secrets or local-only device state.

The root export exposes all generated message/service descriptors and collision-free `BootstrapQueries`, `SettingsQueries`, `UploadQueries`, `AccountQueries`, `DiagnosticsQueries`, and `AdminQueries` namespaces. Stable `devhud/v1/*` subpath exports provide direct access to each generated file. `DEVHUD_CORRELATION_ID_HEADER` and `getDevHudCorrelationId` read and validate response metadata without logging or retaining it. Callers own transports, authorization headers, React Query providers, and token persistence.

## Storage

Generated upload types preserve the server-owned `submission_id`, cross-group 10-image limit, signed expected checksum as 32 raw bytes, staging version/generation, immutable conditional-finalization fields, and the `x-devhud-correlation-id` response metadata used by integrators.

No persistence. App callers own local encrypted storage and secure credentials; the client package must not cache tokens, settings, Deck results, or upload bodies implicitly.

## Security

Keep authentication transport configuration explicit and caller-owned. Redact errors and diagnostics. Never serialize PATs, R2 secrets, DOM, screenshots, agent output, or local paths into generated models or logs.

## Logging

The package must not log by default. Integrators provide redacted structured logging and UUID v7 correlation handling.

## Build and Test

`pnpm proto:generate` is the only generation entrypoint. Generator versions are pinned in the root pnpm lockfile and Go module; `src/gen` is generator-owned and contains no handwritten files. CI regenerates from `protos/devhud/v1`, fails on stale/orphaned output, runs strict TypeScript checks and build, exercises generated descriptors through an in-memory Connect transport, round-trips shared Go/TypeScript fixtures, and verifies no secret-bearing fields or Admin settings bodies are generated.

## Dependencies and Integrations

Generated from `protos/devhud/v1`; consumed by the DevHud and admin apps; targets `servers/devhud-api`. It must remain independent of Tauri, Chrome Native Messaging, GitHub, and R2 SDKs.

## Change Triggers

Update the project index, protocol/server/app/admin contracts, `packages/AGENTS.md`, and generation CI whenever schemas, package API, transport, or error behavior changes.

## References

- [DevHud project index](project-devhud.md)
- [Protocol contract](protos-devhud-v1-contract.md)
- [Server contract](servers-devhud-api-contract.md)
- [Repository defaults](repository-defaults.md)
