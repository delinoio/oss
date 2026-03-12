# Feature: interfaces

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
- When invoked through Cargo external-subcommand mode, a forwarded leading `mono` token is normalized before parsing so `cargo mono <args>` matches direct `cargo-mono <args>` behavior.
- `bump` and `publish` run a clean-working-tree preflight immediately after CLI parsing and before workspace loading.
- Workspace loading occurs after CLI parsing for executable subcommands; for `bump`/`publish`, it occurs only after clean-tree preflight passes.
- Log color override:
: `CARGO_MONO_LOG_COLOR=always|auto|never` controls ANSI color (`always` default).
: If `CARGO_MONO_LOG_COLOR` is unset or `auto`, `NO_COLOR` disables color; otherwise color remains enabled.
- Publish prefetch concurrency override:
: `CARGO_MONO_PUBLISH_PREFETCH_CONCURRENCY=<positive-int>` controls crates.io sparse index prefetch concurrency.
: Default is `16`, maximum is `64`; invalid values fall back to default with a warning log.

Target selection contract (`bump`, `publish`):
- `--all` default when no target selector is provided.
- `--changed` uses `changed` computation contract.
- `--package <name>` supports repeated explicit package targeting.
- Selectors are mutually exclusive (`--all`, `--changed`, `--package`).
- `--changed` honors `--include-path` and `--exclude-path` filters from `ChangedArgs`.

`changed` contract:
- Base ref default: `origin/main`.
- Computes merge base with `git merge-base <base> HEAD`.
- Uses `git diff --name-only <merge-base> HEAD` as baseline.
- `--include-uncommitted` additionally includes staged, unstaged, and untracked paths.
- `--exclude-path <glob>` is repeatable and defaults to excluding `**/AGENTS.md`.
- `--include-path <glob>` is repeatable and acts as an override for excluded paths.
- Include/exclude glob matching is evaluated against workspace-relative paths using `/`.
- Invalid include/exclude glob values fail with an invalid-input error.
- Global impact files (`Cargo.toml`, `Cargo.lock`, `rust-toolchain`) mark all workspace packages as changed.
- Global impact files cannot be filtered out by include/exclude path rules.
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
- For default crates.io (or `--registry crates-io`), the command prefetches selected crate versions from `https://index.crates.io/` before entering the publish loop.
- Prefetch uses Cargo sparse-index path rules and marks matching `(crate, version)` pairs as already published.
- Prefetch retries HTTP `429 Too Many Requests` with `Retry-After` delta-seconds when present and falls back to bounded exponential backoff (`2s`, `4s`, `8s`) when unavailable.
- Prefetch lookup failures are fail-open: failures are logged and affected crates continue through normal `cargo publish` execution.
- For non-crates.io registry names, prefetch is skipped and publish behavior falls back to direct `cargo publish` attempts.
- Skips non-publishable crates and already-published versions with explicit summary output.
- Publishes in workspace dependency topological order.
- Retries index-propagation-related failures with bounded backoff.
- Retries `cargo publish` failures classified as rate-limited (`429`/`Too Many Requests`) with `Retry-After` delta-seconds when present and falls back to bounded exponential backoff (`2s`, `4s`, `8s`) when unavailable.
- GitHub Actions auto-publish integration is defined in `.github/workflows/auto-publish.yml`.
- Auto-publish triggers on `push` to `main` and `workflow_dispatch`, and enforces a `main` branch guard at job runtime.
- Auto-publish requires `CARGO_REGISTRY_TOKEN` and maps it to Cargo registry authentication.
- Auto-publish executes `cargo run -p cargo-mono -- publish` for workspace-wide publish orchestration.

