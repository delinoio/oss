# crates-binpm-foundation

## Scope
- Project/component: `binpm` crate foundation contract
- Canonical path: `crates/binpm`
- Implementation status: runtime implementation has begun with a Rust CLI, clap command surface, typed contract foundations, source parsing, provider release lookup clients, source-form and local-command explain diagnostics, deterministic asset candidate scoring, actionable target rejection diagnostics, target override snippet generation, release asset download, archive extraction for documented formats, TOML-backed local manifest and lockfile records, global and project-local package records, global cache metadata, sanitized persisted URLs, SHA-256 cache verification, atomic file writes, structured tracing setup, centralized errors, README, and tests

## Runtime and Language
- Runtime: Rust CLI
- Primary language: Rust

## Users and Operators
- Developers installing native command-line tools without requiring Node.js or language-specific package managers.
- Maintainers documenting release asset naming and distribution compatibility across GitHub, GitHub Enterprise, and GitLab sources.
- Operators troubleshooting binary resolution, download, extraction, and local installation behavior.

## Interfaces and Contracts
- The Rust crate must expose the full documented command surface through clap.
- Command dispatch must return explicit not-yet-implemented errors for flows whose release lookup, asset selection, download, cache mutation, extraction, install, update, remove, verification, explanation, listing, info, outdated, or process execution behavior is not yet implemented.
- Implemented safe commands may perform read-only or bootstrap behavior when they do not violate storage or mutation contracts:
  - `binpm init` may create `binpm.toml` containing `version = 1`.
  - `binpm env --shell <shell>` may print PATH commands for project-local and global bin directories for supported shells.
  - `binpm doctor` may report manifest, lockfile, and global home state without mutation.
  - `binpm cache key` may print a deterministic key for the current target and project-root `binpm.lock`, using an empty lockfile digest when the file is absent.
- Current install finalization supports bare executable assets and documented archive assets end to end. Archive extraction is implemented for `.tar.gz`, `.tgz`, `.tar.xz`, `.txz`, `.tar.zst`, and `.zip`, and installs only the selected executable member. On POSIX hosts, archive installs must write the selected member with executable permissions even when the upstream archive, especially a `.zip`, omitted Unix executable metadata and binary discovery was unambiguous.
- Canonical global install command: `binpm install <source> [--as <cmd>] [--bin <upstream-binary>]`.
- Canonical local declaration command: `binpm add <cmd> <source> [--bin <upstream-binary>] [--also <cmd=upstream-binary>] [--manifest-only]`.
- Canonical local sync command: `binpm install`.
- Canonical local execution command: `binpm x CMD [args...]`.
- Canonical one-off execution command: `binpm x --package <source> [--bin <upstream-binary>] [CMD] [args...]`.
- Documented execution aliases: `binpm exec` and `binpm run`. These aliases are stable compatibility entry points for discoverability, but `binpm x` remains the canonical execution command used in contracts, diagnostics, examples, and logs.
- Stable source enum values:
  - `github:owner/repo[@version]` addresses GitHub.com Releases and may omit the host only for `github.com`.
  - `github:<host>/owner/repo[@version]` addresses GitHub Enterprise Releases on an explicit host.
  - `gitlab:<host>/<namespace...>/<project>[@version]` addresses GitLab Releases on GitLab.com, GitLab Self-Managed, or GitLab Dedicated hosts.
- Common GitHub.com input shorthands may normalize to the canonical `github:` enum without changing persisted records:
  - `owner/repo[@version]` normalizes to `github:owner/repo[@version]`.
  - HTTPS GitHub.com repository URLs such as `https://github.com/owner/repo` and release URLs such as `https://github.com/owner/repo/releases/download/<tag>/<asset>` normalize to `github:owner/repo` or `github:owner/repo@<tag>`.
  - Persisted `binpm.toml`, `binpm.lock`, package records, cache metadata, logs, and JSON diagnostics must still use canonical source strings, never the shorthand input.
- GitLab URL shorthands, arbitrary HTTP(S) URLs, registries, and package-manager backends are out of scope until documented separately. Recognizable unsupported URL input must fail with a precise canonical replacement hint instead of silently adding a new backend.
- When `@version` is omitted, `binpm` must select the latest stable release exposed by the source provider:
  - GitHub sources must ignore draft and prerelease releases.
  - GitLab sources must list releases in descending `released_at` order and choose the first release whose `released_at` is not in the future, whose API response does not set `upcoming_release = true`, and whose normalized tag does not contain a SemVer prerelease segment such as `-alpha`, `-beta`, `-pre`, `-preview`, `-rc`, or another hyphenated prerelease identifier.
- Version selectors are intentionally exact-tag-only in v1:
  - `github:owner/repo` selects the latest stable release.
  - `github:owner/repo@14.1.1` and `github:owner/repo@v14.1.1` request exact release tags; `@14.1.1` must not match a `v14.1.1` release tag, and `@v14.1.1` must not match a `14.1.1` release tag.
  - `@latest` is rejected with a hint to omit `@version` for latest-stable selection.
  - SemVer range-like selectors such as `@^1`, `@~1.2`, `@>=1.0.0`, `@1.x`, `@1.*`, and `@1 || @2` are rejected with a hint to use an exact release tag or omit `@version`.
  - Channel selectors such as `@stable`, `@beta`, `@alpha`, `@nightly`, `@canary`, `@dev`, `@edge`, and `@next` are rejected with a hint to use an exact release tag or omit `@version`.
  - Major-version pins such as `@1` are rejected with a hint to use an exact release tag; if the upstream release tag is literally `v1`, users may request `@v1`.
