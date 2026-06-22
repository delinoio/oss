# Project: binpm

## Goal
Provide a Rust-based, Node-free binary package manager for installing and running command-line tools from release assets. `binpm` exists to replace dependency-heavy installer flows, especially npm-based global installers and `npx`-style execution paths that require Node.js even when the delivered artifact is only a native executable.

## Project ID
`binpm`

## Domain Ownership Map
- `crates/binpm`
- `apps/binpm-docs`

## Domain Contract Documents
- `docs/crates-binpm-foundation.md`
- `docs/apps-binpm-docs-foundation.md`

## Cross-Domain Invariants
- `binpm` is implemented as a Rust CLI under `crates/binpm`.
- `apps/binpm-docs` is the Rspress static documentation app for `binpm`.
- `apps/binpm-docs` must use the repository-default Rspress/Rsbuild-family static documentation toolchain and Cloudflare Pages deployment contract unless this project index and `docs/apps-binpm-docs-foundation.md` document a replacement.
- The canonical production URL for `apps/binpm-docs` is `https://binpm.delino.io`.
- binpm documentation routes exposed by `apps/binpm-docs` are `/`, `/installation`, `/getting-started`, `/commands`, `/local-tooling`, `/cache-and-verification`, `/releases`, `/troubleshooting`, and `/reference`.
- `apps/binpm-docs` must expose a visible GitHub repository link to `https://github.com/delinoio/oss` in the top-level social links and in the document-page footer.
- binpm documentation content must remain documentation-only and must not expand runtime behavior, release automation, package-manager backend scope, checksum discovery, signature verification, or global update behavior without corresponding runtime contract updates.
- binpm documentation must not infer current product behavior or page content from the live `https://binpm.delino.io` site; repository contracts remain the source of truth.
- The runtime implementation includes clap-based command parsing, discoverable global verbosity flags, enum-backed contract foundations, structured `tracing` setup, centralized CLI error handling, README/test scaffolding, release source parsing, provider release lookup clients, deterministic release asset candidate scoring, asset downloads with interactive large-download progress, archive extraction for documented formats, TOML-backed `binpm.toml` and `binpm.lock` parsing/writing, global and project-local package records, global cache metadata, URL sanitization, SHA-256 cache validation, update planning, and atomic file writes.
- `binpm init`, `binpm env`, `binpm doctor`, source-form and local-command `binpm explain`, `binpm cache key`, `binpm cache list`, `binpm cache prune`, `binpm cache clean`, `binpm list`, `binpm remove`, `binpm info`, `binpm outdated`, `binpm update`, `binpm verify`, bare-executable and archive install flows, and `binpm x` execution have concrete runtime behavior. Checksum sidecar/manifest discovery and signature verification remain implementation work.
- Stable source identifiers are `github:owner/repo[@version]`, `github:<host>/owner/repo[@version]`, and `gitlab:<host>/<namespace...>/<project>[@version]`. Common GitHub.com shorthands such as `owner/repo[@version]` and HTTPS GitHub.com repository or release URLs may be accepted as input, but manifests, lockfiles, package records, and diagnostics must normalize them back to canonical `github:` source strings. GitLab URL shorthands and arbitrary direct URLs remain unsupported and must produce guidance for the canonical `gitlab:` or `github:` forms instead of adding new backends.
- Release lookup authentication is environment-variable based only: GitHub.com uses host-specific `BINPM_GITHUB_TOKEN_GITHUB_COM` before `BINPM_GITHUB_TOKEN` and `GITHUB_TOKEN`; GitHub Enterprise uses only `BINPM_GITHUB_TOKEN_<NORMALIZED_HOST>`; GitLab.com uses host-specific `BINPM_GITLAB_TOKEN_GITLAB_COM` before `BINPM_GITLAB_TOKEN` and `GITLAB_TOKEN`; self-managed GitLab uses only `BINPM_GITLAB_TOKEN_<NORMALIZED_HOST>`. Generic SaaS tokens must never be sent to explicit enterprise or self-managed hosts. For explicit hosts, `<NORMALIZED_HOST>` uppercases ASCII alphanumeric bytes and encodes every other UTF-8 byte as `_HH_` uppercase hexadecimal so distinct hosts cannot share a token variable.
- Release lookup diagnostics must distinguish missing authentication, insufficient permissions, and rate limiting without exposing tokens, authorization headers, private-token headers, query strings, fragments, or credential-bearing URLs in logs, errors, persisted URLs, cache metadata, package records, or lockfiles.
- Versionless installs must resolve to the latest stable release exposed by the source provider; GitHub sources must exclude draft and prerelease releases, and GitLab sources must exclude upcoming releases, releases with future `released_at` values, and prerelease tag patterns.
- Explicit source versions are exact release tag requests. `@latest`, SemVer range-like selectors, channel selectors, and major-version pins are rejected with diagnostics that point users to either omit `@version` for latest stable or use an exact release tag.
- Binary selection must be deterministic and target-aware across operating system, CPU architecture, libc or ABI environment, and documented CPU feature variants. CPU feature tokens such as `baseline` and `modern` are diagnostics and scoring inputs, not architecture aliases; baseline-compatible assets are preferred unless explicit host CPU capability selection is supported.
- Source-form `binpm explain <source>` may perform read-only provider release lookup and must print source parsing, provider API URL, release selection, target normalization, asset candidate scoring, selected asset, rejection reasons, actionable remediation summaries, and target override snippets without mutating manifests, lockfiles, package records, cache entries, or executables.
- Current-host target detection must fail clearly for unsupported operating systems or CPU architectures instead of mapping them to a supported fallback target.
- The asset selection heuristic must remain fully documented in `docs/crates-binpm-foundation.md` before implementation changes alter scoring behavior.
- `~/.binpm` remains the canonical global home directory for globally installed binaries, package records, cache entries, and temporary extraction state.
- `~/.binpm/cache` is the user-level global asset cache shared by all `binpm` installs for the same account.
- Global cache reuse must never bypass provider asset digest, upstream checksum, signature, or locally recorded SHA-256 verification.
- Project-local installs must maintain user-level cache references so pruning from one checkout does not remove cache entries still referenced by another checkout. New cache references must include enough project and command metadata for stale-reference diagnostics.
- Cache management commands must preserve installed package records and `~/.binpm/bin` entries unless a separate uninstall contract explicitly changes that behavior. `binpm cache clean` removes only global cache asset entries under `~/.binpm/cache/sha256` and preserves the local-project reference index under `~/.binpm/cache/refs`.
- `binpm cache prune` must remove stale structured local-project cache references before deciding which cache entries are still referenced. Stale references are references whose project-local package record is absent or no longer points at the referenced cache key. Legacy plain-text references remain preserving until rewritten by a future install or removal flow.
- `binpm doctor` must report stale and legacy cache-reference counts without mutating them.
- `binpm cache key` must be a read-only diagnostic command that prints a current-target CI cache key derived from `binpm.lock`. When `binpm.lock` is absent, human output must warn that the empty lockfile digest is used, and JSON output must expose lockfile status.
- Project-local tooling must use `binpm.toml` at the repository root as the committed local tool manifest.
- `binpm init` must print the resolved full `binpm.toml` destination before it creates or refuses to overwrite the manifest. Creation targets the current Git worktree root when available, otherwise the nearest ancestor containing `binpm.toml` when present, otherwise the current directory. There is no contracted flag for forcing initialization in a nested current directory.
- Project-local tooling must use `binpm.lock` at the repository root as the committed deterministic resolution record for release tags, target-specific assets, selected binaries, checksums, and installed paths.
- Committed lockfiles must store sanitized canonical asset URLs only, never credential-bearing or expiring download URLs.
- Local target-specific asset overrides must live under `[tools.<cmd>.targets.<target-key>]`, must use canonical target keys, and must preserve deterministic lockfile output. Diagnostic snippets must never include credential-bearing URLs, runtime cache paths, or other transient machine-local fields.
- Local command names must be executable basenames and must not contain path separators or relative path components.
- `binpm install <source> --as <cmd> --bin <upstream-binary>` must preserve explicit global command aliases and selected upstream binaries in global package records without changing normalized source identity.
- `binpm add <cmd> <source> --bin <upstream-binary>` must persist the selected upstream binary in `binpm.toml`; `binpm add --manifest-only` must only mutate `binpm.toml`; `binpm add ... --also <cmd=upstream-binary>` must expand to separate deterministic `[tools.<cmd>]` declarations; and `binpm x --package <source> --bin <upstream-binary> [CMD] [args...]` must use that selected upstream binary for one-off execution without inferring sources from command names.
- Project-local executable files must be installed under `$repoRoot/.binpm/bin`; other project-local binpm runtime state must stay under `$repoRoot/.binpm`.
- The current install implementation finalizes bare executable assets and archives in `.tar.gz`, `.tgz`, `.tar.xz`, `.txz`, `.tar.zst`, and `.zip` formats. Archive install finalization validates member paths, rejects symlinks and hard links, selects an executable member, and installs only the selected member into the managed bin directory. On POSIX hosts, unambiguous archive selections are installed with executable permissions even when an upstream archive omits Unix executable metadata; ambiguous non-executable members must fail with a diagnostic instead of being guessed.
- Archive binary ambiguity errors must list the plausible executable candidates and include concrete retry commands using `--bin`.
- `binpm x CMD [args...]` must run commands from the local manifest or from an explicitly supplied `--package`; it must not guess a GitHub repository from `CMD`. `binpm x --package <source>` may omit `CMD` only as a safe explicit-source shortcut that exposes the repository basename or explicit `--bin` basename.
- `binpm x` is the canonical execution command. `binpm exec` and `binpm run` are documented aliases for discoverability and must share the same argument forwarding, local lockfile, install-on-demand, `--package`, PATH, and exit-code behavior.
- Local `install`, `update`, and `x` must honor `--frozen-lockfile`; `CI=true` enables frozen lockfile behavior by default, and `--no-frozen-lockfile` is the explicit escape hatch.
- A frozen local update for an empty manifest with no `binpm.lock` changes must succeed without creating `binpm.lock`; frozen local install, update, and `x` still fail when selected tools or orphan cleanup would require lockfile creation or modification.
- Frozen failures must produce human-readable and JSON diagnostics that identify the frozen mode (`CI=true` or `--frozen-lockfile`), reason (`missing_lockfile`, `missing_lockfile_record`, `stale_lockfile_record`, or `orphan_lockfile_record`), affected file or tool target record, whether `binpm x` was attempting an on-demand executable/package-record sync, the exact `binpm.lock` path that would change, and the safest next command such as `binpm install --local`, `binpm update --local <cmd>`, committing `binpm.lock`, or using `--no-frozen-lockfile` only as a local-development escape hatch.
- Frozen local install and `x` may restore missing project-local executables and package records from an existing lockfile plus SHA-256-verified global cache bytes, or by downloading the lockfile's persisted asset URL and validating the recorded SHA-256, without provider release-list pagination.
- `binpm` must not require Node.js, npm, pnpm, yarn, or Bun to install native binary tools.
- `binpm` release tags use `binpm@v<semver>`.
- Release automation must publish both standalone prebuilt binaries and archive assets for `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/amd64`, and `windows/arm64`, plus `SHA256SUMS`, `SHA256SUMS.sigstore.json`, and Sigstore bundle sidecars (`*.sigstore.json`) for each artifact.
- Direct installers must verify `SHA256SUMS` entries and Sigstore bundle sidecars, require `cosign`, and only support bundle-enabled releases.
- Direct installers must remain available at `scripts/install/binpm.sh` and `scripts/install/binpm.ps1`.
- Public direct-installer documentation must include copy-pasteable remote commands for POSIX shells and PowerShell that fetch the installer from first-party `delinoio/oss` raw GitHub URLs, while also preserving the canonical in-repo script paths for maintainer workflows.
- Remote direct-installer examples must use `https://raw.githubusercontent.com/delinoio/oss/refs/heads/main/scripts/install/binpm.sh` and `https://raw.githubusercontent.com/delinoio/oss/refs/heads/main/scripts/install/binpm.ps1` for current public docs, or a tag/commit-pinned equivalent of those same first-party paths when reproducibility is required.
- `cargo-binstall` metadata must resolve only first-party GitHub Release assets and disable third-party quick-install and compile fallback strategies.
- Homebrew installation must use prebuilt `binpm` release archives for `darwin/amd64`, `darwin/arm64`, `linux/amd64`, and `linux/arm64`.
- Installs without upstream checksum material or successfully verified signature material are allowed in v1 only with an explicit warning and locally recorded SHA-256 metadata.
- `--require-verified` and `binpm verify --require-verified` must fail unless provider digest, upstream checksum sidecar, upstream checksum manifest, or a successfully verified signature under a documented trust policy is available.
- `--no-confirm` must remain a stable scripting flag for bypassing confirmation prompts on future dangerous operations.
- `-v`/`--verbose` and `--debug` must remain discoverable global flags that map to structured binpm tracing. They override `BINPM_LOG`; `BINPM_LOG` remains supported when no CLI verbosity flag is present.
- Local and global `binpm update` and scoped `binpm remove` must print the selected local or global scope before mutation and support `--dry-run` previews that leave manifests, lockfiles, package records, cache references, and executables unchanged. `binpm update --global [cmd...]` updates selected or all global tools from existing global package records, preserves the recorded command alias and selected binary, resolves the latest stable release for each recorded source, and finalizes through the same cache, install, rollback, and verification behavior as `binpm install <source> --as <cmd> --bin <selected_binary>`. `binpm outdated` must report the source needed for planning and auditing global updates. `--global` remains an explicit scope override even inside a project and does not currently require an interactive confirmation prompt.
- `binpm doctor`, `binpm explain`, `binpm verify`, `binpm info`, `binpm outdated`, and `binpm cache key` must not mutate manifests, lockfiles, package records, cache entries, or executables.
- Read-only diagnostic commands `binpm list`, `binpm info`, `binpm outdated`, `binpm doctor`, `binpm explain`, `binpm verify`, and `binpm cache list` expose a stable `--json` output mode for CI and scripts. JSON diagnostic output must reuse documented enum-style scope, target, checksum source, and verification-state values and must not include ANSI color.
- `binpm remove` must clean project-local package records when they exist so removed tools are not reported as installed.
- `binpm env --shell` supports `bash`, `zsh`, `fish`, and `powershell`; `PowerShell` is accepted case-insensitively, and `cmd` is accepted only to return an explicit deferred-shell diagnostic.
- Global install output and `binpm doctor` must guide users to add `~/.binpm/bin` to `PATH` when it is absent, while keeping shell profile modification opt-in only. Local add output must point to `binpm x <cmd>` and optional `binpm env` usage. The guidance must not imply that project-local `.binpm/bin` entries should be persisted in shell profiles.

## Change Policy
- Update this index and `docs/crates-binpm-foundation.md` together when CLI shape, local manifest or lockfile format, target selection, storage layout, cache behavior, security behavior, release distribution, installer behavior, or heuristic scoring changes.
- Update this index and `docs/apps-binpm-docs-foundation.md` in the same change for `apps/binpm-docs` path, route, theme repository-link surface, toolchain, validation, production URL, or deployment contract updates.
- Update root `AGENTS.md`, `apps/AGENTS.md`, and `crates/AGENTS.md` when `binpm` ownership, planned path status, or repository policy boundaries change.
- Keep `crates/binpm` as an explicit Rust workspace member while runtime implementation continues.
- Keep `.github/workflows/release-binpm.yml`, `scripts/install/binpm.sh`, `scripts/install/binpm.ps1`, `crates/binpm/Cargo.toml`, `scripts/release/update-homebrew.sh`, and `packaging/homebrew/templates/binpm.rb.tmpl` synchronized with binpm release asset names and signing contracts.

## References
- `docs/project-template.md`
- `docs/domain-template.md`
- `docs/README.md`
- `docs/crates-binpm-foundation.md`
- `docs/apps-binpm-docs-foundation.md`
