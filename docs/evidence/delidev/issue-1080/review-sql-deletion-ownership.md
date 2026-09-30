# Reject restore during SQL-only backup deletion ownership

Addressed Codex thread `PRRT_kwDORRAKg86ngpTI` on PR #1222.
The finding is valid: backup deletion commits its job, receipt and
`backup_deletions` row before the external intent. A failed intent write can
leave an accepted obligation with no external file. Restore previously checked
only that file, so a fresh inspected live revision could still publish the image.

Under the existing exclusive backup/store gates, restore now checks the current
SQL obligation before creating staging or reserving the restore UUID, in addition
to the independent external intent. Any accepted deletion state retains ownership;
SQL or filesystem uncertainty cannot release it. The existing contract's rejection
of deletion-obligated images is unchanged.

The regression calls the real DeleteBackup path with its empty private intent
directory temporarily absent, producing Unavailable after durable SQL acceptance.
It recreates the private directory before restore so a parent-path error cannot
mask the missing-file window. Before the fix it failed because restore actually
published the deletion-owned image (`<nil>` error). An initial fixture attempt
tried to overwrite the already-created directory and failed during setup; it was
corrected before establishing this negative reproduction.

`GOMAXPROCS=2 go test -race ./cmds/delidev-cli/internal/store
-run 'SQLDeletion|BackupDeletion' -count=1 -timeout 15m` passed (37.231 s).
The repaired regression proves the live event revision/source bytes remain
unchanged, the failed restore creates no reservation, and exact deletion retry
persists the original intent under the same job/request. Existing backup deletion
replay, validation, uncertainty and external-obligation regressions also passed.
Final full-scope Go vet passed. No real account or native filesystem erasure was
used, and no fixture deadline changed.