- Commands with both local and global scope must default to local when a local `binpm.toml` is discovered; otherwise they must default to global. Such commands must document `--local` and `--global` overrides.
- `--frozen-lockfile` on local `binpm install`, `binpm update`, and `binpm x` must fail when the command would need to create or modify `binpm.lock`; `CI=true` must enable this behavior by default, and `--no-frozen-lockfile` is the explicit local-development escape hatch. Empty-manifest local updates that do not require lockfile changes must succeed without creating `binpm.lock`.
- Frozen-lockfile failures must include stable human and JSON diagnostics. The diagnostic must include `mode`, `reason`, `file`, `record`, `on_demand_install_attempt`, `would_change`, `safest_next_command`, and `local_development_escape_hatch` fields, distinguishing CI-enabled frozen mode from explicit `--frozen-lockfile`, missing `binpm.lock`, missing target records, stale records, and `binpm x` on-demand executable or package-record sync attempts.
- Frozen local install and `x` must restore missing `.binpm/bin` executables and `.binpm/packages` package records from existing target lock records when the global cache asset is present and SHA-256-valid for the locked digest. If the cache asset is absent or corrupt, frozen restore may download only the lockfile's persisted sanitized asset URL and must validate the recorded SHA-256 before populating cache or installing; this restore path must not require provider release-list pagination.
- Scoped mutating commands must print the selected local or global scope before mutation. `binpm update` and scoped `binpm remove` must support `--dry-run` previews that validate the selected tools, print the selected scope and planned file/runtime changes, and then exit without mutating manifests, lockfiles, package records, cache references, or executables. `binpm update --global` remains pending and must fail before planning or mutation with a workaround that points users to `binpm outdated --global`, `binpm info --global <cmd>` for the stale command's recorded source and selected binary, and `binpm install <source> --as <cmd> --bin <selected_binary>` for each stale global tool.
- `--global` must remain an explicit scope override even when a local manifest is present. It does not currently add an interactive confirmation prompt; if a future global mutation adds confirmation, `--no-confirm` must bypass that prompt for scripts.
- `--no-confirm` is a stable scripting flag. The default behavior remains no prompt for currently documented operations, but future dangerous operations that add confirmation prompts must allow `--no-confirm` to bypass them.
- Cache management commands for v1:
  - `binpm cache list` must report cached assets with digest, byte size when known, source provider, source host, source path, release tag, asset name, last-used timestamp when known, and whether installed package manifests currently reference the entry.
  - `binpm cache prune` must remove stale structured local-project cache references and then remove only cache entries that are not referenced by installed package manifests or by active references in the user-level local-project cache reference index under `~/.binpm/cache/refs`. A structured reference is stale when its project-local package record is absent or no longer points at the referenced cache key. Legacy plain-text references remain preserving until a future install or removal rewrites them.
  - `binpm cache clean` must remove all global cache asset entries under `~/.binpm/cache/sha256` while preserving `~/.binpm/cache/refs`, installed package records, and executable links or copies under `~/.binpm/bin`. Human output must state the removed asset-entry count and the preserved boundaries.
  - `binpm cache key` must print a stable CI cache key derived from the current target and `binpm.lock`; it must not download, install, modify package records, or populate cache entries. When `binpm.lock` is absent, human output must warn that the empty lockfile digest is used and `--json` output must expose `lockfile = "missing"`.
