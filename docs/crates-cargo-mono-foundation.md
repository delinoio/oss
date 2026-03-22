# crates-cargo-mono-foundation

## Scope
- Project/component: `cargo-mono` crate foundation contract
- Canonical path: `crates/cargo-mono`

## Runtime and Language
- Runtime: Rust CLI (`cargo` subcommand integration)
- Primary language: Rust

## Users and Operators
- Release engineers operating monorepo version and publish workflows
- Maintainers running local and CI release automation

## Interfaces and Contracts
- Binary naming contract: `cargo-mono` must remain compatible with `cargo mono` invocation.
- Command identifiers for lifecycle operations must remain stable and documented.
- Publish and bump workflows must preserve scriptable output contracts for automation.

## Storage
- Uses workspace metadata and package manifests as canonical input.
- Uses temporary local files/caches only for transient command execution.

## Security
- Publishing workflows must rely on explicit credentials and least-privilege secrets.
- Logs and errors must avoid exposing secret registry tokens.

## Logging
- Use structured `tracing` logs for release automation operations.
- Include command phase, package target, and outcome status for debugging.

## Error UX Contract
- Runtime errors must use a single-line `summary + Hint: ...` format so operators can act immediately.
- Human stderr must include stable error kind labels while preserving the existing exit-code mapping contract.
- Error messaging improvements must not change CLI command behavior or JSON output schema keys.
- Error and log output must not expose secret credentials or registry tokens.

## Build and Test
- Local validation: `cargo test -p cargo-mono`
- Workspace validation baseline: `cargo test --workspace --all-targets`
- CI alignment: `.github/workflows/CI.yml` Rust jobs

## Dependencies and Integrations
- Integrates with Cargo workspace metadata and release workflows.
- Integrates with root automation (`auto-publish`) through stable command contracts.

## Change Triggers
- Update `docs/project-cargo-mono.md` with this file when command identifiers or ownership changes.
- Update `crates/AGENTS.md` and root `AGENTS.md` when policy or path contracts change.

## References
- `docs/project-cargo-mono.md`
- `docs/domain-template.md`
