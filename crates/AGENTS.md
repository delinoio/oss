### Instructions for `crates/`

- Follow root `AGENTS.md` and each crate-specific project document.
- Keep repository and domain rules in the appropriate `AGENTS.md` files.
- Write all source and comments in English.
- Prefer enums over free-form strings for stable internal and external contracts.

### Scope in This Domain

- `crates/binpm`: Rust-based Node-free binary package manager for release assets.
- `crates/cargo-mono`: Cargo-based Rust monorepo management CLI.
- `crates/clibox`: non-publishable Rust executable distributed through npm and native packages.
- `crates/clibox-system`, `crates/clibox-transform`, `crates/clibox-wait`: non-publishable clibox command-family implementations.
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

### Rust Workspace Rules

- Add new crates as explicit workspace members in root `Cargo.toml`, except the documented independent `crates/runlens` workspace.
- Keep crate naming aligned with project IDs when possible.
- Document behavior contracts in project index docs and relevant crate-domain docs before large implementation changes.
- Planned crate paths must not be added as workspace members until the crate skeleton exists.
- For new package scaffolding, default `publish = false` until publish contracts are explicitly approved.
- Prefer minimal default features and keep optional capabilities opt-in for size-sensitive crates.
- Keep proc-macro crates and runtime crates separated by explicit crate boundaries.

### nodeup-Specific Rules

- Preserve rustup-like shim behavior: symlink strategy plus executable-name dispatch.
- Keep `nodeup shim setup` as the stable idempotent setup/repair command for managed `node`, `npm`, `npx`, `yarn`, and `pnpm` shims.
- Keep `nodeup shim setup` PATH activation non-mutating by default while reporting shell- and OS-aware activation and verification guidance.
- Keep Windows shim setup documented and implemented as copied `.exe` aliases with adjacent Nodeup ownership marker files because symlink privileges are not guaranteed and stale copies must be repairable without replacing unrelated executables.
- Keep `nodeup self uninstall` scoped to Nodeup-owned data, cache, and config roots; binary, shim, and shell profile/PATH cleanup must remain manual, separated from removed data, and visible in human and JSON output with shell- and OS-aware follow-up guidance.
- Keep channel and command identifiers stable and documented.
- Record storage and download behavior in project docs whenever changed.
- Keep direct installers and `cargo-binstall` metadata aligned with release asset names, checksum-verification contracts, and install docs. Nodeup direct installers must verify `SHA256SUMS` without requiring `cosign` or artifact Sigstore sidecars, and `cargo-binstall` must stay first-party-asset-only with `quick-install` and `compile` fallbacks disabled.
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

### Runlens Rules

- Report outcomes must not contain both a child exit code and termination signal. Any recorded signal prevents execution success and a verification pass, including in-memory analysis before serialization.

- Executable resolution reads exactly PATH on Unix and uses case-insensitive environment-key lookup only on Windows.

- Unix nonstandard descriptors lacking close-on-exec must make collection incomplete while preserving their inheritance; enumerate all open descriptors rather than an arbitrary low range.
- Inherited regular-file standard streams cannot establish complete collection without descriptor-I/O tracing; preserve the streams and mark such runs incomplete.