- `binpm list [--local|--global]` must report declared and installed tools for the selected scope, including source, requested version, resolved release tag when known, selected binary, installed path, and verification state when known.
- `binpm remove <cmd> [--local|--global] [--dry-run]` must remove the selected tool from the selected scope; local removal updates `binpm.toml`, `binpm.lock`, `$repoRoot/.binpm/bin`, and project-local package records under `$repoRoot/.binpm/packages` when they exist, while global removal updates `~/.binpm/packages` and `~/.binpm/bin`. Before mutation it must print `remove scope: local` or `remove scope: global`; with `--dry-run` it must print the planned removal and leave state unchanged.
- `binpm info <cmd-or-source> [--local|--global]` must print source metadata, resolved release metadata when available, selected target asset, selected binary, and checksum source without installing new bytes.
- `binpm outdated [--local|--global]` must compare declared or installed tools with the latest stable release available from their source and must not update manifests, lockfiles, package records, cache entries, or executables. Stale human rows and JSON tool entries must include the source needed to reinstall a global tool with `binpm install <source>`.
- `binpm update [cmd...] [--local] [--dry-run]` must update selected local tools or all local tools to the latest stable release allowed by their source declarations; local updates must update `binpm.lock` and installed project-local executables. Before mutation it must print `update scope: local` and a planned update list; with `--dry-run` it must print the same plan and leave state unchanged. `binpm update --global` is part of the command surface but pending implementation; it must fail consistently, including with `--dry-run`, and direct users to run `binpm outdated --global`, inspect stale global tools with `binpm info --global <cmd>`, and reinstall them with `binpm install <source> --as <cmd> --bin <selected_binary>`.
- `binpm doctor` must inspect manifest discovery, lockfile readability, package records, cache state, cache-reference stale/legacy counts, installed executable records, global and project-local PATH visibility, and provider configuration without mutating state. When `~/.binpm/bin` is not visible on `PATH`, doctor must print guided setup messaging that points to `binpm env` and keeps profile modification opt-in.
- `binpm explain <cmd-or-source> [--local|--global]` must explain source parsing, release selection, target normalization, asset candidate scoring, binary discovery, verification decisions, and target override remediation without mutating state.
- Source-form `binpm explain <source>` may perform read-only GitHub or GitLab release API lookup and must print the normalized source, provider API URL, release decision, normalized target, selected asset when one is eligible, candidate scores, rejection reasons, and a valid `[tools.<cmd>.targets.<target-key>]` override snippet when inspected data can identify an asset candidate. Local command explanation must inspect existing package records and print source, release, target, selected asset, selected binary, archive format, checksum source, verification state, and a valid exact override snippet without installing new bytes.
- `binpm verify [--local|--global] [--require-verified]` must validate lockfile records, package records, cache bytes, and installed executable records without mutating state.
- Read-only diagnostic commands `binpm list`, `binpm info`, `binpm outdated`, `binpm doctor`, `binpm explain`, `binpm verify`, `binpm cache list`, and `binpm cache key` must support `--json` as a stable machine-readable output mode. JSON mode must emit one compact JSON object to stdout on success and must avoid ANSI color in command output. Human output remains concise line-oriented text by default.
- JSON diagnostic envelopes must include a `command` field and stable command-specific arrays or records. Scoped diagnostics must include `scope` using the enum values `local` or `global`. Target fields must reuse the target enum values through a structured `target` object with `os`, `arch`, and `libc` fields. Checksum and verification fields must reuse the documented checksum source values (`github-digest`, `sidecar`, `manifest`, `signature`, `local`) and verification states (`verified`, `unverified`).
- `binpm explain <source> --json` must expose selected asset details and all candidate asset decisions with parseable `score`, `eligible`, `recognized_pattern`, optional `cpu_feature`, and `rejection_reason` fields. When release-level remediation is available, it must expose a parseable `release_diagnostics` array with structured target fields, unsupported installer asset names, sidecar asset names when relevant, and remediation text. `binpm verify --json` must expose successful verification checks and must use a parseable stderr error envelope with `error.message` and `error.exit_code` when verification fails before a success envelope can be produced.
- JSON output is a command output API for read-only diagnostics only. Mutating command progress logs and structured `tracing` logs are not stable machine-readable output contracts.
- `binpm init` must create a minimal `binpm.toml` with `version = 1` at the project root when one does not already exist; it must not install tools by default. Before creating or refusing to overwrite the manifest, it must print the resolved full `binpm.toml` destination path. After a successful creation, it must print a clear created-manifest line with the same path.
- `binpm init` manifest creation root selection is fixed: current Git worktree root when available, otherwise the nearest ancestor containing `binpm.toml` when present, otherwise the current directory. No explicit current-directory or alternate-root flag is currently contracted.
- `binpm env --shell <shell>` must print shell-specific environment commands for adding binpm-managed binary directories to `PATH`; it must not modify shell profiles by default. The printed global bin command must be labeled as suitable for shell profiles, while the project-local command must be labeled for the current project or shell session only.
- Supported `binpm env --shell` values are `bash`, `zsh`, `fish`, and `powershell`. `PowerShell` is accepted case-insensitively for user ergonomics.
- `binpm env --shell cmd` is a recognized but explicitly deferred shell value; it must fail with an unsupported-shell diagnostic that lists supported shells and says cmd support is deferred.
- After global installs, when `~/.binpm/bin` is not visible on `PATH`, binpm must print guided PATH setup messaging that points to `binpm env --shell <bash|zsh|fish|powershell>`, says persistent profile changes are opt-in, and tells users to persist only the global bin line.
- After successful local `binpm add` installs, binpm must print `binpm x <cmd>` as the default project-local invocation path and point users who want direct shell access to `binpm env --shell <bash|zsh|fish|powershell>`. Existing commands must not mutate shell profile files or imply project-local `.binpm/bin` entries are suitable for profile persistence.
- On Windows, `binpm env --shell bash` and `binpm env --shell zsh` must render drive-letter and UNC paths in POSIX shell form so colon-separated `PATH` exports remain valid.
- `binpm install <source> [--as <cmd>] [--bin <upstream-binary>]` must install globally by default. Without `--as`, the installed command name defaults to the source repository basename. With `--as`, global package records, executable paths, and removal keys use the explicit command alias while preserving the normalized source identity, requested version, release, asset, and selected upstream binary in the package record. With `--bin`, global installs use the explicit upstream executable name or archive member path to resolve ambiguous multi-binary archives without manual package-record edits.
- `binpm add <cmd> <source> [--bin <upstream-binary>]` must declare `<cmd>` in `binpm.toml`, install the selected executable into `$repoRoot/.binpm/bin`, and update `binpm.lock`.
- When `binpm add` receives `--bin`, it must persist that value as `[tools.<cmd>].bin` in `binpm.toml` so future local installs and executions use the same upstream binary selection without manual TOML editing.
- When `binpm add` receives `--manifest-only` or its `--no-install` alias, it must only create or update `binpm.toml`. It must not resolve releases, download assets, populate cache entries, write `binpm.lock`, write package records, or install executables. The command output must state that the lockfile and executables were not updated and must suggest `binpm install` as the follow-up command. Frozen lockfile settings do not block declaration-only manifest writes because no lockfile resolution is attempted.
- When `binpm add` receives one or more `--also <cmd=upstream-binary>` values, it must create additional `[tools.<cmd>]` declarations using the same normalized source and version as the primary declaration while persisting each supplied upstream binary in that command's `bin` field. Normal add installs each selected command and writes separate lockfile records; `--manifest-only --also ...` writes the concise declarations without network, lockfile, cache, package-record, or executable mutation.
- Tool command names in `binpm.toml`, package records, and install commands must be executable basenames; path separators, `.` and `..` are invalid command names.
- `binpm install` without a package spec must sync the local `binpm.toml` manifest into `$repoRoot/.binpm/bin` and update `binpm.lock`; `binpm install <source>` keeps the global install behavior.
- `binpm x CMD [args...]` must resolve `CMD` from `binpm.toml`, install it on demand when the lockfile or local executable is missing or stale, prepend `$repoRoot/.binpm/bin` to `PATH`, preserve the caller's current working directory, and forward every argument after `CMD` to the executed command.
- `binpm x --package <source> [--bin <upstream-binary>] CMD [args...]` must install or reuse the explicit package in a temporary or cache-backed execution context, prepend that context and `$repoRoot/.binpm/bin` to `PATH` when a local project exists, and run `CMD [args...]`.
- `binpm x --package <source> [--bin <upstream-binary>]` without `CMD` is the safe one-off shortcut. It must keep the source explicit and must not perform package search or infer a repository from a command name. Without `--bin`, the exposed command defaults to the source repository basename and normal archive binary discovery can still fail safely with candidate retry suggestions. With `--bin`, the exposed command defaults to the selected upstream binary basename. When forwarded args are needed, users must provide an explicit `CMD` before those args.
- When one-off execution receives `--bin`, `binpm` must use that upstream executable name or archive member path while exposing it as the requested `CMD` inside the temporary execution context, or as the `--bin` basename when `CMD` is omitted.
- `binpm exec` and `binpm run` must dispatch through the same execution implementation as `binpm x`, including argument forwarding, `--package` and `--bin` behavior, local manifest and lockfile behavior, install-on-demand behavior, PATH prepending, and process exit handling.
- If `CMD` is absent from the local manifest and no explicit `--package` is provided, `binpm x` and its execution aliases must fail with a clear hint to run `binpm add <cmd> <source>` or retry with `--package`; they must not infer a source repository from the command name.
- The host target model must be enum-driven and include:
  - OS: `linux`, `darwin`, `windows`, `freebsd`
  - CPU architecture: `x86_64`, `aarch64`, `i686`, `armv7`
  - libc or ABI environment: `gnu`, `musl`, `msvc`, `any`
