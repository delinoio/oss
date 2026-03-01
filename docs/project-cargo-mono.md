# Project: cargo-mono

## Goal
`cargo-mono` provides a Cargo-native command surface (`cargo mono`) for operating Rust workspaces at monorepo scale.
The project focuses on deterministic package selection, safe version orchestration, and publish automation with structured operational logs.

## Path
- `crates/cargo-mono`

## Runtime and Language
- Rust CLI (`cargo` external subcommand)

## Users
- Rust monorepo maintainers
- Release managers handling multi-crate publication
- CI operators automating version bump and publish workflows

## In Scope
- Cargo external subcommand entrypoint via `cargo-mono` binary and `cargo mono ...` invocation.
- Workspace package discovery (`list`) based on `cargo metadata`.
- Git-aware package impact resolution (`changed`) with optional working tree inclusion.
- Version bump orchestration (`bump`) with independent versions and internal dependency requirement updates.
- Optional dependent package patch propagation on bump (`--bump-dependents`).
- Automated release commit and crate-level tag generation.
- Publish orchestration (`publish`) with dependency-aware ordering and retry handling for index propagation lag.
- Human and JSON output modes (`--output human|json`).

## Out of Scope
- Non-Rust workspace management.
- Changelog generation or release-note authoring.
- Automatic remote push of release commits or tags.
- Cross-registry credential management beyond invoking Cargo publish with the configured environment.

## Architecture
- CLI layer (`cli.rs`) defines stable command and option contracts.
- Command dispatch layer (`commands/*`) maps parsed arguments to domain operations.
- Workspace graph layer (`workspace.rs`) loads packages, publishability, and dependency/dependent graphs.
- Git integration layer (`git.rs`) resolves merge bases, changed files, working tree status, and git mutation primitives for bump.
- Versioning layer (`versioning.rs`) applies semver transitions and manifest dependency requirement updates using `toml_edit`.
- Shared contract and error modules (`types.rs`, `errors.rs`) provide stable enums and deterministic exit behavior.
- Logging layer (`logging.rs`) configures `tracing` subscribers and structured operational events.

## Interfaces
Canonical command identifiers:

```ts
enum CargoMonoCommand {
  List = "list",
  Changed = "changed",
  Bump = "bump",
  Publish = "publish",
}
```

Canonical output format identifiers:

```ts
enum CargoMonoOutputFormat {
  Human = "human",
  Json = "json",
}
```

Canonical bump level identifiers:

```ts
enum CargoMonoBumpLevel {
  Major = "major",
  Minor = "minor",
  Patch = "patch",
  Prerelease = "prerelease",
}
```

CLI entrypoint:
- `cargo mono [--output <human|json>] <subcommand> ...`
- `cargo mono --help` and `cargo mono --version` must succeed without workspace discovery.
- `bump` and `publish` run a clean-working-tree preflight immediately after CLI parsing and before workspace loading.
- Workspace loading occurs after CLI parsing for executable subcommands; for `bump`/`publish`, it occurs only after clean-tree preflight passes.

Target selection contract (`bump`, `publish`):
- `--all` default when no target selector is provided.
- `--changed` uses `changed` computation contract.
- `--package <name>` supports repeated explicit package targeting.
- Selectors are mutually exclusive (`--all`, `--changed`, `--package`).

`changed` contract:
- Base ref default: `origin/main`.
- Computes merge base with `git merge-base <base> HEAD`.
- Uses `git diff --name-only <merge-base> HEAD` as baseline.
- `--include-uncommitted` additionally includes staged, unstaged, and untracked paths.
- Global impact files (`Cargo.toml`, `Cargo.lock`, `rust-toolchain`) mark all workspace packages as changed.
- Default mode includes direct changes plus reverse dependency propagation.
- `--direct-only` disables reverse dependency propagation.

`bump` contract:
- Requires `--level <major|minor|patch|prerelease>`.
- `--level prerelease` requires `--preid <identifier>`.
- Skips non-publishable crates and reports skip reasons.
- Updates `package.version` for selected crates.
- Updates internal dependency version requirements for bumped crates.
- Updates root `Cargo.toml` `[workspace.dependencies]` version pins for bumped internal crates when present.
- Optional dependent patch propagation via `--bump-dependents`.
- Requires clean working tree unless `--allow-dirty` is provided.
- Enforces clean-tree policy in preflight before workspace metadata loading to avoid false positives from metadata side effects (for example untracked `Cargo.lock` generation).
- Creates one commit: `chore(release): bump <n> crate(s)`.
- Creates crate tags: `<crate>-v<version>`.
- Does not push commits or tags.

`publish` contract:
- Publish execution is the default behavior.
- `--dry-run` switches to validation-only publish mode.
- Requires clean working tree unless `--allow-dirty` is provided.
- Enforces clean-tree policy in preflight before workspace metadata loading to avoid false positives from metadata side effects (for example untracked `Cargo.lock` generation).
- Default registry is crates.io; `--registry <name>` overrides.
- Skips non-publishable crates and already-published versions with explicit summary output.
- Publishes in workspace dependency topological order.
- Retries index-propagation-related failures with bounded backoff.

## Storage
- No project-owned persistent storage.
- Reads and updates workspace `Cargo.toml` manifests during bump operations.
- Uses git repository state for change detection and release commits/tags.
- Uses Cargo local caches managed by Cargo itself.

## Security
- Never logs registry credentials, authentication tokens, or secret environment values.
- Requires explicit `--allow-dirty` to bypass clean-tree checks for mutating operations.
- Treats non-publishable crate metadata as an enforcement boundary and skips publication.
- Uses explicit command argument construction (no shell interpolation) for git and cargo subprocesses.

## Logging
Required structured log fields:
- `command_path`
- `arg_shape`
- `workspace_root`
- `package`
- `action`
- `outcome`
- `retry_attempt`
- `git_ref`
- `base_ref`

Operational expectations:
- Log command invocation shape before execution.
- Log clean-tree preflight start and outcome for `bump` and `publish`.
- Log package selection decisions and skip reasons.
- Log bump mutation summary (updated manifests, commit id, tags).
- Log publish attempt lifecycle including retries and terminal outcome.
- Use Rust `tracing` for all operational logs.

## Build and Test
Planned commands:
- Build: `cargo build -p cargo-mono`
- Test: `cargo test -p cargo-mono`
- Workspace validation: `cargo test`

## Roadmap
- Phase 1: Initial command surface (`list`, `changed`, `bump`, `publish`) with structured logs.
- Phase 2: Extended release ergonomics (custom retry policies, richer output summaries).
- Phase 3: Policy integrations (organization-specific release constraints and validations).

## Open Questions
- Whether to support lockstep version mode in addition to independent version mode.
- Whether to add optional changelog generation hooks during bump.
- Whether to add provenance/signing metadata checks before publish.

## References
- `docs/project-template.md`
- `docs/monorepo.md`
