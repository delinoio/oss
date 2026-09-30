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
