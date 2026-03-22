### Instructions for `cmds/`

- Follow root `AGENTS.md` and command-specific docs in `docs/project-*.md` plus relevant `docs/cmds-*.md` files.
- Keep repository and domain rules in the appropriate `AGENTS.md` files.
- Write all source and comments in English.
- Prefer enums or typed constants over free-form string values.

### Scope in This Domain

- `cmds/derun`: Go tool for AI coding-agent workflow orchestration.
- `cmds/thenv`: Secure `.env` sharing CLI.
- `cmds/ttlc`: TTL compiler CLI for `.ttl` parsing/type-checking, Go code generation, `run` task execution, and cache-aware task execution contracts.

### Command Component Contract

- `cmds/thenv` is the `Cli` component for `thenv`.
- `cmds/ttlc` command runtime is defined in `docs/cmds-ttl-foundation.md`.
- TTL language semantics are defined in `docs/cmds-ttl-language-contract.md`.

### Go Command Rules

- Keep command boundaries explicit and documented.
- Keep configuration schemas documented and synchronized with implementation.
- Add enough structured logging for step-level debugging and failure diagnosis.
- Do not log secret values for sensitive workflows (including thenv operations).
- For `cmds/ttlc`, keep user-facing diagnostics/errors in centralized template helpers (`cmds/ttlc/internal/messages`) with stable enum-like IDs.

### Integration Rules

- Keep integration boundaries with `apps/`, `servers/`, and other domains explicit in docs.
- Avoid undocumented cross-domain coupling.

### Testing and Validation

- Run relevant Go tests (`go test`) when code in this domain changes.
- Update `docs/project-derun.md` and `docs/cmds-derun-foundation.md` whenever derun command contracts change.
- Update `docs/project-thenv.md` and `docs/cmds-thenv-cli-foundation.md` whenever thenv CLI operations or trust boundaries change.
- Update `docs/project-ttl.md` and `docs/cmds-ttl-foundation.md` whenever TTL compiler command shape, cache backend, or runtime boundaries change.
- Update `docs/project-ttl.md` and `docs/cmds-ttl-language-contract.md` whenever TTL syntax/type/invalidation/code-generation contracts change.
