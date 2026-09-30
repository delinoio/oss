# Restore receipt reads retain the original actor

Addressed Codex thread `PRRT_kwDORRAKg86nh2r6` on PR #1222 after inspecting
head `38d7f0c9b72450dd25829c61729a7dafa87afdd6` on 2026-09-30.
The finding is valid: `GetBackupRestore` checked current device authorization
and server identity but did not compare the caller with the original actor in
the immutable external journal.

Receipt reads now require that exact original principal after current
authorization, returning `PermissionDenied` and an empty result for another
actor or a missing principal. An owner cannot read a paired client's receipt,
and another currently authorized paired client cannot read either actor's
receipt merely by knowing its request UUID. Current authorization still denies
a revoked originating client. Publication, exact mutation replay, wire schemas
and receipt bytes are unchanged. The storage contract and scoped instructions
clarify their existing actor-bound receipt requirement.

The new real temporary SQLite regression covers owner/client originators,
completed/rolled-back publication, two server-lock release/reopen cycles,
independently authorized other callers, a missing principal and revocation of
the original client. It also verifies unchanged journal bytes and no renewed
publication authority. Before the fix, all four actor/outcome cases returned
another actor's full receipt with no error (race command failed in 6.255 s).
After the fix, the isolated race regression passed in 6.061 s.

Final verification for this repair:

- `GOMAXPROCS=2 go test -race -p 2 ./cmds/delidev-cli/internal/store
  ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/cli
  -run 'Restore|BackupRestore' -count=1 -timeout 15m` passed: storage 57.271 s,
  server 10.098 s and CLI 5.073 s. The terminal log is
  `/tmp/delidev-1222-actor-restore-race.log`.
- `GOMAXPROCS=2 go vet ./cmds/delidev-cli/...` passed.
- `git diff --check` passed. No frontend, Rust or generated protocol code changed.

GitHub Actions CI passed for the inspected parent head; a new push requires new
CI and review evidence. Cloudflare Pages and security review were still pending
when this repair began. The earlier full local race failure remains recorded in
`full-race-terminal-70b7de3c.md`; no complete local race pass or installed native
acceptance is claimed by these focused checks.
