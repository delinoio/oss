# CacheCow command foundation

## Scope

Own `cmds/cachecow`: the planned `ccow` CLI, preset discovery, CoW preparation, cache lifecycle, locking, process ownership, shims, and shell integration. The [complete requirements](cmds-cachecow-requirements.md) govern every behavior and acceptance gate.

## Runtime and Language

Go under the repository default. Use the repository Go toolchain. Native Cargo operation must not depend on Node.js. Supported platforms and tool versions are exactly those in the complete requirements.

## Users and Operators

Developers, coding agents, and CI jobs working in Git worktrees; maintainers diagnosing local failures.

## Interfaces and Contracts

Public commands are `run`, `status`, `prepare`, `clean`, `doctor`, `config get/set/unset`, `shim install/uninstall/list/doctor`, and `shell init/setup/remove`. Preserve all options, prompts, output, exit-code, and pass-through rules in the complete requirements.

Implementation boundaries:

- Preset discovery consumes the current working directory, requested command/arguments, tool selection, and optional preset. It identifies verified cache paths and compatibility information without changing tool freshness decisions. Unknown locations or compatibility prohibit reuse.
- Cache preparation consumes the preset result, source selection, and explicit copy/replacement authorization. It retains existing caches by default and reports the actual copy mode. Failed preparation preserves the old cache and prevents build launch.
- Managed execution coordinates canonical cache paths and source reads, owns the process tree, and returns the child exit status. Same-session/same-cache nesting reuses protection; different-cache nesting remains managed without deadlocks.
- Shim dispatch resolves the real tool for each invocation, classifies managed operations versus pass-through, and enters that same lifecycle. Managed `cargo clean` locks without preparation; `ccow clean` rejects in-use caches.
- Management output supports a versioned JSON envelope with success and result or error code, cause, and remediation. Shell initialization emits shell code separately from diagnostics.

### Frontend discovery interface

Rspack and Rsbuild projects provide `.ccow/discover.cjs` in the exact execution directory. The CommonJS module exports an async function receiving `{ preset, argv, cwd, env }`. It resolves the same configuration as the requested build and returns `{ schema: 1, preset, caches: [{ type: "persistent", path: absolutePath }], outputs: [absoluteOutputPath], inputs: [absoluteConfigPath], settings: resolvedSettings }`. It lists every persistent cache and final output. `inputs` lists all configuration files, imported dependencies, and absent configuration candidates whose creation can change cache selection. The list must be nonempty. Discovery obtains this list, captures file digests, repeats the resolver, and rejects changed results or files. The supervisor rechecks these digests after acquiring the complete lease set. Store only paths and digests, not file contents. The hook must not build, write artifacts, or print to stdout. Unknown options or unresolved dynamic configuration fail discovery. Project hooks are trusted code under the same boundary as the build.

Discovery verifies tool versions from the package that owns the selected executable, including its resolved Rspack dependency. The executable must be the package manifest's bin entry or a symlink to it. Unverifiable shell/batch wrappers fail discovery; an unrelated installation in the execution directory cannot certify them. Discovery verifies installed tool versions and excludes dependency trees, package download stores, and final outputs. Compatibility incorporates resolved settings, tool/compiler versions, platform, architecture, and relevant invocation environment. Discovery does not rewrite tool fingerprints or timestamps to manufacture cache hits.

### Cargo discovery

Cargo metadata resolves the workspace and configured `target_directory` and `build_directory`. Missing directory fields, unresolved configuration includes, and unverifiable custom compiler wrappers prohibit reuse with actionable errors. Distinct roots are managed together; coincident roots are processed once. Discovery queries the exact resolved command, including explicit executable paths, rather than substituting PATH Cargo. Source and destination must retain compatible build settings while Cargo remains responsible for source freshness. Source discovery translates the current worktree root to the source worktree root in the environment, argv, and `PATH` first (whole-path matches only), so values such as `DIRENV_DIR`, `VIRTUAL_ENV`, `--target-dir`, `CARGO_TARGET_DIR`, and `--manifest-path` that name the current worktree do not make the source incompatible or report cache roots inside the destination. Before metadata discovery, capture file digests for Cargo configuration candidates (including absent files), ancestor manifests, explicit configuration and manifest arguments, and toolchain selectors. Recheck those inputs after discovery and under the complete lease set before launch; a change requires fresh discovery and must not run with stale cache claims. Store only paths and digests, not file contents.

### Coordination and recovery

Acquire the complete source and destination resource set before preparation. Inspect mutable source contents and provenance only after acquiring that complete set, so an active source build can finish before compatibility is checked. An unusable source after acquisition retains the existing interactive recovery choices. The initial coordinator uses exclusive leases for both sources and destinations; independent resource sets can proceed concurrently. Conflicts include aliases and ancestor/descendant paths. A nested request borrows existing protection only through authenticated session ownership. A nested extension that conflicts with another session fails with remediation instead of retaining a partial acquisition and waiting in a possible cycle. A nested extension that is admitted and then fails before any command starts releases exactly the resources it newly acquired, including its sources and new destinations, before it reports the failure; resources borrowed from the parent stay held. A retry therefore repeats full source validation and preparation instead of borrowing an unprepared destination. Initial-request failures are unchanged because they finish the whole session.

