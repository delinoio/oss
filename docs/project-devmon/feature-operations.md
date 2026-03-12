# Feature: operations

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

