# Project: thenv

## Goal
Define secure `.env` sharing workflows across the CLI and server components.

## Project ID
`thenv`

## Domain Ownership Map
- `cmds/thenv` (`cli`)
- `servers/thenv` (`server`)

## Domain Contract Documents
- `docs/cmds-thenv-cli-foundation.md`
- `docs/servers-thenv-server-foundation.md`

## Cross-Domain Invariants
- Component identifiers remain stable: `cli`, `server`.
- Trust boundaries and redaction rules must remain consistent across both components.
- Secret lifecycle operations must use shared semantic contracts for push (create/update), pull (read), activate, and rotate flows.
- Policy and audit contracts must remain aligned across CLI and server behavior.

## Change Policy
- Security or interface changes require synchronized updates to this index and all affected component docs.
- CLI and server contracts must remain aligned on permissions, auditing, and error taxonomy.

## References
- `docs/project-template.md`
- `docs/domain-template.md`
- `docs/README.md`
