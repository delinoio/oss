# Project: ttl

## Goal
`ttl` defines a new task language and toolchain for Go ecosystems inspired by TurboTasks-style execution.
The project focuses on incremental computing, persistent caching, and parallel task scheduling, while allowing v1 delivery by compiling `.ttl` sources to Go code.

## Path
- Canonical compiler CLI path: `cmds/ttlc`
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
- Dependency graph extraction and cycle diagnostics
- Persistent metadata cache contract backed by SQLite
- Cache-key fingerprint derivation for future execution reuse
- Structured logging contract for compile/runtime observability

## Out of Scope
- Distributed cache and remote execution in v1
- Full IDE language server feature set in v1
- Multi-target backends beyond Go source in v1
- Runtime task execution workers in phase 1
- Non-deterministic or side-effectful task semantics by default

## Architecture
Primary boundaries:
- Frontend: lexer/parser and type checker for `.ttl` sources.
- Middle: typed IR and dependency graph builder.
- Backend: Go emitter that lowers typed IR to generated Go source.
- Runtime: scheduler, cache adapter, invalidation evaluator, and execution workers.
- CLI: user-facing commands for build/check/explain/run workflows.

Phase 1 flow:
1. Parse `.ttl` files and type-check task/module declarations.
2. Build typed dependency graph and calculate task fingerprints.
3. Emit generated Go source into `.ttl/gen`.
4. Persist task metadata rows in SQLite cache schema.
5. Expose dependency and fingerprint details through `ttlc explain`.

Phase 2 run flow:
1. Reuse semantic analysis and fingerprint derivation.
2. Select a root task from `--task` and validate `--args` JSON object.
3. Generate and execute a Go runner for the supported TTL subset.
4. Return `result` + `run_trace` and persist root-task run metadata in SQLite cache.

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
  Run = "run",
}

enum TtlSchemaVersion {
  V1Alpha1 = "v1alpha1",
}

enum TtlResponseStatus {
  Ok = "ok",
  Failed = "failed",
}

enum TtlCoreType {
  Vc = "vc",
  ResolvedVc = "resolved-vc",
  OperationVc = "operation-vc",
  TransientValue = "transient-value",
  State = "state",
}

enum TtlInvalidationReason {
  None = "none",
  CacheMiss = "cache_miss",
  InputContentChanged = "input_content_changed",
  ParameterChanged = "parameter_changed",
  EnvironmentChanged = "environment_changed",
  CacheCorruption = "cache_corruption",
}
```

Canonical CLI command contracts:
- `ttlc build [--entry <file.ttl>] [--out-dir <dir>] [--no-color]`
: Compiles `.ttl` to Go source and writes metadata cache rows. Phase 1 does not execute tasks.
- `ttlc check [--entry <file.ttl>] [--no-color]`
: Parses and type-checks without writing generated runtime artifacts.
- `ttlc explain [--entry <file.ttl>] [--task <task-name>] [--no-color]`
: Shows dependency graph, cache-key inputs, and invalidation reasons.
- `ttlc run [--entry <file.ttl>] --task <task-name> [--args <json>] [--no-color]`
: Executes the selected task with generated Go runner code and returns `result`, `run_trace`, and root-task `cache_analysis`.

Default flag contract:
- `--entry`: `./main.ttl`
- `--out-dir`: `.ttl/gen`
- `--no-color`: `false` (ANSI color enabled by default for logs)
- `--task` (`run`): required
- `--args` (`run`): `{}` (JSON object)
- Cache DB path: `.ttl/cache/cache.sqlite3`

Canonical CLI JSON response envelope:

```json
{
  "schema_version": "v1alpha1",
  "command": "build|check|explain|run",
  "status": "ok|failed",
  "diagnostics": [],
  "data": {}
}
```

- `status=failed` when diagnostics are present.
- Command-level runtime failures (for example path resolution or missing entry file errors) must still emit this envelope on stdout with `status=failed`.
- `explain.data` includes per-task `cache_analysis` rows with `task_id`, `cache_key`, `cache_hit`, and `invalidation_reason`.
- When cache initialization/read is unavailable during `explain`, the command still returns semantic explain output with `cache_analysis=[]`.
- `run.data` includes `entry`, `module`, `task`, `args`, `result`, `run_trace`, and root-task `cache_analysis`.
- `run` cache policy in this phase stores persistent results only for the selected root task.

Cache-key contract (v1):
- `cache_key = hash(input_content_hash + parameter_hash + environment_snapshot_hash)`
- For `run`, `parameter_hash` includes task signature and canonicalized `--args` payload.
- Cache hit requires exact key equality.
- Cache mismatch triggers recomputation and cache overwrite.
- Phase 1 uses `environment_snapshot_hash = hash("")` as an explicit baseline default.

## Storage
Canonical local storage layout:
- `.ttl/cache/cache.sqlite3`: SQLite database for task metadata and cache indices.
- `.ttl/cache/blobs/`: optional output blob payload storage referenced by DB rows.
- `.ttl/gen/`: generated Go source artifacts.

Minimum persisted records:
- `task_key`
- `module`
- `task_id`
- `input_content_hash`
- `parameter_hash`
- `environment_snapshot_hash`
- `input_fingerprint`
- `output_blob_ref`
- `deps`
- `metadata`

Retention and persistence expectations:
- Cache is persistent across process restarts.
- Cache invalidation is key-based, not timestamp-only.
- Schema migrations must be explicit and versioned.
- Schema version mismatch resets cache tables and rebuilds schema metadata for safety.

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
- CLI logs are ANSI-colorized by default and support `--no-color` opt-out.

## Build and Test
Phase 1/2 implementation validation commands:
- Build: `go build ./cmds/ttlc/...`
- Test: `go test ./cmds/ttlc/...`
- Workspace sanity: `go test ./...`

Documentation acceptance scenarios:
1. `docs/project-ttl.md` keeps required template section order and content.
2. `AGENTS.md` project IDs and ownership mappings remain consistent with this contract.
3. `docs/project-ttl-language.md` syntax/type contracts remain compatible with this project contract.
4. Go compile target and SQLite cache backend remain identical across all ttl documents.

## Roadmap
- Phase 1: Parser/type-checker/go-emitter/metadata-cache baseline in `cmds/ttlc`.
- Phase 2: Incremental scheduler and runtime task execution with real cache reuse.
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
