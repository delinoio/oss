# Runmoor Operations

> **Version note:** Runmoor 0.2.0 introduced automatic setup and managed runner updates. Version 0.1.3 uses the explicit pinned configuration and manual image preparation also documented below. Check `runmoor version` before using 0.2.0 commands.

```sh
runmoor service install
runmoor service start
runmoor service stop
runmoor service uninstall
```

These commands manage a launchd or systemd **user** service with the same drain semantics. Install the binary at a persistent location first. Installation does not overwrite an existing service definition. Uninstall preserves data/configuration and refuses to abandon known live executions when the manager cannot be contacted. Run the service in a functioning user session; availability after logout/reboot depends on that OS session, and Runmoor does not change system login policy.

Service start, stop, and uninstall must use the same `--config` path that was
recorded when the service was installed. If Runmoor reports
`CONFIG_INVALID` because the requested configuration does not match the
installed service, retry with the installed configuration path. To replace a
service configuration, drain and uninstall the service with its current path,
then install and start it with the new path. On systemd, inspect `ExecStart`
and `ExecStop` with `systemctl --user cat runmoor.service`; on macOS, inspect
the Runmoor launch agent's `ProgramArguments`. Preserve an invalid service
definition and resolve its problem before retrying service commands.
Paths containing `..` that resolve through a symlink to a different file are
also rejected; use the installed absolute configuration path directly.

If a Linux service action or a macOS service stop/uninstall reports that the
active manager does not match the installed service, gracefully stop it with
`runmoor stop --config ORIGINAL_CONFIG_PATH`, then retry the service action
with the installed configuration. This lets the active manager drain using
the configuration it was started with before systemd operates on the
replacement definition.

On macOS, stop and uninstall preserve the plist when launchd reports a loaded
job without a running process, because Runmoor cannot verify the arguments
cached by launchd in that state. Inspect and reconcile the loaded job in the
logged-in GUI session before retrying. If the manager is running, stop it with
its original configuration path first.

## Recover an Ubuntu user service

Run these checks as the Runmoor user in a working login session, without
`sudo`. `service install` does not replace an existing definition. If it
reports that a definition already exists or cannot be created securely,
inspect the existing unit before retrying. A previous install can leave the
unit file in place when systemd's reload fails after file creation.

```sh
systemctl --user status runmoor.service --no-pager -l
systemctl --user daemon-reload
systemctl --user cat runmoor.service
```

An `inactive (dead)` and `disabled` unit is present but not running. In the
unit shown by `cat`, check that `ExecStart` names the current Runmoor binary
and the intended `--config` file, especially after replacing the binary or
configuration. If `systemctl --user` cannot reach the user manager, log in as
the actual service user rather than running Runmoor through `sudo`.

When the unit paths are correct and the service is inactive, try
`runmoor service start` first. Runmoor checks that the unit is a regular,
owner-only file belonging to the current user and does not follow a symlink.
If that command reports exactly `DEPENDENCY_UNAVAILABLE: User service command
failed`, its unit check succeeded and the systemd command failed. Only then
run the same systemd command directly to see its original error:

```sh
systemctl --user enable --now runmoor.service
systemctl --user status runmoor.service --no-pager -l
journalctl --user -u runmoor.service -n 50 --no-pager
```

`runmoor service uninstall` asks Runmoor to drain and stop owned work, then
disables and stops the user unit. A failure during disable/stop leaves the unit
file in place, but a later systemd reload failure can be reported after Runmoor
has removed it. Both return `DEPENDENCY_UNAVAILABLE: User service command
failed`. Check whether the unit file still exists in the user's systemd
configuration directory and inspect the direct `systemctl --user` output and
journal before retrying. After `runmoor status` confirms no active executions
or pending cleanup, the corresponding disable/stop command is
`systemctl --user disable --now runmoor.service`. Do not manually remove the
unit, state, or managed data while executions or cleanup may still be active.
Use the normal drain/stop path before replacing a service definition.

## GitHub PAT for an Ubuntu user service

