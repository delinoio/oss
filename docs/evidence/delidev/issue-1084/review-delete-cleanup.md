# Network profile deletion cleanup review evidence

Issue #1084 / PR #1216, reviewed head `b0d4092b1575f9e8b543f1add4494ccfd9062637`, repaired after the independent receipt fix `42a8e3aa6`.

## Finding and change

[Codex deletion cleanup finding](https://github.com/delinoio/oss/pull/1216#discussion_r4145815375) identified public profile removal before recoverable native cleanup. A canceled request or failed vault operation could retain generations after the original paired client was revoked.

Deletion now synchronizes one authenticated private server-bound intent before the SQL mutation. It retains the original actor/request/revision without credential bytes. Current authorized owner/client Save, Select and Delete operations reconcile its exact original receipt under the shared vault gate. An accepted deletion plus an authoritative absent public profile permits native cleanup. An absent receipt clears metadata only; unknown proof, tampering or cleanup failure prevents replacement mutations. Cleanup is driven by the current bounded RPC, survives server restart, and needs no impersonation of the original actor. A fresh authorized deletion of the pending profile/revision can publish its own receipt after cleanup, while reuse of the old actor's request ID remains rejected.

## Validation

- `GOMAXPROCS=2 go test -race -p 2 ./cmds/delidev-cli/internal/server -run Network -count=1 -timeout=5m` passed, 12.218 seconds.
- The same command after adding the unavailable-journal regression passed, 12.097 seconds.
- New fixtures cover native enumeration failure, native deletion failure, cancellation after SQL deletion, durable recovery after restart and revocation, rejected revoked-client/changed-actor retries without native work, fresh owner recovery and receipt replay, SQL rollback preserving the live credential, bounded replacement denial, journal tampering, and unavailable private intent preventing public deletion.
- `git diff --check` passed.

These tests use temporary private SQLite state, injected credential storage and loopback authenticated Connect only. They do not establish real OS credential-store, real account, enterprise proxy or Worker bootstrap acceptance. Combined validation is recorded independently after the other review repairs.
