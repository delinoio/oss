# crates-nodeup-foundation

## Scope
- Project/component: `nodeup` crate foundation contract
- Canonical path: `crates/nodeup`

## Runtime and Language
- Runtime: Rust CLI and shim dispatch runtime
- Primary language: Rust

## Users and Operators
- Developers managing local Node.js versions
- Maintainers operating release and distribution workflows

## Interfaces and Contracts
- Channel and command identifiers must remain stable and documented.
- Shim dispatch behavior must remain deterministic by executable name.
- Install/update command surfaces must preserve backward-compatible flags and outputs.

## Storage
- Maintains local version metadata, installation roots, and shim state.
- Downloaded runtime artifacts must follow deterministic path resolution.

## Security
- Download and install flows must validate source and artifact integrity.
- Secrets must not be logged, and sensitive file paths should be minimized in logs.

## Logging
- Use structured `tracing` logs for install, resolve, and dispatch flows.
- Include resolution source, requested channel, selected version, and result state.

## Build and Test
- Local validation: `cargo test -p nodeup`
- Workspace baseline: `cargo test --workspace --all-targets`
- Release contract checks should align with `release-nodeup` workflow expectations.

## Dependencies and Integrations
- Integrates with filesystem runtime shims and remote distribution channels.
- Integrates with release automation and package manager update workflows.

## Change Triggers
- Update `docs/project-nodeup.md` with this file when dispatch, storage, or channel contracts change.
- Update `crates/AGENTS.md` and root `AGENTS.md` when ownership or policy contracts change.

## References
- `docs/project-nodeup.md`
- `docs/domain-template.md`
