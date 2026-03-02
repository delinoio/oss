# Project: ttl

## Goal
`ttl` defines a new task language and toolchain for Go ecosystems inspired by TurboTasks-style execution.
The project focuses on incremental computing, persistent caching, and parallel task scheduling, while allowing v1 delivery by compiling `.ttl` sources to Go code.

## Path
- Canonical compiler CLI path: `cmds/ttlc` (planned in v1 documentation phase)
- Canonical language contract path: `docs/project-ttl-language.md`

## Runtime and Language
- TTL language (`.ttl`) + Go compiler CLI
- v1 compile target: Go source (`ttl` -> generated Go)
- v1 cache backend: SQLite

## Users
- Build/tooling engineers maintaining CI and developer workflows
- Monorepo maintainers optimizing repeated build and analysis pipelines
- Platform engineers building deterministic task graphs with cache reuse

## In Scope
- `.ttl` parsing and syntax validation
- Typed task signatures and type checking for v1 core types
- Go source generation from `.ttl` modules
- Incremental scheduler contract (dependency graph + invalidation)
- Persistent cache contract backed by SQLite
- Parallel task execution contract for independent tasks
- Structured logging contract for compile/runtime observability

## Out of Scope
- Distributed cache and remote execution in v1
- Full IDE language server feature set in v1
- Multi-target backends beyond Go source in v1
- Non-deterministic or side-effectful task semantics by default

## Architecture
Primary boundaries:
- Frontend: lexer/parser and type checker for `.ttl` sources.
- Middle: typed IR and dependency graph builder.
- Backend: Go emitter that lowers typed IR to generated Go source.
- Runtime: scheduler, cache adapter, invalidation evaluator, and execution workers.
- CLI: user-facing commands for build/check/explain workflows.

v1 flow:
1. Parse `.ttl` files and type-check task/module declarations.
2. Build typed dependency graph and calculate task fingerprints.
3. Query SQLite cache for reusable task outputs.
4. Schedule cache misses on parallel workers.
5. Emit generated Go source and persist fresh task results/metadata.

## Interfaces
Canonical project and runtime identifiers:

```ts
enum ProjectId {
  Ttl = "ttl",
}

enum TtlCompileTarget {
  GoSource = "go-source",
}

enum TtlCacheBackend {
  SQLite = "sqlite",
}

enum TtlCommand {
  Build = "build",
  Check = "check",
  Explain = "explain",
}

enum TtlCoreType {
  Vc = "vc",
  ResolvedVc = "resolved-vc",
  OperationVc = "operation-vc",
  TransientValue = "transient-value",
  State = "state",
}
```

Canonical CLI command contracts:
- `ttlc build [--entry <file.ttl>] [--out-dir <dir>]`
: Compiles `.ttl` to Go source and materializes cache-aware execution outputs.
- `ttlc check [--entry <file.ttl>]`
: Parses and type-checks without writing generated runtime artifacts.
- `ttlc explain [--entry <file.ttl>] [--task <task-name>]`
: Shows dependency graph, cache-key inputs, and invalidation reasons.

Cache-key contract (v1):
- `cache_key = hash(input_content_hash + parameter_hash + environment_snapshot_hash)`
- Cache hit requires exact key equality.
- Cache mismatch triggers recomputation and cache overwrite.

## Storage
Canonical local storage layout:
- `.ttl/cache/cache.sqlite3`: SQLite database for task metadata and cache indices.
- `.ttl/cache/blobs/`: optional output blob payload storage referenced by DB rows.
- `.ttl/gen/`: generated Go source artifacts.

Minimum persisted records:
- `task_key`
- `input_fingerprint`
- `output_blob_ref`
- `deps`
- `metadata`

Retention and persistence expectations:
- Cache is persistent across process restarts.
- Cache invalidation is key-based, not timestamp-only.
- Schema migrations must be explicit and versioned.

## Security
- Restrict cache directory permissions to current user (POSIX target: `0700` directory, `0600` files).
- Reject path traversal and symlink escape when resolving `.ttl` workspace paths.
- Never log secret material or raw sensitive environment variables.
- Keep generated code output paths under explicit workspace-controlled directories.

## Logging
Required `log/slog` structured fields:
- `compile_stage`
- `task_id`
- `cache_key`
- `cache_hit`
- `invalidation_reason`
- `worker_id`
- `duration_ms`
- `error_kind`

Logging baseline:
- Compiler stages emit start/end events with duration.
- Scheduler emits queue/dequeue/complete events.
- Cache layer emits read/write hit/miss/corruption events.

## Build and Test
v1 implementation validation commands (to apply once `cmds/ttlc` is implemented):
- Build: `go build ./cmds/ttlc/...`
- Test: `go test ./cmds/ttlc/...`
- Workspace sanity: `go test ./...`

Documentation acceptance scenarios:
1. `docs/project-ttl.md` keeps required template section order and content.
2. `AGENTS.md` project IDs and ownership mappings remain consistent with this contract.
3. `docs/project-ttl-language.md` syntax/type contracts remain compatible with this project contract.
4. Go compile target and SQLite cache backend remain identical across all ttl documents.

## Roadmap
- Phase 1: Documentation finalization and compiler scaffold (`cmds/ttlc`).
- Phase 2: Incremental scheduler, hash invalidation engine, and SQLite cache integration.
- Phase 3: Performance tuning, advanced observability, and richer cache lifecycle controls.

## Open Questions
- Should v2 include distributed cache protocol compatibility?
- Should remote execution be introduced before multi-target backend support?
- Which garbage-collection policy should prune stale cache blobs safely?

## References
- `AGENTS.md`
- `cmds/AGENTS.md`
- `docs/project-template.md`
- [turbo-tasks/lib.rs](https://github.com/vercel/next.js/blob/canary/turbopack/crates/turbo-tasks/src/lib.rs)
- [turbo-tasks/vc/mod.rs](https://github.com/vercel/next.js/blob/canary/turbopack/crates/turbo-tasks/src/vc/mod.rs)
- [turbo-tasks/value.rs](https://github.com/vercel/next.js/blob/canary/turbopack/crates/turbo-tasks/src/value.rs)
