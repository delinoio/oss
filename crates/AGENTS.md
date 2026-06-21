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
- Keep channel and command identifiers stable and documented.
- Record storage and download behavior in project docs whenever changed.
- Keep direct installers and `cargo-binstall` metadata aligned with release asset names, signing contracts, and install docs.

### binpm-Specific Rules

- Keep `binpm` runtime work in `crates/binpm` aligned with `docs/project-binpm.md` and `docs/crates-binpm-foundation.md`.
- Keep the initial binpm skeleton explicit about unimplemented package-manager flows; do not silently perform partial installs, updates, cache mutations, removals, verification, explanation, or command execution before the corresponding contract-backed implementation exists.
- Preserve `~/.binpm` as the canonical global home directory for binpm-managed binaries, package records, global cache entries, and temporary extraction state.
- Treat `~/.binpm/cache` as the user-level global release asset cache shared by all binpm installs for the same account.
- Keep cache management and diagnostic command identifiers stable as `binpm cache list`, `binpm cache prune`, `binpm cache clean`, and `binpm cache key`.
- Ensure cache reuse is always verified against provider asset digests, upstream checksum material, successfully verified signatures, or locally recorded SHA-256 metadata before extraction or install finalization.
- Keep cache cleanup behavior separate from uninstall behavior: cache pruning and cleaning must not remove package records or executable links/copies under `~/.binpm/bin`.
- Keep `binpm cache key` read-only; it must not download, install, or populate cache entries.
- Keep source identifiers aligned with the documented enum contract: `github:owner/repo[@version]`, `github:<host>/owner/repo[@version]`, and `gitlab:<host>/<namespace...>/<project>[@version]`.
- Keep GitLab release selection stable by excluding upcoming releases, releases with future `released_at` values, and prerelease tag patterns.
- Keep GitLab release asset link selection HTTPS-only before candidate scoring and download, including final redirect targets.
- Keep GitLab generated `assets.sources` source archives out of installable asset scoring.
- Preserve `binpm.toml` and `binpm.lock` as the canonical project-local declaration and resolution files, with project-local executables installed under `$repoRoot/.binpm/bin`.
- Keep `binpm init` manifest creation rooted at the current Git worktree root when available, otherwise the nearest ancestor containing `binpm.toml` when present, otherwise the current directory.
- Keep target-specific asset overrides under `[tools.<cmd>.targets.<target-key>]` in `binpm.toml`.
- Keep `binpm explain` diagnostics actionable for target-scoring failures: use canonical target keys in generated override snippets, avoid credential-bearing URLs and transient machine paths, and distinguish unsupported installer-only releases from missing release assets.
- Keep explicit upstream binary selection stable: `binpm add <cmd> <source> --bin <upstream-binary>` persists `[tools.<cmd>].bin`, and `binpm x --package <source> --bin <upstream-binary> <cmd>` selects that upstream binary for one-off execution.
- Keep committed `binpm.lock` target-specific and deterministic; install timestamps and other machine-local metadata belong in uncommitted package records or logs.
- Keep committed `binpm.lock` URLs sanitized and free of query strings, fragments, credentials, and expiring signed download parameters.
- Keep local `binpm remove` cleanup aligned with project-local package records when they exist.
- Keep release asset selection deterministic and documented by OS, CPU architecture, and libc/ABI environment.
- Keep checksum/signature fallback behavior aligned with `docs/project-binpm.md` and `docs/crates-binpm-foundation.md`.
- Keep strict verification behavior aligned with `--require-verified` and `binpm verify --require-verified`; signature material must count only after successful verification under a documented trust policy.
- Keep local `binpm install`, `binpm update`, and `binpm x` behavior aligned with `--frozen-lockfile`, default `CI=true` frozen behavior, and `--no-frozen-lockfile`. Documented execution aliases `binpm exec` and `binpm run` must share `binpm x` lockfile behavior.
- Keep `binpm update` and `binpm remove` scope reporting and `--dry-run` previews aligned with `docs/crates-binpm-foundation.md`; previews must not mutate manifests, lockfiles, package records, cache references, or executables.
- Keep `--no-confirm` stable for script compatibility and future dangerous-operation confirmation prompts.
- Keep `binpm x` command execution aligned with the local manifest contract: use manifest-declared tools or explicit `--package`, prepend project-local bin directories to `PATH`, and do not infer GitHub repositories from command names. `binpm exec` and `binpm run` are aliases of that same execution behavior; `binpm x` remains canonical in contracts and examples.

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
