### Instructions for `packages/`

- Follow root `AGENTS.md` and the owning project/domain contracts.
- Generated packages must have a canonical source contract, reproducible generation, freshness checks, and no implicit secret or persistence policy.

### Scope in This Domain

- `packages/devhud-api-client`: planned generated TypeScript DevHud API client and Connect Query bindings.

### DevHud Rules

- Generate only from `protos/devhud/v1`; use `@connectrpc/connect-query` and preserve package `devhud.v1`, stable enums, typed errors, and revision conflicts.
- Generated clients must expose the explicit AdminService RPC names and `AccountService.RestoreAccount` defined by the protocol contract, including upload-finalization validation fields.
- Keep `docs/packages-devhud-api-client-contract.md` and `docs/project-devhud.md` synchronized with schema, transport, or generated API changes.
