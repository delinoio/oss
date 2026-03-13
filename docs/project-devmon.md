# Project: devmon

## Goal
Provide a Go automation daemon that executes recurring workspace commands with menu bar lifecycle management.

## Project ID
`devmon`

## Domain Ownership Map
- `cmds/devmon`

## Domain Contract Documents
- `docs/cmds-devmon-foundation.md`

## Cross-Domain Invariants
- Schedule handling and command execution semantics must remain stable across daemon restarts.
- Operator-visible state must remain compatible with menu bar lifecycle controls.

## Change Policy
- Update this index and `docs/cmds-devmon-foundation.md` together when daemon command, schedule, or config contracts change.
- Keep `cmds/AGENTS.md` and root `AGENTS.md` synchronized with structural updates.

## References
- `docs/project-template.md`
- `docs/domain-template.md`
- `docs/README.md`
