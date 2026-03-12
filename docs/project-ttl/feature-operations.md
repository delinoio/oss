# Feature: operations

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
1. `docs/project-ttl/README.md` keeps required template section order and content.
2. `AGENTS.md` project IDs and ownership mappings remain consistent with this contract.
3. `docs/project-ttl/feature-language-spec.md` syntax/type contracts remain compatible with this project contract.
4. Go compile target and SQLite cache backend remain identical across all ttl documents.

