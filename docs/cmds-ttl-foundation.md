# cmds-ttl-foundation

## Scope
- Project/component: TTL compiler command contract
- Canonical path: `cmds/ttlc`

## Runtime and Language
- Runtime: Go CLI
- Primary language: Go

## Users and Operators
- Engineers authoring and executing `.ttl` task graphs
- Operators validating cache-aware task execution in CI and local workflows

## Interfaces and Contracts
- Stable command identifiers: `build`, `check`, `explain`, `run`.
- `ttlc run` requires `--task` and accepts optional `--args <json>` with default `{}`.
- `ttlc run` response payload includes `result`, `run_trace`, and root-task `cache_analysis`.

## Storage
- Uses cache backend for task execution artifacts and invalidation metadata.
- Build outputs and generated files must follow deterministic path and key derivation rules.

## Security
- Runtime arguments and execution contexts must be validated before execution.
- Logs and diagnostic output must not leak sensitive task arguments by default.

## Logging
- Use structured `log/slog` logs for command lifecycle, cache decisions, and execution outcomes.
- Every top-level command execution emits a stable per-execution `trace_id`.
- Run/cache events include `execution_trace_id` derived deterministically from `run_trace` when available.
- Stable logging keys include `trace_id`, `execution_trace_id`, `task_id`, `cache_key`, `cache_hit`, `invalidation_reason`, `diagnostic_id`, `diagnostic_kind`, `source_path`, `line`, and `column`.

## Build and Test
- Local validation: `go test ./cmds/ttlc/...`
- Repository baseline: `go test ./...`

## Dependencies and Integrations
- Integrates with TTL language semantics defined in `docs/cmds-ttl-language-contract.md`.
- Integrates with task runtime backends and generated artifact consumers.

## Change Triggers
- Update `docs/project-ttl.md` and this file for command shape, runtime, or cache contract updates.
- Update `docs/cmds-ttl-language-contract.md` in the same change when language-level compatibility is affected.

## References
- `docs/project-ttl.md`
- `docs/cmds-ttl-language-contract.md`
- `docs/domain-template.md`
