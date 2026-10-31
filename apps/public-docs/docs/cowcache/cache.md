# Cache lifecycle

> CowCache 0.1.0 is unreleased. This guide describes intended workflows, not published artifacts or verified native support.

## Prepare, run, and inspect

Once installed and configured, use these intended commands from a Git project directory:

```sh
cowc status --preset cargo
cowc prepare --preset cargo
cowc run --preset cargo -- cargo test
cowc doctor
```

`status` inspects cache state. `prepare` prepares a cache without running a build. `run` prepares or reuses a cache and runs the requested command. `doctor` diagnoses configuration, environment, cache, and lock problems. Rspack and Rsbuild use their corresponding presets and [configuration requirements](/cowcache/configuration).

If the worktree already has a cache, keep using it. On first preparation without an existing cache, CowCache clones a compatible source cache while coordinating source access. Subsequent builds modify the worktree's own writable cache. Source changes are not pulled in automatically, and derived changes are not pushed back.

## Explicit replacement

To request a fresh copy from a selected source:

```sh
cowc prepare --preset cargo --source "../main-checkout" --replace
```

Interactive replacement requires confirmation. Noninteractive replacement requires explicit `--replace`. CowCache must finish preparing the new copy before replacing the old cache. A preparation failure must preserve the old cache and remove incomplete copies.

If CoW is unavailable and you accept the storage cost of an ordinary copy, explicitly add `--allow-copy`. CowCache must report ordinary-copy use. This option does not authorize ignoring permission errors, disk exhaustion, incompatible sources, or other preparation failures.

## Unusable source

For a missing, corrupt, or incompatible source, interactive `run` offers:

1. Build in the current worktree without the source cache.
2. Select another source.
3. Build in the source first.
4. Cancel.

Before a source build, CowCache must show the exact requested command and source working directory and obtain confirmation. It runs that same command there, not a separate preset build command. Source building must not recurse into another source-build flow or copy a cache into itself.

Noninteractive execution must fail with remediation when it needs this decision. It must not silently continue without a source cache. For CI, establish a usable source and matching configuration before retrying. A default-primary-checkout build can create its own cache directly.

`prepare` has no build command. With an unusable source, it offers only another source or cancellation; source building requires `cowc run`. Noninteractive `prepare` returns an actionable error.

## Preparation and build failures

Disk-full, permission, and other actual preparation errors stop before launching the build. CowCache must preserve the old cache, remove incomplete preparation, and explain the cause and remedy. Fix that cause before retrying.

Failed or cancelled builds retain their working cache. The build tool decides whether partial results remain usable. A normal rebuild after restoration is permitted; CowCache must not bypass freshness checks to hide it.

## Manual cleaning

```sh
cowc clean --preset cargo
```

`cowc clean` deletes the selected verified cache without confirmation and without `--yes`, including in scripts. Check `cowc status` and the preset before running it. It immediately refuses an in-use cache. Stop the managed build and allow its process tree to exit before retrying.

Only preset-verified cache paths may be deleted. Sources, Git metadata, CowCache settings, external link targets, and unrelated files must remain intact. Normal cache subdirectories within a worktree are allowed; the entire worktree is never a cache.

There is no age-based or capacity-based eviction. Retention is manual. Removing incomplete preparation after a failure is recovery, not eviction.

An activated Cargo shim handles `cargo clean` differently: it waits for the cache lock, performs no preparation or cloning, then preserves Cargo's own arguments and deletion semantics. See [shims](/cowcache/shims#managed-and-pass-through-commands).
