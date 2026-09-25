### Instructions for `crates/`

- Follow root `AGENTS.md` and each crate-specific project document.
- Keep repository and domain rules in the appropriate `AGENTS.md` files.
- Write all source and comments in English.
- Prefer enums over free-form strings for stable internal and external contracts.

### Scope in This Domain

- Repository-owned crates declare Apache-2.0; imported VoidZero and Microsoft crates explicitly retain MIT and their upstream notices. Follow `docs/repository-license-contract.md`.
- `crates/forge-tree-doc`, `crates/forge-pptx`, `crates/delino-forge`: private Forge DSL, preserving PPTX adapter, and local CLI/stdio MCP. Follow `docs/crates-forge-foundation.md`; keep structured logs free of document content and publish output only through explicit export to a separate path from an opened document's tracked source. Keep the committed schema synchronized with Rust types, retain font source/license/hash and external-fixture provenance, and exercise the optional-renderer test explicitly in Forge rendering CI.
- Forge-generated content-type override paths must match the exact spelling of their ZIP members for reader interoperability. Scope compatibility normalization to new packages and preserve imported source XML.
- Forge document lock guards must explicitly unlock on drop, including error exits, so duplicated or fork-inherited descriptors cannot extend a completed operation's lock lifetime. Preserve exclusive ownership checks and redact lock-release diagnostics.
- Forge custom XML ownership requires both its package relationship and namespace identity. Preserve unrelated colliding part names and relationship IDs, and bind those original parts into the metadata hashes.
- Forge metadata bindings must be unique native targets in their corresponding logical slide; validate native slide order and count before accepting the stored tree.
- Forge packages require exactly one supported internal office-document root relationship; reject ambiguous roots before importing or editing.
- Forge exports to an opened document's tracked source, including canonical path aliases, must fail with `unsupported_edit` even with explicit overwrite; a fingerprint check followed by unconditional replacement cannot protect external saves. Keep separate-output export and explicit replacement of those outputs available, and expose this boundary through CLI/MCP help and capabilities. Retain recovery of earlier builds' interrupted source-export journals under the document lock; unrelated external changes remain conflicts.
- Forge atomic file publication must flush its renamed directory entry: sync the parent directory on Unix and use write-through same-volume publication on Windows. Propagate durability failures.
- Forge chart insertion must reject collisions with preexisting chart, workbook and relationship parts, including case-equivalent package names. A supplied node identity never grants ownership of original package parts.
- Forge image media reuse requires identical bytes, including case-equivalent part names; reject mismatched content instead of pointing a new image relationship at unrelated media.
- Forge image import must validate full-frame stretch fill as well as crop/aspect geometry before allowing contain/cover edits; preserve other native picture fills as opaque.
- Forge text import must validate body geometry against measurement semantics before exposing editable text; keep unrepresented insets, wrapping, columns, anchoring, rotation and autofit opaque.
- Forge asset loading must visit only referenced image handles, deduplicate aliases, and bound aggregate bytes before reads in addition to per-file limits and checksum validation.
- Forge nested canvases must validate unchanged children's bounds against their current allocation. Preserve native off-page bounds only for unchanged children under a slide-root canvas with unchanged page dimensions.
- Forge containers must reject placeholder references before creation or patch commit; logical containers do not emit native placeholder shapes.

- `crates/binpm`: Rust-based Node-free binary package manager for release assets.
- `crates/cargo-mono`: Cargo-based Rust monorepo management CLI.
- `crates/clibox`: non-publishable Rust executable distributed through npm and native packages.
- `crates/clibox-config`, `crates/clibox-system`, `crates/clibox-transform`, `crates/clibox-wait`: non-publishable clibox command-family implementations.
- `crates/nodeup`: Rust-based Node.js version manager.
- `crates/with-watch`: Rust-based filesystem-watching command wrapper.
- `crates/serde-feather`: Size-first serde runtime-facing core crate.
- `crates/serde-feather-macros`: Proc-macro companion crate for serde-feather.
- `crates/rustia`: Serde-based LLM JSON runtime crate.
- `crates/rustia-llm`: aisdk tool adapter crate for rustia-based function-calling input validation.
- `crates/rustia-macros`: Proc-macro derive companion crate for rustia.
- `crates/devhud-native-messaging-host`: implemented Rust Native Messaging host for DevHud and explicit workspace member.

### DevHud Native Messaging Host Rules

