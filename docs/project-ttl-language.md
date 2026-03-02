# Project: ttl-language

## Goal
This document defines the stable language and compiler contracts for `.ttl` sources.
The design target is Go-like syntax plus ergonomic TurboTasks-style core types (`Vc`-centric) for incremental, cache-first, parallel task execution.

## Path
- Language spec document: `docs/project-ttl-language.md`
- Compiler implementation target: `cmds/ttlc` (planned in v1 documentation phase)

## Runtime and Language
- Language surface: TTL (`.ttl`)
- v1 compiler backend: generated Go source
- v1 execution/caching backend: runtime APIs + SQLite cache

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
}

enum TtlCoreType {
  Vc = "vc",
  ResolvedVc = "resolved-vc",
  OperationVc = "operation-vc",
  TransientValue = "transient-value",
  State = "state",
}
```

Core language contracts:
- File extension is always `.ttl`.
- Task declaration style is fixed to `task func`.
- Base syntax intentionally tracks Go style (blocks, signatures, package/import model).
- Every `task func` must return `Vc[T]`.
- `read(vc)` establishes dependency tracking from current task to the referenced task/cell.

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
- Reuse occurs only when full fingerprint matches.
- Any component mismatch triggers recomputation.

Parallel execution contract:
- Scheduler may execute tasks concurrently when no unresolved dependency edge exists.
- Execution order is deterministic with respect to dependency constraints, not submission order.

## Storage
Cache backend is fixed to SQLite in v1.
Minimum conceptual schema (field names are stable contract names):
- `task_key`
- `input_fingerprint`
- `output_blob_ref`
- `deps`
- `metadata`

Recommended conceptual tables:
- `task_cache(task_key PRIMARY KEY, input_fingerprint, output_blob_ref, metadata_json, updated_at)`
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
