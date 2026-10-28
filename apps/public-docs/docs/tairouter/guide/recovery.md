# Upgrade, rollback, backup, and restore

## Availability and prerequisites

The supplied `maintenance.py` tool implements backup, restore, upgrade, and key rotation for the single-host Compose deployment. Run the commands below from the directory containing that tool, keeping the complete supplied deployment bundle in its original layout. They are not evidence of a production restore. Do not use a generic full-database dump as a substitute: ordinary backups must exclude temporary continuation and device-handoff data.

Before production use, test the matching tool and deployment version on an isolated instance. Stop every separately managed server using the same database; the tool can stop only its Compose service. Stop evaluation sidecars before maintenance. Maintenance downtime is permitted. Keep the previous image version, matching database backup, and required encryption-key versions available together.

## Maintenance commands

Use an existing protected backup directory and a new absolute archive filename. `/secure/backups/before-upgrade.dump` is a placeholder; replace it with your own path. The backup refuses to overwrite an existing destination, creates a mode-600 archive, and excludes temporary storage and encryption keys.

```sh
python3 maintenance.py backup "/secure/backups/before-upgrade.dump"
```

The following are separate maintenance actions. Select the matching application image and configuration before running them:

```sh
python3 maintenance.py upgrade
python3 maintenance.py restore "/secure/backups/before-upgrade.dump"
python3 maintenance.py rotate
```

Upgrade, restore, and rotation leave the service stopped, including on failure. Restore removes old temporary state, restores durable data transactionally, migrates, and invalidates restored authority. Do not restart after a failed step. Only after successful maintenance and verification, return to the Compose directory and explicitly start the service:

```sh
docker compose up -d tairouter
```

A backup does not stop service admission by itself. Arrange the required consistency and maintenance window before running it. Backup scheduling, storage, retention, and separate key copies remain operator responsibilities.

## Back up

1. Choose and record the backup schedule and retention policy. The operator owns both.
2. Arrange a consistent backup using the release's supported maintenance procedure.
3. Back up durable database data while excluding temporary continuation and device-handoff storage. Temporary raw device keys must never enter ordinary backups.
4. Back up encryption keys separately with restricted access. Retain older key versions required to decrypt older backups.
5. Record the application version and backup time without secrets or request content.
6. Test restoration in isolation and verify the invalidation requirements below before accepting the backup as recoverable.

Temporary conversations may be lost during disaster recovery. Generated images and hosted videos are temporary artifacts, not a permanent response archive. Existing backups expire under the operator's policy; deleting live data does not rewrite old backup copies.

## Upgrade

1. Read the target release's compatibility and migration requirements.
2. Schedule downtime and prevent new traffic.
3. Take a recoverable database backup and preserve the matching previous image and encryption keys.
4. Apply the release's database migration procedure and start the matching new image.
5. Check readiness, login, authorization, and representative client/provider behavior before reopening traffic.
6. If the upgrade fails, keep traffic closed and follow rollback.

Do not assume that starting an old image can reverse a database migration.

## Roll back

Restore both the previous image and its matching database backup. A database restore triggers the complete post-restore invalidation sequence, even when rollback follows a failed upgrade. Changes made after the backup can be lost. Notify users to reconnect and recreate affected configuration where necessary.

## Restore and invalidate before startup

1. Keep all service instances stopped and public traffic blocked.
2. Restore the selected durable database backup and configure its required encryption keys.
3. Do not restore temporary continuation or device-handoff state.
4. Invalidate **all API keys, web sessions, invitations, and device authorizations before starting any server**. The established maintenance command is `tairouter-server restore-invalidate`, run offline with the restored database configuration. The `restore` wrapper above invokes invalidation before returning successfully and never restarts the service itself.
5. Verify invalidation completed successfully. If it fails or cannot be verified, keep the service stopped.
6. Start the matching application image, verify readiness and recovery behavior, then reopen traffic.
7. Require users to log in again, issue new keys, and reconnect CLIs. Send fresh invitations and start fresh device approvals. Old continuation state cannot be recovered; explicitly start new conversations.

A restore test must show that old keys, cookies, invitation links, and device approvals fail. A successful database import or health response alone does not prove this.

## Rotate encryption keys

During maintenance, retain the previous key for decryption and configure the new active version. The server interface names `tairouter-server reencrypt` for re-encryption. Verify completion before removing the previous key from active configuration. Preserve separate copies of every old key needed for retained backups. Do not rotate or delete keys on the assumption that a documentation build verified recovery.
