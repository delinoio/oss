# binpm

`binpm` is a Rust CLI for managing native command-line tools from release
assets without requiring Node.js or language-specific package managers.

This crate contains the `binpm` runtime: stable command parsing, typed contract
foundations, structured tracing setup, centralized errors, provider release
lookup, deterministic asset selection, download and cache handling, archive
extraction, local tooling records, install/diagnostic flows, and command
execution.

Provider release lookup may authenticate with documented environment variables.
Host-specific token variables take precedence, and enterprise or self-managed
hosts only use their host-specific token variable. Tokens and authorization
headers are never logged or persisted.

Cache commands keep asset cleanup separate from uninstall behavior:
`binpm cache clean` removes global cache asset entries while preserving cache
references, package records, and executable links or copies, and `binpm cache
prune` repairs stale structured project references before pruning unreferenced
assets. `binpm cache key` remains read-only and reports missing lockfiles
explicitly.

`binpm update [cmd...] [--local|--global]` supports local and global tools.
Omitting command names updates every tool in the selected scope, and output
states that all-tools mode before printing the planned update list. Local
updates advance exact-version manifest entries to the latest stable release and
write `binpm.toml`, `binpm.lock`, and project-local executables consistently.

Use `-v`/`--verbose` for info-level tracing diagnostics and `--debug` for
debug-level tracing diagnostics. `BINPM_LOG` remains supported when no CLI
verbosity flag is provided; CLI verbosity flags take precedence.

Canonical contracts live in:

- `../../docs/project-binpm.md`
- `../../docs/crates-binpm-foundation.md`

## Validation

```sh
cargo test -p binpm
cargo test --workspace --all-targets
```
