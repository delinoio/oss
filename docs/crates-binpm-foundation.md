# crates-binpm-foundation

## Scope
- Project/component: `binpm` crate foundation contract
- Canonical path: planned `crates/binpm`
- Implementation status: documentation-only onboarding; do not create runtime code until a later implementation change

## Runtime and Language
- Runtime: Rust CLI
- Primary language: Rust

## Users and Operators
- Developers installing native command-line tools without requiring Node.js or language-specific package managers.
- Maintainers documenting release asset naming and distribution compatibility across GitHub, GitHub Enterprise, and GitLab sources.
- Operators troubleshooting binary resolution, download, extraction, and local installation behavior.

## Interfaces and Contracts
- Canonical global install command: `binpm install <source>`.
- Canonical local declaration command: `binpm add <cmd> <source>`.
- Canonical local sync command: `binpm install`.
- Canonical local execution command: `binpm x CMD [args...]`.
- Canonical one-off execution command: `binpm x --package <source> CMD [args...]`.
- Stable source enum values:
  - `github:owner/repo[@version]` addresses GitHub.com Releases and may omit the host only for `github.com`.
  - `github:<host>/owner/repo[@version]` addresses GitHub Enterprise Releases on an explicit host.
  - `gitlab:<host>/<namespace...>/<project>[@version]` addresses GitLab Releases on GitLab.com, GitLab Self-Managed, or GitLab Dedicated hosts.
- Direct URLs, registries, and package-manager backends are out of scope until documented separately.
- When `@version` is omitted, `binpm` must select the latest stable release exposed by the source provider; GitHub sources must ignore draft and prerelease releases.
- Explicit versions may be written with or without a leading `v`; release tag matching must try the exact input first, then the opposite `v` prefix form.
- Commands with both local and global scope must default to local when a local `binpm.toml` is discovered; otherwise they must default to global. Such commands must document `--local` and `--global` overrides.
- `--frozen-lockfile` on local `binpm install`, `binpm update`, and `binpm x` must fail when the command would need to create or modify `binpm.lock`; `CI=true` must enable this behavior by default, and `--no-frozen-lockfile` is the explicit local-development escape hatch.
- `--no-confirm` is a stable scripting flag. The default behavior remains no prompt for currently documented operations, but future dangerous operations that add confirmation prompts must allow `--no-confirm` to bypass them.
- Cache management commands for v1:
  - `binpm cache list` must report cached assets with digest, byte size when known, source provider, source host, source path, release tag, asset name, last-used timestamp when known, and whether installed package manifests currently reference the entry.
  - `binpm cache prune` must remove only cache entries that are not referenced by installed package manifests.
  - `binpm cache clean` must remove all cache entries while preserving installed package records and executable links or copies under `~/.binpm/bin`.
  - `binpm cache key` must print a stable CI cache key derived from the current target and `binpm.lock`; it must not download, install, modify package records, or populate cache entries.
- `binpm list [--local|--global]` must report declared and installed tools for the selected scope, including source, requested version, resolved release tag when known, selected binary, installed path, and verification state when known.
- `binpm remove <cmd> [--local|--global]` must remove the selected tool from the selected scope; local removal updates `binpm.toml`, `binpm.lock`, and `$repoRoot/.binpm/bin`, while global removal updates `~/.binpm/packages` and `~/.binpm/bin`.
- `binpm info <cmd-or-source> [--local|--global]` must print source metadata, resolved release metadata when available, selected target asset, selected binary, and checksum source without installing new bytes.
- `binpm outdated [--local|--global]` must compare declared or installed tools with the latest stable release available from their source and must not update manifests, lockfiles, package records, cache entries, or executables.
- `binpm update [cmd...] [--local|--global]` must update selected tools or all tools in scope to the latest stable release allowed by their source declarations; local updates must update `binpm.lock` and installed project-local executables.
- `binpm doctor` must inspect manifest discovery, lockfile readability, package records, cache state, installed executable records, PATH visibility, and provider configuration without mutating state.
- `binpm explain <cmd-or-source> [--local|--global]` must explain source parsing, release selection, target normalization, asset candidate scoring, binary discovery, and verification decisions without mutating state.
- `binpm verify [--local|--global] [--require-verified]` must validate lockfile records, package records, cache bytes, and installed executable records without mutating state.
- `binpm init` must create a minimal `binpm.toml` with `version = 1` at the project root when one does not already exist; it must not install tools by default.
- `binpm env --shell <shell>` must print shell-specific environment commands for adding binpm-managed binary directories to `PATH`; it must not modify shell profiles by default.
- `binpm add <cmd> <source>` must declare `<cmd>` in `binpm.toml`, install the selected executable into `$repoRoot/.binpm/bin`, and update `binpm.lock`.
- `binpm install` without a package spec must sync the local `binpm.toml` manifest into `$repoRoot/.binpm/bin` and update `binpm.lock`; `binpm install <source>` keeps the global install behavior.
- `binpm x CMD [args...]` must resolve `CMD` from `binpm.toml`, install it on demand when the lockfile or local executable is missing or stale, prepend `$repoRoot/.binpm/bin` to `PATH`, preserve the caller's current working directory, and forward every argument after `CMD` to the executed command.
- `binpm x --package <source> CMD [args...]` must install or reuse the explicit package in a temporary or cache-backed execution context, prepend that context and `$repoRoot/.binpm/bin` to `PATH` when a local project exists, and run `CMD [args...]`.
- If `CMD` is absent from the local manifest and no explicit `--package` is provided, `binpm x` must fail with a clear hint to run `binpm add <cmd> <source>` or retry with `--package`; it must not infer a source repository from the command name.
- The host target model must be enum-driven and include:
  - OS: `linux`, `darwin`, `windows`, `freebsd`
  - CPU architecture: `x86_64`, `aarch64`, `i686`, `armv7`
  - libc or ABI environment: `gnu`, `musl`, `msvc`, `any`
