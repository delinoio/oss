# Runmoor Operations

> **Version note:** Automatic setup and managed runner updates described here target the next Runmoor release. Published 0.1.3 uses the explicit pinned configuration and manual image preparation also documented below. Check `runmoor init --help` for the new options.

```sh
runmoor service install
runmoor service start
runmoor service stop
runmoor service uninstall
```

These commands manage a launchd or systemd **user** service with the same drain semantics. Install the binary at a persistent location first. Installation does not overwrite an existing service definition. Uninstall preserves data/configuration and refuses to abandon known live executions when the manager cannot be contacted. Run the service in a functioning user session; availability after logout/reboot depends on that OS session, and Runmoor does not change system login policy.

Manager-only restart reconciles SQLite with actual Docker/Tart and GitHub state, resumes verified live work and retries incomplete cleanup. Ambiguous resources are quarantined rather than deleted. Confirmed termination releases resources; unresolved cleanup/ownership records remain durable. Runmoor never automatically reruns a failed GitHub job.

A recorded job completion continues through cleanup even if GitHub has already removed its ephemeral runner registration. Capacity becomes available once the owned execution is confirmed stopped, while any remaining cleanup is retried. An upgrade does not automatically recover existing quarantines. For a previously affected completed job, confirm completion in GitHub and verify the exact ownership and stopped state of its local resources before recovering the affected pool with `runmoor stop --pool NAME --force`.

Back up only after `drain` and `stop`. Preserve the complete state and managed-data directories; protect referenced credential files separately. Install the new binary manually and start again. Roll back using a compatible binary and its matching drained state/data backup. The new release upgrades existing state automatically; back up before upgrading and use the matching backup to return to 0.1.3. Unsupported database versions fail without destructive migration; never reuse an older backup while resources created after that backup are still active.

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
- `IMAGE_INVALID` or `RUNNER_VERSION_UNSUPPORTED`: pre-pull a correct digest or seal a compatible image. Managed pools retry runner preparation automatically; inspect status or request `runner update`. Exact pins require explicit replacement. GitHub generally requires replacement within 30 days and may require security updates sooner.
- `CAPACITY_EXHAUSTED` or `DISK_LOW`: adjust explicit budgets/free disk, or remove an unused sealed image yourself. Do not delete active execution storage.
- `OWNERSHIP_AMBIGUOUS`: preserve local state and investigate the exact resource. Use a different scale-set name when another installation owns it; restoring ownership requires the original matching backup.
- `CLEANUP_PENDING`: restore Docker/Tart/GitHub connectivity and let reconciliation retry. A stopped manager reports pending cleanup until the next run.
- `SLEEP_INHIBITION_UNAVAILABLE`: check OS utility/session permissions; work continues without a sleep guarantee.
- Repeated preparation failures suspend the affected pool after three attempts. Unrelated healthy pools continue.
- A suspended pool should show its reason in `runmoor status` and `runmoor doctor`. Older state may lack the original reason; Runmoor reports that gap without guessing or automatically resuming the pool. Run `doctor`, correct any reported dependency failure, then use `runmoor resume --pool NAME`.

Diagnostics are local, sanitized structured metadata bounded by **seven days and 256 MiB**. Completed execution history expires after seven days; unresolved ownership/cleanup remains until reconciliation. Credentials, JIT configuration, workflow secrets and raw job output are excluded. User-created sealed images remain until explicit deletion; generated runner revisions follow managed retention. There is no telemetry or Prometheus endpoint.

Report reproducible issues through [GitHub Issues](https://github.com/delinoio/oss/issues). Include version, platform, safe error code and relevant sanitized status. Do not include credentials, JIT data, raw workflow logs, or private VM contents. The [Runmoor documentation site](./) covers the same supported workflows.

See [installation and signature verification](./install) before any manual update or rollback.
