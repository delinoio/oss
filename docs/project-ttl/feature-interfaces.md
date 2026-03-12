# Feature: interfaces

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
- `run` argument validation rejects fractional values for integer parameters and enforces structured parameter object shapes.
- `run` cache rows are isolated from `build`/`explain` task-state rows to avoid cross-command invalidation drift.

Cache-key contract (v1):
- `cache_key = hash(input_content_hash + parameter_hash + environment_snapshot_hash)`
- For `run`, `parameter_hash` includes task signature and canonicalized `--args` payload.
- Cache hit requires exact key equality.
- Cache mismatch triggers recomputation and cache overwrite.
- Phase 1 uses `environment_snapshot_hash = hash("")` as an explicit baseline default.