- Target alias normalization must include:
  - OS aliases: `darwin`, `macos`, `mac`, `osx` -> `darwin`; `windows`, `win`, `win32` -> `windows`
  - Architecture aliases: `x86_64`, `amd64`, `x64` -> `x86_64`; `aarch64`, `arm64` -> `aarch64`; `i686`, `i386`, `x86`, `ia32` -> `i686`
  - Libc/ABI aliases: `gnu`, `glibc` -> `gnu`; `musl`, `alpine` -> `musl`; `msvc` -> `msvc`; explicit `static`, `portable`, `universal`, or `any` -> `any`; missing Linux libc remains `unknown` during candidate scoring.
- Asset selection must be score-based, deterministic, and stable across identical release asset lists:
  - Exact OS + arch + libc match wins over all partial matches.
  - Exact OS + arch + `any` beats missing-libc candidates and assets with conflicting libc.
  - On Linux `gnu` hosts, exact OS + arch with missing libc may be accepted as a glibc-compatible fallback only when no exact `gnu` or `any` candidate exists.
  - On Linux `musl` hosts, missing-libc candidates must not be accepted unless the asset has an explicit `static`, `portable`, `universal`, or `any` signal; otherwise resolution must fail instead of installing a likely glibc-linked binary.
  - Universal macOS assets may match `darwin/x86_64` and `darwin/aarch64` only when no exact-arch macOS asset exists.
  - If scores tie, prefer the candidate with a recognized tool-specific naming pattern, then shorter normalized filename, then lexicographic filename order.
- Preferred installable artifact kinds:
  - Archives: `.tar.gz`, `.tgz`, `.tar.xz`, `.txz`, `.tar.zst`, `.zip`
  - Bare executables: extensionless POSIX binaries and `.exe` for Windows
- Non-installable or sidecar artifact kinds must be excluded from binary selection:
  - Source archives such as `source.tar.gz`, `source.zip`, GitHub-generated source links, and files containing `src` or `source` without a target token.
  - Checksum, signature, provenance, and metadata files such as `.sha256`, `.sha512`, `SHA256SUMS`, `checksums.txt`, `.sig`, `.asc`, `.minisig`, `.sigstore.json`, `.sbom.json`, `dist-manifest.json`, and `latest.json`.
  - Package formulas and package-manager metadata such as `.rb`, `.json` manifests, npm package tarballs, and Homebrew formula assets.
- Desktop or system package formats are de-prioritized and must not be installed by default in v1: `.deb`, `.rpm`, `.apk`, `.pkg.tar.zst`, `.dmg`, `.msi`, `.pkg`, `.AppImage`, `.flatpak`, `.snap`.
- Archive extraction must locate one or more executable files by executable permission, Windows `.exe` suffix, expected package name, and target-aware filename tokens.
- If an archive contains multiple plausible executables, `binpm` must prefer a binary whose basename matches the repository name; otherwise it must fail with an ambiguity error that lists candidates.