- Follow docs/project-runlens.md and docs/crates-runlens-foundation.md. Run independent tests/fmt/Clippy with nightly-2026-08-02 in addition to root cargo test.
- Keep fspy and dependency notices, immutable provenance, and a tested patch ledger. Do not reformat unrelated vendored sources or add root dependency patches.
- Do not inject unused tracing environment markers or overwrite the caller's `FSPY` value; retain only required backend coordination mutations.
- Collection budgets apply inside the tracing/snapshot layers, not only after materialization. Use typed incomplete outcomes and retain available evidence.
- Charge each snapshot record and directory member before retaining it; an over-budget final entry must remain incomplete even when no subsequent entry is walked.
- Redacted symlink targets are unknown evidence and mark collection incomplete; identical masking placeholders must never prove that two targets are equal.
- Exact directory exclusions apply to every descendant in snapshots, membership, and observed access scope, including paths that no longer exist; excluded inputs cannot become apparently new outputs.
- Scope report envelope budgets to external parsing and restore them on failure; repeated access to validated internal evidence must not cumulatively spend that input budget.
- Deserialize evidence under the shared process memory budget before spilling; do not force every small report map to allocate its own file handles.
- Preflight conflict analysis against the aggregate limit of 65,536 cross-report target pairs before inspecting paths or creating findings; preparation executions do not count.
- Comparison compatibility requires a present matching source revision and matching working-tree inclusion policy; missing revisions and identical observed bytes cannot make unbound or different source selections comparable.
- Offline cache and policy checks require at least one target independently of preparation records; historical-only reports cannot pass.
- Audit declared input/output glob intersections independently of observed paths; bounded analysis exhaustion is inconclusive, never proof that declarations are disjoint.
- Cache and policy input/output declarations bind to the selected command's normalized argv and working directory and any recorded name. A mismatched or redacted identity is inconclusive; reading config and reports must never execute a command.
- Cancellation during after-state snapshots must retain the cancelled invocation status and incomplete evidence even after the target exits successfully.
- New-access policies may infer absence only from a compatible baseline with complete collection; incompatible or incomplete baselines do not create definite new-access violations. Windows baselines union access modes only across proven ordinal path aliases, keep current evidence keys, and report unknown on bounded-search exhaustion or unresolved Unicode table parity.
- Linux OS identity includes distribution ID and VERSION_ID; legacy version-only, missing, or malformed identities cannot establish comparison compatibility.
- Selected environment names cannot prove equality of omitted values. Keep their report comparisons inconclusive without persisting values or guessable value hashes.
- Canonical isolated Windows HOME/cache paths must use normal drive/UNC spelling for Git compatibility, without inheriting user configuration.
- Clean environment selection must reject reserved Git controls case-insensitively on Windows so casing cannot escape fresh checkout/configuration isolation.
- Configured secret environment-name selection follows Windows case-insensitive key semantics before masking values in argv or path metadata; Unix selection remains case-sensitive.
- Retain the inspected executable handle through snapshots and revalidate pathname identity at the launch boundary; a stale digest must never certify a replacement executable.
- Normalize only complete workspace/home/temporary path roots; similarly prefixed external paths must remain distinguishable in policy and query evidence.
- Windows root masking and scope/exclusion checks use native ordinal case-insensitive comparisons with component boundaries; preserve Unix case sensitivity and avoid leaking differently cased local roots.
- Mask local roots in attached path options such as `-I/path` and `-L/path`, preserving component boundaries and unrelated external path identities.
- Windows open hooks must account for creation/overwrite dispositions and delete-on-close independently of the requested access mask.
- Windows file-information mutations record source writes; unresolved handles, rename/link destinations, or information classes mark collection incomplete while preserving the child syscall.
- Unix mutation coverage includes directory/link creation and metadata-only path/descriptor calls, including raw static Linux syscalls. Symlink target text is not a read, and direct directory creation attempts still require explicit write permission.
- Write allowlists cover snapshot directory-membership ancestors of known allowed descendant changes; explicit denies, unrelated siblings, type changes, and direct ancestor access attempts remain independently enforced.
- Linux doctor/preflight requires a known Ubuntu VERSION_ID at least 22.04 in addition to GNU/native architecture and seccomp capabilities; unknown, older, or other distributions cannot be reported as supported.
- Direct Windows NtCreateUserProcess calls must mark child coverage incomplete unless the same thread owns a CreateProcess injection transaction; other threads cannot borrow that ownership.
- Windows Detours LONG results must compare the returned code with NO_ERROR and propagate failures to transaction abort plus incomplete evidence; native Windows CI must exercise a real rejected attach call.
- Unix open-mode classification is shared by preload and seccomp: creation/truncation and Linux O_TMPFILE add write attempts, and stdio update modes add both read and write regardless of binary-mode spelling.
- Unix path removal must record write attempts for unlink/unlinkat/rmdir/remove, including AT_REMOVEDIR, missing paths, directory-relative paths, and Linux static syscalls; external deletion cannot evade literal write boundaries.
- Unix rename tracing must record both source and destination write attempts, including directory-relative, extended macOS, and static Linux syscall variants; syscall failure does not erase attempted access.
- Every workspace access ancestor must be a known directory before and after execution; missing/new ancestors remain unknown even when the other observation was a directory. Missing leaves under established directories remain valid attempts.
- Unsupported accesses, lexical aliases through parent components, and uncovered workspace paths prevent a policy pass; never collapse such paths across potentially changed symlinks to invent an identity. Known absolute external paths still support literal boundary checks without establishing cache coverage.
- Conflict analysis matches Windows ordinal path aliases and directory ancestors while keeping original evidence keys; bounded alias-search exhaustion and non-Windows Unicode case-table uncertainty remain explicit unknown evidence.
- Explain matches Windows path aliases with ordinal case-insensitive comparisons and retains stored evidence keys. Non-Windows Unicode aliases are candidates with explicit unknown table parity; ASCII aliases work on every analyst host.
- Offline path queries follow the report's OS syntax, including Windows drive and UNC paths; do not reinterpret Unix literal backslashes as separators.
- Compile Windows input/output/exclusion/policy globs case-insensitively, including directory roots. Offline checks follow each execution's recorded OS, not the analyst's host OS.
- Combined clean/repeat verdicts preserve definite failures ahead of inconclusive results and passes; an incomplete baseline cannot erase a policy violation.
- Preserve imported baseline executions as historical evidence with the baseline role even when current execution stops or collection is incomplete; only current target/preparation errors determine invocation exit status or receive current cleanup failures.
- Before clean/repeat source preparation, reject baseline plus planned execution counts exceeding the schema execution limit; never run a target whose combined evidence cannot fit that limit.
- Apply global read/write policy boundaries to current preparation and target executions. Target input/output declarations and baseline new-access comparisons remain target-scoped.
- Retained evidence maps share a process-wide memory threshold, including repeat rounds and supplied reports. Cancellation handlers must be registered before owned child work. Reject known macOS restrictive code-signing flags and restricted segments by bounded passive inspection; never invoke the target to discover its version.
- Unix session/group changes and spawn attributes that can detach descendants must mark collection incomplete before the operation, including raw static Linux calls; the managed group becoming empty is not proof of a complete detached tree.
- Raw Linux execve/execveat of shebang scripts must report unsupported interpreter coverage, including directory-relative and empty-path descriptor forms.
- Shebang scripts must not use their text digest as native executable identity; preserve the requested execution but mark unbound interpreter chains incomplete.
- Windows snapshot stability requires present volume serial and file index identities, including the opened handle and final pathname; equal size and timestamps alone cannot prove stability.
- Runlens executable digests must be bound to the native launched image, not a pathname recheck; keep inspected handles alive through completion and make missing/mismatched identity incomplete.
- Linux metadata handlers distinguish NULL capability probes from AT_EMPTY_PATH descriptor reads; diagnostics must never print raw syscall arguments or paths.
- Linux filesystem-statistics path/descriptor reads must count as input attempts, including statvfs wrappers and raw static syscalls.
- Linux extended-attribute value/list reads must record path and descriptor input attempts without retaining attribute names or values.
- Linux readlink/readlinkat collection records a read of the link itself, including empty-path descriptors; link target text is not an extra access.
- Linux nested execution reuses the inherited seccomp listener; do not install a second USER_NOTIF listener from preload hooks.
- Runlens Linux dynamic and static targets must both install inherited kernel syscall collection; libc interposition alone cannot establish completeness.
- Runlens must retain redacted clean/repeat HOME and cache accesses as out-of-scope evidence; omit only tracer-private storage from access records.
- All Runlens metadata spills, including retained baselines, share the excluded tracer-private namespace with collector files; keep storage alive through its owning indexes and surface cleanup failures.
- Runlens Windows NT process and file-object attribute hooks must copy and bound untrusted caller memory before parsing; malformed attributes preserve the original syscall and mark collection incomplete.

