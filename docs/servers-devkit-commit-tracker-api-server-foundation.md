# servers-devkit-commit-tracker-api-server-foundation

## Scope
- Project/component: commit-tracker API server contract
- Canonical path: `servers/commit-tracker`

## Runtime and Language
- Runtime: Go server
- Primary language: Go

## Users and Operators
- API consumers from Devkit commit-tracker web app
- Operators maintaining ingestion/query reliability

## Interfaces and Contracts
- Stable component identifier: `api-server`.
- Ingestion and query APIs must preserve stable commit identifiers and timestamp semantics.
- Query/filter pagination contracts must remain backward compatible for UI consumers.

## Storage
- Owns durable commit/event persistence schema and retention behavior.
- Indexing and query optimization contracts must remain consistent with API expectations.

## Security
- API must enforce authentication and authorization on ingestion and query operations.
- Secrets and tokens must never appear in API logs or default error payloads.

## Logging
- Use structured `log/slog` logs with request IDs, ingestion batch IDs, and query metadata.
- Audit-significant events must remain reconstructable.

## Build and Test
- Local validation: `go test ./servers/commit-tracker/...`
- Repository baseline: `go test ./...`

## Dependencies and Integrations
- Upstream collector integration: `cmds/commit-tracker` ingestion payloads.
- Downstream web integration: `apps/devkit/src/apps/commit-tracker` query consumers.

## Change Triggers
- Update `docs/project-devkit-commit-tracker.md` and this file for API schema, auth, or storage changes.
- Keep compatibility synchronized with collector and web component docs.

## References
- `docs/project-devkit-commit-tracker.md`
- `docs/cmds-devkit-commit-tracker-collector-foundation.md`
- `docs/apps-devkit-commit-tracker-web-app-foundation.md`
- `docs/domain-template.md`
