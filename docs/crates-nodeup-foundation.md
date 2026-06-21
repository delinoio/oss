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
- `nodeup shim setup [--dir <path>]` must create or repair Nodeup-managed shims idempotently without replacing unrelated existing commands.
- Shim setup must default to `NODEUP_SHIM_DIR` when set, otherwise `$HOME/.local/bin`.
- Shim setup must emit PATH guidance when the shim directory is not active on `PATH`.
- macOS and Linux shim setup must use symlinks; Windows shim setup must use copied `.exe` aliases because symlink privileges are not guaranteed.
- Install/update command surfaces must preserve backward-compatible flags and outputs.
- Tracked exact-version selectors must be stored and processed by their canonical `v<semver>` identity, so `22.1.0` and `v22.1.0` are the same tracked selector.
- `nodeup update` treats exact-version selectors as immutable pins and reports them with `skipped-exact-version` rather than installing or reporting a newer runtime for that selector.
- Host support must include `macOS`, `Linux`, and `Windows` x64/arm64, while x86 hosts remain unsupported.
- Direct installers, runtime installation, and shim dispatch must detect unsupported x86 hosts before release asset download or delegated command planning.
- Unsupported host failures must use `unsupported-platform`, include the supported pairs `macos/x64`, `macos/arm64`, `linux/x64`, `linux/arm64`, `windows/x64`, and `windows/arm64`, and guide users to an x64/arm64 host or supported CI image.
- Homebrew installation must consume prebuilt release archives for `darwin/amd64`, `darwin/arm64`, `linux/amd64`, and `linux/arm64`.
- Direct install scripts must verify release artifacts with `SHA256SUMS` and Sigstore bundle sidecars (`<artifact>.sigstore.json`) via `cosign verify-blob --bundle`.
- Direct installers must remain available at `scripts/install/nodeup.sh` and `scripts/install/nodeup.ps1`.
- `cargo-binstall` metadata must resolve only first-party GitHub Release assets and disable `quick-install` and `compile` strategies.
- Runtime archive selection must remain enum-driven: `tar.xz` for `darwin/*` and `linux/*`, `zip` for `windows/*`.
- Windows runtime archives that unpack without a top-level directory must be normalized into the stable `bin/` runtime layout used by nodeup execution and linking flows.
- `yarn`/`pnpm` delegated execution must honor nearest `package.json` `packageManager` when present.
- `packageManager` parsing contract is strict: `<manager>@<exact-semver>` with manager limited to `yarn|pnpm`.
- `packageManager` manager-command mismatch must fail with `conflict`; malformed values must fail with `invalid-input`.
- `packageManager`-aware execution must use runtime `npm exec` (Corepack is out of scope).
- `which yarn|pnpm` in npm-exec mode must resolve to the runtime `npm` executable path.
- `toolchain install` and `toolchain uninstall` runtime selector lists are required command-line arguments.
- `toolchain install` must reject linked-name selectors before linked-runtime lookup; the error kind must be `invalid-input` whether or not a linked runtime by that name exists.
- Human output styling must support `--color auto|always|never` and `NODEUP_COLOR=auto|always|never`.
- Human output color precedence must remain `--color` > `NODEUP_COLOR` > `NO_COLOR` > stream-aware `auto`.
- User-facing `NodeupError` messages must follow the format `<cause>. Hint: <next action>`.
- `NodeupError` cause text should include deterministic key-value diagnostics when available (for example `selector`, `runtime`, `path`, `url`, `status`, `attempt`).
- JSON error envelopes must keep the stable fields `kind`, `message`, and `exit_code` while allowing optional structured `diagnostics`.
- Unsupported platform JSON diagnostics must be deterministic and include `os`, `architecture`, `platform_source`, optional `forced_platform`, and `supported_platforms`.
- `nodeup shim setup` JSON output must include `action`, `status`, `shim_dir`, `nodeup_binary`, `path_active`, `path_instruction`, and `shims`; each shim entry must include `alias`, `path`, `status`, and `method`.
- `nodeup self uninstall` must remove only Nodeup-owned data, cache, and config roots. It must not remove the running binary, managed shims, shell profile entries, or user PATH values.
- `nodeup self uninstall` JSON output must include `removed_paths`, `cleanup_boundaries`, `remaining_manual_steps`, and `likely_leftover_paths`.
- In `--output json` mode, clap parser failures must emit JSON error envelopes on stderr with no ANSI styling; without `--output json`, parser failures must keep clap's native human output.
- ANSI styling must never be injected into `--output json` payloads on stdout/stderr.
- `completions` must generate raw shell completion scripts for `bash`, `zsh`, `fish`, `powershell`, and `elvish`.
- `completions <shell> [command]` command scope must accept only top-level command identifiers and fail with `invalid-input` for unsupported scopes.
- Top-level completion scopes must include `shim`.
- `completions` output must remain raw script text on stdout even when `--output json` is requested.

