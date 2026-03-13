# apps-dexdex-desktop-app-foundation

## Scope
- Project/component: DexDex desktop app contract
- Canonical path: `apps/dexdex`

## Runtime and Language
- Runtime: Tauri desktop app (React frontend + Rust backend adapter)
- Primary language: TypeScript and Rust

## Users and Operators
- End users running orchestration workflows from desktop clients
- Maintainers shipping multi-OS desktop releases

## Interfaces and Contracts
- Stable component identifier: `desktop-app`.
- Business contracts must consume `protos/dexdex/v1` schema definitions.
- Tauri bindings are runtime adapters; Connect RPC contracts remain the primary business interface.
- `LOCAL` and `REMOTE` modes must converge to equivalent post-resolution UX behavior.

## Storage
- Defines local workspace/session persistence for desktop usage.
- Cached orchestration metadata must include clear invalidation behavior.

## Security
- Desktop secret material must remain encrypted or OS-protected.
- Local adapter boundaries must not bypass server-side authorization policies.

## Logging
- Include structured logs for mode transitions, request IDs, and operation outcomes.
- Avoid logging secret payloads or credential material.

## Build and Test
- Local validation: `pnpm --filter dexdex test`
- Packaging build contract: `pnpm --filter dexdex tauri:build`
- CI alignment: `node-dexdex-test` and desktop build workflow

## Dependencies and Integrations
- Integrates with `servers/dexdex-main-server` and `servers/dexdex-worker-server` via shared proto contracts.
- Integrates with Tauri runtime as host adapter.

## Change Triggers
- Update `docs/project-dexdex.md` and this file when desktop behavior or adapter boundaries change.
- Synchronize schema-impacting changes with `docs/protos-dexdex-v1-contract.md` and related server contracts.

## References
- `docs/project-dexdex.md`
- `docs/protos-dexdex-v1-contract.md`
- `docs/servers-dexdex-main-server-foundation.md`
- `docs/servers-dexdex-worker-server-foundation.md`
- `docs/domain-template.md`
