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

`binpm init` creates new manifests without overwriting existing files.
`--manifest-path <PATH>` is the explicit destination escape hatch when the
default Git-root or manifest-ancestor destination is not desired. `binpm env`
prints non-mutating PATH commands, supports optional shell inference, accepts
`pwsh` as PowerShell syntax, and exposes `--global` or `--local` to print only
one PATH command.

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
