# Concurrency and processes

> CacheCow 0.1.0 is unreleased. This guide describes intended workflows, not published artifacts or verified native support.

## Enter through managed execution

All builds that participate in managed cache use must enter through `ccow run` or an activated shim that manages that invocation. Concurrent unmanaged writers are unsupported. This includes bypassed commands and pass-through commands that mutate a managed cache.

Independent worktrees can build concurrently when their actual cache paths are independent. Operations on the same worktree/preset cache wait for one another. Symlinks, aliases, and shared cache locations across projects or presets must resolve to the same protection.

CacheCow must coordinate source reads with source builds so copies are consistent. If a source is busy, preparation waits. Overlapping source/destination requests and worktrees that select each other's caches must not deadlock. Use `ccow doctor` or `--verbose` to inspect waiting and lock state rather than deleting locks.

## Watch servers and nested commands

Finite builds, checks, tests, and long-running watch/development servers use the same lifecycle. A cache remains locked for the process tree's lifetime. A watch server used as a source can therefore block copying until it exits.

Nested invocations in the same management session and against the same cache reuse the lock without repeating preparation. Different-cache nesting remains managed separately. This applies when explicit `ccow run` enters a shim or a build script invokes Cargo. Nested execution must not release outer protection early. Cancelling a nested command cancels the whole managed session, including its outer build and other descendants. Protection remains active throughout graceful shutdown and forced termination.

## Cancellation and recovery

On termination, CacheCow must forward termination appropriately, allow five seconds for graceful shutdown, then terminate remaining owned descendants. It must release locks only after the relevant process tree exits. Failed or cancelled builds retain the working cache.

Abnormal CacheCow termination must not leave descendants writing into an unlocked cache. Native platform process ownership and recovery are acceptance requirements; these guides do not claim that they have passed on any platform.

If a run ends unexpectedly:

1. Use the OS process tools to inspect whether its build descendants remain active.
2. Run `ccow doctor` to inspect cache and lock state.
3. Stop remaining build activity before attempting recovery or deletion.
4. Follow the reported remediation, then retry the managed command.

Do not force-delete protection records to make an active writer appear idle. Do not start an unmanaged build against a cache whose ownership is uncertain. If diagnostics cannot establish a safe recovery, preserve the cache and report the failure through [GitHub Issues](https://github.com/delinoio/oss/issues).

## Cleaning during use

`ccow clean` refuses active use immediately. Shim-dispatched `cargo clean` waits for the relevant lock and does not prepare a cache. A long-running source or destination process may explain that wait. Cancelling a waiting operation must be diagnosable and must not compromise the active process's protection.
