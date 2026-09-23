# Cache management

**pnport 0.1.0 is not released.** The CLI cache commands are present in development source; their behavior below is the release contract and is not an installation recommendation.

pnport lazily places required package content in a private per-user cache. The cache backs native access without creating a project `node_modules` tree. Completed entries are retained until explicit cleanup: there is no automatic expiry, eviction, or product-imposed total-size quota. Disk exhaustion, permission errors, and failed publication are explicit failures.

## Inspect the cache

`pnport cache path` prints the effective cache directory. `pnport cache list` shows retained entries and states. Both work without an active PnP project. Use the global `--cache-dir <path>` option to select a separate location for a session or reproducible measurement.

## Prune or clean

`pnport cache prune` removes abandoned incomplete entries and obsolete cache formats when safe. `pnport cache clean` removes inactive entries. Active entries remain protected. Both commands report retained entries and deletion failures; neither should delete files outside the owned cache or blindly remove entries with uncertain ownership.

Different installed versions can use separate cache-format namespaces, so an explicit [rollback](/pnport/releases) need not destructively migrate the newer version's entries. Cleaning a cache can make the next run cold and require package materialization again. pnport does not download or install dependencies; keep the Yarn installation and its package archives available.
