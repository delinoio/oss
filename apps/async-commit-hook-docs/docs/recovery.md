# Operations and recovery

## Daemon and interruption

```
ach daemon status
ach daemon stop
ach daemon stop --force
ach doctor
ach rerun --run <interrupted-id> --failed
```

Normal stop drains active work; force cancels owned checks. No OS-login service is installed. Unstarted requests recover on the next worker/daemon startup. Interrupted commands are recorded and never automatically replayed. Cancellation confirms that owned descendants have exited before releasing a replacement, including children that detach from their parent. If ownership cannot be verified, cancellation stays incomplete. Preserve local state, restore access and retry recovery. If a Linux ownership supervisor was forcibly killed, reboot the host before retrying. Do not kill unrelated processes based only on an old PID.

## Port, storage and source failures

Port conflicts fail instead of choosing a new port. Stop the conflicting program, or stop ach and change api_port. For disk-full/permission errors, restore free space and account access before retrying; missing evidence is not a pass. Original repository removal leaves history available but source/diff unavailable. Restore the original repository or use retained results for inspection.

## Retention and backups

```
ach prune --dry-run --max-age-days 30
ach prune --max-age-days 30
ach prune --dry-run --max-bytes 1073741824
```

Retention is indefinite by default. Configured cleanup can remove unacknowledged completed results but protects pending/active work. Pruned attempts retain authority so older success cannot resurface. Back up the complete state directory while checks, daemon, viewer and MCP processes are stopped. Keep SQLite and evidence together; preserve account-only permissions. A direct update creates a consistent state backup automatically.

## Upgrade and rollback

```
ach daemon stop
# Stop viewer/MCP processes and finish or cancel checks, then:
ach self-update --version 0.1.0
ach self-update --recover
# Homebrew installations:
brew upgrade async-commit-hook
```

Without `--version`, self-update selects the highest published stable version and refuses a downgrade. Intentional rollback requires an explicit version. Updates refuse active checks/server processes, verify signatures/checksums, back up state, and retain the previous executable. If the verified release executable is already byte-for-byte identical, self-update reports `up-to-date` without creating another backup or replacing it. A different local build is still replaced even when its version number matches. A pending-update diagnostic requires `--recover` before restarting services. If replacement and automatic rollback both fail, restore the recorded binary backup while ach is stopped. For rollback across state versions, restore the matching complete state backup and binary together. Unsupported state/API versions are rejected, never destructively converted. Direct updater does not replace Homebrew-owned files.

For cleanup failures, inspect doctor and the affected run before retrying prune. Product-owned hook/agent entries can be removed with their uninstall commands; edited or unrelated files are preserved. `ach hooks uninstall` removes both installed hooks, including an opted-in pre-push hook; no additional flag is required. For support, include version, OS, stable diagnostic codes and a redacted description in [GitHub Issues](https://github.com/delinoio/oss/issues). Review any material before sharing; no diagnostics are uploaded automatically.