## Local Manifest and Lockfile
- The local project root is the nearest ancestor containing `binpm.toml`; commands that create `binpm.toml` must use the current Git worktree root when available, otherwise the current directory.
- `binpm.toml` is the committed local tool declaration file. It must use TOML, `version = 1`, and `[tools.<cmd>]` tables keyed by the local command name.
- In `binpm.toml`, each tool entry must include `source = "<source-without-version>"`, may include `version = "<release>"`, and may include `bin = "<upstream-binary-name>"` when the executable selected from the release differs from the local command name or needs explicit disambiguation.
- `binpm add <cmd> <source>` must persist the package source without the version suffix in `source`; when a version is supplied, it must persist that value in `version`.
- Target-specific manifest overrides must use `[tools.<cmd>.targets.<target-key>]` tables. Each override must include `asset = "<release-asset-name>"`, `bin = "<asset-member-or-bare-binary>"`, and may include `checksum_source = "<checksum-source>"` when the automatic checksum source must be overridden.
- Multi-binary releases must keep the existing `[tools.<cmd>]` model: each local command has its own declaration, while multiple commands may share the same source, release asset, cache entry, and package bytes.
- `binpm.lock` is the committed deterministic local resolution file. It must use TOML, `version = 1`, `[tools.<cmd>]` command tables keyed by local command name, and `[tools.<cmd>.targets.<target-key>]` records keyed by normalized target.
- The lockfile target key format must be `<target_os>-<target_arch>-<target_libc>`, using the enum-style values from the runtime target model.
- Each `binpm.lock` target record must include package spec, normalized source, source provider, source host, source path, requested version when present, release tag, asset name, asset URL, target OS, target architecture, target libc/ABI, archive format, selected binary path inside the archive or bare asset, installed binary path, SHA-256 digest, checksum source (`github-digest`, `sidecar`, `manifest`, `signature`, `local`), and whether upstream signature material was available.
- Lockfile records for multiple commands that share an upstream asset must preserve command-specific selected binary and installed path fields while allowing cache keys to deduplicate by verified asset bytes.
- `binpm.lock` must not include install timestamps, last-used timestamps, absolute cache paths, or other machine-local operational metadata; those values belong in uncommitted package records or logs.
- Lockfile target and checksum fields must use the same enum-style values as the runtime target model and checksum source model; implementation types should preserve those values as enums rather than free-form strings.

Example `binpm.toml`:

```toml
version = 1

[tools.rg]
source = "github:BurntSushi/ripgrep"
version = "14.1.1"
bin = "rg"

[tools.rg.targets.darwin-aarch64-any]
asset = "ripgrep-14.1.1-aarch64-apple-darwin.tar.gz"
bin = "ripgrep-14.1.1-aarch64-apple-darwin/rg"
checksum_source = "github-digest"
```

Example `binpm.lock`:

```toml
version = 1

[tools.rg]
source = "github:BurntSushi/ripgrep"

[tools.rg.targets.darwin-aarch64-any]
package_spec = "github:BurntSushi/ripgrep@14.1.1"
source = "github:BurntSushi/ripgrep"
source_provider = "github"
source_host = "github.com"
source_path = "BurntSushi/ripgrep"
requested_version = "14.1.1"
release_tag = "14.1.1"
asset_name = "ripgrep-14.1.1-aarch64-apple-darwin.tar.gz"
asset_url = "https://github.com/BurntSushi/ripgrep/releases/download/14.1.1/ripgrep-14.1.1-aarch64-apple-darwin.tar.gz"
target_os = "darwin"
target_arch = "aarch64"
target_libc = "any"
archive_format = "tar.gz"
selected_binary = "ripgrep-14.1.1-aarch64-apple-darwin/rg"
installed_path = ".binpm/bin/rg"
sha256 = "<hex-encoded-sha256>"
checksum_source = "github-digest"
signature_available = false
```

### Binary Release Pattern Catalog
- Rust cargo-binstall defaults:
  - Filename candidates include `{name}-{target}-{version}{archive-suffix}`, `{name}-{target}-v{version}{archive-suffix}`, `{name}-{version}-{target}{archive-suffix}`, underscore variants, and versionless `{name}-{target}{archive-suffix}` forms.
  - Archive directory candidates include `{name}-{target}-v{version}`, `{name}-{target}-{version}`, `{name}-{version}-{target}`, `{name}-v{version}-{target}`, `{name}-{target}`, `{name}-{version}`, `{name}-v{version}`, and `{name}`.
  - Rust target triples such as `x86_64-unknown-linux-gnu`, `aarch64-unknown-linux-musl`, `x86_64-apple-darwin`, and `x86_64-pc-windows-msvc` are strong target signals.
