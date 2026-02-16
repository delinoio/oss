### Instructions for `cmds/`

- Follow `docs/project-monorepo.md` and command-specific docs in `docs/project-*.md`.
- Write all source and comments in English.
- Prefer enums or typed constants over free-form string values.

### Scope in This Domain

- `cmds/derun`: Go tool for AI coding-agent workflow orchestration.

### Go Command Rules

- Keep command boundaries explicit and documented.
- Keep configuration schemas documented and synchronized with implementation.
- Add enough structured logging for step-level debugging and failure diagnosis.

### Integration Rules

- Keep integration boundaries with `apps/`, `servers/`, and other domains explicit in docs.
- Avoid undocumented cross-domain coupling.

### Testing and Validation

- Run relevant Go tests (`go test`) when code in this domain changes.
- Update `docs/project-derun.md` whenever command shape or config contracts change.
