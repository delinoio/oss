# Project: dexdex

## Goal
Define the Connect RPC-first orchestration platform contract across desktop app, main server, worker server, and shared proto definitions.

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
- Shared schemas in `protos/dexdex/v1` are the source of truth for inter-component business contracts.
- Business communication remains Connect RPC-first; Tauri bindings stay as adapters.
- `LOCAL` and `REMOTE` workspace modes must converge to the same post-resolution UX behavior.

## Change Policy
- Interface changes require synchronized updates to this index and all affected domain contract docs.
- Any proto schema updates must propagate to desktop and both server contracts in the same change set.

## References
- `docs/project-template.md`
- `docs/domain-template.md`
- `docs/README.md`