- Current-host target detection must reject unsupported operating systems and CPU architectures with an unsupported-target error; it must not default unknown OS values to `linux`, unknown architecture values to `x86_64`, or generic 32-bit ARM hard-float targets to `armv7` unless the compile target triple is explicitly `armv7-*`. Unsupported generic `arm` current-host diagnostics must include the observed compile target triple when available and must name accepted canonical `armv7` target keys such as `linux-armv7-gnu`, `linux-armv7-musl`, and `linux-armv7-any`.
- Target alias normalization must include:
  - OS aliases: `darwin`, `macos`, `mac`, `osx` -> `darwin`; `windows`, `win`, `win32` -> `windows`
  - Architecture aliases: `x86_64`, `amd64`, `x64` -> `x86_64`; `aarch64`, `arm64` -> `aarch64`; `i686`, `i386`, `x86`, `ia32` -> `i686`; `armv7`, `armv7l`, `armhf` -> `armv7` for release asset names. Manifest override keys under `[tools.<cmd>.targets.<target-key>]` must use canonical target keys such as `linux-armv7-gnu`, `linux-armv7-musl`, and `linux-armv7-any`.
  - Libc/ABI aliases: `gnu`, `glibc` -> `gnu`; `musl`, `alpine` -> `musl`; `msvc` -> `msvc`; explicit `static`, `portable`, `universal`, or `any` -> `any`; missing Linux libc remains `unknown` during candidate scoring.
  - CPU feature variant tokens: `baseline` -> baseline-compatible default, `modern` -> higher-feature variant. These tokens must never satisfy an architecture match and must be reported separately from OS, architecture, and libc/ABI.
- Asset selection must be score-based, deterministic, and stable across identical release asset lists:
  - Exact OS + arch + libc match wins over all partial matches.
  - Exact OS + arch + `any` beats missing-libc candidates and assets with conflicting libc.
  - On Linux `gnu` hosts, exact OS + arch with missing libc may be accepted as a glibc-compatible fallback only when no exact `gnu` or `any` candidate exists.
  - On Linux `musl` hosts, missing-libc candidates must not be accepted unless the asset has an explicit `static`, `portable`, `universal`, or `any` signal; otherwise resolution must fail instead of installing a likely glibc-linked binary. Rejection diagnostics must say that a Linux musl target requires an explicit libc signal and must suggest upstream naming fixes or a target override only when the user has verified compatibility.
  - Universal macOS assets may match `darwin/x86_64` and `darwin/aarch64` only when no exact-arch macOS asset exists.
  - Baseline CPU feature assets receive a deterministic preference over otherwise equivalent unspecified assets. Modern CPU feature assets are rejected by automatic selection until binpm has an explicit host CPU capability model; diagnostics must say that `modern` requires explicit host capability selection and suggest a target override only after compatibility is verified.
  - If scores tie, prefer the candidate with a recognized tool-specific naming pattern, then shorter normalized filename, then lexicographic filename order.
- Target tokenization for asset scoring treats punctuation, hyphens, and underscores as separators after preserving `x86_64` as the `x64` alias, so Rust target triples, GoReleaser underscore names, Bun names, and Deno names all normalize through the same enum-backed OS, architecture, and libc/ABI aliases.
- Preferred installable artifact kinds:
  - Archives: `.tar.gz`, `.tgz`, `.tar.xz`, `.txz`, `.tar.zst`, `.zip`
  - Bare executables: extensionless POSIX binaries and `.exe` for Windows
