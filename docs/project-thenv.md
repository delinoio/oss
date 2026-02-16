# Project: thenv

## Goal
`thenv` provides secure `.env` sharing across teams with clear trust boundaries.
It is a multi-component project composed of a Go CLI, backend server, and Devkit web console.

## Path
- CLI: `cmds/thenv`
- Server: `servers/thenv`
- Web console mini app: `apps/devkit/src/apps/thenv`

## Runtime and Language
- CLI: Go
- Server: Go
- Web console: Next.js 16 mini app (TypeScript)

## Users
- Developers who need secure distribution of environment variables
- Team operators managing shared environment sets

## In Scope
- Secure publish and retrieval workflow for `.env` payloads
- Separation of concerns between CLI, server, and web console
- Audit-oriented operation and policy controls
- Integration with Devkit for web management UX

## Out of Scope
- Replacing secret manager products in every scenario
- Executing arbitrary remote scripts through environment distribution
- Direct storage of plaintext secret material in frontend code

## Architecture
- CLI (`cmds/thenv`) handles local user workflows and secure interactions.
- Server (`servers/thenv`) handles storage, policy, and distribution APIs.
- Web console (`apps/devkit/src/apps/thenv`) provides management and visibility in Devkit.

## Interfaces
Canonical thenv component identifiers:

```ts
enum ThenvComponent {
  Cli = "cli",
  Server = "server",
  WebConsole = "web-console",
}
```

Component mapping contract:
- `Cli` -> `cmds/thenv`
- `Server` -> `servers/thenv`
- `WebConsole` -> `apps/devkit/src/apps/thenv`

Devkit route contract for web console:
- `/apps/thenv`

High-level operation identifiers:

```ts
enum ThenvOperation {
  Push = "push",
  Pull = "pull",
  List = "list",
  Rotate = "rotate",
}
```

## Storage
- Server-owned secure storage for environment payload metadata and encrypted values.
- CLI local cache only for operational metadata when necessary.
- Web console stores view state only, not secret payloads.

## Security
- Encrypt sensitive values in transit and at rest.
- Never display full secret values in default UI/CLI output.
- Require authenticated and authorized access for all environment operations.
- Record audit metadata for sensitive operations.

## Logging
Required baseline logs:
- Operation type, actor identity metadata, and target environment scope
- Authorization outcomes
- Secret access events (without secret value output)
- Failure reason classification for incident response

## Build and Test
Planned commands:
- CLI build/test: `go build ./cmds/thenv/...` and `go test ./cmds/thenv/...`
- Server build/test: `go build ./servers/thenv/...` and `go test ./servers/thenv/...`
- Web console tests: `pnpm --filter devkit... test`

## Roadmap
- Phase 1: Core secure push/pull/list operations.
- Phase 2: Policy management and audit improvements.
- Phase 3: Rotation workflows and broader integration support.
- Phase 4: Enterprise-grade governance and compliance features.

## Open Questions
- Cryptographic key management model and rotation cadence.
- Final policy model granularity (project, environment, role dimensions).
- Offline behavior expectations for CLI operations.

## References
- `docs/project-template.md`
- `docs/monorepo.md`
- `docs/project-devkit.md`
