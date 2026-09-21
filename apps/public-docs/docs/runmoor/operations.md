# Runmoor Operations

```sh
runmoor service install
runmoor service start
runmoor service stop
runmoor service uninstall
```

These commands manage a launchd or systemd **user** service with the same drain semantics. Install the binary at a persistent location first. Installation does not overwrite an existing service definition. Uninstall preserves data/configuration and refuses to abandon known live executions when the manager cannot be contacted. Run the service in a functioning user session; availability after logout/reboot depends on that OS session, and Runmoor does not change system login policy.

Manager-only restart reconciles SQLite with actual Docker/Tart and GitHub state, resumes verified live work and retries incomplete cleanup. Ambiguous resources are quarantined rather than deleted. Confirmed termination releases resources; unresolved cleanup/ownership records remain durable. Runmoor never automatically reruns a failed GitHub job.

A recorded job completion continues through cleanup even if GitHub has already removed its ephemeral runner registration. Capacity becomes available once the owned execution is confirmed stopped, while any remaining cleanup is retried. An upgrade does not automatically recover existing quarantines. For a previously affected completed job, confirm completion in GitHub and verify the exact ownership and stopped state of its local resources before recovering the affected pool with `runmoor stop --pool NAME --force`.

Back up only after `drain` and `stop`. Preserve the complete state and managed-data directories; protect referenced credential files separately. Install the new binary manually and start again. Roll back using a compatible binary and its matching drained state/data backup. Unsupported database versions fail without destructive migration; never reuse an older backup while resources created after that backup are still active.

Jobs retain timeout accounting across restart/sleep. Active work requests OS sleep inhibition; warm idle capacity does not keep the machine awake indefinitely. Failure is a warning and does not change system power settings. Forced sleep, lid closure, shutdown and power loss can still interrupt work.

## Troubleshooting and privacy

- `AUTHENTICATION_FAILED`: correct the referenced credential/permissions, then resume the pool.
- `IMAGE_INVALID` or `RUNNER_VERSION_UNSUPPORTED`: pre-pull a correct digest or seal a compatible image. Runner updates are explicit; GitHub generally requires replacement within 30 days of a new runner release and may require security updates sooner.
- `CAPACITY_EXHAUSTED` or `DISK_LOW`: adjust explicit budgets/free disk, or remove an unused sealed image yourself. Do not delete active execution storage.
- `OWNERSHIP_AMBIGUOUS`: preserve local state and investigate the exact resource. Use a different scale-set name when another installation owns it; restoring ownership requires the original matching backup.
- `CLEANUP_PENDING`: restore Docker/Tart/GitHub connectivity and let reconciliation retry. A stopped manager reports pending cleanup until the next run.
- `SLEEP_INHIBITION_UNAVAILABLE`: check OS utility/session permissions; work continues without a sleep guarantee.
- Repeated preparation failures suspend the affected pool after three attempts. Unrelated healthy pools continue.

Diagnostics are local, sanitized structured metadata bounded by **seven days and 256 MiB**. Completed execution history expires after seven days; unresolved ownership/cleanup remains until reconciliation. Credentials, JIT configuration, workflow secrets and raw job output are excluded. Sealed images remain until explicit deletion. There is no telemetry or Prometheus endpoint.

Report reproducible issues through [GitHub Issues](https://github.com/delinoio/oss/issues). Include version, platform, safe error code and relevant sanitized status. Do not include credentials, JIT data, raw workflow logs, or private VM contents. The [Runmoor documentation site](https://oss.delino.io/runmoor) covers the same supported workflows.

See [installation and signature verification](/runmoor/install) before any manual update or rollback.