- Runlens Linux libc execveat must preserve every original operand and flag; inherited kernel collection owns its observations.

- Runlens Unix PATH exec hooks preserve libc text-file shell fallback and report unsupported interpreter coverage without changing argv or child exit status.

- Runlens variadic exec adapters must defer argument-budget enforcement to the OS, use checked allocation sizes, and preserve errno during cleanup.
- Runlens Windows DLL injection requires exact active-code-page round trips, with a verified short-path fallback or a prelaunch Unsupported error.

- Runlens Windows UNC namespace normalization must retain the absolute server/share root in external observations and prefix comparisons.

- Runlens frozen-source copies preserve a Windows symbolic link’s own directory/file reparse type even when its target does not exist.

- Runlens Windows spawn must complete fallible setup before resuming the child; its existing Job and original child handle own the remaining lifecycle.
- Runlens Unix native fixtures must isolate ambient runner descriptors at the test command boundary. Explicit inheritance fixtures re-enable their own descriptors and must continue to prove that production preserves the handles and rejects complete verification.
- Runlens observes Unix chdir/fchdir as directory metadata read attempts before cwd mutation; Linux kernel collection must also cover direct/static calls.
- Runlens macOS loader observations retain library read paths and mark loose/custom images incomplete; only confirmed active shared-cache images may avoid pre-attachment loader-loss classification.

