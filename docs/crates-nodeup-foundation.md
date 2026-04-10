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
- Binary entrypoints must force-link `swc_malloc` allocator policy, while the library target remains allocator-agnostic for downstream consumers.
- Shim dispatch behavior must remain deterministic by executable name (`node`, `npm`, `npx`, `yarn`, `pnpm`).
- Install/update command surfaces must preserve backward-compatible flags and outputs.
- Host support must include `macOS`, `Linux`, and `Windows` x64/arm64, while x86 hosts remain unsupported.
- Homebrew installation must consume prebuilt release archives for `darwin/amd64`, `darwin/arm64`, `linux/amd64`, and `linux/arm64`.
- Direct install scripts must verify release artifacts with `SHA256SUMS` and Sigstore bundle sidecars (`<artifact>.sigstore.json`) via `cosign verify-blob --bundle`.
- Runtime archive selection must remain enum-driven: `tar.xz` for `darwin/*` and `linux/*`, `zip` for `windows/*`.
- Windows runtime archives that unpack without a top-level directory must be normalized into the stable `bin/` runtime layout used by nodeup execution and linking flows.
- `yarn`/`pnpm` delegated execution must honor nearest `package.json` `packageManager` when present.
- `packageManager` parsing contract is strict: `<manager>@<exact-semver>` with manager limited to `yarn|pnpm`.
- `packageManager` manager-command mismatch must fail with `conflict`; malformed values must fail with `invalid-input`.
- `packageManager`-aware execution must use runtime `npm exec` (Corepack is out of scope).
- `which yarn|pnpm` in npm-exec mode must resolve to the runtime `npm` executable path.
- Human output styling must support `--color auto|always|never` and `NODEUP_COLOR=auto|always|never`.
- Human output color precedence must remain `--color` > `NODEUP_COLOR` > `NO_COLOR` > stream-aware `auto`.
- User-facing `NodeupError` messages must follow the format `<cause>. Hint: <next action>`.
- `NodeupError` cause text should include deterministic key-value diagnostics when available (for example `selector`, `runtime`, `path`, `url`, `status`, `attempt`).
- JSON error envelopes must keep the stable shape `kind`, `message`, and `exit_code` while allowing message text improvements.
- ANSI styling must never be injected into `--output json` payloads on stdout/stderr.
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
- Delegated command planning logs must include `mode=direct|npm-exec`, `package_spec`, `package_json_path`, and `reason`.
- Completion generation logs must include shell, command scope, and `generated|failed` outcome state.

## Build and Test
- Local validation: `cargo test -p nodeup`
- Workspace baseline: `cargo test --workspace --all-targets`
- Release contract checks should align with `release-nodeup` workflow expectations.
- Release assets must include both standalone prebuilt binaries (`nodeup-<os>-<arch>[.exe]`) and compressed archives (`nodeup-<os>-<arch>.tar.gz|zip`) for `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/amd64`, and `windows/arm64`.
- Release signing outputs must include `SHA256SUMS.sigstore.json` and `<artifact>.sigstore.json` sidecars; legacy `.sig`/`.pem` sidecars are out of scope for direct installation.
- Homebrew release automation must render the prebuilt formula from release asset URLs and push tap updates directly to `delinoio/homebrew-tap` `main` with a dedicated tap-write credential.
- Completion coverage must include successful script generation, invalid shell/scope validation, and JSON-mode raw output behavior.
- Output color coverage must include flag/env precedence, invalid env fallback, stream-aware auto-mode behavior, and JSON/completion ANSI exclusion.
- `packageManager` coverage must include strict parsing, mismatch conflicts, yarn v1 vs v2+ mapping, direct-binary preference, and npm-exec fallback behavior.
- Runtime install coverage must include `linux-arm64`, `windows-x64`, and `windows-arm64` archive selection and extraction behavior.

## Dependencies and Integrations
- Integrates with filesystem runtime shims and remote distribution channels.
- Integrates with release automation and package manager update workflows.

## Change Triggers
- Update `docs/project-nodeup.md` with this file when dispatch, storage, or channel contracts change.
- Update `crates/AGENTS.md` and root `AGENTS.md` when ownership or policy contracts change.

## References
- `docs/project-nodeup.md`
- `docs/domain-template.md`