- Keep the canonical path at `crates/devhud-native-messaging-host` and document behavior in `docs/crates-devhud-native-messaging-host-contract.md`.
- The real crate skeleton is an explicit root workspace member. The host is a bounded Chrome-to-desktop broker, not a plugin SDK or API/GitHub/R2 client.
- Preserve Native Messaging origin/extension-ID/nonce/schema/timeout validation, a shared 256 KiB UTF-8 JSON body ceiling measured before length-prefix framing/parsing, user-scoped IPC, redacted `tracing` diagnostics, and the supported desktop OS/architecture matrix. Host connection establishment and authentication share one absolute five-second deadline on every platform. On Linux, create the non-secret per-user removal marker before persisting a pairing secret and remove it only after credential cleanup succeeds so Debian removal never depends on optional Chrome registration for affected-user discovery.
- The host-to-app IPC is an app-owned, versioned v1 length-prefixed JSON protocol over the documented per-user Unix socket or Windows named pipe, authenticated with a platform-secure pairing secret and challenge/response; it is independent of Connect RPC. Keep pairing retries nonce-free after successful pairing authentication, keep revocation unavailable to Chrome-originated message types, and require the secret-bound revocation-only authentication scope to invalidate the live app generation before unregister reports success, including while first pairing is pending. Unregister must delete pairing credentials before removing its per-user registration so failed cleanup remains retryable, and Debian removal must run it through each affected active user session before package-owned files are removed.

- Shared Forge native cancellation must remain operation-scoped, restore worker state after failure, and poll bounded processing loops. Existing CLI/MCP engine calls without a cancellation scope retain their behavior; JavaScript callbacks never enter workers.

### Rust Workspace Rules

- Add new crates as explicit workspace members in root `Cargo.toml`.
- Keep crate naming aligned with project IDs when possible.
- Document behavior contracts in project index docs and relevant crate-domain docs before large implementation changes.
- Keep clibox execution wrappers in `clibox-system` and their `run` command family independent of the other clibox companion crates. Reuse only the system environment planner, preserve literal child invocation semantics, and use native process groups/Windows Job Objects plus bounded cleanup for owned descendants. Unix ownership is limited to the wrapper process group, so daemonizing workloads or managed services that create another session/process group are unsupported and must be documented as self-managed. Lock/rate coordination may retain only documented private hashed local state and must fail closed on unsafe state.
- Planned crate paths must not be added as workspace members until the crate skeleton exists.
- For new package scaffolding, default `publish = false` until publish contracts are explicitly approved.
- Prefer minimal default features and keep optional capabilities opt-in for size-sensitive crates.
- Keep proc-macro crates and runtime crates separated by explicit crate boundaries.

### nodeup-Specific Rules

- Test cleanup must remove only the fixture-owned directory, never the process-wide temporary directory or sibling fixtures.