- Runlens Linux io_uring setup, enter, and registration attempts preserve the syscall while marking collection incomplete; SQPOLL must not evade this rule by avoiding enter.

- Runlens Linux inotify_add_watch records watch paths as read attempts for successful and failed registrations, including static callers; never retain event payloads.

### clibox Rules

- Preserve the six issue #916 OS utility commands and seven issue #917 text/time/Base64/hash commands and three issue #919 readiness waits, compatible root help/version, Rust 2021 edition, MIT license, and Cargo/npm exact version synchronization in `docs/crates-clibox-foundation.md`. Keep OS adapters private and mockable, diagnostics redacted (including parser/tool failures), and stdout dedicated to results.
- Keep transformations offline and in Rust, binary input streaming for Base64/hash, timezone rules bundled and pinned, and publication restricted to completed output with preserved access permissions. Replacement must not require reading the existing output contents; request only metadata/security access and still fail if permissions cannot be preserved. Reject linked replacement destinations and sanitize parser/dependency/runtime errors before stderr. No input content, patterns, replacements, digests, argv, or paths belong in diagnostics. OS-command signals and transformation cancellation retain their distinct exit/publication contracts behind one command tree.
- All four clibox crates remain explicit workspace members with `publish = false` and are excluded from cargo-mono registry publication. Keep root command composition in `clibox`, OS utilities in `clibox-system`, offline transformations in `clibox-transform`, and readiness in `clibox-wait`, using direct path dependencies without companion-to-companion dependencies. Changes beyond the documented command contracts require an explicit contract update. Preserve already-tokenized child quotes/backslashes without assignment unescaping, safe Windows batch argv dispatch, partial port-enumeration errors, process/ownership revalidation before forceful termination, a shared five-second verification wait, and cancellation that leaves opened applications running. Validate clipboard text completely before replacement/output and retain Linux ownership with an installed background tool; never install tools automatically.
- Preserve clap's generated missing-subcommand help for `run`, `port`, `clipboard`, `wait`, `text`, `time`, `base64`, and `hash` on stderr with exit code 2; keep root no-argument and explicit help output successful on stdout. Only generated help/version may bypass static redacted parser diagnostics.
- Wait command kinds, HTTP methods, outcomes, and error classifications use enums. Poll immediately, delay only after unsuccessful attempts, clip all work/delays to monotonic deadlines, and cancel without target mutation or service termination.
- Let every resolved TCP address attempt finish within the shared deadline until any succeeds; preserve terminal errors only for an all-address failure instead of discarding other addresses on one destination's error.
- Use Rust networking and metadata only. HTTP verifies OS trust/hostname, completes at headers, disables proxies/credentials/redirects/client retries and custom CA overrides; file readiness follows symlinks but requires a regular file.
- Enable HTTP/2 explicitly and advertise both h2 and HTTP/1.1 in the preconfigured TLS client's ALPN; preserve headers-only readiness for either negotiated protocol.
- Preserve usable OS trust roots when other entries fail loading or parsing; fail trust initialization only when no usable roots remain, and expose counts rather than individual loader errors or certificate data.
- Keep one in-flight HTTP client/native trust initialization across attempt timeouts; retries await the retained result and cancellation must not wait for an uninterruptible OS trust call.
- Wait results and all authored parser/runtime/tracing diagnostics must omit input locators, credentials, bodies, raw argv, and dependency errors. Static diagnostics survive quiet/log filtering; dependency log targets remain disabled even with detailed `RUST_LOG`. Respect `NO_COLOR` and TTY-only color.
- Keep injected-clock, loopback/TLS, filesystem, privacy, and native signal tests. Trust fixtures must never modify user certificate stores. Preserve standalone runtime behavior and document crypto build-tool changes across the eight-target matrix.
- Delayed TCP readiness fixtures must retain a bound socket until listening starts; never release and reacquire ephemeral ports while parallel fixtures can reuse them.
- Keep each clibox crate MIT licensed. Run all four crates' unit tests plus the executable process tests and npm/native packaging checks; Cargo publication dry-runs are not a clibox completion gate. Companion versions are internal and do not participate in product version bumps.
- Cross-command tests must account for Windows `run env` variable conversion, including numeric `$1` references, separately from direct transformation capture parsing and literal npm launcher forwarding.
- Checksum generation with explicit file output must rebase relative inputs against the manifest directory, resolving symlink parents before rebasing; preserve absolute inputs and stdout filename behavior.
- Windows publication must retain temporary-file rename/cleanup access before copying permissions, replace read-only destinations without clearing their attributes, and leave originals unchanged on cancellation or failed publication. Apply temporary access attributes before the original DACL so denied attribute writes do not block an otherwise authorized replacement. Unsupported filesystem rename capabilities fail closed.
- Windows DACL fixtures must probe attribute-write permission with a fresh handle requesting exactly FILE_WRITE_ATTRIBUTES, including allowed controls and checks after cancellation and publication. Reapplying unchanged attributes is not an access-denial probe.

