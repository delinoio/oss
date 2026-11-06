# Concurrency and processes

> CacheCow 0.1.0 is unreleased. This guide describes intended workflows, not published artifacts or verified native support.

## Enter through managed execution

All builds that participate in managed cache use must enter through `ccow run` or an activated shim that manages that invocation. Concurrent unmanaged writers are unsupported. This includes bypassed commands and pass-through commands that mutate a managed cache.

Independent worktrees can build concurrently when their actual cache paths are independent. Operations on the same worktree/preset cache wait for one another. Symlinks, aliases, and shared cache locations across projects or presets must resolve to the same protection.

CacheCow must coordinate source reads with source builds so copies are consistent. If a source is busy, preparation waits. Overlapping source/destination requests and worktrees that select each other's caches must not deadlock. Use `ccow doctor` or `--verbose` to inspect waiting and lock state rather than deleting locks.

Creating or deleting files in any worktree while a command waits, or while another worktree prepares its cache, does not fail the waiting command: it repeats its discovery automatically, at most five times. A change to the Cargo, Rspack or Rsbuild cache configuration while a command waits still fails the command with exit code 125; run the command again.

## Watch servers and nested commands

Finite builds, checks, tests, and long-running watch/development servers use the same lifecycle. A cache remains locked for the process tree's lifetime. A watch server used as a source can therefore block copying until it exits.

Nested invocations in the same management session and against the same cache reuse the lock without repeating preparation. Different-cache nesting remains managed separately. This applies when explicit `ccow run` enters a shim or a build script invokes Cargo. Nested execution must not release outer protection early. A nested command that fails before it starts, for example on an unusable source or a failed preparation, does not keep its source or new cache locked for the rest of the outer session; the outer session keeps only its own protection. Cancelling a nested command cancels the whole managed session, including its outer build and other descendants. Protection remains active throughout graceful shutdown and forced termination.

## Cancellation and recovery

On termination, CacheCow must forward termination appropriately, allow five seconds for graceful shutdown, then terminate remaining owned descendants. It must release locks only after the relevant process tree exits. Failed or cancelled builds retain the working cache.

Abnormal CacheCow termination must not leave descendants writing into an unlocked cache. Native platform process ownership and recovery are acceptance requirements; these guides do not claim that they have passed on any platform.

CacheCow releases a lease left by a supervisor that died only when the platform proves that every owned process has exited:

- After a reboot, leases recover automatically on Linux and macOS. A waiting command recovers them before it waits, so you do not need to run `ccow doctor` first.
- On Windows, the supervisor's job object terminates every owned process when the supervisor exits, so a lease whose recorded supervisor no longer runs recovers automatically.
- On Linux, if the supervisor is killed without a reboot, the lease stays protected because orphaned build processes can keep writing. It recovers after those processes exit and the machine reboots.
- Leases recorded by an earlier CacheCow version carry no native identity and cannot recover automatically. Stop any remaining build processes and reboot; if `ccow doctor` still keeps the lease, report it.

When a command must wait for a lease that cannot be recovered, it prints one notice with the cache path and the blocking journal directory. `ccow doctor` lists the leases it recovered and the reason it keeps each remaining lease: `owner running`, `same boot; descendants may survive`, or `incomplete journal without native identity (version 1)`.

If a run ends unexpectedly:

1. Use the OS process tools to inspect whether its build descendants remain active.
2. Run `ccow doctor` to inspect cache and lock state.
3. Stop remaining build activity before attempting recovery or deletion.
4. Follow the reported remediation, then retry the managed command.

Do not force-delete protection records to make an active writer appear idle. Do not start an unmanaged build against a cache whose ownership is uncertain. If diagnostics cannot establish a safe recovery, preserve the cache and report the failure through [GitHub Issues](https://github.com/delinoio/oss/issues).

## Cleaning during use

`ccow clean` refuses active use immediately. Shim-dispatched `cargo clean` waits for the relevant lock and does not prepare a cache. A long-running source or destination process may explain that wait. Cancelling a waiting operation must be diagnosable and must not compromise the active process's protection.
