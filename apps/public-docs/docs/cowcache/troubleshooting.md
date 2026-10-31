# Troubleshooting and performance

> CowCache 0.1.0 is unreleased. This guide describes intended workflows, not published artifacts or verified native support.

## Diagnose before changing state

Run `cowc doctor` in the affected project and inspect `cowc status`. Use `--verbose` for paths, versions, CoW capability, lock state, and failure causes. For interception or executable-selection problems, use `cowc shim doctor`.

| Symptom                                       | Action                                                                                                                                                                    |
| --------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| No usable source, including a bare repository | Select a same-repository worktree with `--source`, save a source, or use interactive `run` choices. Noninteractive use must fail until the required decision is resolved. |
| Missing or incompatible source cache          | Match tool versions, platform, architecture, and build settings, or select another source. Use `run` for a confirmed source build; `prepare` cannot build it.             |
| Unknown cache location                        | Confirm the project directory and preset. Complete the persistent-cache integration before reuse. Do not invent paths to suppress an error.                               |
| CoW unavailable or cross-volume failure       | Use an eligible filesystem/volume or explicitly allow ordinary copying with `--allow-copy`. Check the reported mode.                                                      |
| Disk full or permission denied                | Correct capacity or permissions and retry. Preparation must preserve the old cache and must not launch the build.                                                         |
| Waiting for a cache                           | Inspect the active process and source build. Stop a watch server when you need its cache as a source. Do not delete locks to bypass an active writer.                     |
| Clean refuses active use                      | Stop the managed process tree before `cowc clean`. Shim-dispatched `cargo clean` waits instead.                                                                           |
| Tool not found or unexpected executable       | Inspect shim diagnostics, PATH, aliases/functions, and version-manager selection. Restore the real tool or correct PATH.                                                  |
| Unsupported settings/record version           | Use a compatible CowCache version and follow recovery guidance. Do not rewrite unknown records.                                                                           |
| Unexpected termination                        | Inspect remaining descendants and lock state before retrying. Follow [process recovery](/cowcache/concurrency#cancellation-and-recovery).                                 |

Source recovery is different from actual preparation failure. A missing or incompatible source can prompt for a decision in interactive `run`; a failed copy does not implicitly authorize continuing uncached. See the full [cache lifecycle](/cowcache/cache).

## Rebuilds after cloning

A copied cache can still be invalidated. Cargo/rustc, Rspack, and Rsbuild must retain their freshness decisions. Different paths or settings can reduce reuse. Dirty and untracked files must cause the rebuilds needed for correct results.

Do not modify fingerprints or timestamps to manufacture a hit. Compare the resulting build with a clean build if you suspect stale output. Upstream tool improvements are separate work, not a first-release freshness bypass.

## Measure benefits and costs

No CowCache performance result is asserted for unreleased 0.1.0. CoW does not share compiler heaps or guarantee shared file page-cache memory. It does not guarantee lower memory use, faster builds, or a fixed percentage of storage savings. Writes after cloning allocate additional blocks; ordinary-copy fallback consumes independent storage.

For a useful comparison, measure cold preparation, a warm original build, a cloned-worktree build, and a later build after changing the worktree. Record:

- Physical allocated storage, not only apparent file sizes.
- Cache preparation time separately from build time.
- Peak and observed memory use with the measurement method.
- Tool versions, build settings, OS, architecture, filesystem, source revision, and whether CoW or ordinary copy was used.
- Source-cache state, workload changes, run count, and unmeasured limits.

Verify physical sharing and write isolation on the actual filesystem. A successful copy API, fixture, cross-build, or archive does not prove native CoW support. Modifying or cleaning one derived cache must not modify its source or another worktree. APFS, Btrfs, reflink-enabled XFS, and ReFS require their own native evidence.

## Report a problem

Use [GitHub Issues](https://github.com/delinoio/oss/issues). Include the CowCache version, tool and OS versions, filesystem, observed copy mode, safe reproduction steps, expected behavior, actual result, and exit code. State which checks you actually ran. Remove credentials, private contents, environment values, and sensitive command arguments before posting. There is no response-time SLA.