- clibox GNU release builds use the shared pinned AlmaLinux 9/glibc 2.34 boundary for npm and stable APT/DNF distribution; preserve the existing musl targets and optional user-installed desktop tools.

- The isolated Windows Ctrl+C test helper must explicitly clear inherited Ctrl+C-ignore state before spawning clibox, including under Git Bash release jobs. Keep this normalization confined to the test-owned process and include helper stdout/stderr on failure; production signal behavior is unchanged.

- Runlens static Linux targets retain caller LD_PRELOAD entries; remove only the tracer-owned library while preserving descendant loader behavior.

- Serialize all Runlens release versions under one publication concurrency group; dry runs must not own or cancel that lock.

- Every authenticated Runlens installer job must verify its declared platform and exact minimum OS on the executing native host before installation; mutable labels and newer-OS doctor success are insufficient evidence.

- Runlens must reject unrepresentable argv and masked report metadata before child launch; clean/repeat budget the full plan and imported baseline metadata before any preparation. Keep preflight and report metadata validation shared.

- Runlens Windows NtDeleteFile must record attempted writes before deletion, including failed calls and external paths, without changing native results.

- Runlens macOS readlink/readlinkat hooks record reads of the link itself, preserve buffers and native results, and never persist returned target text.

- Runlens secret masking must cover overlapping matches in original text without leaking suffixes or rematching replacement markers; environment iteration order cannot affect privacy.

- Runlens Windows doctor/preflight must verify native process and artifact architecture parity with IsWow64Process2, rejecting failed queries and emulation before tracing.

