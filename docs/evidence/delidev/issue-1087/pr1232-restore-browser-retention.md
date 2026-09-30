# PR #1232 current browser state during managed restore

Codex thread `PRRT_kwDORRAKg86nv4tC` assumes a managed restore republishes the
historical Device documents. Inspection of `prepareRestoreImage` shows the
opposite: it deletes every historical Device/Pairing record and inserts complete
current Device records from the synchronized, immutable safety image outside the
database being replaced. This occurs under the restore's exclusive store gate.
The existing external journal pins the original safety/candidate digests before
publication and startup reconciles the result before serving. No production
restore behavior or separate profile journal was needed for this finding.

The new regression creates three active profiles before a managed backup, then
deletes their account. One profile is confirmed removed, one device remains
offline without observing deletion, and the third device is revoked. Restoring
the older active-profile image and restarting twice preserves each original
profile ID, revision, state and deletion request exactly. Cleanup remains zero
active, two pending and one removed. The revoked device remains unauthorized,
new registration for the deleted account is rejected, and the historical source
backup remains byte-for-byte unchanged.

Validation on 2026-10-01:

- `GOMAXPROCS=2 go test -race -p 1
  ./cmds/delidev-cli/internal/server -run
  'TestBrowserRemovalSurvivesRestore' -count=1` passed in 6.066 seconds.
- The initial draft also attempted a second replacement without reconciling the
  restored session. That attempt correctly failed the existing settled-ownership
  gate. The final regression performs one replacement plus subsequent restart;
  no eligibility rule was weakened.

This verifies the managed SQLite replacement and product reads, not native CEF
directory deletion or manual replacement outside the supported restore workflow.
The inline finding explanation is posted only after the final push succeeds.
