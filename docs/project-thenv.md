# Project: thenv

## Goal
Define secure `.env` sharing workflows across CLI and server components, with a reserved Devkit web-console route for future reactivation.

## Project ID
`thenv`

## Domain Ownership Map
- `cmds/thenv` (`cli`)
- `servers/thenv` (`server`)
- `apps/devkit/src/apps/thenv` (`web-console`)

## Domain Contract Documents
- `docs/cmds-thenv-cli-foundation.md`
- `docs/servers-thenv-server-foundation.md`
- `docs/apps-thenv-web-console-foundation.md`

## Cross-Domain Invariants
- Component identifiers remain stable: `cli`, `server`, `web-console`.
- Trust boundaries and redaction rules must remain consistent across all components.
- Secret lifecycle operations must use shared semantic contracts for create, read, rotate, and revoke flows.
- Devkit web-console component remains scaffold-only until reactivation is documented.

## Change Policy
- Security or interface changes require synchronized updates to this index and all affected component docs.
- CLI, server, and web console contracts must remain aligned on permissions, auditing, and error taxonomy.

## References
- `docs/project-devkit.md`
- `docs/project-template.md`
- `docs/domain-template.md`
- `docs/README.md`
