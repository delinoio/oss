# CacheCow complete requirements

## Scope

This contract preserves the complete CacheCow product requirements, acceptance criteria, and test scenarios. It governs `cmds/cachecow`, `packages/ccow`, and `apps/public-docs/docs/cachecow`. The sections after References retain the source requirements and decision context. Requirements describe the intended release, not completed implementation or native acceptance.

## Runtime and Language

Go CLI; native Cargo use requires no Node.js. The npm launcher and frontend integrations require Node.js 24+. See the exact support matrix below.

## Users and Operators

Developers, coding agents, CI jobs, and release maintainers using multiple Git worktrees.

## Interfaces and Contracts

The command, shim, shell, configuration, lifecycle, and output requirements below are authoritative. Domain summaries do not narrow these requirements.

## Storage

Local-only, current-user-private state and existing tool cache locations. This explicitly replaces the default remote object-storage approach. No central snapshot store or remote transfer.

## Security

The path, repository, user, process ownership, and sensitive-log boundaries below apply to all entry points.

## Logging

English diagnostics, structured Go logging, unchanged child streams, and no sensitive persisted diagnostic content, as specified below.

## Build and Test

The complete acceptance criteria and test scenarios below remain required. Fixtures, compilation, cross-builds, and package creation do not establish native filesystem, process-lifecycle, installation, or performance acceptance. Record exact revisions, commands, results, unperformed checks, and limits in PRs and CI artifacts. Preserve repository CI platform gates.

## Dependencies and Integrations

Git, Cargo, Rspack, Rsbuild, native CoW facilities, supported shells, npm, GitHub Releases, and the consolidated public documentation site.

## Change Triggers

