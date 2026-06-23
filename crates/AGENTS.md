### Instructions for `crates/`

- Follow root `AGENTS.md` and each crate-specific project document.
- Keep repository and domain rules in the appropriate `AGENTS.md` files.
- Write all source and comments in English.
- Prefer enums over free-form strings for stable internal and external contracts.

### Scope in This Domain

- `crates/binpm`: Rust-based Node-free binary package manager for release assets.
- `crates/cargo-mono`: Cargo-based Rust monorepo management CLI.
- `crates/nodeup`: Rust-based Node.js version manager.
- `crates/with-watch`: Rust-based filesystem-watching command wrapper.
- `crates/serde-feather`: Size-first serde runtime-facing core crate.
- `crates/serde-feather-macros`: Proc-macro companion crate for serde-feather.
- `crates/rustia`: Serde-based LLM JSON runtime crate.
- `crates/rustia-llm`: aisdk tool adapter crate for rustia-based function-calling input validation.
- `crates/rustia-macros`: Proc-macro derive companion crate for rustia.

### Rust Workspace Rules

- Add new crates as explicit workspace members in root `Cargo.toml`.
- Keep crate naming aligned with project IDs when possible.
- Document behavior contracts in project index docs and relevant crate-domain docs before large implementation changes.
- Planned crate paths must not be added as workspace members until the crate skeleton exists.
- For new package scaffolding, default `publish = false` until publish contracts are explicitly approved.
- Prefer minimal default features and keep optional capabilities opt-in for size-sensitive crates.
- Keep proc-macro crates and runtime crates separated by explicit crate boundaries.

### nodeup-Specific Rules

- Preserve rustup-like shim behavior: symlink strategy plus executable-name dispatch.
- Keep `nodeup shim setup` as the stable idempotent setup/repair command for managed `node`, `npm`, `npx`, `yarn`, and `pnpm` shims.
- Keep Windows shim setup documented and implemented as copied `.exe` aliases with adjacent Nodeup ownership marker files because symlink privileges are not guaranteed and stale copies must be repairable without replacing unrelated executables.
- Keep `nodeup self uninstall` scoped to Nodeup-owned data, cache, and config roots; binary, shim, and shell profile/PATH cleanup must remain manual and visible in human and JSON output.
- Keep channel and command identifiers stable and documented.
- Record storage and download behavior in project docs whenever changed.
- Keep direct installers and `cargo-binstall` metadata aligned with release asset names, signing contracts, and install docs. Nodeup direct installers must preflight missing `cosign` before release lookup or artifact download, and `cargo-binstall` must stay first-party-asset-only with `quick-install` and `compile` fallbacks disabled.
- Keep unsupported x86 host handling aligned across direct installers, runtime installation, shim dispatch, JSON diagnostics, and Nodeup docs.
- Keep `nodeup update` exact-version selector messaging aligned across human output, JSON diagnostics, CLI help, crate README, `apps/nodeup-docs`, `docs/project-nodeup.md`, and `docs/crates-nodeup-foundation.md`: exact versions are immutable pins reported with stable `skipped-exact-version` status, and output must point users who intended to move pins toward installing or selecting a newer exact runtime.
- Keep Nodeup script-safe output guidance aligned across CLI help, crate README, `apps/nodeup-docs`, `docs/project-nodeup.md`, and `docs/crates-nodeup-foundation.md`: `--output json` for structured automation, `nodeup toolchain list --quiet` for raw runtime identifiers, `nodeup completions <shell> >file` for completion redirection, default Nodeup logging off for those script-safe forms, and `RUST_LOG=off` only when scripts also require quiet stderr after a logging filter was set elsewhere.
- Keep Nodeup tracing logs on stderr when enabled so stdout remains parseable for command results, JSON payloads, quiet runtime identifiers, delegated command stdout, and raw completion scripts.
- Keep Nodeup human output color precedence stable as `--color` > `NODEUP_COLOR` > `NO_COLOR` > stream-aware `auto`, and keep `nodeup show color` reporting effective human stdout, human stderr, and log color decisions, ignored invalid `NODEUP_COLOR`/`NODEUP_LOG_COLOR` values, and `NO_COLOR` overrides by Nodeup-specific color environment variables.
- Keep invalid `NODEUP_COLOR` and `NODEUP_LOG_COLOR` values noticeable on stderr for human-mode commands without writing warnings to JSON stdout or adding ANSI styling to JSON payloads.
- Keep checksum mismatch and runtime download diagnostics for mirror overrides explicit about sanitized release index and download-base source details. URL diagnostics must strip credentials, query strings, and fragments, and hints must tell users to verify that `NODEUP_INDEX_URL` and `NODEUP_DOWNLOAD_BASE_URL` point to matching Node.js release data.

