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
- Windows copied aliases must have Nodeup ownership marker files next to them; setup may repair differing copied aliases only when the matching marker exists.
- Install/update command surfaces must preserve backward-compatible flags and outputs.
- Linked runtime command identifiers are `toolchain link` for registration and `toolchain unlink` for record removal.
- `toolchain unlink <name>...` must remove only nodeup settings records and tracked selectors; it must not delete files from external runtime directories.
- `toolchain unlink` must fail with `conflict` when the linked runtime name is referenced by the global default selector or a directory override.
- Runtime selector kinds exposed in JSON are `exact-version`, `channel`, and `linked-runtime`.
- `current` is the canonical selector for the newest Node.js release-index entry; `latest` is a supported alias of `current` and must report alias metadata in JSON selector-bearing output.
- Tracked exact-version selectors must be stored and processed by their canonical `v<semver>` identity, so `22.1.0` and `v22.1.0` are the same tracked selector.
- Tracked channel aliases must be stored and processed by canonical selector identity, so `latest` and `current` are one tracked selector, `current`.
- `nodeup update` treats exact-version selectors as immutable pins and reports them with `skipped-exact-version` rather than installing or reporting a newer runtime for that selector.
- Host support must include `macOS`, `Linux`, and `Windows` x64/arm64, while x86 hosts remain unsupported.
- Public Nodeup docs must explain that release asset names use `amd64` for the same CPU family commonly called `x64`.
- Direct installers, runtime installation, and shim dispatch must detect unsupported x86 hosts before release asset download or delegated command planning.
- Unsupported host failures must use `unsupported-platform`, include the supported pairs `macos/x64`, `macos/arm64`, `linux/x64`, `linux/arm64`, `windows/x64`, and `windows/arm64`, and guide users to an x64/arm64 host or supported CI image.
- Homebrew installation must consume prebuilt release archives for `darwin/amd64`, `darwin/arm64`, `linux/amd64`, and `linux/arm64`.
- Direct install scripts must verify release artifacts with `SHA256SUMS` and Sigstore bundle sidecars (`<artifact>.sigstore.json`) via `cosign verify-blob --bundle`.
- Direct install scripts must support bundle-enabled releases only; legacy `.sig` or `.pem` sidecars are out of scope and must not be treated as sufficient release verification material.
- Direct install scripts must fail before release lookup or artifact download when `cosign` is missing, describe the missing prerequisite with OS-specific setup guidance, and keep that message distinct from checksum mismatch or Sigstore verification failure.
- Direct installers must remain available at `scripts/install/nodeup.sh` and `scripts/install/nodeup.ps1`.
- Public direct-installer docs must keep remote POSIX and PowerShell examples on first-party `delinoio/oss` raw GitHub URLs for those same scripts, including current `main` commands and tag/commit-pinned command patterns for reproducible automation; third-party installer URLs and verification-disabling examples are out of scope.
- `cargo-binstall` metadata must resolve only first-party GitHub Release assets and disable `quick-install` and `compile` strategies; docs must explain that unsupported hosts and missing first-party assets fail instead of using source compilation or third-party binary fallback.
- Runtime archive selection must remain enum-driven: `tar.xz` for `darwin/*` and `linux/*`, `zip` for `windows/*`.
- Windows runtime archives that unpack without a top-level directory must be normalized into the stable `bin/` runtime layout used by nodeup execution and linking flows.
- Windows managed shim aliases should be copied or linked executable aliases such as `node.exe` and `npm.exe`; batch wrappers that call `nodeup.exe` must not be documented as supported shims because they do not preserve the wrapper name as Nodeup's `argv[0]`. Delegated Windows runtime package-manager executables normalize to `bin/<command>.cmd` for `npm`, `npx`, `yarn`, `pnpm`, and `corepack`.
- Linked runtime validation must require a runnable `node` command during `toolchain link` and active-runtime availability checks.
- Linked runtime names are case-sensitive, but names that differ from reserved channel selectors only by case, such as `LTS`, `Current`, or `LATEST`, must be rejected with `invalid-input`.
- Legacy settings and overrides that already contain reserved-channel case variants as linked runtime selectors must remain removable and must continue to report linked-runtime metadata in JSON output.
- Unix linked-runtime validation must require an executable permission bit on `bin/node`; Windows platform behavior must select `bin/node.exe` for `node`.
- `toolchain link` success output must report per-managed-shim direct command availability for `node`, `npm`, `npx`, `yarn`, and `pnpm`, including checked runtime paths, linked runtime name/path in JSON, install-on-demand eligibility, and PATH/PATHEXT precedence guidance.
- Missing linked-runtime command failures from `which`, `run`, package-manager fallback planning, or managed shim dispatch must include actionable human checked-path context and JSON diagnostics for `command`, `runtime`, `checked_paths`, `selected_path`, direct executable existence/runnability, linked runtime name/path when applicable, install-on-demand eligibility, install-on-demand scope, and PATH/PATHEXT precedence guidance.
- `nodeup run <runtime> <command>` missing-version errors must state that `nodeup run` requires explicit `--install`, include the retry shape `nodeup run --install <runtime> ...`, and distinguish that behavior from managed shim dispatch install-on-demand for missing selected version runtimes. JSON diagnostics must include `install_on_demand_eligible: false` and `retry_with_install`.
- `yarn`/`pnpm` delegated execution must honor nearest `package.json` `packageManager` when present.
- `packageManager` parsing contract is strict: `<manager>@<exact-semver>` with manager limited to `yarn|pnpm`.
- `packageManager` manager-command mismatch must fail with `conflict`; malformed values must fail with `invalid-input`.
- Invalid `packageManager` errors must identify the failed part (`value`, `separator`, `manager`, or `version`), name the problem (`non-string`, `malformed-separator`, `missing-manager`, `missing-version`, `unsupported-manager`, or `non-exact-semver`), and include actionable examples. JSON errors must keep `kind`, `message`, and `exit_code` and add deterministic `diagnostics` fields such as `package_json_path`, `expected`, `supported_managers`, `failed_part`, `problem`, optional `manager`, optional `version`, optional `received_type`, and `correction`.
- `packageManager`-aware execution must use runtime `npm exec` (Corepack is out of scope).
- `yarn`/`pnpm` npm-exec planning must be visible in human output, JSON output, and structured planning logs. Diagnostics must include requested command, mode, reason, executable path, package spec, package JSON path when known, and whether the package spec is pinned.
- When `packageManager` is absent and no direct runtime package-manager binary exists, unpinned npm-exec fallback specs (`@yarnpkg/cli-dist` or `pnpm`) must be surfaced and must recommend adding an exact `packageManager` value for reproducibility.
- `which yarn|pnpm` in npm-exec mode must resolve to the runtime `npm` executable path and must label that path as npm-exec delegation rather than a direct package-manager binary.
- `toolchain install` and `toolchain uninstall` runtime selector lists are required command-line arguments.
- `toolchain install` must reject linked-name selectors before linked-runtime lookup; the error kind must be `invalid-input` whether or not a linked runtime by that name exists.
- `toolchain uninstall` must remove exact installed versions only; channel selectors and linked-name selectors must fail with `invalid-input` before reference-blocker checks.
- `toolchain uninstall` must fail atomically with `conflict` when any requested exact runtime is referenced by an exact-version global default or exact-version directory override.
- `toolchain uninstall` reference-blocker conflicts must report each blocking reference type (`global-default` or `directory-override`), the blocking path, selector, runtime, and follow-up commands for changing the default or unsetting/updating the override.
- `toolchain uninstall` JSON error diagnostics must include deterministic `blocked_versions` and `blockers` fields so scripts do not parse prose.
- Human output styling must support `--color auto|always|never` and `NODEUP_COLOR=auto|always|never`.
- Human output color precedence must remain `--color` > `NODEUP_COLOR` > `NO_COLOR` > stream-aware `auto`.
- `nodeup show color` must report effective color decisions for human stdout, human stderr, and logs, including ignored invalid `NODEUP_COLOR` and `NODEUP_LOG_COLOR` values.
- User-facing `NodeupError` messages must follow the format `<cause>. Hint: <next action>`.
- `NodeupError` cause text should include deterministic key-value diagnostics when available (for example `selector`, `runtime`, `path`, `url`, `status`, `attempt`).
- JSON error envelopes must keep the stable fields `kind`, `message`, and `exit_code` while allowing optional structured `diagnostics`.
- Unsupported platform JSON diagnostics must be deterministic and include `os`, `architecture`, `platform_source`, optional `forced_platform`, and `supported_platforms`.
- `nodeup shim setup` JSON output must include `action`, `status`, `shim_dir`, `nodeup_binary`, `path_active`, `path_instruction`, and `shims`; each shim entry must include `alias`, `path`, `status`, and `method`.
- `nodeup self uninstall` must remove only Nodeup-owned data, cache, and config roots. It must not remove the running binary, managed shims, shell profile entries, or user PATH values.
- `nodeup self uninstall` must report managed shim leftovers from the same default shim directory used by `nodeup shim setup`, including Windows copy marker files when present.
- `nodeup self uninstall` JSON output must include `removed_paths`, `cleanup_boundaries`, `remaining_manual_steps`, and `likely_leftover_paths`.
- In `--output json` mode, clap parser failures must emit JSON error envelopes on stderr with no ANSI styling; without `--output json`, parser failures must keep clap's native human output.
- ANSI styling must never be injected into `--output json` payloads on stdout/stderr.
- `completions` must generate raw shell completion scripts for `bash`, `zsh`, `fish`, `powershell`, and `elvish`.
- `completions <shell> [command]` command scope must accept only top-level command identifiers and fail with `invalid-input` for unsupported scopes.
- Invalid completion subcommand scopes such as `toolchain install` must suggest the valid top-level scope, for example `nodeup completions bash toolchain`, and JSON errors must include deterministic `rejected_scope`, `allowed_scope_category`, `allowed_scopes`, and optional `suggested_scope` diagnostics.
- Top-level completion scopes must include `shim`.
- `completions` output must remain raw script text on stdout even when `--output json` is requested.
- Script-safe stdout guidance must map structured automation to `--output json`, newline-delimited runtime lists to setting `RUST_LOG=off` before `nodeup toolchain list --quiet`, completion redirection to setting `RUST_LOG=off` before `nodeup completions <shell> >file`, and log-free human output to setting `RUST_LOG=off` before `nodeup <command>`.
- Tracing logs must be written to stderr when enabled so stdout remains reserved for command results, JSON payloads, quiet runtime identifiers, delegated command stdout, and raw completion scripts. Management `--output json` keeps tracing logs off by default so JSON stdout and stderr payloads remain parseable unless `RUST_LOG` explicitly enables tracing.

