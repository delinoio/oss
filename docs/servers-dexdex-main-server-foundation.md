# servers-dexdex-main-server-foundation

## Scope
- Project/component: DexDex main server contract
- Canonical path: `servers/dexdex-main-server`

## Runtime and Language
- Runtime: Go server
- Primary language: Go

## Users and Operators
- Desktop clients and worker coordination flows consuming control-plane APIs
- Operators managing orchestration state and control-plane reliability

## Interfaces and Contracts
- Stable component identifier: `main-server`.
- Control-plane interfaces must be defined by shared `protos/dexdex/v1` schemas.
- Main server orchestration commands and state transitions must remain deterministic.

## Storage
- Owns control-plane persistence for orchestration metadata and coordination state.
- Retention and replay behavior must be explicit for operational recovery.

## Security
- Authorization boundaries for control-plane operations must remain explicit.
- Secrets and credentials must remain redacted in logs and diagnostics.

## Logging
- Use structured `log/slog` logs for orchestration lifecycle transitions.
- Include request ID, workflow ID, actor scope, and sanitized outcome metadata.

## Build and Test
- Local validation: `go test ./servers/dexdex-main-server/...`
- Repository baseline: `go test ./...`

## Dependencies and Integrations
- Integrates with `protos/dexdex/v1` schema contracts.
- Integrates with desktop app and worker server through Connect RPC.

## Change Triggers
- Update `docs/project-dexdex.md` and this file when control-plane APIs or state contracts change.
- Synchronize schema-impacting changes with proto and worker/desktop docs.

## References
- `docs/project-dexdex.md`
- `docs/protos-dexdex-v1-contract.md`
- `docs/servers-dexdex-worker-server-foundation.md`
- `docs/apps-dexdex-desktop-app-foundation.md`
- `docs/domain-template.md`
