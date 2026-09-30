# ALLGREEN queue review repairs

Date: 2026-09-30. PR: https://github.com/delinoio/oss/pull/1168.
The repair starts from `c74aacb68d86462f8b24fa064e7f6c25e396e32d` in a
replacement isolated worktree because the previously recorded checkout was
absent. Original validation and its limits remain in `validation.md`.

## Queue entry after the final rules read

Review thread: `PRRT_kwDORRAKg86nbIcA`.

The complete CI inventory is now rechecked after the final rules read for every
PR, including one that was initially unqueued. An entry into the queue rejects
the earlier PR-head/test-merge observation before publishing failure evidence.
The contract and scoped domain instructions now state this boundary explicitly.

- Before the fix, `go test -race ./cmds/delidev-cli/internal/integrations/github -run '^TestCIQueryRejectsMergeQueueEntryAfterFinalRulesRead$' -count=1`
  failed: only two inventory reads occurred and stale head failure evidence was
  returned. Log: `/private/tmp/issue-1105-membership-before.log`.
- After the fix, `go test -race ./cmds/delidev-cli/internal/integrations/github -count=1`
  passed the complete GitHub adapter package in 2.507s, including the new
  transition regression and existing queue pagination/provenance cases.
  Log: `/private/tmp/issue-1105-membership-after.log`.

These are controlled fixture results, not live queued-account acceptance.

## Replacement queue identity with an unchanged native result

Review thread: `PRRT_kwDORRAKg86nbIcF`.

CI storage identity now includes the evaluated source and original queue/entry
nodes alongside the check node and unchanged native-result content version. A
replacement queue or entry creates its own current unhandled problem and proof.
The original remains historical with its dismissal and proof unchanged. Legacy
plain-node indexes stay readable and deduplicate only within their original
source/queue identity; existing non-queue proofs remain usable. No schema or wire
migration is introduced.

- Before the fix, `go test -race ./cmds/delidev-cli/internal/store -run '^TestQueueCIFailureSeparatesReplacementIdentityWithReusedNativeResult$' -count=1`
  failed all four replacement entry/queue and legacy-index cases: history retained
  only the old problem. Log: `/private/tmp/issue-1105-queue-identity-before.log`.
- After the fix, `go test -race ./cmds/delidev-cli/internal/store -run 'TestQueueCI|TestPRCIRetains|TestPRConflictHistory|TestPRProblemV18Migration' -count=1`
  passed in 9.338s. Cases cover repeated identical entries, removal, replacement
  entry/queue with the same commit/check result, original proof/dismissal,
  restart, legacy queue and non-queue indexes, and unchanged migration fixtures.
  Log: `/private/tmp/issue-1105-queue-identity-after.log`.
- `go vet ./cmds/delidev-cli/...`, `pnpm proto:lint` and `pnpm proto:fresh` passed.
  Protobuf generation reproduced the committed sources without drift.