## Storage
- Maintains local version metadata, installation roots, and shim state.
- Downloaded runtime artifacts must follow deterministic path resolution.

## Security
- Download and install flows must validate source and artifact integrity.
- Secrets must not be logged, and sensitive file paths should be minimized in logs.
- URL diagnostics in error messages must omit query strings and fragments.
- `NODEUP_RELEASE_INDEX_TTL_SECONDS` must accept non-negative integer second values, treat invalid empty, negative, or non-integer values as a safe warning category, and preserve the 600-second fallback.
- Channel resolution that uses stale release-index cache after refresh failure must expose selector, selected version, cache age, TTL, fallback reason, and sanitized source URL in logs and JSON command output where the command resolves a channel.

## Logging
- Use structured `tracing` logs for install, resolve, and dispatch flows.
- Default log filters must remain `nodeup=warn` for managed alias dispatch, `nodeup=warn` for human management commands, and `nodeup=off` for JSON management commands unless `RUST_LOG` explicitly overrides them.
- Include resolution source, requested channel, selected version, result state, release-index cache fallback state, and sanitized URL diagnostics.
- Delegated command planning logs must include `mode=direct|npm-exec`, `package_spec`, `package_json_path`, and `reason`.
- Completion generation logs must include shell, command scope, and `generated|failed` outcome state.

## Build and Test
- Local validation: `cargo test -p nodeup`
- Workspace baseline: `cargo test --workspace --all-targets`
- Release contract checks should align with `release-nodeup` workflow expectations.
- Release assets must include both standalone prebuilt binaries (`nodeup-<os>-<arch>[.exe]`) and compressed archives (`nodeup-<os>-<arch>.tar.gz|zip`) for `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/amd64`, and `windows/arm64`.
- Release signing outputs must include `SHA256SUMS.sigstore.json` and `<artifact>.sigstore.json` sidecars; legacy `.sig`/`.pem` sidecars are out of scope for direct installation.
- Homebrew release automation must render the prebuilt formula from release asset URLs and push tap updates directly to `delinoio/homebrew-tap` `main` with a dedicated tap-write credential.
- Install docs that choose to describe direct-install flows must keep Bash, PowerShell, `cargo-binstall`, and GitHub Actions usage aligned with the installer scripts and manifest metadata.
- `apps/public-docs` is not required to surface repo-local direct-installer script examples.
- Completion coverage must include successful script generation, invalid shell/scope validation, and JSON-mode raw output behavior.
- Output color coverage must include flag/env precedence, invalid env fallback, stream-aware auto-mode behavior, and JSON/completion ANSI exclusion.
- Parser-error coverage must include human clap output and JSON envelopes for root, nested subcommand, required argument, conflicting flag, unknown command, and unexpected extra argument failures.
- `packageManager` coverage must include strict parsing, mismatch conflicts, yarn v1 vs v2+ mapping, direct-binary preference, and npm-exec fallback behavior.
- Runtime install coverage must include `linux-arm64`, `windows-x64`, and `windows-arm64` archive selection and extraction behavior plus unsupported x86 CLI override failures.
- Shim setup coverage must include fresh setup, idempotent reruns, stale shim repair, and Windows copy mode.
- Self uninstall coverage must include removed path reporting and manual cleanup fields for binary, shims, and shell profile/PATH boundaries.

## Dependencies and Integrations
- Integrates with filesystem runtime shims and remote distribution channels.
- Integrates with release automation and package manager update workflows.

## Change Triggers
- Update `docs/project-nodeup.md` with this file when dispatch, storage, or channel contracts change.
- Update `crates/AGENTS.md` and root `AGENTS.md` when ownership or policy contracts change.

## References
- `docs/project-nodeup.md`
- `docs/domain-template.md`
