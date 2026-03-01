# Project: devmon

## Goal
`devmon` is a Go CLI daemon that runs multiple customizable commands on configurable intervals across registered folders.
Its primary goal is to automate repetitive local maintenance workflows (for example Git default-branch sync and Rust cleanup tasks) with safe scheduling and structured operational logs.

## Path
- Canonical CLI path: `cmds/devmon`

## Runtime and Language
- Go CLI daemon

## Users
- Engineers maintaining multiple local repositories or workspace folders
- Operators who want deterministic recurring local command automation
- AI-assisted workflows that need explicit and observable local automation contracts

## In Scope
- Foreground daemon command (`devmon daemon`) with graceful shutdown behavior
- Configuration validation command (`devmon validate`)
- TOML-based folder and job configuration (`devmon.toml`)
- Interval-based scheduling with startup run support
- Global concurrency limiting for all jobs
- Per-job overlap protection (skip when previous run is still active)
- Shell-command job execution (`shell -c script`) with timeout enforcement
- Structured logs for lifecycle, skip reasons, and command output streams

## Out of Scope
- Built-in background service lifecycle management (`start/stop/status`) in v1
- Built-in non-shell job types in v1
- Remote orchestration or multi-host execution
- Command output persistence storage beyond process logs in v1
- Queueing backlog execution when concurrency slots are exhausted

## Architecture
Primary components:
- CLI Router: parses `daemon` and `validate` commands and resolves config path.
- Config Loader/Validator: parses TOML, resolves folder paths, and enforces schema/runtime constraints.
- Scheduler Runner: creates per-job timers, triggers startup runs, and applies overlap/concurrency policies.
- Shell Executor: runs `shell -c script` in target folder with timeout and exit/outcome mapping.
- Logging Adapter: emits structured operational events through Go `log/slog`.

Runtime flow:
1. `devmon daemon --config <path>` loads and validates TOML configuration.
2. Scheduler performs startup runs (effective `startup_run=true`) and starts interval tickers.
3. On each trigger, runner checks enabled state, overlap guard, and global concurrency availability.
4. Executor runs shell command in folder working directory, streams stdout/stderr line events, and returns outcome metadata.
5. Daemon reacts to `SIGINT`/`SIGTERM` by stopping new triggers, cancelling active jobs, waiting for drain, then exiting.

## Interfaces
Canonical command identifiers:

```ts
enum DevmonCommand {
  Daemon = "daemon",
  Validate = "validate",
}
```

Canonical job type identifiers:

```ts
enum DevmonJobType {
  ShellCommand = "shell-command",
}
```

Canonical run outcome identifiers:

```ts
enum DevmonRunOutcome {
  Success = "success",
  Failed = "failed",
  Timeout = "timeout",
  SkippedOverlap = "skipped-overlap",
  SkippedCapacity = "skipped-capacity",
  SkippedDisabled = "skipped-disabled",
}
```

CLI command contracts:
- `devmon daemon --config <path>`
: Runs the foreground scheduler daemon until interrupted.
- `devmon validate --config <path>`
: Validates config schema and runtime constraints without starting scheduling.

Config contract (`devmon.toml`):
- Top-level:
: `version = 1`
- Daemon section:
: `[daemon]`
: `max_concurrent_jobs = <int>`
: `startup_run = <bool>`
: `log_level = "debug"|"info"|"warn"|"error"`
- Folder sections:
: `[[folder]]`
: `id = "<kebab-case>"`
: `path = "<absolute-or-relative-path>"`
- Job sections:
: `[[folder.job]]`
: `id = "<kebab-case>"`
: `type = "shell-command"`
: `enabled = true|false`
: `interval = "<Go duration>"`
: `timeout = "<Go duration>"`
: `shell = "<shell binary>"`
: `script = "<shell script>"`
: `startup_run = true|false` (optional; falls back to daemon-level default)

Scheduling contract:
- Startup run occurs immediately when effective startup flag is true.
- Identical job re-entry is skipped with `skipped-overlap`.
- Global concurrency overflow is skipped with `skipped-capacity`.
- No backlog queueing is performed in v1.

## Storage
- No project-owned persistent state in v1.
- Reads configuration from `devmon.toml`.
- Uses process-memory scheduler state for active jobs and ticker lifecycle.
- Relies on command-specific local side effects inside configured folder paths.

## Security
- Commands run only within explicitly configured folder paths.
- Executor passes arguments directly to process APIs and avoids shell interpolation outside configured script text.
- Timeout and cancellation boundaries prevent uncontrolled long-running jobs.
- Structured logs must avoid leaking sensitive environment values from command output where possible; operators should avoid printing secrets in configured scripts.

## Logging
Required baseline fields:
- `event`
- `timestamp`
- `folder_id`
- `folder_path`
- `job_id`
- `job_type`
- `run_id`
- `outcome`
- `duration_ms`
- `interval`
- `timeout_ms`
- `exit_code`
- `error`
- `skip_reason`
- `max_concurrent_jobs`
- `active_jobs`

Output stream logging:
- Command stdout/stderr are logged line-by-line with `stream=stdout|stderr`.
- Logging uses Go `log/slog` with structured JSON output.

## Build and Test
Validation commands:
- Build: `go build ./cmds/devmon/...`
- Test: `go test ./cmds/devmon/...`
- Workspace validation: `go test ./...`

Required behavioral scenarios:
1. Config validation success and deterministic validation failures.
2. Startup run and interval scheduling behavior.
3. Overlap skip behavior for long-running jobs.
4. Global concurrency skip behavior when slots are exhausted.
5. Timeout cancellation and outcome mapping.
6. Signal-driven graceful daemon shutdown.

## Roadmap
- Phase 1: `shell-command` jobs with interval scheduling, concurrency caps, and structured logs.
- Phase 2: Additional built-in job types and richer job policies.
- Phase 3: Optional persistent run history and observability extensions.

## Open Questions
- Whether v2 should add queueing semantics for capacity-skipped runs.
- Whether per-job concurrency limits should be added beyond overlap guard.
- Whether daemon-level health endpoints are needed for external supervision integrations.