- GitLab release asset links are eligible only when both the link `url` and the chosen provider URL use HTTPS. If `direct_asset_url` is present, it is the preferred canonical provider URL, but the original link `url` must still be HTTPS because GitLab direct asset URLs redirect to that target. Redirect resolution before download must reject any final non-HTTPS URL. Non-HTTPS GitLab asset links must be rejected before candidate scoring, and a release with no HTTPS-eligible installable links must fail deterministically. Diagnostics must distinguish non-HTTPS link URL rejection, non-HTTPS direct asset URL rejection, and non-HTTPS final redirect target rejection; redirect diagnostics may include only a sanitized origin and must not expose query strings, fragments, credentials, or tokens.
- Non-installable or sidecar artifact kinds must be excluded from binary selection:
  - Source archives such as `source.tar.gz`, `source.zip`, GitHub-generated source links, GitLab `assets.sources` entries, and files containing `src` or `source` without a target token.
  - Checksum, signature, provenance, and metadata files such as `.sha256`, `.sha512`, `SHA256SUMS`, `checksums.txt`, `.sig`, `.asc`, `.minisig`, `.sigstore.json`, `.sbom.json`, `dist-manifest.json`, and `latest.json`.
  - Package formulas and package-manager metadata such as `.rb`, `.json` manifests, npm package tarballs, and Homebrew formula assets.
- Desktop or system package formats are de-prioritized and must not be installed by default in v1: `.deb`, `.rpm`, `.apk`, `.pkg.tar.zst`, `.dmg`, `.msi`, `.pkg`, `.AppImage`, `.flatpak`, `.snap`.
- When all visible release assets are unsupported desktop or system packages, `binpm explain` must distinguish that installer-only boundary from a release with no assets. It must list the unsupported installer asset names and suggest upstream portable archive or bare executable assets instead of implying target scoring could not find any release asset.
- Archive extraction must locate one or more executable files by executable permission, Windows `.exe` suffix, expected package name, and target-aware filename tokens. Explicit manifest `bin` values may name an exact archive member path or a unique member basename.
- When an archive has no usable POSIX executable metadata for a member, `binpm` may recover the executable bit only after filename and target signals identify the selected binary unambiguously. The automatic recovery path is intentionally narrow: a non-executable member is recoverable when its basename matches the expected repository binary name and target-aware filtering leaves one candidate. Recovered POSIX installs must be chmodded executable during finalization.
- If archive member permissions are missing and filename/target signals do not identify one binary, `binpm` must fail with an actionable diagnostic instead of guessing. If an archive contains multiple plausible executables, `binpm` must prefer a binary whose basename matches the repository name; otherwise it must fail with an ambiguity error that lists candidates and includes concrete retry suggestions such as `binpm add <cmd> <source> --bin <candidate>` or `binpm x --package <source> --bin <candidate> <cmd>`.
- The current foundation implements binary discovery as a deterministic member-list heuristic and uses it during archive extraction and install finalization.

