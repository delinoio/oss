# Restored Worker route administration

The 2026-09-30 19:44 UTC maintenance pass inspected Codex thread
`PRRT_kwDORRAKg86nr2fw` against PR #1216 at
`f6aed111aa71c22a29bf234191c5d87f20a5ccbb`. The current safety image retained
Worker routes while machine entities still came from the historical backup.

The authenticated Connect regression exercises both a machine created after the
backup and a machine whose metadata changed after it. It restores a private real
SQLite backup, restarts the service, reads the original pinned Worker route,
checks current machine metadata and fresh revisions, clears the route through
NetworkService, and deletes its credential-bearing profile without pairing the
Worker again. A private original Worker verifier is valid before restore and
rejected afterward; retained metadata does not restore execution authority.

Both cases failed before the production fix: the newly created machine caused
an authoritative NotFound route read, and the existing machine retained stale
historical metadata. The initial test compilation errors (unused import and
incorrect Record field name) were corrected before that behavioral reproduction.

The candidate transformation now replaces only machine descriptors referenced by
current Worker routes with their current safety-image records. Its existing
transaction, revision freshening, Worker revocation and verifier/instance/grant
clearing remain in force. Historical source and current live images remain
unchanged during staging.

Validation:

- Before: `GOMAXPROCS=2 go test -race -p 2
  ./cmds/delidev-cli/internal/server -run
  '^TestNetworkRestoredWorkerRouteRemainsAdministrativelyClearable$' -count=1
  -timeout=4m` failed both cases, package duration 8.547 seconds.
- After: `GOMAXPROCS=2 go test -race -p 2
  ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/store -run
  'TestNetworkRestoredWorkerRouteRemainsAdministrativelyClearable|TestBackupRestoreRetainsCurrentNetworkAuthority|TestBackupRestoreRejectsPendingPrivateNetworkIntents|TestBackupRestore.*(Revocation|Pair|Device)'
  -count=1 -timeout=5m` passed (server 11.011 seconds, store 12.817 seconds).

The existing storage checks cover current network bodies, pending private intents,
current revocations and session deletion. This fixture result does not establish
native Worker bootstrap or another operating system's runtime acceptance.
