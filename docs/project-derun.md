# Project: derun

## Goal
Provide a Go CLI that preserves terminal fidelity for AI-agent workflows and bridges MCP output transport.

## Project ID
`derun`

## Domain Ownership Map
- `cmds/derun`

## Domain Contract Documents
- `docs/cmds-derun-foundation.md`

## Cross-Domain Invariants
- CLI command identifiers and output contracts must remain stable for automation consumers.
- Terminal stream behavior must preserve ordering and ANSI compatibility by default.

## Change Policy
- Update this index and `docs/cmds-derun-foundation.md` together whenever command shape or runtime contracts change.
- Align command lifecycle changes with `cmds/AGENTS.md` and root `AGENTS.md`.

## References
- `docs/project-template.md`
- `docs/domain-template.md`
- `docs/README.md`
