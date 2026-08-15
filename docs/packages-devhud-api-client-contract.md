# packages-devhud-api-client-contract

## Scope

`packages/devhud-api-client` is the planned generated TypeScript client and React Query bindings for `protos/devhud/v1`. It is not implemented.

## Runtime and Language

TypeScript package generated from the canonical protocol schemas. Use `@connectrpc/connect` and `@connectrpc/connect-query`; preserve repository preference for React Query. No standalone server or fixed port.

## Users and Operators

`apps/devhud` and `apps/devhud-admin`, package maintainers, and CI consumers requiring deterministic generated sources.

## Interfaces and Contracts

Expose generated clients for every v1 service/RPC, including `AccountService.RestoreAccount` and the explicitly named `AdminService` methods, without inventing alternate REST or Tauri bindings. Preserve package `devhud.v1`, enum-backed IDs, UUID v7 fields, revision-conflict payloads, typed Connect errors, the shared bounded opaque-token pagination fields for administrative list RPCs, bootstrap capability declarations, platform-keyed (`desktop`, `ios`, `android`, `admin`) Logto client IDs, the native callback URI `devhud://auth/callback`, the exact deployment-configured admin redirect URI, and upload-group, upload/checksum/finalization fields. Generated output must be reproducible and must not contain secrets or local-only device state.

## Storage

Generated upload types preserve the server-owned `submission_id`, cross-group 10-image limit, signed expected checksum, staging version/generation, and immutable conditional-finalization fields.

No persistence. App callers own local encrypted storage and secure credentials; the client package must not cache tokens, settings, Deck results, or upload bodies implicitly.

## Security

Keep authentication transport configuration explicit and caller-owned. Redact errors and diagnostics. Never serialize PATs, R2 secrets, DOM, screenshots, agent output, or local paths into generated models or logs.

## Logging

The package must not log by default. Integrators provide redacted structured logging and UUID v7 correlation handling.

## Build and Test

CI must regenerate from `protos/devhud/v1`, fail on stale output, run TypeScript checks, Connect compatibility tests, React Query integration tests, and verify no secret-bearing fields are generated.

## Dependencies and Integrations

Generated from `protos/devhud/v1`; consumed by the DevHud and admin apps; targets `servers/devhud-api`. It must remain independent of Tauri, Chrome Native Messaging, GitHub, and R2 SDKs.

## Change Triggers

Update the project index, protocol/server/app/admin contracts, `packages/AGENTS.md`, and generation CI whenever schemas, package API, transport, or error behavior changes.

## References

- [DevHud project index](project-devhud.md)
- [Protocol contract](protos-devhud-v1-contract.md)
- [Server contract](servers-devhud-api-contract.md)
- [Repository defaults](repository-defaults.md)
