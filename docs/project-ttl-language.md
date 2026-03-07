# Project: ttl-language

## Goal
This document defines the stable language and compiler contracts for `.ttl` sources.
The design target is Go-like syntax plus ergonomic TurboTasks-style core types (`Vc`-centric) for incremental, cache-first, parallel task execution.

## Path
- Language spec document: `docs/project-ttl-language.md`
- Compiler implementation target: `cmds/ttlc`

## Runtime and Language
- Language surface: TTL (`.ttl`)
- v1 compiler backend: generated Go source
- v1 execution/caching backend: generated Go runner execution + SQLite metadata cache

## Users
- Engineers authoring build/task pipelines in `.ttl`
- Runtime/compiler maintainers implementing deterministic incremental execution

## In Scope
- Lexical and syntactic contracts for `.ttl`
- Typed task declaration and core runtime types
- Incremental dependency tracking (`read(vc)` contract)
- Hash-based invalidation model
- Go code generation contract
- Runtime/caching schema-level contracts

## Out of Scope
- Full language grammar for every future feature
- Alternative backend targets in v1
- Language server protocol details
- Remote/distributed scheduler protocol

## Architecture
Language-to-runtime architecture:
- Source Layer: `.ttl` modules with Go-like declarations.
- Semantic Layer: type checking and task graph extraction.
- Lowering Layer: typed IR lowered to runtime API calls.
- Runtime Layer: dependency tracking, scheduler, cache lookup/writeback.
- Persistence Layer: SQLite metadata and optional blob storage.

Compilation flow:
1. Parse module declarations (`package`, `import`, `type`, `task func`, `func`).
2. Resolve symbols and validate core type usage.
3. Build dependency edges from `read(vc)` and task calls.
4. Derive task/cache fingerprints.
5. Emit Go source that invokes runtime task APIs.
6. For `ttlc run`, generate/execute runner Go code for supported subset expressions.

## Interfaces
Canonical type and command identifiers:

```ts
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

Core language contracts:
- File extension is always `.ttl`.
- Task declaration style is fixed to `task func`.
- Base syntax intentionally tracks Go style (blocks, signatures, package/import model).
- Every `task func` must return `Vc[T]`.
- `read(vc)` establishes dependency tracking from current task to the referenced task/cell.
- Phase 1 parser accepts `import` syntax, but semantic validation emits `unsupported_imports` diagnostics.

v1 syntax examples:

```ttl
package build

type Artifact struct {
    Path string
    Digest string
}

task func Build(target string) Vc[Artifact] {
    src := read(ResolveSource(target))
    digest := hash(src.Path, src.Digest)
    return vc(Artifact{Path: src.Path, Digest: digest})
}