- Preserve rustup-like shim behavior: symlink strategy plus executable-name dispatch.
- Keep `nodeup shim setup` as the stable idempotent setup/repair command for managed `node`, `npm`, `npx`, `yarn`, and `pnpm` shims.
- Keep `nodeup shim setup` PATH activation non-mutating by default while reporting shell- and OS-aware activation and verification guidance.
- Keep Windows shim setup documented and implemented as copied `.exe` aliases with adjacent Nodeup ownership marker files because symlink privileges are not guaranteed and stale copies must be repairable without replacing unrelated executables.
- Keep `nodeup self uninstall` scoped to Nodeup-owned data, cache, and config roots; binary, shim, and shell profile/PATH cleanup must remain manual, separated from removed data, and visible in human and JSON output with shell- and OS-aware follow-up guidance.
- Keep channel and command identifiers stable and documented.
- Record storage and download behavior in project docs whenever changed.
- Keep direct installers and `cargo-binstall` metadata aligned with release asset names, checksum-verification contracts, and install docs. Nodeup direct installers must verify `SHA256SUMS` without requiring `cosign` or artifact Sigstore sidecars, and `cargo-binstall` must stay first-party-asset-only with `quick-install` and `compile` fallbacks disabled.
- Keep unsupported x86 host handling aligned across direct installers, runtime installation, shim dispatch, JSON diagnostics, and Nodeup docs.
- Keep `nodeup update` exact-version selector messaging aligned across human output, JSON diagnostics, CLI help, crate README, `apps/public-docs/docs/nodeup`, `docs/project-nodeup.md`, and `docs/crates-nodeup-foundation.md`: exact versions are immutable pins reported with stable `skipped-exact-version` status, and output must point users who intended to move pins toward installing or selecting a newer exact runtime.
- Keep Nodeup script-safe output guidance aligned across CLI help, crate README, `apps/public-docs/docs/nodeup`, `docs/project-nodeup.md`, and `docs/crates-nodeup-foundation.md`: `--output json` for structured automation, `nodeup toolchain list --quiet` for raw runtime identifiers, `nodeup completions <shell> >file` for completion redirection, default Nodeup logging off for those script-safe forms, and `RUST_LOG=off` only when scripts also require quiet stderr after a logging filter was set elsewhere.
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
- Preserve stable `--json` output for read-only binpm diagnostics (`list`, `info`, `outdated`, `doctor`, `explain`, `verify`, and `cache list`) and mutating final-result envelopes (`install`, `add`, `update`, and `remove`): emit one compact JSON object on stdout for success, avoid ANSI color in JSON-mode command output, keep parseable stderr error envelopes with `error.message` and `error.exit_code`, keep progress/tracing separate from stdout, and reuse documented enum values for scope, target, checksum source, and verification state.
- Keep explicit global command naming and upstream binary selection stable: `binpm install <source> --as <cmd> --bin <upstream-binary>` uses the explicit global alias while preserving source identity and selected upstream binary in package records, and human source-install output must show `install scope: global` before mutation plus the installed command alias and selected upstream binary as separate fields. When source-form install runs inside a project, output must state that the project manifest is not modified and guide users to `binpm add <cmd> <source>` for project-local tools. Source-form install is global-only; reject `binpm install <source> --local` with guidance to use `binpm add <cmd> <source>`.
- Keep explicit local declaration and one-off binary selection stable: `binpm add <cmd> <source> --bin <upstream-binary>` persists `[tools.<cmd>].bin`, `binpm add --manifest-only` mutates only `binpm.toml`, `binpm add ... --also <cmd=upstream-binary>` expands to separate deterministic `[tools.<cmd>]` declarations, and `binpm x --package <source> --bin <upstream-binary> [cmd]` selects that upstream binary for one-off execution without inferring sources from command names. Manifest-only success output and later `list`, `doctor`, and frozen `x` diagnostics must make declared-but-not-installed state visible and point to `binpm install`.
- Keep archive binary ambiguity diagnostics stable: list plausible executable candidates, include concrete retry commands using `--bin`, and mention repeated `--also <cmd=upstream-binary>` values for local multi-binary archives while preserving one `[tools.<cmd>]` manifest table per command.
- Keep committed `binpm.lock` target-specific and deterministic; install timestamps and other machine-local metadata belong in uncommitted package records or logs.
- Keep committed `binpm.lock` URLs sanitized and free of query strings, fragments, credentials, and expiring signed download parameters.
- Keep local `binpm remove` cleanup aligned with project-local package records when they exist.
- Keep release asset selection deterministic and documented by OS, CPU architecture, and libc/ABI environment.
- Keep checksum/signature fallback behavior aligned with `docs/project-binpm.md` and `docs/crates-binpm-foundation.md`.
- Keep strict verification behavior aligned with `--require-verified` and `binpm verify --require-verified`; signature material must count only after successful verification under a documented trust policy, and strict failures must distinguish missing trusted evidence from unsupported sidecar presence.
- Keep package signature verification distinct from binpm direct-installer Sigstore verification. For binpm-installed packages, raw signature, SBOM, provenance, attestation, certificate, and Sigstore sidecars are metadata only until the package verifier validates the selected asset under the documented package trust policy; detected unsupported sidecars must be reported separately from trusted evidence with safe names only.
- Keep local `binpm install`, `binpm update`, and `binpm x` behavior aligned with `--frozen-lockfile`, default `CI=true` frozen behavior, and `--no-frozen-lockfile`. Frozen commands must fail when they would need to create or modify `binpm.lock`, except empty-manifest local updates that require no lockfile changes must succeed without creating `binpm.lock` and must report the no-op without file-change plans. Frozen mode is a lockfile write guard, not an offline or cache-only mode. Documented execution aliases `binpm exec` and `binpm run` must share `binpm x` lockfile behavior.
- Keep frozen local install and `x` restore behavior deterministic: missing `.binpm/bin` executables and `.binpm/packages` package records may be restored from existing target lock records only when cache bytes match the locked SHA-256. If cache repair needs a download, it may use only the lockfile's persisted sanitized asset URL, must validate the recorded SHA-256 before installing or populating cache, and must not require provider release-list pagination. Same-origin locked GitHub or GitLab provider URLs may use runtime-only host-scoped provider authentication when configured; external locked asset URLs must not receive provider credentials.
- Keep `binpm update` and scoped `binpm remove` scope reporting and `--dry-run` previews aligned with `docs/crates-binpm-foundation.md`; previews must not mutate manifests, lockfiles, package records, cache references, or executables. Keep all-tools update mode visible when no command names are supplied. Keep local update aligned with exact-version manifest advancement to latest stable across `binpm.toml`, `binpm.lock`, and project-local executables. Keep `binpm update --global [cmd...]` aligned with global package records, existing alias and selected-binary preservation, latest-stable release lookup, and global install cache/install/verification finalization behavior.
- Keep `--no-confirm` stable for script compatibility and future dangerous-operation confirmation prompts.
- Keep `binpm x` command execution aligned with the local manifest contract: use manifest-declared tools or explicit `--package`, prepend project-local bin directories to `PATH`, and do not infer GitHub repositories from command names. `binpm exec` and `binpm run` are aliases of that same execution behavior; `binpm x` remains canonical in contracts and examples.
- Keep `binpm env --shell` shell values explicit: support `bash`, `zsh`, `fish`, `powershell`, and `pwsh`; keep `pwsh` as the PowerShell 7 setup profile target; accept `cmd` only to report that cmd.exe support is deferred with actionable cmd.exe PATH guidance. Keep omitted `--shell` best-effort inference and `--global`/`--local` output narrowing non-mutating.
- Keep global install, add, doctor, and plain env PATH setup guidance opt-in and non-mutating. `binpm env setup --shell <shell> [--dry-run]` is the explicit profile modification command and may append only the global bin PATH line after previewing the exact file and line; it must tell PowerShell 7 users to pass `--shell pwsh`, refuse ambiguous shell/profile targets, and not imply project-local `.binpm/bin` entries are suitable for profile persistence.
- Keep binpm publishability, release tags, direct installers, cargo-binstall metadata, and Homebrew packaging aligned with `docs/project-binpm.md` and `docs/crates-binpm-foundation.md`.
- Keep `.github/workflows/release-binpm.yml`, `scripts/install/binpm.sh`, `scripts/install/binpm.ps1`, and `crates/binpm/Cargo.toml` synchronized with release asset names and signing contracts.

