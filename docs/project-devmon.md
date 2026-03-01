# Project: devmon

## Goal
`devmon` is a Go automation daemon that runs recurring folder jobs and can now be managed as a macOS user daemon from a menu bar app.
Its primary goal is to provide safe local command scheduling with clear operational visibility and simple lifecycle control.

## Path
- Canonical CLI path: `cmds/devmon`
- Operator README: `cmds/devmon/README.md`
- Service lifecycle manager: `cmds/devmon/internal/servicecontrol`
- Menu bar app: `cmds/devmon/internal/menubar`
- Runtime status store: `cmds/devmon/internal/state`

## Runtime and Language
- Go CLI + macOS menu bar integration

## Users
- Engineers maintaining multiple local repositories or workspace folders
- Operators who need deterministic recurring local command automation
- macOS users who want to manage daemon lifecycle from the top menu bar

## In Scope
- Foreground daemon command (`devmon daemon`) with graceful shutdown behavior
- Configuration validation command (`devmon validate`)
- Service lifecycle commands (`devmon service install|uninstall|start|stop|status`) on macOS
- Menu bar command (`devmon menubar`) on macOS
- LaunchAgent installation for both daemon and menu bar processes
- TOML-based folder and job configuration (`devmon.toml`)
- Interval-based scheduling with startup run support
- Global concurrency limiting for all jobs
- Per-job overlap protection (skip when previous run is still active)
- Shell-command job execution (`shell -c script`) with timeout enforcement
- Structured logs for lifecycle, skip reasons, and command output streams
- Structured daemon status persistence for operational UI and status queries

## Out of Scope
- System-domain (`root`) service management
- Built-in non-shell job types in v1
- Remote orchestration or multi-host execution
- Queueing backlog execution when concurrency slots are exhausted
- Cross-platform menu bar implementations in this phase

## Architecture
Primary components:
- CLI Router: parses `daemon`, `validate`, `service`, and `menubar` commands.
- Config Loader/Validator: parses TOML, resolves folder paths, and enforces schema/runtime constraints.
- Scheduler Runner: creates per-job timers, triggers startup runs, and applies overlap/concurrency policies.
- Shell Executor: runs `shell -c script` in target folder with timeout and exit/outcome mapping.
- Service Controller (`servicecontrol`): manages LaunchAgent plist generation/validation and `launchctl` operations.
- Menu Bar App (`menubar`): polls service + daemon status and exposes start/stop/open actions.
- State Store (`state`): writes daemon heartbeat and recent run/skip summaries to JSON.
- Logging Adapter: emits structured operational events through Go `log/slog`.

Runtime flow (daemon):
1. `devmon daemon --config <path>` loads and validates TOML configuration.
2. Daemon state store is initialized and marks process start (`pid`, `started_at`, heartbeat).
3. Scheduler performs startup runs (effective `startup_run=true`) and starts interval tickers.
4. On each trigger, runner checks enabled state, overlap guard, and global concurrency availability.
5. Executor runs shell command in folder working directory, streams stdout/stderr line events, and returns outcome metadata.
6. Runner updates state store with latest run/skip summary and active job count.
7. Daemon heartbeat updates are written on a fixed interval.
8. On `SIGINT`/`SIGTERM`, daemon stops new triggers, drains active runs, marks stopped state only when caller PID still owns the snapshot, and exits.

Runtime flow (service + menu bar):
1. `devmon service install` validates daemon config, writes LaunchAgent plists for daemon and menu bar, and bootstraps both in `gui/<uid>`.
2. Daemon LaunchAgent is persistent (`KeepAlive=true`) and starts at login; menu bar LaunchAgent starts at login but is non-persistent (`KeepAlive=false`) so explicit quit is respected.
3. `devmon menubar` polls `devmon service status` data and state file signals.
4. Menu actions call lifecycle operations (`start`, `stop`) and local file open actions (`open log`, `open config`).

## Interfaces
Canonical command identifiers:

```ts
enum DevmonCommand {
  Daemon = "daemon",
  Validate = "validate",
  Service = "service",
  Menubar = "menubar",
}
```

Canonical service action identifiers:

