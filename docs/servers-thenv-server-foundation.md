# servers-thenv-server-foundation

## Scope
- Project/component: thenv server contract
- Canonical path: `servers/thenv`

## Runtime and Language
- Runtime: Go server
- Primary language: Go

## Users and Operators
- CLI and web-console clients performing secret management operations
- Operators enforcing policy, audit, and availability targets

## Interfaces and Contracts
- Stable component identifier: `server`.
- API contracts for secret lifecycle operations must align with CLI and web console semantics.
- Trust/bootstrap and policy evaluation contracts must remain explicit and versioned.

## Storage
- Owns encrypted secret storage and trust metadata persistence.
- Retention and revocation metadata must be auditable and deterministic.

## Security
- Enforce strict authentication, authorization, encryption, and audit requirements.
- Never expose secret values in logs, metrics labels, or default error responses.

## Logging
- Use structured `log/slog` logs for auth decisions, policy checks, and secret lifecycle operations.
- Include actor ID, resource scope, action type, and sanitized result fields.

## Build and Test
- Local validation: `go test ./servers/thenv/...`
- Repository baseline: `go test ./...`

## Dependencies and Integrations
- Client integrations: `cmds/thenv` and `apps/devkit/src/apps/thenv`.
- Shared policy model must remain aligned with thenv project-level trust invariants.

## Change Triggers
- Update `docs/project-thenv.md` and this file for server API, policy, or storage changes.
- Synchronize trust model changes with CLI and web console thenv docs.

## References
- `docs/project-thenv.md`
- `docs/cmds-thenv-cli-foundation.md`
- `docs/apps-thenv-web-console-foundation.md`
- `docs/domain-template.md`