[Create a GitHub PAT](./configuration#create-a-github-pat) for the configured
repository or organization. Run the following commands as the Ubuntu user who
will run the service. They prompt without displaying the token or placing its
value in shell history:

```bash
install -d -m 700 "$HOME/.config/runmoor"
pat_file=$(mktemp "$HOME/.config/runmoor/github.pat.XXXXXX")
read -r -s -p 'GitHub PAT: ' pat
printf '\n'
printf '%s\n' "$pat" > "$pat_file"
unset pat
mv -f -- "$pat_file" "$HOME/.config/runmoor/github.pat"
realpath "$HOME/.config/runmoor/github.pat"
```

In `~/.config/runmoor/config.toml`, replace the connection's environment
reference with a file reference using the absolute path printed by `realpath`:

```toml
auth = "pat"
credential = { file = "REPLACE_WITH_ABSOLUTE_PAT_FILE" }
```

`mktemp` creates a mode 0600 file and `mv` replaces the credential file without
making its contents public during rotation. TOML does not expand `$HOME`. Keep
the PAT value out of TOML and the service definition. The credential file must
be a regular file owned by the service user with mode 0600. Then validate and
start the user service:

```sh
runmoor config validate
runmoor service install
runmoor service start
runmoor status
```

Run `service install` only for a new service definition. For an already running
pool, replacing the PAT in the same file does not automatically refresh its
GitHub connection. Revalidate the credential and clear any authentication
suspension after rotation:

```sh
runmoor resume --pool linux
runmoor status
```

If you change the credential reference in TOML, accept that configuration
before resuming the affected pool:

```sh
runmoor reload
runmoor resume --pool linux
```

A terminal's `export RUNMOOR_PAT` does not automatically reach systemd's user
manager. If you keep `credential = { env = "RUNMOOR_PAT" }`, import it before
starting the service:

```bash
read -r -s -p 'GitHub PAT: ' RUNMOOR_PAT
printf '\n'
export RUNMOOR_PAT
systemctl --user import-environment RUNMOOR_PAT
unset RUNMOOR_PAT
runmoor service start
```

If the service was already running without the PAT, `service start` leaves that
process running with its old environment. After importing the variable, use
`runmoor service stop`, `runmoor service start`, and `runmoor resume --pool linux`
to restart the manager and clear the pool's authentication suspension. Service
stop drains active work before exiting. Supply the variable again when the user
manager restarts. A file reference remains available across those restarts. The
user service starts after logout or reboot only if your Ubuntu user-session
policy keeps its systemd user manager running.

Manager-only restart reconciles SQLite with actual Docker/Tart and GitHub state, resumes verified live work and retries incomplete cleanup. Ambiguous resources are quarantined rather than deleted. Confirmed termination releases resources; unresolved cleanup/ownership records remain durable. Runmoor never automatically reruns a failed GitHub job.

For Tart, `OWNERSHIP_AMBIGUOUS` keeps the VM, its Runmoor records and its
capacity reservation intact, including after `stop --force`. Do not remove or
rename the VM to clear the warning. Follow [Tart ownership recovery](./tart#tart-ownership-recovery)
to restore a matching backup or reimport a known source under a new identity.

A recorded job completion continues through cleanup even if GitHub has already removed its ephemeral runner registration. Capacity becomes available once the owned execution is confirmed stopped, while any remaining cleanup is retried. An upgrade does not automatically recover existing quarantines. For a previously affected completed job, confirm completion in GitHub and verify the exact ownership and stopped state of its local resources before recovering the affected pool with `runmoor stop --pool NAME --force`.

Back up only after `drain` and `stop`. Preserve the complete state and managed-data directories; protect referenced credential files separately. Install the new binary manually and start again. Roll back using a compatible binary and its matching drained state/data backup. Version 0.2.0 upgrades existing state automatically; back up before upgrading and use the matching backup to return to 0.1.3. Unsupported database versions fail without destructive migration; never reuse an older backup while resources created after that backup are still active.

If startup reports `Possible legacy SQLite state exists at an ambiguous location`, stop every Runmoor manager that may use either location. Preserve complete backups of both locations before recovery. Do not move or delete either database; the original state location may be ambiguous and requires explicit review.

Jobs retain timeout accounting across restart/sleep. Active work requests OS sleep inhibition; warm idle capacity does not keep the machine awake indefinitely. Failure is a warning and does not change system power settings. Forced sleep, lid closure, shutdown and power loss can still interrupt work.

## Managed runner updates

While the manager runs, automatic pools check for a new stable runner release at
startup and every hour. Request an immediate check with:

```sh
runmoor runner update
runmoor runner update --pool linux
runmoor status
runmoor config show --resolved
```

Download and validation finish before the replacement activates. Existing jobs
finish with their original environment, and replacements using the same scale
set wait for the previous generation to drain. An update gets the next available
preparation slot without terminating busy jobs. Pause, drain and stop remain in
effect during updates.

If GitHub, the image registry or validation is temporarily unavailable, Runmoor
keeps the last verified environment and retries with backoff. Status reports the
current version, candidate, last check, next retry and failure or capacity wait.
A failed first installation waits because no fallback exists. If the known
GitHub update deadline passes, new work waits until a supported image is ready;
active jobs still finish. Unknown freshness is shown explicitly. GitHub can
require urgent security updates earlier than its usual 30-day window.

Current, previous and actively referenced generated images are retained.
Unreferenced generated artifacts are cleaned up without deleting user source
images or force-removing externally referenced Docker images. Official pulled
base layers remain available in Docker. Runmoor itself, Docker, Tart, Guest Agent,
macOS and Xcode still use their existing manual/package-manager update workflows.

## Troubleshooting and privacy

- `AUTHENTICATION_FAILED`: correct the referenced credential/permissions. Reload can resume the pool when a changed credential reference or runner group passes validation; if the secret changed at the same reference, use `resume`.
- `IMAGE_INVALID` or `RUNNER_VERSION_UNSUPPORTED`: for Docker, pre-pull a correct digest; for Tart, prepare a macOS guest whose metadata reports `darwin`, then seal it and update the pool. Linux, missing, or unrecognized Tart guest OS metadata is rejected. An older unsupported sealed image remains listed for diagnosis and can be removed after references are cleared. Managed pools retry runner preparation automatically; inspect status or request `runner update`. Exact pins require explicit replacement. GitHub generally requires replacement within 30 days and may require security updates sooner.
- `CAPACITY_EXHAUSTED` or `DISK_LOW`: adjust explicit budgets/free disk, or remove an unused sealed image yourself. Do not delete active execution storage.
- `OWNERSHIP_AMBIGUOUS`: preserve local state and investigate the exact resource. Use a different scale-set name when another installation owns it; restoring ownership requires the original matching backup.
- `CLEANUP_PENDING`: restore Docker/Tart/GitHub connectivity and let reconciliation retry. A stopped manager reports pending cleanup until the next run.
- `SLEEP_INHIBITION_UNAVAILABLE`: check OS utility/session permissions; work continues without a sleep guarantee.
- Repeated preparation failures suspend the affected pool after three attempts. Unrelated healthy pools continue.
- A suspended pool should show its reason in `runmoor status` and `runmoor doctor`. Older state may lack the original reason; Runmoor reports that gap without guessing or automatically resuming the pool. Run `doctor`, correct any reported dependency failure, then use `runmoor resume --pool NAME`.

Diagnostics are local, sanitized structured metadata bounded by **seven days and 256 MiB**. Completed execution history expires after seven days; unresolved ownership/cleanup remains until reconciliation. Credentials, JIT configuration, workflow secrets and raw job output are excluded. User-created sealed images remain until explicit deletion; generated runner revisions follow managed retention. There is no telemetry or Prometheus endpoint.

Report reproducible issues through [GitHub Issues](https://github.com/delinoio/oss/issues). Include version, platform, safe error code and relevant sanitized status. Do not include credentials, JIT data, raw workflow logs, or private VM contents. The [Runmoor documentation site](./) covers the same supported workflows.

See [installation and signature verification](./install) before any manual update or rollback.

## macOS host operations

The unreleased [host backend](./host) uses the same launchd and pool control
operations. Run the manager and trusted jobs under a dedicated CI account. PATH
and DEVELOPER_DIR are inherited when set; execution HOME and temporary paths are
disposable. Other manager variables and management credentials are excluded from
job launch configuration. Separate directories do not prevent account-wide file
or Keychain access. Operators own tools, shared caches, signing, Simulators, GUI
sessions, global tool settings and any daemon that escapes the managed group.

A manager-only restart reconciles verified surviving work. Completion,
cancellation and timeout terminate only the verified execution group and children.
Missing supervisors, changed process identities or directories and uncertain
termination retain capacity, state and resources. Preserve those resources and
inspect the safe status/doctor error; restore a matching paired state/data backup
when available. Never repair markers or delete by name. Drain/stop before upgrades
or rollback and use a compatible binary with its paired backup. Older binaries
must not open newer state. Raw workflow output and runner logs are not retained
in manager diagnostics.

Actual host execution, unsigned Xcode builds and live GitHub jobs have not been
validated. Host documentation does not expand Docker/Tart or package acceptance.