### cargo-mono-Specific Rules

- Keep command identifiers stable and documented in `docs/project-cargo-mono.md` and `docs/crates-cargo-mono-foundation.md`.
- Preserve `cargo mono` subcommand compatibility (`cargo-mono` binary naming contract).
- Keep release-tag responsibility split: `bump` must not create tags, and `publish` may create tags only for packages listed in `[workspace.metadata.cargo-mono.publish.tag].packages`.
- The manual `Release Project` coordinator validates the exact version commit and publishes only the selected registry-eligible CLI crate without waiting for main CI. clibox skips registry publication and retains release-source validation before tagging. After registry publication succeeds, it pushes only that crate’s exact version tag with `delino-release-bot`. It may recover a missing remote tag after an already-published crate, but must reject a conflicting tag. There is no main-push workspace publisher.
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
- DevHud native-host IPC and registration fixtures must remain callable from the package-local DevHud CI commands. Native binaries, installers, signing, release, and deployment tasks are non-cacheable and CI must not publish or install outside disposable layouts.
- Keep logs sufficient for debugging install, dispatch, and runtime resolution flow.
- Keep CLI logs colorized by default for human operators, with explicit opt-out controls.

### clibox Rules

- YAML structural sharing uses `imbl` with `RcK`, preserving ordered-map diffing, bounded expansion accounting, and single-threaded reference sharing. Keep the panic-safe chunk dependency and the long shadowed-merge/resource-limit fixtures when updating collections.

- Keep the CLI README and `apps/public-docs/docs/clibox` aligned with user-facing behavior. Follow `docs/apps-clibox-docs-foundation.md`; the consolidated public guide covers all 25 next-release commands while version-specific guidance preserves the 19-command published 0.1.6 surface, including their limits, cancellation, and migration guidance.

- clibox CLI consistency uses canonical `run env`, `port list`, and `hash compute` without old-name aliases. Report `--quiet` suppresses stdout; PID selection is only `port list --pids`. File-output commands interpret `--output -` as stdout and `./-` as a literal dash file; `--force` requires real file output or `--in-place`. Keep short/long help, static redacted migration guidance, numeric owned-operation cancellation (130/143), filtered-error visibility, and native/npm behavior synchronized.