### binpm-Specific Rules

- Keep `binpm` runtime work in `crates/binpm` aligned with `docs/project-binpm.md` and `docs/crates-binpm-foundation.md`.
- Keep the initial binpm skeleton explicit about unimplemented package-manager flows; do not silently perform partial installs, updates, cache mutations, removals, verification, explanation, or command execution before the corresponding contract-backed implementation exists.
- Preserve `~/.binpm` as the canonical global home directory for binpm-managed binaries, package records, global cache entries, and temporary extraction state.
- Treat `~/.binpm/cache` as the user-level global release asset cache shared by all binpm installs for the same account.
- Keep cache management and diagnostic command identifiers stable as `binpm cache list`, `binpm cache prune`, `binpm cache clean`, and `binpm cache key`.
- Ensure cache reuse is always verified against provider asset digests, upstream checksum material, successfully verified signatures, or locally recorded SHA-256 metadata before extraction or install finalization.
- Keep cache cleanup behavior separate from uninstall behavior: cache pruning and cleaning must not remove package records or executable links/copies under `~/.binpm/bin`.
- Keep `binpm cache clean` output explicit in human and JSON modes about removing global cache asset entries while preserving cache references, package records, and executable links/copies.
- Keep `binpm cache prune` responsible for stale structured local-project cache-reference cleanup before asset pruning; legacy plain-text references remain preserving until rewritten by future local install, update, or removal flows and prune output must guide that migration.
- Keep `binpm cache key` read-only; it must not download, install, or populate cache entries.
- Keep `binpm cache key` explicit about missing lockfiles through a human warning or structured JSON status.
- Keep source identifiers aligned with the documented enum contract: `github:owner/repo[@version]`, `github:<host>/owner/repo[@version]`, and `gitlab:<host>/<namespace...>/<project>[@version]`. GitLab sources always require an explicit host, including `gitlab:gitlab.com/<namespace...>/<project>[@version]` for GitLab.com; `gitlab:group/project` is intentionally invalid.
- GitHub.com shorthand source input may be normalized only at CLI input boundaries. Persisted `binpm.toml`, `binpm.lock`, package records, cache metadata, logs, and JSON diagnostics must use canonical source identifiers and must not accept or emit shorthand source strings.
- Keep provider token selection host-scoped: GitHub.com may use `BINPM_GITHUB_TOKEN_GITHUB_COM`, `BINPM_GITHUB_TOKEN`, or `GITHUB_TOKEN`; GitHub Enterprise must use `BINPM_GITHUB_TOKEN_<NORMALIZED_HOST>`; GitLab.com may use `BINPM_GITLAB_TOKEN_GITLAB_COM`, `BINPM_GITLAB_TOKEN`, or `GITLAB_TOKEN`; self-managed GitLab must use `BINPM_GITLAB_TOKEN_<NORMALIZED_HOST>`. For explicit hosts, `<NORMALIZED_HOST>` must encode non-ASCII-alphanumeric UTF-8 bytes as `_HH_` uppercase hexadecimal so distinct hosts cannot share a token variable. Generic SaaS tokens must not be sent to enterprise or self-managed hosts.
- Keep release lookup diagnostics distinct for missing authentication, insufficient permissions, and rate limiting, and keep tokens, authorization headers, private-token headers, query strings, fragments, and credential-bearing URLs out of logs, errors, persisted URLs, cache metadata, package records, and lockfiles. Missing-auth diagnostics for explicit GitHub Enterprise and self-managed GitLab hosts must print the exact expected host-scoped token variable name, and JSON diagnostics must expose safe env-var name fields without token values.
- Keep source version selectors exact-tag-only: omitted `@version` selects latest stable, while `@latest`, semver range-like selectors, channel selectors, and major-version pins are rejected before manifest or lockfile persistence. Diagnostics may suggest an exact leading-`v` tag alternative when the release list shows one, but exact-match semantics must not change.
- Keep GitLab release selection stable by excluding upcoming releases, releases with future `released_at` values, and known SemVer prerelease tag identifiers while preserving non-SemVer stable GitLab tags.
- Keep GitLab release asset link selection HTTPS-only before candidate scoring and download, including final redirect targets.
- Keep source archives and GitLab generated `assets.sources` source archives out of installable asset selection, while preserving source-archive-only diagnostics that distinguish those releases from no-asset and target-mismatch failures.
- Keep Linux musl missing-libc diagnostics concrete: name rejected assets, recommend upstream explicit `musl`/`static`/`portable`/`universal`/`any` naming first, and present target overrides only after compatibility verification.
- Preserve `binpm.toml` and `binpm.lock` as the canonical project-local declaration and resolution files, with project-local executables installed under `$repoRoot/.binpm/bin`.
- Keep `binpm init` manifest creation rooted at the current Git worktree root when available, otherwise the nearest ancestor containing `binpm.toml` when present, otherwise the current directory. Keep init output explicit: print the resolved full manifest destination before creation or overwrite refusal, then print a clear created-manifest line after successful creation. Keep `--manifest-path <PATH>` as the documented explicit destination escape hatch for creating a new `binpm.toml`; it must still refuse existing files and must not overwrite manifests.
- Keep target-specific asset overrides under `[tools.<cmd>.targets.<target-key>]` in `binpm.toml`.
- Keep `binpm explain` diagnostics actionable for release selection and target-scoring failures: expose skipped release tags and reasons, use canonical target keys in generated override snippets, avoid credential-bearing URLs and transient machine paths, distinguish unsupported installer-only releases from missing release assets, and distinguish read-only source explain from network-free package-record explain.
- Preserve stable `--json` output for read-only binpm diagnostics (`list`, `info`, `outdated`, `doctor`, `explain`, `verify`, and `cache list`): emit one compact JSON object on stdout for success, avoid ANSI color in JSON-mode command output, keep parseable stderr error envelopes with `error.message` and `error.exit_code`, and reuse documented enum values for scope, target, checksum source, and verification state.
- Keep explicit global command naming and upstream binary selection stable: `binpm install <source> --as <cmd> --bin <upstream-binary>` uses the explicit global alias while preserving source identity and selected upstream binary in package records, and human install output must show the installed command alias and selected upstream binary as separate fields. Source-form install is global-only; reject `binpm install <source> --local` with guidance to use `binpm add <cmd> <source>` for project-local tools.
- Keep explicit local declaration and one-off binary selection stable: `binpm add <cmd> <source> --bin <upstream-binary>` persists `[tools.<cmd>].bin`, `binpm add --manifest-only` mutates only `binpm.toml`, `binpm add ... --also <cmd=upstream-binary>` expands to separate deterministic `[tools.<cmd>]` declarations, and `binpm x --package <source> --bin <upstream-binary> [cmd]` selects that upstream binary for one-off execution without inferring sources from command names. Manifest-only success output and later `list`, `doctor`, and frozen `x` diagnostics must make declared-but-not-installed state visible and point to `binpm install`.
- Keep archive binary ambiguity diagnostics stable: list plausible executable candidates, include concrete retry commands using `--bin`, and mention repeated `--also <cmd=upstream-binary>` values for local multi-binary archives while preserving one `[tools.<cmd>]` manifest table per command.
- Keep committed `binpm.lock` target-specific and deterministic; install timestamps and other machine-local metadata belong in uncommitted package records or logs.
- Keep committed `binpm.lock` URLs sanitized and free of query strings, fragments, credentials, and expiring signed download parameters.
- Keep local `binpm remove` cleanup aligned with project-local package records when they exist.
- Keep release asset selection deterministic and documented by OS, CPU architecture, and libc/ABI environment.
- Keep checksum/signature fallback behavior aligned with `docs/project-binpm.md` and `docs/crates-binpm-foundation.md`.
- Keep strict verification behavior aligned with `--require-verified` and `binpm verify --require-verified`; signature material must count only after successful verification under a documented trust policy.
- Keep package signature verification distinct from binpm direct-installer Sigstore verification. For binpm-installed packages, raw signature sidecars are metadata only until the package verifier validates the selected asset under the documented package trust policy.
- Keep local `binpm install`, `binpm update`, and `binpm x` behavior aligned with `--frozen-lockfile`, default `CI=true` frozen behavior, and `--no-frozen-lockfile`. Frozen commands must fail when they would need to create or modify `binpm.lock`, except empty-manifest local updates that require no lockfile changes must succeed without creating `binpm.lock` and must report the no-op without file-change plans. Frozen mode is a lockfile write guard, not an offline or cache-only mode. Documented execution aliases `binpm exec` and `binpm run` must share `binpm x` lockfile behavior.
- Keep frozen local install and `x` restore behavior deterministic: missing `.binpm/bin` executables and `.binpm/packages` package records may be restored from existing target lock records only when cache bytes match the locked SHA-256. If cache repair needs a download, it may use only the lockfile's persisted sanitized asset URL, must validate the recorded SHA-256 before installing or populating cache, and must not require provider release-list pagination. Same-origin locked GitHub or GitLab provider URLs may use runtime-only host-scoped provider authentication when configured; external locked asset URLs must not receive provider credentials.
- Keep `binpm update` and scoped `binpm remove` scope reporting and `--dry-run` previews aligned with `docs/crates-binpm-foundation.md`; previews must not mutate manifests, lockfiles, package records, cache references, or executables. Keep all-tools update mode visible when no command names are supplied. Keep local update aligned with exact-version manifest advancement to latest stable across `binpm.toml`, `binpm.lock`, and project-local executables. Keep `binpm update --global [cmd...]` aligned with global package records, existing alias and selected-binary preservation, latest-stable release lookup, and global install cache/install/verification finalization behavior.
- Keep `--no-confirm` stable for script compatibility and future dangerous-operation confirmation prompts.
- Keep `binpm x` command execution aligned with the local manifest contract: use manifest-declared tools or explicit `--package`, prepend project-local bin directories to `PATH`, and do not infer GitHub repositories from command names. `binpm exec` and `binpm run` are aliases of that same execution behavior; `binpm x` remains canonical in contracts and examples.
- Keep `binpm env --shell` shell values explicit: support `bash`, `zsh`, `fish`, and `powershell`; accept `pwsh` as a PowerShell alias; accept `cmd` only to report that cmd.exe support is deferred with actionable cmd.exe PATH guidance. Keep omitted `--shell` best-effort inference and `--global`/`--local` output narrowing non-mutating.
- Keep global install and doctor PATH setup guidance opt-in and non-mutating; do not edit user shell profiles from existing commands or imply project-local `.binpm/bin` entries are suitable for profile persistence.
- Keep binpm publishability, release tags, direct installers, cargo-binstall metadata, and Homebrew packaging aligned with `docs/project-binpm.md` and `docs/crates-binpm-foundation.md`.
- Keep `.github/workflows/release-binpm.yml`, `scripts/install/binpm.sh`, `scripts/install/binpm.ps1`, and `crates/binpm/Cargo.toml` synchronized with release asset names and signing contracts.

