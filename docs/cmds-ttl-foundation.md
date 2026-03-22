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
- User-facing errors and diagnostics must be written in English and remain actionable.

## Logging
- Use structured `log/slog` logs for command lifecycle, cache decisions, and execution outcomes.
- Every top-level command execution emits a stable per-execution `trace_id`.
- Run/cache events include `execution_trace_id` derived deterministically from `run_trace` when available.
- Stable logging keys include `trace_id`, `execution_trace_id`, `task_id`, `cache_key`, `cache_hit`, `invalidation_reason`, `diagnostic_id`, `diagnostic_kind`, `source_path`, `line`, and `column`.

## Error Message Quality Contract
- User-facing command failures (`stderr`, JSON `diagnostics`, and bubbled command errors) must use centralized templates from `cmds/ttlc/internal/messages`.
- Templates must be addressed via stable enum-like IDs and formatted through shared builders, not ad-hoc string literals.
- Message wording must include enough context to act (problem target + expected shape + actual safe metadata + next-step hint when relevant).
- Command/runtime failures must preserve root-cause error chains (`%w`) while keeping the top-level message actionable.
- `run --args` diagnostics must report JSON root shape and trailing-token context using type-only metadata (for example: `object`, `array`, `null`) rather than raw argument values.
- Run-argument type diagnostics must include argument path (including nested object fields) plus `expected` vs `actual` type summaries.
- Error/diagnostic text must avoid exposing sensitive runtime argument values by default.

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