- Runlens Unix launch identity uses the resolved executable but passes the exact requested argv[0] to the child, including PATH names and relative aliases.

- Runlens conflict analysis must include the POSIX root directory as the final ancestor of absolute paths, without assigning that root to relative paths.

- Runlens macOS extended-attribute reads must record path/descriptor input attempts while preserving all Darwin operands and results; attribute names and values never enter reports.

- Linux fanotify_mark must retain watched path/descriptor read attempts for dynamic and static callers, preserve native results, and ignore pathname operands only for path-independent flush operations.

- macOS execvP must use the caller-supplied search path and preserve argv/native fallback behavior while recording executable reads and classifying protected descendants as incomplete.

- Unresolved Windows relative OBJECT_ATTRIBUTES root handles must mark collection incomplete before forwarding the original NT call; a failed normalized-name lookup cannot silently erase an access.

- Policy declaration checks classify masked command names as unknown before configuration lookup and continue independent global boundaries; masked names must never become invalid-input or bind a placeholder key.

- Clean/repeat inherit SystemRoot, WINDIR, COMSPEC and PATHEXT as OS execution context only on Windows; Unix commands and preparations require explicit selection of these names.

- Linux name_to_handle_at must retain absolute, relative and AT_EMPTY_PATH descriptor read attempts, including failed size probes, without recording opaque handles or mount IDs.

- macOS posix_spawn addopen actions must mark collection incomplete before they can run in the pre-injection child; preserve native action/spawn results and ordinary pipe-only child tracing.

- Clean/repeat copies preserve frozen source access/modification timestamps for files, directories and symlinks, restoring directory times after descendants. Copy-induced atime changes may be restored only in the owned frozen checkout, never in the original worktree.

- macOS syscall() calls must preserve the variadic ABI and mark collection incomplete before forwarding. Mark fork() incomplete before creating a child that can bypass symbol tracing with inline kernel operations; group emptiness must not certify that lifetime.

- A failed repetition-directory cleanup after observation must retain every completed execution and imported baseline, classify the current target CleanupFailed/incomplete, stop future rounds, and still allow explicit report saving.

- macOS getattrlist path/relative/descriptor reads must retain metadata input attempts without reading attribute buffers. Bulk attribute reads retain directory access and remain incomplete until individual child metadata identities are bound.

- macOS clonefile/clonefileat/fclonefileat must record source reads and destination writes, including failed attempts, without changing operands, copied bytes or native results.

- Unix pathname hooks must not form C strings from untrusted caller addresses. Copy bounded sub-page chunks with OS memory APIs before resolution; unreadable/unterminated operands mark incomplete while the original libc call receives unchanged pointers. Preserve caller errno during observation.

- Reserved .git snapshot pruning uses Windows ordinal case-insensitive component equality on Windows and exact component equality on Unix, before traversal and budget charging; similarly prefixed ordinary files remain observable.

- Linux mount/root/namespace changes and namespace-creating clone/clone3 calls must mark collection incomplete before forwarding, including denied attempts and new mount APIs. Parent snapshots cannot certify paths after remapping; ordinary thread clones remain supported.

- Native tests that invoke tracing library APIs directly must isolate runner streams and ambient descriptors in a subprocess; do not weaken production inherited-handle completeness rules to reach an injected test boundary.

- Windows source timestamp preservation must request FILE_WRITE_ATTRIBUTES without GENERIC_WRITE and retain no-follow/directory semantics; read-only Git objects must keep their content permissions through every frozen-source copy.

- Unix fstat must record descriptor metadata reads independently of open modes, including write-only descriptors and failed output-buffer calls. Anonymous kernel pipe/socket identities do not become filesystem paths; named filesystem objects remain observable.

- Linux open_by_handle_at attempts must mark collection incomplete before forwarding until the opaque handle can be bound to the accessed object and mode; earlier name_to_handle_at evidence cannot substitute for this open.

- Configured CLI commands apply the process-wide metadata memory limit immediately after config validation and before reading any supplied report or baseline; offline cache/policy checks share the same bound as execution.
