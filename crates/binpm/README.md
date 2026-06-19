# binpm

`binpm` is a Rust CLI for managing native command-line tools from release
assets without requiring Node.js or language-specific package managers.

This crate currently contains the runtime skeleton: stable command parsing,
typed contract foundations, structured tracing setup, centralized errors, and
minimal safe implementations for bootstrapping and diagnostics. Release lookup,
asset selection, download, cache population, extraction, install, update, and
execution flows are intentionally gated behind explicit not-yet-implemented
errors until their storage and verification behavior is implemented.

Canonical contracts live in:

- `../../docs/project-binpm.md`
- `../../docs/crates-binpm-foundation.md`

## Validation

```sh
cargo test -p binpm
cargo test --workspace --all-targets
```