- Rust cargo-dist defaults:
  - Common asset pattern: `<name>-<rust-target>.tar.xz` for Unix-like targets and `<name>-<rust-target>.zip` for Windows targets.
  - Common sidecars: `<archive>.sha256`, `sha256.sum`, `dist-manifest.json`, installer scripts, npm package tarballs, and Homebrew formula files; these are metadata or alternate installers, not selected binaries.
- Go GoReleaser defaults:
  - Archive pattern: `<project>_<version>_<os>_<arch>[v<arm>][_<mips>][<amd64-level>].tar.gz` by default.
  - Binary-upload pattern: `<binary>_<version>_<os>_<arch>[v<arm>][_<mips>][<amd64-level>]`.
  - OS/arch tokens use Go conventions such as `Linux`, `Darwin`, `Windows`, `amd64`, `arm64`, `386`, and `armv7`; Windows commonly uses `.zip`.
- JavaScript and TypeScript:
  - Bun release assets use patterns such as `bun-linux-x64.zip`, `bun-linux-x64-musl.zip`, `bun-darwin-aarch64.zip`, and profile or baseline variants. `baseline` and `modern` are CPU feature signals, not architecture replacements.
  - Bun compiled executables are user-named via `--outfile`, while compile targets use `bun-<os>-<arch>` and optional libc or CPU feature suffixes.
  - Deno compile targets use Rust-like triples such as `x86_64-unknown-linux-gnu`, `aarch64-unknown-linux-gnu`, `x86_64-pc-windows-msvc`, `x86_64-apple-darwin`, and `aarch64-apple-darwin`.
  - Electron Builder default artifact naming is `${productName}-${version}.${ext}` for many targets, with target-specific defaults such as `${productName} Setup ${version}.${ext}` for NSIS; these are usually desktop installers and de-prioritized by default.
  - Tauri updater metadata uses `OS-ARCH` keys such as `linux-x86_64`, `darwin-aarch64`, and `windows-x86_64`; updater `.sig` files and `latest.json` are sidecars, not installable binaries.
- Python:
  - PyInstaller onefile output is a single executable named after the script or `--name`; onedir output is a directory named after the script containing an executable of the same name.
  - Nuitka standalone output commonly produces `<program>.dist/` and onefile mode produces `program.exe` on Windows or `program.bin` on non-Windows unless overridden.
- JVM and native Java:
  - JReleaser distribution artifacts are user-configured and may transform artifact paths; platform tokens may appear as full platform strings or replacement aliases.
  - `jpackage` produces host-native package formats such as `.exe`, `.msi`, `.dmg`, and Linux package formats; these are desktop or system packages and are de-prioritized by default.
  - GraalVM `native-image` defaults the executable name to the input JAR basename unless `-o` or an output argument overrides it; filenames need external target tokens to be safely auto-selected.
- Zig:
  - Official Zig downloads use patterns such as `zig-linux-x86_64-<version>.tar.xz`, `zig-macos-aarch64-<version>.tar.xz`, and `zig-windows-x86_64-<version>.zip`.
  - `.minisig` files are verification sidecars and must not be selected as installable binaries.

