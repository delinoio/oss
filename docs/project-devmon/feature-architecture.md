# Feature: architecture

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

