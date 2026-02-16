### Instructions for `cli/`

- Follow `docs/project-monorepo.md` and CLI project docs before implementing changes.
- Write all source and comments in English.
- Prefer typed constants and enums over raw string contracts.

### Scope in This Domain

- No active project is currently assigned in this domain.
- This domain remains reserved for future standalone CLI projects.

### CLI Rules

- Keep command boundaries and user-facing operations explicit.
- Do not log secret values in sensitive flows.
- Keep operation-level audit metadata in logs when handling privileged actions.

### Integration Rules

- Any cross-domain interface contract must be documented in the relevant `docs/project-*.md`.
- Keep frontend assumptions out of CLI runtime unless documented as shared contract.

### Testing and Validation

- Run relevant Go tests (`go test`) when code in this domain changes.
- Update docs in `docs/` whenever operation semantics, interfaces, or trust boundaries change.