## Storage
- Global home directory: `~/.binpm`
- Global executable link or copy directory: `~/.binpm/bin`
- Global installed package records: `~/.binpm/packages`
- User-level global asset cache: `~/.binpm/cache`
- Global temporary downloads and extraction roots: `~/.binpm/tmp`
- Project-local manifest: `$repoRoot/binpm.toml`
- Project-local lockfile: `$repoRoot/binpm.lock`
- Project-local executable link or copy directory: `$repoRoot/.binpm/bin`
- Project-local temporary downloads and extraction roots: `$repoRoot/.binpm/tmp`
- Project-local package records may be stored under `$repoRoot/.binpm/packages` as runtime implementation detail, but committed resolution data must live in `binpm.lock`.
- The global cache stores release asset original bytes, not extracted package directories or installed binaries.
- Cache entries must be content-addressed by `sha256:<hex>` when provider metadata exposes a trusted SHA-256 digest.
- When provider metadata does not expose a trusted digest, `binpm` must compute SHA-256 after download and use the local digest as both the cache key and the install manifest verification value.
- Cache lookup for assets without provider-provided digests may use source metadata to find a prior local digest, but source provider, source host, source path, release tag, asset name, or URL alone must not make bytes reusable without SHA-256 revalidation.
- Cache metadata must preserve the source provider, source host, source path, release tag, asset name, asset URL, byte size when known, checksum source, creation timestamp, and last-used timestamp when known.
- Cache metadata may reference more than one installed command or package record for the same verified asset bytes.
- Global package records under `~/.binpm/packages` are required machine-local install records for `binpm install <source>`.
- Global package records must record package spec, normalized source, source provider, source host, source path, requested version when present, release tag, asset name, asset URL, target OS, target architecture, target libc/ABI, archive format, selected binary path inside the archive or bare asset, installed binary path, cache key, cache path, SHA-256 digest, checksum source (`github-digest`, `sidecar`, `manifest`, `signature`, `local`), install timestamp, and whether upstream signature material was available.
- Project-local package records under `$repoRoot/.binpm/packages`, when implemented, are uncommitted machine-local install records and must use the same metadata fields as global package records.
- Committed `binpm.lock` target records must preserve deterministic resolution metadata only: package spec, normalized source, source provider, source host, source path, requested version when present, release tag, asset name, asset URL, target OS, target architecture, target libc/ABI, archive format, selected binary path inside the archive or bare asset, installed binary path, SHA-256 digest, checksum source, and whether upstream signature material was available.
- The global cache is separate from installed package records and executable links or copies. Removing cache entries must not remove package manifests or files under `~/.binpm/bin`.
- Temporary extraction and cache population must be atomic: incomplete global downloads and extraction directories stay under `~/.binpm/tmp`, and incomplete project-local downloads and extraction directories stay under `$repoRoot/.binpm/tmp`.
- Failed installs must not update cache entries, package records, `binpm.lock`, `~/.binpm/bin`, or `$repoRoot/.binpm/bin`.
- Concurrent installs for the same asset must use temporary files plus atomic rename or a cache lock so partial downloads are never visible as reusable cache entries.

## Security
- `binpm` must use HTTPS source-provider APIs and release asset URLs.
- Source-provider tokens may be read from documented environment variables in the future, but tokens and authorization headers must never be logged.
- If provider release asset metadata exposes a trusted SHA-256 digest, `binpm` must verify the downloaded asset against that digest before considering checksum sidecars or local fallback hashes.
- If an upstream checksum manifest or sidecar exists, `binpm` must verify the selected asset before installation.
- If no provider asset digest, checksum sidecar, checksum manifest, or signature exists, `binpm` must warn, compute SHA-256 locally, store it in the install manifest, and verify future reinstalls or cache reuse against that recorded digest.
- `--require-verified` and `binpm verify --require-verified` must fail unless the selected asset has at least one trusted verification source: provider digest, upstream checksum sidecar, upstream checksum manifest, or signature material.
- Cache hits must be revalidated before extraction or install finalization using the strongest available integrity source: provider asset digest, upstream checksum material, signature verification, or the locally recorded install manifest digest.
- If cache revalidation fails, `binpm` must discard the corrupted cache entry and redownload the asset. If the redownloaded bytes fail the trusted integrity source, installation must fail.
- Checksum, signature, SBOM, and provenance files are metadata inputs only; they must not be installed as binaries.
- URL diagnostics in errors and logs must omit query strings and fragments.
- Archive extraction must reject absolute paths, parent-directory traversal, unsafe symlinks, and files that would escape the package extraction root.

## Logging
- Use structured `tracing` logs for manifest discovery, lockfile parsing, release lookup, target normalization, asset candidate scoring, checksum discovery, download, extraction, binary discovery, install finalization, and `binpm x` command execution.
- Candidate scoring logs must include normalized package spec, source provider, source host, release tag, asset name, detected OS, detected architecture, detected libc/ABI, artifact kind, score, and rejection reason when applicable.
- Download and cache logs must include sanitized URL origin, asset name, byte count when known, cache hit or miss state, cache key, cache path, cache action, cache validation source, cache reused state, cache eviction state, retry attempt, and final outcome.
- Install logs must include package spec, release tag, selected asset, selected archive member or bare executable, installed path, manifest path, lockfile path when local, and whether the install is global or project-local.
- Stable cache log keys include `cache_key`, `cache_path`, `cache_action`, `cache_validation_source`, `cache_reused`, `cache_evicted`, and `cache_bytes`.
- `binpm x` logs must include local project root when present, resolved command, explicit package spec when used, PATH entries added by binpm, install-on-demand state, process exit status, and whether command resolution came from `binpm.toml` or `--package`.
- Diagnostic command logs for `doctor`, `explain`, `verify`, and `cache key` must include enough structured context to distinguish read-only inspection from mutating install or update flows.
- Human CLI output may be concise, but debug logs must be sufficient to reconstruct why a candidate won or lost.

