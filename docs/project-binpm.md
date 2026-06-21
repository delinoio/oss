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
- binpm documentation routes exposed by `apps/binpm-docs` are `/`, `/installation`, `/getting-started`, `/commands`, `/local-tooling`, `/cache-and-verification`, `/troubleshooting`, and `/reference`.
- binpm documentation content must remain documentation-only and must not expand runtime behavior, release automation, package-manager backend scope, checksum discovery, signature verification, or global update behavior without corresponding runtime contract updates.
- binpm documentation must not infer current product behavior or page content from the live `https://binpm.delino.io` site; repository contracts remain the source of truth.
- The runtime implementation includes clap-based command parsing, enum-backed contract foundations, structured `tracing` setup, centralized CLI error handling, README/test scaffolding, release source parsing, provider release lookup clients, deterministic release asset candidate scoring, asset downloads, archive extraction for documented formats, TOML-backed `binpm.toml` and `binpm.lock` parsing/writing, global and project-local package records, global cache metadata, URL sanitization, SHA-256 cache validation, and atomic file writes.
- `binpm init`, `binpm env`, `binpm doctor`, source-form and local-command `binpm explain`, `binpm cache key`, `binpm cache list`, `binpm cache prune`, `binpm cache clean`, `binpm list`, `binpm remove`, `binpm info`, `binpm outdated`, `binpm verify`, bare-executable and archive install flows, and `binpm x` execution have concrete runtime behavior. Checksum sidecar/manifest discovery, signature verification, and global update remain implementation work.
- Stable source identifiers are `github:owner/repo[@version]`, `github:<host>/owner/repo[@version]`, and `gitlab:<host>/<namespace...>/<project>[@version]`.
- Versionless installs must resolve to the latest stable release exposed by the source provider; GitHub sources must exclude draft and prerelease releases, and GitLab sources must exclude upcoming releases, releases with future `released_at` values, and prerelease tag patterns.
- Binary selection must be deterministic and target-aware across operating system, CPU architecture, and libc or ABI environment.
- Source-form `binpm explain <source>` may perform read-only provider release lookup and must print source parsing, provider API URL, release selection, target normalization, asset candidate scoring, selected asset, rejection reasons, actionable remediation summaries, and target override snippets without mutating manifests, lockfiles, package records, cache entries, or executables.
- Current-host target detection must fail clearly for unsupported operating systems or CPU architectures instead of mapping them to a supported fallback target.
- The asset selection heuristic must remain fully documented in `docs/crates-binpm-foundation.md` before implementation changes alter scoring behavior.
- `~/.binpm` remains the canonical global home directory for globally installed binaries, package records, cache entries, and temporary extraction state.
- `~/.binpm/cache` is the user-level global asset cache shared by all `binpm` installs for the same account.
- Global cache reuse must never bypass provider asset digest, upstream checksum, signature, or locally recorded SHA-256 verification.
- Project-local installs must maintain user-level cache references so pruning from one checkout does not remove cache entries still referenced by another checkout.
- Cache management commands must preserve installed package records and `~/.binpm/bin` entries unless a separate uninstall contract explicitly changes that behavior.
- `binpm cache key` must be a read-only diagnostic command that prints a current-target CI cache key derived from `binpm.lock`.
- Project-local tooling must use `binpm.toml` at the repository root as the committed local tool manifest.
- Project-local tooling must use `binpm.lock` at the repository root as the committed deterministic resolution record for release tags, target-specific assets, selected binaries, checksums, and installed paths.
- Committed lockfiles must store sanitized canonical asset URLs only, never credential-bearing or expiring download URLs.
- Local target-specific asset overrides must live under `[tools.<cmd>.targets.<target-key>]`, must use canonical target keys, and must preserve deterministic lockfile output. Diagnostic snippets must never include credential-bearing URLs, runtime cache paths, or other transient machine-local fields.
- Local command names must be executable basenames and must not contain path separators or relative path components.
- `binpm add <cmd> <source> --bin <upstream-binary>` must persist the selected upstream binary in `binpm.toml`; `binpm x --package <source> --bin <upstream-binary> CMD [args...]` must use that selected upstream binary for one-off execution while installing it under `CMD`.
- Project-local executable files must be installed under `$repoRoot/.binpm/bin`; other project-local binpm runtime state must stay under `$repoRoot/.binpm`.
- The current install implementation finalizes bare executable assets and archives in `.tar.gz`, `.tgz`, `.tar.xz`, `.txz`, `.tar.zst`, and `.zip` formats. Archive install finalization validates member paths, rejects symlinks and hard links, selects an executable member, and installs only the selected member into the managed bin directory.
- Archive binary ambiguity errors must list the plausible executable candidates and include concrete retry commands using `--bin`.
- `binpm x CMD [args...]` must run commands from the local manifest or from an explicitly supplied `--package`; it must not guess a GitHub repository from `CMD`.
- `binpm x` is the canonical execution command. `binpm exec` and `binpm run` are documented aliases for discoverability and must share the same argument forwarding, local lockfile, install-on-demand, `--package`, PATH, and exit-code behavior.
- Local `install`, `update`, and `x` must honor `--frozen-lockfile`; `CI=true` enables frozen lockfile behavior by default, and `--no-frozen-lockfile` is the explicit escape hatch.
- `binpm` must not require Node.js, npm, pnpm, yarn, or Bun to install native binary tools.
- Installs without upstream checksum material or successfully verified signature material are allowed in v1 only with an explicit warning and locally recorded SHA-256 metadata.
- `--require-verified` and `binpm verify --require-verified` must fail unless provider digest, upstream checksum sidecar, upstream checksum manifest, or a successfully verified signature under a documented trust policy is available.
- `--no-confirm` must remain a stable scripting flag for bypassing confirmation prompts on future dangerous operations.
- `binpm update` and `binpm remove` must print the selected local or global scope before mutation and support `--dry-run` previews that leave manifests, lockfiles, package records, cache references, and executables unchanged. `--global` remains an explicit scope override even inside a project and does not currently require an interactive confirmation prompt.
- `binpm doctor`, `binpm explain`, `binpm verify`, `binpm info`, `binpm outdated`, and `binpm cache key` must not mutate manifests, lockfiles, package records, cache entries, or executables.
- `binpm remove` must clean project-local package records when they exist so removed tools are not reported as installed.
- `binpm env --shell` supports `bash`, `zsh`, `fish`, and `powershell`; `PowerShell` is accepted case-insensitively, and `cmd` is accepted only to return an explicit deferred-shell diagnostic.
- Global install output and `binpm doctor` must guide users to add `~/.binpm/bin` to `PATH` when it is absent, while keeping shell profile modification opt-in only. The guidance must not imply that project-local `.binpm/bin` entries should be persisted in shell profiles.

## Change Policy
- Update this index and `docs/crates-binpm-foundation.md` together when CLI shape, local manifest or lockfile format, target selection, storage layout, cache behavior, security behavior, or heuristic scoring changes.
- Update this index and `docs/apps-binpm-docs-foundation.md` in the same change for `apps/binpm-docs` path, route, toolchain, validation, production URL, or deployment contract updates.
- Update root `AGENTS.md`, `apps/AGENTS.md`, and `crates/AGENTS.md` when `binpm` ownership, planned path status, or repository policy boundaries change.
- Keep `crates/binpm` as an explicit Rust workspace member while runtime implementation continues.

## References
- `docs/project-template.md`
- `docs/domain-template.md`
- `docs/README.md`
- `docs/crates-binpm-foundation.md`
- `docs/apps-binpm-docs-foundation.md`
