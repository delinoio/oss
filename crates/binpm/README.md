# binpm

`binpm` is a Rust CLI for managing native command-line tools from release
assets without requiring Node.js or language-specific package managers.

This crate contains the `binpm` runtime: stable command parsing, typed contract
foundations, structured tracing setup, centralized errors, provider release
lookup, deterministic asset selection, download and cache handling, archive
extraction, local tooling records, and install/diagnostic flows. Remaining
unimplemented flows are gated behind explicit not-yet-implemented errors.

Provider release lookup may authenticate with documented environment variables.
Host-specific token variables take precedence, and enterprise or self-managed
hosts only use their host-specific token variable. Tokens and authorization
headers are never logged or persisted.

Canonical contracts live in:

- `../../docs/project-binpm.md`
- `../../docs/crates-binpm-foundation.md`

## Validation

```sh
cargo test -p binpm
cargo test --workspace --all-targets
```
