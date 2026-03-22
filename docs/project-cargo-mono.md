# Project: cargo-mono

## Goal
Provide a Cargo subcommand for Rust monorepo lifecycle management, including version bump and publish orchestration.

## Project ID
`cargo-mono`

## Domain Ownership Map
- `crates/cargo-mono`

## Domain Contract Documents
- `docs/crates-cargo-mono-foundation.md`

## Cross-Domain Invariants
- The binary must remain compatible with `cargo mono` invocation conventions.
- Release automation integration must keep stable command semantics.
- Release tagging responsibility is split by command: `bump` must not create Git tags, while `publish` may create local Git tags for configured packages.
- Remote tag publication remains CI-owned: `auto-publish` pushes release tags with `git push --tags` after a successful `publish` run.
- Publish tag configuration must be opt-in through `[workspace.metadata.cargo-mono.publish.tag].packages`, and tag naming must remain `<crate>@v<version>`.
- Tag release automation must detect `cargo-mono@v*` and produce signed multi-OS prebuilt artifacts without changing CLI command behavior.
- Runtime failure messaging must follow the `Summary/Context/Hint` three-line contract while command behavior, output schema, and exit code semantics remain stable.
- Dependency-cycle conflicts in package ordering must identify cycle package names and dependency scope in `Context` without changing CLI flags, command behavior, or JSON output schema.
- Human output color controls must remain stable: global `--color <auto|always|never>`, `CARGO_MONO_OUTPUT_COLOR`, and `NO_COLOR` with precedence `--color` > `CARGO_MONO_OUTPUT_COLOR` > `NO_COLOR` > auto-detection.
- JSON output must remain ANSI-free and schema-stable regardless of color settings.

## Change Policy
- Update this index and `docs/crates-cargo-mono-foundation.md` together when command shape, release workflow, or ownership changes.
- Keep `crates/AGENTS.md` and root `AGENTS.md` aligned with structural changes.

## References
- `docs/project-template.md`
- `docs/domain-template.md`
- `docs/README.md`
