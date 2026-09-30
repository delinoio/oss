# Restore migration copies retain their original ownership evidence

Addressed Codex thread `PRRT_kwDORRAKg86nkT-F` on PR #1222 after inspecting
head `04a9d22c2791ce8679502e618c27764061461188` on 2026-09-30.
The finding is valid: settled restore cleanup validated migration copies as
same-server SQLite images but did not compare them with the original copies.
A changed or replacement valid image could therefore be deleted as though it
were still an artifact owned by that restore.

Before synchronizing the prepared journal, restore now records a bounded,
deterministically ordered inventory containing each migration copy's UUID,
size, modification time and SHA-256 digest. Cleanup requires every remaining
copy to match its original journal claim before deleting the directory.
Changed, additional or unclaimed images stop startup with `RecoveryRequired`
and retain their bytes. Missing pinned copies permit interrupted authorized
cleanup to finish. The existing v1 private journal adds an optional field;
older journals remain readable, but cannot adopt retained migration images
without independent original fingerprints. Empty or already removed migration
directories still permit cleanup. The storage contract and scoped instructions
clarify this existing original-image ownership invariant.

The regression uses real temporary SQLite databases and an older schema that
creates a migration copy. It interrupts both prepared rollback and recorded
publication, then modifies the copy, replaces it with another valid same-server
database, adds an unexpected image, or removes fingerprints from both journals
to exercise legacy recovery. All eight cases failed against the original
implementation because startup deleted the evidence (store race run: 33.896 s;
log `/tmp/delidev-1222-migration-images-before.log`). The repaired cases verify
that two startup attempts preserve the exact retained bytes. A separate pair of
cases verifies original journal fingerprints and successful recovery after an
authorized unlink interrupted before directory synchronization.

Final verification for this repair:

- `GOMAXPROCS=2 go test -race -p 2 ./cmds/delidev-cli/internal/store
  ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/cli
  -run 'Restore|BackupRestore' -count=1 -timeout 15m` passed: storage 169.332 s,
  server 27.331 s and CLI 8.164 s. Log:
  `/tmp/delidev-1222-migration-images-race.log`.
- `GOMAXPROCS=2 go vet ./cmds/delidev-cli/...` passed. Log:
  `/tmp/delidev-1222-migration-images-vet.log`.
- `git diff --check` passed. Required DevHud administrator and asynchronous
  commit-hook embedded builds passed before committing. Their generated
  ignored outputs are temporary hook prerequisites.

An initial post-edit compile attempt referenced a nonexistent backup revision
field; that implementation error was corrected before the successful
final checks. Its log is `/tmp/delidev-1222-migration-images-after.log` and is
not passing validation evidence. No frontend, Rust or generated protocol source
changed in this repair. The independent Windows harness CI failure is handled
separately. Historical broad local race failures remain recorded in
`full-race-terminal-70b7de3c.md`; these focused passes do not claim a full local
race pass, installed native acceptance or new-head GitHub CI approval.
