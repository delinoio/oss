# CacheCow

> CacheCow 0.1.0 is unreleased. This guide describes intended workflows, not published artifacts or verified native support.

CacheCow (`ccow`) prepares independent writable build caches for Git worktrees with filesystem copy-on-write (CoW). It supports the intended `cargo`, `rspack`, and `rsbuild` presets.

## How it works

The first preparation clones a compatible source cache into the current worktree. The primary checkout is the default source. Later runs keep the worktree's own cache. Changes do not flow back to the source or into other worktrees.

CoW can initially share physical disk blocks while keeping writes separate. It does not guarantee cache hits, faster builds, shared compiler heaps, or shared file page-cache memory. Each build tool still decides what to rebuild.

## Choose a workflow

- [Installation and availability](/cachecow/installation): native archives, npm, requirements, and rollback.
- [Configuration and sources](/cachecow/configuration): Cargo, Rspack, Rsbuild, and source precedence.
- [Cache lifecycle](/cachecow/cache): explicit commands, preparation, replacement, failures, and manual cleaning.
- [Concurrency and processes](/cachecow/concurrency): locks, watch servers, cancellation, and safe recovery.
- [Shims and shells](/cachecow/shims): use existing tool commands with optional activation.
- [Output and exit codes](/cachecow/output): automation and diagnostics.
- [Troubleshooting and performance](/cachecow/troubleshooting): recovery, limits, and measurement.

## Boundaries

CacheCow is local-only, for the current OS user and worktrees of the same Git repository. It has no remote cache, telemetry, automatic cache publication, central snapshot catalog, or automatic eviction. It does not sandbox build code.

Only verified tool-cache paths are eligible. Source files, Git metadata, settings, unrelated files, dependencies, and frontend final outputs must remain intact. Ordinary hard links are not a substitute for independent CoW files.

CacheCow is licensed under Apache-2.0. Support is through [GitHub Issues](https://github.com/delinoio/oss/issues), without a response-time SLA.
