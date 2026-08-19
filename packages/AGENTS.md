### Instructions for `packages/`

- Follow root `AGENTS.md` and the owning project/domain contracts.
- Generated packages must have a canonical source contract, reproducible generation, freshness checks, and no implicit secret or persistence policy.

### Scope in This Domain

- `packages/devhud-api-client`: implemented generated TypeScript DevHud API client, Connect Query bindings, and safe handwritten wire helpers.

### DevHud Rules

- Keep generated bootstrap types aligned with the `desktop`/`ios`/`android`/`admin` Logto client keys, native callback, and exact admin redirect defined by the protocol and server contracts.

- Generate only from `protos/devhud/v1`; use `@connectrpc/connect-query` and preserve package `devhud.v1`, stable enums, typed errors, and revision conflicts.
- Generated clients must expose the explicit AdminService RPC names and `AccountService.RestoreAccount` defined by the protocol contract, including upload-finalization validation fields.
- Generated administrative and user-upload-list clients must preserve the shared bounded page-size, opaque-token, deterministic-order pagination contract defined by the protocol, including query/user scope.
- Keep `docs/packages-devhud-api-client-contract.md` and `docs/project-devhud.md` synchronized with schema, transport, or generated API changes.
- Generated upload clients must preserve submission-scoped groups, expected 32-byte raw checksum/version fields, immutable finalization semantics, and correlation-ID response metadata.
- Keep committed output under `src/gen` tool-owned and reproducible. Public exports use per-service namespaces such as `UploadQuery` and `AdminQuery` so same-named RPCs do not collide; do not handwrite generated messages or service descriptors.
- Handwritten helpers may validate canonical UUID v7 values, checksums, bounded pagination, RFC 8785 settings bytes without a UTF-8 BOM, bounded NUL-free sensitive-content-safe administrator reasons, required crash client/related correlations, duration, browser-only unknown architecture, explicit browser and native platform revision rules, NUL-free 256-byte crash identifiers, redacted crash details, and typed Connect errors, but must not add persistence, implicit authentication, or another business transport.