```ts
enum DevmonServiceAction {
  Install = "install",
  Uninstall = "uninstall",
  Start = "start",
  Stop = "stop",
  Status = "status",
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
- `devmon service install`
: Installs and bootstraps LaunchAgents for daemon and menu bar after validating daemon config.
- `devmon service uninstall`
: Bootouts daemon/menu bar LaunchAgents and removes plist files.
- `devmon service start`
: Starts daemon LaunchAgent in user GUI domain.
- `devmon service stop`
: Stops daemon LaunchAgent in user GUI domain.
- `devmon service status`
: Returns structured JSON status for daemon/menu bar load state plus daemon health summary.
- `devmon menubar`
: Runs the menu bar app process (macOS only).

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

Default path contract (single config strategy):
- Config file: `~/.config/devmon/devmon.toml`
- State file: `~/.local/state/devmon/status.json`
- Daemon log file: `~/Library/Logs/devmon/daemon.log`
- Daemon LaunchAgent plist: `~/Library/LaunchAgents/io.delino.devmon.daemon.plist`
- Menu bar LaunchAgent plist: `~/Library/LaunchAgents/io.delino.devmon.menubar.plist`
- LaunchAgent labels:
: `io.delino.devmon.daemon`
: `io.delino.devmon.menubar`

Scheduling contract:
- Startup run occurs immediately when effective startup flag is true.
- Identical job re-entry is skipped with `skipped-overlap`.
- Global concurrency overflow is skipped with `skipped-capacity`.
- No backlog queueing is performed in v1.

## Storage
- Reads scheduler config from `devmon.toml`.
- Writes daemon state summary JSON to `~/.local/state/devmon/status.json`.
- Writes LaunchAgent plists under `~/Library/LaunchAgents/` for service-managed mode.
- Writes daemon logs to `~/Library/Logs/devmon/daemon.log` when LaunchAgent routes stdio.
- Uses process-memory scheduler state for active jobs and ticker lifecycle.
- Relies on command-specific local side effects inside configured folder paths.

State file schema (`schema_version = "v1"`) includes:
- daemon process state (`running`, `pid`, `started_at`, `last_heartbeat_at`)
: `started_at` is refreshed on every daemon start so restart diagnostics reflect current process lifetime.
: stop-state writes are PID-scoped; stale processes cannot overwrite a newer daemon state, and successful owner stop clears `pid`.
- scheduler occupancy (`active_jobs`)
- recent run summary (`outcome`, `folder_id`, `job_id`, `duration_ms`, `error`, `timestamp`)
- recent skip summary (`outcome`, `folder_id`, `job_id`, `skip_reason`, `timestamp`)
- latest daemon-level error summary (`last_error`)

## Security
- Commands run only within explicitly configured folder paths.
- Executor passes arguments directly to process APIs and avoids shell interpolation outside configured script text.
- Timeout and cancellation boundaries prevent uncontrolled long-running jobs.
- Service management is scoped to macOS user GUI domain (`gui/<uid>`), not privileged system domain.
- LaunchAgent-managed processes must not self-daemonize; lifecycle remains launchd-owned.
- Structured logs and persisted status must avoid secret values from job command output where possible.

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

Service-control event fields:
- `action`
- `label`
- `domain`
- `result`
- `error`

Menu bar failure fields:
- `action`
- `error`

Output stream logging:
- Command stdout/stderr are logged line-by-line with `stream=stdout|stderr`.
- Logging uses Go `log/slog` with structured JSON output.

## Build and Test
Validation commands:
- Build: `go build ./cmds/devmon/...`
- Devmon tests: `go test ./cmds/devmon/...`
- Workspace validation: `go test ./...`
- Focused regression suites:
: `go test ./cmds/devmon/internal/cli -count=1`
: `go test ./cmds/devmon/internal/servicecontrol -count=1`
: `go test ./cmds/devmon/internal/scheduler -count=1`

Required behavioral scenarios:
1. Config validation success and deterministic validation failures.
2. Startup run and interval scheduling behavior.
3. Overlap skip behavior for long-running jobs.
4. Global concurrency skip behavior when slots are exhausted.
5. Timeout cancellation and outcome mapping.
6. Signal-driven graceful daemon shutdown.
7. `service install` writes/validates both plist files and bootstraps/kickstarts both labels.
8. `service status` reports consistent daemon/menu bar load state and daemon health.
9. `service stop` transitions daemon health to non-running and preserves deterministic output.
10. `service uninstall` bootouts labels and removes plist files.
11. State file updates on run completion and skip outcomes.
12. Heartbeat staleness or missing PID process marks daemon health as non-running/error.
13. Non-darwin service/menubar behavior returns deterministic unsupported errors.
14. `service install` fails before `launchctl` operations when daemon config file is missing or invalid.

Testing policy notes:
- Behavioral tests are expected to map directly to the 14 required scenarios above.
- Darwin-specific service lifecycle assertions live behind `//go:build darwin` tests.
- Non-darwin suites must still verify deterministic unsupported errors for `service` and `menubar`.
- Daemon shutdown tests may use internal test seams to avoid subprocess flakiness while keeping public CLI contracts unchanged.

## Roadmap
- Phase 1: shell-command scheduling, service lifecycle controls, menu bar management, and state persistence.
- Phase 2: additional built-in job types and richer policy controls.
- Phase 3: deeper observability (history retention, diagnostics views).

## Open Questions
- Whether future versions should add queueing semantics for capacity-skipped runs.
- Whether per-job concurrency limits should be added beyond overlap guard.
- Whether service-management abstractions should be extended beyond macOS.
