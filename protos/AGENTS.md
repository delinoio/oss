### Instructions for `protos/`

- Follow root `AGENTS.md` and the owning project/domain contracts.
- Keep protobuf package names, enum identifiers, compatibility, and generated-client rules stable and documented before implementation.
- Write schemas and comments in English.

### Scope in This Domain

- `protos/devhud/v1`: implemented versioned DevHud Connect RPC schemas, generated Go bindings, and shared serialization fixtures.

### DevHud Rules

- Keep package `devhud.v1`, UUID v7 service-owned identifiers, typed Connect errors, revision conflicts, and the service/RPC list aligned with `docs/protos-devhud-v1-contract.md`.
- Administrative and user upload-list RPCs use the shared bounded page-size, opaque-token, deterministic-order pagination contract documented in `docs/protos-devhud-v1-contract.md`; user results are owner-scoped and user-search tokens include normalized query scope.
- Keep the explicit AdminService RPC names and FinalizeUpload validation boundary aligned with the protocol contract; schemas must not permit direct-upload callers to bypass ownership, quota, content, replay, or staging cleanup checks. Keep `GetBootstrap` unauthenticated, publish platform-keyed Logto client IDs including `admin` and its exact redirect URI, and preserve the per-RPC auth/role matrix.
- Upload messages also carry the server-owned submission ID, expected checksum as 32 raw bytes, and staging version/generation; finalization enforces the cross-group 10-image cap and 4096×4096/16,777,216-pixel pre-decode limits. The R2 header uses standard Base64 of those bytes, and API correlation IDs use the exposed `x-devhud-correlation-id` response header.
- CI must validate schema compatibility and generated-client freshness. Protocols must not carry secrets, DOM, screenshots, Deck results, agent output, or local paths.
- Treat `*.pb.go`, `devhudv1connect/*.connect.go`, and `packages/devhud-api-client/src/gen` as generator-owned output. Change `.proto` sources and run `pnpm proto:generate`; never hand-edit generated files.
- Preserve the `CreateUpload` ID lifecycle: no IDs create a submission/first group, submission-only creates a later group, both IDs reuse an owned group, and group-only requests are invalid.