### cargo-mono-Specific Rules

- Keep command identifiers stable and documented in `docs/project-cargo-mono.md` and `docs/crates-cargo-mono-foundation.md`.
- Preserve `cargo mono` subcommand compatibility (`cargo-mono` binary naming contract).
- Keep release-tag responsibility split: `bump` must not create tags, and `publish` may create tags only for packages listed in `[workspace.metadata.cargo-mono.publish.tag].packages`.
- Keep `publish` delegation aligned with the documented contract: `cargo mono publish` must invoke `cargo publish --no-verify` in both execute and dry-run modes.
- Keep `publish` package ordering based on manifest-declared workspace path dependencies, including optional feature-gated dependencies; do not rely only on Cargo's default-feature resolve graph.
- Ensure release automation (`bump`, `publish`) logs include structured operational context.
- Keep runtime error output on the fixed `Summary/Context/Hint` three-line contract and include only safe debugging context values.
- Keep direct installers and `cargo-binstall` metadata aligned with release asset names, signing contracts, and install docs.

### with-watch-Specific Rules

- Keep passthrough, shell, and `exec --input` command shapes stable and documented in `docs/project-with-watch.md` and `docs/crates-with-watch-foundation.md`.
- Keep default rerun filtering content-hash-based, with `--no-hash` as the documented metadata-only override.
- Keep `--clear` as a best-effort TTY-only output refresh flag; redirected or piped stdout must stay byte-for-byte clean.
- Keep shell support scoped to command-line expressions and do not silently broaden into shell-script control-flow without updating docs first.
- Keep logs sufficient to explain inferred inputs, watcher anchors, snapshot counts, and rerun causes.
- Keep public release contracts aligned across root publish-tag allowlist, `.github/workflows/release-with-watch.yml`, and Homebrew packaging assets.
- Keep direct installers and `cargo-binstall` metadata aligned with release asset names, signing contracts, and install docs.

