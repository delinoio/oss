# Project: nodeup

## Goal
Provide a Rust-based Node.js version manager with predictable channel resolution, deterministic shell completions, and shim-based execution.

## Project ID
`nodeup`

## Domain Ownership Map
- `crates/nodeup`
- `apps/nodeup-docs`

## Domain Contract Documents
- `docs/crates-nodeup-foundation.md`
- `docs/apps-nodeup-docs-foundation.md`

## Cross-Domain Invariants
- Stable channel naming and runtime dispatch semantics must be preserved.
- `current` is the canonical selector for the newest Node.js release-index entry; `latest` remains a supported alias that resolves identically and reports canonical alias metadata in JSON output.
- Exact-version runtime selectors are immutable pins for `nodeup update`; they are canonicalized to `v<semver>` when tracked and are semantically deduplicated with non-`v` inputs.
- Tracked semantic selectors must be canonicalized so legacy duplicate exact selectors and channel aliases such as `current`/`latest` do not produce duplicate update work.
- No-argument `nodeup update` must report whether implicit targets came from tracked selectors or installed runtimes, including structured empty-target diagnostics for automation.
- Shim behavior must remain deterministic across supported operating systems.
- Windows shim alias filenames and delegated runtime executable filenames are separate concepts: Nodeup recognizes managed alias basenames through extensionless, `.exe`, and `.cmd` invocations, while Windows runtime package-manager delegation checks the selected runtime's `bin/<command>.cmd` files.
- `nodeup shim setup` is the stable idempotent setup/repair command for Nodeup-managed `node`, `npm`, `npx`, `yarn`, and `pnpm` shims, and must not replace unrelated existing commands.
- `nodeup shim setup` conflicts must identify the conflicting path, ownership classification, and remediation in human output and JSON diagnostics.
- Windows copied shims must use adjacent Nodeup ownership markers so stale Nodeup copies can be repaired without replacing unrelated executables.
- Linked runtime lifecycle commands must preserve external runtime directories: `toolchain link` registers settings records and `toolchain unlink` removes those records only.
- Linked runtime names are case-sensitive, but reserved channel names `lts`, `current`, and `latest` and their case variants such as `LTS`, `Current`, and `LATEST` are invalid linked names; rejection diagnostics must explain the channel ambiguity and suggest safe alternatives such as `local-lts` or `work-node`.
- Legacy persisted linked runtime selectors that use reserved-channel case variants must remain removable and reportable as linked-runtime selectors.
- Linked runtime resolution must validate that the selected `node` executable is runnable, including Unix executable-bit checks and Windows `node.exe` naming behavior.
- Linked runtime command availability diagnostics must distinguish the minimum runnable `node` link requirement from optional per-shim availability for `node`, `npm`, `npx`, `yarn`, and `pnpm`, make missing optional package-manager shims visible in human output, and expose checked paths, linked runtime identity, install-on-demand eligibility, and PATH/PATHEXT guidance in JSON output.
- `nodeup run` missing-version errors must keep installation opt-in with `--install`, while managed alias dispatch may install a missing selected version runtime on demand. Human and JSON diagnostics must make that distinction explicit.
- `package.json` `packageManager` support for `yarn|pnpm` must remain strict and deterministic: supported values are exact `yarn@<semver>` or `pnpm@<semver>` strings only, and invalid values must report the failed part plus JSON diagnostics.
- `yarn` and `pnpm` package-manager planning must be visible in human output, JSON output, and planning logs. Output must distinguish direct runtime binaries, pinned npm-exec delegation, unpinned npm-exec fallback specs, and unsupported Corepack behavior, including whether an npm-exec package spec is pinned or an unpinned fallback.
- `nodeup override unset --path <path>` and `nodeup override unset --nonexistent` are mutually exclusive so scripts cannot mix path-scoped removal with global stale-entry cleanup.
- `nodeup self uninstall` cleanup boundaries are data/cache/config only; binary, shims, and shell profile/PATH cleanup must remain manual and visible in human and JSON output, and non-Nodeup-owned configured roots must be reported as ownership-refused instead of deleted.
- Shell completion generation must remain deterministic for supported shells and top-level command scopes, and public docs must distinguish raw script generation from shell-specific installation or sourcing.
- `nodeup default <runtime>` may install version/channel targets as part of setting the default; human and JSON output must report that side effect explicitly.
- Invalid shell completion subcommand scopes must be rejected with hints that point back to the nearest valid top-level scope.
- Human output styling controls (`--color`, `NODEUP_COLOR`, and `NO_COLOR` precedence) must remain stable across CLI and public documentation.
- `nodeup show color` must remain available as the color diagnostic command for human stdout, human stderr, log color decisions, ignored invalid color environment values, and `NO_COLOR` precedence conflicts.
- `--output json` must render both application-level errors and clap parser failures as JSON error envelopes on stderr, except raw completion script output remains unwrapped on success.
- Checksum mismatch and runtime archive download diagnostics must include sanitized release index and download-base source details when a mirror override is configured. JSON diagnostics must include source identifiers and mirror mismatch indicators. URL diagnostics must strip credentials, query strings, and fragments, and the hint must tell users to verify that `NODEUP_INDEX_URL` and `NODEUP_DOWNLOAD_BASE_URL` point to matching Node.js release data.
- Script-safe output guidance must remain discoverable from CLI help and docs: use `--output json` for structured automation, `nodeup toolchain list --quiet` for raw runtime identifiers, and `nodeup completions <shell> >file` for completion redirection. Logs must stay on stderr when enabled; JSON management commands, quiet runtime lists, and completion generation keep Nodeup logging off by default unless `RUST_LOG` explicitly enables tracing. Use `RUST_LOG=off` only when scripts also require quiet stderr after a logging filter was set elsewhere.
- Tracing logs must be written to stderr when enabled so stdout remains reserved for command results, JSON payloads, quiet runtime identifiers, delegated command stdout, and raw completion scripts. Management `--output json`, `nodeup toolchain list --quiet`, and `nodeup completions <shell>` keep tracing logs off by default so parseable stdout and JSON stderr payloads remain parseable unless `RUST_LOG` explicitly enables tracing.
- `nodeup toolchain install` and `nodeup toolchain uninstall` require at least one runtime selector at the parser layer.
- `nodeup toolchain install` accepts only exact-version and channel selectors; linked-name selectors must be rejected before linked-runtime lookup so the error is deterministic whether or not the linked name exists. Multi-selector install invocations must validate every requested selector before any release-index resolution, archive download, extraction, tracking, or install mutation starts.
- Explicit `nodeup update <runtime>...` invocations must validate every requested selector before any channel resolution or install mutation starts; valid linked-name selectors remain non-installing `skipped-linked-runtime` update entries.
- `nodeup toolchain uninstall` removes exact installed versions only and must fail with `conflict` before mutation when an exact-version global default or directory override references a requested runtime; human output must name each blocking reference type and path with follow-up commands, channel-selector rejections must point users to installed exact versions, linked-selector rejections must point users to `toolchain unlink`, and JSON error envelopes must include deterministic blocker diagnostics.
- `nodeup toolchain unlink` must fail atomically with `conflict` before mutation when any requested linked runtime is referenced by the global default or directory overrides; human output must name every blocker with remediation commands and retry commands, JSON diagnostics must include deterministic `blocked_linked_runtimes`, `blockers`, and `retry_commands` fields, and external runtime directories must remain untouched.
- Release automation must publish both standalone prebuilt binaries and archive assets for `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/amd64`, and `windows/arm64`, plus Sigstore bundle sidecars (`*.sigstore.json`) for each artifact and `SHA256SUMS`. Public release and installation docs must explain that `amd64` release asset names correspond to x64 hosts.
- Direct installers must verify `SHA256SUMS` entries and Sigstore bundle sidecars, require `cosign`, and only support bundle-enabled releases.
- Direct installers must preflight missing `cosign` before release lookup or artifact download, explain OS-specific setup, and keep missing-prerequisite failures distinct from checksum or Sigstore verification failures.
- Direct installers must remain available at `scripts/install/nodeup.sh` and `scripts/install/nodeup.ps1`.
- Public direct-installer documentation must include copy-pasteable remote commands for POSIX shells and PowerShell that fetch the installer from first-party `delinoio/oss` raw GitHub URLs, tag/commit-pinned command patterns for reproducible automation, and the canonical in-repo script paths for maintainer workflows.
- Remote direct-installer examples must use `https://raw.githubusercontent.com/delinoio/oss/refs/heads/main/scripts/install/nodeup.sh` and `https://raw.githubusercontent.com/delinoio/oss/refs/heads/main/scripts/install/nodeup.ps1` for current public docs, or a tag/commit-pinned equivalent of those same first-party paths when reproducibility is required.
- Public installation docs must include an install-method chooser that explains when to use Homebrew, direct installers, `cargo-binstall`, and binpm.
- `cargo-binstall` metadata must resolve only first-party GitHub Release assets and disable third-party quick-install and compile fallback strategies; install and troubleshooting docs must explain that unsupported hosts or missing first-party assets do not fall back to source compilation.
- Homebrew installation must use prebuilt `nodeup` release archives for `darwin/amd64`, `darwin/arm64`, `linux/amd64`, and `linux/arm64`.
- `nodeup` runtime installation and shim dispatch must support `macOS`, `Linux`, and `Windows` x64/arm64 hosts while leaving x86 hosts out of scope; forced macOS platform aliases may use `macos-x64`, `macos-arm64`, `macos/x64`, `macos/arm64`, `darwin-x64`, or `darwin-arm64`; unsupported hosts must fail with `unsupported-platform`, deterministic platform diagnostics, the supported OS/architecture pairs, and the next action to use an x64/arm64 host or supported CI image.
- `apps/nodeup-docs` must use the repository-default Rspress/Rsbuild-family static documentation toolchain and Cloudflare Pages deployment contract unless this project index and `docs/apps-nodeup-docs-foundation.md` document a replacement.
- The canonical `nodeup-docs` production URL is `https://nodeup.delino.io`.
- Nodeup documentation routes exposed by `apps/nodeup-docs` are `/`, `/installation`, `/getting-started`, `/commands`, `/runtime-resolution`, `/shims-and-package-managers`, `/output`, `/completions`, `/releases`, `/troubleshooting`, and `/reference`.
- `apps/nodeup-docs` must expose a visible GitHub repository link to `https://github.com/delinoio/oss` in the top-level social links and in the document-page footer.
- Nodeup documentation routes exposed by `apps/nodeup-docs` must stay aligned with runtime, release, installer, shim, completion, package-manager, human/JSON output, and color-control contracts.

## Change Policy
- Update this index, `docs/crates-nodeup-foundation.md`, and `docs/apps-nodeup-docs-foundation.md` in the same change for behavior or storage contract updates that affect Nodeup documentation.
- Update this index and `docs/apps-nodeup-docs-foundation.md` in the same change for `apps/nodeup-docs` path, route, theme repository-link surface, toolchain, validation, or deployment contract updates.
- Keep `scripts/install/nodeup.sh`, `scripts/install/nodeup.ps1`, and `crates/nodeup/Cargo.toml` synchronized with release asset names and signing contracts.
- Keep release, install, and documentation-app contracts synchronized with root, `crates/AGENTS.md`, and `apps/AGENTS.md` rules.

## References
- `docs/project-template.md`
- `docs/domain-template.md`
- `docs/README.md`