## Local Manifest and Lockfile
- The local project root is the nearest ancestor containing `binpm.toml`; commands that create `binpm.toml` must use the current Git worktree root when available, otherwise the nearest ancestor containing `binpm.toml` when present, otherwise the current directory.
- `binpm.toml` is the committed local tool declaration file. It must use TOML, `version = 1`, and `[tools.<cmd>]` tables keyed by the local command name.
- In `binpm.toml`, each tool entry must include `source = "<source-without-version>"`, may include `version = "<release>"`, and may include `bin = "<upstream-binary-name-or-archive-member>"` when the executable selected from the release differs from the local command name or needs explicit disambiguation.
- `binpm add <cmd> <source>` must persist the package source without the version suffix in `source`; when a version is supplied, it must persist that value in `version`; when `--bin` is supplied, it must persist that value in `bin`.
- `binpm add <cmd> <source> --manifest-only` must preserve the same manifest serialization rules as normal add while skipping all lockfile and runtime installation side effects. This supports declaration-only reviews, offline manifest edits, and later resolution through `binpm install`.
- `binpm add foo github:owner/tools --bin bin/foo --also bar=bin/bar --also baz=bin/baz` is the canonical concise CLI workflow for multi-binary releases. It expands into separate `[tools.foo]`, `[tools.bar]`, and `[tools.baz]` declarations with the same source and version so package-specific binary choices remain explicit and reviewable.
- Unsupported selector aliases, ranges, channels, and major-version pins must be rejected before manifest or lockfile writes so `binpm.toml` and `binpm.lock` remain deterministic exact-source records.
- Target-specific manifest overrides must use `[tools.<cmd>.targets.<target-key>]` tables. Each override must include `asset = "<release-asset-name>"`, `bin = "<asset-member-or-bare-binary>"`, and may include `checksum_source = "<checksum-source>"` when the automatic checksum source must be overridden.
- Override snippets emitted by diagnostics must use canonical target keys, TOML-escaped command keys and string values, exact release asset names from inspected release metadata, and exact selected binary paths when the local package record already knows them. Source-form explain must not include credential-bearing URLs, query strings, fragments, expiring download URLs, absolute cache paths, or other transient machine-local fields in snippets. When source-form explain has not downloaded an archive and therefore cannot inspect archive members, the snippet may use the repository command name as the `bin` value and the user must adjust it if the archive member differs.
- Multi-binary releases must keep the existing `[tools.<cmd>]` model: each local command has its own declaration, while multiple commands may share the same source, release asset, cache entry, and package bytes. The `--also <cmd=upstream-binary>` add workflow reduces repetitive manual TOML editing but must not introduce a grouped manifest shape or implicit shared-source contract that can hide command-specific selected binaries.
- `binpm.lock` is the committed deterministic local resolution file. It must use TOML, `version = 1`, `[tools.<cmd>]` command tables keyed by local command name, and `[tools.<cmd>.targets.<target-key>]` records keyed by normalized target.
- The lockfile target key format must be `<target_os>-<target_arch>-<target_libc>`, using the enum-style values from the runtime target model.
- Each `binpm.lock` target record must include package spec, normalized source, source provider, source host, source path, requested version when present, release tag, asset name, sanitized canonical asset URL, target OS, target architecture, target libc/ABI, archive format, selected binary path inside the archive or bare asset, installed binary path, SHA-256 digest, checksum source (`github-digest`, `sidecar`, `manifest`, `signature`, `local`), whether upstream signature material was available, and whether signature verification succeeded.
- Committed `binpm.lock` records must never store query strings, fragments, bearer tokens, private-token parameters, expiring signed URLs, or other credential-bearing download URLs. Runtime-only authenticated URLs may be used for download but belong only in transient memory or uncommitted machine-local state.
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
signature_verified = false
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
- The current concrete cache entry layout is `~/.binpm/cache/sha256/<hex>/asset` for original asset bytes plus `~/.binpm/cache/sha256/<hex>/record.toml` for cache metadata. The cache key string stored in package records is `sha256:<hex>`.
- The local-project cache reference index lives under `~/.binpm/cache/refs`. New references are TOML records containing the project root, command name, and cache key. Existing legacy reference files containing only `sha256:<hex>` remain valid preserving references but cannot be classified as stale.
- Cache entries must be content-addressed by `sha256:<hex>` when provider metadata exposes a trusted SHA-256 digest.
- When provider metadata does not expose a trusted digest, `binpm` must compute SHA-256 after download and use the local digest as both the cache key and the install manifest verification value.
- The current implementation records downloaded assets with `checksum_source = "local"` unless a provider digest or successfully verified package signature supplies a stronger source. Checksum sidecar and checksum manifest discovery remain implementation work.
- Cache lookup for assets without provider-provided digests may use source metadata to find a prior local digest, but source provider, source host, source path, release tag, asset name, or URL alone must not make bytes reusable without SHA-256 revalidation.
- Cache metadata must preserve the source provider, source host, source path, release tag, asset name, sanitized canonical asset URL, byte size when known, checksum source, creation timestamp, and last-used timestamp when known.
- Cache metadata may reference more than one installed command or package record for the same verified asset bytes.
- Global package records under `~/.binpm/packages` are required machine-local install records for `binpm install <source>`.
- Global package records must record package spec, normalized source, source provider, source host, source path, requested version when present, release tag, asset name, sanitized canonical asset URL, target OS, target architecture, target libc/ABI, archive format, selected binary path inside the archive or bare asset, installed binary path, cache key, cache path, SHA-256 digest, checksum source (`github-digest`, `sidecar`, `manifest`, `signature`, `local`), install timestamp, whether upstream signature material was available, and whether signature verification succeeded.
- Project-local package records under `$repoRoot/.binpm/packages`, when implemented, are uncommitted machine-local install records and must use the same metadata fields as global package records.
- Committed `binpm.lock` target records must preserve deterministic resolution metadata only: package spec, normalized source, source provider, source host, source path, requested version when present, release tag, asset name, sanitized canonical asset URL, target OS, target architecture, target libc/ABI, archive format, selected binary path inside the archive or bare asset, installed binary path, SHA-256 digest, checksum source, whether upstream signature material was available, and whether signature verification succeeded.
- The global cache is separate from installed package records and executable links or copies. Removing cache entries must not remove package manifests or files under `~/.binpm/bin`. Cleaning the cache must not remove `~/.binpm/cache/refs`; pruning may remove only stale structured reference files before asset pruning.
- Temporary extraction and cache population must be atomic: incomplete global downloads and extraction directories stay under `~/.binpm/tmp`, and incomplete project-local downloads and extraction directories stay under `$repoRoot/.binpm/tmp`.
- Failed installs must not update cache entries, package records, `binpm.lock`, `~/.binpm/bin`, or `$repoRoot/.binpm/bin`.
- Concurrent installs for the same asset must use temporary files plus atomic rename or a cache lock so partial downloads are never visible as reusable cache entries.

## Security
- `binpm` must use HTTPS source-provider APIs and release asset URLs.
- Source-provider release lookup may authenticate with provider tokens from environment variables only. Host-specific variables take precedence and are the only supported path for GitHub Enterprise and self-managed GitLab hosts:
  - GitHub.com: `BINPM_GITHUB_TOKEN_GITHUB_COM`, then `BINPM_GITHUB_TOKEN`, then `GITHUB_TOKEN`.
  - GitHub Enterprise `github:<host>/owner/repo`: `BINPM_GITHUB_TOKEN_<NORMALIZED_HOST>` only.
  - GitLab.com: `BINPM_GITLAB_TOKEN_GITLAB_COM`, then `BINPM_GITLAB_TOKEN`, then `GITLAB_TOKEN`.
  - Self-managed GitLab `gitlab:<host>/<namespace...>/<project>`: `BINPM_GITLAB_TOKEN_<NORMALIZED_HOST>` only.
