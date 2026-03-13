# cmds-thenv-cli-foundation

## Scope
- Project/component: `thenv` CLI contract
- Canonical path: `cmds/thenv`

## Runtime and Language
- Runtime: Go CLI
- Primary language: Go

## Users and Operators
- Developers managing `.env` sharing and rotation workflows
- Security-conscious operators enforcing trust policies

## Interfaces and Contracts
- CLI subcommands for secret create/read/update/revoke must remain stable and documented.
- Authentication, trust bootstrap, and key management flows must be deterministic.
- Output contracts must align with server API and web console expectations.

## Storage
- Stores local trust metadata and encrypted material according to explicit path contracts.
- Uses transient runtime files for safe key exchange and command execution.

## Security
- Secrets must be encrypted at rest and protected in transit.
- CLI output and logs must avoid leaking secret values.
- Trust decisions must remain explicit and auditable.

## Logging
- Use structured `log/slog` logs for CLI command lifecycle and trust boundary events.
- Include command name, target workspace, trust context, and sanitized outcome fields.

## Build and Test
- Local validation: `go test ./cmds/thenv/...`
- Repository baseline: `go test ./...`

## Dependencies and Integrations
- Integrates with `servers/thenv` APIs.
- Integrates with `apps/devkit/src/apps/thenv` UX expectations.

## Change Triggers
- Update `docs/project-thenv.md` and this file when CLI command shape or trust boundaries change.
- Keep compatibility synchronized with `docs/servers-thenv-server-foundation.md` and `docs/apps-thenv-web-console-foundation.md`.

## References
- `docs/project-thenv.md`
- `docs/servers-thenv-server-foundation.md`
- `docs/apps-thenv-web-console-foundation.md`
- `docs/domain-template.md`