### serde-feather-Specific Rules

- Keep `serde-feather` as the runtime-facing crate and `serde-feather-macros` as the proc-macro crate.
- Keep binary-size-first defaults: minimal default features and no convenience dependencies by default.
- Keep stable derive macro identifiers (`FeatherSerialize`, `FeatherDeserialize`) aligned with `docs/project-serde-feather.md` and crate component docs.

### rustia-Specific Rules

- Keep `rustia` as the runtime-facing crate, `rustia-llm` as the aisdk adapter crate, and `rustia-macros` as the proc-macro companion crate.
- Keep stable rustia identifiers (`Validate`, `IValidation`, `IValidationError`, `LLMData`, `LlmJsonParseResult`, `LlmJsonParseError`, `LlmToolInput`, `LlmToolOutput`, `LlmToolSpec`, `tool`, `LlmToolBuildError`, `LlmToolInputError`, `LlmToolExecutionError`, and `#[derive(LLMData)]`) synchronized with `docs/project-rustia.md`, `docs/crates-rustia-core-foundation.md`, `docs/crates-rustia-llm-foundation.md`, and `docs/crates-rustia-macros-foundation.md`.
- Keep non-contracted v0 identifiers explicitly documented as unstable until promoted in rustia contract docs.
- Keep future macro/runtime compatibility constraints synchronized with rustia project and crate contracts.

### Multi-Component Contract Sync

- `serde-feather` core crate changes must update `docs/crates-serde-feather-core-foundation.md` and `docs/project-serde-feather.md`.
- `serde-feather-macros` changes must update `docs/crates-serde-feather-macros-foundation.md` and `docs/project-serde-feather.md`.
- `rustia` core crate changes must update `docs/crates-rustia-core-foundation.md` and `docs/project-rustia.md`.
- `rustia-llm` crate changes must update `docs/crates-rustia-llm-foundation.md` and `docs/project-rustia.md`.
- `rustia-macros` crate changes must update `docs/crates-rustia-macros-foundation.md` and `docs/project-rustia.md`.

### Testing and Validation

- If Rust code changes in this domain, run `cargo test` from repository root.
- Keep logs sufficient for debugging install, dispatch, and runtime resolution flow.
- Keep CLI logs colorized by default for human operators, with explicit opt-out controls.
