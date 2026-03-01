# devmon

`devmon` is a Go automation daemon for running recurring jobs in local folders.

It supports:
- Interval-based scheduling with optional startup runs
- Global concurrency limits across all jobs
- Per-job overlap protection
- Timeout-enforced shell command execution
- Config validation before runtime
- macOS LaunchAgent lifecycle commands
- macOS menu bar integration for daemon control
- Structured status and log output for troubleshooting

## Commands

```bash
devmon daemon --config <path>
devmon validate --config <path>
devmon service <install|uninstall|start|stop|status>
devmon menubar
```

## Configuration

`devmon` reads a TOML configuration file. The default path is `~/.config/devmon/devmon.toml`.

- `version = 1` is required.
- `[daemon]` controls global behavior.
- `[[folder]]` defines a working directory for jobs.
- `[[folder.job]]` defines each scheduled job.

Use the following example:

```toml
version = 1

[daemon]
max_concurrent_jobs = 2
startup_run = true
log_level = "info"

[[folder]]
id = "oss-repo"
path = "/Users/kdy1/projects/oss"

[[folder.job]]
id = "git-sync-main"
type = "shell-command"
enabled = true
interval = "1m"
timeout = "30s"
shell = "/bin/zsh"
startup_run = true
script = '''
set -eu

git fetch --all -p
git pull origin main

echo "done"
'''
```

## Scheduling Behavior

- If startup run is enabled (daemon-level or job-level), the job runs immediately on daemon start.
- If the same job is still running, the next trigger is skipped as `skipped-overlap`.
- If max global concurrency is reached, the trigger is skipped as `skipped-capacity`.
- Disabled jobs are skipped as `skipped-disabled`.
- v1 does not queue skipped runs for later replay.

## Job Contract

- `type` currently supports only `shell-command`.
- `interval` and `timeout` use Go duration strings such as `30s`, `1m`, and `10m`.
- `script` runs in the folder `path` directory.

## macOS Service and Menu Bar

- `devmon service install` installs LaunchAgents for daemon and menu bar.
- `devmon service status` returns a JSON summary of service and daemon health.
- `devmon menubar` starts the menu bar app process (macOS).

## Runtime Paths

- Config: `~/.config/devmon/devmon.toml`
- State: `~/.local/state/devmon/status.json`
- Daemon log: `~/Library/Logs/devmon/daemon.log`
- Daemon LaunchAgent plist: `~/Library/LaunchAgents/io.delino.devmon.daemon.plist`
- Menu bar LaunchAgent plist: `~/Library/LaunchAgents/io.delino.devmon.menubar.plist`