## Storage
- Maintains local version metadata, installation roots, and shim state.
- Downloaded runtime artifacts must follow deterministic path resolution.
- Linked runtime records are stored in settings under `linked_runtimes`; removing a link must also remove the matching tracked selector while preserving external runtime directories.

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
- Delegated command planning logs must include `mode=direct|npm-exec`, `package_spec`, `package_spec_pinned`, `package_json_path`, and `reason`.
- Completion generation logs must include shell, command scope, and `generated|failed` outcome state.

## Build and Test
- Local validation: `cargo test -p nodeup`
- Workspace baseline: `cargo test --workspace --all-targets`
- Release contract checks should align with `release-nodeup` workflow expectations.
- Release assets must include both standalone prebuilt binaries (`nodeup-<os>-<arch>[.exe]`) and compressed archives (`nodeup-<os>-<arch>.tar.gz|zip`) for `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/amd64`, and `windows/arm64`.
- Release signing outputs must include `SHA256SUMS.sigstore.json` and `<artifact>.sigstore.json` sidecars; legacy `.sig`/`.pem` sidecars are out of scope for direct installation.
- Homebrew release automation must render the prebuilt formula from release asset URLs and push tap updates directly to `delinoio/homebrew-tap` `main` with a dedicated tap-write credential.
- Install docs that choose to describe direct-install flows must keep Bash, PowerShell, `cargo-binstall`, and GitHub Actions usage aligned with the installer scripts and manifest metadata.
- Direct-install documentation must make `cosign` a prerequisite before remote installer commands and must describe missing `cosign` as a prerequisite failure rather than a verification bypass opportunity.
- `apps/public-docs` is not required to surface repo-local direct-installer script examples.
- Completion coverage must include successful script generation, invalid shell/scope validation, and JSON-mode raw output behavior.
- Output color coverage must include flag/env precedence, diagnostic reporting, invalid env fallback, stream-aware auto-mode behavior, and JSON/completion ANSI exclusion.
- Parser-error coverage must include human clap output and JSON envelopes for root, nested subcommand, required argument, conflicting flag, unknown command, and unexpected extra argument failures.
- `packageManager` coverage must include strict parsing diagnostics, mismatch conflicts, yarn v1 vs v2+ mapping, direct-binary preference, pinned npm-exec planning output, unpinned npm-exec fallback output, and `which` npm-exec JSON fields.
- Runtime install coverage must include `linux-arm64`, `windows-x64`, and `windows-arm64` archive selection and extraction behavior plus unsupported x86 CLI override failures.
- Shim setup coverage must include fresh setup, idempotent reruns, stale shim repair, and Windows copy mode.
- Self uninstall coverage must include removed path reporting and manual cleanup fields for binary, shims, and shell profile/PATH boundaries.
- Linked runtime coverage must include unlink success without external directory deletion, missing-link `not-found` errors, default/override unlink conflicts, Unix executable-bit validation, and Windows `node.exe` name selection.
- Runtime uninstall coverage must include default reference blockers, directory override reference blockers, combined default-and-override blockers, JSON blocker diagnostics, and distinct channel-selector rejection.

## Dependencies and Integrations
- Integrates with filesystem runtime shims and remote distribution channels.
- Integrates with release automation and package manager update workflows.

## Change Triggers
- Update `docs/project-nodeup.md` with this file when dispatch, storage, or channel contracts change.
- Update `crates/AGENTS.md` and root `AGENTS.md` when ownership or policy contracts change.

## References
- `docs/project-nodeup.md`
- `docs/domain-template.md`
