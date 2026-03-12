# Feature: architecture

## Architecture
- CLI layer (`cli.rs`) defines stable command and option contracts.
- Command dispatch layer (`commands/*`) maps parsed arguments to domain operations.
- Workspace graph layer (`workspace.rs`) loads packages, publishability, and dependency/dependent graphs.
- Git integration layer (`git.rs`) resolves merge bases, changed files, working tree status, and git mutation primitives for bump.
- Versioning layer (`versioning.rs`) applies semver transitions and manifest dependency requirement updates using `toml_edit`.
- Shared contract and error modules (`types.rs`, `errors.rs`) provide stable enums and deterministic exit behavior.
- Logging layer (`logging.rs`) configures `tracing` subscribers and structured operational events.