- Preserve clap's generated missing-subcommand help for `run`, `port`, `clipboard`, `system`, `wait`, `text`, `time`, `base64`, `hash`, `dotenv`, and `yaml` on stderr with exit code 2; keep root no-argument and explicit help output successful on stdout. Only generated help/version may bypass static redacted parser diagnostics.
- Keep `system cpus` in `clibox-system`: default `available` delegates unchanged to Rust's parallelism estimate, and `logical` counts online CPUs through the private Linux/macOS/Windows adapters without affinity or quota adjustments. The selected query alone runs, zero/malformed/unavailable results fail without fallback, and integer/JSON/quiet outputs remain exact and cancellable. Keep all diagnostics classified and redacted; never invoke external CPU tools or create persistent state.
- Preserve the six issue #916 OS utilities, seven issue #917 text/time/Base64/hash transformations, three issue #919 readiness waits, and issue #920's dotenv/YAML contracts, compatible root help/version, Rust 2021 edition, Apache-2.0 license, and Cargo/npm exact version synchronization in `docs/crates-clibox-foundation.md`. Keep OS adapters private and mockable, diagnostics redacted (including parser/tool failures), and stdout dedicated to results.
- Keep dotenv value tokens literal, validate every record, and count diagnostic columns by Unicode scalar value without changing byte-based limits; YAML must retain scalar precision, resolve document-local references/merges, reject invalid graphs and repeated document version directives, sort mappings, and be byte-idempotent, including escaped BMP noncharacters in keys and values. Bound raw input and final serialized output independently to 64 MiB and collection depth to 128 before excessive expansion. Validate all merge operands, but account for escaped scalar sizes without allocating encoded copies or charging shadowed values to the final output budget.
- Retain structural sharing for merged YAML mappings and incremental size/line/depth summaries; never copy or rescan every inherited key for each merge version. Preserve exact sub-limit metrics and recover them after shadowing oversized values.
- Dotenv `export` is also a valid assignment key, including with whitespace before `=`. The optional prefix requires an immediate ASCII space, following Node's baseline; tabs remain valid ordinary assignment whitespace. Distinguish the key from the prefix without weakening identifier validation or literal value preservation.
- Windows configuration publication must copy the destination DACL/protection to staging through a retained security handle, then use one handle-based rename for the commit. Retain both READ_CONTROL and WRITE_DAC on staging while copying the DACL because the setter also inspects its inheritance state. Close the destination inspection handle after copying its DACL and before the commit so publication cannot block its own rename. Permission failures must log only the operation enum and numeric OS error under debug logging. Retain delete access before copying restrictive permissions so failure/cancellation cleans staging; never use multi-step `ReplaceFileW` for concurrent publication. New Unix configuration outputs must restore mode 0600 after creation regardless of caller umask. Configuration file output must use staging on the destination filesystem, keep Unix staging behind an owner-only directory (0700 without macOS ACL grants) through permission copying and rename, preserve destination access permissions without requiring content-read access to a write-only Unix output, reject replacement links, clean up on handled failures/cancellation, and never duplicate stdout. Concurrent replacement tests must verify complete successful results and cleanup while allowing Windows sharing failures; verify blocked replacement preserves the destination and succeeds after the blocking handle closes. Configuration failures must retain static actionable stderr guidance and redacted classifications/positions when tracing filters disable error events. Failed diagnostic writes must not panic, expose raw fallback errors, or override command/cancellation status. YAML source diagnostics count CR, LF, and CRLF as single line breaks and columns by Unicode scalar value. Keep configuration diagnostics enum-classified and exclude keys, values, content, paths, argv, and dependency/panic text even with RUST_LOG.
- Preserve native/npm and musl distribution without runtime downloads or application state. Configuration commands must not access the network or execute shells. Further domain commands require their own contract-backed implementation.
- Preserve already-tokenized child quotes/backslashes without assignment unescaping, safe Windows batch argv dispatch, partial port-enumeration errors, process/ownership revalidation before forceful termination, a shared five-second verification wait, and cancellation that leaves opened applications running. Validate clipboard text completely before replacement/output and retain Linux ownership with an installed background tool; never install tools automatically.
- In-place YAML reads must open without following the final link/reparse point, validate regular-file and single-link status on that handle, and read from that same handle. Never validate a pathname and reopen it for input; ordinary non-in-place reads may still follow links.
- Install signal handlers only for the selected command family: private configuration runtime cancellation returns numeric 130/143 after cleanup, the Tokio readiness runtime returns numeric cancellation with final JSON, the utility runtime returns numeric 130/143 for owned operations while preserving delegated child status/Unix signals, and the transformation supervisor returns numeric 130/143 after cleaning unpublished output.
- Keep transformations offline and in Rust, binary input streaming for Base64/hash, timezone rules bundled and pinned, and publication restricted to completed output with preserved access permissions. Replacement must not require reading the existing output contents; request only metadata/security access and still fail if permissions cannot be preserved. Reject linked replacement destinations and sanitize parser/dependency/runtime errors before stderr. No input content, patterns, replacements, digests, argv, or paths belong in diagnostics. OS-command signals and transformation cancellation retain their distinct exit/publication contracts behind one command tree.
- Keep configuration publication in `clibox-config::config_publication` and streaming transformation publication in `clibox-transform::publication`: their result validation, staging ownership, permissions, and cancellation lifecycles follow their respective contracts. Do not route one command family through the other family's publication or signal handlers.
- All five clibox crates remain explicit workspace members with `publish = false` and are excluded from cargo-mono registry publication. Keep root command composition in `clibox`, configuration processing in `clibox-config`, OS utilities in `clibox-system`, offline transformations in `clibox-transform`, and readiness in `clibox-wait`, using direct path dependencies without companion-to-companion dependencies. Changes beyond the documented command contracts require an explicit contract update.
- Wait command kinds, HTTP methods, outcomes, and error classifications use enums. Poll immediately, delay only after unsuccessful attempts, clip all work/delays to monotonic deadlines, and cancel without target mutation or service termination.
- Let every resolved TCP address attempt finish within the shared deadline until any succeeds; preserve terminal errors only for an all-address failure instead of discarding other addresses on one destination's error.
- Use Rust networking and metadata only. HTTP verifies OS trust/hostname, completes at headers, disables proxies/credentials/redirects/client retries and custom CA overrides; file readiness follows symlinks but requires a regular file.
- Enable HTTP/2 explicitly and advertise both h2 and HTTP/1.1 in the preconfigured TLS client's ALPN; preserve headers-only readiness for either negotiated protocol.
- Preserve usable OS trust roots when other entries fail loading or parsing; fail trust initialization only when no usable roots remain, and expose counts rather than individual loader errors or certificate data.
- Keep one in-flight HTTP client/native trust initialization across attempt timeouts; retries await the retained result and cancellation must not wait for an uninterruptible OS trust call.
- Wait results and all authored parser/runtime/tracing diagnostics must omit input locators, credentials, bodies, raw argv, and dependency errors. Static diagnostics survive quiet/log filtering; dependency log targets remain disabled even with detailed `RUST_LOG`. Respect `NO_COLOR` and TTY-only color.
- Keep injected-clock, loopback/TLS, filesystem, privacy, and native signal tests. Trust fixtures must never modify user certificate stores. Preserve standalone runtime behavior and document crypto build-tool changes across the eight-target matrix.
- npm cancellation integration must use a disposable Windows console, clear inherited Ctrl+C-ignore state only there, and verify native configuration exit 130 and transformation cleanup/exit 130 through the Node launcher for Ctrl+C and Ctrl+Break. Repository CI provides Node for this npm-source-dependent integration.
- Delayed TCP readiness fixtures must retain a bound socket until listening starts; never release and reacquire ephemeral ports while parallel fixtures can reuse them.
- Keep each clibox crate Apache-2.0 licensed. Run all five crates' unit tests plus the executable process tests and npm/native packaging checks; Cargo publication dry-runs are not a clibox completion gate. Companion versions are internal and do not participate in product version bumps.
- Cross-command tests must account for Windows `run env` variable conversion, including numeric `$1` references, separately from direct transformation capture parsing and literal npm launcher forwarding.
- Checksum generation with explicit file output must rebase relative inputs against the manifest directory, resolving symlink parents before rebasing; preserve absolute inputs and stdout filename behavior.
- Windows transformation publication must retain temporary-file rename/cleanup access before copying permissions, replace read-only destinations without clearing their attributes, and leave originals unchanged on cancellation or failed publication. Apply temporary access attributes before the original DACL so denied attribute writes do not block an otherwise authorized replacement. Unsupported filesystem rename capabilities fail closed.
- Windows DACL fixtures must probe attribute-write permission with a fresh handle requesting exactly FILE_WRITE_ATTRIBUTES, including allowed controls and checks after cancellation and publication. Reapplying unchanged attributes is not an access-denial probe.

