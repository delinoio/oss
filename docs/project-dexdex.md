# Project: dexdex

## Goal
Define DexDex multi-component contracts while the desktop app remains on a temporary React scaffold baseline and server/proto contracts continue evolving.

## Project ID
`dexdex`

## Domain Ownership Map
- `apps/dexdex` (`desktop-app`)
- `servers/dexdex-main-server` (`main-server`)
- `servers/dexdex-worker-server` (`worker-server`)
- `protos/dexdex` (`v1` shared contracts)

## Domain Contract Documents
- `docs/apps-dexdex-desktop-app-foundation.md`
- `docs/servers-dexdex-main-server-foundation.md`
- `docs/servers-dexdex-worker-server-foundation.md`
- `docs/protos-dexdex-v1-contract.md`

## Cross-Domain Invariants
- Component identifiers remain stable: `desktop-app`, `main-server`, `worker-server`.
- Shared schemas in `protos/dexdex/v1` are the source of truth for server-side business contracts and future desktop reintegration.
- `apps/dexdex` is currently a scaffold-phase desktop shell and does not yet consume `dexdex.v1` business RPC contracts.
- Connect RPC-first desktop integration is planned and must restore Tauri-as-adapter boundaries.

## Change Policy
- Interface changes require synchronized updates to this index and all affected domain contract docs.
- Any proto schema updates must propagate to desktop and both server contracts in the same change set.
- Desktop reintegration work must update this index and `docs/apps-dexdex-desktop-app-foundation.md` in the same change where RPC contracts are reintroduced.

## References
- `docs/project-template.md`
- `docs/domain-template.md`
- `docs/README.md`
