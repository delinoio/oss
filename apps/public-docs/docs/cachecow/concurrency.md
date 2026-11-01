# Concurrency and processes

> CacheCow 0.1.0 is unreleased. This guide describes intended workflows, not published artifacts or verified native support.

## Enter through managed execution

All builds that participate in managed cache use must enter through `ccow run` or an activated shim that manages that invocation. Concurrent unmanaged writers are unsupported. This includes bypassed commands and pass-through commands that mutate a managed cache.

Independent worktrees can build concurrently when their actual cache paths are independent. Operations on the same worktree/preset cache wait for one another. Symlinks, aliases, and shared cache locations across projects or presets must resolve to the same protection.

CacheCow must coordinate source reads with source builds so copies are consistent. If a source is busy, preparation waits. Overlapping source/destination requests and worktrees that select each other's caches must not deadlock. Use `ccow doctor` or `--verbose` to inspect waiting and lock state rather than deleting locks.

Short-lived contention on the shared lease registry is different from cache use. CacheCow retries it for up to one second per registry operation, and waiting commands keep waiting. If another process holds the registry longer, a nonblocking command exits 125 with a lease-registry cause and a retry remediation, and does not report the cache as in use.

## Watch servers and nested commands

Finite builds, checks, tests, and long-running watch/development servers use the same lifecycle. A cache remains locked for the process tree's lifetime. A watch server used as a source can therefore block copying until it exits.

Nested invocations in the same management session and against the same cache reuse the lock without repeating preparation. Different-cache nesting remains managed separately. This applies when explicit `ccow run` enters a shim or a build script invokes Cargo. A nested command that fails before it starts, for example when its source is unusable or the copy fails, does not keep its source or new cache locked for the rest of the outer session; retrying it repeats validation and preparation. Nested execution must not release outer protection early. Cancelling a nested command cancels the whole managed session, including its outer build and other descendants. Protection remains active throughout graceful shutdown and forced termination.

## Cancellation and recovery

On termination, CacheCow must forward termination appropriately, allow five seconds for graceful shutdown, then terminate remaining owned descendants. It must release locks only after the relevant process tree exits. Failed or cancelled builds retain the working cache.

Abnormal CacheCow termination must not leave descendants writing into an unlocked cache. Native platform process ownership and recovery are acceptance requirements; these guides do not claim that they have passed on any platform.

If a run ends unexpectedly:

1. Use the OS process tools to inspect whether its build descendants remain active.
2. Run `ccow doctor` to inspect cache and lock state.
3. Stop remaining build activity before attempting recovery or deletion.
4. Follow the reported remediation, then retry the managed command.

### What `ccow doctor` can recover

A build can lose its CacheCow supervisor before the supervisor records that the build finished, for example after a reboot, power loss, or a `SIGKILL` or out-of-memory kill of the supervisor. CacheCow releases the lease for such a build only when the operating system gives positive proof that the whole process tree has exited. `ccow doctor` applies that check to every lease. A waiting managed command and `ccow clean` apply it to the leases that block them, so after a reboot, or when a killed supervisor leaves a provably finished build, you do not need to run `ccow doctor` first. It never releases a lease because a process ID is missing or because time has passed.

| Platform | Evidence CacheCow records and checks                                                                                                                                |
| -------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Linux    | The boot ID. A different boot ID proves every process from the earlier boot has exited.                                                                             |
| macOS    | The boot session UUID (`kern.bootsessionuuid`). A different one proves the earlier boot ended. In the same boot, an empty process coalition also proves completion. |
| Windows  | The supervisor process ID and creation time. If no running process has that identity, Windows has already ended the build's job and all its processes.              |

Limits:

- On Linux, if only the supervisor is killed and the machine does not reboot, descendants of the build can keep running, so the lease stays until the next reboot.
- Journals written by earlier CacheCow versions carry no recovery identity. If one is incomplete, it stays protected and cannot recover automatically.
- If CacheCow cannot read the current identity, it keeps the lease.

When a lease blocks a command and cannot be released, CacheCow keeps the command waiting (`ccow clean` fails with `cache is in use`) and prints one notice on stderr, at the default log level, that names the cache path, the lease and the journal directory with the reason. A lease whose build is still running is ordinary waiting and prints no notice. Registry lock contention is reported separately and is not treated as a lease conflict.

`ccow doctor` lists every lease it released under `recoveredLeases` and every lease it kept under `keptLeases`, each with a reason: `native-completion`, `journal-complete`, `owner-running`, `same-boot-descendants-may-survive`, `incomplete-journal-without-native-identity`, `identity-check-failed`, or `journal-unreadable`. Older CacheCow versions cannot read the newer journals and keep their leases.

Do not force-delete protection records to make an active writer appear idle. Do not start an unmanaged build against a cache whose ownership is uncertain. If diagnostics cannot establish a safe recovery, preserve the cache and report the failure through [GitHub Issues](https://github.com/delinoio/oss/issues).

## Cleaning during use

`ccow clean` refuses active use immediately. Shim-dispatched `cargo clean` waits for the relevant lock and does not prepare a cache. A long-running source or destination process may explain that wait. Cancelling a waiting operation must be diagnosable and must not compromise the active process's protection.
