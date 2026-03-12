# Feature: architecture

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

