### Instructions for `packages/`

- Follow root `AGENTS.md` and the owning project/domain contracts.
- Generated packages must have a canonical source contract, reproducible generation, freshness checks, and no implicit secret or persistence policy.

### Scope in This Domain

- `packages/devhud-api-client`: implemented private generated TypeScript DevHud API client and Connect Query workspace.

### DevHud Rules

- Keep generated bootstrap types aligned with the `desktop`/`ios`/`android`/`admin` Logto client keys, native callback, and exact admin redirect defined by the protocol and server contracts.

- Generate only from `protos/devhud/v1`; use `@connectrpc/connect-query` and preserve package `devhud.v1`, stable enums, typed errors, and revision conflicts.
- Generated clients must expose the explicit AdminService RPC names and `AccountService.RestoreAccount` defined by the protocol contract, including upload-finalization validation fields.
- Generated administrative and user-upload-list clients must preserve the shared bounded page-size, opaque-token, deterministic-order pagination contract defined by the protocol, including query/user scope.
- Keep `docs/packages-devhud-api-client-contract.md` and `docs/project-devhud.md` synchronized with schema, transport, or generated API changes.
- Generated upload clients must preserve submission-scoped groups, expected 32-byte raw checksum/version fields, immutable finalization semantics, and correlation-ID response metadata.
- Treat `src/gen` as generator-owned output and never hand-edit it. Handwritten exports, response-header helpers, tests, and package metadata must remain outside that directory and must not introduce transport, token, persistence, or logging policy.