Registry lock contention is a separate condition from cache use. Every registry transaction (open, record listing, reconcile, acquire, nested acquire, prepared-source and unstarted-grant release) retries SQLite BUSY/LOCKED by rolling back the whole attempt and re-running it, with a 25 ms interval, until a one-second budget ends or the caller's context is done (the context error is returned). Because a transaction body can run more than once, per-attempt state, such as the borrowed flag, added paths, and the next record of `Acquire`, is derived inside the transaction closure so each new path is recorded and reported in `Grant.NewPaths` exactly once. When the budget ends the exported `lease.ErrRegistryBusy` is returned. It is distinct from `lease.ErrBusy` and `lease.ErrNestedConflict`, which mean a real resource overlap found while holding the registry lock and are never retried by the transaction: `Acquire(wait=false)` returns `ErrBusy` and nested `Acquire` returns `ErrNestedConflict` immediately for a real overlap, and returns `ErrRegistryBusy` when the overlap could not be evaluated. `Acquire(wait=true)`, `OpenWait`, and supervisor session completion keep waiting through both registry contention and overlap until the context ends. The supervisor result preserves `ErrRegistryBusy` across the result file and IPC boundary. `status`, `doctor`, and `clean` therefore succeed on an idle cache under brief contention and otherwise exit 125 with a registry-lock cause and a retry remediation (in text and `--json`), never a cache-in-use message.

Persist ownership intent before permitting child execution. Release protection only after positive whole-process-tree completion evidence; a dead PID or released operating-system lock is insufficient. An unfinished ownership record remains protected until native recovery can prove completion. Unknown record versions are read-only. Supervisor recovery must never infer completion from elapsed time. The process journal (`scope.json`) is version 2 for every journal a supervisor writes; version 1 journals still load unchanged and, if incomplete, stay protected because they carry no native identity. Journals of any other version are rejected, and older binaries reject version 2 and fail closed. The supervisor records native identity in the journal before `Owned` can acquire any lease, and does not start if it cannot read that identity (`ErrUnavailable`). Per-platform completion evidence for an incomplete version 2 journal, exposed through `process.Recover`/`process.Reconcile` (`ccow doctor` is the caller): Linux records `/proc/sys/kernel/random/boot_id` as `boot_id`; macOS records `sysctl kern.bootsessionuuid` as `boot_id`; in both cases a different current boot proves every process of the recorded boot has exited. Windows records the supervisor creation time as `owner_created` (FILETIME) next to `owner_pid`; the supervisor holds the only handle of its unnamed, non-inheritable job object created with `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`, so a PID and creation time that no longer name a running process (absent, reused by a process with another creation time, or exited) prove the whole job ended, with or without a reboot. Windows has no documented user-mode boot identity and uses none. When evidence holds, `Reconcile` writes `complete: true` and `exit_code: 125` and `ccow doctor` releases the lease. In the same boot on Linux, a killed subreaper leaves orphaned descendants running, so the journal stays protected until the next boot; on macOS the existing zero-member coalition check still applies in the same boot. An empty identity, a version 1 journal, a failed identity read, a Windows probe error such as access denied, and a missing creation time never prove completion. `ccow doctor` reports a typed reason for every kept lease in `keptLeases` and lists released leases in `recoveredLeases`; reasons are the stable `process.Recovery` identifiers `journal-complete`, `native-completion`, `owner-running`, `incomplete-journal-without-native-identity`, `same-boot-descendants-may-survive`, `identity-check-failed`, and `journal-unreadable`. A cancelled nested command requests termination from the authenticated outer native owner. This cancels the whole session with the same five-second graceful interval and retains outer protection until every owned descendant exits. Repository snapshots exclude contents of wholly ignored directories without tracked descendants; index, ignore-rule and directory-type changes still invalidate discovery.

## Storage

Keep caches in tool-selected locations; keep settings, locks, and records in current-user-private local storage. Source precedence is invocation, saved per-worktree setting, then primary checkout. No remote storage, central cache, automatic publication, snapshots, or eviction. Unknown record versions must not be rewritten.

## Security

Validate same-user/same-repository ownership and resolved paths before mutation. Protect sources, Git metadata, settings, and unrelated files. Do not follow external links during copies or escape the cache during deletion. Ordinary hard links cannot provide CoW isolation. Build code is not sandboxed. Process ownership must survive abnormal parent termination without unlocked writers.

## Logging

Use `log/slog` or compatible structured logging. English notices go to stderr; child streams remain unchanged. Support ANSI output with a documented opt-out. Doctor/verbose expose safe paths, versions, capability, lock state, and failure causes. Never persist source contents, environment values, or full command arguments.

## Build and Test

Once Go runtime code exists, run relevant `go test` coverage under the repository workflow. The full requirements enumerate failure, concurrency, nesting, shell, filesystem, and native acceptance tests. Preserve the five-second graceful shutdown interval and descendant termination tests. Physical sharing/isolation and minimum-version native execution require actual supported hosts; mocks and cross-builds are insufficient. Do not add macOS runners to PR, main-push, or affected manual CI.

## Dependencies and Integrations

Git worktree discovery; Cargo target-dir/build-dir; Rspack/Rsbuild persistent caches; OS CoW and process ownership facilities; Bash, Zsh, PowerShell 7. Distribution consumes the native CLI; public guides describe supported user workflows.

## Change Triggers

Update the project index, complete requirements, affected consumer contracts, and public guides together when interfaces change. Follow the [instruction-update policy](README.md#instruction-update-policy). Keep new validation records in PRs and CI artifacts.

## References

- [Project index](project-cachecow.md)
- [Complete requirements](cmds-cachecow-requirements.md)
- [Repository defaults](repository-defaults.md)
- [Workflow](repository-workflow-contract.md)