func Main(target string) {
    val := read(Build(target))
    print(val.Path)
}
```

Generated Go shape contract (illustrative):

```go
func Build(target string) runtime.Vc[Artifact] {
    return runtime.Task("Build", target, func(ctx runtime.TaskContext) (Artifact, error) {
        src, err := runtime.Read(ctx, ResolveSource(target))
        if err != nil {
            return Artifact{}, err
        }
        digest := runtime.Hash(src.Path, src.Digest)
        return Artifact{Path: src.Path, Digest: digest}, nil
    })
}
```

Invalidation contract:
- Fingerprint inputs are mandatory and ordered:
1. Input content hash
2. Parameter hash
3. Environment snapshot hash
- For `run`, parameter hash includes task signature and canonicalized `--args` JSON object payload.
- Reuse occurs only when full fingerprint matches.
- Any component mismatch triggers recomputation.
- Phase 1 default: `environment_snapshot_hash = hash("")`.

Parallel execution contract:
- Scheduler may execute tasks concurrently when no unresolved dependency edge exists.
- Execution order is deterministic with respect to dependency constraints, not submission order.
- Phase 2 status: `ttlc run` executes task graphs through generated Go runner code with deterministic dependency evaluation.
- Current cache reuse scope for `run`: only the selected root task result is persisted/reused.

Explain output contract (Phase 1 default JSON envelope):
- Top-level envelope fields: `schema_version`, `command`, `status`, `diagnostics`, `data`
- `schema_version` is `v1alpha1`
- `command` is `explain`
- `status` is `ok|failed`
- `data.entry`
- `data.module`
- `data.tasks` (`id`, `params`, `return_type`, `deps`, `cache_key`)
- `data.fingerprint_components` (`input_content_hash`, `parameter_hash`, `environment_snapshot_hash`)
- `data.cache_analysis` (`task_id`, `cache_key`, `cache_hit`, `invalidation_reason`)
- Runtime failures in command execution still return the same envelope shape with `status=failed` and diagnostics.
- If cache store open/read fails during `explain`, semantic analysis output is still returned and `data.cache_analysis` is an empty array.

Run output contract (Phase 2 default JSON envelope):
- Top-level envelope fields: `schema_version`, `command`, `status`, `diagnostics`, `data`
- `schema_version` is `v1alpha1`
- `command` is `run`
- `status` is `ok|failed`
- `data.entry`
- `data.module`
- `data.task`
- `data.args` (JSON object)
- `data.result` (selected task value)
- `data.run_trace` (actual executed task order)
- `data.cache_analysis` (single root-task row with `task_id`, `cache_key`, `cache_hit`, `invalidation_reason`)
- `--task` is required for `run`.
- `--args` must be a JSON object, and parameter type mismatches return `type_error` diagnostics.
- Integer parameters reject fractional numeric values.
- Structured parameters must match declared object shape and field types.

Generated runner subset contract (Phase 2):
- Supported statements: assignment (`:=`, `=`), expression statement, `return`.
- Supported expressions: identifier, string literal, number literal, call, selector, composite literal.
- Built-ins: `vc(...)`, `read(...)`, `hash(...)`, `print(...)`.
- Unsupported expression forms (for example binary operators) are out of runtime subset scope in this phase.

## Storage
Cache backend is fixed to SQLite in v1.
Minimum conceptual schema (field names are stable contract names):
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

Recommended conceptual tables:
- `task_cache(task_key PRIMARY KEY, module, task_id, input_content_hash, parameter_hash, environment_snapshot_hash, input_fingerprint, output_blob_ref, metadata_json, updated_at, UNIQUE(module, task_id))`
- `task_deps(task_key, dep_task_key)`
- `cache_blobs(blob_ref PRIMARY KEY, codec, bytes, size_bytes)`

## Security
- Input file resolution must stay within configured workspace roots.
- Cache metadata and blobs must not expose secret env values.
- Corrupted cache records must fail closed (no unsafe partial decode).

## Logging
Compiler and runtime use `log/slog` with structured event records.
Required fields:
- `compile_stage`
- `task_id`
- `cache_key`
- `cache_hit`
- `invalidation_reason`
- `worker_id`
- `duration_ms`
- `error_kind`

CLI logging contract:
- ANSI color is enabled by default for operator-facing logs.
- `--no-color` disables ANSI color output.

Runtime task event baseline:
- `task_scheduled`
- `task_started`
- `task_cache_hit`
- `task_cache_miss`
- `task_completed`
- `task_failed`

## Build and Test
v1 implementation commands (applies when compiler/runtime code exists):
- `go build ./cmds/ttlc/...`
- `go test ./cmds/ttlc/...`

Documentation acceptance checks:
1. `task func` examples always return `Vc[T]`.
2. `read(vc)` examples always imply dependency tracking.
3. Hash invalidation explicitly includes input/parameter/environment components.
4. Cache backend remains SQLite in all ttl contracts.
5. Failure modes and observability fields remain explicit and stable.

Failure mode contracts:
- Circular dependency: fail with cycle diagnostics and no partial success state.
- Cache corruption: emit `error_kind=cache_corruption`, invalidate row, recompute.
- Type mismatch: fail during type-check or decode boundary with typed diagnostics.
- Non-deterministic task warning: emit warning diagnostics when unstable outputs are detected.
- Cache schema version mismatch: reset cache tables and recreate schema metadata.

## Roadmap
- Phase 1: Lock syntax + type contracts and ship Go emitter skeleton.
- Phase 2: Complete scheduler/dependency tracking and SQLite persistence.
- Phase 3: Improve diagnostics, performance, and cache lifecycle tooling.

## Open Questions
- Should future syntax include explicit task priority annotations?
- Should environment snapshots be user-configurable by allowlist rules?
- Should blob storage remain SQLite-only or move large payloads to file-backed blobs by default?

## References
- `docs/project-ttl.md`
- `docs/project-template.md`
- [turbo-tasks/lib.rs](https://github.com/vercel/next.js/blob/canary/turbopack/crates/turbo-tasks/src/lib.rs)
- [turbo-tasks/vc/mod.rs](https://github.com/vercel/next.js/blob/canary/turbopack/crates/turbo-tasks/src/vc/mod.rs)
- [turbo-tasks/value.rs](https://github.com/vercel/next.js/blob/canary/turbopack/crates/turbo-tasks/src/value.rs)
