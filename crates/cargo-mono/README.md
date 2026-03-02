# cargo-mono

`cargo-mono` is the external Cargo subcommand that powers `cargo mono` for Rust monorepo workflows.

## Commands

```bash
cargo mono list
cargo mono changed [--base <ref>] [--include-uncommitted] [--direct-only]
cargo mono bump [--all|--changed|--package <name>] --level <major|minor|patch|prerelease>
cargo mono publish [--all|--changed|--package <name>] [--dry-run]
```

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
- `bump`: clean-tree preflight, non-publishable skip behavior, manifest/version/dependency updates, dependent patch propagation, release commit creation, and crate tag creation.
- `publish`: clean-tree preflight, non-publishable skip behavior, and unknown package validation.
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