- clibox GNU release builds use the shared pinned AlmaLinux 9/glibc 2.34 boundary for npm and stable APT/DNF distribution; preserve the existing musl targets and optional user-installed desktop tools.

- The isolated Windows Ctrl+C test helper must explicitly clear inherited Ctrl+C-ignore state before spawning clibox, including under Git Bash release jobs. Keep this normalization confined to the test-owned process and include helper stdout/stderr on failure; production signal behavior is unchanged.

### pnport Rules

- Private pnport runtime belongs to `crates/pnport` and `crates/pnport-core`; macOS injection is the pnport-mode `crates/fspy_preload_unix` fork and Linux/Windows retain `crates/pnport-preload`; follow `docs/crates-pnport-foundation.md`, `docs/crates-fspy-vendor-contract.md`, and the complete requirements. Keep exact pnp/fspy pins, data-only graph loading, fail-closed interception, read-only dependency views, private leased cache and all six native conformance gates. No crates.io publication.
- Keep all three private Cargo versions and lock entries synchronized with the pnport npm source at unpublished `0.0.0`; the first selected minor release candidate is `0.1.0`. Build the executable and adjacent interception library together for each native archive, and require actual six-host execution and installation evidence in the separate exact-tag workflow before publication. A version commit or tag alone does not authorize publication.

