# cargo-mono

`cargo-mono` is the external Cargo subcommand that powers `cargo mono` for Rust monorepo workflows.

## Commands

```bash
cargo mono list
cargo mono changed [--base <ref>] [--include-uncommitted] [--direct-only]
cargo mono bump [--all|--changed|--package <name>] --level <major|minor|patch|prerelease>
cargo mono publish [--all|--changed|--package <name>] [--dry-run] [--max-attempts <count>]
```

`cargo mono publish` always delegates to `cargo publish --no-verify`, including `--dry-run`
execution.

Retryable publish failures stay narrowly scoped to index propagation lag and registry rate
limiting. Those failures retry indefinitely by default using capped exponential backoff (`2s`,
`4s`, `8s`, `16s`, `32s`, then `60s`), and rate-limit retries honor `Retry-After` when present.
Operators can cap retries with `--max-attempts <count>` or `CARGO_MONO_PUBLISH_MAX_ATTEMPTS`;
the CLI flag takes precedence over the environment variable.

## Publish Tag Configuration

`cargo mono publish` can create local Git tags for published crates when opt-in allowlist
configuration is set in the workspace manifest:

```toml
[workspace.metadata.cargo-mono.publish.tag]
packages = ["nodeup", "cargo-mono"]
```

Tag format is `<crate>@v<version>` (for example, `nodeup@v0.2.0`).

## Output Contract

All commands support a stable output mode switch:

```bash
cargo mono --output human <command>
cargo mono --output json <command>
```

## Comprehensive Test Coverage

The integration suite at `crates/cargo-mono/tests/cli.rs` covers end-to-end command behavior across real temporary git workspaces.

Coverage highlights:
- `list`: workspace discovery and publishability reporting.
- `changed`: base override, include/exclude filters, invalid glob rejection, direct-only vs dependent expansion, include-uncommitted behavior, and global-impact file handling.
- `bump`: clean-tree preflight, non-publishable skip behavior, manifest/version/dependency updates, dependent patch propagation, and release commit creation (no tag creation).
- `publish`: clean-tree preflight, fixed `--no-verify` delegation (including dry-run), unlimited retryable publish retries with optional caps, unknown package validation, and allowlist-based publish tag creation.
- Cargo external-subcommand mode compatibility (`cargo mono ...`) and top-level help/version behavior outside workspaces.

## Local Validation

Run from repository root:

```bash
cargo test -p cargo-mono --test cli
cargo test -p cargo-mono
```

For repository-wide verification:

```bash
cargo test
```
