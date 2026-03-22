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
- User-facing `NodeupError` messages must follow the format `<cause>. Hint: <next action>`.
- `NodeupError` cause text should include deterministic key-value diagnostics when available (for example `selector`, `runtime`, `path`, `url`, `status`, `attempt`).
- JSON error envelopes must keep the stable shape `kind`, `message`, and `exit_code` while allowing message text improvements.
- `completions` must generate raw shell completion scripts for `bash`, `zsh`, `fish`, `powershell`, and `elvish`.
- `completions <shell> [command]` command scope must accept only top-level command identifiers and fail with `invalid-input` for unsupported scopes.
- `completions` output must remain raw script text on stdout even when `--output json` is requested.

## Storage
- Maintains local version metadata, installation roots, and shim state.
- Downloaded runtime artifacts must follow deterministic path resolution.

## Security
- Download and install flows must validate source and artifact integrity.
- Secrets must not be logged, and sensitive file paths should be minimized in logs.
- URL diagnostics in error messages must omit query strings and fragments.

## Logging
- Use structured `tracing` logs for install, resolve, and dispatch flows.
- Include resolution source, requested channel, selected version, and result state.
- Completion generation logs must include shell, command scope, and `generated|failed` outcome state.

## Build and Test
- Local validation: `cargo test -p nodeup`
- Workspace baseline: `cargo test --workspace --all-targets`
- Release contract checks should align with `release-nodeup` workflow expectations.
- Completion coverage must include successful script generation, invalid shell/scope validation, and JSON-mode raw output behavior.

## Dependencies and Integrations
- Integrates with filesystem runtime shims and remote distribution channels.
- Integrates with release automation and package manager update workflows.

## Change Triggers
- Update `docs/project-nodeup.md` with this file when dispatch, storage, or channel contracts change.
- Update `crates/AGENTS.md` and root `AGENTS.md` when ownership or policy contracts change.

## References
- `docs/project-nodeup.md`
- `docs/domain-template.md`
