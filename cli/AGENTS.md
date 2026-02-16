### Instructions for `cli/`

- Follow `docs/project-monorepo.md` and CLI project docs before implementing changes.
- Write all source and comments in English.
- Prefer typed constants and enums over raw string contracts.

### Scope in This Domain

- `cli/thenv`: Secure `.env` sharing CLI.

### CLI Rules

- Keep user-facing operations explicit (`push`, `pull`, `list`, `rotate`).
- Do not log secret values.
- Keep operation-level audit metadata in logs.
- Keep server integration contracts synchronized with `servers/thenv` documentation.

### Integration Rules

- Any interface change between CLI and server must update `docs/project-thenv.md`.
- Keep Devkit web console assumptions out of CLI runtime unless documented as shared contract.

### Testing and Validation

- Run relevant Go tests (`go test`) when code in this domain changes.
- Update docs in `docs/` whenever operation semantics or trust boundaries change.
