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
- Maintainers documenting GitHub Release asset naming and distribution compatibility.
- Operators troubleshooting binary resolution, download, extraction, and local installation behavior.

## Interfaces and Contracts
- Canonical install command: `binpm install github:owner/repo[@version]`.
- `github:owner/repo` is the canonical v1 package spec. Direct URLs, registries, and non-GitHub hosts are out of scope until documented separately.
- When `@version` is omitted, `binpm` must select the latest stable GitHub Release by ignoring draft and prerelease releases.
- Explicit versions may be written with or without a leading `v`; release tag matching must try the exact input first, then the opposite `v` prefix form.
- The host target model must be enum-driven and include:
  - OS: `linux`, `darwin`, `windows`, `freebsd`
  - CPU architecture: `x86_64`, `aarch64`, `i686`, `armv7`
  - libc or ABI environment: `gnu`, `musl`, `msvc`, `any`
- Target alias normalization must include:
  - OS aliases: `darwin`, `macos`, `mac`, `osx` -> `darwin`; `windows`, `win`, `win32` -> `windows`
  - Architecture aliases: `x86_64`, `amd64`, `x64` -> `x86_64`; `aarch64`, `arm64` -> `aarch64`; `i686`, `i386`, `x86`, `ia32` -> `i686`
  - Libc/ABI aliases: `gnu`, `glibc` -> `gnu`; `musl`, `alpine` -> `musl`; `msvc` -> `msvc`; missing libc -> `any`
- Asset selection must be score-based, deterministic, and stable across identical release asset lists:
  - Exact OS + arch + libc match wins over all partial matches.
  - Exact OS + arch with missing libc is accepted only when no exact libc candidate exists.
  - Exact OS + arch + `any` beats an asset with conflicting libc.
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
- Home directory: `~/.binpm`
- Executable link or copy directory: `~/.binpm/bin`
- Installed package records: `~/.binpm/packages`
- Download cache: `~/.binpm/cache`
- Temporary downloads and extraction roots: `~/.binpm/tmp`
- Local install manifests must record package spec, resolved owner/repo, release tag, asset name, asset URL, target OS, target architecture, target libc/ABI, archive format, selected binary path inside the archive, installed binary path, SHA-256 digest, install timestamp, and whether upstream checksum or signature material was available.
- Temporary extraction must be atomic: incomplete downloads and extraction directories stay under `~/.binpm/tmp` and must not update package records or `~/.binpm/bin`.

## Security
- `binpm` must use HTTPS GitHub API and release asset URLs.
- GitHub tokens may be read from documented environment variables in the future, but tokens and authorization headers must never be logged.
- If an upstream checksum manifest or sidecar exists, `binpm` must verify the selected asset before installation.
- If no checksum or signature exists, `binpm` must warn, compute SHA-256 locally, store it in the install manifest, and verify future reinstalls or cache reuse against that recorded digest.
- Checksum, signature, SBOM, and provenance files are metadata inputs only; they must not be installed as binaries.
- URL diagnostics in errors and logs must omit query strings and fragments.
- Archive extraction must reject absolute paths, parent-directory traversal, unsafe symlinks, and files that would escape the package extraction root.

## Logging
- Use structured `tracing` logs for release lookup, target normalization, asset candidate scoring, checksum discovery, download, extraction, binary discovery, and install finalization.
- Candidate scoring logs must include normalized package spec, release tag, asset name, detected OS, detected architecture, detected libc/ABI, artifact kind, score, and rejection reason when applicable.
- Download logs must include sanitized URL origin, asset name, byte count when known, cache hit or miss state, retry attempt, and final outcome.
- Install logs must include package spec, release tag, selected asset, selected archive member or bare executable, installed path, and manifest path.
- Human CLI output may be concise, but debug logs must be sufficient to reconstruct why a candidate won or lost.

## Build and Test
- No Rust validation command is required while `binpm` remains documentation-only.
- When runtime code is introduced, local validation must include `cargo test -p binpm` and the repository Rust baseline `cargo test --workspace --all-targets`.
- Heuristic tests must cover OS aliases, architecture aliases, libc aliases, exact libc preference, missing-libc fallback, source archive rejection, sidecar rejection, desktop installer de-prioritization, cargo-binstall candidates, cargo-dist candidates, GoReleaser candidates, Bun/Deno candidates, and ambiguous archive contents.
- Storage tests must cover atomic install behavior, cache digest verification, manifest updates, and unsafe archive path rejection.

## Dependencies and Integrations
- Integrates with GitHub Releases through the GitHub API and release asset downloads.
- Depends on archive extraction support for common release formats.
- May integrate with checksum and signature tooling later, but v1 must work without Node.js or language-specific package managers.
- Does not integrate with npm, pnpm, yarn, Bun, Cargo install, cargo-binstall, Homebrew, apt, rpm, or system package managers as install backends in v1.

## Change Triggers
- Update `docs/project-binpm.md` with this file when CLI shape, storage layout, security policy, target model, or asset selection heuristics change.
- Update root `AGENTS.md` and `crates/AGENTS.md` when `binpm` project ownership, planned path status, or Rust-domain policy changes.
- Update implementation tests in the same change set when heuristic scoring rules are implemented or changed.

## References
- `docs/project-binpm.md`
- `docs/domain-template.md`
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