- pnport macOS interpreter admission preserves logical script paths, verifies signed native images offline, and permits hardened images only with both DYLD-environment and disabled-library-validation entitlements. Never rewrite compiler binaries or signatures; native TypeScript fixtures pin the verified official release explicitly.
- pnport Linux runs dynamic and fully static x64/arm64 GNU ELF children under one owned-child seccomp/PTRACE_SEIZE supervisor. The private launch and probe helpers require an inherited socket whose peer is their same-image parent before they can stop for attachment; direct CLI invocation must fail promptly. Preserve foreground terminal job control: when a same-group tracee requests a group stop, its supervisor must stop too until SIGCONT. Doctor must probe actual tracing capability and the matching `.so`; unsupported kernels or container emulation fail closed with exit 125. Use the pinned fspy source fork as the sole upstream Linux reference and record the TRACE/ptrace adaptation in `docs/crates-pnport-foundation.md`; do not copy another snapshot under `pnport-preload`. Validate offline Ubuntu 22.04 Docker C, static Go, and Yarn inline/split fixtures. Emulated amd64 results never count as native x64 release evidence.
- pnport Linux scratch slots follow address-space ownership. Reuse completed task slots for shared-memory threads, reuse inherited mappings independently after fork, and let a vfork child borrow its suspended parent's slot without accumulating mappings in the parent.
- A newly auto-attached Linux child may report its initial ptrace stop before the parent's clone event; keep it parked until clone registration installs its task, address-space, FD, and cwd state. Track it for cleanup immediately and kill it without resuming an unmediated syscall if shutdown begins before registration.
- pnport Linux must classify inherited managed descriptors before resuming the owned root task; descriptor-relative mutations retain read-only rejection even when the descriptor was opened by the caller. Reject writable inherited managed descriptors before execution.
- pnport Linux named standard-stream aliases and their descendant paths must resolve against the tracee's tracked FD table, including after dup onto descriptors 0, 1, or 2.
- pnport Linux tracked descriptor-alias descendants must resolve nested virtual dependencies through the PnP view before entering the kernel.
- pnport Linux `chdir` and `fchdir` must serialize same-group traced filesystem syscall admission through the successful logical cwd update, and cleanup must cancel parked callers.
- pnport Linux must serialize descriptor changes and mediated descriptor mutations across threads sharing an FD table through syscall exit; reserve known dup destinations at entry and cancel parked peer entries before cleanup resumes them.
- pnport Linux native opens must remain available to peer threads while blocked, including FIFO reader/writer rendezvous; only managed opens participate in the FD barrier.
- pnport Linux must reject mount-namespace and filesystem-root transitions before execution while pathname classification uses the supervisor's namespace and root.
- pnport Linux must classify same-group `/proc/.../root` aliases by their underlying path, including physical cache paths; preserve native alias spelling when the path needs no virtual translation.
- pnport Linux must leave native pathname bytes unchanged when no virtual translation is required, including both operands of link and rename calls; lexical normalization cannot replace kernel symlink and trailing-separator lookup.
- pnport Linux must decide pathname rewriting by comparing the caller's resolved lookup path with physical backing. The PnP target identity may equal an unplugged package's physical path even when the caller used a virtual `node_modules` alias.
- Linux pathname classification follows existing workspace symlink components into virtual or managed backing, while native paths retain their original bytes and no-follow operations act on the link inode itself.
- pnport Linux arm64 syscall denial must update `NT_ARM_SYSTEM_CALL` after ordinary registers so rejected operations cannot execute during graceful cleanup.
- A Linux seccomp admission failure must cancel the stopped syscall and leave its consumed exit stop parked until cleanup queues the termination signal and resumes the tracee. Apply the same ordering to parked FD/cwd waiters; send final SIGKILL before the last ptrace continuation.
- Leave invalid child pathname pointers to the Linux kernel so ordinary EFAULT behavior survives, including null pointers in both operands of path operations.
- Linux openat2 must inspect extended open_how bytes before mediation: unknown nonzero extension bytes return E2BIG, zero bytes continue through virtual translation, and unreadable structures retain EFAULT.
- Translated Linux script exec must return EFAULT to the caller for unreadable argv or envp vectors without terminating the owned tree.
- Linux `inotify_add_watch` with `IN_DONT_FOLLOW` on a virtual dependency link must watch the materialized link inode, not its resolved package target.
- Missing entries beneath an existing read-only virtual dependency directory reject mutations with EROFS; ordinary missing reads retain ENOENT.
- Descendant Linux script admission errors must return native exec errno to the invoking process; only mediation and capability failures stop the owned tree.
- An absolute Linux script interpreter that resolves through a symlink loop must return ELOOP to its invoking process.
- pnport cache extraction must use a private snapshot verified against the destination archive digest. Rechecking only the mutable source after extraction cannot prove which bytes were published; retain rewrite-and-restore regression coverage.
- pnport cache cancellation after read-only staging must restore directory write permission and explicitly remove the incomplete stage before returning an error.
- pnport preload constructor entry and completed readiness are distinct acknowledgements. Supported cache lock waits after entry must not trigger the missing-injection deadline; a child result without readiness remains a failure.