Keep this full requirements contract, the project index, and affected domain contracts aligned. Follow the [instruction-update policy](README.md#instruction-update-policy). Record implementation progress and validation outside these requirements.

## References

- [Project index](project-cachecow.md)
- [Command foundation](cmds-cachecow-foundation.md)
- [Distribution](packages-ccow-distribution-contract.md)
- [Public documentation](apps-cachecow-docs-foundation.md)
- [Repository defaults](repository-defaults.md)
- [Repository workflow](repository-workflow-contract.md)

## Summary

Create **CacheCow**, a local CLI named **`ccow`**, to prepare independent build caches for Git worktrees using filesystem copy-on-write (CoW). Provide Cargo, Rspack, and Rsbuild presets. Default to cloning the primary checkout's existing cache; allow another worktree in the same repository as the source.

Provide optional PATH shims so existing `cargo`, `rspack`, and `rsbuild` commands can enter CacheCow after user activation, with shim installation and shell registration management.

Target developers, coding agents, and CI jobs that use multiple worktrees. Reduce redundant disk allocation and cache preparation work without changing the build tool's correctness decisions. Measure build time and memory effects rather than promising process-memory sharing or a fixed performance improvement.

This is the implementation PRD captured through explicit owner decisions. It does not report completed implementation or native acceptance. There is no fixed release deadline.

## Evidence

- The owner identified repeated build caches and resource use across agent worktrees, requested CacheCow with CLI name `ccow`, and selected the requirements below. No baseline savings measurement has been performed.
- Existing repository pnport contracts demonstrate tool-specific filesystem integration, but do not provide this Cargo/Rspack/Rsbuild worktree-cache feature.
- Cargo distinguishes target-dir and build-dir, and retains its own freshness checks: https://doc.rust-lang.org/cargo/reference/build-cache.html and https://doc.rust-lang.org/stable/nightly-rustc/cargo/compiler/fingerprint/index.html
- Rspack exposes persistent cache configuration: https://rspack.rs/config/cache
- OS mechanisms: Apple clonefile (https://github.com/apple-oss-distributions/xnu/blob/main/bsd/man/man2/clonefile.2), Linux FICLONE (https://man7.org/linux/man-pages/man2/FICLONE.2const.html), and ReFS block cloning (https://learn.microsoft.com/en-us/windows/win32/fileio/block-cloning).
- Version discovery during requirements collection: Rust's latest stable release was 1.99.1 (`gh api repos/rust-lang/rust/releases/latest`); npm latest was 2.3.0 for both `@rspack/core` and `@rsbuild/core`. These became owner-approved support floors, not claims about the earliest technically compatible versions.
- Duplicate search: `gh issue list --repo delinoio/oss --search 'CacheCow OR ccow' --state all` returned no matches.
- Repository authorities: `AGENTS.md`, `cmds/AGENTS.md`, `docs/README.md`, `docs/repository-defaults.md`, `docs/repository-environment-contract.md`, `docs/repository-workflow-contract.md`, and project/domain document templates.

## Current Gap

Requiring agents to prefix every build with `ccow run` adds an adoption burden. Optional PATH shims must allow unchanged tool command names while making interception limits explicit.

Each worktree can accumulate a separate, largely overlapping build cache. Sharing one mutable cache introduces coordination and isolation problems; ordinary copies duplicate storage. CoW can provide separate writable files while initially sharing physical storage, but requires tool-specific cache discovery, source selection, compatibility checks, locking, lifecycle handling, and recovery.

CoW is not a compiler cache key algorithm, a shared process heap, or automatic synchronization between diverged caches. Cloning a cache does not guarantee a build-tool cache hit.

## Proposed Scope

### Product and support matrix

- Product: CacheCow. CLI: `ccow`. Use the lowercase project identifier `cachecow` for project onboarding.
- Implement the CLI in Go under the repository's default-language rule; license repository-owned code and distributions under Apache-2.0.
- First-release presets: `cargo`, `rspack`, `rsbuild`.
- Native support and actual acceptance floors:
  - macOS 15+, arm64, APFS.
  - Linux x64 with Ubuntu 22.04-equivalent environment, Btrfs and reflink-enabled XFS.
  - Windows 11 24H2+, x64, ReFS.
- Tool support: Cargo 1.99.1 or later within 1.x; Rspack and Rsbuild 2.3.0 or later within 2.x. Older versions and nightly toolchains are outside the first-release support guarantee.
- npm package: `@delino/ccow`, exposing `ccow`. npm launcher and Rspack/Rsbuild integration require Node.js 24+. Native Cargo usage must not require Node.js.
- Other filesystems, or cross-volume operations where CoW cannot work, fail by default. Only an explicit `--allow-copy` permits ordinary copying. Report that ordinary copying was used.

### Commands and configuration

- `ccow run -- <command> [args...]`: prepare/reuse a worktree cache and execute the requested command.
- Automatically detect the preset; allow `--preset cargo|rspack|rsbuild`. Ambiguous detection is an actionable error.
- `ccow status`: inspect cache state.
- `ccow prepare`: prepare a cache without running a build, including explicit re-cloning.
- `ccow clean`: delete the selected cache without confirmation or `--yes`, in both interactive and noninteractive use. Refuse to delete an in-use cache.
- `ccow doctor`: diagnose configuration, environment, cache, and locking problems.
- `--source <worktree>`: select a source checkout/worktree in the same Git repository.
- `--allow-copy`: explicitly permit ordinary copying when CoW is unavailable.
- `--replace`: explicitly request replacement of an existing working cache; required to authorize replacement noninteractively.
- `ccow config get/set/unset`: manage per-worktree configuration. Support `config set source <worktree>` and `config unset source`.
- Precedence: per-invocation option, then saved per-worktree setting, then the primary checkout default.
- Retain underlying tool cache locations. Rspack and Rsbuild may require documented user configuration integration with CacheCow.
- Keep CacheCow settings, locks, and records in current-user-private local storage. New persisted entity identifiers follow the repository UUID v7 default where identifiers are needed.

### PATH shims and shell integration

- Provide `cargo`, `rspack`, and `rsbuild` shims on the already selected native platforms. After activation, developers and agents can use existing tool commands without adding `ccow run`.
- Retain `ccow run` as an explicit entry point. Shims use the same cache, source-selection, compatibility, interactive/noninteractive error, process-lifecycle, and diagnostic contracts.
- Support current-session and persistent activation for Bash, Zsh, and PowerShell 7.
- Provide `ccow shim install`, `ccow shim uninstall`, `ccow shim list`, and `ccow shim doctor`.
- Install into a current-user-private shim directory. Do not overwrite original tools. Reinstalling CacheCow-owned shims is idempotent; conflicting files owned by another tool cause an actionable error.
- Uninstall removes only CacheCow-owned shims. Preserve caches, settings, and shell registration blocks, and explain how to remove those blocks with `ccow shell remove`.
- `ccow shell init <shell>` emits activation code for the selected shell. Keep generated shell code usable as code, separate from diagnostic output.
- `ccow shell setup <shell>` and `ccow shell remove <shell>` add or remove only a CacheCow-owned block in the user's shell configuration. Preserve unrelated content, make repeated operations idempotent, and preserve the original file if an update fails.
- Show the target file and proposed change and obtain confirmation. Noninteractive setup/remove requires `--yes`. Support `--file` to select the configuration file; if automatic file selection is ambiguous, return an actionable error.
- Persistent setup/removal applies to new shells; explain how to apply the change to the current session.
- Resolve the real executable at each invocation by selecting the first executable in PATH after excluding CacheCow shims, rather than pinning a path during installation. Prevent self-recursion while preserving existing version-manager behavior, including rustup toolchain selection.
- If no real executable is found, return an actionable command-not-found error under the existing exit-code contract.
- Outside a Git repository, pass through to the real tool without cache preparation or locking. This is transparent dispatch, not support for managed caches outside Git.
- Within Git, pass version/help and cache-nonmutating query commands through without preparing or locking a cache. Manage recognized cache-using operations such as builds, checks, tests, and development servers.
- Pass unknown commands and custom Cargo subcommands through to the real tool. Explain that `ccow run` is the explicit managed entry point when these commands need cache management. Do not claim that unknown commands receive automatic protection.
- For shim-dispatched `cargo clean`, do not prepare or clone a cache. Acquire the relevant cache lock, wait if it is in use, and execute the original Cargo command with its arguments and deletion semantics intact. This differs intentionally from `ccow clean`, which refuses an in-use cache immediately.
- Nested calls in the same management session against the same cache reuse the existing lock and do not repeat preparation. A nested call that addresses a different cache is managed separately. Preserve deadlock prevention and process-tree ownership through nesting.
- Provide `CCOW_BYPASS=1` to pass through to the original tool without cache management. Concurrent bypassed and managed access to the same cache is unsupported.
- Preserve arguments, working directory, child streams, and exit codes. Never consume a tool's arguments as CacheCow-specific options; explicit CacheCow options remain available through `ccow run` and saved configuration.
- PATH shims do not guarantee interception of shell aliases/functions, absolute executable paths, package-manager-local executable resolution, or JavaScript API calls. First-release automatic npm/pnpm/yarn script and node_modules/.bin integration is excluded. Document use of `ccow run` and the existing Rspack/Rsbuild configuration integration for those cases.
- Extend shim diagnostics to show installation state, the actual selected tool path, and PATH precedence. Apply the existing English, structured-error, management-JSON, and sensitive-log exclusion rules; shell activation output remains shell code.
- Document shim installation, session activation, persistent setup/removal, uninstall, bypass, tool resolution, limitations, and recovery. No rollout flag is added: users explicitly install and activate shims.

### Cache lifecycle and source selection

- Default source: the repository's primary checkout. The user may select another checkout/worktree of the same repository.
- No separate central cache repository, automatic publication, or saved snapshot catalog.
- If a worktree already has a cache, keep using it. Do not replace it automatically or silently pull changes from the source.
- On first preparation without an existing cache, clone the source cache under coordination locks. Subsequent runs retain the worktree's own writable cache.
- Changes in a derived worktree do not propagate back to the source or other worktrees.
- Explicit replacement prepares a new copy before replacing the old cache. If preparation fails, preserve the old cache and remove incomplete staging.
- Interactive replacement requires confirmation. Noninteractive replacement requires explicit `--replace`.
- Failed or cancelled builds retain the working cache. The underlying build tool decides whether its partial results remain usable.
- Retention and deletion are manual only. Do not add age-based or capacity-based eviction. Cleanup of incomplete preparation is recovery, not eviction.
- When running in the default primary checkout itself, use its cache without self-cloning; if absent, build directly there.
- Prevent self-copy and recursive source-build flows.
- For worktrees of a bare repository without a primary checkout, explain how to select a source. Interactive execution offers source selection or a build without cache; noninteractive execution without a usable source follows the actionable-error rule.

### Missing, corrupt, or incompatible source

For `run`, interactive choices are:

1. Build in the current worktree without the source cache.
2. Select another source.
3. Build in the source first.
4. Cancel.

Before building in the source, show the exact requested command and source working directory, and obtain confirmation. Run the same requested command there; do not invent a separate preset build command.

Noninteractive execution must fail with concrete remediation instructions when it needs this decision. Do not silently build without cache. This final decision supersedes the earlier discussion of automatically continuing after an unusable cache.

For `prepare`, offer only another source or cancellation when no usable source exists. Explain that source building requires `ccow run`. Noninteractive `prepare` returns an actionable error.

Disk exhaustion, permission errors, and other actual preparation failures stop before launching the requested build, preserve the old cache, clean incomplete copies, and explain the cause and remedy. Do not treat these as an implicit authorization to copy normally or continue uncached.

### Preset boundaries and correctness

- Cargo: manage the configured target-dir and build-dir; process coincident paths once.
- Rspack and Rsbuild: manage configured persistent caches only.
- Exclude node_modules, package download caches, and frontend final output directories.
- In monorepos, identify the project from the current execution directory.
- Treat differing tool versions, platforms, architectures, or build settings as incompatible. Unknown cache locations or unverified compatibility also prohibit reuse.
- Dirty and untracked source changes are allowed; the tools must detect the required rebuilds.
- Preserve Cargo/rustc/Rspack/Rsbuild freshness decisions. Do not manipulate fingerprints or mtimes to force cache hits. If restored caches are invalidated, allow normal rebuilding.
- Tool-specific upstream improvements are separate follow-up work, not required patches in this first release.
- Do not claim that disk CoW shares compiler heaps or guarantees shared file page-cache memory.

### Concurrency and process lifecycle

- All builds involved in managed cache use must enter through `ccow run` or an activated CacheCow shim that manages that invocation. Concurrent unmanaged calls (including shim bypass and pass-through commands that mutate caches) against managed caches are unsupported and must be documented.
- Serialize concurrent operations on the same worktree/preset cache by waiting. Independent worktrees can build concurrently.
- Coordinate source reads with source builds to obtain a consistent copy.
- Resolve cache path aliases to apply the same lock, including when different projects or presets target the same cache.
- Avoid deadlocks when operations have overlapping sources and destinations or cross-select each other's caches.
- Support finite builds/checks/tests and long-running watch/development servers. Hold the cache lock for the process tree's lifetime; copies that need that source wait.
- Forward termination appropriately, allow five seconds for graceful shutdown, then terminate remaining owned descendants. Release locks only after the relevant process tree has exited.
- Abnormal CacheCow termination must not leave uncontrolled descendants writing into an unlocked cache. Include platform-specific ownership and recovery.
- Make waiting and cancellation diagnosable. Preserve working caches after failed/cancelled builds.

### Security and storage boundaries

- Local-only, current OS user only, same Git repository only. No cross-repository or cross-user sharing, remote storage, remote transfer, or telemetry.
- The local-only requirement is an explicit project-specific storage choice instead of the repository's default remote object-storage approach.
- Only copy, replace, or delete preset-verified cache paths.
- Refuse paths that overlap source storage, Git metadata, or CacheCow settings storage. Normal cache subdirectories within a worktree are allowed; never interpret the whole source tree as the cache.
- Resolve symlinks and aliases for validation and locking. Do not follow links into external content during copying or escape the selected cache during deletion.
- Preserve unrelated files and the source cache. Ordinary hard links are not a substitute for CoW isolation.
- CacheCow does not sandbox the user's build code. No service accounts, tenant service, billing, or network authorization surface is introduced.

### Output, errors, and observability

- English CLI output. Pass child stdout/stderr through unchanged; CacheCow notices go to stderr.
- Management commands support `--json` with a schema version, success indication, and either result data or an error code, cause, and remediation.
- Preserve the child command's exit code. CacheCow-owned exit codes: 2 for arguments/configuration, 125 for cache/environment/internal failures, 126 for command execution denied/unavailable, and 127 for command not found.
- Text and JSON errors must explain actionable fixes, especially noninteractive decisions, CoW capability, permissions, source selection, and lock contention.
- `doctor` and `--verbose` expose paths, tool versions, CoW capability, lock state, and failure causes.
- Follow repository structured logging requirements (Go `log/slog` or compatible) and ANSI output with a documented opt-out.
- Do not persist original file contents, environment variable values, or full command arguments in diagnostic logs. Displaying the exact command for an interactive source-build confirmation does not authorize storing it in logs.
- No remote metrics, dashboards, alerting service, or telemetry. Local diagnostics and release benchmark artifacts provide observability.

### Distribution, rollback, documentation, and support

- Publish versioned native archives and checksums through GitHub Releases and installation through `@delino/ccow`.
- No feature flag: users opt in through explicit CLI invocation or shim installation and activation. No rollout cohorts or percentages.
- Roll back by reinstalling a previous version. Do not rewrite settings or records that the running version cannot understand; report recovery guidance.
- Exclude automatic updates, standalone installer scripts, and Homebrew from the first release.
- Support via GitHub Issues with no response-time SLA.
- Before runtime implementation, onboard the project with a project index and owning domain contracts under `docs/`, update the catalog, and establish canonical implementation/package paths. Use the project/domain templates. Update AGENTS only for ownership or development-rule changes.
- Add a CacheCow section to `apps/public-docs` covering installation, preset configuration, source selection, concurrency, cleaning, troubleshooting, and performance limitations. Keep internal contracts and implementation details in `docs/`.
- Keep source revision, test commands, results, unperformed checks, and unresolved constraints in PRs and CI artifacts.
- Preserve repository CI rules, including no macOS runners on PRs, main pushes, or affected manual validation. Actual macOS acceptance belongs in the appropriate release-platform gate.

## Acceptance Criteria

- [ ] Activated PATH shims dispatch recognized Cargo/Rspack/Rsbuild cache operations through the same managed lifecycle as explicit execution, without requiring an agent to change the tool command name.
- [ ] Shim installation/removal preserves original tools, unrelated files, caches, and settings; conflicts fail with remediation.
- [ ] Bash, Zsh, and PowerShell 7 support both session activation and persistent owned-block setup/removal, with confirmation, noninteractive authorization, explicit file selection, idempotency, and failure recovery.
- [ ] Per-invocation executable discovery preserves PATH/version-manager selection without recursion; missing tools produce an actionable error.
- [ ] Queries, unknown/custom commands, non-Git calls, and explicit bypass follow their pass-through rules. Managed cargo clean waits for a lock without cache preparation.
- [ ] Nested same-session/same-cache invocations reuse locks, different-cache nesting remains managed, and neither path deadlocks or releases protection prematurely.
- [ ] Shim diagnostics and guides explain real executable selection, PATH priority, bypass, uninstall, and all unsupported interception paths.

- [ ] CacheCow and `ccow` are onboarded with authoritative contracts, Go implementation, Apache-2.0 licensing, native packages, and `@delino/ccow`.
- [ ] Every supported preset can identify and prepare its allowed cache paths without changing unrelated source, dependencies, Git metadata, or outputs.
- [ ] Initial worktree preparation uses the primary checkout or explicit same-repository source; repeat runs preserve the local cache unless replacement is explicitly requested.
- [ ] Real CoW is demonstrated on APFS, Btrfs, reflink-enabled XFS, and ReFS. Writing or cleaning a derived cache does not modify the source or another worktree.
- [ ] CoW-unavailable operations fail unless ordinary copy is explicitly allowed and reported.
- [ ] Compatibility uncertainty, corruption, and missing sources follow the exact interactive/noninteractive rules. Tool rebuild checks remain intact.
- [ ] Replacement and preparation failures preserve existing caches and clean incomplete state.
- [ ] Operations serialize on identical actual cache paths, independent worktrees run concurrently, and overlapping source/destination requests do not deadlock.
- [ ] Watch servers, cancellation, and abnormal parent termination preserve process-tree ownership and cache locking.
- [ ] `clean` deletes only the selected verified cache without confirmation and rejects an in-use cache.
- [ ] Settings precedence, English output, JSON, exit codes, stderr separation, diagnostics, and sensitive-log exclusions match the contract.
- [ ] The exact minimum supported tool/OS environments pass native execution and packaged installation acceptance; mocks, compilation, or cross-builds alone do not satisfy these gates.
- [ ] Publish measured physical storage use, cache preparation time, build time, and memory use, with source/environment details and limitations. No fixed reduction percentage is required.
- [ ] Documentation, rollback guidance, and GitHub issue support are available. No fixed deadline or response SLA applies.

## Test Scenarios

- Exercise installed cargo/rspack/rsbuild shims on all selected native platforms; verify both ordinary tool commands and explicit ccow run.
- Verify Bash/Zsh and PowerShell 7 in their supported platform environments, session activation, new-shell persistent activation/removal, current-session instructions, --file, ambiguous configuration selection, confirmation, noninteractive --yes, repeated setup/remove, and failed-write recovery without damage to unrelated configuration.
- Verify shim installation conflicts, idempotent reinstall, uninstall ownership checks, retained caches/settings/shell blocks, and actionable shell-remove guidance.
- Verify PATH ordering changes between invocations, rustup toolchain selection (including explicit toolchain arguments), executable aliases, missing real tools, and exclusion of CacheCow shims from recursive lookup.
- Verify exact argument, working-directory, stdout/stderr, and exit-code preservation; query/version/help passthrough must not prompt for a cache or wait on its lock.
- Verify non-Git passthrough, unknown/custom Cargo commands, CCOW_BYPASS=1, and documented limits for absolute paths, shell aliases/functions, package-manager-local binaries, and JS API calls.
- Verify cargo clean waits for the relevant lock without preparing a cache and preserves Cargo argument semantics; separately verify ccow clean still rejects in-use caches.
- Verify ccow run entering a shim, build-script nested Cargo, repeated same-cache nesting, different-cache nesting, cross-cache deadlock prevention, cancellation, abrupt parent exit, and descendant ownership.
- Verify shim list/doctor and management JSON, selected executable/PATH diagnostics, shell-code output separation, and no sensitive log regression.

- On every minimum supported native OS/architecture, exercise actual Cargo, Rspack, and Rsbuild commands using the minimum supported tool versions.
- Validate CoW on both supported Linux filesystems, APFS, and ReFS; verify physical sharing/isolation rather than relying solely on apparent file sizes or a successful copy API.
- Compare cold preparation, warm original builds, cloned-worktree builds, and subsequent changed-worktree builds. Measure physical storage, preparation time, build time, and memory without claiming unmeasured savings.
- Exercise primary-checkout defaults, explicit source, saved source, CLI override, unset configuration, missing/deleted source, bare repositories, self-selection, and non-Git directories.
- Exercise existing cache preservation, initial preparation, explicit replacement, and no automatic propagation/publication after a derived build.
- Verify all interactive source-failure choices; confirm the command/location before a source build. Verify noninteractive remediation and `prepare` without a build command.
- Verify version/platform/architecture/settings mismatches, unknown compatibility, malformed configuration, corrupted cache, and normal rebuild after restoration.
- Modify tracked files and add untracked files; compare results with a clean build to detect stale reuse.
- Test Cargo target/build directory overlap and separation, Rspack/Rsbuild persistent-cache integration, monorepo project selection, and shared cache locations across presets.
- Test finite commands, watch/development servers, independent parallel worktrees, same-cache waiting, cross-selected sources, lock cancellation, clean during active use, and deadlock prevention.
- Test graceful termination, five-second escalation, surviving descendants, abrupt parent termination, restart/recovery, and retained partial build caches.
- Inject disk-full, permission, partial-copy, and replacement failures. Verify old-cache preservation, no premature build launch, and cleanup.
- Verify unsupported filesystem/cross-volume errors, explicit ordinary-copy fallback, and accurate user-visible copy-mode reporting.
- Verify spaces, Korean/non-ASCII paths, symlinks, aliases, external links, protected-path overlap, and same-user/same-repository boundaries.
- Verify JSON success/errors, exit-code propagation, unchanged child output, stderr notices, logs without sensitive content, and unsupported record-version handling.
- Install the native archives and npm package on each supported platform. Verify native Cargo use without Node.js, Node.js 24+ npm/frontend use, and reinstall-based rollback.
- Preserve source revisions, commands, results, host/filesystem details, and unperformed checks in PR/CI records. Keep runtime acceptance distinct from fixtures and packaging.

## Out of Scope

- Fish and cmd.exe-specific shell activation, automatic npm/pnpm/yarn script or node_modules/.bin integration, and guaranteed interception of shell aliases/functions, absolute paths, or JS API calls.
- A separate central cache store, automatic cache publication, saved snapshot catalogs, and snapshot save/list/restore/delete commands.
- Automatic eviction, automatic updates, standalone installers, and Homebrew.
- Cross-repository/cross-user sharing, non-Git projects, remote cache transfer/storage, and telemetry.
- Shared process heaps, guaranteed RAM savings, guaranteed build cache hits, or a fixed performance reduction threshold.
- node_modules, package download caches, and frontend final outputs.
- Tool freshness bypasses and first-release upstream tool patches.
- Concurrent unmanaged builds against managed caches and sandboxing of arbitrary build code.
- Additional presets, CPU architectures, unsupported older tool versions, nightly support guarantees, and unselected OS/filesystem guarantees.
- A web/mobile UI, hosted service, billing system, rollout flag, or support response SLA.

## Additional Notes

Decision ledger closure:

The shim extension was decided by the owner after the original PRD: three PATH shims; Bash/Zsh/PowerShell 7 session and persistent activation; owned-file installation/removal; confirmed shell block updates; dynamic real-tool lookup; explicit passthrough/bypass rules; nested lock reuse; lock-aware cargo clean; and documentation/native verification. Existing cache, platform, privacy, distribution, and release requirements remain in force.

| Category                      | Status                         | Basis                                                                                                                         |
| ----------------------------- | ------------------------------ | ----------------------------------------------------------------------------------------------------------------------------- |
| Product intent and evidence   | Answered                       | Owner's multi-worktree problem; observable isolation, recovery, and measured results; no deadline or fixed performance target |
| Scope and preserved behavior  | Answered                       | Explicit presets, commands, cache boundaries, source semantics, and exclusions above                                          |
| CLI experience and interfaces | Answered                       | Owner-approved command/options, prompts, English output, JSON, exit codes, and settings precedence                            |
| Data and state                | Answered                       | Existing tool cache locations, worktree retention, current-user records, manual cleanup, no central snapshots                 |
| Security and privacy          | Answered                       | Same-user/same-repository local boundary, path protection, sensitive-log exclusions, no sandbox or telemetry                  |
| Integrations                  | Answered                       | Cargo/Rspack/Rsbuild, Git, OS CoW, npm/native distribution, selected versions and fallback rules                              |
| Feature flag and rollout      | Not applicable                 | Owner explicitly selected no flag; use is opt-in through CLI invocation or shim installation and activation                   |
| Operations and rollback       | Answered                       | Process-tree ownership, five-second shutdown, manual version rollback, no automatic updater or support SLA                    |
| Observability                 | Answered / contract-determined | Approved doctor/verbose and local errors; structured logs and ANSI opt-out from repository rules                              |
| Failure modes                 | Answered                       | Source errors, preparation failure, cancellation, concurrent use, and replacement contracts above                             |
| Documentation and support     | Answered / contract-determined | Approved public guides and GitHub Issues; documentation-first repository onboarding                                           |
| Testing and acceptance        | Answered                       | Owner approved all native, failure, isolation, installation, and measurement gates                                            |
| Language/license/identifiers  | Contract-determined            | Go, Apache-2.0, default UUID v7 for new persisted entities under repository rules                                             |
| Filing                        | Contract-determined            | Current remote is delinoio/oss; repository issue style; no duplicate found; PRD label absent, so no labels added              |

These are requirements, not evidence that the implementations or native tests have passed.