## Build and Test
- No Rust validation command is required while `binpm` remains documentation-only.
- When runtime code is introduced, local validation must include `cargo test -p binpm` and the repository Rust baseline `cargo test --workspace --all-targets`.
- Heuristic tests must cover OS aliases, architecture aliases, libc aliases, exact libc preference, Linux glibc missing-libc fallback, Linux musl missing-libc rejection, source archive rejection, sidecar rejection, desktop installer de-prioritization, cargo-binstall candidates, cargo-dist candidates, GoReleaser candidates, Bun/Deno candidates, and ambiguous archive contents.
- Storage tests must cover atomic install behavior, cache miss download and digest recording, cache hit reuse after verification, digest mismatch eviction and redownload, concurrent partial download isolation, `binpm.toml` updates, `binpm.lock` updates, global install records, project-local install records, stale lock reinstall behavior, cache command behavior for `list`, `prune`, `clean`, and `key`, multi-command cache sharing, and unsafe archive path rejection.
- Execution tests must cover `binpm x` local PATH behavior, argument forwarding, current-working-directory preservation, explicit `--package` execution, missing-manifest failure, missing-command failure, install-on-demand from a valid lockfile, frozen-lockfile failures, `--no-frozen-lockfile` override behavior, and read-only diagnostics for `doctor`, `explain`, and `verify`.

## Dependencies and Integrations
- Integrates with GitHub Releases through the GitHub API and release asset downloads.
- Integrates with GitHub Enterprise Releases through explicit-host GitHub source specs.
- Integrates with GitLab Releases and release asset links through explicit-host GitLab source specs.
- Depends on archive extraction support for common release formats.
- May integrate with checksum and signature tooling later, but v1 must work without Node.js or language-specific package managers.
- Uses npm `npx` and `npm exec` only as behavioral references for PATH-based command execution and argument forwarding.
- Does not integrate with npm, pnpm, yarn, Bun, Cargo install, cargo-binstall, Homebrew, apt, rpm, or system package managers as install backends in v1.

## Change Triggers
- Update `docs/project-binpm.md` with this file when CLI shape, local manifest or lockfile format, storage layout, cache behavior, security policy, target model, or asset selection heuristics change.
- Update root `AGENTS.md` and `crates/AGENTS.md` when `binpm` project ownership, planned path status, or Rust-domain policy changes.
- Update implementation tests in the same change set when heuristic scoring rules are implemented or changed.

## References
- `docs/project-binpm.md`
- `docs/domain-template.md`
- GitHub Release asset API: https://docs.github.com/en/rest/releases/assets
- GitHub Enterprise Server release asset API: https://docs.github.com/en/enterprise-server@3.21/rest/releases/assets
- GitLab project releases API: https://docs.gitlab.com/api/releases/
- GitLab release links API: https://docs.gitlab.com/api/releases/links/
- GitHub Release asset digest changelog: https://github.blog/changelog/2025-06-03-releases-now-expose-digests-for-release-assets/
- npm exec and npx behavior reference: https://docs.npmjs.com/cli/v11/commands/npm-exec/
- GoReleaser archives: https://goreleaser.com/customization/package/archives/
- GoReleaser Go builder: https://goreleaser.com/customization/builds/builders/go/
- cargo-binstall support metadata: https://github.com/cargo-bins/cargo-binstall/blob/main/SUPPORT.md
- cargo-dist release assets and manifests: https://axodotdev.github.io/cargo-dist/
- Electron Builder configuration: https://www.electron.build/docs/configuration
- Tauri updater static JSON: https://v2.tauri.app/plugin/updater/
- Bun single-file executable docs: https://bun.com/docs/bundler/executables
- Deno compile docs: https://docs.deno.com/runtime/reference/cli/compile/
- PyInstaller usage docs: https://pyinstaller.org/en/stable/usage.html
- Nuitka use cases: https://nuitka.net/user-documentation/use-cases.html
- JReleaser archive assembly: https://jreleaser.org/guide/latest/reference/assemble/archive.html
- Oracle `jpackage` docs: https://docs.oracle.com/en/java/javase/21/docs/specs/man/jpackage.html
- GraalVM native-image JAR guide: https://www.graalvm.org/latest/reference-manual/native-image/guides/build-native-executable-from-jar/
- Zig downloads: https://ziglang.org/download/