### React Forge Engine Rules

- `forge-figma` owns pure bounded Figma model validation, preservation-aware diff and batch planning. Keep OAuth, network I/O and JavaScript execution outside Rust workers. Follow `docs/packages-react-forge-figma-contract.md`.

- Follow `docs/crates-react-forge-contract.md` and the complete issue #968 requirements. `forge-package` owns shared bounded OOXML preservation; `forge-document` owns shared text/style, asset and chart primitives; `forge-docx`, `forge-xlsx`, and `forge-pdf` own independent models and engines. `react-forge-node` is only the private N-API adapter.
- Native workers accept validated serializable data only. Never install a global logger, fetch external relationships, execute embedded content or silently discard unsupported content. Shared changes must retain Forge CLI/MCP behavior and pinned default fonts.
- System discovery and caller fonts apply to React Forge only. Presentation inspection must not require font setup before the caller can register fonts. Check newly rendered content without rendering untouched opaque source. Keep all packages unpublished.
- Emoji fallback must prefer supported color families on complete emoji graphemes before general text families, including with caller fonts only. Preserve ordinary digits/spacing and explicit text presentation selectors; do not globally prioritize emoji fonts for all text.
- Keep React Forge font shaping and PDF tagging operation-owned. `TextLayout` injection and explicit `FontEmbedding` selection must preserve existing Forge API defaults. Office font references do not imply embedded caller fonts. PDF subset embedding must enforce licensing flags, and repeated visual table headers must remain pagination artifacts outside the logical reading order.

- React Forge preserving PPTX updates must restore source-digest-bound node and image identities across independent native imports. External packages without Forge metadata must support no-op byte preservation and repeated mounted edits.

- React Forge resource tests must cover accepted boundaries as well as over-limit rejection. Preserve iterative XML preflight and bounded-stack handling for valid deep XML until the upstream recursive parser has a proven safe stack bound.

- Word drawing replacement owns only supported inline content; foreign paragraph/run attributes and surrounding bookmark/field/revision markers must stay opaque. Validate extension namespaces before exposing a chart as editable.

- Spreadsheet rule editability requires modeled attributes on every rule and nested threshold/color element; unsupported precedence or rendering properties remain opaque even without extension namespaces.

- DOCX text backgrounds use native run shading; paragraph defaults must be materialized on text runs with explicit run overrides retained.

- React Forge must retain six native macOS/Windows/glibc Linux x64/arm64 targets. Enable all system-font tests in its prepared host matrix and validate Windows cancellation in an isolated real console, never by treating Node process.kill as a console event.
- Presentation text editability must validate paragraph/run semantics as well as body geometry, including table-cell text. Preserve unmodeled fields, links, bullets, defaults and extensions as opaque content.
- Imported PPTX update envelopes must contain replacements and source identity, not a serialized copy of untouched Office content. Apply the React-tree input ceiling to authored work without making successfully imported large documents unexportable.
- Imported DOCX mounted measurements must use source section/cell flow constraints. Preserve unknown width as unavailable geometry while retaining safe edits; never substitute a default page width for ambiguous source layout.
