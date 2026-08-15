### Instructions for `protos/`

- Follow root `AGENTS.md` and the owning project/domain contracts.
- Keep protobuf package names, enum identifiers, compatibility, and generated-client rules stable and documented before implementation.
- Write schemas and comments in English.

### Scope in This Domain

- `protos/devhud/v1`: planned versioned DevHud Connect RPC schemas.

### DevHud Rules

- Keep package `devhud.v1`, UUID v7 service-owned identifiers, typed Connect errors, revision conflicts, and the service/RPC list aligned with `docs/protos-devhud-v1-contract.md`.
- Keep the explicit AdminService RPC names and FinalizeUpload validation boundary aligned with the protocol contract; schemas must not permit direct-upload callers to bypass ownership, quota, content, replay, or staging cleanup checks. Keep `GetBootstrap` unauthenticated, publish platform-keyed Logto client IDs, and preserve the per-RPC auth/role matrix.
- CI must validate schema compatibility and generated-client freshness. Protocols must not carry secrets, DOM, screenshots, Deck results, agent output, or local paths.
