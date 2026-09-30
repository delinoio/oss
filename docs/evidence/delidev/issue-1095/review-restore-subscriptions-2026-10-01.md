# Issue #1095: subscription ownership across managed restore

Incoming main `6c749670727b30679e722821846bc8dc00f5ac32` adds database restore,
whose existing eligibility scan did not know managed subscription ownership.
Restore now rejects pending lifecycle operations, active account leases and
recovery-required subscription state under its exclusive store gate. It never
replaces the database to clear that ownership.

The external vault is unchanged by database restore. Restored subscription
references therefore retain their original generation and ownership metadata
under a recovery-required fence, with no account connection. The domain permits
that disconnected historical generation only while quarantined; usable
generations continue to require subscription authentication. Storage/domain
instructions, both owning contracts and the project cross-domain invariant
describe this boundary. Historical selected images and earlier evidence remain
unchanged. This does not implement uncertain-lease recovery.

```sh
GOMAXPROCS=4 go test -race -p 1 -timeout 5m \
  ./cmds/delidev-cli/internal/store ./cmds/delidev-cli/internal/domain \
  -run 'BackupRestore.*Subscription|Subscription' -count 1
```

Passed on macOS arm64: storage 39.160s; domain compiled with no matching tests.
The new storage tests prove queued, claimed and recovery ownership refuse
replacement without changing account bytes/revision or ending the live epoch;
a settled account permits replacement. Reopening an older image preserves its
original valid quarantined generation without adopting the current generation;
removing its fence makes the disconnected generation invalid. The initial fixture
run failed because its synthetic connection omitted ConnectedAt; the corrected
fixture above passed. No real credentials or user state are used.
