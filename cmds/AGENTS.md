### Instructions for `cmds/`

- Follow root `AGENTS.md` and command-specific docs in `docs/project-<id>/*.md`.
- Treat `docs/project-<id>/README.md` as the entrypoint and keep detailed command contracts in sibling `feature-*.md` files.
- Keep repository and domain rules in the appropriate `AGENTS.md` files.
- Write all source and comments in English.
- Prefer enums or typed constants over free-form string values.

### Scope in This Domain

- `cmds/derun`: Go tool for AI coding-agent workflow orchestration.
- `cmds/devmon`: Go daemon CLI for recurring folder command automation.
- `cmds/thenv`: Secure `.env` sharing CLI.
- `cmds/commit-tracker`: Commit Tracker collector component.
- `cmds/ttlc`: TTL compiler CLI for `.ttl` parsing/type-checking, Go code generation, `run` task execution, and cache-aware task execution contracts.

### Command Component Contract

- `cmds/commit-tracker` is the `Collector` component for `devkit-commit-tracker`.
- `cmds/thenv` is the `Cli` component for `thenv`.

### Go Command Rules

- Keep command boundaries explicit and documented.
- Keep configuration schemas documented and synchronized with implementation.
- Add enough structured logging for step-level debugging and failure diagnosis.
- Do not log secret values for sensitive workflows (including thenv operations).

### Integration Rules

- Keep integration boundaries with `apps/`, `servers/`, and other domains explicit in docs.
- Avoid undocumented cross-domain coupling.

### Testing and Validation

- Run relevant Go tests (`go test`) when code in this domain changes.
- Update `docs/project-devmon/README.md` and relevant `feature-*.md` files whenever devmon command shape or config contracts change.
- Update `docs/project-derun/README.md` and relevant `feature-*.md` files whenever command shape or config contracts change.
- Update `docs/project-thenv/README.md` and relevant `feature-*.md` files whenever thenv CLI operations or trust boundaries change.
- Update `docs/project-devkit-commit-tracker/README.md` and relevant `feature-*.md` files whenever commit-tracker collector contracts change.
- Update `docs/project-ttl/README.md` and relevant `feature-*.md` files whenever TTL compiler command shape, cache backend, or runtime boundaries change.
- Update `docs/project-ttl/feature-language-spec.md` whenever TTL syntax/type/invalidation/code-generation contracts change.
