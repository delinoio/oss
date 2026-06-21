# binpm

`binpm` is a Rust CLI for managing native command-line tools from release
assets without requiring Node.js or language-specific package managers.

This crate contains the binpm runtime: stable command parsing, typed contract
foundations, structured tracing setup, centralized errors, release lookup, asset
selection, download/cache/install flows, local tooling records, and command
execution.

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