- For GitHub Enterprise and self-managed GitLab hosts, `<NORMALIZED_HOST>` must be the source host encoded without collisions by uppercasing ASCII alphanumeric bytes and replacing every other UTF-8 byte with `_HH_` uppercase hexadecimal; for example `ghe.example.com` becomes `GHE_2E_EXAMPLE_2E_COM` and `ghe-example.com` becomes `GHE_2D_EXAMPLE_2E_COM`.
- Generic `BINPM_GITHUB_TOKEN`, `GITHUB_TOKEN`, `BINPM_GITLAB_TOKEN`, and `GITLAB_TOKEN` must apply only to `github.com` or `gitlab.com` respectively and must never be sent to explicit enterprise or self-managed hosts.
- GitHub requests must send provider tokens as an `Authorization: Bearer <token>` header. GitLab requests must send provider tokens as a `PRIVATE-TOKEN: <token>` header.
- Release lookup errors must distinguish missing authentication, insufficient permissions, and rate limiting. Private or hidden repositories returning `401`, `403`, or `404` without a configured token must report missing authentication and suggest the documented host-specific token path. The same statuses with a configured token must report insufficient permissions. `429` responses, GitHub/GitLab error responses with remaining rate-limit quota `0`, and equivalent rate-limit error responses must report rate limiting.
- Tokens, authorization headers, private-token headers, and credential-bearing URLs must never be logged, returned in errors, printed in diagnostics, written to cache metadata, written to package records, or persisted in lockfiles.
- Persisted URLs in committed lockfiles, cache metadata, diagnostics, errors, and logs must be sanitized by removing query strings and fragments. Credential-bearing or expiring download URLs must not be written to `binpm.lock`.
- If provider release asset metadata exposes a trusted SHA-256 digest, `binpm` must verify the downloaded asset against that digest before considering checksum sidecars or local fallback hashes.
- If an upstream checksum manifest or sidecar exists, `binpm` must verify the selected asset before installation.
- Package signature verification is separate from direct-installer Sigstore verification for binpm's own release artifacts.
- For packages installed by binpm, the supported signature trust policy is GitHub.com release assets with Sigstore bundle sidecars named `<selected-asset>.sigstore.json`. `binpm` must validate those bundles with `cosign verify-blob --bundle`, issuer `https://token.actions.githubusercontent.com`, and a certificate identity matching `https://github.com/<owner>/<repo>/.github/workflows/<workflow>@refs/tags/<release-tag>`.
- Signature material may satisfy strict verification only after the verifier successfully validates the selected asset under the documented trust policy. Raw `.sig`, `.asc`, `.minisig`, `.sigstore.json`, certificate, attestation, SBOM, checksum, provenance, or other sidecar presence alone must not count as verified bytes.
- If no provider asset digest, checksum sidecar, checksum manifest, or successfully verified signature exists, `binpm` must warn, compute SHA-256 locally, store it in the install manifest, and verify future reinstalls or cache reuse against that recorded digest.
- `--require-verified` and `binpm verify --require-verified` must fail unless the selected asset has at least one trusted verification source: provider digest, upstream checksum sidecar, upstream checksum manifest, or successfully verified signature under the documented package trust policy.
- Cache hits must be revalidated before extraction or install finalization using the strongest available integrity source: provider asset digest, upstream checksum material, successfully verified signature, or the locally recorded install manifest digest.
- If cache revalidation fails, `binpm` must discard the corrupted cache entry and redownload the asset. If the redownloaded bytes fail the trusted integrity source, installation must fail.
- Checksum, signature, SBOM, and provenance files are metadata inputs only; they must not be installed as binaries.
- URL diagnostics in errors and logs must omit query strings and fragments.
- Archive extraction must reject absolute paths, parent-directory traversal, unsafe symlinks, and files that would escape the package extraction root.

## Logging
- Use structured `tracing` logs for manifest discovery, lockfile parsing, release lookup, target normalization, asset candidate scoring, checksum discovery, download, extraction, binary discovery, install finalization, and `binpm x` command execution.
- `binpm -v` and `binpm --verbose` are stable global flags that set the binpm tracing filter to `binpm=info`.
- `binpm --debug` is a stable global flag that sets the binpm tracing filter to `binpm=debug`.
- `BINPM_LOG` remains supported as the binpm-specific `tracing_subscriber` env filter when no CLI verbosity flag is present. Deterministic precedence is: `--debug`, then `-v`/`--verbose`, then non-empty `BINPM_LOG`, then the default `binpm=warn`.
- Tracing color is controlled independently by `BINPM_LOG_COLOR` and `NO_COLOR`; verbosity flags must not change ANSI color policy.
- Candidate scoring logs must include normalized package spec, source provider, source host, release tag, asset name, detected OS, detected architecture, detected libc/ABI, detected CPU feature variant when present, artifact kind, score, and rejection reason when applicable.
- Download and cache logs must include sanitized URL origin, asset name, byte count when known, cache hit or miss state, cache key, cache path, cache action, cache validation source, cache reused state, cache eviction state, retry attempt, and final outcome.
- Install logs must include package spec, release tag, selected asset, selected archive member or bare executable, installed path, manifest path, lockfile path when local, and whether the install is global or project-local.
- Stable cache log keys include `cache_key`, `cache_path`, `cache_action`, `cache_validation_source`, `cache_reused`, `cache_evicted`, and `cache_bytes`.
- `binpm x` logs must include local project root when present, resolved command, explicit package spec when used, PATH entries added by binpm, install-on-demand state, process exit status, and whether command resolution came from `binpm.toml` or `--package`.
- Diagnostic command logs for `doctor`, `explain`, `verify`, and `cache key` must include enough structured context to distinguish read-only inspection from mutating install or update flows.
- Human CLI output may be concise, but debug logs must be sufficient to reconstruct why a candidate won or lost.
- Failures in release lookup, asset selection, download streaming, or verification must mention `--verbose` or `--debug` when structured diagnostics are likely to help.

