# cmds-devkit-commit-tracker-collector-foundation

## Scope
- Project/component: `devkit-commit-tracker` collector contract
- Canonical path: `cmds/commit-tracker`

## Runtime and Language
- Runtime: Go CLI/agent collector
- Primary language: Go

## Users and Operators
- Operators scheduling or running commit ingestion jobs
- Engineers debugging ingestion quality and event normalization

## Interfaces and Contracts
- Collector emits stable ingestion payloads consumed by the commit-tracker API server.
- Source repository metadata, commit identifiers, and timestamps must follow stable formats.
- Retry and deduplication behavior must preserve idempotent ingestion semantics.

## Storage
- Uses transient collection buffers and optional local checkpoints.
- Durable commit history persistence is owned by API server storage contracts.

## Security
- Repository credentials and access tokens must never be logged.
- Source control metadata handling must obey least-privilege access policies.

## Logging
- Use structured `log/slog` logs for source scan, emit, retry, and completion phases.
- Include repository locator, ingestion batch ID, and error taxonomy fields.

## Build and Test
- Local validation: `go test ./cmds/commit-tracker/...`
- Repository baseline: `go test ./...`

## Dependencies and Integrations
- Upstream dependencies: git providers and repository access configuration.
- Downstream dependencies: `servers/commit-tracker` API ingestion endpoints.

## Change Triggers
- Update `docs/project-devkit-commit-tracker.md` and this file when collector ingestion contracts change.
- Keep API compatibility synchronized with `docs/servers-devkit-commit-tracker-api-server-foundation.md`.

## References
- `docs/project-devkit-commit-tracker.md`
- `docs/servers-devkit-commit-tracker-api-server-foundation.md`
- `docs/domain-template.md`
