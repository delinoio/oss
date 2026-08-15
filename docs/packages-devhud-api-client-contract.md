# packages-devhud-api-client-contract

## Scope

`packages/devhud-api-client` is the implemented private `@delinoio/devhud-api-client` TypeScript package generated from `protos/devhud/v1` with small handwritten validation, pagination, and error-mapping helpers.

## Runtime and Language

TypeScript package generated from the canonical protocol schemas. Use `@connectrpc/connect` and `@connectrpc/connect-query`; preserve repository preference for React Query. No standalone server or fixed port.

## Users and Operators

`apps/devhud` and `apps/devhud-admin`, package maintainers, and CI consumers requiring deterministic generated sources.

## Interfaces and Contracts

Expose generated messages/service descriptors and per-service Connect Query namespaces for every v1 RPC, including `AccountService.RestoreAccount` and the explicitly named `AdminService` methods, without inventing alternate REST or Tauri bindings. Separate `UploadQuery.listUploads` from `AdminQuery.listUploads`. Preserve package `devhud.v1`, enum-backed IDs, UUID v7 fields, revision-conflict payloads, typed Connect errors, shared bounded opaque-token pagination for administrative and user upload list RPCs, authenticated-owner filtering for user upload reads/deletes, bootstrap capability declarations, explicit (`desktop`, `ios`, `android`, `admin`) Logto client-ID fields, the native callback URI `devhud://auth/callback`, the exact deployment-configured admin redirect URI, and upload-group, upload/checksum/finalization fields. Generated output under `src/gen` is reproducible, committed, and never handwritten.

Handwritten helpers enforce canonical UUID-v7 text, 32 raw checksum bytes, the default/maximum page sizes and 2 KiB opaque-token ceiling, the 1 MiB RFC 8785 settings snapshot limit without a UTF-8 BOM, non-blank administrator mutation reasons capped at 4 KiB of well-formed UTF-8 with credential and local-path patterns rejected, the 256-byte crash build/code identifier limit, and the 4 KiB/32 KiB redacted crash-report limits and forbidden local-path/credential patterns across every crash string. `mapDevHudError` maps Connect codes plus generated details into a discriminated client error while preserving response correlation metadata. Transport, authorization, and React Query ownership remain with callers.

## Storage

Generated upload types preserve the server-owned `submission_id`, cross-group 10-image limit, signed expected checksum as 32 raw bytes, staging version/generation, immutable conditional-finalization fields, and the `x-devhud-correlation-id` response metadata used by integrators.

No persistence. App callers own local encrypted storage and secure credentials; the client package must not cache tokens, settings, Deck results, or upload bodies implicitly.

## Security

Keep authentication transport configuration explicit and caller-owned. Redact errors and diagnostics. Never serialize PATs, R2 secrets, DOM, screenshots, agent output, or local paths into generated models or logs.

## Logging

The package must not log by default. Integrators provide redacted structured logging and UUID v7 correlation handling.

## Build and Test

CI regenerates from `protos/devhud/v1`, fails on stale output, runs TypeScript lint/build/tests, verifies the exact 18-RPC and Connect Query export inventory, executes generated query and mutation descriptors through the React Query adapter, exercises binary/ProtoJSON serialization and error mapping, and proves forbidden fields, settings bodies, and asset locators are absent from administrator message graphs.

## Dependencies and Integrations

Generated from `protos/devhud/v1`; consumed by the DevHud and admin apps; targets `servers/devhud-api`. It must remain independent of Tauri, Chrome Native Messaging, GitHub, and R2 SDKs.

## Change Triggers

Update the project index, protocol/server/app/admin contracts, `packages/AGENTS.md`, and generation CI whenever schemas, package API, transport, or error behavior changes.

## References

- [DevHud project index](project-devhud.md)
- [Protocol contract](protos-devhud-v1-contract.md)
- [Server contract](servers-devhud-api-contract.md)
- [Repository defaults](repository-defaults.md)
