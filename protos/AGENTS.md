### Instructions for `protos/`

- Follow root `AGENTS.md` and the owning project/domain contracts.
- Keep protobuf package names, enum identifiers, compatibility, and generated-client rules stable and documented before implementation.
- Write schemas and comments in English.

### Scope in This Domain

- `protos/devhud/v1`: implemented versioned DevHud Connect RPC schemas.
- `protos/gen/go/devhud/v1`: committed, tool-owned Go messages and Connect server bindings generated from `protos/devhud/v1`.

### DevHud Rules

- Keep package `devhud.v1`, UUID v7 service-owned identifiers, typed Connect errors, revision conflicts, and the service/RPC list aligned with `docs/protos-devhud-v1-contract.md`.
- Administrative and user upload-list RPCs use the shared bounded page-size, opaque-token, deterministic-order pagination contract documented in `docs/protos-devhud-v1-contract.md`; user results are owner-scoped and user-search tokens include normalized query scope.
- Keep the explicit AdminService RPC names and FinalizeUpload validation boundary aligned with the protocol contract; schemas must not permit direct-upload callers to bypass ownership, quota, content, replay, or staging cleanup checks. Keep `GetBootstrap` unauthenticated, publish platform-keyed Logto client IDs including `admin` and its exact redirect URI, and preserve the per-RPC auth/role matrix.
- Upload messages also carry the server-owned submission ID, expected checksum as 32 raw bytes, and staging version/generation; finalization enforces the cross-group 10-image cap and 4096×4096/16,777,216-pixel pre-decode limits. The R2 header uses standard Base64 of those bytes, and API correlation IDs use the exposed `x-devhud-correlation-id` response header.
- Settings snapshots are at most 1 MiB of RFC 8785 canonical JSON bytes and use exact monotonic revisions: expected revision zero creates revision one, each successful replacement increments once, and stale writes return the typed conflict detail. Successful responses and errors carry correlation metadata mirrored to `x-devhud-correlation-id`.
- `CreateUploadTarget` remains an explicit oneof for new submission, new group, or existing group ownership. Reservation IDs, immutable nonzero staging generations, expected checksum/size, and observed ETag are required finalization bindings.
- Crash diagnostics are typed, user-previewed, and redacted, with 256-byte build/code identifier ceilings, a 4 KiB summary, and a 32 KiB stack ceiling. Administrator message graphs use a metadata-only upload projection and must not reach settings bodies, secrets, DOM, screenshots, public or signed asset locators, Deck results, agent output, or local paths.
- CI must validate schema compatibility and generated-client freshness. Generated sources must never be edited by hand.
