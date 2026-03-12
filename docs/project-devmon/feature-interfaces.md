# Feature: interfaces

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

