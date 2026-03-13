# servers-dexdex-worker-server-foundation

## Scope
- Project/component: DexDex worker server contract
- Canonical path: `servers/dexdex-worker-server`

## Runtime and Language
- Runtime: Go server
- Primary language: Go

## Users and Operators
- Control-plane orchestrators dispatching execution tasks
- Operators running execution-plane capacity and reliability workflows

## Interfaces and Contracts
- Stable component identifier: `worker-server`.
- Execution-plane APIs must align with shared `protos/dexdex/v1` definitions.
- Worker status, result, and error contracts must remain stable for control-plane and desktop consumers.

## Storage
- Owns worker-local execution metadata and cache contracts.
- Execution artifact retention and cleanup policies must remain explicit.

## Security
- Worker execution boundaries must enforce least privilege and isolation controls.
- Sensitive task payloads must be redacted from logs and default diagnostic output.

## Logging
- Use structured `log/slog` logs for task lifecycle, execution state, and failure diagnostics.
- Include task ID, worker ID, workflow correlation ID, and sanitized error taxonomy.

## Build and Test
- Local validation: `go test ./servers/dexdex-worker-server/...`
- Repository baseline: `go test ./...`

## Dependencies and Integrations
- Integrates with main server scheduling and orchestration flows.
- Integrates with shared proto schemas consumed by desktop and server components.

## Change Triggers
- Update `docs/project-dexdex.md` and this file when execution APIs or runtime boundaries change.
- Synchronize schema-impacting changes with proto, main-server, and desktop contracts.

## References
- `docs/project-dexdex.md`
- `docs/protos-dexdex-v1-contract.md`
- `docs/servers-dexdex-main-server-foundation.md`
- `docs/apps-dexdex-desktop-app-foundation.md`
- `docs/domain-template.md`
