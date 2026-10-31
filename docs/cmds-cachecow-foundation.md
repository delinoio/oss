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

Rspack and Rsbuild projects provide `.ccow/discover.cjs` in the exact execution directory. The CommonJS module exports an async function receiving `{ preset, argv, cwd, env }`. It resolves the same configuration as the requested build and returns `{ schema: 1, preset, caches: [{ type: "persistent", path: absolutePath }], outputs: [absoluteOutputPath], settings: resolvedSettings }`. It lists every persistent cache and final output. The hook must not build, write artifacts, or print to stdout. Unknown options or unresolved dynamic configuration fail discovery. Project hooks are trusted code under the same boundary as the build.

Discovery verifies installed tool versions and excludes dependency trees, package download stores, and final outputs. Compatibility incorporates resolved settings, tool/compiler versions, platform, architecture, and relevant invocation environment. Discovery does not rewrite tool fingerprints or timestamps to manufacture cache hits.

### Cargo discovery

Cargo metadata resolves the workspace and configured `target_directory` and `build_directory`. Missing directory fields, unresolved configuration includes, and unverifiable custom compiler wrappers prohibit reuse with actionable errors. Distinct roots are managed together; coincident roots are processed once. Source and destination must retain compatible build settings while Cargo remains responsible for source freshness.

### Coordination and recovery

Acquire the complete source and destination resource set before preparation. The initial coordinator uses exclusive leases for both sources and destinations; independent resource sets can proceed concurrently. Conflicts include aliases and ancestor/descendant paths. A nested request borrows existing protection only through authenticated session ownership. A nested extension that conflicts with another session fails with remediation instead of retaining a partial acquisition and waiting in a possible cycle.

Persist ownership intent before permitting child execution. Release protection only after positive whole-process-tree completion evidence; a dead PID or released operating-system lock is insufficient. An unfinished ownership record remains protected until native recovery can prove completion. Unknown record versions are read-only. Supervisor recovery must never infer completion from elapsed time.

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
