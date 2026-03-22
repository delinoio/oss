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
- Runtime failure messaging must follow the `Summary/Context/Hint` three-line contract while command behavior, output schema, and exit code semantics remain stable.
- Dependency-cycle conflicts in package ordering must identify cycle package names and dependency scope in `Context` without changing CLI flags, command behavior, or JSON output schema.

## Change Policy
- Update this index and `docs/crates-cargo-mono-foundation.md` together when command shape, release workflow, or ownership changes.
- Keep `crates/AGENTS.md` and root `AGENTS.md` aligned with structural changes.

## References
- `docs/project-template.md`
- `docs/domain-template.md`
- `docs/README.md`
