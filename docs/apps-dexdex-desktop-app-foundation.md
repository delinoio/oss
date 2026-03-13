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
- Current frontend contract is a React scaffold UI (logo links + local greeting form) without DexDex domain RPC calls.
- Current scaffold behavior is frontend-local state only and does not expose workspace-mode routing or Connect Query wiring.
- Connect RPC-first desktop behavior is a planned reintegration target and must consume `protos/dexdex/v1` schemas when restored.

## Storage
- Current scaffold stores transient in-memory form state only.
- Reintroduced workspace/session persistence must document cache invalidation behavior.

## Security
- Current scaffold must avoid collecting or persisting secret material.
- When RPC contracts return, local adapter boundaries must not bypass server-side authorization policies.

## Logging
- Current scaffold keeps logging minimal and must avoid secret payloads.
- Reintegration phases should restore structured logs for mode transitions, request IDs, and operation outcomes.

## Build and Test
- Local validation: `pnpm --filter dexdex test`
- Packaging build contract: `pnpm --filter dexdex tauri:build`
- CI alignment: `node-dexdex-test` and desktop build workflow

## Dependencies and Integrations
- Runtime stack: Tauri host + React frontend scaffold.
- No active desktop-to-server business integration is enabled in the scaffold phase.
- Future reintegration target: `servers/dexdex-main-server` and `servers/dexdex-worker-server` via shared proto contracts.

## Change Triggers
- Update `docs/project-dexdex.md` and this file when desktop behavior or adapter boundaries change.
- Synchronize schema-impacting changes with `docs/protos-dexdex-v1-contract.md` and related server contracts when desktop RPC integration is reintroduced.

## References
- `docs/project-dexdex.md`
- `docs/protos-dexdex-v1-contract.md`
- `docs/servers-dexdex-main-server-foundation.md`
- `docs/servers-dexdex-worker-server-foundation.md`
- `docs/domain-template.md`
