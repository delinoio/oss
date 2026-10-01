# Managed restore and current network authority

Issue #1084 / PR #1216, after main merge `2a7fffcf7` incorporating managed restore `9efb1917e0127a9223cee0969877238ab37c0e1d`.

## Composition issue and change

Main's new managed restore quarantines historical SQL receipts and restores configuration from an older image. Network routes embed immutable proxy configuration and credential-generation references, so an old route could reactivate routing authority independently of a later profile deletion. Replacing SQL while a private network publication/deletion intent remains would also make its original receipt comparison uncertain.

Restore now rejects any object at the private network publication/deletion intent paths before closing the database. The owning server holds the network/account credential gate through eligibility and publication, excluding the native-write-before-SQL window. Candidate transformation takes all current network profile and route bodies from the current safety image. It preserves exact generations and selections without restoring native credentials or activating historical routes. Ordinary restore revision/timestamp freshening remains intact, so pre-restore mutations still cannot target replacement state. Native credential storage is untouched.

## Validation and corrected fixture assumptions

- The first composition fixture omitted closing the old Store before reopening and correctly received `conflict: Another process owns this data scope`. The fixture now closes the original Store; product locking was unchanged.
- The next fixture incorrectly expected unchanged public revisions/timestamps. Inspection confirmed the existing restore contract intentionally freshens every replacement resource. The corrected assertion checks exact preserved bodies plus revision increment and the restore receipt's millisecond timestamp; the product revision rule was unchanged.
- `GOMAXPROCS=2 go test -race -p 2 ./cmds/delidev-cli/internal/store ./cmds/delidev-cli/internal/server -run 'BackupRestore.*Network|Network|RestoreRPC|RestoreRefusesConcurrentUserServiceControl' -count=1 -timeout=5m` passed: store 12.419s, server 27.408s.
- `GOMAXPROCS=2 go test -race -p 2 ./cmds/delidev-cli/internal/store -run 'BackupRestore.*Network' -count=1 -timeout=2m` passed after exact timestamp coverage: 12.377s.
- New tests compare current route/profile bodies against an older image, include a profile created after that image and an independently deleted former profile, verify fresh revisions/timestamps, and prove pending publication/deletion paths preserve the original live epoch and private evidence.
- `git diff --check` passed.

These are isolated SQLite/Connect composition fixtures. They do not prove OS credential restoration, real account/proxy acceptance, Worker application or platform release acceptance. Full combined validation follows this independent repair commit.