## Download Progress
- Interactive installs must show human-facing progress on stderr for large or unknown-size release asset downloads. Progress output may be concise, but it must reassure users that the download is active and include a human-readable byte count when available.
- Non-interactive output, including redirected stderr and `CI=true`, must not emit periodic progress lines so scripts and CI logs stay stable.
- Retry messages for retryable download failures must explain which asset is being retried and the retry attempt, but must never include credentials, query strings, fragments, or expiring signed URL parameters.
- Download logs and progress diagnostics must use sanitized URLs that remove query strings and fragments and redact URL userinfo before display.

## Release and Distribution
- The crate remains publishable through `crates/binpm/Cargo.toml` so crates.io publication can carry first-party `cargo-binstall` metadata.
- Publish tag eligibility is enabled through root `[workspace.metadata.cargo-mono.publish.tag].packages`, and binpm release tags must use `binpm@v<version>`.
- Tag release automation is defined in `.github/workflows/release-binpm.yml` and must publish signed GitHub Release assets for `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/amd64`, and `windows/arm64`, including standalone binaries (`binpm-<os>-<arch>[.exe]`) and archives (`binpm-<os>-<arch>.tar.gz|zip`).
- Release signing outputs must include `SHA256SUMS.sigstore.json` and `<artifact>.sigstore.json` sidecars; legacy `.sig`/`.pem` sidecars are out of scope for direct installation.
- Direct installers must remain available at `scripts/install/binpm.sh` and `scripts/install/binpm.ps1`; direct installs must verify `SHA256SUMS` entries and Sigstore bundle sidecars via `cosign verify-blob --bundle`.
- Direct installers must detect unsupported x86 hosts before release tag resolution or artifact download. Use an x64/arm64 host or supported CI image when direct installers report an unsupported host.
- Direct-installer verification applies to binpm's own release artifact and must not be described as implementing signature verification for packages installed by binpm.
- Homebrew release automation must render the `binpm` prebuilt formula from release asset URLs and push tap updates directly to `delinoio/homebrew-tap` `main` with a dedicated tap-write credential.
- Homebrew installation must consume prebuilt `binpm` release archives for `darwin/amd64`, `darwin/arm64`, `linux/amd64`, and `linux/arm64`.
- `cargo-binstall` metadata must resolve only first-party GitHub Release assets and disable `quick-install` and `compile` strategies.
- Install docs that describe direct-install flows must keep Bash, PowerShell, Homebrew, `cargo-binstall`, and GitHub Actions usage aligned with installer scripts and manifest metadata.
- Direct-install documentation must make `cosign` a prerequisite before remote installer commands and describe missing `cosign` as a prerequisite failure rather than a verification bypass opportunity.

## Build and Test
- Local validation for binpm runtime changes must include `cargo test -p binpm` and the repository Rust baseline `cargo test --workspace --all-targets`.
- Publishability validation must include `cargo publish -p binpm --dry-run` or `cargo package -p binpm` when registry authentication or crates.io state blocks a dry-run publish.
- Initial skeleton tests must cover clap command availability, source spec parsing, target alias normalization, logging defaults, `init`, `env`, and read-only cache key foundations.
- Heuristic tests must cover OS aliases, architecture aliases, libc aliases, CPU feature variant scoring, exact libc preference, Linux glibc missing-libc fallback, Linux musl missing-libc rejection, source archive rejection, sidecar rejection, desktop installer de-prioritization, cargo-binstall candidates, cargo-dist candidates, GoReleaser candidates, Bun/Deno candidates, GitLab HTTPS rejection reasons, and ambiguous archive contents.
- Storage tests must cover atomic install behavior, cache miss download and digest recording, cache hit reuse after verification, digest mismatch eviction and redownload, concurrent partial download isolation, `binpm.toml` updates, `binpm.lock` updates, global install records, project-local install records, stale lock reinstall behavior, cache command behavior for `list`, `prune`, `clean`, and `key`, stale local-project cache-reference detection and cleanup, multi-command cache sharing, and unsafe archive path rejection. The current test suite covers atomic TOML writes, atomic bare-executable install writes, cache population/reuse by SHA-256, digest mismatch detection, cache prune/clean preservation boundaries, cache key missing-lockfile status, stale cache-reference detection/removal, sanitized persisted URLs, and deterministic lockfile records without runtime cache paths.
- Execution tests must cover `binpm x` local PATH behavior, argument forwarding, current-working-directory preservation, explicit `--package` execution, missing-manifest failure, missing-command failure, install-on-demand from a valid lockfile, frozen-lockfile failures, `--no-frozen-lockfile` override behavior, and read-only diagnostics for `doctor`, `explain`, and `verify`.

## Dependencies and Integrations
- Integrates with GitHub Releases through the GitHub API and release asset downloads.
- Integrates with GitHub Enterprise Releases through explicit-host GitHub source specs.
- Integrates with GitLab Releases and release asset links through explicit-host GitLab source specs.
- Depends on built-in archive extraction support for `.tar.gz`, `.tgz`, `.tar.xz`, `.txz`, `.tar.zst`, and `.zip`.
- Integrates with release automation, direct installers, cargo-binstall metadata, and Homebrew package updates for binpm distribution.
- May integrate with checksum and signature tooling later, but v1 must work without Node.js or language-specific package managers.
- Uses npm `npx` and `npm exec` only as behavioral references for PATH-based command execution and argument forwarding.
- Does not integrate with npm, pnpm, yarn, Bun, Cargo install, cargo-binstall, Homebrew, apt, rpm, or system package managers as install backends in v1.

## Change Triggers
- Update `docs/project-binpm.md` with this file when CLI shape, local manifest or lockfile format, storage layout, cache behavior, security policy, target model, or asset selection heuristics change.
- Update root `AGENTS.md` and `crates/AGENTS.md` when `binpm` project ownership, planned path status, release eligibility, or Rust-domain policy changes.
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
