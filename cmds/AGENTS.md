### Instructions for `cmds/`

- Follow root `AGENTS.md` and command-specific docs in `docs/project-*.md` plus relevant `docs/cmds-*.md` files.
- Keep repository and domain rules in the appropriate `AGENTS.md` files.
- Write all source and comments in English.
- Prefer enums or typed constants over free-form string values.

### Scope in This Domain

- `cmds/delidev-cli`: DeliDev CLI, server, and execution Worker; follow scoped AGENTS and `docs/cmds-delidev-contract.md`. Product operations require authenticated Connect and an explicitly started server.
- `cmds/runmoor`: local ephemeral GitHub Actions runner manager; follow its scoped AGENTS and `docs/cmds-runmoor-foundation.md`.

- `cmds/derun`: Go tool for AI coding-agent workflow orchestration.

### Go Command Rules

- Keep command boundaries explicit and documented.
- Derun release and MCP server versions share `cmds/derun/internal/version/version.go`; Runmoor retains its `Version` constant. Version tests must work after any supported release bump.
- Keep configuration schemas documented and synchronized with implementation.
- Add enough structured logging for step-level debugging and failure diagnosis.
- Do not log secret values for sensitive workflows.

### Integration Rules

- Keep integration boundaries with `apps/`, `servers/`, and other domains explicit in docs.
- Avoid undocumented cross-domain coupling.

### Testing and Validation

- Run relevant Go tests (`go test`) when code in this domain changes.
- Update `docs/project-derun.md` and `docs/cmds-derun-foundation.md` whenever derun command contracts change.

- Derun exposes `--version` from its shared version constant. Native Linux Derun and Runmoor builds disable CGO and include amd64/arm64; package service/configuration ownership follows `docs/repository-linux-packages-contract.md`.

- Derun Linux amd64 and arm64 assets must remain available consistently through native packages, the direct shell installer and the prebuilt Homebrew formula.

### async-commit-hook
- `cmds/async-commit-hook` implements `ach`; follow `docs/cmds-async-commit-hook-contract.md`.
- Preserve durable receipts, isolated committed source, cross-worker queue ownership, strict gate semantics, descendant-safe cancellation and source-independent history. Tests use temporary config/state/repositories, never real user configuration.
